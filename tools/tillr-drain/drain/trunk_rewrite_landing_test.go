package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func commitWithIdentity(t *testing.T, root, file, content, msg string, env []string) string {
	t.Helper()
	writeForkFile(t, filepath.Join(root, file), content)
	runGit(t, root, "add", file)
	cmd := exec.Command("git", "-C", root, "commit", "-qm", msg)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit %s: %v: %s", msg, err, out)
	}
	return runGit(t, root, "rev-parse", "HEAD")
}

// AcceptRewrite maps an unmapped interior trunk landing commit (not the trunk tip) when
// no trunk-tip rewrite is active, refuses unless both commits carry the same non-empty
// stable patch id, and releases the dependent item from blocker-landing-not-on-trunk.
func TestAcceptRewriteMapsUnmappedInteriorLandingByStablePatchID(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	origIdent := []string{
		"GIT_AUTHOR_NAME=old", "GIT_AUTHOR_EMAIL=old@example.invalid",
		"GIT_COMMITTER_NAME=old", "GIT_COMMITTER_EMAIL=old@example.invalid",
	}
	newIdent := []string{
		"GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.invalid",
		"GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.invalid",
	}

	lost := commitWithIdentity(t, f.root, "blocker.txt", "blocker content\n", "blocker landing", origIdent)
	runGit(t, f.root, "reset", "--hard", base)
	replacement := commitWithIdentity(t, f.root, "blocker.txt", "blocker content\n", "blocker landing", newIdent)
	emptyOnTrunk := func() string {
		runGit(t, f.root, "commit", "--allow-empty", "-qm", "empty commit")
		return runGit(t, f.root, "rev-parse", TrunkRef)
	}()
	tip := commitWithIdentity(t, f.root, "later.txt", "later content\n", "later trunk commit", newIdent)

	runGit(t, f.root, "update-ref", GreenRef, tip)
	runGit(t, f.root, "update-ref", "refs/tillr/landed/blocker-run", lost)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []string{
			`UPDATE items SET state='landed' WHERE item_id='blocker'`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('dependent','blocker')`,
			`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,'` + tip + `','')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return recordFixtureLanding(tx, "blocker", "blocker-run", lost, 1)
	})

	dependent := func() Decision {
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == "dependent" {
				return p.decision
			}
		}
		t.Fatal("dependent missing")
		return Decision{}
	}

	d := dependent()
	wantHold := ReasonBlockerLandingNotOnTrunk + ": blocker at " + lost
	if d.Action != ActionHold || d.Reason != wantHold {
		t.Fatalf("before accept: want hold %q, got %s %q", wantHold, d.Action, d.Reason)
	}
	class, where, mover := whyPresentation(d)
	wantMover := "the owner; restore the named landing to main, or map its replacement with tillr drain accept-rewrite"
	if class != "needs you" || where != "trunk" || mover != wantMover {
		t.Fatalf("whyPresentation = (%q, %q, %q), want (needs you, trunk, %q)", class, where, mover, wantMover)
	}

	// Refusal 1: different stable patch id.
	if _, err := acceptRewrite(f, lost, tip); err == nil || !strings.Contains(err.Error(), "the stable patch ids differ") {
		t.Fatalf("mismatched patch id: want refusal naming stable patch ids, got %v", err)
	}
	// Refusal 2: empty commit has no stable patch id.
	if _, err := acceptRewrite(f, lost, emptyOnTrunk); err == nil || !strings.Contains(err.Error(), "no stable patch id") {
		t.Fatalf("empty commit: want refusal naming no stable patch id, got %v", err)
	}
	// Refusal 3: --current not on trunk.
	if _, err := acceptRewrite(f, lost, lost); err == nil || !strings.Contains(err.Error(), "is not on the trunk") {
		t.Fatalf("current off trunk: want refusal, got %v", err)
	}
	// Dependent is still held after all refusals.
	if d := dependent(); d.Action != ActionHold || d.Reason != wantHold {
		t.Fatalf("after refusals: want hold %q, got %s %q", wantHold, d.Action, d.Reason)
	}

	// Success: interior landing mapped to its patch-identical replacement on main.
	got, err := acceptRewrite(f, lost, replacement)
	if err != nil {
		t.Fatalf("accept interior landing: %v", err)
	}
	if !got.Landing || got.Lost != lost || got.Current != replacement || got.Commits != 1 || got.Pin != acceptedTrunkPrefix+lost {
		t.Fatalf("unexpected acceptance: %+v", got)
	}
	// trunk_history and refs/tillr/green are unchanged.
	if h := rewriteHistory(t, f); h.Observed != tip || h.Lost != "" {
		t.Fatalf("trunk_history modified: %+v", h)
	}
	if green := runGit(t, f.root, "rev-parse", GreenRef); green != tip {
		t.Fatalf("green moved: %s, want %s", green, tip)
	}
	if pin := runGit(t, f.root, "rev-parse", acceptedTrunkPrefix+lost); pin != lost {
		t.Fatalf("lost landing not pinned: %s", pin)
	}
	if marker := runGit(t, f.root, "rev-parse", "refs/tillr/landed/blocker-run"); marker != replacement {
		t.Fatalf("landed marker = %s, want %s", marker, replacement)
	}

	// Dependent now leaves blocker-landing-not-on-trunk and dispatches.
	if d := dependent(); d.Action != ActionDispatch {
		t.Fatalf("after accept: want dispatch, got %+v", d)
	}

	// Refusal 4: second acceptance once mapped refuses with "no rewrite is recorded".
	if _, err := acceptRewrite(f, lost, replacement); err == nil || !strings.Contains(err.Error(), "no rewrite is recorded") {
		t.Fatalf("second acceptance: want no rewrite is recorded, got %v", err)
	}
}

// At pass start, reconcileRewrittenLandings maps unmapped trunk landings with a unique
// git patch-id --stable match in merge-base(old, main)..main into trunk_rewrites, skips
// fork landings, unrelated histories, ambiguous duplicates, and altered diffs, updates
// refs/tillr/landed markers, and preserves refs/tillr/green proof admission.
func TestReconcileRewrittenLandingsMapsUniquePatchMatchAtPassStart(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent", "fork-item", "dup-item", "altered-item")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	origIdent := []string{
		"GIT_AUTHOR_NAME=old", "GIT_AUTHOR_EMAIL=old@example.invalid",
		"GIT_COMMITTER_NAME=old", "GIT_COMMITTER_EMAIL=old@example.invalid",
	}
	newIdent := []string{
		"GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.invalid",
		"GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.invalid",
	}

	// Create old commits off base: one unique match, one ambiguous duplicate, one altered.
	lostUnique := commitWithIdentity(t, f.root, "unique.txt", "unique\n", "unique", origIdent)
	runGit(t, f.root, "reset", "--hard", base)
	lostDup := commitWithIdentity(t, f.root, "dup.txt", "dup\n", "dup", origIdent)
	runGit(t, f.root, "reset", "--hard", base)
	lostAltered := commitWithIdentity(t, f.root, "altered.txt", "before\n", "altered", origIdent)

	// Create an unrelated orphan commit (no merge-base with main).
	runGit(t, f.root, "checkout", "--orphan", "orphan-fork")
	runGit(t, f.root, "rm", "-rfq", ".")
	orphanSHA := commitWithIdentity(t, f.root, "orphan.txt", "orphan\n", "orphan", origIdent)

	// Rebuild main from base with rewritten commits.
	runGit(t, f.root, "checkout", "-q", TrunkRef)
	runGit(t, f.root, "reset", "--hard", base)
	replacementUnique := commitWithIdentity(t, f.root, "unique.txt", "unique\n", "unique", newIdent)
	// Two commits on main with the same patch as lostDup (add dup.txt, remove, add again).
	commitWithIdentity(t, f.root, "dup.txt", "dup\n", "dup one", newIdent)
	runGit(t, f.root, "rm", "-q", "dup.txt")
	runGit(t, f.root, "commit", "-qm", "rm dup")
	commitWithIdentity(t, f.root, "dup.txt", "dup\n", "dup two", newIdent)
	// Altered diff for lostAltered.
	commitWithIdentity(t, f.root, "altered.txt", "after\n", "altered", newIdent)
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	// Green is still at base (behind replacementUnique) to verify proof admission stays intact.
	runGit(t, f.root, "update-ref", GreenRef, base)
	runGit(t, f.root, "update-ref", "refs/tillr/landed/run-unique", lostUnique)
	runGit(t, f.root, "update-ref", "refs/heads/patch/pi", orphanSHA)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []string{
			`UPDATE items SET state='landed' WHERE item_id IN ('blocker','fork-item','dup-item','altered-item')`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('dependent','blocker')`,
			`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,'` + tip + `','')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		if err := recordFixtureLanding(tx, "blocker", "run-unique", lostUnique, 1); err != nil {
			return err
		}
		if err := recordFixtureLanding(tx, "dup-item", "run-dup", lostDup, 2); err != nil {
			return err
		}
		if err := recordFixtureLanding(tx, "altered-item", "run-altered", lostAltered, 3); err != nil {
			return err
		}
		// Fork landing with refs/heads/patch/pi base_ref (skipped).
		forkPayload, err := json.Marshal(map[string]string{"base_ref": "refs/heads/patch/pi"})
		if err != nil {
			return err
		}
		itemID := "fork-item"
		seq, err := tx.Append(event.Event{Kind: "land.landing", ObjectType: "item", ObjectID: &itemID, Payload: forkPayload, At: time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)})
		if err != nil {
			return err
		}
		if err := recordFixtureLanding(tx, "fork-item", "run-fork", orphanSHA, int(seq)); err != nil {
			return err
		}
		return nil
	})

	if err := l.reconcileRewrittenLandings(context.Background()); err != nil {
		t.Fatalf("reconcileRewrittenLandings: %v", err)
	}

	// Verify trunk_rewrites contains only lostUnique -> replacementUnique.
	r, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.ReadTx(context.Background(), func(tx *store.Reader) error {
		got, err := heavy.RewriteResolve(tx, lostUnique)
		if err != nil {
			return err
		}
		if got != replacementUnique {
			t.Fatalf("lostUnique resolved to %s, want %s", got, replacementUnique)
		}
		for _, unmapped := range []string{lostDup, lostAltered, orphanSHA} {
			got, err := heavy.RewriteResolve(tx, unmapped)
			if err != nil {
				return err
			}
			if got != unmapped {
				t.Fatalf("unmapped %s unexpectedly resolved to %s", unmapped, got)
			}
		}
		var absorbed int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, rewriteAbsorbedKind).Scan(&absorbed); err != nil {
			return err
		}
		if absorbed != 1 {
			t.Fatalf("absorbed events = %d, want 1", absorbed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Landed marker moved to replacementUnique.
	if marker := runGit(t, f.root, "rev-parse", "refs/tillr/landed/run-unique"); marker != replacementUnique {
		t.Fatalf("landed marker = %s, want %s", marker, replacementUnique)
	}

	// Dependent leaves blocker-landing-not-on-trunk and waits on trunk-unproved while green is at base.
	dependent := func() Decision {
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == "dependent" {
				return p.decision
			}
		}
		t.Fatal("dependent missing")
		return Decision{}
	}
	if d := dependent(); d.Action != ActionHold || d.Reason != ReasonTrunkUnproved {
		t.Fatalf("with green behind replacement: want %s, got %s %q", ReasonTrunkUnproved, d.Action, d.Reason)
	}
	// Once green advances to replacementUnique, dependent dispatches.
	runGit(t, f.root, "update-ref", GreenRef, replacementUnique)
	if d := dependent(); d.Action != ActionDispatch {
		t.Fatalf("with green at replacement: want dispatch, got %+v", d)
	}

	// Second call on unchanged tip is a no-op (cached) and does not append another event.
	if err := l.reconcileRewrittenLandings(context.Background()); err != nil {
		t.Fatalf("second reconcileRewrittenLandings: %v", err)
	}
	if err := r.ReadTx(context.Background(), func(tx *store.Reader) error {
		var absorbed int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, rewriteAbsorbedKind).Scan(&absorbed); err != nil {
			return err
		}
		if absorbed != 1 {
			t.Fatalf("second pass absorbed events = %d, want 1", absorbed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
