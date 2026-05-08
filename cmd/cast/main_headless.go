//go:build headless

package main

import (
	"os"

	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
)

func runUI() {
	logger.Info("headless build: UI not available")
	os.Exit(0)
}
