package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// settleResolvedFixerRuns ends and settles any run records and leased lanes belonging to
// trunk fixers in StateResolvedElsewhere so they do not show as live. It covers paused,
// running, resumed (run.resume seq above runs.event_seq), unexamined drain.started
// events, locally active runs, and examination-leased runs.
func (l *Loop) settleResolvedFixerRuns(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var needed bool
	var activeToStop []string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var e error
		needed, e = hasUnfinishedResolvedFixerRuns(r)
		if e != nil {
			return e
		}
		if len(l.active) > 0 {
			fixers, e := resolvedFixerIDs(r)
			if e != nil {
				return e
			}
			if len(fixers) > 0 {
				hasReadOnly, e := tableExists(r, "read_only_runs")
				if e != nil {
					return e
				}
				hasNodes, e := tableExists(r, "node_runs")
				if e != nil {
					return e
				}
				hasDispatches, e := tableExists(r, "dispatches")
				if e != nil {
					return e
				}
				for runID := range l.active {
					for _, fixerID := range fixers {
						belongs, e := runBelongsToItem(r, runID, fixerID, hasReadOnly, hasNodes, hasDispatches)
						if e != nil {
							return e
						}
						if belongs {
							activeToStop = append(activeToStop, runID)
							break
						}
					}
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for _, runID := range activeToStop {
		l.stopActiveFixerRun(runID)
	}
	if !needed {
		return nil
	}
	at := l.cfg.Now().UTC()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		r := tx.Reader()
		fixers, err := resolvedFixerIDs(r)
		if err != nil || len(fixers) == 0 {
			return err
		}
		hasReadOnly, err := tableExists(r, "read_only_runs")
		if err != nil {
			return err
		}
		hasNodes, err := tableExists(r, "node_runs")
		if err != nil {
			return err
		}
		hasPauses, err := tableExists(r, "quota_pauses")
		if err != nil {
			return err
		}
		hasDispatches, err := tableExists(r, "dispatches")
		if err != nil {
			return err
		}
		for _, fixerID := range fixers {
			if hasReadOnly {
				if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE item_id=? AND settled=0`, fixerID); err != nil {
					return err
				}
			}
			if hasNodes {
				if _, err := tx.Exec(`UPDATE node_runs SET outcome='superseded' WHERE item_id=? AND outcome='running'`, fixerID); err != nil {
					return err
				}
			}
			if hasPauses {
				if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE item_id=?`, fixerID); err != nil {
					return err
				}
			}
			rows, err := r.Query(`SELECT run_id, state, event_seq FROM runs WHERE item_id=?`, fixerID)
			if err != nil {
				return err
			}
			var toSettle []string
			for rows.Next() {
				var runID, state string
				var eventSeq int64
				if err := rows.Scan(&runID, &state, &eventSeq); err != nil {
					rows.Close()
					return err
				}
				if !isTerminalRunState(state) {
					toSettle = append(toSettle, runID)
					continue
				}
				var resumed bool
				if err := r.QueryRow(
					`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='run' AND object_id=? AND seq>?)`,
					run.ResumeKind, runID, eventSeq,
				).Scan(&resumed); err != nil {
					rows.Close()
					return err
				}
				if resumed {
					toSettle = append(toSettle, runID)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
			for _, runID := range toSettle {
				if err := settleFixerRunRow(ctx, tx, runID, at); err != nil {
					return err
				}
			}
			unexaminedRows, err := r.Query(
				`SELECT DISTINCT e.object_id FROM events e
				 WHERE e.kind=? AND e.object_type='run'
				   AND json_valid(e.payload) AND json_extract(e.payload,'$.decision.item_id')=?
				   AND NOT EXISTS(SELECT 1 FROM events x WHERE x.kind=? AND x.object_type='run' AND x.object_id=e.object_id)`,
				dispatchStartedKind, fixerID, examinationDoneKind,
			)
			if err != nil {
				return err
			}
			var unexamined []string
			for unexaminedRows.Next() {
				var runID string
				if err := unexaminedRows.Scan(&runID); err != nil {
					unexaminedRows.Close()
					return err
				}
				unexamined = append(unexamined, runID)
			}
			if err := unexaminedRows.Err(); err != nil {
				unexaminedRows.Close()
				return err
			}
			unexaminedRows.Close()
			for _, runID := range unexamined {
				if err := appendEffect(tx, examinationDoneKind, runID, struct{}{}, at); err != nil {
					return err
				}
			}
		}

		laneRows, err := r.Query(`SELECT lane_id, run_id FROM lanes WHERE state='leased' AND run_id IS NOT NULL`)
		if err != nil {
			return err
		}
		type leasedLane struct{ laneID, runID string }
		var leased []leasedLane
		for laneRows.Next() {
			var ll leasedLane
			if err := laneRows.Scan(&ll.laneID, &ll.runID); err != nil {
				laneRows.Close()
				return err
			}
			leased = append(leased, ll)
		}
		if err := laneRows.Err(); err != nil {
			laneRows.Close()
			return err
		}
		laneRows.Close()

		for _, ll := range leased {
			for _, fixerID := range fixers {
				belongs, err := runBelongsToItem(r, ll.runID, fixerID, hasReadOnly, hasNodes, hasDispatches)
				if err != nil {
					return err
				}
				if belongs {
					if _, err := run.ReleaseLaneInTx(ctx, tx, ll.laneID, "fixer "+fixerID+" resolved elsewhere", at); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	})
}

func resolvedFixerIDs(r *store.Reader) ([]string, error) {
	rows, err := r.Query(`SELECT item_id FROM items WHERE item_id LIKE 'fixer-main-%' AND state=? ORDER BY item_id`, StateResolvedElsewhere)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func hasUnfinishedResolvedFixerRuns(r *store.Reader) (bool, error) {
	fixers, err := resolvedFixerIDs(r)
	if err != nil || len(fixers) == 0 {
		return false, err
	}
	hasReadOnly, err := tableExists(r, "read_only_runs")
	if err != nil {
		return false, err
	}
	hasNodes, err := tableExists(r, "node_runs")
	if err != nil {
		return false, err
	}
	hasPauses, err := tableExists(r, "quota_pauses")
	if err != nil {
		return false, err
	}
	hasDispatches, err := tableExists(r, "dispatches")
	if err != nil {
		return false, err
	}
	for _, fixerID := range fixers {
		if hasReadOnly {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM read_only_runs WHERE item_id=? AND settled=0)`, fixerID).Scan(&exists); err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
		}
		if hasNodes {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM node_runs WHERE item_id=? AND outcome='running')`, fixerID).Scan(&exists); err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
		}
		if hasPauses {
			var exists bool
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM quota_pauses WHERE item_id=?)`, fixerID).Scan(&exists); err != nil {
				return false, err
			}
			if exists {
				return true, nil
			}
		}
		rows, err := r.Query(`SELECT run_id, state, event_seq FROM runs WHERE item_id=?`, fixerID)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var runID, state string
			var eventSeq int64
			if err := rows.Scan(&runID, &state, &eventSeq); err != nil {
				rows.Close()
				return false, err
			}
			if !isTerminalRunState(state) {
				rows.Close()
				return true, nil
			}
			var resumed bool
			if err := r.QueryRow(
				`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='run' AND object_id=? AND seq>?)`,
				run.ResumeKind, runID, eventSeq,
			).Scan(&resumed); err != nil {
				rows.Close()
				return false, err
			}
			if resumed {
				rows.Close()
				return true, nil
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
		var unexamined bool
		if err := r.QueryRow(
			`SELECT EXISTS(
				SELECT 1 FROM events e
				WHERE e.kind=? AND e.object_type='run'
				  AND json_valid(e.payload) AND json_extract(e.payload,'$.decision.item_id')=?
				  AND NOT EXISTS(SELECT 1 FROM events x WHERE x.kind=? AND x.object_type='run' AND x.object_id=e.object_id)
			)`,
			dispatchStartedKind, fixerID, examinationDoneKind,
		).Scan(&unexamined); err != nil {
			return false, err
		}
		if unexamined {
			return true, nil
		}
	}

	laneRows, err := r.Query(`SELECT run_id FROM lanes WHERE state='leased' AND run_id IS NOT NULL`)
	if err != nil {
		return false, err
	}
	var leasedRuns []string
	for laneRows.Next() {
		var runID string
		if err := laneRows.Scan(&runID); err != nil {
			laneRows.Close()
			return false, err
		}
		leasedRuns = append(leasedRuns, runID)
	}
	if err := laneRows.Err(); err != nil {
		laneRows.Close()
		return false, err
	}
	laneRows.Close()

	for _, runID := range leasedRuns {
		for _, fixerID := range fixers {
			belongs, err := runBelongsToItem(r, runID, fixerID, hasReadOnly, hasNodes, hasDispatches)
			if err != nil {
				return false, err
			}
			if belongs {
				return true, nil
			}
		}
	}
	return false, nil
}

func settleFixerRunRow(ctx context.Context, tx *store.Tx, runID string, at time.Time) error {
	var itemID, route, harness, seat, workflow, node sql.NullString
	var laneID, backend, state, artifactRefs string
	var startedMS int64
	var usageTokens sql.NullInt64
	var usageCost sql.NullFloat64
	var eventSeq int64
	err := tx.Reader().QueryRow(
		`SELECT item_id, lane_id, backend, route, harness, seat, workflow, node, state, started_at, artifact_refs, usage_tokens, usage_cost_usd, event_seq
		   FROM runs WHERE run_id = ?`,
		runID,
	).Scan(&itemID, &laneID, &backend, &route, &harness, &seat, &workflow, &node, &state, &startedMS, &artifactRefs, &usageTokens, &usageCost, &eventSeq)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}

	var rec run.Record
	var finishPayload []byte
	err = tx.Reader().QueryRow(
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		run.FinishKind, runID,
	).Scan(&finishPayload)
	if err == nil {
		if err := json.Unmarshal(finishPayload, &rec); err != nil {
			return err
		}
	} else if err != sql.ErrNoRows {
		return err
	}

	rec.RunID = runID
	if rec.ItemID == "" && itemID.Valid {
		rec.ItemID = itemID.String
	}
	if rec.LaneID == "" {
		rec.LaneID = laneID
	}
	if backend != "" {
		rec.Backend = backend
	}
	if rec.Route == "" && route.Valid {
		rec.Route = route.String
	}
	if rec.Harness == "" && harness.Valid {
		rec.Harness = harness.String
	}
	if rec.Seat == "" && seat.Valid {
		rec.Seat = seat.String
	}
	if rec.Workflow == "" && workflow.Valid {
		rec.Workflow = workflow.String
	}
	if rec.Node == "" && node.Valid {
		rec.Node = node.String
	}
	if rec.ArtifactRefs == nil {
		var refs []run.ArtifactRef
		if artifactRefs != "" {
			if err := json.Unmarshal([]byte(artifactRefs), &refs); err != nil {
				refs = []run.ArtifactRef{}
			}
		}
		if refs == nil {
			refs = []run.ArtifactRef{}
		}
		rec.ArtifactRefs = refs
	}
	if rec.Usage == nil && (usageTokens.Valid || usageCost.Valid) {
		u := &run.Usage{}
		if usageTokens.Valid {
			v := usageTokens.Int64
			u.Tokens = &v
		}
		if usageCost.Valid {
			v := usageCost.Float64
			u.CostUSD = &v
		}
		rec.Usage = u
	}

	started := time.UnixMilli(startedMS).UTC()
	if startedMS > 0 {
		rec.StartedAt = started.Format(time.RFC3339)
	} else if rec.StartedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, rec.StartedAt); err == nil {
			started = parsed.UTC()
		}
	} else {
		rec.StartedAt = started.Format(time.RFC3339)
	}
	finished := at.UTC()
	if finished.Before(started) {
		finished = started
	}

	var resumed bool
	if err := tx.Reader().QueryRow(
		`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='run' AND object_id=? AND seq>?)`,
		run.ResumeKind, runID, eventSeq,
	).Scan(&resumed); err != nil {
		return err
	}
	if resumed {
		rec.Resumed = true
	}

	rec.State = run.StateTimedOut
	rec.ExitCode = nil
	rec.EndCause = run.EndLeaseReclaimed
	rec.SettledAt = ""
	rec.HarnessEndedAt = ""
	rec.RepeatingCall = nil
	rec.EndingException = ""
	rec.FinishedAt = finished.Format(time.RFC3339)

	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := run.Apply(ctx, tx, record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          run.RecordKind,
		ID:            fmt.Sprintf("fixer-settle-%s-%d", runID, eventSeq),
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at.UTC(),
		Subject:       &record.Subject{Kind: "run", ID: runID},
		Payload:       body,
	}); err != nil {
		return err
	}

	if hasPauses, err := tableExists(tx.Reader(), "quota_pauses"); err != nil {
		return err
	} else if hasPauses {
		if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id = ?`, runID); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) stopActiveFixerRun(runID string) {
	l.mu.Lock()
	if l.stoppedRuns == nil {
		l.stoppedRuns = map[string]bool{}
	}
	l.stoppedRuns[runID] = true
	cancel := l.activeCancels[runID]
	if cancel == nil {
		delete(l.active, runID)
	}
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if l.cfg.Store != "" {
		here, _ := currentLoopOwner()
		if faces, err := liveRunFaces(l.cfg.Store, runID, here); err == nil {
			for _, face := range faces {
				_ = syscall.Kill(face.PID, syscall.SIGTERM)
			}
		}
	}
}
