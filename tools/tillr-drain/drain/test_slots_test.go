//go:build linux

package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func holdFlockFile(t *testing.T, path string, how int) *os.File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("flock %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func readTestSlotEvents[T any](t *testing.T, storePath, kind string) []T {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	var out []T
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query("SELECT payload FROM events WHERE kind = ? ORDER BY seq", kind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var item T
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			out = append(out, item)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPassRecordsTestSlotsFreeWithWaitersAcrossConsecutiveObservations(t *testing.T) {
	f, l := passFixture(t)
	ctx := context.Background()
	lockDir := os.Getenv("GO_TEST_SHARDS_LOCK_DIR")
	if lockDir == "" {
		t.Fatal("missing isolated GO_TEST_SHARDS_LOCK_DIR")
	}
	l.testSlots.Limits = TestSlotsLimits{Ordinary: 2, Gate: 1, Proof: 1}

	// Hold all 4 active slots (2 ordinary, 1 gate, 1 proof) while callers wait.
	slot0 := holdFlockFile(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	slot1 := holdFlockFile(t, filepath.Join(lockDir, "slot-1.lock"), syscall.LOCK_EX)
	gate0 := holdFlockFile(t, filepath.Join(lockDir, "gate-0.lock"), syscall.LOCK_EX)
	proof0 := holdFlockFile(t, filepath.Join(lockDir, "proof-0.lock"), syscall.LOCK_EX)

	v1GateTicket := filepath.Join(lockDir, "wait-queue", "02-00000000000000000001-0000000001-00000000000000000001-00000001-sg.wait")
	v1GateWait := filepath.Join(lockDir, "gate-waiting.lock")
	v2ProofTicket := filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "01-00000000000000000002-0000000002-00000000000000000001-00000001-sp.wait")
	v2ProofWait := filepath.Join(lockDir, "wait-rules", "v2", "proof-waiting.lock")

	wGateTicket := holdFlockFile(t, v1GateTicket, syscall.LOCK_SH)
	wGateLock := holdFlockFile(t, v1GateWait, syscall.LOCK_SH)
	wProofTicket := holdFlockFile(t, v2ProofTicket, syscall.LOCK_SH)
	wProofLock := holdFlockFile(t, v2ProofWait, syscall.LOCK_SH)

	for range 3 {
		if err := l.observeTestSlots(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("full slots recorded events: %+v", got)
	}

	// Ordinary-only waiters must stay quiet when ordinary slots are full even if
	// reserved gate/proof slots are free.
	_ = wGateTicket.Close()
	_ = os.Remove(v1GateTicket)
	_ = wGateLock.Close()
	_ = wProofTicket.Close()
	_ = os.Remove(v2ProofTicket)
	_ = wProofLock.Close()
	_ = gate0.Close()
	_ = proof0.Close()

	ordTicketPath := filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "03-00000000000000000003-0000000003-00000000000000000001-00000001-s.wait")
	ordTicket := holdFlockFile(t, ordTicketPath, syscall.LOCK_SH)
	for range 3 {
		if err := l.observeTestSlots(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("ordinary waiter with full ordinary slots recorded events: %+v", got)
	}
	_ = ordTicket.Close()
	_ = os.Remove(ordTicketPath)

	// A single observation of free slots + live waiters does not record an event
	// when the next observation sees full slots again.
	wGateTicket = holdFlockFile(t, v1GateTicket, syscall.LOCK_SH)
	wGateLock = holdFlockFile(t, v1GateWait, syscall.LOCK_SH)
	wProofTicket = holdFlockFile(t, v2ProofTicket, syscall.LOCK_SH)
	wProofLock = holdFlockFile(t, v2ProofWait, syscall.LOCK_SH)
	_ = slot0.Close() // slot-0, gate-0, proof-0 are free (3 of 4 free)

	if err := l.observeTestSlots(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("single non-consecutive observation recorded event: %+v", got)
	}

	slot0 = holdFlockFile(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	gate0 = holdFlockFile(t, filepath.Join(lockDir, "gate-0.lock"), syscall.LOCK_EX)
	proof0 = holdFlockFile(t, filepath.Join(lockDir, "proof-0.lock"), syscall.LOCK_EX)
	if err := l.observeTestSlots(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("reset streak still recorded event: %+v", got)
	}

	// Two consecutive Loop.pass runs with free slots and live waiters record one
	// event for the episode and do not spam on subsequent unchanged passes.
	_ = slot0.Close()
	_ = proof0.Close() // 2 of 4 slots free (slot-0 and proof-0), 2 waiters eligible for s/p/g
	if _, err := l.pass(ctx, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("first observation in streak recorded early: %+v", got)
	}
	if _, err := l.pass(ctx, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind)
	if len(got) != 1 {
		t.Fatalf("want 1 event after 2 consecutive observations, got %+v", got)
	}
	if got[0].LockDir != lockDir || got[0].FreeSlots != 2 || got[0].TotalSlots != 4 ||
		got[0].EligibleFreeSlots != 2 || got[0].Waiters != 2 || got[0].Observations != 2 {
		t.Fatalf("unexpected event payload: %+v", got[0])
	}

	// While the episode stands, another pass does not emit a duplicate event.
	if err := l.observeTestSlots(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 1 {
		t.Fatalf("unchanged standing episode emitted duplicate event: %+v", got)
	}

	// Releasing the waiters clears the standing episode, including across a loop restart.
	_ = wGateTicket.Close()
	_ = os.Remove(v1GateTicket)
	_ = wGateLock.Close()
	_ = wProofTicket.Close()
	_ = os.Remove(v2ProofTicket)
	_ = wProofLock.Close()
	_ = slot1.Close()
	_ = gate0.Close()

	_, restarted := passFixtureForExisting(t, f)
	restarted.testSlots.Limits = TestSlotsLimits{Ordinary: 2, Gate: 1, Proof: 1}
	if _, err := restarted.pass(ctx, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	cleared := readTestSlotEvents[TestSlotsClearedFact](t, f.storePath, TestSlotsClearedKind)
	if len(cleared) != 1 || cleared[0].LockDir != lockDir || cleared[0].Waiters != 0 {
		t.Fatalf("want 1 clear event after restart, got %+v", cleared)
	}
}

func TestObserveTestSlotsIgnoresLeftoverSlotsAboveActiveLimits(t *testing.T) {
	f, l := passFixture(t)
	ctx := context.Background()
	lockDir := os.Getenv("GO_TEST_SHARDS_LOCK_DIR")
	l.testSlots.Limits = TestSlotsLimits{Ordinary: 2, Gate: 1, Proof: 1}

	// Active slots 0..limit-1 are all held.
	_ = holdFlockFile(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "slot-1.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "gate-0.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "proof-0.lock"), syscall.LOCK_EX)

	// Leftover unlocked slot files from an older higher-limit configuration sit on disk.
	for i := 2; i < 6; i++ {
		path := filepath.Join(lockDir, fmt.Sprintf("slot-%d.lock", i))
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Unlocked leftover wait files also sit on disk alongside one live waiter.
	staleTicket := filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "03-00000000000000000001-0000000001-00000000000000000001-00000001-s.wait")
	if err := os.MkdirAll(filepath.Dir(staleTicket), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staleTicket, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	liveTicket := filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "01-00000000000000000002-0000000002-00000000000000000001-00000001-sp.wait")
	_ = holdFlockFile(t, liveTicket, syscall.LOCK_SH)

	for range 3 {
		if err := l.observeTestSlots(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("leftover unlocked slot files above limit falsely triggered event: %+v", got)
	}
}

func TestObserveTestSlotsSkipsNonLanding(t *testing.T) {
	f, l := passFixture(t)
	l.nonLanding = true
	lockDir := os.Getenv("GO_TEST_SHARDS_LOCK_DIR")
	l.testSlots.Limits = TestSlotsLimits{Ordinary: 1, Gate: 0, Proof: 0}
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, "slot-0.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	liveTicket := filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "03-00000000000000000001-0000000001-00000000000000000001-00000001-s.wait")
	_ = holdFlockFile(t, liveTicket, syscall.LOCK_SH)

	for range 2 {
		if err := l.observeTestSlots(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := readTestSlotEvents[TestSlotsFreeWithWaitersFact](t, f.storePath, TestSlotsFreeWithWaitersKind); len(got) != 0 {
		t.Fatalf("non-landing mode recorded test slot events: %+v", got)
	}
}

func TestReadFlockTableAtomicUnderConcurrentLockChurn(t *testing.T) {
	dir := t.TempDir()
	churnFiles := make([]string, 64)
	for i := range churnFiles {
		churnFiles[i] = filepath.Join(dir, fmt.Sprintf("churn-%d.lock", i))
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		held := make([]*os.File, len(churnFiles))
		defer func() {
			for _, f := range held {
				if f != nil {
					_ = f.Close()
				}
			}
		}()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for i, p := range churnFiles {
				f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
				if err == nil {
					_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
					held[i] = f
				}
			}
			for i, f := range held {
				if f != nil {
					_ = f.Close()
					held[i] = nil
				}
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	lockDir := filepath.Join(dir, "pool")
	_ = holdFlockFile(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "slot-1.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "03-00000000000000000001-0000000001-00000000000000000001-00000001-s.wait"), syscall.LOCK_SH)
	limits := TestSlotsLimits{Ordinary: 2, Gate: 0, Proof: 0}

	for iter := range 80 {
		snap, err := inspectTestSlots(lockDir, limits)
		if err != nil {
			t.Fatal(err)
		}
		if snap.EligibleFreeSlots != 0 {
			t.Fatalf("iter %d: /proc/locks churn falsely reported free slots while full: %+v", iter, snap)
		}
	}
}

func TestInspectTestSlotsWaitersStableUnderConcurrentLockChurn(t *testing.T) {
	dir := t.TempDir()
	churnFiles := make([]string, 64)
	for i := range churnFiles {
		churnFiles[i] = filepath.Join(dir, fmt.Sprintf("churn-%d.lock", i))
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		held := make([]*os.File, len(churnFiles))
		defer func() {
			for _, f := range held {
				if f != nil {
					_ = f.Close()
				}
			}
		}()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for i, p := range churnFiles {
				f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
				if err == nil {
					_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
					held[i] = f
				}
			}
			for i, f := range held {
				if f != nil {
					_ = f.Close()
					held[i] = nil
				}
			}
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()

	lockDir := filepath.Join(dir, "pool")
	slot0 := holdFlockFile(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "slot-1.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "gate-0.lock"), syscall.LOCK_EX)
	proof0 := holdFlockFile(t, filepath.Join(lockDir, "proof-0.lock"), syscall.LOCK_EX)
	_ = holdFlockFile(t, filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "01-00000000000000000001-0000000001-00000000000000000001-00000001-sp.wait"), syscall.LOCK_SH)
	_ = holdFlockFile(t, filepath.Join(lockDir, "wait-rules", "v2", "proof-waiting.lock"), syscall.LOCK_SH)
	_ = slot0.Close()
	_ = proof0.Close()
	limits := TestSlotsLimits{Ordinary: 2, Gate: 1, Proof: 1}

	for iter := range 200 {
		snap, err := inspectTestSlots(lockDir, limits)
		if err != nil {
			t.Fatal(err)
		}
		if snap.TotalSlots != 4 || snap.FreeSlots != 2 || snap.EligibleFreeSlots != 2 || snap.Waiters != 1 {
			t.Fatalf("iter %d: concurrent lock churn distorted snapshot: got %+v, want total=4 free=2 eligible=2 waiters=1", iter, snap)
		}
	}
}
