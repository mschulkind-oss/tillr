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

func TestHeavyRevertsOntoAdvancedTrunkWhenClean(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	culprit := h.land("item-broke", "a", "broken\n")
	var other string
	h.steps.onGate = func(Candidate) {
		if other == "" {
			other = h.land("item-next", "c", "unrelated landing while checking the revert\n")
		}
	}
	h.proof()
	if other == "" {
		t.Fatal("the concurrent landing never ran during the revert gate")
	}
	reverts := h.reverts()
	if len(reverts) != 1 || reverts[0].Result != "reverted" || reverts[0].Landing != culprit || reverts[0].Tip != culprit {
		t.Fatalf("reverts=%+v", reverts)
	}
	trunk := heavyGit(t, h.root, "rev-parse", TrunkRef)
	if reverts[0].Revert != trunk {
		t.Fatalf("revert=%s, trunk=%s", reverts[0].Revert, trunk)
	}
	if parent := heavyGit(t, h.root, "rev-parse", trunk+"^"); parent != other {
		t.Fatalf("revert parent=%s, want new trunk tip %s", parent, other)
	}
	if files := heavyGit(t, h.root, "ls-tree", "--name-only", trunk); strings.Contains(files, "a\n") || !strings.Contains(files, "c") {
		t.Fatalf("squashed revert tree did not preserve concurrent landing and remove culprit: %q", files)
	}
	rows := h.query(`SELECT base_sha || '|' || trunk_before || '|' || trunk_after || '|' || gated_tree || '|' || landed_tree FROM landings WHERE item_id=? AND result='landed'`, RevertItemID(culprit))
	if len(rows) != 1 {
		t.Fatalf("revert landing rows=%v", rows)
	}
	parts := strings.Split(rows[0], "|")
	if len(parts) != 5 || parts[0] != culprit || parts[1] != other || parts[2] != trunk || parts[3] == "" || parts[4] == "" || parts[3] == parts[4] {
		t.Fatalf("revert landing row did not record squash onto moved trunk: %q", rows[0])
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("a landed revert filed fixers %v", f)
	}
	if green := heavyGit(t, h.root, "rev-parse", GreenRef); green != trunk {
		t.Fatalf("green pointer=%s, want %s", green, trunk)
	}
}

func TestHeavyRevertConflictOnAdvancedTrunkFilesTheFixer(t *testing.T) {
	h := newRevertHarness(t, fileRed("a"))
	if err := os.WriteFile(filepath.Join(h.root, "shared"), []byte("from culprit\n"), 0644); err != nil {
		t.Fatal(err)
	}
	culprit := h.land("item-broke", "a", "broken\n")
	var other string
	h.steps.onGate = func(Candidate) {
		if other == "" {
			other = h.land("item-next", "shared", "modified while checking the revert\n")
		}
	}
	h.proof()
	if other == "" {
		t.Fatal("the concurrent landing never ran during the revert gate")
	}
	if trunk := heavyGit(t, h.root, "rev-parse", TrunkRef); trunk != other {
		t.Fatalf("a conflicting revert displaced the newer landing: trunk=%s, want %s", trunk, other)
	}
	reverts := h.reverts()
	if len(reverts) == 0 || reverts[0].Result != "not-possible" || reverts[0].Landing != culprit ||
		!strings.Contains(reverts[0].Reason, LandSquashConflict) || !strings.Contains(reverts[0].Reason, "shared") ||
		strings.Contains(reverts[0].Reason, "trunk advanced") {
		t.Fatalf("reverts=%+v, want not-possible from squash-conflict on shared", reverts)
	}
	fixers := h.fixers()
	if len(fixers) != 1 || fixers[0] != TrunkFixerID(culprit) {
		t.Fatalf("fixers=%v, want one against %s", fixers, shortSHA(culprit))
	}
	if text := h.fixerText(fixers[0]); !strings.Contains(text, "Automatic revert of "+shortSHA(culprit)+" for unit was not possible") || !strings.Contains(text, LandSquashConflict) {
		t.Fatalf("fixer brief lacks the squash-conflict reason: %s", text)
	}
	if len(*h.steps.returned) != 0 {
		t.Fatalf("a refused revert returned %v", *h.steps.returned)
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
