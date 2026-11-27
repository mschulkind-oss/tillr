package drain

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type landingDesignSteps struct {
	recordingSteps
	root      string
	storePath string
	gates     atomic.Int32
	lands     atomic.Int32
	verdicts  atomic.Int32
}

func (s *landingDesignSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	s.gates.Add(1)
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *landingDesignSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands.Add(1)
	if out, err := exec.CommandContext(ctx, "git", "-C", s.root, "update-ref", TrunkRef, c.HeadSHA).CombinedOutput(); err != nil {
		return LandOutcome{}, fmt.Errorf("update-ref %s: %v: %s", TrunkRef, err, out)
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: c.HeadSHA}, nil
}

func (s *landingDesignSteps) Verdict(_ context.Context, _ Candidate, _ string) error {
	s.verdicts.Add(1)
	return nil
}

func (s *landingDesignSteps) ItemState(ctx context.Context, id, _, state string, _ ...item.WaitingOn) error {
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

// Reproduces the 2026-11-27 stall where a finished design run answering a builder's
// conflict became a drain.candidate and released its lane (examination-ended), then
// sat unjudged forever because the standalone gate and land coordinators required
// itemState == Ready while the item stayed blocked waiting on design.
func TestDesignConflictCandidateGatedAndRecordedByCoordinators(t *testing.T) {
	f, l := designConflictFixture(t)
	steps := &landingDesignSteps{root: f.root, storePath: f.storePath}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[{"path":"briefs/item-a.md","questions":[]}]}}'`}
	l.questions = &VantageQuestions{Root: f.root, Command: l.cfg.QuestionsCmd}

	// Dispatch the design seat for item-a's builder conflict.
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch || p.decision.Seat != "design" || p.decision.BlockConflict == nil {
		t.Fatalf("expected design conflict dispatch, got %+v", p.decision)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan design dispatch: ok=%v err=%v", ok, err)
	}
	d := work.Decision
	designRunID := d.RunRequest.RunID

	// The design run amends item-a's block, commits it, and exits 0.
	lane := l.lanePath(d.LaneID)
	runGit(t, lane, "checkout", "-q", "--detach", base)
	writeForkFile(t, filepath.Join(lane, "briefs", "item-a.md"), "# Clarify item-a\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/item-a/\" -->\n- **c1:** the first criterion, clarified by the design seat\n")
	runGit(t, lane, "add", ".")
	runGit(t, lane, "commit", "-qm", "design answer for item-a conflict")
	head := runGit(t, lane, "rev-parse", "HEAD")
	if head == base {
		t.Fatal("design run commit did not advance HEAD")
	}

	at := l.cfg.Now().UTC()
	rec := run.Record{
		RunID:        designRunID,
		ItemID:       "item-a",
		LaneID:       d.LaneID,
		Backend:      d.RunRequest.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    at.Format(time.RFC3339),
		FinishedAt:   at.Format(time.RFC3339),
		EndCause:     run.EndNormalExit,
		ArtifactRefs: []run.ArtifactRef{},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state='idle', run_id=NULL, leased_until=NULL WHERE lane_id=?`, d.LaneID)
		return err
	})

	// Activate both standalone coordinators (gate and land) so the scheduling pass
	// delegates candidate judging to them, matching a running loop.
	l.mu.Lock()
	l.gateLand.Gate.Active = true
	l.gateLand.Land.Active = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.gateLand.Gate.Active = false
		l.gateLand.Land.Active = false
		l.mu.Unlock()
	}()

	ctx := context.Background()
	var result LoopResult
	// Pass 1 settles the finished design run into drain.candidate and releases examination.
	if stop, err := l.pass(ctx, &result); err != nil || stop != "" {
		t.Fatalf("settle pass: stop=%q err=%v", stop, err)
	}
	drainOwned(t, l, &result)

	if candCount := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, candidateReadyKind, designRunID); candCount != 1 {
		t.Fatalf("drain.candidate count for %s = %d, want 1", designRunID, candCount)
	}

	// While nothing is acting on the settled candidate, drain why must name what
	// the candidate is waiting for rather than naming the finished design run as mover.
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		ans, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if len(ans.Rows) != 1 {
			t.Fatalf("why rows = %+v, want 1", ans.Rows)
		}
		row := ans.Rows[0]
		if row.Decision.From != "candidate" || row.Reason != ActionJudge {
			t.Fatalf("why row = %+v, want from=candidate reason=%s", row, ActionJudge)
		}
		if strings.Contains(row.Mover, designRunID) {
			t.Errorf("idle candidate mover names finished run %s: %q", designRunID, row.Mover)
		}
		if !strings.Contains(row.Mover, "waiting for gate") {
			t.Errorf("idle candidate mover = %q, want it to name what it is waiting for (gate)", row.Mover)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Step 4 (gate coordinator) must claim and gate the blocked item's design candidate.
	l.scanAndGate(ctx, &result)
	l.examinations.Wait()
	if steps.gates.Load() != 1 {
		t.Fatalf("gate coordinator ran %d gate(s) for design conflict candidate, want 1", steps.gates.Load())
	}
	if gatedCount := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, candidateGatedKind, designRunID); gatedCount != 1 {
		t.Fatalf("drain.candidate-gated count for %s = %d, want 1", designRunID, gatedCount)
	}

	// Step 5 (land coordinator) must claim, land, and record the block answer.
	l.scanAndLand(ctx, &result)
	l.examinations.Wait()
	for {
		select {
		case comp := <-l.completions:
			if comp.outcome.err != nil || comp.outcome.stop != "" {
				t.Fatalf("coordinator completion: step=%s stop=%q err=%v", comp.step, comp.outcome.stop, comp.outcome.err)
			}
		default:
			goto drained
		}
	}
drained:
	if steps.lands.Load() != 1 {
		t.Fatalf("land coordinator ran %d land(s) for design conflict candidate, want 1", steps.lands.Load())
	}

	// Item leaves the waiting-on-design state, its conflict is recorded as answered,
	// and the planner now dispatches the build seat with the answer.
	c, ok := conflictOf(t, f, "item-a")
	if !ok || c.Status != "answered" || c.WaitingOn != "build" || c.AnswerHead != head {
		t.Fatalf("item-a conflict after design answer = %+v (ok=%v), want answered/build head=%s", c, ok, head)
	}
	if next := decisionFor(t, l, "item-a").decision; next.Action != ActionDispatch || next.Reason != ReasonConflictAnswered || next.BlockConflict != nil {
		t.Fatalf("next plan for item-a after design answer = %+v, want dispatch %s without BlockConflict", next, ReasonConflictAnswered)
	}
}

func TestWhyIdleCandidateNamesWaitInsteadOfFinishedRun(t *testing.T) {
	cases := []struct {
		name      string
		gated     bool
		wantMover string
	}{
		{name: "ungated candidate", gated: false, wantMover: "waiting for gate"},
		{name: "gated candidate", gated: true, wantMover: "waiting for landing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Lanes = []string{"lane-1"}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			runID := "run-item-a-a1"
			now := l.cfg.Now().UTC()
			fixtureWrite(t, f, func(tx *store.Tx) error {
				c := Candidate{
					ItemID:  "item-a",
					RunID:   runID,
					LaneID:  "lane-1",
					BaseSHA: base,
					HeadSHA: base,
				}
				d := Decision{
					Action: ActionDispatch,
					ItemID: "item-a",
					LaneID: "lane-1",
					RunRequest: &run.Request{
						RunID:   runID,
						ItemID:  "item-a",
						LaneID:  "lane-1",
						Backend: "fixture",
					},
				}
				work := pendingWork{
					Decision: d,
					BaseSHA:  base,
				}
				if err := appendEffect(tx, dispatchStartedKind, runID, work, now); err != nil {
					return err
				}
				if err := appendEffect(tx, candidateReadyKind, runID, c, now); err != nil {
					return err
				}
				if tc.gated {
					if err := appendEffect(tx, candidateGatedKind, runID, c, now); err != nil {
						return err
					}
				}
				return nil
			})

			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				ans, err := Why(r, Pass{Now: now.Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
				if err != nil {
					return err
				}
				if len(ans.Rows) != 1 {
					t.Fatalf("why rows = %+v, want 1", ans.Rows)
				}
				row := ans.Rows[0]
				if row.Decision.From != "candidate" {
					t.Fatalf("from = %q, want candidate (%+v)", row.Decision.From, row)
				}
				if strings.Contains(row.Mover, runID) {
					t.Fatalf("idle candidate mover names finished run %s: %q", runID, row.Mover)
				}
				if !strings.Contains(row.Mover, tc.wantMover) {
					t.Fatalf("idle candidate mover = %q, want %q", row.Mover, tc.wantMover)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
