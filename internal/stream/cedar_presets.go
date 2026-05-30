// internal/stream/cedar_presets.go
package stream

import (
	"errors"
	"fmt"
	"image"
)

// ErrNotSupported is returned by NewCedarEncoder when Cedar hardware is
// unavailable — either a non-arm64 build or a device without /dev/cedar_dev
// or the required vendor libraries.
var ErrNotSupported = errors.New("cedar: not supported on this platform")

// CedarPreset holds Cedar-specific encoder parameters for one quality level.
type CedarPreset struct {
	Width, Height int
	FPS, GOP      int
	BitrateKbps   int
	SPSPPS        []byte
	bpp           int // filled in by NewCedarEncoder from /sys/class/graphics/fb0/bits_per_pixel
}

// Align16 rounds n up to the next multiple of 16 (Cedar macroblock size).
func Align16(n int) int {
	return (n + 15) &^ 15
}

// cedarSPSPPS is a lookup table keyed by (width, height).
// All entries are Annex B, Baseline Profile, verified on H618 hardware.
// 640x368 is derived; 480x272 and 1280x720 are hardware-verified.
var cedarSPSPPS = map[[2]int][]byte{
	{480, 272}: {
		// Level 3.0, verified on TrimUI Brick (tg5040)
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1e,
		0xed, 0x03, 0xc1, 0x1c, 0x80,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	},
	{640, 368}: {
		// Level 3.0, derived from Exp-Golomb encoding of 640x368
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1e,
		0xed, 0x01, 0x48, 0x63, 0x20,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	},
	{1280, 720}: {
		// Level 3.1, verified on TrimUI Smart Pro (tg5050) via ADB
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1f,
		0xed, 0x00, 0xa0, 0x0b, 0x72,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	},
}

// cedarFixedPresets maps quality → (logical width, logical height) for
// non-native presets. Height is the logical value; Cedar uses Align16(height).
var cedarFixedPresets = map[string]struct{ w, h, fps, gop, kbps int }{
	"low":    {480, 270, 15, 8, 500},
	"medium": {640, 360, 30, 15, 900},
	"ultra":  {480, 270, 15, 1, 1200},
}

// CedarPresetFor resolves Cedar encoder parameters for the given quality and
// native framebuffer resolution. Returns an error if the resolution has
// no SPS/PPS entry (only relevant for the "high" preset) or if quality is unknown.
func CedarPresetFor(quality string, native image.Point) (CedarPreset, error) {
	if quality == "high" {
		w := Align16(native.X)
		h := Align16(native.Y)
		spspps, ok := cedarSPSPPS[[2]int{w, h}]
		if !ok {
			return CedarPreset{}, errors.New("cedar: no SPS/PPS entry for native resolution")
		}
		return CedarPreset{
			Width:       w,
			Height:      h,
			FPS:         30,
			GOP:         15,
			BitrateKbps: 1500,
			SPSPPS:      spspps,
		}, nil
	}

	fp, ok := cedarFixedPresets[quality]
	if !ok {
		return CedarPreset{}, fmt.Errorf("cedar: unknown quality preset %q", quality)
	}
	h := Align16(fp.h)
	spspps, ok := cedarSPSPPS[[2]int{fp.w, h}]
	if !ok {
		panic("cedar: SPS/PPS table and fixed presets are out of sync")
	}
	return CedarPreset{
		Width:       fp.w,
		Height:      h,
		FPS:         fp.fps,
		GOP:         fp.gop,
		BitrateKbps: fp.kbps,
		SPSPPS:      spspps,
	}, nil
}
