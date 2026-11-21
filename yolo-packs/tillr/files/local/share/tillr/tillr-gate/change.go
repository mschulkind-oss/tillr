package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
)

// ChangeKind is the record kind the change face emits and `plan` reads. It is an input to
// the gate rather than an event: a change is a fact about a commit range, so appending it
// would claim a state transition that does not exist.
const ChangeKind = "gate.change"

func init() {
	// The change record had exactly one producer, and it was the loop's Python. Declaring
	// the kind here is what makes this binary the one writer, the same way `gate.plan` and
	// `gate.verdict` are.
	if err := event.Register(ChangeKind, "gate"); err != nil {
		panic(err)
	}
}

// ChangeCandidate is the candidate a caller names, in the argv `tillr-loop gate` already
// takes: which item and lane it is for, which run produced it, and the commit range read
// in one worktree.
//
// `Item` and `Lane` are not fields of the record — a change is a function of the commit
// range alone — and they are carried so that the face takes the loop's own candidate argv
// rather than a subset of it. A caller holding a candidate can hand the same six flags to
// the loop and to this face.
type ChangeCandidate struct {
	Item     string
	Lane     string
	Run      string
	Worktree string
	Base     string
	Head     string
}

// DeriveChange computes the `gate.change` record for one candidate: the base the caller
// named, `head^{tree}`, and the paths between them.
//
// It is a pure function of `(candidate, now)` and the worktree's git state, so two callers
// that name one candidate derive one record. The paths are the three-dot diff
// (`base...head`) the loop has always used: the change a candidate is, not the change the
// trunk has taken since.
//
// Every failure is a refusal: a revision that does not resolve, or a worktree git cannot
// read, answers an error and no record at all. A partial change would plan fewer checks
// than the change owes, and a plan that dropped a check is a verdict about a tree nobody
// judged.
func DeriveChange(candidate ChangeCandidate, now time.Time) (record.Envelope, error) {
	if candidate.Run == "" {
		return record.Envelope{}, usagef("no run was named: the change record's id is change-<run>")
	}
	if candidate.Worktree == "" {
		return record.Envelope{}, usagef("no worktree was named: the change is read in one")
	}
	if candidate.Base == "" || candidate.Head == "" {
		return record.Envelope{}, usagef("the change needs a --base and a --head")
	}
	head, err := changeGit(candidate.Worktree, "rev-parse", candidate.Head+"^{commit}")
	if err != nil {
		return record.Envelope{}, err
	}
	tree, err := changeGit(candidate.Worktree, "rev-parse", head+"^{tree}")
	if err != nil {
		return record.Envelope{}, err
	}
	diff, err := changeGitOutput(candidate.Worktree, "diff", "--name-only", "--no-renames", "-z",
		candidate.Base+"..."+candidate.Head)
	if err != nil {
		return record.Envelope{}, err
	}
	// An empty diff is an empty list, never a missing one: `decodeChange` refuses a record
	// with no `changed_paths`, so a nil slice here would be a record `plan` cannot read.
	paths := make([]string, 0, strings.Count(diff, "\x00"))
	for _, path := range strings.Split(diff, "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	raw, err := json.Marshal(Change{
		HeadSHA:      head,
		BaseSHA:      candidate.Base,
		TreeSHA:      tree,
		ChangedPaths: paths,
	})
	if err != nil {
		return record.Envelope{}, internalf("encoding the gate.change payload: %v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          ChangeKind,
		ID:            "change-" + candidate.Run,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: Version},
		CreatedAt:     now.UTC(),
		Payload:       raw,
	}, nil
}

// changeGit runs one git read in the worktree and returns its trimmed output.
//
// A git that exits non-zero is the caller's input: the revision they named is not in this
// worktree. A git that cannot be started at all is the machine's: nothing was read, so the
// face answers "no judgment" rather than "bad request".
func changeGit(worktree string, args ...string) (string, error) {
	out, err := changeGitOutput(worktree, args...)
	return strings.TrimSpace(out), err
}

func changeGitOutput(worktree string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
	out, err := cmd.Output()
	if err == nil {
		return string(out), nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", usagef("git -C %s %s answered exit %d: %s; the change cannot be derived "+
			"from what the caller named", worktree, strings.Join(args, " "), exit.ExitCode(),
			strings.TrimSpace(string(exit.Stderr)))
	}
	return "", externalf("git could not be run: %v", err)
}

// changeFace is `tillr-gate change`: a candidate in, the `gate.change` record out.
//
// It is the producer `plan` reads, so a caller that is not the loop can drive the gate
// without hand-spelling the record. It writes nothing but the output file, and it opens no
// store: the gate's adoptability claim is that `plan` runs with no store at all (design
// §5.7, §5.11), and a change is derived from git alone.
func changeFace(args []string) int {
	flags := flag.NewFlagSet("change", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	item := flags.String("item", "", "the item the change is for (the loop's candidate argv; not a record field)")
	lane := flags.String("lane", "", "the lane the candidate was made in (the loop's candidate argv; not a record field)")
	run := flags.String("run", "", "the run that produced the candidate (required; the record's id is change-<run>)")
	worktree := flags.String("worktree", "", "the worktree the change is read in (required)")
	base := flags.String("base", "", "the base sha the candidate is on (required)")
	head := flags.String("head", "", "the candidate's head sha (required)")
	now := flags.String("now", "", "the record's created_at, RFC 3339 (default: now)")
	outputPath := flags.String("output", "", "where to write the gate.change record (default: stdout)")
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *run == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate change: --run is required")
		return record.ExitUsage
	}
	if *worktree == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate change: --worktree is required")
		return record.ExitUsage
	}
	if *base == "" || *head == "" {
		fmt.Fprintln(os.Stderr, "tillr-gate change: --base and --head are required")
		return record.ExitUsage
	}
	created := time.Now()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate change: %v\n",
				usagef("--now %q is not RFC 3339: %v", *now, err))
			return record.ExitUsage
		}
		created = parsed
	}
	envelope, err := DeriveChange(ChangeCandidate{
		Item:     *item,
		Lane:     *lane,
		Run:      *run,
		Worktree: *worktree,
		Base:     *base,
		Head:     *head,
	}, created)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate change: %v\n", err)
		return exitCode(err)
	}
	raw, err := record.Encode(envelope)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate change: %v\n", internalf("encoding the change record: %v", err))
		return record.ExitInternal
	}
	body := append(raw, '\n')
	if *outputPath == "" {
		if _, err := os.Stdout.Write(body); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate change: %v\n", internalf("writing the change record: %v", err))
			return record.ExitInternal
		}
		return record.ExitOK
	}
	if err := os.WriteFile(*outputPath, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate change: %v\n", internalf("writing %s: %v", *outputPath, err))
		return record.ExitInternal
	}
	return record.ExitOK
}
