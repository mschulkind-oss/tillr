package drain

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/restart"
)

func TestHeavyReplacementKeepsProofAndReplayResults(t *testing.T) {
	for _, hold := range []string{"proof", "replay"} {
		t.Run(hold, func(t *testing.T) {
			h := newRestartHarness(t, []byte("replacement"), func(context.Context, string) error { return nil }, "")
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			root := h.f.root
			base := heavyGit(t, root, "rev-parse", TrunkRef)
			heavyGit(t, root, "update-ref", GreenRef, base)
			tip := heavyCommit(t, root, "red")
			entered, release := make(chan struct{}), make(chan struct{})
			var calls []string
			h.l.cfg.Steps = heavyTestSteps{judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
				sha := heavyGit(t, dir, "rev-parse", "HEAD")
				calls = append(calls, only+":"+sha)
				if (hold == "proof" && only == "") || (hold == "replay" && sha == base) {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				result := "fail"
				if sha == base {
					result = "pass"
				}
				return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
			}}
			if err := h.l.startHeavy(ctx, tip); err != nil {
				t.Fatal(err)
			}
			awaitRestartTest(t, entered, "live "+hold)
			request := h.request(t, 1)[0]
			handled, stop, err := h.l.gateRestart(ctx, &LoopResult{})
			if err != nil || !handled || stop != StopRestarted || h.execed == nil {
				t.Fatalf("replacement during live %s = %v, %q, %v (execed=%v); want immediate replacement", hold, handled, stop, err, h.execed)
			}
			close(release)
			h.l.heavyWG.Wait()
			wantOld := []string{":" + tip, "unit:" + base, "unit:" + tip}
			if !reflect.DeepEqual(calls, wantOld) {
				t.Fatalf("old image calls=%v want=%v", calls, wantOld)
			}
			dir, err := heavyDirectory(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if red, ok := readHeavyReplay(dir, base, "unit"); red || !ok {
				t.Fatal("finished replay was not recorded")
			}
			completed, ok := completedHeavyRed(dir, base, tip)
			if !ok {
				t.Fatal("red proof did not publish completed red before restart")
			}
			if len(completed.Checks) != 1 || completed.Checks[0].FirstRedLanding != tip {
				t.Fatalf("completed checks = %+v, want first red landing %s", completed.Checks, tip)
			}
			events := readHeavyProgressEvents(t, h.l.cfg.Store)
			if len(events) == 0 {
				t.Fatal("expected heavy progress events")
			}
			wantEnded := HeavyProgressFact{TipSHA: tip, Phase: "ended", Outcome: "red", FailingChecks: "unit"}
			if last := events[len(events)-1].Fact; last != wantEnded {
				t.Fatalf("ended progress event = %+v, want %+v", last, wantEnded)
			}
			// The replacement has no old goroutine, attempted tip, or in-memory result.
			next := &Loop{cfg: h.l.cfg}
			next.cfg.Steps = heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
				t.Error("replacement reran the completed proof")
				return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
			}}
			t.Setenv(restart.RequestEnv, request.ID)
			t.Setenv(restart.FromEnv, "old-build")
			if err := next.adoptRestart(ctx); err != nil {
				t.Fatal(err)
			}
			if ans := h.answer(t, request.ID); ans.Answer != restart.AnswerRestarted {
				t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
			}
			if err := next.startHeavy(ctx, tip); err != nil {
				t.Fatal(err)
			}
			next.heavyWG.Wait()
		})
	}
}

func TestRedProofFinishesAttributionBeforePendingRestartAndNextLoopReusesReplays(t *testing.T) {
	h := newRestartHarness(t, []byte("replacement"), func(context.Context, string) error { return nil }, "")
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	root := h.f.root
	base := heavyGit(t, root, "rev-parse", TrunkRef)
	heavyGit(t, root, "update-ref", GreenRef, base)
	c1 := heavyCommit(t, root, "green-1")
	c2 := heavyCommit(t, root, "red-culprit")
	c3 := heavyCommit(t, root, "red-tip")

	isGreen := func(sha string) bool {
		return sha == base || sha == c1
	}

	enteredBaseReplay := make(chan struct{})
	releaseBaseReplay := make(chan struct{})
	var oldCalls []string
	h.l.cfg.Steps = heavyTestSteps{judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		oldCalls = append(oldCalls, only+":"+sha)
		if only == "unit" && sha == base {
			close(enteredBaseReplay)
			select {
			case <-releaseBaseReplay:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		result := "fail"
		if isGreen(sha) {
			result = "pass"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Detail: "first failure"}}, nil
	}}

	if err := h.l.startHeavy(ctx, c3); err != nil {
		t.Fatal(err)
	}
	awaitRestartTest(t, enteredBaseReplay, "first replay at green base")

	req := h.request(t, 1)[0]
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil || h.execed == nil {
		t.Fatalf("gateRestart during live replay = %v, %q, %v (execed=%v); want immediate replacement", handled, stop, err, h.execed)
	}

	close(releaseBaseReplay)
	h.l.heavyWG.Wait()

	dir, err := heavyDirectory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	completed, ok := completedHeavyRed(dir, base, c3)
	if !ok {
		t.Fatal("red proof did not finish and publish before pending restart")
	}
	if len(completed.Checks) != 1 || completed.Checks[0].FirstRedLanding != c2 {
		t.Fatalf("completed checks = %+v, want first red landing %s", completed.Checks, c2)
	}
	events := readHeavyProgressEvents(t, h.l.cfg.Store)
	if len(events) == 0 {
		t.Fatal("expected heavy progress events")
	}
	wantEnded := HeavyProgressFact{TipSHA: c3, Phase: "ended", Outcome: "red", FailingChecks: "unit"}
	if last := events[len(events)-1].Fact; last != wantEnded {
		t.Fatalf("ended progress event = %+v, want %+v", last, wantEnded)
	}

	// A new commit lands and the replacement loop proves it with a different failure detail
	// so attribution cannot short-circuit on heavy-attributions.json; it must still reuse
	// the replays recorded before the restart instead of searching from the beginning.
	c4 := heavyCommit(t, root, "red-after-restart")
	var nextCalls []string
	next := &Loop{cfg: h.l.cfg}
	next.cfg.Steps = heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		nextCalls = append(nextCalls, only+":"+sha)
		result := "fail"
		if isGreen(sha) {
			result = "pass"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Detail: "second failure"}}, nil
	}}
	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := next.adoptRestart(ctx); err != nil {
		t.Fatal(err)
	}
	if ans := h.answer(t, req.ID); ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}

	if err := next.startHeavy(ctx, c4); err != nil {
		t.Fatal(err)
	}
	next.heavyWG.Wait()

	for _, alreadyJudged := range []string{base, c1, c2} {
		if slices.Contains(nextCalls, "unit:"+alreadyJudged) {
			t.Fatalf("replacement repeated already-judged replay at %s: calls=%v (old calls=%v)", alreadyJudged, nextCalls, oldCalls)
		}
	}
	completedNext, ok := completedHeavyRed(dir, base, c4)
	if !ok {
		t.Fatal("replacement did not complete the red proof at new tip")
	}
	if len(completedNext.Checks) != 1 || completedNext.Checks[0].FirstRedLanding != c2 {
		t.Fatalf("replacement completed checks = %+v, want first red landing %s", completedNext.Checks, c2)
	}
}
