package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestWaitingDirtyPathsAndElapsedStreak(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC().Truncate(time.Second)
	for _, q := range []string{
		`DELETE FROM dispatches`,
		`INSERT INTO items(item_id,title,state,updated_at) VALUES ('one','one','in-queue',0),('two','two','in-queue',0),('landed','landed','landed',0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i, d := range []struct {
		id, reason string
		at         time.Time
	}{
		{"one", "trunk-path-dirty: old.txt", now.Add(-time.Hour)},
		{"one", "trunk-path-dirty: a.txt", now.Add(-2 * time.Minute)},
		{"one", "trunk-path-dirty: a.txt", now.Add(-time.Minute)},
		{"two", "trunk-path-dirty: a.txt, b.txt", now.Add(-3 * time.Minute)},
		{"landed", "trunk-path-dirty: a.txt", now.Add(-time.Hour)},
	} {
		if _, err := db.Exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES (?,?, 'hold',?,?)`, i, d.id, d.reason, d.at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	data := body["data"].([]any)
	seen := 0
	for _, v := range data {
		row := v.(map[string]any)
		id := row["item_id"]
		if id == "landed" {
			t.Fatal("landed item still waiting")
		}
		if id != "one" && id != "two" {
			if row["waited"] != "" {
				t.Fatalf("other row has elapsed wait: %v", row)
			}
			continue
		}
		seen++
		if row["class"] != "waits on work" || row["where"] != "land" || row["mover"] != "operator; commit or restore the path" {
			t.Fatalf("dirty wait unclassified: %v", row)
		}
		elapsed, err := time.ParseDuration(row["waited"].(string))
		want := 2 * time.Minute
		paths := "a.txt"
		if id == "two" {
			want = 3 * time.Minute
			paths = "a.txt, b.txt"
		}
		if err != nil || elapsed < want || elapsed > want+5*time.Second {
			t.Fatalf("elapsed %v / %v: %v", elapsed, err, row)
		}
		if row["since"] != now.Add(-want).Format(time.RFC3339Nano) || row["why"] != "trunk-path-dirty: "+paths {
			t.Fatalf("wrong streak/path: %v", row)
		}
		var out bytes.Buffer
		var terminal waitingRow
		encoded, _ := json.Marshal(row)
		if err := json.Unmarshal(encoded, &terminal); err != nil {
			t.Fatal(err)
		}
		if err := printWaiting(&out, []waitingRow{terminal}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), paths) || !strings.Contains(out.String(), row["waited"].(string)) {
			t.Fatalf("table hides path or wait: %s", out.String())
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d waits", seen)
	}
}
