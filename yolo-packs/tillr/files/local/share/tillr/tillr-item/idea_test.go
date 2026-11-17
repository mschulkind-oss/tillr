package main_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func ideaRun(t *testing.T, words []byte, args ...string) (int, []byte) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"idea"}, args...)...)
	cmd.Stdin = bytes.NewReader(words)
	out, err := cmd.CombinedOutput()
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), out
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, out
}
func ideaFixture(t *testing.T) (string, string, *store.Store) {
	t.Helper()
	path, s := placeFixture(t, "a", "b")
	root := scratch(t, "idea-root")
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[intake]\ndir = 'docs/ideas'\npaths = ['docs/design/', 'docs/roadmap.md']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, path, s
}
func TestIdeaBytesDefinitionRetryAndPlace(t *testing.T) {
	root, path, s := ideaFixture(t)
	words := []byte("Improve the page\r\n  Keep these words. 雪\n")
	hash := fmt.Sprintf("%x", sha256.Sum256(words))
	id := "item-idea-" + hash[:10]
	code, out := ideaRun(t, words, "--root", root, "--store", path, "--after", "0")
	if code != 0 || !bytes.Contains(out, []byte(`"place":2`)) {
		t.Fatalf("%d %s", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), "intake", id+".txt"))
	if err != nil || !bytes.Equal(raw, words) {
		t.Fatalf("words %q: %v", raw, err)
	}
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var title, seat, state, paths, storedHash, tracked string
		var size, n int
		if err := r.QueryRow(`SELECT title,seat,state,allowed_paths FROM items WHERE item_id=?`, id).Scan(&title, &seat, &state, &paths); err != nil {
			return err
		}
		if title != "Improve the page" || seat != "design" || state != "in-queue" || !strings.Contains(paths, "docs/ideas/"+id+".txt") {
			t.Fatalf("%s %s %s %s", title, seat, state, paths)
		}
		if err := r.QueryRow(`SELECT words_hash,words_size,words_path FROM idea_intake WHERE item_id=?`, id).Scan(&storedHash, &size, &tracked); err != nil {
			return err
		}
		if storedHash != hash || size != len(words) || tracked != "docs/ideas/"+id+".txt" {
			t.Fatalf("intake %s %d %s", storedHash, size, tracked)
		}
		if err := r.QueryRow(`SELECT count(*) FROM item_criteria WHERE item_id=?`, id).Scan(&n); err != nil {
			return err
		}
		if n != 3 {
			t.Fatal(n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, order := placeRows(t, s)
	code, out = ideaRun(t, words, "--root", root, "--store", path, "--to", "last")
	after, retryOrder := placeRows(t, s)
	if code != 0 || !bytes.Contains(out, []byte(id)) || fmt.Sprint(before, order) != fmt.Sprint(after, retryOrder) {
		t.Fatalf("retry %d %s", code, out)
	}
}
func TestIdeaRefusals(t *testing.T) {
	for _, tc := range []struct {
		words []byte
		rule  string
	}{{nil, "words-empty"}, {[]byte(" \n\t"), "words-empty"}, {[]byte{255}, "words-not-text"}, {bytes.Repeat([]byte("x"), 256*1024+1), "words-too-large"}} {
		t.Run(tc.rule, func(t *testing.T) {
			root, path, _ := ideaFixture(t)
			code, out := ideaRun(t, tc.words, "--root", root, "--store", path, "--to", "first")
			if code == 0 || !bytes.Contains(out, []byte(tc.rule)) {
				t.Fatalf("%d %s", code, out)
			}
		})
	}
	for _, settings := range []string{"", "[intake]\ndir='docs/ideas'", "[intake]\npaths=['docs/design/']"} {
		root, path, _ := ideaFixture(t)
		os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(settings), 0600)
		code, out := ideaRun(t, []byte("An idea"), "--root", root, "--store", path, "--to", "first")
		if code == 0 || !bytes.Contains(out, []byte("intake-not-configured")) {
			t.Fatalf("%d %s", code, out)
		}
	}
}
func TestIdeaRetryStillRequiresIntakeConfiguration(t *testing.T) {
	root, path, s := ideaFixture(t)
	words := []byte("Configured first save")
	code, out := ideaRun(t, words, "--root", root, "--store", path, "--to", "first")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	before, order := placeRows(t, s)
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	code, out = ideaRun(t, words, "--root", root, "--store", path, "--to", "last")
	if code == 0 || !bytes.Contains(out, []byte("intake-not-configured")) {
		t.Fatalf("%d %s", code, out)
	}
	after, retryOrder := placeRows(t, s)
	if fmt.Sprint(before, order) != fmt.Sprint(after, retryOrder) {
		t.Fatal("unconfigured retry changed the store")
	}
}

func TestIdeaStoreFailurePreservesFileAndRollsBack(t *testing.T) {
	root, path, s := ideaFixture(t)
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_intake BEFORE INSERT ON idea_intake BEGIN SELECT RAISE(ABORT,'forced failure'); END;`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	words := []byte("Preserve me")
	hash := fmt.Sprintf("%x", sha256.Sum256(words))
	id := "item-idea-" + hash[:10]
	file := filepath.Join(filepath.Dir(path), "intake", id+".txt")
	code, out := ideaRun(t, words, "--root", root, "--store", path, "--to", "first")
	if code == 0 || !bytes.Contains(out, []byte(file)) {
		t.Fatalf("%d %s", code, out)
	}
	raw, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(raw, words) {
		t.Fatalf("%q %v", raw, err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM items WHERE item_id=?`, id).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("item escaped rollback")
		}
		if err := r.QueryRow(`SELECT count(*) FROM idea_intake WHERE item_id=?`, id).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("intake escaped rollback")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
