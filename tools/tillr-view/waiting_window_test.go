package main

import (
	"bytes"
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWaitingShowsOnlyUnexpiredBackendWindows(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reset := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	for _, statement := range []string{
		`CREATE TABLE backend_windows (backend TEXT PRIMARY KEY,kind TEXT NOT NULL,reset_at INTEGER NOT NULL)`,
		`INSERT INTO backend_windows VALUES ('claude','five_hour',` + strconv.FormatInt(reset.UnixMilli(), 10) + `),('pi','seven_day',1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := printWaiting(&output, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "backend:claude") || !strings.Contains(output.String(), "five_hour") || !strings.Contains(output.String(), reset.Format(time.RFC3339)) {
		t.Fatalf("missing window: %s", &output)
	}
	if strings.Contains(output.String(), "backend:pi") {
		t.Fatalf("expired window: %s", &output)
	}
	if _, err := db.Exec(`UPDATE backend_windows SET reset_at = 1`); err != nil {
		t.Fatal(err)
	}
	rows, err = readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := printWaiting(&output, rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "backend:claude") {
		t.Fatalf("window remained: %s", &output)
	}
}
