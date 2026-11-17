// Package settings reads the repository's .tillr.toml. It is shared by startup and
// validation-only callers; neither caller has a second set of parsing rules.
package settings

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Settings holds optional post-landing actions and review intake. Empty command
// and false after_landing disable actions; empty remote/ref mean the trunk's
// configured upstream. A nil Comments disables automatic intake.
type Settings struct {
	Roll     Roll
	Push     Push
	Comments *Comments
}

// Comments configures automatic review intake. Seconds and hours are wall-clock units;
// CompactOverTokens counts conversation tokens, and MaxRunning counts desk runs.
type Comments struct {
	VantageURL        string
	Repo              string
	Paths             []string
	QuietSeconds      int64
	PollSeconds       int64
	MaxRunning        int64
	CompactOverTokens int64
	RefreshHours      int64
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
		if table != "roll" && table != "push" && table != "comments" {
			return Settings{}, refuse(table, "a known table (roll, push or comments)")
		}
		values, ok := tables[table].(map[string]any)
		if !ok {
			return Settings{}, refuse(table, "a table")
		}
		if table == "comments" {
			s.Comments = &Comments{QuietSeconds: 60, PollSeconds: 15, MaxRunning: 3, CompactOverTokens: 40000}
		}
		for _, key := range keys(values) {
			value := values[key]
			full := table + "." + key
			switch full {
			case "roll.command", "push.remote", "push.ref", "comments.vantage_url", "comments.repo":
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
				case "comments.vantage_url":
					s.Comments.VantageURL = str
				case "comments.repo":
					s.Comments.Repo = str
				}
			case "roll.paths", "comments.paths":
				list, ok := value.([]any)
				if !ok {
					return Settings{}, refuse(full, "an array of strings")
				}
				paths := make([]string, len(list))
				for i, entry := range list {
					str, ok := entry.(string)
					if !ok {
						return Settings{}, refuse(full, "an array of strings")
					}
					paths[i] = str
				}
				if table == "comments" {
					s.Comments.Paths = paths
				} else {
					s.Roll.Paths = paths
				}
			case "comments.quiet_seconds", "comments.poll_seconds", "comments.max_running", "comments.compact_over_tokens", "comments.refresh_hours":
				n, ok := value.(int64)
				if !ok || n < 0 || (key != "refresh_hours" && n == 0) {
					return Settings{}, refuse(full, "a positive integer (refresh_hours may be zero)")
				}
				switch key {
				case "quiet_seconds":
					s.Comments.QuietSeconds = n
				case "poll_seconds":
					s.Comments.PollSeconds = n
				case "max_running":
					s.Comments.MaxRunning = n
				case "compact_over_tokens":
					s.Comments.CompactOverTokens = n
				case "refresh_hours":
					s.Comments.RefreshHours = n
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
				if table == "comments" {
					expected = "a known comments key (vantage_url, repo, paths, quiet_seconds, poll_seconds, max_running, compact_over_tokens or refresh_hours)"
				}
				return Settings{}, refuse(full, expected)
			}
		}
	}
	if c := s.Comments; c != nil {
		u, err := url.Parse(c.VantageURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return Settings{}, refuse("comments.vantage_url", "an HTTP server URL")
		}
		if len(c.Paths) == 0 {
			return Settings{}, refuse("comments.paths", "a nonempty array of repository-relative paths")
		}
		for _, p := range c.Paths {
			clean := strings.TrimSuffix(p, "/")
			if !filepath.IsLocal(clean) || filepath.ToSlash(filepath.Clean(clean)) != clean {
				return Settings{}, refuse("comments.paths", "clean repository-relative paths")
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
	line := fmt.Sprintf("settings: roll.command=%s roll.paths=%s roll.pause_timeout_seconds=%d push.after_landing=%t push.remote=%s push.ref=%s", command, encoded, s.Roll.PauseTimeoutSeconds, s.Push.AfterLanding, remote, ref)
	if c := s.Comments; c != nil {
		paths, _ := json.Marshal(c.Paths)
		line += fmt.Sprintf(" comments.vantage_url=%s comments.repo=%s comments.paths=%s comments.quiet_seconds=%d comments.poll_seconds=%d comments.max_running=%d comments.compact_over_tokens=%d comments.refresh_hours=%d", strconv.Quote(c.VantageURL), strconv.Quote(c.Repo), paths, c.QuietSeconds, c.PollSeconds, c.MaxRunning, c.CompactOverTokens, c.RefreshHours)
	}
	return line
}
