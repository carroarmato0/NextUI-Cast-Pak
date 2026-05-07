// internal/ipc/client_test.go
package ipc_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
)

func TestClient_SendCommand(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "client_test.sock")

	var received ipc.Command
	done := make(chan struct{})
	srv := ipc.NewServer(sockPath, func(cmd ipc.Command) {
		received = cmd
		close(done)
	})
	srv.Start()
	defer srv.Stop()

	cl := ipc.NewClient(sockPath)
	if err := cl.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cl.Close()

	if err := cl.Send(ipc.Command{Cmd: ipc.CmdStop}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server to receive command")
	}

	if received.Cmd != ipc.CmdStop {
		t.Errorf("server received %q, want %q", received.Cmd, ipc.CmdStop)
	}
}

func TestClient_ReceivesEvent(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "client_ev.sock")

	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	srv.Start()
	defer srv.Stop()

	var gotEvent ipc.Event
	gotCh := make(chan struct{}, 1)

	cl := ipc.NewClient(sockPath)
	cl.OnEvent(func(ev ipc.Event) {
		gotEvent = ev
		gotCh <- struct{}{}
	})
	if err := cl.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cl.Close()

	time.Sleep(20 * time.Millisecond)
	srv.Broadcast(ipc.Event{Event: ipc.EventState, State: ipc.StateIdle})

	select {
	case <-gotCh:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}
	if gotEvent.State != ipc.StateIdle {
		t.Errorf("got state %q, want %q", gotEvent.State, ipc.StateIdle)
	}
}
