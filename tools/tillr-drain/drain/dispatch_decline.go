package drain

import (
	"context"
	"database/sql"
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

func (l *Loop) logDispatchDecline(d Decision, reason string) {
	l.reportf("loop: declined dispatch of %s on %s: %q\n", d.ItemID, d.LaneID, reason)
}

// A decline is an observation, not a hold or a dispatch: it spends no attempt,
// claims no lane, and does not affect what the next pass is allowed to try.
func (l *Loop) recordDispatchDecline(ctx context.Context, d Decision, at time.Time, reason, waitingOn string) error {
	l.logDispatchDecline(d, reason)
	if d.RunRequest == nil {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	observation := dispatchDecline{DecisionID: d.DecisionID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Reason: reason, WaitingOn: waitingOn}
	raw, err := json.Marshal(observation)
	if err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var last string
		var seq int64
		err := tx.QueryRow(`SELECT payload, seq FROM events WHERE object_id=? AND kind=? ORDER BY seq DESC LIMIT 1`, d.ItemID, dispatchDeclinedKind).Scan(&last, &seq)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if last == string(raw) {
			started, err := dispatchStartedAfter(tx.Reader(), observation.RunID, seq)
			if err != nil || !started {
				return err
			}
		}
		_, err = tx.Append(event.Event{Kind: dispatchDeclinedKind, ObjectType: "item", ObjectID: &d.ItemID, At: at, Payload: raw})
		return err
	})
}

func dispatchStartedAfter(r *store.Reader, runID string, seq int64) (bool, error) {
	var started bool
	err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE object_id=? AND kind=? AND seq>?)`, runID, dispatchStartedKind, seq).Scan(&started)
	return started, err
}

func readDispatchDeclines(r *store.Reader) (map[string]dispatchDecline, error) {
	rows, err := r.Query(`SELECT object_id, payload, seq FROM events WHERE seq IN
		(SELECT MAX(seq) FROM events WHERE kind=? GROUP BY object_id)`, dispatchDeclinedKind)
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
