package definition_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestReaderDefinitionCopiesCannotChangeLaterReads(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Completes = "hook"
	d.Hooks = &definition.Hooks{Argv: []string{"hook"}, Names: []string{"complete-item"}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(ctx, s, raw); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		first, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		first.Parsed.States[0] = "tampered"
		first.Parsed.Finished[0] = "tampered"
		first.Parsed.Transitions[0].From = "tampered"
		first.Parsed.Prerequisites[0] = "tampered"
		first.Parsed.Hooks.Argv[0] = "tampered"
		first.Parsed.Hooks.Names[0] = "tampered"
		second, err := definition.ForReader(r)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(second.Parsed, d) {
			t.Fatalf("caller changed cached declaration: %+v", second.Parsed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWriterReaderDefinitionIsNeverCached(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		r := tx.Reader()
		if _, err := definition.ForReader(r); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('store.definition','invalid')`); err != nil {
			return err
		}
		if _, err := definition.ForReader(r); err == nil {
			t.Fatal("write reader retained stale valid declaration")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
