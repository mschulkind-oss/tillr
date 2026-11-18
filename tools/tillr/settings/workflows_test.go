package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestWorkflowDefaultsSettings(t *testing.T) {
	for _, tc := range []struct{ body, key string }{
		{"[workflows]\nother = {}", "workflows.other"},
		{"[workflows.default]\n'' = 'build@v1'", "workflows.default."},
		{"[workflows.default]\nbuild = ''", "workflows.default.build"},
		{"[workflows.default]\nbuild = '  '", "workflows.default.build"},
		{"[workflows.default]\nbuild = 1", "workflows.default.build"},
		{"[workflows]\ndefault = 'build@v1'", "workflows.default"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			dir := root(t)
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := settings.Read(dir); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("expected %s refusal, got %v", tc.key, err)
			}
		})
	}
	dir := root(t)
	body := `[roll]
paths = ['tools/']
command = 'build'
[push]
after_landing = true
[comments]
vantage_url = 'http://localhost:8919'
paths = ['docs/']
[workflows.default]
build = 'build@v1'
design = 'design@v2'
`
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowDefaults["build"] != "build@v1" || got.WorkflowDefaults["design"] != "design@v2" || got.Comments == nil || got.Push.What != "tip" || got.Roll.Command != "build" {
		t.Fatalf("settings: %+v", got)
	}
	if !strings.Contains(got.StartupLine(), "workflows.default") {
		t.Fatal(got.StartupLine())
	}
}
