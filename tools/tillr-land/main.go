// Command tillr-land lands a judged head onto a trunk. It is the land utility of the tillr
// suite: main keeps the flags, the exit codes and the process lifecycle, and the land
// package keeps the schema, the one landing transaction and the squash.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// main flushes the SQLite trace on every route out of the process: before os.Exit,
// which skips defers, and through the deferred call when a panic unwinds this
// goroutine. Tracing off, each flush is one atomic load.
func main() {
	defer store.FlushSQLiteTraceAtExit()
	code := dispatch(os.Args[1:])
	store.FlushSQLiteTraceAtExit()
	os.Exit(code)
}

// dispatch runs one face and answers its exit code. It is not named land because the land
// package is, and one spelling per concept leaves the package the name.
func dispatch(args []string) int {
	if len(args) == 0 {
		return fail(land.Usagef("a face is required: %s", faceList))
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-land: %s\nRun `tillr-land <face> --help` for one face's flags.\n", faceList)
		return record.ExitOK
	}
	face, rest := args[0], args[1:]
	switch face {
	case "pass":
		return passFace(rest)
	case "one":
		return oneFace(rest)
	default:
		return fail(land.Usagef("unknown face %q: want %s", face, faceList))
	}
}

// passFace lands one judged head. It is the landing transaction with nothing in front of it:
// the gate already ran, the verdict is the input, and the face squashes the change onto the
// trunk under the land lock. There is no queue between the gate and the trunk, which is why
// it takes the item, the run, the base and the head directly.
func passFace(args []string) int {
	fs := newFlagSet("pass")
	path := fs.String("store", "", "the store to write")
	repo := fs.String("repo", "", "the git repository whose trunk the landing moves")
	item := fs.String("item", "", "the board item the landing is for")
	run := fs.String("run", "", "the run whose head is being landed")
	base := fs.String("base", "", "the base sha the candidate's change is measured from")
	head := fs.String("head", "", "the head sha to land")
	messageFrom := fs.String("message-from", "", "the commit in base..head whose message and author date to use (default: head)")
	runDir := fs.String("run-dir", "", "the run's commit-list directory (default: <git-common-dir>/tillr/runs/<run>)")
	observed := fs.String("observed-trunk", "", "the loop's last observed trunk tip; a rewrite refuses the landing under the lock")
	baseRef := fs.String("base-ref", "", "the branch to land onto (default: the checkout's branch)")
	verdictPath := fs.String("verdict", "", "the gate.verdict record that judged the head")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	lockPath := fs.String("lock", "", "the land lock file; defaults to <git-common-dir>/land.lock")
	lockWait := fs.Duration("lock-timeout", 0, "how long the landing waits for the land lock; 0 waits until the context ends")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *repo == "" || *item == "" || *run == "" || *base == "" || *head == "" || *verdictPath == "" {
		return fail(land.Usagef("pass needs --store <path>, --repo <dir>, --item <id>, --run <id>, --base <sha>, --head <sha> and --verdict <verdict.json>"))
	}
	at, err := land.Clock(*now)
	if err != nil {
		return fail(err)
	}
	raw, err := os.ReadFile(*verdictPath)
	if err != nil {
		return fail(land.Usagef("reading %s: %v", *verdictPath, err))
	}
	verdict, err := land.DecodeVerdict(raw)
	if err != nil {
		return fail(err)
	}
	trunk, err := land.OpenTrunk(*repo)
	if err != nil {
		return fail(err)
	}
	ref := *baseRef
	if ref == "" {
		if ref, err = trunk.Branch(); err != nil {
			return fail(err)
		}
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()

	req := land.LandingRequest{
		ItemID: *item, RunID: *run, BaseRef: ref, BaseSHA: *base, HeadSHA: *head,
		Verdict: verdict, At: at, LockPath: *lockPath, LockWait: *lockWait,
		MessageFrom: *messageFrom, RunDir: *runDir, ObservedTrunk: *observed,
	}
	landed, err := land.Land(ctx, s, trunk, req)
	if err != nil {
		return fail(err)
	}
	return reportLanding(landed, at)
}

// oneFace gates and lands one branch in one command: the manual landing a coordinator runs
// when no drain is running. It is the gate program plus the landing transaction, and it
// holds the land lock for neither the gate nor the judgment, only for the landing.
func oneFace(args []string) int {
	fs := newFlagSet("one")
	path := fs.String("store", "", "the store to write")
	repo := fs.String("repo", "", "the git repository the branch is in")
	branch := fs.String("branch", "", "the branch to gate and land")
	trunkRef := fs.String("trunk", "main", "the trunk ref the branch lands on")
	item := fs.String("item", "", "the item this landing is for; the run when omitted")
	run := fs.String("run", "", "the run id to file the landing under; derived from the head when omitted")
	gate := fs.String("gate", "", "the gate program: it prints a gate.verdict and exits 0 pass, 1 fail, anything else could not judge")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	lockPath := fs.String("lock", "", "the land lock file; defaults to <git-common-dir>/land.lock")
	lockWait := fs.Duration("lock-timeout", 0, "how long the landing waits for the land lock; 0 waits until the context ends")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *repo == "" || *branch == "" || *gate == "" {
		return fail(land.Usagef("one needs --store <path>, --repo <dir>, --branch <ref> and --gate <program>"))
	}
	at, err := land.Clock(*now)
	if err != nil {
		return fail(err)
	}
	trunk, err := land.OpenTrunk(*repo)
	if err != nil {
		return fail(err)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()

	req := land.OneRequest{
		Branch: *branch, Trunk: *trunkRef, Run: *run, Item: *item,
		Gate: strings.Fields(*gate), At: at, LockPath: *lockPath, LockWait: *lockWait,
	}
	landed, err := land.One(ctx, s, trunk, req)
	if err != nil {
		return fail(err)
	}
	return reportLanding(landed, at)
}

// reportLanding prints the record and answers the face's code: 0 for a landing, 1 for a
// completed negative answer (a squash conflict or a refusal), which is what the drain reads
// as "not landed" rather than as a failure of the step.
func reportLanding(landed land.Landing, at time.Time) int {
	if code := printRecord(land.LandingRecord(landed, record.Envelope{CreatedAt: at})); code != record.ExitOK {
		return code
	}
	if landed.Result == land.ResultLanded {
		return record.ExitOK
	}
	return record.ExitNegative
}

// printRecord writes one record to stdout and answers the exit code of the write.
func printRecord(env record.Envelope) int {
	raw, err := record.Encode(env)
	if err != nil {
		return fail(land.Internalf("encoding the record: %v", err))
	}
	fmt.Println(string(raw))
	return record.ExitOK
}

// faceList is this binary's faces, in the order the usage lines print them. One home, so
// the refusal, the unknown-face line and `--help` cannot disagree.
const faceList = "pass or one"

// errHelp is what parseFlags answers for a `--help`. A face's flags are the layer-1
// surface (docs/design/tillr-distribution.md Section 3), so a help request is an answer and
// not a usage error: fail turns it into exit 0 and prints nothing.
var errHelp = errors.New("help requested")

func newFlagSet(face string) *flag.FlagSet {
	fs := flag.NewFlagSet("tillr-land "+face, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags turns a flag error into the usage exit, with the diagnostic on stderr.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printHelp(fs)
			return errHelp
		}
		return land.Usagef("%v", err)
	}
	if fs.NArg() > 0 {
		return land.Usagef("unexpected argument %q", fs.Arg(0))
	}
	return nil
}

// printHelp writes one face's flags to stdout, under the face's own name: a reader learns a
// flag from the binary and never from a document.
func printHelp(fs *flag.FlagSet) {
	fs.SetOutput(os.Stdout)
	fs.Usage()
}
