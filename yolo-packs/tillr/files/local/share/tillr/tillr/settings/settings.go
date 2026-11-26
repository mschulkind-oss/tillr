// Package settings reads the repository's .tillr.toml. It is shared by startup and
// validation-only callers; neither caller has a second set of parsing rules.
// Architecture and invariants: docs/reference/tillr-restart.md
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

// DefaultTrunkBranch is the default branch name when [trunk] branch is unset.
const DefaultTrunkBranch = "main"

// Trunk names the branch where landings go.
type Trunk struct {
	Branch string
	Set    bool
}

// Intake names the tracked words directory and the paths an idea may change.
type Intake struct {
	Dir   string
	Paths []string
}

// Settings holds optional post-landing actions and review intake. Empty command
// and push.what "none" disable actions; empty remote/ref mean the trunk's
// configured upstream. A nil Comments disables automatic review intake.
type Settings struct {
	Trunk    Trunk
	Sync     Sync
	Intake   Intake
	Lanes    Lanes
	Roll     Roll
	Push     Push
	Comments *Comments
	Prose    Prose
	// WorkflowDefaults maps seats to workflow references; the drain validates files.
	WorkflowDefaults map[string]string
	CandidateSize    CandidateSizeLimits
}

// TrunkBranch returns the configured landing branch, or DefaultTrunkBranch when unset.
func (s Settings) TrunkBranch() string {
	if s.Trunk.Branch == "" {
		return DefaultTrunkBranch
	}
	return s.Trunk.Branch
}

// TrunkRef returns the full branch reference for the configured landing branch.
func (s Settings) TrunkRef() string {
	return "refs/heads/" + s.TrunkBranch()
}

// Sync selects the roadmaps and their direct links read at each loop sync.
// Empty Roadmaps leaves sync unrestricted; the sync command validates paths.
type Sync struct{ Roadmaps []string }

// Prose enables automatic rewrite items; its zero value leaves findings advisory.
type Prose struct{ Rewrite bool }

// CandidateSizeLimits are the advisory thresholds shown with a refused candidate.
type CandidateSizeLimits struct {
	FileLimit      int64
	AddedLineLimit int64
}

// DefaultCandidateSizeLimits returns the thresholds used without overrides.
func DefaultCandidateSizeLimits() CandidateSizeLimits {
	return CandidateSizeLimits{FileLimit: 27, AddedLineLimit: 1200}
}

// Lanes configures lanes reserved for owner-facing work, outside the ordinary cap.
type Lanes struct{ Express int64 }

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

// Push modes: what the loop pushes, and so when. "none" pushes nothing; "tip" pushes the
// trunk tip after each landing, proven or not; "green" pushes the green pointer's commit
// when the pointer advances, so the remote never receives a commit the proof has not passed.
const (
	PushNone  = "none"
	PushTip   = "tip"
	PushGreen = "green"
)

// Push names what to push and where. What is one of PushNone, PushTip and PushGreen.
// The legacy key after_landing = true reads as PushTip.
type Push struct {
	What   string
	Remote string
	Ref    string
}

// Read loads only <root>/.tillr.toml. Missing files use the documented defaults;
// unreadable or malformed files refuse rather than silently disabling actions.
func Read(root string) (Settings, error) {
	return read(root, "")
}

// ReadWithoutLanding rejects explicitly configured trunk/roll/push keys, even empty
// strings and false flags, for the named definition that finishes without landing.
func ReadWithoutLanding(root, definitionName string) (Settings, error) {
	return read(root, definitionName)
}

func read(root, nonLandingDefinition string) (Settings, error) {
	s := Settings{
		Trunk:         Trunk{Branch: DefaultTrunkBranch},
		Roll:          Roll{PauseTimeoutSeconds: 120},
		Push:          Push{What: PushNone},
		CandidateSize: DefaultCandidateSizeLimits(),
	}
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
	if nonLandingDefinition != "" {
		var incompatible []string
		for _, table := range []string{"trunk", "roll", "push"} {
			if values, ok := tables[table].(map[string]any); ok {
				for _, key := range keys(values) {
					incompatible = append(incompatible, table+"."+key)
				}
			}
		}
		if len(incompatible) != 0 {
			return Settings{}, fmt.Errorf("%s: definition %q does not complete on landing: remove settings %s", path, nonLandingDefinition, strings.Join(incompatible, ", "))
		}
	}
	var legacyPush *bool
	whatSet := false
	for _, table := range keys(tables) {
		if table != "trunk" && table != "roll" && table != "push" && table != "comments" && table != "workflows" && table != "prose" && table != "intake" && table != "lanes" && table != "sync" && table != "candidate_size" {
			return Settings{}, refuse(table, "a known table (trunk, roll, push, comments, workflows, prose, intake, lanes, sync or candidate_size)")
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
			case "lanes.express":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuse(full, "a nonnegative integer")
				}
				s.Lanes.Express = n
			case "candidate_size.file_limit", "candidate_size.added_line_limit":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuse(full, "a nonnegative integer")
				}
				if key == "file_limit" {
					s.CandidateSize.FileLimit = n
				} else {
					s.CandidateSize.AddedLineLimit = n
				}
			case "workflows.default":
				defaults, ok := value.(map[string]any)
				if !ok {
					return Settings{}, refuse(full, "a table from seats to workflow strings")
				}
				s.WorkflowDefaults = map[string]string{}
				for _, seat := range keys(defaults) {
					key := full + "." + seat
					if strings.TrimSpace(seat) == "" {
						return Settings{}, refuse(key, "a nonempty seat name")
					}
					ref, ok := defaults[seat].(string)
					if !ok || strings.TrimSpace(ref) == "" {
						return Settings{}, refuse(key, "a nonempty string")
					}
					s.WorkflowDefaults[seat] = ref
				}
			case "trunk.branch":
				str, ok := value.(string)
				if !ok || !validTrunkBranch(str) {
					return Settings{}, refuse(full, "a valid short branch name (for example \"main\" or \"dev\", without \"refs/heads/\")")
				}
				s.Trunk.Branch = str
				s.Trunk.Set = true
			case "roll.command", "push.remote", "push.ref", "comments.vantage_url", "comments.repo", "intake.dir":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuse(full, "a string")
				}
				switch full {
				case "intake.dir":
					s.Intake.Dir = str
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
			case "roll.paths", "comments.paths", "intake.paths", "sync.roadmaps":
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
				if table == "sync" {
					s.Sync.Roadmaps = paths
				} else if table == "intake" {
					s.Intake.Paths = paths
				} else if table == "comments" {
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
			case "prose.rewrite":
				flag, ok := value.(bool)
				if !ok {
					return Settings{}, refuse(full, "a boolean")
				}
				s.Prose.Rewrite = flag
			case "push.after_landing":
				flag, ok := value.(bool)
				if !ok {
					return Settings{}, refuse(full, "a boolean")
				}
				legacyPush = &flag
			case "push.what":
				str, ok := value.(string)
				if !ok || (str != PushNone && str != PushTip && str != PushGreen) {
					return Settings{}, refuse(full, `"none", "tip" or "green"`)
				}
				s.Push.What = str
				whatSet = true
			default:
				expected := "a known push key (what, remote or ref; after_landing is the legacy spelling of what = \"tip\")"
				if table == "trunk" {
					expected = "a known trunk key (branch)"
				}
				if table == "candidate_size" {
					expected = "a known candidate_size key (file_limit or added_line_limit)"
				}
				if table == "sync" {
					expected = "a known sync key (roadmaps)"
				}
				if table == "intake" {
					expected = "a known intake key (dir or paths)"
				}
				if table == "prose" {
					expected = "a known prose key (rewrite)"
				}
				if table == "lanes" {
					expected = "a known lanes key (express)"
				}
				if table == "workflows" {
					expected = "a known workflows key (default)"
				}
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
	if legacyPush != nil {
		if whatSet {
			return Settings{}, refuse("push.after_landing", "absent when push.what is set; after_landing = true is the old spelling of what = \"tip\"")
		}
		if *legacyPush {
			s.Push.What = PushTip
		}
	}
	pathsToCheck := append([]string{}, s.Intake.Paths...)
	if s.Intake.Dir != "" {
		pathsToCheck = append(pathsToCheck, s.Intake.Dir)
	}
	for _, p := range pathsToCheck {
		clean := strings.TrimSuffix(p, "/")
		if clean == "" || clean == "." || !filepath.IsLocal(clean) || filepath.ToSlash(filepath.Clean(clean)) != clean || strings.ContainsAny(p, "*?") {
			return Settings{}, refuse("intake", "clean repository-relative paths, not wildcards or the whole tree")
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
	return s, nil
}

// validTrunkBranch checks git check-ref-format rules for a short branch name.
func validTrunkBranch(name string) bool {
	if name == "" || name == "@" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "refs/") {
		return false
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") {
		return false
	}
	if strings.Contains(name, "..") || strings.Contains(name, "@{") || strings.HasSuffix(name, ".lock") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= 0x20 || c == 0x7f || strings.ContainsRune("~^:?*[\\", rune(c)) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
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
	line := fmt.Sprintf("settings: trunk.branch=%s roll.command=%s roll.paths=%s roll.pause_timeout_seconds=%d push.what=%s push.remote=%s push.ref=%s", strconv.Quote(s.TrunkBranch()), command, encoded, s.Roll.PauseTimeoutSeconds, s.Push.What, remote, ref)
	roadmaps := s.Sync.Roadmaps
	if roadmaps == nil {
		roadmaps = []string{}
	}
	scope, _ := json.Marshal(roadmaps)
	line += " sync.roadmaps=" + string(scope)
	line += fmt.Sprintf(" prose.rewrite=%t lanes.express=%d", s.Prose.Rewrite, s.Lanes.Express)
	if c := s.Comments; c != nil {
		paths, _ := json.Marshal(c.Paths)
		line += fmt.Sprintf(" comments.vantage_url=%s comments.repo=%s comments.paths=%s comments.quiet_seconds=%d comments.poll_seconds=%d comments.max_running=%d comments.compact_over_tokens=%d comments.refresh_hours=%d", strconv.Quote(c.VantageURL), strconv.Quote(c.Repo), paths, c.QuietSeconds, c.PollSeconds, c.MaxRunning, c.CompactOverTokens, c.RefreshHours)
	}
	if s.WorkflowDefaults != nil {
		defaults, _ := json.Marshal(s.WorkflowDefaults)
		line += " workflows.default=" + string(defaults)
	}
	return line
}
