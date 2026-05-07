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
	// Save helpers — called immediately on each option change (matches Itch-io Pak pattern).
	// This ensures changes are persisted even when the user exits with B.
	saveQuality := func(val string) {
		a.cfg.Quality = val
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
		if a.client != nil {
			a.client.Send(ipc.Command{Cmd: ipc.CmdSetQuality, Quality: val}) //nolint:errcheck
		}
	}
	saveAudio := func(val bool) {
		a.cfg.Audio = val
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
		if a.client != nil {
			a.client.Send(ipc.Command{Cmd: ipc.CmdSetAudio, Audio: &val}) //nolint:errcheck
		}
	}
	saveLog := func(val string) {
		a.cfg.LogLevel = val
		if err := config.Save(a.cfgPath, a.cfg); err != nil {
			logger.Error("ui: save config: %v", err)
		}
	}

	qualityOptions := []gaba.Option{
		{DisplayName: "Low", Value: "low", OnUpdate: func(_ interface{}) { saveQuality("low") }},
		{DisplayName: "Medium", Value: "medium", OnUpdate: func(_ interface{}) { saveQuality("medium") }},
		{DisplayName: "High", Value: "high", OnUpdate: func(_ interface{}) { saveQuality("high") }},
	}
	audioOptions := []gaba.Option{
		{DisplayName: "On", Value: "true", OnUpdate: func(_ interface{}) { saveAudio(true) }},
		{DisplayName: "Off", Value: "false", OnUpdate: func(_ interface{}) { saveAudio(false) }},
	}
	logOptions := []gaba.Option{
		{DisplayName: "Info", Value: "info", OnUpdate: func(_ interface{}) { saveLog("info") }},
		{DisplayName: "Debug", Value: "debug", OnUpdate: func(_ interface{}) { saveLog("debug") }},
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

	gaba.OptionsList("Settings", gaba.OptionListSettings{}, items) //nolint:errcheck
}

func optionIndex(opts []gaba.Option, value string) int {
	for i, o := range opts {
		if fmt.Sprintf("%s", o.Value) == value {
			return i
		}
	}
	return 0
}
