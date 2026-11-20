// Architecture and invariants: docs/reference/tillr-queue-persistence.md, docs/reference/observatory-actions.md
package item

import (
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// placeFrom reads the current order, not the plan's snapshot: earlier imports
// may already have respaced it. Missing or unkeyed sources are historical marks,
// not refusals. Terminal sources retain a key and are deliberately included.
func placeFrom(tx *store.Tx, id, from string, at time.Time) error {
	rows, err := tx.Reader().Query(`SELECT item_id,rank,state,
   EXISTS (SELECT 1 FROM events WHERE object_type='item' AND object_id=items.item_id
     AND kind=? AND json_extract(payload,'$.placed_from')=?)
   FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`, UpdateKind, from)
	if err != nil {
		return Internalf("reading source placement: %v", err)
	}
	rest := []orderedItem{}
	var target orderedItem
	sourcePos := -1
	siblings := map[string]bool{}
	for rows.Next() {
		var v orderedItem
		var sibling bool
		if err := rows.Scan(&v.id, &v.rank, &v.state, &sibling); err != nil {
			rows.Close()
			return err
		}
		if v.id == id {
			target = v
			continue
		}
		// Parked and finished rows without keys do not belong to a gap.
		if v.rank == "" && !InOrder(v.state) {
			continue
		}
		if v.id == from && v.rank != "" {
			sourcePos = len(rest)
		}
		siblings[v.id] = sibling
		rest = append(rest, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if sourcePos < 0 {
		return nil
	}
	pos := sourcePos + 1
	for i := pos; i < len(rest); i++ {
		if rest[i].rank != "" && siblings[rest[i].id] {
			pos = i + 1
		}
	}
	_, err = insertOrdered(tx, target, rest, pos, at, "", "sync", from)
	return err
}
