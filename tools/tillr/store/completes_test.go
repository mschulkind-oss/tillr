package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStoreCompletes(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	got, err := s.Completes(ctx)
	if err != nil {
		t.Fatalf("default Completes: %v", err)
	}
	if got != "landing" {
		t.Fatalf("default Completes = %q, want landing", got)
	}

	rec, err := json.Marshal(map[string]any{
		"content": `{"completes":"hook"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, definitionMetaKey, string(rec))
		return err
	}); err != nil {
		t.Fatalf("WriteTx: %v", err)
	}

	got, err = s.Completes(ctx)
	if err != nil {
		t.Fatalf("hook Completes: %v", err)
	}
	if got != "hook" {
		t.Fatalf("hook Completes = %q, want hook", got)
	}
}
