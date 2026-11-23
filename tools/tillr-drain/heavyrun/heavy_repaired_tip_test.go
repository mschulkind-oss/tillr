package heavyrun

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

// A proof can finish red after the repair has already landed. The report still
// names the historical failures until the advanced tip is proved, but the
// focused passes on that tip make another fixer unnecessary.
func TestHeavyHistoricalGoFailuresNeedNoFixerAfterFocusedPasses(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "red")
	const fork = "patch-branch-inv2/fork-pi"
	ids := []string{
		GoTestPrefix + "tools/tillr-drain:example/drain:TestOne",
		GoTestPrefix + "tools/tillr-drain:example/drain:TestTwo",
	}
	var repaired string
	focused := map[string]bool{}
	runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		result := "pass"
		if sha == red {
			result = "fail"
		}
		if only != "" {
			if sha == repaired {
				focused[only] = true
			}
			return []HeavyCheck{{CheckID: only, Result: result}}, nil
		}
		if sha == red {
			repaired = heavyCommit(t, root, "repair")
		}
		return []HeavyCheck{
			{CheckID: ids[0], Result: result},
			{CheckID: ids[1], Result: result},
			{CheckID: fork, Result: "unavailable"},
		}, nil
	})
	runner.Revert = func(context.Context, string, string, []HeavyCheck, HeavyReplay) (RevertOutcome, error) {
		t.Fatal("attempted to revert from a historical proof")
		return RevertOutcome{}, nil
	}
	runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		t.Fatalf("filed a duplicate repair against %s: %+v", sha, checks)
		return nil
	}
	runner.Report = func(run HeavyRun) {
		if run.TipSHA != red {
			return
		}
		status, err := heavy.ReadStatus(ctx, root, "")
		if err != nil {
			t.Fatal(err)
		}
		if status.Result != "red" || status.GreenCommit != base || status.TipCommit != repaired ||
			len(status.FailingChecks) != len(ids) || len(status.UnavailableChecks) != 1 || status.UnavailableChecks[0].CheckID != fork {
			t.Fatalf("expected historical red alongside the unproved repaired tip: %+v", status)
		}
		for i, check := range status.FailingChecks {
			if check.CheckID != ids[i] || check.FirstRedLanding != red || check.RevertedBy != "" || !focused[check.CheckID] ||
				!strings.Contains(check.Detail, "repair deferred to proof of "+ShortSHA(repaired)+": the focused check passed on the advanced trunk") {
				t.Fatalf("missing attribution or focused-pass reason: %+v (replayed %v)", check, focused)
			}
		}
	}
	out, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 2 || out.Runs[0].TipSHA != red || out.Runs[1].TipSHA != repaired || out.Runs[1].Result != "green" {
		t.Fatalf("expected historical red followed by green proof of the repaired tip: %+v", out)
	}
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != repaired {
		t.Fatalf("green pointer = %s, want %s", got, repaired)
	}
}

// When a heavy proof records a gate fault with repair deferred and the next proof
// fails the same check again, the runner must not defer it a second time: it files
// one fixer naming that check and its log path.
func TestHeavyDeferredGateFaultFilesFixerOnNextProofFailure(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	first := heavyCommit(t, root, "first")
	const checkID = GoTestPrefix + "tools/tillr-drain:example/drain:TestFlaky"
	const logFirst = "/logs/first.log"
	const logSecond = "/logs/second.log"
	var second, third string
	runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		if only != "" {
			// Every focused replay passes (flaky check), causing a gate fault
			// on attribution and a focused-pass deferral on the first proof.
			return []HeavyCheck{{CheckID: only, Result: "pass"}}, nil
		}
		switch sha {
		case first:
			second = heavyCommit(t, root, "second")
			return []HeavyCheck{{CheckID: checkID, Result: "fail", LogPath: logFirst, LogRef: logFirst}}, nil
		case second:
			third = heavyCommit(t, root, "third")
			return []HeavyCheck{{CheckID: checkID, Result: "fail", LogPath: logSecond, LogRef: logSecond}}, nil
		case third:
			return []HeavyCheck{{CheckID: checkID, Result: "pass"}}, nil
		default:
			t.Fatalf("unexpected tip %s", sha)
			return nil, nil
		}
	})
	type fixCall struct {
		sha    string
		checks []HeavyCheck
	}
	var fixes []fixCall
	runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		fixes = append(fixes, fixCall{sha: sha, checks: append([]HeavyCheck(nil), checks...)})
		return nil
	}
	out, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 3 {
		t.Fatalf("runs = %d, want 3: %+v", len(out.Runs), out.Runs)
	}
	if !out.Runs[0].Checks[0].GateFault || !strings.Contains(out.Runs[0].Checks[0].Detail, "repair deferred to proof of "+ShortSHA(second)) {
		t.Fatalf("first proof should record a deferred gate fault: %+v", out.Runs[0].Checks[0])
	}
	if !out.Runs[1].Checks[0].GateFault || strings.Contains(out.Runs[1].Checks[0].Detail, "repair deferred to proof of ") {
		t.Fatalf("second proof should not defer again: %+v", out.Runs[1].Checks[0])
	}
	if len(fixes) != 1 || fixes[0].sha != second || len(fixes[0].checks) != 1 || fixes[0].checks[0].CheckID != checkID || fixes[0].checks[0].LogPath != logSecond {
		t.Fatalf("fixes = %+v, want one fixer at %s for %s with log %s", fixes, second, checkID, logSecond)
	}
	if out.Runs[2].Result != "green" {
		t.Fatalf("third proof = %+v, want green", out.Runs[2])
	}
}

// A deferred gate fault followed by a passing proof files nothing.
func TestHeavyDeferredGateFaultFollowedByPassingProofFilesNothing(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	first := heavyCommit(t, root, "first")
	const checkID = GoTestPrefix + "tools/tillr-drain:example/drain:TestFlaky"
	var second string
	runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		if only != "" {
			return []HeavyCheck{{CheckID: only, Result: "pass"}}, nil
		}
		if sha == first {
			second = heavyCommit(t, root, "second")
			return []HeavyCheck{{CheckID: checkID, Result: "fail", LogPath: "/logs/first.log"}}, nil
		}
		return []HeavyCheck{{CheckID: checkID, Result: "pass"}}, nil
	})
	runner.Fix = func(_ context.Context, sha string, checks []HeavyCheck) error {
		t.Fatalf("unexpected fixer filed against %s: %+v", sha, checks)
		return nil
	}
	out, err := runner.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 2 || !out.Runs[0].Checks[0].GateFault ||
		!strings.Contains(out.Runs[0].Checks[0].Detail, "repair deferred to proof of "+ShortSHA(second)) ||
		out.Runs[1].Result != "green" {
		t.Fatalf("expected deferred gate fault followed by green proof: %+v", out.Runs)
	}
}
