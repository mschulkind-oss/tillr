package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func waitingBackendQuotas(r *store.Reader, now time.Time) ([]waitingRow, error) {
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
			class, mover = "waits on work", "a successful quota reading"
		case now.Sub(q.ObservedAt) > quota.ProbeInterval || resetReached:
			parts = append(parts, "reading stale; reopening unknown until the next probe")
			class, mover = "waits on work", "a fresh quota reading"
		case closed:
			if reopen.IsZero() {
				parts = append(parts, "closed by reserve; reopening unknown")
			} else {
				parts = append(parts, "reopens "+reopen.UTC().Format(time.RFC3339))
			}
			class, mover = "waits on work", "the reset time and a fresh quota reading"
		default:
			parts = append(parts, "open now")
		}
		q := q
		out = append(out, waitingRow{ItemID: id, Class: class, Where: "backend", Why: strings.Join(parts, "; "), Mover: mover, Since: q.ObservedAt.Format(time.RFC3339), ClassSource: "store", Quota: &q})
	}
	return out, nil
}
