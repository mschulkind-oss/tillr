package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func waitingBackendQuotas(r *store.Reader, now time.Time) ([]waitingRow, error) {
	return waitingBackendQuotasWithRoot(r, now, "")
}

func waitingBackendQuotasWithRoot(r *store.Reader, now time.Time, root string) ([]waitingRow, error) {
	statuses, err := quota.ReadStatuses(r)
	if err != nil {
		return nil, err
	}
	legacy, err := waitingBackendWindows(r, now)
	if err != nil {
		return nil, err
	}
	refusal := map[string]waitingRow{}
	for _, row := range legacy {
		refusal[row.ItemID] = row
	}
	summary, activeBackends, _ := readProfileState(root)
	anyActiveClosed := false
	if len(activeBackends) > 0 {
		for _, q := range statuses {
			if !activeBackends[q.Backend] {
				continue
			}
			if _, ok := refusal["backend:"+q.Backend]; ok {
				anyActiveClosed = true
				break
			}
			for _, w := range q.Windows {
				if w.Fraction >= 1-q.Reserve {
					anyActiveClosed = true
					break
				}
			}
			if anyActiveClosed {
				break
			}
		}
		if !anyActiveClosed {
			for id := range refusal {
				name := strings.TrimPrefix(id, "backend:")
				if activeBackends[name] {
					anyActiveClosed = true
					break
				}
			}
		}
	}
	out := []waitingRow{}
	for _, q := range statuses {
		parts := []string{fmt.Sprintf("reserve %g", q.Reserve)}
		var reopen time.Time
		closed, resetReached := false, false
		for _, w := range q.Windows {
			reset := "idle; no active reset"
			if w.ResetAt != nil {
				reset = "resets " + w.ResetAt.UTC().Format(time.RFC3339)
				resetReached = resetReached || !w.ResetAt.After(now)
			}
			parts = append(parts, fmt.Sprintf("%s used %g (%s)", w.Kind, w.Fraction, reset))
			if w.Fraction >= 1-q.Reserve {
				closed = true
				if w.ResetAt != nil && w.ResetAt.After(reopen) {
					reopen = *w.ResetAt
				}
			}
		}
		id := "backend:" + q.Backend
		if old, ok := refusal[id]; ok {
			closed = true
			if old.backendReset.After(reopen) {
				reopen = old.backendReset
			}
			parts = append(parts, "retained "+old.Why)
		}
		class, mover := "could run now", "the loop"
		switch {
		case q.Error != "":
			parts = append(parts, "probe failed: "+q.Error+"; reopening unknown; retry after "+q.ObservedAt.Add(quota.ProbeInterval).Format(time.RFC3339))
			class, mover = "alarm", "operator; repair the quota probe for a successful reading"
		case now.Sub(q.ObservedAt) > quota.ProbeInterval || resetReached:
			parts = append(parts, "reading stale; reopening unknown until the next probe")
			class, mover = "alarm", "operator; check for a fresh quota reading"
		case closed:
			if reopen.IsZero() {
				parts = append(parts, "closed by reserve; reopening unknown")
			} else {
				parts = append(parts, "reopens "+reopen.UTC().Format(time.RFC3339))
			}
			if len(activeBackends) > 0 && !activeBackends[q.Backend] && !anyActiveClosed {
				class = "waits on work"
				mover = inactiveProfileQuotaMover(summary, q.Backend, reopen)
			} else {
				class, mover = "alarm", "operator; wait for the reset and a fresh quota reading"
			}
		default:
			parts = append(parts, "open now")
		}
		if q.Error != "" || now.Sub(q.ObservedAt) > quota.ProbeInterval || resetReached {
			reopen = time.Time{}
		}
		q := q
		out = append(out, waitingRow{ItemID: id, Class: class, Where: "backend", Why: strings.Join(parts, "; "), Mover: mover, Since: q.ObservedAt.Format(time.RFC3339), ClassSource: "store", Quota: &q, backendReset: reopen})
	}
	// Reuse the backend row's reopening decision, including an explicit unknown
	// after a failed or stale reading. Never infer a pause from a closed window.
	backends := refusal
	for _, row := range out {
		backends[row.ItemID] = row
	}
	paused, err := waitingQuotaPauses(r, backends)
	if err != nil {
		return nil, err
	}
	return append(out, paused...), nil
}

func inactiveProfileQuotaMover(summary *profileSummary, backend string, reopen time.Time) string {
	activeName := ""
	var resetStr, reopenMaxStr string
	if summary != nil {
		activeName = summary.Profile
		for _, sp := range summary.SkippedProfiles {
			if sp.Backend == backend {
				if sp.ResetAt != nil {
					resetStr = sp.ResetAt.UTC().Format(time.RFC3339)
				}
				if sp.ReopenThreshold > 0 {
					reopenMaxStr = fmt.Sprintf("%g", sp.ReopenThreshold)
				}
				break
			}
		}
	}
	if resetStr == "" && !reopen.IsZero() {
		resetStr = reopen.UTC().Format(time.RFC3339)
	}
	switch {
	case activeName != "" && resetStr != "" && reopenMaxStr != "":
		return fmt.Sprintf("the loop; switched to %s, switches back after %s when <= %s", activeName, resetStr, reopenMaxStr)
	case activeName != "" && resetStr != "":
		return fmt.Sprintf("the loop; switched to %s, switches back after %s", activeName, resetStr)
	case activeName != "":
		return fmt.Sprintf("the loop; profile %s is in force", activeName)
	default:
		return "the loop; an open profile is in force"
	}
}

func waitingQuotaPauses(r *store.Reader, backends map[string]waitingRow) ([]waitingRow, error) {
	rows, err := r.Query(`SELECT run_id,item_id,lane_id,backend,paused_at FROM quota_pauses WHERE state='waiting' ORDER BY run_id`)
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitingRow
	for rows.Next() {
		var id, item, lane, backend string
		var at int64
		if err := rows.Scan(&id, &item, &lane, &backend, &at); err != nil {
			return nil, err
		}
		reopening := "reopening unknown"
		q := backends["backend:"+backend]
		if !q.backendReset.IsZero() {
			reopening = "reopens " + q.backendReset.UTC().Format(time.RFC3339)
		}
		out = append(out, waitingRow{ItemID: item, RunID: id, Backend: backend, LaneID: lane, Class: "waits on work", Where: "backend", Why: fmt.Sprintf("run %s paused on lane %s waiting on backend %s; %s", id, lane, backend, reopening), Mover: "an open backend in the seat's list and the loop", Since: time.UnixMilli(at).UTC().Format(time.RFC3339), ClassSource: "store", Quota: q.Quota})
	}
	return out, rows.Err()
}

// Replace the item's generic history/planner explanation, not its metadata.
// The paused run's durable row is the reason it waits, not an unjudged fault.
func mergeQuotaWaiting(rows, quotas []waitingRow) []waitingRow {
	for _, q := range quotas {
		found := false
		if q.RunID != "" {
			for i, row := range rows {
				if row.ItemID == q.ItemID && row.RunID == "" {
					q.Place, q.Document = row.Place, row.Document
					rows[i] = q
					found = true
					break
				}
			}
		}
		if !found {
			rows = append(rows, q)
		}
	}
	return rows
}
