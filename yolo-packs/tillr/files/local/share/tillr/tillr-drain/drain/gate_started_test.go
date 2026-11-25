package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestGateStartedEventIsRegistered(t *testing.T) {
	kind, err := event.Lookup(drain.GateStartedKind)
	if err != nil {
		t.Fatalf("event.Lookup(%q) failed: %v", drain.GateStartedKind, err)
	}
	if kind.Owner != drain.Utility {
		t.Fatalf("event.Lookup(%q) owner = %q, want %q", drain.GateStartedKind, kind.Owner, drain.Utility)
	}
}

func TestGateStartedWrittenOncePerGateStartAndVisibleToViewEvents(t *testing.T) {
	dir := scratch(t, "gate-started")
	root := filepath.Join(dir, "repo")
	worktree := filepath.Join(dir, "worktree")
	for _, p := range []string{root, worktree} {
		if err := os.MkdirAll(filepath.Join(p, ".git"), 0o755); err != nil {
			t.Fatalf("creating %s failed: %v", p, err)
		}
	}
	storePath := filepath.Join(dir, "board.sqlite3")
	if initStore, err := store.Open(context.Background(), storePath, true); err != nil {
		t.Fatalf("initializing store: %v", err)
	} else {
		_ = initStore.Close()
	}
	t.Setenv("GATE_ROOT", root)

	enteredFile := filepath.Join(dir, "gate.entered")
	releaseFile := filepath.Join(dir, "gate.release")
	blockingGate := stub(t, "blocking-gate", `#!/bin/sh
set -eu
run=; prev=
for a in "$@"; do
  if [ "$prev" = "--run" ]; then run=$a; fi
  prev=$a
done
printf '%s\n' "$run" > "$GATE_ENTERED"
while [ ! -f "$GATE_RELEASE" ]; do
  sleep 0.02
done
dir="$GATE_ROOT/.tillr/loop/$run"
mkdir -p "$dir"
payload='{"plan_id":"plan-1","registry_digest":"d1","base_sha":"b1","tree_sha":"t1","result":"pass","checks":[]}'
printf '{"record_version":1,"kind":"gate.plan","id":"plan-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-24T00:00:00Z","payload":%s}\n' "$payload" > "$dir/gate.plan.json"
printf '{"record_version":1,"kind":"gate.verdict","id":"verdict-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-24T00:00:00Z","payload":%s}\n' "$payload" > "$dir/gate.verdict.json"
exit 0
`)
	t.Setenv("GATE_ENTERED", enteredFile)
	t.Setenv("GATE_RELEASE", releaseFile)

	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	steps := &drain.ProcessSteps{
		Store:       storePath,
		Root:        root,
		Now:         func() time.Time { return now },
		GateProgram: blockingGate,
	}

	candidates := []drain.Candidate{
		{
			ItemID: "item-ordinary", LaneID: "lane-1", RunID: "run-ordinary-1",
			TickID: "tick-1", Worktree: worktree, BaseSHA: "aaaa1111", HeadSHA: "bbbb1111",
		},
		{
			ItemID: "item-express", LaneID: "express-1", RunID: "run-express-1",
			TickID: "tick-2", Worktree: worktree, BaseSHA: "aaaa2222", HeadSHA: "bbbb2222",
		},
		{
			ItemID: "fixer-main-1293971a3597", LaneID: "lane-2", RunID: "run-fixer-1",
			TickID: "tick-3", Worktree: worktree, BaseSHA: "aaaa3333", HeadSHA: "bbbb3333",
		},
	}

	runGateAndAssert := func(c drain.Candidate, wantCount int) {
		t.Helper()
		_ = os.Remove(enteredFile)
		_ = os.Remove(releaseFile)

		type gateRes struct {
			out drain.GateOutcome
			err error
		}
		done := make(chan gateRes, 1)
		go func() {
			out, err := steps.Gate(context.Background(), c)
			done <- gateRes{out: out, err: err}
		}()

		deadline := time.Now().Add(hangGuard)
		for {
			if _, err := os.Stat(enteredFile); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("gate for %s never entered", c.RunID)
			}
			time.Sleep(20 * time.Millisecond)
		}

		// While the gate is still running (blocked on releaseFile, before any verdict),
		// gate.started is already written to the store.
		var startedWhileRunning, verdictsWhileRunning int
		for {
			startedWhileRunning, verdictsWhileRunning = 0, 0
			for _, e := range eventsOf(t, storePath) {
				if e.ObjectID == nil || *e.ObjectID != c.RunID {
					continue
				}
				switch e.Kind {
				case drain.GateStartedKind:
					startedWhileRunning++
				case drain.GateVerdictKind:
					verdictsWhileRunning++
				}
			}
			if startedWhileRunning == wantCount || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if startedWhileRunning != wantCount {
			t.Fatalf("while gate running for %s: gate.started count = %d, want %d", c.RunID, startedWhileRunning, wantCount)
		}
		if wantCount == 1 && verdictsWhileRunning != 0 {
			t.Fatalf("while gate running for %s: gate.verdict count = %d, want 0", c.RunID, verdictsWhileRunning)
		}

		if err := os.WriteFile(releaseFile, []byte("go\n"), 0o600); err != nil {
			t.Fatalf("writing release file: %v", err)
		}
		res := <-done
		if res.err != nil {
			t.Fatalf("Gate(%s) failed: %v", c.RunID, res.err)
		}
		if res.out.Outcome != drain.GatePassed {
			t.Fatalf("Gate(%s) outcome = %q, want %q", c.RunID, res.out.Outcome, drain.GatePassed)
		}

		var startedSeqs []int64
		var planSeq, verdictSeq int64
		for _, e := range eventsOf(t, storePath) {
			if e.ObjectID == nil || *e.ObjectID != c.RunID {
				continue
			}
			switch e.Kind {
			case drain.GateStartedKind:
				if e.ObjectType != "run" {
					t.Fatalf("gate.started object_type = %q, want run", e.ObjectType)
				}
				p := eventPayload(t, e)
				if p["item_id"] != c.ItemID || p["run_id"] != c.RunID || p["lane_id"] != c.LaneID || p["base_sha"] != c.BaseSHA {
					t.Fatalf("gate.started payload = %v, want item_id=%q run_id=%q lane_id=%q base_sha=%q",
						p, c.ItemID, c.RunID, c.LaneID, c.BaseSHA)
				}
				startedSeqs = append(startedSeqs, e.Seq)
			case drain.GatePlanKind:
				planSeq = e.Seq
			case drain.GateVerdictKind:
				verdictSeq = e.Seq
			}
		}
		if len(startedSeqs) != wantCount {
			t.Fatalf("after gate finished for %s: gate.started count = %d, want %d", c.RunID, len(startedSeqs), wantCount)
		}
		if wantCount == 1 && (startedSeqs[0] >= planSeq || startedSeqs[0] >= verdictSeq) {
			t.Fatalf("gate.started seq %d should precede gate.plan (%d) and gate.verdict (%d)", startedSeqs[0], planSeq, verdictSeq)
		}
	}

	for _, c := range candidates {
		runGateAndAssert(c, 1)
	}
	// A second gate start for the same run writes a second gate.started event (once per gate start).
	runGateAndAssert(candidates[0], 2)

	// Verify the events are visible to `tillr view events`.
	raw, err := exec.Command(sibling(t, "view"), "events", "--store", storePath).Output()
	if err != nil {
		t.Fatalf("tillr-view events failed: %v\n%s", err, raw)
	}
	var env struct {
		Kind    string `json:"kind"`
		Payload struct {
			Events []struct {
				Seq        int64          `json:"seq"`
				Kind       string         `json:"kind"`
				ObjectType string         `json:"object_type"`
				ObjectID   *string        `json:"object_id"`
				Payload    map[string]any `json:"payload"`
			} `json:"events"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decoding view events output: %v\n%s", err, raw)
	}
	if env.Kind != "view.events" {
		t.Fatalf("view events kind = %q, want view.events", env.Kind)
	}
	seenByRun := map[string]int{}
	byRun := map[string]drain.Candidate{}
	for _, c := range candidates {
		byRun[c.RunID] = c
	}
	for _, ev := range env.Payload.Events {
		if ev.Kind != drain.GateStartedKind {
			continue
		}
		if ev.ObjectType != "run" || ev.ObjectID == nil {
			t.Fatalf("view events gate.started has object %q/%v, want run/<run_id>", ev.ObjectType, ev.ObjectID)
		}
		want, ok := byRun[*ev.ObjectID]
		if !ok {
			t.Fatalf("unexpected gate.started run_id %q in view events", *ev.ObjectID)
		}
		if ev.Payload["item_id"] != want.ItemID || ev.Payload["run_id"] != want.RunID ||
			ev.Payload["lane_id"] != want.LaneID || ev.Payload["base_sha"] != want.BaseSHA {
			t.Fatalf("view events gate.started payload = %v, want item_id=%q run_id=%q lane_id=%q base_sha=%q",
				ev.Payload, want.ItemID, want.RunID, want.LaneID, want.BaseSHA)
		}
		seenByRun[*ev.ObjectID]++
	}
	if seenByRun["run-ordinary-1"] != 2 || seenByRun["run-express-1"] != 1 || seenByRun["run-fixer-1"] != 1 {
		t.Fatalf("view events gate.started counts = %v, want ordinary=2 express=1 fixer=1", seenByRun)
	}
}
