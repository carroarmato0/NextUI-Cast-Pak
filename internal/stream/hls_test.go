// internal/stream/hls_test.go
package stream_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

func TestHLSServer_ServesFile(t *testing.T) {
	dir := t.TempDir()
	content := "#EXTM3U\n#EXT-X-VERSION:3\n"
	if err := os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte(content), 0644); err != nil {
		t.Fatalf("setup: write fixture: %v", err)
	}

	srv := stream.NewHLSServer(dir, ":0")
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	resp, err := http.Get("http://" + srv.Addr() + "/stream.m3u8")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != content {
		t.Errorf("body: got %q, want %q", body, content)
	}

	// CORS header required by Chromecast Default Media Receiver (browser sandbox)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin: got %q, want %q", got, "*")
	}
	// Correct MIME type so the Chromecast recognises the playlist
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type for .m3u8: got %q, want %q", ct, "application/vnd.apple.mpegurl")
	}
}

func TestHLSServer_SegmentMIME(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seg0.ts"), []byte("fake-ts"), 0644); err != nil {
		t.Fatalf("setup: write fixture: %v", err)
	}

	srv := stream.NewHLSServer(dir, ":0")
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	resp, err := http.Get("http://" + srv.Addr() + "/seg0.ts")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin: got %q, want %q", got, "*")
	}
	if ct := resp.Header.Get("Content-Type"); ct != "video/MP2T" {
		t.Errorf("Content-Type for .ts: got %q, want %q", ct, "video/MP2T")
	}
}

func TestHLSServer_TracksTsButNotManifest(t *testing.T) {
	dir := t.TempDir()
	// Create fixture files
	if err := os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte("#EXTM3U\n"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stream0.ts"), []byte("fake-ts"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	srv := stream.NewHLSServer(dir, ":0")
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	// Before any request, LastSegmentFetchAt must be zero.
	if !srv.LastSegmentFetchAt().IsZero() {
		t.Error("LastSegmentFetchAt should be zero before any request")
	}

	// Fetching the manifest must NOT update the timestamp.
	before := time.Now()
	http.Get("http://" + srv.Addr() + "/stream.m3u8") //nolint:errcheck
	if !srv.LastSegmentFetchAt().IsZero() {
		t.Error("manifest fetch must not update LastSegmentFetchAt")
	}

	// Fetching a .ts segment MUST update the timestamp.
	http.Get("http://" + srv.Addr() + "/stream0.ts") //nolint:errcheck
	last := srv.LastSegmentFetchAt()
	if last.IsZero() {
		t.Error("LastSegmentFetchAt should be non-zero after .ts fetch")
	}
	if last.Before(before) {
		t.Error("LastSegmentFetchAt should be >= request time")
	}
}
