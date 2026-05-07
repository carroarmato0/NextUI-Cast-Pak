//go:build !headless

package ui

import (
	"fmt"
	"sync"
	"time"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/BrandonKowalski/gabagool/v2/pkg/gabagool/constants"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
)

var (
	deviceCacheMu sync.RWMutex
	deviceCache   []ipc.DeviceInfo
)

func RunDevicePicker(a *App) {
	// Receive device list from daemon
	if a.client != nil {
		a.client.OnEvent(func(ev ipc.Event) {
			if ev.Event == ipc.EventDevices {
				deviceCacheMu.Lock()
				deviceCache = ev.Devices
				deviceCacheMu.Unlock()
			}
		})
		a.client.Send(ipc.Command{Cmd: ipc.CmdGetStatus}) //nolint:errcheck
	}

	// Initial scan via ProcessMessage spinner
	if a.client != nil {
		gaba.ProcessMessage("Scanning for Chromecast devices…", //nolint:errcheck
			gaba.ProcessMessageOptions{ShowProgressBar: false},
			func() (struct{}, error) {
				a.client.Send(ipc.Command{Cmd: ipc.CmdRefreshDevices}) //nolint:errcheck
				time.Sleep(6 * time.Second)                             // allow mDNS scan to complete
				return struct{}{}, nil
			},
		)
	}

	for {
		deviceCacheMu.RLock()
		devs := make([]ipc.DeviceInfo, len(deviceCache))
		copy(devs, deviceCache)
		deviceCacheMu.RUnlock()

		items := make([]gaba.MenuItem, 0, len(devs)+1)
		for _, d := range devs {
			label := d.Name
			if d.Model != "" {
				label = fmt.Sprintf("%s  (%s)", d.Name, d.Model)
			}
			items = append(items, gaba.MenuItem{Text: label})
		}
		if len(items) == 0 {
			items = append(items, gaba.MenuItem{Text: "(no devices found)"})
		}

		opts := gaba.DefaultListOptions("Select Chromecast Device", items)
		opts.SecondaryActionButton = constants.VirtualButtonSelect
		opts.FooterHelpItems = []gaba.FooterHelpItem{
			{ButtonName: constants.VirtualButtonSelect.GetName(), HelpText: "Refresh"},
		}

		result, err := gaba.List(opts)
		if err == gaba.ErrCancelled {
			return
		}
		if err != nil {
			return
		}

		// Select button (secondary action) → refresh
		if result.Action == gaba.ListActionSecondaryTriggered {
			logger.Info("ui: device picker refresh requested")
			if a.client != nil {
				gaba.ProcessMessage("Refreshing…", //nolint:errcheck
					gaba.ProcessMessageOptions{},
					func() (struct{}, error) {
						a.client.Send(ipc.Command{Cmd: ipc.CmdRefreshDevices}) //nolint:errcheck
						time.Sleep(6 * time.Second)
						return struct{}{}, nil
					},
				)
			}
			continue
		}

		if len(devs) == 0 {
			continue
		}

		if len(result.Selected) == 0 {
			continue
		}

		selected := devs[result.Selected[0]]
		if a.client != nil {
			a.client.Send(ipc.Command{ //nolint:errcheck
				Cmd:        ipc.CmdSelectDevice,
				DeviceAddr: selected.Addr,
				DeviceName: selected.Name,
			})
		}
		return
	}
}
