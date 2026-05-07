package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/carroarmato0/nextui-cast-pak/internal/cast"
	"github.com/carroarmato0/nextui-cast-pak/internal/config"
	"github.com/carroarmato0/nextui-cast-pak/internal/discovery"
	"github.com/carroarmato0/nextui-cast-pak/internal/ipc"
	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
	"github.com/carroarmato0/nextui-cast-pak/internal/wifi"
)

const (
	sockPath = "/tmp/cast/control.sock"
	pidPath  = "/tmp/cast/daemon.pid"
	castDir  = "/tmp/cast"
)

func runDaemon() {
	cfgPath := filepath.Join(os.Getenv("HOME"), "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		logger.Error("daemon: mkdir config dir: %v", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("daemon: config load: %v", err)
		os.Exit(1)
	}

	logger.SetLevel(logger.LevelFromString(cfg.LogLevel))

	if err := os.MkdirAll(castDir, 0755); err != nil {
		logger.Error("daemon: mkdir cast dir: %v", err)
	}

	scanner := discovery.NewRealScanner()
	go scanner.Scan() //nolint:errcheck

	var ctrl *cast.Controller
	srv := ipc.NewServer(sockPath, func(cmd ipc.Command) {
		ctrl.HandleCommand(cmd)
	})
	srv.SetPidFile(pidPath)

	ctrl = cast.NewController(
		&cfg, cfgPath, srv,
		scanner,
		func() cast.CastClient { return cast.NewRealClient() },
		wifi.HasWiFi,
	)

	if err := srv.Start(); err != nil {
		logger.Error("daemon: ipc server: %v", err)
		os.Exit(1)
	}

	// Auto-connect to last device if configured
	if cfg.DeviceAddr != "" {
		logger.Info("daemon: auto-connecting to %s (%s)", cfg.DeviceName, cfg.DeviceAddr)
		ctrl.HandleCommand(ipc.Command{
			Cmd:        ipc.CmdStart,
			DeviceAddr: cfg.DeviceAddr,
			DeviceName: cfg.DeviceName,
		})
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Background device refresh every 30s
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				scanner.Scan() //nolint:errcheck
				ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdGetStatus})
			case <-ctx.Done():
				return
			}
		}
	}()

	// Handle SIGTERM / SIGINT for clean shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	sig := <-sigCh
	logger.Info("daemon: signal %v — shutting down", sig)
	cancel()
	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStop})
	srv.Stop()
}
