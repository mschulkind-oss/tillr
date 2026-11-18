package drain

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type heldBuildReaders struct {
	*parallelSteps
	entered chan string
	release chan struct{}
}

func (s *heldBuildReaders) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if strings.HasSuffix(req.RunID, "-restart") {
		s.entered <- req.RunID
		<-s.release
	}
	return s.parallelSteps.Run(ctx, req)
}

func TestNewBuildRestartsOnlyInflightReadersAndKeepsTheGroupLease(t *testing.T) {
	f, l, steps := parallelFixture(t)
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, w, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	staged, err := l.stageDispatches(context.Background(), []Decision{w.Decision}, base, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %v %v", len(staged), err)
	}
	d := staged[0]
	first := d
	first.Parallel = d.Parallel[:1]
	steps.wantOverlap = 2
	if err := l.runParallel(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	// Two completed readers remain in the leased four-reader group. Their
	// replacements must be reserved before either unfinished membership ends.
	assertLeased := func() {
		t.Helper()
		fixtureRead(t, f, func(r *store.Reader) error {
			var state, id string
			if err := r.QueryRow(`SELECT state,coalesce(run_id,'') FROM lanes WHERE lane_id=?`, d.LaneID).Scan(&state, &id); err != nil {
				return err
			}
			if state != run.LaneStateLeased || id != d.RunRequest.ReadOnlyGroup {
				t.Errorf("group lease=%s/%s", state, id)
			}
			return nil
		})
	}
	assertLeased()
	steps.wantOverlap = 4
	steps.overlap = make(chan struct{})
	held := &heldBuildReaders{parallelSteps: steps, entered: make(chan string, 2), release: make(chan struct{})}
	l.cfg.Steps = held
	l.cfg.Restart = &RestartConfig{}
	l.build.Revision = "new-build"
	result := &LoopResult{}
	if err := l.migrateBuildRuns(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-held.entered:
		case <-time.After(hangGuard):
			t.Fatal("replacement reader did not start")
		}
	}
	assertLeased()
	before := eventCount(t, f.storePath)
	if err := l.migrateBuildRuns(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	if after := eventCount(t, f.storePath); after != before {
		t.Fatalf("new-build readers interrupted again: events %d => %d", before, after)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var pauses int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, run.PauseRequestKind).Scan(&pauses); err != nil {
			return err
		}
		if pauses != 0 {
			t.Errorf("readers sent %d pause requests", pauses)
		}
		facts, err := readInputDecisions(r)
		if err != nil {
			return err
		}
		hash, _ := l.build.Hash()
		for _, id := range []string{"run-item-a-a1-n3-restart", "run-item-a-a1-n4-restart"} {
			if facts[id].Inputs == nil || facts[id].Inputs.TillrBuild != hash {
				t.Errorf("reader %s missing new build fact", id)
			}
		}
		return nil
	})
	close(held.release)
	drainOwned(t, l, result)
	for i := 0; i < 3; i++ {
		settleContinuation(t, l, result)
	}
	if steps.started != 4 || steps.builders != 1 || result.Tokens != 40 {
		t.Fatalf("readers=%d builders=%d tokens=%d", steps.started, steps.builders, result.Tokens)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var retries int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, RetryAckKind).Scan(&retries); err != nil {
			return err
		}
		if retries != 0 {
			t.Errorf("reader restart spent %d retries", retries)
		}
		return nil
	})
}
