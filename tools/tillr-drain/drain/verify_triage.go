package drain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	reasonVerifyTriage    = faults.VerifyTriageReason
	verifyTriageResetKind = "drain.verify-triage.reset"
)

func (l *Loop) verifyTriageHold(itemID, runID, laneID, ack string, findings []string) ItemFault {
	return ItemFault{ItemID: itemID, RunID: runID, LaneID: laneID, Reason: reasonVerifyTriage, RetryAck: ack,
		Detail: "Three verifier refusals: " + strings.Join(findings, "; ") +
			". The operator moves: " + faults.VerifyTriageCommand(l.cfg.Store, itemID, runID, ack)}
}

// readVerifyTriage reads the first three distinct verifier refusals in this retry cycle.
// Sequence orders design retries and new build retries even when clocks tie.
// Until the first build reset event, an older-format build retry can still
// supersede a design retry. Its time gives the cutoff; the superseded run's
// refusal (or the finish event written by its run record) gives the sequence floor.
func readVerifyTriage(r *store.Reader, itemID string) ([]string, error) {
	return readVerifyTriageCycle(r, itemID, "")
}

// When recovering a blocked legacy repeat, require its originating refusal to
// belong to this cycle too. An unrelated or superseded conflict is left alone.
func readVerifyTriageCycle(r *store.Reader, itemID, repeatRun string) ([]string, error) {
	rows, err := r.Query(`WITH retry_events AS (
 SELECT seq, ts, kind FROM events
 WHERE (kind=:reset AND object_type='item' AND object_id=:item)
    OR (kind=:design AND json_extract(payload,'$.item_id')=:item)
), boundary AS (
 SELECT seq, ts FROM retry_events ORDER BY seq DESC LIMIT 1
), legacy_retry AS (
 SELECT requested_at, COALESCE(
  (SELECT MAX(seq) FROM events WHERE kind=:refusal AND object_type='run' AND object_id=retries.supersedes_run_id),
  (SELECT MAX(seq) FROM events WHERE kind=:run_finish AND object_type='run' AND object_id=retries.supersedes_run_id),
  0) AS floor_seq
 FROM retries WHERE item_id=:item AND NOT EXISTS (SELECT 1 FROM retry_events WHERE kind=:reset)
 ORDER BY rowid DESC LIMIT 1
), distinct_runs AS (
 SELECT e.object_id, MIN(e.seq) AS first_seq FROM events e
 LEFT JOIN boundary b ON 1=1 LEFT JOIN legacy_retry a ON 1=1
 WHERE e.kind=:refusal AND e.object_type='run' AND json_extract(e.payload,'$.item_id')=:item AND json_extract(e.payload,'$.step')=:step
 AND e.seq > COALESCE(b.seq,0)
 AND (a.requested_at IS NULL OR (a.requested_at < b.ts AND a.floor_seq <= b.seq)
      OR (e.ts >= a.requested_at AND e.seq > a.floor_seq))
 GROUP BY e.object_id
)
SELECT e.ts, COALESCE(json_extract(e.payload,'$.detail'),'')
 FROM distinct_runs d JOIN events e ON e.seq=d.first_seq
 WHERE :repeat_run='' OR EXISTS (SELECT 1 FROM distinct_runs WHERE object_id=:repeat_run)
 ORDER BY d.first_seq LIMIT 3`,
		sql.Named("item", itemID), sql.Named("repeat_run", repeatRun), sql.Named("reset", verifyTriageResetKind), sql.Named("design", DesignRetryKind),
		sql.Named("run_finish", run.FinishKind), sql.Named("refusal", RefusalKind), sql.Named("step", RefusalVerifyNotDone))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var findings []string
	for rows.Next() {
		var ts int64
		var detail string
		if err := rows.Scan(&ts, &detail); err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(strings.TrimSpace(detail), "\n")
		findings = append(findings, fmt.Sprintf("%s: %s", time.UnixMilli(ts).UTC().Format(time.RFC3339), first))
	}
	return findings, rows.Err()
}

// recoverVerifyTriage projects the same hold as a live refusal for history
// recorded before triage was introduced. Discover it without the writer lock;
// recheck eligibility and retry boundaries in the write before recording it.
func (l *Loop) recoverVerifyTriage(ctx context.Context, s *store.Store) error {
	var ids []string
	var holds []ItemFault
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err != nil || !holdEnabled(declared.Parsed, reasonVerifyTriage) {
			return err
		}
		rows, err := r.Query(`SELECT i.item_id FROM events e JOIN items i ON i.item_id=json_extract(e.payload,'$.item_id')
 WHERE e.kind=? AND e.object_type='run' AND json_extract(e.payload,'$.step')=? AND i.state IN (?,?)
 GROUP BY i.item_id HAVING COUNT(DISTINCT e.object_id)>=3 ORDER BY i.item_id`,
			RefusalKind, RefusalVerifyNotDone, declared.Parsed.Ready, declared.Parsed.Blocked)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil || len(ids) == 0 {
			return err
		}
		rows.Close()
		holds, err = l.missingVerifyTriage(r, ids, declared.Parsed)
		return err
	}); err != nil || len(holds) == 0 {
		return err
	}
	ids = ids[:0]
	for _, hold := range holds {
		ids = append(ids, hold.ItemID)
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		r := tx.Reader()
		declared, err := definition.ForReader(r)
		if err != nil || !holdEnabled(declared.Parsed, reasonVerifyTriage) {
			return err
		}
		holds, err := l.missingVerifyTriage(r, ids, declared.Parsed)
		if err != nil {
			return err
		}
		for _, hold := range holds {
			if err := l.recordItemFaultTx(tx, hold); err != nil {
				return err
			}
		}
		return nil
	})
}

func (l *Loop) missingVerifyTriage(r *store.Reader, ids []string, def definition.Definition) ([]ItemFault, error) {
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, err
	}
	conflicts, err := item.ReadConflicts(r)
	if err != nil {
		return nil, err
	}
	var holds []ItemFault
	for _, id := range ids {
		if _, held := faults[id]; held {
			continue
		}
		var state string
		err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		var repeatRun, repeatConflict string
		c := conflicts[id]
		switch state {
		case def.Ready:
			// Published questions still own their disposition even though
			// their item is stored in the queue.
			if c.Status == "question" {
				continue
			}
		case def.Blocked:
			if !legacyVerifyRepeat(c) {
				continue
			}
			repeatRun, repeatConflict = c.RunID, c.ConflictID
		default:
			continue
		}
		findings, err := readVerifyTriageCycle(r, id, repeatRun)
		if err != nil {
			return nil, err
		}
		if len(findings) < 3 {
			continue
		}
		// Old drains could run the item again after the third refusal. Show the
		// first three findings, but name the run the ordinary retry face accepts.
		var rec run.Record
		err = r.QueryRow(`SELECT r.run_id, COALESCE(r.lane_id,''), r.backend, r.state, r.exit_code
 FROM runs r WHERE r.item_id=? AND r.state IN (?,?,?)
 ORDER BY r.started_at DESC, r.event_seq DESC, r.rowid DESC LIMIT 1`, id,
			run.StateExited, run.StateFailedToStart, run.StateTimedOut).
			Scan(&rec.RunID, &rec.LaneID, &rec.Backend, &rec.State, &rec.ExitCode)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		judged, err := runJudged(r, rec.RunID)
		if err != nil {
			return nil, err
		}
		// AttemptUnjudged depends only on the run state and exit code, not its
		// report. Do not open artifact files while holding the store write lock.
		ack := acceptedAck(judged, ClassifyAttempt(rec, ""))
		hold := l.verifyTriageHold(id, rec.RunID, rec.LaneID, ack, findings)
		hold.RepeatConflictID = repeatConflict
		holds = append(holds, hold)
	}
	if len(holds) == 0 {
		return nil, nil
	}
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	idle := holds[:0]
	for _, hold := range holds {
		// Let an existing attempt settle; this is a hold on redispatch, not a
		// cancellation of an in-flight attempt.
		if work := works[hold.ItemID]; work == nil || work.Done {
			idle = append(idle, hold)
		}
	}
	return idle, nil
}

// The old loop wrote this prefix for an identical repeat, including its run
// and refusal step. Do not claim published questions or other blocked reports.
func legacyVerifyRepeat(c item.Conflict) bool {
	return (c.Status == "raised" || c.Status == "awaiting" || c.Status == "owner") &&
		c.RunID != "" && strings.HasPrefix(c.Text, fmt.Sprintf("%s run %s ended (refused at %s)", IdenticalRepeatPrefix, c.RunID, RefusalVerifyNotDone))
}
