package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type listedItem struct {
	ItemID    string   `json:"item_id"`
	Title     string   `json:"title"`
	State     string   `json:"state"`
	BlockedBy []string `json:"blocked_by"`
	UpdatedAt string   `json:"updated_at"`
}

func listItems(t *testing.T, db string, states ...string) []listedItem {
	t.Helper()
	args := []string{"list", "--store", db}
	for _, state := range states {
		args = append(args, "--state", state)
	}
	code, out := run(t, args...)
	if code != 0 {
		t.Fatalf("list exited %d: %s", code, out)
	}
	var got struct {
		Kind    string `json:"kind"`
		Payload struct {
			Items []listedItem `json:"items"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("list did not emit one record: %v\n%s", err, out)
	}
	if got.Kind != "item.list" || got.Payload.Items == nil {
		t.Fatalf("list needs an item.list record with an array: %s", out)
	}
	return got.Payload.Items
}

func TestListItemsOrderedAndFiltered(t *testing.T) {
	db := filepath.Join(scratch(t, "list"), "board.sqlite3")
	// Insert out of id order and keep a dependency on a finished item: list
	// reports stored blockers, not only dependencies that currently hold work.
	for _, id := range []string{"item-c", "item-a", "item-b"} {
		in := base(id, "title "+id)
		if id == "item-b" {
			in.BlockedBy = []string{"item-c", "item-a"}
		}
		if code, out := apply(t, db, envelope(t, "item.record", id, in)); code != 0 {
			t.Fatalf("apply exited %d: %s", code, out)
		}
	}
	if code, out := run(t, "state", "--store", db, "--item", "item-b", "--state", "in-queue", "--now", "2026-11-11T01:02:03Z"); code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}
	if code, out := run(t, "close", "--store", db, "--item", "item-c", "--reason", "not needed", "--by", "owner", "--now", "2026-11-12T00:00:00Z"); code != 0 {
		t.Fatalf("close exited %d: %s", code, out)
	}
	want := []listedItem{
		{"item-a", "title item-a", "not-started", []string{}, "2026-11-10T00:00:00Z"},
		{"item-b", "title item-b", "in-queue", []string{"item-a", "item-c"}, "2026-11-11T01:02:03Z"},
		{"item-c", "title item-c", "closed", []string{}, "2026-11-12T00:00:00Z"},
	}
	before := eventsOfKind(t, db, "item.update")
	for _, tc := range []struct {
		name   string
		states []string
		want   []listedItem
	}{
		{"all", nil, want},
		{"one state", []string{"in-queue"}, want[1:2]},
		{"repeated states", []string{"closed", "not-started"}, []listedItem{want[0], want[2]}},
		{"duplicate state", []string{"closed", "closed"}, want[2:]},
		{"no matches", []string{"landed"}, []listedItem{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listItems(t, db, tc.states...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("list = %#v, want %#v", got, tc.want)
			}
		})
	}
	if after := eventsOfKind(t, db, "item.update"); after != before {
		t.Fatalf("list appended events: before %d, after %d", before, after)
	}
}

func TestListItemsEmptyStore(t *testing.T) {
	db := filepath.Join(scratch(t, "list-empty"), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := listItems(t, db); len(got) != 0 {
		t.Fatalf("empty store returned %#v", got)
	}
}

func TestListItemsUsageAndMissingStore(t *testing.T) {
	for _, args := range [][]string{{"list"}, {"list", "--store", "unused", "extra"}, {"list", "--unknown"}} {
		if code, out := run(t, args...); code != 2 || len(out) != 0 {
			t.Fatalf("%v exited %d: %s", args, code, out)
		}
	}
	db := filepath.Join(scratch(t, "list-missing"), "absent.sqlite3")
	if code, out := run(t, "list", "--store", db); code != 2 || len(out) != 0 {
		t.Fatalf("missing store exited %d: %s", code, out)
	}
	if code, out := run(t, "list", "--help"); code != 0 || len(out) == 0 {
		t.Fatalf("help exited %d: %s", code, out)
	}
}

func TestListItemsTable(t *testing.T) {
	db := filepath.Join(scratch(t, "list-table"), "board.sqlite3")
	for _, id := range []string{"item-a", "item-b"} {
		in := base(id, "title "+id)
		if id == "item-b" {
			in.BlockedBy = []string{"item-a"}
		}
		if code, out := apply(t, db, envelope(t, "item.record", id, in)); code != 0 {
			t.Fatalf("apply exited %d: %s", code, out)
		}
	}
	if code, out := run(t, "state", "--store", db, "--item", "item-b", "--state", "in-queue", "--now", "2026-11-11T01:02:03Z"); code != 0 {
		t.Fatalf("state exited %d: %s", code, out)
	}

	// Default output remains JSON item.list.
	if got := listItems(t, db); len(got) != 2 {
		t.Fatalf("default list returned %d items, want 2", len(got))
	}

	// --table prints a readable table with header and aligned rows.
	code, out := run(t, "list", "--store", db, "--table")
	if code != 0 {
		t.Fatalf("list --table exited %d: %s", code, out)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("list --table printed %d lines, want 3:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "ITEM") || !strings.Contains(lines[0], "STATE") || !strings.Contains(lines[0], "TITLE") {
		t.Fatalf("missing header columns: %q", lines[0])
	}
	if !strings.Contains(lines[1], "item-a") || !strings.Contains(lines[1], "not-started") || !strings.Contains(lines[1], "title item-a") {
		t.Fatalf("unexpected row 1: %q", lines[1])
	}
	if !strings.Contains(lines[2], "item-b") || !strings.Contains(lines[2], "in-queue") || !strings.Contains(lines[2], "item-a") || !strings.Contains(lines[2], "title item-b") {
		t.Fatalf("unexpected row 2: %q", lines[2])
	}

	// --table respects --state filtering.
	code, out = run(t, "list", "--store", db, "--table", "--state", "in-queue")
	if code != 0 {
		t.Fatalf("list --table --state exited %d: %s", code, out)
	}
	lines = strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], "item-b") {
		t.Fatalf("filtered table = %q", out)
	}
}
