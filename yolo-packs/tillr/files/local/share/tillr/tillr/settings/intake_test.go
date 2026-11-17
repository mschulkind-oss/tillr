package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIntakeSettings(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{"[intake]\ndir='docs/ideas'\npaths=['docs/design/', 'docs/roadmap.md']", true},
		{"[intake]\ndir='../escape'\npaths=['docs/design/']", false},
		{"[intake]\ndir='/absolute'", false},
		{"[intake]\npaths=['*']", false},
		{"[intake]\npaths=['../escape']", false},
		{"[intake]\npaths=42", false},
	} {
		if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(tc.raw), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := Read(root)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if tc.valid && (s.Intake.Dir != "docs/ideas" || !reflect.DeepEqual(s.Intake.Paths, []string{"docs/design/", "docs/roadmap.md"})) {
			t.Fatal(s.Intake)
		}
	}
}
