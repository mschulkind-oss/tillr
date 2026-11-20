package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newCompileTogetherFixture is the 2026-11-20 incident in miniature: the base has a Go
// module whose a.go imports os twice over (one use by f, one by g). The trunk's change
// removes f's use and the import; the candidate adds a new use of os. Each compiles on
// its own base, git merges them without a conflict, and the merged tree does not compile.
func newCompileTogetherFixture(t *testing.T) fixture {
	t.Helper()
	dir := filepath.Join(scratch(t, "repo"), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("cannot create the fixture repository: %v", err)
	}
	git(t, dir, "init", "-b", "master")
	mod := filepath.Join(dir, "mod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mod, "go.mod"), "module example.invalid/mod\n\ngo 1.21\n")
	write(t, filepath.Join(mod, "a.go"), `package mod

import "os"

func First() string {
	return os.Getenv("FIRST")
}

func Pad() int {
	return 1
}

func Pad2() int {
	return 2
}

func Pad3() int {
	return 3
}

func Pad4() int {
	return 4
}

func Pad5() int {
	return 5
}

func Last() int {
	return 6
}
`)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")

	git(t, dir, "checkout", "-b", "candidate")
	src, err := os.ReadFile(filepath.Join(mod, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(mod, "a.go"), strings.Replace(string(src),
		"func Last() int {\n\treturn 6\n}\n",
		"func Last() int {\n\treturn len(os.Environ())\n}\n", 1))
	git(t, dir, "commit", "-am", "candidate uses os again")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")

	git(t, dir, "checkout", "master")
	write(t, filepath.Join(mod, "a.go"), strings.NewReplacer(
		"import \"os\"\n\n", "",
		"\treturn os.Getenv(\"FIRST\")\n", "\treturn \"first\"\n").Replace(string(src)))
	git(t, dir, "commit", "-am", "trunk drops its only use of os")
	return fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree}
}

// TestAMergeThatDoesNotCompileIsRefusedAsAConflict: two changes that merge cleanly as text
// but do not build together land only the first. The second comes back as a squash
// conflict that names the module and the compiler's complaint, and the trunk does not move.
func TestAMergeThatDoesNotCompileIsRefusedAsAConflict(t *testing.T) {
	f := newCompileTogetherFixture(t)
	moved := f.trunk(t)
	_, code, out := f.land(t, "run-1", "pass")
	if code != 1 {
		t.Fatalf("pass exited %d, want 1: %s", code, out)
	}
	got := decodeLanding(t, out)
	if got.Payload.Result != "squash-conflict" {
		t.Fatalf("landing is %s/%s, want squash-conflict", got.Payload.Result, got.Payload.Reason)
	}
	joined := strings.Join(got.Payload.ConflictPaths, "\n")
	if !strings.Contains(joined, "mod") || !strings.Contains(joined, "undefined: os") {
		t.Fatalf("the conflict does not name the module and the compile error: %q", joined)
	}
	if f.trunk(t) != moved {
		t.Fatal("a merge that does not compile moved the trunk")
	}
	if pinned := git(t, f.dir, "rev-parse", "refs/tillr/runs/run-1"); pinned != f.headSHA {
		t.Fatalf("refs/tillr/runs/run-1 is %s, want the head %s", pinned, f.headSHA)
	}
}

// TestAMergeThatCompilesStillLands: the check refuses only a tree that does not build.
func TestAMergeThatCompilesStillLands(t *testing.T) {
	f := newCompileTogetherFixture(t)
	// Put the trunk back on the base and move it by an unrelated file, so the squash
	// differs from the gated tree and the check runs and passes.
	git(t, f.dir, "reset", "--hard", f.baseSHA)
	f.advance(t)
	_, code, out := f.land(t, "run-1", "pass")
	if code != 0 {
		t.Fatalf("pass exited %d: %s", code, out)
	}
	if got := decodeLanding(t, out); got.Payload.Result != "landed" {
		t.Fatalf("landing is %s/%s, want landed", got.Payload.Result, got.Payload.Reason)
	}
}
