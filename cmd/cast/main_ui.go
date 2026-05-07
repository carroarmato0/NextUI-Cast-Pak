//go:build !headless

package main

import (
	"os"
	"path/filepath"

	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/ui"
)

func runUI() {
	cfgPath := filepath.Join(os.Getenv("HOME"), "config.json")
	cfg, _ := config.Load(cfgPath)
	app := ui.NewApp(cfg, cfgPath, version, gitCommit)
	app.Run()
}
