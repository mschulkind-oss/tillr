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
	"os"
	"runtime/debug"
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
		stamp.Executable = exe
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
