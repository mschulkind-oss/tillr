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
	var express int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT words, express FROM request_words JOIN items USING(item_id)`).Scan(&stored, &express)
	}); err != nil || stored != words || express != 1 {
		t.Fatalf("stored words=%q express=%d err=%v", stored, express, err)
	}
	code, again := requestRun(t, words, args...)
	if code != 0 || !strings.Contains(again, `"place":2`) || !strings.Contains(again, `"kind":"item.request"`) {
		t.Fatalf("refile %d: %s", code, again)
	}
	code, out = requestRun(t, words, append(append([]string{}, args...), "--ordinary")...)
	if code == 0 || !strings.Contains(out, "request-differs") {
		t.Fatalf("toggling --ordinary on existing request: %d %s", code, out)
	}
	const ordinaryWords = "Keep this one on the ordinary queue.\n"
	ordArgs := []string{"--store", path, "--root", root, "--title", "Ordinary request", "--path", "docs/", "--ordinary", "--now", "2026-11-18T22:16:00-05:00"}
	code, out = requestRun(t, ordinaryWords, ordArgs...)
	if code != 0 {
		t.Fatalf("ordinary request %d: %s", code, out)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT express FROM items WHERE title='Ordinary request'`).Scan(&express)
	}); err != nil || express != 0 {
		t.Fatalf("ordinary express=%d err=%v", express, err)
	}
	code, again = requestRun(t, ordinaryWords, ordArgs...)
	if code != 0 || !strings.Contains(again, `"place":3`) || !strings.Contains(again, `"kind":"item.request"`) {
		t.Fatalf("ordinary refile %d: %s", code, again)
	}
	noOrdArgs := []string{"--store", path, "--root", root, "--title", "Ordinary request", "--path", "docs/", "--now", "2026-11-18T22:16:00-05:00"}
	code, out = requestRun(t, ordinaryWords, noOrdArgs...)
	if code == 0 || !strings.Contains(out, "request-differs") {
		t.Fatalf("dropping --ordinary on existing ordinary request: %d %s", code, out)
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

func requestRunStreams(t *testing.T, words string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"request"}, args...)...)
	cmd.Stdin = bytes.NewReader([]byte(words))
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if e, ok := err.(*exec.ExitError); ok {
		return e.ExitCode(), stdout.String(), stderr.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, stdout.String(), stderr.String()
}

func TestRequestDesignPathNotice(t *testing.T) {
	path, _ := placeFixture(t, "a")
	root := scratch(t, "request-notice-root")
	if err := os.MkdirAll(filepath.Join(root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, wf := range []string{"planned-build@v1.json", "scouted-design@v1.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "config", "tillr", "workflows", wf))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "config/tillr/workflows", wf), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name       string
		seat       string
		paths      []string
		wantNotice bool
		wantNamed  []string
		wantAbsent []string
	}{
		{
			name:       "build-docs-dir",
			paths:      []string{"docs/"},
			wantNotice: true,
			wantNamed:  []string{"docs/"},
		},
		{
			name:       "build-docs-design-dir",
			seat:       "build",
			paths:      []string{"docs/design/"},
			wantNotice: true,
			wantNamed:  []string{"docs/design/"},
		},
		{
			name:       "build-docs-design-file",
			paths:      []string{"docs/design/example.md"},
			wantNotice: true,
			wantNamed:  []string{"docs/design/example.md"},
		},
		{
			name:       "build-design-named-md",
			paths:      []string{"docs/pi/merge-queue-design.md"},
			wantNotice: true,
			wantNamed:  []string{"docs/pi/merge-queue-design.md"},
		},
		{
			name:       "build-design-basename-md",
			paths:      []string{"docs/pi/patch-branches/design.md"},
			wantNotice: true,
			wantNamed:  []string{"docs/pi/patch-branches/design.md"},
		},
		{
			name:       "build-mixed-paths",
			paths:      []string{"tools/tillr-item/", "docs/design/"},
			wantNotice: true,
			wantNamed:  []string{"docs/design/"},
			wantAbsent: []string{"tools/tillr-item/"},
		},
		{
			name:       "build-code-only",
			paths:      []string{"tools/tillr-item/"},
			wantNotice: false,
		},
		{
			name:       "build-go-file-with-design-in-name",
			paths:      []string{"tools/tillr-item/item/design_without_item.go"},
			wantNotice: false,
		},
		{
			name:       "build-non-design-doc",
			paths:      []string{"docs/tillr/roadmap.md"},
			wantNotice: false,
		},
		{
			name:       "design-seat-docs-design",
			seat:       "design",
			paths:      []string{"docs/design/"},
			wantNotice: false,
		},
		{
			name:       "design-seat-docs-dir",
			seat:       "design",
			paths:      []string{"docs/"},
			wantNotice: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--store", path, "--root", root, "--title", "Notice " + tc.name, "--now", "2026-11-20T10:00:00-05:00"}
			if tc.seat != "" {
				args = append(args, "--seat", tc.seat)
			}
			for _, p := range tc.paths {
				args = append(args, "--path", p)
			}
			code, stdout, stderr := requestRunStreams(t, "Request words for "+tc.name+".\n", args...)
			if code != 0 {
				t.Fatalf("exit %d: stdout=%s stderr=%s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, `"kind":"item.request"`) {
				t.Fatalf("stdout missing item.request envelope: %s", stdout)
			}
			if tc.wantNotice {
				if !strings.Contains(stderr, "notice:") || !strings.Contains(stderr, "design") {
					t.Fatalf("expected notice on stderr, got %q", stderr)
				}
				for _, want := range tc.wantNamed {
					if !strings.Contains(stderr, want) {
						t.Errorf("stderr %q missing path %q", stderr, want)
					}
				}
				for _, absent := range tc.wantAbsent {
					if strings.Contains(stderr, absent) {
						t.Errorf("stderr %q unexpectedly named non-design path %q", stderr, absent)
					}
				}
			} else if strings.Contains(stderr, "notice:") || strings.Contains(stderr, "tillr-item:") {
				t.Fatalf("unexpected notice on stderr for %s: %q", tc.name, stderr)
			}
		})
	}
}
