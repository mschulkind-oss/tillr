// The build a board was served by, reported on every response.
//
// Two failures cost an owner a debugging session each and both were the same shape: a
// board that is not the current tree says nothing about being an old one. A `tillr-view`
// started before the item `rank` field drew 55 rows in item-id order, because the client's
// order is rank-then-id and a missing rank falls through to the id; the same process
// answered `/api/v1/items/{id}/doc` with "not a route this face serves". Nothing on the
// screen said "this server is older than the code you are reading". This stamp is what
// makes that a one-glance answer instead of an investigation: the revision, the time it
// was built, whether the front end is embedded or a directory, and which executable is
// answering.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// buildStamp is the program a response came from. Every field is best-effort: a binary
// built outside a repository has no VCS information, and that absence is reported as
// empty rather than filled with a guess.
type buildStamp struct {
	// Revision is the commit the binary was built from (`vcs.revision`), empty when the
	// build carried none.
	Revision string `json:"revision"`
	// Time is when that commit was made (`vcs.time`), RFC 3339, empty when unknown. It is
	// the commit's time, not the build's: a stale binary is stale by its source, and a
	// rebuild of old source is not news.
	Time string `json:"time"`
	// Modified is true when the working tree had uncommitted changes at build time, which
	// is the difference between "the current commit" and "something near it".
	Modified bool `json:"modified"`
	// FrontEnd is where the UI came from: "embedded", "dir:<path>" or "none". "none" is
	// the state whose consequence is `noFrontEndNote` on every page.
	FrontEnd string `json:"front_end"`
	// Executable is the path of the running program, so "which of the three installs am I
	// looking at" is answered by the page rather than by `ps`.
	Executable string `json:"executable"`
	// Digest is the SHA-256 of the executable file at startup. Two builds of one dirty
	// revision share every field above, and a second `just observatory` must be able to
	// tell whether the process already holding its port is the binary it just built.
	Digest string `json:"digest,omitempty"`
	// Checkout is this build compared with the checkout being served, refreshed while the
	// server runs (checkoutFreshness), and absent where no comparison was asked for.
	Checkout *checkoutFreshness `json:"checkout,omitempty"`
}

// String is the line the server prints at startup and the board can show, one line and
// never multi-line.
func (b buildStamp) String() string {
	rev := b.Revision
	if rev == "" {
		rev = "no-revision"
	} else if len(rev) > 12 {
		rev = rev[:12]
	}
	if b.Modified {
		rev += "+dirty"
	}
	at := b.Time
	if at == "" {
		at = "unknown-time"
	}
	return rev + " (" + at + ") front-end " + b.FrontEnd + " exe " + b.Executable
}

// buildInfo reads this binary's stamp. It never fails: a build with no module or VCS
// information answers zero revisions, because a board that refuses to render its own
// provenance answers the question it was asked by not answering at all.
func buildInfo(frontEnd string) buildStamp {
	stamp := buildStamp{FrontEnd: frontEnd}
	if exe, err := os.Executable(); err == nil {
		// A binary replaced by a rename reads back as "<path> (deleted)": the path is the
		// new file's, and the running image is the one /proc still holds.
		stamp.Executable = strings.TrimSuffix(exe, " (deleted)")
		if runtime.GOOS == "linux" {
			stamp.Digest = fileDigest("/proc/self/exe")
		} else {
			stamp.Digest = fileDigest(exe)
		}
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return stamp
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			stamp.Revision = setting.Value
		case "vcs.time":
			stamp.Time = setting.Value
		case "vcs.modified":
			stamp.Modified = setting.Value == "true"
		}
	}
	return stamp
}

// fileDigest is a file's SHA-256, or "" when it cannot be read.
func fileDigest(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// checkoutFreshness compares this build with the checkout it is serving. A server outlives
// the tree it was built from — the owner's screenshot of 2026-11-17 was answered by a
// process started days before, from another build — so the board says whether the code it
// is drawing is still the checkout's.
//
// State is "current" (the checkout's tillr sources are the ones this binary was built
// from), "behind" (they have moved since), or "unknown" (git could not say: no repository at
// the root, no git, or a revision the checkout does not hold). Only the sources this
// program and its planner are built from count: a landing that touches a document does not
// make the board stale.
type checkoutFreshness struct {
	State string `json:"state"`
	Head  string `json:"head,omitempty"`
	Note  string `json:"note"`
}

// tillrSourcePaths are the module directories `tillr-view` and `tillr-drain` build from:
// their own and every replace target of their go.mod files.
var tillrSourcePaths = []string{"tools/tillr", "tools/tillr-drain", "tools/tillr-item", "tools/tillr-run", "tools/tillr-view"}

// gitRunner runs one git command in the root and answers its stdout and exit code; -1 is
// git not running at all.
type gitRunner func(root string, args ...string) (string, int)

func runGit(root string, args ...string) (string, int) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return string(out), exit.ExitCode()
		}
		return "", -1
	}
	return string(out), 0
}

// freshness answers the comparison for one build and root.
func freshness(build buildStamp, root string, git gitRunner) checkoutFreshness {
	out, code := git(root, "rev-parse", "HEAD")
	if code != 0 {
		return checkoutFreshness{State: "unknown", Note: "git cannot read HEAD at " + root + ", so the board cannot tell whether this build is the checkout's"}
	}
	head := strings.TrimSpace(out)
	if build.Revision == "" {
		return checkoutFreshness{State: "unknown", Head: head, Note: "this binary carries no revision, so it cannot be compared with HEAD"}
	}
	args := append([]string{"diff", "--quiet", build.Revision, head, "--"}, tillrSourcePaths...)
	if build.Revision != head {
		switch _, code := git(root, args...); code {
		case 0:
		case 1:
			return checkoutFreshness{State: "behind", Head: head,
				Note: "the checkout's tillr sources moved since this server was built: stop it and run `just observatory` again"}
		default:
			return checkoutFreshness{State: "unknown", Head: head,
				Note: "this build's revision is not in the checkout's history, so it cannot be compared with HEAD"}
		}
	}
	if !build.Modified {
		dirty := append([]string{"diff", "--quiet", "HEAD", "--"}, tillrSourcePaths...)
		if _, code := git(root, dirty...); code == 1 {
			return checkoutFreshness{State: "behind", Head: head,
				Note: "the checkout has uncommitted edits to the tillr sources that this build does not include: stop it and run `just observatory` again"}
		}
		return checkoutFreshness{State: "current", Head: head, Note: "the checkout's tillr sources are the ones this server was built from"}
	}
	return checkoutFreshness{State: "current", Head: head,
		Note: "built from this revision with uncommitted edits; edits made since cannot be compared"}
}

// checkoutWatch caches freshness, because it is on every response and a git call per
// request would be the board's most expensive read. It refreshes at most once per
// checkoutTTL; the clock and git are fields so a test names both.
type checkoutWatch struct {
	mu    sync.Mutex
	root  string
	git   gitRunner
	now   func() time.Time
	at    time.Time
	value *checkoutFreshness
}

const checkoutTTL = 3 * time.Second

func (w *checkoutWatch) read(build buildStamp) *checkoutFreshness {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if w.value == nil || now.Sub(w.at) >= checkoutTTL || now.Before(w.at) {
		value := freshness(build, w.root, w.git)
		w.value, w.at = &value, now
	}
	copied := *w.value
	return &copied
}
