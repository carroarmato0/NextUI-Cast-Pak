package stream

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HLSServer struct {
	dir      string
	addr     string
	listener net.Listener
	srv      *http.Server
	stopMu   sync.Mutex

	lastFetchMu sync.RWMutex
	lastFetchAt time.Time
}

func NewHLSServer(dir, addr string) *HLSServer {
	return &HLSServer{dir: dir, addr: addr}
}

// LastSegmentFetchAt returns the time a .ts segment was last served.
// Returns zero time if no segment has been fetched yet.
func (h *HLSServer) LastSegmentFetchAt() time.Time {
	h.lastFetchMu.RLock()
	defer h.lastFetchMu.RUnlock()
	return h.lastFetchAt
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
	mux.Handle("/", h.handler())
	h.srv = &http.Server{Handler: mux}
	go h.srv.Serve(ln) //nolint:errcheck
	return nil
}

// handler wraps http.FileServer to add CORS and correct MIME types.
// The Chromecast Default Media Receiver runs in a browser sandbox — without
// Access-Control-Allow-Origin: * it silently blocks every segment fetch.
// Go's FileServer serves .m3u8 as text/plain; Chromecast needs the proper
// HLS MIME type to recognise the playlist.
// This handler also tracks when .ts segments are fetched for use in detecting stalls.
func (h *HLSServer) handler() http.Handler {
	fs := http.FileServer(http.Dir(h.dir))
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
			h.lastFetchMu.Lock()
			h.lastFetchAt = time.Now()
			h.lastFetchMu.Unlock()
		}
		fs.ServeHTTP(w, r)
	})
}

func (h *HLSServer) Stop() {
	h.stopMu.Lock()
	defer h.stopMu.Unlock()
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
