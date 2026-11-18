package drain

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
)

func TestRefusedAndCurrentRestartRetainInflightWorkAndItsResult(t *testing.T) {
	for _, answer := range []string{restart.AnswerAlreadyCurrent, restart.AnswerRefused} {
		for _, kind := range []string{"worker", "rolling", "heavy"} {
			t.Run(answer+"/"+kind, func(t *testing.T) {
				var disk []byte
				check := func(context.Context, string) error { return errors.New("candidate rejected") }
				if answer == restart.AnswerRefused {
					disk = []byte("broken build")
				}
				h := newRestartHarness(t, disk, check, "")
				held := &restartHeldSteps{entered: make(chan struct{}, 1), release: make(chan struct{}), finished: make(chan struct{})}
				h.l.cfg.Steps = held
				result := &LoopResult{}
				returned := make(chan error, 1)
				tip := runGit(t, h.f.root, "rev-parse", TrunkRef)
				switch kind {
				case "worker":
					go func() { _, _, err := h.l.runRestartable(context.Background(), run.Request{}); returned <- err }()
				case "rolling":
					go func() { returned <- h.l.maybeRolling(context.Background(), tip, true, result) }()
				case "heavy":
					h.l.gatePolicy.LandingStrategy = "after-merge"
					if err := h.l.startHeavy(context.Background(), tip); err != nil {
						t.Fatal(err)
					}
				}
				awaitRestartTest(t, held.entered, "held work")
				request := h.request(t, 1)[0]
				ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
				a, err := restart.AwaitAnswer(ctx, h.f.storePath, request.ID, time.Millisecond)
				cancel()
				if err != nil || a.Answer != answer {
					t.Fatalf("answer=%+v/%v", a, err)
				}
				if h.execed != nil {
					t.Fatal("nonreplacement request executed")
				}
				select {
				case <-held.finished:
					t.Fatal("answer waited for work")
				default:
				}
				close(held.release)
				awaitRestartTest(t, held.finished, "work completion")
				if kind == "heavy" {
					h.l.heavyWG.Wait()
					if green := heavyGit(t, h.f.root, "rev-parse", GreenRef); green != tip {
						t.Fatalf("heavy result lost: %s", green)
					}
				} else {
					select {
					case err := <-returned:
						if err != nil {
							t.Fatalf("work canceled despite %s: %v", answer, err)
						}
					case <-time.After(hangGuard):
						t.Fatal("work result lost")
					}
					if kind == "rolling" && result.RollingGreen != 1 {
						t.Fatalf("rolling result lost: %+v", result)
					}
				}
			})
		}
	}
}
