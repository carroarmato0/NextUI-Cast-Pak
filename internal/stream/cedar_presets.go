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
var cedarSPSPPS = map[[2]int][]byte{
	{480, 272}: {
		// Baseline L3.0, poc_type=2, log2_max_frame_num_minus4=4, verified on H618
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1e,
		0x95, 0xa0, 0x78, 0x23, 0x90,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	},
	{640, 368}: {
		// Baseline L3.0, poc_type=2, log2_max_frame_num_minus4=4, verified on H618
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1e,
		0x95, 0xa0, 0x28, 0x0b, 0xe4,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	},
	{1280, 720}: {
		// Baseline L3.1, poc_type=0, log2_max_frame_num_minus4=4, log2_max_poc_lsb_minus4=4
		// Derived by bit-parsing actual IDR output from H618 Cedar encoder (tg5040).
		// The encoder uses poc_type=0 (not poc_type=2); without matching poc_lsb field
		// in the SPS, decoders misparse the slice header and reject every frame.
		0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x40, 0x1f,
		0x96, 0x54, 0x02, 0x80, 0x2d, 0xc8,
		0x00, 0x00, 0x00, 0x01, 0x68, 0xce, 0x3c, 0x80,
	},
}

// cedarQualityParams maps quality name → (fps, gop, bitrate_kbps).
// Cedar always encodes at native framebuffer resolution; quality only
// controls temporal/bitrate parameters.
var cedarQualityParams = map[string]struct{ fps, gop, kbps int }{
	"low":    {15, 8, 500},
	"medium": {30, 15, 900},
	"high":   {30, 15, 1500},
	"ultra":  {15, 1, 1200},
}

// CedarPresetFor resolves Cedar encoder parameters for the given quality and
// native framebuffer resolution. Cedar always encodes at native resolution;
// quality governs FPS, GOP, and bitrate only.
func CedarPresetFor(quality string, native image.Point) (CedarPreset, error) {
	w := Align16(native.X)
	h := Align16(native.Y)
	spspps, ok := cedarSPSPPS[[2]int{w, h}]
	if !ok {
		return CedarPreset{}, fmt.Errorf("cedar: no SPS/PPS entry for %dx%d", w, h)
	}

	qp, ok := cedarQualityParams[quality]
	if !ok {
		return CedarPreset{}, fmt.Errorf("cedar: unknown quality preset %q", quality)
	}

	return CedarPreset{
		Width:       w,
		Height:      h,
		FPS:         qp.fps,
		GOP:         qp.gop,
		BitrateKbps: qp.kbps,
		SPSPPS:      spspps,
	}, nil
}
