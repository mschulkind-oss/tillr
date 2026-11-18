package main

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

type ideaOutcome struct {
	ItemID string `json:"item_id"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

type ideaWords struct {
	Outcomes  []ideaOutcome `json:"outcomes"`
	Words     string        `json:"words"`
	WordsPath string        `json:"words_path"`
	InTree    bool          `json:"in_tree"`
}

// ideaIDs reads metadata once for the snapshot; old stores without intake are
// ordinary items, not an error or a reason to migrate the read-only handle.
func ideaIDs(r *store.Reader) (map[string]bool, error) {
	ids := map[string]bool{}
	exists, err := hasTable(r, "idea_intake")
	if err != nil || !exists {
		return ids, err
	}
	rows, err := r.Query(`SELECT item_id FROM idea_intake`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}
func (s *server) readIdeaWords(r *store.Reader, id string) (*ideaWords, error) {
	exists, err := hasTable(r, "idea_intake")
	if err != nil || !exists {
		return nil, err
	}
	var path, hash string
	var size int
	err = r.QueryRow(`SELECT words_path,words_hash,words_size FROM idea_intake WHERE item_id=?`, id).Scan(&path, &hash, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path || strings.ContainsAny(id, "/\\") {
		return nil, fmt.Errorf("invalid idea words path for %s", id)
	}
	raw, inTree, err := committedIdeaWords(s.root, path)
	if err != nil {
		return nil, err
	}
	out := &ideaWords{WordsPath: path, InTree: inTree}
	if !inTree {
		// The path is derived from the served store, never a request or a store row
		// that could trick the server into reading an unrelated file.
		raw, err = os.ReadFile(filepath.Join(filepath.Dir(s.storePath), "intake", id+".txt"))
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read idea words for %s: %w", id, err)
	}
	if len(raw) != size || fmt.Sprintf("%x", sha256.Sum256(raw)) != hash {
		return nil, fmt.Errorf("idea words for %s differ from saved bytes", id)
	}
	out.Words = string(raw)
	out.Outcomes = []ideaOutcome{}
	// Sync records the source once at first import. The receipt outlives retired
	// blocks and hand moves, so landed outcomes remain linked to their idea.
	rows, err := r.Query(`SELECT item_id,title,state FROM items WHERE EXISTS
 (SELECT 1 FROM events WHERE object_type='item' AND object_id=items.item_id
 AND kind='item.update' AND json_valid(payload) AND json_extract(payload,'$.placed_from')=?)
 ORDER BY rank,item_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v ideaOutcome
		if err := rows.Scan(&v.ItemID, &v.Title, &v.State); err != nil {
			return nil, err
		}
		out.Outcomes = append(out.Outcomes, v)
	}
	return out, rows.Err()
}
