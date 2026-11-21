package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func backendRun(t *testing.T, f refusalLoopFixture, id, backend string, exit int) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: id, ItemID: "item-a", LaneID: "lane-1", Backend: backend, State: run.StateExited, ExitCode: code(exit), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z"})
	})
}

func TestBackendInfrastructureClosureAndRecordedReopening(t *testing.T) {
	for _, event := range []string{"run", "probe"} {
		t.Run(event, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			backendRun(t, f, "broken", "fixture", 2)
			d := mustPlan(t, l)[0].decision
			if d.Action != ActionHold || d.Reason != ReasonBackendClosed || d.DeferredBy != "fixture" {
				t.Fatalf("closed backend dispatched: %+v", d)
			}
			// Neither a later clock nor reconstructing a loop is a reopening event.
			l.cfg.Now = func() time.Time { return time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC) }
			if got := mustPlan(t, l)[0].decision; got.Action != ActionHold {
				t.Fatalf("clock reopened backend: %+v", got)
			}
			if event == "run" {
				backendRun(t, f, "answered", "fixture", 0)
			} else {
				if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/false"}); err == nil {
					t.Fatal("failed probe accepted")
				}
				if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
					t.Fatalf("failed probe reopened: %+v", got)
				}
				if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
					t.Fatal(err)
				}
			}
			// Inspect availability directly: an answered item may independently wait for a retry.
			if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				closed, err := readInfrastructureClosed(tx.Reader())
				if err == nil && closed["fixture"] {
					t.Error("recorded event did not reopen backend")
				}
				return err
			})
			if event == "probe" && mustPlan(t, l)[0].decision.Action != ActionDispatch {
				t.Fatal("probe did not release item")
			}
			backendRun(t, f, "broken-again", "fixture", 2)
			if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
				t.Fatalf("old reopening event won over new failure: %+v", got)
			}
		})
	}
}

func TestSingleBackendSeatNamesTheRecordedQuotaWindow(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["alternate"] = b
	reset := l.cfg.Now().Add(time.Hour).UTC()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_windows VALUES('fixture','weekly',1,'rejected',?,'refusing-run')`, reset.UnixMilli())
		return err
	})
	d := planFor(t, l, "item-a")
	if d.Action != ActionHold || d.Reason != ReasonBackendWindow || d.RunRequest != nil || d.DeferredBy != "fixture" ||
		d.BackendHoldCause != "weekly quota refusal recorded by run refusing-run" ||
		!strings.Contains(d.BackendHoldMover, "provider quota resets at "+reset.Format(time.RFC3339)) ||
		!strings.Contains(d.BackendHoldMover, "successful probe-backend") {
		t.Fatalf("want the seat's quota hold with cause and mover, got %+v", d)
	}
}

func TestWorkflowBackendFailureWaitsAndReopensSameNode(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	l.cfg.Policy.Backends["alternate"] = l.cfg.Policy.Backends["fixture"]
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	d := p.decision
	backendRun(t, f, d.RunRequest.RunID, "fixture", 2)
	if err := l.holdFailedWorkflowNode(context.Background(), d, runGit(t, f.root, "rev-parse", TrunkRef), run.Record{RunID: d.RunRequest.RunID, Backend: "fixture", State: run.StateExited, ExitCode: code(2)}, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	wait := mustPlan(t, l)[0].decision
	if wait.Action != ActionHold || wait.Reason != ReasonBackendClosed || wait.NodeIndex != d.NodeIndex ||
		wait.DeferredBy != "fixture" || wait.RunRequest.Backend != "fixture" ||
		!strings.Contains(wait.BackendHoldCause, d.RunRequest.RunID) || !strings.Contains(wait.BackendHoldMover, "operator") {
		t.Fatalf("node did not wait: %+v", wait)
	}
	if err := ProbeBackend(context.Background(), f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	next := mustPlan(t, l)[0].decision
	if next.Action != ActionDispatch || next.NodeIndex != d.NodeIndex || next.Attempt != d.Attempt || next.RunRequest.Backend != "fixture" {
		t.Fatalf("node not resumed: %+v", next)
	}
}

func TestBackendAvailabilityIgnoresMetadataAndReportEdits(t *testing.T) {
	f, l := passFixture(t, "item-a")
	report := filepath.Join(f.root, "answer.md")
	writeForkFile(t, report, "RESULT: failed\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: "answered", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(1), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}})
	})
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			closed, err := readInfrastructureClosed(tx.Reader())
			if err == nil && closed["fixture"] != want {
				t.Errorf("closed=%v, want %v", closed, want)
			}
			return err
		})
	}
	check(false)
	if err := os.Remove(report); err != nil {
		t.Fatal(err)
	}
	check(false)
	writeForkFile(t, report, "stream without a footer\n")
	check(false)
	backendRun(t, f, "broken", "fixture", 2)
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	check(true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE runs SET event_seq=999999 WHERE run_id='answered'`)
		return err
	})
	check(true)
	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed {
		t.Fatalf("metadata reopened backend: %+v", got)
	}
}

func TestBackendHoldNamesOnlyTheFailedBackend(t *testing.T) {
	f, l := passFixture(t, "item-a")
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["override"] = b
	l.cfg.Policy.Backends["alternate"] = b
	l.cfg.Policy.Overrides = map[string]string{"item-a": "override"}
	backendRun(t, f, "override-failed", "override", 2)
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionHold || d.Reason != ReasonBackendClosed || d.DeferredBy != "override" ||
		!strings.Contains(d.BackendHoldCause, "override-failed") || !strings.Contains(d.BackendHoldCause, "exit 2") {
		t.Fatalf("hold did not name the measured failure on override: %+v", d)
	}
	if err := ProbeBackend(context.Background(), f.storePath, f.root, "override", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	d = mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.RunRequest.Backend != "override" {
		t.Fatalf("did not redispatch on override: %+v", d)
	}
}
