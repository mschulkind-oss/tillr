package drain

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// The 2026-11-18 fixer for the red documentation check named the check twice: by its kind
// (`tillr-docs`, the replay's selector) and by its labelled id. A proof records only the
// labelled id, so a fixer that waited for the bare kind too could never be resolved by a
// proof. A bare kind is answered by the labelled check of that kind the fixer also names.
func TestAFixerNamingAKindAndItsLabelledCheckIsResolvedByTheLabelledPass(t *testing.T) {
	ctx := context.Background()
	f, l, _, red := fixerRoutingFixture(t)
	labelled := "tillr-docs/every-face-the-tillr-documents-name-answers-help-and-no-document-carries-a-flag-table"
	fixer, err := l.fileTrunkFixer(ctx, red, []string{"tillr-docs", labelled}, GateOutcome{SuspectPaths: []string{"base.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "repair lands")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	later := l.cfg.Now().UTC().Add(time.Minute)
	writeTipProgress(t, l, tip, nil, map[string]time.Time{labelled: later, "go-tool/unit": later})
	if err := l.resolveProvedFixers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, fixer); got != StateResolvedElsewhere {
		t.Fatalf("the labelled check passed on the tip, but the fixer is %s", got)
	}
}

// A bare kind with no labelled sibling is still a check of its own: a pass of some other
// check does not answer it.
func TestABareKindIsNotAnsweredByAnotherKindsPass(t *testing.T) {
	if got := provableChecks([]string{"tillr-docs", "go-tool/unit"}); strings.Join(got, ",") != "tillr-docs,go-tool/unit" {
		t.Fatalf("provable = %v", got)
	}
	if got := provableChecks([]string{"tillr-docs", "tillr-docs/a", "tillr-docs/b"}); strings.Join(got, ",") != "tillr-docs/a,tillr-docs/b" {
		t.Fatalf("provable = %v", got)
	}
}

// A fixer closed by hand while its check passes on the trunk releases the items acquitted
// behind it, with no operator retry: on 2026-11-18 three items stayed `trunk-broken`
// naming a closed fixer, each needing `tillr drain retry`. A closed fixer whose check has
// not been proved releases nothing.
func TestAClosedFixerReleasesItsAcquittalsOnceItsCheckPassesOnTheTrunk(t *testing.T) {
	ctx := context.Background()
	f, l, fixer, _ := fixerRoutingFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := item.SetState(ctx, tx, "item-spare", StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: "item:" + fixer, Reason: RefusalTrunkAlreadyRed + ": go-tool/unit"}); err != nil {
			return err
		}
		_, err := item.Close(ctx, tx, fixer, "checked by hand", "operator", l.cfg.Now())
		return err
	})
	if err := l.resolveProvedFixers(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, "item-spare"); got != StateBlocked {
		t.Fatalf("a closed fixer with no proof released its acquittal: %s", got)
	}

	runGit(t, f.root, "commit", "--allow-empty", "-qm", "repair lands")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	writeTipProgress(t, l, tip, nil, map[string]time.Time{"go-tool/unit": l.cfg.Now().UTC().Add(time.Minute)})
	if err := l.resolveProvedFixers(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, fixer); got != "closed" {
		t.Fatalf("the proof rewrote a closed fixer's state: %s", got)
	}
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, "item-spare"); got != StateInQueue {
		t.Fatalf("acquittal behind the closed, proved fixer not released: %s", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var raw []byte
		if err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=?`, FixerProvedKind, fixer).Scan(&raw); err != nil {
			return err
		}
		var p fixerProof
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.Green != tip {
			t.Fatalf("proof event %+v, want %s", p, tip)
		}
		return nil
	})
}

// A trunk fixer that names a fence obstacle for paths outside the gate's own code gets
// its fence widened and goes back to its worker; it is not parked on the owner. This is
// the 2026-11-18 fixer for the red documentation check, which needed
// tests/tillr-roll.test.py and waited on the owner for it with every other item held.
// A path in the gate's own code is never granted: that one still reaches the owner.
func TestATrunkFixersFenceObstacleWidensItsFence(t *testing.T) {
	const fixer = "fixer-main-0123456789ab"
	t.Run("widened", func(t *testing.T) {
		f, l, work := obstacleFixture(t, &obstacleSteps{}, fixer)
		settleReport(t, f, l, work, "OBSTACLE: fence\nPATHS: tests/tillr-roll.test.py, .tillr.toml\nEVIDENCE: the failing test asserts the old key\nRESULT: blocked\n")
		if c, ok := conflictOf(t, f, fixer); ok && c.WaitingOn == "owner" {
			t.Fatalf("the fence obstacle reached the owner: %+v", c)
		}
		var paths []string
		fixtureWrite(t, f, func(tx *store.Tx) error {
			var raw string
			if err := tx.QueryRow(`SELECT allowed_paths FROM items WHERE item_id=?`, fixer).Scan(&raw); err != nil {
				return err
			}
			return json.Unmarshal([]byte(raw), &paths)
		})
		joined := strings.Join(paths, ",")
		if !strings.Contains(joined, "tests/tillr-roll.test.py") || !strings.Contains(joined, ".tillr.toml") {
			t.Fatalf("fence not widened: %v", paths)
		}
		p := refusalOf(t, f, work.Decision.RunRequest.RunID)
		if p.Step != RefusalNoOp || !strings.Contains(p.Detail, "tests/tillr-roll.test.py") {
			t.Fatalf("refusal %+v", p)
		}
	})
	t.Run("gate code", func(t *testing.T) {
		f, l, work := obstacleFixture(t, &obstacleSteps{}, fixer)
		settleReport(t, f, l, work, "OBSTACLE: fence\nPATHS: tools/fork-check.py\nEVIDENCE: the gate is wrong\nRESULT: blocked\n")
		c, ok := conflictOf(t, f, fixer)
		if !ok || c.WaitingOn != "owner" || !strings.Contains(c.Text, "tools/fork-check.py") {
			t.Fatalf("a gate-code fence request did not reach the owner: %+v", c)
		}
	})
}

// A red the replay confirms on the trunk is aimed at the landing that broke it, found the
// way the proof finds it: the check replayed at the green pointer and bisected over the
// recorded landings since. On 2026-11-18 the fixer was named for the commit it was
// observed on, not for the change that broke the check.
func TestAConfirmedTrunkRedIsAimedAtTheFirstRedLanding(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	green := runGit(t, f.root, "rev-parse", TrunkRef)
	commits := []string{}
	for _, name := range []string{"one", "culprit", "three", "four"} {
		runGit(t, f.root, "commit", "--allow-empty", "-qm", name)
		commits = append(commits, runGit(t, f.root, "rev-parse", TrunkRef))
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, sha := range commits {
			if err := recordFixtureLanding(tx, "item-"+sha[:6], "run-"+sha[:6], sha, i+1); err != nil {
				return err
			}
		}
		return nil
	})
	tip := commits[3]
	steps := &obstacleSteps{known: true, byRev: map[string][]string{green: nil, commits[0]: nil, commits[1]: {"go-build"}, commits[2]: {"go-build"}, tip: {"go-build"}}}
	got, note := l.culpritLanding(ctx, steps, Candidate{}, green, tip, []string{"go-build"})
	if got != commits[1] {
		t.Fatalf("culprit %s, want %s (%s); replays %v", got, commits[1], note, steps.revs)
	}
	// A red already present at the green pointer names no landing: the fixer stays on the
	// commit it was observed on, and the note says why.
	steps = &obstacleSteps{known: true, byRev: map[string][]string{green: {"go-build"}, tip: {"go-build"}}}
	if got, note := l.culpritLanding(ctx, steps, Candidate{}, green, tip, []string{"go-build"}); got != tip || note == "" {
		t.Fatalf("red at green: culprit %s, note %q", got, note)
	}
}
