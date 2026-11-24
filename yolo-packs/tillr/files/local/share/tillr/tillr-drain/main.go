// Command tillr-drain computes one tick's dispatch decisions, and records them. It is the
// drain utility of the tillr suite: plan consumes a drain.pass, reads item, lane and run
// state, and writes one drain.decision record per item — and writes nothing else,
// anywhere; once does the same planning and then, in one write transaction, records the
// decisions in drain's own `dispatches` table and hands each dispatch decision's
// run.request to a caller-named runner; retry records one operator's authorization to
// re-dispatch a run the harness refused; boundary records the write boundary before and
// after a run and compares the pair. main keeps the flags, the exit codes and the process
// lifecycle, and the drain package keeps the kinds, the tick, the decision rules and the
// boundary snapshot.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
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

// recordSinkName is the one runner this lane has, named for the caller and for the refusal
// that answers anything else.
const recordSinkName = "record-sink"

// dispatch runs one face and answers its exit code. It is not named run because the run
// package is, and one spelling per concept leaves the package the name.
func dispatch(args []string) int {
	if len(args) == 0 {
		return fail(drain.Usagef("a face is required: %s", faceList))
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-drain: %s\nRun `tillr-drain <face> --help` for one face's flags.\n", faceList)
		return record.ExitOK
	}
	face, rest := args[0], args[1:]
	switch face {
	case "probe-backend":
		return probeBackendFace(rest)
	case "workflow":
		return workflowFace(rest)
	case "why":
		return whyFace(rest)
	case "policy":
		return policyFace(rest)
	case "plan":
		return planFace(rest)
	case "once":
		return onceFace(rest)
	case "split":
		return splitFace(rest)
	case "clear-fault":
		return clearFaultFace(rest)
	case "accept-rewrite":
		return acceptRewriteFace(rest)
	case "acknowledge-patch-move":
		return acknowledgePatchMoveFace(rest)
	case "push":
		return pushFace(rest)
	case "sweep-refs":
		return sweepRefsFace(rest)
	case "acquit", "examine", "set-aside":
		return recoveryFace(face, rest)
	case "retry":
		return retryFace(rest)
	case "boundary":
		return boundaryFace(rest)
	case "resume":
		return resumeFace(rest)
	case "scale":
		return scaleFace(rest)
	case "stop":
		return stopFace(rest)
	case "loop":
		return loopFace(rest)
	case "restart":
		return restartFace(rest)
	default:
		return fail(drain.Usagef("unknown face %q: want %s", face, faceList))
	}
}

func acknowledgePatchMoveFace(args []string) int {
	fs := newFlagSet("acknowledge-patch-move")
	path := fs.String("store", "", "the store holding the observation (required)")
	seq := fs.Int64("observation", 0, "the boundary event sequence shown by waiting")
	fork := fs.String("fork", "", "the fork whose observed move was reviewed")
	by := fs.String("by", "", "who acknowledges the move (required)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *seq <= 0 || strings.TrimSpace(*fork) == "" || strings.TrimSpace(*by) == "" {
		return fail(drain.Usagef("acknowledge-patch-move needs --store <path>, --observation <sequence>, --fork <fork> and --by <owner>"))
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := patchmoves.Acknowledge(tx, *seq, *fork, *by, time.Now().UTC()); err != nil {
			return drain.Usagef("%v", err)
		}
		return nil
	}); err != nil {
		return fail(err)
	}
	fmt.Printf("acknowledged patch/%s observation %d by %s\n", *fork, *seq, *by)
	if raw, err := exec.CommandContext(ctx, "git", "-C", filepath.Dir(*path), "rev-parse", "--show-toplevel").Output(); err == nil {
		root := strings.TrimSpace(string(raw))
		if cfg, err := settings.Read(root); err == nil && drain.HasUnpublishedCommits(ctx, root, cfg.Push) {
			out, err := drain.PushOnce(ctx, root, *path)
			printPushOutcome(out, err, false)
		}
	}
	return record.ExitOK
}

func pushFace(args []string) int {
	fs := newFlagSet("push")
	root := fs.String("root", ".", "the repository to push")
	path := fs.String("store", "", "the store (default <root>/.tillr/state.db)")
	asJSON := fs.Bool("json", false, "emit the recorded push outcome as JSON")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	out, err := drain.PushOnce(context.Background(), *root, *path)
	return printPushOutcome(out, err, *asJSON)
}

func printPushOutcome(out drain.PushOutcome, err error, asJSON bool) int {
	if out.Kind == "" {
		return fail(err)
	}
	if asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			return fail(err)
		}
	} else {
		line := fmt.Sprintf("push %s (%s)", strings.TrimSpace(out.What+" "+out.Tip), out.Kind)
		if out.OK {
			fmt.Println(line)
		} else {
			fmt.Fprintf(os.Stderr, "%s: %s\n", line, out.Detail)
			if out.Mover != "" && !strings.Contains(out.Detail, out.Mover) {
				fmt.Fprintf(os.Stderr, "mover: %s\n", out.Mover)
			}
		}
	}
	if out.OK {
		return record.ExitOK
	}
	return drain.ExitCode(err)
}

func sweepRefsFace(args []string) int {
	fs := newFlagSet("sweep-refs")
	root := fs.String("root", "", "the repository whose refs should be cleaned up")
	path := fs.String("store", "", "the store (default <root>/.tillr/state.db)")
	dry := fs.Bool("dry-run", false, "report the plan without deleting refs")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *root == "" {
		return fail(drain.Usagef("sweep-refs needs --root <repository>"))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	refs, err := drain.SweepRefs(context.Background(), drain.RefSweepRequest{Root: *root, Store: *path, DryRun: *dry})
	if err != nil {
		return fail(err)
	}
	for _, ref := range refs {
		action := "kept"
		if ref.Remove {
			action = "removed"
			if *dry {
				action = "would remove"
			}
		}
		fmt.Printf("%s %s (%s): %s\n", action, ref.Ref, ref.SHA, ref.Reason)
	}
	return record.ExitOK
}

// clearFaultFace acknowledges one diagnosed fault without erasing its evidence.
func clearFaultFace(args []string) int {
	fs := newFlagSet("clear-fault")
	path := fs.String("store", "", "the store holding the fault")
	itemID := fs.String("item", "", "the item whose fault was diagnosed")
	seq := fs.Int64("fault", 0, "the fault event sequence shown by waiting")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *itemID == "" || *seq <= 0 {
		return fail(drain.Usagef("clear-fault needs --store <path>, --item <id> and --fault <sequence>"))
	}
	ctx := context.Background()
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error { return drain.ClearItemFault(tx, *itemID, *seq, time.Now().UTC()) }); err != nil {
		return fail(err)
	}
	return record.ExitOK
}

// acceptRewriteFace accepts a trunk rewrite that preserved the content, clearing the
// trunk-rewritten hold. drain.AcceptRewrite holds the condition; anything else is refused.
func acceptRewriteFace(args []string) int {
	fs := newFlagSet("accept-rewrite")
	root := fs.String("root", "", "the repository whose trunk was rewritten")
	path := fs.String("store", "", "the store holding the trunk history (default <root>/.tillr/state.db)")
	lost := fs.String("lost", "", "the lost tip the hold names (trunk-rewritten: <lost> -> <current>)")
	current := fs.String("current", "", "the commit on the trunk that replaces the lost tip")
	by := fs.String("by", "", "who accepts the rewrite")
	why := fs.String("why", "", "why the rewrite was made")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *root == "" || *lost == "" || *current == "" || *by == "" || *why == "" || *now == "" {
		return fail(drain.Usagef("accept-rewrite needs --root <repository>, --lost <sha>, --current <sha>, --by <who>, --why <reason> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return fail(drain.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	a, err := drain.AcceptRewrite(context.Background(), drain.AcceptRewriteRequest{
		Root: *root, Store: *path, Lost: *lost, Current: *current, By: *by, Why: *why, At: at.UTC(),
	})
	if err != nil {
		return fail(err)
	}
	if a.Landing {
		fmt.Printf("accepted the landing rewrite %s -> %s: same stable patch id, current tree %s; lost landing pinned at %s\n", a.Lost, a.Current, a.Tree, a.Pin)
	} else if a.Rebased {
		fmt.Printf("accepted the rewrite %s -> %s: rebase onto a moved base, %d lost commits matched by patch, current tree %s; lost tip pinned at %s\n", a.Lost, a.Current, a.Commits, a.Tree, a.Pin)
	} else {
		fmt.Printf("accepted the rewrite %s -> %s: same tree %s, %d commits replaced one for one; lost tip pinned at %s\n", a.Lost, a.Current, a.Tree, a.Commits, a.Pin)
	}
	if a.GreenTo != "" {
		fmt.Printf("moved %s from %s to the commit that replaced it, %s (same tree)\n", drain.GreenRef, a.GreenFrom, a.GreenTo)
	}
	return record.ExitOK
}

// planFace computes one tick's decisions and writes them to the file the caller named.
// The store is opened read-only and never migrated; only --output is written,
// after every decision is computed.
func planFace(args []string) int {
	fs := newFlagSet("plan")
	path := fs.String("store", "", "the store to read")
	input := fs.String("input", "", "the drain.pass to plan")
	output := fs.String("output", "", "the file the drain.decision records are written to")
	root := fs.String("root", ".", questionsRootHelp)
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *input == "" || *output == "" {
		return fail(drain.Usagef("plan needs --store <path>, --input <tick.json> and --output <decisions.json>"))
	}

	tick, err := readPass(*input)
	if err != nil {
		return fail(err)
	}
	repositorySettings, err := settings.Read(*root)
	if err != nil {
		return fail(drain.Usagef("%v", err))
	}
	tick.WorkflowDefaults = repositorySettings.WorkflowDefaults
	tick.Comments = repositorySettings.Comments
	// The policy is loaded before the store is read, so a tick naming an unreadable
	// policy is refused as malformed input rather than silently holding every item with
	// `no-policy`.
	policy, err := readPolicy(tick.PolicyRef)
	if err != nil {
		return fail(err)
	}
	if policy != nil {
		policy.Root = *root
	}

	ctx := context.Background()
	// The index is read lazily, on the first item that names a design document. An absent
	// tool is not a failure of the face — it is `questions-unknown` for the items whose
	// documents are in the tree, which is the hold rather than the guess.
	questions := &drain.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: ctx}
	s, err := store.OpenRead(ctx, *path)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()

	var decisions []record.Envelope
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		decisions, err = drain.Plan(r, tick, policy, questions)
		return err
	}); err != nil {
		return fail(err)
	}
	if err := writeDecisions(*output, decisions); err != nil {
		return fail(err)
	}
	return record.ExitOK
}

// onceFace makes one tick's decisions real: the same planning plan does, then one write
// transaction that records every decision in `dispatches` and hands the dispatch
// decisions' run requests to the runner the caller named.
//
// The store is opened the way every write face in the suite opens one: created if it is
// not there, and migrated with the DDL this process registered. That is not a convenience —
// drain links item's and run's packages, so a store another utility created gains the
// tables drain reads and writes, and a store drain created gains theirs. Nothing is
// printed: the rows are the answer, and a caller that wants the decisions reads them
// through `plan`.
//
// A tick that dispatches nothing is a successful tick with zero dispatches — exit 0, an
// empty set of requests handed over — because the face made no negative decision.
func onceFace(args []string) int {
	fs := newFlagSet("once")
	path := fs.String("store", "", "the store to record the decisions in")
	input := fs.String("input", "", "the drain.pass to plan and record")
	name := fs.String("runner", "", "where to hand each dispatch's run.request: record-sink")
	sink := fs.String("runner-output", "", "the file a record-sink runner captures the requests to")
	root := fs.String("root", ".", questionsRootHelp)
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *input == "" || *name == "" {
		return fail(drain.Usagef("once needs --store <path>, --input <tick.json> and --runner <name>"))
	}
	runner, err := newRunner(*name, *sink)
	if err != nil {
		return fail(err)
	}

	tick, err := readPass(*input)
	if err != nil {
		return fail(err)
	}
	repositorySettings, err := settings.Read(*root)
	if err != nil {
		return fail(drain.Usagef("%v", err))
	}
	tick.WorkflowDefaults = repositorySettings.WorkflowDefaults
	tick.Comments = repositorySettings.Comments
	policy, err := readPolicy(tick.PolicyRef)
	if err != nil {
		return fail(err)
	}
	if policy != nil {
		policy.Root = *root
	}

	ctx := context.Background()
	questions := &drain.VantageQuestions{Root: *root, Command: commandFields(*questionsCmd), Context: ctx}
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	if err := drain.Once(ctx, s, tick, policy, questions, runner); err != nil {
		return fail(err)
	}
	return record.ExitOK
}

// retryFace records one operator's authorization to re-dispatch a run the harness refused,
// or to raise an item's conflict again for one more design run, and echoes it as a
// drain.retry (or drain.retry.design) record. It is the one write a retry needs: `plan` reads the
// authorization and decides, and AuthorizeRetry is where the claim's structural and
// judgment checks live, so the face itself adds no rule of its own.
//
// The store is opened write-capable and migrated, the way every write face in the suite
// opens one, but a path that does not exist is refused before the store is opened: a retry
// names an item and a run in a store that already exists, and creating one to hold a claim
// about nothing is the shape this refusal removes.
// retryHelp is the retry face's own help, ahead of its flags.
const retryHelp = `Usage: tillr drain retry --store <path> --item <id> --run <id> --by <who> --why <reason> --now <RFC3339> [--acknowledge gate-failed|unjudged]

Records one operator's authorization, with who asked and why. Two forms, chosen by the run:

  A build retry names the item's latest finished run. The plan re-dispatches the item,
  superseding that run. A run the gate judged needs --acknowledge gate-failed, and a run
  a wall cut short needs --acknowledge unjudged.

  A design retry names the design run whose objection holds the item's conflict for the
  owner (conflict-owner). It raises the item's conflict again for one more design run,
  and records a drain.retry.design event; it writes no build retry. That design run is
  settled like any other: the conflict returns to the owner only if it objects naming
  the owner. A design run the conflict no longer names is refused, naming the current one.

Every refusal names the exact command it would accept, or says that none exists and who
moves instead.

`

func retryFace(args []string) int {
	fs := newFlagSet("retry")
	path := fs.String("store", "", "the store to record the authorization in")
	itemID := fs.String("item", "", "the item whose run is retried")
	runID := fs.String("run", "", "the run the retry supersedes, or the design run whose objection the owner holds")
	by := fs.String("by", "", "who asked for the retry")
	why := fs.String("why", "", "why the retry is authorized")
	acknowledge := fs.String("acknowledge", "", "the outcome the retry acknowledges: gate-failed or unjudged (default: neither)")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), retryHelp)
		fs.PrintDefaults()
	}
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *itemID == "" || *runID == "" || *by == "" || *why == "" || *now == "" {
		return fail(drain.Usagef("retry needs --store <path>, --item <id>, --run <id>, --by <who>, --why <reason> and --now <RFC3339>"))
	}
	at, err := time.Parse(time.RFC3339, *now)
	if err != nil {
		return fail(drain.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(drain.Incompatiblef("the store %s: %v", *path, err))
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	var retry drain.Retry
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		retry, err = drain.AuthorizeRetry(ctx, tx, drain.RetryRequest{
			ItemID: *itemID, RunID: *runID, By: *by, Why: *why, At: at.UTC(),
			Acknowledge: *acknowledge,
		})
		return err
	}); err != nil {
		return fail(err)
	}
	env, err := drain.RetryEnvelope(retry)
	if err != nil {
		return fail(err)
	}
	raw, err := record.Encode(env)
	if err != nil {
		return fail(drain.Internalf("encoding the retry record: %v", err))
	}
	fmt.Println(string(raw))
	return record.ExitOK
}

// resumeFace continues one run that died without a verdict, from its own harness session.
//
// It is a face of its own rather than a plan decision because a resume is not a dispatch: the
// run id, the lane, the brief and the fence are the ones the run already had, and taking them
// through the planner would have to re-derive the lane and would spend an attempt. The face
// re-takes the boundary bracket and re-derives the fence before it continues (drain.Resume),
// and it answers the resumed record, so a caller reads what the continuation did.
//
// It exits with the run's own code, like `run exec`: a resumed segment that failed is a run
// that failed, and the record printed beside the code says which.
func resumeFace(args []string) int {
	fs := newFlagSet("resume")
	root := fs.String("root", "", "the repository the run's boundary bracket covers")
	path := fs.String("store", "", "the store holding the run, its item and the bracket's event")
	laneRoot := fs.String("lane-root", "", "the directory the lanes live under")
	runID := fs.String("run", "", "the run whose session is continued: its id, lane, brief and fence are reused")
	requestPath := fs.String("request", "", "the run.request to re-execute (default <lane-root>/.requests/<run-id>.request.json, where the dispatch wrote it)")
	runBin := fs.String("run-bin", "tillr-run", "the tillr-run binary the resume executes")
	backendsDir := fs.String("backends-dir", "", "the directory the backend's extract-report lives in (default: the run face's own)")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: the wall clock, once)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *root == "" || *runID == "" {
		return fail(drain.Usagef("resume needs --root <repository> and --run <id>: the run is the thing continued, and the root is the boundary it is held to"))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if *laneRoot == "" {
		*laneRoot = filepath.Join(*root, ".tillr", "lanes")
	}
	at := time.Now
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(drain.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = func() time.Time { return parsed }
	}
	// A resume judges nothing, so the step set is the run face alone: an item-state or gate
	// binary here would be a step this face never calls.
	steps := &drain.ProcessSteps{
		RunBin: *runBin, Store: *path, LaneRoot: *laneRoot, Root: *root,
		BackendsDir: *backendsDir, Now: at,
	}
	spec := drain.ResumeSpec{RunID: *runID}
	if *requestPath != "" {
		request, err := readRequestRecord(*requestPath)
		if err != nil {
			return fail(err)
		}
		spec.Request = request
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := drain.Resume(ctx, drain.ResumeConfig{
		Root: *root, Store: *path, LaneRoot: *laneRoot,
		Steps: steps, Now: at,
	}, spec)
	if err != nil {
		return fail(err)
	}
	// The sibling lanes are the ones this store knows, not a count: a resume names them to
	// the bracket so another run's write into one is a violation.
	for _, violation := range result.Violations {
		fmt.Fprintf(os.Stderr, "resume: %s\n", violation.String())
	}
	body, err := json.Marshal(result.Run)
	if err != nil {
		return fail(drain.Internalf("encoding the resumed record: %v", err))
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "run.record",
		ID:            "run-record-" + result.Run.RunID,
		Producer:      record.Producer{System: record.System, Utility: run.Utility, Version: run.Version},
		Subject:       &record.Subject{Kind: "run", ID: result.Run.RunID},
		CreatedAt:     at().UTC(),
		Payload:       body,
	}
	raw, err := record.Encode(env)
	if err != nil {
		return fail(drain.Internalf("encoding the resume record: %v", err))
	}
	fmt.Println(string(raw))
	if len(result.Violations) > 0 {
		// A change outside the bracket is a completed negative decision, the same class the
		// loop's own boundary stop answers with.
		return record.ExitNegative
	}
	if result.Run.State == "exited" && result.Run.ExitCode != nil && *result.Run.ExitCode != 0 {
		return *result.Run.ExitCode
	}
	return record.ExitOK
}

// readRequestRecord reads the run.request a resume re-executes from a file: the record the
// dispatch wrote, decoded the way `run exec` decodes it.
func readRequestRecord(path string) (*run.Request, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, drain.Usagef("reading the request %s: %v", path, err)
	}
	env, err := record.Decode(raw)
	if err != nil {
		return nil, drain.Usagef("reading %s: %v", path, err)
	}
	if env.Kind != run.RequestKind {
		return nil, drain.Usagef("%s holds a %q record, want %s", path, env.Kind, run.RequestKind)
	}
	var req run.Request
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		return nil, drain.Usagef("reading the %s payload in %s: %v", run.RequestKind, path, err)
	}
	return &req, nil
}

// newRunner answers the runner the caller named. This lane has one: a record sink that
// captures the requests to a file instead of launching anything. A subprocess runner is a
// separate decision about how tillr-run is invoked, so the refusal says so rather than
// guessing.
func newRunner(name, output string) (drain.Runner, error) {
	switch name {
	case recordSinkName:
		if output == "" {
			return nil, drain.Usagef("the %s runner needs --runner-output <file>: it captures the requests to a file", recordSinkName)
		}
		return &recordSink{path: output}, nil
	default:
		return nil, drain.Usagef("unknown runner %q: this face has one runner, %s, and a runner that launches a process is a later lane",
			name, recordSinkName)
	}
}

// recordSink is the fixture runner design §5.5 names: it writes the tick's run.request
// records to one file, as a JSON array in decision order, and launches nothing.
//
// Each element is the whole record, not its payload, because step 3 of §5.9 carries a
// record: `run exec` reads a run.request record, so what the sink captures is exactly what
// it is handed to.
type recordSink struct {
	path string
}

// HandOver writes the requests the tick dispatched, and nothing else. A tick that
// dispatched nothing writes an empty array, which is the answer "no requests", not a
// missing file.
func (s *recordSink) HandOver(requests []record.Envelope) error {
	raw, err := json.MarshalIndent(requests, "", "  ")
	if err != nil {
		return drain.Internalf("encoding the run requests for %s: %v", s.path, err)
	}
	if err := os.WriteFile(s.path, append(raw, '\n'), 0o644); err != nil {
		return drain.Externalf("the record sink could not write %s: %v", s.path, err)
	}
	return nil
}

// readPass reads the drain.pass record the caller named. A record of another kind is
// malformed input for this face, not a decision.
func readPass(path string) (drain.Pass, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return drain.Pass{}, drain.Usagef("reading %s: %v", path, err)
	}
	env, err := record.Decode(raw)
	if err != nil {
		return drain.Pass{}, drain.Usagef("%v", err)
	}
	if env.Kind != drain.PassKind {
		return drain.Pass{}, drain.Usagef("plan consumes %s, not %q", drain.PassKind, env.Kind)
	}
	var tick drain.Pass
	if err := json.Unmarshal(env.Payload, &tick); err != nil {
		return drain.Pass{}, drain.Usagef("reading the %s payload: %v", drain.PassKind, err)
	}
	if tick.Now == "" {
		tick.Now = env.CreatedAt.Format(time.RFC3339)
	}
	return tick, nil
}

// readPolicy reads the policy document the tick names, and answers nil for a tick that
// names none: a tick without a policy is not an error, it is a tick that can dispatch
// nothing, and every otherwise-dispatchable item holds with reason `no-policy`.
func readPolicy(ref string) (*drain.Policy, error) {
	return drain.LoadPolicy(ref)
}

// The two flags the question hold is configured by. They are shared because the root and
// the index command mean the same thing on every face that plans: a document reference
// resolves against the root, and the program that answers the index is one tool.
const (
	questionsRootHelp = "the repository the items' design_doc references resolve against"
	questionsCmdHelp  = "the program that answers Vantage's index, as one command line (default: " +
		"uvx vantage-check index --format json). An explicit override replaces the default. " +
		"A program that is not there holds the affected items with " +
		drain.ReasonQuestionsUnknown + " rather than dispatching them"
)

// commandFields turns one command line into the argv the reader runs. A blank value is no
// program named at all, which the reader answers by running its default.
func commandFields(line string) []string { return strings.Fields(line) }

// writeDecisions writes the tick's decisions as one JSON array of records, indented so
// two ticks diff cleanly. The file is created only here, after every decision is
// computed.
func writeDecisions(path string, decisions []record.Envelope) error {
	raw, err := json.MarshalIndent(decisions, "", "  ")
	if err != nil {
		return drain.Internalf("encoding the decisions: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return drain.Internalf("writing %s: %v", path, err)
	}
	return nil
}

// openFailure classifies a store that would not open. A store that is not there is the
// caller's path, not a defect in this binary.
func openFailure(err error) error {
	return drain.Incompatiblef("%v", err)
}

// fail prints one diagnostic on stderr and answers the code the error was made with. A
// face that answered `--help` already wrote its flags to stdout, so it answers 0 and
// prints nothing.
func fail(err error) int {
	if errors.Is(err, errHelp) {
		return record.ExitOK
	}
	fmt.Fprintln(os.Stderr, err)
	return drain.ExitCode(err)
}

// failLoopStartup answers a loop that cannot start: it writes the terminal record under the
// root the caller named, then fails the way every face does. The record is the point — a
// loop that printed four lines to stderr and exited left nothing under <root>/.tillr/loop, so
// "why did the night never start" was answerable only from the terminal of whoever launched
// it, and only until they closed it.
func failLoopStartup(root string, at time.Time, err error) int {
	if root != "" {
		drain.RecordStartupFault(root, at, err, os.Stderr)
	}
	return fail(err)
}

// faceList is this binary's faces, in the order the usage lines print them. One home,
// so the refusal, the unknown-face line and `--help` cannot disagree.
const faceList = "probe-backend, workflow, why, policy, plan, once, split, retry, clear-fault, accept-rewrite, acknowledge-patch-move, push, sweep-refs, acquit, examine, set-aside, boundary, resume, scale, stop, restart or loop"

func scaleFace(args []string) int {
	fs := newFlagSet("scale")
	root := fs.String("root", "", "the repository whose ordinary lanes should change")
	path := fs.String("store", "", "the loop's store (default: <root>/.tillr/state.db)")
	lanes := fs.Int("lanes", -1, "ordinary lane target (0 stops new ordinary starts)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" {
		if *root == "" {
			return fail(drain.Usagef("scale needs --root <repository> or --store <path>"))
		}
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if err := drain.Scale(context.Background(), *path, *lanes); err != nil {
		return fail(err)
	}
	fmt.Printf("loop: saved ordinary lane target %d; takes effect on the next pass or start\n", *lanes)
	return record.ExitOK
}

func stopFace(args []string) int {
	fs := newFlagSet("stop")
	root := fs.String("root", "", "the repository whose loop should stop")
	path := fs.String("store", "", "the loop's store (default: <root>/.tillr/state.db)")
	endRuns := fs.Bool("end-runs", false, "also end every in-flight run without spending an item attempt")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *root == "" {
		return fail(drain.Usagef("stop needs --root <repository>"))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	report, err := drain.Stop(context.Background(), drain.StopConfig{Store: *path, EndRuns: *endRuns})
	for _, line := range report.Skipped {
		fmt.Fprintf(os.Stderr, "loop: stop left alone: %s\n", line)
	}
	if err != nil {
		return fail(err)
	}
	if *endRuns {
		fmt.Println("loop: stopped; in-flight runs were ended without an attempt outcome")
	} else {
		fmt.Println("loop: stopped; in-flight runs were left alone")
	}
	return record.ExitOK
}

// errHelp is what parseFlags answers for a `--help`. A face's flags are the layer-1
// surface (docs/design/tillr-distribution.md Section 3), so a help request is an answer
// and not a usage error: fail turns it into exit 0 and prints nothing.
var errHelp = errors.New("help requested")

func newFlagSet(face string) *flag.FlagSet {
	fs := flag.NewFlagSet("tillr-drain "+face, flag.ContinueOnError)
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
		return drain.Usagef("%v", err)
	}
	if fs.NArg() > 0 {
		return drain.Usagef("unexpected argument %q", fs.Arg(0))
	}
	return nil
}

// printHelp writes one face's flags to stdout, under the face's own name: a reader
// learns a flag from the binary and never from a document.
func printHelp(fs *flag.FlagSet) {
	fs.SetOutput(os.Stdout)
	fs.Usage()
}

// loopFace is `drain loop`: the unattended supervisor. It runs ticks while work is
// dispatchable and waits otherwise by default, until an operator stops it or a fault it
// cannot route around. It answers that condition's exit status with a reason on stdout.
// There is no tick, spend or
// token budget: the pile is the only throttle (docs/design/momentum-principle.md).
//
// The four programs it drives are the caller's, because three of them judge work this
// utility does not: `tillr run exec` executes a run, and the gate, land and verdict
// programs turn a candidate into landed work. The loop owns the order, the boundary between
// the steps and the stopping conditions — never a step's judgment.
func loopFace(args []string) int {
	fs := newFlagSet("loop")
	check := fs.Bool("check", false, "validate flags, settings, policies, step programs and any required store migration without committing it or starting a pass")
	migrateOwner := fs.Bool("migrate-owner-questions", false, "convert existing owner decision reports into gated document questions, print each conversion or skipped report, then exit")
	exitWhenDrained := fs.Bool("exit-when-drained", false, "exit with queue-drained when nothing is dispatchable and no run is in flight; the loop is a daemon by default")
	root := fs.String("root", "", "the repository the loop stages lanes from and lands onto")
	path := fs.String("store", "", "the store to reap and plan from (default <root>/.tillr/state.db)")
	laneCount := fs.Int("lanes", 2, "ordinary lane cap (reserved express lanes are configured separately)")
	laneRoot := fs.String("lane-root", "", "where the lanes' worktrees live (default <root>/.tillr/lanes)")
	backoffBase := fs.Duration("backoff-base", drain.DefaultBackoffBase, "the wait before the next attempt after the first consecutive infrastructure failure; each further one doubles it, up to 5m. It is a wait and not a stop: the loop reports the failure and keeps trying, because the pile still holds work")
	fallbackPoll := fs.Duration("fallback-poll", drain.DefaultFallbackPollInterval, "fallback store reconciliation poll interval when no relayed notification arrives (D-SN6)")
	noNotify := fs.Bool("no-notify", false, "disable local notification relay subscription and rely on fallback polling only (D-SN7)")
	policyPath := fs.String("policy", "", "the backend policy (default <root>/config/tillr/backend-policy.json)")
	runBin := fs.String("run-bin", "tillr-run", "the tillr-run binary the run step executes")
	itemBin := fs.String("item-bin", "tillr-item", "the tillr-item binary the item-state step executes: a landed candidate's item is `landed`, and a run that reports `blocked` leaves its item `blocked`")
	briefTemplate := fs.String("brief-template", "", "the tracked brief template staging renders each run's brief from (default <root>/tools/tillr-run/backends/brief-template.md)")
	gate := fs.String("gate", "", "the gate program: exit 0 pass, 1 fail, anything else could not judge")
	land := fs.String("land", "", "the land program: exit 0 landed, 1 not landed")
	verdict := fs.String("verdict", "", "the verdict program: records the item's verdict, named by --verdict")
	replay := fs.String("replay", drain.DefaultReplayProgram, "the program that re-runs a red gate's failing checks on the base, so a trunk-red failure acquits the item instead of convicting it; {only} is the comma-separated failing check ids. Empty disables the acquittal")
	heavyGate := fs.String("heavy-gate", "tillr-gate", "the declaration judge for after-merge heavy runs")
	rolling := fs.String("rolling", drain.DefaultRollingProgram, "the program that runs the rolling tier over the trunk range; {since} is `--since <watermark>` or `--all`. Empty disables the rolling tier")
	fixerGate := fs.String("fixer-gate", drain.DefaultFixerGateProgram, "the named-check judge a trunk fixer runs after --gate; receives the candidate flags and --only <proof selector>")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: the wall clock, once per tick)")
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
	questionsIndex := fs.String("questions-index", "", "saved Vantage index JSON for the advisory start view (default <store>.questions-index.json on landing loops)")
	if err := parseFlags(fs, args); err != nil {
		if err.Error() == "flag provided but not defined: -stay" {
			return fail(drain.Usagef("--stay was removed: the loop is a daemon by default; use --exit-when-drained to exit instead of waiting"))
		}
		return fail(err)
	}
	if *root == "" {
		// Nowhere to write the record: the root is what names it.
		return fail(drain.Usagef("loop needs --root <repository>"))
	}
	if !*check {
		if err := drain.AuthorizeCheckoutRoot(*root); err != nil {
			return fail(err)
		}
	}
	// The loop folds the same selected event kinds many times a second, often inside a
	// write transaction; with the narrow cache, each selected read is a tail read for its
	// keys without holding the whole log (mostly unselected run.child-session worker
	// records) in memory (store.CacheSelectedEvents).
	store.CacheSelectedEvents()
	failStartup := func(err error) int {
		if *check {
			return fail(err)
		}
		return failLoopStartup(*root, time.Now(), err)
	}
	// A missing store must not mask invalid repository settings, including
	// when --check is run before the first loop startup.
	repositorySettings, err := settings.Read(*root)
	if err != nil {
		return failStartup(drain.Usagef("%v", err))
	}
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	var checkVersion int
	if *check {
		var err error
		checkVersion, err = loopCheckVersion(context.Background(), *path)
		if err != nil {
			return fail(err)
		}
	}
	hooks, err := storeHooks(context.Background(), *path)
	if err != nil {
		return failStartup(err)
	}
	if hooks == nil && (*gate == "" || *land == "" || *verdict == "") {
		return failStartup(drain.Usagef("loop needs --gate, --land and --verdict: the three programs that judge a candidate, and the loop judges nothing itself"))
	}
	gatePolicy, absent, err := drain.ReadGatePolicy(*root)
	if err != nil {
		return failStartup(err)
	}
	// Check's failure must be one diagnostic, not startup chatter followed by a
	// refusal. Normal startup reports settings as soon as they have been read.
	if !*check {
		fmt.Fprintln(os.Stderr, repositorySettings.StartupLine())
		fmt.Fprintln(os.Stderr, gatePolicy.StartupLine(absent))
	}
	if *laneRoot == "" {
		*laneRoot = filepath.Join(*root, ".tillr", "lanes")
	}
	if *policyPath == "" {
		*policyPath = filepath.Join(*root, "config", "tillr", "backend-policy.json")
	}
	if *briefTemplate == "" {
		*briefTemplate = filepath.Join(*root, "tools", "tillr-run", "backends", "brief-template.md")
	}
	policy, err := readPolicy(*policyPath)
	if err != nil {
		return failStartup(err)
	}
	if policy == nil {
		return failStartup(drain.Usagef("loop needs a policy: %s names none", *policyPath))
	}
	at := time.Now
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return failStartup(drain.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = func() time.Time { return parsed }
	}
	steps := &drain.ProcessSteps{
		RunBin: *runBin, HeavyGateBin: *heavyGate, ItemBin: *itemBin, Store: *path, LaneRoot: *laneRoot, Root: *root, Now: at,
		GateProgram: *gate, LandProgram: *land, VerdictProgram: *verdict,
		ReplayProgram: *replay, RollingProgram: *rolling, FixerGateProgram: *fixerGate,
		SyncRoadmaps: repositorySettings.Sync.Roadmaps,
	}
	explicitSteps := map[string]bool{}
	fs.Visit(func(set *flag.Flag) { explicitSteps[set.Name] = true })
	steps.ConfigureHooks(hooks, explicitSteps)
	stager := &drain.LaneStager{
		Root: *root, LaneRoot: *laneRoot, Store: *path, Template: *briefTemplate, Now: at,
	}
	loop, err := drain.NewLoop(drain.LoopConfig{
		Root: *root, Store: *path, LaneRoot: *laneRoot, MigrateOwnerQuestions: *migrateOwner,
		Lanes: append(laneIDs(*laneCount), expressLaneIDs(repositorySettings.Lanes.Express)...), LiveScale: true, BackoffBase: *backoffBase, StorePollInterval: *fallbackPoll, NoNotify: *noNotify, ExitWhenDrained: *exitWhenDrained,
		Policy: policy, PolicyRef: *policyPath, QuestionsCmd: commandFields(*questionsCmd), QuestionsIndex: *questionsIndex,
		Steps: steps, Stage: stager, Now: at, Report: os.Stderr,
		Comments: repositorySettings.Comments, WorkflowDefaults: repositorySettings.WorkflowDefaults,
		CandidateSizeLimits: &repositorySettings.CandidateSize,
		Restart:             restartConfig(), PostLanding: repositorySettings,
	})
	if err != nil {
		return failStartup(err)
	}
	if err := checkLoopPrograms(steps, loop.StagedSchemaPrograms()); err != nil {
		return failStartup(err)
	}
	if *check {
		if checkVersion < store.SchemaVersion {
			if err := store.CheckMigration(context.Background(), *path); err != nil {
				return fail(drain.Incompatiblef("the store %s: checking migration from schema %d to %d: %v", *path, checkVersion, store.SchemaVersion, err))
			}
			// The roller keeps the other binaries staged until this drain activates them.
			fmt.Fprintln(os.Stdout, drain.SchemaRollCheckLine)
		}
		fmt.Fprintln(os.Stderr, repositorySettings.StartupLine())
		return record.ExitOK
	}
	// Signals stop supervision only. Detached run faces retain their own lifetime;
	// the explicit stop --end-runs request is the only drain path that ends them.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result, err := loop.Run(ctx)
	if err != nil {
		return fail(err)
	}
	fmt.Printf("loop: stopped after %d tick(s): %s — %s\n", result.Ticks, result.Stop, result.Detail)
	fmt.Printf("loop: dispatched %d, landed %d, rejected %d, infra failures %d\n",
		result.Dispatched, result.Landed, result.Rejected, result.InfraFailures)
	if result.TrunkAcquitted > 0 || result.RollingGreen > 0 || result.RollingRed > 0 {
		fmt.Printf("loop: trunk acquitted %d, rolling green %d, rolling red %d\n",
			result.TrunkAcquitted, result.RollingGreen, result.RollingRed)
	}
	// A drain that has landed nothing and refused every run it produced is a fact about
	// the loop's judging, not N unlucky items: it is named beside the totals rather than
	// left for the operator to infer from a rejection count.
	if result.RepeatedRefusal != nil {
		fmt.Printf("loop: repeated-refusal: %s\n", drain.RepeatedRefusalLine(result))
	}
	// The spend line always prints: the loop has no budget to compare it against — the
	// pile is the only throttle (docs/design/momentum-principle.md) — but an operator still
	// wants to know what the night cost, and that is a different thing from refusing to
	// spend it.
	fmt.Println(spendLine(result))
	if result.Stop == drain.StopOperatorStop {
		fmt.Println("loop: stopped by the operator")
	}
	return drain.StopExitCode(result.Stop)
}

// Check the staged tools with the successor's CLI. They are not on the old
// loop's live PATH yet, including when its pre-exec check launches this image.
func checkLoopPrograms(steps *drain.ProcessSteps, staged string) error {
	if info, err := os.Stat(staged); err == nil && info.IsDir() {
		path, set := os.LookupEnv("PATH")
		if err := os.Setenv("PATH", staged+string(os.PathListSeparator)+path); err != nil {
			return err
		}
		defer func() {
			if set {
				os.Setenv("PATH", path)
			} else {
				os.Unsetenv("PATH")
			}
		}()
	}
	return steps.CheckPrograms(context.Background(), os.Stderr)
}

// loopCheckVersion rejects unknown or newer schemas before checking store hooks.
func loopCheckVersion(ctx context.Context, path string) (int, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return 0, openFailure(err)
	}
	defer s.Close()
	version, err := s.SchemaVersion()
	if err != nil {
		return 0, drain.Incompatiblef("the store %s: %v", path, err)
	}
	if version < 1 || version > store.SchemaVersion {
		return 0, drain.Incompatiblef("the store %s: schema version %d; expected %d", path, version, store.SchemaVersion)
	}
	return version, nil
}

// storeHooks reads the declaration without migrating or creating a store. The
// reader and its transaction are closed before any hook process is started.
func storeHooks(ctx context.Context, path string) (*definition.Hooks, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil // An absent store uses the embedded, hook-free definition.
	} else if err != nil {
		return nil, openFailure(err)
	}
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return nil, openFailure(err)
	}
	defer s.Close()
	declaration, err := definition.ForStore(ctx, s)
	if err != nil {
		return nil, drain.Usagef("store definition: %v", err)
	}
	return declaration.Parsed.Hooks, nil
}

// spendLine reports the spend accumulated during the loop. It is a measurement and never a budget:
// there is no ceiling here to compare against, because a ceiling that stops good work is
// the wall docs/design/momentum-principle.md rules out. A run whose report carried no
// number is named rather than dropped: a total that omits it and says nothing reads as
// complete.
func spendLine(result drain.LoopResult) string {
	line := fmt.Sprintf("loop: spend $%f, %d tokens; %d unreported run(s)", result.SpendUSD, result.Tokens, result.StartedSpend.Unreported+result.CarriedSpend.Unreported)
	if result.SpendUnknown > 0 || result.CostUnknown > 0 {
		line += "; lower bound"
	}
	if result.SpendUnknown > 0 {
		line += fmt.Sprintf("; %d run(s) reported no token usage (first: %s)", result.SpendUnknown, result.SpendUnknownExample)
	}
	if result.CostUnknown > 0 {
		line += fmt.Sprintf("; %d run(s) reported no cost (first: %s)", result.CostUnknown, result.CostUnknownExample)
	}
	return line + "\nloop: spend started in this loop: " + result.StartedSpend.String() +
		"\nloop: spend carried over from earlier loops: " + result.CarriedSpend.String()
}

// expressLaneIDs names the reserved pool separately from the ordinary cap.
func expressLaneIDs(n int64) []string {
	var lanes []string
	for i := int64(1); i <= n; i++ {
		lanes = append(lanes, fmt.Sprintf("express-%d", i))
	}
	return lanes
}

// laneIDs answers the lane ids a loop with n lanes offers: lane-1 .. lane-n, in that order,
// because the tick's lane preference is the caller's list order.
func laneIDs(n int) []string {
	if n < 1 {
		n = 1
	}
	lanes := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		lanes = append(lanes, fmt.Sprintf("lane-%d", i))
	}
	return lanes
}

// restartFace files one request to replace the loop's process and prints its id. With
// --wait it then blocks until a loop answers and prints the answer. A request filed while
// no loop runs is answered by the next loop that starts.
func restartFace(args []string) int {
	fs := newFlagSet("restart")
	root := fs.String("root", "", "the repository whose loop should restart")
	path := fs.String("store", "", "the loop's store (default: <root>/.tillr/state.db)")
	wait := fs.Bool("wait", false, "block until the loop answers the request, then print the answer")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" {
		if *root == "" {
			return fail(drain.Usagef("restart needs --root <repository> or --store <path>"))
		}
		*path = filepath.Join(*root, ".tillr", "state.db")
	}
	if _, err := os.Stat(*path); err != nil {
		return fail(openFailure(err))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	request, err := restart.File(ctx, *path, restart.RoleDrain, time.Now())
	if err != nil {
		return fail(err)
	}
	fmt.Println(request.ID)
	if !*wait {
		return record.ExitOK
	}
	answer, err := restart.AwaitAnswer(ctx, *path, request.ID, 100*time.Millisecond)
	if err != nil {
		return fail(err)
	}
	fmt.Println(answer)
	if answer.Answer == restart.AnswerRefused {
		return 1
	}
	return record.ExitOK
}

// restartConfig is what lets this loop replace itself: the binary it was started from, as
// it is on disk now, and the arguments it was started with.
func restartConfig() *drain.RestartConfig {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	exe = strings.TrimSuffix(exe, " (deleted)")
	return &drain.RestartConfig{
		Executable: exe,
		Argv:       append([]string(nil), os.Args...),
		Check:      checkBinary(os.Args[1:]),
	}
}

// checkBinary runs a binary with the loop's own arguments and --check. Its failure is the
// last line the check printed, which is the one line a refusal records.
func checkBinary(args []string) func(context.Context, string) error {
	return func(ctx context.Context, executable string) error {
		out, err := exec.CommandContext(ctx, executable, append(append([]string(nil), args...), "--check")...).CombinedOutput()
		if err == nil {
			return nil
		}
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return err
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == record.ExitIncompatible {
			return drain.Incompatiblef("%s", detail)
		}
		return errors.New(detail)
	}
}
