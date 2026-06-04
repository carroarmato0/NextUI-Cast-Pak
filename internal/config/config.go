package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	DeviceName string `json:"device_name"`
	DeviceAddr string `json:"device_addr"`
	Quality    string `json:"quality"`
	Audio      bool   `json:"audio"`
	LogLevel   string `json:"log_level"`
	Encoder    string `json:"encoder"`
	Transport  string `json:"transport"`
}

func Defaults() Config {
	return Config{
		Quality:   "medium",
		Audio:     true,
		LogLevel:  "info",
		Encoder:   "auto",
		Transport: "ts",
	}
}

func Load(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
