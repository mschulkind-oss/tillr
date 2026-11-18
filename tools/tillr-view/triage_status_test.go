package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestActualSyncToStoreStatusShowsUnsharedCount(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("fixture root: %v %s", err, out)
	}
	path := filepath.Join(root, "state.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE items(item_id TEXT PRIMARY KEY,title TEXT NOT NULL,body_ref TEXT,state TEXT NOT NULL,design_doc TEXT,updated_at INTEGER,event_seq INTEGER NOT NULL DEFAULT 0)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('item-request','Request','in-queue',1)`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Invoke the real writer without importing it into the read-only view; importing
	// its package would register migrations and change every legacy reader fixture.
	sync := exec.Command("go", "run", ".", "sync", "--store", path, "--root", root, "--json")
	sync.Dir = filepath.Join("..", "tillr-item")
	raw, err := sync.Output()
	if err != nil {
		t.Fatalf("actual sync: %v", err)
	}
	var observed struct {
		Payload struct {
			UnsharedOpen int `json:"unshared_open"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Payload.UnsharedOpen != 1 {
		t.Fatalf("sync count: %s", raw)
	}
	r, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM events WHERE kind='item.sync'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatalf("sync unexpectedly persisted a batch event: %d", n)
		}
		return nil
	})
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	env, code := statusFace([]string{"--store", path, "--root", root})
	if code != record.ExitOK {
		t.Fatalf("status exit %d", code)
	}
	var payload statusPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.UnsharedOpen == nil || *payload.UnsharedOpen != 1 {
		t.Fatalf("real sync had no status count: %s", env.Payload)
	}
}
