package drain

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHeavyDefersOnlyRepairsWhoseDeclaredInputsWereTouched(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "red")
	var calls []string
	var newest string
	checks := []HeavyCheck{
		{CheckID: "unchanged", Result: "fail", Reads: []string{"value"}},
		{CheckID: "touched", Result: "fail", Reads: []string{"value", "other"}},
	}
	runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		calls = append(calls, only+":"+sha)
		if only == "" {
			if sha == red {
				for _, check := range checks {
					heavyCheckEnded(ctx, check)
				}
				return append([]HeavyCheck(nil), checks...), nil
			}
			return []HeavyCheck{{CheckID: "touched", Result: "pass"}, checks[0]}, nil
		}
		if sha == base && newest == "" {
			// Move during attribution, and restore the file so an endpoint diff
			// would miss that the later commits touched this check's input.
			if err := os.WriteFile(filepath.Join(root, "other"), []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
			heavyGit(t, root, "add", "other")
			heavyGit(t, root, "commit", "-qm", "touch other input")
			heavyGit(t, root, "rm", "other")
			heavyGit(t, root, "commit", "-qm", "restore other input")
			newest = heavyGit(t, root, "rev-parse", "HEAD")
		}
		result := "fail"
		if sha == base {
			result = "pass"
		}
		return []HeavyCheck{{CheckID: only, Result: result}}, nil
	})
	runner.Revert = func(_ context.Context, tip, _ string, _ []HeavyCheck, _ HeavyReplay) (RevertOutcome, error) {
		if tip != newest {
			t.Fatalf("attempted an automatic revert from the superseded proof %s", tip)
		}
		return RevertOutcome{Refused: "fixture only files fixers"}, nil
	}
	runner.Fix = func(_ context.Context, sha string, failed []HeavyCheck) error {
		if sha != red || len(failed) != 1 || failed[0].CheckID != "unchanged" {
			t.Fatalf("unexpected repair against %s: %+v", sha, failed)
		}
		calls = append(calls, "repair")
		return nil
	}
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{":" + red, "unchanged:" + base, "unchanged:" + red, "touched:" + base, "touched:" + red, "repair", ":" + newest, "repair"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("judge and repair order=%v, want %v", calls, want)
	}
	if len(out.Runs) != 2 || out.Runs[0].TipSHA != red || !strings.Contains(out.Runs[0].Checks[1].Detail, "touched a declared input: other") {
		t.Fatalf("missing original verdict and deferral: %+v", out)
	}
}

func TestHeavyKeepsRecordedFirstRedOnLaterRedTip(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "red")
	tip := heavyCommit(t, root, "still red")
	var calls []string
	runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		calls = append(calls, only+":"+sha)
		result := "fail"
		if sha == base {
			result = "pass"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
	})
	first, err := runner.Run(context.Background())
	if err != nil || first.Runs[0].Checks[0].FirstRedLanding != red {
		t.Fatalf("first run=%+v, err=%v", first, err)
	}
	if !reflect.DeepEqual(calls, []string{":" + tip, "unit:" + base, "unit:" + tip, "unit:" + red}) {
		t.Fatalf("first search order=%v", calls)
	}
	calls = nil
	newTip := heavyCommit(t, root, "still red later")
	second, err := runner.Run(context.Background())
	if err != nil || second.Runs[0].Checks[0].FirstRedLanding != red || !reflect.DeepEqual(calls, []string{":" + newTip}) {
		t.Fatalf("later run=%+v, err=%v, calls=%v", second, err, calls)
	}
}
