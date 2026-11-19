package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
)

// ProcessSteps is the loop's real steps: each one a program the supervisor starts, which is
// the design's boundary (§5.9) rather than a convenience. `tillr run exec` is the run; the
// gate, land and verdict programs are the caller's, named on the loop's argv.
//
// A program step is named rather than hardcoded because the gate and the land are the
// caller's choice: `tillr-gate run --gate tools/fork-check.py` and `tillr land pass` take
// records this loop does not author (a gate.plan, an enqueued land.candidate), and a loop
// that invented those records would be a second, silent implementation of two utilities.
// The named program reads the candidate on its argv and answers with its exit code.
type ProcessSteps struct {
	hooks        *definition.Hooks
	hookCommands map[string][]string
	// RunBin is the `tillr-run` binary the run step executes: `run exec`.
	RunBin string
	// HeavyGateBin is the declaration judge used after a landing (default tillr-gate).
	HeavyGateBin         string
	heavyReplaySelectors sync.Map // stable span ID -> legacy kind:label selector
	// ItemBin is the `tillr-item` binary the item-state step executes: `item state`. It
	// is this utility's neighbour rather than the caller's program, for `RunBin`'s reason:
	// a run's execution and an item's state are both the tillr utilities' own, and only
	// the gate, the land and the verdict are judgments a caller supplies.
	ItemBin string
	// Store and LaneRoot are handed to `run exec`, which claims the lane in the store and
	// writes the run's log inside the lane directory.
	Store    string
	LaneRoot string
	// Root is the repository whose record directory each step's span is appended to:
	// <Root>/.tillr/loop/<run-id>/steps.jsonl, the directory the adapters write a run's
	// gate, land and verdict records into (LoopDir). Empty records no spans, which is what a
	// step set that drives no repository has; the loop's own face always names it.
	Root string
	// SyncRoadmaps is passed in order to every sync; empty reads all documents.
	SyncRoadmaps []string
	// BackendsDir is where the backend's extractor lives, handed to `run exec` so the
	// run's spend reaches the record. Empty asks `run exec` for its own default, which is
	// the repository's `tools/tillr-run/backends`; a case that wants a spend to see sets
	// it, because no fixture backend has an extractor in the real directory.
	BackendsDir string
	// Now is the injected clock `run exec` stamps the claim with.
	Now func() time.Time
	// GateProgram, LandProgram and VerdictProgram are the programs the loop runs, each in
	// the directory its judgment is about: the gate in the lane worktree, land and verdict
	// in the root.
	GateProgram    string
	LandProgram    string
	VerdictProgram string
	// ReplayProgram re-runs a named set of checks on one revision. It is how the loop
	// settles a red gate's suspicion on the base before it convicts the candidate. `{only}`
	// is replaced with the comma-separated failing check ids. Empty is a loop that cannot
	// replay, and a gate failure then takes the ordinary refusal (never an acquittal).
	ReplayProgram string
	// RollingProgram runs the rolling tier over a trunk range. `{since}` is replaced with
	// `--since <watermark>` (or `--all` on the first run). Empty is a loop with no rolling
	// tier; the watermark then never advances and no fixer is filed from a rolling red.
	RollingProgram string
	// FixerGateProgram is the full, UNTIERED gate a fixer runs instead of GateProgram.
	// `{green}` is replaced with `--since <last_green_sha>` (or `--all` when no rolling
	// run has proved a trunk commit yet). The exemption needs the full gate so a fixer
	// cannot pass while the sibling breakage its range reaches is still red; the landing
	// tier's deferred kinds are exactly what must run here. Empty leaves a fixer on the
	// ordinary gate program, which is a loop that has not configured the exemption.
	FixerGateProgram string
}

// candidateArgs is the candidate as the three programs read it: one flag per field, so a
// program takes what it needs and a stub ignores what it does not.
func candidateArgs(c Candidate, root string) []string {
	args := []string{
		"--item", c.ItemID,
		"--lane", c.LaneID,
		"--run", c.RunID,
		"--worktree", c.Worktree,
		"--base", c.BaseSHA,
		"--head", c.HeadSHA,
	}
	// The root is the repository a fork checkout is not: a fork item's worktree is the
	// fork's own checkout, so a step that derived its repository from the worktree would
	// land the patch branch in the fork instead of in this repository. Naming the root
	// keeps the land on the trunk's repository while the gate runs where the change is.
	if root != "" {
		args = append(args, "--root", root)
	}
	return args
}

// Run executes one run.request through `run exec` and answers the terminal record with the
// run's log text.
//
// The request travels as the record `run exec` consumes, written inside the lane directory
// — the lane's own target, so the file is the run's and not the loop's litter in the
// repository. The log is read from the record's own artifact_refs: it is where the report
// the attempt is classified from lives, and a log that is gone classifies as "no report"
// rather than failing the run.
func (s *ProcessSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	env, err := requestEnvelope(Decision{ItemID: req.ItemID, RunRequest: &req}, s.Now().UTC())
	if err != nil {
		return run.Record{}, "", err
	}
	body, err := record.Encode(env)
	if err != nil {
		return run.Record{}, "", Internalf("encoding the run.request record: %v", err)
	}
	// The request is written beside the lanes rather than inside one: a file in the lane's
	// worktree would be untracked work the model's `git add` could commit into its own
	// candidate, which is the loop's litter becoming the candidate's diff.
	requests := filepath.Join(s.LaneRoot, ".requests")
	if err := os.MkdirAll(requests, 0o755); err != nil {
		return run.Record{}, "", Externalf("creating the request directory %s: %v", requests, err)
	}
	input := filepath.Join(requests, req.RunID+".request.json")
	if err := os.WriteFile(input, body, 0o644); err != nil {
		return run.Record{}, "", Externalf("writing %s: %v", input, err)
	}
	args := []string{"exec",
		"--store", s.Store, "--lane-root", s.LaneRoot, "--input", input, "--now", s.Now().UTC().Format(time.RFC3339Nano)}
	// A resume is told to the face as well as carried in the request: the face's flag is the
	// hand-driven spelling of the same execution, and `run exec` ORs the two rather than
	// letting one silently win.
	if req.Resume {
		args = append(args, "--resume")
	}
	if s.BackendsDir != "" {
		args = append(args, "--backends-dir", s.BackendsDir)
	}
	// A run face owns its lifetime. Only an explicit stop or build migration
	// may signal it; canceling this wait never signals an already handed-off run.
	cmd := exec.Command(s.RunBin, args...)
	// A terminal Ctrl-C targets the foreground process group, not just the
	// supervisor. Keep the face outside that group as well as outside its context.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	storeIdentity, err := processStoreIdentity(s.Store)
	if err != nil {
		return run.Record{}, "", err
	}
	cmd.Env = append(withoutProcessEnv(os.Environ(), "TILLR_RUN_ID", "TILLR_LOOP_STORE", "TILLR_RUN_RESUME"), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+storeIdentity, fmt.Sprintf("TILLR_RUN_RESUME=%t", req.Resume))
	// Both streams are regular files: even stdout must survive replacement of
	// the loop. Close the parent's handles immediately after inheritance so the
	// loop only opens files to read once the run has finished.
	dir := s.runOutputDir(req.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return run.Record{}, "", Externalf("creating run output directory %s: %v", dir, err)
	}
	stdout, err := os.Create(filepath.Join(dir, RunRecordFile))
	if err != nil {
		return run.Record{}, "", Externalf("creating run record output: %v", err)
	}
	stderr, err := os.Create(filepath.Join(dir, RunStderrFile))
	if err != nil {
		stdout.Close()
		return run.Record{}, "", Externalf("creating run error output: %v", err)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	observation, observeErr := pipelineStart(s.Root, req.RunID, "graph")
	if observeErr != nil {
		stdout.Close()
		stderr.Close()
		return run.Record{}, "", observeErr
	}
	started := time.Now().UnixMilli()
	err = s.startRunFaceContext(ctx, cmd, req)
	stdout.Close()
	stderr.Close()
	if errors.Is(err, errRestartPending) {
		return run.Record{}, "", err
	}
	if err != nil && ctx.Err() != nil {
		return run.Record{}, "", ctx.Err()
	}
	if err == nil {
		finished := make(chan error, 1)
		go func() { finished <- cmd.Wait() }()
		select {
		case err = <-finished:
		case <-ctx.Done():
			// Abandon this caller's wait, not the run. The face retains its
			// output files and terminal bookkeeping for the replacement.
			return run.Record{}, "", ctx.Err()
		}
	}
	if observeErr := pipelineFinish(s.Root, req.RunID, observation); observeErr != nil {
		return run.Record{}, "", observeErr
	}
	diagnostic, readErr := runOutputTail(dir)
	if readErr != nil {
		return run.Record{}, "", readErr
	}
	if spanErr := s.recordStep(req.RunID, stepSpan("run", append([]string{s.RunBin}, args...), cmd, started, diagnostic)); spanErr != nil {
		return run.Record{}, "", spanErr
	}
	out, readErr := os.ReadFile(filepath.Join(dir, RunRecordFile))
	if readErr != nil {
		return run.Record{}, "", Externalf("reading run record output: %v", readErr)
	}
	if err != nil && len(out) == 0 {
		return run.Record{}, "", Externalf("%s exec could not run %s: %v: %s",
			s.RunBin, req.RunID, err, diagnostic)
	}
	rec, log, decodeErr := decodeRunRecord(out)
	if decodeErr != nil {
		return run.Record{}, "", decodeErr
	}
	return rec, log, nil
}

// decodeRunRecord reads the record `run exec` prints and the log it points at.
func decodeRunRecord(out []byte) (run.Record, string, error) {
	env, err := record.Decode(out)
	if err != nil {
		return run.Record{}, "", Internalf("reading the run record: %v", err)
	}
	if env.Kind != run.RecordKind {
		return run.Record{}, "", Internalf("the run face answered a %q record, want %s", env.Kind, run.RecordKind)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		return run.Record{}, "", Internalf("reading the run.record payload: %v", err)
	}
	return rec, logTail(rec.ArtifactRefs), nil
}

// Gate runs the gate program in the candidate's worktree and maps its exit onto the gate's
// outcomes: 0 is a pass, 1 is a failing verdict about the code, 6 is a recorded stall,
// and every other exit
// — including a signal or a missing program — is "could not judge".
//
// The step's judgment is appended to the store before it is answered with: the plan and the
// verdict are what a reader reconstructing the run needs between `run.finish` and
// `land.enqueue`, and a loop that judged a candidate and recorded nothing leaves exactly the
// hole the event log exists to close. An append that fails fails the step — the loop then
// stops with the gate's judgment unrecorded rather than carrying on as if it were.
func (s *ProcessSteps) Gate(ctx context.Context, candidate Candidate) (GateOutcome, error) {
	// The spans path is the run's own record, and it travels in the environment rather
	// than on the command line: the gate program the caller names is a wrapper (the
	// documented one is `tillr-loop gate`) that composes the real gate call, and a flag it
	// did not declare would be refused before the gate ever saw it. `fork-check.py` reads
	// `TILLR_GATE_SPANS_OUT` when it is given no `--spans-out`, so the flag stays the
	// gate's own explicit spelling and the environment is the drain's hand-off.
	env := s.forkRootEnv()
	if s.Root != "" {
		for _, name := range []string{GateSpansFile, GateFastVerdictFile} {
			if err := os.Remove(filepath.Join(RunDir(s.Root, candidate.RunID), name)); err != nil && !os.IsNotExist(err) {
				return GateOutcome{}, Externalf("clearing preceding gate measurements: %v", err)
			}
		}
		env = append(env, "TILLR_GATE_SPANS_OUT="+filepath.Join(RunDir(s.Root, candidate.RunID), GateSpansFile),
			"TILLR_FAST_VERDICT_OUT="+filepath.Join(RunDir(s.Root, candidate.RunID), GateFastVerdictFile))
	}
	program := s.GateProgram
	if fork := strings.TrimPrefix(candidate.BaseRef, "refs/heads/patch/"); s.hooks == nil && fork != candidate.BaseRef {
		program = "python3 " + filepath.Join(s.Root, "tools", "tillr-drain", "fork-gate.py") + " --fork " + fork
	}
	if IsTrunkFixer(candidate.ItemID) && s.FixerGateProgram != "" {
		program = s.fixerGate(candidate)
	}
	code, _, span, err := s.programWithEnv(ctx, "gate", candidate.Worktree, program, candidate, env)
	if err != nil {
		return GateOutcome{}, err
	}
	outcome := GateOutcome{
		Outcome:    GateErrored,
		DurationMS: span.FinishedAtMS - span.StartedAtMS,
	}
	switch code {
	case record.ExitOK:
		outcome.Outcome = GatePassed
		if s.Root != "" {
			if payload, ok := readRecordPayload(filepath.Join(RunDir(s.Root, candidate.RunID), GateVerdictRecord)); ok {
				var v struct {
					Result            string             `json:"result"`
					InheritedFailures []InheritedFailure `json:"inherited_failures"`
				}
				if json.Unmarshal(payload, &v) == nil && v.Result == GatePassedInherited {
					outcome.Outcome = GatePassedInherited
					outcome.InheritedFailures = v.InheritedFailures
				}
			}
		}
	case GateStallExit:
		outcome.Outcome = GateStalled
		outcome.StalledChecks, err = s.gateStallContext(candidate)
		if err != nil {
			return GateOutcome{}, err
		}
	case record.ExitNegative:
		outcome.Outcome = GateFailed
		// A red gate carries the facts the trunk replay and the fixer need. Reading them
		// here, from the records the gate just wrote, keeps the loop from re-parsing a
		// program's stdout to find out what it judged.
		outcome.FailingChecks, outcome.SuspectPaths, outcome.CheckWatches = s.gateFailureContext(candidate)
		outcome.CandidateChangedPaths = outcome.SuspectPaths
		outcome.CheckReads = s.gateCheckReads(candidate, outcome.CheckWatches)
	}
	var firstStall *gateStallHold
	if outcome.Outcome == GateStalled {
		firstStall, err = s.firstStallHold(ctx, candidate, outcome.StalledChecks)
		if err != nil {
			return GateOutcome{}, err
		}
	}
	if err := s.recordGateJudgment(ctx, candidate, gateStepJudgment{
		FirstStallHold: firstStall,
		TickID:         candidate.TickID, Run: runRef{RunID: candidate.RunID, ItemID: candidate.ItemID, LaneID: candidate.LaneID},
		Outcome: outcome.Outcome, Program: span.Argv, ExitCode: exitCode(span),
		DurationMS: outcome.DurationMS,
	}); err != nil {
		return GateOutcome{}, err
	}
	outcome.StallHeld = firstStall != nil
	return outcome, nil
}

// LandOutcome is what the land program answered: the result the lander recorded, the
// reason it gave, and the conflict paths a squash conflict reported. The record is the
// lander's own, so the drain reads the outcome instead of inferring it from an exit code:
// a squash conflict and a refused landing both exit 1, and only the record says which.
//
// ConflictPaths is the lander's `conflict_paths` field, which carries the CONFLICT lines
// git reported rather than a list of bare paths. A conflict-fix run's brief names both the
// paths that can be parsed out of those lines and the lines themselves.
type LandOutcome struct {
	TrunkAfter      string
	LockObservation *LandObservation
	RedCap          *redcap.Hold
	Result          string
	Reason          string
	ConflictPaths   []string
}

// Land runs the land program in the repository and maps its answer: 0 is a landing, 1 is a
// completed negative answer (a refusal or a squash conflict), and anything else is the step
// failing. The record the lander prints on stdout is what distinguishes a conflict from a
// refusal and carries the conflict paths; a land program that printed no record falls back
// to its exit code, which is what a stub is.
func (s *ProcessSteps) Land(ctx context.Context, candidate Candidate) (LandOutcome, error) {
	extra := landArgs(candidate)
	code, out, span, err := s.programWithEnv(ctx, "land", ".", s.LandProgram, candidate, []string{"TILLR_LAND_OBSERVE=1"}, extra...)
	if err != nil {
		return LandOutcome{}, err
	}
	outcome := LandOutcome{}
	switch code {
	case record.ExitOK:
		outcome.Result = LandLanded
	case record.ExitNegative:
		outcome.Result = LandRefused
	default:
		return LandOutcome{}, Externalf("the land program answered exit %d for %s%s", code, candidate.ItemID, stderrReason(span))
	}
	if strings.TrimSpace(out) == "" {
		return outcome, nil
	}
	env, err := record.Decode([]byte(out))
	if err != nil {
		return LandOutcome{}, Internalf("reading the land record for %s: %v", candidate.ItemID, err)
	}
	var landing struct {
		TrunkAfter      string           `json:"trunk_after"`
		LockObservation *LandObservation `json:"lock_observation"`
		RedCap          *redcap.Hold     `json:"red_cap"`
		Result          string           `json:"result"`
		Reason          string           `json:"reason"`
		ConflictPaths   []string         `json:"conflict_paths"`
	}
	if err := json.Unmarshal(env.Payload, &landing); err != nil {
		return LandOutcome{}, Internalf("reading the land payload for %s: %v", candidate.ItemID, err)
	}
	if landing.Result != "" {
		outcome.Result = landing.Result
	}
	outcome.TrunkAfter = landing.TrunkAfter
	outcome.RedCap = landing.RedCap
	outcome.Reason = landing.Reason
	outcome.ConflictPaths = landing.ConflictPaths
	outcome.LockObservation = landing.LockObservation
	if err := s.saveLandObservation(candidate.RunID, landing.LockObservation); err != nil {
		return LandOutcome{}, landingMeasurementError(err)
	}
	return outcome, nil
}

// Verdict runs the verdict program, which records the item's verdict. A program that fails
// fails the step: a landed candidate whose item was not retired would be landed work the
// board cannot see.
func (s *ProcessSteps) Verdict(ctx context.Context, candidate Candidate, verdict string) error {
	// The store is passed explicitly because the verdict program records against the item's
	// frozen criteria, and it must read the same store the loop planned from: a verdict
	// recorded into a default path the loop never touched is a verdict nobody will find.
	code, _, span, err := s.program(ctx, "verdict", ".", s.VerdictProgram, candidate, s.verdictArgs(verdict)...)
	if err != nil {
		return err
	}
	if code != record.ExitOK {
		return Externalf("the verdict program answered exit %d for %s (%s)%s", code, candidate.ItemID, verdict, stderrReason(span))
	}
	return nil
}

// ItemState moves one item's own state through `tillr-item state`, against the same store
// the loop planned from. A move that does not take fails the step: an item left in the
// queue after its candidate landed is the board disagreeing with the trunk, and an
// unattended night may not report a landing the board never recorded.
func (s *ProcessSteps) ItemState(ctx context.Context, itemID, runID, state string, waiting ...item.WaitingOn) error {
	var extra []string
	if len(waiting) > 0 {
		extra = append(extra, "--waiter", waiting[0].Waiter, "--reason", waiting[0].Reason)
	}
	return s.itemState(ctx, itemID, runID, state, extra)
}

func (s *ProcessSteps) itemState(ctx context.Context, itemID, runID, state string, extra []string) error {
	if s.ItemBin == "" {
		return Usagef("the loop was given no item binary: it cannot record where an item's work stands")
	}
	args := []string{"state",
		"--store", s.Store, "--item", itemID, "--state", state,
		"--now", s.Now().UTC().Format(time.RFC3339)}
	args = append(args, extra...)
	cmd := exec.CommandContext(ctx, s.ItemBin, args...)
	var stderr stderrTail
	cmd.Stderr = &stderr
	started := time.Now().UnixMilli()
	out, err := cmd.Output()
	if spanErr := s.recordStep(runID, stepSpan("item-state", append([]string{s.ItemBin}, args...), cmd, started, stderr.String())); spanErr != nil {
		return spanErr
	}
	if err != nil {
		var rejection struct {
			Kind    string `json:"kind"`
			Payload struct {
				Rule   string `json:"rule"`
				Reason string `json:"reason"`
			} `json:"payload"`
		}
		if json.Unmarshal(out, &rejection) == nil && rejection.Kind == "item.rejection" && rejection.Payload.Rule == ReasonCriteriaUnjudged {
			return &completionFault{ReasonCriteriaUnjudged, rejection.Payload.Reason}
		}
		return Externalf("%s state %s %s: %v: %s %s",
			s.ItemBin, itemID, state, err, stderr.String(), strings.TrimSpace(string(out)))
	}
	return nil
}

// exitCode reads a span's exit status: -1 when the program could not be started, which is the
// value a process that never ran answers with and is never a judgment about a candidate.
func exitCode(span StepSpan) int {
	if span.ExitCode == nil {
		return -1
	}
	return *span.ExitCode
}

// program runs one named program in a directory and answers its exit code, its stdout and
// its span. A program that cannot be started at all is an external failure, which stops the
// loop rather than being read as a verdict about a candidate.
func (s *ProcessSteps) program(ctx context.Context, step, dir, program string, candidate Candidate, extra ...string) (int, string, StepSpan, error) {
	return s.programWithEnv(ctx, step, dir, program, candidate, nil, extra...)
}

// programWithEnv is `program` plus the environment a step needs on top of the loop's own.
// It is separate rather than a variadic on `program` so a step that needs one names it in
// its own signature instead of every caller passing an empty slice.
func (s *ProcessSteps) programWithEnv(ctx context.Context, step, dir, program string, candidate Candidate, env []string, extra ...string) (int, string, StepSpan, error) {
	if program == "" {
		return 0, "", StepSpan{}, Usagef("the loop was given no program for this step: it cannot judge a candidate without one")
	}
	argv := s.programArgv(program, candidate, extra...)
	var cmd *exec.Cmd
	if s.isHookCommand(program) {
		// The definition selects this exact executable, even at the legacy
		// adapter path. Only CLI programs opt into the embedded adapter.
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		baseEnv := os.Environ()
		if step == "gate" {
			baseEnv = withoutProcessEnv(baseEnv, "TILLR_SQLITE_TRACE")
		}
		cmd.Env = append(baseEnv, env...)
	} else {
		baseEnv := env
		if step == "gate" {
			// The gate runs candidate checks, not the loop's store: strip
			// TILLR_SQLITE_TRACE so checks see the same environment as the
			// trunk proof and a worker's own test runs.
			cmd = s.stepCommand(ctx, argv, env)
			cmd.Env = withoutProcessEnv(cmd.Env, "TILLR_SQLITE_TRACE")
		} else {
			cmd = s.stepCommand(ctx, argv, baseEnv)
		}
	}
	cmd.Dir = dir
	var stderr stderrTail
	cmd.Stderr = &stderr
	if os.Getenv("TILLR_SQLITE_TRACE") == "1" {
		// The tail keeps what the step said; its trace goes where every other trace is.
		traces := &sqliteTraceForwarder{output: os.Stderr}
		cmd.Stderr = io.MultiWriter(&stderr, traces)
		defer traces.Flush()
	}
	var observation pipelineObservation
	if step == "gate" || step == "land" {
		var err error
		observation, err = pipelineStart(s.Root, candidate.RunID, step)
		if err != nil {
			return 0, "", StepSpan{}, err
		}
	}
	started := time.Now().UnixMilli()
	var stdout bytes.Buffer
	var err error
	recordedArgv := append([]string(nil), cmd.Args...)
	if step == "gate" {
		cmd.Stdout = &stdout
		err = runSupervisedTree(cmd)
	} else {
		var outBytes []byte
		outBytes, err = cmd.Output()
		stdout.Write(outBytes)
	}
	out := stdout.Bytes()
	var notStarted *heavyCommandNotStarted
	if errors.As(err, &notStarted) {
		span := stepSpan(step, recordedArgv, &exec.Cmd{Dir: cmd.Dir}, started, stderr.String())
		if observation.Step != "" {
			if obsErr := pipelineFinish(s.Root, candidate.RunID, observation); obsErr != nil {
				return 0, "", span, obsErr
			}
		}
		if spanErr := s.recordStep(candidate.RunID, span); spanErr != nil {
			return 0, "", span, spanErr
		}
		return 0, "", span, Externalf("running %q in %s: %s: %s", argv[0], dir, notStarted.detail, stderr.String())
	}
	span := stepSpan(step, recordedArgv, cmd, started, stderr.String())
	if observation.Step != "" {
		if err := pipelineFinish(s.Root, candidate.RunID, observation); err != nil {
			return 0, "", span, err
		}
	}
	if spanErr := s.recordStep(candidate.RunID, span); spanErr != nil {
		return 0, "", span, spanErr
	}
	if step != "land" && ctx.Err() != nil {
		return 0, "", span, ctx.Err()
	}
	if cmd.ProcessState == nil {
		return 0, "", span, Externalf("running %q in %s: %v: %s", argv[0], dir, err, stderr.String())
	}
	if step == "land" && ctx.Err() != nil && cmd.ProcessState.ExitCode() != record.ExitOK && cmd.ProcessState.ExitCode() != record.ExitNegative {
		return 0, "", span, ctx.Err()
	}
	if cmd.ProcessState.ExitCode() == record.ExitUsage {
		return record.ExitUsage, string(out), span, Usagef("configuration fault: %s program %q refused its arguments for %s%s", step, program, candidate.ItemID, stderrReason(span))
	}
	return cmd.ProcessState.ExitCode(), string(out), span, nil
}

// forkRootEnv is the repository root a step that drives `bin/pi-forks` must be told. `.forks/`
// is one gitignored directory at the main checkout, absent from every linked worktree, and the
// driver refuses to run from a linked worktree unless PI_FORKS_ROOT names the checkout that owns
// it. The rolling, replay and fixer-gate programs run in a linked worktree (a detached scratch
// worktree, or a candidate's lane), so the loop hands them its own repository, as `tillr-loop
// gate` does for the ordinary gate. It is set outright rather than inherited: a value left in
// the loop's shell must not point a run at another tree's checkouts. The driver's refusal stays:
// a lane still cannot run fork steps against a root nobody gave it.
func (s *ProcessSteps) forkRootEnv() []string {
	if s.Root == "" {
		return nil
	}
	return []string{"PI_FORKS_ROOT=" + s.Root}
}

// stderrReason is the tail a failed step's error carries: why the program said it failed, from
// the same stderr tail its span records. A program that said nothing adds nothing.
func stderrReason(span StepSpan) string {
	if reason := strings.TrimSpace(span.StderrTail); reason != "" {
		return ": " + reason
	}
	return ""
}

// stepSpan assembles one finished command's span. FinishedAtMS is read here rather than at the
// call site so a span's end is the moment the command's own wait returned.
func stepSpan(step string, argv []string, cmd *exec.Cmd, started int64, stderr string) StepSpan {
	return StepSpan{
		Step: step, Argv: argv, Cwd: cwdOf(cmd),
		StartedAtMS: started, FinishedAtMS: time.Now().UnixMilli(),
		ExitCode: exitCodeOf(cmd), StderrTail: stderr,
	}
}

// recordStep appends one step's span to the run's step file. A step that ran and cannot be
// recorded is reported as a failure of the step: the timeline is the reason the span is taken
// at all, and a run whose steps are half-recorded is the plausible record the observatory
// design exists to replace.
func (s *ProcessSteps) recordStep(runID string, span StepSpan) error {
	if s.Root == "" {
		return nil
	}
	return appendStepSpan(s.Root, runID, span)
}

// String is the step set as the report names it, so a log line says which programs judged a
// candidate.
func (s *ProcessSteps) String() string {
	return fmt.Sprintf("run=%s item=%s gate=%s land=%s verdict=%s",
		s.RunBin, s.ItemBin, s.GateProgram, s.LandProgram, s.VerdictProgram)
}

// gateFailureContext reads the verdict and plan the gate just wrote and answers the three
// facts a fixer needs: the failing check ids, the candidate's changed paths, and each
// check's watched paths.
//
// A missing or unreadable record is not an error here: the gate's exit is already the
// judgment, and a verdict that carried no check ids simply cannot be replayed or fenced.
// The answer is empty, and the caller's ordinary gate refusal stands.
func (s *ProcessSteps) gateFailureContext(candidate Candidate) ([]string, []string, map[string][]string) {
	checks := []string{}
	watches := map[string][]string{}
	if s.Root != "" {
		if payload, ok := readRecordPayload(filepath.Join(RunDir(s.Root, candidate.RunID), GateVerdictRecord)); ok {
			var verdict struct {
				Checks []struct {
					CheckID string `json:"check_id"`
					Result  string `json:"result"`
				} `json:"checks"`
			}
			if err := json.Unmarshal(payload, &verdict); err == nil {
				for _, check := range verdict.Checks {
					if check.Result == "fail" && check.CheckID != "" {
						checks = append(checks, check.CheckID)
					}
				}
			}
		}
		if payload, ok := readRecordPayload(filepath.Join(RunDir(s.Root, candidate.RunID), GatePlanRecord)); ok {
			var plan struct {
				Checks []struct {
					CheckID string   `json:"check_id"`
					Reads   []string `json:"reads"`
					Writes  []string `json:"writes"`
				} `json:"checks"`
			}
			if err := json.Unmarshal(payload, &plan); err == nil {
				for _, check := range plan.Checks {
					watched := append(append([]string{}, check.Reads...), check.Writes...)
					watches[check.CheckID] = watched
				}
			}
		}
	}
	// The outer verdict judges the wrapper. Its fast verdict and remaining spans
	// name the checks that actually failed, which are the ids replay understands.
	if s.Root != "" {
		fast, state := readFastGateSpans(filepath.Join(RunDir(s.Root, candidate.RunID), GateFastVerdictFile))
		actual := []string{}
		if state == GateSpansRead {
			for _, check := range fast {
				if check.Status == "fail" {
					actual = append(actual, check.CheckID)
				}
			}
		}
		remaining, remainingWatches := readFailingSpans(filepath.Join(RunDir(s.Root, candidate.RunID), GateSpansFile))
		actual = append(actual, remaining...)
		for id, paths := range remainingWatches {
			watches[id] = paths
		}
		if len(actual) > 0 {
			checks = actual
		}
		// Fast tree declarations own their read sets. A wrapper's generated plan
		// need not hold their raw ids, so use that same declaration directory.
		declarations, _ := filepath.Glob(filepath.Join(candidate.Worktree, "tools/gate/checks/*.json"))
		for _, path := range declarations {
			body, err := os.ReadFile(path)
			var decl struct {
				ID     string   `json:"id"`
				Reads  []string `json:"reads"`
				Writes []string `json:"writes"`
			}
			if err == nil && json.Unmarshal(body, &decl) == nil {
				watches[decl.ID] = append(decl.Reads, decl.Writes...)
			}
		}
	}
	return checks, s.changedPaths(candidate), watches
}

// gateCheckReads resolves declared `reads` per check for OQ-GP7 path intersection.
// Checks in tools/gate/checks/*.json default undeclared `reads` to ["."] (whole-tree),
// matching tools/tillr-gate/checkfiles.go.
func (s *ProcessSteps) gateCheckReads(candidate Candidate, watches map[string][]string) map[string][]string {
	reads := map[string][]string{}
	for id, w := range watches {
		if len(w) > 0 {
			reads[id] = append([]string(nil), w...)
		}
	}
	if s.Root != "" {
		if payload, ok := readRecordPayload(filepath.Join(RunDir(s.Root, candidate.RunID), GatePlanRecord)); ok {
			var plan struct {
				Checks []struct {
					CheckID string   `json:"check_id"`
					Reads   []string `json:"reads"`
				} `json:"checks"`
			}
			if err := json.Unmarshal(payload, &plan); err == nil {
				for _, check := range plan.Checks {
					if len(check.Reads) > 0 {
						reads[check.CheckID] = append([]string(nil), check.Reads...)
					} else {
						reads[check.CheckID] = []string{"."}
					}
				}
			}
		}
	}
	if candidate.Worktree != "" {
		declarations, _ := filepath.Glob(filepath.Join(candidate.Worktree, "tools/gate/checks/*.json"))
		for _, path := range declarations {
			body, err := os.ReadFile(path)
			var decl struct {
				ID    string   `json:"id"`
				Reads []string `json:"reads"`
			}
			if err == nil && json.Unmarshal(body, &decl) == nil && decl.ID != "" {
				if len(decl.Reads) == 0 {
					reads[decl.ID] = []string{"."}
				} else {
					reads[decl.ID] = decl.Reads
				}
			}
		}
	}
	return reads
}

// readRecordPayload decodes one record file and answers its payload.
func readRecordPayload(path string) (json.RawMessage, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	env, err := record.Decode(body)
	if err != nil {
		return nil, false
	}
	return env.Payload, true
}

// changedPaths answers the candidate's changed paths, base..head, for a fixer's fence.
func (s *ProcessSteps) changedPaths(candidate Candidate) []string {
	base := candidate.BaseSHA
	if base == "" || candidate.Worktree == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", candidate.Worktree, "diff", "--name-only", "-z", base+".."+candidate.HeadSHA).Output()
	if err != nil {
		return nil
	}
	paths := make([]string, 0)
	for _, path := range strings.Split(string(out), "\x00") {
		if path = strings.TrimSpace(path); path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// Replay re-runs a named set of checks on one revision in a scratch worktree, and answers
// which of them are red. It is the trunk-already-red replay's program: it judges the base,
// never the candidate, so it takes no candidate verdict and writes no record.
func (s *ProcessSteps) Replay(ctx context.Context, candidate Candidate, rev string, checks []string) (ReplayOutcome, error) {
	if s.ReplayProgram == "" || len(checks) == 0 {
		return ReplayOutcome{Rev: rev, Known: false}, nil
	}
	dir, cleanup, err := s.replayWorktree(ctx, rev)
	if err != nil {
		return ReplayOutcome{Rev: rev, Known: false}, err
	}
	defer cleanup()
	out := ReplayOutcome{Rev: rev, Known: true}
	// A grouped exit 1 says only that SOME check failed. Judge each id so a
	// candidate's own red cannot hide behind a different red already on its base.
	for _, check := range checks {
		program := strings.ReplaceAll(s.ReplayProgram, "{only}", check)
		argv := s.commandArgv(program)
		if len(argv) == 0 {
			return ReplayOutcome{Rev: rev, Known: false}, nil
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir = dir
		cmd.Env = append(withoutProcessEnv(os.Environ(), "TILLR_SQLITE_TRACE"), s.forkRootEnv()...)
		var stderr stderrTail
		cmd.Stderr = &stderr
		runErr := runCheckGroup(cmd)
		if ctx.Err() != nil {
			return ReplayOutcome{Rev: rev, Known: false}, ctx.Err()
		}
		if cmd.ProcessState == nil {
			return ReplayOutcome{Rev: rev, Known: false}, Externalf("replaying %s on %s: %v: %s",
				check, shortSHA(rev), runErr, stderr.String())
		}
		switch cmd.ProcessState.ExitCode() {
		case 0:
		case 1:
			out.Red = append(out.Red, check)
		default:
			// Unknown is never evidence for acquittal.
			return ReplayOutcome{Rev: rev, Known: false}, nil
		}
	}
	return out, nil
}

// checkGroupGrace gives a check program time to end checks it owns in other sessions
// after SIGTERM, and bounds the final wait for its own killed process group.
const checkGroupGrace = 5 * time.Second

// runCheckGroup runs a check program that works in one of the loop's scratch worktrees, in
// a process group of its own, and ends that whole group before it returns, so the caller's
// deferred cleanup never removes the worktree under a process still working in it.
//
// The program is a gate: it starts test binaries, and they start their own children. A
// cancellation that reached only the program left those running — on 2026-11-17 a loop
// stop deleted the rolling worktree under a `go test -parallel 48 ./...` that kept going
// for two minutes. The program also owns checks in separate sessions: SIGTERM lets it
// end those before WaitDelay escalates to SIGKILL. On any exit, kill what remains in
// the program's own group before returning to the worktree cleanup.
func runCheckGroup(cmd *exec.Cmd) error { return runCheckGroupStarted(cmd, nil) }

// runCheckGroupStarted is runCheckGroup that tells started the command's pid once it
// has started; a command that never starts calls nothing.
func runCheckGroupStarted(cmd *exec.Cmd, started func(pid int)) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	// CommandContext starts this grace period on cancellation; only if the program
	// has not exited does it kill the leader. The final group cleanup below reaches
	// remaining pipe holders too.
	cmd.WaitDelay = checkGroupGrace
	err := startAndWait(cmd, started)
	if cmd.Process != nil {
		endProcessGroup(cmd.Process.Pid)
	}
	return err
}

// startAndWait is cmd.Run, telling started the pid between the start and the wait.
func startAndWait(cmd *exec.Cmd, started func(pid int)) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if started != nil {
		started(cmd.Process.Pid)
	}
	return cmd.Wait()
}

// endProcessGroup kills every process left in group pgid and waits, up to checkGroupGrace,
// until none is left.
func endProcessGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline := time.Now().Add(checkGroupGrace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// detachedWorktree checks one revision out at dir, a detached worktree of the loop's
// repository, and answers the cleanup that removes it. A step judges a worktree of s.Root
// rather than s.Root itself: a landing may move the trunk while the step runs, and a step
// that read the trunk's own checkout would read a tree something else rewrote underneath
// it.
func (s *ProcessSteps) detachedWorktree(ctx context.Context, dir, rev string) (string, func(), error) {
	if s.Root == "" {
		return "", func() {}, Usagef("the loop cannot check out %s: it was given no repository", shortSHA(rev))
	}
	// A worktree a previous run left behind is removed rather than reused: a stale
	// registration would refuse the add, and a reused directory is not provably the
	// revision this run was asked to judge.
	err := withWorktreeRegistryLock(ctx, s.Root, func() error {
		_ = exec.Command("git", "-C", s.Root, "worktree", "remove", "--force", dir).Run()
		_ = os.RemoveAll(dir)
		_ = exec.Command("git", "-C", s.Root, "worktree", "prune").Run()
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return Externalf("creating the worktree directory %s: %v", filepath.Dir(dir), err)
		}
		cmd := exec.CommandContext(ctx, "git", "-C", s.Root, "worktree", "add", "--detach", "--force", dir, rev)
		if out, err := cmd.CombinedOutput(); err != nil {
			return Externalf("checking out %s: %v: %s", shortSHA(rev), err, strings.TrimSpace(string(out)))
		}
		return nil
	})
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() {
		if err := withWorktreeRegistryLock(context.Background(), s.Root, func() error {
			_ = exec.Command("git", "-C", s.Root, "worktree", "remove", "--force", dir).Run()
			return os.RemoveAll(dir)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "tillr-drain: cleaning detached worktree %s: %v\n", dir, err)
		}
	}
	return dir, cleanup, nil
}

// replayWorktree checks one revision out into a detached scratch worktree under the lane
// root, and answers its path and the cleanup that removes it. It lives under the lane root
// because that is the loop's own writable state, never the trunk's checkout.
func (s *ProcessSteps) replayWorktree(ctx context.Context, rev string) (string, func(), error) {
	if s.LaneRoot == "" {
		return "", func() {}, Usagef("the loop cannot replay %s: it was given no lane root", shortSHA(rev))
	}
	return s.detachedWorktree(ctx, filepath.Join(s.LaneRoot, ".replay", shortSHA(rev)), rev)
}

// fixerGate answers the full gate program a fixer runs, with its range since the last
// green. No watermark yet is the whole tree (`--all`): a first fixer has nothing proved
// behind it, and a range from an empty base would plan nothing and pass.
func (s *ProcessSteps) fixerGate(candidate Candidate) string {
	green := strings.TrimSpace(candidate.FixerBaseSHA)
	if green == "" {
		return strings.ReplaceAll(s.FixerGateProgram, "{green}", "--all")
	}
	return strings.ReplaceAll(s.FixerGateProgram, "{green}", "--since "+green)
}

// RollingConfigured reports whether a rolling program is named.
func (s *ProcessSteps) RollingConfigured() bool {
	return s.RollingProgram != ""
}

// Rolling runs the rolling tier over base..tip and answers whether it was green.
//
// The run happens in a detached worktree of the tip, never in s.Root: a landing may move
// the trunk while a rolling run is in flight, and a run that judged the trunk's own
// checkout would see a half-landed tree. The worktree is removed when the run ends.
//
// A red run is a judgment only when it says what was red. The program writes the gate's
// own span file, named by TILLR_GATE_SPANS_OUT, and the failing checks are read from it. A
// run that failed without recording a failing span answers Known=false: it advances no
// watermark and files no fixer, rather than inventing an empty failure list.
func (s *ProcessSteps) Rolling(ctx context.Context, baseSHA, tipSHA string) (RollingOutcome, error) {
	if s.RollingProgram == "" {
		return RollingOutcome{Known: false}, nil
	}
	program := s.RollingProgram
	if baseSHA == "" {
		program = strings.ReplaceAll(program, "{since}", "--all")
	} else {
		program = strings.ReplaceAll(program, "{since}", "--since "+baseSHA)
	}
	argv := s.commandArgv(program)
	if len(argv) == 0 {
		return RollingOutcome{Known: false}, nil
	}
	ctx, releaseOwnership, err := rollingOwnership(ctx, s.Root)
	if err != nil {
		return RollingOutcome{Known: false}, err
	}
	defer releaseOwnership()
	dir, cleanup, err := s.rollingWorktree(ctx, tipSHA)
	if err != nil {
		return RollingOutcome{Known: false}, err
	}
	defer cleanup()
	spans := filepath.Join(s.LaneRoot, ".rolling", GateSpansFile)
	_ = os.Remove(spans)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(append(withoutProcessEnv(os.Environ(), "TILLR_SQLITE_TRACE"), s.forkRootEnv()...), "TILLR_GATE_SPANS_OUT="+spans)
	var stderr stderrTail
	cmd.Stderr = &stderr
	runErr := runOwnedHeavy(ctx, cmd, "legacy")
	if ctx.Err() != nil {
		return RollingOutcome{}, ctx.Err()
	}
	var notStarted *heavyCommandNotStarted
	if cmd.ProcessState == nil || errors.As(runErr, &notStarted) {
		return RollingOutcome{Known: false, ExitCode: -1,
			Detail: fmt.Sprintf("rolling program did not start: %v: %s", runErr, stderr.String())}, nil
	}
	code := cmd.ProcessState.ExitCode()
	coverage := readRollingCoverage(spans)
	var declared map[string][]string
	if code == 1 || code == 6 {
		declared, err = legacyCheckWatches(ctx, dir)
		if err != nil {
			return RollingOutcome{Known: false, ExitCode: code, Detail: err.Error() + "; " + stderr.String(), Coverage: coverage}, nil
		}
	}
	switch code {
	case 0:
		return RollingOutcome{Green: true, Known: true, ExitCode: code, Coverage: coverage}, nil
	case 6:
		stalled, watches, err := readSpansWithWatches(spans, "STALLED", declared)
		if err != nil {
			return RollingOutcome{Known: false, ExitCode: code, Detail: err.Error() + "; " + stderr.String(), Coverage: coverage}, nil
		}
		return RollingOutcome{Stalled: len(stalled) > 0, Known: false, ExitCode: code,
			FailingChecks: stalled, CheckWatches: watches, Coverage: coverage, Detail: stderr.String()}, nil
	case 1:
		failing, watches, err := readSpansWithWatches(spans, "FAIL", declared)
		if err != nil {
			return RollingOutcome{Known: false, ExitCode: code, Detail: err.Error() + "; " + stderr.String(), Coverage: coverage}, nil
		}
		if len(failing) == 0 {
			return RollingOutcome{Known: false, ExitCode: code, Coverage: coverage,
				Detail: "it named no failing check. " + stderr.String()}, nil
		}
		return RollingOutcome{Green: false, Known: true, ExitCode: code, FailingChecks: failing, CheckWatches: watches, Coverage: coverage}, nil
	default:
		return RollingOutcome{Known: false, ExitCode: code, Detail: stderr.String(), Coverage: coverage}, nil
	}
}

// rollingWorktree is the one detached worktree a rolling run judges: the tip, under the
// lane root, so the run cannot see the trunk move underneath it.
func (s *ProcessSteps) rollingWorktree(ctx context.Context, tipSHA string) (string, func(), error) {
	if s.LaneRoot == "" {
		return "", func() {}, Usagef("the loop cannot run the rolling tier: it was given no lane root")
	}
	return s.detachedWorktree(ctx, filepath.Join(s.LaneRoot, ".rolling", "worktree"), tipSHA)
}
