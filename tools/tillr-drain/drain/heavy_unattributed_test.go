package drain

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr/store"
)

// replayNoMatch is what the gate's replay printed when attribution at a verified green
// pointer planned none of the checks it named (2026-11-18, tillr-view's suite).
const replayNoMatch = "fork-check replay: no planned check matches --only"

// tipRedAttributionErrors judges "unit" red everywhere but the base, and answers every
// single-check attribution with a non-verdict, as the replay did.
func tipRedAttributionErrors(base string) func(context.Context, string, string) ([]HeavyCheck, error) {
	return func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		if only != "" {
			return []HeavyCheck{{CheckID: only, Result: "error", Detail: replayNoMatch}}, nil
		}
		head, err := heavyRev(dir)
		if err != nil {
			return nil, err
		}
		result := "fail"
		if head == base {
			result = "pass"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, nil
	}
}

func heavyRev(dir string) (string, error) {
	return heavyGitOutput(context.Background(), dir, "rev-parse", "HEAD")
}

// A red at the tip whose attribution answers no verdict is still a red: the run says
// so, the pointer holds, and a fixer is filed against the tip naming why attribution
// failed. Before, the attribution error aborted the run as "unknown" and filed nothing,
// so every candidate was held "waiting on no open fixer recorded" with no mover.
func TestHeavyAttributionErrorStillFilesAFixerAgainstTheTip(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	heavyCommit(t, root, "bad")
	tip := heavyCommit(t, root, "worse")
	runner := heavyFixture(t, root, tipRedAttributionErrors(base))
	var fixed []string
	var fixedChecks []HeavyCheck
	runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		fixed = append(fixed, sha)
		fixedChecks = append(fixedChecks, checks...)
		return nil
	}
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("an attribution failure is not a runner fault: %v", err)
	}
	run := out.Runs[0]
	if run.Result != "red" {
		t.Fatalf("a red tip reads %q, want red", run.Result)
	}
	check := run.Checks[0]
	if check.FirstRedLanding != "" || check.GateFault {
		t.Fatalf("no landing may be accused without a verdict: %+v", check)
	}
	if !strings.Contains(check.Detail, "attribution failed") || !strings.Contains(check.Detail, replayNoMatch) {
		t.Fatalf("detail does not say attribution failed and why: %q", check.Detail)
	}
	if len(fixed) != 1 || fixed[0] != tip || len(fixedChecks) != 1 || fixedChecks[0].CheckID != "unit" {
		t.Fatalf("fixers=%v checks=%+v, want one against tip %s", fixed, fixedChecks, tip)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != base {
		t.Fatalf("red moved the pointer to %s", green)
	}
}

// A loop stop during attribution is not an attribution failure: nothing is filed, and
// the run reports the cancellation rather than a red it did not finish judging.
func TestHeavyCancelledAttributionFilesNothing(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	heavyCommit(t, root, "bad")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := heavyFixture(t, root, func(c context.Context, dir, only string) ([]HeavyCheck, error) {
		if only != "" {
			cancel()
			return nil, c.Err()
		}
		return []HeavyCheck{{CheckID: "unit", Result: "fail"}}, nil
	})
	runner.Fix = func(context.Context, string, []HeavyCheck) error {
		t.Error("a cancelled attribution filed a fixer")
		return nil
	}
	if _, err := runner.Run(ctx); err == nil {
		t.Fatal("a cancelled attribution reported success")
	}
}

// Through the loop: the unattributed red files one fixer against the tip whose brief
// names the failed attribution, and the red-cap hold names that fixer as its mover. A
// later red tip on the same check attaches to it, and the hold at the new tip still
// names it — the attached fixer's id is the older tip's, not the newest.
func TestHeavyUnattributedRedGivesTheHoldAMover(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "bad")
	if err := os.MkdirAll(filepath.Join(root, "config", "tillr"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "tillr", "gate-policy.json"), []byte(`{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	steps := heavyTestSteps{judge: tipRedAttributionErrors(base)}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard}}
	heavyOnce := func(at string) {
		t.Helper()
		l.heavyAttempted = ""
		if err := l.startHeavy(context.Background(), at); err != nil {
			t.Fatal(err)
		}
		l.heavyWG.Wait()
	}
	holdAt := func(at string) *redcap.Hold {
		t.Helper()
		var hold *redcap.Hold
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			var err error
			hold, err = redcap.Check(context.Background(), r, root, TrunkRef, at, "item-ordinary", time.Now().Add(time.Hour))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if hold == nil {
			t.Fatal("the red cap did not hold")
		}
		return hold
	}
	heavyOnce(tip)
	var id, text string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT items.item_id,item_criteria.text FROM items JOIN item_criteria USING(item_id)`).Scan(&id, &text)
	}); err != nil {
		t.Fatalf("no fixer was filed for the unattributed red: %v", err)
	}
	if id != TrunkFixerID(tip) || !strings.Contains(text, "unit") || !strings.Contains(text, "attribution failed") || !strings.Contains(text, replayNoMatch) {
		t.Fatalf("fixer=%s criteria=%s", id, text)
	}
	if hold := holdAt(tip); hold.FixerItem != id || strings.Contains(hold.Detail, "no open fixer") {
		t.Fatalf("hold names no mover: %+v", hold)
	}
	newer := heavyCommit(t, root, "worse")
	heavyOnce(newer)
	var count int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count)
	}); err != nil || count != 1 {
		t.Fatalf("the later red filed a second repair: count=%d %v", count, err)
	}
	if hold := holdAt(newer); hold.FixerItem != id {
		t.Fatalf("hold at the newer tip lost its mover: %+v", hold)
	}
	status, err := ReadHeavyStatus(context.Background(), root, s.Path())
	if err != nil || status.Result != "red" {
		t.Fatalf("status=%+v %v, want red rather than unknown", status, err)
	}
}
