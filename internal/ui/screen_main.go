//go:build !headless

package ui

import (
	"sync/atomic"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

type menuState struct {
	state      string
	deviceName string
	errMsg     string
}

var latestState atomic.Value // stores menuState

// RunMainMenu runs the main menu loop until the user quits.
func RunMainMenu(a *App) {
	latestState.Store(menuState{state: ""})

	// Listen for daemon state pushes
	if a.client != nil {
		a.client.OnEvent(func(ev ipc.Event) {
			if ev.Event == ipc.EventState {
				latestState.Store(menuState{
					state:      ev.State,
					deviceName: ev.DeviceName,
					errMsg:     ev.Error,
				})
			}
		})
		a.client.Send(ipc.Command{Cmd: ipc.CmdGetStatus}) //nolint:errcheck
	}

	for {
		ms := latestState.Load().(menuState)

		// Re-check WiFi on each menu redraw
		if !wifi.HasWiFi(nil, nil) {
			logger.Warn("ui: WiFi lost on menu redraw")
			gaba.ConfirmationMessage( //nolint:errcheck
				"WiFi is not connected.\nEnable WiFi before using Cast.",
				nil,
				gaba.MessageOptions{},
			)
			return
		}

		statusText := statusPill(ms)
		items := menuItems(ms)

		result, err := gaba.List(gaba.DefaultListOptions(statusText, items))
		if err == gaba.ErrCancelled {
			return
		}
		if err != nil {
			return
		}

		if len(result.Selected) == 0 {
			continue
		}

		action := items[result.Selected[0]].Text
		switch action {
		case "Select Device", "Change Device":
			RunDevicePicker(a)
		case "Start Casting":
			if a.client != nil {
				a.client.Send(ipc.Command{Cmd: ipc.CmdStart}) //nolint:errcheck
			}
		case "Stop", "Stop Casting":
			if a.client != nil {
				a.client.Send(ipc.Command{Cmd: ipc.CmdStop}) //nolint:errcheck
			}
		case "Settings":
			RunSettings(a)
		case "Quit":
			return
		}
	}
}

func statusPill(ms menuState) string {
	switch ms.state {
	case "":
		return "○ Service not running"
	case ipc.StateIdle:
		if ms.deviceName != "" {
			return "○ Ready  —  " + ms.deviceName
		}
		return "○ Ready"
	case ipc.StateStreaming:
		return "● Casting to " + ms.deviceName
	case ipc.StateConnecting:
		return "◌ Connecting to " + ms.deviceName + "…"
	case ipc.StateScanning:
		return "◌ Scanning for devices…"
	case ipc.StateError:
		return "⚠ " + ms.errMsg
	default:
		return "○ Ready"
	}
}

func menuItems(ms menuState) []gaba.MenuItem {
	switch ms.state {
	case ipc.StateStreaming:
		return []gaba.MenuItem{
			{Text: "Stop Casting"},
			{Text: "Change Device"},
			{Text: "Settings"},
			{Text: "Quit"},
		}
	case ipc.StateScanning, ipc.StateConnecting:
		return []gaba.MenuItem{
			{Text: "Stop"},
			{Text: "Settings"},
			{Text: "Quit"},
		}
	default:
		if ms.deviceName != "" {
			return []gaba.MenuItem{
				{Text: "Start Casting"},
				{Text: "Change Device"},
				{Text: "Settings"},
				{Text: "Quit"},
			}
		}
		return []gaba.MenuItem{
			{Text: "Select Device"},
			{Text: "Settings"},
			{Text: "Quit"},
		}
	}
}
