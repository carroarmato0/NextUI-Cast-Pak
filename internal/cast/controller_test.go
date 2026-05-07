// internal/cast/controller_test.go
package cast_test

import (
	"testing"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/cast"
	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/discovery"
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
		discovery.NewScanner(func(string, time.Duration) ([]discovery.Device, error) { return nil, nil }),
		func() cast.CastClient { return &fakeClient{} },
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
		discovery.NewScanner(func(string, time.Duration) ([]discovery.Device, error) { return nil, nil }),
		func() cast.CastClient { return &fakeClient{} },
		wifi.HasWiFi,
	)

	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStop})
	if ctrl.State() != ipc.StateIdle {
		t.Errorf("stop from idle: state = %q, want idle", ctrl.State())
	}
}
