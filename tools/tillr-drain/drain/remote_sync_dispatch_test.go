package drain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func fileDispatchableRemoteSyncRepair(t *testing.T) (*pushFixture, remoteSyncState) {
	t.Helper()
	p := newPushFixture(t)
	root := p.f.root
	remote := p.remoteCommit(t, "two.txt", "remote version\n")
	recordFixtureLandings(t, p.f.storePath, remote)
	runGit(t, root, "fetch", "-q", "origin")
	parent := runGit(t, root, "rev-parse", p.local[0]+"^")
	tree := runGit(t, root, "merge-tree", "--write-tree", "--merge-base="+parent, remote, p.local[0])
	base := runGit(t, root, "commit-tree", tree, "-p", remote, "-m", "replay one.txt")
	st := remoteSyncFilingFixture(t, p, base, remote, p.local[1])
	st.ReplayedOrder = []string{p.local[0]}
	st.ReplayedPairs = map[string]string{p.local[0]: base}
	if _, err := p.l.fileOrUpdateRemoteSyncItem(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	p.l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	return p, st
}

func TestRemoteSyncDispatchStagesPinnedBaseAndConflictBrief(t *testing.T) {
	ctx := context.Background()
	p, st := fileDispatchableRemoteSyncRepair(t)
	root := p.f.root
	fixer, err := p.l.fileTrunkFixer(ctx, p.local[2], []string{"fixture-red-check"}, GateOutcome{SuspectPaths: []string{"three.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if notice, err := openTrunkRedNotice(ctx, p.f.storePath); err != nil || !strings.Contains(notice, fixer) || !strings.Contains(notice, "fixture-red-check") {
		t.Fatalf("missing trunk-red notice in fixture: %q, %v", notice, err)
	}
	p.l.cfg.Stage = &LaneStager{
		Root: root, LaneRoot: p.f.laneRoot, Store: p.f.storePath, Now: p.l.cfg.Now,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	for _, plan := range mustPlan(t, p.l) {
		if plan.decision.ItemID != st.ItemID || plan.decision.Action != ActionDispatch {
			continue
		}
		ok, work, err := p.l.applyPlan(ctx, plan, p.local[2], p.l.cfg.Now())
		if err != nil || !ok || work == nil {
			t.Fatalf("apply repair dispatch = %v, %+v, %v", ok, work, err)
		}
		staged, refused, err := p.l.prepareClaimedStage(ctx, &work.Decision, &work.BaseSHA, &LoopResult{})
		if err != nil || refused || len(staged) != 1 {
			t.Fatalf("stage repair dispatch = %+v, %v, %v", staged, refused, err)
		}
		lane := p.l.laneDirPath(staged[0].LaneID)
		pin := runGit(t, root, "rev-parse", remoteSyncPinRef(st.ItemID))
		if got := runGit(t, filepath.Join(lane, WorktreeDir), "rev-parse", "HEAD"); got != pin {
			t.Errorf("lane HEAD = %s, want pinned %s", got, pin)
		}
		raw, err := os.ReadFile(filepath.Join(lane, BriefFile))
		if err != nil {
			t.Fatal(err)
		}
		brief := string(raw)
		for _, want := range []string{
			"Conflicting paths: two.txt",
			"Merge-tree conflict output:\n" + st.ConflictOutput,
			"Local commit message (" + shortSHA(st.ConflictCommit) + "):\n" + st.CommitMessage,
			"Resolve the conflict with one linear commit atop the staged base",
		} {
			if !strings.Contains(brief, want) {
				t.Errorf("brief omits %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, fixer) || strings.Contains(brief, "fixture-red-check") {
			t.Errorf("repair brief contains the unrelated trunk-red notice:\n%s", brief)
		}
		return
	}
	t.Fatal("filed repair was not planned for dispatch")
}

func TestRemoteSyncRepairUsesSerialDeskWhenLanesAreOccupied(t *testing.T) {
	p, st := fileDispatchableRemoteSyncRepair(t)
	p.l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id) VALUES ('lane-1','leased','held')`)
		return err
	})
	check := func(source string, d Decision) {
		t.Helper()
		if d.ItemID != st.ItemID || d.Action != ActionDispatch || d.LaneID != "desk-trunk-fixer" || d.RunRequest == nil || d.RunRequest.LaneID != d.LaneID {
			t.Errorf("%s did not dispatch the repair on the serial desk: %+v", source, d)
		}
	}
	check("queue pass", decisionFor(t, p.l, st.ItemID).decision)
	start, _ := proposeFixture(t, p.l, p.l.cfg.Now(), startSnapshot{}).nextFreshStart()
	check("start proposal", start.decision)
}

type remoteSyncReplayProbe struct {
	Steps
	called bool
}

func (s *remoteSyncReplayProbe) Replay(context.Context, Candidate, string, []string) (ReplayOutcome, error) {
	s.called = true
	return ReplayOutcome{}, nil
}

func TestRemoteSyncRepairCannotInheritTrunkRed(t *testing.T) {
	steps := &remoteSyncReplayProbe{}
	l := &Loop{cfg: LoopConfig{Steps: steps}}
	c := Candidate{ItemID: RemoteSyncItemID(strings.Repeat("a", 40)), BaseSHA: strings.Repeat("b", 40)}
	reading, err := l.trunkAlreadyRed(context.Background(), c, GateOutcome{Outcome: GateFailed, FailingChecks: []string{"unit"}})
	if err != nil || reading.Confirmed || steps.called {
		t.Fatalf("trunk-red reading = %+v, replay called = %v, err = %v", reading, steps.called, err)
	}
}

func TestAutoRevertRefusesRemoteSyncRepairLanding(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	id := RemoteSyncItemID(h.base)
	landing := h.land(id, "a", "fixture repair\n")
	out, err := h.loop.revertLanding(context.Background(), landing, landing, []HeavyCheck{{CheckID: "unit"}}, func(context.Context, string, string) (bool, error) {
		return false, errors.New("repair landing must be refused before replay")
	})
	if err != nil || !strings.Contains(out.Refused, "remote-sync repair "+id+"'s landing") {
		t.Fatalf("auto-revert did not refuse the repair landing: %+v, %v", out, err)
	}
	if got := runGit(t, h.root, "rev-parse", TrunkRef); got != landing || len(*h.steps.gated) != 0 {
		t.Fatalf("auto-revert attempted to undo the repair: trunk = %s, gated = %+v", got, *h.steps.gated)
	}
}
