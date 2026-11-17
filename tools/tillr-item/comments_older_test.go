package main_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCommentsTakeOlderFromSettings(t *testing.T) {
	root := scratch(t, "comments-older")
	for _, dir := range []string{"docs/design", ".tillr"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	doc := "docs/design/example.md"
	if err := os.WriteFile(filepath.Join(root, doc), []byte("# Example\n\nA passage.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"comments":[{"id":"older","comment":"Why?","created_at":1,"reactions":[]}]}`)
	}))
	defer server.Close()
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(fmt.Sprintf("[comments]\nvantage_url=%q\nrepo='fixture'\npaths=['docs/design/']\n", server.URL)), 0644); err != nil {
		t.Fatal(err)
	}
	invoke := func(older bool) string {
		t.Helper()
		args := []string{"comments", "take", "--root", root, "--doc", doc, "--now", "2026-11-17T00:00:00Z"}
		if older {
			args = append(args, "--older")
		}
		code, out := run(t, args...)
		if code != 0 {
			t.Fatalf("take %d %s", code, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := invoke(false); got != "" {
		t.Fatalf("old comment taken automatically: %s", got)
	}
	if got := invoke(true); got != "item-comments-example-1" {
		t.Fatalf("older not taken: %s", got)
	}
	if got := invoke(true); got != "" {
		t.Fatalf("open batch duplicated: %s", got)
	}
	st, err := store.Open(context.Background(), filepath.Join(root, ".tillr", "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	err = st.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := tx.Exec("UPDATE items SET state='closed'"); return err })
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := invoke(true); got != "" {
		t.Fatalf("same identity taken again: %s", got)
	}
}
