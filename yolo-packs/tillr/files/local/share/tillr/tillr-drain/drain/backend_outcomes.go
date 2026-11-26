package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// readBackendOutcomes answers every agent run ending, newest first. A saved
// ending keeps its saved facts; an unsaved one is classified from the record and
// the log as they are now. It writes nothing, so a read-only face may call it.
func readBackendOutcomes(r *store.Reader) ([]backendOutcome, error) {
	return readBackendOutcomesFrom(r, nil)
}

// readBackendOutcomesFrom reuses a supplied history from this transaction or a
// snapshot validated against pendingHistoryHead. Nil asks it to fold for itself.
func readBackendOutcomesFrom(r *store.Reader, byRun map[string]*pendingWork) ([]backendOutcome, error) {
	out, _, _, err := readBackendOutcomesFull(r, byRun)
	return out, err
}

func readBackendOutcomesFull(r *store.Reader, byRun map[string]*pendingWork) ([]backendOutcome, int, map[int64]bool, error) {
	return policy.ReadBackendOutcomesWith(r, func(r *store.Reader, out []backendOutcome, offered map[int64]bool) error {
		return classifyUnsavedBackendOutcomes(r, out, offered, byRun)
	})
}

func classifyUnsavedBackendOutcomes(r *store.Reader, out []backendOutcome, offered map[int64]bool, byRun map[string]*pendingWork) error {
	needClassify := false
	for i := range out {
		if offered[out[i].Seq] {
			out[i].Class, out[i].Closes = AttemptAnswered, false
			continue
		}
		if !out[i].Saved {
			needClassify = true
		}
	}
	if !needClassify {
		return nil
	}
	interrupted, _, err := readInterruptedRuns(r)
	if err != nil {
		return err
	}
	if byRun == nil {
		_, byRun, err = readPendingHistory(r)
		if err != nil {
			return err
		}
	}
	for i := range out {
		if out[i].Saved || offered[out[i].Seq] {
			continue
		}
		rec := out[i].Rec
		log := logTail(rec.ArtifactRefs)
		out[i].Class = classifyWorkAttempt(byRun[rec.RunID], rec, log)
		closes := out[i].Class == AttemptInfrastructure && !answeredByAnotherRule(rec, interrupted, byRun)
		if closes && isUnknownFaceFailure(rec, log) {
			pending, err := rollPendingAt(r, out[i].Seq)
			if err != nil {
				return err
			}
			if pending {
				closes = false
			}
		}
		out[i].Closes = closes
	}
	return nil
}

func seedBackendOutcomesState(r *store.Reader, head int64, byRun map[string]*pendingWork) (*backendOutcomesState, error) {
	return policy.SeedBackendOutcomesState(r, head, func(r *store.Reader, out []backendOutcome, offered map[int64]bool) error {
		return classifyUnsavedBackendOutcomes(r, out, offered, byRun)
	})
}

func advanceBackendOutcomesState(s *backendOutcomesState, r *store.Reader, checkEvents bool, byRun map[string]*pendingWork) (*backendOutcomesState, bool, error) {
	return s.Advance(r, checkEvents, func(r *store.Reader, out []backendOutcome, offered map[int64]bool) error {
		return classifyUnsavedBackendOutcomes(r, out, offered, byRun)
	})
}

// answeredByAnotherRule says an infrastructure-class ending is not evidence that
// its backend is broken: the loop interrupted the run itself, the provider
// refused it with a stated reset time (the quota window closes and reopens that
// backend), or the session it resumed is gone (a fresh session replaces it).
func answeredByAnotherRule(rec run.Record, interrupted map[string]bool, byRun map[string]*pendingWork) bool {
	if interrupted[rec.RunID] {
		return true
	}
	if _, _, reset, ok := refusedWindow(rec); ok && !reset.IsZero() {
		return true
	}
	work := byRun[rec.RunID]
	if work == nil {
		return false
	}
	decision := work.Decision
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest != nil && node.RunRequest.RunID == rec.RunID {
			decision = node
			break
		}
	}
	// An exited workflow evaluator is judged from its verdict file, not its
	// process footer. Its exit alone is not evidence that the backend is closed.
	if decision.Workflow != nil && decision.NodeIndex >= 0 && decision.NodeIndex < len(decision.Workflow.Stages) && evaluatorNode(decision) && rec.State == run.StateExited {
		return true
	}
	gone, err := failedDesignerResume(decision, rec)
	return err == nil && gone
}

// RecordBackendOutcomes saves the class and the availability fact of every
// ending not saved yet, including standalone and judge runs. The loop calls it
// where it settles a run and when it starts; a saved ending survives a later
// edit or loss of its report.
func RecordBackendOutcomes(ctx context.Context, path string) error {
	return policy.RecordBackendOutcomesWith(ctx, path, readBackendOutcomes, infrastructureDetail)
}
