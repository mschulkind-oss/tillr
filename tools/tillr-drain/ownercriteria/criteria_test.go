package ownercriteria

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestReadOwnerCriteriaForItems(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE item_criteria (
		item_id TEXT NOT NULL,
		criterion_id TEXT NOT NULL,
		text TEXT NOT NULL,
		state TEXT NOT NULL,
		claim_class TEXT NOT NULL DEFAULT 'agent'
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO item_criteria (item_id, criterion_id, text, state, claim_class) VALUES
		('item-a', 'c1', 'already done', 'done', 'owner-only'),
		('item-a', 'c2', 'owner approves release', 'not-done', 'owner-only'),
		('item-a', 'c3', 'second owner check', 'not-done', 'owner-only'),
		('item-b', 'c1', 'agent check', 'not-done', 'agent'),
		('item-c', 'c1', 'out of scope owner check', 'not-done', 'owner-only')
	`); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		qBefore := store.SQLQueriesIssued()
		empty, err := ReadOwnerCriteriaForItems(r, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 {
			t.Fatalf("empty ReadOwnerCriteriaForItems = %+v, want empty", empty)
		}
		if got := store.SQLQueriesIssued() - qBefore; got != 0 {
			t.Fatalf("empty ReadOwnerCriteriaForItems issued %d queries, want 0", got)
		}

		got, err := ReadOwnerCriteriaForItems(r, []string{"item-a", "item-b"})
		if err != nil {
			return err
		}
		if len(got) != 1 {
			t.Fatalf("ReadOwnerCriteriaForItems returned %d items, want 1: %+v", len(got), got)
		}
		if c := got["item-a"]; c == nil || c.CriterionID != "c2" || c.Text != "owner approves release" {
			t.Fatalf("item-a = %+v, want c2", c)
		}
		if _, ok := got["item-c"]; ok {
			t.Fatalf("unexpected out-of-scope item-c: %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadOwnerCriteriaForItemsWithoutClaimClassColumn(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE item_criteria (item_id TEXT, criterion_id TEXT, text TEXT, state TEXT)`); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		got, err := ReadOwnerCriteriaForItems(r, []string{"item-a"})
		if err != nil {
			return err
		}
		if len(got) != 0 {
			t.Fatalf("legacy ReadOwnerCriteriaForItems = %+v, want empty", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
