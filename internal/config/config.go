package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultDBName     = "lifecycle.db"
	DefaultServerPort = 3847
	ConfigFileName    = ".lifecycle.json"
	YAMLConfigName    = ".lifecycle.yaml"
)

type Config struct {
	ProjectDir string `json:"project_dir" yaml:"-"`
	DBPath     string `json:"db_path" yaml:"db_path"`
	ServerPort int    `json:"server_port" yaml:"server_port"`

	// Extended defaults (from .lifecycle.yaml)
	DefaultMilestone string `json:"default_milestone,omitempty" yaml:"default_milestone"`
	DefaultPriority  int    `json:"default_priority,omitempty" yaml:"default_priority"`
	Theme            string `json:"theme,omitempty" yaml:"theme"`
	AgentTimeout     int    `json:"agent_timeout_minutes,omitempty" yaml:"agent_timeout_minutes"`
}

// Defaults returns a Config with sensible defaults.
func Defaults() *Config {
	return &Config{
		DefaultPriority: 5,
		ServerPort:      DefaultServerPort,
		Theme:           "system",
		AgentTimeout:    30,
		DBPath:          DefaultDBName,
	}
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
// It loads .lifecycle.json first, then overlays .lifecycle.yaml defaults.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, ConfigFileName))
	if err != nil {
		return nil, err
	}

	cfg := Defaults()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.ProjectDir = root

	// Overlay YAML defaults if present
	loadYAMLOverlay(cfg, root)

	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(root, DefaultDBName)
	} else if !filepath.IsAbs(cfg.DBPath) {
		cfg.DBPath = filepath.Join(root, cfg.DBPath)
	}
	if cfg.ServerPort == 0 {
		cfg.ServerPort = DefaultServerPort
	}

	return cfg, nil
}

// loadYAMLOverlay reads .lifecycle.yaml and applies non-zero values onto cfg.
func loadYAMLOverlay(cfg *Config, root string) {
	yamlPath := filepath.Join(root, YAMLConfigName)
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		// Also check $HOME
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return
		}
		data, err = os.ReadFile(filepath.Join(home, YAMLConfigName))
		if err != nil {
			return
		}
	}

	var y Config
	if err := yaml.Unmarshal(data, &y); err != nil {
		return
	}

	// Only overlay non-zero values from YAML
	if y.DefaultMilestone != "" && cfg.DefaultMilestone == "" {
		cfg.DefaultMilestone = y.DefaultMilestone
	}
	if y.DefaultPriority != 0 && cfg.DefaultPriority == 0 {
		cfg.DefaultPriority = y.DefaultPriority
	}
	if y.ServerPort != 0 && cfg.ServerPort == DefaultServerPort {
		cfg.ServerPort = y.ServerPort
	}
	if y.Theme != "" && cfg.Theme == "" {
		cfg.Theme = y.Theme
	}
	if y.AgentTimeout != 0 && cfg.AgentTimeout == 0 {
		cfg.AgentTimeout = y.AgentTimeout
	}
	if y.DBPath != "" && cfg.DBPath == "" {
		cfg.DBPath = y.DBPath
	}
}

// LoadYAML reads only the .lifecycle.yaml file from root (or $HOME).
func LoadYAML(root string) (*Config, error) {
	yamlPath := filepath.Join(root, YAMLConfigName)
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}
	cfg := Defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.ProjectDir = root
	return cfg, nil
}

// SaveYAML writes the YAML config file.
func SaveYAML(cfg *Config, dir string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, YAMLConfigName), data, 0o644)
}

// Save writes the config to disk.
func Save(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.ProjectDir, ConfigFileName), data, 0o644)
}
