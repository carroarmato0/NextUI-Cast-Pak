//go:build !headless

package ui

import (
	"os"

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

	// Connect to daemon IPC
	a.client = ipc.NewClient("/tmp/cast/control.sock")
	a.client.Connect() // best-effort; main menu handles disconnected state

	RunMainMenu(a)
}

func uiLogPath() string {
	if platform := os.Getenv("PLATFORM"); platform != "" {
		return "/mnt/SDCARD/.userdata/" + platform + "/logs/Cast.txt"
	}
	return os.Getenv("HOME") + "/Cast.txt"
}
