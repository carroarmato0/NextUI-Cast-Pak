package stream_test

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/stream"
)

func TestStreamServer_OnDemandStream(t *testing.T) {
	srv := stream.NewStreamServer("127.0.0.1:0")

	srv.GetFFmpegCmd = func() (*exec.Cmd, error) {
		return exec.Command("sh", "-c", "printf 'hello world'"), nil
	}

	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start stream server: %v", err)
	}
	defer srv.Stop()

	addr := srv.Addr()

	resp, err := http.Get("http://" + addr + "/stream.ts")
	if err != nil {
		t.Fatalf("client request error: %v", err)
	}
	defer resp.Body.Close()

	gotData, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	wantData := "hello world"
	if string(gotData) != wantData {
		t.Errorf("got %q, want %q", gotData, wantData)
	}
}

func TestStreamServer_ContentDirectoryMetadata(t *testing.T) {
	srv := stream.NewStreamServer("127.0.0.1:0")
	srv.GetFFmpegCmd = func() (*exec.Cmd, error) {
		return exec.Command("true"), nil
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("failed to start stream server: %v", err)
	}
	defer srv.Stop()

	addr := srv.Addr()
	soap := `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/">
  <s:Body>
    <u:Browse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">
      <ObjectID>0</ObjectID>
      <BrowseFlag>BrowseDirectChildren</BrowseFlag>
    </u:Browse>
  </s:Body>
</s:Envelope>`
	resp, err := http.Post("http://"+addr+"/control/ContentDirectory", "text/xml; charset=utf-8", strings.NewReader(soap))
	if err != nil {
		t.Fatalf("post request error: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}
	got := string(body)
	for _, want := range []string{"video_folder", "object.container.storageFolder"} {
		if !strings.Contains(got, want) {
			t.Fatalf("response missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "video/mp4") {
		t.Fatalf("response unexpectedly advertised mp4: %s", got)
	}
	if strings.Contains(got, "video/mpeg") {
		t.Fatalf("response unexpectedly advertised mpeg: %s", got)
	}
}
