package drain

import (
	"context"
	"reflect"
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
			_, stop, err := h.l.gateRestart(ctx, &LoopResult{})
			close(release)
			h.l.heavyWG.Wait()
			if err != nil || stop != "" || h.execed != nil {
				t.Fatalf("replacement discarded live %s: %s, %v, %v", hold, stop, err, h.execed)
			}
			if _, stop, err := h.l.gateRestart(ctx, &LoopResult{}); err != nil || stop != StopRestarted {
				t.Fatalf("replacement after checkpoint=%s, %v", stop, err)
			}
			want := []string{":" + tip, "unit:" + base, "unit:" + tip}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("old image calls=%v want=%v", calls, want)
			}
			dir, err := heavyDirectory(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if red, ok := readHeavyReplay(dir, base, "unit"); red || !ok {
				t.Fatal("finished replay was not recorded")
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
			if err := next.startHeavy(ctx, tip); err != nil {
				t.Fatal(err)
			}
			next.heavyWG.Wait()
		})
	}
}
