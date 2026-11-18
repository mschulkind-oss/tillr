package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A pause request with no timeout_ms has no straggler bound: the adapter stops at its next
// gap, or the run ends on its own, and the runner kills nothing. The loop's build-migration
// pause is such a request (tillr-drain migrateBuildRuns). Before, a pi run whose harness
// never stopped at a gap was SIGKILLed mid-call when the 120 s bound elapsed (2026-11-18).
func TestAPauseWithNoBoundWaitsForTheGapOrTheRunsOwnEnd(t *testing.T) {
	dir := scratch(t)
	adapter := filepath.Join(dir, "run-stub")
	// The fixture sees the pause file, ignores it as a harness that cannot stop would, and
	// ends only when the test lets it.
	script := "#!/bin/sh\necho ready\nwhile [ ! -f \"$TILLR_PAUSE_REQUEST\" ]; do sleep .01; done\necho saw > saw-pause\nwhile [ ! -f finish ]; do sleep .01; done\necho done > finished\nexit 0\n"
	if err := os.WriteFile(adapter, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pause-kind"), []byte(run.PauseAtGap+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := filepath.Join(dir, "lanes")
	lane := filepath.Join(root, "lane")
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	bounds := make(chan time.Duration, 1)
	opts := run.ExecOptions{LaneRoot: root, BackendsDir: dir, Now: time.Now(), PauseAfter: func(d time.Duration) <-chan time.Time {
		bounds <- d
		return make(chan time.Time)
	}}
	done := make(chan error, 1)
	go func() {
		_, err := run.Exec(ctx, s, run.Request{RunID: "run", LaneID: "lane", Backend: "stub", Command: []string{adapter}, WorkingDir: "."}, opts)
		done <- err
	}()
	waitFor := func(name string) {
		t.Helper()
		for {
			if _, err := os.Stat(filepath.Join(lane, name)); err == nil {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("the run ended before %s: %v", name, err)
			case <-ctx.Done():
				t.Fatalf("no %s", name)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	for {
		log, _ := os.ReadFile(filepath.Join(lane, "run.log"))
		if strings.Contains(string(log), "ready") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("harness not ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	body, _ := json.Marshal(map[string]string{"tillr_build": "next-build"})
	id := "run"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: run.PauseRequestKind, ObjectType: "run", ObjectID: &id, Payload: body, At: time.Now().UTC()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	waitFor("saw-pause")
	if err := os.WriteFile(filepath.Join(lane, "finish"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lane, "finished")); err != nil {
		t.Fatalf("the harness was ended before its own end: %v", err)
	}
	select {
	case d := <-bounds:
		t.Fatalf("an unbounded pause started a %s straggler bound", d)
	default:
	}
}
