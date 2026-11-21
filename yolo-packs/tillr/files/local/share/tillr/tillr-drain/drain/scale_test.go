package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestTenLanesLandWithoutWaitingOnEachOther(t *testing.T) {
	testLanesLandWithoutWaitingOnEachOther(t, 10)
}

func TestTwentyLanesLandWithoutWaitingOnEachOther(t *testing.T) {
	testLanesLandWithoutWaitingOnEachOther(t, 20)
}

type scaleSteps struct {
	recordingSteps
	db    *store.Store
	root  string
	now   func() time.Time
	trees map[string]string
	// Each gate can enter independently. Only the test driver releases it;
	// there is no all-or-nothing barrier between the lane goroutines.
	entered chan string
	release map[string]chan struct{}
}

func (s *scaleSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	select {
	case s.entered <- c.RunID:
	case <-ctx.Done():
		return GateOutcome{}, ctx.Err()
	}
	select {
	case <-s.release[c.RunID]:
	case <-ctx.Done():
		return GateOutcome{}, ctx.Err()
	}
	// The fixture check reads a candidate file and writes only to its own lane.
	input, err := os.ReadFile(filepath.Join(c.Worktree, scalePath(c.ItemID)))
	if err != nil {
		return GateOutcome{}, err
	}
	if string(input) != c.ItemID+"\n" {
		return GateOutcome{}, fmt.Errorf("gate %s read a sibling's input: %q", c.RunID, input)
	}
	if err := os.WriteFile(filepath.Join(c.Worktree, ".gate-fixture"), input, 0o644); err != nil {
		return GateOutcome{}, err
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *scaleSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	output, err := os.ReadFile(filepath.Join(c.Worktree, ".gate-fixture"))
	if err != nil || string(output) != c.ItemID+"\n" {
		return LandOutcome{}, fmt.Errorf("land %s without its own gate output: %q (%v)", c.RunID, output, err)
	}
	trunk, err := land.OpenTrunk(s.root)
	if err != nil {
		return LandOutcome{}, err
	}
	out, err := land.Land(ctx, s.db, trunk, land.LandingRequest{
		ItemID: c.ItemID, RunID: c.RunID, BaseRef: TrunkRef, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA,
		ObservedTrunk: c.ObservedTrunk, At: s.now(), ObserveLock: true,
		Verdict: land.Verdict{VerdictID: c.RunID + "-gate", BaseSHA: c.BaseSHA, TreeSHA: s.trees[c.RunID], Result: land.VerdictPass},
	})
	if err != nil || !out.Landed() {
		return LandOutcome{}, fmt.Errorf("land %s: %s %s (%v)", c.RunID, out.Result, out.Reason, err)
	}
	if out.LockObservation == nil || out.LockObservation.RunID != c.RunID {
		return LandOutcome{}, fmt.Errorf("land %s: missing lock observation", c.RunID)
	}
	observation := &LandObservation{RunID: c.RunID}
	var stages []string
	for _, e := range out.LockObservation.Events {
		stages = append(stages, e.Kind)
		observation.Events = append(observation.Events, LandLockEvent{Kind: e.Kind, AtNS: e.AtNS})
	}
	if !slices.Equal(stages, []string{"acquired", "squash", "trunk-move", "receipt", "released"}) {
		return LandOutcome{}, fmt.Errorf("land %s: invalid lock stages %v", c.RunID, stages)
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: out.TrunkAfter, LockObservation: observation}, nil
}

func scalePath(id string) string { return filepath.Join("tools", id, "change") }

func testLanesLandWithoutWaitingOnEachOther(t *testing.T, n int) {
	t.Helper()
	const rounds = 2 // Each physical lane is reused on the new trunk once.
	var items, lanes []string
	for i := range n {
		lanes = append(lanes, fmt.Sprintf("lane-%d", i+1))
	}
	for round := range rounds {
		for i := range n {
			items = append(items, fmt.Sprintf("scale-%02d-%02d", round, i))
		}
	}
	f := newRefusalLoopFixtureWithLanes(t, n, items...)
	writeForkFile(t, filepath.Join(f.root, ".git", "info", "exclude"), ".tillr/\n.gate-fixture\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	steps := &scaleSteps{db: db, root: f.root, trees: map[string]string{}}
	var report bytes.Buffer
	l, err := NewLoop(LoopConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot,
		Lanes: lanes, Steps: steps, Stage: &itemContentStager{}, Report: &report,
		Policy: &Policy{DefaultBackend: "fixture", Backends: map[string]Backend{"fixture": {Adapter: "fixture"}}},
		Now:    func() time.Time { return time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		l.examinations.Wait()
		if t.Failed() {
			t.Log(report.String())
		}
	}()
	steps.now = l.cfg.Now
	initial := runGit(t, f.root, "rev-parse", TrunkRef)
	for round := range rounds {
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		// Start at the candidate-examination seam. Preparing ready candidates up
		// front removes worker subprocesses, queue replanning and boundary scans
		// from this test of concurrent gate and landing, not from the drain.
		works := make([]pendingWork, n)
		for i, lane := range lanes {
			id := items[round*n+i]
			wt := l.lanePath(lane)
			runGit(t, wt, "reset", "--hard", base)
			if err := os.Remove(filepath.Join(wt, ".gate-fixture")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			path := scalePath(id)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, path)), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(wt, path), id+"\n")
			writeForkFile(t, filepath.Join(l.laneDirPath(lane), FenceFile), path+"\n")
			runGit(t, wt, "add", path)
			runGit(t, wt, "commit", "-qm", "test: add "+id)
			runID := "run-" + id
			candidate := &Candidate{ItemID: id, RunID: runID, LaneID: lane, Worktree: wt,
				BaseSHA: base, HeadSHA: runGit(t, wt, "rev-parse", "HEAD")}
			steps.trees[runID] = runGit(t, wt, "rev-parse", "HEAD^{tree}")
			works[i] = pendingWork{Decision: Decision{Action: ActionJudge, ItemID: id, LaneID: lane,
				RunRequest: &run.Request{RunID: runID, ItemID: id, LaneID: lane, Backend: "fixture"}}, Candidate: candidate, BaseSHA: base}
		}
		steps.entered, steps.release = make(chan string, n), make(map[string]chan struct{}, n)
		for _, w := range works {
			steps.release[w.Candidate.RunID] = make(chan struct{})
		}
		// The guard diagnoses a deadlocked regression. Passing depends on gate
		// entries and landing completions, never on a measured duration.
		guard := time.NewTimer(hangGuard)
		defer guard.Stop()
		for i := range works {
			w := &works[i]
			l.launchExamination(ctx, w.Decision, w, l.cfg.Now(), &PassTiming{}, nil)
		}
		entered := make(map[string]bool, n)
		for len(entered) < n {
			select {
			case id := <-steps.entered:
				if steps.release[id] == nil || entered[id] {
					t.Fatalf("unexpected gate entry %s", id)
				}
				entered[id] = true
			case done := <-l.completions:
				t.Fatalf("%s exited before all gates entered: stop=%q err=%v", done.id, done.outcome.stop, done.outcome.err)
			case <-guard.C:
				t.Fatalf("only %d/%d gates entered", len(entered), n)
			}
		}
		complete := func() string {
			t.Helper()
			select {
			case done := <-l.completions:
				delete(l.active, done.id)
				if done.outcome.err != nil || done.outcome.stop != "" || done.result.Landed != 1 {
					t.Fatalf("%s did not land: stop=%q err=%v (landed=%d)", done.id, done.outcome.stop, done.outcome.err, done.result.Landed)
				}
				return done.id
			case <-guard.C:
				t.Fatal("candidate examinations stopped making progress")
				return ""
			}
		}
		// Every peer must actually land with the first gate still suspended.
		// Release pairs to exercise the real land lock without launching a
		// synchronized burst of twenty Git/SQLite publishers on the host.
		for pair := range slices.Chunk(works[1:], 2) {
			want := make(map[string]bool, len(pair))
			for _, w := range pair {
				id := w.Candidate.RunID
				want[id] = true
				close(steps.release[id])
			}
			for len(want) > 0 {
				id := complete()
				if !want[id] {
					t.Fatalf("unexpected landing %s, want one of %v", id, want)
				}
				delete(want, id)
			}
		}
		held := works[0].Candidate.RunID
		close(steps.release[held])
		if got := complete(); got != held {
			t.Fatalf("last landing %s, want %s", got, held)
		}
		l.examinations.Wait()
		guard.Stop()
	}
	l.reportLandingMeasurements()
	for _, want := range []string{"land-lock longest=", "median=", fmt.Sprintf("measured=%d unmeasured=0", rounds*n)} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("summary missing %s:\n%s", want, report.String())
		}
	}
	// A complete, unbranched chain of receipts proves no peer overwrote another
	// landing. It does not compare wall-clock stamps from concurrent workers.
	tip, count := initial, 0
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT trunk_before,trunk_after FROM landings WHERE result='landed' ORDER BY event_seq`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var before, after string
			if err := rows.Scan(&before, &after); err != nil {
				return err
			}
			if before != tip || after == before {
				return fmt.Errorf("landing %d moved %s to %s, want a successor of %s", count, before, after, tip)
			}
			tip, count = after, count+1
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if count != rounds*n || tip != runGit(t, f.root, "rev-parse", TrunkRef) {
		t.Fatalf("receipt chain: %d landings, last tip %s", count, tip)
	}
	for _, id := range items {
		if raw, err := os.ReadFile(filepath.Join(f.root, scalePath(id))); err != nil || string(raw) != id+"\n" {
			t.Fatalf("trunk missing %s: %q (%v)", id, raw, err)
		}
	}
}
