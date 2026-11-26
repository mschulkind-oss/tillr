package drain

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

type versionSchema struct {
	itemColumns   map[string]bool
	hasCriteria   bool
	hasClaimClass bool
	hasEdges      bool
	hasEdgeKind   bool
}

// Older stores may not have every column or relationship table yet. Read the
// schema in the same transaction as the content, without migrating the store.
func readVersionSchema(r *store.Reader) (versionSchema, error) {
	s := versionSchema{itemColumns: map[string]bool{}}
	rows, err := r.Query(`SELECT 'items', name FROM pragma_table_info('items')
		UNION ALL SELECT 'item_criteria', name FROM pragma_table_info('item_criteria')
		UNION ALL SELECT 'item_edges', name FROM pragma_table_info('item_edges')
		UNION ALL SELECT name, '' FROM sqlite_master WHERE type='table' AND name='item_edges'`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			return s, err
		}
		switch table {
		case "items":
			s.itemColumns[column] = true
		case "item_criteria":
			s.hasCriteria = true
			s.hasClaimClass = s.hasClaimClass || column == "claim_class"
		case "item_edges":
			s.hasEdges = true
			s.hasEdgeKind = s.hasEdgeKind || column == "kind"
		}
	}
	return s, rows.Err()
}

type itemContent struct {
	Fields   json.RawMessage
	Conflict json.RawMessage
	Criteria []json.RawMessage
	Edges    []json.RawMessage
}

func (content *itemContent) version() (string, error) {
	raw, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func readAllItemContentVersions(r *store.Reader, schema versionSchema) (map[string]string, error) {
	return readItemContentVersions(r, schema, nil)
}

func readItemContentVersion(r *store.Reader, schema versionSchema, itemID string) (string, error) {
	versions, err := readItemContentVersions(r, schema, &itemID)
	if err != nil {
		return "", err
	}
	version, ok := versions[itemID]
	if !ok {
		return "", sql.ErrNoRows
	}
	return version, nil
}

// Single and bulk reads share the projection, ordering and hash. In particular,
// Place may rewrite rank, updated_at and event_seq even on neighboring rows;
// none is evidence that an already planned start's content changed.
func readItemContentVersions(r *store.Reader, schema versionSchema, only *string) (map[string]string, error) {
	fields := []string{"i.state", "i.title", "COALESCE(i.body_ref,'')", "COALESCE(i.design_doc,'')"}
	for _, col := range []string{"allowed_paths", "seat", "express", "workflow", "workflow_args", "judge_mode", "prose", "waiting_on"} {
		fallback := "''"
		if col == "express" {
			fallback = "0"
		}
		if schema.itemColumns[col] {
			fields = append(fields, "COALESCE(i."+col+","+fallback+")")
		} else {
			fields = append(fields, fallback)
		}
	}
	var where string
	var args []any
	if only != nil {
		where, args = ` WHERE i.item_id = ?`, []any{*only}
	}
	items := map[string]*itemContent{}
	// Conflicts are event-backed on trunk. Use the latest transition for the
	// item, as ReadConflicts does. Pin the object index: the kind index would
	// make SQLite search other items' conflicts for each row in a bulk read.
	itemArgs := append([]any{item.ConflictKind}, args...)
	err := readContentRows(r, `SELECT i.item_id, json_array(`+strings.Join(fields, ", ")+`),
		COALESCE((SELECT e.payload FROM events e INDEXED BY events_by_object
		WHERE e.object_id = i.item_id AND e.object_type = 'item' AND e.kind = ?
		ORDER BY e.seq DESC LIMIT 1), 'null')
		FROM items i`+where, itemArgs, func(rows *sql.Rows) error {
		var id string
		var fields, conflict []byte
		if err := rows.Scan(&id, &fields, &conflict); err != nil {
			return err
		}
		items[id] = &itemContent{Fields: fields, Conflict: conflict}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if schema.hasCriteria {
		claimClass := "''"
		if schema.hasClaimClass {
			claimClass = "COALESCE(i.claim_class,'')"
		}
		err := readContentRows(r, `SELECT i.item_id, json_array(i.criterion_id, i.text, i.state, `+claimClass+`)
			FROM item_criteria i`+where+` ORDER BY i.item_id, i.criterion_id`, args, func(rows *sql.Rows) error {
			var id string
			var value []byte
			if err := rows.Scan(&id, &value); err != nil {
				return err
			}
			if content := items[id]; content != nil {
				content.Criteria = append(content.Criteria, value)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if schema.hasEdges {
		edgeSelect := `json_array(i.blocked_by, COALESCE(b.state,''))`
		if schema.hasEdgeKind {
			edgeSelect = `json_array(i.blocked_by, COALESCE(b.state,''), COALESCE(NULLIF(i.kind,''),'` + item.EdgeKindBlockedBy + `'))`
		}
		err := readContentRows(r, `SELECT i.item_id, `+edgeSelect+`
			FROM item_edges i LEFT JOIN items b ON b.item_id = i.blocked_by`+where+`
			ORDER BY i.item_id, i.blocked_by`, args, func(rows *sql.Rows) error {
			var id string
			var value []byte
			if err := rows.Scan(&id, &value); err != nil {
				return err
			}
			if content := items[id]; content != nil {
				content.Edges = append(content.Edges, value)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	versions := make(map[string]string, len(items))
	for id, content := range items {
		version, err := content.version()
		if err != nil {
			return nil, err
		}
		versions[id] = version
	}
	return versions, nil
}

func readContentRows(r *store.Reader, query string, args []any, scan func(*sql.Rows) error) error {
	rows, err := r.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
