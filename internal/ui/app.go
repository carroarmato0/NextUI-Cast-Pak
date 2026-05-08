//go:build !headless

package ui

import (
	"os"
	"time"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

// App holds all UI-side state.
type App struct {
	cfg     config.Config
	cfgPath string
	client  *ipc.Client
	version string
	commit  string
}

func NewApp(cfg config.Config, cfgPath, version, commit string) *App {
	return &App{cfg: cfg, cfgPath: cfgPath, version: version, commit: commit}
}

func (a *App) Run() {
	logPath := uiLogPath()
	gaba.SetLogPath(logPath)
	gaba.SetRawLogLevel(a.cfg.LogLevel)

	gaba.Init(gaba.Options{
		WindowTitle:    "Cast",
		ShowBackground: true,
		IsNextUI:       true,
	})
	defer gaba.Close()

	// WiFi guard
	if !wifi.HasWiFi(nil, nil) {
		logger.Warn("ui: WiFi not connected")
		gaba.ConfirmationMessage( //nolint:errcheck
			"WiFi is not connected.\nEnable WiFi before using Cast.",
			nil,
			gaba.MessageOptions{},
		)
		return
	}

	// Connect to daemon IPC; retry briefly since the daemon may still be starting.
	a.client = ipc.NewClient("/tmp/cast/control.sock")
	for i := 0; i < 10; i++ {
		if err := a.client.Connect(); err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Register ONE unified handler for all daemon events.
	// Per-screen handlers must not call OnEvent — each call replaces the previous
	// callback, so a device-picker registration would silently drop state events.
	a.client.OnEvent(func(ev ipc.Event) {
		switch ev.Event {
		case ipc.EventState:
			latestState.Store(menuState{
				state:      ev.State,
				deviceName: ev.DeviceName,
				errMsg:     ev.Error,
			})
		case ipc.EventDevices:
			deviceCacheMu.Lock()
			deviceCache = ev.Devices
			deviceCacheMu.Unlock()
		}
	})

	RunMainMenu(a)
}

func uiLogPath() string {
	if platform := os.Getenv("PLATFORM"); platform != "" {
		return "/mnt/SDCARD/.userdata/" + platform + "/logs/Cast.txt"
	}
	return os.Getenv("HOME") + "/Cast.txt"
}
