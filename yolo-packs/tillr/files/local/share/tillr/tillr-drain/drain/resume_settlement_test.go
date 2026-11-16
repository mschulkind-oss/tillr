package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// Execute the real claim/resume/finish lifecycle, not a hand-written resume event.
type continuationSteps struct {
	recordingSteps
	t               *testing.T
	store, laneRoot string
	at              time.Time
	gates, lands    int
	process         *ProcessSteps
}

func (s *continuationSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if s.process != nil {
		return s.process.Run(ctx, req)
	}
	db, err := store.Open(ctx, s.store, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer db.Close()
	env, err := run.Exec(ctx, db, req, run.ExecOptions{LaneRoot: s.laneRoot, Now: s.at})
	if err != nil {
		return run.Record{}, "", err
	}
	var rec run.Record
	err = json.Unmarshal(env.Payload, &rec)
	return rec, "", err
}
func (s *continuationSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	db, err := store.Open(ctx, s.store, true)
	if err != nil {
		return GateOutcome{}, err
	}
	defer db.Close()
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, GateVerdictKind, c.RunID, map[string]string{"result": "pass"}, s.at)
	})
	return GateOutcome{Outcome: GatePassed}, err
}
func (s *continuationSteps) Land(_ context.Context, _ Candidate) (LandOutcome, error) {
	s.lands++
	return LandOutcome{Result: LandLanded}, nil
}

func readContinuationWork(t *testing.T, f refusalLoopFixture) *pendingWork {
	t.Helper()
	db := openFixture(t, f.storePath)
	defer db.Close()
	var work *pendingWork
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		work = works["item-a"]
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return work
}

func continuationFixture(t *testing.T, command string) (refusalLoopFixture, *Loop, *continuationSteps, Decision) {
	t.Helper()
	return continuationDispatchFixture(t, command, true)
}

func continuationDispatchFixture(t *testing.T, command string, orphaned bool) (refusalLoopFixture, *Loop, *continuationSteps, Decision) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	dir := l.laneDirPath("lane-1")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(dir, BriefFile), "continue this attempt\n")
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "33b5f2d2-0cd4-4a49-b293-c58f5635561f\n")
	plan := mustPlan(t, l)[0]
	plan.decision.RunRequest.Command = []string{continuationShell(t), "-c", command}
	ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v / %v", ok, err)
	}
	if _, _, err := steps.Run(context.Background(), *plan.decision.RunRequest); err != nil {
		t.Fatal(err)
	}
	if orphaned {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendEffect(tx, loopOrphanedKind, plan.decision.RunRequest.RunID, struct{}{}, steps.at)
		})
	}
	return f, l, steps, plan.decision
}

func settleContinuation(t *testing.T, l *Loop, result *LoopResult) {
	t.Helper()
	stop, err := l.pass(context.Background(), result)
	if err != nil || (stop != "" && stop != StopQueueDrained) {
		t.Fatalf("settlement pass: %s / %v", stop, err)
	}
	drainOwned(t, l, result)
}

func TestResumeReopensExaminedAttemptAndSettlesOnce(t *testing.T) {
	for _, first := range []string{"kill -TERM $$", "exit 0"} {
		t.Run(first, func(t *testing.T) {
			f, l, steps, d := continuationFixture(t, first)
			var result LoopResult
			settleContinuation(t, l, &result)
			before := readContinuationWork(t, f)
			if !before.Done || !before.Orphaned || steps.gates != 0 || steps.lands != 0 {
				t.Fatalf("first segment not examined without judgment: %+v", before)
			}
			req := *d.RunRequest
			req.Command = []string{continuationShell(t), "-c", "echo candidate > candidate.txt; git add candidate.txt; git commit -qm candidate"}
			steps.at = steps.at.Add(time.Second)
			resumed, err := Resume(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps, Now: func() time.Time { return steps.at }}, ResumeSpec{RunID: req.RunID, Request: &req})
			if err != nil || !resumed.Run.Resumed {
				t.Fatalf("resume: %+v / %v", resumed, err)
			}
			work := readContinuationWork(t, f)
			if work.Done || work.Orphaned || work.Candidate != nil {
				t.Fatalf("real continuation did not reopen examination: %+v", work)
			}
			if work.BaseSHA != before.BaseSHA || work.Decision.RunRequest.RunID != d.RunRequest.RunID || work.Decision.RunRequest.Resume {
				t.Fatal("continuation replaced original base/request/attempt")
			}
			for i := 0; i < 3; i++ {
				settleContinuation(t, l, &result)
			}
			if steps.gates != 1 || steps.lands != 1 || result.Dispatched != 0 {
				t.Fatalf("duplicate or missing settlement: gates=%d lands=%d %+v", steps.gates, steps.lands, result)
			}
			db := openFixture(t, f.storePath)
			defer db.Close()
			if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
				var runs, dispatches, retries int
				if err := r.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, dispatchStartedKind).Scan(&dispatches); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, RetryAckKind).Scan(&retries); err != nil {
					return err
				}
				if runs != 1 || dispatches != 1 || retries != 0 {
					t.Errorf("attempt lifecycle duplicated: runs=%d dispatches=%d retries=%d", runs, dispatches, retries)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := Resume(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps}, ResumeSpec{RunID: req.RunID, Request: &req}); err == nil {
				t.Fatal("judged run resumed")
			}
		})
	}
}

// Replay the emitted segment events after examination; this is not a new execution.
func replayContinuation(t *testing.T, f refusalLoopFixture) {
	t.Helper()
	db, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var replay []event.Event
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		events, err := r.Events(0)
		for _, e := range events {
			if e.Kind == run.AllocateKind || e.Kind == run.ResumeKind {
				replay = append(replay, e)
			}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, e := range replay {
			if _, err := tx.Append(e); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResumeMultipleInterruptedSegmentsAndReplay(t *testing.T) {
	f, l, steps, d := continuationFixture(t, "kill -TERM $$")
	var result LoopResult
	settleContinuation(t, l, &result)
	cfg := ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps, Now: func() time.Time { return steps.at }}
	for i := 0; i < 2; i++ {
		steps.at = steps.at.Add(time.Second)
		if _, err := Resume(context.Background(), cfg, ResumeSpec{RunID: d.RunRequest.RunID, Request: d.RunRequest}); err != nil {
			t.Fatal(err)
		}
		if w := readContinuationWork(t, f); w.Done || w.Orphaned {
			t.Fatalf("segment %d did not reopen: %+v", i, w)
		}
		settleContinuation(t, l, &result)
		if w := readContinuationWork(t, f); !w.Done {
			t.Fatalf("segment %d not examined", i)
		}
		replayContinuation(t, f)
		if w := readContinuationWork(t, f); !w.Done {
			t.Fatalf("replay reopened segment %d", i)
		}
	}
	req := *d.RunRequest
	req.Command = []string{continuationShell(t), "-c", "echo candidate > candidate.txt; git add candidate.txt; git commit -qm candidate"}
	steps.at = steps.at.Add(time.Second)
	if _, err := Resume(context.Background(), cfg, ResumeSpec{RunID: req.RunID, Request: &req}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		settleContinuation(t, l, &result)
	}
	replayContinuation(t, f)
	if w := readContinuationWork(t, f); !w.Done {
		t.Fatal("judged replay reopened")
	}
	if steps.gates != 1 || steps.lands != 1 || result.Dispatched != 0 {
		t.Fatalf("settlement: gates=%d lands=%d %+v", steps.gates, steps.lands, result)
	}
}

func TestResumeDoesNotReopenJudgedOrLandedAttempt(t *testing.T) {
	for _, kind := range []string{GateVerdictKind, "land.landing", candidateGatedKind} {
		t.Run(kind, func(t *testing.T) {
			f, l, steps, d := continuationFixture(t, "exit 0")
			var result LoopResult
			settleContinuation(t, l, &result)
			if kind == "land.landing" {
				if err := event.Register(kind, "land"); err != nil {
					t.Fatal(err)
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if kind == candidateGatedKind {
					return appendEffect(tx, kind, d.RunRequest.RunID, Candidate{RunID: d.RunRequest.RunID}, steps.at)
				}
				objectType, objectID := "run", d.RunRequest.RunID
				if kind == "land.landing" {
					objectType, objectID = "item", d.ItemID
				}
				payload, _ := json.Marshal(map[string]string{"run_id": d.RunRequest.RunID, "result": "landed"})
				_, err := tx.Append(event.Event{Kind: kind, ObjectType: objectType, ObjectID: &objectID, Payload: payload, At: steps.at})
				return err
			})
			// Bypass drain.Resume's refusal deliberately: even a real runner event
			// appended after judgment cannot resurrect an already examined attempt.
			req := *d.RunRequest
			req.Resume = true
			steps.at = steps.at.Add(time.Second)
			if _, _, err := steps.Run(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			if w := readContinuationWork(t, f); !w.Done {
				t.Fatalf("%s permitted reopening: %+v", kind, w)
			}
		})
	}
}

func TestResumeKeepsBoundaryFaultAndSupersedesSegmentCandidate(t *testing.T) {
	f, l, steps, d := continuationFixture(t, "exit 0")
	var result LoopResult
	settleContinuation(t, l, &result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := appendEffect(tx, candidateReadyKind, d.RunRequest.RunID, Candidate{RunID: d.RunRequest.RunID, HeadSHA: "stale"}, steps.at); err != nil {
			return err
		}
		return appendEffect(tx, BoundaryCheckKind, d.RunRequest.RunID, boundaryCheckPayload{Clean: false, Runs: []runRef{{RunID: d.RunRequest.RunID}}}, steps.at)
	})
	steps.at = steps.at.Add(time.Second)
	if _, err := Resume(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Steps: steps, Now: func() time.Time { return steps.at }}, ResumeSpec{RunID: d.RunRequest.RunID, Request: d.RunRequest}); err != nil {
		t.Fatal(err)
	}
	w := readContinuationWork(t, f)
	if w.Done || w.Orphaned || w.Examining || w.Owner != nil || w.Candidate != nil || !w.BoundaryFailed {
		t.Fatalf("segment supersession lost attempt safety: %+v", w)
	}
	if got := mustPlan(t, l)[0].decision.Reason; got != StopHostBoundaryViolation {
		t.Fatalf("resume cleared boundary fault: %s", got)
	}
}
