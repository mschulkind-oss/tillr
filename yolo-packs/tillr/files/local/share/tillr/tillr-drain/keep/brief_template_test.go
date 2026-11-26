package keep

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoopCommandPassesBriefTemplateFromSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[brief]\ntemplate = \"config/tillr/brief-template.md\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := LoopCommand(dir, []string{"--lanes", "2"})
	wantPath := filepath.Join(dir, "config", "tillr", "brief-template.md")
	idx := slices.Index(got, "--brief-template")
	if idx < 0 || idx+1 >= len(got) || got[idx+1] != wantPath {
		t.Fatalf("LoopCommand with [brief] template = %q, want --brief-template %q", got, wantPath)
	}

	// An explicit --brief-template in extra wins without duplicating the flag.
	explicit := LoopCommand(dir, []string{"--brief-template", "/custom/brief.md", "--lanes", "2"})
	count := 0
	for _, arg := range explicit {
		if arg == "--brief-template" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("explicit --brief-template duplicated in %q", explicit)
	}
	idx = slices.Index(explicit, "--brief-template")
	if idx < 0 || idx+1 >= len(explicit) || explicit[idx+1] != "/custom/brief.md" {
		t.Fatalf("explicit --brief-template overridden: %q", explicit)
	}

	// Without [brief] template, LoopCommand omits --brief-template so loop uses the installed default.
	plainDir := t.TempDir()
	plain := LoopCommand(plainDir, []string{"--lanes", "1"})
	if slices.Contains(plain, "--brief-template") {
		t.Fatalf("LoopCommand without [brief] template passed --brief-template: %q", plain)
	}
}
