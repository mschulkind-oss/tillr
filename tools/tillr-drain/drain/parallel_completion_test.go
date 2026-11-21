package drain

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type orderedParallelSteps struct {
	*parallelSteps
	mu                 sync.Mutex
	events             []string
	replacementStarted chan run.Request
	replacementEnded   chan struct{}
	releaseReplacement chan struct{}
	releaseSibling     chan struct{}
}

func (s *orderedParallelSteps) record(name string) {
	s.mu.Lock()
	s.events = append(s.events, name)
	s.mu.Unlock()
}

func (s *orderedParallelSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	replacement := req.Resume || strings.HasSuffix(req.RunID, "-restart")
	name := req.NodeID
	if replacement {
		name = "replacement"
	}
	s.record(name + "-start")
	if replacement {
		s.replacementStarted <- req
		select {
		case <-s.releaseReplacement:
		case <-ctx.Done():
			return run.Record{}, "", ctx.Err()
		}
	}
	if req.NodeID == "scout4" {
		// Let every initial reader reach the fixture's overlap rendezvous, but
		// hold this sibling's ending until the replacement has both run and ended.
		s.parallelSteps.mu.Lock()
		s.parallelSteps.started++
		if s.parallelSteps.started == 4 {
			close(s.parallelSteps.overlap)
		}
		s.parallelSteps.mu.Unlock()
		select {
		case <-s.releaseSibling:
		case <-ctx.Done():
			return run.Record{}, "", ctx.Err()
		}
		// Run adds this reader itself; restore the rendezvous's accounting first.
		s.parallelSteps.mu.Lock()
		s.parallelSteps.started--
		s.parallelSteps.mu.Unlock()
	}
	rec, log, err := s.parallelSteps.Run(ctx, req)
	s.record(name + "-end")
	if replacement {
		close(s.replacementEnded)
	}
	return rec, log, err
}

// Infrastructure cannot replace a reader before its held sibling ends: the backend
// must reopen first. An unanswered zero-exit reader still restarts immediately.
func TestParallelReaderReplacementEndsBeforeHeldSibling(t *testing.T) {
	for _, kind := range []string{"missing-footer-restart", "continuation"} {
		t.Run(kind, func(t *testing.T) {
			f, l, steps := parallelFixture(t)
			steps.writerAfter = 5
			if kind == "missing-footer-restart" {
				steps.badReportNode = "-n2"
				steps.badReport = "no report footer\n"
				l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
				l.cfg.Policy.Preference = []string{"fixture", "alternate"}
				l.cfg.Policy.SeatLists = map[string][]string{"scout": {"fixture", "alternate"}, "build": {"fixture", "alternate"}}
			} else {
				steps.wallNode = "-n2"
			}
			ordered := &orderedParallelSteps{parallelSteps: steps, replacementStarted: make(chan run.Request, 1), replacementEnded: make(chan struct{}), releaseReplacement: make(chan struct{}), releaseSibling: make(chan struct{})}
			l.cfg.Steps = ordered
			var releaseReplacement, releaseSibling sync.Once
			t.Cleanup(func() {
				releaseReplacement.Do(func() { close(ordered.releaseReplacement) })
				releaseSibling.Do(func() { close(ordered.releaseSibling) })
			})
			result := &LoopResult{}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			var request run.Request
			select {
			case request = <-ordered.replacementStarted:
			case <-time.After(hangGuard):
				t.Fatal("replacement cannot start while its sibling is held")
			}
			if request.Backend != "fixture" || (kind == "missing-footer-restart" && request.RunID != "run-item-a-a1-n2-restart") {
				t.Fatalf("replacement changed backend or restart identity: %+v", request)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				works, err := readPending(r)
				if err != nil {
					return err
				}
				work := works["item-a"]
				if work.Done || work.Ready || work.Owner == nil || work.LoopID != l.sessionID || work.Decision.Parallel[0].RunRequest.RunID != request.RunID {
					return fmt.Errorf("replacement lost durable group ownership: %+v", work)
				}
				var state, group string
				if err := r.QueryRow(`SELECT state,run_id FROM lanes WHERE lane_id='lane-1'`).Scan(&state, &group); err != nil {
					return err
				}
				if state != run.LaneStateLeased || group != "run-item-a-a1-n1" {
					return fmt.Errorf("group lease changed: %s %s", state, group)
				}
				return nil
			})
			releaseReplacement.Do(func() { close(ordered.releaseReplacement) })
			select {
			case <-ordered.replacementEnded:
			case <-time.After(hangGuard):
				t.Fatal("replacement did not end")
			}
			releaseSibling.Do(func() { close(ordered.releaseSibling) })
			drainOwned(t, l, result)
			for i := 0; i < 2; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			ordered.mu.Lock()
			defer ordered.mu.Unlock()
			positions := map[string]int{}
			for i, event := range ordered.events {
				positions[event] = i
			}
			for _, event := range []string{"scout2-end", "replacement-start", "replacement-end", "scout4-end", "build-start"} {
				if _, found := positions[event]; !found {
					t.Fatalf("missing %s: %v", event, ordered.events)
				}
			}
			if !(positions["scout2-end"] < positions["replacement-start"] && positions["replacement-start"] < positions["replacement-end"] && positions["replacement-end"] < positions["scout4-end"] && positions["scout4-end"] < positions["build-start"]) {
				t.Fatalf("wrong reader/writer event order: %v", ordered.events)
			}
			if steps.started != 5 || steps.builders != 1 {
				t.Fatalf("readers=%d writers=%d", steps.started, steps.builders)
			}
		})
	}
}
