package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAcquitKeepsConvictionAndReadmitsCandidate(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	c := Candidate{ItemID: "item-a", RunID: p.runID, LaneID: "lane-1"}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		return appendEffect(tx, BoundaryCheckKind, p.runID, map[string]any{"clean": false, "runs": []runRef{{RunID: p.runID, ItemID: "item-a", LaneID: "lane-1"}}}, l.cfg.Now())
	})
	if err := l.holdItemFault(context.Background(), c, StopHostBoundaryViolation, "another process wrote it"); err != nil {
		t.Fatal(err)
	}
	if d := mustPlan(t, l)[0].decision; d.Action != ActionHold {
		t.Fatalf("conviction did not hold: %+v", d)
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error { return AcquitRun(tx, p.runID, "operator checked attribution", l.cfg.Now()) })
	if d := mustPlan(t, l)[0].decision; d.Action != ActionJudge {
		t.Fatalf("acquittal did not readmit: %+v", d)
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		if len(faults) != 0 {
			t.Fatalf("fault survived: %+v", faults)
		}
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		if works["item-a"].BoundaryFailed || works["item-a"].Candidate == nil {
			t.Fatalf("candidate not eligible: %+v", works["item-a"])
		}
		return nil
	})
	events := eventsAfter(t, p.f.storePath, 0)
	held, cleared := 0, 0
	for _, e := range events {
		if e.kind == itemFaultKind {
			held++
		}
		if e.kind == itemFaultClearedKind {
			cleared++
			if !strings.Contains(string(e.payload), "operator checked attribution") {
				t.Fatal("explanation lost")
			}
		}
	}
	if held != 1 || cleared != 1 {
		t.Fatalf("conviction history: %d held, %d cleared", held, cleared)
	}
	s := openFixture(t, p.f.storePath)
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { return AcquitRun(tx, p.runID, "again", l.cfg.Now()) }); err == nil {
		t.Fatal("acquitted run with no conviction")
	}
}

func TestExamineUsesLoopExamination(t *testing.T) {
	for _, manual := range []bool{false, true} {
		p := newPendingTerminalFixture(t, true, "")
		steps := &terminalSteps{}
		l := p.replacement(t, steps)
		var result LoopResult
		var err error
		if manual {
			result, err = l.Examine(context.Background(), p.runID)
		} else {
			result, err = l.Run(context.Background())
		}
		if err != nil {
			t.Fatal(err)
		}
		if result.Landed != 1 || steps.gates != 1 || steps.lands != 1 || steps.verdicts != 1 {
			t.Fatalf("manual=%v: %+v / %+v", manual, result, steps)
		}
		fixtureWrite(t, p.f, func(tx *store.Tx) error {
			works, err := readPending(tx.Reader())
			if err != nil {
				return err
			}
			if !works["item-a"].Done {
				t.Fatal("not examined")
			}
			var state string
			if err := tx.QueryRow(`SELECT state FROM lanes WHERE lane_id='lane-1'`).Scan(&state); err != nil {
				return err
			}
			if state != "idle" {
				t.Fatal(state)
			}
			return nil
		})
	}
}

func TestRecoveryRefusesLeasedLane(t *testing.T) {
	for _, face := range []string{"examine", "set-aside"} {
		p := newPendingTerminalFixture(t, true, "")
		l := p.replacement(t, &terminalSteps{})
		fixtureWrite(t, p.f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE lanes SET state='leased',run_id=?,leased_until=NULL WHERE lane_id='lane-1'`, p.runID)
			return err
		})
		before := snapshotStore(t, p.f.storePath)
		var err error
		if face == "examine" {
			_, err = l.Examine(context.Background(), p.runID)
		} else {
			_, err = l.SetAside(context.Background(), "item-a", "needs rework")
		}
		if err == nil {
			t.Fatalf("%s stole lane", face)
		}
		if got := snapshotStore(t, p.f.storePath); got != before {
			t.Fatalf("%s wrote despite refusal", face)
		}
		if head := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD"); head != p.head {
			t.Fatal("moved refused candidate")
		}
	}
}

func TestSetAsideRetainsHeadAndRestartDoesNotExamineMissingCandidate(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	ref, err := l.SetAside(context.Background(), "item-a", "cannot land this change")
	if err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, p.f.root, "rev-parse", ref); got != p.head {
		t.Fatalf("retained %s, want %s", got, p.head)
	}
	if got := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD"); got != p.base {
		t.Fatalf("lane not freed: %s", got)
	}
	steps := &terminalSteps{}
	restarted := p.replacement(t, steps)
	result, err := restarted.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Stop != StopQueueDrained || steps.gates != 0 {
		t.Fatalf("restart examined missing candidate: %+v / %+v", result, steps)
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		if !strings.Contains(faults["item-a"].Detail, "cannot land this change") {
			t.Fatal("rework reason missing")
		}
		return nil
	})
}

func TestSetAsideRollsBackRecordAndLaneOnStoreFailure(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_set_aside BEFORE INSERT ON events WHEN NEW.kind='drain.set-aside' BEGIN SELECT RAISE(ABORT,'fixture store failure'); END`)
		return err
	})
	before := snapshotStore(t, p.f.storePath)
	_, err := l.SetAside(context.Background(), "item-a", "needs rework")
	if err == nil || !strings.Contains(err.Error(), "fixture store failure") {
		t.Fatalf("unexpected result: %v", err)
	}
	if got := snapshotStore(t, p.f.storePath); got != before {
		t.Fatalf("partial record: %+v != %+v", got, before)
	}
	if head := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD"); head != p.head {
		t.Fatal("lane head was not restored")
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error { _, err := tx.Exec(`DROP TRIGGER refuse_set_aside`); return err })
	if _, err := l.SetAside(context.Background(), "item-a", "needs rework"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestSetAsideCanFinishAfterInterruptionFollowingReset(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	if err := l.pin(context.Background(), p.runID, p.head); err != nil {
		t.Fatal(err)
	}
	runGit(t, p.f.root, "update-ref", "refs/tillr/runs/"+p.runID+"-set-aside-base", p.base)
	runGit(t, l.lanePath("lane-1"), "reset", "--hard", p.base)
	if _, err := l.SetAside(context.Background(), "item-a", "finishing interrupted recovery"); err != nil {
		t.Fatal(err)
	}
	result, err := p.replacement(t, &terminalSteps{}).Run(context.Background())
	if err != nil || result.Stop != StopQueueDrained {
		t.Fatalf("restart: %+v / %v", result, err)
	}
}
