package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func seedRefRun(t *testing.T, f refusalLoopFixture, id, item string, at int) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes (lane_id,state) VALUES ('lane-1','idle')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES (?,?,'lane-1','fixture','finished',?,?,'[]',1)`, id, item, at, at+1)
		return err
	})
}
func seedRefLanding(t *testing.T, f refusalLoopFixture, id, item, head, after string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO landings (landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after) VALUES (?,?,?,'verdict','landed',?,?,?,?)`, "landing-"+id, item, id, head, head, head, after)
		return err
	})
}
func sweepFixture(t *testing.T, f refusalLoopFixture, dry bool) []RefDisposition {
	t.Helper()
	refs, err := SweepRefs(context.Background(), RefSweepRequest{Root: f.root, Store: f.storePath, DryRun: dry})
	if err != nil {
		t.Fatal(err)
	}
	return refs
}
func refExists(t *testing.T, f refusalLoopFixture, ref string) bool {
	t.Helper()
	return runGit(t, f.root, "for-each-ref", "--format=%(refname)", ref) != ""
}

func TestRefSweepCoordinatesWithLandingBeforeEnumeratingRefs(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	scratch := scratchUnderDurable(t)
	// Deleting the last loose marker can remove its directory while a sibling
	// lander creates another marker. Both operations must hold land.lock.
	script := fmt.Sprintf(`#!/usr/bin/env python3
import fcntl, os, sys
if 'for-each-ref' in sys.argv or 'update-ref' in sys.argv:
    with open(%q, 'a+') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            pass
        else:
            print('ref sweep is not coordinated with landing', file=sys.stderr)
            sys.exit(91)
os.execv(%q, [%q] + sys.argv[1:])
`, filepath.Join(f.root, ".git", "land.lock"), real, real)
	if err := os.WriteFile(filepath.Join(scratch, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scratch+string(os.PathListSeparator)+os.Getenv("PATH"))
	sweepFixture(t, f, false)
}

func TestRefSweepUsesLandingNotSquashAncestryAndKeepsOperatorRefs(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	lane := filepath.Join(f.laneRoot, "lane-1", WorktreeDir)
	writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\n")
	runGit(t, lane, "commit", "-qam", "candidate")
	head := runGit(t, lane, "rev-parse", "HEAD")
	// A different parent and message produce a squash commit, not an ancestor.
	tree := runGit(t, lane, "rev-parse", "HEAD^{tree}")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	squash := runGit(t, f.root, "commit-tree", tree, "-p", base, "-m", "squash candidate")
	runGit(t, f.root, "reset", "--hard", squash)
	for i, id := range []string{"attempt-one", "conflict-fix", "attempt-two"} {
		seedRefRun(t, f, id, "item-a", i)
		runGit(t, f.root, "update-ref", "refs/tillr/runs/"+id, head)
	}
	seedRefLanding(t, f, "attempt-two", "item-a", head, squash)
	runGit(t, f.root, "update-ref", "refs/tillr/landed/attempt-two", squash)
	runGit(t, f.root, "update-ref", "refs/tillr/held/item-a", head)
	runGit(t, f.root, "update-ref", "refs/tillr/recovery/item-b", base)
	before := runGit(t, f.root, "for-each-ref", "refs/tillr")
	plan := sweepFixture(t, f, true)
	if got := runGit(t, f.root, "for-each-ref", "refs/tillr"); got != before {
		t.Fatal("dry run changed refs")
	}
	removed := 0
	operators := 0
	for _, ref := range plan {
		if ref.Remove {
			removed++
		}
		if operatorRef(ref.Ref) {
			operators++
			if ref.Remove || !strings.Contains(ref.Reason, "operator owns") || !strings.Contains(ref.Reason, "item state=in-queue") || !strings.Contains(ref.Reason, "trunk contains commit=") {
				t.Fatalf("operator disposition: %+v", ref)
			}
		}
		if ref.Remove && strings.Contains(ref.Ref, "/runs/") && !strings.Contains(ref.Reason, "operator ref still holds commit") {
			t.Fatalf("missing operator retention explanation: %+v", ref)
		}
	}
	if removed != 4 || operators != 2 {
		t.Fatalf("plan: %+v", plan)
	}
	sweepFixture(t, f, false)
	for _, id := range []string{"attempt-one", "conflict-fix", "attempt-two"} {
		if refExists(t, f, "refs/tillr/runs/"+id) {
			t.Fatal("pin remains:", id)
		}
	}
	if refExists(t, f, "refs/tillr/landed/attempt-two") {
		t.Fatal("recorded marker remains")
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/held/item-a"); got != head {
		t.Fatal("held ref moved")
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/recovery/item-b"); got != base {
		t.Fatal("recovery ref moved")
	}
	runGit(t, f.root, "cat-file", "-e", head)
	if ancestry := runGit(t, f.root, "rev-list", TrunkRef); strings.Contains(ancestry, head) {
		t.Fatal("fixture didn't squash")
	}
	for _, ref := range sweepFixture(t, f, false) {
		if ref.Remove {
			t.Fatal("second sweep removed a ref:", ref)
		}
	}
}

func TestRefSweepRetiresSetAsideResetBaseWithItsItem(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	seedRefRun(t, f, "attempt", "item-a", 1)
	for _, name := range []string{"attempt", "attempt-set-aside-base", "unknown-set-aside-base"} {
		runGit(t, f.root, "update-ref", "refs/tillr/runs/"+name, head)
	}
	// Before settlement both pointers must survive for crash recovery.
	sweepFixture(t, f, false)
	if !refExists(t, f, "refs/tillr/runs/attempt-set-aside-base") {
		t.Fatal("retryable reset destination lost")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
		return err
	})
	sweepFixture(t, f, false)
	for _, name := range []string{"attempt", "attempt-set-aside-base"} {
		if refExists(t, f, "refs/tillr/runs/"+name) {
			t.Fatal("finished item's pin remains:", name)
		}
	}
	if !refExists(t, f, "refs/tillr/runs/unknown-set-aside-base") {
		t.Fatal("unknown reset destination deleted")
	}
}

func TestDrainRetiresSetAsideResetBaseAfterClosure(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	l := p.replacement(t, &terminalSteps{})
	ref, err := l.SetAside(context.Background(), "item-a", "needs rework")
	if err != nil {
		t.Fatal(err)
	}
	if !refExists(t, p.f, ref+"-set-aside-base") {
		t.Fatal("set-aside did not retain reset destination")
	}
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`)
		return err
	})
	result, err := p.replacement(t, &terminalSteps{}).Run(context.Background())
	if err != nil || result.Stop != StopQueueDrained {
		t.Fatalf("drain: %+v / %v", result, err)
	}
	if refExists(t, p.f, ref) || refExists(t, p.f, ref+"-set-aside-base") {
		t.Fatal("closed set-aside item's refs remain after drain")
	}
}

func TestRefSweepKeepsLiveLaneRetryableAndUnrecordedMarker(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	seedRefRun(t, f, "live", "item-a", 1)
	seedRefRun(t, f, "earlier-conflict-attempt", "item-a", 0)
	seedRefRun(t, f, "retry", "item-b", 2)
	finishByHand(t, f, "closed")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state='leased',run_id='live' WHERE lane_id='lane-1'`)
		return err
	})
	for _, ref := range []string{"refs/tillr/runs/live", "refs/tillr/runs/earlier-conflict-attempt", "refs/tillr/runs/retry", "refs/tillr/landed/unrecorded"} {
		runGit(t, f.root, "update-ref", ref, head)
	}
	for _, ref := range sweepFixture(t, f, false) {
		if ref.Remove {
			t.Fatalf("unsafe deletion: %+v", ref)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE lanes SET state='idle',run_id=NULL`); return err })
	sweepFixture(t, f, false)
	if refExists(t, f, "refs/tillr/runs/live") || refExists(t, f, "refs/tillr/runs/earlier-conflict-attempt") {
		t.Fatal("closed pins not removed after lane release")
	}
	if !refExists(t, f, "refs/tillr/runs/retry") || !refExists(t, f, "refs/tillr/landed/unrecorded") {
		t.Fatal("retry evidence removed")
	}
}

func TestRefSweepKeepsRunsThatHaveNoItemLink(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	seedRefRun(t, f, "known", "item-a", 1)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES('unassociated',NULL,'lane-1','fixture','finished',1,2,'[]',1)`)
		return err
	})
	runGit(t, f.root, "update-ref", "refs/tillr/runs/unassociated", runGit(t, f.root, "rev-parse", TrunkRef))
	for _, ref := range sweepFixture(t, f, false) {
		if ref.Remove || !strings.Contains(ref.Reason, "unknown run") {
			t.Fatalf("unassociated run: %+v", ref)
		}
	}
}

func TestRefSweepBoundsExaminedRetryPins(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	for i := 0; i < RetryPinLimit+3; i++ {
		id := fmt.Sprintf("attempt-%02d", i)
		seedRefRun(t, f, id, "item-a", i)
		runGit(t, f.root, "update-ref", "refs/tillr/runs/"+id, head)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendEffect(tx, examinationDoneKind, id, struct{}{}, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
		})
	}
	seedRefRun(t, f, "unexamined-old-attempt", "item-a", -1)
	runGit(t, f.root, "update-ref", "refs/tillr/runs/unexamined-old-attempt", head)
	sweepFixture(t, f, false)
	if got := strings.Fields(runGit(t, f.root, "for-each-ref", "--format=%(refname)", "refs/tillr/runs")); len(got) != RetryPinLimit+1 {
		t.Fatalf("retry pins: %v", got)
	}
	if refExists(t, f, "refs/tillr/runs/attempt-00") {
		t.Fatal("oldest attempt retained")
	}
}

func TestRefSweepForkMarkerSurvivesSiblingBoundaryComparison(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	// Claim a sibling exactly as the loop does, with its bracket still open.
	plans := mustPlan(t, l)
	var sibling plannedApplication
	for _, p := range plans {
		if p.decision.ItemID == "item-b" {
			sibling = p
		}
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), sibling, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch sibling: %t %v", ok, err)
	}
	runGit(t, f.root, "update-ref", "refs/heads/patch/fixture", base)
	target := filepath.Join(f.laneRoot, "lane-2", WorktreeDir)
	before, err := l.snapshot(context.Background(), []string{target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tree := runGit(t, f.root, "rev-parse", base+"^{tree}")
	head := runGit(t, f.root, "commit-tree", tree, "-p", base, "-m", "fork delivery")
	seedRefRun(t, f, "fork", "item-a", 1)
	seedRefLanding(t, f, "fork", "item-a", head, head)
	marker := "refs/tillr/landed/fork"
	runGit(t, f.root, "update-ref", marker, head)
	runGit(t, f.root, "update-ref", "refs/heads/patch/fixture", head)
	sweepFixture(t, f, false)
	if !refExists(t, f, marker) {
		t.Fatal("fork marker removed before sibling boundary")
	}
	after, err := l.snapshot(context.Background(), []string{target}, nil)
	if err != nil {
		t.Fatal(err)
	}
	violations, observed, err := CompareRunObserved(before, after)
	if err != nil || len(violations) != 0 || len(observed) != 0 {
		t.Fatalf("fork delivery boundary: %v %v %v", violations, observed, err)
	}
	if err := l.markExamined(context.Background(), sibling.decision); err != nil {
		t.Fatal(err)
	}
	sweepFixture(t, f, false)
	if refExists(t, f, marker) {
		t.Fatal("fork marker remains after all siblings settle")
	}
}

func TestRefSweepForkMarkerDoesNotWaitForSupersededHistory(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	seedRefLanding(t, f, "fork-final", "item-a", head, head)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, id := range []string{"fork-before-conflict", "fork-final"} {
			work := pendingWork{Decision: Decision{ItemID: "item-a", RunRequest: &run.Request{RunID: id, ItemID: "item-a"}}}
			if err := appendEffect(tx, dispatchStartedKind, id, work, time.Now().UTC()); err != nil {
				return err
			}
		}
		return appendEffect(tx, examinationDoneKind, "fork-final", struct{}{}, time.Now().UTC())
	})
	marker := "refs/tillr/landed/fork-final"
	runGit(t, f.root, "update-ref", marker, head)
	sweepFixture(t, f, false)
	if refExists(t, f, marker) {
		t.Fatal("dead superseded attempt kept drained fork marker")
	}
}

func TestRefSweepNeverDereferencesSymbolicPinsIntoOperatorRefs(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	for _, name := range []string{"held", "recovery"} {
		id := "symbolic-" + name
		seedRefRun(t, f, id, "item-a", 1)
		operator := "refs/tillr/" + name + "/item-a"
		runGit(t, f.root, "update-ref", operator, head)
		runGit(t, f.root, "symbolic-ref", "refs/tillr/runs/"+id, operator)
	}
	finishByHand(t, f, "closed")
	sweepFixture(t, f, false)
	for _, name := range []string{"held", "recovery"} {
		if !refExists(t, f, "refs/tillr/"+name+"/item-a") {
			t.Fatal("symbolic pin deletion removed operator's", name, "ref")
		}
		if got := runGit(t, f.root, "rev-parse", "refs/tillr/"+name+"/item-a"); got != head {
			t.Fatal("operator ref changed")
		}
		if !refExists(t, f, "refs/tillr/runs/symbolic-"+name) {
			t.Fatal("unexpected symbolic ref was not retained for diagnosis")
		}
	}
}

func TestRefSweepDeletionDoesNotFollowAnAliasCreatedAfterEnumeration(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	seedRefRun(t, f, "sampled", "item-a", 1)
	finishByHand(t, f, "closed")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	pin, held := "refs/tillr/runs/sampled", "refs/tillr/held/item-a"
	runGit(t, f.root, "update-ref", pin, head)
	runGit(t, f.root, "update-ref", held, head)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(scratchUnderDurable(t), "git-wrapper")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(bin, "git")
	flag := filepath.Join(bin, "moved")
	// Model an external Git writer replacing a direct pin after it was sampled.
	writeForkFile(t, wrapper, fmt.Sprintf("#!/bin/sh\n%q \"$@\"\nstatus=$?\nif test \"$3\" = for-each-ref && ! test -f %q; then\n %q -C %q symbolic-ref %q %q || exit $?\n : > %q\nfi\nexit \"$status\"\n", realGit, flag, realGit, f.root, pin, held, flag))
	if err := os.Chmod(wrapper, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sweepFixture(t, f, false)
	if !refExists(t, f, held) {
		t.Fatal("concurrent alias caused deletion of operator ref")
	}
	if got := runGit(t, f.root, "rev-parse", held); got != head {
		t.Fatal("operator ref changed")
	}
	if refExists(t, f, pin) {
		t.Fatal("sampled pin was not deleted without dereferencing")
	}
}

func TestRefSweepOrdinaryMarkerDoesNotWaitForSibling(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	head := runGit(t, f.root, "rev-parse", TrunkRef)
	seedRefLanding(t, f, "ordinary", "item-a", head, head)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id) VALUES('lane-2','leased','sibling')`); err != nil {
			return err
		}
		return appendEffect(tx, "land.landing", "ordinary", map[string]string{"run_id": "ordinary", "item_id": "item-a", "result": "landed", "base_ref": "main"}, time.Now().UTC())
	})
	marker := "refs/tillr/landed/ordinary"
	runGit(t, f.root, "update-ref", marker, head)
	sweepFixture(t, f, false)
	if refExists(t, f, marker) {
		t.Fatal("ordinary squash marker waited for sibling despite durable receipt")
	}
}

func TestSavedEditCleanupDoesNotDeleteAnOperatorRefThroughAnAlias(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	seedRefRun(t, f, "saved-alias", "item-a", 1)
	writeForkFile(t, filepath.Join(l.lanePath("lane-1"), "base.txt"), "saved bytes\n")
	if stop := l.abandon(context.Background(), "lane-1", "saved-alias", l.headOf(context.Background(), "lane-1")); stop != "" {
		t.Fatal(stop)
	}
	ref := preservedPrefix + "saved-alias"
	head := runGit(t, f.root, "rev-parse", ref)
	operator := "refs/tillr/held/item-a"
	runGit(t, f.root, "update-ref", operator, head)
	runGit(t, f.root, "update-ref", "-d", ref)
	runGit(t, f.root, "symbolic-ref", ref, operator)
	finishByHand(t, f, "closed")
	if err := l.prunePreservedWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !refExists(t, f, operator) {
		t.Fatal("saved-edit pruning deleted operator's held ref")
	}
	if got := runGit(t, f.root, "rev-parse", operator); got != head {
		t.Fatal("operator ref changed")
	}
}

func TestRefSweepSavedEditsUseExistingRetention(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	seedRefRun(t, f, "saved", "item-a", 1)
	writeForkFile(t, filepath.Join(l.lanePath("lane-1"), "base.txt"), "uncommitted work\n")
	if stop := l.abandon(context.Background(), "lane-1", "saved", l.headOf(context.Background(), "lane-1")); stop != "" {
		t.Fatal(stop)
	}
	ref := preservedPrefix + "saved"
	sweepFixture(t, f, false)
	if !refExists(t, f, ref) {
		t.Fatal("retryable saved edits removed")
	}
	finishByHand(t, f, "closed")
	sweepFixture(t, f, true)
	if !refExists(t, f, ref) {
		t.Fatal("dry run removed saved edits")
	}
	sweepFixture(t, f, false)
	if refExists(t, f, ref) {
		t.Fatal("finished saved edits remain")
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err == nil && (len(saved) != 1 || !saved[0].Pruned) {
			t.Fatalf("missing pruning event: %+v", saved)
		}
		return err
	})
	for _, ref := range sweepFixture(t, f, false) {
		if ref.Remove {
			t.Fatal("repeat removed:", ref)
		}
	}
}
