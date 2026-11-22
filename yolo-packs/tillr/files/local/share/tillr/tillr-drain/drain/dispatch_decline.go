package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const dispatchDeclinedKind = "drain.dispatch-declined"

func init() {
	if err := event.Register(dispatchDeclinedKind, Utility); err != nil {
		panic(err)
	}
}

type dispatchDecline struct {
	DecisionID string `json:"decision_id"`
	LaneID     string `json:"lane_id"`
	RunID      string `json:"run_id"`
	Reason     string `json:"reason"`
	WaitingOn  string `json:"waiting_on"`
	seq        int64
}

func isDispatch(d Decision) bool { return d.Action == ActionDispatch || d.Action == ActionRetry }

// Each decline is an observation, not a hold or a dispatch: it spends no attempt,
// claims no lane, and does not affect what the next pass is allowed to try.
func (l *Loop) recordDispatchDecline(ctx context.Context, d Decision, at time.Time, reason, waitingOn string) (err error) {
	l.reportf("loop: declined dispatch of %s on %s: %q\n", d.ItemID, d.LaneID, reason)
	defer func() {
		if store.IsBusy(err) && ctx.Err() == nil {
			l.reportf("loop: dispatch decline for %s on %s was not recorded, the store was busy: %v\n", d.ItemID, d.LaneID, err)
			err = nil
		}
	}()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	observation := dispatchDecline{DecisionID: d.DecisionID, LaneID: d.LaneID, Reason: reason, WaitingOn: waitingOn}
	if d.RunRequest != nil {
		observation.RunID = d.RunRequest.RunID
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: dispatchDeclinedKind, ObjectType: "item", ObjectID: &d.ItemID, At: at, Payload: raw})
		return err
	})
}

func dispatchStartedAfter(r *store.Reader, runID string, seq int64) (bool, error) {
	var started bool
	err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE object_id=? AND kind=? AND seq>?)`, runID, dispatchStartedKind, seq).Scan(&started)
	return started, err
}

func readDispatchDeclines(r *store.Reader) (map[string]dispatchDecline, error) {
	// Why describes claim waits, not application errors (which have no mover).
	rows, err := r.Query(`SELECT object_id, payload, seq FROM events WHERE seq IN
		(SELECT MAX(seq) FROM events WHERE kind=? AND json_extract(payload,'$.waiting_on')<>'' GROUP BY object_id)`, dispatchDeclinedKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	latest := map[string]dispatchDecline{}
	for rows.Next() {
		var id string
		var raw []byte
		var decline dispatchDecline
		if err := rows.Scan(&id, &raw, &decline.seq); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &decline); err != nil {
			return nil, err
		}
		latest[id] = decline
	}
	return latest, rows.Err()
}

func (v dispatchDecline) appliesTo(r *store.Reader, d Decision) (bool, error) {
	if v.seq == 0 || !isDispatch(d) || d.RunRequest == nil || d.DecisionID != v.DecisionID || d.LaneID != v.LaneID || d.RunRequest.RunID != v.RunID {
		return false, nil
	}
	started, err := dispatchStartedAfter(r, v.RunID, v.seq)
	return !started, err
}
