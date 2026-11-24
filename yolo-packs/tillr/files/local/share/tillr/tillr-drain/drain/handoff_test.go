package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type handoffSteps struct {
	*workflowSteps
	workerRelease chan struct{}
}

func (s *handoffSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	select {
	case <-s.workerRelease:
		return s.workflowSteps.Run(ctx, req)
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	}
}

func TestIntermediateHandoffDoesNotWaitForSiblingLanding(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.cfg.Now = time.Now
	workerRelease := make(chan struct{})
	l.cfg.Steps = &handoffSteps{workflowSteps: steps, workerRelease: workerRelease}
	var result LoopResult
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatal(err)
	}

	// Hold the lock a sibling landing takes. The boundary and this
	// intermediate examination do not need to publish anything.
	unlock, err := l.lockTrunkHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	// Leave sweep work due before the next pass, while the same holder
	// still owns the landing lock.
	runGit(t, f.root, "update-ref", pinRef("run-sweep-fixture"), "HEAD")
	close(workerRelease)
	var worker runCompletion
	select {
	case worker = <-l.completions:
	case <-time.After(hangGuard):
		t.Fatal("worker could not close its boundary")
	}
	if worker.outcome.err != nil || worker.outcome.stop != "" {
		t.Fatalf("worker completion: %+v", worker.outcome)
	}
	if _, err := l.takeCompletion(worker, &result); err != nil {
		t.Fatal(err)
	}
	passCtx, stop := context.WithTimeout(ctx, hangGuard)
	defer stop()
	if _, err := l.pass(passCtx, &result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, &result)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, nodeAdvancedKind, "run-item-a-a1-n2").Scan(&count); err != nil {
			return err
		}
		if count != 1 || steps.gates != 0 {
			t.Errorf("successors=%d, gates=%d; want one successor and no gate", count, steps.gates)
		}
		return nil
	})
	timings := readHandoffTimings(t, f.root, worker.id)
	var boundary, examination handoffTiming
	var hasBoundary, hasExam bool
	for _, entry := range timings {
		switch entry.Phase {
		case "boundary":
			boundary, hasBoundary = entry, true
		case "examination":
			examination, hasExam = entry, true
		}
	}
	if !hasBoundary || !hasExam || examination.StartMS < boundary.EndMS {
		t.Fatalf("expected boundary before examination: %+v", timings)
	}
	if !slices.ContainsFunc(examination.Phases, func(span handoffSpan) bool {
		return span.Phase == "store_write" && span.StartMS >= examination.StartMS && span.EndMS <= examination.EndMS
	}) {
		t.Fatalf("no store write recorded inside examination: %+v", examination)
	}
	if _, err := l.pass(passCtx, &result); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.run_id')=?`, run.AllocateKind, "run-item-a-a1-n2").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("next node allocations=%d, want one before land.lock releases", count)
		}
		return nil
	})
	unlock()
	locked = false
	drainOwned(t, l, &result)
}

func readHandoffTimings(t *testing.T, root, runID string) []handoffTiming {
	t.Helper()
	f, err := os.Open(filepath.Join(RunDir(root, runID), handoffTimingFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []handoffTiming
	dec := json.NewDecoder(f)
	for dec.More() {
		var entry handoffTiming
		if err := dec.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		out = append(out, entry)
	}
	return out
}

func handoffCoveredMS(exam handoffTiming) int64 {
	type interval struct{ start, end int64 }
	intervals := make([]interval, 0, len(exam.Phases))
	for _, p := range exam.Phases {
		s := max(exam.StartMS, p.StartMS)
		e := min(exam.EndMS, p.EndMS)
		if s <= e {
			intervals = append(intervals, interval{s, e})
		}
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})
	var total int64
	var curEnd int64
	hasCur := false
	for _, iv := range intervals {
		if !hasCur || iv.start > curEnd {
			total += iv.end - iv.start
			curEnd = iv.end
			hasCur = true
		} else if iv.end > curEnd {
			total += iv.end - curEnd
			curEnd = iv.end
		}
	}
	return total
}

func assertHandoffAccounting(t *testing.T, entry handoffTiming, wantPhase string, wantPhases []string) {
	t.Helper()
	if entry.Phase != wantPhase {
		t.Fatalf("phase = %q, want %s", entry.Phase, wantPhase)
	}
	have := map[string]bool{}
	for _, p := range entry.Phases {
		have[p.Phase] = true
	}
	for _, want := range wantPhases {
		if !have[want] {
			t.Errorf("%s missing sub-phase %q; recorded phases: %+v", wantPhase, want, entry.Phases)
		}
	}
	wall := entry.EndMS - entry.StartMS
	if wall > 0 {
		covered := handoffCoveredMS(entry)
		if covered*10 < wall*9 {
			t.Errorf("%s sub-phases covered %d/%d ms (%.1f%%), want >= 90%%; phases=%+v",
				wantPhase, covered, wall, float64(covered)*100/float64(wall), entry.Phases)
		}
	}
}

func assertExaminationAccounting(t *testing.T, exam handoffTiming, wantPhases []string) {
	t.Helper()
	assertHandoffAccounting(t, exam, "examination", wantPhases)
}

func TestNextNodeStartRecordsSubPhasesAndReusesWorktreeWithoutLandLock(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.cfg.Now = time.Now

	// Configure a two-node workflow whose first stage is a read-only classify
	// node so node 2 sets spec.Continue = false and exercises ensureWorktree.
	const classifyBuildWF = `{"name":"classify-build","version":1,"stages":[
		{"id":"classify","kind":"agent","seat":"classify","access":"read_only","outputs":{"delivery":"json"}},
		{"id":"build","kind":"agent","seat":"build","access":"write"}
	],"edges":[{"from_node":"classify","to_node":"build"}]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/classify-build@v1.json"), classifyBuildWF)
	runGit(t, f.root, "add", "config/tillr/workflows/classify-build@v1.json")
	runGit(t, f.root, "commit", "-qm", "add classify-build workflow")
	l.cfg.Policy.Seats = map[string]string{"classify": "fixture", "build": "fixture"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow = 'classify-build@v1' WHERE item_id = 'item-a'`)
		return err
	})
	l.cfg.Steps = &funcSteps{
		Steps: steps,
		run: func(ctx context.Context, req run.Request) (run.Record, string, error) {
			report := "Done.\nRESULT: done\n"
			if req.RunID == "run-item-a-a1-n1" {
				dir := nodeOutputsDir(f.root, req.RunID)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return run.Record{}, "", err
				}
				if err := os.WriteFile(filepath.Join(dir, deliveryOutput), []byte(`{"kind":"repo"}`), 0o644); err != nil {
					return run.Record{}, "", err
				}
			} else {
				target := filepath.Join(f.laneRoot, req.LaneID, "worktree")
				if err := os.WriteFile(filepath.Join(target, "result.txt"), []byte("built\n"), 0o644); err != nil {
					return run.Record{}, "", err
				}
				runGit(t, target, "add", "result.txt")
				runGit(t, target, "commit", "-qm", "build")
			}
			path := filepath.Join(f.laneRoot, req.LaneID, "report.md")
			if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
				return run.Record{}, "", err
			}
			rec := run.Record{
				RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
				State: run.StateExited, ExitCode: countPtr(0),
				StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z",
				ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}},
			}
			st, err := store.Open(ctx, f.storePath, true)
			if err != nil {
				return rec, report, err
			}
			defer st.Close()
			err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
			return rec, report, err
		},
	}

	var result LoopResult
	l.pollOnly = true
	// Pass 1: dispatch and finish node 1 (classify).
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, &result)
	// Pass 2: examine node 1 and advance to node 2 (build).
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, &result)

	// Hold land.lock and guard git worktree registry commands. Reusing lane-1's
	// already-registered worktree for node 2 must not wait on land.lock or call
	// git worktree remove/add.
	registryGuardGit(t, f.root)
	unlock, err := l.lockTrunkHistory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()

	passCtx, stop := context.WithTimeout(ctx, hangGuard)
	defer stop()
	if _, err := l.pass(passCtx, &result); err != nil {
		t.Fatalf("next-node pass blocked or failed while land.lock held: %v", err)
	}
	unlock()
	locked = false
	drainOwned(t, l, &result)

	timings := readHandoffTimings(t, f.root, "run-item-a-a1-n2")
	var startTiming *handoffTiming
	for i := range timings {
		if timings[i].Phase == "start" {
			startTiming = &timings[i]
			break
		}
	}
	if startTiming == nil {
		t.Fatalf("missing start timing for run-item-a-a1-n2: %+v", timings)
	}
	t.Logf("start wall=%dms covered=%dms phases=%+v", startTiming.EndMS-startTiming.StartMS, handoffCoveredMS(*startTiming), startTiming.Phases)
	assertHandoffAccounting(t, *startTiming, "start", []string{
		"stage_prepare",
		"stage_read",
		"stage_brief",
		"refuse_unlanded_commit",
		"list_worktrees",
		"reuse_worktree",
		"ensure_worktree",
		"stage_write",
		"stage_lane",
		"record_staged_node",
		"stage_parallel",
		"check_stop_requested",
		"snapshot_idle_siblings",
	})
	for _, forbidden := range []string{"land_lock_wait", "clear_worktree", "worktree_add"} {
		if slices.ContainsFunc(startTiming.Phases, func(p handoffSpan) bool { return p.Phase == forbidden }) {
			t.Errorf("next-node start unexpectedly ran %q: %+v", forbidden, startTiming.Phases)
		}
	}
}

func TestExaminationRecordsSubPhasesAndAccountsForWallTime(t *testing.T) {
	t.Run("settle-through-gate-and-land", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		steps := &countingJudgeSteps{storePath: f.storePath}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-1"}
		dispatched := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")

		var result LoopResult
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)

		timings := readHandoffTimings(t, f.root, dispatched.RunRequest.RunID)
		if len(timings) != 1 {
			t.Fatalf("expected 1 examination timing, got %d: %+v", len(timings), timings)
		}
		assertExaminationAccounting(t, timings[0], []string{
			"check_surviving_processes",
			"read_terminal_record",
			"record_settlement_spend",
			"settle",
			"claim_settled_judge",
			"claim_judge_plan",
			"verify_pre_gate",
			"candidate_gate",
			"post_gate",
			"pre_land",
			"land",
			"post_land",
			"lane_cleanup",
			"release_examination",
		})
	})

	t.Run("settle-yields-to-waiting-express-request", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		steps := &countingJudgeSteps{storePath: f.storePath}
		l.cfg.Steps = steps
		l.cfg.Lanes = []string{"lane-1"}
		l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
		dispatched := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
		fileFixtureRequestWith(t, f, "Interactive request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/notes.md"}, false)

		var result LoopResult
		stop, err := l.pass(context.Background(), &result)
		if err != nil || stop != "" {
			t.Fatalf("pass: stop=%q err=%v", stop, err)
		}
		drainOwned(t, l, &result)

		timings := readHandoffTimings(t, f.root, dispatched.RunRequest.RunID)
		if len(timings) != 1 {
			t.Fatalf("expected 1 examination timing, got %d: %+v", len(timings), timings)
		}
		assertExaminationAccounting(t, timings[0], []string{
			"check_surviving_processes",
			"read_terminal_record",
			"record_settlement_spend",
			"settle",
			"claim_settled_judge",
			"claim_judge_plan",
			"release_examination",
		})
	})
}
