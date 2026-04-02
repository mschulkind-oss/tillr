package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	DefaultDBName     = "tillr.db"
	DefaultServerPort = 3847
	ConfigFileName    = ".tillr.json"
	YAMLConfigName    = ".tillr.yaml"
)

// QARule defines a single QA review rule for automatic evaluation.
type QARule struct {
	Type      string  `json:"type" yaml:"type"`           // "priority_threshold", "tag", "cycle_type", "score_threshold"
	Value     string  `json:"value" yaml:"value"`         // threshold value or match string
	Action    string  `json:"action" yaml:"action"`       // "review" or "auto_approve"
	Threshold float64 `json:"threshold" yaml:"threshold"` // numeric threshold (for score/priority rules)
}

// QAConfig holds QA review configuration.
type QAConfig struct {
	Rules     []QARule `json:"rules" yaml:"rules"`
	ReviewAll bool     `json:"review_all" yaml:"review_all"` // default: true (review everything)
}

type Config struct {
	ProjectDir string `json:"-" yaml:"-"`
	DBPath     string `json:"db_path" yaml:"db_path"`
	ServerPort int    `json:"server_port" yaml:"server_port"`

	// Extended defaults (from .tillr.yaml)
	DefaultMilestone string `json:"default_milestone,omitempty" yaml:"default_milestone"`
	DefaultPriority  int    `json:"default_priority,omitempty" yaml:"default_priority"`
	Theme            string `json:"theme,omitempty" yaml:"theme"`
	AgentTimeout     int    `json:"agent_timeout_minutes,omitempty" yaml:"agent_timeout_minutes"`

	// QA review rules
	QA *QAConfig `json:"qa,omitempty" yaml:"qa"`

	// Rate limiting configuration
	RateLimit float64 `json:"rate_limit,omitempty" yaml:"rate_limit"` // requests per second (0 = disabled)
	RateBurst int     `json:"rate_burst,omitempty" yaml:"rate_burst"` // burst capacity

	// API key for server authentication (stored in .tillr.json only)
	ApiKey string `json:"api_key,omitempty" yaml:"-"`

	// EncryptionKeyHash stores SHA-256 hash of the encryption password for
	// verification. The actual key is NEVER stored.
	EncryptionKeyHash string `json:"encryption_key_hash,omitempty" yaml:"-"`

	// ActiveProject is the currently selected project ID for multi-project support.
	ActiveProject string `json:"active_project,omitempty" yaml:"active_project"`

	// VantageURL is the base URL for the Vantage documentation viewer.
	// If set, doc links in the web UI open in Vantage for rendered markdown.
	// Can also be set via LIFECYCLE_VANTAGE_URL env var.
	VantageURL string `json:"vantage_url,omitempty" yaml:"vantage_url"`
}

// DefaultQAConfig returns the default QA config (review everything).
func DefaultQAConfig() *QAConfig {
	return &QAConfig{ReviewAll: true}
}

// EvaluateQARules checks QA rules against a feature and returns the action
// ("review" or "auto_approve") and which rule matched (empty string if default).
func (cfg *Config) EvaluateQARules(priority int, tags []string, cycleType string, lastScore float64) (action string, matchedRule string) {
	if cfg.QA == nil || len(cfg.QA.Rules) == 0 {
		return "review", ""
	}
	for _, rule := range cfg.QA.Rules {
		switch rule.Type {
		case "priority_threshold":
			if float64(priority) >= rule.Threshold {
				return rule.Action, fmt.Sprintf("priority_threshold>=%.0f", rule.Threshold)
			}
		case "tag":
			for _, t := range tags {
				if t == rule.Value {
					return rule.Action, fmt.Sprintf("tag=%s", rule.Value)
				}
			}
		case "cycle_type":
			if cycleType == rule.Value {
				return rule.Action, fmt.Sprintf("cycle_type=%s", rule.Value)
			}
		case "score_threshold":
			if lastScore >= rule.Threshold {
				return rule.Action, fmt.Sprintf("score_threshold>=%.1f", rule.Threshold)
			}
		}
	}
	if cfg.QA.ReviewAll {
		return "review", ""
	}
	return "review", ""
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

// FindProjectRoot walks up from cwd looking for .tillr.json
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
// It loads .tillr.json first, then overlays .tillr.yaml defaults.
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

	// Environment variable overrides
	if v := os.Getenv("LIFECYCLE_VANTAGE_URL"); v != "" {
		cfg.VantageURL = v
	}

	return cfg, nil
}

// loadYAMLOverlay reads .tillr.yaml and applies non-zero values onto cfg.
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
	if y.QA != nil && cfg.QA == nil {
		cfg.QA = y.QA
	}
	if y.RateLimit != 0 && cfg.RateLimit == 0 {
		cfg.RateLimit = y.RateLimit
	}
	if y.RateBurst != 0 && cfg.RateBurst == 0 {
		cfg.RateBurst = y.RateBurst
	}
	if y.VantageURL != "" && cfg.VantageURL == "" {
		cfg.VantageURL = y.VantageURL
	}
}

// LoadYAML reads only the .tillr.yaml file from root (or $HOME).
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

// GenerateAPIKey generates a cryptographically random 32-byte hex API key.
func GenerateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// MaskAPIKey returns a masked version of an API key for display.
func MaskAPIKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
