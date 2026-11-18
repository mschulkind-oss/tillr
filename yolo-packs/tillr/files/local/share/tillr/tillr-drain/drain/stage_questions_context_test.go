package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// Staging and resume both need only stored fields, never an index subprocess.
func TestStagedItemReadsStoredFieldsWithoutIndex(t *testing.T) {
	for _, presence := range []string{"file", "missing", "directory"} {
		t.Run(presence, func(t *testing.T) {
			root := t.TempDir()
			storePath := filepath.Join(root, "state.db")
			seedStagedItem(t, storePath, "item-a", "docs/")
			doc := filepath.Join(root, "design.md")
			switch presence {
			case "file":
				if err := os.WriteFile(doc, []byte("# Design\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(doc, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			s, err := store.Open(context.Background(), storePath, false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				env, err := item.Show(tx.Reader(), "item-a", root)
				if err != nil {
					return err
				}
				var payload map[string]any
				if err := json.Unmarshal(env.Payload, &payload); err != nil {
					return err
				}
				payload["design_doc"] = "design.md"
				env.Payload, err = json.Marshal(payload)
				if err != nil {
					return err
				}
				_, err = item.Apply(context.Background(), tx, env)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			command := filepath.Join(root, "uvx")
			if err := os.WriteFile(command, []byte("#!/bin/sh\n: > index-started\nprintf '%s\\n' '{\"index\":{\"documents\":[]}}'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			got, err := readStagedItem(context.Background(), storePath, root, "item-a")
			if presence == "file" {
				if err != nil {
					t.Fatal(err)
				}
				if got.ItemID != "item-a" || len(got.AllowedPaths) != 1 || got.AllowedPaths[0] != "docs/" {
					t.Fatalf("stored fields lost: %+v", got)
				}
			} else if err == nil || !strings.Contains(err.Error(), "which is not there") {
				t.Fatalf("%s document: want design-doc-missing, got %v", presence, err)
			}
			if _, err := os.Stat(filepath.Join(root, "index-started")); !os.IsNotExist(err) {
				t.Fatalf("staging started the index: marker stat = %v", err)
			}
		})
	}
}
