package stream

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

type preset struct {
	width, height, fps, crf, audioBitrate int
}

var presets = map[string]preset{
	"low":    {640, 480, 10, 35, 64},
	"medium": {640, 480, 15, 28, 128}, // scale down to reduce ARM encoding cost
	"high":   {0, 0, 15, 23, 192},     // native resolution
}

type FFmpegConfig struct {
	Quality    string
	Audio      bool
	ALSADevice string
	Resolution image.Point
	HLSDir     string
}

func BuildArgs(cfg FFmpegConfig) []string {
	p, ok := presets[cfg.Quality]
	if !ok {
		p = presets["medium"]
	}

	var args []string

	// Video input
	args = append(args, "-f", "fbdev", "-framerate", fmt.Sprintf("%d", p.fps), "-i", "/dev/fb0")

	// Audio input
	if cfg.Audio && cfg.ALSADevice != "" {
		args = append(args, "-f", "alsa", "-i", cfg.ALSADevice)
	}

	// Video encoding.
	// -g <fps>: force a keyframe at every second so HLS segments actually close
	//   at the -hls_time boundary. Without this libx264 uses keyint=250 (16s
	//   at 15fps) and the first segment doesn't appear for ~10 seconds.
	// yuv420p: the fbdev BGRA source defaults to High 4:4:4 Predictive profile
	//   (yuv444p) which is CPU-intensive and not universally supported by
	//   Chromecasts. Force the standard 4:2:0 chroma subsampling instead.
	args = append(args,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-crf", fmt.Sprintf("%d", p.crf),
		"-g", fmt.Sprintf("%d", p.fps),
	)

	// Scale + pixel-format filter
	if p.width > 0 && p.height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d,format=yuv420p", p.width, p.height))
	} else {
		args = append(args, "-vf", "format=yuv420p")
	}

	// Audio encoding
	if cfg.Audio && cfg.ALSADevice != "" {
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", p.audioBitrate))
	}

	// HLS output
	manifest := filepath.Join(cfg.HLSDir, "stream.m3u8")
	args = append(args,
		"-f", "hls",
		"-hls_time", "1",
		"-hls_list_size", "10",
		"-hls_flags", "delete_segments+temp_file",
		manifest,
	)

	return args
}

type Process struct {
	mu  sync.Mutex
	cmd *exec.Cmd
}

func (p *Process) Start(hlsDir string, args []string) error {
	// Stop any previously running process before starting a new one.
	p.Stop()
	if err := os.MkdirAll(hlsDir, 0755); err != nil {
		return fmt.Errorf("create HLS dir: %w", err)
	}
	cmd := exec.Command("ffmpeg", append([]string{"-y"}, args...)...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()
	return nil
}

func (p *Process) Stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.cmd = nil // nil out so Wait() sees no process and returns nil
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait() // reap; ignore error (process may have already exited)
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
