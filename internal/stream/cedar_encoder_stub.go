//go:build !linux || !arm64

package stream

import "errors"

// ErrNotSupported is returned by NewCedarEncoder on non-device builds.
var ErrNotSupported = errors.New("cedar: not supported on this platform")

// NewCedarEncoder always returns ErrNotSupported on non-arm64 platforms.
func NewCedarEncoder(_ FFmpegConfig) (Encoder, error) {
	return nil, ErrNotSupported
}
