// Command tillr is the control surface over the tillr binaries: it resolves
// `tillr <noun> ...` to `tillr-<noun> ...` on PATH and replaces itself with that binary,
// so argv, stdio and the exit code reach the caller untouched (design §11), and hosts
// the built-in `tillr notify serve` local wake-hint relay (D-SN5).
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/mschulkind-oss/tillr/record"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// nouns is the dispatch set, in the order the usage line prints it. A noun
// has to be added here to be reachable; the wrapper never forwards unknown nouns.
var nouns = []string{"store", "item", "land", "drain", "run", "gate", "view", "watch", "notify"}

// dispatch is one invocation, resolved: the binary that answers it and the arguments it
// is handed, unread.
type dispatch struct {
	binary string   // the tillr-<noun> path PATH resolved
	argv   []string // every argument after the noun, in the order they arrived
}

// run resolves an invocation and becomes the binary that answers it. It returns only
// when nothing was started, so the code it answers with is the wrapper's own failure and
// never stands in for a child's outcome.
func run(args []string) int {
	// The wrapper's own surface, and the only thing it answers itself: the nouns it can
	// dispatch to (docs/reference/tillr-distribution.md, "Documentation split" makes `<bin> --help` a
	// layer-1 surface for all eight binaries). Everything after a noun is still handed
	// over untouched, which is what keeps this from becoming a second interface.
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Printf("tillr: %s\nRun `tillr <noun> --help` for one noun's faces.\n", nounList())
		return record.ExitOK
	}
	if len(args) > 0 && args[0] == "notify" {
		return runNotify(args[1:], os.Stdout, os.Stderr)
	}
	d, err := resolve(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr: %v\n", err)
		return exitCode(err)
	}
	// The handover replaces this process image, so the child's argv, stdio, signals and
	// exit status are the caller's own: nothing is copied, translated or re-encoded on
	// the way through, and there is no wrapper left to observe it.
	if err := syscall.Exec(d.binary, append([]string{d.binary}, d.argv...), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "tillr: exec %s: %v\n", d.binary, err)
		return record.ExitExternal
	}
	return record.ExitOK // unreachable: a successful exec does not return
}

// resolve maps an invocation to the binary it dispatches to. A missing noun and a noun
// outside the dispatch set are usage errors; a noun whose binary is not installed is
// an external failure, because the invocation was well formed and the artifact is
// missing.
func resolve(args []string) (dispatch, error) {
	if len(args) == 0 {
		return dispatch{}, usagef("a noun is required; name one of: %s", nounList())
	}
	noun, rest := args[0], args[1:]
	if !isNoun(noun) {
		return dispatch{}, usagef("%q is not a tillr noun; name one of: %s", noun, nounList())
	}
	name := "tillr-" + noun
	binary, err := exec.LookPath(name)
	if err != nil {
		return dispatch{}, externalf("%s is not on PATH", name)
	}
	return dispatch{binary: binary, argv: rest}, nil
}

// isNoun reports whether noun is in the dispatch set. It is exact: no prefix matching and no
// aliases, so a near-miss is refused instead of resolving to a neighbour.
func isNoun(noun string) bool {
	for _, known := range nouns {
		if noun == known {
			return true
		}
	}
	return false
}

// nounList is the usage line's list, in the fixed order of nouns.
func nounList() string {
	list := ""
	for i, noun := range nouns {
		if i > 0 {
			list += ", "
		}
		list += noun
	}
	return list
}

// exitError is a refusal that already knows the code it answers with, so the code and the
// message are decided together rather than reassembled at main.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }

func (e *exitError) Unwrap() error { return e.err }

// usagef is a usage error: exit 2, with nothing started and nothing on stdout.
func usagef(format string, args ...any) error {
	return &exitError{code: record.ExitUsage, err: fmt.Errorf(format, args...)}
}

// externalf is an invocation that named a real noun but could not be handed over: exit 4.
// It is never the noun's outcome, because the noun never ran.
func externalf(format string, args ...any) error {
	return &exitError{code: record.ExitExternal, err: fmt.Errorf(format, args...)}
}

// exitCode is the code a refusal answers with. A refusal that named none is a bug, so it
// is exit 5 rather than a code a caller could read as a noun's result.
func exitCode(err error) int {
	var named *exitError
	if errors.As(err, &named) {
		return named.code
	}
	return record.ExitInternal
}
