// Package config handles the post-reset minimal project configuration
// stored at .tillr.json in the project root.
//
// Pre-reset config carried QA rules, theme, agent timeouts, encryption
// key hashes, vantage URLs, snapshot retention, etc. After the reset
// the surface is: project_dir, db_path, server_port, plus an optional
// API key for serving auth.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

const (
	DefaultDBName     = "tillr.db"
	DefaultServerPort = 3847
	ConfigFileName    = ".tillr.json"
)

// Config is the minimal post-reset project config.
type Config struct {
	ProjectDir string `json:"-"`
	DBPath     string `json:"db_path"`
	ServerPort int    `json:"server_port"`

	// API key for /api/* authentication. Empty disables auth.
	ApiKey string `json:"api_key,omitempty"`
}

// FindProjectRoot walks up from cwd looking for .tillr.json.
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

// Load reads and returns the project config from the given root.
// WK_PORT env var (set by agent harnesses to assign a per-session port)
// overrides server_port if present.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, ConfigFileName))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		DBPath:     DefaultDBName,
		ServerPort: DefaultServerPort,
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.ProjectDir = root

	if cfg.DBPath == "" {
		cfg.DBPath = DefaultDBName
	}
	if !filepath.IsAbs(cfg.DBPath) {
		cfg.DBPath = filepath.Join(root, cfg.DBPath)
	}
	if cfg.ServerPort == 0 {
		cfg.ServerPort = DefaultServerPort
	}
	if v := os.Getenv("WK_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			cfg.ServerPort = p
		}
	}
	return cfg, nil
}

// Save writes the config to .tillr.json in cfg.ProjectDir.
func Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.ProjectDir, ConfigFileName), data, 0o644)
}

// GenerateAPIKey returns a 32-byte hex API key.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
