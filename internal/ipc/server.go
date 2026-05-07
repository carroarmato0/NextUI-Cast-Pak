package ipc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
)

type Server struct {
	path      string
	pidFile   string
	onCommand func(Command)
	listener  net.Listener

	mu      sync.Mutex
	clients map[net.Conn]struct{}
	done    chan struct{}
}

func NewServer(sockPath string, onCommand func(Command)) *Server {
	return &Server{
		path:      sockPath,
		onCommand: onCommand,
		clients:   make(map[net.Conn]struct{}),
		done:      make(chan struct{}),
	}
}

func (s *Server) SetPidFile(path string) { s.pidFile = path }

func (s *Server) Start() error {
	_ = os.Remove(s.path)
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return fmt.Errorf("ipc server listen: %w", err)
	}
	s.listener = ln
	if s.pidFile != "" {
		_ = os.MkdirAll(filepath.Dir(s.pidFile), 0755)
		_ = os.WriteFile(s.pidFile, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0644)
	}
	go s.accept()
	return nil
}

func (s *Server) Stop() {
	close(s.done)
	if s.listener != nil {
		s.listener.Close()
	}
	if s.pidFile != "" {
		os.Remove(s.pidFile)
	}
	os.Remove(s.path)
}

func (s *Server) Broadcast(ev Event) {
	data, _ := json.Marshal(ev)
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		c.Write(data)
	}
}

func (s *Server) accept() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		s.mu.Lock()
		s.clients[conn] = struct{}{}
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		conn.Close()
	}()
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		var cmd Command
		if err := json.Unmarshal(sc.Bytes(), &cmd); err == nil {
			s.onCommand(cmd)
		}
	}
}
