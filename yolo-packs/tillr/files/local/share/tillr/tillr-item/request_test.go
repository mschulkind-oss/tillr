package main_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func requestRun(t *testing.T, words string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"request"}, args...)...)
	cmd.Stdin = bytes.NewReader([]byte(words))
	out, err := cmd.CombinedOutput()
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// The face reads the words from standard input, files the item last by default with the
// seat's default workflow and the named fence, answers the same item on an identical
// refile, and refuses a fence-less request with its rule and a nonzero exit.
func TestRequestFaceFilesTheWords(t *testing.T) {
	path, s := placeFixture(t, "a")
	root := scratch(t, "request-root")
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "tillr", "workflows", "planned-build@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config/tillr/workflows/planned-build@v1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	const words = "Remove the tillr-trace index cache; readers answer from source records.\n"
	args := []string{"--store", path, "--root", root, "--title", "Remove the index cache", "--path", "tools/tillr-trace/", "--path", "tools/tillr-view/", "--now", "2026-11-18T22:15:00-05:00"}
	code, out := requestRun(t, words, args...)
	if code != 0 {
		t.Fatalf("%d: %s", code, out)
	}
	for _, want := range []string{`"kind":"item.request"`, `"item_id":"item-request-`, `"place":2`, `"workflow":"planned-build@v1"`, `"seat":"build"`, `"allowed_paths":["tools/tillr-trace/","tools/tillr-view/"]`} {
		if !strings.Contains(out, want) {
			t.Errorf("answer lacks %s: %s", want, out)
		}
	}
	var stored string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT words FROM request_words`).Scan(&stored)
	}); err != nil || stored != words {
		t.Fatalf("stored words %q %v", stored, err)
	}
	code, again := requestRun(t, words, args...)
	if code != 0 || !strings.Contains(again, `"place":2`) || !strings.Contains(again, `"kind":"item.request"`) {
		t.Fatalf("refile %d: %s", code, again)
	}
	code, out = requestRun(t, "Something else.\n", "--store", path, "--root", root, "--title", "No fence")
	if code == 0 || !strings.Contains(out, "fence-required") {
		t.Fatalf("a request with no fence: %d %s", code, out)
	}
	code, out = requestRun(t, "Something else.\n", "--store", path, "--root", root, "--title", "Wrong workflow", "--path", "docs/", "--workflow", "missing@v1")
	if code == 0 || !strings.Contains(out, "workflow-missing") {
		t.Fatalf("a request naming no workflow file: %d %s", code, out)
	}
	if exitOf(exec.Command(bin, "request", "--help").Run()) != 0 {
		t.Fatal("request --help failed")
	}
}
