package drain

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func missingCandidateFixture(t *testing.T) (refusalLoopFixture, *Loop, *parallelSteps, string) {
	t.Helper()
	f, l, steps := parallelFixture(t)
	l.cfg.Policy.Seats["test-run"] = "fixture"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref='' WHERE item_id='item-a'`)
		return err
	})
	expansionFile(t, f.root, "scout-then-build", `"stages":[{"id":"scout","kind":"agent","seat":"scout","access":"read_only"},{"id":"build","kind":"agent","seat":"build","access":"write"},{"id":"check","kind":"agent","seat":"test-run","access":"read_only"}],"edges":[{"from_node":"scout","to_node":"build"}]`)
	runGit(t, f.root, "commit", "-qam", "candidate check fixture")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	steps.firstHead = base
	steps.wantOverlap = 2
	steps.writerAfter = 2
	steps.builderSuffix = "-n2"
	steps.badReportNode = "-a1-n3"
	return f, l, steps, base
}

func TestCandidateCheckWithoutCommitReturnsToWriter(t *testing.T) {
	f, l, steps, base := missingCandidateFixture(t)
	finding := "Returned 0: HEAD equals the item base, so there is no committed slice to verify."
	checkFinished := make(chan struct{})
	steps.beforeTerminal = func(req run.Request) error {
		switch req.NodeID {
		case "scout":
			select {
			case <-checkFinished:
			case <-time.After(hangGuard):
				return fmt.Errorf("candidate check never finished")
			}
		case "check":
			if req.RunID == "run-item-a-a1-n3" {
				count, err := l.git(context.Background(), l.laneTarget(req.LaneID), "rev-list", "--count", base+"..HEAD")
				if err != nil || strings.TrimSpace(count) != "0" {
					return fmt.Errorf("premature check had %q commits: %v", count, err)
				}
			}
		}
		return nil
	}
	steps.afterTerminal = func(req run.Request) error {
		if req.RunID == "run-item-a-a1-n3" {
			close(checkFinished)
		}
		return nil
	}
	steps.badReport = "# run report\n\n---\nOBSTACLE: tooling\nCOMMAND: git rev-list --count " + base + "..HEAD\nEVIDENCE: " + finding + "\nPATHS: /absolute/lane/worktree\nRESULT: blocked\n"

	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	d := decisionFor(t, l, "item-a").decision
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.update' AND object_id='item-a' AND json_extract(payload,'$.waiting_on.waiter')='owner' AND json_extract(payload,'$.waiting_on.reason')='# run report'`); n != 0 {
		t.Fatalf("no-commit check held for the owner with reason %q", "# run report")
	}
	if d.RunRequest == nil || d.RunRequest.Node != "build" || d.Returned == nil || !strings.Contains(d.Returned.Detail, finding) {
		t.Fatalf("expected an automatic return to build with the finding, got %s (%s), request=%+v, return=%+v", d.Action, d.Reason, d.RunRequest, d.Returned)
	}
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a'`); n != 0 {
		t.Fatalf("recorded %d conflicts for a missing commit", n)
	}
	for i := 0; i < 6 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 || steps.builders != 1 || steps.started != 3 {
		t.Fatalf("expected one build, a rerun of the check, and landing: result=%+v builders=%d readers=%d", result, steps.builders, steps.started)
	}
}

func TestCandidateCheckAfterNoOpWriterReturnsToWriter(t *testing.T) {
	f, l, steps, _ := missingCandidateFixture(t)
	changeParallelWorkflow(t, f, func(w *Workflow) {
		w.Edges = append(w.Edges, WorkflowEdge{From: "build", To: "check"})
	})
	base := runGit(t, f.root, "rev-parse", "HEAD")
	steps.firstHead = base
	steps.wantOverlap = 1
	steps.writerAfter = 3
	// The first build reports done without a commit; only its return writes.
	steps.builderSuffix = "-a2-n2"
	finding := "Returned 0: HEAD equals the item base."
	steps.badReport = "OBSTACLE: tooling\nCOMMAND: git rev-list --count " + base + "..HEAD\nEVIDENCE: " + finding + "\nPATHS: .\nRESULT: blocked\n"
	result := &LoopResult{}
	for i := 0; i < 12 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 || steps.builders != 1 || steps.started != 4 {
		t.Fatalf("expected one repair build, a rerun of the check, and landing: result=%+v builders=%d other runs=%d", result, steps.builders, steps.started)
	}
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='drain.refusal' AND object_id='run-item-a-a1-n3' AND json_extract(payload,'$.detail')=?`, finding); n != 1 {
		t.Fatalf("expected one return with finding %q, got %d", finding, n)
	}
}

func TestCandidateCheckHoldNamesFinding(t *testing.T) {
	f, l, steps, base := missingCandidateFixture(t)
	finding := "Git could not read the object database."
	steps.badReport = "# run report\n\n---\nOBSTACLE: tooling\nCOMMAND: git rev-list --count " + base + "..HEAD\nEVIDENCE: " + finding + "\nPATHS: /absolute/lane/worktree\nRESULT: blocked\n"
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind='item.update' AND object_id='item-a' AND json_extract(payload,'$.waiting_on.reason')=?`, finding); n != 1 {
		t.Fatalf("expected one hold naming %q, got %d", finding, n)
	}
}
