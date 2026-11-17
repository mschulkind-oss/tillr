package item_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCommentHistoryBeforeLedger(t *testing.T) {
	root := scratch(t)
	doc := "docs/design/example.md"
	if err := os.MkdirAll(filepath.Join(root, "docs/design"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, doc), []byte("# Example\n\nA passage.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(root, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	comments := []item.ReviewComment{{ID: "a", Comment: "Why?", CreatedAt: 1}}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		id, err := item.TakeComments(ctx, tx, root, doc, comments, time.Now())
		if err != nil {
			return err
		}
		if id == "" {
			t.Fatal("no initial batch")
		}
		// Simulate the pre-ledger history made by hand intake, not an empty store.
		if _, err = tx.Exec("DELETE FROM comment_taken"); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE items SET state='closed'"); err != nil {
			return err
		}
		id, err = item.TakeComments(ctx, tx, root, doc, comments, time.Now())
		if id != "" {
			t.Fatalf("legacy identity retaken: %s", id)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestQuietCommentsWaitForWholeDocument(t *testing.T) {
	comments := []item.ReviewComment{{ID: "a", Comment: "First", CreatedAt: 1001}, {ID: "b", Comment: "Second", CreatedAt: 1032}}
	got, _ := item.QuietComments(comments, time.Unix(1000, 0), time.Unix(1062, 0), 60*time.Second, false)
	if len(got) != 0 {
		t.Fatalf("took earlier comment during document edits: %+v", got)
	}
	got, _ = item.QuietComments(comments, time.Unix(1000, 0), time.Unix(1093, 0), 60*time.Second, false)
	if len(got) != 2 {
		t.Fatalf("quiet document: %+v", got)
	}
}
