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
	"time"

	"github.com/pelletier/go-toml/v2"
)

// DefaultTrunkBranch is the default branch name when [trunk] branch is unset.
const DefaultTrunkBranch = "main"

// DefaultChecksDir is the default check-declarations directory when [gate] checks_dir is unset.
const DefaultChecksDir = "tools/gate/checks"

// DefaultWorktreeDiskBudgetPerAgentBytes is the default worktree+scratch disk budget per
// running agent (4 GiB, D-VO10).
const DefaultWorktreeDiskBudgetPerAgentBytes int64 = 4 * 1024 * 1024 * 1024

// DefaultWorktreeCountMultiplier is the default worktree count bound multiplier against
// the running-agent limit (2x, D-VO10).
const DefaultWorktreeCountMultiplier = 2

// Worktrees configures worktree count and disk bounds (D-VO10).
type Worktrees struct {
	DiskBudgetBytes int64
	DiskBudgetSet   bool
	MaxCount        int64
	MaxCountSet     bool
}

// EffectiveDiskBudgetBytes returns the configured disk budget or
// runningAgentLimit * DefaultWorktreeDiskBudgetPerAgentBytes.
func (w Worktrees) EffectiveDiskBudgetBytes(runningAgentLimit int) int64 {
	if w.DiskBudgetSet {
		return w.DiskBudgetBytes
	}
	if runningAgentLimit <= 0 {
		return 0
	}
	return int64(runningAgentLimit) * DefaultWorktreeDiskBudgetPerAgentBytes
}

// EffectiveMaxCount returns the configured worktree count bound or
// runningAgentLimit * DefaultWorktreeCountMultiplier.
func (w Worktrees) EffectiveMaxCount(runningAgentLimit int) int {
	if w.MaxCountSet {
		return int(w.MaxCount)
	}
	if runningAgentLimit <= 0 {
		return 0
	}
	return runningAgentLimit * DefaultWorktreeCountMultiplier
}

// Gate names the repository's check-declarations directory.
type Gate struct {
	ChecksDir string
	// DisabledTests is the repository-relative path of the tracked disabled-test list.
	DisabledTests string
	Set           bool
}

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

// Brief names a repository-specific brief template that overrides the installed default.
type Brief struct {
	Template string
}

// Drain configures defaults for tillr-drain loop.
type Drain struct {
	ExitWhenDrained        bool
	ExitWhenDrainedSet     bool
	Store                  string
	StoreSet               bool
	MaxAgents              int64
	MaxAgentsSet           bool
	Lanes                  int64
	LanesSet               bool
	WorktreeCount          int64
	WorktreeCountSet       bool
	WorktreeDiskBudget     int64
	WorktreeDiskBudgetSet  bool
	MaxConcurrentTriage    int64
	MaxConcurrentTriageSet bool
	LaneRoot               string
	LaneRootSet            bool
	BackoffBase            string
	BackoffBaseSet         bool
	FallbackPoll           string
	FallbackPollSet        bool
	NoNotify               bool
	NoNotifySet            bool
	Policy                 string
	PolicySet              bool
	Profile                string
	ProfileSet             bool
	Profiles               []string
	ProfilesSet            bool
	RunBin                 string
	RunBinSet              bool
	ItemBin                string
	ItemBinSet             bool
	BriefTemplate          string
	BriefTemplateSet       bool
	Gate                   string
	GateSet                bool
	Land                   string
	LandSet                bool
	Verdict                string
	VerdictSet             bool
	Replay                 string
	ReplaySet              bool
	HeavyGate              string
	HeavyGateSet           bool
	Rolling                string
	RollingSet             bool
	FixerGate              string
	FixerGateSet           bool
	QuestionsCmd           string
	QuestionsCmdSet        bool
	QuestionsIndex         string
	QuestionsIndexSet      bool
}

// Settings holds optional post-landing actions and review intake. Empty command
// and push.what "none" disable actions; empty remote/ref mean the trunk's
// configured upstream. A nil Comments disables automatic review intake.
type Settings struct {
	Trunk     Trunk
	Brief     Brief
	Gate      Gate
	Drain     Drain
	Sync      Sync
	Intake    Intake
	Lanes     Lanes
	Worktrees Worktrees
	Roll      Roll
	Push      Push
	Comments  *Comments
	Prose     Prose
	// WorkflowDefaults maps seats to workflow references; the drain validates files.
	WorkflowDefaults map[string]string
	CandidateSize    CandidateSizeLimits
}

// ChecksDir returns the configured check-declarations directory, or DefaultChecksDir when unset.
func (s Settings) ChecksDir() string {
	if s.Gate.ChecksDir == "" {
		return DefaultChecksDir
	}
	return s.Gate.ChecksDir
}

// DefaultDisabledTests is the disabled-test list path when [gate] disabled_tests is unset.
const DefaultDisabledTests = "tillr-disabled-tests.json"

// DisabledTestsPath returns the configured disabled-test list path, or DefaultDisabledTests when unset.
func (s Settings) DisabledTestsPath() string {
	if s.Gate.DisabledTests == "" {
		return DefaultDisabledTests
	}
	return s.Gate.DisabledTests
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
	refuseValue := func(key string, val any, expected string) error {
		if str, ok := val.(string); ok {
			return fmt.Errorf("%s: %s=%q: expected %s", path, key, str, expected)
		}
		return fmt.Errorf("%s: %s=%v: expected %s", path, key, val, expected)
	}
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
		if table != "trunk" && table != "brief" && table != "gate" && table != "drain" && table != "roll" && table != "push" && table != "comments" && table != "workflows" && table != "prose" && table != "intake" && table != "lanes" && table != "worktrees" && table != "sync" && table != "candidate_size" {
			return Settings{}, refuse(table, "a known table (trunk, brief, gate, drain, roll, push, comments, workflows, prose, intake, lanes, worktrees, sync or candidate_size)")
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
			case "worktrees.disk_budget_bytes":
				n, ok := value.(int64)
				if !ok || n <= 0 {
					return Settings{}, refuse(full, "a positive integer")
				}
				s.Worktrees.DiskBudgetBytes = n
				s.Worktrees.DiskBudgetSet = true
			case "worktrees.max_count":
				n, ok := value.(int64)
				if !ok || n <= 0 {
					return Settings{}, refuse(full, "a positive integer")
				}
				s.Worktrees.MaxCount = n
				s.Worktrees.MaxCountSet = true
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
			case "brief.template":
				str, ok := value.(string)
				if !ok || !validRepoFilePath(str) {
					return Settings{}, refuse(full, "a clean repository-relative file path")
				}
				s.Brief.Template = str
			case "gate.checks_dir":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuse(full, "a clean repository-relative subdirectory path outside .git")
				}
				clean, ok := cleanChecksDir(str)
				if !ok {
					return Settings{}, refuse(full, "a clean repository-relative subdirectory path outside .git")
				}
				s.Gate.ChecksDir = clean
				s.Gate.Set = true
			case "gate.disabled_tests":
				str, ok := value.(string)
				if !ok || !validRepoFilePath(str) {
					return Settings{}, refuse(full, "a clean repository-relative file path outside .git")
				}
				s.Gate.DisabledTests = str
				s.Gate.Set = true
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
			case "drain.exit_when_drained":
				flag, ok := value.(bool)
				if !ok {
					return Settings{}, refuseValue(full, value, "a boolean")
				}
				s.Drain.ExitWhenDrained = flag
				s.Drain.ExitWhenDrainedSet = true
			case "drain.no_notify":
				flag, ok := value.(bool)
				if !ok {
					return Settings{}, refuseValue(full, value, "a boolean")
				}
				s.Drain.NoNotify = flag
				s.Drain.NoNotifySet = true
			case "drain.max_agents":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative integer")
				}
				s.Drain.MaxAgents = n
				s.Drain.MaxAgentsSet = true
			case "drain.lanes":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative integer")
				}
				s.Drain.Lanes = n
				s.Drain.LanesSet = true
			case "drain.worktree_count":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative integer")
				}
				s.Drain.WorktreeCount = n
				s.Drain.WorktreeCountSet = true
			case "drain.worktree_disk_budget":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative integer")
				}
				s.Drain.WorktreeDiskBudget = n
				s.Drain.WorktreeDiskBudgetSet = true
			case "drain.max_concurrent_triage":
				n, ok := value.(int64)
				if !ok || n < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative integer")
				}
				s.Drain.MaxConcurrentTriage = n
				s.Drain.MaxConcurrentTriageSet = true
			case "drain.store":
				str, ok := value.(string)
				if !ok || !validRepoFilePath(str) {
					return Settings{}, refuseValue(full, value, "a clean repository-relative file path")
				}
				s.Drain.Store = str
				s.Drain.StoreSet = true
			case "drain.lane_root":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a clean repository-relative subdirectory path outside .git")
				}
				clean, ok := cleanChecksDir(str)
				if !ok {
					return Settings{}, refuseValue(full, value, "a clean repository-relative subdirectory path outside .git")
				}
				s.Drain.LaneRoot = clean
				s.Drain.LaneRootSet = true
			case "drain.backoff_base":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a nonnegative duration string (for example \"1s\")")
				}
				d, err := time.ParseDuration(str)
				if err != nil || d < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative duration string (for example \"1s\")")
				}
				s.Drain.BackoffBase = str
				s.Drain.BackoffBaseSet = true
			case "drain.fallback_poll":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a nonnegative duration string (for example \"10s\")")
				}
				d, err := time.ParseDuration(str)
				if err != nil || d < 0 {
					return Settings{}, refuseValue(full, value, "a nonnegative duration string (for example \"10s\")")
				}
				s.Drain.FallbackPoll = str
				s.Drain.FallbackPollSet = true
			case "drain.policy":
				str, ok := value.(string)
				if !ok || !validRepoFilePath(str) {
					return Settings{}, refuseValue(full, value, "a clean repository-relative file path")
				}
				s.Drain.Policy = str
				s.Drain.PolicySet = true
			case "drain.profile":
				str, ok := value.(string)
				if !ok || !ValidProfileName(str) {
					return Settings{}, refuseValue(full, value, "a valid profile name matching [a-z0-9][a-z0-9_-]*")
				}
				s.Drain.Profile = str
				s.Drain.ProfileSet = true
			case "drain.profiles":
				list, ok := value.([]any)
				if !ok || len(list) == 0 {
					return Settings{}, refuseValue(full, value, "a nonempty array of distinct valid profile names matching [a-z0-9][a-z0-9_-]*")
				}
				profiles := make([]string, len(list))
				seen := make(map[string]bool, len(list))
				for i, item := range list {
					str, ok := item.(string)
					if !ok || !ValidProfileName(str) {
						return Settings{}, refuseValue(full, value, "a nonempty array of distinct valid profile names matching [a-z0-9][a-z0-9_-]*")
					}
					if seen[str] {
						return Settings{}, fmt.Errorf("%s: %s: duplicate profile name %q", path, full, str)
					}
					seen[str] = true
					profiles[i] = str
				}
				s.Drain.Profiles = profiles
				s.Drain.ProfilesSet = true
			case "drain.run_bin":
				str, ok := value.(string)
				if !ok || strings.TrimSpace(str) == "" {
					return Settings{}, refuseValue(full, value, "a nonempty string")
				}
				s.Drain.RunBin = str
				s.Drain.RunBinSet = true
			case "drain.item_bin":
				str, ok := value.(string)
				if !ok || strings.TrimSpace(str) == "" {
					return Settings{}, refuseValue(full, value, "a nonempty string")
				}
				s.Drain.ItemBin = str
				s.Drain.ItemBinSet = true
			case "drain.brief_template":
				str, ok := value.(string)
				if !ok || !validRepoFilePath(str) {
					return Settings{}, refuseValue(full, value, "a clean repository-relative file path")
				}
				s.Drain.BriefTemplate = str
				s.Drain.BriefTemplateSet = true
			case "drain.gate":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.Gate = str
				s.Drain.GateSet = true
			case "drain.land":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.Land = str
				s.Drain.LandSet = true
			case "drain.verdict":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.Verdict = str
				s.Drain.VerdictSet = true
			case "drain.replay":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.Replay = str
				s.Drain.ReplaySet = true
			case "drain.heavy_gate":
				str, ok := value.(string)
				if !ok || strings.TrimSpace(str) == "" {
					return Settings{}, refuseValue(full, value, "a nonempty string")
				}
				s.Drain.HeavyGate = str
				s.Drain.HeavyGateSet = true
			case "drain.rolling":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.Rolling = str
				s.Drain.RollingSet = true
			case "drain.fixer_gate":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.FixerGate = str
				s.Drain.FixerGateSet = true
			case "drain.questions_cmd":
				str, ok := value.(string)
				if !ok {
					return Settings{}, refuseValue(full, value, "a string")
				}
				s.Drain.QuestionsCmd = str
				s.Drain.QuestionsCmdSet = true
			case "drain.questions_index":
				str, ok := value.(string)
				if !ok || !validRepoFilePath(str) {
					return Settings{}, refuseValue(full, value, "a clean repository-relative file path")
				}
				s.Drain.QuestionsIndex = str
				s.Drain.QuestionsIndexSet = true
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
				if table == "brief" {
					expected = "a known brief key (template)"
				}
				if table == "gate" {
					expected = "a known gate key (checks_dir or disabled_tests)"
				}
				if table == "drain" {
					expected = "a known drain key (exit_when_drained, store, max_agents, lanes, worktree_count, worktree_disk_budget, max_concurrent_triage, lane_root, backoff_base, fallback_poll, no_notify, policy, profile, profiles, run_bin, item_bin, brief_template, gate, land, verdict, replay, heavy_gate, rolling, fixer_gate, questions_cmd or questions_index)"
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
				if table == "worktrees" {
					expected = "a known worktrees key (disk_budget_bytes or max_count)"
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
	if s.Drain.MaxAgentsSet && s.Drain.LanesSet && s.Drain.MaxAgents != s.Drain.Lanes {
		return Settings{}, fmt.Errorf("%s: drain.max_agents (%d) and drain.lanes (%d) disagree; set one", path, s.Drain.MaxAgents, s.Drain.Lanes)
	}
	if s.Drain.ProfileSet && s.Drain.ProfilesSet {
		return Settings{}, fmt.Errorf("%s: drain.profile and drain.profiles cannot both be set; set one", path)
	}
	if s.Worktrees.MaxCountSet && s.Drain.WorktreeCountSet && s.Worktrees.MaxCount != s.Drain.WorktreeCount {
		return Settings{}, fmt.Errorf("%s: worktrees.max_count (%d) and drain.worktree_count (%d) disagree; set one", path, s.Worktrees.MaxCount, s.Drain.WorktreeCount)
	}
	if s.Worktrees.DiskBudgetSet && s.Drain.WorktreeDiskBudgetSet && s.Worktrees.DiskBudgetBytes != s.Drain.WorktreeDiskBudget {
		return Settings{}, fmt.Errorf("%s: worktrees.disk_budget_bytes (%d) and drain.worktree_disk_budget (%d) disagree; set one", path, s.Worktrees.DiskBudgetBytes, s.Drain.WorktreeDiskBudget)
	}
	if s.Brief.Template != "" && s.Drain.BriefTemplateSet && s.Brief.Template != s.Drain.BriefTemplate {
		return Settings{}, fmt.Errorf("%s: brief.template (%q) and drain.brief_template (%q) disagree; set one", path, s.Brief.Template, s.Drain.BriefTemplate)
	}
	return s, nil
}

// ValidProfileName reports whether name matches [a-z0-9][a-z0-9_-]*.
func ValidProfileName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if i == 0 {
			if !isLower && !isDigit {
				return false
			}
			continue
		}
		if !isLower && !isDigit && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

// validRepoFilePath checks that path is a clean repository-relative file path outside .git.
func validRepoFilePath(path string) bool {
	if path == "" || path == "." || path == ".." || strings.HasSuffix(path, "/") || strings.Contains(path, "\\") || strings.ContainsAny(path, "*?") {
		return false
	}
	if !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	if path == ".git" || strings.HasPrefix(path, ".git/") {
		return false
	}
	return true
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

// cleanChecksDir validates gate.checks_dir as a clean repository-relative subdirectory
// outside .git, trimming at most one trailing '/'.
func cleanChecksDir(raw string) (string, bool) {
	if strings.ContainsAny(raw, "*?\\") || strings.Contains(raw, "//") {
		return "", false
	}
	clean := strings.TrimSuffix(raw, "/")
	if clean == "" || clean == "." || clean == ".." || strings.HasSuffix(clean, "/") {
		return "", false
	}
	if clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", false
	}
	if !filepath.IsLocal(clean) || filepath.ToSlash(filepath.Clean(clean)) != clean {
		return "", false
	}
	return clean, true
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
	line := fmt.Sprintf("settings: trunk.branch=%s gate.checks_dir=%s roll.command=%s roll.paths=%s roll.pause_timeout_seconds=%d push.what=%s push.remote=%s push.ref=%s", strconv.Quote(s.TrunkBranch()), strconv.Quote(s.ChecksDir()), command, encoded, s.Roll.PauseTimeoutSeconds, s.Push.What, remote, ref)
	roadmaps := s.Sync.Roadmaps
	if roadmaps == nil {
		roadmaps = []string{}
	}
	scope, _ := json.Marshal(roadmaps)
	line += " sync.roadmaps=" + string(scope)
	line += fmt.Sprintf(" prose.rewrite=%t lanes.express=%d", s.Prose.Rewrite, s.Lanes.Express)
	if s.Worktrees.DiskBudgetSet {
		line += fmt.Sprintf(" worktrees.disk_budget_bytes=%d", s.Worktrees.DiskBudgetBytes)
	}
	if s.Worktrees.MaxCountSet {
		line += fmt.Sprintf(" worktrees.max_count=%d", s.Worktrees.MaxCount)
	}
	if c := s.Comments; c != nil {
		paths, _ := json.Marshal(c.Paths)
		line += fmt.Sprintf(" comments.vantage_url=%s comments.repo=%s comments.paths=%s comments.quiet_seconds=%d comments.poll_seconds=%d comments.max_running=%d comments.compact_over_tokens=%d comments.refresh_hours=%d", strconv.Quote(c.VantageURL), strconv.Quote(c.Repo), paths, c.QuietSeconds, c.PollSeconds, c.MaxRunning, c.CompactOverTokens, c.RefreshHours)
	}
	if s.Brief.Template != "" {
		line += fmt.Sprintf(" brief.template=%s", strconv.Quote(s.Brief.Template))
	}
	if s.Drain.ExitWhenDrainedSet {
		line += fmt.Sprintf(" drain.exit_when_drained=%t", s.Drain.ExitWhenDrained)
	}
	if s.Drain.StoreSet {
		line += fmt.Sprintf(" drain.store=%s", strconv.Quote(s.Drain.Store))
	}
	if s.Drain.MaxAgentsSet {
		line += fmt.Sprintf(" drain.max_agents=%d", s.Drain.MaxAgents)
	}
	if s.Drain.LanesSet {
		line += fmt.Sprintf(" drain.lanes=%d", s.Drain.Lanes)
	}
	if s.Drain.WorktreeCountSet {
		line += fmt.Sprintf(" drain.worktree_count=%d", s.Drain.WorktreeCount)
	}
	if s.Drain.WorktreeDiskBudgetSet {
		line += fmt.Sprintf(" drain.worktree_disk_budget=%d", s.Drain.WorktreeDiskBudget)
	}
	if s.Drain.MaxConcurrentTriageSet {
		line += fmt.Sprintf(" drain.max_concurrent_triage=%d", s.Drain.MaxConcurrentTriage)
	}
	if s.Drain.LaneRootSet {
		line += fmt.Sprintf(" drain.lane_root=%s", strconv.Quote(s.Drain.LaneRoot))
	}
	if s.Drain.BackoffBaseSet {
		line += fmt.Sprintf(" drain.backoff_base=%s", strconv.Quote(s.Drain.BackoffBase))
	}
	if s.Drain.FallbackPollSet {
		line += fmt.Sprintf(" drain.fallback_poll=%s", strconv.Quote(s.Drain.FallbackPoll))
	}
	if s.Drain.NoNotifySet {
		line += fmt.Sprintf(" drain.no_notify=%t", s.Drain.NoNotify)
	}
	if s.Drain.PolicySet {
		line += fmt.Sprintf(" drain.policy=%s", strconv.Quote(s.Drain.Policy))
	}
	if s.Drain.Profile != "" {
		line += fmt.Sprintf(" drain.profile=%s", strconv.Quote(s.Drain.Profile))
	}
	if s.Drain.ProfilesSet {
		encodedProfiles, _ := json.Marshal(s.Drain.Profiles)
		line += " drain.profiles=" + string(encodedProfiles)
	}
	if s.Drain.RunBinSet {
		line += fmt.Sprintf(" drain.run_bin=%s", strconv.Quote(s.Drain.RunBin))
	}
	if s.Drain.ItemBinSet {
		line += fmt.Sprintf(" drain.item_bin=%s", strconv.Quote(s.Drain.ItemBin))
	}
	if s.Drain.BriefTemplateSet {
		line += fmt.Sprintf(" drain.brief_template=%s", strconv.Quote(s.Drain.BriefTemplate))
	}
	if s.Drain.GateSet {
		line += fmt.Sprintf(" drain.gate=%s", strconv.Quote(s.Drain.Gate))
	}
	if s.Drain.LandSet {
		line += fmt.Sprintf(" drain.land=%s", strconv.Quote(s.Drain.Land))
	}
	if s.Drain.VerdictSet {
		line += fmt.Sprintf(" drain.verdict=%s", strconv.Quote(s.Drain.Verdict))
	}
	if s.Drain.ReplaySet {
		line += fmt.Sprintf(" drain.replay=%s", strconv.Quote(s.Drain.Replay))
	}
	if s.Drain.HeavyGateSet {
		line += fmt.Sprintf(" drain.heavy_gate=%s", strconv.Quote(s.Drain.HeavyGate))
	}
	if s.Drain.RollingSet {
		line += fmt.Sprintf(" drain.rolling=%s", strconv.Quote(s.Drain.Rolling))
	}
	if s.Drain.FixerGateSet {
		line += fmt.Sprintf(" drain.fixer_gate=%s", strconv.Quote(s.Drain.FixerGate))
	}
	if s.Drain.QuestionsCmdSet {
		line += fmt.Sprintf(" drain.questions_cmd=%s", strconv.Quote(s.Drain.QuestionsCmd))
	}
	if s.Drain.QuestionsIndexSet {
		line += fmt.Sprintf(" drain.questions_index=%s", strconv.Quote(s.Drain.QuestionsIndex))
	}
	if s.WorkflowDefaults != nil {
		defaults, _ := json.Marshal(s.WorkflowDefaults)
		line += " workflows.default=" + string(defaults)
	}
	return line
}

// ReadChecksDir reads only <root>/.tillr.toml's [gate] checks_dir and returns it, or
// DefaultChecksDir when the file, [gate] or checks_dir is absent. It is the reader for
// a candidate's settings and for check declarations: every other table and key is
// ignored, so a candidate that adds a key this build does not know is still settled and
// gated. Unreadable or malformed files, a non-table [gate] and an invalid checks_dir
// refuse; none falls back to DefaultChecksDir.
func ReadChecksDir(root string) (string, error) {
	path := filepath.Join(root, ".tillr.toml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return DefaultChecksDir, nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: expected a readable settings file: %w", path, err)
	}
	return ParseChecksDir(path, raw)
}

// ParseChecksDir is ReadChecksDir over bytes already read; path only names the file in
// refusals.
func ParseChecksDir(path string, raw []byte) (string, error) {
	var tables map[string]any
	if err := toml.Unmarshal(raw, &tables); err != nil {
		return "", fmt.Errorf("%s: expected valid TOML: %w", path, err)
	}
	gate, present := tables["gate"]
	if !present {
		return DefaultChecksDir, nil
	}
	values, ok := gate.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s: gate: expected a table", path)
	}
	value, present := values["checks_dir"]
	if !present {
		return DefaultChecksDir, nil
	}
	str, ok := value.(string)
	if ok {
		if clean, valid := cleanChecksDir(str); valid {
			return clean, nil
		}
	}
	return "", fmt.Errorf("%s: gate.checks_dir: expected a clean repository-relative subdirectory path outside .git", path)
}
