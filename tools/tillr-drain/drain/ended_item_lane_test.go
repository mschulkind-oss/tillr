package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type testWriteSteps struct {
	*workflowSteps
	proposal     string
	testWriteRun string
	commitFirst  bool
	nextItemRuns int
}

func (s *testWriteSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	report := "RESULT: done\n"
	if req.ItemID == "item-a" {
		id := strings.SplitN(req.RunID[strings.LastIndex(req.RunID, "-n")+2:], "-", 2)[0]
		switch id {
		case "1":
			proposalArtifact(s.t, s.f.root, req.RunID, s.proposal)
		case "2":
			s.testWriteRun = req.RunID
			if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
				return run.Record{}, "", err
			}
			if s.commitFirst {
				writeForkFile(s.t, filepath.Join(target, "tools/item-a/setup.go"), "package itema\n")
				runGit(s.t, target, "add", "tools/item-a/setup.go")
				runGit(s.t, target, "commit", "-qm", "setup before handoff")
			}
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/failing_test.go"), "package itema\n// uncommitted test-write handoff\n")
			report = "Completed the test-write node. The changes are uncommitted as directed.\nRESULT: done\n"
		}
	} else {
		s.nextItemRuns++
	}
	path := filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return run.Record{}, "", err
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    "2026-11-22T00:00:00Z",
		FinishedAt:   "2026-11-22T00:00:00Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestClosingItemAfterTestWriteUncommittedWorkRestoresLane(t *testing.T) {
	f, l, baseSteps := plannedFixture(t)
	l.cfg.Policy.Seats["test-write"] = "fixture"
	steps := &testWriteSteps{
		workflowSteps: baseSteps,
		proposal: pieceProposal([]map[string]any{
			{"id": "tests", "seat": "test-write", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "write failing test, leave uncommitted"},
			{"id": "impl", "seat": "build", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "implement fix"},
		}),
	}
	l.cfg.Steps = steps
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	ctx := context.Background()

	// Run plan (n1) and test-write (n2). test-write leaves uncommitted files in lane-1.
	for i := 0; i < 6 && steps.testWriteRun == ""; i++ {
		var r LoopResult
		if stop, err := l.pass(ctx, &r); err != nil || stop != "" {
			t.Fatalf("pass %d: stop=%q err=%v", i, stop, err)
		}
	}
	if steps.testWriteRun == "" {
		t.Fatal("test-write step never ran")
	}
	// Let the test-write completion settle.
	var r LoopResult
	for i := 0; i < 6 && (i == 0 || l.activeCount() > 0); i++ {
		if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
			t.Fatalf("settle test-write %d: stop=%q err=%v", i, stop, err)
		}
	}
	if l.activeCount() > 0 {
		t.Fatalf("test-write examination still active after settle passes")
	}
	laneWT := l.lanePath("lane-1")
	if status := runGit(t, laneWT, "status", "--porcelain", "--untracked-files=all"); !strings.Contains(status, "failing_test.go") {
		t.Fatalf("expected uncommitted test-write file in lane-1, got status=%q", status)
	}

	// Close item-a (the operator closes it as superseded). No other item takes lane-1 yet,
	// matching the 2026-11-21 incident where lane-4 sat idle until later offered work.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, "item-a", "superseded by ruling", "owner", l.cfg.Now())
		return err
	})

	// Next pass observes item-a closed: should save leftover work and restore lane-1.
	if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
		t.Fatalf("pass after close: stop=%q err=%v", stop, err)
	}
	if err := l.pruneRunRefs(ctx); err != nil {
		t.Fatalf("pruneRunRefs: %v", err)
	}

	// Now item-b becomes ready and is staged onto lane-1.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id='item-b'`)
		return err
	})

	// Staging the next item onto lane-1 must NOT refuse with lane-holds-uncommitted-work.
	stager := l.cfg.Stage.(*LaneStager)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if err := stager.Stage(ctx, StageSpec{
		ItemID:         "item-b",
		RunID:          "run-item-b-a1",
		LaneID:         "lane-1",
		Backend:        "fixture",
		WorkingDir:     WorktreeDir,
		BaseSHA:        base,
		RetryCandidate: &RetryCandidate{RunID: "run-prev", BaseSHA: base, HeadSHA: base},
	}); err != nil {
		if refusal := asLaneRefusal(err); refusal != nil && refusal.Reason == ReasonLaneUncommittedWork {
			t.Fatalf("lane-1 refused staging as %s after item-a closed: %v", refusal.Reason, err)
		}
		t.Fatalf("staging next item on lane-1 failed: %v", err)
	}

	// Verify lane-1's worktree was restored (clean) and the leftover work was saved under
	// a named ref recorded in an event that survived ref sweeping.
	if status := runGit(t, laneWT, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("lane-1 worktree still dirty after close: %q", status)
	}
	var savedRef string
	fixtureRead(t, f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = 'drain.ended-work-saved' AND object_type = 'run' AND object_id = ?`, steps.testWriteRun)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var p struct {
				ItemID    string `json:"item_id"`
				ItemState string `json:"item_state"`
				LaneID    string `json:"lane_id"`
				Ref       string `json:"ref"`
			}
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			if p.ItemID != "item-a" || p.ItemState != "closed" || p.LaneID != "lane-1" || p.Ref == "" {
				t.Errorf("unexpected ended-work-saved payload: %+v", p)
			}
			savedRef = p.Ref
		}
		return rows.Err()
	})
	if savedRef == "" {
		t.Fatalf("expected drain.ended-work-saved event for %s", steps.testWriteRun)
	}
	if got := runGit(t, f.root, "show", savedRef+":tools/item-a/failing_test.go"); !strings.Contains(got, "uncommitted test-write handoff") {
		t.Fatalf("saved ref %s missing test-write content: %q", savedRef, got)
	}
}

func TestAllTerminalEndingsSaveUncommittedWorkAndRestoreLane(t *testing.T) {
	head := func(t *testing.T, root string) string {
		t.Helper()
		return runGit(t, root, "rev-parse", TrunkRef)
	}
	cases := []struct {
		name      string
		wantState string
		finish    func(t *testing.T, f refusalLoopFixture, l *Loop)
	}{
		{
			name:      "closed",
			wantState: "closed",
			finish: func(t *testing.T, f refusalLoopFixture, l *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := item.Close(context.Background(), tx, "item-a", "superseded", "owner", l.cfg.Now())
					return err
				})
			},
		},
		{
			name:      "resolved-elsewhere",
			wantState: StateResolvedElsewhere,
			finish: func(t *testing.T, f refusalLoopFixture, l *Loop) {
				sha := head(t, f.root)
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := item.SetStateWithReceipt(context.Background(), tx, "item-a", StateResolvedElsewhere, l.cfg.Now(), item.MoveReceipt{
						Reason:      "covered by another commit",
						By:          "owner",
						ResolvesVia: "commit:" + sha,
					})
					return err
				})
			},
		},
		{
			name:      "landed",
			wantState: StateLanded,
			finish: func(t *testing.T, f refusalLoopFixture, l *Loop) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-a'`, StateLanded)
					return err
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, l, baseSteps := plannedFixture(t)
			l.cfg.Policy.Seats["test-write"] = "fixture"
			steps := &testWriteSteps{
				workflowSteps: baseSteps,
				proposal: pieceProposal([]map[string]any{
					{"id": "tests", "seat": "test-write", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "write failing test, leave uncommitted"},
					{"id": "impl", "seat": "build", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "implement fix"},
				}),
			}
			l.cfg.Steps = steps
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
			ctx := context.Background()

			for i := 0; i < 6 && steps.testWriteRun == ""; i++ {
				var r LoopResult
				if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
					t.Fatalf("pass %d: stop=%q err=%v", i, stop, err)
				}
			}
			var r LoopResult
			for i := 0; i < 6 && (i == 0 || l.activeCount() > 0); i++ {
				if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
					t.Fatalf("settle %d: stop=%q err=%v", i, stop, err)
				}
			}
			if l.activeCount() > 0 {
				t.Fatalf("examination still active after settle passes")
			}

			tc.finish(t, f, l)

			if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
				t.Fatalf("pass after %s: stop=%q err=%v", tc.name, stop, err)
			}
			if err := l.pruneRunRefs(ctx); err != nil {
				t.Fatalf("pruneRunRefs: %v", err)
			}

			var saved EndedWorkSaved
			fixtureRead(t, f, func(rd *store.Reader) error {
				var raw []byte
				if err := rd.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, endedWorkSavedKind, steps.testWriteRun).Scan(&raw); err != nil {
					return err
				}
				return json.Unmarshal(raw, &saved)
			})
			if saved.ItemID != "item-a" || saved.ItemState != tc.wantState || saved.LaneID != "lane-1" || saved.Ref != endedWorkPrefix+steps.testWriteRun {
				t.Fatalf("unexpected saved record: %+v", saved)
			}
			if got := runGit(t, f.root, "show", saved.Ref+":tools/item-a/failing_test.go"); !strings.Contains(got, "uncommitted test-write handoff") {
				t.Fatalf("saved ref %s missing content: %q", saved.Ref, got)
			}
			if status := runGit(t, l.lanePath("lane-1"), "status", "--porcelain", "--untracked-files=all"); status != "" {
				t.Fatalf("lane-1 still dirty after %s: %q", tc.name, status)
			}

			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id='item-b'`)
				return err
			})
			stager := l.cfg.Stage.(*LaneStager)
			base := head(t, f.root)
			if err := stager.Stage(ctx, StageSpec{
				ItemID:         "item-b",
				RunID:          "run-item-b-a1",
				LaneID:         "lane-1",
				Backend:        "fixture",
				WorkingDir:     WorktreeDir,
				BaseSHA:        base,
				RetryCandidate: &RetryCandidate{RunID: "run-prev", BaseSHA: base, HeadSHA: base},
			}); err != nil {
				t.Fatalf("staging next item after %s failed: %v", tc.name, err)
			}
		})
	}
}

func TestWorkflowReleaseAfterItemClosedSavesUncommittedHandoff(t *testing.T) {
	f, l, baseSteps := plannedFixture(t)
	l.cfg.Policy.Seats["test-write"] = "fixture"
	steps := &testWriteSteps{
		workflowSteps: baseSteps,
		commitFirst:   true,
		proposal: pieceProposal([]map[string]any{
			{"id": "tests", "seat": "test-write", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "write setup and leave failing test uncommitted"},
			{"id": "impl", "seat": "build", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "implement fix"},
		}),
	}
	l.cfg.Steps = steps
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	ctx := context.Background()

	// Run plan (n1) and test-write (n2). Pause live handoff so item-a can be closed
	// immediately after n2 completes (before n2 is settled or n3 runs), forcing the
	// workflow-release path to save the uncommitted handoff and clean lane-1.
	l.pollOnly = true
	var r LoopResult
	for steps.testWriteRun == "" {
		stop, err := l.pass(ctx, &r)
		if err != nil || stop != "" {
			t.Fatalf("advance to n2: stop=%q err=%v", stop, err)
		}
		if l.activeCount() > 0 {
			c := <-l.completions
			l.completions <- c
		}
	}
	// Close item-a immediately after n2 completed (before n2 settles / n3 runs).
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, "item-a", "no longer needed", "owner", l.cfg.Now())
		return err
	})
	l.pollOnly = false
	for {
		stop, err := l.pass(ctx, &r)
		if err != nil {
			t.Fatalf("release pass: %v", err)
		}
		if stop == StopQueueDrained {
			break
		}
		if stop != "" {
			t.Fatalf("release pass: unexpected stop=%q", stop)
		}
	}
	if err := l.pruneRunRefs(ctx); err != nil {
		t.Fatalf("pruneRunRefs: %v", err)
	}
	var saved EndedWorkSaved
	fixtureRead(t, f, func(rd *store.Reader) error {
		var raw []byte
		if err := rd.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND json_extract(payload, '$.item_id') = 'item-a' ORDER BY seq DESC LIMIT 1`, endedWorkSavedKind).Scan(&raw); err != nil {
			rows, _ := rd.Query(`SELECT kind, object_id, payload FROM events WHERE kind IN (?, 'drain.node-advanced', 'drain.refusal') ORDER BY seq`, endedWorkSavedKind)
			if rows != nil {
				defer rows.Close()
				for rows.Next() {
					var k, oid, pl string
					_ = rows.Scan(&k, &oid, &pl)
					t.Logf("event %s %s: %s", k, oid, pl)
				}
			}
			return err
		}
		return json.Unmarshal(raw, &saved)
	})
	if saved.Ref != endedWorkPrefix+steps.testWriteRun || saved.ItemState != "closed" {
		t.Fatalf("unexpected saved handoff: %+v", saved)
	}
	if got := runGit(t, f.root, "show", saved.Ref+":tools/item-a/failing_test.go"); !strings.Contains(got, "uncommitted test-write handoff") {
		t.Fatalf("saved handoff ref missing uncommitted file: %q", got)
	}
	if status := runGit(t, l.lanePath("lane-1"), "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("lane-1 still dirty after workflow release: %q", status)
	}
}

func TestSettleTerminalItemSavesUncommittedWorkAndSurvivesPrune(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	ctx := context.Background()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, d.ItemID, "closed before settlement", "owner", l.cfg.Now())
		return err
	})
	var work *pendingWork
	fixtureRead(t, f, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		work = works[d.ItemID]
		return nil
	})
	if err := l.settleTerminalItem(ctx, work, &itemTerminalError{ItemID: d.ItemID, State: "closed", Step: "plan"}); err != nil {
		t.Fatalf("settleTerminalItem: %v", err)
	}
	ref := endedWorkPrefix + d.RunRequest.RunID
	if got := runGit(t, f.root, "show", ref+":base.txt"); got != "earlier work" {
		t.Fatalf("saved ref %s missing uncommitted work: %q", ref, got)
	}
	if status := runGit(t, l.lanePath(d.LaneID), "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("lane still dirty after settleTerminalItem: %q", status)
	}
}

func TestReclaimEndedItemLanesLeavesActiveItemWorkUntouched(t *testing.T) {
	f, l, baseSteps := plannedFixture(t)
	l.cfg.Policy.Seats["test-write"] = "fixture"
	steps := &testWriteSteps{
		workflowSteps: baseSteps,
		proposal: pieceProposal([]map[string]any{
			{"id": "tests", "seat": "test-write", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "write failing test, leave uncommitted"},
			{"id": "impl", "seat": "build", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "implement fix"},
		}),
	}
	l.cfg.Steps = steps
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	ctx := context.Background()

	for i := 0; i < 6 && steps.testWriteRun == ""; i++ {
		var r LoopResult
		if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
			t.Fatalf("pass %d: stop=%q err=%v", i, stop, err)
		}
	}
	var r LoopResult
	if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
		t.Fatalf("settle: stop=%q err=%v", stop, err)
	}

	// item-a is still active (in-queue, not closed). reclaimEndedItemLanes must NOT touch lane-1.
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes: %v", err)
	}
	if status := runGit(t, l.lanePath("lane-1"), "status", "--porcelain", "--untracked-files=all"); !strings.Contains(status, "failing_test.go") {
		t.Fatalf("active item's uncommitted work was unexpectedly cleared: %q", status)
	}
}

func TestReclaimEndedItemLanesSkipsPendingFoldAndTempSorts(t *testing.T) {
	f, l, baseSteps := plannedFixture(t)
	l.cfg.Policy.Seats["test-write"] = "fixture"
	steps := &testWriteSteps{
		workflowSteps: baseSteps,
		proposal: pieceProposal([]map[string]any{
			{"id": "tests", "seat": "test-write", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "write failing test, leave uncommitted"},
			{"id": "impl", "seat": "build", "access": "write", "write_scope": []string{"tools/item-a"}, "prompt": "implement fix"},
		}),
	}
	l.cfg.Steps = steps
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	ctx := context.Background()

	for i := 0; i < 6 && steps.testWriteRun == ""; i++ {
		var r LoopResult
		if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
			t.Fatalf("pass %d: stop=%q err=%v", i, stop, err)
		}
	}
	var r LoopResult
	for i := 0; i < 6 && (i == 0 || l.activeCount() > 0); i++ {
		if stop, err := l.pass(ctx, &r); err != nil || (stop != "" && stop != "queue-drained") {
			t.Fatalf("settle %d: stop=%q err=%v", i, stop, err)
		}
	}

	var recorded []recordedSQLQuery
	endedLaneQueryHook = func(query string, args []any) {
		recorded = append(recorded, recordedSQLQuery{query: query, args: args})
	}
	t.Cleanup(func() { endedLaneQueryHook = nil })

	// 1. Idle lane belongs to active item-a (in-queue): must skip readPending.
	before := pendingFolds.Load()
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("active idle lane reclaimEndedItemLanes: %v", err)
	}
	if folds := pendingFolds.Load() - before; folds != 0 {
		t.Errorf("active idle lane folded pending history %d times, want 0", folds)
	}

	// 2. All on-disk lanes are leased: must skip readPending.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state = 'leased', run_id = 'busy-run' WHERE lane_id = 'lane-1'`)
		return err
	})
	before = pendingFolds.Load()
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("leased lane reclaimEndedItemLanes: %v", err)
	}
	if folds := pendingFolds.Load() - before; folds != 0 {
		t.Errorf("all-leased lanes folded pending history %d times, want 0", folds)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state = 'idle', run_id = NULL WHERE lane_id = 'lane-1'`)
		return err
	})

	// 3. Positive control: item-a closes while lane-1 holds uncommitted work -> folds pending once and reclaims lane-1.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, "item-a", "superseded", "owner", l.cfg.Now())
		return err
	})
	// A lane-2 run opens its bracket while idle lane-1 still holds the ended item's
	// test-write handoff. Reclamation is the drain's write, not lane-2's escape.
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	epochs := l.idleLaneEpochs()
	watched := []string{"lane-1"}
	opening, err := l.snapshotIdleSiblings(ctx, l.lanePath("lane-2"), watched, epochs)
	if err != nil {
		t.Fatalf("opening sibling bracket: %v", err)
	}
	lanePath, err := canonicalDir(l.lanePath("lane-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(opening.Lanes, lanePath) {
		t.Fatalf("opening bracket did not watch lane-1: %v", opening.Lanes)
	}
	watchedDirty := false
	for _, worktree := range opening.Worktrees {
		if worktree.Path == lanePath && len(worktree.Status) > 0 {
			watchedDirty = true
		}
	}
	if !watchedDirty {
		t.Fatal("opening bracket did not capture lane-1's dirty status")
	}
	before = pendingFolds.Load()
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("ended item reclaimEndedItemLanes: %v", err)
	}
	if folds := pendingFolds.Load() - before; folds != 1 {
		t.Errorf("ended item reclamation folded pending history %d times, want 1", folds)
	}
	if status := runGit(t, l.lanePath("lane-1"), "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("lane-1 still dirty after reclamation: %q", status)
	}

	sibling := Decision{LaneID: "lane-2", RunRequest: &run.Request{RunID: "run-sibling-reclamation", ItemID: "item-b"}}
	var boundary LoopResult
	if _, err := l.closeAsyncBracket(ctx, sibling, l.cfg.Now(), opening, &boundary, watched, epochs); err != nil {
		t.Fatalf("closing sibling bracket: %v", err)
	}
	if check := lastBoundaryCheck(t, f, sibling.RunRequest.RunID); check["clean"] != true {
		t.Errorf("drain reclamation of idle lane-1 convicted lane-2: check=%v, stop step=%q detail=%q", check, boundary.StopStep, boundary.Detail)
	}

	// 4. Repeat pass while ended item's lane-1 sits idle and already clean: must skip readPending.
	before = pendingFolds.Load()
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("repeat clean ended lane reclaimEndedItemLanes: %v", err)
	}
	if folds := pendingFolds.Load() - before; folds != 0 {
		t.Errorf("already-clean ended lane folded pending history %d times on repeat pass, want 0", folds)
	}

	assertNoSQLiteTempSortOrEphemeral(t, f.storePath, recorded)
}

func TestEndedLaneOwnerOrderingMatchesChronologicalTieBreak(t *testing.T) {
	earlierEvent := endedLaneOwner{runID: "run-1", itemID: "item-a", seq: 10}
	sameSeqRunEarly := endedLaneOwner{runID: "run-1a", itemID: "item-b", seq: 10, fromRuns: true, finishedAt: 100, startedAt: 50}
	sameSeqRunLate := endedLaneOwner{runID: "run-1b", itemID: "item-c", seq: 10, fromRuns: true, finishedAt: 100, startedAt: 60}
	laterEvent := endedLaneOwner{runID: "run-2", itemID: "item-d", seq: 11}

	if !sameSeqRunEarly.newerOrEqual(earlierEvent) || earlierEvent.newerOrEqual(sameSeqRunEarly) {
		t.Fatal("runs row at equal event_seq must win over event row")
	}
	if !sameSeqRunLate.newerOrEqual(sameSeqRunEarly) || sameSeqRunEarly.newerOrEqual(sameSeqRunLate) {
		t.Fatal("later startedAt at equal (seq, finishedAt) must win")
	}
	if !laterEvent.newerOrEqual(sameSeqRunLate) || sameSeqRunLate.newerOrEqual(laterEvent) {
		t.Fatal("higher seq must win regardless of source")
	}
}
