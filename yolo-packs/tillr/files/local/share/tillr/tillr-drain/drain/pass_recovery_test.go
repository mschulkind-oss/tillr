package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDispatchRejectsALaneThatReturnedToIdleWithAnUnexaminedCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES ('lane-1',?)`, run.LaneStateIdle); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-a'`, StateBlocked)
		return err
	})
	stale := mustPlan(t, l)[1]
	if stale.decision.LaneID != "lane-1" {
		t.Fatalf("initial B lane %+v", stale)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-a'`, StateInQueue)
		return err
	})
	foreign := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), foreign, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("foreign dispatch %v / %v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: foreign.decision.RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
	})
	before := eventCount(t, f.storePath)
	ok, _, err = l.applyPlan(context.Background(), stale, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || ok {
		t.Fatalf("stale B overwrote A's unfinished ownership: %v / %v", ok, err)
	}
	assertOnlyDispatchDecline(t, f.storePath, before, stale.decision, "lane is held by item-a")
	next := mustPlan(t, l)[1]
	if next.decision.LaneID != "lane-2" {
		t.Fatalf("fresh plan did not preserve A's candidate lane: %+v", next)
	}
}

func TestAContinuationBoundaryFailureSurvivesReplacement(t *testing.T) {
	f, l := passFixture(t, "item-a")
	plan := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), plan, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %v / %v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: plan.decision.RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, Resumed: true, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
	})
	refs := runRefs([]Decision{plan.decision})
	targets := []string{l.lanePath("lane-1")}
	if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeRun, "initial", refs, targets, nil, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeResume, "continued", refs, targets, []Violation{{Kind: "ref", Where: "refs/heads/rogue", Detail: "appeared"}}, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	replacement := f.loop(t, &recordingSteps{}, &itemContentStager{})
	var result LoopResult
	stop, err := replacement.pass(context.Background(), &result)
	if err != nil || stop != StopQueueDrained || result.Candidates != 0 || mustPlan(t, replacement)[0].decision.Reason != StopHostBoundaryViolation {
		t.Fatalf("continuation failure forgotten: %s / %v / %+v", stop, err, result)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rec, err := terminalRecord(r, plan.decision.RunRequest.RunID)
		if err == nil && (rec == nil || !rec.Resumed) {
			t.Error("replacement lost the terminal segment's resumed fact")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

type callbackRunSteps struct {
	recordingSteps
	run func(run.Request) (run.Record, string, error)
}

func (s *callbackRunSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	return s.run(req)
}

func TestAContinuationDoesNotConvictASiblingDispatchedAfterItStarted(t *testing.T) {
	_, l := passFixture(t, "item-a", "item-b")
	dir := l.laneDirPath("lane-1")
	writeForkFile(t, filepath.Join(dir, BriefFile), "the brief\n")
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
	req := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	l.cfg.Steps = &callbackRunSteps{run: func(req run.Request) (run.Record, string, error) {
		// An independent pass legitimately dispatched this sibling while the resumed
		// segment was in flight. Its dirt belongs to its own run's bracket.
		l.mu.Lock()
		l.laneChanges["lane-2"]++
		l.mu.Unlock()
		err := os.WriteFile(filepath.Join(l.lanePath("lane-2"), "active.txt"), []byte("active\n"), 0o644)
		return run.Record{RunID: req.RunID, Backend: "fixture", Resumed: true, State: run.StateExited, ExitCode: countPtr(0)}, "", err
	}}
	var result LoopResult
	rec, _, stop, err := l.resumeAfterWall(context.Background(), "lane-1", req, l.lanePath("lane-1"), run.Record{}, "continued", time.Now().UTC(), &result, []string{"lane-2"})
	if err != nil || stop != "" || !rec.Resumed {
		t.Fatalf("authorized sibling was convicted: %s / %v / %+v", stop, err, rec)
	}
}
