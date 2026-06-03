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
} cedar_cfg_t;

extern int cedar_run(cedar_cfg_t *cfg, volatile int *stop_flag);
extern int cedar_probe(void);
*/
import "C"
import (
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
	preset   CedarPreset
	stopFlag *int32 // separately allocated so unsafe.Pointer(e.stopFlag) passes CGO pointer rules
	doneOnce sync.Once
	doneErr  error
	done     chan struct{}

	mu     sync.Mutex
	muxCmd *exec.Cmd
	muxPW  *os.File
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

	sf := new(int32)
	return &cedarEncoder{preset: p, stopFlag: sf}, nil
}

func CedarAvailable() bool                  { return C.cedar_probe() == 0 }
func (e *cedarEncoder) Name() string        { return "cedar" }
func (e *cedarEncoder) ContentType() string { return "video/mp2t" }

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

	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("cedar: os.Pipe: %w", err)
	}

	cmd := exec.Command("ffmpeg",
		"-y",
		"-fflags", "nobuffer",
		"-f", "h264",
		"-framerate", strconv.Itoa(e.preset.FPS),
		"-i", "pipe:0",
		"-map", "0:v",
		"-c:v", "copy",
		"-f", "mpegts",
		"-fflags", "nobuffer",
		"-flush_packets", "1",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-mpegts_flags", "resend_headers+initial_discontinuity",
		"pipe:1",
	)
	cmd.Stdin = pr  // *os.File → fd passed directly, no copy goroutine
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
