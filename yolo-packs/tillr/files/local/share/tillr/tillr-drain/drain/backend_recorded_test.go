package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestStartViewRefreshBackendsAvoidsRescanningHistoricalFinishes(t *testing.T) {
	f, l := passFixture(t, "item-a")
	for i := 0; i < 16; i++ {
		backendUsageRun(t, f, fmt.Sprintf("prior-%02d", i), "fixture", 0)
	}
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}

	var sync startStoreSync
	t.Cleanup(sync.close)
	ctx, at := context.Background(), l.cfg.Now()
	tick := l.startViewPass()
	questions := &VantageQuestions{Root: f.root}
	var previous startSnapshot
	refresh := func() startSnapshot {
		t.Helper()
		next, err := sync.syncStore(ctx, tick.StorePath, at, l.currentPolicy(), previous, func(r *store.Reader, next *startSnapshot, touched map[string]bool) (func() error, error) {
			return next.prepareRepair(ctx, r, at, l.currentPolicy(), tick, questions, previous, touched)
		})
		if err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			want, err := readInfrastructureClosedFrom(tx.Reader(), next.pending.byRun)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(next.infrastructureClosed, want) {
				t.Fatalf("incremental infrastructureClosed = %+v, want %+v", next.infrastructureClosed, want)
			}
			return nil
		})
		previous = next
		return next
	}

	seedReads, seedUnmarshals := backendOutcomesReads.Load(), backendOutcomeUnmarshals.Load()
	seed := refresh()
	// Note: refresh's oracle above performs 1 readInfrastructureClosedFrom call after syncStore.
	if got := backendOutcomesReads.Load() - seedReads; got != 2 {
		t.Fatalf("seed + oracle performed %d backend outcome scans, want 2 (1 seed + 1 oracle)", got)
	}
	if got := backendOutcomeUnmarshals.Load() - seedUnmarshals; got != 0 {
		t.Fatalf("seed + oracle unmarshalled %d saved payloads, want 0", got)
	}
	if seed.shut["fixture"] {
		t.Fatalf("seed shut fixture unexpectedly: %+v", seed.shut)
	}

	assertRefreshCost := func(label string, wantScans, wantUnmarshals int64) startSnapshot {
		t.Helper()
		readsBefore, unmarshalsBefore := backendOutcomesReads.Load(), backendOutcomeUnmarshals.Load()
		next, err := sync.syncStore(ctx, tick.StorePath, at, l.currentPolicy(), previous, func(r *store.Reader, next *startSnapshot, touched map[string]bool) (func() error, error) {
			return next.prepareRepair(ctx, r, at, l.currentPolicy(), tick, questions, previous, touched)
		})
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got := backendOutcomesReads.Load() - readsBefore; got != wantScans {
			t.Fatalf("%s: backendOutcomesReads = %d, want %d", label, got, wantScans)
		}
		if got := backendOutcomeUnmarshals.Load() - unmarshalsBefore; got != wantUnmarshals {
			t.Fatalf("%s: backendOutcomeUnmarshals = %d, want %d", label, got, wantUnmarshals)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			want, err := readInfrastructureClosedFrom(tx.Reader(), next.pending.byRun)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(next.infrastructureClosed, want) {
				t.Fatalf("%s: incremental infrastructureClosed = %+v, want %+v", label, next.infrastructureClosed, want)
			}
			return nil
		})
		previous = next
		return next
	}

	// 1. Unlogged write (data_version bump with unchanged event seq) triggers refreshBackends.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT OR REPLACE INTO backend_windows VALUES('alternate','weekly',1,'rejected',?,'refusing-run')`, at.Add(time.Hour).UnixMilli())
		return err
	})
	assertRefreshCost("unlogged-quota-write", 0, 0)

	// 2. Clock advance past QuotaProbeInterval triggers refreshBackends.
	at = at.Add(QuotaProbeInterval + time.Second)
	assertRefreshCost("quota-probe-interval", 0, 0)

	// 3. New unsaved infrastructure run.finish closes the backend without scanning 16 historical rows.
	backendUsageRun(t, f, "infra-close-1", "fixture", 2)
	closedUnsaved := assertRefreshCost("new-unsaved-finish", 0, 1)
	if !closedUnsaved.shut["fixture"] || closedUnsaved.infrastructureClosed["fixture"].Saved {
		t.Fatalf("unsaved finish did not close fixture: %+v / %+v", closedUnsaved.shut, closedUnsaved.infrastructureClosed)
	}

	// 4. Saving that outcome in backend_outcomes transitions saved=true with 0 scans and 0 unmarshals.
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatal(err)
	}
	closedSaved := assertRefreshCost("record-backend-outcomes", 0, 0)
	if !closedSaved.shut["fixture"] || !closedSaved.infrastructureClosed["fixture"].Saved {
		t.Fatalf("saved finish did not stay closed with saved=true: %+v / %+v", closedSaved.shut, closedSaved.infrastructureClosed)
	}

	// 5. A successful probe reopens the backend incrementally with 0 scans and 0 unmarshals.
	if err := ProbeBackend(ctx, f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
		t.Fatal(err)
	}
	reopened := assertRefreshCost("probe-reopen", 0, 0)
	if reopened.shut["fixture"] || len(reopened.infrastructureClosed) != 0 {
		t.Fatalf("probe did not reopen fixture: %+v / %+v", reopened.shut, reopened.infrastructureClosed)
	}

	// 6. call.offered overrides a saved closing finish incrementally with 0 scans and 0 unmarshals.
	backendUsageRun(t, f, "infra-close-2", "fixture", 2)
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatal(err)
	}
	closedAgain := assertRefreshCost("new-saved-close", 0, 1)
	closingSeq := closedAgain.infrastructureClosed["fixture"].Seq
	if !closedAgain.shut["fixture"] || closingSeq == 0 {
		t.Fatalf("second close missing: %+v", closedAgain.infrastructureClosed)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		runID := "infra-close-2"
		_, err := tx.Append(event.Event{
			Kind:       callOffered,
			ObjectType: "run",
			ObjectID:   &runID,
			Payload:    []byte(fmt.Sprintf(`{"terminal_seq":%d}`, closingSeq)),
			At:         at,
		})
		return err
	})
	offeredOpen := assertRefreshCost("call-offered-override", 0, 0)
	if offeredOpen.shut["fixture"] || len(offeredOpen.infrastructureClosed) != 0 {
		t.Fatalf("call.offered did not clear closure: %+v / %+v", offeredOpen.shut, offeredOpen.infrastructureClosed)
	}

	// 7. Direct refreshBackends on an already-seeded snapshot also advances incrementally.
	backendUsageRun(t, f, "direct-answered", "fixture", 0)
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatal(err)
	}
	readsBefore, unmarshalsBefore := backendOutcomesReads.Load(), backendOutcomeUnmarshals.Load()
	direct := offeredOpen
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return direct.refreshBackends(tx.Reader(), at, l.currentPolicy())
	})
	if got := backendOutcomesReads.Load() - readsBefore; got != 0 {
		t.Fatalf("direct refreshBackends scans = %d, want 0", got)
	}
	if got := backendOutcomeUnmarshals.Load() - unmarshalsBefore; got != 0 {
		t.Fatalf("direct refreshBackends unmarshals = %d, want 0", got)
	}
}
