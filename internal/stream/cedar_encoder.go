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
	volatile int  *stop_flag;
	int            bpp;
} cedar_cfg_t;

extern int cedar_run(cedar_cfg_t *cfg);
extern int cedar_probe(void);
*/
import "C"
import (
	"fmt"
	"io"
	"os"
	"runtime/cgo"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

type cedarEncoder struct {
	preset   CedarPreset
	stopFlag int32
	doneOnce sync.Once
	doneErr  error
	done     chan struct{}
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
	return &cedarEncoder{preset: p}, nil
}

func (e *cedarEncoder) Name() string        { return "cedar" }
func (e *cedarEncoder) ContentType() string { return "video/h264" }

func (e *cedarEncoder) Start(w io.Writer) error {
	atomic.StoreInt32(&e.stopFlag, 0)
	e.done = make(chan struct{})
	e.doneOnce = sync.Once{}
	e.doneErr = nil

	h := cgo.NewHandle(w)

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
			stop_flag:     (*C.int)(unsafe.Pointer(&e.stopFlag)),
			bpp:           C.int(e.preset.bpp),
		}

		rc := C.cedar_run(&cfg)
		if rc != 0 {
			err = fmt.Errorf("cedar: encode loop exited with rc=%d", rc)
		}
		e.doneOnce.Do(func() { e.doneErr = err; close(e.done) })
	}()

	return nil
}

func (e *cedarEncoder) Stop() {
	atomic.StoreInt32(&e.stopFlag, 1)
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
