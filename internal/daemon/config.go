// Package daemon implements multi-project server configuration and management.
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProjectEntry represents a single project in the daemon config.
type ProjectEntry struct {
	// Path is the absolute path to the project directory (must contain .tillr.json + tillr.db).
	Path string `json:"path"`

	// Slug is the URL-safe identifier used in /api/p/{slug}/... routes.
	// If empty, derived from the directory basename.
	Slug string `json:"slug,omitempty"`
}

// DaemonConfig holds the daemon's multi-project configuration.
type DaemonConfig struct {
	// Projects is the list of project directories to serve.
	Projects []ProjectEntry `json:"projects"`

	// Port is the HTTP server port (default 3847).
	Port int `json:"port,omitempty"`

	// LogFile is an optional log file path.
	LogFile string `json:"log_file,omitempty"`
}

// DefaultConfigPath returns ~/.config/tillr/daemon.json.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "daemon.json"
	}
	return filepath.Join(home, ".config", "tillr", "daemon.json")
}

// LoadConfig reads the daemon config from the given path.
func LoadConfig(path string) (*DaemonConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading daemon config %s: %w", path, err)
	}
	var cfg DaemonConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing daemon config: %w", err)
	}
	if len(cfg.Projects) == 0 {
		return nil, fmt.Errorf("daemon config has no projects")
	}
	if cfg.Port == 0 {
		cfg.Port = 3847
	}

	// Normalize: resolve paths, derive slugs
	for i := range cfg.Projects {
		p := &cfg.Projects[i]
		abs, err := filepath.Abs(p.Path)
		if err != nil {
			return nil, fmt.Errorf("resolving path %q: %w", p.Path, err)
		}
		p.Path = abs
		if p.Slug == "" {
			p.Slug = filepath.Base(p.Path)
		}
	}

	// Check for duplicate slugs
	seen := make(map[string]string)
	for _, p := range cfg.Projects {
		if prev, ok := seen[p.Slug]; ok {
			return nil, fmt.Errorf("duplicate slug %q: %s and %s", p.Slug, prev, p.Path)
		}
		seen[p.Slug] = p.Path
	}

	return &cfg, nil
}

// InitConfig creates a default daemon config at the given path with the listed project dirs.
func InitConfig(path string, projectDirs []string) error {
	cfg := DaemonConfig{
		Port: 3847,
	}
	for _, dir := range projectDirs {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolving path %q: %w", dir, err)
		}
		cfg.Projects = append(cfg.Projects, ProjectEntry{
			Path: abs,
			Slug: filepath.Base(abs),
		})
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
