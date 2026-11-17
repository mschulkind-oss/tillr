package main_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Read every column, including columns not exposed by show, so a rank update
// cannot silently clear an unrelated stored value as the schema grows.
func rankStoredRows(t *testing.T, db, query, id string) []map[string]any {
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

func TestRankOnlyPreservesEveryStoredField(t *testing.T) {
	for _, fixture := range []struct{ workflow, state string }{
		{"scout-then-build@v1", "in-queue"}, {"", "blocked"}, {"", "closed"},
		{"", "landed"}, {"", "resolved-elsewhere"},
	} {
		workflow := fixture.workflow
		t.Run("workflow="+workflow+"/state="+fixture.state, func(t *testing.T) {
			db := filepath.Join(scratch(t, "rank-workflow"), "board.sqlite3")
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
			conn, err := sql.Open("sqlite", db)
			if err != nil {
				t.Fatal(err)
			}
			// Standalone item does not register run DDL. This independent table proves
			// the rank writer leaves a run ledger untouched without owning its schema.
			if _, err := conn.Exec(`CREATE TABLE runs (run_id TEXT, item_id TEXT, result TEXT); INSERT INTO runs VALUES ('run-1', 'ranked', 'pass')`); err != nil {
				t.Fatal(err)
			}
			if _, err := conn.Exec(`UPDATE items SET state = ?, waiting_on = '{"waiter":"owner","reason":"ruling"}', underlying_state = 'in-queue', close_reason = 'parked', closed_by = 'owner', closed_at = '2026-11-11T00:00:00Z', closed_from = 'blocked' WHERE item_id = ?`, fixture.state, entry.ItemID); err != nil {
				t.Fatal(err)
			}
			conn.Close()
			runsQuery := `SELECT * FROM runs WHERE item_id = ?`
			runs := rankStoredRows(t, db, runsQuery, entry.ItemID)
			itemQuery := `SELECT * FROM items WHERE item_id = ?`
			criteriaQuery := `SELECT * FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`
			edgesQuery := `SELECT * FROM item_edges WHERE item_id = ? ORDER BY blocked_by`
			before := rankStoredRows(t, db, itemQuery, entry.ItemID)
			criteria := rankStoredRows(t, db, criteriaQuery, entry.ItemID)
			edges := rankStoredRows(t, db, edgesQuery, entry.ItemID)
			if len(before) != 1 || before[0]["workflow"] != workflow || len(criteria) != 2 || len(edges) != 1 {
				t.Fatalf("incomplete fixture: items=%v criteria=%v edges=%v", before, criteria, edges)
			}

			if code, out := rank(t, db, entry.ItemID, "b"); code != 0 {
				t.Fatalf("rank: %d: %s", code, out)
			} else {
				var got struct {
					Payload struct {
						Rank string `json:"rank"`
					} `json:"payload"`
				}
				if err := json.Unmarshal(out, &got); err != nil || got.Payload.Rank != "b" {
					t.Fatalf("rank echo: %s (%v)", out, err)
				}
			}
			after := rankStoredRows(t, db, itemQuery, entry.ItemID)
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
			if got := rankStoredRows(t, db, criteriaQuery, entry.ItemID); !reflect.DeepEqual(criteria, got) {
				t.Errorf("criteria changed: %v -> %v", criteria, got)
			}
			if got := rankStoredRows(t, db, edgesQuery, entry.ItemID); !reflect.DeepEqual(edges, got) {
				t.Errorf("edges changed: %v -> %v", edges, got)
			}
			if got := rankStoredRows(t, db, runsQuery, entry.ItemID); !reflect.DeepEqual(runs, got) {
				t.Errorf("runs changed: %v -> %v", runs, got)
			}
			updates := eventsOfKind(t, db, "item.update")
			if code, out := rank(t, db, entry.ItemID, "b"); code != 0 {
				t.Fatalf("repeat: %d %s", code, out)
			}
			if n := eventsOfKind(t, db, "item.update"); n != updates {
				t.Errorf("same rank added events: %d -> %d", updates, n)
			}
			if got := rankStoredRows(t, db, itemQuery, entry.ItemID); !reflect.DeepEqual(after, got) {
				t.Errorf("same rank changed item: %v", got)
			}
			if code, out := rank(t, db, entry.ItemID, ""); code != 0 {
				t.Fatalf("clear: %d %s", code, out)
			}
			if got := rankStoredRows(t, db, itemQuery, entry.ItemID); got[0]["rank"] != "" {
				t.Errorf("rank not cleared: %v", got)
			}
		})
	}
}

func rank(t *testing.T, db, id, token string) (int, []byte) {
	t.Helper()
	return run(t, "rank", "--store", db, "--item", id, "--rank", token, "--now", "2026-11-12T00:00:00Z")
}

// Omitting the flag is not an explicit request to clear priority. Pin the entire
// row and event log so a usage refusal cannot hide a write made before validation.
func TestRankRequiresExplicitRankAndWritesNothing(t *testing.T) {
	db := filepath.Join(scratch(t, "rank-omitted"), "board.sqlite3")
	entry := base("known", "Known")
	entry.Rank = "a"
	if code, out := apply(t, db, envelope(t, "item.record", "known", entry)); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	itemQuery := `SELECT * FROM items WHERE item_id = ?`
	eventsQuery := `SELECT * FROM events WHERE object_id = ? ORDER BY seq`
	before := rankStoredRows(t, db, itemQuery, "known")
	events := rankStoredRows(t, db, eventsQuery, "known")
	cmd := exec.Command(bin, "rank", "--store", db, "--item", "known", "--now", "2026-11-12T00:00:00Z")
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	out, err := cmd.Output()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 || len(out) != 0 || !strings.Contains(string(exit.Stderr), "--rank") {
		t.Fatalf("omitted rank: %v %s", err, out)
	}
	if got := rankStoredRows(t, db, itemQuery, "known"); !reflect.DeepEqual(before, got) {
		t.Fatalf("usage refusal changed item: %v -> %v", before, got)
	}
	if got := rankStoredRows(t, db, eventsQuery, "known"); !reflect.DeepEqual(events, got) {
		t.Fatalf("usage refusal changed events: %v -> %v", events, got)
	}
	if code, out := rank(t, db, "known", ""); code != 0 {
		t.Fatalf("explicit empty rank: %d %s", code, out)
	}
	if got := rankStoredRows(t, db, itemQuery, "known"); got[0]["rank"] != "" {
		t.Fatalf("explicit empty rank did not clear priority: %v", got)
	}
}

func TestRankRefusesMissingItemAndInvalidToken(t *testing.T) {
	db := filepath.Join(scratch(t, "rank-missing"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "known", base("known", "Known"))); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	for _, tc := range []struct{ id, token, rule string }{{"missing", "a", "not-found"}, {"known", "!", "rank-alphabet"}} {
		code, out := rank(t, db, tc.id, tc.token)
		if code != 1 || decode(t, out).Payload.Rule != tc.rule {
			t.Fatalf("rank: %d %s, want %s", code, out, tc.rule)
		}
	}
	if got := rankStoredRows(t, db, `SELECT * FROM items WHERE item_id = ?`, "missing"); len(got) != 0 {
		t.Fatalf("created missing item: %v", got)
	}
	if n := eventsOfKind(t, db, "item.update"); n != 0 {
		t.Fatalf("refused rank appended %d events", n)
	}
}
