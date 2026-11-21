package drain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type landingCallLanding struct {
	Commit string `json:"commit"`
}

type landingCallClose struct {
	Reason string `json:"reason"`
}

// An open call can land without receiving an answer. A later proof closes it
// silently; a revert can still answer it until then.
func projectLandingCall(tx *store.Tx, id, landed, outcome string, at time.Time) error {
	for _, kind := range []string{callAnswered, callClosed} {
		seen, err := judgmentSeen(tx, kind, "call", id)
		if err != nil {
			return err
		}
		if seen != 0 {
			return nil
		}
	}
	if landed != "" {
		seen, err := judgmentSeen(tx, callLanded, "call", id)
		if err != nil {
			return err
		}
		if seen == 0 {
			if err := appendLandingCallEvent(tx, callLanded, id, landingCallLanding{Commit: landed}, at); err != nil {
				return err
			}
		}
	}
	if choice := chooseLandingCallAnswer(outcome); choice.Close != "" {
		return appendLandingCallEvent(tx, callClosed, id, landingCallClose{Reason: choice.Close}, at)
	}
	return nil
}

func recordLandingCallLanding(tx *store.Tx, runID, commit string, at time.Time) error {
	if commit == "" {
		return nil
	}
	var id string
	err := tx.QueryRow(`SELECT object_id FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, runID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return projectLandingCall(tx, id, commit, "", at)
}

// Reconcile from durable landing rows, item state and the green ref, so a loop
// replacement between the native state transition and the call event can catch up.
func (l *Loop) reconcileLandingCalls(ctx context.Context) error {
	type openCall struct{ id, itemID, state, landed, recorded string }
	var calls []openCall
	var closedState string
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT o.object_id, i.item_id, i.state,
    COALESCE((SELECT trunk_after FROM landings WHERE run_id=json_extract(o.payload,'$.run_id') AND result='landed' ORDER BY event_seq DESC LIMIT 1),''),
    COALESCE((SELECT json_extract(payload,'$.commit') FROM events WHERE object_type='call' AND object_id=o.object_id AND kind=?),'')
    FROM events o JOIN items i ON i.item_id=json_extract(o.payload,'$.item_id')
    WHERE o.kind=? AND o.object_type='call' AND NOT EXISTS
    (SELECT 1 FROM events e WHERE e.object_type='call' AND e.object_id=o.object_id AND e.kind IN (?,?)) ORDER BY o.seq`, callLanded, callOffered, callAnswered, callClosed)
		if err != nil {
			return err
		}

		for rows.Next() {
			var c openCall
			if err := rows.Scan(&c.id, &c.itemID, &c.state, &c.landed, &c.recorded); err != nil {
				rows.Close()
				return err
			}
			calls = append(calls, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || len(calls) == 0 {
			return err
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		closedState = declared.Parsed.Closed
		for i := range calls {
			c := &calls[i]
			for _, sha := range []*string{&c.landed, &c.recorded} {
				if *sha != "" {
					if *sha, err = heavy.RewriteResolve(r, *sha); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	db.Close()
	if err != nil || len(calls) == 0 {
		return err
	}
	var green string
	greenChecked := false
	for _, c := range calls {
		landed := c.recorded
		if landed == "" {
			landed = c.landed
		}
		outcome := ""
		if landed != "" {
			if !greenChecked {
				green, err = heavyGreen(ctx, l.cfg.Root)
				if err != nil {
					return err
				}
				greenChecked = true
			}
			if green != "" {
				proven, err := containsCommit(ctx, l.cfg.Root, landed, green)
				if err != nil {
					return err
				}
				if proven {
					outcome = "proven"
				}
			}
		}
		// Read the item state again inside the write, rather than closing on a stale
		// snapshot of a board item the owner might have reopened.
		if landed == c.recorded && outcome == "" && c.state != closedState && c.state != StateResolvedElsewhere {
			continue
		}
		db, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		err = db.WriteTx(ctx, func(tx *store.Tx) error {
			var state string
			if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, c.itemID).Scan(&state); err != nil {
				return err
			}
			declared, err := definition.ForReader(tx.Reader())
			if err != nil {
				return err
			}
			switch state {
			case declared.Parsed.Closed:
				outcome = "withdrawn"
			case StateResolvedElsewhere:
				outcome = "superseded"
			}
			return projectLandingCall(tx, c.id, landed, outcome, l.cfg.Now().UTC())
		})
		db.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) recordLandingCallLanded(ctx context.Context, runID, commit string) error {
	if commit == "" {
		return nil
	}
	reader, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var offered bool
	err = reader.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?)`, callOffered, runID).Scan(&offered)
	})
	reader.Close()
	if err != nil || !offered {
		return err
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.WriteTx(ctx, func(tx *store.Tx) error { return recordLandingCallLanding(tx, runID, commit, l.cfg.Now().UTC()) })
}
