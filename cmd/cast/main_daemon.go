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

	var ctrl *cast.Controller
	srv := ipc.NewServer(sockPath, func(cmd ipc.Command) {
		ctrl.HandleCommand(cmd)
	})
	srv.SetPidFile(pidPath)

	ctrl = cast.NewController(
		&cfg, cfgPath, srv,
		wifi.HasWiFi,
	)

	if err := srv.Start(); err != nil {
		logger.Error("daemon: ipc server: %v", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Background status refresh every 15s to push any updates
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdGetStatus})
			case <-ctx.Done():
				return
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	sig := <-sigCh
	logger.Info("daemon: signal %v — shutting down", sig)
	cancel()
	ctrl.HandleCommand(ipc.Command{Cmd: ipc.CmdStop})
	srv.Stop()
}
