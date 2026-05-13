package stream

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// hlsSegmentSeconds is the HLS segment duration passed to -hls_time.
// The GOP (keyframe interval) must equal fps × hlsSegmentSeconds so that
// each segment starts on a keyframe. Change both together.
const hlsSegmentSeconds = 1

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

	// Audio input: use the real ALSA capture when available; otherwise feed silence
	// via anullsrc. Chromecast's default receiver may reject video-only HLS streams,
	// so we always include an audio track even when the user has disabled audio.
	useRealAudio := cfg.Audio && cfg.ALSADevice != ""
	if useRealAudio {
		args = append(args, "-f", "alsa", "-i", cfg.ALSADevice)
	} else {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=44100")
	}

	// Video encoding.
	// -g <fps×hlsSegmentSeconds>: force a keyframe every hlsSegmentSeconds to match the HLS segment target.
	// Without a keyframe at each segment boundary the HLS muxer can only cut at
	// the next available keyframe, producing longer segments than requested and
	// adding unnecessary latency.
	// yuv420p: the fbdev BGRA source defaults to High 4:4:4 Predictive profile
	//   (yuv444p) which is CPU-intensive and not universally supported by
	//   Chromecasts. Force the standard 4:2:0 chroma subsampling instead.
	gop := p.fps * hlsSegmentSeconds
	args = append(args,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-crf", fmt.Sprintf("%d", p.crf),
		"-g", fmt.Sprintf("%d", gop),
	)

	// Scale + pixel-format filter
	if p.width > 0 && p.height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d,format=yuv420p", p.width, p.height))
	} else {
		args = append(args, "-vf", "format=yuv420p")
	}

	// Audio encoding
	if useRealAudio {
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", p.audioBitrate))
	} else {
		args = append(args, "-c:a", "aac", "-b:a", "32k")
	}

	// HLS output.
	// hls_time=hlsSegmentSeconds: segment duration must match GOP interval.
	// 1-second segments produce valid TARGETDURATION:1 and halve end-to-end latency
	// vs. the previous 2s setting. The original 0.5s setting caused ffmpeg to write
	// TARGETDURATION:0 (invalid per RFC 8216).
	manifest := filepath.Join(cfg.HLSDir, "stream.m3u8")
	args = append(args,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentSeconds),
		"-hls_list_size", "6",
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
