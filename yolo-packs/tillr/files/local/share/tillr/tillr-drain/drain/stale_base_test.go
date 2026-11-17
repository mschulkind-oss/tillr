package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// writeBlock writes an item's whole-file definition (its body_ref names no anchor).
func writeBlock(t *testing.T, root, id, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "briefs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "briefs", id+".md"), "<!-- tillr: item id=\""+id+"\" paths=\"tools/"+id+"/\" -->\n"+text+"\n")
}

func commitBlock(t *testing.T, root, id, text string) string {
	t.Helper()
	writeBlock(t, root, id, text)
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "block "+id+": "+text)
	return runGit(t, root, "rev-parse", TrunkRef)
}

func decisionFor(t *testing.T, l *Loop, id string) plannedApplication {
	t.Helper()
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == id {
			return p
		}
	}
	t.Fatalf("%s missing from the plan", id)
	return plannedApplication{}
}

func staleBaseEvents(t *testing.T, f refusalLoopFixture) []string {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	ids := []string{}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT object_id FROM events WHERE kind=? ORDER BY seq`, staleBaseKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func conflictOf(t *testing.T, f refusalLoopFixture, id string) (item.Conflict, bool) {
	t.Helper()
	var (
		c  item.Conflict
		ok bool
	)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		cs, err := item.ReadConflicts(tx.Reader())
		c, ok = cs[id]
		return err
	})
	return c, ok
}

// The planner syncs an item's block at the trunk tip. A proved commit carrying an
// older version (or none) of that block is not a base the item may be staged on: the
// claim cuts its lane from the oldest trunk commit that carries the block, without
// waiting for a proof and without taking the rest of the unproved range.
func TestLaneBaseCutsALaneFromTheCommitCarryingItsBlock(t *testing.T) {
	for _, mode := range []string{"amended", "added"} {
		t.Run(mode, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			green := runGit(t, f.root, "rev-parse", TrunkRef)
			if mode == "amended" {
				green = commitBlock(t, f.root, "item-a", "- **c1:** the first version")
			}
			runGit(t, f.root, "update-ref", GreenRef, green)
			carrier := commitBlock(t, f.root, "item-a", "- **c1:** the amended version the planner synced")
			runGit(t, f.root, "commit", "--allow-empty", "-qm", "later unproved trunk work")
			tip := runGit(t, f.root, "rev-parse", TrunkRef)
			p := decisionFor(t, l, "item-a")
			if p.decision.Action != ActionDispatch {
				t.Fatalf("an item whose block is newer than green waits: %+v", p.decision)
			}
			ok, work, err := l.applyPlan(context.Background(), p, tip, l.cfg.Now())
			if err != nil || !ok || work == nil {
				t.Fatalf("claim: %v %v %+v", err, ok, work)
			}
			if work.BaseSHA != carrier {
				t.Fatalf("base=%s want the commit carrying the block %s (green %s, tip %s)", work.BaseSHA, carrier, green, tip)
			}
		})
	}
}

func TestLaneBaseKeepsGreenWhenItCarriesTheBlock(t *testing.T) {
	f, l := passFixture(t, "item-a")
	green := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, green)
	commitBlock(t, f.root, "item-b", "- **c1:** another item's block")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), decisionFor(t, l, "item-a"), tip, l.cfg.Now())
	if err != nil || !ok || work.BaseSHA != green {
		t.Fatalf("claim moved off green: %v %v %+v", err, ok, work)
	}
}

func TestLaneBaseTipPolicyDoesNotWaitForTheBlock(t *testing.T) {
	f, l := passFixture(t, "item-a")
	green := commitBlock(t, f.root, "item-a", "- **c1:** the first version")
	runGit(t, f.root, "update-ref", GreenRef, green)
	commitBlock(t, f.root, "item-a", "- **c1:** amended")
	if err := os.MkdirAll(filepath.Join(f.root, "config", "tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config", "tillr", "gate-policy.json"), `{"lane_base":"tip"}`)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "tip policy")
	l.gatePolicy.LaneBase = "tip"
	if d := decisionFor(t, l, "item-a").decision; d.Action != ActionDispatch {
		t.Fatalf("tip policy held: %+v", d)
	}
}

// staleDispatch claims item-a on a proved commit carrying the block's first version
// while the planner has already read an amendment, which the trunk then commits. It
// returns the claimed work and the commit that carried the amendment.
func staleDispatch(t *testing.T, f refusalLoopFixture, l *Loop, id string) (*pendingWork, string) {
	t.Helper()
	base := commitBlock(t, f.root, id, "- **c1:** the first version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	writeBlock(t, f.root, id, "- **c1:** the amended version the planner synced")
	p := decisionFor(t, l, id)
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok || work == nil || work.BaseSHA != base {
		t.Fatalf("claim: %v %v %+v", err, ok, work)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "commit the amendment the planner read")
	return work, runGit(t, f.root, "rev-parse", TrunkRef)
}

func settleBlocked(t *testing.T, f refusalLoopFixture, l *Loop, d Decision, base string) {
	t.Helper()
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	outcome := l.settle(context.Background(), d, rec, "My item block is missing from the staged base.\n"+ReportFooter+" blocked\n", base, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, false)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle: %+v", outcome)
	}
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

// A worker that stops because its base lacks its own block is reporting the staging,
// not the item: no design or owner conflict, and the next dispatch restages it.
func TestStaleBaseBlockedReportRestagesWithoutAConflict(t *testing.T) {
	f, l := passFixture(t, "item-a")
	work, amended := staleDispatch(t, f, l, "item-a")
	settleBlocked(t, f, l, work.Decision, work.BaseSHA)
	if c, ok := conflictOf(t, f, "item-a"); ok {
		t.Fatalf("a stale-base report became a conflict: %+v", c)
	}
	if got := staleBaseEvents(t, f); len(got) != 1 || got[0] != work.Decision.RunRequest.RunID {
		t.Fatalf("stale-base record: %v", got)
	}
	// The proved commit still lacks the amendment. The restage does not wait for the
	// proof: it is cut from the commit that carries the block.
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch || p.decision.Reason != ReasonRestagedStaleBase {
		t.Fatalf("stale base not restaged: %+v", p.decision)
	}
	ok, next, err := l.applyPlan(context.Background(), p, amended, l.cfg.Now())
	if err != nil || !ok || next.BaseSHA != amended {
		t.Fatalf("restage claim: %v %v %+v", err, ok, next)
	}
}

// A report on a base that carried the block is the worker's real question.
func TestBlockedReportOnASufficientBaseStillRaisesAConflict(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := commitBlock(t, f.root, "item-a", "- **c1:** the only version")
	runGit(t, f.root, "update-ref", GreenRef, base)
	p := decisionFor(t, l, "item-a")
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", err, ok)
	}
	commitBlock(t, f.root, "item-b", "- **c1:** an unrelated block")
	settleBlocked(t, f, l, work.Decision, base)
	if c, ok := conflictOf(t, f, "item-a"); !ok || c.Status != "raised" || c.WaitingOn != "design" {
		t.Fatalf("real block report lost its conflict: %+v", c)
	}
	if got := staleBaseEvents(t, f); len(got) != 0 {
		t.Fatalf("sufficient base recorded stale: %v", got)
	}
}

// An owner conflict already raised from a stale base is answered by the loop at its
// next pass; a conflict raised on a base that carried the block is left to the owner.
func TestStaleBaseReleasesAnExistingOwnerConflict(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	// item-b's block is in the proved commit staleDispatch records.
	commitBlock(t, f.root, "item-b", "- **c1:** item-b's only version")
	staleWork, amended := staleDispatch(t, f, l, "item-a")
	pb := decisionFor(t, l, "item-b")
	ok, realWork, err := l.applyPlan(context.Background(), pb, amended, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim item-b: %v %v", err, ok)
	}
	if realWork.BaseSHA != staleWork.BaseSHA {
		t.Fatalf("item-b base=%s want %s", realWork.BaseSHA, staleWork.BaseSHA)
	}
	for _, w := range []*pendingWork{staleWork, realWork} {
		d := w.Decision
		rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if err := applyTerminal(tx, rec); err != nil {
				return err
			}
			return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-" + d.RunRequest.RunID, ItemID: d.ItemID, RunID: d.RunRequest.RunID, BodyRef: "briefs/" + d.ItemID + ".md", Text: "# run report\nneeds the owner", Status: "owner", WaitingOn: "owner", BlockedCriteria: []string{}}, l.cfg.Now())
		})
		if err := l.markExamined(context.Background(), d); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.releaseStaleBaseConflicts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "answered" || c.WaitingOn != "build" || !strings.Contains(c.Text, "Stale base") {
		t.Fatalf("stale owner conflict not answered: %+v", c)
	}
	if c, _ := conflictOf(t, f, "item-b"); c.Status != "owner" || c.WaitingOn != "owner" {
		t.Fatalf("real owner conflict touched: %+v", c)
	}
	if got := staleBaseEvents(t, f); len(got) != 1 || got[0] != staleWork.Decision.RunRequest.RunID {
		t.Fatalf("stale-base record: %v", got)
	}
	// A second pass reads nothing new and writes nothing.
	before := eventCount(t, f.storePath)
	if err := l.releaseStaleBaseConflicts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("second pass wrote %d event(s)", got-before)
	}
	pa := decisionFor(t, l, "item-a")
	if pa.decision.Action != ActionDispatch || pa.decision.Reason != ReasonConflictAnswered {
		t.Fatalf("released conflict did not restage: %+v", pa.decision)
	}
	if ok, next, err := l.applyPlan(context.Background(), pa, amended, l.cfg.Now()); err != nil || !ok || next.BaseSHA != amended {
		t.Fatalf("restage base: %v %v %+v (want %s)", err, ok, next, amended)
	}
	if d := decisionFor(t, l, "item-b").decision; d.Action != ActionHold || d.Reason != ReasonConflictOwner {
		t.Fatalf("owner hold lost: %+v", d)
	}
}

// A design run answering a conflict whose raising run was staged on a stale base is
// not asked to rule on it: the loop answers the conflict as a restage, whatever the
// design run reported, rather than handing it to the owner.
func TestStaleBaseDesignRunReportDoesNotReachTheOwner(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.SeatLists = map[string][]string{"design": {"fixture"}}
	work, amended := staleDispatch(t, f, l, "item-a")
	d := work.Decision
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		return item.RecordConflict(tx, item.Conflict{ConflictID: "conflict-" + rec.RunID, ItemID: "item-a", RunID: rec.RunID, BodyRef: "briefs/item-a.md", Text: "# run report\nblock missing", Status: "raised", WaitingOn: "design", BlockedCriteria: []string{}}, l.cfg.Now())
	})
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	p := decisionFor(t, l, "item-a")
	if p.decision.Action != ActionDispatch || p.decision.BlockConflict == nil {
		t.Fatalf("design seat not dispatched: %+v", p.decision)
	}
	ok, design, err := l.applyPlan(context.Background(), p, amended, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("design claim: %v %v", err, ok)
	}
	// The design seat is not cut from the same stale proved commit either.
	if design.BaseSHA != amended {
		t.Fatalf("design base=%s want %s", design.BaseSHA, amended)
	}
	settleBlocked(t, f, l, design.Decision, design.BaseSHA)
	if c, _ := conflictOf(t, f, "item-a"); c.Status != "answered" || c.WaitingOn != "build" {
		t.Fatalf("stale-base conflict reached %s/%s: %+v", c.Status, c.WaitingOn, c)
	}
	if next := decisionFor(t, l, "item-a").decision; next.Action != ActionDispatch || next.Reason != ReasonConflictAnswered {
		t.Fatalf("answered stale base did not restage: %+v", next)
	}
}
