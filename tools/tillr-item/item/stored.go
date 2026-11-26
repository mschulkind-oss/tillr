package item

import (
	"database/sql"
	"errors"
	"github.com/mschulkind-oss/tillr/store"
)

// storedItem reads the stored definition without following its document reference.
func storedItem(r *store.Reader, id string) (itemPayload, bool, error) {
	cols, err := itemColumns(r)
	if err != nil {
		return itemPayload{}, false, err
	}
	var payload itemPayload
	var allowedPaths string
	err = r.QueryRow(
		`SELECT item_id, title, COALESCE(body_ref, ''), `+itemTextColumnExpr(cols, "task")+`, state, COALESCE(design_doc, ''), `+
			itemTextColumnExpr(cols, "allowed_paths")+`, `+itemTextColumnExpr(cols, "rank")+`, `+
			itemTextColumnExpr(cols, "seat")+`, `+itemTextColumnExpr(cols, "prose")+`, `+
			itemTextColumnExpr(cols, "workflow")+`
		   FROM items WHERE item_id = ?`, id,
	).Scan(&payload.ItemID, &payload.Title, &payload.BodyRef, &payload.Task, &payload.State, &payload.DesignDoc,
		&allowedPaths, &payload.Rank, &payload.Seat, &payload.Prose, &payload.Workflow)
	if errors.Is(err, sql.ErrNoRows) {
		return itemPayload{}, false, nil
	}
	if err != nil {
		return itemPayload{}, false, Internalf("reading item %q: %v", id, err)
	}
	if payload.AllowedPaths, err = decodeAllowedPaths(allowedPaths); err != nil {
		return itemPayload{}, false, err
	}
	if payload.Express, err = readExpress(r, id); err != nil {
		return itemPayload{}, false, err
	}
	if payload.JudgeMode, err = readJudgeMode(r, id); err != nil {
		return itemPayload{}, false, err
	}
	if payload.Criteria, err = readCriteria(r, id); err != nil {
		return itemPayload{}, false, err
	}
	if payload.BlockedBy, payload.After, err = readEdges(r, id); err != nil {
		return itemPayload{}, false, err
	}
	return payload, true, nil
}

func itemColumns(r *store.Reader) (map[string]bool, error) {
	rows, err := r.Query(`SELECT name FROM pragma_table_info('items')`)
	if err != nil {
		return nil, Internalf("reading items columns: %v", err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, Internalf("scanning items columns: %v", err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, Internalf("reading items columns: %v", err)
	}
	return cols, nil
}

func itemTextColumnExpr(cols map[string]bool, name string) string {
	if cols[name] {
		return "COALESCE(" + name + ", '')"
	}
	return "''"
}
