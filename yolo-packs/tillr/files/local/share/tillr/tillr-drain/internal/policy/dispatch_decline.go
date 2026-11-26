package policy

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const DispatchDeclinedKind = "drain.dispatch-declined"

func init() {
	if err := event.Register(DispatchDeclinedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

type DispatchDecline struct {
	DecisionID string `json:"decision_id"`
	LaneID     string `json:"lane_id"`
	RunID      string `json:"run_id"`
	Reason     string `json:"reason"`
	WaitingOn  string `json:"waiting_on"`
	Seq        int64  `json:"-"`
}

func IsDispatchAction(action string) bool {
	return action == drainmodel.ActionDispatch || action == drainmodel.ActionRetry
}

// Each decline is an observation, not a hold or a dispatch: it spends no attempt,
// claims no lane, and does not affect what the next pass is allowed to try.
func RecordDispatchDecline(
	ctx context.Context,
	storePath, itemID, decisionID, laneID, runID string,
	at time.Time,
	reason, waitingOn string,
	now func() time.Time,
	writeTx func(context.Context, *store.Store, func(*store.Tx) error) error,
	recordBusy func(string, time.Duration, time.Time),
	reportf func(string, ...any),
) (err error) {
	reportf("loop: declined dispatch of %s on %s: %q\n", itemID, laneID, reason)
	start := time.Now()
	defer func() {
		if store.IsBusy(err) && ctx.Err() == nil {
			recordBusy("dispatch-decline", time.Since(start), now())
			reportf("loop: dispatch decline for %s on %s was not recorded, the store was busy: %v\n", itemID, laneID, err)
			err = nil
		}
	}()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	observation := DispatchDecline{DecisionID: decisionID, LaneID: laneID, RunID: runID, Reason: reason, WaitingOn: waitingOn}
	raw, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	return writeTx(ctx, s, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: DispatchDeclinedKind, ObjectType: "item", ObjectID: &itemID, At: at, Payload: raw})
		return err
	})
}

func DispatchStartedAfter(r *store.Reader, runID string, seq int64) (bool, error) {
	var started bool
	err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE object_id=? AND kind=? AND seq>?)`, runID, runhistory.DispatchStartedKind, seq).Scan(&started)
	return started, err
}

func ReadDispatchDeclines(r *store.Reader) (map[string]DispatchDecline, error) {
	// Why describes claim waits, not application errors (which have no mover).
	rows, err := r.Query(`SELECT object_id, payload, seq FROM events WHERE seq IN
		(SELECT MAX(seq) FROM events WHERE kind=? AND json_extract(payload,'$.waiting_on')<>'' GROUP BY object_id)`, DispatchDeclinedKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]DispatchDecline{}
	for rows.Next() {
		var id string
		var raw []byte
		var decline DispatchDecline
		if err := rows.Scan(&id, &raw, &decline.Seq); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &decline); err != nil {
			return nil, err
		}
		latest[id] = decline
	}
	return latest, rows.Err()
}

func (v DispatchDecline) AppliesTo(r *store.Reader, action, decisionID, laneID, runID string) (bool, error) {
	if v.Seq == 0 || !IsDispatchAction(action) || runID == "" || decisionID != v.DecisionID || laneID != v.LaneID || runID != v.RunID {
		return false, nil
	}
	started, err := DispatchStartedAfter(r, v.RunID, v.Seq)
	return !started, err
}
