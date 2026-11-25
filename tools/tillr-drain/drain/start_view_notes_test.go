package drain

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func noteFixture(at time.Time) startSnapshot {
	return startSnapshot{
		occupied: map[string]bool{}, storeRuns: map[string]string{},
		rotation: []string{"lane-1", "lane-2"}, freeLanes: []string{"lane-1", "lane-2"},
		laneFreeAt: map[string]time.Time{"lane-1": at, "lane-2": at},
		readyAt:    map[string]time.Time{"item-a": at, "item-b": at},
		entries:    map[string]startViewEntry{"item-a": {decision: noteDecision("item-a", "lane-1", "run-a")}},
	}
}

func noteDecision(item, lane, id string) Decision {
	return Decision{Action: ActionDispatch, ItemID: item, LaneID: lane, RunRequest: &run.Request{RunID: id, ItemID: item, LaneID: lane}}
}

func TestReleaseOfAClaimedLaneFreesItAndWakesTheStart(t *testing.T) {
	at := time.Now()
	v := newStartView()
	initial := noteFixture(at)
	v.current.Store(&initial)
	a := noteDecision("item-a", "lane-1", "run-a")
	v.noteStartClaimed(a, nil)
	awaitStartViewSignal(t, v.updated)
	claimed := v.snapshot()
	if !claimed.occupied[a.LaneID] || claimed.holds[a.ItemID] != ReasonRunLive || !initial.readyAt[a.ItemID].Equal(at) || initial.occupied[a.LaneID] {
		t.Fatalf("bad claim or mutation of old snapshot: %+v", claimed)
	}
	v.noteLaneReleased(a.LaneID, a.RunRequest.RunID, false, at.Add(time.Second))
	select {
	case <-v.updated:
	default:
		t.Fatal("release did not notify")
	}
	free := v.snapshot()
	if free.occupied[a.LaneID] || !slices.Equal(free.freeLanes, initial.freeLanes) || !free.laneFreeAt[a.LaneID].Equal(at.Add(time.Second)) {
		t.Fatalf("released lane not free in lane order: %v", free.freeLanes)
	}
}

func TestReleaseOfAnEarlierRunDoesNotFreeALaterOne(t *testing.T) {
	at := time.Now()
	v := newStartView()
	initial := noteFixture(at)
	v.current.Store(&initial)
	v.noteStartClaimed(noteDecision("item-a", "lane-1", "run-new"), nil)
	v.noteLaneReleased("lane-1", "run-old", true, at)
	got := v.snapshot()
	if !got.occupied["lane-1"] || slices.Contains(got.freeLanes, "lane-1") {
		t.Fatal("stale release freed the new occupant")
	}
}

func TestReleaseByNameLeavesAFoldedUnsettledRunHoldingItsLane(t *testing.T) {
	at := time.Now()
	v := newStartView()
	s := noteFixture(at)
	s.pending = &pendingHistoryState{works: map[string]*pendingWork{"item-a": {Decision: noteDecision("item-a", "lane-1", "run-a")}}}
	s.storeRuns["lane-1"] = "run-a"
	s.occupied["lane-1"] = true
	s.freeLanes = []string{"lane-2"}
	v.current.Store(&s)
	v.noteLaneReleased("lane-1", "run-a", false, at)
	got := v.snapshot()
	if !got.occupied["lane-1"] || slices.Contains(got.freeLanes, "lane-1") || got.pending.works["item-a"].LaneReleased {
		t.Fatal("released examination lease freed an unsettled candidate's lane")
	}
}

func TestNotesDuringASyncSurviveItsInstall(t *testing.T) {
	at := time.Now()
	v := newStartView()
	a := noteDecision("item-a", "lane-1", "run-a")
	b := noteDecision("item-b", "lane-1", "run-b")
	stale := noteFixture(at)
	initial := noteFixture(at)
	v.current.Store(&initial)
	v.noteStartClaimed(a, nil)
	// The incoming read sees the old lease, but not its release or replacement.
	stale.storeRuns["lane-1"] = "run-a"
	stale.occupied["lane-1"] = true
	stale.setStartWork(&pendingWork{Decision: a}, nil)
	stale.tip = "refreshed-tip" // distinguishes installation from the pre-install publication
	stale.offerLanes(stale.rotation, at)
	entered, resume := make(chan struct{}), make(chan struct{})
	stop := v.watch(context.Background(), time.Hour, nil, func(context.Context, startWatchChange) (startSnapshot, error) {
		close(entered)
		<-resume
		return stale, nil
	})
	defer stop()
	<-entered
	v.noteLaneReleased("lane-1", "run-a", true, at)
	v.noteStartClaimed(b, nil)
	v.noteItemHold("item-a", heldItem{reason: ReasonOwnerOnlyOwed})
	<-v.updated
	close(resume)
	select {
	case <-v.updated:
	case <-time.After(hangGuard):
		t.Fatal("refresh did not install")
	}
	got := v.snapshot()
	if got.pending.byRun["run-a"] == nil || !got.pending.byRun["run-a"].LaneReleased || got.pending.byRun["run-b"] == nil || !got.pending.byRun["run-b"].Optimistic {
		t.Fatal("sync install did not retain the detached run and its replacement")
	}
	if got.tip != "refreshed-tip" || !got.occupied["lane-1"] || got.storeRuns["lane-1"] != "run-b" || got.holds["item-a"] != ReasonOwnerOnlyOwed || got.holds["item-b"] != ReasonRunLive || !got.readyAt["item-a"].IsZero() || slices.Contains(got.freeLanes, "lane-1") {
		t.Fatalf("sync lost loop notes: occupied=%v owners=%v holds=%v free=%v", got.occupied, got.storeRuns, got.holds, got.freeLanes)
	}
}

func TestStartViewPublishesLoopRotation(t *testing.T) {
	_, l := passFixture(t, "item-a")
	ctx := context.Background()
	v := newStartView()
	initial := noteFixture(l.cfg.Now().UTC())
	v.current.Store(&initial)
	l.startView.Store(v)
	refusal := asLaneRefusal(unlandedLaneRefusal("lane-1", "head", "base"))
	if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
		t.Fatal(err)
	}
	if got := v.snapshot(); !slices.Equal(got.freeLanes, []string{"lane-2"}) || !got.laneFreeAt["lane-1"].IsZero() {
		t.Fatalf("refused lane remained available: %v", got.freeLanes)
	}
	if err := l.restoreLane(ctx, "lane-1"); err != nil {
		t.Fatal(err)
	}
	if got := v.snapshot(); !slices.Equal(got.freeLanes, initial.freeLanes) || got.occupied["lane-1"] {
		t.Fatalf("restored lane was not offered: %v", got.freeLanes)
	}
}

func TestReleaseWithNoLaneNameFreesRestoredAndUnleasedLanes(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	at := l.cfg.Now().UTC()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(ctx, tx, run.Request{RunID: "run-other", ItemID: "item-other", Backend: "fixture"}, "lane-2", at)
		return err
	})
	initial, err := readStartSnapshot(ctx, l.cfg.Store, at, l.currentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	initial.rotation = []string{"lane-2"}
	v := newStartView()
	v.current.Store(&initial)
	l.startView.Store(v)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ReleaseLaneInTx(ctx, tx, "lane-2", "reaped", at.Add(time.Second))
		return err
	})
	l.reconcileStartLanes(ctx, []string{"lane-1", "lane-2"}, at.Add(2*time.Second))
	if got := v.snapshot(); got.occupied["lane-2"] || !slices.Equal(got.freeLanes, []string{"lane-1", "lane-2"}) {
		t.Fatalf("reconciliation did not publish the restored and reaped lanes: %v", got.freeLanes)
	}
}
