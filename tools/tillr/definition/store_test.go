package definition_test

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func TestStoreDefinitionRoundTripAndRollback(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial, err := definition.ForStore(ctx, s)
	if err != nil || !initial.Default {
		t.Fatalf("default = %+v, %v", initial, err)
	}
	d, err := definition.GitLanding()
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "custom"
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	written, err := definition.Set(ctx, s, raw)
	if err != nil {
		t.Fatal(err)
	}
	read, err := definition.ForStore(ctx, s)
	if err != nil || !reflect.DeepEqual(written, read) || read.Default || !reflect.DeepEqual(read.Parsed, d) {
		t.Fatalf("round trip = %+v, %v", read, err)
	}
	if _, err := definition.Set(ctx, s, []byte(`{}`)); err == nil {
		t.Fatal("invalid definition accepted")
	}
	// A failing event insertion rolls back the declaration too.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER reject_definition BEFORE INSERT ON events WHEN NEW.kind = 'store.definition.set' BEGIN SELECT RAISE(ABORT, 'reject test event'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d.Name = "replacement"
	raw, err = json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := definition.Set(ctx, s, raw); err == nil {
		t.Fatal("event failure accepted")
	}
	after, err := definition.ForStore(ctx, s)
	if err != nil || !reflect.DeepEqual(after, written) {
		t.Fatalf("rollback = %+v, %v", after, err)
	}
	events, err := s.Events(ctx, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

func TestStoreDefinitionInvalidUTF8DoesNotWrite(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	before, err := definition.ForStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	d := before.Parsed
	d.Name = "checks"
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("checks"), []byte{'c', 'h', 'e', 'c', 'k', 's', 0xff}, 1)
	if _, err := definition.Read(bytes.NewReader(raw)); err == nil {
		t.Error("shared validation accepted invalid UTF-8")
	}
	if _, err := definition.Set(ctx, s, raw); err == nil {
		t.Error("Set accepted invalid UTF-8")
	}
	after, err := definition.ForStore(ctx, s)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("invalid UTF-8 changed declaration: %+v, %v", after, err)
	}
	events, err := s.Events(ctx, 0)
	if err != nil || len(events) != 0 {
		t.Errorf("invalid UTF-8 events: %+v, %v", events, err)
	}
}

func TestStoreDefinitionCorruptionDoesNotDefault(t *testing.T) {
	for _, value := range []string{
		`not-json`,
		`{"definition":"bad","version":1,"content":"{}","sha256":"wrong","default":false}`,
	} {
		t.Run(value, func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO meta (key,value) VALUES ('store.definition',?)`, value)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if got, err := definition.ForStore(ctx, s); err == nil || got.Default {
				t.Fatalf("corrupt declaration = %+v, %v", got, err)
			}
		})
	}
}

func TestStoreDefinitionMetadataMustMatch(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	embedded, err := definition.ForStore(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"name", "version", "hash", "default"} {
		t.Run(field, func(t *testing.T) {
			bad := embedded
			bad.Default = false
			switch field {
			case "name":
				bad.Name = "wrong"
			case "version":
				bad.Version++
			case "hash":
				bad.SHA256 = "wrong"
			case "default":
				bad.Default = true
			}
			raw, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO meta (key,value) VALUES ('store.definition',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, string(raw))
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := definition.ForStore(ctx, s); err == nil {
				t.Fatal("mismatched metadata accepted")
			}
		})
	}
}
