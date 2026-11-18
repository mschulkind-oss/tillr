package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestSyncRoadmapsSettings(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"", "[]"},
		{"[sync]\nroadmaps = []", "[]"},
		{`[sync]
roadmaps = ["docs/b/roadmap.md", "docs/a/roadmap.md"]`, `["docs/b/roadmap.md","docs/a/roadmap.md"]`},
	} {
		t.Run(tc.want+tc.body, func(t *testing.T) {
			dir := root(t)
			if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(tc.body), 0644); err != nil {
				t.Fatal(err)
			}
			for _, read := range []func(string) (settings.Settings, error){settings.Read, func(root string) (settings.Settings, error) { return settings.ReadWithoutLanding(root, "findings") }} {
				got, err := read(dir)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(got.StartupLine(), "sync.roadmaps="+tc.want) {
					t.Fatal(got.StartupLine())
				}
			}
		})
	}
}

func TestSyncRoadmapsRefusals(t *testing.T) {
	for _, value := range []string{`"docs/roadmap.md"`, "true", "1", `["docs/roadmap.md", 2]`, `[false]`, "{}"} {
		t.Run(value, func(t *testing.T) {
			dir := root(t)
			path := filepath.Join(dir, ".tillr.toml")
			if err := os.WriteFile(path, []byte("[sync]\nroadmaps = "+value), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := settings.Read(dir)
			if err == nil {
				t.Fatal("invalid roadmaps accepted")
			}
			for _, want := range []string{path, "sync.roadmaps", "array of strings"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%v does not contain %q", err, want)
				}
			}
		})
	}
}
