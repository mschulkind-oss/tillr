// Command tillr-run executes one run request to a terminal run.record under a
// caller-named lane root, and reads the lane and run state back. It is the run utility
// of the tillr suite: main keeps the flags, the exit codes and the process lifecycle,
// and the run package keeps the schema, the kinds and the record↔row mapping.
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
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func main() { os.Exit(dispatch(os.Args[1:])) }

// dispatch runs one face and answers its exit code. It is not named run because the
// run package is, and one spelling per concept leaves the package the name.
func dispatch(args []string) int {
	if len(args) == 0 {
		return fail(run.Usagef("a face is required: %s", faceList))
	}
	if args[0] == "-h" || args[0] == "--help" {
		fmt.Printf("tillr-run: %s\nRun `tillr-run <face> --help` for one face's flags.\n", faceList)
		return record.ExitOK
	}
	face, rest := args[0], args[1:]
	switch face {
	case "exec":
		return execFace(rest)
	case "pause":
		return pauseFace(rest)
	case "request":
		return requestFace(rest)
	case "lanes":
		return lanesFace(rest)
	case "status":
		return statusFace(rest)
	case "release":
		return releaseFace(rest)
	default:
		return fail(run.Usagef("unknown face %q: want %s", face, faceList))
	}
}

// pauseFace writes a durable request; the executor observes it independently.
func pauseFace(args []string) int {
	fs := newFlagSet("pause")
	path := fs.String("store", "", "the store holding the run")
	id := fs.String("run", "", "the live run to pause")
	bound := fs.Int("pause-timeout-seconds", 120, "the straggler bound for an adapter that stops at a gap")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *id == "" || *bound <= 0 {
		return fail(run.Usagef("pause needs --store <path>, --run <id> and a positive --pause-timeout-seconds"))
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()
	if err := run.RequestPause(ctx, s, *id, time.Duration(*bound)*time.Second, time.Now()); err != nil {
		return fail(err)
	}
	fmt.Printf("pause requested for %s\n", *id)
	return record.ExitOK
}

// execFace runs one request and prints the record it produced.
func execFace(args []string) int {
	fs := newFlagSet("exec")
	path := fs.String("store", "", "the store to write")
	laneRoot := fs.String("lane-root", "", "the directory the run's lane lives under")
	input := fs.String("input", "", "the run.request to execute")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	resume := fs.Bool("resume", false, "continue the run from its lane's session id instead of starting cold: the run id, lane, brief and fence are reused, the run's log is appended to, and the store records a run.resume event on the same run (a request whose payload sets resume already asks for this)")
	backendsDir := fs.String("backends-dir", "", "the directory the backends' extract-report programs live in: the run's spend is read from the report its backend's extractor writes (default <repo>/tools/tillr-run/backends, derived from the git common dir)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *laneRoot == "" || *input == "" {
		return fail(run.Usagef("exec needs --store <path>, --lane-root <dir> and --input <run-request.json>"))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(run.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return fail(run.Usagef("reading %s: %v", *input, err))
	}
	env, err := record.Decode(raw)
	if err != nil {
		return fail(run.Usagef("%v", err))
	}
	if env.Kind != run.RequestKind {
		return fail(run.Usagef("exec consumes %s, not %q", run.RequestKind, env.Kind))
	}
	var req run.Request
	if err := json.Unmarshal(env.Payload, &req); err != nil {
		return fail(run.Usagef("reading the %s payload: %v", run.RequestKind, err))
	}
	// The record envelope is the design's carrier of the correlation id, so a request whose
	// payload names none (an older emitter, a hand-written record) still propagates the
	// envelope's value into the run.allocate event. The payload wins when both are present,
	// because it is the half the request itself was built from.
	if req.CorrelationID == "" {
		req.CorrelationID = env.CorrelationID
	}
	// The request payload says whether it is a resume (the re-dispatch a resume writes) and
	// the face's own flag says so for a hand-driven one. Either asks for the same execution;
	// the OR is what keeps the two spellings from disagreeing.
	if *resume {
		req.Resume = true
	}

	// A signal is the operator's stop, and it has to reach the child the same way the
	// timeout does: cancel the run's context, kill the process group through `run exec`'s
	// own Cancel, and let the terminal record and the lane release be written. An exec face
	// that took `context.Background()` here was deaf to the SIGTERM its parent sent it, so
	// the run's worker outlived the loop and its lane stayed leased.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s, err := store.Open(ctx, *path, true)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()

	if *backendsDir == "" {
		*backendsDir = defaultBackendsDir()
	}
	stored, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: *laneRoot, BackendsDir: *backendsDir, Now: at})
	if err != nil {
		// A run that could not start still produced its record: the record is the
		// answer to "what happened", and exit 4 says the answer is not a run.
		if stored.Kind != "" {
			if code := printRecord(stored); code != record.ExitOK {
				return code
			}
		}
		return fail(err)
	}
	if code := printRecord(stored); code != record.ExitOK {
		return code
	}
	return runExit(stored)
}

// runExit answers the exit code of the run the record describes.
//
// `exec` answers the run's own exit code, because a shell-driven caller's only portable
// signal is the exit status: `set -e`, a `&&` chain and a CI step all read 0 as a green, and
// a red run that answers 0 is a red run reported as a green one. The face's own failures keep
// their codes — 2 usage or malformed input, 3 an incompatible store, 4 an external dependency
// that prevented a decision, 5 a bug — so a caller tells "the run failed with 7" from "the
// face could not start" by the code alone.
//
// The two can collide, because a run may exit 2, 3, 4 or 5 itself. The discriminator is the
// record: a run that ran prints one on stdout, and a face that could not start a process
// prints a diagnostic on stderr and either no record or one that says failed-to-start. So a
// caller that needs to tell them apart reads the presence of the record, never its contents.
//
// A run with no exit code of its own answers 0: a timed-out run is a completed run whose
// outcome is in the record, and there is no status to propagate. A code the record could not
// express — a process killed by a signal records -1 — answers 255, which is what an exit
// status outside 0-255 becomes at the process boundary anyway.
func runExit(env record.Envelope) int {
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		return fail(run.Internalf("reading the %s payload: %v", run.RecordKind, err))
	}
	if rec.State != run.StateExited || rec.ExitCode == nil {
		return record.ExitOK
	}
	code := *rec.ExitCode
	if code < 0 || code > 255 {
		return 255
	}
	return code
}

// requestFace derives the run.request a dispatch carries and prints it, so a driver that will
// not run the loop has one producer of the record rather than a second spelling of it.
//
// The derivation is run.Backend.Request, the same function drain's policy calls, and the
// backend it derives from is the one the caller names: a request names exactly one backend,
// and which backend a dispatch gets — the item's override, the policy's default, a failover's
// next hop — is the emitter's decision (drain's A-POLICY: the policy's shape is drain's own),
// so this face does not re-spell it.
//
// The candidate's worktree, base and head are not flags here because a run.request carries
// none of them: they are what the gate and the land are judged against, and the loop hands
// them to those programs rather than to the run.
func requestFace(args []string) int {
	fs := newFlagSet("request")
	policyPath := fs.String("policy", "", "the backend policy document the request's adapter, model and bound are read from")
	backendName := fs.String("backend", "", "the policy's backend to derive the request for")
	itemID := fs.String("item", "", "the item the run is dispatched for")
	laneID := fs.String("lane", "", "the lane the run holds")
	runID := fs.String("run", "", "the run's id, which the route is drawn from")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *policyPath == "" || *itemID == "" || *laneID == "" || *runID == "" {
		return fail(run.Usagef("request needs --policy <policy.json>, --item <id>, --lane <lane-id> and --run <id>"))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(run.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	policy, err := readPolicy(*policyPath)
	if err != nil {
		return fail(err)
	}
	if *backendName == "" {
		return fail(run.Usagef("request needs --backend <name>: the policy's backends are %v (its default is %q)",
			backendNames(policy.Backends), policy.DefaultBackend))
	}
	backend, ok := policy.Backends[*backendName]
	if !ok {
		return fail(run.Usagef("the policy names no backend %q: want one of %v", *backendName, backendNames(policy.Backends)))
	}
	if missing := backend.Missing(); missing != "" {
		return fail(run.Usagef("the policy's backend %q %s", *backendName, missing))
	}
	env, err := run.RequestRecord(
		backend.Request(*itemID, *laneID, *runID, *backendName),
		record.Producer{System: record.System, Utility: run.Utility, Version: run.Version}, at)
	if err != nil {
		return fail(err)
	}
	return printRecord(env)
}

// policyDocument is the part of a backend policy a request is derived from: the backends and
// the one it dispatches to by default. The rest of the document — the preference a failover
// walks, the failover budget, the per-item overrides — decides *which* backend a dispatch
// gets, which is drain's and is why this face takes the backend as a flag.
//
// The backend's own values are run.Backend, so a field a request is built from is spelled
// once, in the package that builds it.
type policyDocument struct {
	DefaultBackend string                 `json:"default_backend"`
	Backends       map[string]run.Backend `json:"backends"`
}

// readPolicy reads the backend policy the caller named. A document this utility cannot read
// is the caller's input rather than an incompatible store: exit 2, with nothing printed.
func readPolicy(path string) (*policyDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, run.Usagef("reading the policy %s: %v", path, err)
	}
	var policy policyDocument
	if err := json.Unmarshal(raw, &policy); err != nil {
		return nil, run.Usagef("reading the policy %s: %v", path, err)
	}
	if len(policy.Backends) == 0 {
		return nil, run.Usagef("the policy %s names no backends: a run.request needs an adapter program, a model and a timeout", path)
	}
	return &policy, nil
}

// backendNames answers a policy's backend names in a stable order, so a refusal naming the
// candidates reads the same twice.
func backendNames(backends map[string]run.Backend) []string {
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// defaultBackendsDir answers where the backends' extractors live, derived from the git
// common directory rather than from the working directory: a run's cwd is inside its
// lane's worktree, so a relative default would resolve against the lane and find nothing.
// `<git-common-dir>/..` is the repository's root in a linked worktree as well, which is
// the same derivation the loop and the jails use. An unresolvable repository answers the
// empty string, which reads no spend rather than guessing a path.
func defaultBackendsDir() string {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dir), "tools", "tillr-run", "backends")
}

// lanesFace lists the store's lanes. It writes nothing: it reports the lease as it
// stands, and `release` is the face that frees one.
func lanesFace(args []string) int {
	fs := newFlagSet("lanes")
	path := fs.String("store", "", "the store to read")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" {
		return fail(run.Usagef("lanes needs --store <path>"))
	}
	env, err := readStore(*path, run.LanesRecord)
	if err != nil {
		return fail(err)
	}
	return printRecord(env)
}

// statusFace answers one run's stored state. A run the store does not hold is a
// negative decision rather than an error: the utility ran and the answer is no, and the
// record says so.
func statusFace(args []string) int {
	fs := newFlagSet("status")
	path := fs.String("store", "", "the store to read")
	runID := fs.String("run", "", "the run to report")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *runID == "" {
		return fail(run.Usagef("status needs --store <path> and --run <id>"))
	}
	var found bool
	env, err := readStore(*path, func(r *store.Reader) (record.Envelope, error) {
		env, ok, err := run.StatusRecord(r, *runID)
		found = ok
		return env, err
	})
	if err != nil {
		return fail(err)
	}
	if code := printRecord(env); code != record.ExitOK {
		return code
	}
	if !found {
		return record.ExitNegative
	}
	return record.ExitOK
}

// releaseFace frees one lane and records why: design §5.6's standalone release, for a
// lane whose run is terminal or whose lease has expired with no terminal record.
//
// A reason is required, and the requirement is checked here as well as in the package
// because it is a flag: a caller who forgot it should read which flag, not the rule the
// package states. A release the face declines — an unexpired lease, a lane holding no
// lease, a lane the store does not hold — is a completed negative decision: the record
// on stdout names the reason and the exit is 1, so a supervisor reads an answer rather
// than a diagnostic.
func releaseFace(args []string) int {
	fs := newFlagSet("release")
	path := fs.String("store", "", "the store to write")
	laneID := fs.String("lane", "", "the lane to release")
	reason := fs.String("reason", "", "why the lane is being released; required")
	now := fs.String("now", "", "the injected clock, RFC 3339")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *laneID == "" {
		return fail(run.Usagef("release needs --store <path> and --lane <lane-id>"))
	}
	if *reason == "" {
		return fail(run.Usagef("release needs --reason <string>: a lane freed without one is a lane nobody can account for"))
	}
	at := time.Now().UTC()
	if *now != "" {
		parsed, err := time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(run.Usagef("--now %q is not an RFC 3339 timestamp: %v", *now, err))
		}
		at = parsed.UTC()
	}
	ctx := context.Background()
	s, err := store.Open(ctx, *path, false)
	if err != nil {
		return fail(openFailure(err))
	}
	defer s.Close()

	release, err := run.ReleaseLane(ctx, s, *laneID, *reason, at)
	if err != nil {
		return fail(err)
	}
	env, err := run.ReleaseRecord(release)
	if err != nil {
		return fail(err)
	}
	if code := printRecord(env); code != record.ExitOK {
		return code
	}
	if release.Result != run.ReleaseReleased {
		return record.ExitNegative
	}
	return record.ExitOK
}

// readStore runs one read face inside one read transaction over the store the caller
// named. The store is opened read-only and never migrated: a read face that created a
// store would answer a question about a store that did not exist.
func readStore(path string, face func(*store.Reader) (record.Envelope, error)) (record.Envelope, error) {
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	defer s.Close()
	var env record.Envelope
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		env, err = face(r)
		return err
	}); err != nil {
		return record.Envelope{}, err
	}
	return env, nil
}

// printRecord writes one record to stdout and answers the exit code of the write.
func printRecord(env record.Envelope) int {
	raw, err := record.Encode(env)
	if err != nil {
		return fail(run.Internalf("encoding the record: %v", err))
	}
	fmt.Println(string(raw))
	return record.ExitOK
}

// faceList is this binary's faces, in the order the usage lines print them. One home,
// so the refusal, the unknown-face line and `--help` cannot disagree.
const faceList = "exec, pause, request, lanes, status or release"

// errHelp is what parseFlags answers for a `--help`. A face's flags are the layer-1
// surface (docs/design/tillr-distribution.md Section 3), so a help request is an answer
// and not a usage error: fail turns it into exit 0 and prints nothing.
var errHelp = errors.New("help requested")

func newFlagSet(face string) *flag.FlagSet {
	fs := flag.NewFlagSet("tillr-run "+face, flag.ContinueOnError)
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
		return run.Usagef("%v", err)
	}
	if fs.NArg() > 0 {
		return run.Usagef("unexpected argument %q", fs.Arg(0))
	}
	return nil
}

// printHelp writes one face's flags to stdout, under the face's own name: a reader
// learns a flag from the binary and never from a document.
func printHelp(fs *flag.FlagSet) {
	fs.SetOutput(os.Stdout)
	fs.Usage()
}
