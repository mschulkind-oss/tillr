package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCheckedJudgeSurvivesReplacementWithoutAnotherRun(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "pass")
	persistFixtureJudgeSelection(t, f)
	lane := l.laneDirPath("lane-1")
	writeForkFile(t, filepath.Join(lane, BriefFile), "worker brief\n")
	workerSession, err := NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, SessionIDFile), workerSession+"\n")
	base := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD")
	req := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	if _, _, err := s.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	c := Candidate{ItemID: req.ItemID, LaneID: req.LaneID, RunID: req.RunID, Worktree: l.lanePath("lane-1"), BaseSHA: base, HeadSHA: s.workerHead}
	checked, ok, err := l.judgeCandidate(context.Background(), c, &LoopResult{})
	if err != nil || !ok {
		t.Fatalf("judge %v %v", ok, err)
	}
	if checked.Judge == nil {
		t.Fatal("checked receipt absent")
	}
	replacement := f.loop(t, s, l.cfg.Stage)
	replacement.cfg.Policy = l.cfg.Policy
	if _, ok, err := replacement.judgeCandidate(context.Background(), c, &LoopResult{}); err != nil || !ok {
		t.Fatalf("recovered judge %v %v", ok, err)
	}
	if len(s.runs) != 2 {
		t.Fatalf("review repeated: %v", s.runs)
	}
	raw, err := os.ReadFile(filepath.Join(lane, SessionIDFile))
	if err != nil || strings.TrimSpace(string(raw)) != workerSession {
		t.Fatalf("worker session not restored: %s %v", raw, err)
	}
	raw, err = os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil || string(raw) != "worker brief\n" {
		t.Fatalf("worker brief not restored: %s %v", raw, err)
	}
	if head := runGit(t, c.Worktree, "rev-parse", "HEAD"); head != s.workerHead {
		t.Fatalf("candidate changed %s", head)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("preland verdicts %d", n)
		}
		return nil
	})
}
