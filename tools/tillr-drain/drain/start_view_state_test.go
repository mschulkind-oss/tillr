package drain

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestClonePendingHistoryStateKeepsDiscardedRuns(t *testing.T) {
	s := newPendingHistoryState()
	s.discarded["run-x"] = true
	s.works["item-a"] = &pendingWork{Ready: true, Candidate: &Candidate{BaseSHA: "original"}}
	s.byRun["run-x"] = s.works["item-a"]
	got := clonePendingHistoryState(s)
	if !got.discarded["run-x"] {
		t.Fatalf("clone lost discarded run: %v", got.discarded)
	}
	got.discarded["run-y"] = true
	runID := "run-x"
	if err := foldPendingHistory(nil, 42, got, []event.Event{
		{Kind: candidateAssignedKind, ObjectID: &runID, Payload: []byte(`{"candidate":{"base_sha":"assigned"}}`)},
		{Kind: dirtyHeldKind, ObjectID: &runID, Payload: []byte(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if s.discarded["run-y"] || s.works["item-a"].Candidate.BaseSHA != "original" ||
		got.works["item-a"].Candidate.BaseSHA != "assigned" || got.works["item-a"].DirtyHold != nil {
		t.Fatal("assignment and discarded-hold replay did not stay private to the clone")
	}
}

func TestSyncRefreshesInputDecisionsOnARecordedInput(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx, at := context.Background(), l.cfg.Now()
	var sync startStoreSync
	t.Cleanup(sync.close)
	refresh := func(previous startSnapshot) startSnapshot {
		t.Helper()
		next, err := sync.syncStore(ctx, f.storePath, at, l.currentPolicy(), previous, nil)
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	before := refresh(startSnapshot{})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, inputRecordedKind, "run-input-1", Decision{ItemID: "item-a", Reason: "recorded-input"}, at)
	})
	after := refresh(before)
	if got := after.inputDecisions["run-input-1"]; got.Reason != "recorded-input" || after.pending != before.pending || before.inputDecisions["run-input-1"].Reason != "" {
		t.Fatalf("recording inputs did not refresh just the relevant cached inputs: %+v", got)
	}
	// An unhandled kind must not mask a change this classifier cannot explain.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='m' WHERE item_id='item-a'`); err != nil {
			return err
		}
		_, err := tx.Append(event.Event{Kind: "run.observation-error", At: at, Payload: []byte(`{}`)})
		return err
	})
	if fallback := refresh(after); fallback.pending == after.pending || fallback.itemsByID["item-a"].Rank != "m" {
		t.Fatal("unhandled change did not reseed the view")
	}
}

func TestStartStoreSyncRefreshesLaneAndWindowExpiry(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx, at := context.Background(), l.cfg.Now()
	until := at.Add(5 * time.Second)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO lanes (lane_id,state,run_id,leased_until) VALUES ('lane-1','leased','foreign',?)`, until.UnixMilli()); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('fixture','quota',1,'closed',?,'quota-run')`, until.UnixMilli())
		return err
	})
	var sync startStoreSync
	t.Cleanup(sync.close)
	before, err := sync.syncStore(ctx, f.storePath, at, l.currentPolicy(), startSnapshot{}, nil)
	if err != nil || !before.occupied["lane-1"] || !before.shut["fixture"] {
		t.Fatalf("fixture did not seed live facts: %v / %v / %v", err, before.occupied, before.shut)
	}
	after, err := sync.syncStore(ctx, f.storePath, until, l.currentPolicy(), before, nil)
	if err != nil || after.occupied["lane-1"] || after.shut["fixture"] || after.pending != before.pending ||
		after.nextLeaseExpiry != 0 || after.nextWindowExpiry != 0 || !before.occupied["lane-1"] || !before.shut["fixture"] {
		t.Fatalf("expiry did not update only the private lane/window facts: %v / %v / %v", err, after.occupied, after.shut)
	}
}

func TestStartViewSeedMatchesStore(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx := context.Background()
	at := l.cfg.Now()
	p := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), at)
	if err != nil || !ok {
		t.Fatalf("claim fixture: %v / %v", ok, err)
	}
	lane, id := p.decision.LaneID, p.decision.ItemID
	leaseEnd, windowEnd := at.Add(time.Minute), at.Add(2*time.Minute)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`UPDATE lanes SET leased_until=? WHERE lane_id=?`, []any{leaseEnd.UnixMilli(), lane}},
			{`INSERT INTO lanes (lane_id,state,run_id,leased_until) VALUES ('expired','leased','old',?)`, []any{at.UnixMilli()}},
			{`INSERT INTO backend_windows VALUES ('fixture','quota',1,'closed',?,'quota-run'), ('expired','quota',1,'closed',?,'old-run')`, []any{windowEnd.UnixMilli(), at.UnixMilli()}},
			{`INSERT INTO item_edges (item_id,blocked_by) VALUES ('item-b','item-a')`, nil},
			{`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at) VALUES ('run-item-a-a3',?,?,'fixture','exited',1,2)`, []any{id, lane}},
			{`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES (?,'owner','Owner input','not-done','owner-only')`, []any{id}},
		} {
			if _, err := tx.Exec(q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	got, err := readStartSnapshot(ctx, f.storePath, at, l.currentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		works, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		lanes, err := readLanes(r, at)
		if err != nil {
			return err
		}
		shut, err := readQuotaShutBackends(r, at, l.currentPolicy())
		if err != nil {
			return err
		}
		if works[id] == nil || !reflect.DeepEqual(got.pending.works, works) || !reflect.DeepEqual(got.pending.byRun, byRun) || got.attempts[id] != 4 {
			t.Fatalf("pending work/attempt: %v / %v (store works %v)", got.pending, got.attempts, works)
		}
		if !lanes[lane] || lanes["expired"] || !reflect.DeepEqual(got.occupied, lanes) || got.nextLeaseExpiry != leaseEnd.UnixMilli() {
			t.Fatalf("lanes %v, store %v, expiry %v", got.occupied, lanes, got.nextLeaseExpiry)
		}
		if !shut["fixture"] || shut["expired"] || !reflect.DeepEqual(got.shut, shut) || got.nextWindowExpiry != windowEnd.UnixMilli() {
			t.Fatalf("shut %v, store %v, expiry %v", got.shut, shut, got.nextWindowExpiry)
		}
		if !reflect.DeepEqual(got.reverseEdges, map[string][]string{"item-a": {"item-b"}}) {
			t.Fatalf("reverse edges: %v", got.reverseEdges)
		}
		row, found, err := readItemByID(r, id)
		if err != nil || !found || row.OwnerOwed == nil || row.OwnerOwed.CriterionID != "owner" || !reflect.DeepEqual(row, got.itemsByID[id]) {
			t.Fatalf("single item %v / %v / %v does not match seed %v", row, found, err, got.itemsByID[id])
		}
		_, found, err = readItemByID(r, "missing")
		if err != nil || found {
			t.Fatalf("missing item: found=%v err=%v", found, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, err := readStartSnapshot(ctx, f.storePath, windowEnd, l.currentPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if after.occupied[lane] || after.shut["fixture"] || after.nextLeaseExpiry != 0 || after.nextWindowExpiry != 0 {
		t.Fatalf("expired facts in reseed: %+v", after)
	}
	if !got.occupied[lane] || !got.shut["fixture"] {
		t.Fatal("reseed changed the previous snapshot")
	}
}

func TestStartStoreSyncReleasesLaneOnCancelStaging(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	ctx, at := context.Background(), l.cfg.Now()
	p := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), at)
	if err != nil || !ok {
		t.Fatalf("claim fixture: %v / %v", ok, err)
	}
	lane := p.decision.LaneID
	tick := l.startViewPass()
	var sync startStoreSync
	t.Cleanup(sync.close)
	refresh := func(previous startSnapshot) startSnapshot {
		t.Helper()
		next, err := sync.syncStore(ctx, f.storePath, at, l.currentPolicy(), previous, func(r *store.Reader, s *startSnapshot, touched map[string]bool) (func() error, error) {
			return s.prepareRepair(ctx, r, at, l.currentPolicy(), tick, &VantageQuestions{Root: f.root}, previous, touched)
		})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	before := refresh(startSnapshot{})
	if !before.occupied[lane] || !before.storeLanes[lane] {
		t.Fatalf("claimed lane %s not occupied before cancelStaging: occupied=%v storeLanes=%v", lane, before.occupied, before.storeLanes)
	}
	if err := l.cancelStaging(ctx, p.decision); err != nil {
		t.Fatalf("cancelStaging: %v", err)
	}
	after := refresh(before)
	if after.occupied[lane] || after.storeLanes[lane] {
		t.Fatalf("cancelStaging left lane %s occupied: occupied=%v storeLanes=%v freeLanes=%v", lane, after.occupied, after.storeLanes, after.freeLanes)
	}
	if start, ok := after.nextFreshStart(); !ok || start.decision.LaneID != lane {
		t.Fatalf("nextFreshStart after cancelStaging = (%v, %+v), want fresh start on %s", ok, start.decision, lane)
	}
}

func TestStartStoreSyncSkipsObservationEventsWithoutReseed(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx, at := context.Background(), l.cfg.Now()
	var sync startStoreSync
	t.Cleanup(sync.close)
	rebuilt := false
	refresh := func(previous startSnapshot) startSnapshot {
		t.Helper()
		rebuilt = false
		next, err := sync.syncStore(ctx, f.storePath, at, l.currentPolicy(), previous, func(_ *store.Reader, _ *startSnapshot, _ map[string]bool) (func() error, error) {
			rebuilt = true
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}
	before := refresh(startSnapshot{})
	if !rebuilt {
		t.Fatal("initial seed did not run rebuild")
	}

	observations := []struct {
		kind       string
		objectType string
		objectID   string
	}{
		{"run.child-session", "run", "run-1"},
		{"run.children-recorded", "run", "run-1"},
		{GateStartedKind, "run", "run-1"},
		{GateStoppedKind, "run", "run-1"},
		{GatePlanKind, "run", "run-1"},
		{FastGateTimingKind, "run", "run-1"},
		{HeavyProgressKind, "trunk", "main"},
		{CheckpointNotRestartedKind, "session", "sess-1"},
		{LoopPassKind, "session", "sess-1"},
		{ProcessCPUKind, "session", "sess-1"},
		{StoreBusyKind, "session", "sess-1"},
		{dispatchDeclinedKind, "item", "item-a"},
		{"watch.alert.opened", "alert", "alrt-1"},
		{"watch.alert.resolved", "alert", "alrt-1"},
	}
	prev := before
	for _, obs := range observations {
		id := obs.objectID
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       obs.kind,
				ObjectType: obs.objectType,
				ObjectID:   &id,
				Payload:    []byte(`{}`),
				At:         at,
			})
			return err
		})
		next := refresh(prev)
		if next.pending != prev.pending {
			t.Fatalf("%s (%s) triggered a full reseed", obs.kind, obs.objectType)
		}
		if rebuilt {
			t.Fatalf("%s (%s) triggered a proposal rebuild", obs.kind, obs.objectType)
		}
		if next.lastEventSeq <= prev.lastEventSeq {
			t.Fatalf("%s (%s) did not advance lastEventSeq: %d <= %d", obs.kind, obs.objectType, next.lastEventSeq, prev.lastEventSeq)
		}
		prev = next
	}

	// An observation kind carrying an unexpected object_type still reseeds.
	wrongID := "item-a"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       HeavyProgressKind,
			ObjectType: "item",
			ObjectID:   &wrongID,
			Payload:    []byte(`{}`),
			At:         at,
		})
		return err
	})
	fallback := refresh(prev)
	if fallback.pending == prev.pending || !rebuilt {
		t.Fatal("observation event with unexpected object_type did not reseed")
	}
}
