package settings_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func root(t *testing.T) string {
	t.Helper()
	base := os.Getenv("YOLO_DURABLE_DIR")
	if base == "" {
		base = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "settings-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestDefaultsAndTOML(t *testing.T) {
	dir := root(t)
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Settings{
		Trunk: settings.Trunk{Branch: settings.DefaultTrunkBranch},
		Roll:  settings.Roll{PauseTimeoutSeconds: 120}, Push: settings.Push{What: "none"},
		CandidateSize: settings.CandidateSizeLimits{FileLimit: 27, AddedLineLimit: 1200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	specimen := `[roll]
paths = ["tools/tillr*/", "config/tillr/"]
command = "just tillr-roll --no-request"
pause_timeout_seconds = 45
[push]
what = "green"
remote = "origin"
ref = "refs/heads/main"
[candidate_size]
file_limit = 42
added_line_limit = 900
`
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(specimen), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want = settings.Settings{
		Trunk:         settings.Trunk{Branch: settings.DefaultTrunkBranch},
		Roll:          settings.Roll{Paths: []string{"tools/tillr*/", "config/tillr/"}, Command: "just tillr-roll --no-request", PauseTimeoutSeconds: 45},
		Push:          settings.Push{What: "green", Remote: "origin", Ref: "refs/heads/main"},
		CandidateSize: settings.CandidateSizeLimits{FileLimit: 42, AddedLineLimit: 900},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[candidate_size]\nfile_limit = 0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Read(dir)
	if want := (settings.CandidateSizeLimits{FileLimit: 0, AddedLineLimit: 1200}); err != nil || got.CandidateSize != want {
		t.Fatalf("partial override = %+v, %v; want %+v", got.CandidateSize, err, want)
	}
}

func TestRefusalsNameFileKeyAndExpectation(t *testing.T) {
	cases := []struct{ body, key, want string }{
		{"[rol]\n", "rol", "table"},
		{"[order]\nrecord = 'docs/order.txt'", "order", "known table"},
		{"[roll]\ncommmand = 'x'", "roll.commmand", "command"},
		{"[push]\nafter_landng = true", "push.after_landng", "after_landing"},
		{"after_landing = true", "after_landing", "table"},
		{"roll = 'x'", "roll", "table"},
		{"[roll]\ncommand = true", "roll.command", "string"},
		{"[roll]\npaths = 'x'", "roll.paths", "array of strings"},
		{"[roll]\npaths = ['x', 1]", "roll.paths", "array of strings"},
		{"[roll]\npause_timeout_seconds = 1.5", "roll.pause_timeout_seconds", "positive integer"},
		{"[roll]\npause_timeout_seconds = 0", "roll.pause_timeout_seconds", "positive integer"},
		{"[roll]\npause_timeout_seconds = -1", "roll.pause_timeout_seconds", "positive integer"},
		{"[push]\nafter_landing = 'true'", "push.after_landing", "boolean"},
		{"[push]\nwhat = 'main'", "push.what", "green"},
		{"[push]\nwhat = true", "push.what", "green"},
		{"[push]\nwhat = 'green'\nafter_landing = true", "push.after_landing", "push.what"},
		{"[push]\nremote = false", "push.remote", "string"},
		{"[push]\nref = 1", "push.ref", "string"},
		{"[roll.extra]\nx = 1", "roll.extra", "command"},
		{"[candidate_size]\nfile_limit = -1", "candidate_size.file_limit", "nonnegative integer"},
		{"[candidate_size]\nadded_line_limit = '1200'", "candidate_size.added_line_limit", "nonnegative integer"},
		{"[trunk]\nother = 'dev'", "trunk.other", "branch"},
		{"[trunk]\nbranch = 1", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = ''", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'refs/heads/dev'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev..other'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev@{0}'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = '-dev'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = '/dev'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev/'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev//v2'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev.lock'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = '@'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev branch'", "trunk.branch", "valid short branch name"},
		{"[trunk]\nbranch = 'dev~1'", "trunk.branch", "valid short branch name"},
		{"[drain]\nother = 'x'", "drain.other", "profile"},
		{"[drain]\nprofile = 1", "drain.profile", "valid profile name"},
		{"[drain]\nprofile = ''", "drain.profile", "valid profile name"},
		{"[drain]\nprofile = '-bad'", "drain.profile", "valid profile name"},
		{"[drain]\nprofile = '_bad'", "drain.profile", "valid profile name"},
		{"[drain]\nprofile = 'Bad'", "drain.profile", "valid profile name"},
		{"[drain]\nprofile = 'bad.name'", "drain.profile", "valid profile name"},
		{"[drain]\nprofile = '../bad'", "drain.profile", "valid profile name"},
	}
	for _, tc := range cases {
		t.Run(tc.key+tc.body, func(t *testing.T) {
			dir := root(t)
			path := filepath.Join(dir, ".tillr.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := settings.Read(dir)
			if err == nil {
				t.Fatal("invalid settings accepted")
			}
			for _, word := range []string{path, tc.key, tc.want, "expected"} {
				if !strings.Contains(err.Error(), word) {
					t.Errorf("%q does not contain %q", err, word)
				}
			}
		})
	}
}

func TestMalformedOrUnreadableFileIsNotDefaults(t *testing.T) {
	dir := root(t)
	path := filepath.Join(dir, ".tillr.toml")
	for _, body := range []string{"[roll", "[push]\nafter_landing = true\nafter_landing = false"} {
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := settings.Read(dir); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("malformed file: %v", err)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Read(dir); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("unreadable file: %v", err)
	}
}

func TestPushWhatSpellings(t *testing.T) {
	for body, want := range map[string]string{
		"":                              "none",
		"[push]\nremote = 'o'":          "none",
		"[push]\nafter_landing = false": "none",
		"[push]\nafter_landing = true":  "tip",
		"[push]\nwhat = 'tip'":          "tip",
		"[push]\nwhat = 'green'":        "green",
		"[push]\nwhat = 'none'":         "none",
	} {
		dir := root(t)
		if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := settings.Read(dir)
		if err != nil || got.Push.What != want || !strings.Contains(got.StartupLine(), "push.what="+want+" ") {
			t.Fatalf("%q: what = %q, %v; line %s", body, got.Push.What, err, got.StartupLine())
		}
	}
}

func TestTrunkBranchSetting(t *testing.T) {
	dir := root(t)
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trunk.Set || got.TrunkBranch() != "main" || got.TrunkRef() != "refs/heads/main" {
		t.Fatalf("unset trunk = %+v", got.Trunk)
	}
	if !strings.Contains(got.StartupLine(), `trunk.branch="main" `) {
		t.Fatalf("startup line missing default trunk.branch: %s", got.StartupLine())
	}

	for _, branch := range []string{"dev", "release/v2"} {
		if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[trunk]\nbranch = '"+branch+"'\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err = settings.Read(dir)
		if err != nil {
			t.Fatalf("%q: %v", branch, err)
		}
		if !got.Trunk.Set || got.TrunkBranch() != branch || got.TrunkRef() != "refs/heads/"+branch {
			t.Fatalf("%q: got %+v", branch, got.Trunk)
		}
		if !strings.Contains(got.StartupLine(), `trunk.branch="`+branch+`" `) {
			t.Fatalf("%q: startup line = %s", branch, got.StartupLine())
		}
		if _, err := settings.ReadWithoutLanding(dir, "findings"); err == nil || !strings.Contains(err.Error(), "trunk.branch") {
			t.Fatalf("%q: ReadWithoutLanding = %v, want trunk.branch refusal", branch, err)
		}
	}
}

func TestRollCommandAllowedWithNoPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "paths omitted", body: "[roll]\ncommand = 'run'\n"},
		{name: "paths empty", body: "[roll]\ncommand = 'run'\npaths = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := root(t)
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := settings.Read(dir)
			if err != nil {
				t.Fatalf("settings.Read(%q): %v", tc.body, err)
			}
			if got.Roll.Command != "run" || len(got.Roll.Paths) != 0 {
				t.Fatalf("roll = %+v, want command=\"run\" and empty paths", got.Roll)
			}
			line := got.StartupLine()
			for _, want := range []string{`roll.command="run"`, `roll.paths=[]`} {
				if !strings.Contains(line, want) {
					t.Fatalf("StartupLine missing %q: %s", want, line)
				}
			}
		})
	}
}

func TestDrainProfileSettingsAndStartupLine(t *testing.T) {
	dir := root(t)
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[drain]\nprofile = 'copilot-gemini_1'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatalf("settings.Read: %v", err)
	}
	if got.Drain.Profile != "copilot-gemini_1" {
		t.Fatalf("Drain.Profile = %q, want %q", got.Drain.Profile, "copilot-gemini_1")
	}
	line := got.StartupLine()
	if !strings.Contains(line, `drain.profile="copilot-gemini_1"`) {
		t.Fatalf("StartupLine missing drain.profile: %s", line)
	}

	dirUnset := root(t)
	unset, err := settings.Read(dirUnset)
	if err != nil {
		t.Fatalf("settings.Read(unset): %v", err)
	}
	if strings.Contains(unset.StartupLine(), "drain.profile") {
		t.Fatalf("StartupLine unexpectedly included drain.profile when unset: %s", unset.StartupLine())
	}
}

func TestDrainAllStartDefaultSettingsAndConflicts(t *testing.T) {
	dir := root(t)
	body := `[drain]
exit_when_drained = true
store = ".tillr/custom.db"
lanes = 6
worktree_count = 12
worktree_disk_budget = 8589934592
max_concurrent_triage = 4
lane_root = ".tillr/custom-lanes/"
backoff_base = "2s"
fallback_poll = "15s"
no_notify = true
policy = "config/tillr/backend-policy.json"
run_bin = "custom-run"
item_bin = "custom-item"
brief_template = "config/tillr/brief.md"
gate = "my-gate"
land = "my-land"
verdict = "my-verdict"
replay = ""
heavy_gate = "my-heavy-gate"
rolling = ""
fixer_gate = "my-fixer-gate"
questions_cmd = "uvx custom"
questions_index = ".tillr/q.json"
`
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatalf("settings.Read: %v", err)
	}
	if !got.Drain.ExitWhenDrainedSet || !got.Drain.ExitWhenDrained ||
		got.Drain.Store != ".tillr/custom.db" ||
		!got.Drain.LanesSet || got.Drain.Lanes != 6 ||
		got.Drain.LaneRoot != ".tillr/custom-lanes" ||
		got.Drain.BackoffBase != "2s" ||
		got.Drain.FallbackPoll != "15s" ||
		!got.Drain.NoNotify ||
		got.Drain.Policy != "config/tillr/backend-policy.json" ||
		!got.Drain.ReplaySet || got.Drain.Replay != "" ||
		!got.Drain.RollingSet || got.Drain.Rolling != "" {
		t.Fatalf("unexpected Drain settings: %+v", got.Drain)
	}
	line := got.StartupLine()
	for _, want := range []string{
		`drain.exit_when_drained=true`,
		`drain.lanes=6`,
		`drain.policy="config/tillr/backend-policy.json"`,
		`drain.lane_root=".tillr/custom-lanes"`,
		`drain.replay=""`,
		`drain.rolling=""`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("StartupLine missing %q: %s", want, line)
		}
	}

	for _, tc := range []struct {
		name string
		toml string
		want string
	}{
		{"absolute policy", "[drain]\npolicy = '/etc/policy.json'\n", "drain.policy"},
		{"traversal policy", "[drain]\npolicy = '../policy.json'\n", "drain.policy"},
		{"git store", "[drain]\nstore = '.git/state.db'\n", "drain.store"},
		{"negative lanes", "[drain]\nlanes = -1\n", "drain.lanes"},
		{"bad backoff", "[drain]\nbackoff_base = 'soon'\n", "drain.backoff_base"},
		{"negative backoff", "[drain]\nbackoff_base = '-1s'\n", "drain.backoff_base"},
		{"empty run_bin", "[drain]\nrun_bin = ' '\n", "drain.run_bin"},
		{"max_agents vs lanes", "[drain]\nmax_agents = 3\nlanes = 4\n", "drain.max_agents"},
		{"worktrees vs drain", "[worktrees]\nmax_count = 4\n[drain]\nworktree_count = 5\n", "worktrees.max_count"},
		{"disk budget vs drain", "[worktrees]\ndisk_budget_bytes = 100\n[drain]\nworktree_disk_budget = 200\n", "worktrees.disk_budget_bytes"},
		{"brief vs drain", "[brief]\ntemplate = 'a.md'\n[drain]\nbrief_template = 'b.md'\n", "brief.template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := root(t)
			if err := os.WriteFile(filepath.Join(d, ".tillr.toml"), []byte(tc.toml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := settings.Read(d)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("settings.Read = %v, want error naming %q", err, tc.want)
			}
		})
	}
}
