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
	want := settings.Settings{Roll: settings.Roll{PauseTimeoutSeconds: 120}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
	specimen := `[roll]
paths = ["tools/tillr*/", "config/tillr/"]
command = "just tillr-roll --no-request"
pause_timeout_seconds = 45
[push]
after_landing = true
remote = "origin"
ref = "refs/heads/main"
`
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(specimen), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	want = settings.Settings{
		Roll: settings.Roll{Paths: []string{"tools/tillr*/", "config/tillr/"}, Command: "just tillr-roll --no-request", PauseTimeoutSeconds: 45},
		Push: settings.Push{AfterLanding: true, Remote: "origin", Ref: "refs/heads/main"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestRefusalsNameFileKeyAndExpectation(t *testing.T) {
	cases := []struct{ body, key, want string }{
		{"[rol]\n", "rol", "table"},
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
		{"[push]\nremote = false", "push.remote", "string"},
		{"[push]\nref = 1", "push.ref", "string"},
		{"[roll]\ncommand = 'run'", "roll.command", "roll.paths"},
		{"[roll]\ncommand = 'run'\npaths = []", "roll.command", "roll.paths"},
		{"[roll.extra]\nx = 1", "roll.extra", "command"},
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
