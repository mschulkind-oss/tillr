package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestGateChecksDirDefaultsWhenAbsent(t *testing.T) {
	dir := root(t)
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gate.Set || got.Gate.ChecksDir != "" {
		t.Fatalf("expected unset Gate, got %+v", got.Gate)
	}
	if got.ChecksDir() != settings.DefaultChecksDir {
		t.Fatalf("ChecksDir() = %q, want %q", got.ChecksDir(), settings.DefaultChecksDir)
	}
	if !strings.Contains(got.StartupLine(), `gate.checks_dir="tools/gate/checks"`) {
		t.Fatalf("StartupLine() = %q, missing default gate.checks_dir", got.StartupLine())
	}

	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[lanes]\nexpress = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Gate.Set || got.ChecksDir() != settings.DefaultChecksDir {
		t.Fatalf("absent [gate] got %+v, ChecksDir() = %q", got.Gate, got.ChecksDir())
	}
}

func TestGateChecksDirValidAndTrailingSlashNormalized(t *testing.T) {
	dir := root(t)
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"custom/checks/\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Gate.Set || got.Gate.ChecksDir != "custom/checks" || got.ChecksDir() != "custom/checks" {
		t.Fatalf("got %+v, ChecksDir() = %q", got.Gate, got.ChecksDir())
	}
	if !strings.Contains(got.StartupLine(), `gate.checks_dir="custom/checks"`) {
		t.Fatalf("StartupLine() = %q, missing custom gate.checks_dir", got.StartupLine())
	}
	if _, err := settings.ReadWithoutLanding(dir, "research"); err != nil {
		t.Fatalf("ReadWithoutLanding should allow [gate], got %v", err)
	}
}

func TestGateChecksDirRefusesInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		toml string
		key  string
	}{
		{"unknown key", "[gate]\nunknown = \"x\"\n", "gate.unknown"},
		{"wrong type", "[gate]\nchecks_dir = 42\n", "gate.checks_dir"},
		{"empty", "[gate]\nchecks_dir = \"\"\n", "gate.checks_dir"},
		{"dot", "[gate]\nchecks_dir = \".\"\n", "gate.checks_dir"},
		{"dotdot", "[gate]\nchecks_dir = \"..\"\n", "gate.checks_dir"},
		{"parent escape", "[gate]\nchecks_dir = \"../x\"\n", "gate.checks_dir"},
		{"absolute", "[gate]\nchecks_dir = \"/abs\"\n", "gate.checks_dir"},
		{"dotgit", "[gate]\nchecks_dir = \".git\"\n", "gate.checks_dir"},
		{"under dotgit", "[gate]\nchecks_dir = \".git/hooks\"\n", "gate.checks_dir"},
		{"double slash", "[gate]\nchecks_dir = \"a//b\"\n", "gate.checks_dir"},
		{"backslash", "[gate]\nchecks_dir = \"a\\\\b\"\n", "gate.checks_dir"},
		{"star wildcard", "[gate]\nchecks_dir = \"a/*\"\n", "gate.checks_dir"},
		{"question wildcard", "[gate]\nchecks_dir = \"a/?\"\n", "gate.checks_dir"},
		{"double trailing slash", "[gate]\nchecks_dir = \"a/b//\"\n", "gate.checks_dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := root(t)
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(tc.toml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := settings.Read(dir)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), ".tillr.toml") || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error %q should name .tillr.toml and %s", err.Error(), tc.key)
			}
		})
	}
}

func TestGateDisabledTestsDefaultsAndReads(t *testing.T) {
	dir := root(t)
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisabledTestsPath() != settings.DefaultDisabledTests || settings.DefaultDisabledTests != "tillr-disabled-tests.json" {
		t.Fatalf("DisabledTestsPath() = %q, want the root default", got.DisabledTestsPath())
	}
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[gate]\ndisabled_tests = \"tools/gate/disabled-tests.json\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err = settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisabledTestsPath() != "tools/gate/disabled-tests.json" || got.ChecksDir() != settings.DefaultChecksDir {
		t.Fatalf("got path %q checks dir %q", got.DisabledTestsPath(), got.ChecksDir())
	}
}

func TestGateDisabledTestsRefusesInvalidPath(t *testing.T) {
	for name, toml := range map[string]string{
		"wrong type": "[gate]\ndisabled_tests = 3\n",
		"empty":      "[gate]\ndisabled_tests = \"\"\n",
		"absolute":   "[gate]\ndisabled_tests = \"/abs.json\"\n",
		"escape":     "[gate]\ndisabled_tests = \"../x.json\"\n",
		"dotgit":     "[gate]\ndisabled_tests = \".git/x.json\"\n",
		"directory":  "[gate]\ndisabled_tests = \"a/\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := root(t)
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(toml), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := settings.Read(dir)
			if err == nil || !strings.Contains(err.Error(), "gate.disabled_tests") {
				t.Fatalf("error %v should name gate.disabled_tests", err)
			}
		})
	}
}

func TestReadChecksDirIgnoresUnknownKeysAndTables(t *testing.T) {
	cases := map[string]string{
		"": settings.DefaultChecksDir,
		"[gate]\ndisabled_tests = [\"pkg:TestX\"]\n":             settings.DefaultChecksDir,
		"[gate]\nchecks_dir = \"custom/checks/\"\nnew_key = 1\n": "custom/checks",
		"[future]\nx = 1\n[[things]]\ny = 2\n":                   settings.DefaultChecksDir,
		"[gate]\nchecks_dir = \"c\"\n[future]\nx = 1\n":          "c",
	}
	for body, want := range cases {
		dir := root(t)
		if body != "" {
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
		got, err := settings.ReadChecksDir(dir)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, %v; want %q", body, got, err, want)
		}
		if body != "" && strings.Contains(body, "[future]") {
			if _, err := settings.Read(dir); err == nil {
				t.Fatalf("settings.Read must stay strict for %q", body)
			}
		}
	}
}

func TestReadChecksDirRefusesWithoutFallback(t *testing.T) {
	for body, want := range map[string]string{
		"[gate\n":                         "expected valid TOML",
		"gate = 3\n":                      "gate: expected a table",
		"[gate]\nchecks_dir = 3\n":        "gate.checks_dir",
		"[gate]\nchecks_dir = \"../x\"\n": "gate.checks_dir",
	} {
		dir := root(t)
		if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := settings.ReadChecksDir(dir)
		if err == nil || got != "" || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), ".tillr.toml") {
			t.Fatalf("%q: got %q, %v; want refusal containing %q", body, got, err, want)
		}
	}
}

func TestParseChecksDirNamesGivenPath(t *testing.T) {
	got, err := settings.ParseChecksDir("rev:.tillr.toml", []byte("[gate]\nchecks_dir = \"a/b\"\nfuture = true\n"))
	if err != nil || got != "a/b" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := settings.ParseChecksDir("rev:.tillr.toml", []byte("[gate\n")); err == nil || !strings.Contains(err.Error(), "rev:.tillr.toml") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadStaysStrictOnMistypedGateKey(t *testing.T) {
	dir := root(t)
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[gate]\nmistyped_key = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Read(dir); err == nil || !strings.Contains(err.Error(), "gate.mistyped_key") {
		t.Fatalf("err = %v", err)
	}
}
