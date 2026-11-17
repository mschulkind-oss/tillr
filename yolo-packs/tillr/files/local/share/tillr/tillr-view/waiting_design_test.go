package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestWaitingDesignWithoutItem(t *testing.T) {
	for _, mode := range []string{"absent", "empty", "present"} {
		t.Run(mode, func(t *testing.T) {
			h := newServeHarness(t)
			db, err := sql.Open("sqlite", "file:"+h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if mode != "absent" {
				if _, err := db.Exec(`CREATE TABLE design_without_item (document TEXT PRIMARY KEY, roadmap TEXT NOT NULL, roadmap_line INTEGER NOT NULL, first_seen INTEGER NOT NULL)`); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "present" {
				if _, err := db.Exec(`INSERT INTO design_without_item VALUES ('docs/design/z.md','docs/roadmap.md',9,1000),('docs/design/a.md','docs/tillr/roadmap.md',3,2000)`); err != nil {
					t.Fatal(err)
				}
			}
			before := storeFingerprint(t, h.store)
			status, body, raw := h.get("/api/v1/waiting")
			if status != 200 {
				t.Fatalf("status %d: %s", status, raw)
			}
			rows := body["data"].([]any)
			var designs []map[string]any
			seenDesign := false
			for _, value := range rows {
				row := value.(map[string]any)
				if row["document"] != "" && row["document"] != nil {
					seenDesign = true
					designs = append(designs, row)
					if row["item_id"] != "" || row["rank"] != "" || row["class"] != "needs you" || row["where"] != "queue" || !strings.HasPrefix(row["why"].(string), "design-without-item") || !strings.Contains(row["mover"].(string), "owner") {
						t.Fatalf("bad design row: %+v", row)
					}
				} else if seenDesign && row["item_id"] != "" {
					t.Fatal("item sorted after design")
				}
			}
			want := 0
			if mode == "present" {
				want = 2
			}
			if len(designs) != want {
				t.Fatalf("design rows: %+v", rows)
			}
			history, err := readWaiting(context.Background(), h.store, nil)
			if err != nil {
				t.Fatal(err)
			}
			var printed bytes.Buffer
			if err := printWaiting(&printed, history); err != nil {
				t.Fatal(err)
			}
			if want > 0 && !strings.Contains(printed.String(), "docs/design/a.md") {
				t.Fatalf("terminal omitted document: %s", printed.String())
			}
			if want > 0 {
				if designs[0]["document"] != "docs/design/a.md" || designs[0]["since"] != "1970-01-01T00:00:02Z" || !strings.Contains(designs[0]["why"].(string), "docs/tillr/roadmap.md:3") {
					t.Fatalf("location/order/time: %+v", designs)
				}
			}
			if after := storeFingerprint(t, h.store); after != before {
				t.Fatal("waiting mutated store")
			}
		})
	}
}

// The browser reads /why rather than /waiting. Both the shared display rows and
// raw planner JSON must carry warnings, even when the planner has no item rows.
func TestPlannerDesignWithoutItem(t *testing.T) {
	for _, hasItem := range []bool{false, true} {
		t.Run(fmt.Sprint(hasItem), func(t *testing.T) {
			h := newServeHarness(t)
			db, err := sql.Open("sqlite", "file:"+h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE design_without_item (document TEXT PRIMARY KEY, roadmap TEXT NOT NULL, roadmap_line INTEGER NOT NULL, first_seen INTEGER NOT NULL); INSERT INTO design_without_item VALUES ('docs/design/a.md','docs/roadmap.md',3,1000)`); err != nil {
				t.Fatal(err)
			}
			itemRows := `[]`
			if hasItem {
				itemRows = `[{"item_id":"item-a","rank":"a","why":"ready","planner_only":9007199254740993}]`
			}
			script := filepath.Join(t.TempDir(), "planner")
			writeScript(t, script, `printf '%s' '{"event_head_seq":7,"summary":{"held":{}},"rows":`+itemRows+`}'`)
			before := storeFingerprint(t, h.store)
			raw, rows, err := plannerWhy(context.Background(), plannerProgram{Path: script}, buildStamp{}, h.store, ".", "", "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if hasItem {
				want = 2
			}
			if len(rows) != want || rows[want-1].Document != "docs/design/a.md" {
				t.Fatalf("display rows: %+v", rows)
			}
			var answer struct {
				Rows []map[string]json.RawMessage `json:"rows"`
				Head int                          `json:"event_head_seq"`
			}
			if err := json.Unmarshal(raw, &answer); err != nil {
				t.Fatal(err)
			}
			if len(answer.Rows) != want || answer.Head != 7 || string(answer.Rows[want-1]["document"]) != `"docs/design/a.md"` {
				t.Fatalf("raw rows: %s", raw)
			}
			if hasItem && string(answer.Rows[0]["planner_only"]) != "9007199254740993" {
				t.Fatalf("planner field changed: %s", raw)
			}
			if after := storeFingerprint(t, h.store); after != before {
				t.Fatal("planner display mutated store")
			}
		})
	}
}
