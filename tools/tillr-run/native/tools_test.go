package native

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testFenceEnv struct {
	root         string
	workDir      string
	artifactDir  string
	artifactsDir string
	durableDir   string
	progressFile string
	homeDir      string
	outsideDir   string
}

func newTestFenceEnv(t *testing.T, fenceLines []string) testFenceEnv {
	t.Helper()
	root := t.TempDir()
	env := testFenceEnv{
		root:         root,
		workDir:      filepath.Join(root, "lane", "worktree"),
		artifactDir:  filepath.Join(root, "lane"),
		artifactsDir: filepath.Join(root, "loop", "run-1", "artifacts"),
		durableDir:   filepath.Join(root, "lane", "durable", "run-1"),
		progressFile: filepath.Join(root, "lane", "durable", "run-1", "progress"),
		homeDir:      filepath.Join(root, "home"),
		outsideDir:   filepath.Join(root, "outside"),
	}
	for _, d := range []string{env.workDir, env.artifactDir, env.artifactsDir, env.durableDir, env.homeDir, env.outsideDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if fenceLines != nil {
		body := strings.Join(fenceLines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(env.artifactDir, "fence.txt"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	denyDir := filepath.Join(env.workDir, "config", "tillr")
	if err := os.MkdirAll(denyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	denyBody := `{"entries":[{"path":".tillr","reason":"store belongs to the loop"},{"path":".forks","reason":"fork checkouts are not trunk deliverables"},{"content":"item-declaration","reason":"item declarations are protected"}]}`
	if err := os.WriteFile(filepath.Join(denyDir, "deny-list.json"), []byte(denyBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return env
}

func (e testFenceEnv) policy(readOnly, planQuickWay bool) FencePolicy {
	return FencePolicy{
		WorkDir:      e.workDir,
		ArtifactDir:  e.artifactDir,
		ArtifactsDir: e.artifactsDir,
		DurableDir:   e.durableDir,
		ProgressFile: e.progressFile,
		ReadOnly:     readOnly,
		PlanQuickWay: planQuickWay,
		HomeDir:      e.homeDir,
	}
}

func mustCall(t *testing.T, ts *Toolset, name string, args any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return ts.Execute(context.Background(), ContentBlock{
		Type:  BlockToolUse,
		ID:    "call-1",
		Name:  name,
		Input: raw,
	})
}

func TestWriteAndEditFileFenceAndReadOnly(t *testing.T) {
	env := newTestFenceEnv(t, []string{"tools/tillr-run/native", "docs/notes.md"})

	// Create symlink escapes inside the fenced directory.
	fencedDir := filepath.Join(env.workDir, "tools", "tillr-run", "native")
	if err := os.MkdirAll(fencedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(env.outsideDir, "escaped.txt")
	if err := os.WriteFile(outsideFile, []byte("original-outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(fencedDir, "link-existing.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.outsideDir, "dangling-new.txt"), filepath.Join(fencedDir, "link-dangling.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(env.outsideDir, filepath.Join(fencedDir, "link-dir")); err != nil {
		t.Fatal(err)
	}

	// Pre-populate an editable file inside fence and one outside fence.
	insideExisting := filepath.Join(fencedDir, "existing.go")
	if err := os.WriteFile(insideExisting, []byte("package native\nconst v = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outsideFenceFile := filepath.Join(env.workDir, "README.md")
	if err := os.WriteFile(outsideFenceFile, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dotGitDir := filepath.Join(env.workDir, ".git")
	if err := os.MkdirAll(dotGitDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		readOnly    bool
		path        string
		wantAllowed bool
		wantErrSub  string
		checkAbsent string // file that must not exist on disk after refused write
	}{
		{
			name:        "fenced workDir subdirectory allowed",
			path:        "tools/tillr-run/native/new.go",
			wantAllowed: true,
		},
		{
			name:        "fenced exact file allowed",
			path:        "docs/notes.md",
			wantAllowed: true,
		},
		{
			name:        "artifacts dir allowed on write run",
			path:        filepath.Join(env.artifactsDir, "summary.md"),
			wantAllowed: true,
		},
		{
			name:        "scratch dir allowed on write run",
			path:        filepath.Join(env.durableDir, "scratch.txt"),
			wantAllowed: true,
		},
		{
			name:        "artifacts dir allowed on read_only run",
			readOnly:    true,
			path:        filepath.Join(env.artifactsDir, "report.json"),
			wantAllowed: true,
		},
		{
			name:        "scratch dir allowed on read_only run",
			readOnly:    true,
			path:        filepath.Join(env.durableDir, "notes.txt"),
			wantAllowed: true,
		},
		{
			name:        "read_only refuses fenced workDir write",
			readOnly:    true,
			path:        "tools/tillr-run/native/blocked.go",
			wantAllowed: false,
			wantErrSub:  "read_only",
			checkAbsent: filepath.Join(fencedDir, "blocked.go"),
		},
		{
			name:        "out-of-fence workDir write refused",
			path:        "tools/other/bad.go",
			wantAllowed: false,
			wantErrSub:  "outside lane fence",
			checkAbsent: filepath.Join(env.workDir, "tools", "other", "bad.go"),
		},
		{
			name:        "dot-git write refused even without fence match",
			path:        ".git/config",
			wantAllowed: false,
			wantErrSub:  ".git",
			checkAbsent: filepath.Join(dotGitDir, "config"),
		},
		{
			name:        "deny-list .tillr refused",
			path:        ".tillr/state.db",
			wantAllowed: false,
			wantErrSub:  "deny-list",
			checkAbsent: filepath.Join(env.workDir, ".tillr", "state.db"),
		},
		{
			name:        "deny-list .forks refused",
			path:        ".forks/foo/bar.txt",
			wantAllowed: false,
			wantErrSub:  "deny-list",
			checkAbsent: filepath.Join(env.workDir, ".forks", "foo", "bar.txt"),
		},
		{
			name:        "outside root absolute path refused",
			path:        filepath.Join(env.outsideDir, "nope.txt"),
			wantAllowed: false,
			wantErrSub:  "outside permitted",
			checkAbsent: filepath.Join(env.outsideDir, "nope.txt"),
		},
		{
			name:        "symlink to existing outside file refused",
			path:        "tools/tillr-run/native/link-existing.txt",
			wantAllowed: false,
			wantErrSub:  "outside permitted",
		},
		{
			name:        "dangling symlink to outside file refused before creation",
			path:        "tools/tillr-run/native/link-dangling.txt",
			wantAllowed: false,
			wantErrSub:  "outside permitted",
			checkAbsent: filepath.Join(env.outsideDir, "dangling-new.txt"),
		},
		{
			name:        "symlinked directory escape refused before creation",
			path:        "tools/tillr-run/native/link-dir/nested/escape.txt",
			wantAllowed: false,
			wantErrSub:  "outside permitted",
			checkAbsent: filepath.Join(env.outsideDir, "nested", "escape.txt"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := NewToolset(env.policy(tc.readOnly, false))
			out, isErr := mustCall(t, ts, "write_file", writeFileArgs{
				Path:    tc.path,
				Content: "hello\n",
			})
			if tc.wantAllowed && isErr {
				t.Fatalf("write_file(%q) unexpectedly failed: %s", tc.path, out)
			}
			if !tc.wantAllowed && !isErr {
				t.Fatalf("write_file(%q) unexpectedly succeeded: %s", tc.path, out)
			}
			if !tc.wantAllowed && tc.wantErrSub != "" && !strings.Contains(out, tc.wantErrSub) {
				t.Errorf("error = %q, want substring %q", out, tc.wantErrSub)
			}
			if tc.checkAbsent != "" {
				if _, err := os.Stat(tc.checkAbsent); !os.IsNotExist(err) {
					t.Errorf("refused write created %s on disk", tc.checkAbsent)
				}
			}
		})
	}

	// Verify symlinked outside file was never modified.
	gotOutside, err := os.ReadFile(outsideFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotOutside) != "original-outside\n" {
		t.Fatalf("outside file mutated through symlink: %q", string(gotOutside))
	}

	// Verify edit_file allows fenced edit and refuses out-of-fence & read_only edits before touching disk.
	tsWrite := NewToolset(env.policy(false, false))
	out, isErr := mustCall(t, tsWrite, "edit_file", editFileArgs{
		Path:  "tools/tillr-run/native/existing.go",
		Edits: []textEdit{{OldText: "const v = 1", NewText: "const v = 2"}},
	})
	if isErr {
		t.Fatalf("fenced edit_file failed: %s", out)
	}
	gotInside, _ := os.ReadFile(insideExisting)
	if !strings.Contains(string(gotInside), "const v = 2") {
		t.Fatalf("edit_file did not update file: %q", string(gotInside))
	}

	out, isErr = mustCall(t, tsWrite, "edit_file", editFileArgs{
		Path:  "README.md",
		Edits: []textEdit{{OldText: "untouched", NewText: "mutated"}},
	})
	if !isErr || !strings.Contains(out, "outside lane fence") {
		t.Fatalf("out-of-fence edit_file = (%q, %v), want fence error", out, isErr)
	}
	gotReadme, _ := os.ReadFile(outsideFenceFile)
	if string(gotReadme) != "untouched\n" {
		t.Fatalf("out-of-fence file mutated: %q", string(gotReadme))
	}

	tsRO := NewToolset(env.policy(true, false))
	out, isErr = mustCall(t, tsRO, "edit_file", editFileArgs{
		Path:  "tools/tillr-run/native/existing.go",
		Edits: []textEdit{{OldText: "const v = 2", NewText: "const v = 3"}},
	})
	if !isErr || !strings.Contains(out, "read_only") {
		t.Fatalf("read_only edit_file = (%q, %v), want read_only error", out, isErr)
	}
}

func TestDenyListBlocksEvenWhenInsideFence(t *testing.T) {
	// Even if fence.txt is empty (unrestricted worktree), deny-list.json must still refuse .tillr and .forks.
	env := newTestFenceEnv(t, nil)
	ts := NewToolset(env.policy(false, false))
	for _, p := range []string{".tillr/state.db", ".forks/repo/file.go"} {
		out, isErr := mustCall(t, ts, "write_file", writeFileArgs{Path: p, Content: "x"})
		if !isErr || !strings.Contains(out, "deny-list") {
			t.Errorf("write_file(%q) = (%q, %v), want deny-list refusal", p, out, isErr)
		}
	}
}

func TestPlanQuickWayDoItNowUnlock(t *testing.T) {
	env := newTestFenceEnv(t, []string{"pkg/allowed"})
	ts := NewToolset(env.policy(true, true))

	// Before do-it-now exists, worktree writes are refused.
	target := "pkg/allowed/file.go"
	out, isErr := mustCall(t, ts, "write_file", writeFileArgs{Path: target, Content: "package allowed\n"})
	if !isErr || !strings.Contains(out, "do-it-now") {
		t.Fatalf("before unlock: write_file = (%q, %v), want do-it-now refusal", out, isErr)
	}
	if _, err := os.Stat(filepath.Join(env.workDir, target)); !os.IsNotExist(err) {
		t.Fatalf("refused write created %s before do-it-now", target)
	}

	// Writing <artifacts>/do-it-now is permitted on read_only plan seat.
	marker := filepath.Join(env.artifactsDir, "do-it-now")
	out, isErr = mustCall(t, ts, "write_file", writeFileArgs{Path: marker, Content: "quick way\n"})
	if isErr {
		t.Fatalf("writing do-it-now marker failed: %s", out)
	}

	// Now fenced worktree writes succeed...
	out, isErr = mustCall(t, ts, "write_file", writeFileArgs{Path: target, Content: "package allowed\n"})
	if isErr {
		t.Fatalf("after unlock: fenced write_file failed: %s", out)
	}

	// ...while out-of-fence writes are still refused!
	out, isErr = mustCall(t, ts, "write_file", writeFileArgs{Path: "pkg/other/file.go", Content: "package other\n"})
	if !isErr || !strings.Contains(out, "outside lane fence") {
		t.Fatalf("after unlock: out-of-fence write_file = (%q, %v), want fence error", out, isErr)
	}
}

func TestSecretPathReadRefusals(t *testing.T) {
	env := newTestFenceEnv(t, nil)
	ts := NewToolset(env.policy(true, false))

	secretPaths := []string{
		filepath.Join(env.homeDir, ".config", "yolo-agent-env", "tillr-run.sh"),
		filepath.Join(env.homeDir, ".config", "tillr", "credentials.json"),
		filepath.Join(env.homeDir, ".aws", "credentials"),
		filepath.Join(env.homeDir, ".ssh", "id_ed25519"),
		filepath.Join(env.homeDir, ".claude", "credentials.json"),
		filepath.Join(env.homeDir, ".pi", "agent", "auth.json"),
		filepath.Join(env.homeDir, ".pi", "agent", "auth-tokens", "key"),
	}
	for _, sp := range secretPaths {
		if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sp, []byte("SECRET_VALUE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Symlink inside workDir pointing to a secret file.
	symSecret := filepath.Join(env.workDir, "innocent.txt")
	if err := os.Symlink(filepath.Join(env.homeDir, ".aws", "credentials"), symSecret); err != nil {
		t.Fatal(err)
	}

	cases := append([]string{}, secretPaths...)
	cases = append(cases, "innocent.txt", "/proc/self/environ", "/proc/1/environ")

	for _, path := range cases {
		t.Run("read_file:"+path, func(t *testing.T) {
			out, isErr := mustCall(t, ts, "read_file", readFileArgs{Path: path})
			if !isErr || !strings.Contains(out, "read refused") {
				t.Errorf("read_file(%q) = (%q, %v), want read refused", path, out, isErr)
			}
		})
		t.Run("list_files:"+path, func(t *testing.T) {
			out, isErr := mustCall(t, ts, "list_files", listFilesArgs{Path: path})
			if !isErr || !strings.Contains(out, "read refused") {
				t.Errorf("list_files(%q) = (%q, %v), want read refused", path, out, isErr)
			}
		})
		t.Run("search_files:"+path, func(t *testing.T) {
			out, isErr := mustCall(t, ts, "search_files", searchFilesArgs{Pattern: "SECRET", Path: path})
			if !isErr || !strings.Contains(out, "read refused") {
				t.Errorf("search_files(%q) = (%q, %v), want read refused", path, out, isErr)
			}
		})
	}
}

func TestReadListSearchAndEditTools(t *testing.T) {
	env := newTestFenceEnv(t, nil)
	ts := NewToolset(env.policy(false, false))

	// 1. read_file: pagination and binary refusal.
	var lines []string
	for i := 1; i <= 300; i++ {
		lines = append(lines, fmt.Sprintf("line-%03d", i))
	}
	bigPath := filepath.Join(env.workDir, "big.txt")
	if err := os.WriteFile(bigPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr := mustCall(t, ts, "read_file", readFileArgs{Path: "big.txt", Offset: 2, Limit: 3})
	if isErr {
		t.Fatalf("read_file failed: %s", out)
	}
	if !strings.Contains(out, "line-002\nline-003\nline-004") || !strings.Contains(out, "use offset=5") {
		t.Errorf("unexpected paginated read_file output:\n%s", out)
	}

	binPath := filepath.Join(env.workDir, "bin.dat")
	if err := os.WriteFile(binPath, []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr = mustCall(t, ts, "read_file", readFileArgs{Path: "bin.dat"})
	if !isErr || !strings.Contains(out, "binary") {
		t.Errorf("binary read_file = (%q, %v), want binary error", out, isErr)
	}

	// 2. list_files: plain and glob.
	if err := os.MkdirAll(filepath.Join(env.workDir, "pkg", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.workDir, "pkg", "a.go"), []byte("package pkg\nfunc Alpha() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.workDir, "pkg", "sub", "b.go"), []byte("package sub\nfunc Beta() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr = mustCall(t, ts, "list_files", listFilesArgs{Path: "pkg", Glob: "**/*.go"})
	if isErr {
		t.Fatalf("list_files glob failed: %s", out)
	}
	if !strings.Contains(out, "a.go") || !strings.Contains(out, "sub/b.go") {
		t.Errorf("list_files glob = %q, want a.go and sub/b.go", out)
	}

	// 3. search_files: regex, glob, binary skipped.
	out, isErr = mustCall(t, ts, "search_files", searchFilesArgs{Pattern: "func (Alpha|Beta)", Path: "pkg", Glob: "*.go"})
	if isErr {
		t.Fatalf("search_files failed: %s", out)
	}
	if !strings.Contains(out, "a.go:2:func Alpha() {}") || !strings.Contains(out, "sub/b.go:2:func Beta() {}") {
		t.Errorf("search_files = %q", out)
	}

	// 4. edit_file validation: duplicate and overlapping edits rejected cleanly.
	dupPath := filepath.Join(env.workDir, "dup.txt")
	if err := os.WriteFile(dupPath, []byte("foo\nbar\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr = mustCall(t, ts, "edit_file", editFileArgs{
		Path:  "dup.txt",
		Edits: []textEdit{{OldText: "foo", NewText: "baz"}},
	})
	if !isErr || !strings.Contains(out, "matches 2 locations") {
		t.Errorf("duplicate edit_file = (%q, %v), want uniqueness error", out, isErr)
	}

	overlapPath := filepath.Join(env.workDir, "overlap.txt")
	if err := os.WriteFile(overlapPath, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, isErr = mustCall(t, ts, "edit_file", editFileArgs{
		Path: "overlap.txt",
		Edits: []textEdit{
			{OldText: "abcd", NewText: "X"},
			{OldText: "cdef", NewText: "Y"},
		},
	})
	if !isErr || !strings.Contains(out, "overlap") {
		t.Errorf("overlapping edit_file = (%q, %v), want overlap error", out, isErr)
	}
}

func TestReportProgressAppendsLines(t *testing.T) {
	env := newTestFenceEnv(t, nil)
	ts := NewToolset(env.policy(true, false))

	out, isErr := mustCall(t, ts, "report_progress", reportProgressArgs{Criterion: "c1"})
	if isErr {
		t.Fatalf("first report_progress failed: %s", out)
	}
	out, isErr = mustCall(t, ts, "report_progress", reportProgressArgs{Criteria: []string{"c2", "c3"}})
	if isErr {
		t.Fatalf("second report_progress failed: %s", out)
	}

	raw, err := os.ReadFile(env.progressFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "c1\nc2\nc3\n" {
		t.Fatalf("progress file = %q, want %q", string(raw), "c1\nc2\nc3\n")
	}

	// Reject multiline criterion ID.
	out, isErr = mustCall(t, ts, "report_progress", reportProgressArgs{Criterion: "c4\nc5"})
	if !isErr || !strings.Contains(out, "single line") {
		t.Fatalf("multiline report_progress = (%q, %v), want single-line error", out, isErr)
	}
}
