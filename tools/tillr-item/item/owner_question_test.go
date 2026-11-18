package item

import (
	"strings"
	"testing"
	"time"
)

func TestOwnerReportBecomesANumberedQuestionBesideItsItem(t *testing.T) {
	body := "# Requests\n\n### Feature\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/a/\" -->\n- **c1:** Build it.\n\n### Other\n\n<!-- tillr: item id=\"item-b\" paths=\"tools/b/\" -->\n- **c1:** Something else.\n"
	report := "Which delivery should we use?\n\nOptions:\n- A: ship the built-in copy\n- B: download the upstream copy\nNEXT-ACTOR: owner\nRESULT: blocked\n"
	got, id, err := OwnerQuestion(body, "item-a", "run-item-a-a2", report, time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"1. 💬 **" + id + ": Which delivery should we use?**", "<!-- vantage: question id=" + id + " -->", "A: ship the built-in copy", "B: download the upstream copy", "run-item-a-a2", "2026-11-18"} {
		if !strings.Contains(got, text) {
			t.Errorf("missing %q: %s", text, got)
		}
	}
	if strings.Index(got, "vantage: question") > strings.Index(got, "### Other") {
		t.Fatal("question outside item's block")
	}
	again, same, err := OwnerQuestion(got, "item-a", "run-item-a-a2", report, time.Now())
	if err != nil || again != got || same != id {
		t.Fatalf("not idempotent: %q %v", same, err)
	}
}

func TestIndependentOwnerReportsDoNotAllocateTheSameQuestionAnchor(t *testing.T) {
	body := "# Work\n\n## First\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/a/\" -->\n- **c1:** Build a.\n\n## Second\n\n<!-- tillr: item id=\"item-b\" paths=\"tools/b/\" -->\n- **c1:** Build b.\n"
	report := "Which delivery?\nOptions:\n- A: local\n- B: upstream\n"
	_, a, err := OwnerQuestion(body, "item-a", "run-item-a-a1", report, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := OwnerQuestion(body, "item-b", "run-item-b-a1", report, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatalf("two candidates on the same base allocate %s", a)
	}
	// Compacted ledger IDs are still anchors, even without question directives.
	ledger := body + "\n## Decision Ledger\n\n| ID | Ruling |\n| --- | --- |\n| " + a + " | Settled earlier |\n"
	_, next, err := OwnerQuestion(ledger, "item-a", "run-item-a-a2", report, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next == a {
		t.Fatal("reused a compacted question anchor")
	}
}

func TestStaleBaseReportIsNotAnOwnerQuestion(t *testing.T) {
	report := "The base lacks the already-landed prerequisite. Only the operator may restage this lane.\nNEXT-ACTOR: owner\nRESULT: blocked"
	if _, ok := OwnerDecision(report); ok {
		t.Fatal("staging failure misread as a choice")
	}
}

func TestDiagnosticQuestionAndFileListAreNotAnOwnerDecision(t *testing.T) {
	report := "Why is the lane missing the prerequisite?\nFiles inspected:\n- docs/prerequisite.md\n- tools/worker/main.go\nNEXT-ACTOR: owner\nRESULT: blocked"
	if _, ok := OwnerDecision(report); ok {
		t.Fatal("diagnostics and a file list were misread as alternatives")
	}
}

func TestOwnerQuestionRequiresTheItemBlock(t *testing.T) {
	_, _, err := OwnerQuestion("# No block\n", "item-a", "run-a", "Which?\nOptions:\n- A\n- B\n", time.Now())
	if err == nil {
		t.Fatal("invented a home for an undeclared item")
	}
}
