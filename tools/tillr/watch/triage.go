package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	triageStartedKind = "watch.alert.triage-started"
	triagedKind       = "watch.alert.triaged"
	escalatedKind     = "watch.alert.escalated"
)

// ErrTriageAlreadyClaimed reports that an episode already has an active triage claim.
var ErrTriageAlreadyClaimed = errors.New("alert triage already claimed")

// ErrTriageNotEligible reports that an episode is not in an untriaged eligible state.
var ErrTriageNotEligible = errors.New("alert is not eligible for triage")

// ErrInvalidTriageOutcome reports an invalid or refused triage outcome.
var ErrInvalidTriageOutcome = errors.New("invalid triage outcome")

var triageColumnNames = []string{
	"triage_state",
	"triage_outcome",
	"triage_outcome_kind",
	"triage_run_id",
	"triage_finding",
	"triage_action_needed",
	"triage_question",
	"triage_options_json",
	"triaged_at",
}

func hasTriageColumns(rd *store.Reader) (bool, error) {
	for _, col := range triageColumnNames {
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

func appendTriageResetSets(rd *store.Reader, sets []string) ([]string, error) {
	hasActive, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
	if err != nil {
		return nil, err
	}
	if hasActive {
		sets = append(sets, "active_triage_run_id=NULL")
	}
	hasEscalated, err := alertEpisodesHasColumn(rd, "escalated_to")
	if err != nil {
		return nil, err
	}
	if hasEscalated {
		sets = append(sets, "escalated_to=NULL")
	}
	hasTriage, err := hasTriageColumns(rd)
	if err != nil {
		return nil, err
	}
	if hasTriage {
		sets = append(sets,
			"triage_state='untriaged'",
			"triage_outcome=NULL",
			"triage_outcome_kind=NULL",
			"triage_run_id=NULL",
			"triage_finding=NULL",
			"triage_action_needed=NULL",
			"triage_question=NULL",
			"triage_options_json=NULL",
			"triaged_at=NULL",
		)
	}
	return sets, nil
}

// RecordTriageOptions configures Evaluator.RecordTriage.
type RecordTriageOptions struct {
	RunID        string
	Outcome      string // "filed" | "linked" | "escalated"
	OutcomeKind  string // "fix-fault" | "fix-rule"
	ItemID       string
	Finding      string
	EscalateTo   string // "operator-session" | "owner"
	ActionNeeded string
	Question     string
	Options      []string
}

func validateRecordTriageOptions(opts RecordTriageOptions) error {
	if strings.TrimSpace(opts.RunID) == "" {
		return fmt.Errorf("run_id is required and must not be empty")
	}
	if opts.Outcome == "expected" {
		return fmt.Errorf("%w: outcome %q is refused; there is no evidence-only expected outcome (OQ-AT2)", ErrInvalidTriageOutcome, opts.Outcome)
	}
	switch opts.Outcome {
	case "filed", "linked":
		if strings.TrimSpace(opts.ItemID) == "" {
			return fmt.Errorf("item_id is required for triage outcome %q", opts.Outcome)
		}
		if opts.OutcomeKind != "fix-fault" && opts.OutcomeKind != "fix-rule" {
			return fmt.Errorf("outcome_kind must be fix-fault or fix-rule for triage outcome %q", opts.Outcome)
		}
	case "escalated":
		if opts.EscalateTo != "operator-session" && opts.EscalateTo != "owner" {
			return fmt.Errorf("escalate_to must be operator-session or owner")
		}
		if opts.EscalateTo == "operator-session" && strings.TrimSpace(opts.ActionNeeded) == "" {
			return fmt.Errorf("action_needed is required when escalating to operator-session")
		}
		if opts.EscalateTo == "owner" && strings.TrimSpace(opts.ItemID) == "" {
			if strings.TrimSpace(opts.Question) == "" {
				return fmt.Errorf("question is required when escalating to owner without an existing item")
			}
			validOpts := 0
			for _, o := range opts.Options {
				if strings.TrimSpace(o) != "" {
					validOpts++
				}
			}
			if validOpts == 0 {
				return fmt.Errorf("at least one non-empty option is required when escalating to owner without an existing item")
			}
		}
		if (strings.TrimSpace(opts.ItemID) != "") != (strings.TrimSpace(opts.OutcomeKind) != "") {
			return fmt.Errorf("item_id and outcome_kind must be provided together when attaching an item to an escalation")
		}
		if opts.OutcomeKind != "" && opts.OutcomeKind != "fix-fault" && opts.OutcomeKind != "fix-rule" {
			return fmt.Errorf("outcome_kind must be fix-fault or fix-rule")
		}
	default:
		return fmt.Errorf("%w: %q (must be filed, linked, or escalated)", ErrInvalidTriageOutcome, opts.Outcome)
	}
	if strings.TrimSpace(opts.Finding) == "" {
		return fmt.Errorf("finding is required and must not be empty")
	}
	return nil
}

// NeedsClaimTriageMigration validates a triage claim read-only and reports whether
// alert_episodes lacks the triage columns so a CLI caller can migrate via store.OpenMigrated.
func (e Evaluator) NeedsClaimTriageMigration(ctx context.Context, alertID, runID string) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	if strings.TrimSpace(runID) == "" {
		return false, fmt.Errorf("run_id is required and must not be empty")
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		hasAlerts, err := tableExists(rd, "alert_episodes")
		if err != nil {
			return err
		}
		if !hasAlerts {
			return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
		}
		hasTriage, err := hasTriageColumns(rd)
		if err != nil {
			return err
		}
		hasSettle, err := hasSettlementColumns(rd)
		if err != nil {
			return err
		}
		hasActive, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
		if err != nil {
			return err
		}
		settlementExpr := "'uninvestigated'"
		if hasSettle {
			settlementExpr = "COALESCE(NULLIF(settlement_state, ''), 'uninvestigated')"
		}
		activeExpr := "''"
		if hasActive {
			activeExpr = "COALESCE(active_triage_run_id, '')"
		}
		triageStateExpr := "'untriaged'"
		if hasTriage {
			triageStateExpr = "COALESCE(NULLIF(triage_state, ''), 'untriaged')"
		}
		var state, settlement, activeRun, triageState string
		err = rd.QueryRow("SELECT state, "+settlementExpr+", "+activeExpr+", "+triageStateExpr+" FROM alert_episodes WHERE alert_id=?", alertID).
			Scan(&state, &settlement, &activeRun, &triageState)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
		}
		if err != nil {
			return err
		}
		if activeRun != "" || triageState == "triaging" {
			return fmt.Errorf("%w: alert %s (run %s)", ErrTriageAlreadyClaimed, alertID, activeRun)
		}
		if triageState != "untriaged" || (state != "open" && !(state == "resolved" && settlement == "uninvestigated")) {
			return fmt.Errorf("%w: alert %s (state=%s, settlement=%s, triage_state=%s)", ErrTriageNotEligible, alertID, state, settlement, triageState)
		}
		need = !hasTriage || !hasSettle || !hasActive
		return nil
	})
	return need, err
}

// ClaimTriage atomically claims an untriaged open or auto-resolved uninvestigated
// episode for runID in one IMMEDIATE write transaction.
func (e Evaluator) ClaimTriage(ctx context.Context, alertID, runID string) error {
	if e.Store == nil {
		return fmt.Errorf("watch: Evaluator requires Store")
	}
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("run_id is required and must not be empty")
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		hasAlerts, err := tableExists(tx.Reader(), "alert_episodes")
		if err != nil {
			return err
		}
		if !hasAlerts {
			return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
		}
		hasTriage, err := hasTriageColumns(tx.Reader())
		if err != nil {
			return err
		}
		hasSettle, err := hasSettlementColumns(tx.Reader())
		if err != nil {
			return err
		}
		hasActive, err := alertEpisodesHasColumn(tx.Reader(), "active_triage_run_id")
		if err != nil {
			return err
		}
		if !hasTriage || !hasSettle || !hasActive {
			return fmt.Errorf("alert %s: triage columns missing; migrate store first", alertID)
		}
		res, err := tx.Exec(`UPDATE alert_episodes
			SET active_triage_run_id = ?, triage_run_id = ?, triage_state = 'triaging'
			WHERE alert_id = ?
			  AND active_triage_run_id IS NULL
			  AND COALESCE(NULLIF(triage_state, ''), 'untriaged') = 'untriaged'
			  AND (state = 'open' OR (state = 'resolved' AND COALESCE(NULLIF(settlement_state, ''), 'uninvestigated') = 'uninvestigated'))`,
			runID, runID, alertID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			var state, settlement, activeRun, triageState string
			err := tx.QueryRow(`SELECT state, COALESCE(NULLIF(settlement_state, ''), 'uninvestigated'), COALESCE(active_triage_run_id, ''), COALESCE(NULLIF(triage_state, ''), 'untriaged') FROM alert_episodes WHERE alert_id = ?`, alertID).
				Scan(&state, &settlement, &activeRun, &triageState)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("alert %s %w", alertID, ErrAlertNotFound)
			}
			if err != nil {
				return err
			}
			if activeRun != "" || triageState == "triaging" {
				return fmt.Errorf("%w: alert %s (run %s)", ErrTriageAlreadyClaimed, alertID, activeRun)
			}
			return fmt.Errorf("%w: alert %s (state=%s, settlement=%s, triage_state=%s)", ErrTriageNotEligible, alertID, state, settlement, triageState)
		}
		var ruleID, subjectKey, severity string
		if err := tx.QueryRow(`SELECT rule_id, subject_key, severity FROM alert_episodes WHERE alert_id = ?`, alertID).
			Scan(&ruleID, &subjectKey, &severity); err != nil {
			return err
		}
		return appendAlert(tx, alertID, triageStartedKind, e.now(), map[string]any{
			"alert_id":    alertID,
			"rule_id":     ruleID,
			"subject_key": subjectKey,
			"severity":    severity,
			"run_id":      runID,
		})
	})
}

// NeedsRecordTriageMigration validates a record-triage operation read-only and reports
// whether alert_episodes lacks the triage or settlement columns.
func (e Evaluator) NeedsRecordTriageMigration(ctx context.Context, alertID string, opts RecordTriageOptions) (bool, error) {
	if err := validateRecordTriageOptions(opts); err != nil {
		return false, err
	}
	if e.Store == nil {
		return false, nil
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		a, err := readEpisodeAction(rd, alertID)
		if err != nil {
			return err
		}
		hasTriage, err := hasTriageColumns(rd)
		if err != nil {
			return err
		}
		hasEscalated, err := alertEpisodesHasColumn(rd, "escalated_to")
		if err != nil {
			return err
		}
		hasActive, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
		if err != nil {
			return err
		}
		if hasActive {
			var activeRun sql.NullString
			if err := rd.QueryRow(`SELECT active_triage_run_id FROM alert_episodes WHERE alert_id = ?`, alertID).Scan(&activeRun); err != nil {
				return err
			}
			if activeRun.Valid && activeRun.String != "" && activeRun.String != opts.RunID {
				return fmt.Errorf("%w: alert %s is claimed by run %s, not %s", ErrTriageAlreadyClaimed, alertID, activeRun.String, opts.RunID)
			}
		}
		if opts.ItemID != "" {
			if err := validateStoreSettleItem(rd, a, alertID, opts.ItemID); err != nil {
				return err
			}
		}
		need = !hasTriage || !a.hasSettlementCols || !a.hasTrackingCol || !a.hasLagPairCol || !hasEscalated || !hasActive
		return nil
	})
	return need, err
}

// RecordTriage records one of the three triage outcomes (filed, linked, escalated)
// on an open or auto-resolved episode without reopening resolved episodes.
func (e Evaluator) RecordTriage(ctx context.Context, alertID string, opts RecordTriageOptions) error {
	if err := validateRecordTriageOptions(opts); err != nil {
		return err
	}
	if e.Store == nil {
		return fmt.Errorf("watch: Evaluator requires Store")
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		a, err := readEpisodeAction(tx.Reader(), alertID)
		if err != nil {
			return err
		}
		hasTriage, err := hasTriageColumns(tx.Reader())
		if err != nil {
			return err
		}
		hasEscalated, err := alertEpisodesHasColumn(tx.Reader(), "escalated_to")
		if err != nil {
			return err
		}
		hasActive, err := alertEpisodesHasColumn(tx.Reader(), "active_triage_run_id")
		if err != nil {
			return err
		}
		if !hasTriage || !a.hasSettlementCols || !a.hasTrackingCol || !a.hasLagPairCol || !hasEscalated || !hasActive {
			return fmt.Errorf("alert %s: triage columns missing; migrate store first", alertID)
		}
		var activeRun sql.NullString
		var bundleRaw []byte
		if err := tx.QueryRow(`SELECT active_triage_run_id, bundle_json FROM alert_episodes WHERE alert_id = ?`, alertID).
			Scan(&activeRun, &bundleRaw); err != nil {
			return err
		}
		if activeRun.Valid && activeRun.String != "" && activeRun.String != opts.RunID {
			return fmt.Errorf("%w: alert %s is claimed by run %s, not %s", ErrTriageAlreadyClaimed, alertID, activeRun.String, opts.RunID)
		}
		if opts.ItemID != "" {
			if err := validateStoreSettleItem(tx.Reader(), a, alertID, opts.ItemID); err != nil {
				return err
			}
		}

		now := e.now()
		nowMs := now.UnixMilli()
		settledSeq := a.lastObservedSeq
		if a.trackedLagPair.Valid && a.trackedLagPair.String != "" {
			headSeq, err := tx.Reader().EventHeadSeq()
			if err != nil {
				return err
			}
			settledSeq = max(settledSeq, headSeq)
		}

		if opts.Outcome == "filed" || opts.Outcome == "linked" {
			if _, err := tx.Exec(`UPDATE alert_episodes SET
				settlement_state='settled',
				investigated_by='tillr-triage',
				investigated_at=?,
				investigation_cause=?,
				investigation_evidence=?,
				outcome=?,
				tracking_item_id=?,
				item_link_kind=?,
				settled_by='tillr-triage',
				settled_at=?,
				settled_seq=?,
				settled_fingerprint=condition_fingerprint,
				settled_lag_pair=tracked_lag_pair,
				triage_state='settled',
				triage_outcome=?,
				triage_outcome_kind=?,
				triage_run_id=?,
				triage_finding=?,
				triage_action_needed=NULL,
				triage_question=NULL,
				triage_options_json=NULL,
				triaged_at=?,
				active_triage_run_id=NULL,
				escalated_to=NULL
				WHERE alert_id=?`,
				nowMs, opts.Finding, opts.Finding, opts.OutcomeKind, opts.ItemID, opts.Outcome,
				nowMs, settledSeq, opts.Outcome, opts.OutcomeKind, opts.RunID, opts.Finding, nowMs, alertID); err != nil {
				return err
			}
			seq, err := appendAlertSeq(tx, alertID, triagedKind, now, map[string]any{
				"alert_id":     alertID,
				"rule_id":      a.payload["rule_id"],
				"subject_key":  a.payload["subject_key"],
				"severity":     a.payload["severity"],
				"run_id":       opts.RunID,
				"outcome":      opts.Outcome,
				"outcome_kind": opts.OutcomeKind,
				"item_id":      opts.ItemID,
				"finding":      opts.Finding,
			})
			if err != nil {
				return err
			}
			return upsertAlertItemLink(tx, alertID, opts.ItemID, opts.OutcomeKind, "tillr-triage", "", nowMs, seq)
		}

		// outcome == "escalated"
		var b Bundle
		if len(bundleRaw) > 0 {
			if err := json.Unmarshal(bundleRaw, &b); err != nil {
				return fmt.Errorf("decode bundle for alert %s: %w", alertID, err)
			}
		}
		var cleanOpts []string
		for _, o := range opts.Options {
			if strings.TrimSpace(o) != "" {
				cleanOpts = append(cleanOpts, o)
			}
		}
		b.Triage = &TriageHeader{
			RunID:        opts.RunID,
			Outcome:      "escalated",
			OutcomeKind:  opts.OutcomeKind,
			EscalatedTo:  opts.EscalateTo,
			ItemID:       opts.ItemID,
			Finding:      opts.Finding,
			ActionNeeded: opts.ActionNeeded,
			Question:     opts.Question,
			Options:      cleanOpts,
		}
		newBundle, err := b.encode()
		if err != nil {
			return err
		}

		var optionsJSON sql.NullString
		if len(cleanOpts) > 0 {
			raw, err := json.Marshal(cleanOpts)
			if err != nil {
				return err
			}
			optionsJSON = sql.NullString{String: string(raw), Valid: true}
		}
		nullStr := func(s string) sql.NullString {
			if s == "" {
				return sql.NullString{}
			}
			return sql.NullString{String: s, Valid: true}
		}

		if opts.ItemID != "" {
			if _, err := tx.Exec(`UPDATE alert_episodes SET
				settlement_state='settled',
				investigated_by='tillr-triage',
				investigated_at=?,
				investigation_cause=?,
				investigation_evidence=?,
				outcome=?,
				tracking_item_id=?,
				item_link_kind='linked',
				settled_by='tillr-triage',
				settled_at=?,
				settled_seq=?,
				settled_fingerprint=condition_fingerprint,
				settled_lag_pair=tracked_lag_pair,
				triage_state='escalated',
				triage_outcome='escalated',
				triage_outcome_kind=?,
				triage_run_id=?,
				triage_finding=?,
				triage_action_needed=?,
				triage_question=?,
				triage_options_json=?,
				triaged_at=?,
				active_triage_run_id=NULL,
				escalated_to=?,
				bundle_json=?
				WHERE alert_id=?`,
				nowMs, opts.Finding, opts.Finding, opts.OutcomeKind, opts.ItemID, nowMs, settledSeq,
				opts.OutcomeKind, opts.RunID, opts.Finding, nullStr(opts.ActionNeeded), nullStr(opts.Question), optionsJSON, nowMs, opts.EscalateTo, string(newBundle), alertID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE alert_episodes SET
				settlement_state='investigated',
				investigated_by='tillr-triage',
				investigated_at=?,
				investigation_cause=?,
				investigation_evidence=?,
				triage_state='escalated',
				triage_outcome='escalated',
				triage_outcome_kind=NULL,
				triage_run_id=?,
				triage_finding=?,
				triage_action_needed=?,
				triage_question=?,
				triage_options_json=?,
				triaged_at=?,
				active_triage_run_id=NULL,
				escalated_to=?,
				bundle_json=?
				WHERE alert_id=?`,
				nowMs, opts.Finding, opts.Finding,
				opts.RunID, opts.Finding, nullStr(opts.ActionNeeded), nullStr(opts.Question), optionsJSON, nowMs, opts.EscalateTo, string(newBundle), alertID); err != nil {
				return err
			}
		}

		triagedBody := map[string]any{
			"alert_id":     alertID,
			"rule_id":      a.payload["rule_id"],
			"subject_key":  a.payload["subject_key"],
			"severity":     a.payload["severity"],
			"run_id":       opts.RunID,
			"outcome":      "escalated",
			"escalated_to": opts.EscalateTo,
			"finding":      opts.Finding,
		}
		if opts.ItemID != "" {
			triagedBody["item_id"] = opts.ItemID
			triagedBody["outcome_kind"] = opts.OutcomeKind
		}
		if opts.ActionNeeded != "" {
			triagedBody["action_needed"] = opts.ActionNeeded
		}
		if opts.Question != "" {
			triagedBody["question"] = opts.Question
		}
		if len(cleanOpts) > 0 {
			triagedBody["options"] = cleanOpts
		}
		seq, err := appendAlertSeq(tx, alertID, triagedKind, now, triagedBody)
		if err != nil {
			return err
		}
		if opts.ItemID != "" {
			if err := upsertAlertItemLink(tx, alertID, opts.ItemID, opts.OutcomeKind, "tillr-triage", "", nowMs, seq); err != nil {
				return err
			}
		}
		return appendAlert(tx, alertID, escalatedKind, now, triagedBody)
	})
}

// TriageFailureOptions configures Evaluator.RecordTriageFailure.
type TriageFailureOptions struct {
	RunID        string
	Finding      string
	ActionNeeded string
	ItemID       string
	OutcomeKind  string // optional; "fix-fault" or "fix-rule" when ItemID != ""
}

// RecordTriageFailure clears active_triage_run_id and escalates the episode to
// operator-session with triage_outcome = "triage-failed".
func (e Evaluator) RecordTriageFailure(ctx context.Context, alertID string, opts TriageFailureOptions) error {
	if strings.TrimSpace(opts.Finding) == "" {
		return fmt.Errorf("finding is required and must not be empty")
	}
	if strings.TrimSpace(opts.ActionNeeded) == "" {
		return fmt.Errorf("action_needed is required and must not be empty")
	}
	if strings.TrimSpace(opts.ItemID) != "" {
		if opts.OutcomeKind != "fix-fault" && opts.OutcomeKind != "fix-rule" {
			return fmt.Errorf("outcome_kind must be fix-fault or fix-rule when item_id is provided")
		}
	} else if strings.TrimSpace(opts.OutcomeKind) != "" {
		return fmt.Errorf("outcome_kind must be empty when item_id is empty")
	}
	if e.Store == nil {
		return fmt.Errorf("watch: Evaluator requires Store")
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		a, err := readEpisodeAction(tx.Reader(), alertID)
		if err != nil {
			return err
		}
		hasTriage, err := hasTriageColumns(tx.Reader())
		if err != nil {
			return err
		}
		hasEscalated, err := alertEpisodesHasColumn(tx.Reader(), "escalated_to")
		if err != nil {
			return err
		}
		hasActive, err := alertEpisodesHasColumn(tx.Reader(), "active_triage_run_id")
		if err != nil {
			return err
		}
		if !hasTriage || !a.hasSettlementCols || !a.hasTrackingCol || !a.hasLagPairCol || !hasEscalated || !hasActive {
			return fmt.Errorf("alert %s: triage columns missing; migrate store first", alertID)
		}
		var bundleRaw []byte
		if err := tx.QueryRow(`SELECT bundle_json FROM alert_episodes WHERE alert_id = ?`, alertID).Scan(&bundleRaw); err != nil {
			return err
		}
		if opts.ItemID != "" {
			if err := validateStoreSettleItem(tx.Reader(), a, alertID, opts.ItemID); err != nil {
				return err
			}
		}

		now := e.now()
		nowMs := now.UnixMilli()
		settledSeq := a.lastObservedSeq
		if a.trackedLagPair.Valid && a.trackedLagPair.String != "" {
			headSeq, err := tx.Reader().EventHeadSeq()
			if err != nil {
				return err
			}
			settledSeq = max(settledSeq, headSeq)
		}

		var b Bundle
		if len(bundleRaw) > 0 {
			if err := json.Unmarshal(bundleRaw, &b); err != nil {
				return fmt.Errorf("decode bundle for alert %s: %w", alertID, err)
			}
		}
		b.Triage = &TriageHeader{
			RunID:        opts.RunID,
			Outcome:      "triage-failed",
			OutcomeKind:  opts.OutcomeKind,
			EscalatedTo:  "operator-session",
			ItemID:       opts.ItemID,
			Finding:      opts.Finding,
			ActionNeeded: opts.ActionNeeded,
		}
		newBundle, err := b.encode()
		if err != nil {
			return err
		}

		nullStr := func(s string) sql.NullString {
			if s == "" {
				return sql.NullString{}
			}
			return sql.NullString{String: s, Valid: true}
		}

		if opts.ItemID != "" {
			if _, err := tx.Exec(`UPDATE alert_episodes SET
				settlement_state='settled',
				investigated_by='tillr-triage',
				investigated_at=?,
				investigation_cause=?,
				investigation_evidence=?,
				outcome=?,
				tracking_item_id=?,
				item_link_kind='filed',
				settled_by='tillr-triage',
				settled_at=?,
				settled_seq=?,
				settled_fingerprint=condition_fingerprint,
				settled_lag_pair=tracked_lag_pair,
				triage_state='escalated',
				triage_outcome='triage-failed',
				triage_outcome_kind=?,
				triage_run_id=?,
				triage_finding=?,
				triage_action_needed=?,
				triage_question=NULL,
				triage_options_json=NULL,
				triaged_at=?,
				active_triage_run_id=NULL,
				escalated_to='operator-session',
				bundle_json=?
				WHERE alert_id=?`,
				nowMs, opts.Finding, opts.Finding, opts.OutcomeKind, opts.ItemID, nowMs, settledSeq,
				opts.OutcomeKind, nullStr(opts.RunID), opts.Finding, opts.ActionNeeded, nowMs, string(newBundle), alertID); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(`UPDATE alert_episodes SET
				settlement_state='investigated',
				investigated_by='tillr-triage',
				investigated_at=?,
				investigation_cause=?,
				investigation_evidence=?,
				triage_state='escalated',
				triage_outcome='triage-failed',
				triage_outcome_kind=NULL,
				triage_run_id=?,
				triage_finding=?,
				triage_action_needed=?,
				triage_question=NULL,
				triage_options_json=NULL,
				triaged_at=?,
				active_triage_run_id=NULL,
				escalated_to='operator-session',
				bundle_json=?
				WHERE alert_id=?`,
				nowMs, opts.Finding, opts.Finding,
				nullStr(opts.RunID), opts.Finding, opts.ActionNeeded, nowMs, string(newBundle), alertID); err != nil {
				return err
			}
		}

		body := map[string]any{
			"alert_id":      alertID,
			"rule_id":       a.payload["rule_id"],
			"subject_key":   a.payload["subject_key"],
			"severity":      a.payload["severity"],
			"outcome":       "triage-failed",
			"escalated_to":  "operator-session",
			"finding":       opts.Finding,
			"action_needed": opts.ActionNeeded,
		}
		if opts.RunID != "" {
			body["run_id"] = opts.RunID
		}
		if opts.ItemID != "" {
			body["item_id"] = opts.ItemID
			body["outcome_kind"] = opts.OutcomeKind
		}
		seq, err := appendAlertSeq(tx, alertID, triagedKind, now, body)
		if err != nil {
			return err
		}
		if opts.ItemID != "" {
			if err := upsertAlertItemLink(tx, alertID, opts.ItemID, opts.OutcomeKind, "tillr-triage", "", nowMs, seq); err != nil {
				return err
			}
		}
		return appendAlert(tx, alertID, escalatedKind, now, body)
	})
}

// EligibleTriageEpisode describes an untriaged episode eligible for tillr-drain dispatch.
type EligibleTriageEpisode struct {
	AlertID              string
	RuleID               string
	SubjectKey           string
	State                string
	Severity             string
	ConditionFingerprint string
	OpenedSeq            int64
	ObservationCount     int64
	ReopenCountForItem   int64
	TrackingItemID       string
	VariablesJSON        string
	BundleJSON           string
}

// ReadEligibleTriageEpisodes returns untriaged open or auto-resolved-uninvestigated
// episodes with active_triage_run_id IS NULL whose rule_id is in triageRuleIDs,
// ordered by opened_seq ASC, alert_id ASC.
func ReadEligibleTriageEpisodes(rd *store.Reader, triageRuleIDs map[string]bool) ([]EligibleTriageEpisode, error) {
	if len(triageRuleIDs) == 0 {
		return nil, nil
	}
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil || !hasAlerts {
		return nil, err
	}
	hasTriage, err := hasTriageColumns(rd)
	if err != nil {
		return nil, err
	}
	hasSettle, err := hasSettlementColumns(rd)
	if err != nil {
		return nil, err
	}
	hasActive, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
	if err != nil {
		return nil, err
	}
	if !hasTriage || !hasSettle || !hasActive {
		return nil, nil
	}
	hasReopen, err := alertEpisodesHasColumn(rd, "reopen_count_for_item")
	if err != nil {
		return nil, err
	}
	hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
	if err != nil {
		return nil, err
	}
	hasVars, err := alertEpisodesHasColumn(rd, "variables_json")
	if err != nil {
		return nil, err
	}
	reopenExpr := "0"
	if hasReopen {
		reopenExpr = "COALESCE(reopen_count_for_item, 0)"
	}
	trackingExpr := "''"
	if hasTracking {
		trackingExpr = "COALESCE(tracking_item_id, '')"
	}
	varsExpr := "'{}'"
	if hasVars {
		varsExpr = "COALESCE(NULLIF(variables_json, ''), '{}')"
	}
	q := `SELECT alert_id, rule_id, subject_key, state, severity, condition_fingerprint,
		opened_seq, observation_count, ` + reopenExpr + `, ` + trackingExpr + `, ` + varsExpr + `, bundle_json
		FROM alert_episodes
		WHERE active_triage_run_id IS NULL
		  AND COALESCE(NULLIF(triage_state, ''), 'untriaged') = 'untriaged'
		  AND (state = 'open' OR (state = 'resolved' AND COALESCE(NULLIF(settlement_state, ''), 'uninvestigated') = 'uninvestigated'))
		ORDER BY opened_seq ASC, alert_id ASC`
	rows, err := rd.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EligibleTriageEpisode
	for rows.Next() {
		var ep EligibleTriageEpisode
		if err := rows.Scan(
			&ep.AlertID, &ep.RuleID, &ep.SubjectKey, &ep.State, &ep.Severity, &ep.ConditionFingerprint,
			&ep.OpenedSeq, &ep.ObservationCount, &ep.ReopenCountForItem, &ep.TrackingItemID, &ep.VariablesJSON, &ep.BundleJSON,
		); err != nil {
			return nil, err
		}
		if triageRuleIDs[ep.RuleID] {
			out = append(out, ep)
		}
	}
	return out, rows.Err()
}

// ActiveTriageClaim describes an episode currently holding an active_triage_run_id.
type ActiveTriageClaim struct {
	AlertID        string
	RuleID         string
	SubjectKey     string
	ActiveRunID    string
	TrackingItemID string
}

// ReadActiveTriageClaims returns episodes with a non-empty active_triage_run_id.
func ReadActiveTriageClaims(rd *store.Reader) ([]ActiveTriageClaim, error) {
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil || !hasAlerts {
		return nil, err
	}
	hasActive, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
	if err != nil || !hasActive {
		return nil, err
	}
	hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
	if err != nil {
		return nil, err
	}
	trackingExpr := "''"
	if hasTracking {
		trackingExpr = "COALESCE(tracking_item_id, '')"
	}
	rows, err := rd.Query(`SELECT alert_id, rule_id, subject_key, active_triage_run_id, ` + trackingExpr + `
		FROM alert_episodes
		WHERE active_triage_run_id IS NOT NULL AND active_triage_run_id != ''
		ORDER BY opened_seq ASC, alert_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActiveTriageClaim
	for rows.Next() {
		var c ActiveTriageClaim
		if err := rows.Scan(&c.AlertID, &c.RuleID, &c.SubjectKey, &c.ActiveRunID, &c.TrackingItemID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// TriageEpisodeState captures the triage-relevant columns of a single episode.
type TriageEpisodeState struct {
	AlertID            string
	RuleID             string
	SubjectKey         string
	State              string
	SettlementState    string
	TriageState        string
	TriageOutcome      string
	TriageOutcomeKind  string
	TriageFinding      string
	ActiveTriageRunID  string
	TrackingItemID     string
	ReopenCountForItem int64
	VariablesJSON      string
	BundleJSON         string
}

// ReadTriageEpisodeState returns the current triage state for alertID.
func ReadTriageEpisodeState(rd *store.Reader, alertID string) (TriageEpisodeState, bool, error) {
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil || !hasAlerts {
		return TriageEpisodeState{}, false, err
	}
	hasTriage, err := hasTriageColumns(rd)
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	hasSettle, err := hasSettlementColumns(rd)
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	hasActive, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	if !hasTriage || !hasSettle || !hasActive {
		return TriageEpisodeState{}, false, nil
	}
	hasReopen, err := alertEpisodesHasColumn(rd, "reopen_count_for_item")
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	hasVars, err := alertEpisodesHasColumn(rd, "variables_json")
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	reopenExpr := "0"
	if hasReopen {
		reopenExpr = "COALESCE(reopen_count_for_item, 0)"
	}
	trackingExpr := "''"
	if hasTracking {
		trackingExpr = "COALESCE(tracking_item_id, '')"
	}
	varsExpr := "'{}'"
	if hasVars {
		varsExpr = "COALESCE(NULLIF(variables_json, ''), '{}')"
	}
	var st TriageEpisodeState
	err = rd.QueryRow(`SELECT alert_id, rule_id, subject_key, state,
		COALESCE(NULLIF(settlement_state, ''), 'uninvestigated'),
		COALESCE(NULLIF(triage_state, ''), 'untriaged'),
		COALESCE(triage_outcome, ''),
		COALESCE(triage_outcome_kind, ''),
		COALESCE(triage_finding, ''),
		COALESCE(active_triage_run_id, ''),
		`+trackingExpr+`, `+reopenExpr+`, `+varsExpr+`, bundle_json
		FROM alert_episodes WHERE alert_id = ?`, alertID).Scan(
		&st.AlertID, &st.RuleID, &st.SubjectKey, &st.State,
		&st.SettlementState, &st.TriageState, &st.TriageOutcome, &st.TriageOutcomeKind, &st.TriageFinding,
		&st.ActiveTriageRunID, &st.TrackingItemID, &st.ReopenCountForItem, &st.VariablesJSON, &st.BundleJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return TriageEpisodeState{}, false, nil
	}
	if err != nil {
		return TriageEpisodeState{}, false, err
	}
	return st, true, nil
}

// HasActiveSilenceForItem reports whether an active (unexpired, unlifted) silence
// in watch_silences covers the episode's rule/variables or tracking item.
func HasActiveSilenceForItem(rd *store.Reader, ruleID, varsJSON, trackingItemID string, now time.Time) (bool, error) {
	silences, err := ReadActiveSilences(rd)
	if err != nil {
		return false, err
	}
	nowMs := now.UnixMilli()
	for _, s := range silences {
		if s.SilenceUntilMS != nil && nowMs >= *s.SilenceUntilMS {
			continue
		}
		if trackingItemID != "" && s.TrackingItemID == trackingItemID {
			return true, nil
		}
		if ruleID != "" && s.RuleID == ruleID && silenceMatches(s.VarsJSON, varsJSON) {
			return true, nil
		}
	}
	return false, nil
}

// ReadTriageCorrelationContext formats recent active episodes and active silences
// for inclusion in the triage brief's {{CORRELATION_CONTEXT}} placeholder.
func ReadTriageCorrelationContext(rd *store.Reader, excludeAlertID string, now time.Time) (string, error) {
	hasAlerts, err := tableExists(rd, "alert_episodes")
	if err != nil {
		return "", err
	}
	var lines []string
	if hasAlerts {
		hasTriage, err := hasTriageColumns(rd)
		if err != nil {
			return "", err
		}
		hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
		if err != nil {
			return "", err
		}
		triageExpr := "'untriaged'"
		if hasTriage {
			triageExpr = "COALESCE(NULLIF(triage_state, ''), 'untriaged')"
		}
		trackingExpr := "''"
		if hasTracking {
			trackingExpr = "COALESCE(tracking_item_id, '')"
		}
		rows, err := rd.Query(`SELECT alert_id, rule_id, subject_key, state, `+triageExpr+`, `+trackingExpr+`
			FROM alert_episodes
			WHERE alert_id != ? AND state != 'resolved'
			ORDER BY opened_seq DESC, alert_id ASC
			LIMIT 20`, excludeAlertID)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var aid, rid, sk, st, tst, item string
			if err := rows.Scan(&aid, &rid, &sk, &st, &tst, &item); err != nil {
				rows.Close()
				return "", err
			}
			line := fmt.Sprintf("- episode %s rule=%s subject=%s state=%s triage=%s", aid, rid, sk, st, tst)
			if item != "" {
				line += " item=" + item
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", err
		}
		rows.Close()
	}
	silences, err := ReadActiveSilences(rd)
	if err != nil {
		return "", err
	}
	nowMs := now.UnixMilli()
	for _, s := range silences {
		if s.SilenceUntilMS != nil && nowMs >= *s.SilenceUntilMS {
			continue
		}
		lines = append(lines, fmt.Sprintf("- silence %s rule=%s vars=%s item=%s", s.SilenceID, s.RuleID, s.VarsJSON, s.TrackingItemID))
	}
	if len(lines) == 0 {
		return "(none)", nil
	}
	return strings.Join(lines, "\n"), nil
}
