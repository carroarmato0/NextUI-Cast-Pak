//go:build !headless

package ui

import (
	"fmt"

	gaba "github.com/BrandonKowalski/gabagool/v2/pkg/gabagool"
	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
)

func RunSettings(a *App) {
	qualityOptions := []gaba.Option{
		{DisplayName: "Low", Value: "low"},
		{DisplayName: "Medium", Value: "medium"},
		{DisplayName: "High", Value: "high"},
	}
	audioOptions := []gaba.Option{
		{DisplayName: "On", Value: "true"},
		{DisplayName: "Off", Value: "false"},
	}
	logOptions := []gaba.Option{
		{DisplayName: "Info", Value: "info"},
		{DisplayName: "Debug", Value: "debug"},
	}

	qualityIdx := optionIndex(qualityOptions, a.cfg.Quality)
	audioIdx := 0
	if !a.cfg.Audio {
		audioIdx = 1
	}
	logIdx := optionIndex(logOptions, a.cfg.LogLevel)

	items := []gaba.ItemWithOptions{
		{
			Item:           gaba.MenuItem{Text: "Quality"},
			Options:        qualityOptions,
			SelectedOption: qualityIdx,
		},
		{
			Item:           gaba.MenuItem{Text: "Audio"},
			Options:        audioOptions,
			SelectedOption: audioIdx,
		},
		{
			Item:           gaba.MenuItem{Text: "Log Level"},
			Options:        logOptions,
			SelectedOption: logIdx,
		},
		{
			Item: gaba.MenuItem{Text: "About"},
			Options: []gaba.Option{{
				DisplayName: "View",
				Type:        gaba.OptionTypeClickable,
				OnUpdate: func(_ interface{}) {
					msg := fmt.Sprintf("Cast Pak\nVersion: %s\nCommit: %s", a.version, a.commit)
					gaba.ConfirmationMessage(msg, nil, gaba.MessageOptions{}) //nolint:errcheck
				},
			}},
		},
	}

	result, err := gaba.OptionsList("Settings", gaba.OptionListSettings{}, items)
	if err != nil {
		return
	}

	// When About (clickable) is selected, OptionsList returns with that item selected.
	// Its OnUpdate was already called; no config to persist for that item.
	if result.Items[3].Options[result.Items[3].SelectedOption].Type == gaba.OptionTypeClickable {
		return
	}

	// Persist changes
	qualitySelected := result.Items[0].SelectedOption
	a.cfg.Quality = fmt.Sprintf("%s", result.Items[0].Options[qualitySelected].Value)

	audioSelected := result.Items[1].SelectedOption
	audioVal := fmt.Sprintf("%s", result.Items[1].Options[audioSelected].Value)
	a.cfg.Audio = audioVal == "true"

	logSelected := result.Items[2].SelectedOption
	a.cfg.LogLevel = fmt.Sprintf("%s", result.Items[2].Options[logSelected].Value)

	if err := config.Save(a.cfgPath, a.cfg); err != nil {
		logger.Error("ui: save config: %v", err)
	}

	// Hot-reload in daemon
	if a.client != nil {
		a.client.Send(ipc.Command{Cmd: ipc.CmdSetQuality, Quality: a.cfg.Quality}) //nolint:errcheck
		audio := a.cfg.Audio
		a.client.Send(ipc.Command{Cmd: ipc.CmdSetAudio, Audio: &audio}) //nolint:errcheck
	}
}

func optionIndex(opts []gaba.Option, value string) int {
	for i, o := range opts {
		if fmt.Sprintf("%s", o.Value) == value {
			return i
		}
	}
	return 0
}
