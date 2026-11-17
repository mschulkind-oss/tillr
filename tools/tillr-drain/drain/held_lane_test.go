package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func heldCandidateFixture(t *testing.T) (refusalLoopFixture, *Loop, Candidate) {
	t.Helper()
	return heldCandidateFixtureInLane(t, "lane-1")
}

func heldCandidateFixtureInLane(t *testing.T, lane string) (refusalLoopFixture, *Loop, Candidate) {
	t.Helper()
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Steps = &faultSteps{fault: "gate"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]'`)
		return err
	})
	p := mustPlan(t, l)[0]
	p.decision.LaneID = lane
	p.decision.RunRequest.LaneID = lane
	if lane != "lane-1" {
		l.cfg.Lanes = []string{lane}
		if err := l.stageLanes(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v/%v", ok, err)
	}
	path := l.lanePath(p.decision.LaneID)
	writeForkFile(t, filepath.Join(path, "candidate.txt"), "candidate\n")
	runGit(t, path, "add", "candidate.txt")
	runGit(t, path, "commit", "-qm", "candidate")
	writeForkFile(t, filepath.Join(l.laneDirPath(p.decision.LaneID), FenceFile), "candidate.txt\n")
	c := Candidate{ItemID: "item-a", LaneID: p.decision.LaneID, RunID: p.decision.RunRequest.RunID, Worktree: path, BaseSHA: base, HeadSHA: runGit(t, path, "rev-parse", "HEAD")}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	return f, l, c
}

func examineHeldCandidate(t *testing.T, l *Loop) {
	t.Helper()
	p := mustPlan(t, l)[0]
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, l.cfg.Root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim examination: %v/%v", ok, err)
	}
	l.launchExamination(context.Background(), p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
	select {
	case done := <-l.completions:
		delete(l.active, done.id)
		if done.outcome.err != nil || done.outcome.stop != "" {
			t.Fatalf("examination: %+v", done.outcome)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("examination did not finish")
	}
}

func TestHeldCandidateOffersReleasedLaneInSamePlan(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	plans := mustPlan(t, l)
	if got := plans[1].decision; got.Action != ActionDispatch || got.LaneID != c.LaneID {
		t.Fatalf("held candidate still occupies ready sibling's lane: %+v", got)
	}
	if got := runGit(t, l.cfg.Root, "rev-parse", "refs/tillr/runs/"+c.RunID+"^{commit}"); got != c.HeadSHA {
		t.Fatalf("pin %s, want %s", got, c.HeadSHA)
	}
}

func TestHeldCandidateRetriesPinnedHeadOnDifferentLane(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	sibling := mustPlan(t, l)[1]
	ok, _, err := l.applyPlan(context.Background(), sibling, c.BaseSHA, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("take old lane: %v/%v", ok, err)
	}
	runGit(t, l.lanePath(c.LaneID), "reset", "--hard", c.BaseSHA)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	steps := &heldHeadSteps{}
	l.cfg.Steps = steps
	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionJudge || p.decision.LaneID != "lane-2" {
		t.Fatalf("retry did not choose a free lane: %+v", p.decision)
	}
	// The free lane has advanced past the candidate's base. Admission must use
	// today's trunk without rebasing or replacing the pinned candidate.
	writeForkFile(t, filepath.Join(f.root, "unrelated.txt"), "new trunk\n")
	runGit(t, f.root, "add", "unrelated.txt")
	runGit(t, f.root, "commit", "-qm", "new trunk")
	runGit(t, l.lanePath("lane-2"), "reset", "--hard", TrunkRef)
	archive := filepath.Join(RunDir(f.root, c.RunID), BriefFile)
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, archive, "original worker brief\n")
	examineHeldCandidate(t, l)
	if raw, err := os.ReadFile(archive); err != nil || string(raw) != "original worker brief\n" {
		t.Fatalf("retry rewrote original worker inputs: %q/%v", raw, err)
	}
	if steps.candidate.HeadSHA != c.HeadSHA || steps.candidate.RunID != c.RunID || steps.candidate.LaneID != "lane-2" || steps.candidate.Worktree != l.lanePath("lane-2") {
		t.Fatalf("gate did not receive the original pinned head on a new lane: %+v", steps.candidate)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("retry changed pinned head: %s", got)
	}
	// The sibling's checkout must never be reset by the old candidate's examination.
	if got := runGit(t, l.lanePath(c.LaneID), "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("retry wrote old lane: %s", got)
	}
}

type heldHeadSteps struct {
	recordingSteps
	candidate Candidate
}

func (s *heldHeadSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.candidate = c
	return s.recordingSteps.Gate(ctx, c)
}

func TestWhySeparatesTwoReservedIdleLanesFromWorkers(t *testing.T) {
	f, l, _ := heldCandidateFixture(t)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	// A second durable pending attempt with no worker lease reserves the second lane.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		req := run.Request{RunID: "run-b", ItemID: "item-b", LaneID: "lane-2", Backend: "fixture"}
		return appendEffect(tx, dispatchStartedKind, req.RunID, pendingWork{Decision: Decision{ItemID: req.ItemID, LaneID: req.LaneID, RunRequest: &req}}, l.cfg.Now())
	})
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		a, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		if !strings.Contains(a.Summary.String(), "reserved 2; workers 0") {
			t.Errorf("summary hides reservations: %s", a.Summary.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
