package item

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

func TestStoredItemReadsStoreWithoutTaskColumn(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")

	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO items (item_id, title, state, updated_at) VALUES ('item-old', 'Old item', 'in-queue', 1000)`,
		`ALTER TABLE items DROP COLUMN task`,
		`ALTER TABLE items DROP COLUMN workflow`,
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			raw.Close()
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	rs, err := store.OpenRead(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()

	if err := rs.ReadTx(ctx, func(r *store.Reader) error {
		got, found, err := storedItem(r, "item-old")
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("storedItem reported not found")
		}
		if got.ItemID != "item-old" || got.Title != "Old item" || got.State != "in-queue" {
			t.Fatalf("unexpected item fields: %+v", got)
		}
		if got.Task != "" {
			t.Fatalf("Task = %q, want empty", got.Task)
		}
		if got.Workflow != "" {
			t.Fatalf("Workflow = %q, want empty", got.Workflow)
		}
		var hasTask int
		if err := r.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('items') WHERE name = 'task'`).Scan(&hasTask); err != nil {
			return err
		}
		if hasTask != 0 {
			t.Fatal("reader-only open migrated items.task")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
