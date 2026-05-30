package stream

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"sync"
)

type preset struct {
	width, height, fps, bitrate, audioBitrate int
}

var presets = map[string]preset{
	"low":    {480, 270, 15, 500, 64},
	"medium": {640, 360, 30, 900, 96},
	"high":   {0, 0, 30, 1500, 128},
	// Ultra trades bandwidth for minimum decoder reordering and the fastest
	// possible frame availability on clients that struggle with buffering.
	"ultra": {480, 270, 15, 1200, 64},
}

type FFmpegConfig struct {
	Quality    string
	Audio      bool
	ALSADevice string
	Resolution image.Point
}

func BuildArgs(cfg FFmpegConfig) []string {
	p, ok := presets[cfg.Quality]
	if !ok {
		p = presets["medium"]
	}

	var args []string

	// Aggressive low-latency tuning for live screen mirroring.
	args = append(args,
		"-fflags", "nobuffer",
		"-flags", "low_delay",
		"-max_delay", "0",
	)

	// Input video: frame buffer
	args = append(args, "-f", "fbdev", "-framerate", fmt.Sprintf("%d", p.fps), "-i", "/dev/fb0")

	// Input audio: ALSA or silence
	useRealAudio := cfg.Audio && cfg.ALSADevice != ""
	if useRealAudio {
		args = append(args, "-f", "alsa", "-i", cfg.ALSADevice)
	} else {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
	}

	// H.264 with ultra-low-latency tuning tends to start faster and is much
	// easier for mobile clients to buffer efficiently than MPEG-2.
	gop := max(1, p.fps/2) // half-second keyframe interval
	if cfg.Quality == "ultra" {
		gop = 1 // all-I frames: maximize decoder immediacy at the cost of bitrate.
	}
	args = append(args,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-level", "3.1",
		"-pix_fmt", "yuv420p",
		"-bf", "0",
		"-g", fmt.Sprintf("%d", gop),
		"-keyint_min", fmt.Sprintf("%d", gop),
		"-sc_threshold", "0",
		"-x264-params", fmt.Sprintf("keyint=%d:min-keyint=%d:scenecut=0:rc-lookahead=0:repeat-headers=1:aud=1", gop, gop),
		"-b:v", fmt.Sprintf("%dk", p.bitrate),
		"-maxrate", fmt.Sprintf("%dk", p.bitrate),
		"-bufsize", fmt.Sprintf("%dk", p.bitrate*2),
	)

	// Video filters
	if p.width > 0 && p.height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", p.width, p.height))
	}

	// AAC is widely compatible and buffers efficiently in mobile players.
	args = append(args,
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", p.audioBitrate),
		"-ar", "48000",
		"-ac", "2",
	)

	// Output format: MPEG-TS over pipe
	args = append(args,
		"-f", "mpegts",
		"-fflags", "nobuffer",
		"-flush_packets", "1",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"-max_interleave_delta", "0",
		"-mpegts_flags", "resend_headers+initial_discontinuity",
		"pipe:1",
	)

	return args
}

// NewFFmpegEncoder returns the current software encoder backend.
//
// This is intentionally a small, transport-agnostic constructor so the same
// interface can later be backed by Cedar or another hardware path without
// rewriting the server layer.
func NewFFmpegEncoder(cfg FFmpegConfig) Encoder {
	return NewExecCmdEncoder("ffmpeg", func() (*exec.Cmd, error) {
		return exec.Command("ffmpeg", append([]string{"-y"}, BuildArgs(cfg)...)...), nil
	})
}

// NewEncoder is the transport-level factory for the stream backend.
//
// It currently returns the FFmpeg software path, but the selection point is
// intentionally centralized here so a Cedar backend can be added later
// without changing the controller or UI plumbing.
func NewEncoder(cfg FFmpegConfig) (Encoder, error) {
	return NewFFmpegEncoder(cfg), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type Process struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (p *Process) Start(args []string, writer io.Writer) error {
	p.Stop()

	cmd := exec.Command("ffmpeg", append([]string{"-y"}, args...)...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()

	go func() {
		buf := make([]byte, 32*1024)
		_, _ = io.CopyBuffer(writer, stdout, buf)
		p.Stop()
	}()

	return nil
}

func (p *Process) Stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

func (p *Process) Wait() error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil {
		return nil
	}
	return cmd.Wait()
}
