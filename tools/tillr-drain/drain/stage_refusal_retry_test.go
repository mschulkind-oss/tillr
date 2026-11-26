package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	drainfaults "github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Uncommitted work in a fork checkout during a multi-node workflow (-a1-n1 finished,
// -a1-n2 staging refused) records a durable stage-failed item fault (firing the
// drain-item-fault watch rule), shows the item as held for the operator in tillr drain
// why naming the unstarted -a1-n2 run id, and tillr drain retry accepts that run id
// without --acknowledge, clearing the fault without writing a retries row so the next
// pass re-offers -a1-n2 while keeping completed -a1-n1.
func TestUncommittedForkCheckoutRefusalRecordsFaultShowsHeldAndAcceptsRetry(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	l.cfg.Policy.Seats["classify"] = "fixture"
	if err := os.Mkdir(filepath.Join(f.root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", "bin/pi-forks.list", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "fork catalog")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET workflow='single-worker@v1' WHERE item_id='item-a'`)
		return err
	})

	fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-qb", "main")
	runGit(t, fork, "commit", "--allow-empty", "-qm", "delivered base")
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	// Leave uncommitted work in the fork checkout so staging node 2 refuses it.
	dirtyFile := filepath.Join(fork, "uncommitted.txt")
	writeForkFile(t, dirtyFile, "uncommitted work\n")

	l.cfg.Steps = &mixedDeliverySteps{workflowSteps: steps}
	var totalInfra int
	for i := 0; i < 8; i++ {
		res := &LoopResult{}
		graphPass(t, l, res)
		totalInfra += res.InfraFailures
		if planFor(t, l, "item-a").Reason == ReasonStageFailed {
			break
		}
	}
	if totalInfra != 0 {
		t.Fatalf("InfraFailures = %d, want 0 (uncommitted fork checkout is a per-item staging refusal)", totalInfra)
	}

	var fault ItemFault
	const wantHeldRun = "run-item-a-a1-n2"
	fixtureRead(t, f, func(r *store.Reader) error {
		// Verify durable drain.item-fault event (consumed by config/tillr/watch/drain-item-fault.toml).
		events, err := r.EventsOfKind(0, itemFaultKind)
		if err != nil {
			return err
		}
		if len(events) != 1 || events[0].ObjectID == nil || *events[0].ObjectID != "item-a" {
			t.Fatalf("drain.item-fault events = %+v, want 1 event for item-a", events)
		}

		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		var ok bool
		fault, ok = faults["item-a"]
		if !ok || fault.Reason != ReasonStageFailed || fault.RunID != wantHeldRun || fault.LaneID != "lane-1" {
			t.Fatalf("fault = %+v (ok=%v), want stage-failed on %s / lane-1", fault, ok, wantHeldRun)
		}
		if !strings.Contains(fault.Detail, "uncommitted work") {
			t.Fatalf("fault detail = %q, want uncommitted work refusal", fault.Detail)
		}

		// Verify node 1 is recorded in runs and node 2 is unstarted (no row in runs).
		var n1Count, n2Count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id='run-item-a-a1-n1'`).Scan(&n1Count); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id=?`, wantHeldRun).Scan(&n2Count); err != nil {
			return err
		}
		if n1Count != 1 || n2Count != 0 {
			t.Fatalf("runs counts: n1=%d n2=%d, want 1 and 0", n1Count, n2Count)
		}

		// Verify tillr drain why (separate reader, no in-memory loop holds) shows held for operator
		// naming run-item-a-a1-n2 and recommending tillr drain retry with that run id.
		answer, err := Why(r, Pass{
			StorePath:      f.storePath,
			Now:            l.cfg.Now().Format(time.RFC3339),
			AvailableLanes: &l.cfg.Lanes,
		}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		found := false
		wantRetry := drainfaults.StageRetryCommand(f.storePath, "item-a", wantHeldRun)
		wantClear := fault.ClearCommand(f.storePath)
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			found = true
			if row.Position != "held" || row.Reason != ReasonStageFailed || row.Class != "needs you" || row.Where != "lane" {
				t.Fatalf("why row = %+v, want position=held reason=stage-failed class='needs you' where=lane", row)
			}
			if !strings.Contains(row.Why, "; run "+wantHeldRun) || !strings.Contains(row.Why, "uncommitted work") {
				t.Fatalf("why = %q, want uncommitted work detail and '; run %s'", row.Why, wantHeldRun)
			}
			if !strings.Contains(row.Mover, wantRetry) || !strings.Contains(row.Mover, wantClear) {
				t.Fatalf("mover = %q, want %q and %q", row.Mover, wantRetry, wantClear)
			}
		}
		if !found {
			t.Fatal("Why() returned no row for held item-a")
		}
		return nil
	})

	// AuthorizeRetry rejects --acknowledge on an unstarted stage-failed run.
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			StorePath:   f.storePath,
			ItemID:      "item-a",
			RunID:       wantHeldRun,
			By:          "operator",
			Why:         "cleaned fork checkout",
			At:          l.cfg.Now().Add(time.Minute),
			Acknowledge: RetryAckGateFailed,
		})
		if err == nil {
			t.Fatal("AuthorizeRetry with --acknowledge unexpectedly succeeded on unstarted stage-failed run")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// AuthorizeRetry accepts the held unstarted run id (-a1-n2) with no --acknowledge,
	// clears stage-failed, appends drain.retry.stage, and writes no retries row.
	var retry Retry
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var err error
		retry, err = AuthorizeRetry(context.Background(), tx, RetryRequest{
			StorePath: f.storePath,
			ItemID:    "item-a",
			RunID:     wantHeldRun,
			By:        "operator",
			Why:       "cleaned fork checkout",
			At:        l.cfg.Now().Add(time.Minute),
		})
		return err
	})
	env, err := RetryEnvelope(retry)
	if err != nil {
		t.Fatalf("RetryEnvelope: %v", err)
	}
	if env.Kind != StageRetryKind || retry.SupersedesRunID != wantHeldRun || retry.ClearsStageFault != fault.Seq {
		t.Fatalf("stage retry envelope = %+v (retry=%+v), want kind=%s run=%s fault=%d", env, retry, StageRetryKind, wantHeldRun, fault.Seq)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, held := faults["item-a"]; held {
			t.Fatalf("stage-failed fault still active after AuthorizeRetry: %+v", faults["item-a"])
		}
		var retryRows int
		if err := r.QueryRow(`SELECT COUNT(*) FROM retries WHERE item_id='item-a'`).Scan(&retryRows); err != nil {
			return err
		}
		if retryRows != 0 {
			t.Fatalf("retries rows = %d, want 0 for stage retry", retryRows)
		}
		stageEvents, err := r.EventsOfKind(0, StageRetryKind)
		if err != nil {
			return err
		}
		if len(stageEvents) != 1 || stageEvents[0].ObjectID == nil || *stageEvents[0].ObjectID != wantHeldRun {
			t.Fatalf("drain.retry.stage events = %+v, want 1 event for %s", stageEvents, wantHeldRun)
		}
		return nil
	})

	// Clean the fork checkout; the next pass offers -a1-n2 again (keeping completed -a1-n1)
	// and stages cleanly.
	if err := os.Remove(dirtyFile); err != nil {
		t.Fatal(err)
	}
	planned := false
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID != "item-a" {
			continue
		}
		planned = true
		if d.Action != ActionDispatch || d.NodeIndex != 1 || d.RunRequest == nil || d.RunRequest.RunID != wantHeldRun {
			t.Fatalf("after stage retry plan = %+v, want ActionDispatch for node 1 (%s)", d, wantHeldRun)
		}
		ctx := context.Background()
		trunk := runGit(t, f.root, "rev-parse", "HEAD")
		ok, work, err := l.applyPlan(ctx, p, trunk, l.cfg.Now().Add(2*time.Minute))
		if err != nil || !ok {
			t.Fatalf("applyPlan after stage retry: (%v, %v)", ok, err)
		}
		staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, trunk, &LoopResult{})
		if err != nil || len(staged) != 1 {
			t.Fatalf("stageDispatches after cleaning fork checkout: staged=%d err=%v", len(staged), err)
		}
	}
	if !planned {
		t.Fatal("item-a was not planned after stage retry")
	}
}

// A non-per-item staging failure that repeats unchanged records a durable stage-failed
// item fault (not an in-memory local hold), shows as held in tillr drain why with the
// unstarted run id, and tillr drain retry accepts that run id.
func TestRepeatedUnchangedStagingFailureRecordsFaultShowsHeldAndAcceptsRetry(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-b")
	l, report := stageFaultLoop(t, f, &recordingSteps{})
	l.cfg.Lanes = []string{"lane-1"}
	res, err := l.Run(t.Context())
	if err != nil || res.Stop != StopQueueDrained || res.Dispatched != 0 || res.InfraFailures != 2 {
		t.Fatalf("expected unchanged fault to be held after retry: %+v (%v)\n%s", res, err, report)
	}

	const wantRun = "run-item-b-a1"
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault, ok := faults["item-b"]
		if !ok || fault.Reason != ReasonStageFailed || fault.RunID != wantRun || fault.LaneID != "lane-1" {
			t.Fatalf("fault = %+v (ok=%v), want stage-failed on %s / lane-1", fault, ok, wantRun)
		}
		if !strings.Contains(fault.Detail, "staging failure repeated unchanged") {
			t.Fatalf("fault detail = %q, want repeated unchanged detail", fault.Detail)
		}
		answer, err := Why(r, Pass{
			StorePath:      f.storePath,
			Now:            l.cfg.Now().Format(time.RFC3339),
			AvailableLanes: &l.cfg.Lanes,
		}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		wantRetry := drainfaults.StageRetryCommand(f.storePath, "item-b", wantRun)
		found := false
		for _, row := range answer.Rows {
			if row.ItemID != "item-b" {
				continue
			}
			found = true
			if row.Position != "held" || row.Reason != ReasonStageFailed || row.Class != "needs you" || row.Where != "lane" {
				t.Fatalf("why row = %+v, want held stage-failed needs you / lane", row)
			}
			if !strings.Contains(row.Why, "; run "+wantRun) || !strings.Contains(row.Mover, wantRetry) {
				t.Fatalf("why=%q mover=%q, want run %s and retry command %q", row.Why, row.Mover, wantRun, wantRetry)
			}
		}
		if !found {
			t.Fatal("Why() returned no row for item-b")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		retry, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			StorePath: f.storePath,
			ItemID:    "item-b",
			RunID:     wantRun,
			By:        "operator",
			Why:       "repaired staging condition",
			At:        l.cfg.Now().Add(time.Minute),
		})
		if err != nil {
			return err
		}
		env, err := RetryEnvelope(retry)
		if err != nil {
			return err
		}
		if env.Kind != StageRetryKind {
			t.Fatalf("envelope kind = %q, want %q", env.Kind, StageRetryKind)
		}
		return nil
	}); err != nil {
		t.Fatalf("AuthorizeRetry on repeated staging hold: %v", err)
	}

	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, held := faults["item-b"]; held {
			t.Fatalf("item-b still held after stage retry: %+v", faults["item-b"])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Authorizing a build retry of the item's latest finished run while stage-failed is
// active also clears stage-failed.
func TestBuildRetryOfLatestFinishedRunClearsActiveStageFailedFault(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	at := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:        "run-item-a-a1",
			ItemID:       "item-a",
			LaneID:       "lane-1",
			Backend:      "fixture",
			State:        run.StateFailedToStart,
			StartedAt:    at.Format(time.RFC3339),
			FinishedAt:   at.Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		})
	})
	if err := l.holdItemFault(ctx, Candidate{ItemID: "item-a", RunID: "run-item-a-a1-n2", LaneID: "lane-1"}, ReasonStageFailed, "stage: fork checkout has uncommitted work"); err != nil {
		t.Fatal(err)
	}
	var retry Retry
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var err error
		retry, err = AuthorizeRetry(ctx, tx, RetryRequest{
			StorePath: f.storePath,
			ItemID:    "item-a",
			RunID:     "run-item-a-a1",
			By:        "operator",
			Why:       "restart from scratch",
			At:        at.Add(time.Minute),
		})
		return err
	})
	env, err := RetryEnvelope(retry)
	if err != nil {
		t.Fatal(err)
	}
	if env.Kind != RetryKind {
		t.Fatalf("envelope kind = %q, want %q", env.Kind, RetryKind)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, held := faults["item-a"]; held {
			t.Fatalf("stage-failed fault still active after build retry: %+v", faults["item-a"])
		}
		return nil
	})
}

func TestForkCheckoutRefusalVariantsArePerItemStageFailures(t *testing.T) {
	cases := []error{
		forkCheckoutRefusalf("fork checkout /lanes/lane-1/pi is missing its own Git directory or aliases another checkout"),
		forkCheckoutRefusalf("fork checkout /lanes/lane-1/pi is not an isolated checkout"),
		forkCheckoutRefusalf("fork checkout /lanes/lane-1/pi has uncommitted work; staging will not overwrite it"),
		forkCheckoutRefusalf("fork checkout /lanes/lane-1/pi is on refs/heads/topic, not main; staging will not reset it"),
		forkCheckoutRefusalf("fork checkout /lanes/lane-1/pi has unlanded or divergent commits; staging will not reset it to abc"),
		&forkCheckoutUnretainedError{checkout: "/lanes/lane-1/pi", head: "aaa", tip: "bbb"},
	}
	for _, err := range cases {
		if !isPerItemStageFailure(StageSpec{ItemID: "item-a", RunID: "run-item-a-a1"}, err) {
			t.Errorf("isPerItemStageFailure(%v) = false, want true", err)
		}
		if _, ok := err.(*forkCheckoutRefusalError); ok && ExitCode(err) != record.ExitExternal {
			t.Errorf("ExitCode(%v) = %d, want %d", err, ExitCode(err), record.ExitExternal)
		}
	}
}
