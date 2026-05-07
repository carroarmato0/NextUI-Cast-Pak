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
	os.WriteFile(filepath.Join(dir, "stream.m3u8"), []byte(content), 0644)

	srv := stream.NewHLSServer(dir, ":0")
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	time.Sleep(20 * time.Millisecond)
	resp, err := http.Get("http://" + srv.Addr() + "/stream.m3u8")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != content {
		t.Errorf("got %q, want %q", body, content)
	}
}
