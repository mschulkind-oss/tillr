package drain

import (
	"context"
	"reflect"
	"strings"
	"syscall"
	"testing"

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
