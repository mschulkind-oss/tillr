package drain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// A failed hold rolls back the whole batch, not just the last write.
func TestWriteHoldsIsOneTransaction(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()
	marker := func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET title = 'held' WHERE item_id = 'item-a'`)
		return err
	}
	title := func() string {
		var title string
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT title FROM items WHERE item_id = 'item-a'`).Scan(&title)
		})
		return title
	}
	original := title()
	boom := errors.New("boom")
	if err := l.writeHolds(ctx, []func(*store.Tx) error{marker, func(*store.Tx) error { return boom }}); !errors.Is(err, boom) {
		t.Fatalf("writeHolds = %v, want %v", err, boom)
	}
	if got := title(); got != original {
		t.Fatalf("the first write survived rollback: title = %q, want %q", got, original)
	}
	if err := l.writeHolds(ctx, []func(*store.Tx) error{marker}); err != nil {
		t.Fatal(err)
	}
	if got := title(); got != "held" {
		t.Fatalf("title = %q, want the batch to commit", got)
	}
	if err := l.writeHolds(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPendingOnlyPlansCompletionButNotFreshStart(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	initial := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(ctx, initial, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim pending item: applied = %v, err = %v", ok, err)
	}
	d := initial.decision
	at := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID,
			Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
			ArtifactRefs: []run.ArtifactRef{},
		})
	})
	var generation string
	pending, _, err := l.passPendingPlansGeneration(ctx, at, &generation)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		plans []plannedApplication
		want  map[string]string
	}{
		{"pending only", pending, map[string]string{"item-a": ActionSettle}},
		{"default", mustPlan(t, l), map[string]string{"item-a": ActionSettle, "item-b": ActionDispatch}},
	} {
		got := make(map[string]string)
		for _, p := range tc.plans {
			got[p.decision.ItemID] = p.decision.Action
		}
		if len(tc.plans) != len(tc.want) || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: planned %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestNoteLaneWriteCountsTheLane(t *testing.T) {
	var l Loop
	l.noteLaneWrite("lane-1")
	l.noteLaneWrite("lane-1")
	l.noteLaneWrite("")
	if got := l.laneChanges; !reflect.DeepEqual(got, map[string]int{"lane-1": 2}) {
		t.Fatalf("laneChanges = %v, want only lane-1 counted twice", got)
	}
}

func TestUnmovedLanesDropsLanesThatMoved(t *testing.T) {
	got := unmovedLanes([]string{"lane-1", "lane-2", "lane-3"}, map[string]int{"lane-1": 4, "lane-2": 7, "lane-3": 0}, map[string]int{"lane-1": 4, "lane-2": 9, "lane-3": 0})
	if want := []string{"lane-1", "lane-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unmovedLanes = %v, want %v", got, want)
	}
}

func TestLaneSnapshotOverlapsStaging(t *testing.T) {
	boom := errors.New("stage failed")
	entered, release := make(chan struct{}), make(chan struct{})
	l := Loop{cfg: LoopConfig{Lanes: []string{"moving", "untouched"}, Stage: stagerFunc(func(context.Context, StageSpec) error {
		close(entered)
		<-release
		return boom
	})}}
	watch := l.idleLaneEpochs()
	result := make(chan error, 1)
	go func() { result <- l.stageLane(context.Background(), StageSpec{LaneID: "moving"}) }()
	<-entered
	during := l.idleLaneEpochs()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			l.idleLaneEpochs()
		}
	}()
	close(release)
	if err := <-result; !errors.Is(err, boom) {
		t.Errorf("stage error = %v, want %v", err, boom)
	}
	for range 100 {
		l.noteLaneWrite("moving")
	}
	wg.Wait()
	if _, ok := during["moving"]; ok {
		t.Errorf("snapshot during staging still watches moving lane: %v", during)
	}
	after := l.idleLaneEpochs()
	if after["moving"] != 102 {
		t.Errorf("epoch after failed staging and 100 notes = %d, want 102", after["moving"])
	}
	if got := unmovedLanes(l.cfg.Lanes, watch, after); !reflect.DeepEqual(got, []string{"untouched"}) {
		t.Errorf("still watching %v, want only untouched", got)
	}
}

func TestAsyncBracketStillChecksUntouchedSibling(t *testing.T) {
	for _, escape := range []bool{false, true} {
		f := newRefusalLoopFixtureWithLanes(t, 3)
		l := f.loop(t, &recordingSteps{}, stagerFunc(func(_ context.Context, spec StageSpec) error {
			writeRepoFile(t, f.laneRoot+"/"+spec.LaneID+"/"+WorktreeDir, "authorized.txt", "staging")
			return nil
		}))
		l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
		ctx := context.Background()
		watched := []string{"lane-2", "lane-3"}
		epochs := l.idleLaneEpochs()
		before, err := l.snapshotIdleSiblings(ctx, l.lanePath("lane-1"), watched, epochs)
		if err != nil {
			t.Fatal(err)
		}
		if err := l.stageLane(ctx, StageSpec{LaneID: "lane-2"}); err != nil {
			t.Fatal(err)
		}
		if escape {
			writeRepoFile(t, l.lanePath("lane-3"), "escape.txt", "outside target")
		}
		var result LoopResult
		d := Decision{LaneID: "lane-1", RunRequest: &run.Request{RunID: "run-sibling-bracket", ItemID: "item-sibling-bracket"}}
		if _, err := l.closeAsyncBracket(ctx, d, l.cfg.Now(), before, &result, watched, epochs); err != nil {
			t.Fatal(err)
		}
		if (result.StopStep == "boundary check") != escape || (escape && !strings.Contains(result.Detail, "escape.txt")) {
			t.Fatalf("escape=%v, boundary step=%q detail=%q", escape, result.StopStep, result.Detail)
		}
	}
}

func proposeFixture(t *testing.T, l *Loop, at time.Time, previous startSnapshot) startSnapshot {
	t.Helper()
	ctx := context.Background()
	s, err := readProposedStarts(ctx, l.cfg.Store, at, l.currentPolicy(), l.startViewPass(), &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}, previous)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStartViewCutsOrdinaryItemsFromProvedGreen(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeForkFile(t, f.root+"/second.txt", "second\n")
	runGit(t, f.root, "add", "second.txt")
	runGit(t, f.root, "commit", "-qm", "second")
	tip := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", GreenRef, base)
	at := l.cfg.Now()
	s := proposeFixture(t, l, at, startSnapshot{})
	d, ok := s.nextStart()
	legacy := decisionFor(t, l, "item-a")
	if !ok || d.decision.ItemID != legacy.decision.ItemID || d.decision.LaneID != legacy.decision.LaneID || s.tip != tip || s.green != base || legacy.start == nil || legacy.start.base != base {
		t.Fatalf("proposal: %+v, ok=%v; refs %s %s; legacy: %+v", d, ok, s.tip, s.green, legacy)
	}
	if d.version == "" || !s.readyAt["item-a"].Equal(at) || !s.laneFreeAt[d.decision.LaneID].Equal(at) {
		t.Fatalf("missing version or ready/free instants: %+v, %v, %v", d, s.readyAt, s.laneFreeAt)
	}
	runGit(t, f.root, "update-ref", GreenRef, tip)
	next := proposeFixture(t, l, at.Add(time.Second), s)
	if next.green != tip || !next.readyAt["item-a"].Equal(at) || !next.laneFreeAt[d.decision.LaneID].Equal(at) || s.green != base {
		t.Fatalf("refresh lost stable instants or mutated old refs: %s %v %v", next.green, next.readyAt, next.laneFreeAt)
	}
}

func TestStartViewBlockedAndPinnedParity(t *testing.T) {
	f, l := passFixture(t, "item-blocked", "item-desk", "item-express")
	l.cfg.Lanes = []string{"lane-1", "express-1"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
	l.cfg.Comments = &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	setDesignFence(t, f, "item-desk", "docs/design/example.md")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET allowed_paths='[]' WHERE item_id='item-blocked'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET express=1 WHERE item_id='item-express'`)
		return err
	})
	at := l.cfg.Now()
	s := proposeFixture(t, l, at, startSnapshot{})
	legacy := decisionFor(t, l, "item-blocked").decision
	if legacy.Action != ActionHold || s.holds[legacy.ItemID] != legacy.Reason {
		t.Fatalf("holds %v; legacy %+v", s.holds, legacy)
	}
	check := func(s startSnapshot, id, lane string) {
		t.Helper()
		p, ok := s.nextStart()
		d := decisionFor(t, l, id).decision
		if !ok || p.decision.ItemID != id || p.decision.LaneID != lane || d.Action != p.decision.Action || d.LaneID != lane || p.decision.RunRequest.LaneID != lane {
			t.Fatalf("next %+v %v; legacy %+v; want %s on %s", p, ok, d, id, lane)
		}
	}
	check(s, "item-desk", "desk-example")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id) VALUES ('desk-example','leased','foreign')`)
		return err
	})
	next := proposeFixture(t, l, at.Add(time.Second), s)
	legacy = decisionFor(t, l, "item-desk").decision
	if legacy.Reason != ReasonDocumentDeskBusy || next.holds[legacy.ItemID] != legacy.Reason {
		t.Fatalf("pinned item escaped its occupied desk: %v; legacy %+v", next.holds, legacy)
	}
	check(next, "item-express", "express-1")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id) VALUES ('express-1','leased','foreign-e'), ('lane-1','leased','foreign-p')`)
		return err
	})
	busy := proposeFixture(t, l, at.Add(2*time.Second), next)
	if _, ok := busy.nextStart(); ok || !busy.readyAt["item-express"].Equal(at) || len(busy.laneFreeAt) != 0 {
		t.Fatalf("expected no available lane but unchanged item readiness: %v %v", busy.readyAt, busy.laneFreeAt)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state='idle', run_id=NULL WHERE lane_id='lane-1'`)
		return err
	})
	fallback := proposeFixture(t, l, at.Add(3*time.Second), busy)
	check(fallback, "item-express", "lane-1")
	if !fallback.laneFreeAt["lane-1"].Equal(at.Add(3 * time.Second)) {
		t.Fatalf("newly released lane has old free instant: %v", fallback.laneFreeAt)
	}
	ctx := context.Background()
	if err := l.takeLaneOutOfRotation(ctx, laneRefusalf("lane-1", ReasonLaneChildren, "fixture", "fixture refusal").(*LaneRefusalError)); err != nil {
		t.Fatal(err)
	}
	refused := proposeFixture(t, l, at.Add(4*time.Second), fallback)
	if _, ok := refused.nextStart(); ok {
		t.Fatal("proposed a lane out of rotation")
	}
	if err := l.restoreLane(ctx, "lane-1"); err != nil {
		t.Fatal(err)
	}
	check(proposeFixture(t, l, at.Add(5*time.Second), refused), "item-express", "lane-1")
}

func proposalFollower(t *testing.T, l *Loop) func(time.Time) startSnapshot {
	t.Helper()
	ctx := context.Background()
	var follower startStoreSync
	t.Cleanup(follower.close)
	var previous startSnapshot
	tick, policy := l.startViewPass(), l.currentPolicy()
	questions := &VantageQuestions{Root: l.cfg.Root}
	return func(at time.Time) startSnapshot {
		t.Helper()
		next, err := follower.syncStore(ctx, l.cfg.Store, at, policy, previous, func(r *store.Reader, next *startSnapshot, touched map[string]bool) error {
			return next.repairEntries(ctx, r, at, policy, tick, questions, previous, touched)
		})
		if err != nil {
			t.Fatal(err)
		}
		previous = next
		return next
	}
}

func TestSyncReadsOnlyWhatItsInputsChanged(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c")
	at := l.cfg.Now()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item_edges VALUES ('item-b','item-a')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('other','weekly',1,'closed',?,'refused')`, at.Add(time.Minute).UnixMilli())
		return err
	})
	refresh := proposalFollower(t, l)
	seed := refresh(at)
	untouched := seed.entries["item-c"].decision.RunRequest
	if untouched == nil || seed.holds["item-b"] == "" {
		t.Fatalf("invalid fixture: %+v", seed.holds)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-a'`); err != nil {
			return err
		}
		// Only the blocker has an event. Find the dependent by its reverse edge.
		id := "item-a"
		_, err := tx.Append(event.Event{Kind: "item.update", ObjectType: "item", ObjectID: &id, At: at, Payload: []byte(`{"state":"landed"}`)})
		return err
	})
	changed := refresh(at.Add(time.Second))
	if changed.entries["item-b"].decision.RunRequest == nil || changed.entries["item-c"].decision.RunRequest != untouched || changed.pending != seed.pending || changed.contentVersions["item-b"] == seed.contentVersions["item-b"] || seed.holds["item-b"] == "" {
		t.Fatalf("dependency change did not repair only affected entries: %+v", changed.holds)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE backend_windows SET reset_at=? WHERE backend='other'`, at.Add(4*time.Second).UnixMilli())
		return err
	})
	if same := refresh(at.Add(2 * time.Second)); !same.shut["other"] || same.entries["item-c"].decision.RunRequest != untouched || same.pending != seed.pending {
		t.Fatal("an already closed backend reopened early or caused an unnecessary rebuild")
	}
	if reopened := refresh(at.Add(4 * time.Second)); reopened.shut["other"] || reopened.entries["item-c"].decision.RunRequest == untouched {
		t.Fatal("reopening a backend did not rebuild the entries")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='[]' WHERE item_id='item-c'`)
		return err
	})
	if raw := refresh(at.Add(5 * time.Second)); raw.entries["item-c"].decision.RunRequest != nil || raw.pending == seed.pending {
		t.Fatal("raw item edit did not trigger reseeding")
	}
}

func TestNextStartDoesNotWaitOnASyncInItsRead(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	tick, policy := l.startViewPass(), l.currentPolicy()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var follower startStoreSync
	defer follower.close()
	at := l.cfg.Now()
	seed := proposeFixture(t, l, at, startSnapshot{})
	view := newStartView()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id) VALUES ('lane-1','leased','foreign')`)
		return err
	})
	insideRead, release := make(chan struct{}), make(chan struct{})
	wakes := make(testStartWatcher, 1)
	stop := view.watch(ctx, time.Hour, func(context.Context) startWatcher { return wakes }, func(ctx context.Context, change startWatchChange) (startSnapshot, error) {
		if change == 0 {
			return seed, nil
		}
		return follower.syncStoreWithRefresh(ctx, f.storePath, at, policy, view.snapshot(), true, func(r *store.Reader, next *startSnapshot, touched map[string]bool) error {
			close(insideRead)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
			}
			return next.repairEntries(ctx, r, at, policy, tick, &VantageQuestions{Root: f.root}, seed, touched)
		})
	})
	defer stop()
	awaitStartViewSignal(t, view.updated)
	wakes <- startWatchExternal
	awaitStartViewSignal(t, insideRead)
	proposed := make(chan string, 1)
	go func() {
		entry, _ := view.snapshot().nextStart()
		proposed <- entry.decision.LaneID
	}()
	select {
	case lane := <-proposed:
		if lane != "lane-1" {
			t.Fatalf("proposal while refresh was paused: %q", lane)
		}
	case <-time.After(hangGuard):
		t.Fatal("nextStart waited for a paused store read")
	}
	close(release)
	awaitStartViewSignal(t, view.updated)
	if _, ok := view.snapshot().nextStart(); ok || !view.snapshot().occupied["lane-1"] || seed.occupied["lane-1"] {
		t.Fatal("refresh did not publish the private lane update")
	}
}

func TestLaneChangeOwesRebuildUntilSafetyInterval(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	at := l.cfg.Now()
	refresh := proposalFollower(t, l)
	request := refresh(at).entries["item-a"].decision.RunRequest
	if request == nil {
		t.Fatal("fixture did not propose a start")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id) VALUES ('lane-1','leased','foreign')`)
		return err
	})
	for _, dt := range []time.Duration{time.Second, startViewRefreshInterval / 2} {
		busy := refresh(at.Add(dt))
		if _, ok := busy.nextStart(); ok || !busy.occupied["lane-1"] || busy.entries["item-a"].decision.RunRequest != request {
			t.Fatal("lane facts were stale or the full rebuild ran early")
		}
	}
	if due := refresh(at.Add(startViewRefreshInterval)); due.entries["item-a"].decision.RunRequest == request {
		t.Fatal("the safety interval did not pay the owed rebuild")
	}
}
