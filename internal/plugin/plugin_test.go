package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParsePluginName(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"tillr-plugin-foo", "foo"},
		{"tillr-plugin-bar-baz", "bar-baz"},
		{"tillr-plugin-", ""},
		{"tillr-plugin", ""},
		{"not-a-plugin", ""},
		{"tillr-foo", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ParsePluginName(tt.filename)
			if got != tt.want {
				t.Errorf("ParsePluginName(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}

func TestParsePluginNameWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only test")
	}
	got := ParsePluginName("tillr-plugin-foo.exe")
	if got != "foo" {
		t.Errorf("ParsePluginName(%q) = %q, want %q", "tillr-plugin-foo.exe", got, "foo")
	}
}

func TestDiscoverInPath(t *testing.T) {
	// Create a temporary directory with mock plugin executables.
	tmpDir := t.TempDir()

	// Create a valid plugin executable.
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-test")
	if err := os.WriteFile(pluginPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create another valid plugin.
	plugin2Path := filepath.Join(tmpDir, "tillr-plugin-hello")
	if err := os.WriteFile(plugin2Path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a non-plugin executable — should be ignored.
	nonPlugin := filepath.Join(tmpDir, "some-other-tool")
	if err := os.WriteFile(nonPlugin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a file with the right name but not executable — should be ignored.
	noExec := filepath.Join(tmpDir, "tillr-plugin-noexec")
	if err := os.WriteFile(noExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a directory with plugin name — should be ignored.
	dirPlugin := filepath.Join(tmpDir, "tillr-plugin-isdir")
	if err := os.Mkdir(dirPlugin, 0o755); err != nil {
		t.Fatal(err)
	}

	plugins, err := DiscoverInPath(tmpDir)
	if err != nil {
		t.Fatalf("DiscoverInPath() error = %v", err)
	}

	if len(plugins) != 2 {
		t.Fatalf("DiscoverInPath() returned %d plugins, want 2", len(plugins))
	}

	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Name] = true
		if p.Path == "" {
			t.Errorf("plugin %q has empty path", p.Name)
		}
	}

	if !names["test"] {
		t.Error("expected plugin 'test' to be discovered")
	}
	if !names["hello"] {
		t.Error("expected plugin 'hello' to be discovered")
	}
}

func TestDiscoverInPathEmpty(t *testing.T) {
	plugins, err := DiscoverInPath("")
	if err != nil {
		t.Fatalf("DiscoverInPath('') error = %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("DiscoverInPath('') returned %d plugins, want 0", len(plugins))
	}
}

func TestDiscoverInPathMultipleDirs(t *testing.T) {
	// Create two directories, each with a plugin.
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	p1 := filepath.Join(dir1, "tillr-plugin-alpha")
	if err := os.WriteFile(p1, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	p2 := filepath.Join(dir2, "tillr-plugin-beta")
	if err := os.WriteFile(p2, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Put the same plugin name in dir2 — should be shadowed by dir1.
	p1dup := filepath.Join(dir2, "tillr-plugin-alpha")
	if err := os.WriteFile(p1dup, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	pathEnv := dir1 + string(os.PathListSeparator) + dir2
	plugins, err := DiscoverInPath(pathEnv)
	if err != nil {
		t.Fatalf("DiscoverInPath() error = %v", err)
	}

	if len(plugins) != 2 {
		t.Fatalf("DiscoverInPath() returned %d plugins, want 2", len(plugins))
	}

	// alpha should come from dir1 (first in PATH).
	for _, p := range plugins {
		if p.Name == "alpha" && p.Path != p1 {
			t.Errorf("plugin 'alpha' path = %q, want %q (first PATH entry wins)", p.Path, p1)
		}
	}
}

func TestDiscoverInPathNonexistentDir(t *testing.T) {
	pathEnv := "/nonexistent/dir/that/does/not/exist"
	plugins, err := DiscoverInPath(pathEnv)
	if err != nil {
		t.Fatalf("DiscoverInPath() error = %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("DiscoverInPath() returned %d plugins, want 0", len(plugins))
	}
}

func TestRunWithMockPlugin(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock plugin that echoes back its input.
	script := `#!/bin/sh
cat
`
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-echo")
	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Plugin{Name: "echo", Path: pluginPath}
	input := map[string]string{"hello": "world"}

	output, err := Run(p, "test", input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if string(output) != `{"hello":"world"}` {
		t.Errorf("Run() output = %s, want %s", output, `{"hello":"world"}`)
	}
}

func TestRunWithNilInput(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/sh
echo '{"status":"ok"}'
`
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-noinput")
	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Plugin{Name: "noinput", Path: pluginPath}
	output, err := Run(p, "ping", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if string(output) != `{"status":"ok"}` {
		t.Errorf("Run() output = %s, want %s", output, `{"status":"ok"}`)
	}
}

func TestRunPluginError(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/sh
echo "something went wrong" >&2
exit 1
`
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-fail")
	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Plugin{Name: "fail", Path: pluginPath}
	_, err := Run(p, "boom", nil)
	if err == nil {
		t.Fatal("Run() expected error, got nil")
	}

	pluginErr, ok := err.(*PluginError)
	if !ok {
		t.Fatalf("expected *PluginError, got %T", err)
	}
	if pluginErr.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", pluginErr.ExitCode)
	}
	if pluginErr.Stderr != "something went wrong" {
		t.Errorf("Stderr = %q, want %q", pluginErr.Stderr, "something went wrong")
	}
}

func TestRunPluginInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/sh
echo "not json at all"
`
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-badjson")
	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Plugin{Name: "badjson", Path: pluginPath}
	_, err := Run(p, "test", nil)
	if err == nil {
		t.Fatal("Run() expected error for invalid JSON, got nil")
	}
}

func TestRunPluginEmptyOutput(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/sh
# No output
`
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-silent")
	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Plugin{Name: "silent", Path: pluginPath}
	output, err := Run(p, "noop", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if string(output) != "null" {
		t.Errorf("Run() output = %s, want null", output)
	}
}

func TestQueryInfo(t *testing.T) {
	tmpDir := t.TempDir()

	script := `#!/bin/sh
echo '{"name":"test","version":"1.0.0","description":"A test plugin","commands":["greet","farewell"]}'
`
	pluginPath := filepath.Join(tmpDir, "tillr-plugin-test")
	if err := os.WriteFile(pluginPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := Plugin{Name: "test", Path: pluginPath}
	enriched, err := QueryInfo(p)
	if err != nil {
		t.Fatalf("QueryInfo() error = %v", err)
	}

	if enriched.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", enriched.Version, "1.0.0")
	}
	if enriched.Description != "A test plugin" {
		t.Errorf("Description = %q, want %q", enriched.Description, "A test plugin")
	}
	if len(enriched.Commands) != 2 {
		t.Errorf("Commands = %v, want 2 commands", enriched.Commands)
	}
}

func TestPluginErrorMessage(t *testing.T) {
	e := &PluginError{
		Plugin:   "foo",
		ExitCode: 2,
		Stderr:   "bad input",
	}
	want := `plugin "foo" failed (exit 2): bad input`
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}
