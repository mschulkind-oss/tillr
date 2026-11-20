package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func lastPushEvent(t *testing.T, f refusalLoopFixture) map[string]any {
	t.Helper()
	var raw string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw)
	})
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal push payload %s: %v", raw, err)
	}
	return out
}

func TestBoundaryExcusesPublishedRefs(t *testing.T) {
	before := &Boundary{Refs: map[string]string{}}
	after := &Boundary{Refs: map[string]string{
		land.PublishedRefPrefix + "origin/main": strings.Repeat("a", 40),
	}}
	violations, _, err := CompareRunObserved(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected published ref to be excused by CompareRunObserved, got %v", violations)
	}
	compareViolations, err := Compare(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(compareViolations) != 0 {
		t.Fatalf("expected published ref to be excused by Compare, got %v", compareViolations)
	}
}

func TestPushAdvancesPublishedRefAndConfirmsOnAlreadyPublished(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	pubRef := publishedRef("origin", "refs/heads/"+TrunkRef)
	if got := optionalCommit(ctx, p.f.root, pubRef); got != "" {
		t.Fatalf("expected published ref to start unset, got %s", got)
	}

	// Simulate an already-published green commit where remote-tracking ref is ahead of green
	// but published ref is still unset.
	green := runGit(t, p.f.root, "rev-parse", GreenRef)
	if err := pushTrunk(ctx, p.f.root, settings.Push{}); err != nil {
		t.Fatalf("pushTrunk: %v", err)
	}
	if got := optionalCommit(ctx, p.f.root, pubRef); got != p.local[2] {
		t.Fatalf("published ref after pushTrunk = %s, want %s", got, p.local[2])
	}

	// Reset published ref behind green, then call pushGreen which returns errAlreadyPublished.
	runGit(t, p.f.root, "update-ref", pubRef, p.local[0])
	gotGreen, err := pushGreen(ctx, p.f.root, settings.Push{})
	if !errors.Is(err, errAlreadyPublished) || gotGreen != green {
		t.Fatalf("pushGreen = (%s, %v), want errAlreadyPublished", gotGreen, err)
	}
	if got := optionalCommit(ctx, p.f.root, pubRef); got != green {
		t.Fatalf("published ref after errAlreadyPublished = %s, want %s", got, green)
	}

	// Now advance green to local[2] while remote is behind at local[1] (reset remote to local[1]),
	// so pushGreen performs an actual push and advances published ref to local[2].
	runGit(t, p.f.root, "update-ref", "refs/remotes/origin/"+TrunkRef, p.local[1])
	runGit(t, p.remote, "update-ref", "refs/heads/"+TrunkRef, p.local[1])
	runGit(t, p.f.root, "update-ref", GreenRef, p.local[2])
	gotGreen, err = pushGreen(ctx, p.f.root, settings.Push{})
	if err != nil || gotGreen != p.local[2] {
		t.Fatalf("actual pushGreen = (%s, %v), want (%s, nil)", gotGreen, err, p.local[2])
	}
	if got := optionalCommit(ctx, p.f.root, pubRef); got != p.local[2] {
		t.Fatalf("published ref after actual pushGreen = %s, want %s", got, p.local[2])
	}
}

func TestRefusedPushStopsOnRemoteForcePush(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	if err := pushTrunk(ctx, p.f.root, settings.Push{}); err != nil {
		t.Fatalf("initial pushTrunk: %v", err)
	}
	rPrev := p.local[2]

	// Force-push the remote branch back to its parent and replace with a different commit.
	runGit(t, p.other, "fetch", "-q", "origin", TrunkRef)
	runGit(t, p.other, "reset", "--hard", rPrev+"^")
	writeForkFile(t, filepath.Join(p.other, "rewritten.txt"), "rewritten\n")
	runGit(t, p.other, "add", ".")
	runGit(t, p.other, "commit", "-qm", "rewritten")
	runGit(t, p.other, "push", "--force", "-q", "origin", TrunkRef)
	rNew := runGit(t, p.other, "rev-parse", "HEAD")

	// Advance local trunk so a push is attempted and refused, then check force-push guard.
	ownerCommit(t, p.f.root, "four.txt")

	err := p.refused(t)
	before := runGit(t, p.f.root, "rev-parse", "HEAD")
	_, herr := p.l.healRefusedPush(ctx, settings.Push{}, err)
	var se *pushStateError
	if !errors.As(herr, &se) || se.kind != pushKindRewritten {
		t.Fatalf("heal error = %v, want remote-rewritten", herr)
	}
	if !strings.Contains(se.Error(), "mover: operator; reconcile force-pushed origin/"+TrunkRef+" ("+shortSHA(rPrev)+" -> "+shortSHA(rNew)+")") {
		t.Fatalf("unexpected detail: %s", se.Error())
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != before {
		t.Fatal("force-push guard moved HEAD")
	}
}

func TestPushClassificationAndStructuredState(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(2 * time.Minute)
	now := t0
	p.l.cfg.Now = func() time.Time { return now }

	// Install a pre-receive hook that declines pushes with [remote rejected].
	hookPath := filepath.Join(p.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho 'declined by policy' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	err := pushTrunk(ctx, p.f.root, settings.Push{})
	var se *pushStateError
	if !errors.As(err, &se) || se.kind != pushKindRemoteDeclined {
		t.Fatalf("pushTrunk error = %v, want push-remote-declined", err)
	}
	p.l.recordPush(ctx, settings.PushTip, p.local[2], err)
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != pushKindRemoteDeclined || ev["escalated_mover"] != "operator" || ev["since"] != t0.Format(time.RFC3339) {
		t.Fatalf("unexpected declined event: %+v", ev)
	}

	// A second failure preserves since across consecutive non-ok updates.
	now = t1
	p.l.recordPush(ctx, settings.PushTip, p.local[2], &pushStateError{kind: pushKindWorktreeDirty, detail: "dirty", escalated: "operator"})
	ev2 := lastPushEvent(t, p.f)
	if ev2["kind"] != pushKindWorktreeDirty || ev2["since"] != t0.Format(time.RFC3339) {
		t.Fatalf("since not preserved: %+v", ev2)
	}

	// Remove hook and succeed: ok push clears since.
	if err := os.Remove(hookPath); err != nil {
		t.Fatal(err)
	}
	if err := pushTrunk(ctx, p.f.root, settings.Push{}); err != nil {
		t.Fatalf("pushTrunk after hook removed: %v", err)
	}
	p.l.recordPush(ctx, settings.PushTip, p.local[2], nil)
	evOK := lastPushEvent(t, p.f)
	if evOK["kind"] != pushKindOK {
		t.Fatalf("want push-ok, got %+v", evOK)
	}
	if _, hasSince := evOK["since"]; hasSince {
		t.Fatalf("push-ok should clear since: %+v", evOK)
	}

	// Active unacknowledged patch_moves hold classifies noRefLine push-failed as push-patch-move-held.
	before := &Boundary{Repo: p.f.root, Refs: map[string]string{"refs/heads/patch/pi": "old"}}
	after := &Boundary{Repo: p.f.root, Refs: map[string]string{"refs/heads/patch/pi": "new"}}
	_, observed, err := CompareRunObserved(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.l.recordBoundaryCheck(ctx, BoundaryScopeRun, "tick", []runRef{{RunID: "run-a", ItemID: "item-a", LaneID: "lane-1"}}, nil, nil, t0, observed...); err != nil {
		t.Fatal(err)
	}
	kind, _ := p.l.classifyPushFailure(ctx, &pushStateError{kind: pushKindFailed, detail: "pre-push failed", noRefLine: true})
	if kind != pushKindPatchMoveHeld {
		t.Fatalf("kind = %s, want %s", kind, pushKindPatchMoveHeld)
	}
}

func TestAfterGreenRecordsProvingStateOnCleanReplay(t *testing.T) {
	p := newPushFixture(t)
	p.remoteCommit(t, "remote.txt", "remote\n")
	green := runGit(t, p.f.root, "rev-parse", GreenRef)
	p.l.afterGreen(context.Background(), green)
	for p.l.pushAction.busy() {
		time.Sleep(10 * time.Millisecond)
	}
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != pushKindProving {
		t.Fatalf("afterGreen clean replay kind = %v, want %s (%+v)", ev["kind"], pushKindProving, ev)
	}
}

func TestRefusedPushDirectRewritesSupersessionIdentityAndMarkers(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	// Mark local[0] as landed by run-x and accept its identity.
	runGit(t, p.f.root, "update-ref", land.LandedRefPrefix+"run-x", p.local[0])
	fixtureWrite(t, p.f, func(tx *store.Tx) error {
		return land.AcceptIdentity(tx, p.local[0], "owner", "accepted local")
	})

	// Remote applies the exact same change as local[0] (one.txt), so local[0] is superseded!
	p.remoteCommit(t, "one.txt", "one.txt\n")
	err := p.refused(t)
	n, herr := p.l.healRefusedPush(ctx, settings.Push{}, err)
	if herr != nil || n != 3 {
		t.Fatalf("heal = %d, %v", n, herr)
	}
	if h := rewriteHistory(t, p.f); h.Lost != "" {
		t.Fatalf("trunk-rewritten hold raised on supersession: %+v", h)
	}
	// Landed marker for run-x should now point to the replacement commit (remoteTip) and
	// trunk_rewrites + trunk_identity_acceptances should both map local[0] -> replacement.
	marker := runGit(t, p.f.root, "rev-parse", land.LandedRefPrefix+"run-x")
	if marker == p.local[0] {
		t.Fatalf("landed marker was not updated from %s", p.local[0])
	}
	fixtureRead(t, p.f, func(r *store.Reader) error {
		resolved, err := heavy.RewriteResolve(r, p.local[0])
		if err != nil || resolved != marker {
			t.Fatalf("RewriteResolve(%s) = (%s, %v), want %s", p.local[0], resolved, err, marker)
		}
		var count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM trunk_identity_acceptances WHERE sha=?`, marker).Scan(&count); err != nil || count != 1 {
			t.Fatalf("carried acceptance count = %d, err = %v", count, err)
		}
		return nil
	})
}

func TestRemoteRebaseTriggersRollPaths(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	rolled := filepath.Join(filepath.Dir(p.f.root), "rolled.txt")
	p.l.cfg.PostLanding.Roll = settings.Roll{
		Command: "touch " + rolled,
		Paths:   []string{"tools/tillr-drain/"},
	}
	// Remote commits a change under tools/tillr-drain/ while local green is already at local[1].
	if err := os.MkdirAll(filepath.Join(p.other, "tools", "tillr-drain"), 0755); err != nil {
		t.Fatal(err)
	}
	p.remoteCommit(t, "tools/tillr-drain/x.txt", "upstream tool\n")
	p.l.afterGreen(ctx, runGit(t, p.f.root, "rev-parse", GreenRef))
	for p.l.pushAction.busy() || p.l.rollAction.busy() {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(rolled); err != nil {
		t.Fatalf("expected roll command to run for remote tools/tillr-drain/ change: %v", err)
	}
}

func TestAfterLandingRemoteDeclinedDoesNotFetchOrRebase(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	p.l.cfg.PostLanding.Push.What = settings.PushTip

	// Install a pre-receive hook that declines with [remote rejected] on a fast-forwardable push.
	mainBefore := runGit(t, p.f.root, "rev-parse", "HEAD")
	remoteBefore := runGit(t, p.remote, "rev-parse", "refs/heads/"+TrunkRef)

	hookPath := filepath.Join(p.remote, "hooks", "pre-receive")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho 'declined by server policy' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.l.afterLandingCandidate(ctx, Candidate{ItemID: "item-a"}, p.local[2])
	for p.l.pushAction.busy() {
		time.Sleep(10 * time.Millisecond)
	}
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != pushKindRemoteDeclined || ev["escalated_mover"] != "operator" {
		t.Fatalf("push event = %+v, want %s with escalated_mover=operator", ev, pushKindRemoteDeclined)
	}
	if runGit(t, p.f.root, "rev-parse", "HEAD") != mainBefore {
		t.Fatal("main moved on remote-declined")
	}
	if got := runGit(t, p.remote, "rev-parse", "refs/heads/"+TrunkRef); got != remoteBefore {
		t.Fatalf("remote ref changed (%s -> %s)", remoteBefore, got)
	}
	if _, err := os.Stat(filepath.Join(p.f.root, ".git", "FETCH_HEAD")); err == nil {
		t.Fatal("FETCH_HEAD created on remote-declined (fetch should not run)")
	}
}

func TestPushHookFailureWithoutPatchMoveRecordsPushFailed(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	hookDir := filepath.Join(p.f.root, ".git", "hooks")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	hookPath := filepath.Join(hookDir, "pre-push")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho 'local pre-push denied' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.l.afterLandingCandidate(ctx, Candidate{ItemID: "item-a"}, p.local[2])
	for p.l.pushAction.busy() {
		time.Sleep(10 * time.Millisecond)
	}
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != pushKindFailed {
		t.Fatalf("push event = %+v, want kind=%s", ev, pushKindFailed)
	}
}

func gitPatchID(t *testing.T, repo, commit string) string {
	t.Helper()
	show := exec.Command("git", "-C", repo, "show", commit)
	patch := exec.Command("git", "patch-id", "--stable")
	var err error
	patch.Stdin, err = show.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := show.Start(); err != nil {
		t.Fatal(err)
	}
	out, err := patch.Output()
	if err != nil {
		t.Fatalf("git patch-id: %v", err)
	}
	_ = show.Wait()
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func TestRefusedPushShiftedContextChangesPatchID(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	l.cfg.PostLanding.Push.What = settings.PushGreen

	// Base commit has a 10-line shared.txt.
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, strings.Repeat("line", 1)+string(rune('0'+i%10)))
	}
	writeForkFile(t, filepath.Join(f.root, "shared.txt"), strings.Join(lines, "\n")+"\n")
	runGit(t, f.root, "add", "shared.txt")
	runGit(t, f.root, "commit", "-qm", "base shared.txt")

	remote := filepath.Join(filepath.Dir(f.root), "remote.git")
	other := filepath.Join(filepath.Dir(f.root), "other")
	runGit(t, f.root, "init", "--bare", "-q", remote)
	runGit(t, f.root, "remote", "add", "origin", remote)
	runGit(t, f.root, "push", "-q", "-u", "origin", TrunkRef)
	runGit(t, f.root, "clone", "-q", "-b", TrunkRef, remote, other)

	// Local commit C1 edits line 9 of shared.txt.
	localLines := append([]string(nil), lines...)
	localLines[8] = "local-edit-9"
	writeForkFile(t, filepath.Join(f.root, "shared.txt"), strings.Join(localLines, "\n")+"\n")
	runGit(t, f.root, "add", "shared.txt")
	runGit(t, f.root, "commit", "-qm", "local edit line 9")
	c1 := runGit(t, f.root, "rev-parse", "HEAD")

	// Remote commit R edits line 7 of shared.txt (within 3-line diff context of line 9, no conflict).
	remoteLines := append([]string(nil), lines...)
	remoteLines[6] = "remote-edit-7"
	writeForkFile(t, filepath.Join(other, "shared.txt"), strings.Join(remoteLines, "\n")+"\n")
	runGit(t, other, "add", "shared.txt")
	runGit(t, other, "commit", "-qm", "remote edit line 7")
	runGit(t, other, "push", "-q", "origin", TrunkRef)

	err := pushTrunk(ctx, f.root, settings.Push{})
	if _, ok := err.(*refusedPushError); !ok {
		t.Fatalf("expected refusedPushError, got %v", err)
	}
	n, herr := l.healRefusedPush(ctx, settings.Push{}, err)
	if herr != nil || n != 1 {
		t.Fatalf("heal = %d, %v", n, herr)
	}
	c1Prime := runGit(t, f.root, "rev-parse", "HEAD")
	if gitPatchID(t, f.root, c1) == gitPatchID(t, f.root, c1Prime) {
		t.Fatalf("expected shifted context to change git patch-id --stable")
	}
	if h := rewriteHistory(t, f); h.Lost != "" {
		t.Fatalf("trunk-rewritten hold raised on shifted-context replay: %+v", h)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		resolved, err := heavy.RewriteResolve(r, c1)
		if err != nil || resolved != c1Prime {
			t.Fatalf("RewriteResolve(%s) = (%s, %v), want %s", c1, resolved, err, c1Prime)
		}
		return nil
	})
}

func TestTrunkRewritesResolveRefsStaleBaseAndLandingCalls(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	oldCommit := ownerCommit(t, f.root, "old.txt")
	oldParent := runGit(t, f.root, "rev-parse", oldCommit+"^")
	newCommit := ownerCommit(t, f.root, "new.txt")
	trunkTip := runGit(t, f.root, "rev-parse", "HEAD")
	startedPayload, err := json.Marshal(pendingWork{
		Decision: Decision{ItemID: "item-a"},
		BaseSHA:  oldParent,
	})
	if err != nil {
		t.Fatal(err)
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha, new_sha, recorded_at, evidence) VALUES(?, ?, '2026-11-20T00:00:00Z', 'test')`, oldCommit, newCommit); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings(landing_id, event_seq, item_id, run_id, verdict_id, result, reason, base_sha, head_sha, trunk_before, trunk_after, gated_tree, landed_tree, conflict_paths, gate_skipped_reason)
			VALUES('l-old', 1, 'item-old', 'run-old', 'v-old', 'landed', '', ?, ?, ?, ?, 'gt', 'lt', '[]', '')`,
			oldCommit, oldCommit, oldCommit, oldCommit); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO items(item_id, title, state, allowed_paths, updated_at) VALUES('item-old', 'old', 'done', '["old.txt"]', '2026-11-20T00:00:00Z') ON CONFLICT(item_id) DO UPDATE SET state='done', allowed_paths='["old.txt"]'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(1, 'item.update', 'item', 'item-a', '{"item_id":"item-a","title":"a","state":"in-queue","blocked_by":["item-old"]}')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(2, ?, 'run', 'run-stale', ?)`, dispatchStartedKind, string(startedPayload)); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(3, 'call.offered', 'call', 'call-1', '{"run_id":"run-old","item_id":"item-old","lane_id":"lane-1"}')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(4, 'call.landed', 'call', 'call-1', ?)`,
			`{"commit":"`+oldCommit+`"}`); err != nil {
			return err
		}
		return nil
	})

	// 1. planRefSweep (refs.go): marker pointing at newCommit matches landing at oldCommit via trunk_rewrites.
	fixtureRead(t, f, func(r *store.Reader) error {
		planned, err := planRefSweep(r, []RefDisposition{{
			Ref: land.LandedRefPrefix + "run-old",
			SHA: newCommit,
		}}, map[string][]string{}, map[string]bool{newCommit: true}, f.root)
		if err != nil {
			t.Fatal(err)
		}
		if len(planned) != 1 || strings.Contains(planned[0].Reason, "marker differs") {
			t.Fatalf("planRefSweep = %+v", planned)
		}

		// 2. runBaseLacks (stale_base.go): resolves blocker's trunk_after through trunk_rewrites.
		sb, err := runBaseLacks(ctx, r, f.root, "run-stale", trunkTip)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sb.Lacks, "item-old") {
			t.Fatalf("runBaseLacks = %+v, want Lacks containing item-old", sb)
		}
		return nil
	})

	// 3. reconcileLandingCalls (landing_call_lifecycle.go): resolves c.landed and c.recorded without faulting.
	if err := l.reconcileLandingCalls(ctx); err != nil {
		t.Fatalf("reconcileLandingCalls: %v", err)
	}
}
