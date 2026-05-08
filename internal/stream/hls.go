package stream

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

type HLSServer struct {
	dir      string
	addr     string
	listener net.Listener
	srv      *http.Server
}

func NewHLSServer(dir, addr string) *HLSServer {
	return &HLSServer{dir: dir, addr: addr}
}

func (h *HLSServer) Start() error {
	if h.listener != nil {
		return fmt.Errorf("HLSServer already started")
	}
	ln, err := net.Listen("tcp", h.addr)
	if err != nil {
		return err
	}
	h.listener = ln
	mux := http.NewServeMux()
	mux.Handle("/", hlsHandler(http.Dir(h.dir)))
	h.srv = &http.Server{Handler: mux}
	go h.srv.Serve(ln) //nolint:errcheck
	return nil
}

// hlsHandler wraps http.FileServer to add CORS and correct MIME types.
// The Chromecast Default Media Receiver runs in a browser sandbox — without
// Access-Control-Allow-Origin: * it silently blocks every segment fetch.
// Go's FileServer serves .m3u8 as text/plain; Chromecast needs the proper
// HLS MIME type to recognise the playlist.
func hlsHandler(root http.FileSystem) http.Handler {
	fs := http.FileServer(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		case strings.HasSuffix(r.URL.Path, ".ts"):
			w.Header().Set("Content-Type", "video/MP2T")
		}
		fs.ServeHTTP(w, r)
	})
}

func (h *HLSServer) Stop() {
	if h.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		h.srv.Shutdown(ctx) //nolint:errcheck
		h.srv = nil
		h.listener = nil
	}
}

func (h *HLSServer) Addr() string {
	if h.listener != nil {
		return h.listener.Addr().String()
	}
	return h.addr
}
