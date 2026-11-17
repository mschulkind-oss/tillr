package main_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
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
