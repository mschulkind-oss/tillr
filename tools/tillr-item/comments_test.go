package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCommentsTakeZeroOneFive(t *testing.T) {
	for _, n := range []int{0, 1, 5} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			root := scratch(t, "comments")
			doc := "docs/design/example.md"
			if err := os.MkdirAll(filepath.Join(root, "docs/design"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, doc), []byte("# Example design\n\n## Shape\n\nA passage.\n"), 0644); err != nil {
				t.Fatal(err)
			}
			comments := []map[string]any{}
			for i := 0; i < n; i++ {
				comments = append(comments, map[string]any{"id": fmt.Sprintf("comment-%d", i), "comment": "Why this shape?", "created_at": 1700000000, "captured_block": "A passage.", "anchor": map[string]any{"source_line": 5}, "reactions": []any{}})
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/r/fixture/review" || r.URL.Query().Get("path") != doc {
					t.Errorf("request %s", r.URL)
				}
				if n == 0 {
					fmt.Fprint(w, "null")
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"file_path": doc, "comments": comments})
			}))
			defer server.Close()
			path := filepath.Join(root, "state.db")
			code, out := run(t, "comments", "take", "--store", path, "--root", root, "--repo", "fixture", "--doc", doc, "--vantage", server.URL)
			if code != 0 {
				t.Fatalf("comments take exited %d: %s", code, out)
			}
			if n == 0 {
				if len(out) != 0 {
					t.Fatalf("empty intake printed %s", out)
				}
				return
			}
			id := strings.TrimSpace(string(out))
			if id != "item-comments-example-1" {
				t.Fatalf("id %q", id)
			}
			s, err := store.OpenRead(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			err = s.ReadTx(context.Background(), func(r *store.Reader) error {
				var seat, title, fence, rank string
				if err := r.QueryRow("SELECT seat,title,allowed_paths,rank FROM items WHERE item_id=?", id).Scan(&seat, &title, &fence, &rank); err != nil {
					return err
				}
				if seat != "design" || title != fmt.Sprintf("Answer %d review comments on Example design", n) || !strings.Contains(fence, doc) || rank == "" {
					t.Fatalf("item %s %s %s %s", seat, title, fence, rank)
				}
				var count int
				if err := r.QueryRow("SELECT count(*) FROM item_criteria WHERE item_id=?", id).Scan(&count); err != nil {
					return err
				}
				if count != 3 {
					t.Fatalf("criteria %d", count)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
