package drain

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A run whose examination already recorded its gate refusal has concluded, even when the
// loop stopped before recording the examination done. A later examination of that run
// replays the conclusion: it does not gate the candidate again, and it does not compare a
// lane that the refusal already released and recut with the candidate's retirement commit.
//
// On 2026-11-17 item-oa-idea-design-turn run a7 failed its gate at 23:41:34 and recorded
// the refusal at 23:41:40. Lane-4 was recut, and no drain.examined followed. The loop
// restarted at 23:43:30, examined a7 again, and recoverRetirement compared lane-4's new
// HEAD with the recorded retirement commit. Fault 14658 then convicted the run as
// candidate-record-mismatch.
func TestARefusedRunExaminedAgainAfterItsLaneWasRecutIsNotConvicted(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["base.txt"]'`)
		return err
	})
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("dispatch: %v %v", ok, err)
	}
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "base.txt\n")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\n")
	runGit(t, lane, "commit", "-qam", "candidate")
	worker := runGit(t, lane, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\nretired\n")
	runGit(t, lane, "commit", "-qam", "chore: retire landed document block")
	c := Candidate{ItemID: "item-a", RunID: p.decision.RunRequest.RunID, LaneID: "lane-1", Worktree: lane, BaseSHA: base,
		HeadSHA: worker, WorkerHeadSHA: worker, RetirementTreeSHA: runGit(t, lane, "rev-parse", "HEAD^{tree}"), Request: p.decision.RunRequest}
	if err := appendJudgment(ctx, f.storePath, candidateReadyKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	if err := appendJudgment(ctx, f.storePath, candidateRetirementKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	// The first examination's ending: the gate refused the retired head, and the lane was
	// recut to the trunk. The loop stopped before it recorded the examination done.
	refused := c
	refused.HeadSHA = runGit(t, lane, "rev-parse", "HEAD")
	var result LoopResult
	if err := l.refuse(ctx, refused, &result, RefusalGate); err != nil {
		t.Fatal(err)
	}
	runGit(t, lane, "reset", "-q", "--hard", base)

	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	var again LoopResult
	stop, landed, err := replacement.Verify(ctx, c, &again, nil)
	if err != nil || stop != "" || landed {
		t.Fatalf("the re-examination of a refused run answered stop=%q landed=%v err=%v", stop, landed, err)
	}
	if steps.gates != 0 {
		t.Fatalf("the refused run was gated again (%d gate run(s))", steps.gates)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if fault, held := faults["item-a"]; held {
			t.Fatalf("the refused run was convicted: %+v", fault)
		}
		return nil
	})
}
