package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
)

// boundaryFace is `drain boundary`: the pre-run and post-run halves of the write boundary
// design §1 rules a run stays inside, and the comparison that answers whether it did.
//
//   - `boundary snapshot` records one side of the bracket to the file the caller names.
//   - `boundary check` compares a before and an after snapshot and answers exit 1, naming
//     every change outside the target, when the run moved state it does not own.
//
// The two are separate faces because the run happens between them, in the caller's
// process: the loop takes the first snapshot before it launches `run exec`, the second
// after the run returns, and calls check before anything from that run stages or lands.
// A snapshot that cannot be taken exits nonzero and never writes a partial snapshot, so a
// caller that ignores the exit cannot mistake "could not look" for "nothing changed".
func boundaryFace(args []string) int {
	if len(args) == 0 {
		return fail(drain.Usagef("boundary needs a face: snapshot or check"))
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		boundarySnapshotFace([]string{"--help"})
		boundaryCheckFace([]string{"--help"})
		return record.ExitOK
	}
	switch face, rest := args[0], args[1:]; face {
	case "snapshot":
		return boundarySnapshotFace(rest)
	case "check":
		return boundaryCheckFace(rest)
	default:
		return fail(drain.Usagef("unknown boundary face %q: want snapshot or check", face))
	}
}

// pathFlag collects a flag whose value is a path a caller repeats: --target once per
// worktree this bracket's runs own, and --lane once per worktree the caller holds. The value
// is not comma-split: a path may hold a comma, and the loop passes one flag per path.
type pathFlag []string

func (p *pathFlag) String() string { return strings.Join(*p, ",") }

func (p *pathFlag) Set(value string) error {
	*p = append(*p, value)
	return nil
}

// boundarySnapshotFace records one snapshot of the span a run must stay inside.
func boundarySnapshotFace(args []string) int {
	fs := newFlagSet("boundary snapshot")
	repo := fs.String("repo", "", "the repository whose refs and worktrees are the boundary: the main checkout")
	var targets pathFlag
	fs.Var(&targets, "target", "a worktree this bracket's runs own; repeat once per run in the bracket")
	var lanes pathFlag
	fs.Var(&lanes, "lane", "another worktree the caller holds; repeat once per lane")
	var patchRefs pathFlag
	fs.Var(&patchRefs, "patch-ref", "a ref this bracket's runs may move besides their own branch (a fork lane's patch/<fork>); repeat once per fork lane")
	storePath := fs.String("store", "", "the store file to stat, only when the bracket wraps the adapter alone")
	output := fs.String("out", "", "the file the snapshot is written to")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *repo == "" || len(targets) == 0 || *output == "" {
		return fail(drain.Usagef("boundary snapshot needs --repo <main checkout>, at least one --target <worktree> and --out <snapshot.json>"))
	}
	snapshot, err := drain.TakeBoundary(context.Background(), drain.BoundarySpec{
		Repo:      *repo,
		Targets:   targets,
		Lanes:     lanes,
		PatchRefs: patchRefs,
		Store:     *storePath,
	})
	if err != nil {
		return fail(err)
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fail(drain.Internalf("encoding the boundary snapshot: %v", err))
	}
	if err := os.WriteFile(*output, append(raw, '\n'), 0o644); err != nil {
		return fail(drain.Externalf("writing %s: %v", *output, err))
	}
	return record.ExitOK
}

// boundaryCheckFace compares a run's two snapshots. Exit 1 is the check doing its job: the
// run changed state outside the worktree it was given, and every line on stdout names one
// change. A pair it cannot read or compare is exit 2, and a snapshot it cannot take is not
// this face's business — it already failed on the far side of the run.
func boundaryCheckFace(args []string) int {
	fs := newFlagSet("boundary check")
	beforePath := fs.String("before", "", "the snapshot taken before the run")
	afterPath := fs.String("after", "", "the snapshot taken after the run")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *beforePath == "" || *afterPath == "" {
		return fail(drain.Usagef("boundary check needs --before <snapshot.json> and --after <snapshot.json>"))
	}
	before, err := readBoundary(*beforePath)
	if err != nil {
		return fail(err)
	}
	after, err := readBoundary(*afterPath)
	if err != nil {
		return fail(err)
	}
	violations, err := drain.Compare(before, after)
	if err != nil {
		return fail(err)
	}
	for _, violation := range violations {
		fmt.Println(violation.String())
	}
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "boundary: %d change(s) outside %s: stop\n", len(violations), strings.Join(before.Targets, ", "))
		return record.ExitNegative
	}
	return record.ExitOK
}

// readBoundary reads one snapshot file. A file this face cannot read or decode is
// malformed input, not an empty snapshot: an unreadable half of a bracket must stop.
func readBoundary(path string) (*drain.Boundary, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, drain.Usagef("reading %s: %v", path, err)
	}
	var snapshot drain.Boundary
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, drain.Usagef("reading the boundary snapshot %s: %v", path, err)
	}
	if snapshot.Repo == "" || len(snapshot.Targets) == 0 || snapshot.Worktrees == nil {
		return nil, drain.Usagef("%s is not a boundary snapshot: it names no repo, targets or worktrees", path)
	}
	return &snapshot, nil
}
