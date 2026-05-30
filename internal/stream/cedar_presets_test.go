// internal/stream/cedar_presets_test.go
package stream_test

import (
	"image"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

func TestAlign16(t *testing.T) {
	cases := []struct{ in, want int }{
		{270, 272}, {272, 272}, {360, 368}, {368, 368}, {720, 720}, {480, 480},
	}
	for _, tc := range cases {
		if got := stream.Align16(tc.in); got != tc.want {
			t.Errorf("Align16(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestCedarPresetFor_Low(t *testing.T) {
	p, err := stream.CedarPresetFor("low", image.Point{X: 1280, Y: 720})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Width != 480 || p.Height != 272 {
		t.Errorf("low: want 480x272, got %dx%d", p.Width, p.Height)
	}
	if p.FPS != 15 {
		t.Errorf("low: want 15 fps, got %d", p.FPS)
	}
	if p.GOP != 8 {
		t.Errorf("low: want GOP 8, got %d", p.GOP)
	}
	if p.BitrateKbps != 500 {
		t.Errorf("low: want 500kbps, got %d", p.BitrateKbps)
	}
	if len(p.SPSPPS) == 0 {
		t.Error("low: SPS/PPS must not be empty")
	}
}

func TestCedarPresetFor_Medium(t *testing.T) {
	p, err := stream.CedarPresetFor("medium", image.Point{X: 1280, Y: 720})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Width != 640 || p.Height != 368 {
		t.Errorf("medium: want 640x368, got %dx%d", p.Width, p.Height)
	}
	if p.FPS != 30 {
		t.Errorf("medium: want 30 fps, got %d", p.FPS)
	}
	if p.GOP != 15 {
		t.Errorf("medium: want GOP 15, got %d", p.GOP)
	}
}

func TestCedarPresetFor_High_KnownResolution(t *testing.T) {
	p, err := stream.CedarPresetFor("high", image.Point{X: 1280, Y: 720})
	if err != nil {
		t.Fatalf("high 1280x720: unexpected error: %v", err)
	}
	if p.Width != 1280 || p.Height != 720 {
		t.Errorf("high 1280x720: want 1280x720, got %dx%d", p.Width, p.Height)
	}
	if len(p.SPSPPS) == 0 {
		t.Error("high 1280x720: SPS/PPS must not be empty")
	}
}

func TestCedarPresetFor_High_UnknownResolution(t *testing.T) {
	_, err := stream.CedarPresetFor("high", image.Point{X: 854, Y: 480})
	if err == nil {
		t.Error("high 854x480: expected error for unsupported resolution")
	}
}

func TestCedarPresetFor_Ultra(t *testing.T) {
	p, err := stream.CedarPresetFor("ultra", image.Point{X: 1280, Y: 720})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Width != 480 || p.Height != 272 {
		t.Errorf("ultra: want 480x272, got %dx%d", p.Width, p.Height)
	}
	if p.GOP != 1 {
		t.Errorf("ultra: want GOP 1, got %d", p.GOP)
	}
	if p.BitrateKbps != 1200 {
		t.Errorf("ultra: want 1200kbps, got %d", p.BitrateKbps)
	}
}

func TestCedarPresetFor_UnknownQuality(t *testing.T) {
	_, err := stream.CedarPresetFor("bogus", image.Point{X: 1280, Y: 720})
	if err == nil {
		t.Error("unknown quality: expected error")
	}
}

func TestSPSPPS_480x272(t *testing.T) {
	p, _ := stream.CedarPresetFor("low", image.Point{X: 1280, Y: 720})
	want := []byte{
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1e,
		0xed, 0x03, 0xc1, 0x1c, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	}
	if len(p.SPSPPS) != len(want) {
		t.Fatalf("480x272 SPS/PPS: want %d bytes, got %d", len(want), len(p.SPSPPS))
	}
	for i, b := range want {
		if p.SPSPPS[i] != b {
			t.Errorf("480x272 SPS/PPS byte[%d]: want 0x%02x, got 0x%02x", i, b, p.SPSPPS[i])
		}
	}
}
