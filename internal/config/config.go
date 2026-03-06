package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	DefaultDBName     = "lifecycle.db"
	DefaultServerPort = 3847
	ConfigFileName    = ".lifecycle.json"
)

type Config struct {
	ProjectDir string `json:"project_dir"`
	DBPath     string `json:"db_path"`
	ServerPort int    `json:"server_port"`
}

// FindProjectRoot walks up from cwd looking for .lifecycle.json
func FindProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, ConfigFileName)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// Load reads the project config from the given root directory.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, ConfigFileName))
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	cfg.ProjectDir = root

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(root, DefaultDBName)
	}
	if cfg.ServerPort == 0 {
		cfg.ServerPort = DefaultServerPort
	}

	return &cfg, nil
}

// Save writes the config to disk.
func Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.ProjectDir, ConfigFileName), data, 0o644)
}
