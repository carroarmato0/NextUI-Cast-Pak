// internal/cast/controller_test.go
package cast_test

import (
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/cast"
	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

func TestController_StartsIdle(t *testing.T) {
	sockPath := t.TempDir() + "/ctrl.sock"
	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	srv.Start()
	defer srv.Stop()

	cfg := config.Defaults()
	ctrl := cast.NewController(
		&cfg,
		t.TempDir()+"/config.json",
		srv,
		wifi.HasWiFi,
	)

	if ctrl.State() != ipc.StateIdle {
		t.Errorf("initial state = %q, want %q", ctrl.State(), ipc.StateIdle)
	}
}

func TestController_StopFromIdle(t *testing.T) {
	sockPath := t.TempDir() + "/ctrl.sock"
	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	srv.Start()
	defer srv.Stop()

	cfg := config.Defaults()
	ctrl := cast.NewController(
		&cfg,
		t.TempDir()+"/config.json",
		srv,
		wifi.HasWiFi,
	)

	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStop})
	if ctrl.State() != ipc.StateIdle {
		t.Errorf("stop from idle: state = %q, want idle", ctrl.State())
	}
}

func TestController_StopResetsSession(t *testing.T) {
	sockPath := t.TempDir() + "/ctrl.sock"
	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	srv.Start()
	defer srv.Stop()

	cfg := config.Defaults()
	ctrl := cast.NewController(
		&cfg,
		t.TempDir()+"/config.json",
		srv,
		wifi.HasWiFi,
	)

	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStop})
	if ctrl.State() != ipc.StateIdle {
		t.Errorf("after stop from idle: state = %q, want idle", ctrl.State())
	}
}
