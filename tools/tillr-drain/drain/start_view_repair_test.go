package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

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
