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
	args := []string{"--store", path, "--root", root, "--title", "Remove the index cache", "--now", "2026-11-18T22:15:00-05:00"}
	code, out := requestRun(t, words, args...)
	if code != 0 {
		t.Fatalf("%d: %s", code, out)
	}
	for _, want := range []string{`"kind":"item.request"`, `"item_id":"item-request-`, `"place":2`, `"workflow":"planned-build@v1"`, `"seat":"build"`} {
		if !strings.Contains(out, want) {
			t.Errorf("answer lacks %s: %s", want, out)
		}
	}
	// Passing --path is accepted and ignored with a notice.
	code, out = requestRun(t, "Request with deprecated path.\n", "--store", path, "--root", root, "--title", "Deprecated path", "--path", "docs/", "--now", "2026-11-18T22:15:30-05:00")
	if code != 0 || !strings.Contains(out, "notice: --path is deprecated") {
		t.Fatalf("request with --path: %d %s", code, out)
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
	ordArgs := []string{"--store", path, "--root", root, "--title", "Ordinary request", "--ordinary", "--now", "2026-11-18T22:16:00-05:00"}
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
	if code != 0 || !strings.Contains(again, `"place":4`) || !strings.Contains(again, `"kind":"item.request"`) {
		t.Fatalf("ordinary refile %d: %s", code, again)
	}
	noOrdArgs := []string{"--store", path, "--root", root, "--title", "Ordinary request", "--now", "2026-11-18T22:16:00-05:00"}
	code, out = requestRun(t, ordinaryWords, noOrdArgs...)
	if code == 0 || !strings.Contains(out, "request-differs") {
		t.Fatalf("dropping --ordinary on existing ordinary request: %d %s", code, out)
	}
	code, out = requestRun(t, "Something else.\n", "--store", path, "--root", root, "--title", "Wrong workflow", "--workflow", "missing@v1")
	if code == 0 || !strings.Contains(out, "workflow-missing") {
		t.Fatalf("a request naming no workflow file: %d %s", code, out)
	}
	if exitOf(exec.Command(bin, "request", "--help").Run()) != 0 {
		t.Fatal("request --help failed")
	}
}

func TestRequestFaceRefusesAnIdenticalClosedRequest(t *testing.T) {
	path, s := placeFixture(t, "a")
	const words = "Do the small thing.\n"
	args := []string{"--store", path, "--title", "Small thing", "--workflow", "single-worker@v1"}
	if code, out := requestRun(t, words, args...); code != 0 {
		t.Fatalf("initial filing exited %d: %s", code, out)
	}
	var id string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT item_id FROM request_words`).Scan(&id)
	}); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "close", "--store", path, "--item", id, "--reason", "no longer needed", "--by", "owner"); code != 0 {
		t.Fatalf("close exited %d: %s", code, out)
	}
	code, out := requestRun(t, words, args...)
	if code == 0 || !strings.Contains(out, "matched closed item "+id) || !strings.Contains(out, "nothing was queued") {
		t.Fatalf("closed refile exited %d: %s", code, out)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		var count int
		if err := r.QueryRow(`SELECT state, (SELECT count(*) FROM items) FROM items WHERE item_id=?`, id).Scan(&state, &count); err != nil {
			return err
		}
		if state != "closed" || count != 2 {
			t.Errorf("after refusal: state=%q, item count=%d; want closed and 2 items", state, count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tillr item reopen --item "+id+" --reason <text> --by <who>") {
		t.Fatalf("closed refile lacks reopen instructions: %s", out)
	}
	if code, out := run(t, "reopen", "--store", path, "--item", id, "--reason", "work is needed again", "--by", "owner"); code != 0 {
		t.Fatalf("reopen exited %d: %s", code, out)
	}
	if code, out := requestRun(t, words, args...); code != 0 || !strings.Contains(out, `"place":2`) {
		t.Fatalf("reopened refile exited %d: %s", code, out)
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

func TestRequestFaceUsesInstalledWorkflowsWhenRepoHasNone(t *testing.T) {
	path, s := placeFixture(t, "a")
	root := scratch(t, "consumer-repo")
	installRoot, err := filepath.Abs(filepath.Join("..", "..", "yolo-packs", "tillr", "files", "local", "share", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	// Default build workflow (planned-build@v1) works with no config/tillr/workflows in root.
	code, out := requestRun(t, "File without repo workflows.\n", "--store", path, "--root", root, "--title", "Installed default build")
	if code != 0 || !strings.Contains(out, `"workflow":"planned-build@v1"`) {
		t.Fatalf("build request without repo workflows: %d %s", code, out)
	}
	// Default design workflow (scouted-design@v1) works with no config/tillr/workflows in root.
	code, out = requestRun(t, "Design without repo workflows.\n", "--store", path, "--root", root, "--title", "Installed default design", "--seat", "design")
	if code != 0 || !strings.Contains(out, `"workflow":"scouted-design@v1"`) {
		t.Fatalf("design request without repo workflows: %d %s", code, out)
	}
	var storedWorkflow string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT workflow FROM items WHERE title='Installed default build'`).Scan(&storedWorkflow)
	}); err != nil || storedWorkflow != "planned-build@v1" {
		t.Fatalf("stored workflow=%q err=%v", storedWorkflow, err)
	}

	// Operated repository workflow wins over the installed one of the same name.
	wfDir := filepath.Join(root, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Put a workflow with top-level args at planned-build@v1 in the repo; if repo wins,
	// RequestWorkflowCheck must refuse it with workflow-needs-args rather than using the
	// installed argless planned-build@v1.
	if err := os.WriteFile(filepath.Join(wfDir, "planned-build@v1.json"), []byte(`{"name":"planned-build","version":1,"args":{"x":{"type":"strings","most":1}},"stages":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = requestRun(t, "Repo override wins.\n", "--store", path, "--root", root, "--title", "Repo wins")
	if code == 0 || !strings.Contains(out, "workflow-needs-args") {
		t.Fatalf("expected repo workflow to win and be refused with workflow-needs-args: %d %s", code, out)
	}
}
