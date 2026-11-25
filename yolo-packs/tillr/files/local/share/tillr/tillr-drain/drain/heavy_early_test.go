package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHeavyRedCheckIsRevertedWhileTheProofContinues(t *testing.T) {
	var h *revertHarness
	first, seen := true, false
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
				if rows := h.reverts(); len(rows) == 1 && rows[0].Result == "reverted" {
					seen = true
					break
				}
			}
			if green := heavyGit(t, h.root, "rev-parse", GreenRef); green != h.base {
				t.Errorf("green advanced during red proof: %s", green)
			}
		}
		heavyCheckEnded(ctx, slow)
		return []HeavyCheck{quick, slow}, nil
	})
	culprit := h.land("item-broke", "a", "broken")
	h.land("item-later", "c", "unrelated")
	h.proof()
	rows := h.reverts()
	if !seen || len(rows) != 1 || rows[0].Landing != culprit {
		t.Fatalf("seen before slow ended=%v, reverts=%+v", seen, rows)
	}
	if green := heavyGit(t, h.root, "rev-parse", GreenRef); green != rows[0].Revert || green != heavyGit(t, h.root, "rev-parse", TrunkRef) {
		t.Fatalf("green=%s, revert=%+v", green, rows[0])
	}
	if fixes := h.fixers(); len(fixes) != 0 {
		t.Fatalf("fixers=%v", fixes)
	}
}
