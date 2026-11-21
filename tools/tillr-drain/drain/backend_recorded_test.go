package drain

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSavedInfrastructureClassKeepsItemWaitingAfterReportEdit(t *testing.T) {
	f, l := passFixture(t, "item-a")
	report := filepath.Join(f.root, "report.md")
	writeForkFile(t, report, "dropped stream without footer\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: "stream-failed", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(1), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}})
	})
	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
		t.Fatalf("failed stream did not wait: %+v", got)
	}
	// A plan only reads: the class is saved where the loop settles or starts.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM backend_outcomes`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("a plan saved %d classes, want none", count)
		}
		return nil
	})
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, report, "RESULT: failed\n")
	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
		t.Fatalf("edited report displaced saved infrastructure class: %+v", got)
	}
	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	if got := mustPlan(t, l)[0].decision; got.Action != ActionDispatch {
		t.Fatalf("probe did not release failed item: %+v", got)
	}
}

// An ending nobody has saved is classified by whoever reads it, and the read
// saves nothing: a plan made before the loop's first save still sees the closure.
func TestUnsavedEndingClosesItsBackendWithoutAWrite(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	backendRun(t, f, "not-observed", "fixture", 2)
	events := func() (count int) {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return tx.Reader().QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count)
		})
		return count
	}
	before := events()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosed(tx.Reader())
		if !closed["fixture"] {
			t.Error("unsaved infrastructure ending did not close its backend")
		}
		return err
	})
	if after := events(); after != before {
		t.Errorf("reading availability wrote %d event(s)", after-before)
	}
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosed(tx.Reader())
		if !closed["fixture"] {
			t.Error("saved infrastructure ending did not keep its backend closed")
		}
		return err
	})
}

func TestBackendOutcomesCaptureEveryEndedRunOnce(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	backendRun(t, f, "older", "fixture", 2)
	backendRun(t, f, "newer", "fixture", 0)
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.Reader().QueryRow(`SELECT COUNT(*) FROM backend_outcomes`).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("saved %d classes, want both endings once", count)
		}
		return nil
	})
}

func TestParallelBackendHoldNamesWaitingNodeNotFirstSibling(t *testing.T) {
	f, l, _ := parallelFixture(t)
	d := mustPlan(t, l)[0].decision
	first := d
	first.WorkflowRestart = true
	first.RunRequest = rerouteNodeRequest(l.cfg.Policy, first, "fixture")
	first.RunRequest.RunID = "first-ready"
	second := first
	second.RunRequest = rerouteNodeRequest(l.cfg.Policy, second, "fixture")
	second.RunRequest.Backend = "broken"
	second.RunRequest.RunID = "second-ready"
	second.NodeIndex = 1
	second.Seat = "review"
	second.NodeInfraFailures = 1
	first.Parallel = []Decision{second}
	l.cfg.Policy.Backends["broken"] = l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Seats["review"] = "broken"
	backendRun(t, f, "broken-ending", "broken", 2)
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	for _, restart := range []bool{false, true} {
		first.WorkflowRestart = restart
		fixtureWrite(t, f, func(tx *store.Tx) error {
			hold, err := pendingDecision(tx.Reader(), &pendingWork{Decision: first, Ready: true}, l.cfg.Now(), nil, nil, l.cfg.Policy, map[string]bool{"broken": true}, 0)
			if err != nil {
				return err
			}
			if err := describeBackendHold(tx.Reader(), &hold, l.cfg.Policy, l.cfg.Now(), nil); err != nil {
				return err
			}
			if hold.Action != ActionHold || hold.Reason != ReasonBackendClosed || hold.DeferredBy != "broken" ||
				hold.BackendHoldCause != "infrastructure failure: run broken-ending (exited), exit 2" || hold.BackendHoldMover == "" {
				t.Errorf("restart=%v hold names wrong backend: %+v", restart, hold)
			}
			return nil
		})
	}
}
