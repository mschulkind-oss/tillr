package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// On 2026-11-17 a run reported RESULT: blocked with new, uncommitted files in its lane. The
// settlement preserved them under refs/tillr/preserved/<run> and reset the lane, but the
// reset (checkout --force, reset --hard) keeps untracked files, so the lane was released
// with the run's files still in it. Once preserved, those bytes have a durable copy: the
// reset clears them, and the lane is clean for the next run.
func TestAbandonClearsTheUntrackedFilesItPreserved(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	lane := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	if err := os.MkdirAll(filepath.Join(lane, "inside"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, "inside/new.txt"), "inside\n")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "edited\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes (lane_id,state) VALUES ('lane-1','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('run-a','item-a','lane-1','fixture','finished',1,2,'[]',1)`)
		return err
	})
	if stop := l.abandon(context.Background(), "lane-1", "run-a", l.headOf(context.Background(), "lane-1"), "reported RESULT: blocked"); stop != "" {
		t.Fatal(stop)
	}
	if got := runGit(t, f.root, "show", "refs/tillr/preserved/run-a:inside/new.txt"); got != "inside" {
		t.Fatalf("the untracked file was not preserved: %q", got)
	}
	if status := runGit(t, lane, "status", "--porcelain", "--untracked-files=all"); status != "" {
		t.Fatalf("the lane was released with the preserved run's files still in it:\n%s", status)
	}
}

// The same lane, released dirty by a build before the fix, was then assigned a pinned
// candidate, and the pinned staging refused it with a plain external error that stopped the
// loop every few minutes (2026-11-17, 20:07Z onward). A lane's own uncommitted files are
// that lane's condition: the lane leaves rotation with its files untouched, the candidate is
// released for another lane, and the loop runs on.
func TestPinnedCandidateOnALaneWithUncommittedFilesLeavesTheLoopRunning(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	examineHeldCandidate(t, l)
	sibling := mustPlan(t, l)[1]
	if ok, _, err := l.applyPlan(context.Background(), sibling, c.BaseSHA, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("take old lane: %v/%v", ok, err)
	}
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(l.lanePath("lane-2"), "leftover.go")
	writeForkFile(t, leftover, "package leftover\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		return ClearItemFault(tx, c.ItemID, faults[c.ItemID].Seq, l.cfg.Now())
	})
	p := mustPlan(t, l)[0]
	if p.decision.Action != ActionJudge || p.decision.LaneID != "lane-2" {
		t.Fatalf("fixture: retry did not choose lane-2: %+v", p.decision)
	}
	ok, work, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim examination: %v/%v", ok, err)
	}
	l.launchExamination(context.Background(), p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
	var result LoopResult
	stop, err := l.takeCompletion(<-l.completions, &result)
	if err != nil || stop != "" {
		t.Fatalf("one lane's uncommitted files stopped the loop: %q/%v (%s)", stop, err, result.Detail)
	}
	if raw, err := os.ReadFile(leftover); err != nil || string(raw) != "package leftover\n" {
		t.Fatalf("the refused lane's files were touched: %q/%v", raw, err)
	}
	if cond := l.laneConditions["lane-2"]; cond == nil || !cond.out || cond.reason != ReasonLaneUncommittedWork || !strings.Contains(cond.detail, "leftover.go") {
		t.Fatalf("lane-2 is not out of rotation for its uncommitted work: %+v", cond)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c.RunID); got != c.HeadSHA {
		t.Fatalf("candidate pin moved: %s", got)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works[c.ItemID]; w == nil || !w.LaneReleased || w.Done {
			t.Errorf("candidate is not released for another lane: %+v", w)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The clear must not delete what the snapshot could not see. A run that adds a pattern to a
// tracked ignore file hides a new file from preservation; clearing after the reset restored
// the trunk's ignore rules would delete that file with no saved copy. The clear runs first,
// under the run's own rules, so the hidden file stays.
func TestAbandonKeepsAFileTheRunsOwnIgnoreEditHid(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), "*.log\n")
	runGit(t, f.root, "add", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "ignore logs")
	if err := l.resetLane(context.Background(), "lane-1"); err != nil {
		t.Fatal(err)
	}
	lane := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	writeForkFile(t, filepath.Join(lane, ".gitignore"), "*.log\nhidden.txt\n")
	writeForkFile(t, filepath.Join(lane, "hidden.txt"), "only copy\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes (lane_id,state) VALUES ('lane-1','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('run-a','item-a','lane-1','fixture','finished',1,2,'[]',1)`)
		return err
	})
	if stop := l.abandon(context.Background(), "lane-1", "run-a", l.headOf(context.Background(), "lane-1")); stop != "" {
		t.Fatal(stop)
	}
	if raw, err := os.ReadFile(filepath.Join(lane, "hidden.txt")); err != nil || string(raw) != "only copy\n" {
		t.Fatalf("a file the snapshot could not see was deleted: %q/%v", raw, err)
	}
}
