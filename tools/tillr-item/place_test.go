package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func placeFixture(t *testing.T, ranks ...string) (string, *store.Store) {
	t.Helper()
	path := filepath.Join(scratch(t, "place"), "board.db")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for i, rank := range ranks {
			if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,rank,updated_at) VALUES(?,?,'in-queue',?,0)`, fmt.Sprint(i), fmt.Sprint(i), rank); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return path, s
}
func placeRows(t *testing.T, s *store.Store) (map[string]string, []string) {
	t.Helper()
	values := map[string]string{}
	var order []string
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id,title,state,rank,updated_at,event_seq FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, state, rank string
			var at, seq int64
			if err := rows.Scan(&id, &title, &state, &rank, &at, &seq); err != nil {
				return err
			}
			values[id] = fmt.Sprintf("%s|%s|%s|%d|%d", title, state, rank, at, seq)
			order = append(order, id)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	return values, order
}
func TestPlaceDestinations(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"--to", "first"}, []string{"2", "0", "1", "3"}},
		{[]string{"--to", "last"}, []string{"0", "1", "3", "2"}},
		{[]string{"--before", "1"}, []string{"0", "2", "1", "3"}},
		{[]string{"--after", "0"}, []string{"0", "2", "1", "3"}},
	} {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			path, s := placeFixture(t, "a", "b", "c", "d")
			before, _ := placeRows(t, s)
			args := append([]string{"place", "--store", path, "--item", "2", "--now", "2026-11-17T12:00:00Z"}, tc.args...)
			code, out := run(t, args...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			after, order := placeRows(t, s)
			if !reflect.DeepEqual(order, tc.want) {
				t.Fatalf("order %v want %v", order, tc.want)
			}
			for id, v := range before {
				if id != "2" && after[id] != v {
					t.Fatalf("other row %s changed", id)
				}
			}
			var answer struct {
				Kind    string
				Payload struct {
					ItemID       string `json:"item_id"`
					Place, Count int
					EventSeq     int64 `json:"event_seq"`
				}
			}
			if err := json.Unmarshal(out, &answer); err != nil {
				t.Fatal(err)
			}
			wantPlace := 0
			for i, id := range tc.want {
				if id == "2" {
					wantPlace = i + 1
				}
			}
			if answer.Kind != "item.place" || answer.Payload.ItemID != "2" || answer.Payload.Place != wantPlace || answer.Payload.Count != 4 || answer.Payload.EventSeq == 0 {
				t.Fatalf("answer %s", out)
			}
		})
	}
}
func TestPlaceCrowdedAndRepeatedFirst(t *testing.T) {
	for _, ranks := range [][]string{{"a", "a0", "z"}, {"0", "1", "z"}, {"a", "a", "z"}, {"a", "", ""}} {
		t.Run(fmt.Sprint(ranks), func(t *testing.T) {
			path, s := placeFixture(t, ranks...)
			code, out := run(t, "place", "--store", path, "--item", "2", "--before", "1")
			if code != 0 {
				t.Fatalf("%d %s", code, out)
			}
			_, order := placeRows(t, s)
			if !reflect.DeepEqual(order, []string{"0", "2", "1"}) {
				t.Fatal(order)
			}
			for i := 0; i < 200; i++ {
				id := fmt.Sprint(i % 3)
				expected := []string{id}
				for _, other := range order {
					if other != id {
						expected = append(expected, other)
					}
				}
				code, out = run(t, "place", "--store", path, "--item", id, "--to", "first")
				if code != 0 {
					t.Fatalf("move %d: %d %s", i, code, out)
				}
				_, order = placeRows(t, s)
				if !reflect.DeepEqual(order, expected) {
					t.Fatalf("move %d: order %v want %v", i, order, expected)
				}
			}
		})
	}
}
func TestPlaceRespacingRetainsUnchangedTargetReceipt(t *testing.T) {
	// The moved target happens to already have its respaced key. Other rows
	// change, but its stored receipt must still be echoed rather than zeroed.
	path, s := placeFixture(t, "i0000000", "i0000000", "i0000000")
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET event_seq=77 WHERE item_id='2'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	code, out := run(t, "place", "--store", path, "--item", "2", "--before", "1")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	var answer struct {
		Payload struct {
			EventSeq int64 `json:"event_seq"`
		}
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Payload.EventSeq != 77 {
		t.Fatalf("receipt %d; want 77", answer.Payload.EventSeq)
	}
}

func TestPlaceRefusalsAndSelfAreReadOnly(t *testing.T) {
	for _, tc := range []struct {
		id          string
		args        []string
		state, rule string
	}{
		{"missing", []string{"--to", "first"}, "", "unknown-item"},
		{"0", []string{"--before", "missing"}, "", "unknown-item"},
		{"1", []string{"--to", "first"}, "landed", "not-in-order"},
		{"1", []string{"--to", "first"}, "closed", "not-in-order"},
		{"1", []string{"--to", "first"}, "resolved-elsewhere", "not-in-order"},
		{"1", []string{"--to", "first"}, "not-started", "not-in-order"},
		{"0", []string{"--after", "1"}, "not-started", "not-in-order"},
		{"0", []string{"--before", "0"}, "", ""},
		{"0", []string{"--after", "0"}, "", ""},
		{"0", []string{"--to", "first"}, "", ""},
	} {
		t.Run(tc.id+tc.state+fmt.Sprint(tc.args), func(t *testing.T) {
			path, s := placeFixture(t, "a", "b")
			if tc.state != "" {
				if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='1'`, tc.state)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := placeRows(t, s)
			code, out := run(t, append([]string{"place", "--store", path, "--item", tc.id}, tc.args...)...)
			want := 0
			if tc.rule != "" {
				want = 1
			}
			if code != want {
				t.Fatalf("exit %d want %d: %s", code, want, out)
			}
			var answer struct{ Payload struct{ Rule string } }
			json.Unmarshal(out, &answer)
			if answer.Payload.Rule != tc.rule {
				t.Fatalf("%s", out)
			}
			after, _ := placeRows(t, s)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rows changed")
			}
			var count int
			s.ReadTx(context.Background(), func(r *store.Reader) error { return r.QueryRow(`SELECT count(*) FROM events`).Scan(&count) })
			if count != 0 {
				t.Fatalf("%d events appended", count)
			}
		})
	}
}
