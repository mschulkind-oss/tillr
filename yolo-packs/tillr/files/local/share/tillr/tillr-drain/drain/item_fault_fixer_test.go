package drain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type rebasedFaultSteps struct {
	recordingSteps
	errored bool
	gates   int
}

func (s *rebasedFaultSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.gates++
	if s.errored {
		return GateOutcome{Outcome: GateErrored}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func TestClearedGateFaultRejudgesTheLoopRebasedFixer(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-placeholder")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	id := TrunkFixerID(base)
	s, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, id)
	s.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-placeholder'; UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id=?`, id)
		return err
	})
	var p plannedApplication
	for _, plan := range mustPlan(t, l) {
		if plan.decision.ItemID == id {
			p = plan
		}
	}
	d := p.decision
	ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %v/%v", ok, err)
	}
	path := l.lanePath(d.LaneID)
	writeForkFile(t, filepath.Join(path, "candidate.txt"), "fixer\n")
	runGit(t, path, "add", "candidate.txt")
	runGit(t, path, "commit", "-qm", "fixer")
	head := runGit(t, path, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
	c := Candidate{ItemID: id, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Worktree: path, BaseSHA: base, HeadSHA: head}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}); err != nil {
			return err
		}
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	writeForkFile(t, filepath.Join(f.root, "advanced.txt"), "trunk advanced\n")
	runGit(t, f.root, "add", "advanced.txt")
	runGit(t, f.root, "commit", "-qm", "advance trunk")
	steps := &rebasedFaultSteps{errored: true}
	l.cfg.Steps = steps
	first, err := l.Run(ctx)
	if err != nil || first.Stop != StopQueueDrained || steps.gates != 1 {
		t.Fatalf("first examination %+v/%v gates=%d", first, err, steps.gates)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, id, faults[id].Seq, l.cfg.Now())
	})
	steps.errored = false
	replacement := f.loop(t, steps, &itemContentStager{})
	second, err := replacement.Run(ctx)
	if err != nil || second.Landed != 1 || steps.gates != 2 {
		t.Fatalf("cleared rebase could not recover: %+v/%v gates=%d", second, err, steps.gates)
	}
}
