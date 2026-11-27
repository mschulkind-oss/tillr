package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// TestGateCoordinatorWakesOnCandidateReadyAndFallback verifies that the standalone
// Step 4 gate coordinator claims a settled candidate without invoking planDefinition
// or claimSettledCandidateJudge, commits candidateGatedKind, and hands off to Step 5
// rather than landing inline.
func TestGateCoordinatorWakesOnCandidateReadyAndFallback(t *testing.T) {
	f, l := passFixture(t, "item-a")
	steps := &countingJudgeSteps{storePath: f.storePath}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")

	var result LoopResult
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Activate the Step 4 gate coordinator flag (without starting Step 5) so we can
	// observe the Step 4 -> Step 5 boundary directly.
	l.mu.Lock()
	l.gateLand.Gate.Active = true
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.gateLand.Gate.Active = false
		l.mu.Unlock()
	}()

	stop, err := l.pass(ctx, &result)
	if err != nil || stop != "" {
		t.Fatalf("pass: stop=%q err=%v", stop, err)
	}
	drainOwned(t, l, &result)
	if steps.gates.Load() != 0 || steps.lands.Load() != 0 {
		t.Fatalf("pass ran gate/land while gate coordinator active: gates=%d lands=%d",
			steps.gates.Load(), steps.lands.Load())
	}

	// Run one Step 4 coordinator scan (simulating wake on candidate.ready or 15s fallback).
	l.scanAndGate(ctx, &result)
	l.examinations.Wait()

	if steps.gates.Load() != 1 {
		t.Fatalf("gate coordinator ran %d gate(s), want 1", steps.gates.Load())
	}
	if steps.lands.Load() != 0 {
		t.Fatalf("gate coordinator ran %d land(s), want 0 (Step 5 owns landing)", steps.lands.Load())
	}

	// Verify candidateGatedKind was recorded for item-a.
	gatedCount := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind = ?`, candidateGatedKind)
	if gatedCount != 1 {
		t.Fatalf("candidateGatedKind count = %d, want 1", gatedCount)
	}

	// Verify handoff.jsonl contains no claim_settled_judge phase (deleted in Slice 4 / D-SN4).
	if raw, err := os.ReadFile(filepath.Join(f.laneRoot, "handoff.jsonl")); err == nil {
		if strings.Contains(string(raw), "claim_settled_judge") {
			t.Fatalf("handoff.jsonl unexpectedly recorded deleted claim_settled_judge phase:\n%s", string(raw))
		}
	}
}

// TestLandCoordinatorFIFOOrder verifies that findLandableCandidates returns gated
// candidates in strict FIFO order by candidateGatedKind event sequence.
func TestLandCoordinatorFIFOOrder(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	now := l.cfg.Now().UTC()

	// Record three gated candidates in reverse alphabetical order: item-c, item-a, item-b.
	order := []struct {
		itemID, runID, laneID string
	}{
		{"item-c", "run-item-c-a1", "lane-3"},
		{"item-a", "run-item-a-a1", "lane-1"},
		{"item-b", "run-item-b-a1", "lane-2"},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, tc := range order {
			c := Candidate{
				ItemID:  tc.itemID,
				RunID:   tc.runID,
				LaneID:  tc.laneID,
				BaseSHA: base,
				HeadSHA: base,
			}
			work := pendingWork{
				Decision: Decision{
					Action: ActionDispatch,
					ItemID: tc.itemID,
					LaneID: tc.laneID,
					RunRequest: &run.Request{
						RunID:   tc.runID,
						ItemID:  tc.itemID,
						LaneID:  tc.laneID,
						Backend: "fixture",
					},
				},
				BaseSHA:   base,
				Candidate: &c,
			}
			if err := appendEffect(tx, dispatchStartedKind, tc.runID, work, now); err != nil {
				return err
			}
			if err := appendEffect(tx, candidateGatedKind, tc.runID, c, now); err != nil {
				return err
			}
		}
		return nil
	})

	ready, err := l.findLandableCandidates(context.Background())
	if err != nil {
		t.Fatalf("findLandableCandidates: %v", err)
	}
	var got []string
	for _, r := range ready {
		got = append(got, r.ItemID)
	}
	want := []string{"item-c", "item-a", "item-b"}
	if !slices.Equal(got, want) {
		t.Fatalf("FIFO land order = %v, want %v", got, want)
	}
}

// twoCandidateSquashSteps implements Steps for testing concurrent gates followed by
// back-to-back FIFO squash landings onto main with zero second gate.
type twoCandidateSquashSteps struct {
	countingJudgeSteps
	root      string
	mu        sync.Mutex
	landOrder []string
	landedTip map[string]string
}

func (s *twoCandidateSquashSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lands.Add(1)
	s.landOrder = append(s.landOrder, c.ItemID)

	// Simulate real squash landing: apply the candidate's file change onto current main,
	// advancing main so the second candidate lands on top of the first (gated_tree != landed_tree).
	file := c.ItemID + ".txt"
	content := c.ItemID + "-landed\n"
	if err := os.WriteFile(filepath.Join(s.root, file), []byte(content), 0o644); err != nil {
		return LandOutcome{}, err
	}
	if err := runGitHelper(ctx, s.root, "add", file); err != nil {
		return LandOutcome{}, err
	}
	if err := runGitHelper(ctx, s.root, "-c", "user.name=test", "-c", "user.email=test@example.invalid", "commit", "-qm", "land "+c.ItemID); err != nil {
		return LandOutcome{}, err
	}
	tip, err := runGitHelperOut(ctx, s.root, "rev-parse", "main")
	if err != nil {
		return LandOutcome{}, err
	}
	if s.landedTip == nil {
		s.landedTip = map[string]string{}
	}
	s.landedTip[c.ItemID] = tip
	return LandOutcome{Result: LandLanded}, nil
}

func runGitHelper(ctx context.Context, dir string, args ...string) error {
	_, err := runGitHelperOut(ctx, dir, args...)
	return err
}

func runGitHelperOut(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TestConcurrentCandidatesGateAndSquashBackToBackWithoutSecondGate verifies the
// Slice 4 / D-SN3 invariant: two candidates based on the same trunk SHA pass their
// gates independently via Step 4 and then squash back-to-back onto main in FIFO order
// via Step 5 with zero second gate, even though the second candidate lands on top of
// the first (trunk_after_first != base_sha).
func TestConcurrentCandidatesGateAndSquashBackToBackWithoutSecondGate(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET allowed_paths = '["item-a.txt"]' WHERE item_id = 'item-a'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["item-b.txt"]' WHERE item_id = 'item-b'`)
		return err
	})
	steps := &twoCandidateSquashSteps{
		countingJudgeSteps: countingJudgeSteps{storePath: f.storePath},
		root:               f.root,
	}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}

	stageDisjointCandidate := func(itemID, laneID, file string) {
		t.Helper()
		wt := filepath.Join(f.laneRoot, laneID, WorktreeDir)
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			runGit(t, f.root, "worktree", "add", "-q", "-B", "test/"+laneID, wt, base)
		}
		writeForkFile(t, filepath.Join(l.laneDirPath(laneID), FenceFile), file+"\n")
		writeForkFile(t, filepath.Join(l.laneDirPath(laneID), BaseSHAFile), base+"\n")
		writeForkFile(t, filepath.Join(wt, file), itemID+"\n")
		runGit(t, wt, "add", file)
		runGit(t, wt, "commit", "-qm", "candidate "+itemID)
		head := runGit(t, wt, "rev-parse", "HEAD")

		runID := "run-" + itemID + "-a1"
		now := l.cfg.Now().UTC()
		c := Candidate{
			ItemID:   itemID,
			RunID:    runID,
			LaneID:   laneID,
			Worktree: wt,
			BaseSHA:  base,
			HeadSHA:  head,
		}
		work := pendingWork{
			Decision: Decision{
				Action: ActionDispatch,
				ItemID: itemID,
				LaneID: laneID,
				RunRequest: &run.Request{
					RunID:   runID,
					ItemID:  itemID,
					LaneID:  laneID,
					Backend: "fixture",
				},
			},
			BaseSHA:   base,
			Candidate: &c,
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := appendEffect(tx, dispatchStartedKind, runID, work, now); err != nil {
				return err
			}
			// Record candidate ready for Step 4 gate coordinator.
			payload, err := json.Marshal(c)
			if err != nil {
				return err
			}
			_, err = tx.Append(event.Event{
				Kind:       candidateReadyKind,
				ObjectType: "run",
				ObjectID:   &runID,
				Payload:    payload,
				At:         now,
			})
			return err
		})
	}

	stageDisjointCandidate("item-a", "lane-1", "item-a.txt")
	stageDisjointCandidate("item-b", "lane-2", "item-b.txt")

	ctx := context.Background()
	var result LoopResult
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

	// Step 4: gate both candidates against the same base_sha.
	l.scanAndGate(ctx, &result)
	l.examinations.Wait()

	if steps.gates.Load() != 2 {
		t.Fatalf("gates = %d, want 2", steps.gates.Load())
	}
	if steps.lands.Load() != 0 {
		t.Fatalf("lands before Step 5 = %d, want 0", steps.lands.Load())
	}

	// Step 5: land both gated candidates back-to-back in FIFO order without a second gate.
	l.scanAndLand(ctx, &result)
	if stop, err := l.drainCompletions(ctx, &result); err != nil || stop != "" {
		t.Fatalf("drainCompletions: stop=%q err=%v", stop, err)
	}

	if steps.lands.Load() != 2 {
		t.Fatalf("lands = %d, want 2", steps.lands.Load())
	}
	if steps.gates.Load() != 2 {
		t.Fatalf("gates after landing = %d, want 2 (zero second gate)", steps.gates.Load())
	}
	if result.Landed != 2 {
		t.Fatalf("result.Landed = %d, want 2", result.Landed)
	}
	if steps.landedTip["item-a"] == "" || steps.landedTip["item-b"] == "" || steps.landedTip["item-a"] == steps.landedTip["item-b"] {
		t.Fatalf("expected distinct advancing trunk tips: %+v", steps.landedTip)
	}
	// Verify both files exist on main.
	for _, file := range []string{"item-a.txt", "item-b.txt"} {
		if _, err := os.Stat(filepath.Join(f.root, file)); err != nil {
			t.Fatalf("expected %s on main after back-to-back squash: %v", file, err)
		}
	}
}

// TestGateAndLandWakeSignals verifies that signalGateWake and signalLandWake
// wake their respective coordinator channels without blocking.
func TestGateAndLandWakeSignals(t *testing.T) {
	_, l := passFixture(t, "item-a")

	drainChan := func(ch chan struct{}) bool {
		select {
		case <-ch:
			return true
		case <-time.After(50 * time.Millisecond):
			return false
		}
	}

	l.signalGateWake()
	if !drainChan(l.gateLand.Gate.Wake) {
		t.Fatal("signalGateWake did not signal gateWake")
	}

	l.signalLandWake()
	if !drainChan(l.gateLand.Land.Wake) {
		t.Fatal("signalLandWake did not signal landWake")
	}
}
