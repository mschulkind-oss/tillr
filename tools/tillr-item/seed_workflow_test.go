package main_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

// Read every column, including columns not exposed by show, so a rank update
// cannot silently clear an unrelated stored value as the schema grows.
func seedStoredRows(t *testing.T, db, query, id string) []map[string]any {
	t.Helper()
	conn, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.Query(query, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = values[i]
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSeedRankOnlyPreservesEveryStoredField(t *testing.T) {
	for _, workflow := range []string{"scout-then-build@v1", ""} {
		t.Run("workflow="+workflow, func(t *testing.T) {
			db := filepath.Join(scratch(t, "seed-workflow"), "board.sqlite3")
			blocker := base("blocker", "a dependency")
			if code, out := apply(t, db, envelope(t, "item.record", "blocker", blocker)); code != 0 {
				t.Fatalf("store blocker: %d: %s", code, out)
			}
			entry := base("ranked", "an item with stored facts")
			entry.Rank = "a"
			entry.BlockedBy = []string{blocker.ItemID}
			entry.Criteria = append(entry.Criteria, criterion{CriterionID: "c2", Text: "another criterion", State: "open"})
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			payload["workflow"] = workflow
			payload["prose"] = "2"
			if code, out := apply(t, db, envelope(t, "item.record", "ranked", payload)); code != 0 {
				t.Fatalf("store item: %d: %s", code, out)
			}
			itemQuery := `SELECT * FROM items WHERE item_id = ?`
			criteriaQuery := `SELECT * FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`
			edgesQuery := `SELECT * FROM item_edges WHERE item_id = ? ORDER BY blocked_by`
			before := seedStoredRows(t, db, itemQuery, entry.ItemID)
			criteria := seedStoredRows(t, db, criteriaQuery, entry.ItemID)
			edges := seedStoredRows(t, db, edgesQuery, entry.ItemID)
			if len(before) != 1 || before[0]["workflow"] != workflow || len(criteria) != 2 || len(edges) != 1 {
				t.Fatalf("incomplete fixture: items=%v criteria=%v edges=%v", before, criteria, edges)
			}

			entry.Rank = "b"
			if code, out := seed(t, db, seedDocument(t, entry)); code != 0 {
				t.Fatalf("rank-only seed: %d: %s", code, out)
			} else if got := decodeSeed(t, out); !reflect.DeepEqual(got.Payload.Ranked, []string{entry.ItemID}) {
				t.Fatalf("ranked = %v", got.Payload.Ranked)
			}
			after := seedStoredRows(t, db, itemQuery, entry.ItemID)
			if len(after) != 1 || len(after[0]) != len(before[0]) {
				t.Fatalf("stored row shape changed: %v -> %v", before, after)
			}
			if after[0]["workflow"] != workflow {
				t.Errorf("workflow = %q, want unchanged %q", after[0]["workflow"], workflow)
			}
			for column, value := range before[0] {
				got, present := after[0][column]
				switch column {
				case "rank", "updated_at", "event_seq":
					if reflect.DeepEqual(value, got) {
						t.Errorf("%s did not change: %v", column, got)
					}
				default:
					if !present || !reflect.DeepEqual(value, got) {
						t.Errorf("%s changed: %v -> %v", column, value, got)
					}
				}
			}
			if after[0]["rank"] != "b" || after[0]["updated_at"] != int64(1794441600000) || after[0]["event_seq"].(int64) <= before[0]["event_seq"].(int64) {
				t.Errorf("incorrect rank/bookkeeping: %v", after[0])
			}
			if got := seedStoredRows(t, db, criteriaQuery, entry.ItemID); !reflect.DeepEqual(criteria, got) {
				t.Errorf("criteria changed: %v -> %v", criteria, got)
			}
			if got := seedStoredRows(t, db, edgesQuery, entry.ItemID); !reflect.DeepEqual(edges, got) {
				t.Errorf("edges changed: %v -> %v", edges, got)
			}
		})
	}
}
