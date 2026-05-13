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

func TestController_SelectDeviceDoesNotStartPipeline(t *testing.T) {
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

	ctrl.HandleCommand(ipc.Command{
		Cmd:        ipc.CmdSelectDevice,
		DeviceAddr: "192.168.1.5:8009",
		DeviceName: "Living Room TV",
	})

	// State must still be idle — pipeline must NOT have started.
	if ctrl.State() != ipc.StateIdle {
		t.Errorf("after CmdSelectDevice: state = %q, want idle", ctrl.State())
	}
}

func TestController_StartWithNoDeviceIsNoop(t *testing.T) {
	sockPath := t.TempDir() + "/ctrl.sock"
	srv := ipc.NewServer(sockPath, func(ipc.Command) {})
	srv.Start()
	defer srv.Stop()

	cfg := config.Defaults() // DeviceAddr is empty
	ctrl := cast.NewController(
		&cfg,
		t.TempDir()+"/config.json",
		srv,
		discovery.NewScanner(func(string, time.Duration) ([]discovery.Device, error) { return nil, nil }),
		func() cast.CastClient { return &fakeClient{} },
		wifi.HasWiFi,
	)

	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStart})
	// With no device saved and no device in the command, state stays idle.
	if ctrl.State() != ipc.StateIdle {
		t.Errorf("CmdStart with no device: state = %q, want idle", ctrl.State())
	}
}

func TestStateReconnecting_ConstantDefined(t *testing.T) {
	// Ensure the constant exists and has the expected wire value.
	if ipc.StateReconnecting != "reconnecting" {
		t.Errorf("StateReconnecting = %q, want %q", ipc.StateReconnecting, "reconnecting")
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
		discovery.NewScanner(func(string, time.Duration) ([]discovery.Device, error) { return nil, nil }),
		func() cast.CastClient { return &fakeClient{} },
		wifi.HasWiFi,
	)

	// Stop from idle should leave state as idle (session fields are already zero).
	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStop})
	if ctrl.State() != ipc.StateIdle {
		t.Errorf("after stop from idle: state = %q, want idle", ctrl.State())
	}
}
