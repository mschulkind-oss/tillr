package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type triageGateSpy struct {
	recordingSteps
	gates int
}

func (s *triageGateSpy) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.gates++
	return GateOutcome{Outcome: GatePassed}, nil
}

func TestTriageUnmarkedCandidateNeverReachesGate(t *testing.T) {
	f, l := triageFixture(t)
	p := findTriagePlan(t, l, "item-request")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	target := l.laneTarget(p.decision.LaneID)
	runGit(t, target, "reset", "--hard", base)
	writeForkFile(t, filepath.Join(target, "docs/request.md"), triageBlock+strings.ReplaceAll(strings.ReplaceAll(triageBlock, "item-declared", "item-request"), ` prose="2"`, ""))
	runGit(t, target, "add", "docs")
	runGit(t, target, "commit", "-qm", "unmarked request")
	writeForkFile(t, filepath.Join(l.laneDirPath(p.decision.LaneID), FenceFile), "docs/request.md\n")
	c := Candidate{ItemID: "item-request", RunID: p.decision.RunRequest.RunID, LaneID: p.decision.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD"), BlockConflict: p.decision.BlockConflict}
	spy := &triageGateSpy{}
	l.cfg.Steps = spy
	_, landed, err := l.verify(context.Background(), &c, &LoopResult{}, nil)
	if err != nil || landed || spy.gates != 0 {
		t.Fatalf("admission ran gate: landed=%v gates=%d err=%v", landed, spy.gates, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		if c := cs["item-request"]; c.WaitingOn != "owner" || !strings.Contains(c.Text, "prose-unmarked") {
			t.Errorf("rule missing: %+v", c)
		}
		return err
	})
}

func TestTriageRequestVersionTracksEveryRequestFieldOnly(t *testing.T) {
	for _, change := range []string{"title", "body_ref", "criteria", "rank"} {
		t.Run(change, func(t *testing.T) {
			f, l := triageFixture(t)
			first := findTriagePlan(t, l, "item-request").decision
			c := *first.BlockConflict
			c.DesignRunID = "settled-triage"
			c.Status, c.WaitingOn = "owner", "owner"
			fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
			if d := findTriagePlan(t, l, "item-request").decision; d.Reason != ReasonConflictOwner {
				t.Fatalf("repeated triage: %+v", d)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var err error
				switch change {
				case "title":
					_, err = tx.Exec(`UPDATE items SET title=title||' again' WHERE item_id='item-request'`)
				case "body_ref":
					_, err = tx.Exec(`UPDATE items SET body_ref='docs/request.md#changed' WHERE item_id='item-request'`)
				case "criteria":
					_, err = tx.Exec(`UPDATE item_criteria SET text=text||' again' WHERE item_id='item-request'`)
				case "rank":
					_, err = tx.Exec(`UPDATE items SET rank='a' WHERE item_id='item-request'`)
				}
				return err
			})
			d := findTriagePlan(t, l, "item-request").decision
			if change == "rank" {
				if d.Reason != ReasonConflictOwner {
					t.Fatalf("rank spent a run: %+v", d)
				}
				return
			}
			if d.Action != ActionDispatch || d.Reason != ReasonTriageNoBlock {
				t.Fatalf("request change not admitted: %+v", d)
			}
		})
	}
}

func TestWaitingNamesDueTriageAndOwnerReportAndStartupCount(t *testing.T) {
	f, l := triageFixture(t)
	s := openFixture(t, f.storePath)
	defer s.Close()
	check := func(owner bool) {
		t.Helper()
		err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, l.questions)
			if err != nil {
				return err
			}
			for _, row := range answer.Rows {
				if row.ItemID != "item-request" {
					continue
				}
				if owner {
					if !strings.HasPrefix(row.Mover, "operator;") || !strings.Contains(row.Why, "no document block") {
						t.Errorf("owner invisible: %+v", row)
					}
				} else if row.Mover != "triage on the design seat" {
					t.Errorf("triage invisible: %+v", row)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(false)
	c := *findTriagePlan(t, l, "item-request").decision.BlockConflict
	c.Status, c.WaitingOn = "owner", "owner"
	c.DesignRunID = "triage-settled"
	fixtureWrite(t, f, func(tx *store.Tx) error { return item.RecordConflict(tx, c, l.cfg.Now()) })
	check(true)
	if line := (SyncOutcome{UnsharedOpen: 1}).Line(); !strings.Contains(line, "unshared_open: 1") {
		t.Fatalf("start line count: %s", line)
	}
}
