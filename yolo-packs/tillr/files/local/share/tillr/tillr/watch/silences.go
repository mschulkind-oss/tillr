package watch

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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

const silenceLiftedKind = "watch.silence.lifted"

// ErrInvalidSilenceBinding reports a missing, unknown, or disallowed variable
// binding when creating a silence.
var ErrInvalidSilenceBinding = errors.New("invalid silence variable binding")

// SilenceOptions configures Evaluator.Silence.
type SilenceOptions struct {
	AlertID     string
	RuleID      string
	Vars        map[string]string
	By          string
	ItemID      string
	Reason      string
	Until       *string
	UntilChange bool
	Cause       string
	Evidence    string
}

// SilenceRecord represents a row in watch_silences.
type SilenceRecord struct {
	SilenceID             string            `json:"silence_id"`
	RuleID                string            `json:"rule_id"`
	Vars                  map[string]string `json:"vars"`
	VarsJSON              string            `json:"vars_json"`
	TrackingItemID        string            `json:"tracking_item_id"`
	CreatedBy             string            `json:"created_by"`
	CreatedAt             int64             `json:"created_at"`
	Reason                string            `json:"reason"`
	InvestigationCause    string            `json:"investigation_cause"`
	InvestigationEvidence string            `json:"investigation_evidence"`
	SourceAlertID         string            `json:"source_alert_id,omitempty"`
	SilenceUntilMS        *int64            `json:"silence_until_ms,omitempty"`
	SilenceOnFingerprint  string            `json:"silence_on_fingerprint,omitempty"`
	LiftedAt              *int64            `json:"lifted_at,omitempty"`
	LiftedBy              string            `json:"lifted_by,omitempty"`
	LiftedReason          string            `json:"lifted_reason,omitempty"`
	MatchedAlerts         int64             `json:"matched_alerts,omitempty"`
}

const watchSilencesDDL = `
CREATE TABLE IF NOT EXISTS watch_silences (
    silence_id             TEXT PRIMARY KEY,
    rule_id                TEXT NOT NULL,
    vars_json              TEXT NOT NULL,
    tracking_item_id       TEXT NOT NULL,
    created_by             TEXT NOT NULL,
    created_at             INTEGER NOT NULL,
    reason                 TEXT NOT NULL,
    investigation_cause    TEXT NOT NULL,
    investigation_evidence TEXT NOT NULL,
    source_alert_id        TEXT,
    silence_until_ms       INTEGER,
    silence_on_fingerprint TEXT,
    lifted_at              INTEGER,
    lifted_by              TEXT,
    lifted_reason          TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS watch_silences_active_scope
    ON watch_silences(rule_id, vars_json) WHERE lifted_at IS NULL;
`

func hasSilenceSchema(rd *store.Reader) (bool, error) {
	hasTable, err := tableExists(rd, "watch_silences")
	if err != nil || !hasTable {
		return false, err
	}
	for _, col := range []string{"silence_id", "variables_json", "tracking_item_id", "tracked_lag_pair"} {
		ok, err := alertEpisodesHasColumn(rd, col)
		if err != nil || !ok {
			return false, err
		}
	}
	return hasSettlementColumns(rd)
}

func silenceMatches(silenceVarsJSON, episodeVarsJSON string) bool {
	silVars := decodeVariablesJSON(silenceVarsJSON)
	if len(silVars) == 0 {
		return true
	}
	epVars := decodeVariablesJSON(episodeVarsJSON)
	for k, v := range silVars {
		if epVars[k] != v {
			return false
		}
	}
	return true
}

func appendSilenceLifted(tx *store.Tx, silenceID string, now time.Time, body map[string]any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: silenceLiftedKind, At: now, ObjectType: "silence", ObjectID: &silenceID, Payload: payload})
	return err
}

func validateSilenceActorAndReason(opts SilenceOptions) error {
	by := strings.TrimSpace(opts.By)
	if by == "" {
		return fmt.Errorf("--by is required and must not be empty")
	}
	if by == "tillr-triage" {
		return fmt.Errorf("actor %q is not permitted to create silences", by)
	}
	if strings.TrimSpace(opts.Reason) == "" {
		return fmt.Errorf("--reason is required and must not be empty")
	}
	if strings.TrimSpace(opts.ItemID) == "" {
		return fmt.Errorf("--item is required and must not be empty")
	}
	if (opts.Until != nil) == opts.UntilChange {
		return fmt.Errorf("silence requires exactly one of --until and --until-change")
	}
	if (opts.AlertID == "") == (opts.RuleID == "") {
		return fmt.Errorf("silence requires either an alert ID or --rule")
	}
	return nil
}

func validateStoreSilenceItem(rd *store.Reader, itemID string) error {
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
		return err
	}
	d := rec.Parsed
	var st sql.NullString
	err = rd.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("item %s not found", itemID)
	}
	if err != nil {
		return err
	}
	if st.String == d.Closed || slices.Contains(d.Finished, st.String) {
		return fmt.Errorf("item %s is %s; silence requires an open item", itemID, st.String)
	}
	return nil
}

func validateSourceSilenceItem(ctx context.Context, src ObservationSource, itemID string) error {
	lookup := newSourceItemTerminal(ctx, src)
	if !lookup.ready {
		// Standalone mode without items/charter projections accepts any non-empty reference (D-AW10).
		return nil
	}
	state, exists, err := lookup.stateOf(itemID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("item %s not found", itemID)
	}
	if lookup.isTerminal(state) {
		return fmt.Errorf("item %s is %s; silence requires an open item", itemID, state)
	}
	return nil
}

func validateSilenceRuleVars(r Rule, vars map[string]string) (string, error) {
	varNames := r.VariableNames()
	if len(varNames) == 0 {
		if len(vars) > 0 {
			return "", fmt.Errorf("%w: rule %s is global and accepts no --var", ErrInvalidSilenceBinding, r.ID)
		}
		return "{}", nil
	}
	if len(vars) == 0 {
		return "", fmt.Errorf("%w: rule %s requires at least one --var (%s)", ErrInvalidSilenceBinding, r.ID, strings.Join(varNames, ", "))
	}
	for k, v := range vars {
		if !slices.Contains(varNames, k) {
			return "", fmt.Errorf("%w: unknown variable %q for rule %s (valid: %s)", ErrInvalidSilenceBinding, k, r.ID, strings.Join(varNames, ", "))
		}
		if strings.TrimSpace(v) == "" {
			return "", fmt.Errorf("%w: variable %q must not be empty", ErrInvalidSilenceBinding, k)
		}
	}
	return encodeVariablesJSON(vars), nil
}

func (e Evaluator) findRuleRead(rd *store.Reader, ruleID string) (Rule, bool, error) {
	if rd != nil {
		rules, _, err := e.effective(rd)
		if err != nil {
			return Rule{}, false, err
		}
		for _, r := range rules {
			if r.ID == ruleID {
				return r, true, nil
			}
		}
	}
	for _, r := range e.Rules {
		if r.ID == ruleID {
			return r, true, nil
		}
	}
	return Rule{}, false, nil
}

func newSilenceID() string {
	var randomID [16]byte
	rand.Read(randomID[:])
	return "sil-" + hex.EncodeToString(randomID[:])
}

type silencePlan struct {
	ruleID       string
	varsJSON     string
	vars         map[string]string
	invCause     string
	invEvidence  string
	inlineInv    bool
	untilMS      *int64
	fingerprint  string
	sourceAlert  string
	targetIDs    []string // active episode IDs to transition to silenced
	resolvedOnly bool     // true if AlertID is resolved and we only settle it
}

func (e Evaluator) planStoreSilence(rd *store.Reader, opts SilenceOptions, now time.Time) (silencePlan, bool, error) {
	var plan silencePlan
	if err := validateSilenceActorAndReason(opts); err != nil {
		return plan, false, err
	}
	if opts.Cause != "" || opts.Evidence != "" {
		if strings.TrimSpace(opts.Cause) == "" || strings.EqualFold(strings.TrimSpace(opts.Cause), "unknown") || strings.TrimSpace(opts.Evidence) == "" {
			return plan, false, fmt.Errorf("%w: inline --cause and --evidence must both be non-empty and --cause must not be %q", ErrInvestigationRequired, "unknown")
		}
		plan.inlineInv = true
		plan.invCause = opts.Cause
		plan.invEvidence = opts.Evidence
	}
	if opts.Until != nil {
		expires, err := silenceTime(*opts.Until, now)
		if err != nil {
			if opts.AlertID != "" {
				if a, aerr := readEpisodeAction(rd, opts.AlertID); aerr == nil {
					return plan, false, fmt.Errorf("%s: %w", a.refuse(opts.AlertID), err)
				}
			}
			return plan, false, err
		}
		ms := expires.UnixMilli()
		if expires.After(time.UnixMilli(ms)) {
			ms++
		}
		plan.untilMS = &ms
	}

	hasSchema, err := hasSilenceSchema(rd)
	if err != nil {
		return plan, false, err
	}
	needMigrate := !hasSchema

	if opts.AlertID != "" {
		a, err := readEpisodeAction(rd, opts.AlertID)
		if err != nil {
			return plan, false, err
		}
		if a.settlementState == "settled" {
			return plan, false, fmt.Errorf("alert %s is already settled", opts.AlertID)
		}
		if !plan.inlineInv {
			if a.settlementState != "investigated" {
				return plan, false, fmt.Errorf("%w: %s", ErrInvestigationRequired, a.refuse(opts.AlertID))
			}
			var cause, evidence sql.NullString
			if err := rd.QueryRow(`SELECT investigation_cause, investigation_evidence FROM alert_episodes WHERE alert_id = ?`, opts.AlertID).Scan(&cause, &evidence); err != nil {
				return plan, false, err
			}
			if strings.TrimSpace(cause.String) == "" || strings.TrimSpace(evidence.String) == "" {
				return plan, false, fmt.Errorf("%w: %s", ErrInvestigationRequired, a.refuse(opts.AlertID))
			}
			plan.invCause = cause.String
			plan.invEvidence = evidence.String
		}
		ruleID, _ := a.payload["rule_id"].(string)
		plan.ruleID = ruleID
		plan.sourceAlert = opts.AlertID

		var rawVars string
		hasVarsCol, err := alertEpisodesHasColumn(rd, "variables_json")
		if err != nil {
			return plan, false, err
		}
		if hasVarsCol {
			if err := rd.QueryRow(`SELECT variables_json FROM alert_episodes WHERE alert_id = ?`, opts.AlertID).Scan(&rawVars); err != nil {
				return plan, false, err
			}
		}
		if len(opts.Vars) > 0 {
			r, found, err := e.findRuleRead(rd, ruleID)
			if err != nil {
				return plan, false, err
			}
			if found {
				vj, err := validateSilenceRuleVars(r, opts.Vars)
				if err != nil {
					return plan, false, err
				}
				plan.varsJSON = vj
				plan.vars = decodeVariablesJSON(vj)
			} else {
				plan.varsJSON = encodeVariablesJSON(opts.Vars)
				plan.vars = decodeVariablesJSON(plan.varsJSON)
			}
		} else {
			plan.vars = decodeVariablesJSON(rawVars)
			plan.varsJSON = encodeVariablesJSON(plan.vars)
		}

		if opts.UntilChange {
			if a.state == "resolved" {
				return plan, false, fmt.Errorf("%s: --until-change requires an active episode", a.refuse(opts.AlertID))
			}
			r, found, err := e.findRuleRead(rd, ruleID)
			if err != nil {
				return plan, false, err
			}
			if (found && len(r.When.Fingerprint) == 0) || a.fingerprint == "" {
				return plan, false, fmt.Errorf("%s and has no fingerprint; use --until", a.refuse(opts.AlertID))
			}
			plan.fingerprint = a.fingerprint
		}
		if a.state == "resolved" {
			plan.resolvedOnly = true
		} else {
			plan.targetIDs = []string{opts.AlertID}
		}
	} else {
		r, found, err := e.findRuleRead(rd, opts.RuleID)
		if err != nil {
			return plan, false, err
		}
		if !found {
			return plan, false, fmt.Errorf("rule %q not found", opts.RuleID)
		}
		if !plan.inlineInv {
			return plan, false, fmt.Errorf("%w: silencing by --rule requires --cause and --evidence", ErrInvestigationRequired)
		}
		vj, err := validateSilenceRuleVars(r, opts.Vars)
		if err != nil {
			return plan, false, err
		}
		plan.ruleID = opts.RuleID
		plan.varsJSON = vj
		plan.vars = decodeVariablesJSON(vj)

		hasAlerts, err := tableExists(rd, "alert_episodes")
		if err != nil {
			return plan, false, err
		}
		if hasAlerts {
			hasVarsCol, err := alertEpisodesHasColumn(rd, "variables_json")
			if err != nil {
				return plan, false, err
			}
			q := `SELECT alert_id, condition_fingerprint`
			if hasVarsCol {
				q += `, variables_json`
			}
			q += ` FROM alert_episodes WHERE rule_id = ? AND state != 'resolved' ORDER BY opened_seq ASC, alert_id ASC`
			rows, err := rd.Query(q, opts.RuleID)
			if err != nil {
				return plan, false, err
			}
			for rows.Next() {
				var aid, fp, epVars string
				if hasVarsCol {
					if err := rows.Scan(&aid, &fp, &epVars); err != nil {
						rows.Close()
						return plan, false, err
					}
				} else {
					if err := rows.Scan(&aid, &fp); err != nil {
						rows.Close()
						return plan, false, err
					}
					epVars = "{}"
				}
				if silenceMatches(plan.varsJSON, epVars) {
					plan.targetIDs = append(plan.targetIDs, aid)
					if plan.fingerprint == "" && fp != "" {
						plan.fingerprint = fp
					}
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return plan, false, err
			}
			rows.Close()
		}
		if opts.UntilChange {
			if len(r.When.Fingerprint) == 0 {
				return plan, false, fmt.Errorf("rule %s has no fingerprint; use --until", opts.RuleID)
			}
			if len(plan.targetIDs) == 0 || plan.fingerprint == "" {
				return plan, false, fmt.Errorf("rule %s has no active matching episode with a fingerprint; use --until", opts.RuleID)
			}
		}
	}

	if err := validateStoreSilenceItem(rd, opts.ItemID); err != nil {
		return plan, false, err
	}

	hasSilTable, err := tableExists(rd, "watch_silences")
	if err != nil {
		return plan, false, err
	}
	if hasSilTable {
		var existingID string
		err := rd.QueryRow(`SELECT silence_id FROM watch_silences WHERE rule_id = ? AND vars_json = ? AND lifted_at IS NULL`,
			plan.ruleID, plan.varsJSON).Scan(&existingID)
		if err == nil {
			return plan, false, fmt.Errorf("rule %s with vars %s is already silenced by %s", plan.ruleID, plan.varsJSON, existingID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return plan, false, err
		}
	}
	return plan, needMigrate, nil
}

// NeedsSilenceMigration validates a silence operation read-only and reports
// whether the store lacks watch_silences or the required alert_episodes columns.
func (e Evaluator) NeedsSilenceMigration(ctx context.Context, opts SilenceOptions) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	var need bool
	now := e.now()
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		_, n, err := e.planStoreSilence(rd, opts, now)
		need = n
		return err
	})
	return need, err
}

// Silence creates a durable, item-linked, bounded silence in watch_silences and
// transitions any matching active episodes (or settles a resolved AlertID episode).
func (e Evaluator) Silence(ctx context.Context, opts SilenceOptions) (SilenceRecord, error) {
	if e.Store == nil {
		if e.Source == nil {
			return SilenceRecord{}, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.silenceMemory(ctx, opts)
	}
	var rec SilenceRecord
	now := e.now()
	err := e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		plan, needMigrate, err := e.planStoreSilence(tx.Reader(), opts, now)
		if err != nil {
			return err
		}
		if needMigrate {
			return fmt.Errorf("watch_silences schema missing; migrate store first")
		}
		silID := newSilenceID()
		var untilArg, fpArg, srcAlertArg any
		if plan.untilMS != nil {
			untilArg = *plan.untilMS
		}
		if plan.fingerprint != "" {
			fpArg = plan.fingerprint
		}
		if plan.sourceAlert != "" {
			srcAlertArg = plan.sourceAlert
		}
		if _, err := tx.Exec(`INSERT INTO watch_silences
			(silence_id, rule_id, vars_json, tracking_item_id, created_by, created_at, reason, investigation_cause, investigation_evidence, source_alert_id, silence_until_ms, silence_on_fingerprint)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			silID, plan.ruleID, plan.varsJSON, opts.ItemID, opts.By, now.UnixMilli(), opts.Reason, plan.invCause, plan.invEvidence, srcAlertArg, untilArg, fpArg,
		); err != nil {
			return err
		}

		updateEpisode := func(aid string, keepResolved bool) error {
			a, err := readEpisodeAction(tx.Reader(), aid)
			if err != nil {
				return err
			}
			settledSeq := a.lastObservedSeq
			if a.trackedLagPair.Valid && a.trackedLagPair.String != "" {
				headSeq, err := tx.Reader().EventHeadSeq()
				if err != nil {
					return err
				}
				settledSeq = max(settledSeq, headSeq)
			}
			if plan.inlineInv {
				invPayload := map[string]any{
					"alert_id":    aid,
					"rule_id":     a.payload["rule_id"],
					"subject_key": a.payload["subject_key"],
					"severity":    a.payload["severity"],
					"by":          opts.By,
					"cause":       plan.invCause,
					"evidence":    plan.invEvidence,
				}
				if err := appendAlert(tx, aid, investigatedKind, now, invPayload); err != nil {
					return err
				}
			}
			newState := "silenced"
			if keepResolved {
				newState = "resolved"
			}
			if plan.inlineInv {
				_, err = tx.Exec(`UPDATE alert_episodes SET
					state=?, settlement_state='settled', outcome='silence',
					tracking_item_id=?, silence_id=?, silence_until_ms=?, silence_on_fingerprint=?,
					investigated_by=?, investigated_at=?, investigation_cause=?, investigation_evidence=?,
					settled_by=?, settled_at=?, settled_seq=?, settled_fingerprint=condition_fingerprint, settled_lag_pair=tracked_lag_pair
					WHERE alert_id=?`,
					newState, opts.ItemID, silID, untilArg, fpArg,
					opts.By, now.UnixMilli(), plan.invCause, plan.invEvidence,
					opts.By, now.UnixMilli(), settledSeq, aid)
			} else {
				_, err = tx.Exec(`UPDATE alert_episodes SET
					state=?, settlement_state='settled', outcome='silence',
					tracking_item_id=?, silence_id=?, silence_until_ms=?, silence_on_fingerprint=?,
					settled_by=?, settled_at=?, settled_seq=?, settled_fingerprint=condition_fingerprint, settled_lag_pair=tracked_lag_pair
					WHERE alert_id=?`,
					newState, opts.ItemID, silID, untilArg, fpArg,
					opts.By, now.UnixMilli(), settledSeq, aid)
			}
			if err != nil {
				return err
			}
			silPayload := map[string]any{
				"alert_id":    aid,
				"rule_id":     a.payload["rule_id"],
				"subject_key": a.payload["subject_key"],
				"severity":    a.payload["severity"],
				"silence_id":  silID,
				"by":          opts.By,
				"item_id":     opts.ItemID,
				"reason":      opts.Reason,
				"vars":        plan.vars,
			}
			if plan.untilMS != nil {
				silPayload["until"] = time.UnixMilli(*plan.untilMS).UTC().Format(time.RFC3339Nano)
			}
			if opts.UntilChange {
				silPayload["until_change"] = true
			}
			return appendAlert(tx, aid, silencedKind, now, silPayload)
		}

		if plan.resolvedOnly && opts.AlertID != "" {
			if err := updateEpisode(opts.AlertID, true); err != nil {
				return err
			}
		} else {
			for _, aid := range plan.targetIDs {
				if err := updateEpisode(aid, false); err != nil {
					return err
				}
			}
		}

		rec = SilenceRecord{
			SilenceID:             silID,
			RuleID:                plan.ruleID,
			Vars:                  plan.vars,
			VarsJSON:              plan.varsJSON,
			TrackingItemID:        opts.ItemID,
			CreatedBy:             opts.By,
			CreatedAt:             now.UnixMilli(),
			Reason:                opts.Reason,
			InvestigationCause:    plan.invCause,
			InvestigationEvidence: plan.invEvidence,
			SourceAlertID:         plan.sourceAlert,
			SilenceUntilMS:        plan.untilMS,
			SilenceOnFingerprint:  plan.fingerprint,
		}
		return nil
	})
	return rec, err
}

// ListSilences returns silences from watch_silences, optionally including lifted ones.
func (e Evaluator) ListSilences(ctx context.Context, includeLifted bool) ([]SilenceRecord, error) {
	if e.Store == nil {
		if e.Source == nil {
			return nil, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.listSilences(includeLifted), nil
	}
	var out []SilenceRecord
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		hasTable, err := tableExists(rd, "watch_silences")
		if err != nil || !hasTable {
			return err
		}
		hasSilenceIDCol, err := alertEpisodesHasColumn(rd, "silence_id")
		if err != nil {
			return err
		}
		matchExpr := "0"
		if hasSilenceIDCol {
			matchExpr = "(SELECT COUNT(*) FROM alert_episodes a WHERE a.silence_id = s.silence_id)"
		}
		q := `SELECT s.silence_id, s.rule_id, s.vars_json, s.tracking_item_id, s.created_by, s.created_at,
			s.reason, s.investigation_cause, s.investigation_evidence,
			COALESCE(s.source_alert_id, ''), s.silence_until_ms, COALESCE(s.silence_on_fingerprint, ''),
			s.lifted_at, COALESCE(s.lifted_by, ''), COALESCE(s.lifted_reason, ''), ` + matchExpr + `
			FROM watch_silences s`
		if !includeLifted {
			q += ` WHERE s.lifted_at IS NULL`
		}
		q += ` ORDER BY s.created_at DESC, s.silence_id ASC`
		rows, err := rd.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r SilenceRecord
			var untilMS, liftedAt sql.NullInt64
			if err := rows.Scan(
				&r.SilenceID, &r.RuleID, &r.VarsJSON, &r.TrackingItemID, &r.CreatedBy, &r.CreatedAt,
				&r.Reason, &r.InvestigationCause, &r.InvestigationEvidence,
				&r.SourceAlertID, &untilMS, &r.SilenceOnFingerprint,
				&liftedAt, &r.LiftedBy, &r.LiftedReason, &r.MatchedAlerts,
			); err != nil {
				return err
			}
			r.Vars = decodeVariablesJSON(r.VarsJSON)
			if untilMS.Valid {
				v := untilMS.Int64
				r.SilenceUntilMS = &v
			}
			if liftedAt.Valid {
				v := liftedAt.Int64
				r.LiftedAt = &v
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// NeedsLiftSilenceMigration validates a lift operation read-only and reports
// whether schema migration is needed.
func (e Evaluator) NeedsLiftSilenceMigration(ctx context.Context, silenceID, by, reason string) (bool, error) {
	if e.Store == nil {
		return false, nil
	}
	var need bool
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		if strings.TrimSpace(silenceID) == "" {
			return fmt.Errorf("silence ID is required")
		}
		if strings.TrimSpace(by) == "" {
			return fmt.Errorf("--by is required and must not be empty")
		}
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("--reason is required and must not be empty")
		}
		hasTable, err := tableExists(rd, "watch_silences")
		if err != nil {
			return err
		}
		if !hasTable {
			return fmt.Errorf("silence %s not found", silenceID)
		}
		var liftedAt sql.NullInt64
		err = rd.QueryRow(`SELECT lifted_at FROM watch_silences WHERE silence_id = ?`, silenceID).Scan(&liftedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("silence %s not found", silenceID)
		}
		if err != nil {
			return err
		}
		if liftedAt.Valid {
			return fmt.Errorf("silence %s is already lifted", silenceID)
		}
		ok, err := hasSilenceSchema(rd)
		if err != nil {
			return err
		}
		need = !ok
		return nil
	})
	return need, err
}

// LiftSilence manually lifts an active silence, appends watch.silence.lifted,
// and reopens any still-firing silenced episode linked to it as open and uninvestigated.
func (e Evaluator) LiftSilence(ctx context.Context, silenceID, by, reason string) error {
	if strings.TrimSpace(silenceID) == "" {
		return fmt.Errorf("silence ID is required")
	}
	if strings.TrimSpace(by) == "" {
		return fmt.Errorf("--by is required and must not be empty")
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("--reason is required and must not be empty")
	}
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.liftSilence(silenceID, by, reason, e.now())
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		hasTable, err := tableExists(tx.Reader(), "watch_silences")
		if err != nil {
			return err
		}
		if !hasTable {
			return fmt.Errorf("silence %s not found", silenceID)
		}
		var ruleID, varsJSON, itemID string
		var liftedAt sql.NullInt64
		err = tx.Reader().QueryRow(`SELECT rule_id, vars_json, tracking_item_id, lifted_at FROM watch_silences WHERE silence_id = ?`, silenceID).
			Scan(&ruleID, &varsJSON, &itemID, &liftedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("silence %s not found", silenceID)
		}
		if err != nil {
			return err
		}
		if liftedAt.Valid {
			return fmt.Errorf("silence %s is already lifted", silenceID)
		}
		now := e.now()
		if _, err := tx.Exec(`UPDATE watch_silences SET lifted_at = ?, lifted_by = ?, lifted_reason = ? WHERE silence_id = ?`,
			now.UnixMilli(), by, reason, silenceID); err != nil {
			return err
		}
		if err := appendSilenceLifted(tx, silenceID, now, map[string]any{
			"silence_id": silenceID,
			"rule_id":    ruleID,
			"vars":       decodeVariablesJSON(varsJSON),
			"item_id":    itemID,
			"by":         by,
			"reason":     reason,
		}); err != nil {
			return err
		}

		hasSilenceIDCol, err := alertEpisodesHasColumn(tx.Reader(), "silence_id")
		if err != nil || !hasSilenceIDCol {
			return err
		}
		rows, err := tx.Reader().Query(`SELECT alert_id, subject_key, severity FROM alert_episodes WHERE silence_id = ? AND state = 'silenced' ORDER BY alert_id ASC`, silenceID)
		if err != nil {
			return err
		}
		type epInfo struct{ id, subject, severity string }
		var eps []epInfo
		for rows.Next() {
			var ep epInfo
			if err := rows.Scan(&ep.id, &ep.subject, &ep.severity); err != nil {
				rows.Close()
				return err
			}
			eps = append(eps, ep)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		for _, ep := range eps {
			if _, err := tx.Exec(`UPDATE alert_episodes SET
				state='open',
				silence_id=NULL,
				silence_until_ms=NULL,
				silence_on_fingerprint=NULL,
				tracking_item_id=NULL,
				acked_by=NULL, acked_note=NULL, acked_at=NULL, acked_seq=NULL, acked_fingerprint=NULL, acked_lag_pair=NULL,
				settlement_state='uninvestigated',
				outcome=NULL, item_link_kind=NULL, settled_by=NULL, settled_at=NULL, settled_seq=NULL, settled_fingerprint=NULL, settled_lag_pair=NULL
				WHERE alert_id = ?`, ep.id); err != nil {
				return err
			}
			if err := appendAlert(tx, ep.id, reopenedKind, now, map[string]any{
				"alert_id":    ep.id,
				"rule_id":     ruleID,
				"subject_key": ep.subject,
				"severity":    ep.severity,
				"cause":       "silence-lifted",
				"silence_id":  silenceID,
				"by":          by,
				"reason":      reason,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

type silenceEvalState struct {
	active           []SilenceRecord
	liftedNow        []SilenceRecord
	liftedCauses     map[string]string
	recentItemLifted []SilenceRecord
}

func loadSilenceEvalState(rd *store.Reader) (silenceEvalState, error) {
	var state silenceEvalState
	hasTable, err := tableExists(rd, "watch_silences")
	if err != nil || !hasTable {
		return state, err
	}
	rows, err := rd.Query(`SELECT silence_id, rule_id, vars_json, tracking_item_id, created_by, created_at,
		reason, investigation_cause, investigation_evidence, COALESCE(source_alert_id, ''),
		silence_until_ms, COALESCE(silence_on_fingerprint, ''), lifted_at, COALESCE(lifted_by, ''), COALESCE(lifted_reason, '')
		FROM watch_silences
		WHERE lifted_at IS NULL OR lifted_reason LIKE 'item-%'
		ORDER BY created_at ASC, silence_id ASC`)
	if err != nil {
		if isMissingTableError(err, "watch_silences") {
			return state, nil
		}
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SilenceRecord
		var untilMS, liftedAt sql.NullInt64
		if err := rows.Scan(
			&r.SilenceID, &r.RuleID, &r.VarsJSON, &r.TrackingItemID, &r.CreatedBy, &r.CreatedAt,
			&r.Reason, &r.InvestigationCause, &r.InvestigationEvidence, &r.SourceAlertID,
			&untilMS, &r.SilenceOnFingerprint, &liftedAt, &r.LiftedBy, &r.LiftedReason,
		); err != nil {
			return state, err
		}
		r.Vars = decodeVariablesJSON(r.VarsJSON)
		if untilMS.Valid {
			v := untilMS.Int64
			r.SilenceUntilMS = &v
		}
		if liftedAt.Valid {
			v := liftedAt.Int64
			r.LiftedAt = &v
			state.recentItemLifted = append(state.recentItemLifted, r)
		} else {
			state.active = append(state.active, r)
		}
	}
	return state, rows.Err()
}

func (s *silenceEvalState) evaluateAutoLift(rules []Rule, derived []map[string]*condition, activeByRule func(string) map[string]episode, startHead int64, now time.Time, lookup itemTerminalChecker) {
	if len(s.active) == 0 {
		return
	}
	ruleIdx := make(map[string]int, len(rules))
	for i, r := range rules {
		ruleIdx[r.ID] = i
	}
	var remaining []SilenceRecord
	for _, sil := range s.active {
		var liftReason, liftCause string
		if lookup.ready {
			itemState, itemExists, err := lookup.stateOf(sil.TrackingItemID)
			if err == nil && (!itemExists || lookup.isTerminal(itemState)) {
				if !itemExists {
					liftReason = "item-missing"
				} else {
					liftReason = "item-" + itemState
				}
				liftCause = "silence-" + liftReason
			}
		}
		if liftReason == "" && sil.SilenceUntilMS != nil && !now.Before(time.UnixMilli(*sil.SilenceUntilMS)) {
			liftReason = "silence-expired"
			liftCause = "silence-expired"
		}
		if liftReason == "" && sil.SilenceOnFingerprint != "" {
			if idx, ok := ruleIdx[sil.RuleID]; ok && idx < len(derived) && derived[idx] != nil {
				r := rules[idx]
				eps := activeByRule(sil.RuleID)
				for key, c := range derived[idx] {
					if !r.Enabled || c == nil || !c.fires(r.When) {
						continue
					}
					varsJSON := encodeVariablesJSON(extractVariables(r, key, c))
					if !silenceMatches(sil.VarsJSON, varsJSON) {
						continue
					}
					since := startHead
					if ep, exists := eps[key]; exists {
						since = ep.last
					}
					newest := conditionNewest(r, c, since)
					if newest.fingerprint != sil.SilenceOnFingerprint {
						liftReason = "fingerprint-changed"
						liftCause = "fingerprint-changed"
						break
					}
				}
			}
			if liftReason == "" {
				for _, ep := range activeByRule(sil.RuleID) {
					if ep.state == "silenced" && ep.silenceID.String == sil.SilenceID && ep.conditionFingerprint != "" && ep.conditionFingerprint != sil.SilenceOnFingerprint {
						liftReason = "fingerprint-changed"
						liftCause = "fingerprint-changed"
						break
					}
				}
			}
		}
		if liftReason != "" {
			liftedAt := now.UnixMilli()
			sil.LiftedAt = &liftedAt
			sil.LiftedBy = "evaluator"
			sil.LiftedReason = liftReason
			s.liftedNow = append(s.liftedNow, sil)
			if s.liftedCauses == nil {
				s.liftedCauses = map[string]string{}
			}
			s.liftedCauses[sil.SilenceID] = liftCause
			if strings.HasPrefix(liftReason, "item-") {
				s.recentItemLifted = append(s.recentItemLifted, sil)
			}
			continue
		}
		remaining = append(remaining, sil)
	}
	s.active = remaining
}

func (s silenceEvalState) isActiveID(silenceID string) bool {
	if silenceID == "" {
		return false
	}
	for _, sil := range s.active {
		if sil.SilenceID == silenceID {
			return true
		}
	}
	return false
}

func (s silenceEvalState) liftCauseFor(silenceID string) string {
	if silenceID == "" {
		return ""
	}
	if cause, ok := s.liftedCauses[silenceID]; ok {
		return cause
	}
	return ""
}

func (s silenceEvalState) matchActive(ruleID, varsJSON string) *SilenceRecord {
	for i := range s.active {
		sil := &s.active[i]
		if sil.RuleID == ruleID && silenceMatches(sil.VarsJSON, varsJSON) {
			return sil
		}
	}
	return nil
}

func (s silenceEvalState) recentItemLiftCause(ruleID, varsJSON string, sinceMS int64) string {
	var best *SilenceRecord
	for i := range s.recentItemLifted {
		sil := &s.recentItemLifted[i]
		if sil.RuleID != ruleID || sil.LiftedAt == nil || !silenceMatches(sil.VarsJSON, varsJSON) {
			continue
		}
		if *sil.LiftedAt >= sinceMS && (best == nil || *sil.LiftedAt > *best.LiftedAt) {
			best = sil
		}
	}
	if best == nil {
		return ""
	}
	return "silence-" + best.LiftedReason
}
