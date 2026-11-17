package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingShowsHeldLaneReleasedOrKeptReason(t *testing.T) {
	for _, released := range []bool{true, false} {
		t.Run(map[bool]string{true: "released", false: "kept"}[released], func(t *testing.T) {
			h := newServeHarness(t)
			db, err := sql.Open("sqlite", "file:"+h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var runID, itemID string
			if err := db.QueryRow(`SELECT run_id,item_id FROM runs LIMIT 1`).Scan(&runID, &itemID); err != nil {
				t.Fatal(err)
			}
			started, _ := json.Marshal(map[string]any{"decision": map[string]any{"item_id": itemID, "lane_id": "lane-1", "run_request": map[string]any{"run_id": runID, "item_id": itemID}}})
			held, _ := json.Marshal(map[string]any{"candidate": map[string]any{"item_id": itemID, "run_id": runID, "lane_id": "lane-1"}, "released": released, "reason": map[bool]string{true: "", false: "candidate pin cannot be read back"}[released]})
			for i, payload := range []string{string(started), string(held)} {
				kind := []string{"drain.started", "drain.candidate-lane"}[i]
				if _, err := db.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (?,?,'run',?,?)`, 9000+i, kind, runID, payload); err != nil {
					t.Fatal(err)
				}
			}
			s, err := store.OpenRead(context.Background(), h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			rows := []waitingRow{{ItemID: itemID, Class: "needs you", Why: "gate-harness-error"}}
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error { return addHeldLaneHistory(r, rows) }); err != nil {
				t.Fatal(err)
			}
			want := map[bool]string{true: "released", false: "kept"}[released]
			if rows[0].LaneStatus != want || !strings.Contains(rows[0].Why, "lane "+want) {
				t.Fatalf("missing lane status: %+v", rows[0])
			}
			if !released && !strings.Contains(rows[0].LaneReason, "cannot be read back") {
				t.Fatalf("missing kept reason: %+v", rows[0])
			}
			var table strings.Builder
			if err := printWaiting(&table, rows); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(table.String(), "lane "+want) {
				t.Fatalf("terminal omits status: %s", table.String())
			}
			raw, _ := json.Marshal(rows)
			if !strings.Contains(string(raw), `"lane_status":"`+want+`"`) {
				t.Fatalf("JSON omits status: %s", raw)
			}
		})
	}
}
