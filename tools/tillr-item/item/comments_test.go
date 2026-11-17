package item_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestGeneratedCommentIdentityExcludesBuildItems(t *testing.T) {
	for _, id := range []string{"item-comments-first-delivery", "item-comments-intake-in-the-loop", "item-comments-default-bmad"} {
		if item.IsCommentItem(id) {
			t.Fatalf("ordinary build item %s recognized as a batch", id)
		}
	}
	if !item.IsCommentItem("item-comments-example-1") {
		t.Fatal("generated batch not recognized")
	}
	paths := []string{"docs/design/a-b.md", "docs/design/a_b.md", "docs/design/a_5fb.md", "docs/design/A-B.md", "docs/other/a-b.md", "docs/design/a-b.markdown", "docs/design/sub/a-b.md"}
	seen := map[string]string{}
	for _, path := range paths {
		name := item.CommentDocumentName(path)
		if prior, ok := seen[name]; ok {
			t.Fatalf("document names collide: %s and %s as %s", prior, path, name)
		}
		seen[name] = path
	}
}

func TestPendingReviewIgnoresLegacyNotedReactions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		thread  string
		pending bool
	}{
		{"reviewer noted after addressed", `[{"actor":"agent","kind":"addressed","timestamp":2},{"actor":"reviewer","kind":"noted","timestamp":3}]`, false},
		{"agent noted does not answer", `[{"actor":"agent","kind":"noted","timestamp":2}]`, true},
		{"late root answer after reviewer noted", `[{"actor":"reviewer","kind":"noted","timestamp":2},{"actor":"agent","kind":"addressed","timestamp":3,"answers_round":0}]`, false},
		{"reviewer followup", `[{"actor":"agent","kind":"addressed","timestamp":2},{"actor":"reviewer","kind":"needs_clarification","timestamp":3}]`, true},
		{"reworded reviewer reply", `[{"actor":"reviewer","kind":"needs_clarification","timestamp":2,"edited_at":4},{"actor":"agent","kind":"addressed","timestamp":3,"answers_round":1}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reactions []json.RawMessage
			if err := json.Unmarshal([]byte(tc.thread), &reactions); err != nil {
				t.Fatal(err)
			}
			c := item.ReviewComment{ID: "a", Comment: "Why?", CreatedAt: 1, Reactions: reactions}
			if item.PendingReview(c) != tc.pending {
				t.Fatalf("pending=%v want %v", item.PendingReview(c), tc.pending)
			}
		})
	}
}

func TestReadReviewRefusesMalformedReaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"file_path":"docs/design/example.md","comments":[{"id":"a","comment":"Why?","reactions":[12]}]}`))
	}))
	defer server.Close()
	if _, err := item.ReadReview(context.Background(), server.URL, "repo", "docs/design/example.md"); err == nil {
		t.Fatal("malformed Vantage thread silently accepted")
	}
}

func TestCommentsIntakeFenceRankAndSync(t *testing.T) {
	ctx := context.Background()
	root := scratch(t)
	files := map[string]string{
		"docs/design/example.md":      "# A title\n\n## Shape\nA passage.\n",
		"docs/design/example-plan.md": "# Plan\n",
		"docs/roadmap.md":             "# Roadmap\n\n- [Design](design/example.md) — Build this.\n",
	}
	for p, raw := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := store.Open(ctx, filepath.Join(root, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var id string
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec("INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('ordinary','Build','in-queue','m',1),('fixer-main-abc','Fix','in-queue','000',1)"); err != nil {
			return err
		}
		var e error
		id, e = item.TakeComments(ctx, tx, root, "docs/design/example.md", []item.ReviewComment{{ID: "comment", Comment: "Why?", CreatedAt: 1}}, time.Now())
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var rank, raw string
		if err := r.QueryRow("SELECT rank,allowed_paths FROM items WHERE item_id=?", id).Scan(&rank, &raw); err != nil {
			return err
		}
		if !("000" < rank && rank < "m") {
			t.Fatalf("comment rank %q not between fixer and ordinary item", rank)
		}
		for path := range files {
			if !strings.Contains(raw, path) {
				t.Errorf("fence lacks %s: %s", path, raw)
			}
		}
		report, err := item.Sync(ctx, r, nil, item.SyncInput{Root: root})
		if err != nil {
			return err
		}
		for _, unshared := range report.UnsharedOpenIDs {
			if unshared == id {
				t.Fatal("sync reported a loop-created batch as unshared")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
