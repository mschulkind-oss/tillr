package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/settings"
)

func TestBriefTemplateSetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".tillr.toml")
	if err := os.WriteFile(path, []byte("[brief]\ntemplate = \"config/tillr/brief-template.md\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := settings.Read(dir)
	if err != nil {
		t.Fatalf("Read valid [brief]: %v", err)
	}
	if got.Brief.Template != "config/tillr/brief-template.md" {
		t.Fatalf("Brief.Template = %q, want %q", got.Brief.Template, "config/tillr/brief-template.md")
	}
	if !strings.Contains(got.StartupLine(), `brief.template="config/tillr/brief-template.md"`) {
		t.Fatalf("StartupLine missing brief.template: %s", got.StartupLine())
	}

	// Non-landing definitions also allow [brief].
	gotNoLand, err := settings.ReadWithoutLanding(dir, "recorded-verdict")
	if err != nil {
		t.Fatalf("ReadWithoutLanding with [brief]: %v", err)
	}
	if gotNoLand.Brief.Template != "config/tillr/brief-template.md" {
		t.Fatalf("ReadWithoutLanding Brief.Template = %q", gotNoLand.Brief.Template)
	}

	// Unset omits brief.template from StartupLine.
	emptyDir := t.TempDir()
	defaults, err := settings.Read(emptyDir)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Brief.Template != "" || strings.Contains(defaults.StartupLine(), "brief.template=") {
		t.Fatalf("unset brief should be empty and omitted from StartupLine: %+v / %s", defaults.Brief, defaults.StartupLine())
	}
}

func TestBriefTemplateSettingRefusals(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"[brief]\ntemplate = ''\n", "brief.template"},
		{"[brief]\ntemplate = '/abs/brief.md'\n", "brief.template"},
		{"[brief]\ntemplate = '../escape.md'\n", "brief.template"},
		{"[brief]\ntemplate = 'config/tillr/'\n", "brief.template"},
		{"[brief]\ntemplate = '.git/brief.md'\n", "brief.template"},
		{"[brief]\ntemplate = 'config/*/brief.md'\n", "brief.template"},
		{"[brief]\ntemplate = 42\n", "brief.template"},
		{"[brief]\nother = 'x'\n", "brief.other"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := settings.Read(dir); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%q: err = %v, want substring %q", tc.body, err, tc.want)
		}
	}
}
