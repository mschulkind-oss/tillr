package main_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func reconcileIndexCommand(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "reconcile-index"), "index.py")
	if err := os.WriteFile(path, []byte("import sys\nsys.stdout.write("+strconv.Quote(raw)+")\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return "python3 " + path
}

func TestReconcileFaceReadOnlyAndDeterministic(t *testing.T) {
	repo := syncRepo(t, map[string]string{"docs/design.md": "# Accepted design\n", "docs/outside.md": "# No status\n", "docs/other.md": syncGood})
	db := filepath.Join(scratch(t, "reconcile-db"), "state.db")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "sync", "--root", repo, "--store", db); code != 0 {
		t.Fatalf("populating store: exit %d: %s", code, out)
	}
	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	command := reconcileIndexCommand(t, `{"index":{"documents":[{"path":"docs/design.md","stage":"DESIGN","status":"accepted","questions":[]}]}}`)
	args := []string{"reconcile", "--root", repo, "--store", db, "--questions-cmd", command}
	code, first := run(t, args...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, first)
	}
	code, second := run(t, args...)
	if code != 0 || !bytes.Equal(first, second) {
		t.Fatalf("repeat exit %d: %s != %s", code, first, second)
	}
	after, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("store bytes changed")
	}
	if !strings.Contains(string(first), `id="item-design-design"`) || !strings.Contains(string(first), "outside-index: docs/outside.md") {
		t.Fatal(string(first))
	}
	missing := filepath.Join(scratch(t, "reconcile-missing"), "absent.db")
	code, out := run(t, "reconcile", "--root", repo, "--store", missing, "--questions-cmd", command)
	if code != 2 || len(out) != 0 {
		t.Fatalf("missing store exit %d stdout %s", code, out)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing store created")
	}
}

func TestReconcileFaceRefusalHasNoStdout(t *testing.T) {
	repo := syncRepo(t, map[string]string{"docs/a-b.md": "# One\n", "docs/A_b.md": "# Two\n"})
	for _, tc := range []struct{ raw, reason string }{
		{`{"index":{"documents":[{"path":"docs/a-b.md"},{"path":"docs/A_b.md"}]}}`, "slug-collision"},
		{`{"index":{"refused":true,"documents":[]}}`, "index-unreadable"},
		{`{"index":{"documents":[]}}`, "index-empty"},
	} {
		cmd := exec.Command(bin, "reconcile", "--root", repo, "--questions-cmd", reconcileIndexCommand(t, tc.raw))
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.reason) {
			t.Fatalf("err %v stdout %s stderr %s", err, stdout.String(), stderr.String())
		}
	}
}
