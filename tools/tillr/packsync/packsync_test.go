package packsync

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/packdocs"
	"github.com/mschulkind-oss/tillr/packmirror"
)

func TestCaseBuildOutputs(t *testing.T) {
	var out bytes.Buffer
	if err := CaseBuildOutputs(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok   a Python bytecode cache is a build output") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCaseCopyBuildOutputs(t *testing.T) {
	var out bytes.Buffer
	if err := CaseCopyBuildOutputs(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok   a bytecode cache on the pack side is ignored") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCaseDocsTransform(t *testing.T) {
	var out bytes.Buffer
	if err := CaseDocsTransform(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok   the documentation transform re-resolves links") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCaseDocsDeclaration(t *testing.T) {
	root := findRepoRoot()
	var out bytes.Buffer
	if err := CaseDocsDeclaration(root, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok   the index declares exactly its `shipped` rows") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCaseDocsStaleRule(t *testing.T) {
	var out bytes.Buffer
	if err := CaseDocsStaleRule(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok   a delivered source changed or deleted at HEAD reads as stale") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCaseLaunchers(t *testing.T) {
	root := findRepoRoot()
	var out bytes.Buffer
	if err := CaseLaunchers(root, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ok   the launchers are one body") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestCaseTreeBytesModesAndCandidate(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")

	for _, mod := range Modules {
		modDir := filepath.Join(repo, "tools", mod)
		if err := os.MkdirAll(modDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(modDir, "README.md"), []byte("[doc](../../docs/a.md)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(modDir, "bin.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", ".")

	var syncOut, syncErr bytes.Buffer
	if code := packmirror.Main([]string{"--sync", repo}, &syncOut, &syncErr); code != 0 {
		t.Fatalf("packmirror.Main exit = %d: %s", code, syncErr.String())
	}

	var out bytes.Buffer
	if err := CaseTreeMatchesSource(repo, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseBytes(repo, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseModes(repo, false, &out); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := CaseTreeMatchesSource(repo, true, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseBytes(repo, true, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseModes(repo, true, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseLoopAdapter(repo, true, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseDocsDelivered(repo, true, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "n/a  ") != 5 {
		t.Fatalf("expected 5 n/a lines in candidate mode, got:\n%s", out.String())
	}
}

func TestCaseLoopAdapterAndDocsDeliveredAndFresh(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")

	loopDir := filepath.Join(repo, "tools", "tillr-loop")
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, "go.mod"), []byte("module example.com/tillr-loop\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs", "design"), 0o755); err != nil {
		t.Fatal(err)
	}
	indexContent := "# Status\n\n| Doc | Pack |\n| :--- | :--- |\n| [A](../design/a.md) | shipped |\n"
	if err := os.WriteFile(filepath.Join(repo, packdocs.Index), []byte(indexContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "docs", "design", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "init")

	var buf, errBuf bytes.Buffer
	if code := packmirror.Sync(repo, []string{packmirror.LoopModule}, &buf, &errBuf); code != 0 {
		t.Fatalf("packmirror.Sync = %d: %s", code, errBuf.String())
	}
	if code := packdocs.Sync(repo, "HEAD", &buf, &errBuf); code != 0 {
		t.Fatalf("packdocs.Sync = %d: %s", code, errBuf.String())
	}

	var out bytes.Buffer
	if err := CaseLoopAdapter(repo, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseDocsDelivered(repo, false, &out); err != nil {
		t.Fatal(err)
	}
	if err := CaseDocsFresh(repo, &out); err != nil {
		t.Fatal(err)
	}

	// Modify a.md at HEAD and verify CaseDocsFresh fails.
	if err := os.WriteFile(filepath.Join(repo, "docs", "design", "a.md"), []byte("# A\n\nupdated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "docs/design/a.md")
	runGit("commit", "-m", "update a")
	if err := CaseDocsFresh(repo, &out); err == nil || !strings.Contains(err.Error(), "changed at HEAD since the snapshot") {
		t.Fatalf("expected CaseDocsFresh to fail on stale snapshot, got %v", err)
	}
}

func TestCaseWorkflows(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	wfDir := filepath.Join(repo, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "planned-build@v1.json"), []byte(`{"name":"planned-build","version":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")

	var out, errBuf bytes.Buffer
	if err := CaseWorkflows(repo, true, &out); err != nil {
		t.Fatalf("candidate mode failed: %v", err)
	}
	if err := CaseWorkflows(repo, false, &out); err == nil || !strings.Contains(err.Error(), "missing=[planned-build@v1.json]") {
		t.Fatalf("expected missing workflow error, got %v", err)
	}
	if code := packmirror.Sync(repo, []string{packmirror.WorkflowsMount}, &out, &errBuf); code != 0 {
		t.Fatalf("packmirror.Sync = %d: %s", code, errBuf.String())
	}
	out.Reset()
	if err := CaseWorkflows(repo, false, &out); err != nil {
		t.Fatalf("CaseWorkflows after sync failed: %v", err)
	}
}
