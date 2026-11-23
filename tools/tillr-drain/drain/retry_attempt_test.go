package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type parallelConflictRetrySteps struct {
	*parallelSteps
	root        string
	conflicted  bool
	cfixStarted bool
	cfixGated   bool
}

func (s *parallelConflictRetrySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if isConflictFixRun(req.RunID) {
		s.mu.Lock()
		s.cfixStarted = true
		s.mu.Unlock()
		return s.writeRun(ctx, req)
	}
	return s.parallelSteps.Run(ctx, req)
}

func (s *parallelConflictRetrySteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if isConflictFixRun(c.RunID) {
		s.mu.Lock()
		s.cfixGated = true
		s.mu.Unlock()
		return GateOutcome{Outcome: GateFailed}, nil
	}
	return s.parallelSteps.Gate(ctx, c)
}

func (s *parallelConflictRetrySteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if !s.conflicted && !isConflictFixRun(c.RunID) {
		s.conflicted = true
		writeForkFile(s.t, filepath.Join(s.root, "work.txt"), "trunk moved\n")
		runGit(s.t, s.root, "add", ".")
		runGit(s.t, s.root, "commit", "-qm", "trunk moved before landing")
		return LandOutcome{
			Result:        "squash-conflict",
			ConflictPaths: []string{"CONFLICT (content): Merge conflict in work.txt"},
		}, nil
	}
	return s.parallelSteps.Land(ctx, c)
}

func TestOperatorRetryAfterParallelWorkflowAndConflictFixUsesNextAttempt(t *testing.T) {
	f, l, parallel := parallelFixture(t)
	steps := &parallelConflictRetrySteps{
		parallelSteps: parallel,
		root:          f.root,
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 8 && !steps.cfixGated; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if !steps.cfixStarted || !steps.cfixGated {
		t.Fatalf("conflict-fix did not run and fail gate: started=%v gated=%v", steps.cfixStarted, steps.cfixGated)
	}

	const cfixRunID = "run-item-a-a1-n5-cfix"
	var cfixAttempt int
	fixtureRead(t, f, func(r *store.Reader) error {
		_, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		work := byRun[cfixRunID]
		if work == nil {
			t.Fatalf("missing pending history for %s", cfixRunID)
		}
		cfixAttempt = work.Decision.Attempt
		return nil
	})
	if cfixAttempt != 2 {
		t.Fatalf("conflict-fix attempt = %d, want 2 (attempt 1 had 5 parallel/writer runs)", cfixAttempt)
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: "item-a",
			RunID:  cfixRunID,
			By:     "operator",
			Why:    "retry after conflict-fix gate failure",
			At:     l.cfg.Now(),
		})
		return err
	})

	next := decisionFor(t, l, "item-a").decision
	if next.Action != ActionRetry || next.Reason != ReasonRetry {
		t.Fatalf("after AuthorizeRetry got %s/%s, want retry/operator-retry", next.Action, next.Reason)
	}
	if next.Attempt != cfixAttempt+1 {
		t.Fatalf("operator retry attempt = %d, want %d (last attempt %d + 1)", next.Attempt, cfixAttempt+1, cfixAttempt)
	}
	if next.RunRequest == nil || !strings.HasPrefix(next.RunRequest.RunID, "run-item-a-a3-") {
		t.Fatalf("operator retry run id = %+v, want prefix run-item-a-a3-", next.RunRequest)
	}
}
