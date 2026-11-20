package drain

import "github.com/mschulkind-oss/tillr/store"

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
