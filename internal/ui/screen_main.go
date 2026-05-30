//go:build !headless

package ui

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
)

type menuState struct {
	state             string
	deviceName        string
	errMsg            string
	connected         bool
	lastClientAddr    string
	kbps              int
	ffmpegStartMs     int
	firstByteMs       int
	sessionStartedAt  int64
	reconnects        int
	lastConnectedAt   time.Time
	lastNonZeroKbps   int
	lastNonZeroKbpsAt time.Time
}

var latestState atomic.Value

func RunMainMenu(a *App) {
	runSplitMainMenu(a)
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
		return "○ DLNA Server: Disabled\nReady to start."
	case ipc.StateStreaming:
		addr := "http://" + ms.deviceName + "/description.xml"
		line := "● DLNA Server: Enabled & Running\nAddress: " + addr
		if age != "" {
			line += "\nUp Time: " + age
		}
		metrics := make([]string, 0, 4)
		if ms.connected {
			metrics = append(metrics, "Client: connected")
		} else {
			metrics = append(metrics, "Client: waiting")
		}
		if ms.lastClientAddr != "" {
			metrics = append(metrics, "Last: "+ms.lastClientAddr)
		}
		if ms.kbps > 0 {
			metrics = append(metrics, fmt.Sprintf("Rate: %d kbps", ms.kbps))
		}
		if ms.ffmpegStartMs > 0 {
			metrics = append(metrics, fmt.Sprintf("FFmpeg: %d ms", ms.ffmpegStartMs))
		}
		if ms.firstByteMs > 0 {
			metrics = append(metrics, fmt.Sprintf("First byte: %d ms", ms.firstByteMs))
		}
		if len(metrics) > 0 {
			line += "\n" + strings.Join(metrics, " | ")
		}
		return line
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
			{Text: "Disable DLNA Server"},
			{Text: "Settings"},
			{Text: "Quit"},
		}
	default:
		return []gaba.MenuItem{
			{Text: "Enable DLNA Server"},
			{Text: "Settings"},
			{Text: "Quit"},
		}
	}
}
