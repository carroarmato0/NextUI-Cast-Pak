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
	"medium": {0, 0, 15, 28, 128},
	"high":   {0, 0, 20, 23, 192},
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

	// Video encoding
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-crf", fmt.Sprintf("%d", p.crf))

	// Scale filter (only if preset has explicit dimensions)
	if p.width > 0 && p.height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", p.width, p.height))
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
		"-hls_list_size", "3",
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
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = os.MkdirAll(hlsDir, 0755)
	p.cmd = exec.Command("ffmpeg", append([]string{"-y"}, args...)...)
	p.cmd.Stderr = os.Stderr
	return p.cmd.Start()
}

func (p *Process) Stop() {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
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
