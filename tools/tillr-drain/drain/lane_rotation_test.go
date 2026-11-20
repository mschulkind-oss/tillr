package drain

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// laneRefusingStager refuses to stage lane while that lane's worktree holds a commit the
// base does not contain, with the same lane-owned refusal the real stager answers
// (refuseUnlandedCommit), and stages every other lane. It counts the attempts per lane.
type laneRefusingStager struct {
	t     *testing.T
	f     refusalLoopFixture
	lane  string
	mu    sync.Mutex
	tries map[string]int
}

func (s *laneRefusingStager) Stage(_ context.Context, spec StageSpec) error {
	s.mu.Lock()
	if s.tries == nil {
		s.tries = map[string]int{}
	}
	s.tries[spec.LaneID]++
	s.mu.Unlock()
	if spec.LaneID != s.lane {
		return nil
	}
	head := runGit(s.t, laneWorktree(s.f, spec.LaneID), "rev-parse", "HEAD")
	if head == spec.BaseSHA {
		return nil
	}
	return unlandedLaneRefusal(spec.LaneID, head, spec.BaseSHA)
}

func (s *laneRefusingStager) attempts(lane string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tries[lane]
}

func laneWorktree(f refusalLoopFixture, lane string) string {
	return f.laneRoot + "/" + lane + "/" + WorktreeDir
}

// laneSteps is recordingSteps that also records which lane each item ran on.
type laneSteps struct {
	recordingSteps
	mu    sync.Mutex
	lanes map[string][]string
}

func (s *laneSteps) Run(ctx context.Context, request run.Request) (run.Record, string, error) {
	s.mu.Lock()
	if s.lanes == nil {
		s.lanes = map[string][]string{}
	}
	s.lanes[request.ItemID] = append(s.lanes[request.ItemID], request.LaneID)
	s.mu.Unlock()
	return s.recordingSteps.Run(ctx, request)
}

func (s *laneSteps) ranOn(itemID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.lanes[itemID]...)
}

// onePass runs one pass and takes every completion it started, so the next pass plans
// from a settled store.
func onePass(t *testing.T, l *Loop) {
	t.Helper()
	var result LoopResult
	if _, err := l.pass(context.Background(), &result); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	for len(l.active) > 0 {
		c := <-l.completions
		if _, err := l.takeCompletion(c, &result); err != nil {
			t.Fatalf("taking a completion failed: %v", err)
		}
	}
}

func laneEvents(t *testing.T, f refusalLoopFixture, kind, lane string) int {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var n int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ?`, kind, lane).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// On 2026-11-17 a lane whose worktree held a change nobody had landed refused staging on
// every pass, and every pass counted it an infrastructure failure, printed it again, and
// left the item it was offered waiting. A lane-owned refusal takes that lane out of
// rotation for the loop session, with one event and one line naming the lane, the reason
// and the remedy, and the item is offered the next free lane in the same pass.
func TestALaneThatRefusesStagingLeavesRotationAndTheItemTakesTheNextLane(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	steps := &laneSteps{}
	stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
	l := f.loop(t, steps, stager)
	var report bytes.Buffer
	l.cfg.Report = &report

	onePass(t, l)
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("item-a ran on %v in the refusing pass, want lane-2", got)
	}
	if l.infraStreak != 0 {
		t.Fatalf("the lane's refusal counted an infrastructure streak of %d", l.infraStreak)
	}
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 {
		t.Fatalf("the refusal wrote %d drain.lane-refused events, want 1", n)
	}
	line := report.String()
	if strings.Count(line, "out of rotation") != 1 || !strings.Contains(line, "lane-1") ||
		!strings.Contains(line, ReasonLaneUnlandedChange) || !strings.Contains(line, "reset") {
		t.Fatalf("the refusal's report line is missing or does not name the lane, reason and remedy: %q", line)
	}
}

// While the condition persists the loop writes nothing per pass and does not retry the
// lane; it re-checks when the lane's HEAD or status changes, or at the slow interval on the
// injected clock. A re-check that still refuses writes nothing; a lane that stages again is
// restored with one event and one line.
func TestAnOutOfRotationLaneIsQuietUntilItChangesOrTheSlowIntervalPasses(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	steps := &laneSteps{}
	stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
	l := f.loop(t, steps, stager)
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return now }
	var report bytes.Buffer
	l.cfg.Report = &report

	for i := 0; i < 4; i++ {
		onePass(t, l)
	}
	if n := stager.attempts("lane-1"); n != 1 {
		t.Fatalf("an unchanged lane out of rotation was staged %d times over four passes, want once", n)
	}
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 || strings.Count(report.String(), "out of rotation") != 1 {
		t.Fatalf("four passes over a persisting condition wrote %d events and %d lines, want 1 and 1: %q", n, strings.Count(report.String(), "out of rotation"), report.String())
	}

	// The slow interval passes on the injected clock: the lane is re-checked by a probe,
	// which still refuses it, so no item is staged on it and nothing new is written.
	now = now.Add(laneRecheckInterval + time.Second)
	for i := 0; i < 3; i++ {
		onePass(t, l)
	}
	if n := stager.attempts("lane-1"); n != 1 {
		t.Fatalf("the slow re-check staged the lane again: %d attempts, want the probe to answer", n)
	}
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 || strings.Count(report.String(), "out of rotation") != 1 {
		t.Fatalf("a re-check that still refused wrote %d events and %d lines, want 1 and 1", n, strings.Count(report.String(), "out of rotation"))
	}

	// The operator resets the lane deliberately: its HEAD changes, the loop re-checks it at
	// once, stages it, and restores it.
	attempts := stager.attempts("lane-1")
	runGit(t, worktree, "reset", "-q", "--hard", TrunkRef)
	for i := 0; i < 3 && laneEvents(t, f, laneRestoredKind, "lane-1") == 0; i++ {
		onePass(t, l)
	}
	if stager.attempts("lane-1") == attempts {
		t.Fatal("a lane whose HEAD changed was not re-checked")
	}
	if n := laneEvents(t, f, laneRestoredKind, "lane-1"); n != 1 || !strings.Contains(report.String(), "back in rotation") {
		t.Fatalf("the restored lane wrote %d drain.lane-restored events, want 1, and said %q", n, report.String())
	}
}

// A loop started over a store that recorded a lane's condition does not record it again
// when the same lane refuses the same way: the session ended, the condition did not. (Its
// one report line is printed again, since the new session's operator has not seen it.)
func TestARestartedLoopDoesNotRecordAnUnchangedLaneConditionAgain(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
	onePass(t, f.loop(t, &laneSteps{}, stager))
	next := f.loop(t, &laneSteps{}, stager)
	var report bytes.Buffer
	next.cfg.Report = &report
	onePass(t, next)
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 {
		t.Fatalf("a restarted loop recorded the unchanged condition again: %d events, %q", n, report.String())
	}
}

// On 2026-11-17 at 19:34:32Z a lane refusal re-planned item-oa-idea-design-turn from lane-5
// onto lane-6, a lane that was idle when the pass began and so was in the pass's watched set.
// The run's bracket then named lane-6 both as its target and as a lane to watch, the
// snapshot refused that as a usage error, and the loop stopped with a fault. A run's own
// lane is never one it watches, however it came to be chosen.
func TestAnItemReplannedOntoAnIdleLaneRunsThereWithoutAFault(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	steps := &laneSteps{}
	stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
	l := f.loop(t, steps, stager)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, worktree, "log", "-1", "--format=%s"); got != "an unlanded change" {
		t.Fatalf("fixture: lane-1 lost its commit: %s", got)
	}
	if p := mustPlan(t, l); len(p) == 0 || p[0].decision.LaneID != "lane-1" {
		t.Fatalf("fixture: item-a is not planned on lane-1 first: %+v", p)
	}
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("the pass stopped: %q/%v", stop, err)
	}
	for len(l.active) > 0 {
		c := <-l.completions
		if stop, err := l.takeCompletion(c, &result); err != nil || stop != "" {
			t.Fatalf("the re-planned run stopped the loop: %q/%v (%s)", stop, err, result.Detail)
		}
	}
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-2" {
		t.Fatalf("item-a ran on %v, want lane-2", got)
	}
}

// claimedRunIDs answers the run ids of the item's run.allocate events, oldest first.
func claimedRunIDs(t *testing.T, f refusalLoopFixture, item string) []string {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var ids []string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT json_extract(payload,'$.run_id') FROM events WHERE kind = ? AND json_extract(payload,'$.item_id') = ? ORDER BY seq`, run.AllocateKind, item)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

// On 2026-11-20 an item offered lanes that each refused staging was allocated and counted
// an attempt for every refusal, so a brand-new item read "attempt 8" before its first
// worker. With five refused lanes and one free lane the item starts on the free lane in
// the first pass, and the run it starts is attempt 1.
func TestAnItemOfferedFiveRefusingLanesStartsOnTheFreeLaneAsAttemptOne(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 6, "item-a")
	stager := &laneRefusingStagerSet{t: t, f: f, refuse: map[string]bool{}}
	for _, lane := range []string{"lane-1", "lane-2", "lane-3", "lane-4", "lane-5"} {
		worktree := laneWorktree(f, lane)
		writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
		runGit(t, worktree, "add", "unlanded.txt")
		runGit(t, worktree, "commit", "-qm", "an unlanded change")
		stager.refuse[lane] = true
	}
	steps := &laneSteps{}
	l := f.loop(t, steps, stager)
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4", "lane-5", "lane-6"}
	onePass(t, l)
	if got := steps.ranOn("item-a"); len(got) != 1 || got[0] != "lane-6" {
		t.Fatalf("item-a ran on %v in the first pass, want lane-6", got)
	}
	ids := claimedRunIDs(t, f, "item-a")
	if len(ids) == 0 || ids[len(ids)-1] != "run-item-a-a1" {
		t.Fatalf("the item's run claims were %v, want the started run to be run-item-a-a1", ids)
	}
}

// laneRefusingStagerSet refuses every lane in refuse the way laneRefusingStager does.
type laneRefusingStagerSet struct {
	t      *testing.T
	f      refusalLoopFixture
	refuse map[string]bool
}

func (s *laneRefusingStagerSet) Stage(_ context.Context, spec StageSpec) error {
	if !s.refuse[spec.LaneID] {
		return nil
	}
	head := runGit(s.t, laneWorktree(s.f, spec.LaneID), "rev-parse", "HEAD")
	if head == spec.BaseSHA {
		return nil
	}
	return unlandedLaneRefusal(spec.LaneID, head, spec.BaseSHA)
}

// ProbeLane lets the loop ask whether lane-1 still refuses without allocating an item to it.
func (s *laneRefusingStager) ProbeLane(_ context.Context, lane, base string) error {
	if lane != s.lane {
		return nil
	}
	head := runGit(s.t, laneWorktree(s.f, lane), "rev-parse", "HEAD")
	if head == base {
		return nil
	}
	return unlandedLaneRefusal(lane, head, base)
}

// The slow re-check puts a lane back into rotation so staging can learn whether it still
// refuses. On 2026-11-20 that cost an allocation and an attempt number for every refused
// lane every ten minutes. A stager that can probe answers without a claim: the lane that
// still refuses stays out, the item is never allocated to it, and when the condition
// clears the same probe returns the lane to rotation.
func TestASlowRecheckProbesALaneInsteadOfAllocatingAnItemToIt(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b", "item-c")
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	steps := &laneSteps{}
	stager := &laneRefusingStager{t: t, f: f, lane: "lane-1"}
	l := f.loop(t, steps, stager)
	now := time.Date(2026, 11, 20, 6, 0, 0, 0, time.UTC)
	l.cfg.Now = func() time.Time { return now }
	onePass(t, l)
	if n := stager.attempts("lane-1"); n != 1 {
		t.Fatalf("the first pass staged lane-1 %d times, want the one refusal", n)
	}
	allocations := len(claimedRunIDs(t, f, "item-a")) + len(claimedRunIDs(t, f, "item-b")) + len(claimedRunIDs(t, f, "item-c"))
	for i := 0; i < 3; i++ {
		now = now.Add(laneRecheckInterval + time.Second)
		onePass(t, l)
	}
	if n := stager.attempts("lane-1"); n != 1 {
		t.Fatalf("lane-1 was staged %d times across three slow re-checks, want only the first refusal", n)
	}
	if got := len(claimedRunIDs(t, f, "item-a")) + len(claimedRunIDs(t, f, "item-b")) + len(claimedRunIDs(t, f, "item-c")); got != allocations {
		t.Fatalf("the re-checks allocated %d run claims, want none", got-allocations)
	}
	// The operator resets the lane: its HEAD changes, the probe finds it clear and it returns.
	runGit(t, worktree, "reset", "-q", "--hard", TrunkRef)
	for i := 0; i < 3 && laneEvents(t, f, laneRestoredKind, "lane-1") == 0; i++ {
		onePass(t, l)
	}
	if n := laneEvents(t, f, laneRestoredKind, "lane-1"); n != 1 {
		t.Fatalf("the cleared lane wrote %d drain.lane-restored events, want 1", n)
	}
}

// A dispatch that ends before a worker starts leaves no trace in the item's attempt number.
// On 2026-11-20 a workflow item offered lanes that refused staging kept a running node row
// for each refused claim, so the attempt counted allocations: its first worker was
// attempt 8. Here the real stager refuses lane-1 for an unlanded change; the item's work
// starts on lane-2 as attempt 1, and no node row of a later attempt exists.
func TestADispatchRefusedBeforeAWorkerStartsDoesNotRaiseTheAttemptNumber(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	worktree := laneWorktree(f, "lane-1")
	writeForkFile(t, worktree+"/unlanded.txt", "nobody landed this\n")
	runGit(t, worktree, "add", "unlanded.txt")
	runGit(t, worktree, "commit", "-qm", "an unlanded change")
	if p := mustPlan(t, l); len(p) == 0 || p[0].decision.ItemID != "item-a" || p[0].decision.LaneID != "lane-1" {
		t.Fatalf("fixture: item-a is not planned on the refusing lane first: %+v", p)
	}
	var result LoopResult
	if _, err := l.pass(context.Background(), &result); err != nil {
		t.Fatalf("the pass failed: %v", err)
	}
	for len(l.active) > 0 {
		c := <-l.completions
		if _, err := l.takeCompletion(c, &result); err != nil {
			t.Fatalf("taking a completion failed: %v", err)
		}
	}
	if len(steps.runs) == 0 || steps.runs[0].RunID != "run-item-a-a1-n1" || steps.runs[0].LaneID != "lane-2" {
		t.Fatalf("the item's first worker was %+v, want run-item-a-a1-n1 on lane-2", steps.runs)
	}
	if n := laneEvents(t, f, laneRefusedKind, "lane-1"); n != 1 {
		t.Fatalf("fixture: lane-1 wrote %d refusals, want 1", n)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	var later int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND attempt > 1`).Scan(&later)
	}); err != nil {
		t.Fatal(err)
	}
	if later != 0 {
		t.Fatalf("the refused claim left %d node rows of a later attempt", later)
	}
}
