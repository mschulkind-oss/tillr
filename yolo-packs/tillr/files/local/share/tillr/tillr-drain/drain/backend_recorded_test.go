package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
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
	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendClosed || !strings.Contains(got.BackendHoldCause, "dropped stream without footer") {
		t.Fatalf("edited report displaced saved infrastructure class or cause: %+v", got)
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
	firstRequest := *d.RunRequest
	first.RunRequest = &firstRequest
	first.RunRequest.RunID = "first-ready"
	second := first
	secondRequest := firstRequest
	second.RunRequest = &secondRequest
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
				hold.BackendHoldCause != "run broken-ending on broken (exited), exit 2" || hold.BackendHoldMover == "" {
				t.Errorf("restart=%v hold names wrong backend: %+v", restart, hold)
			}
			return nil
		})
	}
}

func backendUsageRun(t *testing.T, f refusalLoopFixture, id, backend string, exit int) {
	t.Helper()
	tokens, cost := int64(1500), 0.42
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID: id, ItemID: "item-a", LaneID: "lane-1", Backend: backend,
			State: run.StateExited, ExitCode: code(exit),
			StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z",
			Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost},
		})
	})
}

func TestSavedBackendOutcomesSkipPayloadUnmarshal(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Backends["broken"] = l.cfg.Policy.Backends["fixture"]
	for i := 0; i < 12; i++ {
		backendUsageRun(t, f, fmt.Sprintf("hist-%02d", i), "fixture", 0)
	}
	// Positive control: unsaved endings must unmarshal their payloads to classify.
	unmarshalsBefore := backendOutcomeUnmarshals.Load()
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	if got := backendOutcomeUnmarshals.Load() - unmarshalsBefore; got != 12 {
		t.Fatalf("unsaved RecordBackendOutcomes unmarshalled %d payloads, want 12", got)
	}

	// Once saved in backend_outcomes, reading outcomes and open-backend availability
	// must not unmarshal historical run.finish payloads.
	unmarshalsBefore = backendOutcomeUnmarshals.Load()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		outcomes, err := readBackendOutcomes(tx.Reader())
		if err != nil {
			return err
		}
		if len(outcomes) != 12 {
			t.Fatalf("outcomes = %d, want 12", len(outcomes))
		}
		for _, o := range outcomes {
			if !o.Saved || o.Rec.RunID == "" || o.Rec.Backend != "fixture" || o.Class != AttemptAnswered || o.Closes {
				t.Fatalf("unexpected saved outcome: %+v", o)
			}
		}
		closed, err := readInfrastructureClosedFrom(tx.Reader(), nil)
		if err != nil {
			return err
		}
		if len(closed) != 0 {
			t.Fatalf("closed = %+v, want empty", closed)
		}
		return nil
	})
	if got := backendOutcomeUnmarshals.Load() - unmarshalsBefore; got != 0 {
		t.Fatalf("saved open-backend reads unmarshalled %d payloads, want 0", got)
	}

	// A saved closed backend unmarshals only its single closing record (not all 12+1 rows)
	// and still populates the full record for describeBackendHold.
	backendUsageRun(t, f, "broken-close", "broken", 2)
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	unmarshalsBefore = backendOutcomeUnmarshals.Load()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosedFrom(tx.Reader(), nil)
		if err != nil {
			return err
		}
		got, ok := closed["broken"]
		if !ok || got.Rec.RunID != "broken-close" || got.Rec.ItemID != "item-a" || got.Rec.State != run.StateExited {
			t.Fatalf("closed[broken] = %+v (ok=%v), want full closing record", got, ok)
		}
		return nil
	})
	if got := backendOutcomeUnmarshals.Load() - unmarshalsBefore; got != 1 {
		t.Fatalf("saved closed-backend read unmarshalled %d payloads, want 1", got)
	}

	// A saved event without a matching runs row still projects run_id and backend without json.Unmarshal.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		seq, err := tx.Append(event.Event{
			Kind:       run.FinishKind,
			ObjectType: "run",
			Payload:    []byte(`{"run_id":"orphan-event","backend":"fixture","state":"exited","exit_code":0}`),
			At:         l.cfg.Now(),
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO backend_outcomes(terminal_seq,class,closes) VALUES(?,?,0)`, seq, AttemptAnswered)
		return err
	})
	unmarshalsBefore = backendOutcomeUnmarshals.Load()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		outcomes, err := readBackendOutcomes(tx.Reader())
		if err != nil {
			return err
		}
		if len(outcomes) == 0 || outcomes[0].Rec.RunID != "orphan-event" || outcomes[0].Rec.Backend != "fixture" {
			t.Fatalf("orphan saved outcome = %+v, want orphan-event on fixture", outcomes)
		}
		return nil
	})
	if got := backendOutcomeUnmarshals.Load() - unmarshalsBefore; got != 0 {
		t.Fatalf("orphan saved read unmarshalled %d payloads, want 0", got)
	}
}
