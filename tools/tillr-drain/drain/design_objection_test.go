package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func settleDesignReport(t *testing.T, f refusalLoopFixture, l *Loop, d Decision, base, report string) item.Conflict {
	t.Helper()
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	outcome := l.settle(context.Background(), d, rec, report, base, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, false)
	if outcome.err != nil {
		t.Fatalf("settle: %+v", outcome)
	}
	c, _ := conflictOf(t, f, d.ItemID)
	return c
}

// The owner ruled on 2026-11-17 that a design run that commits nothing is an objection,
// and its report names who acts next. "build" says the block stands as written: the item
// goes back to its worker with no owner act and no design commit.
func TestADesignObjectionNamingBuildSendsTheItemBackToItsWorker(t *testing.T) {
	for _, footer := range []string{"RESULT: blocked", "RESULT: done"} {
		t.Run(footer, func(t *testing.T) {
			f, l, d, base := designAnswerFixture(t)
			c := settleDesignReport(t, f, l, d, base, "The worker misread c1; the block is right as written.\n"+DesignNextActorField+": build\n"+footer+"\n")
			if c.Status != "answered" || c.WaitingOn != "build" || !strings.Contains(c.Text, "block is right as written") {
				t.Fatalf("a build objection did not return the item to its worker: %+v", c)
			}
			if next := decisionFor(t, l, "item-a").decision; next.Action != ActionDispatch || next.BlockConflict != nil || next.ConflictID != c.ConflictID {
				t.Fatalf("the worker is not dispatched on the answered conflict: %+v", next)
			}
		})
	}
}

// An objection naming the owner goes to the owner, whatever its footer: that is the only
// way a design conflict reaches the owner.
func TestADesignObjectionNamingTheOwnerGoesToTheOwner(t *testing.T) {
	for _, report := range []string{"Needs a ruling between A and B.\n" + DesignNextActorField + ": owner\nRESULT: blocked\n", "Needs a ruling.\n" + DesignNextActorField + ": owner\nRESULT: done\n"} {
		f, l, d, base := designAnswerFixture(t)
		if c := settleDesignReport(t, f, l, d, base, report); c.Status != "owner" || c.WaitingOn != "owner" {
			t.Fatalf("an owner objection did not reach the owner (%q): %+v", report, c)
		}
	}
}

// A blocked design report that commits nothing and names no next actor is a missing
// output, not an objection to the owner: it goes back to the design seat to state its
// actor (the owner's ruling of 2026-11-17).
func TestABlockedDesignReportNamingNoActorGoesBackToTheDesignSeat(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	c := settleDesignReport(t, f, l, d, base, "Needs a ruling.\nRESULT: blocked\n")
	if c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, DesignNextActorField+": owner") {
		t.Fatalf("a blocked report with no actor did not go back to the design seat: %+v", c)
	}
}

// The meaning of a design run's ending is one pure function of its evidence.
func TestADesignRunsEndingHasOneReading(t *testing.T) {
	for _, tc := range []struct {
		committed, exists bool
		actor             string
		want              designReading
	}{
		{true, true, "", designAmended},
		{true, true, DesignNextOwner, designAmended},
		{true, false, "", designBlockRemoved},
		{false, true, DesignNextBuild, designObjectionBuild},
		{false, true, DesignNextOwner, designObjectionOwner},
		{false, true, "", designMissingActor},
	} {
		if got := designReadingOf(tc.committed, tc.exists, tc.actor); got != tc.want {
			t.Fatalf("designReadingOf(%v, %v, %q) = %s, want %s", tc.committed, tc.exists, tc.actor, got, tc.want)
		}
	}
}

// A design commit that removes or renames the item's block leaves no item to return to
// its worker. It goes back to the design seat naming the commit, not to the owner.
func TestADesignCommitThatRemovesTheBlockGoesBackToTheDesignSeat(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	target := l.laneTarget("lane-1")
	writeForkFile(t, filepath.Join(target, "briefs/item-a.md"), "the block is gone\n")
	runGit(t, target, "add", ".")
	runGit(t, target, "commit", "-qm", "remove the block")
	head := runGit(t, target, "rev-parse", "HEAD")
	c := settleDesignReport(t, f, l, d, base, "RESULT: done\n")
	if c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, shortSHA(head)) {
		t.Fatalf("a removed block did not go back to the design seat naming %s: %+v", shortSHA(head), c)
	}
}
