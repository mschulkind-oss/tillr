// Package settings reads the repository's .tillr.toml. It is shared by startup and
// validation-only callers; neither caller has a second set of parsing rules.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/pelletier/go-toml/v2"
)

// Settings holds the optional post-landing actions. Empty command and false
// after_landing disable actions; empty remote/ref mean the trunk's configured upstream.
type Settings struct {
	Roll Roll
	Push Push
}

// Roll names the paths and command for a post-landing build and the restart wait bound.
type Roll struct {
	Paths               []string
	Command             string
	PauseTimeoutSeconds int64
}

// Push names whether and where to push after landing.
type Push struct {
	AfterLanding bool
	Remote       string
	Ref          string
}

// Read loads only <root>/.tillr.toml. Missing files use the documented defaults;
// unreadable or malformed files refuse rather than silently disabling actions.
func Read(root string) (Settings, error) {
	s := Settings{Roll: Roll{PauseTimeoutSeconds: 120}}
	path := filepath.Join(root, ".tillr.toml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("%s: expected a readable settings file: %w", path, err)
	}
	var tables map[string]any
	if err := toml.Unmarshal(raw, &tables); err != nil {
		return Settings{}, fmt.Errorf("%s: expected valid TOML: %w", path, err)
	}
	refuse := func(key, expected string) error { return fmt.Errorf("%s: %s: expected %s", path, key, expected) }
	for _, table := range keys(tables) {
		if table != "roll" && table != "push" {
			return Settings{}, refuse(table, "a known table (roll or push)")
		}
		values, ok := tables[table].(map[string]any)
		if !ok {
			return Settings{}, refuse(table, "a table")
		}
		for _, key := range keys(values) {
			value := values[key]
			full := table + "." + key
			switch full {
			case "roll.command", "push.remote", "push.ref":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuse(full, "a string")
				}
				switch full {
				case "roll.command":
					s.Roll.Command = str
				case "push.remote":
					s.Push.Remote = str
				case "push.ref":
					s.Push.Ref = str
				}
			case "roll.paths":
				list, ok := value.([]any)
				if !ok {
					return Settings{}, refuse(full, "an array of strings")
				}
				s.Roll.Paths = make([]string, len(list))
				for i, entry := range list {
					str, ok := entry.(string)
					if !ok {
						return Settings{}, refuse(full, "an array of strings")
					}
					s.Roll.Paths[i] = str
				}
			case "roll.pause_timeout_seconds":
				seconds, ok := value.(int64)
				if !ok || seconds <= 0 {
					return Settings{}, refuse(full, "a positive integer number of seconds")
				}
				s.Roll.PauseTimeoutSeconds = seconds
			case "push.after_landing":
				flag, ok := value.(bool)
				if !ok {
					return Settings{}, refuse(full, "a boolean")
				}
				s.Push.AfterLanding = flag
			default:
				expected := "a known push key (after_landing, remote or ref)"
				if table == "roll" {
					expected = "a known roll key (command, paths or pause_timeout_seconds)"
				}
				return Settings{}, refuse(full, expected)
			}
		}
	}
	if s.Roll.Command != "" && len(s.Roll.Paths) == 0 {
		return Settings{}, refuse("roll.command", "nonempty roll.paths when roll.command is set")
	}
	return s, nil
}

// keys keeps multiple refusals deterministic instead of depending on map iteration.
func keys(values map[string]any) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

// StartupLine names every setting, including inactive defaults. upstream denotes
// the trunk's configured upstream, not a guessed remote or branch. JSON quoting
// keeps newlines in TOML strings from turning this into several log lines.
func (s Settings) StartupLine() string {
	command := "none"
	if s.Roll.Command != "" {
		command = strconv.Quote(s.Roll.Command)
	}
	paths := s.Roll.Paths
	if paths == nil {
		paths = []string{}
	}
	encoded, _ := json.Marshal(paths)
	remote, ref := "upstream", "upstream"
	if s.Push.Remote != "" {
		remote = strconv.Quote(s.Push.Remote)
	}
	if s.Push.Ref != "" {
		ref = strconv.Quote(s.Push.Ref)
	}
	return fmt.Sprintf("settings: roll.command=%s roll.paths=%s roll.pause_timeout_seconds=%d push.after_landing=%t push.remote=%s push.ref=%s", command, encoded, s.Roll.PauseTimeoutSeconds, s.Push.AfterLanding, remote, ref)
}
