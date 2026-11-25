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
		{"[roll]\ncommand = 'run'", "roll.command", "roll.paths"},
		{"[roll]\ncommand = 'run'\npaths = []", "roll.command", "roll.paths"},
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
