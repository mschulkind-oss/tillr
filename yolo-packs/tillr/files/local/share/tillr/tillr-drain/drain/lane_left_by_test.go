package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func latestLaneRefusalPayload(t *testing.T, storePath, lane string) laneRefusal {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var raw []byte
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT payload FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
			laneRefusedKind, lane,
		).Scan(&raw)
	}); err != nil {
		t.Fatalf("reading latest %s for %s: %v", laneRefusedKind, lane, err)
	}
	var p laneRefusal
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decoding %s payload: %v", laneRefusedKind, err)
	}
	return p
}

func latestLaneRestoredRaw(t *testing.T, storePath, lane string) map[string]any {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var raw []byte
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT payload FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
			laneRestoredKind, lane,
		).Scan(&raw)
	}); err != nil {
		t.Fatalf("reading latest %s for %s: %v", laneRestoredKind, lane, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decoding %s payload: %v", laneRestoredKind, err)
	}
	return m
}

func appendAllocateEvent(t *testing.T, storePath, lane, runID, itemID string, at time.Time) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"run_id":     runID,
		"item_id":    itemID,
		"backend":    "command",
		"lane_id":    lane,
		"started_at": at.UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id := lane
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: run.AllocateKind, ObjectType: "lane", ObjectID: &id, Payload: body, At: at})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func insertRunRow(t *testing.T, storePath, runID, itemID, lane string, eventSeq int64) {
	t.Helper()
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var itemArg any
		if itemID != "" {
			itemArg = itemID
		}
		_, err := tx.Exec(
			`INSERT INTO lanes(lane_id, state) VALUES (?, 'idle') ON CONFLICT(lane_id) DO NOTHING`,
			lane,
		)
		if err != nil {
			return err
		}
		_, err = tx.Exec(
			`INSERT INTO runs(run_id, item_id, lane_id, backend, state, started_at, finished_at, event_seq) VALUES (?, ?, ?, 'command', 'finished', 1, 2, ?)`,
			runID, itemArg, lane, eventSeq,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func insertDispatchRow(t *testing.T, storePath, decisionID, itemID, lane, runID string, dispatchedAt int64) {
	t.Helper()
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var runArg any
		if runID != "" {
			runArg = runID
		}
		_, err := tx.Exec(
			`INSERT INTO dispatches(decision_id, item_id, lane_id, run_id, action, reason, dispatched_at) VALUES (?, ?, ?, ?, 'start', 'test', ?)`,
			decisionID, itemID, lane, runArg, dispatchedAt,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLaneRefusalLeftByLookupTiers(t *testing.T) {
	ctx := context.Background()

	t.Run("runs tier wins over allocate and dispatches", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-a")
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		l := f.loop(t, &recordingSteps{}, &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: func() time.Time { return now }})
		insertDispatchRow(t, f.storePath, "dec-1", "item-disp", "lane-1", "run-disp", 100)
		appendAllocateEvent(t, f.storePath, "lane-1", "run-alloc", "item-alloc", now)
		insertRunRow(t, f.storePath, "run-older", "item-older", "lane-1", 10)
		insertRunRow(t, f.storePath, "run-newest", "item-newest", "lane-1", 20)

		refusal := asLaneRefusal(laneRefusalf("lane-1", ReasonLaneUncommittedWork, "clean it", "dirty worktree"))
		if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "run-newest (item-newest)" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "run-newest (item-newest)")
		}
		// loadLaneConditions restores leftBy, and restoreLane keeps drain.lane-restored unchanged.
		l.laneConditions = nil
		l.laneConditionsLoaded = false
		if err := l.loadLaneConditions(ctx); err != nil {
			t.Fatal(err)
		}
		if cond := l.laneConditions["lane-1"]; cond == nil || cond.leftBy != "run-newest (item-newest)" {
			t.Fatalf("loaded laneCondition.leftBy = %+v, want %q", cond, "run-newest (item-newest)")
		}
		if err := l.restoreLane(ctx, "lane-1"); err != nil {
			t.Fatal(err)
		}
		restored := latestLaneRestoredRaw(t, f.storePath, "lane-1")
		if len(restored) != 1 || restored["lane_id"] != "lane-1" {
			t.Fatalf("drain.lane-restored payload = %v, want only lane_id", restored)
		}
	})

	t.Run("run.allocate tier used when runs has no match", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-a")
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		l := f.loop(t, &recordingSteps{}, &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: func() time.Time { return now }})
		insertDispatchRow(t, f.storePath, "dec-1", "item-disp", "lane-1", "run-disp", 100)
		appendAllocateEvent(t, f.storePath, "lane-1", "run-alloc-1", "item-alloc-1", now)
		appendAllocateEvent(t, f.storePath, "lane-1", "run-alloc-2", "item-alloc-2", now.Add(time.Second))

		refusal := asLaneRefusal(laneRefusalf("lane-1", ReasonLaneUnlandedChange, "land it", "unlanded commit"))
		if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "run-alloc-2 (item-alloc-2)" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "run-alloc-2 (item-alloc-2)")
		}
	})

	t.Run("dispatches tier used when runs and allocate have no match", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-a")
		l := f.loop(t, &recordingSteps{}, &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: time.Now})
		insertDispatchRow(t, f.storePath, "dec-1", "item-disp-1", "lane-1", "run-disp-1", 100)
		insertDispatchRow(t, f.storePath, "dec-2", "item-disp-2", "lane-1", "", 200)

		refusal := asLaneRefusal(laneRefusalf("lane-1", ReasonLaneBranchHeld, "remove worktree", "branch held"))
		if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "item-disp-2" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "item-disp-2")
		}
	})

	t.Run("unknown when no tier has a record", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-a")
		l := f.loop(t, &recordingSteps{}, &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: time.Now})

		refusal := asLaneRefusal(laneRefusalf("lane-1", ReasonLaneBranchHeld, "remove worktree", "branch held externally"))
		if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "unknown" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "unknown")
		}
	})
}

func TestLaneRefusalExcludedRunIDOnStageDispatchesAndPinnedCandidate(t *testing.T) {
	ctx := context.Background()

	t.Run("stageDispatches excludes incoming unlaunched RunID", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-incoming")
		worktree := laneWorktree(f, "lane-1")
		writeForkFile(t, worktree+"/unlanded.txt", "leftover commit\n")
		runGit(t, worktree, "add", "unlanded.txt")
		runGit(t, worktree, "commit", "-qm", "leftover")

		// Prior run that actually ran on lane-1, plus a fresh run.allocate for the incoming unlaunched run.
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		appendAllocateEvent(t, f.storePath, "lane-1", "run-prior", "item-prior", now.Add(-2*time.Minute))
		appendAllocateEvent(t, f.storePath, "lane-1", "run-incoming", "item-incoming", now)

		stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
		l := f.loop(t, &laneSteps{}, stager)
		base := runGit(t, f.root, "rev-parse", "HEAD")
		d := Decision{
			Action: ActionDispatch,
			LaneID: "lane-1",
			ItemID: "item-incoming",
			RunRequest: &run.Request{
				RunID:  "run-incoming",
				ItemID: "item-incoming",
				LaneID: "lane-1",
			},
		}
		staged, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{})
		if err != nil {
			t.Fatalf("stageDispatches failed: %v", err)
		}
		if len(staged) != 0 {
			t.Fatalf("staged = %d, want 0", len(staged))
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "run-prior (item-prior)" {
			t.Fatalf("left_by = %q, want %q (must exclude incoming run-incoming)", got.LeftBy, "run-prior (item-prior)")
		}
	})

	t.Run("stageDispatches yields unknown when only incoming unlaunched RunID exists", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-incoming")
		worktree := laneWorktree(f, "lane-1")
		writeForkFile(t, worktree+"/unlanded.txt", "leftover commit\n")
		runGit(t, worktree, "add", "unlanded.txt")
		runGit(t, worktree, "commit", "-qm", "leftover")

		appendAllocateEvent(t, f.storePath, "lane-1", "run-incoming", "item-incoming", time.Now().UTC())

		stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
		l := f.loop(t, &laneSteps{}, stager)
		base := runGit(t, f.root, "rev-parse", "HEAD")
		d := Decision{
			Action: ActionDispatch,
			LaneID: "lane-1",
			ItemID: "item-incoming",
			RunRequest: &run.Request{
				RunID:  "run-incoming",
				ItemID: "item-incoming",
				LaneID: "lane-1",
			},
		}
		if _, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{}); err != nil {
			t.Fatalf("stageDispatches failed: %v", err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "unknown" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "unknown")
		}
	})

	t.Run("examineClaimed sets ExcludedRunID when stagePinnedCandidate refuses", func(t *testing.T) {
		f, l := passFixture(t, "item-pinned")
		// Record a prior run in runs, and a fresh run.allocate for the reassigned run.
		insertRunRow(t, f.storePath, "run-prior", "item-prior", "lane-1", 5)
		appendAllocateEvent(t, f.storePath, "lane-1", "run-reassigned", "item-pinned", time.Now().UTC())

		// Make lane-1 dirty so stagePinnedCandidate returns ReasonLaneUncommittedWork.
		worktree := filepath.Join(l.cfg.LaneRoot, "lane-1", WorktreeDir)
		writeForkFile(t, filepath.Join(worktree, "dirty.txt"), "dirty\n")
		base := runGit(t, f.root, "rev-parse", "HEAD")
		if err := l.pin(ctx, "run-candidate", base); err != nil {
			t.Fatal(err)
		}

		d := Decision{
			Action: ActionSettle,
			LaneID: "lane-1",
			ItemID: "item-pinned",
			RunRequest: &run.Request{
				RunID:  "run-reassigned",
				ItemID: "item-pinned",
				LaneID: "lane-1",
			},
		}
		work := &pendingWork{
			Decision:   d,
			NeedsStage: true,
			BaseSHA:    base,
			Candidate: &Candidate{
				ItemID:   "item-pinned",
				RunID:    "run-candidate",
				LaneID:   "lane-1",
				HeadSHA:  base,
				Worktree: worktree,
			},
		}
		completion := l.examineClaimed(ctx, d, work, time.Now().UTC(), &PassTiming{}, nil)
		if completion.laneRefusal == nil {
			t.Fatal("expected laneRefusal on completion")
		}
		if completion.laneRefusal.ExcludedRunID != "run-reassigned" {
			t.Fatalf("ExcludedRunID = %q, want %q", completion.laneRefusal.ExcludedRunID, "run-reassigned")
		}
		if err := l.takeLaneOutOfRotation(ctx, completion.laneRefusal); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "run-prior (item-prior)" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "run-prior (item-prior)")
		}
	})
}

func TestLaneRefusalLeftBySurvivingChildren(t *testing.T) {
	ctx := context.Background()

	t.Run("retainSurvivingProcesses passes formatted left_by on terminal item", func(t *testing.T) {
		f, l := passFixture(t, "item-term")
		// Close the item so holdItemFault returns itemTerminalError.
		st, err := store.Open(ctx, f.storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, "closed", "item-term")
			return err
		}); err != nil {
			st.Close()
			t.Fatal(err)
		}
		st.Close()
		out := l.retainSurvivingProcesses(ctx, "item-term", "lane-1", "run-live-child", []survivingProcess{{PID: 4242, Command: "sleep"}})
		if out.laneRefusal == nil || out.laneRefusal.Reason != ReasonLaneChildren {
			t.Fatalf("laneRefusal = %+v, want %s", out.laneRefusal, ReasonLaneChildren)
		}
		if out.laneRefusal.LeftBy != "run-live-child (item-term)" {
			t.Fatalf("LeftBy = %q, want %q", out.laneRefusal.LeftBy, "run-live-child (item-term)")
		}
		if err := l.takeLaneOutOfRotation(ctx, out.laneRefusal); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, "lane-1")
		if got.LeftBy != "run-live-child (item-term)" {
			t.Fatalf("recorded left_by = %q, want %q", got.LeftBy, "run-live-child (item-term)")
		}
	})

	t.Run("recheckLaneChildren picks greatest endedAt seq run and looks up item_id", func(t *testing.T) {
		f, l := passFixture(t, "item-older", "item-newer")
		lane := "lane-1"
		dir := filepath.Join(l.cfg.LaneRoot, lane, "durable")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Two ended runs in lane-1: run-older (seq 10) and run-newer (seq 50).
		insertRunRow(t, f.storePath, "run-older", "item-older", lane, 10)
		insertRunRow(t, f.storePath, "run-newer", "item-newer", lane, 50)

		for _, runID := range []string{"run-older", "run-newer"} {
			child := exec.Command("sleep", "300")
			child.Dir = dir
			child.Env = append(os.Environ(), "TILLR_RUN_ID="+runID, "TILLR_LOOP_STORE="+l.cfg.Store)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
		}

		if err := l.recheckLaneChildren(ctx); err != nil {
			t.Fatal(err)
		}
		got := latestLaneRefusalPayload(t, f.storePath, lane)
		if got.Reason != ReasonLaneChildren {
			t.Fatalf("reason = %q, want %q", got.Reason, ReasonLaneChildren)
		}
		if got.LeftBy != "run-newer (item-newer)" {
			t.Fatalf("left_by = %q, want %q", got.LeftBy, "run-newer (item-newer)")
		}
	})
}
