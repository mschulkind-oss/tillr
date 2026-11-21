package drain

import (
	"context"
	"reflect"
	"testing"
)

func TestHeavyProvesNewestTipBeforeSearchingHistory(t *testing.T) {
	for _, move := range []string{"at verdict", "during replay", "new tip stays red"} {
		t.Run(move, func(t *testing.T) {
			root, base := heavyRepo(t)
			heavyGit(t, root, "update-ref", GreenRef, base)
			older := heavyCommit(t, root, "older")
			red := heavyCommit(t, root, "red")
			var calls []string
			var newest string
			runner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
				sha := heavyGit(t, dir, "rev-parse", "HEAD")
				check := HeavyCheck{CheckID: "unit", Result: "pass"}
				switch {
				case only == "" && sha == red:
					calls = append(calls, "red tip")
					check.Result = "fail"
					if move != "during replay" {
						newest = heavyCommit(t, root, "newest")
					}
					heavyCheckEnded(ctx, check)
				case only != "" && sha == base:
					calls = append(calls, "base replay")
					if newest == "" {
						newest = heavyCommit(t, root, "newest")
					}
				case only == "" && sha == newest:
					calls = append(calls, "newest tip")
					if move == "new tip stays red" {
						check.Result = "fail"
					}
				case move == "new tip stays red":
					calls = append(calls, "search replay")
					if sha != older {
						check.Result = "fail"
					}
				default:
					calls = append(calls, "unwanted replay")
					check.Result = "fail"
				}
				return []HeavyCheck{check}, nil
			})
			out, err := runner.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"red tip", "newest tip"}
			if move == "during replay" {
				want = []string{"red tip", "base replay", "newest tip"}
			}
			if move == "new tip stays red" {
				want = append(want, "base replay", "search replay", "search replay", "search replay")
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("judge order=%v, want %v", calls, want)
			}
			if move == "new tip stays red" {
				if len(out.Runs) != 1 || out.Runs[0].TipSHA != newest || out.Runs[0].Checks[0].FirstRedLanding != red {
					t.Fatalf("unexpected attribution: %+v", out)
				}
			} else if green := heavyGit(t, root, "rev-parse", GreenRef); green != newest {
				t.Fatalf("green=%s, want %s", green, newest)
			}
		})
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
