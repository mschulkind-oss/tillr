package drain

import (
	"context"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// A claim (applyPlanGeneration) records its run as started before staging runs, and for
// an item with a conflict it also moves that conflict in the same write: a design-seat
// dispatch marks the conflict awaiting that design run, and a build dispatched against an
// answer closes the answer. Staging can still refuse the claimed run — most often because
// the lane it was offered holds a change nobody landed — and the claim is then cancelled
// (cancelStaging) before any run face starts.
//
// A run that was claimed but never started is no attempt: it has no terminal record, so
// the planner's attempt count does not include it and the next dispatch reuses its run
// id. Its conflict move must not outlive it either. Left in place, an awaiting conflict
// reads as "waits on work: design" with no design run alive, and a closed answer leaves
// the item with no authorization to build, held as run-link-missing or retry-stale for an
// operator. On 2026-11-17 six runs were cancelled that way on refusing lanes.

// conflictBeforeClaim answers the conflict as it stood before d's claim moved it, and
// whether the current record is that claim's move at all. A record any later act wrote —
// an answer, an owner disposition, another dispatch — is not the claim's and is left as is.
func conflictBeforeClaim(current item.Conflict, d Decision) (item.Conflict, bool) {
	if d.RunRequest == nil || current.ItemID != d.ItemID {
		return current, false
	}
	switch {
	case d.BlockConflict != nil:
		if current.ConflictID != d.BlockConflict.ConflictID || current.Status != "awaiting" || current.DesignRunID != d.RunRequest.RunID {
			return current, false
		}
		current.Status, current.WaitingOn, current.DesignRunID = "raised", "design", d.DesignContinuationFrom
		return current, true
	case d.ConflictID != "":
		if current.ConflictID != d.ConflictID || current.Status != "closed" {
			return current, false
		}
		current.Status, current.WaitingOn = "answered", "build"
		return current, true
	}
	return current, false
}

// undoClaimedConflict restores the conflict d's claim moved, in the caller's write. An
// item that went terminal keeps its record: recording a conflict would move its state.
func undoClaimedConflict(tx *store.Tx, d Decision, at time.Time) (bool, error) {
	conflicts, err := item.ReadConflicts(tx.Reader())
	if err != nil {
		return false, err
	}
	c, ok := conflicts[d.ItemID]
	if !ok {
		return false, nil
	}
	before, moved := conflictBeforeClaim(c, d)
	if !moved {
		return false, nil
	}
	if err := refuseTerminalItem(tx.Reader(), d.ItemID, "unlaunched-claim"); err != nil {
		return false, nil
	}
	return true, item.RecordConflict(tx, before, at)
}

// releaseUnlaunchedClaims recognizes the records a cancelled claim left before
// cancelStaging undid its conflict move: the item's latest dispatch is examined, its run
// has no terminal record, and the item's conflict is still as that claim moved it. The
// move is undone, so the planner dispatches the item again — the design seat for an
// awaiting conflict, the build for a closed answer — under the same run id. No operator
// command is involved, and nothing is written for a record that is not that shape.
func (l *Loop) releaseUnlaunchedClaims(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	found := []Decision{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(conflicts))
		for id := range conflicts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			work := works[id]
			if work == nil || !work.Done || work.Decision.RunRequest == nil || len(work.Decision.Parallel) > 0 {
				continue
			}
			if _, moved := conflictBeforeClaim(conflicts[id], work.Decision); !moved {
				continue
			}
			rec, err := terminalRecord(r, work.Decision.RunRequest.RunID)
			if err != nil {
				return err
			}
			if rec == nil {
				found = append(found, work.Decision)
			}
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return err
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		for _, d := range found {
			// Re-read under the write: a dispatch claimed since is not this record.
			if w := works[d.ItemID]; w == nil || !w.Done || w.Decision.RunRequest == nil || w.Decision.RunRequest.RunID != d.RunRequest.RunID {
				continue
			}
			if rec, err := terminalRecord(tx.Reader(), d.RunRequest.RunID); err != nil || rec != nil {
				if err != nil {
					return err
				}
				continue
			}
			undone, err := undoClaimedConflict(tx, d, at)
			if err != nil {
				return err
			}
			if undone {
				l.reportf("loop: %s run %s was claimed but never started; its conflict is released for the next dispatch\n", d.ItemID, d.RunRequest.RunID)
			}
		}
		return nil
	})
}
