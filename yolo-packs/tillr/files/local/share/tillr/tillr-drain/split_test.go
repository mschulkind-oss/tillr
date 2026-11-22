package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSplitFaceFilesDesignStatementFromStdin(t *testing.T) {
	root := scratch(t, "split")
	workflow := filepath.Join(root, "config/tillr/workflows/planned-build@v1.json")
	if err := os.MkdirAll(filepath.Dir(workflow), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := filepath.Join(root, "board.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var parent string
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{Words: []byte("Build and test it"), Title: "Parent", Destination: item.Destination{To: "last"}, At: time.Now().UTC()})
		parent = env.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if code, out := invoke(t, "split", "--help"); code != 0 || !strings.Contains(string(out), "JSON statement from stdin") {
		t.Fatalf("help exited %d: %s", code, out)
	}
	cmd := exec.Command(bin, "split", "--root", root, "--store", path, "--item", parent)
	cmd.Stdin = strings.NewReader(`{"parts":[{"label":"build","title":"Build","share":"Build it","required":true},{"label":"test","title":"Test","share":"Test it","follows":["build"],"required":true}],"coverage":{}}`)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("split: %v: %s", err, &stderr)
	}
	var ids map[string]string
	if err := json.Unmarshal(out, &ids); err != nil || len(ids) != 2 || ids["build"] == "" || ids["test"] == "" {
		t.Fatalf("part IDs: %s (%v)", out, err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		for _, id := range ids {
			var express bool
			if err := r.QueryRow(`SELECT express FROM items WHERE item_id=?`, id).Scan(&express); err != nil {
				return err
			}
			if !express {
				t.Errorf("part %s lost express standing", id)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
