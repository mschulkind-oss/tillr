package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	acknowledgedKind = "watch.alert.acknowledged"
	trackedKind      = "watch.alert.tracked"
	silencedKind     = "watch.alert.silenced"
	reopenedKind     = "watch.alert.reopened"
)

// ErrAlertNotFound reports that no episode has the given alert ID.
var ErrAlertNotFound = errors.New("not found")

type episodeAction struct {
	state, fingerprint string
	ackedBy            sql.NullString
	ackedNote          sql.NullString
	trackingItemID     sql.NullString
	hasTrackingCol     bool
	hasLagPairCol      bool
	payload            map[string]any
}

func (a episodeAction) refuse(id string) error {
	if a.state == "acknowledged" {
		msg := fmt.Sprintf("alert %s is acknowledged", id)
		if a.ackedBy.Valid && a.ackedBy.String != "" {
			msg += " by " + a.ackedBy.String
		}
		if a.trackingItemID.Valid && a.trackingItemID.String != "" {
			msg += fmt.Sprintf(" (tracked by %s)", a.trackingItemID.String)
		}
		return errors.New(msg)
	}
	if a.state == "silenced" && a.ackedBy.Valid && a.ackedBy.String != "" {
		return fmt.Errorf("alert %s is silenced (acknowledged by %s)", id, a.ackedBy.String)
	}
	return fmt.Errorf("alert %s is %s", id, a.state)
}

func validateStoreOpenItem(rd *store.Reader, a episodeAction, id, itemID string) error {
	hasItems, err := tableExists(rd, "items")
	if err != nil {
		return err
	}
	if !hasItems {
		return fmt.Errorf("%w: items table does not exist", a.refuse(id))
	}
	rec, err := definition.ForReader(rd)
	if err != nil {
		return fmt.Errorf("%w: %w", a.refuse(id), err)
	}
	var st sql.NullString
	err = rd.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: item %s not found", a.refuse(id), itemID)
	}
	if err != nil {
		return err
	}
	d := rec.Parsed
	if st.String == d.Closed || slices.Contains(d.Finished, st.String) {
		return fmt.Errorf("%w: item %s is %s; tracking requires an open item", a.refuse(id), itemID, st.String)
	}
	return nil
}

func ensureTrackingColumns(tx *store.Tx, a episodeAction) error {
	if !a.hasTrackingCol {
		if _, err := tx.Exec(`ALTER TABLE alert_episodes ADD COLUMN tracking_item_id TEXT`); err != nil {
			return err
		}
	}
	if !a.hasLagPairCol {
		if _, err := tx.Exec(`ALTER TABLE alert_episodes ADD COLUMN tracked_lag_pair TEXT`); err != nil {
			return err
		}
	}
	return nil
}

// NeedsTrackingMigration validates a track or ack-with-item operation read-only
// and reports whether alert_episodes exists without tracking_item_id so a CLI
// caller can upgrade via store.OpenMigrated only after all refusals pass.
func (e Evaluator) NeedsTrackingMigration(ctx context.Context, isTrack bool, id, itemID string, by *string) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		a, err := readEpisodeAction(rd, id)
		if err != nil {
			return err
		}
		if isTrack {
			if itemID == "" {
				return fmt.Errorf("%w: --item is required and must not be empty", a.refuse(id))
			}
			if by != nil && *by == "" {
				return fmt.Errorf("%w: --by must not be empty", a.refuse(id))
			}
			if a.state == "resolved" {
				return a.refuse(id)
			}
		} else {
			if by == nil || *by == "" {
				return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
			}
			if itemID == "" {
				return fmt.Errorf("%w: --item must not be empty", a.refuse(id))
			}
			if a.state != "open" {
				return a.refuse(id)
			}
		}
		if err := validateStoreOpenItem(rd, a, id, itemID); err != nil {
			return err
		}
		need = !a.hasTrackingCol
		return nil
	})
	return need, err
}

// Ack records an actor's claim on an open episode. It does not evaluate rules.
func (e Evaluator) Ack(ctx context.Context, id, by string, note *string) error {
	return e.AckWithItem(ctx, id, by, note, nil)
}

// AckWithItem records an actor's claim on an open episode, optionally attaching
// an open queue item that tracks its cause. It does not evaluate rules.
func (e Evaluator) AckWithItem(ctx context.Context, id, by string, note, itemID *string) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.act(id, acknowledgedKind, now, func(ep *memEpisode, a episodeAction) error {
			if by == "" {
				return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
			}
			if itemID != nil && *itemID == "" {
				return fmt.Errorf("%w: --item must not be empty", a.refuse(id))
			}
			if a.state != "open" {
				return a.refuse(id)
			}
			if itemID != nil {
				if err := validateSourceOpenItem(ctx, e.Source, a, id, *itemID); err != nil {
					return err
				}
				ep.trackingItemID = sql.NullString{String: *itemID, Valid: true}
				ep.silenceUntilMS = sql.NullInt64{}
				ep.silenceOnFingerprint = sql.NullString{}
				a.payload["item_id"] = *itemID
			}
			a.payload["by"], a.payload["note"] = by, note
			ep.state = "acknowledged"
			ep.ackedBy = sql.NullString{String: by, Valid: true}
			if note != nil {
				ep.ackedNote = sql.NullString{String: *note, Valid: true}
			} else {
				ep.ackedNote = sql.NullString{}
			}
			ep.ackedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
			return nil
		})
	}
	return e.act(ctx, id, acknowledgedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		if by == "" {
			return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
		}
		if itemID != nil && *itemID == "" {
			return fmt.Errorf("%w: --item must not be empty", a.refuse(id))
		}
		if a.state != "open" {
			return a.refuse(id)
		}
		a.payload["by"], a.payload["note"] = by, note
		if itemID == nil {
			_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',acked_by=?,acked_note=?,acked_at=? WHERE alert_id=?`, by, note, now.UnixMilli(), id)
			return err
		}
		if err := validateStoreOpenItem(tx.Reader(), a, id, *itemID); err != nil {
			return err
		}
		if err := ensureTrackingColumns(tx, a); err != nil {
			return err
		}
		a.payload["item_id"] = *itemID
		_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',acked_by=?,acked_note=?,acked_at=?,tracking_item_id=?,silence_until_ms=NULL,silence_on_fingerprint=NULL WHERE alert_id=?`,
			by, note, now.UnixMilli(), *itemID, id)
		return err
	})
}

// Track attaches or updates the open queue item tracking an unresolved alert
// episode and records watch.alert.tracked. It does not evaluate rules.
func (e Evaluator) Track(ctx context.Context, id, itemID string, by, note *string) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.act(id, trackedKind, now, func(ep *memEpisode, a episodeAction) error {
			if itemID == "" {
				return fmt.Errorf("%w: --item is required and must not be empty", a.refuse(id))
			}
			if by != nil && *by == "" {
				return fmt.Errorf("%w: --by must not be empty", a.refuse(id))
			}
			if a.state == "resolved" {
				return a.refuse(id)
			}
			if err := validateSourceOpenItem(ctx, e.Source, a, id, itemID); err != nil {
				return err
			}
			a.payload["item_id"] = itemID
			a.payload["by"] = by
			a.payload["note"] = note
			ep.state = "acknowledged"
			ep.trackingItemID = sql.NullString{String: itemID, Valid: true}
			ep.silenceUntilMS = sql.NullInt64{}
			ep.silenceOnFingerprint = sql.NullString{}
			if by != nil {
				ep.ackedBy = sql.NullString{String: *by, Valid: true}
				ep.ackedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
				if note != nil {
					ep.ackedNote = sql.NullString{String: *note, Valid: true}
				} else {
					ep.ackedNote = sql.NullString{}
				}
			} else if note != nil {
				ep.ackedNote = sql.NullString{String: *note, Valid: true}
			}
			return nil
		})
	}
	return e.act(ctx, id, trackedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		if itemID == "" {
			return fmt.Errorf("%w: --item is required and must not be empty", a.refuse(id))
		}
		if by != nil && *by == "" {
			return fmt.Errorf("%w: --by must not be empty", a.refuse(id))
		}
		if a.state == "resolved" {
			return a.refuse(id)
		}
		if err := validateStoreOpenItem(tx.Reader(), a, id, itemID); err != nil {
			return err
		}
		if err := ensureTrackingColumns(tx, a); err != nil {
			return err
		}
		a.payload["item_id"] = itemID
		a.payload["by"] = by
		a.payload["note"] = note
		if by != nil {
			_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',tracking_item_id=?,acked_by=?,acked_note=?,acked_at=?,silence_until_ms=NULL,silence_on_fingerprint=NULL WHERE alert_id=?`,
				itemID, *by, note, now.UnixMilli(), id)
			return err
		}
		if note != nil {
			_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',tracking_item_id=?,acked_note=?,silence_until_ms=NULL,silence_on_fingerprint=NULL WHERE alert_id=?`,
				itemID, *note, id)
			return err
		}
		_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',tracking_item_id=?,silence_until_ms=NULL,silence_on_fingerprint=NULL WHERE alert_id=?`,
			itemID, id)
		return err
	})
}

// Silence records new silence terms, retaining any acknowledgement. A nil
// until means to silence until the fingerprint changes.
func (e Evaluator) Silence(ctx context.Context, id string, until, reason *string) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.act(id, silencedKind, now, func(ep *memEpisode, a episodeAction) error {
			var deadline sql.NullInt64
			var fp sql.NullString
			a.payload["reason"] = reason
			if until != nil {
				expires, err := silenceTime(*until, now)
				if err != nil {
					return fmt.Errorf("%s: %w", a.refuse(id), err)
				}
				ms := expires.UnixMilli()
				if expires.After(time.UnixMilli(ms)) {
					ms++
				}
				deadline = sql.NullInt64{Int64: ms, Valid: true}
				a.payload["until"] = expires.UTC().Format(time.RFC3339Nano)
			}
			if a.state == "resolved" {
				return a.refuse(id)
			}
			if a.trackingItemID.Valid && a.trackingItemID.String != "" {
				return fmt.Errorf("%w: cannot silence an item-tracked alert", a.refuse(id))
			}
			if until == nil {
				if a.fingerprint == "" {
					return fmt.Errorf("%w and has no fingerprint; use --until", a.refuse(id))
				}
				fp = sql.NullString{String: a.fingerprint, Valid: true}
				a.payload["until_change"] = true
			}
			ep.state = "silenced"
			ep.silenceUntilMS = deadline
			ep.silenceOnFingerprint = fp
			return nil
		})
	}
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
		if a.trackingItemID.Valid && a.trackingItemID.String != "" {
			return fmt.Errorf("%w: cannot silence an item-tracked alert", a.refuse(id))
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
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.act(id, resolvedKind, now, func(ep *memEpisode, a episodeAction) error {
			if note == "" {
				return fmt.Errorf("%w: --reason is required and must not be empty", a.refuse(id))
			}
			if a.state == "resolved" {
				return a.refuse(id)
			}
			seq := max(e.mem.lastSourceSeq, ep.lastObservedSeq)
			a.payload["resolved_seq"], a.payload["reason"], a.payload["note"] = seq, "manual", note
			if a.trackingItemID.Valid && a.trackingItemID.String != "" {
				a.payload["item_id"] = a.trackingItemID.String
			}
			ep.state = "resolved"
			ep.resolvedSeq = sql.NullInt64{Int64: seq, Valid: true}
			ep.resolvedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
			ep.resolvedReason = sql.NullString{String: "manual", Valid: true}
			return nil
		})
	}
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
		if a.trackingItemID.Valid && a.trackingItemID.String != "" {
			a.payload["item_id"] = a.trackingItemID.String
		}
		_, err = tx.Exec(`UPDATE alert_episodes SET state='resolved',resolved_seq=?,resolved_at=?,resolved_reason='manual' WHERE alert_id=?`, seq, now.UnixMilli(), id)
		return err
	})
}

// RefuseEpisode reports an invalid command option alongside the episode state
// without editing the store.
func (e Evaluator) RefuseEpisode(ctx context.Context, id string, reason error) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.refuseEpisode(id, reason)
	}
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
	hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
	if err != nil {
		return a, err
	}
	hasLagPair, err := alertEpisodesHasColumn(rd, "tracked_lag_pair")
	if err != nil {
		return a, err
	}
	a.hasTrackingCol = hasTracking
	a.hasLagPairCol = hasLagPair
	var rule, subject, severity string
	if hasTracking {
		err = rd.QueryRow(`SELECT state,condition_fingerprint,acked_by,acked_note,tracking_item_id,rule_id,subject_key,severity FROM alert_episodes WHERE alert_id=?`, id).
			Scan(&a.state, &a.fingerprint, &a.ackedBy, &a.ackedNote, &a.trackingItemID, &rule, &subject, &severity)
	} else {
		err = rd.QueryRow(`SELECT state,condition_fingerprint,acked_by,acked_note,rule_id,subject_key,severity FROM alert_episodes WHERE alert_id=?`, id).
			Scan(&a.state, &a.fingerprint, &a.ackedBy, &a.ackedNote, &rule, &subject, &severity)
	}
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
