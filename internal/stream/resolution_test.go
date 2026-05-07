// internal/stream/resolution_test.go
package stream_test

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

func TestReadNative_ParsesFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "modes")
	os.WriteFile(p, []byte("U:1280x720p-60\n"), 0644)

	got := stream.ReadNativeResolution(p)
	want := image.Point{X: 1280, Y: 720}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadNative_ParsesNonDefaultResolution(t *testing.T) {
	// TrimUI Brick (tg5040) reports U:1024x768p-60.
	dir := t.TempDir()
	p := filepath.Join(dir, "modes")
	os.WriteFile(p, []byte("U:1024x768p-60\n"), 0644)

	got := stream.ReadNativeResolution(p)
	want := image.Point{X: 1024, Y: 768}
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReadNative_FallbackOnMissing(t *testing.T) {
	got := stream.ReadNativeResolution("/nonexistent/modes")
	want := image.Point{X: 1280, Y: 720}
	if got != want {
		t.Errorf("fallback: got %v, want %v", got, want)
	}
}
