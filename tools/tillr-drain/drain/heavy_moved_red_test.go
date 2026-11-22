package drain

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestHeavyDoesNotLandARevertCheckedBeforeTrunkAdvanced(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	red := h.land("item-broke", "a", "broken")
	var newest string
	out, err := h.loop.revertLanding(context.Background(), red, red, []HeavyCheck{{CheckID: "unit"}}, func(context.Context, string, string) (bool, error) {
		newest = h.land("item-next", "a", "fixed while checking the revert")
		return false, nil
	})
	if err != nil || out.Reverted != "" || !strings.Contains(out.Refused, "trunk advanced") {
		t.Fatalf("unexpected revert outcome: %+v, %v", out, err)
	}
	if newest == "" || heavyGit(t, h.root, "rev-parse", TrunkRef) != newest {
		t.Fatal("the checked revert displaced the newer landing")
	}
}

func TestHeavyFilesNamedTestRepairsWhileTrunkKeepsMoving(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seq := 0
	commit := func(file, value string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, file), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		heavyGit(t, root, "add", file)
		heavyGit(t, root, "commit", "-qm", file+": "+value)
		sha := heavyGit(t, root, "rev-parse", "HEAD")
		seq++
		id := fmt.Sprintf("landing-%d", seq)
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			return recordFixtureLanding(tx, id, "run-"+id, sha, seq)
		}); err != nil {
			t.Fatal(err)
		}
		return sha
	}
	first := commit("input-a", "red")
	const suiteID = "go-tool/tools-example-s-whole-suite-passes"
	suite := HeavyCheck{CheckID: suiteID, Result: "fail"}
	failures := goSuiteFailures("tools/example's whole suite passes", legacyFailure{Cases: []legacyGoCase{
		{Package: "example/pkg", Name: "TestA", Log: "test-a.log"},
		{Package: "example/pkg", Name: "TestB", Log: "test-b.log"},
	}}, suite)
	if len(failures) != 2 {
		t.Fatalf("expected two package-qualified failures, got %+v", failures)
	}
	fixerNames := func(sha, check string) error {
		return s.ReadTx(ctx, func(r *store.Reader) error {
			var text string
			if err := r.QueryRow(`SELECT text FROM item_criteria WHERE item_id=? AND criterion_id='trunk-green'`, TrunkFixerID(sha)).Scan(&text); err != nil {
				return fmt.Errorf("no repair for %s before the next proof: %w", check, err)
			}
			if !strings.Contains(text, check) {
				return fmt.Errorf("repair for %s omits %s: %s", sha, check, text)
			}
			return nil
		})
	}
	inputVerdict := func(dir, input string) string {
		value, err := os.ReadFile(filepath.Join(dir, input))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if string(value) == "red" {
			return "fail"
		}
		return "pass"
	}
	var secondRed, secondTip, thirdTip, lastTip string
	var judgeErr error
	steps := heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		if only != "" {
			for i, check := range failures {
				if check.CheckID == only {
					check.Result = inputVerdict(dir, []string{"input-a", "input-b"}[i])
					return []HeavyCheck{check}, nil
				}
			}
			return nil, fmt.Errorf("unexpected focused check %s", only)
		}
		switch sha {
		case first:
			secondRed = commit("input-b", "red")
			secondTip = commit("notes", "one")
			return failures[:1], nil
		case secondTip:
			if judgeErr = fixerNames(first, failures[0].CheckID); judgeErr != nil {
				return nil, judgeErr
			}
			commit("notes", "two")
			thirdTip = commit("notes", "three")
			return failures, nil
		case thirdTip:
			if judgeErr = fixerNames(secondRed, failures[1].CheckID); judgeErr != nil {
				return nil, judgeErr
			}
			commit("input-a", "fixed")
			lastTip = commit("input-b", "fixed")
			return failures, nil
		case lastTip:
			return []HeavyCheck{{CheckID: suiteID, Result: "pass"}}, nil
		default:
			judgeErr = fmt.Errorf("unexpected whole proof on %s", sha)
			return nil, judgeErr
		}
	}}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard}}
	if err := l.startHeavy(ctx, first); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if judgeErr != nil {
		t.Fatal(judgeErr)
	}
	if lastTip == "" || heavyGit(t, root, "rev-parse", GreenRef) != lastTip {
		t.Fatal("newest tip was not proved green")
	}
	if err := l.resolveProvedFixers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE state=?`, StateResolvedElsewhere).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			return fmt.Errorf("newest proof should withdraw both named-test repairs, withdrew %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
