package alarms

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const UnsettledWatchAlerts = "unsettled-watch-alerts"

type unsettledWatchSummary struct {
	ids      []string
	oldestAt int64
}

func readUnsettledWatchAlerts(r *store.Reader) (unsettledWatchSummary, error) {
	var hasTable bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='alert_episodes')`).Scan(&hasTable); err != nil {
		return unsettledWatchSummary{}, err
	}
	if !hasTable {
		return unsettledWatchSummary{}, nil
	}
	hasCol := func(name string) (bool, error) {
		var ok bool
		err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('alert_episodes') WHERE name = ?)`, name).Scan(&ok)
		return ok, err
	}
	hasSettlement, err := hasCol("settlement_state")
	if err != nil || !hasSettlement {
		return unsettledWatchSummary{}, err
	}
	hasTriage, err := hasCol("active_triage_run_id")
	if err != nil {
		return unsettledWatchSummary{}, err
	}
	hasEscalated, err := hasCol("escalated_to")
	if err != nil {
		return unsettledWatchSummary{}, err
	}

	query := `SELECT alert_id, opened_at FROM alert_episodes
		WHERE COALESCE(NULLIF(settlement_state, ''), 'uninvestigated') <> 'settled'`
	if hasTriage {
		query += ` AND COALESCE(active_triage_run_id, '') = ''`
	}
	if hasEscalated {
		query += ` AND COALESCE(escalated_to, '') <> 'owner'`
	}
	query += ` ORDER BY opened_at, alert_id`

	rows, err := r.Query(query)
	if err != nil {
		return unsettledWatchSummary{}, err
	}
	defer rows.Close()
	var out unsettledWatchSummary
	for rows.Next() {
		var id string
		var openedAt int64
		if err := rows.Scan(&id, &openedAt); err != nil {
			return unsettledWatchSummary{}, err
		}
		if len(out.ids) == 0 || openedAt < out.oldestAt {
			out.oldestAt = openedAt
		}
		out.ids = append(out.ids, id)
	}
	return out, rows.Err()
}

func (u unsettledWatchSummary) alarm(now time.Time) *Alarm {
	if len(u.ids) == 0 {
		return nil
	}
	oldest := time.UnixMilli(u.oldestAt)
	age := now.Sub(oldest)
	return &Alarm{
		Kind:    UnsettledWatchAlerts,
		Backend: "watch",
		Since:   easternTime(oldest),
		Cause:   fmt.Sprintf("%d unsettled watch alert(s), oldest %s [%s]", len(u.ids), formatWatchAge(age), strings.Join(u.ids, ", ")),
		Clears:  "every unsettled watch alert is investigated and settled",
		Command: "run tillr watch status --unsettled and investigate/settle or silence each alert",
	}
}

func formatWatchAge(d time.Duration) string {
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
