package main_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var wrapperBin string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns before TestMain exits, so cleanup also runs after setup or test failure.
func runTests(m *testing.M) int {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	dir, err := os.MkdirTemp(root, "tillr-cli-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(dir)
	wrapperBin = filepath.Join(dir, "tillr")
	build := exec.Command("go", "build", "-o", wrapperBin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}
	return m.Run()
}

// nouns is the dispatch set under test, spelled here rather than imported so a wrapper
// that narrowed or widened it disagrees with this list instead of agreeing with itself.
var nouns = []string{"store", "item", "land", "drain", "run", "gate", "view", "watch"}

// stubScript is one fake tillr-<noun>. It records that it ran and the argv it was handed,
// copies stdin to stdout, writes one line to stderr, and exits with the code the test
// chose -- so a wrapper that reordered, dropped, added or translated anything leaves a
// record of it, and a wrapper that refused leaves neither record behind.
const stubScript = `#!/bin/sh
printf 'ran' > "$TILLR_STUB_DIR/%[1]s.ran"
: > "$TILLR_STUB_DIR/%[1]s.argv"
for arg in "$@"; do printf '<%%s>\n' "$arg" >> "$TILLR_STUB_DIR/%[1]s.argv"; done
cat
printf '%[1]s on stderr\n' >&2
exit %[2]d
`

// stub is one fake binary's noun and the exit code it answers with.
type stub struct {
	noun string
	exit int
}

// stubDir writes one stub per spec into a fresh directory and answers its path. It is the
// whole of PATH's front, so nothing but these binaries answers a noun.
func stubDir(t *testing.T, specs ...stub) string {
	t.Helper()
	dir := t.TempDir()
	for _, spec := range specs {
		script := filepath.Join(dir, "tillr-"+spec.noun)
		body := fmt.Sprintf(stubScript, spec.noun, spec.exit)
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatalf("writing stub %s: %v", spec.noun, err)
		}
	}
	return dir
}

// runWrapper runs the built wrapper with args, the stub directory in front of PATH, and
// stdin fed from the argument. It answers the exit code and the two streams.
func runWrapper(t *testing.T, dir, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(wrapperBin, args...)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TILLR_STUB_DIR="+dir,
	)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running wrapper with %v: %v", args, err)
		}
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	return 0, stdout.String(), stderr.String()
}

// recorded answers whether the given noun's stub ran, and the argv it saw if it did.
func recorded(t *testing.T, dir, noun string) (bool, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, noun+".argv"))
	if os.IsNotExist(err) {
		return false, ""
	}
	if err != nil {
		t.Fatalf("reading %s's argv record: %v", noun, err)
	}
	return true, string(raw)
}

// expectArgv is the argv record of a stub handed exactly these arguments, one per line in
// angle brackets, so an empty argument is visible instead of vanishing into a blank line.
func expectArgv(args ...string) string {
	var want strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&want, "<%s>\n", arg)
	}
	return want.String()
}

// TestDispatchPassesArgvStdioAndExitCode is the pass-through pin: every one of the
// nouns reaches its own binary, gets the arguments after the noun byte for byte, gets
// this wrapper's stdin and stdout as its own, and answers the caller with its own exit
// code -- including the non-zero ones, which are the child's outcome and not a wrapper
// verdict about it.
func TestDispatchPassesArgvStdioAndExitCode(t *testing.T) {
	specs := []stub{
		{"store", 0},
		{"item", 1},
		{"land", 2},
		{"drain", 3},
		{"run", 4},
		{"gate", 6},
		{"view", 7},
		{"watch", 2},
	}
	for _, spec := range specs {
		t.Run(spec.noun, func(t *testing.T) {
			// A directory per subtest, so "no other noun ran" is a claim about this
			// invocation rather than about the ones before it.
			dir := stubDir(t, specs...)
			code, stdout, stderr := runWrapper(t, dir, "piped through\n", spec.noun, "alpha", "beta gamma")
			if code != spec.exit {
				t.Errorf("exit code = %d, want the child's %d", code, spec.exit)
			}
			if stdout != "piped through\n" {
				t.Errorf("stdout = %q, want the stdin the stub copied through", stdout)
			}
			if stderr != spec.noun+" on stderr\n" {
				t.Errorf("stderr = %q, want only the stub's line", stderr)
			}
			ran, argv := recorded(t, dir, spec.noun)
			if !ran {
				t.Fatalf("tillr-%s did not run", spec.noun)
			}
			if want := expectArgv("alpha", "beta gamma"); argv != want {
				t.Errorf("argv = %q, want %q", argv, want)
			}
			for _, other := range nouns {
				if other == spec.noun {
					continue
				}
				if ran, _ := recorded(t, dir, other); ran {
					t.Errorf("tillr %s also ran tillr-%s", spec.noun, other)
				}
			}
		})
	}
}

// TestWrapperReadsNothingAfterTheNoun is the "no behavior of its own" pin: an argument
// that any wrapper tempted to be helpful would answer itself -- a help flag, a version
// flag, a bare word, an empty string, a flag-shaped value -- arrives at the child
// unchanged, and the wrapper prints none of its own output.
func TestWrapperReadsNothingAfterTheNoun(t *testing.T) {
	dir := stubDir(t, stub{"store", 1})
	handed := []string{"--db=/nonexistent", "--help", "-h", "--version", "help", "", "two words", "--", "-x", "-", "json"}
	code, stdout, stderr := runWrapper(t, dir, "stdin payload", append([]string{"store"}, handed...)...)
	if code != 1 {
		t.Errorf("exit code = %d, want the child's 1", code)
	}
	if stdout != "stdin payload" {
		t.Errorf("stdout = %q, want the stdin the stub copied through", stdout)
	}
	if stderr != "store on stderr\n" {
		t.Errorf("stderr = %q, want only the stub's line: the wrapper answered an argument itself", stderr)
	}
	ran, argv := recorded(t, dir, "store")
	if !ran {
		t.Fatal("tillr-store did not run")
	}
	if want := expectArgv(handed...); argv != want {
		t.Errorf("argv = %q, want %q", argv, want)
	}
}

// TestUnknownNounIsRefusedNamingTheNouns pins the refusal: an unknown noun exits
// 2, names the nouns, writes nothing to stdout, and starts no process at all -- even
// though a binary for that very name is on PATH.
func TestUnknownNounIsRefusedNamingTheNouns(t *testing.T) {
	dir := stubDir(t, stub{"bogus", 0})
	code, stdout, stderr := runWrapper(t, dir, "", "bogus", "--anything")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	for _, noun := range nouns {
		if !strings.Contains(stderr, noun) {
			t.Errorf("stderr = %q, want it to name %q", stderr, noun)
		}
	}
	if ran, _ := recorded(t, dir, "bogus"); ran {
		t.Error("tillr-bogus ran: an unknown noun was dispatched instead of refused")
	}
	if !strings.Contains(stderr, `"bogus"`) {
		t.Errorf("stderr = %q, want it to name the refused noun", stderr)
	}
}

// TestNoNounIsRefusedNamingTheNouns pins the empty invocation: with nothing to dispatch,
// the wrapper explains the set instead of guessing or exiting silently.
func TestNoNounIsRefusedNamingTheNouns(t *testing.T) {
	dir := stubDir(t, stub{"store", 0})
	code, stdout, stderr := runWrapper(t, dir, "")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
	for _, noun := range nouns {
		if !strings.Contains(stderr, noun) {
			t.Errorf("stderr = %q, want it to name %q", stderr, noun)
		}
	}
	if ran, _ := recorded(t, dir, "store"); ran {
		t.Error("tillr-store ran on an invocation that named no noun")
	}
}

// TestMissingBinaryIsAnExternalFailure pins the one failure that is neither the caller's
// mistake nor the noun's outcome: a real noun whose binary is not installed exits 4 and
// says which binary is missing, rather than reporting a usage error or a negative answer
// the noun never gave.
func TestMissingBinaryIsAnExternalFailure(t *testing.T) {
	dir := t.TempDir() // empty: nothing on this PATH answers any noun
	cmd := exec.Command(wrapperBin, "watch", "status")
	cmd.Env = append(os.Environ(), "PATH="+dir, "TILLR_STUB_DIR="+dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("running the wrapper without tillr-watch: %v", err)
	}
	if got := exit.ExitCode(); got != 4 {
		t.Errorf("exit code = %d, want 4", got)
	}
	if stdout.String() != "" {
		t.Errorf("stdout = %q, want nothing", stdout.String())
	}
	if !strings.Contains(stderr.String(), "tillr-watch is not on PATH") {
		t.Errorf("stderr = %q, want it to name the missing binary and PATH", stderr.String())
	}
}

// TestHelpNamesTheNouns pins the wrapper's own surface (docs/design/tillr-distribution.md
// Section 3 makes `<bin> --help` layer 1): `tillr --help` names the
// dispatchable nouns and exits 0, and starts none of them. The wrapper still answers nothing that
// comes after a noun -- TestWrapperReadsNothingAfterTheNoun is the other half of that rule.
func TestHelpNamesTheNouns(t *testing.T) {
	dir := stubDir(t, stub{"store", 4}, stub{"gate", 4})
	code, stdout, stderr := runWrapper(t, dir, "", "--help")
	if code != 0 {
		t.Fatalf("--help exited %d, want 0: %s", code, stderr)
	}
	for _, noun := range nouns {
		if !strings.Contains(stdout, noun) {
			t.Errorf("--help does not name the noun %q: %q", noun, stdout)
		}
	}
	for _, noun := range []string{"store", "gate"} {
		if ran, _ := recorded(t, dir, noun); ran {
			t.Errorf("tillr --help ran tillr-%s", noun)
		}
	}
}
