package heavyrun

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestHeavyProgressOnlyReusesPassOnExactTip(t *testing.T) {
	dir := scratchUnderDurable(t)
	p, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-one")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), HeavyProgressKey{}, p)
	for _, result := range []string{"pass", "fail", "unavailable", "stalled"} {
		if err := SaveHeavyCheck(ctx, HeavyCheck{CheckID: result, Result: result}); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-one")
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(context.Background(), HeavyProgressKey{}, restarted)
	for _, result := range []string{"pass", "fail", "unavailable", "stalled"} {
		_, ok := ReuseHeavyCheck(ctx, result)
		if ok != (result == "pass") {
			t.Fatalf("reused %s: %v", result, ok)
		}
	}
	next, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-two")
	if err != nil || len(next.Checks) != 0 {
		t.Fatalf("different tip reused: %+v %v", next, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy-progress.json"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHeavyProgress(context.Background(), dir, dir, "tip-one"); err == nil {
		t.Fatal("corrupt proof silently discarded")
	}
}

func TestHeavyAttributionDoesNotReuseTipProof(t *testing.T) {
	root, base := heavyRepo(t)
	dir := scratchUnderDurable(t)
	p := &HeavyProgress{TipSHA: base, Checks: map[string]HeavyCheck{"unit": {CheckID: "unit", Result: "pass"}}, dir: dir}
	calls := 0
	runner := heavyFixture(t, root, func(ctx context.Context, _ string, id string) ([]HeavyCheck, error) {
		if _, ok := ReuseHeavyCheck(ctx, id); ok {
			t.Fatal("attribution used saved tip proof")
		}
		calls++
		return []HeavyCheck{{CheckID: id, Result: "fail"}}, nil
	})
	red, err := runner.one(context.WithValue(context.Background(), HeavyProgressKey{}, p), base, "unit")
	if err != nil || !red || calls != 1 {
		t.Fatalf("red=%v calls=%d err=%v", red, calls, err)
	}
}

func readHeavyProgressFacts(t *testing.T, storePath string) []HeavyProgressFact {
	t.Helper()
	db, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	var facts []HeavyProgressFact
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		all, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, ev := range all {
			if ev.Kind != HeavyProgressKind {
				continue
			}
			var fact HeavyProgressFact
			if err := json.Unmarshal(ev.Payload, &fact); err != nil {
				return err
			}
			facts = append(facts, fact)
		}
		return nil
	}); err != nil {
		t.Fatalf("read %s events: %v", HeavyProgressKind, err)
	}
	return facts
}

func TestHeavyLogProgressTracker(t *testing.T) {
	storePath := filepath.Join(scratchUnderDurable(t), "state.db")
	db, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	logDir := filepath.Join(scratchUnderDurable(t), "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	baseLog := filepath.Join(logDir, "00-retry-check.log")
	if err := os.WriteFile(baseLog, []byte(strings.Repeat("x", 500)), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, sp := (HeavyRunner{Store: storePath}).StartStoreProgress(context.Background(), "tip-retry")
	tracker := newHeavyLogProgressTracker(ctx, "retry-check", func() map[string]int64 {
		return readHeavyCheckLogSizes(logDir, "retry-check")
	})

	// First sample establishes baseline for baseLog (500 bytes); second flat sample does not emit.
	if tracker.sample() {
		t.Fatal("initial baseline sample reported growth")
	}
	if tracker.sample() {
		t.Fatal("flat sample reported growth")
	}

	// Truncating baseLog (restarted run) must not emit growth.
	if err := os.WriteFile(baseLog, []byte("header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tracker.sample() {
		t.Fatal("truncation reported growth")
	}

	// Truncating to empty resets baseline without emitting; rewriting header sets baseline.
	if err := os.WriteFile(baseLog, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if tracker.sample() {
		t.Fatal("zero-size log reported growth")
	}
	if err := os.WriteFile(baseLog, []byte("header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tracker.sample() {
		t.Fatal("header after zero-size log reported growth")
	}

	// An unrelated check's log growing must not emit progress for retry-check.
	otherLog := filepath.Join(logDir, "00-other-check.log")
	if err := os.WriteFile(otherLog, []byte("header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tracker.sample() {
		t.Fatal("unrelated log creation reported growth")
	}
	if err := os.WriteFile(otherLog, []byte("header\nmore output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tracker.sample() {
		t.Fatal("unrelated log growth reported growth")
	}

	// Creating attempt-2 log establishes its baseline without emitting.
	attemptLog := filepath.Join(logDir, "00-retry-check.attempt-2.log")
	if err := os.WriteFile(attemptLog, []byte("header\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if tracker.sample() {
		t.Fatal("attempt-2 baseline reported growth")
	}
	if got := readHeavyProgressFacts(t, storePath); len(got) != 1 {
		t.Fatalf("expected only started before attempt-2 grew, got %+v", got)
	}

	// Growing attempt-2 log emits one running event without incrementing WholeStepCount.
	if err := os.WriteFile(attemptLog, []byte("header\n..."), 0o644); err != nil {
		t.Fatal(err)
	}
	if !tracker.sample() {
		t.Fatal("attempt-2 growth did not report growth")
	}
	// Flat sample after growth emits nothing.
	if tracker.sample() {
		t.Fatal("flat sample after growth reported growth")
	}
	if got := sp.WholeStepCount(); got != 0 {
		t.Fatalf("WholeStepCount = %d, want 0", got)
	}
	sp.Finish("green")

	got := readHeavyProgressFacts(t, storePath)
	want := []HeavyProgressFact{
		{TipSHA: "tip-retry", Phase: "started"},
		{TipSHA: "tip-retry", Phase: "progress", CheckID: "retry-check", Outcome: "running"},
		{TipSHA: "tip-retry", Phase: "ended", Outcome: "green"},
	}
	if len(got) != len(want) {
		t.Fatalf("facts = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fact[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
