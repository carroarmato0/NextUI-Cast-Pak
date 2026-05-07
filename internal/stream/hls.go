package stream

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
	mux.Handle("/", http.FileServer(http.Dir(h.dir)))
	h.srv = &http.Server{Handler: mux}
	go h.srv.Serve(ln)
	return nil
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
