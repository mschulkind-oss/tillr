package watch

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Status is a read-only snapshot: conditions are evaluated but episodes are not reconciled.
type Status struct {
	Summary  StatusSummary
	Rules    []RuleResult
	Alerts   []AlertStatus
	Silences []SilenceStatus
	Owners   []OwnerStatus
}

// StatusSummary counts episodes by settlement state and active silences.
type StatusSummary struct {
	Unsettled      int `json:"unsettled"`
	Uninvestigated int `json:"uninvestigated"`
	Investigating  int `json:"investigating"`
	Investigated   int `json:"investigated"`
	Settled        int `json:"settled"`
	ActiveSilences int `json:"active_silences"`
}

// StatusOptions controls what ReadStatusWithOptions returns.
type StatusOptions struct {
	OnlyUnsettled bool
}

// SilenceStatus describes one active silence in watch_silences.
type SilenceStatus struct {
	SilenceID     string            `json:"silence_id"`
	RuleID        string            `json:"rule_id"`
	Vars          map[string]string `json:"vars"`
	ItemID        string            `json:"item_id"`
	By            string            `json:"by"`
	Until         string            `json:"until"`
	MatchedAlerts int               `json:"matched_alerts"`
}

type AlertStatus struct {
	ID           string            `json:"alert_id"`
	RuleID       string            `json:"rule_id"`
	Subject      string            `json:"subject"`
	State        string            `json:"state"`
	Severity     string            `json:"severity"`
	Opened       string            `json:"opened"`
	Observed     int64             `json:"observed"`
	Vars         map[string]string `json:"vars"`
	Settlement   string            `json:"settlement,omitempty"`
	UnsettledFor string            `json:"unsettled_for,omitempty"`
	EscalatedTo  string            `json:"escalated_to,omitempty"`
	By           *string           `json:"by,omitempty"`
	Until        string            `json:"until,omitempty"`
	ItemID       *string           `json:"item_id,omitempty"`
}

type OwnerStatus struct {
	Owner    string  `json:"owner"`
	Attached bool    `json:"attached"`
	Since    *string `json:"since"`
	LastWait *string `json:"last_wait"`
	Pending  int     `json:"pending"`
}

// ReadStatus reads existing watch rows without migrating the store or writing
// attachment files. attached queries the kernel for each stored owner.
func (e Evaluator) ReadStatus(ctx context.Context, attached func(string) (bool, error)) (Status, error) {
	return e.ReadStatusWithOptions(ctx, attached, StatusOptions{})
}

// ReadStatusWithOptions reads existing watch rows without migrating the store
// or writing attachment files. When opts.OnlyUnsettled is true, Alerts contains
// every episode whose settlement_state is not settled (including resolved ones).
func (e Evaluator) ReadStatusWithOptions(ctx context.Context, attached func(string) (bool, error), opts StatusOptions) (Status, error) {
	now := e.now()
	if e.Store == nil {
		if e.Source == nil {
			return Status{}, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		if err := requireRoot(e.Root, e.Rules); err != nil {
			return Status{}, err
		}
		results, err := e.RuleResults(ctx)
		if err != nil {
			return Status{}, err
		}
		s := Status{Rules: results, Alerts: []AlertStatus{}, Silences: []SilenceStatus{}, Owners: []OwnerStatus{}}
		e.mem.readStatusInto(&s, e.Rules)
		for i := range s.Owners {
			owner := &s.Owners[i]
			owner.Attached, err = attached(owner.Owner)
			if err != nil {
				return Status{}, err
			}
			if !owner.Attached {
				owner.Since = nil
			}
		}
		return s, nil
	}
	var rules []Rule
	if err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		rules, _, err = e.effective(rd)
		return err
	}); err != nil {
		return Status{}, err
	}
	s := Status{Alerts: []AlertStatus{}, Silences: []SilenceStatus{}, Owners: []OwnerStatus{}}
	err := e.readEvaluation(ctx, rules, func(rd *store.Reader, results []RuleResult) error {
		s.Rules = results
		hasAlerts, err := tableExists(rd, "alert_episodes")
		if err != nil {
			return err
		}
		if hasAlerts {
			if err := s.readSummary(rd); err != nil {
				return err
			}
			if err := s.readAlerts(rd, now, opts.OnlyUnsettled); err != nil {
				return err
			}
		}
		if err := s.readSilences(rd, hasAlerts); err != nil {
			return err
		}
		hasOwners, err := tableExists(rd, "foreground_delivery")
		if err != nil || !hasOwners {
			return err
		}
		return s.readOwners(rd, rules, hasAlerts)
	})
	if err != nil {
		return Status{}, err
	}
	for i := range s.Owners {
		owner := &s.Owners[i]
		owner.Attached, err = attached(owner.Owner)
		if err != nil {
			return Status{}, err
		}
		if !owner.Attached {
			owner.Since = nil
		}
	}
	return s, nil
}

func requireRoot(root string, rules []Rule) error {
	if root == "" {
		for _, r := range rules {
			if r.Enabled && len(r.refs()) != 0 {
				return fmt.Errorf("rule %s requires --root", r.ID)
			}
		}
	}
	return nil
}

func statusTime(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano) }

type alertEpisodeColKey string

func alertEpisodesHasColumn(rd *store.Reader, name string) (bool, error) {
	v, err := rd.ReadOnce(alertEpisodeColKey(name), func() (any, error) {
		var ok bool
		err := rd.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('alert_episodes') WHERE name = ?)`, name).Scan(&ok)
		return ok, err
	})
	if err != nil {
		return false, err
	}
	return v.(bool), nil
}

func formatDurationCompact(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	totalSec := int64(d / time.Second)
	if totalSec < 60 {
		return fmt.Sprintf("%ds", totalSec)
	}
	days := totalSec / 86400
	hours := (totalSec % 86400) / 3600
	mins := (totalSec % 3600) / 60
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh%dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

func (s *Status) readSummary(rd *store.Reader) error {
	hasSettlement, err := alertEpisodesHasColumn(rd, "settlement_state")
	if err != nil {
		return err
	}
	if !hasSettlement {
		rows, err := rd.Query(`SELECT state, COUNT(*) FROM alert_episodes GROUP BY state`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var state string
			var n int
			if err := rows.Scan(&state, &n); err != nil {
				return err
			}
			if state == "resolved" {
				s.Summary.Settled += n
			} else {
				s.Summary.Uninvestigated += n
				s.Summary.Unsettled += n
			}
		}
		return rows.Err()
	}
	rows, err := rd.Query(`SELECT COALESCE(NULLIF(settlement_state, ''), 'uninvestigated'), COUNT(*) FROM alert_episodes GROUP BY 1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sst string
		var n int
		if err := rows.Scan(&sst, &n); err != nil {
			return err
		}
		switch sst {
		case "settled":
			s.Summary.Settled += n
		case "investigating":
			s.Summary.Investigating += n
			s.Summary.Unsettled += n
		case "investigated":
			s.Summary.Investigated += n
			s.Summary.Unsettled += n
		default:
			s.Summary.Uninvestigated += n
			s.Summary.Unsettled += n
		}
	}
	return rows.Err()
}

func (s *Status) readSilences(rd *store.Reader, hasAlerts bool) error {
	hasSilences, err := tableExists(rd, "watch_silences")
	if err != nil || !hasSilences {
		return err
	}
	matchedExpr := "0"
	if hasAlerts {
		hasSilenceID, err := alertEpisodesHasColumn(rd, "silence_id")
		if err != nil {
			return err
		}
		if hasSilenceID {
			matchedExpr = "(SELECT COUNT(*) FROM alert_episodes a WHERE a.silence_id = s.silence_id)"
		}
	}
	rows, err := rd.Query(`SELECT s.silence_id, s.rule_id, s.vars_json, s.tracking_item_id, s.created_by, s.silence_until_ms, s.silence_on_fingerprint, ` + matchedExpr + `
		FROM watch_silences s
		WHERE s.lifted_at IS NULL
		ORDER BY s.created_at, s.silence_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sil SilenceStatus
		var rawVars string
		var untilMs sql.NullInt64
		var untilFP sql.NullString
		if err := rows.Scan(&sil.SilenceID, &sil.RuleID, &rawVars, &sil.ItemID, &sil.By, &untilMs, &untilFP, &sil.MatchedAlerts); err != nil {
			return err
		}
		sil.Vars = decodeVariablesJSON(rawVars)
		if untilMs.Valid {
			sil.Until = statusTime(untilMs.Int64)
		} else {
			sil.Until = "change"
		}
		s.Silences = append(s.Silences, sil)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	s.Summary.ActiveSilences = len(s.Silences)
	return nil
}

func (s *Status) readAlerts(rd *store.Reader, now time.Time, onlyUnsettled bool) error {
	hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
	if err != nil {
		return err
	}
	hasVars, err := alertEpisodesHasColumn(rd, "variables_json")
	if err != nil {
		return err
	}
	hasSettlement, err := alertEpisodesHasColumn(rd, "settlement_state")
	if err != nil {
		return err
	}
	hasEscalated, err := alertEpisodesHasColumn(rd, "escalated_to")
	if err != nil {
		return err
	}
	cols := "alert_id, rule_id, subject_key, state, severity, opened_at, observation_count, acked_by, silence_until_ms"
	if hasTracking {
		cols += ", tracking_item_id"
	}
	if hasVars {
		cols += ", variables_json"
	}
	if hasSettlement {
		cols += ", COALESCE(NULLIF(settlement_state, ''), 'uninvestigated')"
	}
	if hasEscalated {
		cols += ", COALESCE(escalated_to, '')"
	}
	where := "state<>'resolved'"
	if onlyUnsettled {
		if hasSettlement {
			where = "COALESCE(NULLIF(settlement_state, ''), 'uninvestigated')<>'settled'"
		} else {
			where = "state<>'resolved'"
		}
	}
	rows, err := rd.Query(`SELECT ` + cols + ` FROM alert_episodes WHERE ` + where + ` ORDER BY opened_seq, alert_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var alert AlertStatus
		var opened int64
		var until sql.NullInt64
		var by, itemID *string
		var rawVars sql.NullString
		var settlement, escalated string
		dest := []any{&alert.ID, &alert.RuleID, &alert.Subject, &alert.State, &alert.Severity, &opened, &alert.Observed, &by, &until}
		if hasTracking {
			dest = append(dest, &itemID)
		}
		if hasVars {
			dest = append(dest, &rawVars)
		}
		if hasSettlement {
			dest = append(dest, &settlement)
		}
		if hasEscalated {
			dest = append(dest, &escalated)
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		alert.Opened = statusTime(opened)
		if hasVars {
			alert.Vars = decodeVariablesJSON(rawVars.String)
		} else {
			alert.Vars = map[string]string{}
		}
		if hasSettlement {
			alert.Settlement = settlement
		} else if alert.State == "resolved" {
			alert.Settlement = "settled"
		} else {
			alert.Settlement = "uninvestigated"
		}
		if alert.Settlement != "settled" {
			alert.UnsettledFor = formatDurationCompact(now.Sub(time.UnixMilli(opened)))
		}
		if hasEscalated && escalated != "" {
			alert.EscalatedTo = escalated
		}
		switch alert.State {
		case "acknowledged":
			alert.By = by
		case "silenced":
			alert.Until = "change"
			if until.Valid {
				alert.Until = statusTime(until.Int64)
			}
		}
		if itemID != nil {
			alert.ItemID = itemID
		}
		s.Alerts = append(s.Alerts, alert)
	}
	return rows.Err()
}

func (s *Status) readOwners(rd *store.Reader, rules []Rule, hasAlerts bool) error {
	rows, err := rd.Query(`SELECT owner_handle, waiting_since, last_wait_at FROM foreground_delivery ORDER BY owner_handle`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var owner OwnerStatus
		var since, last sql.NullInt64
		if err := rows.Scan(&owner.Owner, &since, &last); err != nil {
			return err
		}
		if since.Valid {
			v := statusTime(since.Int64)
			owner.Since = &v
		}
		if last.Valid {
			v := statusTime(last.Int64)
			owner.LastWait = &v
		}
		s.Owners = append(s.Owners, owner)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasAlerts {
		for i := range s.Owners {
			if err := s.Owners[i].readPending(rd, rules); err != nil {
				return err
			}
		}
	}
	return nil
}

func (owner *OwnerStatus) readPending(rd *store.Reader, rules []Rule) error {
	foreground := map[string]bool{}
	for _, r := range rules {
		if r.Delivery.Receiver == "foreground" {
			foreground[r.ID] = true
		}
	}
	// EXISTS counts each open episode once, even when several of its
	// opening events are newer than seen_seq.
	rows, err := rd.Query(`SELECT a.rule_id FROM alert_episodes a
		WHERE a.state='open' AND EXISTS (
			SELECT 1 FROM events v WHERE v.object_id=a.alert_id AND v.kind IN (?, ?)
			AND v.seq>(SELECT seen_seq FROM foreground_delivery WHERE owner_handle=?)
		)`, openedKind, reopenedKind, owner.Owner)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ruleID string
		if err := rows.Scan(&ruleID); err != nil {
			return err
		}
		if foreground[ruleID] {
			owner.Pending++
		}
	}
	return rows.Err()
}
