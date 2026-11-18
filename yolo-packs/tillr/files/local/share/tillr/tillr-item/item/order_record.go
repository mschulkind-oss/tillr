package item

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// OrderRecord returns the plain text rebuild record, without writing a file or
// an event. Zero names the absence of any hand placement. Sync placement receipts
// are excluded; an idea's creation also counts when its first place was a no-op.
func OrderRecord(r *store.Reader) (string, error) {
	var seq int64
	if err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE
 (kind='item.update' AND COALESCE(json_extract(payload,'$.placed_via'),'') NOT IN ('','sync'))
 OR (kind='item.add' AND object_id IN (SELECT item_id FROM idea_intake))`).Scan(&seq); err != nil {
		return "", Internalf("reading the last hand placement: %v", err)
	}
	places, err := OrderPlaces(r)
	if err != nil {
		return "", err
	}
	ids := make([]string, 0, len(places))
	for id := range places {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return places[ids[i]] < places[ids[j]] })
	var b strings.Builder
	fmt.Fprintf(&b, "# last_hand_move_event_seq: %d\n", seq)
	for _, id := range ids {
		fmt.Fprintln(&b, id)
	}
	return b.String(), nil
}

// readOrderRecord never reads a file unless the repository opts in. A missing
// file means the first record has not landed yet; other read errors become sync
// warnings so the loop can regenerate an unusable hand-edited record.
// Unknown IDs are retained as historical lines but never create items.
func readOrderRecord(root, path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(filepath.Join(root, path))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading order record %s: %w", path, err)
	}
	ids := []string{}
	seen := map[string]bool{}
	for n, line := range strings.Split(string(raw), "\n") {
		id := strings.TrimSpace(line)
		if id == "" || strings.HasPrefix(id, "#") {
			continue
		}
		if !itemIDShape.MatchString(id) {
			return nil, fmt.Errorf("order record %s:%d: expected an item id", path, n+1)
		}
		if seen[id] {
			return nil, fmt.Errorf("order record %s:%d: duplicate item %s", path, n+1, id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// placeRecorded uses only existing items and earlier record imports as anchors.
// Later imports have provisional append keys, but are not yet placed for rule 1.
func placeRecorded(tx *store.Tx, id string, record []string, available map[string]bool, at time.Time) error {
	rows, err := tx.Reader().Query(`SELECT item_id,rank,state FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
	if err != nil {
		return err
	}
	rest := []orderedItem{}
	var target orderedItem
	positions := map[string]int{}
	for rows.Next() {
		var v orderedItem
		if err := rows.Scan(&v.id, &v.rank, &v.state); err != nil {
			rows.Close()
			return err
		}
		if !InOrder(v.state) {
			continue
		}
		if v.id == id {
			target = v
			continue
		}
		if available[v.id] {
			positions[v.id] = len(rest)
		}
		rest = append(rest, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if target.id == "" {
		return nil
	}
	index := -1
	for i, v := range record {
		if v == id {
			index = i
			break
		}
	}
	pos := len(rest)
	anchored := false
	for i := index - 1; i >= 0; i-- {
		if p, ok := positions[record[i]]; ok {
			pos = p + 1
			anchored = true
			break
		}
	}
	if !anchored {
		for i := index + 1; i < len(record); i++ {
			if p, ok := positions[record[i]]; ok {
				pos = p
				break
			}
		}
	}
	_, err = insertOrdered(tx, target, rest, pos, at, "", "sync", "")
	return err
}
