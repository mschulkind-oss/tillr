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
	spans, err := os.Open(filepath.Join(RunDir(f.root, worker.id), handoffTimingFile))
	if err != nil {
		t.Fatal(err)
	}
	defer spans.Close()
	dec := json.NewDecoder(spans)
	var boundary, examination handoffTiming
	if err := dec.Decode(&boundary); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&examination); err != nil {
		t.Fatal(err)
	}
	if boundary.Phase != "boundary" || examination.Phase != "examination" || examination.StartMS < boundary.EndMS {
		t.Fatalf("expected boundary before examination: %+v, %+v", boundary, examination)
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

func assertExaminationAccounting(t *testing.T, exam handoffTiming, wantPhases []string) {
	t.Helper()
	if exam.Phase != "examination" {
		t.Fatalf("phase = %q, want examination", exam.Phase)
	}
	have := map[string]bool{}
	for _, p := range exam.Phases {
		have[p.Phase] = true
	}
	for _, want := range wantPhases {
		if !have[want] {
			t.Errorf("examination missing sub-phase %q; recorded phases: %+v", want, exam.Phases)
		}
	}
	wall := exam.EndMS - exam.StartMS
	if wall > 0 {
		covered := handoffCoveredMS(exam)
		if covered*10 < wall*9 {
			t.Errorf("examination sub-phases covered %d/%d ms (%.1f%%), want >= 90%%; phases=%+v",
				covered, wall, float64(covered)*100/float64(wall), exam.Phases)
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
			"restart_lock_wait",
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
			"restart_lock_wait",
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
