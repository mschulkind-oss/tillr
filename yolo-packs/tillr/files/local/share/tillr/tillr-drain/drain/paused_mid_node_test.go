package drain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type pausedMidNodeSteps struct {
	*judgeRouteSteps
	pauseTarget   string
	pausedOnce    bool
	pauseSession  string
	resumeSession string
	resumedReqs   []run.Request
	blockItem     string
	blockEntered  chan struct{}
	unblock       chan struct{}
}

func (s *pausedMidNodeSteps) shouldPause(req run.Request) bool {
	if s.pausedOnce {
		return false
	}
	switch s.pauseTarget {
	case "evaluate":
		return req.NodeID == "evaluate"
	case "judge":
		return req.Node == "judge" || strings.HasSuffix(req.RunID, "-judge")
	case "worker":
		return req.NodeID == "second"
	default:
		return false
	}
}

func (s *pausedMidNodeSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	if req.Resume {
		s.resumedReqs = append(s.resumedReqs, req)
		session, err := ReadSessionID(laneDir)
		if err != nil {
			return run.Record{}, "", err
		}
		s.resumeSession = session
	}
	if s.shouldPause(req) {
		s.pausedOnce = true
		s.runs = append(s.runs, req)
		session, err := ReadSessionID(laneDir)
		if err != nil {
			return run.Record{}, "", err
		}
		if session == "" {
			session = "paused-session-" + req.RunID
			writeForkFile(s.t, filepath.Join(laneDir, SessionIDFile), session+"\n")
		}
		s.pauseSession = session
		evalPath := filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output")
		if _, err := os.Stat(evalPath); err == nil {
			return run.Record{}, "", os.ErrExist
		}
		rec := run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       req.LaneID,
			Backend:      req.Backend,
			State:        run.StatePaused,
			EndCause:     run.EndRestart,
			StartedAt:    "2026-11-23T05:20:00Z",
			FinishedAt:   "2026-11-23T05:24:00Z",
			ArtifactRefs: []run.ArtifactRef{},
		}
		st, err := store.Open(ctx, s.f.storePath, true)
		if err != nil {
			return rec, "", err
		}
		defer st.Close()
		err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "", err
	}
	// Stage a worker session id so worker nodes can also resume cleanly when paused.
	if session, _ := ReadSessionID(laneDir); session == "" {
		writeForkFile(s.t, filepath.Join(laneDir, SessionIDFile), "session-"+req.RunID+"\n")
	}
	if req.ItemID != "item-a" {
		s.runs = append(s.runs, req)
		if req.ItemID == s.blockItem && s.unblock != nil {
			if s.blockEntered != nil {
				select {
				case s.blockEntered <- struct{}{}:
				default:
				}
			}
			select {
			case <-s.unblock:
			case <-ctx.Done():
				return run.Record{}, "", ctx.Err()
			}
		}
		target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
		dir := filepath.Join(target, "tools", req.ItemID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(filepath.Join(dir, "result.txt"), []byte("built "+req.RunID+"\n"), 0644); err != nil {
			return run.Record{}, "", err
		}
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build "+req.ItemID)
		zero := 0
		rec := run.Record{
			RunID:      req.RunID,
			ItemID:     req.ItemID,
			LaneID:     req.LaneID,
			Backend:    req.Backend,
			State:      run.StateExited,
			ExitCode:   &zero,
			StartedAt:  "2026-11-23T05:30:00Z",
			FinishedAt: "2026-11-23T05:31:00Z",
		}
		st, err := store.Open(ctx, s.f.storePath, true)
		if err != nil {
			return rec, "", err
		}
		defer st.Close()
		err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "RESULT: done\n", err
	}
	return s.judgeRouteSteps.Run(ctx, req)
}

func TestPausedRunMidNodeWaitsForFreeLaneAndResumesWithoutJudgeUnreadable(t *testing.T) {
	for _, mode := range []string{"evaluate", "judge", "worker"} {
		t.Run(mode, func(t *testing.T) {
			f, l, base := judgeRouteFixture(t, "pass")
			steps := &pausedMidNodeSteps{judgeRouteSteps: base, pauseTarget: mode}
			l.cfg.Steps = steps
			l.cfg.Lanes = []string{"lane-2"}
			l.cfg.StorePollInterval = 5 * time.Millisecond

			if mode != "judge" {
				writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
				runGit(t, f.root, "add", "config/tillr/workflows")
				runGit(t, f.root, "commit", "-qm", "evaluator workflow")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='' WHERE item_id='item-a'`)
					return err
				})
			}

			ctx := context.Background()
			result := &LoopResult{}
			for i := 0; i < 6 && !steps.pausedOnce; i++ {
				if _, err := l.pass(ctx, result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if !steps.pausedOnce {
				t.Fatalf("target %s never paused; runs=%+v", mode, steps.runs)
			}
			if result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
				t.Fatalf("mid-node pause recorded judge-unreadable on pause pass: %+v", result.RefusedSteps)
			}

			// Simulate scaling down to lane-1 (removing leftover lane-2) while lane-1 is busy
			// and item-b is queued ahead of item-a.
			l.cfg.Lanes = []string{"lane-1"}
			at := l.cfg.Now()
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('lane-1', 'leased', 'busy-run', ?)
					ON CONFLICT(lane_id) DO UPDATE SET state='leased', run_id='busy-run', leased_until=excluded.leased_until`,
					at.Add(time.Hour).UnixMilli()); err != nil {
					return err
				}
				_, err := tx.Exec(`UPDATE items SET state='in-queue', rank='0', workflow='', judge_mode='' WHERE item_id='item-b'`)
				return err
			})

			runsBeforeBlockedPass := len(steps.runs)
			if _, err := l.pass(ctx, result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)

			if result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
				t.Fatalf("next pass held paused run as judge-unreadable: %+v", result.RefusedSteps)
			}
			if len(steps.runs) != runsBeforeBlockedPass || len(steps.resumedReqs) != 0 {
				t.Fatalf("paused run resumed before a lane freed: runs=%d->%d resumed=%+v", runsBeforeBlockedPass, len(steps.runs), steps.resumedReqs)
			}

			got := decisionFor(t, l, "item-a").decision
			if got.Action != ActionHold || got.Reason != ReasonNoLane {
				t.Fatalf("want item-a held on %s while lane-1 is busy, got %+v", ReasonNoLane, got)
			}

			lanes := []string{"lane-1"}
			fixtureRead(t, f, func(r *store.Reader) error {
				rows, err := r.Query(`SELECT kind, payload FROM events ORDER BY seq`)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var kind, payload string
					if err := rows.Scan(&kind, &payload); err != nil {
						return err
					}
					if kind == RefusalKind || strings.Contains(kind, "fault") {
						t.Fatalf("unexpected refusal/fault event after pause: %s %s", kind, payload)
					}
				}
				if err := rows.Err(); err != nil {
					return err
				}
				answer, err := Why(r, Pass{AvailableLanes: &lanes, Now: at.Format(time.RFC3339Nano), Build: &l.build}, l.cfg.Policy, &VantageQuestions{Root: f.root})
				if err != nil {
					return err
				}
				for _, row := range answer.Rows {
					if row.ItemID == "item-a" {
						if row.Reason != ReasonNoLane || !slices.Contains(row.WaitingOn, "lane-1") {
							t.Fatalf("unexpected why row for paused item-a: %+v", row)
						}
					}
				}
				return nil
			})

			// Free lane-1: the paused run on lane-2 must resume before fresh item-b claims lane-1,
			// continue the same session, finish judging, and land item-a.
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE lanes SET state='idle', run_id=NULL, leased_until=NULL WHERE lane_id='lane-1'`)
				return err
			})

			for i := 0; i < 8; i++ {
				if _, err := l.pass(ctx, result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
				var state string
				fixtureRead(t, f, func(r *store.Reader) error {
					return r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state)
				})
				if state == "landed" {
					break
				}
			}

			if result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
				t.Fatalf("judge-unreadable recorded during resume/settle: %+v", result.RefusedSteps)
			}
			if len(steps.resumedReqs) != 1 || !steps.resumedReqs[0].Resume {
				t.Fatalf("expected exactly 1 resumed continuation, got %+v", steps.resumedReqs)
			}
			if steps.pauseSession == "" || steps.resumeSession != steps.pauseSession {
				t.Fatalf("continuation lost session id: paused=%q resumed=%q", steps.pauseSession, steps.resumeSession)
			}
			// Verify item-a resumed ahead of higher-ranked fresh item-b when the single lane freed.
			resumeIdx, itemBIdx := -1, -1
			for i, req := range steps.runs {
				if req.ItemID == "item-a" && req.Resume && resumeIdx < 0 {
					resumeIdx = i
				}
				if req.ItemID == "item-b" && itemBIdx < 0 {
					itemBIdx = i
				}
			}
			if resumeIdx < 0 || (itemBIdx >= 0 && itemBIdx < resumeIdx) {
				t.Fatalf("fresh item-b ran before paused item-a resumed: runs=%+v", steps.runs)
			}

			fixtureRead(t, f, func(r *store.Reader) error {
				var state string
				if err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
					return err
				}
				if state != "landed" {
					t.Fatalf("item-a state=%q, want landed", state)
				}
				return nil
			})
		})
	}
}

func TestPausedRunWaitsForLiveDrainingWorkerAboveLaneCount(t *testing.T) {
	f, l, base := judgeRouteFixture(t, "pass")
	steps := &pausedMidNodeSteps{
		judgeRouteSteps: base,
		pauseTarget:     "evaluate",
		blockItem:       "item-b",
		blockEntered:    make(chan struct{}, 1),
		unblock:         make(chan struct{}),
	}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-2", "lane-3"}
	l.cfg.StorePollInterval = 5 * time.Millisecond

	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', judge_mode='', rank='a' WHERE item_id='item-a'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='in-queue', rank='b', workflow='', judge_mode='' WHERE item_id='item-b'`)
		return err
	})

	ctx := context.Background()
	result := &LoopResult{}

	// Pass 1 dispatches item-a on lane-2 and item-b on lane-3 (item-b stays live on lane-3).
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	<-steps.blockEntered

	// Advance item-a on lane-2 until its evaluate node pauses, while item-b remains live on lane-3.
	for i := 0; i < 6 && !steps.pausedOnce; i++ {
		for len(l.active) > 1 {
			select {
			case c := <-l.completions:
				if stop, err := l.takeCompletion(c, result); stop != "" || err != nil {
					t.Fatalf("completion %s / %v", stop, err)
				}
			case <-time.After(hangGuard):
				t.Fatal("timed out waiting for item-a step completion")
			}
		}
		if steps.pausedOnce {
			break
		}
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
	}
	for len(l.active) > 1 {
		c := <-l.completions
		if stop, err := l.takeCompletion(c, result); stop != "" || err != nil {
			t.Fatalf("completion %s / %v", stop, err)
		}
	}
	if !steps.pausedOnce {
		t.Fatalf("evaluate never paused; runs=%+v", steps.runs)
	}

	// Scale down to ["lane-1"] (idle): lane-3 still has live draining item-b (1/1 pool capacity),
	// so paused item-a on lane-2 must wait on lane-3 rather than resuming early.
	l.cfg.Lanes = []string{"lane-1"}
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatal(err)
	}
	if len(steps.resumedReqs) != 0 {
		t.Fatalf("paused item-a resumed while draining lane-3 still filled the 1-lane cap: %+v", steps.resumedReqs)
	}
	lanes := []string{"lane-1"}
	at := l.cfg.Now()
	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{AvailableLanes: &lanes, Now: at.Format(time.RFC3339Nano), Build: &l.build}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == "item-a" {
				if row.Reason != ReasonNoLane || !slices.Contains(row.WaitingOn, "lane-3") {
					t.Fatalf("expected item-a waiting on live draining lane-3, got %+v", row)
				}
			}
		}
		return nil
	})

	// Finish item-b on lane-3, then verify item-a resumes and lands without judge-unreadable.
	close(steps.unblock)
	drainOwned(t, l, result)
	for i := 0; i < 8; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
		var state string
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state)
		})
		if state == "landed" {
			break
		}
	}
	if result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
		t.Fatalf("unexpected judge-unreadable: %+v", result.RefusedSteps)
	}
	if len(steps.resumedReqs) != 1 {
		t.Fatalf("expected 1 resumed continuation, got %+v", steps.resumedReqs)
	}
}
