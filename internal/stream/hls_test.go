// internal/stream/hls_test.go
package stream_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

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
		t.Errorf("got %q, want %q", body, content)
	}
}
