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
	Rules  []RuleResult
	Alerts []AlertStatus
	Owners []OwnerStatus
}

type AlertStatus struct {
	ID       string  `json:"alert_id"`
	RuleID   string  `json:"rule_id"`
	Subject  string  `json:"subject"`
	State    string  `json:"state"`
	Severity string  `json:"severity"`
	Opened   string  `json:"opened"`
	Observed int64   `json:"observed"`
	By       *string `json:"by,omitempty"`
	Until    string  `json:"until,omitempty"`
	ItemID   *string `json:"item_id,omitempty"`
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
		s := Status{Rules: results, Alerts: []AlertStatus{}, Owners: []OwnerStatus{}}
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
	s := Status{Alerts: []AlertStatus{}, Owners: []OwnerStatus{}}
	err := e.readEvaluation(ctx, rules, func(rd *store.Reader, results []RuleResult) error {
		s.Rules = results
		hasAlerts, err := tableExists(rd, "alert_episodes")
		if err != nil {
			return err
		}
		if hasAlerts {
			if err := s.readAlerts(rd); err != nil {
				return err
			}
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

func (s *Status) readAlerts(rd *store.Reader) error {
	hasTracking, err := alertEpisodesHasColumn(rd, "tracking_item_id")
	if err != nil {
		return err
	}
	query := `SELECT alert_id, rule_id, subject_key, state, severity, opened_at, observation_count, acked_by, silence_until_ms FROM alert_episodes WHERE state<>'resolved' ORDER BY opened_seq, alert_id`
	if hasTracking {
		query = `SELECT alert_id, rule_id, subject_key, state, severity, opened_at, observation_count, acked_by, silence_until_ms, tracking_item_id FROM alert_episodes WHERE state<>'resolved' ORDER BY opened_seq, alert_id`
	}
	rows, err := rd.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var alert AlertStatus
		var opened int64
		var until sql.NullInt64
		var by, itemID *string
		var scanErr error
		if hasTracking {
			scanErr = rows.Scan(&alert.ID, &alert.RuleID, &alert.Subject, &alert.State, &alert.Severity, &opened, &alert.Observed, &by, &until, &itemID)
		} else {
			scanErr = rows.Scan(&alert.ID, &alert.RuleID, &alert.Subject, &alert.State, &alert.Severity, &opened, &alert.Observed, &by, &until)
		}
		if scanErr != nil {
			return scanErr
		}
		alert.Opened = statusTime(opened)
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
