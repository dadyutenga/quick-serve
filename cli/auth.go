package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type SiteConfig struct {
	OwnerToken string `json:"owner_token"`
	SiteToken  string `json:"site_token"`
	URL        string `json:"url"`
}

type CLIConfig struct {
	Server string                `json:"server"`
	Sites  map[string]SiteConfig `json:"sites"`
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".quick")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func loadConfig() (*CLIConfig, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &CLIConfig{
				Server: defaultServer(),
				Sites:  map[string]SiteConfig{},
			}, nil
		}
		return nil, err
	}
	var cfg CLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Sites == nil {
		cfg.Sites = map[string]SiteConfig{}
	}
	if cfg.Server == "" {
		cfg.Server = defaultServer()
	}
	return &cfg, nil
}

func saveConfig(cfg *CLIConfig) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func defaultServer() string {
	if v := os.Getenv("QUICK_SERVER"); v != "" {
		return v
	}
	return "http://localhost:8080"
}
