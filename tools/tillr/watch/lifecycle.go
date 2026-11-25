package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	investigatedKind = "watch.alert.investigated"
	settledKind      = "watch.alert.settled"
)

// ErrAlertNotFound reports that no episode has the given alert ID.
var ErrAlertNotFound = errors.New("not found")

// ErrInvestigationRequired reports that settle was attempted without a recorded
// investigation or inline cause and evidence.
var ErrInvestigationRequired = errors.New("investigation required before settlement")

// SettleOptions configures Evaluator.Settle.
type SettleOptions struct {
	By       string
	Outcome  string // "fix-rule" | "fix-fault"
	ItemID   string
	LinkKind string // optional: "filed" | "linked"
	Cause    string // optional inline investigation cause
	Evidence string // optional inline investigation evidence
}

var settlementColumnNames = []string{
	"settlement_state",
	"investigated_by",
	"investigated_at",
	"investigation_cause",
	"investigation_evidence",
	"outcome",
	"item_link_kind",
	"settled_by",
	"settled_at",
	"settled_seq",
	"settled_fingerprint",
	"settled_lag_pair",
	"reopen_count_for_item",
}

func hasSettlementColumns(rd *store.Reader) (bool, error) {
	for _, col := range settlementColumnNames {
		ok, err := alertEpisodesHasColumn(rd, col)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

type episodeAction struct {
	state, fingerprint string
	lastObservedSeq    int64
	ackedBy            sql.NullString
	ackedNote          sql.NullString
	trackingItemID     sql.NullString
	trackedLagPair     sql.NullString
	settlementState    string
	outcome            sql.NullString
	settledBy          sql.NullString
	hasTrackingCol     bool
	hasLagPairCol      bool
	hasAckCols         bool
	hasSettlementCols  bool
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

func validateStoreSettleItem(rd *store.Reader, a episodeAction, id, itemID string) error {
	hasItems, err := tableExists(rd, "items")
	if err != nil {
		return err
	}
	if !hasItems {
		// Standalone watch store without an items table accepts any non-empty reference (D-AW10).
		return nil
	}
	rec, err := definition.ForReader(rd)
	if err != nil {
		return fmt.Errorf("%w: %w", a.refuse(id), err)
	}
	return validateStoreSettleItemWithItems(rd, a, id, itemID, rec.Parsed)
}

func validateStoreSettleItemWithItems(rd *store.Reader, a episodeAction, id, itemID string, d definition.Definition) error {
	var hasEventSeq bool
	if err := rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name = 'event_seq')`).Scan(&hasEventSeq); err != nil {
		return err
	}
	var st sql.NullString
	var itemSeq int64
	var err error
	if hasEventSeq {
		err = rd.QueryRow(`SELECT state, COALESCE(event_seq, 0) FROM items WHERE item_id = ?`, itemID).Scan(&st, &itemSeq)
	} else {
		err = rd.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&st)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: item %s not found", a.refuse(id), itemID)
	}
	if err != nil {
		return err
	}
	if st.String == d.Closed {
		return fmt.Errorf("%w: item %s is %s; cannot settle with a closed item", a.refuse(id), itemID, st.String)
	}
	if slices.Contains(d.Finished, st.String) {
		if itemSeq == 0 {
			hasEvents, err := tableExists(rd, "events")
			if err != nil {
				return err
			}
			if hasEvents {
				if err := rd.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM events WHERE object_type = 'item' AND object_id = ?`, itemID).Scan(&itemSeq); err != nil {
					return err
				}
			}
		}
		if itemSeq <= a.lastObservedSeq {
			return fmt.Errorf("%w: item %s is %s (seq %d <= last observed seq %d); file a new item", a.refuse(id), itemID, st.String, itemSeq, a.lastObservedSeq)
		}
	}
	return nil
}

func ensureAckColumns(tx *store.Tx, a episodeAction) error {
	if !a.hasLagPairCol {
		if _, err := tx.Exec(`ALTER TABLE alert_episodes ADD COLUMN tracked_lag_pair TEXT`); err != nil {
			return err
		}
	}
	if !a.hasAckCols {
		for _, col := range []struct{ name, decl string }{
			{"acked_seq", "INTEGER"},
			{"acked_fingerprint", "TEXT"},
			{"acked_lag_pair", "TEXT"},
		} {
			exists, err := alertEpisodesHasColumn(tx.Reader(), col.name)
			if err != nil {
				return err
			}
			if !exists {
				if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE alert_episodes ADD COLUMN %s %s", col.name, col.decl)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// NeedsAckMigration validates an ack operation read-only and reports whether
// alert_episodes exists without the single-instance ack or settlement columns so a
// CLI caller can upgrade via store.OpenMigrated only after all refusals pass.
func (e Evaluator) NeedsAckMigration(ctx context.Context, id, by string) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		a, err := readEpisodeAction(rd, id)
		if err != nil {
			return err
		}
		if by == "" {
			return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
		}
		if a.state != "open" {
			return a.refuse(id)
		}
		need = !a.hasAckCols || !a.hasLagPairCol || !a.hasSettlementCols
		return nil
	})
	return need, err
}

func validateInvestigationFields(a episodeAction, id, by, cause, evidence string) error {
	if strings.TrimSpace(by) == "" {
		return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
	}
	trimmedCause := strings.TrimSpace(cause)
	if trimmedCause == "" {
		return fmt.Errorf("%w: --cause is required and must not be empty", a.refuse(id))
	}
	if strings.EqualFold(trimmedCause, "unknown") {
		return fmt.Errorf("%w: --cause must not be %q", a.refuse(id), "unknown")
	}
	if strings.TrimSpace(evidence) == "" {
		return fmt.Errorf("%w: --evidence is required and must not be empty", a.refuse(id))
	}
	return nil
}

// NeedsInvestigateMigration validates an investigate operation read-only and reports
// whether alert_episodes lacks the settlement columns.
func (e Evaluator) NeedsInvestigateMigration(ctx context.Context, id, by, cause, evidence string) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		a, err := readEpisodeAction(rd, id)
		if err != nil {
			return err
		}
		if err := validateInvestigationFields(a, id, by, cause, evidence); err != nil {
			return err
		}
		if a.settlementState == "settled" {
			return fmt.Errorf("alert %s is already settled", id)
		}
		need = !a.hasSettlementCols
		return nil
	})
	return need, err
}

// NeedsSettleMigration validates a settle operation read-only and reports
// whether alert_episodes lacks the settlement or tracking columns.
func (e Evaluator) NeedsSettleMigration(ctx context.Context, id string, opts SettleOptions) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		a, err := readEpisodeAction(rd, id)
		if err != nil {
			return err
		}
		if err := validateSettleOptions(a, id, opts); err != nil {
			return err
		}
		if a.settlementState == "settled" {
			if err := checkSettleConflict(a, id, opts); err != nil {
				return err
			}
			need = !a.hasSettlementCols || !a.hasTrackingCol || !a.hasLagPairCol
			return nil
		}
		if opts.Cause != "" || opts.Evidence != "" {
			if err := validateInvestigationFields(a, id, opts.By, opts.Cause, opts.Evidence); err != nil {
				return err
			}
		} else if a.settlementState != "investigated" {
			return fmt.Errorf("%w: %s", ErrInvestigationRequired, a.refuse(id))
		}
		if err := validateStoreSettleItem(rd, a, id, opts.ItemID); err != nil {
			return err
		}
		need = !a.hasSettlementCols || !a.hasTrackingCol || !a.hasLagPairCol
		return nil
	})
	return need, err
}

func validateSettleOptions(a episodeAction, id string, opts SettleOptions) error {
	if strings.TrimSpace(opts.By) == "" {
		return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
	}
	if opts.Outcome != "fix-rule" && opts.Outcome != "fix-fault" {
		return fmt.Errorf("%w: --outcome must be fix-rule or fix-fault", a.refuse(id))
	}
	if strings.TrimSpace(opts.ItemID) == "" {
		return fmt.Errorf("%w: --item is required and must not be empty", a.refuse(id))
	}
	if opts.LinkKind != "" && opts.LinkKind != "filed" && opts.LinkKind != "linked" {
		return fmt.Errorf("%w: --link-kind must be filed or linked", a.refuse(id))
	}
	return nil
}

func checkSettleConflict(a episodeAction, id string, opts SettleOptions) error {
	if a.settledBy.String == opts.By && a.outcome.String == opts.Outcome && a.trackingItemID.String == opts.ItemID {
		return nil
	}
	return fmt.Errorf("alert %s is already settled by %s (outcome=%s, item=%s)", id, a.settledBy.String, a.outcome.String, a.trackingItemID.String)
}

// Ack records an actor's single-instance claim on an open episode (recording
// acked_seq, acked_fingerprint, and acked_lag_pair) and moves settlement_state
// from uninvestigated to investigating. It does not evaluate rules.
func (e Evaluator) Ack(ctx context.Context, id, by string, note *string) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.act(id, acknowledgedKind, now, func(ep *memEpisode, a episodeAction) error {
			if by == "" {
				return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
			}
			if a.state != "open" {
				return a.refuse(id)
			}
			a.payload["by"], a.payload["note"] = by, note
			ep.state = "acknowledged"
			if ep.settlementState == "" || ep.settlementState == "uninvestigated" {
				ep.settlementState = "investigating"
			}
			ep.ackedBy = sql.NullString{String: by, Valid: true}
			if note != nil {
				ep.ackedNote = sql.NullString{String: *note, Valid: true}
			} else {
				ep.ackedNote = sql.NullString{}
			}
			ep.ackedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
			ackedSeq := ep.lastObservedSeq
			if ep.trackedLagPair.Valid && ep.trackedLagPair.String != "" {
				ackedSeq = max(ackedSeq, e.mem.lastSourceSeq)
			}
			ep.ackedSeq = sql.NullInt64{Int64: ackedSeq, Valid: true}
			ep.ackedFingerprint = sql.NullString{String: ep.conditionFingerprint, Valid: true}
			ep.ackedLagPair = ep.trackedLagPair
			return nil
		})
	}
	return e.act(ctx, id, acknowledgedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		if by == "" {
			return fmt.Errorf("%w: --by is required and must not be empty", a.refuse(id))
		}
		if a.state != "open" {
			return a.refuse(id)
		}
		if err := ensureAckColumns(tx, a); err != nil {
			return err
		}
		ackedSeq := a.lastObservedSeq
		if a.trackedLagPair.Valid && a.trackedLagPair.String != "" {
			headSeq, err := tx.Reader().EventHeadSeq()
			if err != nil {
				return err
			}
			ackedSeq = max(ackedSeq, headSeq)
		}
		a.payload["by"], a.payload["note"] = by, note
		if a.hasSettlementCols {
			_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',acked_by=?,acked_note=?,acked_at=?,acked_seq=?,acked_fingerprint=condition_fingerprint,acked_lag_pair=tracked_lag_pair,settlement_state=CASE WHEN settlement_state IN ('uninvestigated','') THEN 'investigating' ELSE settlement_state END WHERE alert_id=?`,
				by, note, now.UnixMilli(), ackedSeq, id)
			return err
		}
		_, err := tx.Exec(`UPDATE alert_episodes SET state='acknowledged',acked_by=?,acked_note=?,acked_at=?,acked_seq=?,acked_fingerprint=condition_fingerprint,acked_lag_pair=tracked_lag_pair WHERE alert_id=?`,
			by, note, now.UnixMilli(), ackedSeq, id)
		return err
	})
}

// Investigate records the established cause and evidence on an episode in any
// state (including resolved), moving settlement_state to investigated.
func (e Evaluator) Investigate(ctx context.Context, id, by, cause, evidence string) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.act(id, investigatedKind, now, func(ep *memEpisode, a episodeAction) error {
			if err := validateInvestigationFields(a, id, by, cause, evidence); err != nil {
				return err
			}
			if a.settlementState == "settled" {
				return fmt.Errorf("alert %s is already settled", id)
			}
			a.payload["by"] = by
			a.payload["cause"] = cause
			a.payload["evidence"] = evidence
			ep.settlementState = "investigated"
			ep.investigatedBy = sql.NullString{String: by, Valid: true}
			ep.investigatedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
			ep.investigationCause = sql.NullString{String: cause, Valid: true}
			ep.investigationEvidence = sql.NullString{String: evidence, Valid: true}
			return nil
		})
	}
	return e.act(ctx, id, investigatedKind, func(tx *store.Tx, a episodeAction, now time.Time) error {
		if err := validateInvestigationFields(a, id, by, cause, evidence); err != nil {
			return err
		}
		if a.settlementState == "settled" {
			return fmt.Errorf("alert %s is already settled", id)
		}
		if !a.hasSettlementCols {
			return fmt.Errorf("alert %s: settlement columns missing; migrate store first", id)
		}
		a.payload["by"] = by
		a.payload["cause"] = cause
		a.payload["evidence"] = evidence
		_, err := tx.Exec(`UPDATE alert_episodes SET settlement_state='investigated',investigated_by=?,investigated_at=?,investigation_cause=?,investigation_evidence=? WHERE alert_id=?`,
			by, now.UnixMilli(), cause, evidence, id)
		return err
	})
}

// Settle records an episode's settlement outcome (fix-rule or fix-fault) and
// verified queue item, plus single-instance settled watermarks.
func (e Evaluator) Settle(ctx context.Context, id string, opts SettleOptions) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		now := e.now()
		return e.mem.settle(id, now, func(ep *memEpisode, a episodeAction) (bool, error) {
			if err := validateSettleOptions(a, id, opts); err != nil {
				return false, err
			}
			if a.settlementState == "settled" {
				return false, checkSettleConflict(a, id, opts)
			}
			inline := opts.Cause != "" || opts.Evidence != ""
			if inline {
				if err := validateInvestigationFields(a, id, opts.By, opts.Cause, opts.Evidence); err != nil {
					return false, err
				}
			} else if a.settlementState != "investigated" {
				return false, fmt.Errorf("%w: %s", ErrInvestigationRequired, a.refuse(id))
			}
			if err := validateSourceSettleItem(ctx, e.Source, a, id, opts.ItemID); err != nil {
				return false, err
			}
			if inline {
				ep.investigatedBy = sql.NullString{String: opts.By, Valid: true}
				ep.investigatedAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
				ep.investigationCause = sql.NullString{String: opts.Cause, Valid: true}
				ep.investigationEvidence = sql.NullString{String: opts.Evidence, Valid: true}
			}
			settledSeq := ep.lastObservedSeq
			if ep.trackedLagPair.Valid && ep.trackedLagPair.String != "" {
				settledSeq = max(settledSeq, e.mem.lastSourceSeq)
			}
			ep.settlementState = "settled"
			ep.outcome = sql.NullString{String: opts.Outcome, Valid: true}
			ep.trackingItemID = sql.NullString{String: opts.ItemID, Valid: true}
			if opts.LinkKind != "" {
				ep.itemLinkKind = sql.NullString{String: opts.LinkKind, Valid: true}
			} else {
				ep.itemLinkKind = sql.NullString{}
			}
			ep.settledBy = sql.NullString{String: opts.By, Valid: true}
			ep.settledAt = sql.NullInt64{Int64: now.UnixMilli(), Valid: true}
			ep.settledSeq = sql.NullInt64{Int64: settledSeq, Valid: true}
			ep.settledFingerprint = sql.NullString{String: ep.conditionFingerprint, Valid: true}
			ep.settledLagPair = ep.trackedLagPair
			return inline, nil
		}, opts)
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		a, err := readEpisodeAction(tx.Reader(), id)
		if err != nil {
			return err
		}
		if err := validateSettleOptions(a, id, opts); err != nil {
			return err
		}
		if a.settlementState == "settled" {
			return checkSettleConflict(a, id, opts)
		}
		inline := opts.Cause != "" || opts.Evidence != ""
		if inline {
			if err := validateInvestigationFields(a, id, opts.By, opts.Cause, opts.Evidence); err != nil {
				return err
			}
		} else if a.settlementState != "investigated" {
			return fmt.Errorf("%w: %s", ErrInvestigationRequired, a.refuse(id))
		}
		if err := validateStoreSettleItem(tx.Reader(), a, id, opts.ItemID); err != nil {
			return err
		}
		if !a.hasSettlementCols || !a.hasTrackingCol || !a.hasLagPairCol {
			return fmt.Errorf("alert %s: settlement columns missing; migrate store first", id)
		}
		now := e.now()
		settledSeq := a.lastObservedSeq
		if a.trackedLagPair.Valid && a.trackedLagPair.String != "" {
			headSeq, err := tx.Reader().EventHeadSeq()
			if err != nil {
				return err
			}
			settledSeq = max(settledSeq, headSeq)
		}
		var linkKind sql.NullString
		if opts.LinkKind != "" {
			linkKind = sql.NullString{String: opts.LinkKind, Valid: true}
		}
		if inline {
			if _, err := tx.Exec(`UPDATE alert_episodes SET settlement_state='settled',investigated_by=?,investigated_at=?,investigation_cause=?,investigation_evidence=?,outcome=?,tracking_item_id=?,item_link_kind=?,settled_by=?,settled_at=?,settled_seq=?,settled_fingerprint=condition_fingerprint,settled_lag_pair=tracked_lag_pair WHERE alert_id=?`,
				opts.By, now.UnixMilli(), opts.Cause, opts.Evidence, opts.Outcome, opts.ItemID, linkKind, opts.By, now.UnixMilli(), settledSeq, id); err != nil {
				return err
			}
			invPayload := map[string]any{
				"alert_id":    id,
				"rule_id":     a.payload["rule_id"],
				"subject_key": a.payload["subject_key"],
				"severity":    a.payload["severity"],
				"by":          opts.By,
				"cause":       opts.Cause,
				"evidence":    opts.Evidence,
			}
			if err := appendAlert(tx, id, investigatedKind, now, invPayload); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE alert_episodes SET settlement_state='settled',outcome=?,tracking_item_id=?,item_link_kind=?,settled_by=?,settled_at=?,settled_seq=?,settled_fingerprint=condition_fingerprint,settled_lag_pair=tracked_lag_pair WHERE alert_id=?`,
				opts.Outcome, opts.ItemID, linkKind, opts.By, now.UnixMilli(), settledSeq, id); err != nil {
				return err
			}
		}
		settlePayload := map[string]any{
			"alert_id":    id,
			"rule_id":     a.payload["rule_id"],
			"subject_key": a.payload["subject_key"],
			"severity":    a.payload["severity"],
			"by":          opts.By,
			"outcome":     opts.Outcome,
			"item_id":     opts.ItemID,
			"settled_seq": settledSeq,
		}
		if opts.LinkKind != "" {
			settlePayload["link_kind"] = opts.LinkKind
		}
		return appendAlert(tx, id, settledKind, now, settlePayload)
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
	hasAckedSeq, err := alertEpisodesHasColumn(rd, "acked_seq")
	if err != nil {
		return a, err
	}
	hasAckedFP, err := alertEpisodesHasColumn(rd, "acked_fingerprint")
	if err != nil {
		return a, err
	}
	hasAckedLag, err := alertEpisodesHasColumn(rd, "acked_lag_pair")
	if err != nil {
		return a, err
	}
	hasSettlementCols, err := hasSettlementColumns(rd)
	if err != nil {
		return a, err
	}
	a.hasTrackingCol = hasTracking
	a.hasLagPairCol = hasLagPair
	a.hasAckCols = hasAckedSeq && hasAckedFP && hasAckedLag
	a.hasSettlementCols = hasSettlementCols
	var rule, subject, severity string
	cols := "state,condition_fingerprint,last_observed_seq,acked_by,acked_note"
	dest := []any{&a.state, &a.fingerprint, &a.lastObservedSeq, &a.ackedBy, &a.ackedNote}
	if hasTracking {
		cols += ",tracking_item_id"
		dest = append(dest, &a.trackingItemID)
	}
	if hasLagPair {
		cols += ",tracked_lag_pair"
		dest = append(dest, &a.trackedLagPair)
	}
	if hasSettlementCols {
		cols += ",settlement_state,outcome,settled_by"
		dest = append(dest, &a.settlementState, &a.outcome, &a.settledBy)
	}
	cols += ",rule_id,subject_key,severity"
	dest = append(dest, &rule, &subject, &severity)
	err = rd.QueryRow("SELECT "+cols+" FROM alert_episodes WHERE alert_id=?", id).Scan(dest...)
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
