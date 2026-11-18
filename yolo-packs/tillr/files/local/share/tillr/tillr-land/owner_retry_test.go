package main_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
)

func TestOwnerSavesDoNotConsumeAnArbitraryLandingRetryBudget(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	path := filepath.Join(f.f.dir, "a.txt")
	const saves = 25
	predictions := 0
	land.OwnerHooks.AfterPredict = func() {
		predictions++
		if predictions <= saves {
			write(t, path, strings.TrimSuffix(lines, "l9\n")+fmt.Sprintf("save-%d\n", predictions))
		}
	}
	l, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err != nil || !l.Landed() {
		t.Fatalf("landing stopped after %d predictions: %+v, %v", predictions, l, err)
	}
	if predictions != saves+1 {
		t.Fatalf("predictions = %d, want %d", predictions, saves+1)
	}
	want := "L1\n" + strings.TrimSuffix(strings.TrimPrefix(lines, "l1\n"), "l9\n") + "save-25\n"
	if got := statOf(t, path).body; got != want {
		t.Fatalf("owner's last save was not carried: got %q, want %q", got, want)
	}
	if refs := recoveryRefs(t, f); refs != "" {
		t.Fatalf("clean landing left recovery refs: %s", refs)
	}
}

// TestAChangeReportedWithNothingSavedEndsTheLandingAfterOneFurtherPrediction: the comparison
// answers that the owner changed a path while the checkout holds what the prediction read. The
// next prediction reads the same, so the lander is at fault, not the owner: the landing returns
// with a recorded fault and the owner's file untouched, instead of predicting without end.
func TestAChangeReportedWithNothingSavedEndsTheLandingAfterOneFurtherPrediction(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	path := filepath.Join(f.f.dir, "a.txt")
	write(t, path, strings.TrimSuffix(lines, "l9\n")+"mine\n")
	before := statOf(t, path)
	predictions := 0
	land.OwnerHooks.AfterPredict = func() { predictions++ }
	land.OwnerHooks.FailComparison = func() bool { return true }
	l, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || l.Landed() || !strings.Contains(err.Error(), "fault recorded") {
		t.Fatalf("landing = %+v, %v; want a recorded fault", l, err)
	}
	if predictions != 2 {
		t.Fatalf("predictions = %d, want 2", predictions)
	}
	if got := statOf(t, path); got != before {
		t.Fatalf("the owner's file changed: %+v, was %+v", got, before)
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Fatalf("fault events = %d, want 1", n)
	}
	if tip := git(t, f.f.dir, "rev-parse", "HEAD"); tip != f.f.baseSHA {
		t.Fatalf("the trunk moved to %s", tip)
	}
}
