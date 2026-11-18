package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// The face files a triage item for documents named as arguments, placed last by default,
// and answers the item, its report path and its documents.
func TestDocTriageFaceFilesTheItem(t *testing.T) {
	path, s := placeFixture(t, "a")
	root := scratch(t, "triage-root")
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "tillr", "workflows", "doc-triage@v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"config/tillr/workflows/doc-triage@v1.json": string(raw), "docs/design/a.md": "# A\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(bin, "doc-triage", "--store", path, "--root", root, "--title", "One design", "--now", "2026-11-18T09:00:00-05:00", "docs/design/a.md").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	const id = "item-doc-triage-2026-11-18-one-design"
	for _, want := range []string{`"kind":"item.doc-triage"`, `"item_id":"` + id + `"`, `"place":2`, `"report":"docs/triage/2026-11-18-one-design.md"`, `"documents":["docs/design/a.md"]`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("answer lacks %s: %s", want, out)
		}
	}
	var workflow, args string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT workflow, workflow_args FROM items WHERE item_id=?`, id).Scan(&workflow, &args)
	}); err != nil {
		t.Fatal(err)
	}
	if workflow != "doc-triage@v1" || args != `{"docs":["docs/design/a.md"]}` {
		t.Fatalf("stored %s %s", workflow, args)
	}
	out, err = exec.Command(bin, "doc-triage", "--store", path, "--root", root, "--title", "Nothing").CombinedOutput()
	if code := exitOf(err); code == 0 || !strings.Contains(string(out), "no-documents") {
		t.Fatalf("a triage of nothing: %d %s", code, out)
	}
}

func exitOf(err error) int {
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}
