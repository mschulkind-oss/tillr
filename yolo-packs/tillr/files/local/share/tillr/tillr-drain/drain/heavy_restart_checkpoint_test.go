package drain

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/restart"
)

func TestHeavyReplacementKeepsProofAndReplayResults(t *testing.T) {
	for _, hold := range []string{"proof", "replay", "completed"} {
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
			var request restart.Request
			if hold == "completed" {
				h.l.heavyWG.Wait()
				request = h.request(t, 1)[0]
			} else {
				awaitRestartTest(t, entered, "live "+hold)
				request = h.request(t, 1)[0]
				_, stop, err := h.l.gateRestart(ctx, &LoopResult{})
				close(release)
				h.l.heavyWG.Wait()
				if err != nil || stop != "" || h.execed != nil {
					t.Fatalf("replacement discarded live %s: %s, %v, %v", hold, stop, err, h.execed)
				}
			}
			if _, stop, err := h.l.gateRestart(ctx, &LoopResult{}); err != nil || stop != StopRestarted {
				t.Fatalf("replacement after checkpoint=%s, %v", stop, err)
			}
			wantOld := []string{":" + tip, "unit:" + base, "unit:" + tip}
			switch hold {
			case "proof":
				wantOld = []string{":" + tip}
			case "replay":
				wantOld = []string{":" + tip, "unit:" + base}
			}
			if !reflect.DeepEqual(calls, wantOld) {
				t.Fatalf("old image calls=%v want=%v", calls, wantOld)
			}
			dir, err := heavyDirectory(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if hold != "proof" {
				if red, ok := readHeavyReplay(dir, base, "unit"); red || !ok {
					t.Fatal("finished replay was not recorded")
				}
			}
			// The replacement has no old goroutine, attempted tip, or in-memory result.
			var nextCalls []string
			next := &Loop{cfg: h.l.cfg}
			next.cfg.Steps = heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
				sha := heavyGit(t, dir, "rev-parse", "HEAD")
				nextCalls = append(nextCalls, only+":"+sha)
				if hold == "completed" {
					t.Error("replacement reran the completed proof")
				}
				result := "fail"
				if sha == base {
					result = "pass"
				}
				return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
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
			var wantNext []string
			switch hold {
			case "proof":
				wantNext = []string{":" + tip, "unit:" + base, "unit:" + tip}
			case "replay":
				wantNext = []string{":" + tip, "unit:" + tip}
			case "completed":
				wantNext = nil
			}
			if !reflect.DeepEqual(nextCalls, wantNext) {
				t.Fatalf("replacement calls=%v want=%v", nextCalls, wantNext)
			}
		})
	}
}

func TestPendingRestartTakenBetweenFirstRedReplaysAndSearchResumesWithoutRepeat(t *testing.T) {
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
		return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
	}}

	if err := h.l.startHeavy(ctx, c3); err != nil {
		t.Fatal(err)
	}
	awaitRestartTest(t, enteredBaseReplay, "first replay at green base")

	req := h.request(t, 1)[0]
	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); handled || stop != "" || err != nil || h.execed != nil {
		t.Fatalf("gateRestart during live replay = %v, %q, %v (execed=%v); want deferred", handled, stop, err, h.execed)
	}

	close(releaseBaseReplay)
	h.l.heavyWG.Wait()

	wantOld := []string{":" + c3, "unit:" + base}
	if !reflect.DeepEqual(oldCalls, wantOld) {
		t.Fatalf("old image calls = %v, want %v (next replay must not start while restart is pending)", oldCalls, wantOld)
	}
	dir, err := heavyDirectory(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := completedHeavyRed(dir, base, c3); ok {
		t.Fatal("interrupted first-red search published a completed red proof")
	}

	if handled, stop, err := h.l.gateRestart(ctx, &LoopResult{}); !handled || stop != StopRestarted || err != nil || h.execed == nil {
		t.Fatalf("gateRestart after first replay = %v, %q, %v (execed=%v); want replaced", handled, stop, err, h.execed)
	}

	var nextCalls []string
	next := &Loop{cfg: h.l.cfg}
	next.cfg.Steps = heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		sha := heavyGit(t, dir, "rev-parse", "HEAD")
		nextCalls = append(nextCalls, only+":"+sha)
		result := "fail"
		if isGreen(sha) {
			result = "pass"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
	}}
	t.Setenv(restart.RequestEnv, req.ID)
	t.Setenv(restart.FromEnv, "old-build")
	if err := next.adoptRestart(ctx); err != nil {
		t.Fatal(err)
	}
	if ans := h.answer(t, req.ID); ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer before next replay = %+v, want %q", ans, restart.AnswerRestarted)
	}

	if err := next.startHeavy(ctx, c3); err != nil {
		t.Fatal(err)
	}
	next.heavyWG.Wait()

	if slices.Contains(nextCalls, "unit:"+base) {
		t.Fatalf("replacement repeated already-judged replay at base %s: calls=%v", base, nextCalls)
	}
	if !slices.Contains(nextCalls, "unit:"+c3) {
		t.Fatalf("replacement did not continue search with next replay at tip %s: calls=%v", c3, nextCalls)
	}
	completed, ok := completedHeavyRed(dir, base, c3)
	if !ok {
		t.Fatal("replacement did not complete the red proof")
	}
	if len(completed.Checks) != 1 || completed.Checks[0].FirstRedLanding != c2 {
		t.Fatalf("completed checks = %+v, want first red landing %s", completed.Checks, c2)
	}
}
