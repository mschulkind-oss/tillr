// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"context"
	"time"
)

// DefaultStorePollInterval is 100 milliseconds: the maximum wait before noticing a new
// item or operator edit, because the store has no change notification. Polling
// bounds observation latency, never the amount or duration of work.
const DefaultStorePollInterval = 100 * time.Millisecond

func (l *Loop) waitForTrigger(ctx context.Context, leases []LaneLease, at time.Time, result *LoopResult) (string, error) {
	if err := l.checkStopRequested(ctx); err != nil {
		if stoppedByOperator(ctx, err) {
			result.Detail = "the loop was asked to stop while waiting for a trigger"
			return StopOperatorStop, nil
		}
		return "", err
	}
	wait := l.cfg.StorePollInterval
	for _, lease := range leases {
		if !l.pollOnly && lease.LeasedUntil != nil {
			until := lease.LeasedUntil.Sub(at)
			if until < wait {
				wait = until
			}
		}
	}
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	completions := l.completions
	// Tests can suppress completion/expiry accelerators; polling alone must
	// converge on the same durable outcomes. The next pass drains completions.
	if l.pollOnly {
		completions = nil
	}
	for {
		select {
		case c := <-completions:
			before := l.dispatched(result)
			stop, err := l.finishRunStep(ctx, c, result)
			l.mu.Lock()
			advanced := result.Dispatched != before || l.activeItems[c.itemID] == ""
			l.mu.Unlock()
			if stop != "" || err != nil || advanced {
				return stop, err
			}
			// The handoff began this item's examination. Keep listening for its
			// answer without resetting the existing store/lease wake deadline.
		case <-timer.C:
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			return "", nil
		case <-ctx.Done():
			result.Detail = "the loop was asked to stop while waiting for a trigger"
			return StopOperatorStop, nil
		}
	}
}
