// Package plugin implements discovery and execution of external lifecycle plugins.
//
// Plugins are external executables following the naming convention lifecycle-plugin-<name>.
// They communicate via JSON over stdin/stdout.
//
// Protocol:
//   - lifecycle-plugin-<name> info → returns PluginInfo JSON on stdout
//   - lifecycle-plugin-<name> <command> → reads JSON on stdin, writes JSON on stdout
//   - Exit 0 = success, non-zero = error (stderr has message)
package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const pluginPrefix = "lifecycle-plugin-"

// Plugin represents a discovered plugin executable.
type Plugin struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Commands    []string `json:"commands,omitempty"`
}

// PluginInfo is the JSON structure returned by `lifecycle-plugin-<name> info`.
type PluginInfo struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Commands    []string `json:"commands"`
}

// PluginResult wraps the output of a plugin command execution.
type PluginResult struct {
	Plugin  string          `json:"plugin"`
	Command string          `json:"command"`
	Output  json.RawMessage `json:"output"`
}

// PluginError is returned when a plugin exits with a non-zero status.
type PluginError struct {
	Plugin   string `json:"plugin"`
	ExitCode int    `json:"exit_code"`
	Stderr   string `json:"stderr"`
}

func (e *PluginError) Error() string {
	return fmt.Sprintf("plugin %q failed (exit %d): %s", e.Plugin, e.ExitCode, e.Stderr)
}

// Discover scans the directories in PATH for executables matching the
// lifecycle-plugin-* naming convention. It returns one Plugin per unique
// name (first match wins, matching shell PATH precedence).
func Discover() ([]Plugin, error) {
	return DiscoverInPath(os.Getenv("PATH"))
}

// DiscoverInPath scans the given PATH string for plugin executables.
func DiscoverInPath(pathEnv string) ([]Plugin, error) {
	if pathEnv == "" {
		return nil, nil
	}

	seen := make(map[string]bool)
	var plugins []Plugin

	dirs := filepath.SplitList(pathEnv)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // skip unreadable directories
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			pluginName := ParsePluginName(name)
			if pluginName == "" {
				continue
			}
			if seen[pluginName] {
				continue
			}

			fullPath := filepath.Join(dir, name)
			if !isExecutable(fullPath) {
				continue
			}

			seen[pluginName] = true
			plugins = append(plugins, Plugin{
				Name: pluginName,
				Path: fullPath,
			})
		}
	}

	return plugins, nil
}

// ParsePluginName extracts the plugin name from a filename.
// Returns "" if the filename doesn't match the plugin naming convention.
func ParsePluginName(filename string) string {
	// Strip .exe suffix on Windows.
	base := filename
	if runtime.GOOS == "windows" {
		base = strings.TrimSuffix(base, ".exe")
	}

	if !strings.HasPrefix(base, pluginPrefix) {
		return ""
	}
	name := strings.TrimPrefix(base, pluginPrefix)
	if name == "" {
		return ""
	}
	return name
}

// QueryInfo executes `lifecycle-plugin-<name> info` and parses the result.
func QueryInfo(p Plugin) (Plugin, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(p.Path, "info") //nolint:gosec
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return p, &PluginError{
				Plugin:   p.Name,
				ExitCode: exitErr.ExitCode(),
				Stderr:   strings.TrimSpace(stderr.String()),
			}
		}
		return p, fmt.Errorf("running plugin %q info: %w", p.Name, err)
	}

	var info PluginInfo
	if err := json.Unmarshal(stdout.Bytes(), &info); err != nil {
		return p, fmt.Errorf("parsing plugin %q info output: %w", p.Name, err)
	}

	p.Version = info.Version
	p.Description = info.Description
	p.Commands = info.Commands
	return p, nil
}

// Run executes a plugin command, passing input as JSON on stdin and
// reading JSON output from stdout.
func Run(p Plugin, command string, input interface{}) (json.RawMessage, error) {
	var stdin bytes.Buffer
	if input != nil {
		if err := json.NewEncoder(&stdin).Encode(input); err != nil {
			return nil, fmt.Errorf("encoding plugin input: %w", err)
		}
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(p.Path, command) //nolint:gosec
	cmd.Stdin = &stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, &PluginError{
				Plugin:   p.Name,
				ExitCode: exitErr.ExitCode(),
				Stderr:   strings.TrimSpace(stderr.String()),
			}
		}
		return nil, fmt.Errorf("running plugin %q command %q: %w", p.Name, command, err)
	}

	output := bytes.TrimSpace(stdout.Bytes())
	if len(output) == 0 {
		return json.RawMessage("null"), nil
	}

	if !json.Valid(output) {
		return nil, fmt.Errorf("plugin %q command %q returned invalid JSON", p.Name, command)
	}

	return json.RawMessage(output), nil
}

// ListPlugins discovers plugins and queries each for metadata.
// Plugins that fail the info query are still included with whatever
// metadata could be determined (name, path).
func ListPlugins() ([]Plugin, error) {
	discovered, err := Discover()
	if err != nil {
		return nil, fmt.Errorf("discovering plugins: %w", err)
	}

	result := make([]Plugin, 0, len(discovered))
	for _, p := range discovered {
		enriched, queryErr := QueryInfo(p)
		if queryErr != nil {
			// Include the plugin even if info query fails.
			result = append(result, p)
		} else {
			result = append(result, enriched)
		}
	}

	return result, nil
}

// isExecutable checks if a file is executable by the current user.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// On Windows, check for common executable extensions.
		ext := strings.ToLower(filepath.Ext(path))
		return ext == ".exe" || ext == ".cmd" || ext == ".bat"
	}
	return info.Mode()&0o111 != 0
}
