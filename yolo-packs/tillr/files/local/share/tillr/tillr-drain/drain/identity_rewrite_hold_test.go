package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestIdentityOnlyRewriteCarriesGreenWithoutHolding(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", "HEAD")
	old := foreignCommit(t, f.root, base)
	if _, err := l.observeTrunk(ctx, ""); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "update-ref", GreenRef, old)
	replacement := runGit(t, f.root, "commit-tree", old+"^{tree}", "-p", base, "-m", "foreign")
	runGit(t, f.root, "reset", "--hard", replacement)
	h, err := l.observeTrunk(ctx, "")
	if err != nil || h.Lost != "" {
		t.Fatalf("identity-only rewrite held: %+v, %v", h, err)
	}
	if green := runGit(t, f.root, "rev-parse", GreenRef); green != replacement {
		t.Fatalf("green = %s, want %s", green, replacement)
	}
}

func TestMessageRewriteKeepsLostHistoryHold(t *testing.T) {
	f, l := passFixture(t, "item-a")
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state='blocked'`); return err })
	base := runGit(t, f.root, "rev-parse", "HEAD")
	bad := foreignCommit(t, f.root, base)
	if _, err := l.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	edited := runGit(t, f.root, "commit-tree", runGit(t, f.root, "rev-parse", bad+"^{tree}"), "-p", base, "-m", "edited message")
	runGit(t, f.root, "reset", "--hard", edited)
	var result LoopResult
	historyPass(t, l, &result)
	h := rewriteHistory(t, f)
	if h.Lost != bad || h.Observed != edited || steps.lands != 0 {
		t.Fatalf("message edit waived history: %+v", h)
	}
}
