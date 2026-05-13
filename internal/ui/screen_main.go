//go:build !headless

package ui

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

type menuState struct {
	state            string
	deviceName       string
	errMsg           string
	sessionStartedAt int64
	reconnects       int
}

var latestState atomic.Value // stores menuState

// RunMainMenu runs the main menu loop until the user quits.
func RunMainMenu(a *App) {
	latestState.Store(menuState{state: ""})

	if a.client != nil {
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

func sessionAge(startedAt int64) string {
	if startedAt == 0 {
		return ""
	}
	d := time.Since(time.Unix(startedAt, 0)).Round(time.Second)
	if d <= 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func statusPill(ms menuState) string {
	age := sessionAge(ms.sessionStartedAt)
	switch ms.state {
	case "":
		return "○ Service not running"
	case ipc.StateIdle:
		if ms.deviceName != "" {
			return "○ Ready  —  " + ms.deviceName
		}
		return "○ Ready"
	case ipc.StateStreaming:
		line2 := age + "  ·  Reconnects: " + strconv.Itoa(ms.reconnects)
		return "● Casting to " + ms.deviceName + "\n" + line2
	case ipc.StateReconnecting:
		line2 := fmt.Sprintf("Attempt %d", ms.reconnects+1)
		if age != "" {
			line2 += "  ·  Session: " + age
		}
		return "◌ Reconnecting to " + ms.deviceName + "…\n" + line2
	case ipc.StateConnecting:
		return "◌ Connecting to " + ms.deviceName + "…"
	case ipc.StateScanning:
		return "◌ Scanning for devices…"
	case ipc.StateError:
		pill := "⚠ " + ms.errMsg
		if age != "" {
			pill += "\nStopped after " + age
		}
		return pill
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
	case ipc.StateReconnecting:
		return []gaba.MenuItem{
			{Text: "Stop"},
			{Text: "Change Device"},
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
