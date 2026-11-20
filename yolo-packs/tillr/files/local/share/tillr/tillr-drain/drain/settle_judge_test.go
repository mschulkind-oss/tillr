package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

type countingJudgeSteps struct {
	recordingSteps
	storePath string
	gates     atomic.Int32
	lands     atomic.Int32
	verdicts  atomic.Int32
}

func (s *countingJudgeSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	s.gates.Add(1)
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *countingJudgeSteps) Land(_ context.Context, _ Candidate) (LandOutcome, error) {
	s.lands.Add(1)
	return LandOutcome{Result: LandLanded}, nil
}

func (s *countingJudgeSteps) Verdict(_ context.Context, _ Candidate, _ string) error {
	s.verdicts.Add(1)
	return nil
}

func (s *countingJudgeSteps) ItemState(ctx context.Context, id, _, state string, _ ...item.WaitingOn) error {
	if s.storePath == "" {
		return nil
	}
	st, err := store.Open(ctx, s.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, state, id)
		return err
	})
}

func stageFinishedCandidateOnLane(t *testing.T, f refusalLoopFixture, l *Loop, itemID, laneID string) Decision {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]' WHERE item_id = ?`, itemID)
		return err
	})
	var target *plannedApplication
	plans := mustPlan(t, l)
	for i := range plans {
		if plans[i].decision.ItemID == itemID {
			target = &plans[i]
			break
		}
	}
	if target == nil || target.decision.Action != ActionDispatch || target.decision.LaneID != laneID {
		t.Fatalf("unexpected dispatch plan for %s: %+v", itemID, target)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), *target, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %s: %v / %v", itemID, ok, err)
	}
	lane := l.lanePath(laneID)
	writeForkFile(t, filepath.Join(l.laneDirPath(laneID), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath(laneID), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate "+itemID+"\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate "+itemID)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:        target.decision.RunRequest.RunID,
			ItemID:       itemID,
			LaneID:       laneID,
			Backend:      "fixture",
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    l.cfg.Now().Format(time.RFC3339),
			FinishedAt:   l.cfg.Now().Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		})
	})
	return target.decision
}

func TestOnePassTakesFinishedRunThroughSettleGateAndLand(t *testing.T) {
	f, l := passFixture(t, "item-a")
	steps := &countingJudgeSteps{storePath: f.storePath}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-1"}
	stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")

	var result LoopResult
	stop, err := l.pass(context.Background(), &result)
	if err != nil || stop != "" {
		t.Fatalf("pass: stop=%q err=%v", stop, err)
	}
	drainOwned(t, l, &result)

	if result.Candidates != 1 || result.Landed != 1 {
		t.Fatalf("expected one pass to settle and land candidate: %+v", result)
	}
	if steps.gates.Load() != 1 || steps.lands.Load() != 1 || steps.verdicts.Load() != 1 {
		t.Fatalf("expected 1 gate, 1 land, 1 verdict; got gates=%d lands=%d verdicts=%d",
			steps.gates.Load(), steps.lands.Load(), steps.verdicts.Load())
	}

	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id = 'item-a'`).Scan(&state); err != nil {
			return err
		}
		if state != "landed" {
			t.Fatalf("item state = %q, want landed", state)
		}
		var laneState string
		if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id = 'lane-1'`).Scan(&laneState); err != nil {
			return err
		}
		if laneState != run.LaneStateIdle {
			t.Fatalf("lane state = %q, want %q", laneState, run.LaneStateIdle)
		}
		rows, err := r.Query(`SELECT action, COALESCE(pass_id, '') FROM dispatches WHERE item_id = 'item-a' ORDER BY rowid`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var actions []string
		var passIDs []string
		for rows.Next() {
			var action, passID string
			if err := rows.Scan(&action, &passID); err != nil {
				return err
			}
			actions = append(actions, action)
			passIDs = append(passIDs, passID)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if !slices.Equal(actions, []string{ActionDispatch, ActionSettle, ActionJudge}) {
			t.Fatalf("dispatch actions = %v, want [dispatch settle judge]", actions)
		}
		if passIDs[1] == "" || passIDs[1] != passIDs[2] {
			t.Fatalf("settle and judge did not share the same pass_id: %v", passIDs)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestHoldRecordedBeforeGateStartsStopsSamePassGate(t *testing.T) {
	t.Run("fault-recorded-during-settle", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		steps := &countingJudgeSteps{storePath: f.storePath}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-1"}
		dispatched := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")

		var recorded atomic.Bool
		l.cfg.Report = writerFunc(func(p []byte) (int, error) {
			if strings.Contains(string(p), " spent ") && recorded.CompareAndSwap(false, true) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					id := "item-a"
					body, err := json.Marshal(ItemFault{
						ItemID: id,
						RunID:  dispatched.RunRequest.RunID,
						LaneID: "lane-1",
						Reason: StopGateHarnessError,
						Detail: "operator hold recorded before gate",
					})
					if err != nil {
						return err
					}
					_, err = tx.Append(event.Event{Kind: itemFaultKind, ObjectType: "item", ObjectID: &id, Payload: body, At: l.cfg.Now().UTC()})
					return err
				})
			}
			return len(p), nil
		})

		var result LoopResult
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)

		if !recorded.Load() {
			t.Fatal("hold was not recorded during settlement")
		}
		if result.Landed != 0 || steps.gates.Load() != 0 || steps.lands.Load() != 0 {
			t.Fatalf("gate or land ran despite hold recorded before gate: result=%+v gates=%d lands=%d",
				result, steps.gates.Load(), steps.lands.Load())
		}
		assertCandidateRecordedNotDone(t, f.storePath, "item-a")

		plans := mustPlan(t, l)
		if plans[0].decision.Action != ActionHold || plans[0].decision.Reason != StopGateHarnessError {
			t.Fatalf("planner did not hold candidate on recorded fault: %+v", plans[0].decision)
		}

		// Clearing the fault lets the next pass gate and land the retained candidate.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			faults, err := ReadItemFaults(tx.Reader())
			if err != nil {
				return err
			}
			return ClearItemFault(tx, "item-a", faults["item-a"].Seq, l.cfg.Now().UTC())
		})
		stop, err = l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("resume pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)
		if result.Landed != 1 || steps.gates.Load() != 1 || steps.lands.Load() != 1 {
			t.Fatalf("clearing hold did not gate and land candidate: result=%+v gates=%d lands=%d",
				result, steps.gates.Load(), steps.lands.Load())
		}
	})

	t.Run("judge-grant-absent-hold", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		steps := &countingJudgeSteps{storePath: f.storePath}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-1"}
		stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET judge_mode = 'agent' WHERE item_id = 'item-a'`)
			return err
		})

		var result LoopResult
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)

		if result.Landed != 0 || steps.gates.Load() != 0 {
			t.Fatalf("judge-grant-absent candidate gated in same pass: result=%+v gates=%d",
				result, steps.gates.Load())
		}
		assertCandidateRecordedNotDone(t, f.storePath, "item-a")
		plans := mustPlan(t, l)
		if plans[0].decision.Action != ActionHold || plans[0].decision.Reason != ReasonJudgeGrantAbsent {
			t.Fatalf("expected %s hold from planner, got %+v", ReasonJudgeGrantAbsent, plans[0].decision)
		}
	})
}

func TestWaitingInteractiveRequestForLaneReturnsToPlannerBeforeGate(t *testing.T) {
	t.Run("express-request-waiting-for-lane", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		steps := &countingJudgeSteps{storePath: f.storePath}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-1"}
		l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
		stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")

		// File a default (interactive/express) request while lane-1 is occupied by item-a.
		expressID := fileFixtureRequestWith(t, f, "Interactive request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"}, false)

		var result LoopResult
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)

		if result.Landed != 0 || steps.gates.Load() != 0 || steps.lands.Load() != 0 {
			t.Fatalf("gate or land ran while interactive request %s waited for lane-1: result=%+v gates=%d lands=%d",
				expressID, result, steps.gates.Load(), steps.lands.Load())
		}
		assertCandidateRecordedNotDone(t, f.storePath, "item-a")
	})

	t.Run("comment-waiting-for-document-desk", func(t *testing.T) {
		const docPath = "docs/design/doc.md"
		desk := commentDesk(docPath)
		f, l := passFixture(t, "item-design", "item-comments-doc-1")
		steps := &countingJudgeSteps{storePath: f.storePath}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-1"}
		l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
		l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
		l.cfg.QuestionsCmd = []string{"sh", "-c", "printf '[]'"}

		if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(f.root, docPath), "# Design\n")
		runGit(t, f.root, "add", docPath)
		runGit(t, f.root, "commit", "-qm", "add design doc")
		deskWorktree := filepath.Join(f.laneRoot, desk, WorktreeDir)
		if err := os.MkdirAll(filepath.Dir(deskWorktree), 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, f.root, "worktree", "add", "-q", "-B", "test/"+desk, deskWorktree, TrunkRef)

		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET seat = 'design', allowed_paths = '["docs/design/doc.md","candidate.txt"]' WHERE item_id = 'item-design'`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE items SET state = 'closed', design_doc = ?, allowed_paths = '["docs/design/doc.md"]' WHERE item_id = 'item-comments-doc-1'`, docPath)
			return err
		})

		var target *plannedApplication
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == "item-design" {
				cp := p
				target = &cp
				break
			}
		}
		if target == nil || target.decision.Action != ActionDispatch || target.decision.LaneID != desk {
			t.Fatalf("unexpected dispatch plan for item-design: %+v", target)
		}
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		ok, _, err := l.applyPlan(context.Background(), *target, base, l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("dispatch item-design: %v / %v", ok, err)
		}
		writeForkFile(t, filepath.Join(l.laneDirPath(desk), FenceFile), "docs/design/doc.md\ncandidate.txt\n")
		writeForkFile(t, filepath.Join(l.laneDirPath(desk), BaseSHAFile), base+"\n")
		writeForkFile(t, filepath.Join(deskWorktree, "candidate.txt"), "candidate\n")
		runGit(t, deskWorktree, "add", "candidate.txt")
		runGit(t, deskWorktree, "commit", "-qm", "candidate")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := applyTerminal(tx, run.Record{
				RunID:        target.decision.RunRequest.RunID,
				ItemID:       "item-design",
				LaneID:       desk,
				Backend:      "fixture",
				State:        run.StateExited,
				ExitCode:     countPtr(0),
				StartedAt:    l.cfg.Now().Format(time.RFC3339),
				FinishedAt:   l.cfg.Now().Format(time.RFC3339),
				ArtifactRefs: []run.ArtifactRef{},
			}); err != nil {
				return err
			}
			// Queue the interactive review comment for the same document desk before settlement runs.
			_, err := tx.Exec(`UPDATE items SET state = 'in-queue' WHERE item_id = 'item-comments-doc-1'`)
			return err
		})

		var result LoopResult
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)

		if result.Landed != 0 || steps.gates.Load() != 0 {
			t.Fatalf("gate ran on %s despite waiting comment request: result=%+v gates=%d",
				desk, result, steps.gates.Load())
		}
		assertCandidateRecordedNotDone(t, f.storePath, "item-design")
	})
}

func assertCandidateRecordedNotDone(t *testing.T, storePath, itemID string) {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		w := works[itemID]
		if w == nil || w.Candidate == nil || w.Done {
			t.Fatalf("expected settled pending candidate for %s, got %+v", itemID, w)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
