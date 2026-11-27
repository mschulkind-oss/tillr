package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlice1DesignItemsHoldOnOQQM2OQQM3AndQuestionsNeverDispatch(t *testing.T) {
	f, l := passFixture(t)
	indexJSON := `{
		"index": {
			"config": {
				"stages": {"SKETCH": "open", "DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"}
			},
			"documents": [
				{"path": "docs/design/oq-qm2.md", "status": "in-review", "stage": "DESIGN", "questions": []},
				{"path": "docs/design/oq-qm3.md", "status": "accepted", "stage": "DECIDED", "questions": []},
				{"path": "docs/design/upstream.md", "status": "in-review", "stage": "DESIGN", "questions": [{"id": "OQ-UP1", "state": "open", "title": "Upstream open"}]},
				{"path": "docs/design/dep-oq.md", "status": "in-review", "stage": "DESIGN", "dependsOn": [{"raw": "./upstream.md#OQ-UP1", "target": "docs/design/upstream.md", "fragment": "OQ-UP1", "line": 4}], "questions": []},
				{"path": "docs/design/parked.md", "status": "draft", "stage": "SKETCH", "questions": []}
			]
		}
	}`
	indexScript := filepath.Join(scratchUnderDurable(t), "vantage-stub.sh")
	writeForkFile(t, indexScript, "#!/bin/sh\ncat <<'EOF'\n"+indexJSON+"\nEOF\n")
	if err := os.Chmod(indexScript, 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.QuestionsCmd = []string{indexScript}
	if p := l.currentPolicy(); p != nil {
		if p.Seats == nil {
			p.Seats = map[string]string{}
		}
		if p.SeatLists == nil {
			p.SeatLists = map[string][]string{}
		}
		p.Seats["design"] = p.DefaultBackend
		p.Seats["plan"] = p.DefaultBackend
		p.SeatLists["design"] = []string{p.DefaultBackend}
		p.SeatLists["plan"] = []string{p.DefaultBackend}
	}

	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0700); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/roadmap.md"), strings.Join([]string{
		"# Roadmap",
		"- [OQ-QM2](design/oq-qm2.md)",
		"- [OQ-QM3](design/oq-qm3.md)",
		"- [Dep OQ](design/dep-oq.md)",
		"## Icebox",
		"- [Parked](design/parked.md)",
	}, "\n"))
	writeForkFile(t, filepath.Join(f.root, "docs/design/oq-qm2.md"), "---\nstage: DESIGN\n---\n# OQ-QM2\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/oq-qm3.md"), "---\nstage: DECIDED\n---\n# OQ-QM3\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/upstream.md"), "---\nstage: DESIGN\n---\n# Upstream\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/dep-oq.md"), "---\nstage: DESIGN\ndepends-on:\n  - ./upstream.md#OQ-UP1\n---\n# Dep OQ\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/parked.md"), "---\nstage: SKETCH\n---\n# Parked\n")
	// Add a normal document block so documentPrerequisites sees a document-fed repository too.
	writeForkFile(t, filepath.Join(f.root, "docs/work.md"), "### Work\n<!-- tillr: item id=\"item-work-1\" paths=\"docs/\" -->\n- **c1:** Works\n")
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "slice 1 fixture")

	binary := filepath.Join(scratchUnderDurable(t), "tillr-item")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "../../tillr-item"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build item binary: %v\n%s", err, out)
	}
	steps := &ProcessSteps{
		Root:         f.root,
		Store:        f.storePath,
		ItemBin:      binary,
		QuestionsCmd: []string{indexScript},
		Now:          l.cfg.Now,
	}
	outcome, err := steps.Sync(context.Background())
	if err != nil || outcome.Created != 5 || len(outcome.Findings) != 0 {
		t.Fatalf("sync: %+v / %v", outcome, err)
	}

	assertHold := func(id, wantReason, wantQuestion string) {
		t.Helper()
		d := decisionFor(t, l, id).decision
		if d.Action != ActionHold || d.Reason != wantReason {
			t.Fatalf("%s decision = %s / %s, want hold / %s", id, d.Action, d.Reason, wantReason)
		}
		if wantQuestion != "" && d.QuestionID != wantQuestion {
			t.Fatalf("%s question_id = %q, want %q", id, d.QuestionID, wantQuestion)
		}
		if d.RunRequest != nil {
			t.Fatalf("%s must not have a RunRequest", id)
		}
	}

	assertHold("item-design-design-oq-qm2", ReasonAwaitingOQQM2, "")
	assertHold("item-design-design-oq-qm3", ReasonAwaitingOQQM3, "")
	assertHold("item-design-design-dep-oq", ReasonQuestionOpen, "OQ-UP1")
	assertHold("item-design-design-parked", ReasonBlocked, "")

	// Verify item-design-* rows are never routed to triage-no-block.
	plans := mustPlan(t, l)
	for _, entry := range plans {
		if strings.HasPrefix(entry.decision.ItemID, "item-design-") && entry.decision.Reason == ReasonTriageNoBlock {
			t.Fatalf("%s was routed to %s", entry.decision.ItemID, ReasonTriageNoBlock)
		}
	}
}
