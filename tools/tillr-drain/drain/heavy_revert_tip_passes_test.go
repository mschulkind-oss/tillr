package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// landRemoval commits the removal of a file as a recorded repair landing on trunk.
func (h *revertHarness) landRemoval(item, name string) string {
	h.t.Helper()
	if err := os.Remove(filepath.Join(h.root, name)); err != nil {
		h.t.Fatal(err)
	}
	heavyGit(h.t, h.root, "add", "-A")
	heavyGit(h.t, h.root, "commit", "-qm", "fix: "+item)
	sha := heavyGit(h.t, h.root, "rev-parse", "HEAD")
	h.seq++
	if err := writeFixture(h.path, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,rank,updated_at) VALUES(?,?,?,?,0)`, item, item, StateLanded, item); err != nil {
			return err
		}
		return recordFixtureLanding(tx, item, "run-"+item, sha, h.seq)
	}); err != nil {
		h.t.Fatal(err)
	}
	return sha
}

func (h *revertHarness) itemState(itemID string) string {
	h.t.Helper()
	rows := h.query(`SELECT state FROM items WHERE item_id=?`, itemID)
	if len(rows) != 1 {
		h.t.Fatalf("item %s rows = %v, want 1", itemID, rows)
	}
	return rows[0]
}

// When a repair lands on trunk while an automatic revert's gate is running and makes the
// failing check pass on the new tip, the revert's gate is cancelled, the revert does not
// land, the reverted item stays landed, no fixer is filed, and a drain.revert event
// records that the tip already passes.
func TestHeavyRevertDroppedAndGateCancelledWhenRepairLandsDuringGate(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-broke", "a", "broken\n")
	var repair string
	gateCancelled := false
	h.steps.onGateCtx = func(ctx context.Context, _ Candidate) (GateOutcome, error) {
		repair = h.landRemoval("item-repair", "a")
		select {
		case <-ctx.Done():
			gateCancelled = true
			return GateOutcome{}, ctx.Err()
		case <-time.After(3 * time.Second):
			return GateOutcome{Outcome: GatePassed}, nil
		}
	}
	h.proof()
	if repair == "" {
		t.Fatal("repair landing never ran during the revert gate")
	}
	if !gateCancelled {
		t.Fatal("revert gate was not cancelled when repair landed and made the failing check pass on the new tip")
	}
	if trunk := heavyGit(t, h.root, "rev-parse", TrunkRef); trunk != repair {
		t.Fatalf("trunk = %s, want repair tip %s (revert must not land)", shortSHA(trunk), shortSHA(repair))
	}
	if got := h.itemState("item-broke"); got != StateLanded {
		t.Fatalf("item-broke state = %q, want %q", got, StateLanded)
	}
	if len(*h.steps.returned) != 0 {
		t.Fatalf("reverted item was returned despite dropped revert: %v", *h.steps.returned)
	}
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "dropped" || reverts[0].Landing != culprit || reverts[0].Tip != culprit ||
		!strings.Contains(reverts[0].Reason, shortSHA(repair)) || !strings.Contains(reverts[0].Reason, "unit") || !strings.Contains(reverts[0].Reason, "already passes") {
		t.Fatalf("reverts = %+v, want one dropped event naming tip %s and unit", reverts, shortSHA(repair))
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("dropped revert filed fixers %v, want none", f)
	}
	if green := heavyGit(t, h.root, "rev-parse", GreenRef); green != repair {
		t.Fatalf("green pointer = %s, want %s", shortSHA(green), shortSHA(repair))
	}
}

// Even if a repair lands right as the revert's gate finishes (before the watcher ticks),
// the pre-landing check sees the moved tip already passes and drops the revert.
func TestHeavyRevertDroppedBeforeLandWhenRepairLandsAtGateEnd(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-broke", "a", "broken\n")
	var repair string
	h.steps.onGate = func(Candidate) {
		if repair == "" {
			repair = h.landRemoval("item-repair", "a")
		}
	}
	h.proof()
	if repair == "" {
		t.Fatal("repair landing never ran during the revert gate")
	}
	if trunk := heavyGit(t, h.root, "rev-parse", TrunkRef); trunk != repair {
		t.Fatalf("trunk = %s, want repair tip %s (revert must not land)", shortSHA(trunk), shortSHA(repair))
	}
	if got := h.itemState("item-broke"); got != StateLanded {
		t.Fatalf("item-broke state = %q, want %q", got, StateLanded)
	}
	if len(*h.steps.returned) != 0 {
		t.Fatalf("reverted item was returned despite dropped revert: %v", *h.steps.returned)
	}
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "dropped" || reverts[0].Landing != culprit ||
		!strings.Contains(reverts[0].Reason, shortSHA(repair)) || !strings.Contains(reverts[0].Reason, "already passes") {
		t.Fatalf("reverts = %+v, want one dropped event naming tip %s", reverts, shortSHA(repair))
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("dropped revert filed fixers %v, want none", f)
	}
}

// When the trunk tip has already moved and passes the failing check before revertLanding
// even builds the revert commit, the revert is dropped without starting its gate.
func TestHeavyRevertDroppedBeforeGateWhenTipAlreadyPasses(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-broke", "a", "broken\n")
	repair := h.landRemoval("item-repair", "a")
	replay := func(ctx context.Context, sha, _ string) (bool, error) {
		_, err := heavyGitOutput(ctx, h.root, "cat-file", "-e", sha+":a")
		return err == nil, nil
	}
	out, err := h.loop.revertLanding(context.Background(), culprit, culprit, []HeavyCheck{{CheckID: "unit"}}, replay)
	if err != nil {
		t.Fatalf("revertLanding: %v", err)
	}
	if out.Reverted != "" || out.Refused != "" || !strings.Contains(out.Dropped, shortSHA(repair)) || !strings.Contains(out.Dropped, "already passes") {
		t.Fatalf("revertLanding = %+v, want Dropped naming %s", out, shortSHA(repair))
	}
	if len(*h.steps.gated) != 0 {
		t.Fatalf("revert gate ran despite tip already passing: %+v", *h.steps.gated)
	}
	if trunk := heavyGit(t, h.root, "rev-parse", TrunkRef); trunk != repair {
		t.Fatalf("trunk = %s, want repair tip %s", shortSHA(trunk), shortSHA(repair))
	}
	if got := h.itemState("item-broke"); got != StateLanded {
		t.Fatalf("item-broke state = %q, want %q", got, StateLanded)
	}
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "dropped" || reverts[0].Landing != culprit {
		t.Fatalf("reverts = %+v, want one dropped event", reverts)
	}
}

// When a partial repair fixes only one of several failing checks on the new tip, the
// revert's gate is not cancelled and the revert still lands.
func TestHeavyRevertContinuesWhenPartialRepairLeavesOneCheckRed(t *testing.T) {
	judge := func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		check := func(id, file string) HeavyCheck {
			res := "pass"
			if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
				res = "fail"
			}
			return HeavyCheck{CheckID: id, Result: res, Reads: []string{file}}
		}
		switch only {
		case "unit-a":
			return []HeavyCheck{check("unit-a", "a")}, nil
		case "unit-b":
			return []HeavyCheck{check("unit-b", "b")}, nil
		default:
			return []HeavyCheck{check("unit-a", "a"), check("unit-b", "b")}, nil
		}
	}
	h := newRevertHarness(t, judge)
	h.loop.revertTipPoll = 10 * time.Millisecond
	if err := os.WriteFile(filepath.Join(h.root, "b"), []byte("broken-b\n"), 0644); err != nil {
		t.Fatal(err)
	}
	culprit := h.land("item-broke", "a", "broken-a\n")
	var partial string
	gateCancelled := false
	h.steps.onGateCtx = func(ctx context.Context, _ Candidate) (GateOutcome, error) {
		partial = h.landRemoval("item-partial", "a")
		select {
		case <-ctx.Done():
			gateCancelled = true
			return GateOutcome{}, ctx.Err()
		case <-time.After(80 * time.Millisecond):
			return GateOutcome{Outcome: GatePassed}, nil
		}
	}
	h.proof()
	if partial == "" {
		t.Fatal("partial repair never ran during the revert gate")
	}
	if gateCancelled {
		t.Fatal("revert gate was cancelled even though unit-b still fails on the new tip")
	}
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "reverted" || reverts[0].Landing != culprit {
		t.Fatalf("reverts = %+v, want one reverted event", reverts)
	}
}

// Mirrors an early revert started while a slower proof check is still running: when a
// repair lands during the revert's gate, the gate is cancelled and the revert is dropped
// before the slow check ends, leaving the item landed and filing no fixer.
func TestHeavyEarlyRevertDroppedWhileProofContinues(t *testing.T) {
	var h *revertHarness
	first, seenDropped := true, false
	h = newRevertHarness(t, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		quick := HeavyCheck{CheckID: "quick", Result: "pass", Reads: []string{"a"}}
		if _, err := os.Stat(filepath.Join(dir, "a")); err == nil {
			quick.Result = "fail"
		}
		slow := HeavyCheck{CheckID: "slow", Result: "pass"}
		switch only {
		case "quick":
			return []HeavyCheck{quick}, nil
		case "slow":
			return []HeavyCheck{slow}, nil
		}
		heavyCheckEnded(ctx, quick)
		if first {
			first = false
			for until := time.Now().Add(hangGuard); time.Now().Before(until); time.Sleep(10 * time.Millisecond) {
				if rows := h.reverts(); len(rows) == 1 && rows[0].Result == "dropped" {
					seenDropped = true
					break
				}
			}
		}
		heavyCheckEnded(ctx, slow)
		return []HeavyCheck{quick, slow}, nil
	})
	h.loop.revertTipPoll = 10 * time.Millisecond
	culprit := h.land("item-broke", "a", "broken\n")
	var repair string
	gateCancelled := false
	h.steps.onGateCtx = func(ctx context.Context, _ Candidate) (GateOutcome, error) {
		repair = h.landRemoval("item-repair", "a")
		select {
		case <-ctx.Done():
			gateCancelled = true
			return GateOutcome{}, ctx.Err()
		case <-time.After(3 * time.Second):
			return GateOutcome{Outcome: GatePassed}, nil
		}
	}
	h.proof()
	if !seenDropped {
		t.Fatal("early revert was not dropped before the slow check finished")
	}
	if !gateCancelled {
		t.Fatal("early revert's gate was not cancelled when repair landed")
	}
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "dropped" || reverts[0].Landing != culprit ||
		!strings.Contains(reverts[0].Reason, shortSHA(repair)) || !strings.Contains(reverts[0].Reason, "quick") {
		t.Fatalf("reverts = %+v, want one dropped event for %s naming %s and quick", reverts, shortSHA(culprit), shortSHA(repair))
	}
	if trunk := heavyGit(t, h.root, "rev-parse", TrunkRef); trunk != repair {
		t.Fatalf("trunk = %s, want repair tip %s", shortSHA(trunk), shortSHA(repair))
	}
	if got := h.itemState("item-broke"); got != StateLanded {
		t.Fatalf("item-broke state = %q, want %q", got, StateLanded)
	}
	if fixes := h.fixers(); len(fixes) != 0 {
		t.Fatalf("early dropped revert filed fixers %v, want none", fixes)
	}
	if green := heavyGit(t, h.root, "rev-parse", GreenRef); green != repair {
		t.Fatalf("green = %s, want repair tip %s", shortSHA(green), shortSHA(repair))
	}
}
