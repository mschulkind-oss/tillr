package drain

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A rendered brief is read by a worker that nobody answers, so everything in it is
// addressed to that worker. Measured on 2026-11-18 by replaying a captured request: the template's
// author notes, an HTML comment describing the template itself, were the first thing in
// every brief, and removing them alone turned a Sol 6.2 stop-point replay from 0 of 8
// continuing to 8 of 8. The brief also has to say that the run is unattended and what
// its goal is, because a run that reports instead of working is read as finished.
func TestARenderedBriefIsAddressedToAnUnattendedWorker(t *testing.T) {
	recorded := refusalStagedItem("item-a")
	spec := refusalSpec(recorded.ItemID)
	values, err := briefValuesFor(recorded, spec, "/wt", "lane worktree", strings.Repeat("0", 40), "/lane/durable", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	template, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderBrief(template, recorded.ItemID, values)
	if err != nil {
		t.Fatalf("rendering the tracked template: %v", err)
	}
	if strings.Contains(rendered, "<!--") || strings.Contains(rendered, "-->") {
		t.Fatalf("the rendered brief still carries the template's own comments:\n%s", rendered)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 || !strings.HasPrefix(lines[2], "You are running unattended: nobody reads or answers your messages") {
		t.Fatalf("the brief does not open by saying the run is unattended:\n%s", strings.Join(lines[:min(len(lines), 6)], "\n"))
	}
	shape := rendered[strings.Index(rendered, "## Report shape"):]
	for _, want := range []string{
		"The goal of this run is one commit in the target",
		"Ending without the commit and without a named stop condition is not a valid\nending",
		"Unfinished work is not a failure",
	} {
		if !strings.Contains(shape, want) {
			t.Fatalf("Report shape does not state %q:\n%s", want, shape)
		}
	}
}
