// internal/stream/ffmpeg_test.go
package stream_test

import (
	"image"
	"strings"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

func TestBuildArgs_LowPreset(t *testing.T) {
	cfg := stream.FFmpegConfig{
		Quality:    "low",
		Audio:      false,
		Resolution: image.Point{X: 1280, Y: 720},
	}
	args := stream.BuildArgs(cfg)
	joined := strings.Join(args, " ")

	assertContains := func(sub string) {
		t.Helper()
		if !strings.Contains(joined, sub) {
			t.Errorf("args missing %q in: %s", sub, joined)
		}
	}
	assertAbsent := func(sub string) {
		t.Helper()
		if strings.Contains(joined, sub) {
			t.Errorf("args should not contain %q in: %s", sub, joined)
		}
	}

	assertContains("-framerate 15")
	assertContains("-fflags nobuffer")
	assertContains("-c:v libx264")
	assertContains("-preset ultrafast")
	assertContains("-tune zerolatency")
	assertContains("-profile:v baseline")
	assertContains("scale=480:270")
	assertAbsent("-f alsa")
	assertContains("anullsrc")
	assertContains("-f mpegts")
	assertContains("-flush_packets 1")
	assertContains("-mpegts_flags resend_headers+initial_discontinuity")
	assertContains("pipe:1")
}

func TestBuildArgs_MediumWithAudio(t *testing.T) {
	cfg := stream.FFmpegConfig{
		Quality:    "medium",
		Audio:      true,
		ALSADevice: "hw:0,0",
		Resolution: image.Point{X: 1280, Y: 720},
	}
	args := stream.BuildArgs(cfg)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-f alsa") {
		t.Error("medium+audio should include -f alsa")
	}
	if !strings.Contains(joined, "hw:0,0") {
		t.Error("should include ALSA device")
	}
	if !strings.Contains(joined, "-framerate 30") {
		t.Error("medium should be 30 fps")
	}
	if !strings.Contains(joined, "scale=640:360") {
		t.Error("medium should scale to 640x360")
	}
	if !strings.Contains(joined, "yuv420p") {
		t.Error("medium should force yuv420p pixel format")
	}
	if !strings.Contains(joined, "-c:v libx264") {
		t.Error("should use libx264 video codec")
	}
	if !strings.Contains(joined, "-c:a aac") {
		t.Error("should use aac audio codec")
	}
	if !strings.Contains(joined, "-f mpegts") {
		t.Error("should output mpegts")
	}
}

func TestBuildArgs_HighPreset(t *testing.T) {
	cfg := stream.FFmpegConfig{
		Quality:    "high",
		Audio:      true,
		ALSADevice: "default",
		Resolution: image.Point{X: 1280, Y: 720},
	}
	args := stream.BuildArgs(cfg)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-framerate 30") {
		t.Error("high should be 30 fps")
	}
	if !strings.Contains(joined, "-preset ultrafast") {
		t.Error("high should use ultrafast preset")
	}
	if !strings.Contains(joined, "128k") {
		t.Error("high audio should be 128k")
	}
	if strings.Contains(joined, "scale=") {
		t.Error("high preset should not scale (native resolution)")
	}
	if !strings.Contains(joined, "yuv420p") {
		t.Error("high should force yuv420p pixel format")
	}
}

func TestBuildArgs_UltraPreset(t *testing.T) {
	cfg := stream.FFmpegConfig{
		Quality:    "ultra",
		Audio:      true,
		ALSADevice: "default",
		Resolution: image.Point{X: 1280, Y: 720},
	}
	args := stream.BuildArgs(cfg)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-framerate 15") {
		t.Error("ultra should be 15 fps")
	}
	if !strings.Contains(joined, "scale=480:270") {
		t.Error("ultra should scale to 480x270")
	}
	if !strings.Contains(joined, "-g 1") {
		t.Error("ultra should use all-I frames with GOP 1")
	}
	if !strings.Contains(joined, "keyint=1:min-keyint=1:scenecut=0") {
		t.Error("ultra should force x264 all-I settings")
	}
	if !strings.Contains(joined, "-b:v 1200k") {
		t.Error("ultra should use 1200k video bitrate")
	}
	if !strings.Contains(joined, "-c:a aac") {
		t.Error("ultra should still use aac audio")
	}
}

func TestBuildArgs_KeyframeInterval(t *testing.T) {
	cases := []struct {
		quality string
		wantGOP string
	}{
		{"low", "-g 7"},    // 15 fps × 0.5 s ≈ 7 frames
		{"medium", "-g 15"}, // 30 fps × 0.5 s = 15 frames
		{"high", "-g 15"},   // 30 fps × 0.5 s = 15 frames
		{"ultra", "-g 1"},   // all-I frames for minimal latency
	}
	for _, tc := range cases {
		cfg := stream.FFmpegConfig{Quality: tc.quality}
		args := stream.BuildArgs(cfg)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, tc.wantGOP) {
			t.Errorf("%s preset: want %q in args: %s", tc.quality, tc.wantGOP, joined)
		}
	}
}
