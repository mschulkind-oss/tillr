package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// DefaultUnsettledRemindAfter is the default interval after which an unsettled
// alert routed or escalated to the operating session is re-emitted by wait.
const DefaultUnsettledRemindAfter = 15 * time.Minute

// Notice's Seq refers to the latest opening or reopening, not the fact that caused it.
type Notice struct {
	Seq     int64
	AlertID string
	Bundle  Bundle
}

// UnsettledReminder is a due reminder for an unsettled episode routed or
// escalated to the operating session.
type UnsettledReminder struct {
	AlertID      string
	RuleID       string
	Subject      string
	State        string
	Severity     string
	Settlement   string
	UnsettledFor string
	Bundle       Bundle
}

// Pending returns pending alerts for owner in notice sequence order.
func (e Evaluator) Pending(ctx context.Context, owner string) ([]Notice, error) {
	if e.Store == nil {
		if e.Source == nil {
			return nil, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.pending(owner, e.Rules)
	}
	// Preserve file-rule routing for initial openings. Reopenings are new
	// notices routed by the effective rules, independent of eligibility.
	fileMatched := rulesForOwner(owner, e.Rules)
	triageOwner := isTriageOwner(owner)
	acceptsEscalations := ownerAcceptsEscalations(owner)
	var notices []Notice
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		hasSettlement, err := alertEpisodesHasColumn(rd, "settlement_state")
		if err != nil {
			return err
		}
		hasActiveTriage, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
		if err != nil {
			return err
		}
		hasEscalated, err := alertEpisodesHasColumn(rd, "escalated_to")
		if err != nil {
			return err
		}
		hasTriageState, err := alertEpisodesHasColumn(rd, "triage_state")
		if err != nil {
			return err
		}
		settlementExpr := "'uninvestigated'"
		if hasSettlement {
			settlementExpr = "COALESCE(NULLIF(a.settlement_state, ''), 'uninvestigated')"
		}
		activeTriageExpr := "''"
		if hasActiveTriage {
			activeTriageExpr = "COALESCE(a.active_triage_run_id, '')"
		}
		escalatedExpr := "''"
		if hasEscalated {
			escalatedExpr = "COALESCE(a.escalated_to, '')"
		}
		triageStateExpr := "'untriaged'"
		if hasTriageState {
			triageStateExpr = "COALESCE(NULLIF(a.triage_state, ''), 'untriaged')"
		}
		rows, err := rd.Query(`WITH latest_notice AS (
      SELECT n.object_id AS alert_id, MAX(n.seq) AS seq
      FROM events n
      WHERE n.kind IN (?,?,?)
        AND n.seq > COALESCE((SELECT seen_seq FROM foreground_delivery WHERE owner_handle=?),0)
        AND n.object_id IS NOT NULL
      GROUP BY n.object_id
   )
   SELECT v.seq,v.kind,a.alert_id,a.rule_id,a.state,`+settlementExpr+`,`+activeTriageExpr+`,`+escalatedExpr+`,`+triageStateExpr+`,a.bundle_json
   FROM latest_notice m
   JOIN alert_episodes a ON a.alert_id=m.alert_id
   JOIN events v ON v.seq=m.seq
   ORDER BY v.seq`, openedKind, reopenedKind, escalatedKind, owner)
		if err != nil {
			return err
		}
		defer rows.Close()
		var effectiveMatched map[string]bool
		for rows.Next() {
			var n Notice
			var kind, id, epState, settlement, activeRun, escalatedTo, triageState string
			var bundle []byte
			if err := rows.Scan(&n.Seq, &kind, &n.AlertID, &id, &epState, &settlement, &activeRun, &escalatedTo, &triageState, &bundle); err != nil {
				return err
			}
			if kind == escalatedKind {
				if !acceptsEscalations || escalatedTo == "owner" {
					continue
				}
			} else {
				if triageOwner {
					if triageState != "untriaged" || settlement == "settled" || activeRun != "" || escalatedTo != "" {
						continue
					}
					if epState != "open" && !(epState == "resolved" && settlement == "uninvestigated") {
						continue
					}
				} else {
					if epState != "open" || settlement == "settled" || activeRun != "" || escalatedTo == "owner" {
						continue
					}
				}
				matched := fileMatched
				if kind == reopenedKind {
					if effectiveMatched == nil {
						rules, err := ReadEffectiveRules(rd, e.Rules)
						if err != nil {
							return err
						}
						effectiveMatched = rulesForOwner(owner, rules)
					}
					matched = effectiveMatched
				}
				if !matched[id] {
					continue
				}
			}
			if err := json.Unmarshal(bundle, &n.Bundle); err != nil {
				return err
			}
			notices = append(notices, n)
		}
		return rows.Err()
	})
	return notices, err
}

func isTriageOwner(owner string) bool {
	return owner == "tillr-triage" || owner == "triage"
}

func ownerAcceptsEscalations(owner string) bool {
	return !isTriageOwner(owner)
}

func receiverMatchesOwner(receiver, owner string) bool {
	switch {
	case isTriageOwner(owner):
		return receiver == "triage"
	case owner == "operator-session":
		return receiver == "operator-session" || receiver == "foreground"
	default:
		return receiver == "foreground"
	}
}

func rulesForOwner(owner string, rules []Rule) map[string]bool {
	matched := map[string]bool{}
	for _, r := range rules {
		if receiverMatchesOwner(r.Delivery.Receiver, owner) {
			matched[r.ID] = true
		}
	}
	return matched
}

// Seen confirms a printed cursor without changing alert state.
func (e Evaluator) Seen(ctx context.Context, owner string, seq int64) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.seen(owner, seq)
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		var shown, seen int64
		err := tx.QueryRow(`SELECT shown_seq,seen_seq FROM foreground_delivery WHERE owner_handle=?`, owner).Scan(&shown, &seen)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if seq > shown {
			return fmt.Errorf("seen cursor %d exceeds shown_seq %d for %q", seq, shown, owner)
		}
		if seq < 0 {
			return fmt.Errorf("seen cursor must be nonnegative")
		}
		if seq <= seen {
			return nil
		}
		_, err = tx.Exec(`UPDATE foreground_delivery SET seen_seq=? WHERE owner_handle=?`, seq, owner)
		return err
	})
}

// Shown records successful printing, never confirmation.
func (e Evaluator) Shown(ctx context.Context, owner string, seq int64) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.shown(owner, seq)
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle,shown_seq) VALUES (?,?)
   ON CONFLICT(owner_handle) DO UPDATE SET shown_seq=MAX(shown_seq,excluded.shown_seq) WHERE shown_seq<excluded.shown_seq`, owner, seq)
		return err
	})
}

func (e Evaluator) BeginWait(ctx context.Context, owner string) (time.Time, error) {
	if e.Store == nil {
		if e.Source == nil {
			return time.Time{}, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return e.mem.beginWait(owner, e.now()), nil
	}
	now := e.now()
	err := e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle,waiting_since,last_wait_at) VALUES (?,?,?)
   ON CONFLICT(owner_handle) DO UPDATE SET waiting_since=excluded.waiting_since,last_wait_at=excluded.last_wait_at`, owner, now.UnixMilli(), now.UnixMilli())
		return err
	})
	return now, err
}

func (e Evaluator) EndWait(ctx context.Context, owner string, start time.Time) error {
	if e.Store == nil {
		if e.Source == nil {
			return fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		e.mem.endWait(owner, start, e.now())
		return nil
	}
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE foreground_delivery SET waiting_since=NULL,last_wait_at=? WHERE owner_handle=? AND waiting_since=?`, e.now().UnixMilli(), owner, start.UnixMilli())
		return err
	})
}

// UnsettledReminders returns unsettled episodes (including resolved ones) routed
// or escalated to the operating session whose reminder interval has elapsed.
// Episodes escalated to the owner or already pending for owner are excluded.
func (e Evaluator) UnsettledReminders(ctx context.Context, owner string, remindAfter time.Duration) ([]UnsettledReminder, error) {
	if e.Store == nil {
		if e.Source == nil {
			return nil, fmt.Errorf("watch: Evaluator requires Store or Source")
		}
		return nil, nil
	}
	if isTriageOwner(owner) {
		return nil, nil
	}
	if remindAfter <= 0 {
		remindAfter = DefaultUnsettledRemindAfter
	}
	now := e.now()
	var reminders []UnsettledReminder
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		hasAlerts, err := tableExists(rd, "alert_episodes")
		if err != nil || !hasAlerts {
			return err
		}
		hasSettlement, err := alertEpisodesHasColumn(rd, "settlement_state")
		if err != nil || !hasSettlement {
			return err
		}
		hasEscalated, err := alertEpisodesHasColumn(rd, "escalated_to")
		if err != nil {
			return err
		}
		hasReminded, err := alertEpisodesHasColumn(rd, "last_reminded_at")
		if err != nil {
			return err
		}
		hasTriage, err := alertEpisodesHasColumn(rd, "active_triage_run_id")
		if err != nil {
			return err
		}
		hasDelivery, err := tableExists(rd, "foreground_delivery")
		if err != nil {
			return err
		}
		rules, err := ReadEffectiveRules(rd, e.Rules)
		if err != nil {
			return err
		}
		effectiveMatched := rulesForOwner(owner, rules)

		escalatedExpr := "''"
		if hasEscalated {
			escalatedExpr = "COALESCE(a.escalated_to, '')"
		}
		remindedExpr := "NULL"
		if hasReminded {
			remindedExpr = "a.last_reminded_at"
		}
		triageExpr := "''"
		if hasTriage {
			triageExpr = "COALESCE(a.active_triage_run_id, '')"
		}

		query := `SELECT a.alert_id, a.rule_id, a.subject_key, a.state, a.severity,
			COALESCE(NULLIF(a.settlement_state, ''), 'uninvestigated'),
			a.opened_at, ` + remindedExpr + `, ` + escalatedExpr + `, ` + triageExpr + `, a.bundle_json
			FROM alert_episodes a
			WHERE COALESCE(NULLIF(a.settlement_state, ''), 'uninvestigated') <> 'settled'`
		var args []any
		if hasDelivery {
			query += ` AND NOT (
				(a.state = 'open' AND EXISTS (
					SELECT 1 FROM events v
					WHERE v.object_id = a.alert_id AND v.kind IN (?, ?)
					  AND v.seq > COALESCE((SELECT seen_seq FROM foreground_delivery WHERE owner_handle = ?), 0)
				))
				OR EXISTS (
					SELECT 1 FROM events v
					WHERE v.object_id = a.alert_id AND v.kind = ?
					  AND v.seq > COALESCE((SELECT seen_seq FROM foreground_delivery WHERE owner_handle = ?), 0)
				)
			)`
			args = append(args, openedKind, reopenedKind, owner, escalatedKind, owner)
		}
		query += ` ORDER BY a.opened_at, a.alert_id`

		rows, err := rd.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r UnsettledReminder
			var openedAt int64
			var lastReminded sql.NullInt64
			var escalatedTo, triageRunID string
			var bundle []byte
			if err := rows.Scan(&r.AlertID, &r.RuleID, &r.Subject, &r.State, &r.Severity, &r.Settlement,
				&openedAt, &lastReminded, &escalatedTo, &triageRunID, &bundle); err != nil {
				return err
			}
			if escalatedTo == "owner" {
				continue
			}
			if escalatedTo != "operator-session" {
				if triageRunID != "" {
					continue
				}
				if !effectiveMatched[r.RuleID] {
					continue
				}
			}
			anchor := openedAt
			if lastReminded.Valid {
				anchor = lastReminded.Int64
			}
			if now.Sub(time.UnixMilli(anchor)) < remindAfter {
				continue
			}
			if len(bundle) > 0 {
				if err := json.Unmarshal(bundle, &r.Bundle); err != nil {
					return err
				}
			}
			r.UnsettledFor = formatDurationCompact(now.Sub(time.UnixMilli(openedAt)))
			reminders = append(reminders, r)
		}
		return rows.Err()
	})
	return reminders, err
}

// MarkReminded stamps last_reminded_at on the given episodes to prevent
// repeated reminders until unsettled_remind_after elapses again.
func (e Evaluator) MarkReminded(ctx context.Context, alertIDs []string) error {
	if len(alertIDs) == 0 || e.Store == nil {
		return nil
	}
	nowMs := e.now().UnixMilli()
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		hasReminded, err := alertEpisodesHasColumn(tx.Reader(), "last_reminded_at")
		if err != nil || !hasReminded {
			return err
		}
		for _, id := range alertIDs {
			if _, err := tx.Exec(`UPDATE alert_episodes SET last_reminded_at = ? WHERE alert_id = ?`, nowMs, id); err != nil {
				return err
			}
		}
		return nil
	})
}
