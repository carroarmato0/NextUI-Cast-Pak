//go:build linux && arm64

package stream

/*
#cgo LDFLAGS: -ldl
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	unsigned int   width;
	unsigned int   height;
	unsigned int   fps;
	unsigned int   gop;
	unsigned int   bitrate_kbps;
	uintptr_t      writer_handle;
	int            bpp;
	unsigned int   input_buffers;
	unsigned int   max_frames;
	int            synthetic;
} cedar_cfg_t;

extern int cedar_run(cedar_cfg_t *cfg, volatile int *stop_flag);
extern int cedar_probe(void);
*/
import "C"
import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime/cgo"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

type cedarEncoder struct {
	preset       CedarPreset
	rawOutput    bool
	rtpOutput    bool
	rtpTarget    string
	synthetic    bool
	inputBuffers int
	maxFrames    int
	stopFlag     *int32 // separately allocated so unsafe.Pointer(e.stopFlag) passes CGO pointer rules
	doneOnce     sync.Once
	doneErr      error
	done         chan struct{}

	mu     sync.Mutex
	muxCmd *exec.Cmd
	muxPW  *os.File
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// NewCedarEncoder probes for Cedar support and returns an encoder if available.
// Performs a lightweight probe (open /dev/cedar_dev + dlopen libvencoder.so)
// without initializing the encoder hardware.
func NewCedarEncoder(cfg FFmpegConfig) (Encoder, error) {
	if C.cedar_probe() != 0 {
		return nil, ErrNotSupported
	}

	bpp, err := readFBBpp()
	if err != nil {
		return nil, fmt.Errorf("cedar: %w", err)
	}
	if bpp != 16 && bpp != 32 {
		return nil, fmt.Errorf("cedar: unsupported framebuffer bpp %d", bpp)
	}

	p, err := CedarPresetFor(cfg.Quality, cfg.Resolution)
	if err != nil {
		return nil, err
	}
	p.bpp = bpp

	inputBuffers := cfg.CedarInputBuffers
	if inputBuffers < 1 {
		inputBuffers = 1
	}
	maxFrames := cfg.CedarMaxFrames
	if maxFrames < 0 {
		return nil, fmt.Errorf("cedar: invalid CedarMaxFrames %d", maxFrames)
	}

	sf := new(int32)
	return &cedarEncoder{preset: p, rawOutput: cfg.CedarRaw, rtpOutput: cfg.CedarRTP, synthetic: cfg.CedarSynthetic, inputBuffers: inputBuffers, maxFrames: maxFrames, stopFlag: sf, rtpTarget: "239.255.0.1"}, nil
}

func CedarAvailable() bool           { return C.cedar_probe() == 0 }
func (e *cedarEncoder) Name() string { return "cedar" }
func (e *cedarEncoder) ContentType() string {
	if e != nil && e.rtpOutput {
		return "application/sdp"
	}
	if e != nil && e.rawOutput {
		return "video/h264"
	}
	return "video/mp2t"
}

// Start launches the Cedar encode loop and a lightweight ffmpeg mux subprocess
// that wraps the raw H.264 Annex B stream into MPEG-TS.
//
// os.Pipe() is used instead of io.Pipe() so that the kernel pipe read end can
// be passed as an *os.File directly to cmd.Stdin — no copy goroutine needed.
// Cedar writes Annex B frames to the pipe write end via cedar_write_go.
func (e *cedarEncoder) Start(w io.Writer) error {
	atomic.StoreInt32(e.stopFlag, 0)
	e.done = make(chan struct{})
	e.doneOnce = sync.Once{}
	e.doneErr = nil

	if e.rawOutput {
		h := cgo.NewHandle(io.Writer(w))
		go func() {
			defer h.Delete()

			var err error

			// Write SPS/PPS before the first encoded frame so decoders can initialise.
			if _, err = w.Write(e.preset.SPSPPS); err != nil {
				e.doneOnce.Do(func() { e.doneErr = err; close(e.done) })
				return
			}

			cfg := C.cedar_cfg_t{
				width:         C.uint(e.preset.Width),
				height:        C.uint(e.preset.Height),
				fps:           C.uint(e.preset.FPS),
				gop:           C.uint(e.preset.GOP),
				bitrate_kbps:  C.uint(e.preset.BitrateKbps),
				writer_handle: C.uintptr_t(h),
				bpp:           C.int(e.preset.bpp),
				input_buffers: C.uint(e.inputBuffers),
				max_frames:    C.uint(e.maxFrames),
				synthetic:     C.int(boolToInt(e.synthetic)),
			}

			// e.stopFlag is a separately allocated *int32 (no other Go pointers in
			// that allocation), so unsafe.Pointer(e.stopFlag) satisfies CGO rules.
			rc := C.cedar_run(&cfg, (*C.int)(unsafe.Pointer(e.stopFlag)))
			if rc != 0 {
				err = fmt.Errorf("cedar: encode loop exited with rc=%d", rc)
			}

			e.doneOnce.Do(func() { e.doneErr = err; close(e.done) })
		}()

		return nil
	}

	if e.rtpOutput {
		pr, pw, err := os.Pipe()
		if err != nil {
			return fmt.Errorf("cedar: os.Pipe: %w", err)
		}

		rtpHost := e.rtpTarget
		if rtpHost == "" {
			rtpHost = "239.255.0.1"
		}
		rtpTarget := fmt.Sprintf("rtp://%s:5004?ttl=1&pkt_size=1200", rtpHost)
		sdp := buildRTPSDP(e.preset.SPSPPS, rtpHost)
		fmt.Fprintf(os.Stderr, "[cedar_encoder] RTP branch start target=%s sdp-bytes=%d\n", rtpTarget, len(sdp))
		if _, err := w.Write([]byte(sdp)); err != nil {
			_ = pr.Close()
			_ = pw.Close()
			return err
		}
		if fl, ok := w.(interface{ Flush() }); ok {
			fl.Flush()
		}

		cmd := exec.Command("ffmpeg",
			"-y",
			"-r", strconv.Itoa(e.preset.FPS),
			"-f", "h264",
			"-i", "pipe:0",
			"-map", "0:v",
			"-c:v", "copy",
			"-an",
			"-f", "rtp",
			"-pkt_size", "1200",
			rtpTarget,
		)
		cmd.Stdin = pr
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			_ = pr.Close()
			_ = pw.Close()
			return fmt.Errorf("cedar: rtp ffmpeg: %w", err)
		}
		fmt.Fprintf(os.Stderr, "[cedar_encoder] ffmpeg RTP started pid=%d target=%s\n", cmd.Process.Pid, rtpTarget)
		_ = pr.Close()

		e.mu.Lock()
		e.muxCmd = cmd
		e.muxPW = pw
		e.mu.Unlock()

		h := cgo.NewHandle(io.Writer(pw))
		go func() {
			defer h.Delete()
			defer pw.Close()

			var err error
			cfg := C.cedar_cfg_t{
				width:         C.uint(e.preset.Width),
				height:        C.uint(e.preset.Height),
				fps:           C.uint(e.preset.FPS),
				gop:           C.uint(e.preset.GOP),
				bitrate_kbps:  C.uint(e.preset.BitrateKbps),
				writer_handle: C.uintptr_t(h),
				bpp:           C.int(e.preset.bpp),
				input_buffers: C.uint(e.inputBuffers),
				max_frames:    C.uint(e.maxFrames),
				synthetic:     C.int(boolToInt(e.synthetic)),
			}

			rc := C.cedar_run(&cfg, (*C.int)(unsafe.Pointer(e.stopFlag)))
			fmt.Fprintf(os.Stderr, "[cedar_encoder] cedar_run RTP returned rc=%d err=%v\n", rc, err)
			if rc != 0 {
				err = fmt.Errorf("cedar: encode loop exited with rc=%d", rc)
			}

			if muxErr := cmd.Wait(); muxErr != nil && err == nil {
				if atomic.LoadInt32(e.stopFlag) == 0 {
					err = fmt.Errorf("cedar: mux: %w", muxErr)
				}
			}

			e.doneOnce.Do(func() { e.doneErr = err; close(e.done) })
		}()

		return nil
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("cedar: os.Pipe: %w", err)
	}

	cmd := exec.Command("ffmpeg",
		"-y",
		"-r", strconv.Itoa(e.preset.FPS),
		"-f", "h264",
		"-i", "pipe:0",
		"-map", "0:v",
		"-c:v", "copy",
		"-f", "mpegts",
		"-flush_packets", "1",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-mpegts_flags", "resend_headers+initial_discontinuity",
		"pipe:1",
	)
	cmd.Stdin = pr // *os.File → fd passed directly, no copy goroutine
	cmd.Stdout = w
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return fmt.Errorf("cedar: mux ffmpeg: %w", err)
	}
	_ = pr.Close() // parent no longer needs the read end

	e.mu.Lock()
	e.muxCmd = cmd
	e.muxPW = pw
	e.mu.Unlock()

	h := cgo.NewHandle(io.Writer(pw))

	go func() {
		defer h.Delete()
		defer pw.Close()

		var err error

		// Write SPS/PPS before the first encoded frame so decoders can initialise.
		if _, err = pw.Write(e.preset.SPSPPS); err != nil {
			_ = cmd.Wait()
			e.doneOnce.Do(func() { e.doneErr = err; close(e.done) })
			return
		}

		cfg := C.cedar_cfg_t{
			width:         C.uint(e.preset.Width),
			height:        C.uint(e.preset.Height),
			fps:           C.uint(e.preset.FPS),
			gop:           C.uint(e.preset.GOP),
			bitrate_kbps:  C.uint(e.preset.BitrateKbps),
			writer_handle: C.uintptr_t(h),
			bpp:           C.int(e.preset.bpp),
			input_buffers: C.uint(e.inputBuffers),
			max_frames:    C.uint(e.maxFrames),
			synthetic:     C.int(boolToInt(e.synthetic)),
		}

		// e.stopFlag is a separately allocated *int32 (no other Go pointers in
		// that allocation), so unsafe.Pointer(e.stopFlag) satisfies CGO rules.
		rc := C.cedar_run(&cfg, (*C.int)(unsafe.Pointer(e.stopFlag)))
		if rc != 0 {
			err = fmt.Errorf("cedar: encode loop exited with rc=%d", rc)
		}

		// pw.Close() via defer signals EOF to ffmpeg.
		if muxErr := cmd.Wait(); muxErr != nil && err == nil {
			// ffmpeg exits non-zero when killed by Stop(); ignore that case.
			if atomic.LoadInt32(e.stopFlag) == 0 {
				err = fmt.Errorf("cedar: mux: %w", muxErr)
			}
		}

		e.doneOnce.Do(func() { e.doneErr = err; close(e.done) })
	}()

	return nil
}

func (e *cedarEncoder) Stop() {
	atomic.StoreInt32(e.stopFlag, 1)

	e.mu.Lock()
	cmd := e.muxCmd
	pw := e.muxPW
	e.muxCmd = nil
	e.muxPW = nil
	e.mu.Unlock()

	if pw != nil {
		_ = pw.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (e *cedarEncoder) Wait() error {
	if e.done == nil {
		return nil
	}
	<-e.done
	return e.doneErr
}

func buildRTPSDP(spspps []byte, rtpHost string) string {
	const sc = "\x00\x00\x00\x01"
	if len(spspps) == 0 {
		return ""
	}
	first := bytes.Index(spspps, []byte(sc))
	if first < 0 {
		return ""
	}
	spspps = spspps[first+4:]
	second := bytes.Index(spspps, []byte(sc))
	if second < 0 {
		return ""
	}
	sps := spspps[:second]
	pps := spspps[second+4:]
	if len(sps) < 4 || len(pps) == 0 {
		return ""
	}
	profileLevel := fmt.Sprintf("%02X%02X%02X", sps[1], sps[2], sps[3])
	if rtpHost == "" {
		rtpHost = "239.255.0.1"
	}
	return fmt.Sprintf(
		"v=0\r\n"+
			"o=- 0 0 IN IP4 127.0.0.1\r\n"+
			"s=TrimUI Cast\r\n"+
			"c=IN IP4 %s\r\n"+
			"t=0 0\r\n"+
			"a=tool:TrimUI Cast\r\n"+
			"m=video 5004 RTP/AVP 96\r\n"+
			"a=rtpmap:96 H264/90000\r\n"+
			"a=fmtp:96 packetization-mode=1; sprop-parameter-sets=%s,%s; profile-level-id=%s\r\n",
		rtpHost,
		base64.StdEncoding.EncodeToString(sps),
		base64.StdEncoding.EncodeToString(pps),
		profileLevel,
	)
}

func (e *cedarEncoder) SetRTPTarget(target string) {
	if e == nil || !e.rtpOutput {
		return
	}
	if target == "" {
		return
	}
	e.rtpTarget = target
}

func (e *cedarEncoder) SDP() string {
	if e == nil || !e.rtpOutput {
		return ""
	}
	return buildRTPSDP(e.preset.SPSPPS, e.rtpTarget)
}

// cedar_write_go is the CGO export called from C to write encoded bytes to the Go io.Writer.
//
//export cedar_write_go
func cedar_write_go(handle C.uintptr_t, data unsafe.Pointer, n C.int) C.int {
	w := cgo.Handle(handle).Value().(io.Writer)
	b := (*[1 << 30]byte)(data)[:int(n):int(n)]
	written, err := w.Write(b)
	if err != nil {
		return -1
	}
	return C.int(written)
}

// readFBBpp reads /sys/class/graphics/fb0/bits_per_pixel.
func readFBBpp() (int, error) {
	data, err := os.ReadFile("/sys/class/graphics/fb0/bits_per_pixel")
	if err != nil {
		return 0, fmt.Errorf("read bits_per_pixel: %w", err)
	}
	bpp, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse bits_per_pixel: %w", err)
	}
	return bpp, nil
}
