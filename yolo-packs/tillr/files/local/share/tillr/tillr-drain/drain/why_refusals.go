package drain

import (
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

func readWhyRefusals(r *store.Reader) (map[string]map[string]int, error) {
	rows, err := r.Query(`SELECT COALESCE(NULLIF(json_extract(e.payload,'$.item_id'),''), runs.item_id,
 CASE WHEN e.object_type='item' THEN e.object_id END) AS item,
 json_extract(e.payload,'$.step') AS step, COUNT(*)
 FROM events e LEFT JOIN runs ON e.object_type='run' AND runs.run_id=e.object_id
 WHERE e.kind=? AND step IN (?,?,?,?) AND item IS NOT NULL GROUP BY item, step`,
		RefusalKind, RefusalVerifyNotDone, RefusalGate, RefusalFence, RefusalConflict)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var item, step string
		var n int
		if err := rows.Scan(&item, &step, &n); err != nil {
			return nil, err
		}
		if out[item] == nil {
			out[item] = map[string]int{}
		}
		out[item][step] = n
	}
	return out, rows.Err()
}

func readWhyRefusalsForItems(r *store.Reader, itemIDs, runIDs []string) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	if len(itemIDs) == 0 && len(runIDs) == 0 {
		return out, nil
	}
	itemSet := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		itemSet[id] = true
	}
	runToItem := map[string]string{}
	if len(runIDs) > 0 {
		const chunk = 400
		for start := 0; start < len(runIDs); start += chunk {
			end := start + chunk
			if end > len(runIDs) {
				end = len(runIDs)
			}
			batch := runIDs[start:end]
			args := make([]any, len(batch))
			for i, id := range batch {
				args[i] = id
			}
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
			rows, err := r.Query(`SELECT run_id, item_id FROM runs WHERE run_id IN (`+placeholders+`) AND item_id IS NOT NULL`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var runID, itemID string
				if err := rows.Scan(&runID, &itemID); err != nil {
					rows.Close()
					return nil, err
				}
				runToItem[runID] = itemID
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	objectIDs := make([]string, 0, len(itemIDs)+len(runIDs))
	objectIDs = append(objectIDs, itemIDs...)
	objectIDs = append(objectIDs, runIDs...)
	events, err := r.EventsForObjects(0, objectIDs, RefusalKind)
	if err != nil {
		return nil, err
	}
	seenSeq := make(map[int64]bool, len(events))
	for _, e := range events {
		seenSeq[e.Seq] = true
	}
	if len(itemIDs) > 0 {
		const chunk = 400
		for start := 0; start < len(itemIDs); start += chunk {
			end := start + chunk
			if end > len(itemIDs) {
				end = len(itemIDs)
			}
			batch := itemIDs[start:end]
			args := make([]any, 0, 1+len(batch))
			args = append(args, RefusalKind)
			for _, id := range batch {
				args = append(args, id)
			}
			placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
			rows, err := r.Query(`SELECT seq, object_type, object_id, payload FROM events WHERE kind=? AND json_valid(payload) AND json_extract(payload,'$.item_id') IN (`+placeholders+`)`, args...)
			if err != nil {
				return nil, err
			}
			for rows.Next() {
				var seq int64
				var objType, objID sql.NullString
				var payload []byte
				if err := rows.Scan(&seq, &objType, &objID, &payload); err != nil {
					rows.Close()
					return nil, err
				}
				if seenSeq[seq] {
					continue
				}
				seenSeq[seq] = true
				var p struct {
					ItemID string `json:"item_id"`
					Step   string `json:"step"`
				}
				if err := json.Unmarshal(payload, &p); err != nil {
					continue
				}
				switch p.Step {
				case RefusalVerifyNotDone, RefusalGate, RefusalFence, RefusalConflict:
				default:
					continue
				}
				itemID := p.ItemID
				if itemID == "" && objType.Valid && objType.String == "run" && objID.Valid {
					itemID = runToItem[objID.String]
				}
				if itemID == "" && objType.Valid && objType.String == "item" && objID.Valid {
					itemID = objID.String
				}
				if itemID == "" || !itemSet[itemID] {
					continue
				}
				if out[itemID] == nil {
					out[itemID] = map[string]int{}
				}
				out[itemID][p.Step]++
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return nil, err
			}
			rows.Close()
		}
	}
	for _, e := range events {
		var p struct {
			ItemID string `json:"item_id"`
			Step   string `json:"step"`
		}
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			continue
		}
		switch p.Step {
		case RefusalVerifyNotDone, RefusalGate, RefusalFence, RefusalConflict:
		default:
			continue
		}
		itemID := p.ItemID
		if itemID == "" && e.ObjectType == "run" && e.ObjectID != nil {
			itemID = runToItem[*e.ObjectID]
		}
		if itemID == "" && e.ObjectType == "item" && e.ObjectID != nil {
			itemID = *e.ObjectID
		}
		if itemID == "" || !itemSet[itemID] {
			continue
		}
		if out[itemID] == nil {
			out[itemID] = map[string]int{}
		}
		out[itemID][p.Step]++
	}
	return out, nil
}
