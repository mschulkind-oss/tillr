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
	"github.com/mschulkind-oss/tillr-run/run"
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
	case "why":
		return whyFace(rest)
	case "policy":
		return policyFace(rest)
	case "plan":
		return planFace(rest)
	case "once":
		return onceFace(rest)
	case "clear-fault":
		return clearFaultFace(rest)
	case "retry":
		return retryFace(rest)
	case "boundary":
		return boundaryFace(rest)
	case "resume":
		return resumeFace(rest)
	case "stop":
		return stopFace(rest)
	case "loop":
		return loopFace(rest)
	default:
		return fail(drain.Usagef("unknown face %q: want %s", face, faceList))
	}
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
	// The policy is loaded before the store is read, so a tick naming an unreadable
	// policy is refused as malformed input rather than silently holding every item with
	// `no-policy`.
	policy, err := readPolicy(tick.PolicyRef)
	if err != nil {
		return fail(err)
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
	policy, err := readPolicy(tick.PolicyRef)
	if err != nil {
		return fail(err)
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
// and echoes it as a drain.retry record. It is the one write a retry needs: `plan` reads the
// authorization and decides, and AuthorizeRetry is where the claim's structural and
// judgment checks live, so the face itself adds no rule of its own.
//
// The store is opened write-capable and migrated, the way every write face in the suite
// opens one, but a path that does not exist is refused before the store is opened: a retry
// names an item and a run in a store that already exists, and creating one to hold a claim
// about nothing is the shape this refusal removes.
func retryFace(args []string) int {
	fs := newFlagSet("retry")
	path := fs.String("store", "", "the store to record the authorization in")
	itemID := fs.String("item", "", "the item whose run is retried")
	runID := fs.String("run", "", "the run the retry supersedes")
	by := fs.String("by", "", "who asked for the retry")
	why := fs.String("why", "", "why the retry is authorized")
	acknowledge := fs.String("acknowledge", "", "the outcome the retry acknowledges: gate-failed or unjudged (default: neither)")
	now := fs.String("now", "", "the injected clock, RFC 3339")
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
const faceList = "why, policy, plan, once, retry, clear-fault, boundary, resume, stop or loop"

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
	check := fs.Bool("check", false, "validate flags, settings, policies and the existing store, then exit without starting a pass or migrating the store")
	exitWhenDrained := fs.Bool("exit-when-drained", false, "exit with queue-drained when nothing is dispatchable and no run is in flight; the loop is a daemon by default")
	root := fs.String("root", "", "the repository the loop stages lanes from and lands onto")
	path := fs.String("store", "", "the store to reap and plan from (default <root>/.tillr/state.db)")
	laneCount := fs.Int("lanes", 2, "how many lanes the loop may dispatch onto")
	laneRoot := fs.String("lane-root", "", "where the lanes' worktrees live (default <root>/.tillr/lanes)")
	backoffBase := fs.Duration("backoff-base", drain.DefaultBackoffBase, "the wait before the next attempt after the first consecutive infrastructure failure; each further one doubles it, up to 5m. It is a wait and not a stop: the loop reports the failure and keeps trying, because the pile still holds work")
	policyPath := fs.String("policy", "", "the backend policy (default <root>/config/tillr/backend-policy.json)")
	runBin := fs.String("run-bin", "tillr-run", "the tillr-run binary the run step executes")
	itemBin := fs.String("item-bin", "tillr-item", "the tillr-item binary the item-state step executes: a landed candidate's item is `landed`, and a run that reports `blocked` leaves its item `blocked`")
	briefTemplate := fs.String("brief-template", "", "the tracked brief template staging renders each run's brief from (default <root>/tools/tillr-run/backends/brief-template.md)")
	gate := fs.String("gate", "", "the gate program: exit 0 pass, 1 fail, anything else could not judge")
	land := fs.String("land", "", "the land program: exit 0 landed, 1 not landed")
	verdict := fs.String("verdict", "", "the verdict program: records the item's verdict, named by --verdict")
	replay := fs.String("replay", drain.DefaultReplayProgram, "the program that re-runs a red gate's failing checks on the base, so a trunk-red failure acquits the item instead of convicting it; {only} is the comma-separated failing check ids. Empty disables the acquittal")
	rolling := fs.String("rolling", drain.DefaultRollingProgram, "the program that runs the rolling tier over the trunk range; {since} is `--since <watermark>` or `--all`. Empty disables the rolling tier")
	fixerGate := fs.String("fixer-gate", drain.DefaultFixerGateProgram, "the FULL untiered gate a fixer item runs instead of --gate; it is handed the candidate's flags like --gate, and its verdict record is what the land program reads; {green} is `--since <last_green_sha>` or `--all`. Empty leaves a fixer on --gate, which cannot prove the sibling breakage its range reaches")
	traceBin := fs.String("trace-bin", "", "the tillr-trace program the loop runs after each tick to index what the tick's runs recorded (default: the repository's tools/tillr-trace, the pack's copy, or PATH). An explicit empty value disables the index step")
	traceRoot := fs.String("trace-root", "", "the trace root the indexer writes (default: tillr-trace's own $TILLR_TRACE_ROOT or state home)")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: the wall clock, once per tick)")
	questionsCmd := fs.String("questions-cmd", "", questionsCmdHelp)
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
	failStartup := func(err error) int {
		if *check {
			return fail(err)
		}
		return failLoopStartup(*root, time.Now(), err)
	}
	if *gate == "" || *land == "" || *verdict == "" {
		return failStartup(drain.Usagef("loop needs --gate, --land and --verdict: the three programs that judge a candidate, and the loop judges nothing itself"))
	}
	repositorySettings, err := settings.Read(*root)
	if err != nil {
		return failStartup(drain.Usagef("%v", err))
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
	if *path == "" {
		*path = filepath.Join(*root, ".tillr", "state.db")
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
	traceBinNamed := false
	fs.Visit(func(set *flag.Flag) {
		if set.Name == "trace-bin" {
			traceBinNamed = true
		}
	})
	steps := &drain.ProcessSteps{
		RunBin: *runBin, ItemBin: *itemBin, Store: *path, LaneRoot: *laneRoot, Root: *root, Now: at,
		GateProgram: *gate, LandProgram: *land, VerdictProgram: *verdict,
		ReplayProgram: *replay, RollingProgram: *rolling, FixerGateProgram: *fixerGate,
		TraceBin: resolveTraceBin(*root, *traceBin, traceBinNamed), TraceRoot: *traceRoot,
	}
	stager := &drain.LaneStager{
		Root: *root, LaneRoot: *laneRoot, Store: *path, Template: *briefTemplate, Now: at,
	}
	loop, err := drain.NewLoop(drain.LoopConfig{
		Root: *root, Store: *path, LaneRoot: *laneRoot,
		Lanes: laneIDs(*laneCount), BackoffBase: *backoffBase, ExitWhenDrained: *exitWhenDrained,
		Policy: policy, PolicyRef: *policyPath, QuestionsCmd: commandFields(*questionsCmd),
		Steps: steps, Stage: stager, Now: at, Report: os.Stderr,
	})
	if err != nil {
		return failStartup(err)
	}
	if *check {
		s, err := store.OpenRead(context.Background(), *path)
		if err != nil {
			return fail(openFailure(err))
		}
		version, schemaErr := s.SchemaVersion()
		closeErr := s.Close()
		if schemaErr != nil {
			return fail(drain.Incompatiblef("the store %s: %v", *path, schemaErr))
		}
		if closeErr != nil {
			return fail(openFailure(closeErr))
		}
		if version != store.SchemaVersion {
			return fail(drain.Incompatiblef("the store %s: schema version %d; expected %d", *path, version, store.SchemaVersion))
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

// spendLine is what the loop spent, as one line. It is a measurement and never a budget:
// there is no ceiling here to compare against, because a ceiling that stops good work is
// the wall docs/design/momentum-principle.md rules out. A run whose report carried no
// number is named rather than dropped: a total that omits it and says nothing reads as
// complete.
func spendLine(result drain.LoopResult) string {
	line := fmt.Sprintf("loop: spend $%f, %d tokens", result.SpendUSD, result.Tokens)
	if result.SpendUnknown > 0 {
		line += fmt.Sprintf("; %d run(s) reported no token usage (first: %s)", result.SpendUnknown, result.SpendUnknownExample)
	}
	if result.CostUnknown > 0 {
		line += fmt.Sprintf("; %d run(s) reported no cost (first: %s)", result.CostUnknown, result.CostUnknownExample)
	}
	return line
}

// resolveTraceBin answers the tillr-trace program the loop indexes with. The indexer is a
// Python script beside the Go tree rather than a built binary, so it is not necessarily on
// PATH: the repository's own `tools/tillr-trace` is the canonical copy when the loop runs
// from a checkout, and the pack's `~/.local/share/tillr/tillr-trace` is the copy a delivered
// install has. An explicit --trace-bin always wins, and a caller that names a program that
// does not exist gets that program's own failure rather than a silent fallback.
//
// `named` and `given` are separate parameters because an absent program and a disabled one
// are different requests, and one string cannot carry both. `--trace-bin ""` is how an
// operator bounds the loop to the steps that judge work: it answers the empty string, which
// `ProcessSteps.Index` reads as "skip", instead of falling through to the search below and
// silently re-enabling the step the operator just turned off.
func resolveTraceBin(root, named string, given bool) string {
	if given {
		return named
	}
	candidates := []string{
		filepath.Join(root, "tools", "tillr-trace", "tillr-trace"),
		filepath.Join(os.Getenv("HOME"), ".local", "share", "tillr", "tillr-trace", "tillr-trace"),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if path, err := exec.LookPath("tillr-trace"); err == nil {
		return path
	}
	return "tillr-trace"
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
