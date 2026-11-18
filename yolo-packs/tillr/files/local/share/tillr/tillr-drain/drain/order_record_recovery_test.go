package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderRecordRebuiltStoreHandMoveIsNotAlreadyRecorded(t *testing.T) {
	f, l, s, now := orderFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/order.txt"), "# last_hand_move_event_seq: 1000\nitem-a\nitem-b\n")
	runGit(t, f.root, "add", "docs/order.txt")
	runGit(t, f.root, "commit", "-qm", "record from old store")
	moveOrder(t, f, *now, "item-b", "first", "cli")
	*now = now.Add(10 * time.Minute)
	orderPass(t, l)
	if s.gates != 1 {
		t.Fatalf("rebuilt store hand move skipped: gates=%d", s.gates)
	}
}
func TestOrderRecordDetachedPlaceIsLoopOwned(t *testing.T) {
	p := loopPlaces{root: "/lanes"}
	good := Worktree{Path: "/lanes/.order-record/run-order-record-1-2/worktree"}
	if !p.worktree(good) {
		t.Fatal("record checkout convicts a concurrent worker")
	}
	for _, bad := range []Worktree{
		{Path: good.Path, Branch: "refs/heads/escape"},
		{Path: "/elsewhere/.order-record/run-order-record-1-2/worktree"},
		{Path: "/lanes/.order-record/other/worktree"},
		{Path: good.Path + "/nested"},
	} {
		if p.worktree(bad) {
			t.Fatalf("excused wrong shape: %+v", bad)
		}
	}
}
func TestOrderRecordInterruptedGateRecoversTheSameCandidate(t *testing.T) {
	f, l, s, now := orderFixture(t)
	moveOrder(t, f, *now, "item-b", "first", "cli")
	*now = now.Add(10 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	var first Candidate
	s.onGate = func(c Candidate) { first = c; cancel() }
	err := l.recordOrder(ctx, runGit(t, f.root, "rev-parse", TrunkRef), &LoopResult{}, &PassTiming{})
	if err != context.Canceled {
		t.Fatalf("cancel: %v", err)
	}
	s.onGate = func(c Candidate) {
		if c.HeadSHA != first.HeadSHA || c.RunID != first.RunID {
			t.Fatal("replacement created another candidate")
		}
	}
	orderPass(t, l)
	if s.lands != 1 {
		t.Fatal("recovered candidate did not land")
	}
	if _, err := os.Stat(first.Worktree); !os.IsNotExist(err) {
		t.Fatalf("terminal worktree remains: %v", err)
	}
}
func TestOrderRecordForeignOwnerPresenceProtectsAndRecovers(t *testing.T) {
	f, l, s, now := orderFixture(t)
	moveOrder(t, f, *now, "item-b", "first", "cli")
	*now = now.Add(10 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	s.onGate = func(Candidate) { cancel() }
	if err := l.recordOrder(ctx, runGit(t, f.root, "rev-parse", TrunkRef), &LoopResult{}, &PassTiming{}); err != context.Canceled {
		t.Fatal(err)
	}
	var w *orderRecordWork
	fixtureWrite(t, f, func(tx *store.Tx) error { var err error; w, err = readOrderWork(tx.Reader()); return err })
	foreign, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	foreign.Domain += "-foreign"
	identity, err := processStoreIdentity(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := openLoopPresence(identity, *foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer presence.Close()
	w.Owner = foreign
	if err := l.saveOrderWork(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	s.onGate = nil
	orderPass(t, l)
	if s.gates != 1 || s.lands != 0 {
		t.Fatal("stole live foreign owner's candidate")
	}
	if err := presence.Close(); err != nil {
		t.Fatal(err)
	}
	orderPass(t, l)
	if s.gates != 2 || s.lands != 1 {
		t.Fatal("dead foreign owner kept candidate in flight")
	}
}

func TestOrderRecordCleanupCompletesBeforeNextClaim(t *testing.T) {
	f, l, s, now := orderFixture(t)
	moveOrder(t, f, *now, "item-b", "first", "cli")
	*now = now.Add(10 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	s.onGate = func(Candidate) { cancel() }
	if err := l.recordOrder(ctx, runGit(t, f.root, "rev-parse", TrunkRef), &LoopResult{}, &PassTiming{}); err != context.Canceled {
		t.Fatal(err)
	}
	var old *orderRecordWork
	fixtureWrite(t, f, func(tx *store.Tx) error { var err error; old, err = readOrderWork(tx.Reader()); return err })
	// Simulate a process exit after the terminal transaction, before cleanup.
	if err := l.finishOrderWork(context.Background(), old, &PassTiming{TickID: "terminal"}, "order-record-refused", "gate: fail"); err != nil {
		t.Fatal(err)
	}
	moveOrder(t, f, *now, "item-a", "first", "cli")
	*now = now.Add(10 * time.Minute)
	s.onGate = nil
	orderPass(t, l)
	if s.gates != 1 {
		t.Fatal("new claim hid pending cleanup")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		latest, err := readOrderWork(tx.Reader())
		if err == nil && (latest.Candidate.RunID != old.Candidate.RunID || !latest.Cleaned) {
			t.Fatal("cleanup was not acknowledged before new claim")
		}
		return err
	})
	if _, err := os.Stat(old.Candidate.Worktree); !os.IsNotExist(err) {
		t.Fatal("old checkout remains")
	}
	orderPass(t, l)
	if s.gates != 2 {
		t.Fatal("cleaned terminal blocked next claim")
	}
	// A lagging cleanup observer must not overwrite a newer attempt's receipt.
	if err := l.cleanOrderWork(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		latest, err := readOrderWork(tx.Reader())
		if err == nil && latest.Candidate.RunID == old.Candidate.RunID {
			t.Fatal("stale cleanup overwrote latest candidate")
		}
		return err
	})
}

func TestOrderRecordFailedFinalizationRetainsRecoverablePhase(t *testing.T) {
	f, l, _, now := orderFixture(t)
	w := &orderRecordWork{Phase: "gated", Seq: 1, Candidate: Candidate{RunID: "run-order-record-1-2"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.finishOrderWork(ctx, w, &PassTiming{TickID: "test", DecisionCounts: map[string]int{}}, "order-record-refused", "gate: fail"); err == nil {
		t.Fatal("canceled terminal write succeeded")
	}
	if w.Phase != "gated" {
		t.Fatalf("failed terminal write changed recoverable phase: %s", w.Phase)
	}
	// The release persisted by the caller must retain that phase.
	if err := l.saveOrderWork(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var raw []byte
		if err := tx.QueryRow("SELECT payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1", orderRecordKind).Scan(&raw); err != nil {
			return err
		}
		var got orderRecordWork
		if err := json.Unmarshal(raw, &got); err != nil {
			return err
		}
		if got.Phase != "gated" {
			t.Fatal(got.Phase)
		}
		return nil
	})
	_ = now
}
