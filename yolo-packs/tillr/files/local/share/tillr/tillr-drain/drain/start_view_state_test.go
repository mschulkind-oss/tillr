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
