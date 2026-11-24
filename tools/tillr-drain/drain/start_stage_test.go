package drain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestStageClaimedFailureReleasesLaneAndPreservesSiblingBoundary(t *testing.T) {
	for _, escape := range []bool{false, true} {
		name := "authorized stage only"
		if escape {
			name = "untouched sibling changed"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			f := newRefusalLoopFixtureWithLanes(t, 3, "item-a", "item-b")
			steps := &recordingSteps{}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			var l *Loop
			l = f.loop(t, steps, stagerFunc(func(_ context.Context, spec StageSpec) error {
				if spec.BaseSHA != base || spec.LaneID != "lane-1" {
					t.Errorf("staging %s on %s at %s, want lane-1 at %s", spec.ItemID, spec.LaneID, spec.BaseSHA, base)
				}
				if _, idle := l.idleLaneEpochs()[spec.LaneID]; idle {
					t.Error("lane appears idle during staging")
				}
				if spec.ItemID == "item-a" {
					writeRepoFile(t, l.lanePath(spec.LaneID), "stage.txt", "authorized staging write")
					return syscall.ENAMETOOLONG
				}
				return nil
			}))
			l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
			holdWALKeeper(t, f.storePath)
			var result LoopResult
			claim := func(want string) (*pendingWork, Decision) {
				t.Helper()
				v, s, e := claimFixture(t, l)
				ok, work, _, d, err := l.claimFastStart(ctx, v, s, e, l.cfg.Now())
				if err != nil || !ok || work == nil || d.ItemID != want || work.BaseSHA != base {
					t.Fatalf("claim %s: ok=%v work=%+v decision=%+v err=%v", want, ok, work, d, err)
				}
				v.noteStartClaimed(d, work)
				return work, d
			}
			watched := []string{"lane-1", "lane-3"}
			epochs := l.idleLaneEpochs()
			before, err := l.snapshotIdleSiblings(ctx, l.lanePath("lane-2"), watched, epochs)
			if err != nil {
				t.Fatal(err)
			}
			work, first := claim("item-a")
			runGit(t, f.root, "commit", "--allow-empty", "-qm", "later trunk")
			runGit(t, f.root, "update-ref", GreenRef, base)
			if err := l.stageAndLaunchClaimed(ctx, first, work.BaseSHA, l.cfg.Now(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Dispatched != 0 || steps.ran(first.ItemID) {
				t.Fatal("failed stage launched a run")
			}
			if l.startView.Load().snapshot().occupied[first.LaneID] {
				t.Fatal("view still marks the failed claim's lane occupied")
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				faults, err := ReadItemFaults(r)
				if err != nil {
					return err
				}
				if fault := faults[first.ItemID]; fault.Reason != ReasonStageFailed || fault.RunID != first.RunRequest.RunID || !strings.Contains(fault.Detail, syscall.ENAMETOOLONG.Error()) {
					t.Errorf("failed stage fault = %+v", fault)
				}
				var dispatches int
				if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE item_id=?`, first.ItemID).Scan(&dispatches); err != nil {
					return err
				}
				busy, err := readLanes(r, l.cfg.Now())
				if dispatches != 0 || busy[first.LaneID] {
					t.Errorf("failed claim still present: dispatches=%d busy=%v", dispatches, busy)
				}
				return err
			})
			if got := unmovedLanes(watched, epochs, l.idleLaneEpochs()); !reflect.DeepEqual(got, []string{"lane-3"}) {
				t.Fatalf("still watching %v, want only the untouched lane", got)
			}
			if escape {
				writeRepoFile(t, l.lanePath("lane-3"), "escape.txt", "outside target")
			}
			var boundary LoopResult
			sibling := Decision{LaneID: "lane-2", RunRequest: &run.Request{RunID: "run-sibling-bracket", ItemID: "item-sibling-bracket"}}
			if _, err := l.closeAsyncBracket(ctx, sibling, l.cfg.Now(), before, &boundary, watched, epochs); err != nil {
				t.Fatal(err)
			}
			if (boundary.StopStep == "boundary check") != escape || (escape && !strings.Contains(boundary.Detail, "escape.txt")) {
				t.Fatalf("unexpected sibling boundary: %q %q", boundary.StopStep, boundary.Detail)
			}
			work, second := claim("item-b")
			if err := l.stageAndLaunchClaimed(ctx, second, work.BaseSHA, l.cfg.Now(), &result); err != nil {
				t.Fatal(err)
			}
			select {
			case completion := <-l.completions:
				if completion.id != second.RunRequest.RunID || completion.outcome.err != nil || completion.outcome.stop != "" || !steps.ran(second.ItemID) {
					t.Fatalf("second staged run did not complete: %+v", completion)
				}
			case <-ctx.Done():
				t.Fatal("second staged run did not launch")
			}
		})
	}
}

func TestPendingRestartAnsweredDuringSlowStagePreservesStagedRun(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement []byte
		wantStop    string
		wantAnswer  string
	}{
		{name: "without-replacing", replacement: nil, wantStop: "", wantAnswer: restart.AnswerAlreadyCurrent},
		{name: "with-replacement", replacement: []byte("next build"), wantStop: StopRestarted, wantAnswer: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			h := newRestartHarness(t, tc.replacement, func(context.Context, string) error { return nil }, "item-a")
			h.l.cfg.Lanes = []string{"lane-1"}
			steps := &recordingSteps{}
			h.l.cfg.Steps = steps
			stageEntered := make(chan struct{})
			releaseStage := make(chan struct{})
			h.l.cfg.Stage = stagerFunc(func(_ context.Context, _ StageSpec) error {
				close(stageEntered)
				<-releaseStage
				return nil
			})
			holdWALKeeper(t, h.f.storePath)

			v, s, e := claimFixture(t, h.l)
			ok, work, _, d, err := h.l.claimFastStart(ctx, v, s, e, h.l.cfg.Now())
			if err != nil || !ok || work == nil {
				t.Fatalf("claimFastStart: ok=%v work=%+v err=%v", ok, work, err)
			}
			v.noteStartClaimed(d, work)

			var result LoopResult
			stageDone := make(chan error, 1)
			go func() {
				stageDone <- h.l.stageAndLaunchClaimed(ctx, d, work.BaseSHA, h.l.cfg.Now(), &result)
			}()

			select {
			case <-stageEntered:
			case <-time.After(hangGuard):
				t.Fatal("slow stage did not start")
			}

			req := h.request(t, 1)[0]
			handled, stop, err := h.l.gateRestart(ctx, &LoopResult{})
			if err != nil || !handled || stop != tc.wantStop {
				close(releaseStage)
				<-stageDone
				t.Fatalf("gateRestart during slow stage = handled=%v stop=%q err=%v; want handled=true stop=%q", handled, stop, err, tc.wantStop)
			}
			if tc.wantAnswer != "" {
				if got := h.answer(t, req.ID).Answer; got != tc.wantAnswer {
					close(releaseStage)
					<-stageDone
					t.Fatalf("restart answer = %q, want %q", got, tc.wantAnswer)
				}
			} else if len(h.execed) == 0 {
				close(releaseStage)
				<-stageDone
				t.Fatalf("replacement did not exec: %v", h.execed)
			}

			close(releaseStage)
			var stageErr error
			select {
			case stageErr = <-stageDone:
			case <-time.After(hangGuard):
				t.Fatal("slow stage did not finish after release")
			}

			if tc.wantStop == "" {
				if stageErr != nil {
					t.Fatalf("stageAndLaunchClaimed err = %v, want nil", stageErr)
				}
				select {
				case completion := <-h.l.completions:
					if completion.id != d.RunRequest.RunID || completion.outcome.err != nil || completion.outcome.stop != "" || !steps.ran(d.ItemID) {
						t.Fatalf("staged run did not complete: id=%s stop=%q err=%v ran=%v", completion.id, completion.outcome.stop, completion.outcome.err, steps.ran(d.ItemID))
					}
				case <-time.After(hangGuard):
					t.Fatal("staged run did not launch after non-replacing restart")
				}
				return
			}

			if !errors.Is(stageErr, errRestartPending) {
				t.Fatalf("stageAndLaunchClaimed after replacement err = %v, want errRestartPending", stageErr)
			}
			fixtureRead(t, h.f, func(r *store.Reader) error {
				var dispatches int
				if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE item_id=? AND run_id=?`, d.ItemID, d.RunRequest.RunID).Scan(&dispatches); err != nil {
					return err
				}
				if dispatches != 1 {
					t.Errorf("dispatches for %s/%s = %d, want 1 (claim must not be retracted on replacement)", d.ItemID, d.RunRequest.RunID, dispatches)
				}
				return nil
			})
		})
	}
}
