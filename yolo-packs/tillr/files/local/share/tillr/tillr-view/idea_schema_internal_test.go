package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestIdeasHTTPRefusesNewerSchemaWithoutSaving(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tillr-item")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../tillr-item"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	h, srv := placeServer(t, true, bin)
	if err := os.WriteFile(filepath.Join(h.dir, ".tillr.toml"), []byte("[intake]\ndir='docs/ideas'\npaths=['docs/design/']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,0)`, store.SchemaVersion+1)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	status, body := postPlace(t, srv, "/api/v1/ideas", `{"words":"Do not save me","place":{"to":"first"}}`, "application/json", "")
	if status != 503 || body["error"].(map[string]any)["code"] != "writer-unavailable" {
		t.Fatalf("%d %v", status, body)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.store), "intake")); !os.IsNotExist(err) {
		t.Fatalf("newer-schema writer created intake: %v", err)
	}
}
