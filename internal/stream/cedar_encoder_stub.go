//go:build !linux || !arm64

package stream

// NewCedarEncoder always returns ErrNotSupported on non-arm64 platforms.
func NewCedarEncoder(_ FFmpegConfig) (Encoder, error) {
	return nil, ErrNotSupported
}
