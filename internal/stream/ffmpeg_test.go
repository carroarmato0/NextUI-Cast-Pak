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
		HLSDir:     "/tmp/cast/hls",
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

	assertContains("-framerate 10")
	assertContains("crf 35")
	assertContains("scale=640:480")
	assertAbsent("-f alsa")
	assertContains("stream.m3u8")
}

func TestBuildArgs_MediumWithAudio(t *testing.T) {
	cfg := stream.FFmpegConfig{
		Quality:    "medium",
		Audio:      true,
		ALSADevice: "hw:0,0",
		Resolution: image.Point{X: 1280, Y: 720},
		HLSDir:     "/tmp/cast/hls",
	}
	args := stream.BuildArgs(cfg)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-f alsa") {
		t.Error("medium+audio should include -f alsa")
	}
	if !strings.Contains(joined, "hw:0,0") {
		t.Error("should include ALSA device")
	}
	if !strings.Contains(joined, "-framerate 15") {
		t.Error("medium should be 15 fps")
	}
	if strings.Contains(joined, "scale=") {
		t.Error("medium at native res should not scale")
	}
}

func TestBuildArgs_HighPreset(t *testing.T) {
	cfg := stream.FFmpegConfig{
		Quality:    "high",
		Audio:      true,
		ALSADevice: "default",
		Resolution: image.Point{X: 1280, Y: 720},
		HLSDir:     "/tmp/cast/hls",
	}
	args := stream.BuildArgs(cfg)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-framerate 20") {
		t.Error("high should be 20 fps")
	}
	if !strings.Contains(joined, "crf 23") {
		t.Error("high CRF should be 23")
	}
	if !strings.Contains(joined, "192k") {
		t.Error("high audio should be 192k")
	}
}
