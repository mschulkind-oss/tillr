package runhistory

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const RunSupersededKind = "drain.run-superseded"

func init() {
	if err := event.Register(RunSupersededKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

type RunSuperseded struct {
	Reason   string `json:"reason"`
	PriorSeq int64  `json:"prior_seq,omitempty"`
}

func TableExists(r *store.Reader, name string) (bool, error) {
	var exists bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&exists)
	return exists, err
}

func IsTerminalRunState(state string) bool {
	return state == run.StateExited || state == run.StateFailedToStart || state == run.StateTimedOut
}

// EndNonTerminalRunRow closes one non-terminal runs row (e.g. paused or running) through
// run.Apply so runs, run.finish, read_only_runs, and matching lanes update canonically.
// When markSuperseded is true, it appends drain.run-superseded with the row's prior
// event_seq first so readPriorRuns does not reorder this run ahead of newer attempts.
func EndNonTerminalRunRow(ctx context.Context, tx *store.Tx, runID, reason string, markSuperseded bool, at time.Time) error {
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
	if IsTerminalRunState(state) {
		return nil
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
	if rec.Backend == "" {
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
	if rec.StartedAt != "" {
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

	rec.State = run.StateTimedOut
	rec.ExitCode = nil
	rec.EndCause = run.EndLeaseReclaimed
	rec.SettledAt = ""
	rec.HarnessEndedAt = ""
	rec.RepeatingCall = nil
	rec.EndingException = ""
	rec.FinishedAt = finished.Format(time.RFC3339)

	if markSuperseded {
		if err := AppendEffect(tx, RunSupersededKind, runID, RunSuperseded{Reason: reason, PriorSeq: eventSeq}, at.UTC()); err != nil {
			return err
		}
	}

	body, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := run.Apply(ctx, tx, record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          run.RecordKind,
		ID:            "run-end-" + runID,
		Producer:      record.Producer{System: record.System, Utility: drainmodel.Utility, Version: drainmodel.Version},
		CreatedAt:     at.UTC(),
		Subject:       &record.Subject{Kind: "run", ID: runID},
		Payload:       body,
	}); err != nil {
		return err
	}

	if hasPauses, err := TableExists(tx.Reader(), "quota_pauses"); err != nil {
		return err
	} else if hasPauses {
		if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id = ?`, runID); err != nil {
			return err
		}
	}
	return nil
}

func AwaitBuildReader(ctx context.Context, storePath, id string, now func() time.Time, hasLiveWorkers func(storePath, runID string) (bool, error)) error {
	for {
		s, err := store.OpenRead(ctx, storePath)
		if err != nil {
			return err
		}
		var rec *run.Record
		err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; rec, err = TerminalRecord(r, id); return err })
		s.Close()
		if err != nil {
			return err
		}
		hasLive, err := hasLiveWorkers(storePath, id)
		if err != nil {
			return err
		}
		if !hasLive {
			// A killed face can fail before writing run.finish. Its reserved replacement
			// still owns the group; settle only the vanished old membership, and end any
			// non-terminal (e.g. paused) runs row for this replaced reader.
			if rec == nil || !IsTerminalRunState(rec.State) {
				db, err := store.Open(ctx, storePath, true)
				if err != nil {
					return err
				}
				err = db.WriteTx(ctx, func(tx *store.Tx) error {
					if hasReadOnly, err := TableExists(tx.Reader(), "read_only_runs"); err != nil {
						return err
					} else if hasReadOnly {
						if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, id); err != nil {
							return err
						}
					}
					if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE run_id=? AND state=?`, run.LaneStateIdle, id, run.LaneStateLeased); err != nil {
						return err
					}
					if rec != nil && !IsTerminalRunState(rec.State) {
						return EndNonTerminalRunRow(ctx, tx, id, "superseded by build reader restart", true, now().UTC())
					}
					return nil
				})
				db.Close()
				if err != nil {
					return err
				}
			}
			return nil
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
