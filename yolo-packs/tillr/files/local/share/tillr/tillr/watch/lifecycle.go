package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	acknowledgedKind = "watch.alert.acknowledged"
	silencedKind     = "watch.alert.silenced"
	reopenedKind     = "watch.alert.reopened"
)

// ErrAlertNotFound reports that no episode has the given alert ID.
var ErrAlertNotFound = errors.New("not found")

type episodeAction struct {
	state, fingerprint string
	ackedBy            sql.NullString
	payload            map[string]any
}

func (a episodeAction) refuse(id string) error {
	if a.state == "acknowledged" {
		return fmt.Errorf("alert %s is acknowledged by %s", id, a.ackedBy.String)
	}
	if a.state == "silenced" && a.ackedBy.Valid && a.ackedBy.String != "" {
		return fmt.Errorf("alert %s is silenced (acknowledged by %s)", id, a.ackedBy.String)
	}
	return fmt.Errorf("alert %s is %s", id, a.state)
}

// Ack records an actor's claim on an open episode. It does not evaluate rules.
func (e Evaluator) Ack(ctx context.Context, id, by string, note *string) error {
	return e.act(ctx, id, acknowledgedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		if by == "" {
			return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
		}
		if a.state != "open" {
			return a.refuse(id)
		}
		a.payload["by"], a.payload["note"] = by, note
		_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',acked_by=?,acked_note=?,acked_at=? WHERE alert_id=?`, by, note, now.UnixMilli(), id)
		return err
	})
}

// Silence records new silence terms, retaining any acknowledgement. A nil
// until means to silence until the fingerprint changes.
func (e Evaluator) Silence(ctx context.Context, id string, until, reason *string) error {
	return e.act(ctx, id, silencedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		var deadline, fingerprint any
		a.payload["reason"] = reason
		if until != nil {
			expires, err := silenceTime(*until, now)
			if err != nil {
				return fmt.Errorf("%s: %w", a.refuse(id), err)
			}
			ms := expires.UnixMilli()
			// The stored deadline has millisecond precision. Never expire early.
			if expires.After(time.UnixMilli(ms)) {
				ms++
			}
			deadline = ms
			a.payload["until"] = expires.UTC().Format(time.RFC3339Nano)
		}
		if a.state == "resolved" {
			return a.refuse(id)
		}
		if until == nil {
			if a.fingerprint == "" {
				return fmt.Errorf("%w and has no fingerprint; use --until", a.refuse(id))
			}
			fingerprint = a.fingerprint
			a.payload["until_change"] = true
		}
		_, err := tx.Exec(`UPDATE alert_episodes SET state='silenced',silence_until_ms=?,silence_on_fingerprint=? WHERE alert_id=?`, deadline, fingerprint, id)
		return err
	})
}

func silenceTime(input string, now time.Time) (time.Time, error) {
	var expires time.Time
	if d := parseWindow(input); d > 0 {
		expires = now.Add(d)
	} else {
		var err error
		expires, err = time.Parse(time.RFC3339, input)
		if err != nil {
			return time.Time{}, fmt.Errorf("--until requires a positive whole-number duration in s, m or h, or an RFC 3339 time")
		}
	}
	if !expires.After(now) {
		return time.Time{}, fmt.Errorf("--until must be later than the current time")
	}
	return expires, nil
}

// Resolve records a manual resolution through the store head at the time of
// the action, excluding the resolution event itself.
func (e Evaluator) Resolve(ctx context.Context, id, note string) error {
	return e.act(ctx, id, resolvedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		if note == "" {
			return fmt.Errorf("%w: --reason is required and must not be empty", a.refuse(id))
		}
		if a.state == "resolved" {
			return a.refuse(id)
		}
		seq, err := tx.Reader().EventHeadSeq()
		if err != nil {
			return err
		}
		a.payload["resolved_seq"], a.payload["reason"], a.payload["note"] = seq, "manual", note
		_, err = tx.Exec(`UPDATE alert_episodes SET state='resolved',resolved_seq=?,resolved_at=?,resolved_reason='manual' WHERE alert_id=?`, seq, now.UnixMilli(), id)
		return err
	})
}

// RefuseEpisode reports an invalid command option alongside the episode state
// without editing the store.
func (e Evaluator) RefuseEpisode(ctx context.Context, id string, reason error) error {
	return e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		a, err := readEpisodeAction(rd, id)
		if err != nil {
			return err
		}
		return fmt.Errorf("%s: %w", a.refuse(id), reason)
	})
}

func readEpisodeAction(rd *store.Reader, id string) (episodeAction, error) {
	var a episodeAction
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil {
		return a, err
	}
	if !hasAlerts {
		return a, fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	var rule, subject, severity string
	err = rd.QueryRow(`SELECT state,condition_fingerprint,acked_by,rule_id,subject_key,severity FROM alert_episodes WHERE alert_id=?`, id).Scan(&a.state, &a.fingerprint, &a.ackedBy, &rule, &subject, &severity)
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("alert %s %w", id, ErrAlertNotFound)
	}
	if err != nil {
		return a, err
	}
	a.payload = map[string]any{"alert_id": id, "rule_id": rule, "subject_key": subject, "severity": severity}
	return a, nil
}

func (e Evaluator) act(ctx context.Context, id, kind string, change func(*store.Tx, episodeAction, time.Time) error) error {
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		a, err := readEpisodeAction(tx.Reader(), id)
		if err != nil {
			return err
		}
		now := e.now()
		if err := change(tx, a, now); err != nil {
			return err
		}
		return appendAlert(tx, id, kind, now, a.payload)
	})
}

func appendAlert(tx *store.Tx, id, kind string, now time.Time, body map[string]any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, At: now, ObjectType: "alert", ObjectID: &id, Payload: payload})
	return err
}
