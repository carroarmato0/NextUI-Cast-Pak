package main

import (
	"flag"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"runtime/pprof"
	"syscall"

	"github.com/carroarmato0/nextui-cast-pak/internal/logger"
)

var (
	version   = "dev"
	gitCommit = "unknown"
)

func main() {
	daemonMode := flag.Bool("daemon", false, "run as background service")
	cpuProfile := flag.String("cpuprofile", "", "write CPU profile to file")
	memProfile := flag.String("memprofile", "", "write memory profile to file on exit")
	pprofAddr  := flag.String("pprof", "", "start pprof HTTP server on addr")
	flag.Parse()

	logSuffix := "Cast.txt"
	if *daemonMode {
		logSuffix = "Cast.service.txt"
	}
	logPath := logFilePath(logSuffix)
	_ = os.MkdirAll(filepath.Dir(logPath), 0755)
	logger.RotateLog(logPath, "cast "+logSuffix+" starting")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		log.SetOutput(logFile)
		_ = syscall.Dup3(int(logFile.Fd()), 2, 0)
		defer logFile.Close()
	}

	defer func() {
		if r := recover(); r != nil {
			logger.Error("PANIC: %v\n%s", r, debug.Stack())
		}
	}()

	logger.Info("cast %s starting version=%s commit=%s", logSuffix, version, gitCommit)

	setupProfiling(*cpuProfile, *memProfile, *pprofAddr)

	if *daemonMode {
		runDaemon()
	} else {
		runUI()
	}

	if *memProfile != "" {
		writeMemProfile(*memProfile)
	}
}

func logFilePath(suffix string) string {
	if platform := os.Getenv("PLATFORM"); platform != "" {
		return filepath.Join("/mnt/SDCARD/.userdata", platform, "logs", suffix)
	}
	return filepath.Join(os.Getenv("HOME"), suffix)
}

func setupProfiling(cpu, mem, pprofAddr string) {
	if pprofAddr != "" {
		go func() {
			logger.Info("pprof: listening on %s", pprofAddr)
			http.ListenAndServe(pprofAddr, nil) //nolint:errcheck
		}()
	}
	if cpu != "" {
		f, err := os.Create(cpu)
		if err != nil {
			logger.Error("cpuprofile: %v", err)
			os.Exit(1)
		}
		pprof.StartCPUProfile(f)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		go func() {
			<-sigCh
			pprof.StopCPUProfile()
			f.Close()
			if mem != "" {
				writeMemProfile(mem)
			}
			os.Exit(0)
		}()
	}
}

func writeMemProfile(path string) {
	f, err := os.Create(path)
	if err != nil {
		logger.Error("memprofile: %v", err)
		return
	}
	defer f.Close()
	pprof.WriteHeapProfile(f) //nolint:errcheck
	logger.Info("memprofile: written to %s", path)
}

// platformDescription returns a human-readable device name.
func platformDescription(p string) string {
	switch p {
	case "tg5040":
		return "TrimUI Brick / Smart Pro"
	case "tg5050":
		return "TrimUI Smart Pro S"
	case "my355":
		return "Miyoo Flip"
	default:
		return "unknown device"
	}
}
