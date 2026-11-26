package item

import (
	"database/sql"
	"errors"
	"github.com/mschulkind-oss/tillr/store"
)

// storedItem reads the stored definition without following its document reference.
func storedItem(r *store.Reader, id string) (itemPayload, bool, error) {
	var payload itemPayload
	var allowedPaths string
	err := r.QueryRow(
		`SELECT item_id, title, COALESCE(body_ref, ''), COALESCE(task, ''), state, COALESCE(design_doc, ''),
		        COALESCE(allowed_paths, ''), COALESCE(rank, ''), COALESCE(seat, ''), COALESCE(prose, ''),
		        COALESCE(workflow, '')
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
