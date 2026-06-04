package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carroarmato0/nextui-cast-pak/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg := config.Defaults()
	if cfg.Quality != "medium" {
		t.Errorf("default quality = %q, want %q", cfg.Quality, "medium")
	}
	if !cfg.Audio {
		t.Error("default audio should be true")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("default log_level = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.Transport != "ts" {
		t.Errorf("default transport = %q, want %q", cfg.Transport, "ts")
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := config.Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if cfg.Quality != "medium" {
		t.Error("Load missing should return defaults")
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	want := config.Config{
		DeviceName: "TV",
		DeviceAddr: "192.168.1.5:8009",
		Quality:    "high",
		Audio:      false,
		LogLevel:   "debug",
		Transport:  "ts",
	}
	if err := config.Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLoadInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("{invalid json"), 0644)
	_, err := config.Load(path)
	if err == nil {
		t.Error("Load invalid JSON should return error")
	}
}
