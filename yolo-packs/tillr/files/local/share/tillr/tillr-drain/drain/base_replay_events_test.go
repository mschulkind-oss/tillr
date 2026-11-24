package drain_test

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
)

type clockReplaySteps struct {
	*drain.ProcessSteps
	mu        sync.Mutex
	now       time.Time
	advanceBy time.Duration
	outcome   drain.ReplayOutcome
	onReplay  func(candidate drain.Candidate, rev string, checks []string)
}

func (s *clockReplaySteps) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *clockReplaySteps) Replay(_ context.Context, candidate drain.Candidate, rev string, checks []string) (drain.ReplayOutcome, error) {
	if s.onReplay != nil {
		s.onReplay(candidate, rev, checks)
	}
	s.mu.Lock()
	s.now = s.now.Add(s.advanceBy)
	out := s.outcome
	s.mu.Unlock()
	if out.Rev == "" {
		out.Rev = rev
	}
	return out, nil
}

func filterEventsByKind(events []event.Event, kind string) []event.Event {
	var out []event.Event
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestBaseReplayEventsAreRegisteredAndRecordedThroughDrain(t *testing.T) {
	for _, kind := range []string{drain.BaseReplayStartedKind, drain.BaseReplayEndedKind} {
		reg, err := event.Lookup(kind)
		if err != nil {
			t.Fatalf("%s is not registered: %v", kind, err)
		}
		if reg.Owner != drain.Utility {
			t.Fatalf("%s owner = %q, want %q", kind, reg.Owner, drain.Utility)
		}
	}

	cases := []struct {
		name        string
		replayOut   drain.ReplayOutcome
		wantOutcome string
	}{
		{
			name:        "red",
			replayOut:   drain.ReplayOutcome{Red: []string{"unit"}, Known: true},
			wantOutcome: "red",
		},
		{
			name:        "green",
			replayOut:   drain.ReplayOutcome{Known: true},
			wantOutcome: "green",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
			fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
			base := git(t, f.root, "rev-parse", "main")
			intersectingGate := strings.ReplaceAll(failingGateBody, `"reads":["src/"]`, `"reads":["candidate.txt"]`)
			gate := stub(t, "gate", intersectingGate)

			var sawStartedWhileRunning bool
			var sawEndedWhileRunning bool
			var firstRunID string
			steps := &clockReplaySteps{
				ProcessSteps: f.steps(t, gate, always(t, "land", 0), always(t, "verdict", 0)),
				now:          time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC),
				advanceBy:    1500 * time.Millisecond,
				outcome:      tc.replayOut,
				onReplay: func(candidate drain.Candidate, _ string, _ []string) {
					if candidate.ItemID != "item_041" || firstRunID != "" {
						return
					}
					firstRunID = candidate.RunID
					evs := eventsOf(t, f.store)
					for _, ev := range filterEventsByKind(evs, drain.BaseReplayStartedKind) {
						if ev.ObjectID != nil && *ev.ObjectID == candidate.RunID {
							sawStartedWhileRunning = true
						}
					}
					for _, ev := range filterEventsByKind(evs, drain.BaseReplayEndedKind) {
						if ev.ObjectID != nil && *ev.ObjectID == candidate.RunID {
							sawEndedWhileRunning = true
						}
					}
				},
			}

			loop, err := drain.NewLoop(drain.LoopConfig{
				ExitWhenDrained: true,
				Root:            f.root,
				Store:           f.store,
				LaneRoot:        f.laneRoot,
				Lanes:           []string{laneName(1)},
				Policy:          f.policy,
				Steps:           steps,
				Stage:           trunkStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, t: t, root: f.root},
				Now:             steps.Now,
				Report:          io.Discard,
			})
			if err != nil {
				t.Fatalf("NewLoop: %v", err)
			}
			if _, err := loop.Run(context.Background()); err != nil {
				t.Fatalf("loop.Run: %v", err)
			}

			if !sawStartedWhileRunning {
				t.Fatalf("started event was not in the store while Replay was running")
			}
			if sawEndedWhileRunning {
				t.Fatalf("ended event was already in the store while Replay was still running")
			}

			all := eventsOf(t, f.store)
			startedEvents := filterEventsByKind(all, drain.BaseReplayStartedKind)
			endedEvents := filterEventsByKind(all, drain.BaseReplayEndedKind)
			if len(startedEvents) == 0 || len(endedEvents) == 0 {
				t.Fatalf("got %d started and %d ended events, want at least 1 each", len(startedEvents), len(endedEvents))
			}

			var startedEv, endedEv *event.Event
			var startedPayload drain.BaseReplayStartedPayload
			var endedPayload drain.BaseReplayEndedPayload
			for i := range startedEvents {
				var p drain.BaseReplayStartedPayload
				if err := json.Unmarshal(startedEvents[i].Payload, &p); err != nil {
					t.Fatalf("unmarshal started payload: %v", err)
				}
				if p.ItemID == "item_041" && p.RunID == firstRunID {
					startedEv = &startedEvents[i]
					startedPayload = p
					break
				}
			}
			for i := range endedEvents {
				var p drain.BaseReplayEndedPayload
				if err := json.Unmarshal(endedEvents[i].Payload, &p); err != nil {
					t.Fatalf("unmarshal ended payload: %v", err)
				}
				if p.ItemID == "item_041" && p.RunID == firstRunID {
					endedEv = &endedEvents[i]
					endedPayload = p
					break
				}
			}
			if startedEv == nil || endedEv == nil {
				t.Fatalf("missing base replay events for item_041: started=%+v ended=%+v", startedEvents, endedEvents)
			}
			if startedEv.Seq >= endedEv.Seq {
				t.Fatalf("started seq %d >= ended seq %d", startedEv.Seq, endedEv.Seq)
			}
			if startedPayload.BaseSHA != base {
				t.Fatalf("started base_sha = %q, want %q", startedPayload.BaseSHA, base)
			}
			if startedPayload.RunID == "" {
				t.Fatalf("started run_id is empty")
			}
			if !reflect.DeepEqual(startedPayload.Checks, []string{"unit"}) {
				t.Fatalf("started checks = %v, want [unit]", startedPayload.Checks)
			}
			if startedEv.ObjectType != "run" || startedEv.ObjectID == nil || *startedEv.ObjectID != startedPayload.RunID {
				t.Fatalf("started object = (%q, %v), want (run, %q)", startedEv.ObjectType, startedEv.ObjectID, startedPayload.RunID)
			}

			if endedPayload.BaseSHA != startedPayload.BaseSHA || endedPayload.ItemID != startedPayload.ItemID || endedPayload.RunID != startedPayload.RunID || !reflect.DeepEqual(endedPayload.Checks, startedPayload.Checks) {
				t.Fatalf("ended names %+v do not match started %+v", endedPayload, startedPayload)
			}
			if endedPayload.Outcome != tc.wantOutcome {
				t.Fatalf("ended outcome = %q, want %q", endedPayload.Outcome, tc.wantOutcome)
			}
			if endedPayload.ElapsedMS != 1500 {
				t.Fatalf("ended elapsed_ms = %d, want 1500", endedPayload.ElapsedMS)
			}
			if endedEv.ObjectType != "run" || endedEv.ObjectID == nil || *endedEv.ObjectID != endedPayload.RunID {
				t.Fatalf("ended object = (%q, %v), want (run, %q)", endedEv.ObjectType, endedEv.ObjectID, endedPayload.RunID)
			}
		})
	}
}
