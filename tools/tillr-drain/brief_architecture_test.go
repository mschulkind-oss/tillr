package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRepoBriefTellsDrainWorkersToRunArchitectureTest pins the paragraph of this
// repository's brief template that tells every seat about TestArchitecture, so a
// candidate under tools/tillr-drain meets its structure rules before the gate names them.
func TestRepoBriefTellsDrainWorkersToRunArchitectureTest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "tillr", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(raw)
	const command = "`go -C tools/tillr-drain test . -run '^TestArchitecture$'`"
	for _, want := range []string{
		"**The drain has an architecture test.**",
		"- **plan:** a piece that writes under `tools/tillr-drain` names these rules in its prompt.",
		"- **build:** run " + command + " before reporting\n  done",
		"- **verify:** run the same command as one of your own checks.",
		"no new domain `.go`\nfile in `drain/`",
		"forwarding wrappers of at most 10\nlines",
		"`struct Loop` stays under its field and mutex ceilings",
		"Never raise or weaken a ceiling or rule in that test",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("config/tillr/brief-template.md omits %q", want)
		}
	}
	// The paragraph belongs in the scope section every seat reads, ahead of the seat list.
	if strings.Index(brief, "**The drain has an architecture test.**") > strings.Index(brief, "## Seats") {
		t.Errorf("architecture-test paragraph must precede ## Seats")
	}
}
