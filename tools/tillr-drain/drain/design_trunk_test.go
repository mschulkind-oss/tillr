package drain

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// landProven commits files on trunk by hand — no trunk fixer, no recorded landing — and
// moves the heavy proof's green pointer to the new tip.
func landProven(t *testing.T, f refusalLoopFixture, files map[string]string) string {
	t.Helper()
	for path, body := range files {
		writeForkFile(t, filepath.Join(f.root, path), body)
		runGit(t, f.root, "add", path)
	}
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "a hand commit")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, tip)
	return tip
}

// pinDesignCandidate records a design run's candidate as a commit on its base that
// changes path, at the run's pin.
func pinDesignCandidate(t *testing.T, f refusalLoopFixture, l *Loop, runID, path string) {
	t.Helper()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	lane := l.lanePath("lane-1")
	runGit(t, lane, "checkout", "-q", "--detach", base)
	writeForkFile(t, filepath.Join(lane, path), "the design run's edit\n")
	runGit(t, lane, "add", path)
	runGit(t, lane, "commit", "-qm", "design candidate")
	runGit(t, f.root, "update-ref", "refs/tillr/runs/"+runID, runGit(t, lane, "rev-parse", "HEAD"))
	runGit(t, lane, "checkout", "-q", "--detach", base)
}

func lastDesignMark(t *testing.T, f refusalLoopFixture, conflict string) designUnanswered {
	t.Helper()
	var marks map[string][]designUnanswered
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		marks, err = readDesignUnanswered(r)
		return err
	})
	m := marks[conflict]
	if len(m) == 0 {
		t.Fatalf("no drain.design-unanswered record for %s", conflict)
	}
	return m[len(m)-1]
}

// On 2026-11-17 two design runs were refused at the gate only for two dead roadmap
// anchors already on their base, and both went to the owner with nothing changed. A hand
// commit fixed the anchors, but it was no recorded trunk fixer and touched neither block,
// so neither conflict was raised again. A gate refusal is suspected on the trunk: it is no
// attempt, and once a trunk commit the heavy proof has proven changes a file the refused
// candidate touched, the conflict is raised again without spending the input-change
// allowance.
func TestAGateRefusedDesignRunIsRaisedOnceAProvenTrunkCommitChangesItsFiles(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalGate)
	pinDesignCandidate(t, f, l, design, "roadmap.md")
	settleDesign(t, l)
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "owner" || c.DesignRunID != design || !strings.Contains(c.Text, "The trunk may have caused it") {
		t.Fatalf("the refusal was not held as suspected on the trunk: %+v", c)
	}
	if m := lastDesignMark(t, f, c.ConflictID); !m.TrunkSuspect || m.BaseSHA == "" || m.Reraised {
		t.Fatalf("the record does not name the suspect base: %+v", m)
	}
	// A proven trunk commit elsewhere, or an unproven one to the same file, raises nothing.
	landProven(t, f, map[string]string{"elsewhere.txt": "x\n"})
	writeForkFile(t, filepath.Join(f.root, "roadmap.md"), "unproven fix\n")
	runGit(t, f.root, "add", "roadmap.md")
	runGit(t, f.root, "commit", "-qm", "unproven fix")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("a trunk commit that changed none of the candidate's files, or one not proven, raised the conflict: %+v", c)
	}
	fix := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, fix)
	settleDesign(t, l)
	c, _ = conflictOf(t, f, "item-a")
	if c.Status != "raised" || c.WaitingOn != "design" || c.DesignRunID != "" || !strings.Contains(c.Text, "Trunk commit "+shortSHA(fix)) {
		t.Fatalf("the proven trunk change did not raise the conflict: %+v", c)
	}
	if m := lastDesignMark(t, f, c.ConflictID); !m.TrunkReraised || m.Reraised {
		t.Fatalf("the trunk re-raise spent the input-change allowance or was not recorded: %+v", m)
	}
	// The input-change allowance is unspent: a later no-op refusal after a block change
	// still earns it.
	endDesignRun(t, f, l, RefusalNoOp)
	commitBlock(t, f.root, "item-a", "- **c1:** amended after the trunk re-raise")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" {
		t.Fatalf("the trunk re-raise spent the input-change allowance: %+v", c)
	}
}

// A second refusal with the same outcome and failing checks after a trunk re-raise is the
// candidate's: an attempt, which a later proven trunk change does not raise again.
func TestASecondIdenticalRefusalAfterATrunkReraiseIsAnAttempt(t *testing.T) {
	f, l := designConflictFixture(t)
	endDesignRun(t, f, l, RefusalGate)
	settleDesign(t, l)
	landProven(t, f, nil)
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" {
		t.Fatalf("fixture: the first refusal was not raised again: %+v", c)
	}
	second := endDesignRun(t, f, l, RefusalGate)
	settleDesign(t, l)
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "owner" || c.DesignRunID != second || lastDesignMark(t, f, c.ConflictID).TrunkSuspect {
		t.Fatalf("a repeated refusal was suspected on the trunk again: %+v", c)
	}
	landProven(t, f, nil)
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("a repeated refusal was raised again by a later trunk change: %+v", c)
	}
}

// A design run that reported trunk-broken, refused as a no-op with no candidate, is
// raised again once any trunk commit past its base is proven.
func TestADesignRunReportingTrunkBrokenIsRaisedOnceATrunkCommitIsProvenPastItsBase(t *testing.T) {
	f, l := designConflictFixture(t)
	endDesignRunReporting(t, f, l, RefusalNoOp, "# run report\n\nThe base is red.\n\nRESULT: trunk-broken\n")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" || !strings.Contains(c.Text, "The trunk may have caused it") {
		t.Fatalf("the trunk-broken report was not suspected on the trunk: %+v", c)
	}
	landProven(t, f, nil)
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" {
		t.Fatalf("the trunk-broken report was not raised after a proven trunk change: %+v", c)
	}
}

// A no-op refusal whose report blames nothing on the trunk stays an ordinary attempt.
func TestANoOpDesignRunThatDoesNotBlameTheTrunkStaysAnAttempt(t *testing.T) {
	f, l := designConflictFixture(t)
	endDesignRunReporting(t, f, l, RefusalNoOp, "# run report\n\nRESULT: done\n")
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" || strings.Contains(c.Text, "The trunk may have caused it") {
		t.Fatalf("a no-op that blames nothing was suspected on the trunk: %+v", c)
	}
	landProven(t, f, nil)
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("a no-op that blames nothing was raised by a trunk change: %+v", c)
	}
}

// The records written before this rule name no base. Those owner holds are re-read: a
// gate refusal or a trunk-broken report recorded that way is raised once a proven trunk
// commit changes what the run touched, which is how the three 2026-11-17 holds recover.
func TestALegacyUnansweredRecordIsRaisedOnceAProvenTrunkCommitChangesItsFiles(t *testing.T) {
	f, l := designConflictFixture(t)
	design := endDesignRun(t, f, l, RefusalGate)
	pinDesignCandidate(t, f, l, design, "roadmap.md")
	c, _ := conflictOf(t, f, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		mark := designUnanswered{ConflictID: c.ConflictID, ItemID: "item-a", RunID: design, Outcome: "refused at gate", Changes: []string{}}
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\n\nDesign run " + design + " ended without answering this conflict (refused at gate)."
		if err := item.RecordConflict(tx, c, l.cfg.Now()); err != nil {
			return err
		}
		return appendEffect(tx, designUnansweredKind, design, mark, l.cfg.Now())
	})
	landProven(t, f, map[string]string{"elsewhere.txt": "x\n"})
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "owner" {
		t.Fatalf("a trunk change elsewhere raised the legacy hold: %+v", c)
	}
	landProven(t, f, map[string]string{"roadmap.md": "the anchors, fixed by hand\n"})
	settleDesign(t, l)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "raised" || c.WaitingOn != "design" {
		t.Fatalf("the legacy hold was not raised after the proven fix: %+v", c)
	}
}
