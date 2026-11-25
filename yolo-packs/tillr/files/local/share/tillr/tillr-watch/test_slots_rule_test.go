//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func holdWatchTestFlock(t *testing.T, path string, how int) *os.File {
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

func TestDrainTestSlotsFreeWithWaitersRule(t *testing.T) {
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == "drain-test-slots-free-with-waiters" })
	if i < 0 {
		t.Fatal("missing drain-test-slots-free-with-waiters rule")
	}
	if reason := rules[i].UnevaluatedReason(); reason != "" {
		t.Fatalf("rule drain-test-slots-free-with-waiters is unevaluated: %s", reason)
	}

	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: rules[i : i+1], Clock: func() time.Time { return now }}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}

	lockDir := filepath.Join(t.TempDir(), "go-test-shards-locks")
	obs := drain.TestSlotsObserver{
		Store:   storePath,
		LockDir: lockDir,
		Limits:  drain.TestSlotsLimits{Ordinary: 2, Gate: 1, Proof: 1},
		Now:     func() time.Time { return now },
	}

	// While all slots are full and callers wait, the observer and rule stay quiet.
	slot0 := holdWatchTestFlock(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	slot1 := holdWatchTestFlock(t, filepath.Join(lockDir, "slot-1.lock"), syscall.LOCK_EX)
	gate0 := holdWatchTestFlock(t, filepath.Join(lockDir, "gate-0.lock"), syscall.LOCK_EX)
	proof0 := holdWatchTestFlock(t, filepath.Join(lockDir, "proof-0.lock"), syscall.LOCK_EX)

	waitTicketPath := filepath.Join(lockDir, "wait-rules", "v2", "wait-queue", "01-00000000000000000001-0000000001-00000000000000000001-00000001-sp.wait")
	waitTicket := holdWatchTestFlock(t, waitTicketPath, syscall.LOCK_SH)
	waitLock := holdWatchTestFlock(t, filepath.Join(lockDir, "wait-rules", "v2", "proof-waiting.lock"), syscall.LOCK_SH)

	for range 3 {
		now = now.Add(time.Second)
		if err := obs.Observe(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 0 {
			t.Fatalf("expected rule to stay quiet while slots are full, got %+v", notices)
		}
	}

	// A synthetic full-slots event payload also leaves the rule quiet.
	now = now.Add(time.Second)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.TestSlotsFreeWithWaitersKind,
			ObjectType: "slot-pool",
			ObjectID:   &lockDir,
			At:         now,
			Payload:    []byte(`{"lock_dir":"` + lockDir + `","free_slots":0,"total_slots":4,"eligible_free_slots":0,"waiters":1,"observations":2}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("expected rule to stay quiet on zero eligible free slots, got %+v / %v", notices, err)
	}

	// Freeing slots while live waiters remain across consecutive observations opens one episode.
	_ = slot0.Close()
	_ = proof0.Close()

	now = now.Add(time.Second)
	if err := obs.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("expected no alert after only one observation, got %+v / %v", notices, err)
	}

	now = now.Add(time.Second)
	if err := obs.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("want one foreground notice after consecutive observations, got %+v / %v", notices, err)
	}
	firstAlertID := notices[0].AlertID
	g := notices[0].Bundle.Guidance
	if notices[0].Bundle.Subject != lockDir ||
		!strings.Contains(g.Summary, "2/4 free") ||
		!strings.Contains(g.Summary, "1 waiters") ||
		!strings.Contains(strings.Join(g.Check, " "), "/proc/locks") {
		t.Fatalf("unexpected bundle guidance: %+v", notices[0].Bundle)
	}

	// Repeated events in the same episode keep the same alert rather than raising a second one.
	now = now.Add(time.Second)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.TestSlotsFreeWithWaitersKind,
			ObjectType: "slot-pool",
			ObjectID:   &lockDir,
			At:         now,
			Payload:    []byte(`{"lock_dir":"` + lockDir + `","free_slots":2,"total_slots":4,"eligible_free_slots":2,"waiters":3,"observations":3}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("want one foreground notice on repeat event, got %+v / %v", notices, err)
	}
	if notices[0].AlertID != firstAlertID {
		t.Fatalf("repeat event in same episode opened new alert: %s != %s", notices[0].AlertID, firstAlertID)
	}

	// Filling the slots again clears the episode.
	slot0 = holdWatchTestFlock(t, filepath.Join(lockDir, "slot-0.lock"), syscall.LOCK_EX)
	proof0 = holdWatchTestFlock(t, filepath.Join(lockDir, "proof-0.lock"), syscall.LOCK_EX)
	now = now.Add(time.Second)
	if err := obs.Observe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("expected episode to clear when slots filled, got %+v / %v", notices, err)
	}

	// A later episode opens a fresh alert once per episode.
	_ = slot0.Close()
	for range 2 {
		now = now.Add(time.Second)
		if err := obs.Observe(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("want one foreground notice for second episode, got %+v / %v", notices, err)
	}
	if notices[0].AlertID == firstAlertID {
		t.Fatalf("second episode reused first alert id %s", firstAlertID)
	}

	_ = slot1.Close()
	_ = gate0.Close()
	_ = proof0.Close()
	_ = waitTicket.Close()
	_ = waitLock.Close()
}
