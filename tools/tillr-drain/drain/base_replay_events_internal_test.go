package drain

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

type gatedReplayStep struct {
	started chan string
	release map[string]chan struct{}
}

func (g gatedReplayStep) Replay(_ context.Context, candidate Candidate, rev string, _ []string) (ReplayOutcome, error) {
	g.started <- candidate.RunID
	<-g.release[candidate.RunID]
	return ReplayOutcome{Rev: rev, Known: true}, nil
}

func TestRunReplayTracksActiveReplaysAcrossConcurrentCallsAndUnknownReplay(t *testing.T) {
	ctx := context.Background()
	storePath := filepath.Join(t.TempDir(), "state.db")
	st, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	loop := &Loop{
		cfg: LoopConfig{
			Store:  storePath,
			Report: io.Discard,
			Now: func() time.Time {
				clockMu.Lock()
				defer clockMu.Unlock()
				now = now.Add(time.Second)
				return now
			},
		},
	}

	// unknownReplay touches neither activeBaseReplays nor the store.
	if _, err := loop.runReplay(ctx, unknownReplay{}, Candidate{ItemID: "item-u", RunID: "run-u"}, "sha-u", []string{"unit"}); err != nil {
		t.Fatalf("unknownReplay: %v", err)
	}
	if loop.activeBaseReplays != 0 {
		t.Fatalf("activeBaseReplays after unknownReplay = %d, want 0", loop.activeBaseReplays)
	}

	// Overlapping sequence: start(R1), start(R2), end(R2), start(R3), end(R1), end(R3).
	step := gatedReplayStep{
		started: make(chan string, 3),
		release: map[string]chan struct{}{
			"run-1": make(chan struct{}),
			"run-2": make(chan struct{}),
			"run-3": make(chan struct{}),
		},
	}

	done := map[string]chan struct{}{
		"run-1": make(chan struct{}),
		"run-2": make(chan struct{}),
		"run-3": make(chan struct{}),
	}
	launch := func(runID, itemID, sha string) {
		go func() {
			defer close(done[runID])
			if _, err := loop.runReplay(ctx, step, Candidate{ItemID: itemID, RunID: runID}, sha, []string{"unit"}); err != nil {
				t.Errorf("runReplay(%s): %v", runID, err)
			}
		}()
		got := <-step.started
		if got != runID {
			t.Fatalf("started %q, want %q", got, runID)
		}
	}

	launch("run-1", "item-1", "sha-1")
	launch("run-2", "item-2", "sha-1")

	close(step.release["run-2"])
	<-done["run-2"]

	launch("run-3", "item-3", "sha-2")

	close(step.release["run-1"])
	<-done["run-1"]

	close(step.release["run-3"])
	<-done["run-3"]

	if loop.activeBaseReplays != 0 {
		t.Fatalf("final activeBaseReplays = %d, want 0", loop.activeBaseReplays)
	}

	st, err = store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("store.OpenRead: %v", err)
	}
	defer st.Close()

	events, err := st.Events(ctx, 0)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}

	type transition struct {
		kind          string
		runID         string
		activeReplays int
	}
	var got []transition
	for _, ev := range events {
		if ev.Kind != BaseReplayStartedKind && ev.Kind != BaseReplayEndedKind {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal(ev.Payload, &raw); err != nil {
			t.Fatalf("unmarshal %s: %v", ev.Kind, err)
		}
		v, ok := raw["active_replays"]
		if !ok {
			t.Fatalf("%s missing explicit active_replays key: %s", ev.Kind, string(ev.Payload))
		}
		runID, _ := raw["run_id"].(string)
		got = append(got, transition{
			kind:          ev.Kind,
			runID:         runID,
			activeReplays: int(v.(float64)),
		})
	}

	want := []transition{
		{kind: BaseReplayStartedKind, runID: "run-1", activeReplays: 1},
		{kind: BaseReplayStartedKind, runID: "run-2", activeReplays: 2},
		{kind: BaseReplayEndedKind, runID: "run-2", activeReplays: 1},
		{kind: BaseReplayStartedKind, runID: "run-3", activeReplays: 2},
		{kind: BaseReplayEndedKind, runID: "run-1", activeReplays: 1},
		{kind: BaseReplayEndedKind, runID: "run-3", activeReplays: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("transitions =\n%#v\nwant\n%#v", got, want)
	}
}
