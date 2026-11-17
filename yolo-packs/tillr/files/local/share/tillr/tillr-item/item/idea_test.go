package item

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestIdeaUniqueIDAndTitle(t *testing.T) {
	words := []byte(strings.Repeat("雪 ", 50) + "\nThe rest")
	hash := fmt.Sprintf("%x", sha256.Sum256(words))
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(root, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES(?, 'collision', 'in-queue',0)`, "item-idea-"+hash[:10]); err != nil {
			return err
		}
		dir := filepath.Join(root, "intake")
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "item-idea-"+hash[:11]+".txt"), []byte("other words"), 0600); err != nil {
			return err
		}
		env, err := Idea(ctx, tx, words, hash, dir, settings.Intake{Dir: "docs/ideas", Paths: []string{"docs/design/"}}, Destination{To: "first"}, time.Now(), "owner", "cli")
		if err != nil {
			return err
		}
		if !strings.Contains(string(env.Payload), "item-idea-"+hash[:12]) {
			t.Fatal(string(env.Payload))
		}
		var title string
		if err := tx.Reader().QueryRow(`SELECT title FROM items WHERE item_id=?`, "item-idea-"+hash[:12]).Scan(&title); err != nil {
			return err
		}
		if utf8.RuneCountInString(title) > 80 || title != strings.TrimSpace(strings.Repeat("雪 ", 40)) {
			t.Fatalf("title %q", title)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestIdeaExhaustedIDNeverOverwritesAnItem(t *testing.T) {
	words := []byte("A deliberately occupied hash")
	hash, err := IdeaHash(words)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(root, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		for n := 10; n <= len(hash); n++ {
			if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES(?, 'keep me', 'in-queue',0)`, "item-idea-"+hash[:n]); err != nil {
				return err
			}
		}
		_, err := Idea(ctx, tx, words, hash, filepath.Join(root, "intake"), settings.Intake{Dir: "docs/ideas", Paths: []string{"docs/design/"}}, Destination{To: "first"}, time.Now(), "owner", "cli")
		if err == nil {
			t.Fatal("exhausted hash overwrote an unrelated item")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIdeaHashExactBounds(t *testing.T) {
	for _, n := range []int{1, MaxIdeaWords} {
		if _, err := IdeaHash([]byte(strings.Repeat("x", n))); err != nil {
			t.Fatal(err)
		}
	}
	if got := ideaTitle("\nSecond line"); got != "" {
		t.Fatal(got)
	}
	if got := ideaTitle(strings.Repeat("x", 81)); utf8.RuneCountInString(got) != 80 {
		t.Fatal(got)
	}
}
