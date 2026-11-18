package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// A revert withdraws what the reverted landing settled for its item. The landing recorded
// a verdict on every criterion; the revert took the judged change off the trunk, so those
// verdicts no longer describe anything. Left standing, they froze the item's definition
// (tillr-item sync's definition-frozen) and the design answer that amended the block for
// the item's next build could never apply: item-gate-stall-watch-without-perf-counter was
// held definition-sync-failed that way on 2026-11-18. The withdrawal is recorded as an
// item.verdicts-withdrawn event naming the landing and the revert, so the item is exactly
// as amendable and dispatchable as before it landed, and its next landing judges it again.
//
// The rule is applied to the records as they stand, before each document sync, not only
// at the revert: a revert recorded by a build that did not withdraw (or one whose
// withdrawal did not commit) is repaired at the next sync, which a loop start always runs.
// Nothing is counted: an item reverted twice is withdrawn after each revert.

// revertedItem is the latest landed revert of one item's landing.
type revertedItem struct {
	itemID, landing, revert string
	seq                     int64
}

// withdrawRevertedVerdicts withdraws the criterion states of every item a landed revert
// returned whose verdicts still stand from before that revert.
func (l *Loop) withdrawRevertedVerdicts(ctx context.Context) error {
	var todo []revertedItem
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		todo, err = verdictsLeftByReverts(r)
		return err
	}); err != nil || len(todo) == 0 {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	var withdrawn []string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		withdrawn = nil
		// The write re-reads what it acts on, so a landing or a verdict recorded since the
		// read above is never withdrawn.
		current, err := verdictsLeftByReverts(tx.Reader())
		if err != nil {
			return err
		}
		for _, r := range current {
			reason := "landing " + r.landing + " reverted by " + r.revert + ": the verdicts it recorded no longer stand"
			cleared, err := item.WithdrawVerdicts(ctx, tx, r.itemID, reason, revertBy, at)
			if err != nil {
				return err
			}
			if len(cleared) > 0 {
				withdrawn = append(withdrawn, r.itemID+" ("+shortSHA(r.landing)+")")
			}
		}
		return nil
	})
	if err == nil && len(withdrawn) > 0 {
		l.reportf("loop: withdrew the verdicts of reverted landings: %s\n", strings.Join(withdrawn, ", "))
	}
	return err
}

// verdictsLeftByReverts answers each item whose latest landed revert is newer than every
// verdict and withdrawal it carries, that is not finished, and that still carries a
// criterion state: the verdicts its reverted landing left standing.
func verdictsLeftByReverts(r *store.Reader) ([]revertedItem, error) {
	rows, err := r.Query(`SELECT seq, payload FROM events WHERE kind = ? AND object_type = 'landing' AND json_extract(payload, '$.result') = 'reverted' ORDER BY seq`, RevertKind)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	latest := map[string]revertedItem{}
	var order []string
	for rows.Next() {
		var seq int64
		var raw []byte
		if err := rows.Scan(&seq, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		var p revertPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			rows.Close()
			return nil, err
		}
		if p.ItemID == "" {
			continue
		}
		if _, seen := latest[p.ItemID]; !seen {
			order = append(order, p.ItemID)
		}
		latest[p.ItemID] = revertedItem{itemID: p.ItemID, landing: p.Landing, revert: p.Revert, seq: seq}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return nil, nil
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return nil, err
	}
	var out []revertedItem
	for _, id := range order {
		rev := latest[id]
		var state string
		var judged, later bool
		err := r.QueryRow(`SELECT state, EXISTS (SELECT 1 FROM item_criteria WHERE item_id = ? AND state != ''), EXISTS (SELECT 1 FROM events WHERE object_type = 'item' AND object_id = ? AND kind IN (?, ?) AND seq > ?) FROM items WHERE item_id = ?`,
			id, id, item.VerdictRecordKind, item.VerdictsWithdrawnKind, rev.seq, id).Scan(&state, &judged, &later)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if !judged || later || finishedState(declared.Parsed, state) {
			continue
		}
		out = append(out, rev)
	}
	return out, nil
}
