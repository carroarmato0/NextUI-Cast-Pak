// internal/ipc/server_test.go
package ipc_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
)

func TestServer_AcceptsCommand(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	var got ipc.Command
	done := make(chan struct{})
	srv := ipc.NewServer(sockPath, func(cmd ipc.Command) {
		got = cmd
		close(done)
	})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	json.NewEncoder(conn).Encode(ipc.Command{Cmd: ipc.CmdGetStatus})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
	}

	if got.Cmd != ipc.CmdGetStatus {
		t.Errorf("got cmd %q, want %q", got.Cmd, ipc.CmdGetStatus)
	}
}

func TestServer_BroadcastEvent(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer srv.Stop()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	time.Sleep(20 * time.Millisecond)

	ev := ipc.Event{Event: ipc.EventState, State: ipc.StateStreaming}
	srv.Broadcast(ev)

	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	var got ipc.Event
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.State != ipc.StateStreaming {
		t.Errorf("got state %q, want %q", got.State, ipc.StateStreaming)
	}
}

func TestServer_RemovesPidfile(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	pidPath := filepath.Join(dir, "daemon.pid")

	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	srv.SetPidFile(pidPath)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := os.Stat(pidPath); err != nil {
		t.Error("pidfile should exist after Start")
	}
	srv.Stop()
	if _, err := os.Stat(pidPath); err == nil {
		t.Error("pidfile should be removed after Stop")
	}
}
