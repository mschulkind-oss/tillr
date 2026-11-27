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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
	"golang.org/x/sys/unix"
)

const (
	GateFaceFile = "gate.face.json"
	GateDoneFile = "gate.done.json"
	gateSpecFile = "gate.spec.json"
)

func init() {
	if len(os.Args) > 1 && os.Args[1] == "--tillr-gate-supervisor" {
		defer store.FlushSQLiteTraceAtExit()
		code := superviseDetachedGate(os.Args[2:])
		store.FlushSQLiteTraceAtExit()
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "--tillr-heavy-proof" {
		defer store.FlushSQLiteTraceAtExit()
		code := heavyrun.SuperviseHeavyProof(os.Args[2:], processStepsHeavyCallbacks())
		store.FlushSQLiteTraceAtExit()
		os.Exit(code)
	}
}

// ProcessSteps is the loop's real steps: each one a program the supervisor starts, which is
// the design's boundary (§5.9) rather than a convenience. `tillr run exec` is the run; the
// gate, land and verdict programs are the caller's, named on the loop's argv.
//
// A program step is named rather than hardcoded because the gate and the land are the
// caller's choice: `tillr-gate run --gate tools/gate/repo-gate` and `tillr land pass` take
// records this loop does not author (a gate.plan, an enqueued land.candidate), and a loop
// that invented those records would be a second, silent implementation of two utilities.
// The named program reads the candidate on its argv and answers with its exit code.
type ProcessSteps struct {
	hooks        *definition.Hooks
	hookCommands map[string][]string
	nonLanding   bool
	// RunBin is the `tillr-run` binary the run step executes: `run exec`.
	RunBin string
	// HeavyGateBin is the declaration judge used after a landing (default tillr-gate).
	HeavyGateBin           string
	heavyLogSampleInterval time.Duration
	heavyReplaySelectors   sync.Map // stable span ID -> legacy kind:label selector
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
	// QuestionsCmd is forwarded to `tillr-item sync --questions-cmd` when set.
	QuestionsCmd   []string
	syncRoadmapsMu sync.RWMutex
	// TrunkRef names the landing branch; empty reads .tillr.toml or defaults to main.
	TrunkRef string
	// BackendsDir is where the backend's extractor lives, handed to `run exec` so the
	// run's spend reaches the record. Empty omits `--backends-dir` so `run exec` resolves
	// its own installed backends directory.
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
	// FixerGateProgram judges one named legacy check after GateProgram has accepted
	// the candidate. The drain supplies --only; empty cannot judge legacy names.
	FixerGateProgram string
	priority         *ProcessPriority
	selfExe          string
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
func (*ProcessSteps) reportsWorkerProcessStart() {}

func (s *ProcessSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	endPrep := timeHandoffPhase(ctx, "prepare_worker_process")
	prepEnded := false
	finishPrep := func() {
		if !prepEnded {
			prepEnded = true
			endPrep()
		}
	}
	defer func() {
		finishPrep()
	}()
	if s.LaneRoot == "" {
		return run.Record{}, "", Usagef("ProcessSteps.Run requires LaneRoot")
	}
	if len(req.Command) > 0 && req.Backend != "command" && s.Root != "" {
		resolved := resolveAdapterPath(s.Root, req.Command[0])
		if resolved != req.Command[0] {
			req.Command = append([]string{resolved}, req.Command[1:]...)
		}
	}
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
	if s.LaneRoot == "" {
		// An empty lane root would put the request relative to the working directory, which
		// for a test is the source tree: the stray file failed the proof's pack-sync-test.
		return run.Record{}, "", Internalf("writing the run.request: no lane root is set")
	}
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
	backendsDir := s.BackendsDir
	if backendsDir != "" {
		if !filepath.IsAbs(backendsDir) && s.Root != "" {
			root := s.Root
			if absRoot, err := filepath.Abs(root); err == nil {
				root = absRoot
			}
			backendsDir = filepath.Join(root, backendsDir)
		}
		args = append(args, "--backends-dir", backendsDir)
	}
	// A run face owns its lifetime. Only an explicit stop or build migration
	// may signal it; canceling this wait never signals an already handed-off run.
	cmd := exec.Command(s.RunBin, args...)
	// A terminal Ctrl-C targets the foreground process group, not just the
	// supervisor. Keep the face outside that group as well as outside its context.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	runClass := ProcessClassWorker
	if IsTrunkFixer(req.ItemID) {
		runClass = ProcessClassRepair
	}
	if err := s.priority.ApplyToCmd(cmd, runClass, s.selfExe); err != nil {
		return run.Record{}, "", err
	}
	storeIdentity, err := processStoreIdentity(s.Store)
	if err != nil {
		return run.Record{}, "", err
	}
	runEnv := append(withoutProcessEnv(os.Environ(), "TILLR_RUN_ID", "TILLR_LOOP_STORE", "TILLR_RUN_RESUME", "TILLR_NON_LANDING"), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+storeIdentity, fmt.Sprintf("TILLR_RUN_RESUME=%t", req.Resume))
	if s.nonLanding {
		runEnv = withoutProcessEnv(runEnv, "TILLR_TRACE_DIR", "TILLR_REPO_ROOT")
		runEnv = append(runEnv, "TILLR_NON_LANDING=1")
		if traceDir, err := filepath.Abs(s.runOutputDir(req.RunID)); err == nil {
			runEnv = append(runEnv, "TILLR_TRACE_DIR="+traceDir)
		}
		if s.Root != "" {
			if absRoot, err := filepath.Abs(s.Root); err == nil {
				runEnv = append(runEnv, "TILLR_REPO_ROOT="+absRoot)
			}
		}
	}
	cmd.Env = withGitCeilingEnv(runEnv, s.LaneRoot)
	// Both streams are regular files: even stdout must survive replacement of
	// the loop. Close the parent's handles immediately after inheritance so the
	// loop only opens files to read once the run has finished.
	dir := s.runOutputDir(req.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return run.Record{}, "", Externalf("creating run output directory %s: %v", dir, err)
	}
	recordPath := filepath.Join(dir, RunRecordFile)
	stderrPath := filepath.Join(dir, RunStderrFile)
	stdout, err := os.Create(recordPath)
	if err != nil {
		return run.Record{}, "", Externalf("creating run record output: %v", err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		stdout.Close()
		_ = os.Remove(recordPath)
		return run.Record{}, "", Externalf("creating run error output: %v", err)
	}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	observation, observeErr := pipelineStart(s.Root, req.RunID, "graph")
	if observeErr != nil {
		stdout.Close()
		stderr.Close()
		_ = os.Remove(recordPath)
		_ = os.Remove(stderrPath)
		return run.Record{}, "", observeErr
	}
	finishPrep()
	started := time.Now().UnixMilli()
	endSpawn := timeHandoffPhase(ctx, "spawn_worker_process")
	err = s.startRunFaceContext(ctx, cmd, req)
	endSpawn()
	if !errors.Is(err, errRestartPending) {
		finishStartHandoff(ctx)
	}
	stdout.Close()
	stderr.Close()
	if cmd.Process != nil {
		// Start can succeed before the admission transaction reports cancellation.
		// A started face always has a waiter, regardless of the transaction result.
		finished := make(chan error, 1)
		exited := make(chan struct{})
		go func() {
			err := cmd.Wait()
			close(exited)
			finished <- err
		}()
		s.holdForClaim(ctx, req, exited)
		select {
		case err = <-finished:
		case <-ctx.Done():
			// Abandon this caller's wait, not the run. The face retains its
			// output files and terminal bookkeeping for the replacement.
			return run.Record{}, "", ctx.Err()
		}
	} else {
		// No run face ever started. Remove the empty stream files we just opened so
		// a replacement loop recognizes the claim as unlaunched rather than a
		// vanished worker face.
		_ = os.Remove(recordPath)
		_ = os.Remove(stderrPath)
		if errors.Is(err, errRestartPending) {
			return run.Record{}, "", err
		}
		if err != nil && ctx.Err() != nil {
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
		return run.Record{}, "", &runStartError{
			ExitError: Externalf("%s exec could not run %s: %v: %s", s.RunBin, req.RunID, err, diagnostic).(*ExitError),
			RunID:     req.RunID,
		}
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
	handle, err := s.startOrAttachDetachedGate(ctx, candidate)
	if err != nil {
		return GateOutcome{}, err
	}
	done, err := s.awaitDetachedGate(ctx, nil, handle)
	if err != nil {
		reason := "aborted"
		if ctx.Err() != nil {
			reason = "cancelled"
		}
		s.recordGateStopped(ctx, candidate, reason)
		return GateOutcome{}, err
	}
	return s.finishDetachedGate(ctx, candidate, handle, done)
}

func detachedGateDir(root, storePath, worktree, runID string) string {
	if root != "" {
		return RunDir(root, runID)
	}
	if storePath != "" {
		return filepath.Join(filepath.Dir(storePath), "gate-runs", runID)
	}
	return filepath.Join(worktree, ".git", "tillr-gate", runID)
}

func (s *ProcessSteps) gateRunDir(candidate Candidate) string {
	return detachedGateDir(s.Root, s.Store, candidate.Worktree, candidate.RunID)
}

type detachedGateFace struct {
	PID                 int    `json:"pid"`
	StartTicks          string `json:"start_ticks"`
	OwnerPID            int    `json:"owner_pid"`
	OwnerStartTicks     string `json:"owner_start_ticks"`
	Store               string `json:"store,omitempty"`
	RunID               string `json:"run_id"`
	ItemID              string `json:"item_id"`
	LaneID              string `json:"lane_id"`
	BaseSHA             string `json:"base_sha"`
	HeadSHA             string `json:"head_sha"`
	StartedAtMS         int64  `json:"started_at_ms"`
	PipelineStartedAtMS int64  `json:"pipeline_started_at_ms,omitempty"`
	MeasureTiming       bool   `json:"measure_timing"`
}

type detachedGateDone struct {
	RunID               string   `json:"run_id"`
	BaseSHA             string   `json:"base_sha"`
	HeadSHA             string   `json:"head_sha"`
	Argv                []string `json:"argv"`
	Cwd                 string   `json:"cwd"`
	StartedAtMS         int64    `json:"started_at_ms"`
	FinishedAtMS        int64    `json:"finished_at_ms"`
	PipelineStartedAtMS int64    `json:"pipeline_started_at_ms,omitempty"`
	MeasureTiming       bool     `json:"measure_timing"`
	ExitCode            int      `json:"exit_code"`
	NotStarted          bool     `json:"not_started,omitempty"`
	StartError          string   `json:"start_error,omitempty"`
	Interrupted         bool     `json:"interrupted,omitempty"`
	StdoutFile          string   `json:"stdout_file"`
	StderrTail          string   `json:"stderr_tail"`
}

type detachedGateSpec struct {
	RunID               string   `json:"run_id"`
	ItemID              string   `json:"item_id"`
	LaneID              string   `json:"lane_id"`
	BaseSHA             string   `json:"base_sha"`
	HeadSHA             string   `json:"head_sha"`
	Store               string   `json:"store,omitempty"`
	Argv                []string `json:"argv"`
	Env                 []string `json:"env"`
	Cwd                 string   `json:"cwd"`
	StdinFile           string   `json:"stdin_file,omitempty"`
	StdoutFile          string   `json:"stdout_file"`
	StderrFile          string   `json:"stderr_file"`
	FaceFile            string   `json:"face_file"`
	DoneFile            string   `json:"done_file"`
	OwnerPID            int      `json:"owner_pid"`
	OwnerStartTicks     string   `json:"owner_start_ticks"`
	PipelineStartedAtMS int64    `json:"pipeline_started_at_ms,omitempty"`
	MeasureTiming       bool     `json:"measure_timing"`
	NiceTarget          *int     `json:"nice_target,omitempty"`
}

type detachedGateHandle struct {
	dir     string
	face    detachedGateFace
	preDone *detachedGateDone
}

func readDetachedGateFace(dir string) (detachedGateFace, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, GateFaceFile))
	if err != nil {
		return detachedGateFace{}, false
	}
	var face detachedGateFace
	if json.Unmarshal(raw, &face) != nil || face.PID <= 0 || face.StartTicks == "" {
		return detachedGateFace{}, false
	}
	return face, true
}

func readDetachedGateDone(dir string) (detachedGateDone, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, GateDoneFile))
	if err != nil {
		return detachedGateDone{}, false
	}
	var done detachedGateDone
	if json.Unmarshal(raw, &done) != nil || done.RunID == "" {
		return detachedGateDone{}, false
	}
	return done, true
}

func hasDetachedGateInDir(dir string, candidate Candidate, matchSHA bool) (live bool, done bool) {
	if d, ok := readDetachedGateDone(dir); ok {
		if !d.Interrupted && d.RunID == candidate.RunID && (!matchSHA || (d.BaseSHA == candidate.BaseSHA && d.HeadSHA == candidate.HeadSHA)) {
			done = true
		}
	}
	if f, ok := readDetachedGateFace(dir); ok {
		if f.RunID == candidate.RunID && (!matchSHA || (f.BaseSHA == candidate.BaseSHA && f.HeadSHA == candidate.HeadSHA)) {
			if running, err := heavySupervisorRunning(f.PID, f.StartTicks); err == nil && running {
				live = true
			}
		}
	}
	return live, done
}

func (s *ProcessSteps) hasDetachedGate(candidate Candidate) (live bool, done bool) {
	return hasDetachedGateInDir(s.gateRunDir(candidate), candidate, true)
}

func detachedGateSettled(candidate Candidate, done detachedGateDone) bool {
	if done.NotStarted || done.Interrupted || done.ExitCode < 0 || done.ExitCode == record.ExitUsage {
		return false
	}
	code := done.ExitCode
	outcome := classifyCheck(checkresult.Observation{
		Commit:   candidate.HeadSHA,
		Worktree: candidate.Worktree,
		Source:   checkresult.JudgingProgram,
		ExitCode: &code,
	}).Outcome
	return outcome == checkresult.Pass || outcome == checkresult.Fail
}

func (s *ProcessSteps) hasSettledDetachedGate(candidate Candidate) bool {
	d, ok := readDetachedGateDone(s.gateRunDir(candidate))
	if !ok || d.RunID != candidate.RunID || d.BaseSHA != candidate.BaseSHA || d.HeadSHA != candidate.HeadSHA {
		return false
	}
	return detachedGateSettled(candidate, d)
}

func writeJSONAtomic(path string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *ProcessSteps) startOrAttachDetachedGate(ctx context.Context, candidate Candidate) (detachedGateHandle, error) {
	if err := CheckTrunkWorktree(ctx, s.Root); err != nil {
		return detachedGateHandle{}, err
	}
	gateDir := s.gateRunDir(candidate)
	if d, ok := readDetachedGateDone(gateDir); ok {
		if !d.Interrupted && d.RunID == candidate.RunID && d.BaseSHA == candidate.BaseSHA && d.HeadSHA == candidate.HeadSHA {
			face, _ := readDetachedGateFace(gateDir)
			return detachedGateHandle{dir: gateDir, face: face, preDone: &d}, nil
		}
	}
	if f, ok := readDetachedGateFace(gateDir); ok {
		if f.RunID == candidate.RunID && f.BaseSHA == candidate.BaseSHA && f.HeadSHA == candidate.HeadSHA {
			if running, err := heavySupervisorRunning(f.PID, f.StartTicks); err == nil && running {
				return detachedGateHandle{dir: gateDir, face: f}, nil
			}
		}
	}

	env := append(s.forkRootEnv(), "PYTHONDONTWRITEBYTECODE=1")
	if s.Root != "" {
		for _, name := range []string{GateSpansFile, GateFastVerdictFile, GateChecksFile, filepath.Join("change-logs", GateChecksFile)} {
			if err := os.Remove(filepath.Join(RunDir(s.Root, candidate.RunID), name)); err != nil && !os.IsNotExist(err) {
				return detachedGateHandle{}, Externalf("clearing preceding gate measurements: %v", err)
			}
		}
		env = append(env, "TILLR_GATE_SPANS_OUT="+filepath.Join(RunDir(s.Root, candidate.RunID), GateSpansFile),
			"TILLR_FAST_VERDICT_OUT="+filepath.Join(RunDir(s.Root, candidate.RunID), GateFastVerdictFile))
	}
	program := s.GateProgram
	fork := strings.TrimPrefix(candidate.BaseRef, "refs/heads/patch/")
	if s.hooks == nil && fork != candidate.BaseRef {
		program = "tillr-drain fork-gate --fork " + fork
	}
	if program == "" {
		return detachedGateHandle{}, Usagef("the loop was given no program for this step: it cannot judge a candidate without one")
	}
	measureTiming := s.Root != "" && fork == candidate.BaseRef && !IsRevertItem(candidate.ItemID) && !candidate.skipFastGateTiming
	timing := ""
	if measureTiming {
		timing = "1"
	}
	env = append(env, "TILLR_FORK_DRIFT=1", "TILLR_ORDINARY_GATE_TIMING="+timing)
	trunkRepair := IsRevertItem(candidate.ItemID) || IsTrunkFixer(candidate.ItemID)
	gateClass := ProcessClassGate
	if trunkRepair {
		gateClass = ProcessClassRepair
	}

	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		return detachedGateHandle{}, Externalf("creating gate directory %s: %v", gateDir, err)
	}
	for _, name := range []string{GateFaceFile, GateDoneFile, gateSpecFile, "gate.stdout", "gate.stderr", "gate.stdin"} {
		if err := os.Remove(filepath.Join(gateDir, name)); err != nil && !os.IsNotExist(err) {
			return detachedGateHandle{}, Externalf("clearing stale gate state %s: %v", name, err)
		}
	}

	argv := s.programArgv(program, candidate)
	var childArgv []string
	var childEnv []string
	var stdinFile string
	dropEnv := []string{"TILLR_SQLITE_TRACE", "PYTHONDONTWRITEBYTECODE"}
	if s.isHookCommand(program) {
		childArgv = append([]string(nil), argv...)
		baseEnv := withoutProcessEnv(os.Environ(), dropEnv...)
		childEnv = withGitCeilingEnv(append(baseEnv, env...), s.LaneRoot)
	} else {
		probeCmd := s.stepCommand(ctx, argv, nil)
		childArgv = append([]string(nil), probeCmd.Args...)
		childEnv = withGitCeilingEnv(append(withoutProcessEnv(probeCmd.Env, dropEnv...), env...), s.LaneRoot)
	}

	ownerPID, ownerStartTicks, err := heavySupervisorIdentity()
	if err != nil {
		return detachedGateHandle{}, Externalf("reading loop process identity: %v", err)
	}
	observation, err := pipelineStart(s.Root, candidate.RunID, "gate")
	if err != nil {
		return detachedGateHandle{}, err
	}

	specPath := filepath.Join(gateDir, gateSpecFile)
	spec := detachedGateSpec{
		RunID:               candidate.RunID,
		ItemID:              candidate.ItemID,
		LaneID:              candidate.LaneID,
		BaseSHA:             candidate.BaseSHA,
		HeadSHA:             candidate.HeadSHA,
		Store:               s.Store,
		Argv:                childArgv,
		Env:                 childEnv,
		Cwd:                 candidate.Worktree,
		StdinFile:           stdinFile,
		StdoutFile:          filepath.Join(gateDir, "gate.stdout"),
		StderrFile:          filepath.Join(gateDir, "gate.stderr"),
		FaceFile:            filepath.Join(gateDir, GateFaceFile),
		DoneFile:            filepath.Join(gateDir, GateDoneFile),
		OwnerPID:            ownerPID,
		OwnerStartTicks:     ownerStartTicks,
		PipelineStartedAtMS: observation.StartedAtMS,
		MeasureTiming:       measureTiming,
	}
	if target, ok := s.priority.NiceTarget(gateClass); ok {
		spec.NiceTarget = &target
	}
	if err := writeJSONAtomic(specPath, spec); err != nil {
		return detachedGateHandle{}, Externalf("writing gate supervisor spec: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		return detachedGateHandle{}, Externalf("locating executable for gate supervisor: %v", err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return detachedGateHandle{}, Externalf("creating gate supervisor readiness pipe: %v", err)
	}
	cmd := exec.Command(self, "--tillr-gate-supervisor", specPath)
	cmd.Dir = candidate.Worktree
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if s.priority.Mechanism() == "cgroup" {
		if err := s.priority.ApplyToCmd(cmd, gateClass, s.selfExe); err != nil {
			_ = readyW.Close()
			_ = readyR.Close()
			return detachedGateHandle{}, err
		}
	}
	cmd.ExtraFiles = []*os.File{readyW}
	if err := cmd.Start(); err != nil {
		_ = readyW.Close()
		_ = readyR.Close()
		return detachedGateHandle{}, Externalf("starting gate supervisor: %v", err)
	}
	_ = readyW.Close()
	go func() { _ = cmd.Wait() }()

	var buf [1]byte
	_, _ = readyR.Read(buf[:])
	_ = readyR.Close()

	face, ok := readDetachedGateFace(gateDir)
	if !ok {
		if d, doneOK := readDetachedGateDone(gateDir); doneOK {
			if err := s.recordGateStarted(ctx, candidate); err != nil {
				return detachedGateHandle{}, err
			}
			return detachedGateHandle{dir: gateDir, preDone: &d}, nil
		}
		return detachedGateHandle{}, Externalf("gate supervisor exited before publishing live face for %s", candidate.RunID)
	}
	handle := detachedGateHandle{dir: gateDir, face: face}
	if err := s.recordGateStarted(ctx, candidate); err != nil {
		s.stopDetachedGate(handle)
		return detachedGateHandle{}, err
	}
	return handle, nil
}

func (s *ProcessSteps) awaitDetachedGate(waitCtx context.Context, loop *Loop, handle detachedGateHandle) (detachedGateDone, error) {
	if handle.preDone != nil {
		if waitCtx.Err() != nil {
			if loop != nil && loop.isRestartReplaced() {
				return detachedGateDone{}, waitCtx.Err()
			}
			s.stopDetachedGate(handle)
			return detachedGateDone{}, waitCtx.Err()
		}
		return *handle.preDone, nil
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if d, ok := readDetachedGateDone(handle.dir); ok {
			return d, nil
		}
		if handle.face.PID > 0 {
			running, err := heavySupervisorRunning(handle.face.PID, handle.face.StartTicks)
			if err == nil && !running {
				if d, ok := readDetachedGateDone(handle.dir); ok {
					return d, nil
				}
				return detachedGateDone{}, Externalf("gate supervisor %d exited without writing %s", handle.face.PID, GateDoneFile)
			}
		}
		select {
		case <-waitCtx.Done():
			if loop != nil && loop.isRestartReplaced() {
				return detachedGateDone{}, waitCtx.Err()
			}
			s.stopDetachedGate(handle)
			return detachedGateDone{}, waitCtx.Err()
		case <-ticker.C:
		}
	}
}

func (s *ProcessSteps) stopDetachedGate(handle detachedGateHandle) {
	if handle.face.PID > 0 && handle.face.StartTicks != "" {
		if running, err := heavySupervisorRunning(handle.face.PID, handle.face.StartTicks); err == nil && running {
			_ = syscall.Kill(handle.face.PID, syscall.SIGTERM)
		}
		for {
			running, err := heavySupervisorRunning(handle.face.PID, handle.face.StartTicks)
			if err != nil || !running {
				break
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	for _, name := range []string{GateFaceFile, GateDoneFile, gateSpecFile, "gate.stdin"} {
		_ = os.Remove(filepath.Join(handle.dir, name))
	}
}

func (s *ProcessSteps) finishDetachedGate(ctx context.Context, candidate Candidate, handle detachedGateHandle, done detachedGateDone) (GateOutcome, error) {
	verdictHandled := false
	stopReason := "aborted"
	defer func() {
		if !verdictHandled {
			s.recordGateStopped(ctx, candidate, stopReason)
		}
	}()
	for _, name := range []string{GateFaceFile, GateDoneFile, gateSpecFile, "gate.stdin"} {
		_ = os.Remove(filepath.Join(handle.dir, name))
	}
	if s.Root != "" && done.PipelineStartedAtMS > 0 {
		obs := pipelineObservation{Step: "gate", StartedAtMS: done.PipelineStartedAtMS}
		if err := pipelineFinish(s.Root, candidate.RunID, obs); err != nil {
			return GateOutcome{}, err
		}
	}
	argv0 := ""
	if len(done.Argv) > 0 {
		argv0 = done.Argv[0]
	}
	if done.NotStarted {
		stopReason = "not-started"
		span := stepSpan("gate", done.Argv, &exec.Cmd{Dir: done.Cwd}, done.StartedAtMS, done.StderrTail)
		span.FinishedAtMS = done.FinishedAtMS
		if err := s.recordStep(candidate.RunID, span); err != nil {
			return GateOutcome{}, err
		}
		if done.MeasureTiming {
			s.recordGateTiming(context.WithoutCancel(ctx), candidate, span)
		}
		return GateOutcome{}, Externalf("running %q in %s: %s: %s", argv0, candidate.Worktree, done.StartError, done.StderrTail)
	}
	var exitCodePtr *int
	if done.ExitCode >= 0 {
		code := done.ExitCode
		exitCodePtr = &code
	}
	span := StepSpan{
		Step:         "gate",
		Argv:         done.Argv,
		Cwd:          done.Cwd,
		StartedAtMS:  done.StartedAtMS,
		FinishedAtMS: done.FinishedAtMS,
		ExitCode:     exitCodePtr,
		StderrTail:   done.StderrTail,
	}
	if err := s.recordStep(candidate.RunID, span); err != nil {
		return GateOutcome{}, err
	}
	if done.MeasureTiming {
		s.recordGateTiming(context.WithoutCancel(ctx), candidate, span)
	}
	if ctx.Err() != nil {
		stopReason = "cancelled"
		return GateOutcome{}, ctx.Err()
	}
	if done.Interrupted {
		stopReason = "interrupted"
		return GateOutcome{}, Externalf("running %q in %s: gate supervisor interrupted: %s", argv0, candidate.Worktree, done.StderrTail)
	}
	if done.ExitCode < 0 {
		stopReason = "signal-or-error"
		return GateOutcome{}, Externalf("running %q in %s: exit %d: %s", argv0, candidate.Worktree, done.ExitCode, done.StderrTail)
	}
	var outBytes []byte
	if done.StdoutFile != "" {
		if raw, err := os.ReadFile(done.StdoutFile); err == nil {
			outBytes = raw
		} else if !os.IsNotExist(err) {
			return GateOutcome{}, Externalf("reading gate stdout: %v", err)
		}
	}
	outStr := string(outBytes)
	if done.ExitCode == record.ExitUsage {
		stopReason = "usage-error"
		return GateOutcome{}, Usagef("configuration fault: %s program %q refused its arguments for %s%s", "gate", s.GateProgram, candidate.ItemID, stderrReason(span))
	}

	code := done.ExitCode
	classified := classifyCheck(checkresult.Observation{
		Commit:   candidate.HeadSHA,
		Worktree: candidate.Worktree,
		Source:   checkresult.JudgingProgram,
		ExitCode: &code,
	})
	outcome := GateOutcome{
		Outcome:    GateErrored,
		DurationMS: span.FinishedAtMS - span.StartedAtMS,
	}
	switch {
	case classified.Outcome == checkresult.Pass:
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
	case classified.Outcome == checkresult.Unknown && classified.Detail == checkresult.DetailStalled:
		outcome.Outcome = GateStalled
		var err error
		outcome.StalledChecks, err = s.gateStallContext(candidate)
		if err != nil {
			return GateOutcome{}, err
		}
	case classified.Outcome == checkresult.Fail:
		outcome.Outcome = GateFailed
		// A red gate carries the facts the trunk replay and the fixer need. Reading them
		// here, from the records the gate just wrote, keeps the loop from re-parsing a
		// program's stdout to find out what it judged.
		outcome.FailingChecks, outcome.SuspectPaths, outcome.CheckWatches = s.gateFailureContext(candidate)
		outcome.CandidateChangedPaths = outcome.SuspectPaths
		outcome.CheckReads = s.gateCheckReads(candidate, outcome.CheckWatches)
		if s.Root != "" {
			runDir := RunDir(s.Root, candidate.RunID)
			outcome.forkConflicts, outcome.unfinished, outcome.unansweredFork = readForkConflicts(filepath.Join(runDir, GateSpansFile))
			wrapperWholeLog := ""
			for _, c := range gateFailingChecks(runDir, candidate.RunID) {
				if c.LogRef == "" {
					continue
				}
				if outcome.CheckLogs == nil {
					outcome.CheckLogs = map[string]string{}
				}
				outcome.CheckLogs[c.CheckID] = c.LogRef
				if strings.HasPrefix(c.CheckID, "gate:") && wrapperWholeLog == "" {
					whole := strings.TrimSuffix(c.LogRef, ".triage.log")
					if whole != c.LogRef {
						wrapperWholeLog = whole + ".log"
					} else {
						wrapperWholeLog = c.LogRef
					}
				}
			}
			perCheck := extractForkCheckLogs(ctx, candidate.Worktree, candidate.HeadSHA, filepath.Join(runDir, GateSpansFile), wrapperWholeLog, outStr, filepath.Join(runDir, "logs"))
			for id, ref := range perCheck {
				if outcome.CheckLogs == nil {
					outcome.CheckLogs = map[string]string{}
				}
				outcome.CheckLogs[id] = ref
			}
		}
	}
	if outcome.Outcome == GateErrored {
		if detail := gateStartErrorDetail(span.StderrTail, s.gateErrorTriage(candidate)); detail != "" {
			outcome.ErrorDetail = detail
			outcome.StartError = true
		} else if gateUsageError(span.StderrTail) {
			outcome.ErrorDetail = gateErrorDetail(span.StderrTail, candidateChangedPaths(ctx, candidate))
		}
	}
	var firstStall *gateStallHold
	if outcome.Outcome == GateStalled {
		var err error
		firstStall, err = s.firstStallHold(ctx, candidate, outcome.StalledChecks)
		if err != nil {
			return GateOutcome{}, err
		}
	}
	judgment := gateStepJudgment{
		FirstStallHold: firstStall,
		TickID:         candidate.TickID, Run: runRef{RunID: candidate.RunID, ItemID: candidate.ItemID, LaneID: candidate.LaneID},
		Outcome: outcome.Outcome, Program: span.Argv, ExitCode: exitCode(span),
		DurationMS: outcome.DurationMS,
	}
	verdictHandled = true
	if (candidate.deferGateJudgment || len(outcome.forkConflicts) > 0 || outcome.unansweredFork) && (outcome.Outcome == GatePassed || outcome.Outcome == GatePassedInherited || outcome.Outcome == GateFailed) {
		outcome.finish = func(final GateOutcome) error {
			judgment.Outcome, judgment.DurationMS = final.Outcome, final.DurationMS
			switch final.Outcome {
			case GatePassed, GatePassedInherited:
				judgment.ExitCode = record.ExitOK
			case GateFailed:
				judgment.ExitCode = record.ExitNegative
			case GateErrored:
				judgment.ExitCode = record.ExitExternal
			}
			return s.recordGateJudgment(ctx, candidate, judgment)
		}
	} else if err := s.recordGateJudgment(ctx, candidate, judgment); err != nil {
		return GateOutcome{}, err
	}
	outcome.StallHeld = firstStall != nil
	return outcome, nil
}

func readStderrTailFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var tail stderrTail
	_, _ = tail.Write(raw)
	return tail.String()
}

func superviseDetachedGate(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "gate supervisor needs a spec path")
		return 2
	}
	// Trap SIGINT/SIGTERM before publishing the supervisor face or starting
	// the gate, and keep the handler installed through finishHeavyTree so an
	// early or repeated SIGTERM (such as concurrent stopDetachedGate calls on
	// loop shutdown) cannot kill the subreaper before its descendants are reaped.
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ready := os.NewFile(3, "gate ready pipe")
	raw, err := os.ReadFile(args[0])
	if err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "gate supervisor reading spec:", err)
		return 4
	}
	var spec detachedGateSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "gate supervisor decoding spec:", err)
		return 4
	}
	if err := prepareHeavyTree(); err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "gate supervisor subreaper:", err)
		return 4
	}
	pid, startTicks, err := heavySupervisorIdentity()
	if err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "gate supervisor identity:", err)
		return 4
	}
	startedAt := time.Now().UnixMilli()
	face := detachedGateFace{
		PID:                 pid,
		StartTicks:          startTicks,
		OwnerPID:            spec.OwnerPID,
		OwnerStartTicks:     spec.OwnerStartTicks,
		Store:               spec.Store,
		RunID:               spec.RunID,
		ItemID:              spec.ItemID,
		LaneID:              spec.LaneID,
		BaseSHA:             spec.BaseSHA,
		HeadSHA:             spec.HeadSHA,
		StartedAtMS:         startedAt,
		PipelineStartedAtMS: spec.PipelineStartedAtMS,
		MeasureTiming:       spec.MeasureTiming,
	}
	if err := writeJSONAtomic(spec.FaceFile, face); err != nil {
		_ = ready.Close()
		fmt.Fprintln(os.Stderr, "gate supervisor writing face:", err)
		return 4
	}
	_ = ready.Close()

	done := detachedGateDone{
		RunID:               spec.RunID,
		BaseSHA:             spec.BaseSHA,
		HeadSHA:             spec.HeadSHA,
		Argv:                spec.Argv,
		Cwd:                 spec.Cwd,
		StartedAtMS:         startedAt,
		PipelineStartedAtMS: spec.PipelineStartedAtMS,
		MeasureTiming:       spec.MeasureTiming,
		ExitCode:            -1,
		StdoutFile:          spec.StdoutFile,
	}
	if len(spec.Argv) == 0 {
		done.NotStarted = true
		done.StartError = "empty gate argv"
		done.FinishedAtMS = time.Now().UnixMilli()
		_ = writeJSONAtomic(spec.DoneFile, done)
		return 0
	}
	stdoutFile, err := os.Create(spec.StdoutFile)
	if err != nil {
		done.NotStarted = true
		done.StartError = err.Error()
		done.FinishedAtMS = time.Now().UnixMilli()
		_ = writeJSONAtomic(spec.DoneFile, done)
		return 0
	}
	stderrFile, err := os.Create(spec.StderrFile)
	if err != nil {
		_ = stdoutFile.Close()
		done.NotStarted = true
		done.StartError = err.Error()
		done.FinishedAtMS = time.Now().UnixMilli()
		_ = writeJSONAtomic(spec.DoneFile, done)
		return 0
	}
	var stdinFile *os.File
	if spec.StdinFile != "" {
		stdinFile, err = os.Open(spec.StdinFile)
		if err != nil {
			_ = stdoutFile.Close()
			_ = stderrFile.Close()
			done.NotStarted = true
			done.StartError = err.Error()
			done.FinishedAtMS = time.Now().UnixMilli()
			_ = writeJSONAtomic(spec.DoneFile, done)
			return 0
		}
	}

	child := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	child.Dir = spec.Cwd
	child.Env = spec.Env
	child.Stdout = stdoutFile
	child.Stderr = stderrFile
	if stdinFile != nil {
		child.Stdin = stdinFile
	}
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	select {
	case <-sigCh:
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		if stdinFile != nil {
			_ = stdinFile.Close()
		}
		_ = finishHeavyTree()
		done.Interrupted = true
		done.FinishedAtMS = time.Now().UnixMilli()
		done.StderrTail = readStderrTailFile(spec.StderrFile)
		_ = writeJSONAtomic(spec.DoneFile, done)
		return 0
	default:
	}

	if spec.NiceTarget != nil {
		if err := unix.Setpriority(unix.PRIO_PROCESS, 0, *spec.NiceTarget); err != nil {
			_ = stdoutFile.Close()
			_ = stderrFile.Close()
			if stdinFile != nil {
				_ = stdinFile.Close()
			}
			_ = finishHeavyTree()
			done.NotStarted = true
			done.StartError = fmt.Sprintf("setpriority(%d): %v", *spec.NiceTarget, err)
			done.FinishedAtMS = time.Now().UnixMilli()
			_ = writeJSONAtomic(spec.DoneFile, done)
			return 0
		}
	}

	startedCh := make(chan int, 1)
	stopReaping := reapWhileSupervising(startedCh)
	startErr := child.Start()
	if startErr != nil {
		stopReaping()
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
		if stdinFile != nil {
			_ = stdinFile.Close()
		}
		_ = finishHeavyTree()
		done.NotStarted = true
		done.StartError = startErr.Error()
		done.FinishedAtMS = time.Now().UnixMilli()
		done.StderrTail = readStderrTailFile(spec.StderrFile)
		_ = writeJSONAtomic(spec.DoneFile, done)
		return 0
	}
	startedCh <- child.Process.Pid

	waitDone := make(chan error, 1)
	go func() { waitDone <- child.Wait() }()

	ticker := time.NewTicker(50 * time.Millisecond)

waitLoop:
	for {
		select {
		case <-waitDone:
			break waitLoop
		case <-sigCh:
			done.Interrupted = true
		case <-ticker.C:
			if spec.OwnerPID > 0 && spec.OwnerStartTicks != "" {
				if running, err := heavySupervisorRunning(spec.OwnerPID, spec.OwnerStartTicks); err == nil && !running {
					done.Interrupted = true
				}
			}
		}
		if done.Interrupted {
			if pgid := child.Process.Pid; pgid > 0 {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
			<-waitDone
			break waitLoop
		}
	}
	ticker.Stop()
	stopReaping()

	_ = stdoutFile.Close()
	_ = stderrFile.Close()
	if stdinFile != nil {
		_ = stdinFile.Close()
	}
	_ = finishHeavyTree()

	done.FinishedAtMS = time.Now().UnixMilli()
	if child.ProcessState != nil {
		done.ExitCode = child.ProcessState.ExitCode()
	}
	done.StderrTail = readStderrTailFile(spec.StderrFile)
	_ = writeJSONAtomic(spec.DoneFile, done)
	return 0
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
	if err := CheckTrunkWorktree(ctx, s.Root); err != nil {
		return LandOutcome{}, err
	}
	extra := s.landArgs(candidate)
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
	var out []byte
	var err error
	if state == StateLanded && s.Store != "" {
		// Hold the loop's repeating writers off the store's write lock while
		// `tillr-item state ... landed` settles this landed item (claim_gate.go).
		func() {
			defer claimGateFor(s.Store).begin()()
			out, err = cmd.Output()
		}()
	} else {
		out, err = cmd.Output()
	}
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
	gateCheck := step == "gate" || step == "fixer-gate"
	if s.isHookCommand(program) {
		// The definition selects this exact executable, even at the legacy
		// adapter path. Only CLI programs opt into the embedded adapter.
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		baseEnv := os.Environ()
		if gateCheck {
			baseEnv = withoutProcessEnv(baseEnv, "TILLR_SQLITE_TRACE", "PYTHONDONTWRITEBYTECODE")
		}
		cmd.Env = withGitCeilingEnv(append(baseEnv, env...), s.LaneRoot)
	} else {
		baseEnv := env
		if gateCheck {
			// The gate runs candidate checks, not the loop's store: strip
			// TILLR_SQLITE_TRACE and prior PYTHONDONTWRITEBYTECODE so checks
			// see the same environment as the trunk proof and write no
			// __pycache__ files into the checked-out worktree.
			cmd = s.stepCommand(ctx, argv, nil)
			cmd.Env = append(withoutProcessEnv(cmd.Env, "TILLR_SQLITE_TRACE", "PYTHONDONTWRITEBYTECODE"), env...)
		} else {
			cmd = s.stepCommand(ctx, argv, baseEnv)
		}
	}
	if step == "fixer-gate" {
		if err := s.priority.ApplyToCmd(cmd, ProcessClassRepair, s.selfExe); err != nil {
			return 0, "", StepSpan{}, err
		}
	}
	cmd.Dir = dir
	var stderr stderrTail
	cmd.Stderr = &stderr
	if os.Getenv("TILLR_SQLITE_TRACE") == "1" {
		// The tail keeps what the step said; its trace goes where every other trace is.
		traces := &sqliteTraceForwarder{Output: os.Stderr}
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
	if gateCheck {
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
		return 0, "", span, Externalf("running %q in %s: %s: %s", argv[0], dir, notStarted.Error(), stderr.String())
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

func (s *ProcessSteps) gitCeilingEnv() []string {
	if s.LaneRoot == "" {
		return nil
	}
	abs, err := filepath.Abs(s.LaneRoot)
	if err != nil {
		return nil
	}
	return []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(abs)}
}

func withGitCeilingEnv(env []string, laneRoot string) []string {
	if laneRoot == "" {
		return env
	}
	abs, err := filepath.Abs(laneRoot)
	if err != nil {
		return env
	}
	return append(withoutProcessEnv(env, "GIT_CEILING_DIRECTORIES"), "GIT_CEILING_DIRECTORIES="+filepath.Dir(abs))
}

func isUnderDir(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	return true
}

func requireLaneWorktreeGit(dir, laneRoot string, args []string) error {
	if laneRoot == "" || !isUnderDir(dir, laneRoot) {
		return nil
	}
	if len(args) > 0 && args[0] == "init" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return Externalf("lane worktree %s has no .git (%v)", dir, err)
	}
	return nil
}

// checkProcessEnv builds the environment for a replay, rolling or heavy check subprocess:
// it strips the loop's own SQLite trace toggle and sets PYTHONDONTWRITEBYTECODE=1 so Python
// imports inside a checked-out proof or scratch worktree never write __pycache__/*.pyc.<id>
// or .pyc files that flip `git status --porcelain` during concurrent `go build` VCS stamping.
func (s *ProcessSteps) checkProcessEnv(base []string, extra ...string) []string {
	out := withoutProcessEnv(base, "TILLR_SQLITE_TRACE", "PYTHONDONTWRITEBYTECODE", "GIT_CEILING_DIRECTORIES")
	out = append(out, "PYTHONDONTWRITEBYTECODE=1")
	out = append(out, s.forkRootEnv()...)
	out = append(out, s.gitCeilingEnv()...)
	return append(out, extra...)
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
					if check.CheckID == "" {
						continue
					}
					if classifyCheck(checkresult.Observation{
						Check:    check.CheckID,
						Commit:   candidate.HeadSHA,
						Worktree: candidate.Worktree,
						Source:   checkresult.ResultWord,
						Word:     check.Result,
					}).Outcome == checkresult.Fail {
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
				if check.CheckID == "" {
					continue
				}
				if classifyCheck(checkresult.Observation{
					Check:    check.CheckID,
					Commit:   candidate.HeadSHA,
					Worktree: candidate.Worktree,
					Source:   checkresult.ResultWord,
					Word:     check.Status,
				}).Outcome == checkresult.Fail {
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
		checksDir := settings.DefaultChecksDir
		if s, err := settings.Read(candidate.Worktree); err == nil {
			checksDir = s.ChecksDir()
		}
		declarations, _ := filepath.Glob(filepath.Join(candidate.Worktree, filepath.FromSlash(checksDir), "*.json"))
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
		checksDir := settings.DefaultChecksDir
		if s, err := settings.Read(candidate.Worktree); err == nil {
			checksDir = s.ChecksDir()
		}
		declarations, _ := filepath.Glob(filepath.Join(candidate.Worktree, filepath.FromSlash(checksDir), "*.json"))
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
	isDefault := strings.TrimSpace(s.ReplayProgram) == DefaultReplayProgram
	// A grouped exit 1 says only that SOME check failed. Judge each id so a
	// candidate's own red cannot hide behind a different red already on its base.
	for _, check := range checks {
		if isDefault && !candidate.wholeCheckReplay && isReplayGoCheck(check) {
			handled, red, known, output, replayErr := s.replayGoCheck(ctx, dir, candidate, rev, check)
			if replayErr != nil {
				return ReplayOutcome{Rev: rev, Known: false}, replayErr
			}
			if handled {
				if !known {
					return ReplayOutcome{Rev: rev, Known: false}, nil
				}
				if red {
					out.Red = append(out.Red, check)
					if s.LaneRoot != "" && rev != "" {
						text := output
						if strings.TrimSpace(text) == "" {
							text = fmt.Sprintf("replay of %s on %s failed with no output\n", check, shortSHA(rev))
						}
						if replayLog := writeReplayCheckLog(ctx, dir, rev, check, text, filepath.Join(s.LaneRoot, ".rolling", "logs", rev)); replayLog != "" {
							if out.CheckLogs == nil {
								out.CheckLogs = map[string]string{}
							}
							out.CheckLogs[check] = replayLog
						}
					}
				}
				continue
			}
		}
		program := strings.ReplaceAll(s.ReplayProgram, "{only}", check)
		argv := s.commandArgv(program)
		if len(argv) == 0 {
			return ReplayOutcome{Rev: rev, Known: false}, nil
		}
		cmd := s.stepCommand(ctx, argv, nil)
		cmd.Dir = dir
		env := s.replayChildEnv(candidate, "TILLR_FORK_DRIFT=1")
		env = append(withoutProcessEnv(env, "TILLR_LOOP_ADAPTER_BUILD", "TILLR_DRAIN_EXEC_LOOP_ADAPTER"), stepBuildEnv())
		if s.standardAdapterArg(argv) >= 0 {
			env = append(env, execLoopAdapterEnv)
		}
		cmd.Env = env
		replayClass := ProcessClassGate
		if IsRevertItem(candidate.ItemID) || IsTrunkFixer(candidate.ItemID) {
			replayClass = ProcessClassRepair
		}
		if err := s.priority.ApplyToCmd(cmd, replayClass, s.selfExe); err != nil {
			return ReplayOutcome{Rev: rev, Known: false}, err
		}
		var stdout bytes.Buffer
		var stderr stderrTail
		var combined bytes.Buffer
		cmd.Stdout = io.MultiWriter(&combined, &stdout)
		cmd.Stderr = io.MultiWriter(&combined, &stderr)
		runErr := runCheckGroup(cmd)
		if ctx.Err() != nil {
			return ReplayOutcome{Rev: rev, Known: false}, ctx.Err()
		}
		if cmd.ProcessState == nil {
			return ReplayOutcome{Rev: rev, Known: false}, Externalf("replaying %s on %s: %v: %s",
				check, shortSHA(rev), runErr, stderr.String())
		}
		code := cmd.ProcessState.ExitCode()
		classified := classifyCheck(checkresult.Observation{
			Check:    check,
			Commit:   rev,
			Worktree: dir,
			Source:   checkresult.BinaryProgram,
			ExitCode: &code,
		})
		switch classified.Outcome {
		case checkresult.Pass:
		case checkresult.Fail:
			out.Red = append(out.Red, check)
			if observation, ok := replayForkConflict(check, combined.String()); ok {
				if out.forkConflicts == nil {
					out.forkConflicts = make(map[string]forkObservation)
				}
				out.forkConflicts[check] = observation
			}
			if s.LaneRoot != "" && rev != "" {
				logDir := filepath.Join(s.LaneRoot, ".rolling", "logs", rev)
				replayLog := replayVerdictCheckLog(dir, stdout.Bytes(), check, logDir)
				if replayLog == "" {
					text := combined.String()
					if strings.TrimSpace(text) == "" {
						text = fmt.Sprintf("replay of %s on %s exited %d with no output\n", check, shortSHA(rev), code)
					}
					replayLog = writeReplayCheckLog(ctx, dir, rev, check, text, logDir)
				}
				if replayLog != "" {
					if out.CheckLogs == nil {
						out.CheckLogs = map[string]string{}
					}
					out.CheckLogs[check] = replayLog
				}
			}
		default:
			// Unknown is never evidence for acquittal.
			return ReplayOutcome{Rev: rev, Known: false}, nil
		}
	}
	return out, nil
}

// replayVerdictCheckLog extracts a failing check's log_ref from a declaration-runner
// gate.verdict written to stdout by tillr-loop replay and snapshots it (with its whole
// sibling, if any) under logDir before the replay worktree is removed.
func replayVerdictCheckLog(dir string, stdout []byte, check, logDir string) string {
	checks, err := decodeHeavyVerdict(stdout)
	if err != nil {
		return ""
	}
	for _, c := range checks {
		if c.CheckID != check {
			continue
		}
		ref := c.LogRef
		if ref == "" {
			ref = c.LogPath
		}
		if ref == "" {
			return ""
		}
		suffix := ".log"
		if strings.HasSuffix(ref, ".triage.log") {
			suffix = ".triage.log"
		}
		dstName := "replay-" + sanitizeLogComponent(check) + suffix
		if copied, ok := copyLogWithWhole(dir, ref, logDir, dstName); ok {
			return copied
		}
		return ""
	}
	return ""
}

func runCheckGroup(cmd *exec.Cmd) error { return runCheckGroupStarted(cmd, nil) }

// detachedWorktree checks one revision out at dir, a detached worktree of the loop's
// repository, and answers the cleanup that removes it. A step judges a worktree of s.Root
// rather than s.Root itself: a landing may move the trunk while the step runs, and a step
// that read the trunk's own checkout would read a tree something else rewrote underneath
// it.
func (s *ProcessSteps) detachedWorktree(ctx context.Context, dir, rev string) (string, func(), error) {
	return s.detachedWorktreeAt(ctx, rev, func() (string, error) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", Externalf("creating the worktree directory %s: %v", filepath.Dir(dir), err)
		}
		return dir, nil
	})
}

func (s *ProcessSteps) detachedWorktreeAt(ctx context.Context, rev string, pickDir func() (string, error)) (string, func(), error) {
	if s.Root == "" {
		return "", func() {}, Usagef("the loop cannot check out %s: it was given no repository", shortSHA(rev))
	}
	if err := authorizeWorktreeRoot(s.Root); err != nil {
		return "", func() {}, err
	}
	var dir string
	// A worktree a previous run left behind is removed rather than reused: a stale
	// registration would refuse the add, and a reused directory is not provably the
	// revision this run was asked to judge.
	err := withWorktreeRegistryLock(ctx, s.Root, func() error {
		var err error
		dir, err = pickDir()
		if err != nil {
			return err
		}
		if _, _, _, err := unlockRegisteredWorktree(ctx, s.Root, "", dir); err != nil {
			return Externalf("unlocking stale worktree %s: %v", dir, err)
		}
		_ = exec.Command("git", "-C", s.Root, "worktree", "remove", "--force", dir).Run()
		_ = os.RemoveAll(dir)
		_ = exec.Command("git", "-C", s.Root, "worktree", "prune").Run()
		cmd := exec.CommandContext(ctx, "git", "-C", s.Root, "worktree", "add", "--detach", "--force", dir, rev)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return os.ErrProcessDone
			}
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			if err != nil {
				return cmd.Process.Signal(syscall.SIGTERM)
			}
			return nil
		}
		cmd.WaitDelay = 30 * time.Second
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

// replayWorktree checks one revision out into a unique detached scratch worktree under
// the lane root's .replay directory, and answers its path and the cleanup that removes it.
// Each replay gets its own directory so concurrent replays of the same commit never share
// or remove each other's tree.
func (s *ProcessSteps) replayWorktree(ctx context.Context, rev string) (string, func(), error) {
	if s.LaneRoot == "" {
		return "", func() {}, Usagef("the loop cannot replay %s: it was given no lane root", shortSHA(rev))
	}
	parent := filepath.Join(s.LaneRoot, ".replay")
	return s.detachedWorktreeAt(ctx, rev, func() (string, error) {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", Externalf("creating the worktree directory %s: %v", parent, err)
		}
		dir, err := os.MkdirTemp(parent, shortSHA(rev)+"-")
		if err != nil {
			return "", Externalf("creating a replay worktree directory under %s: %v", parent, err)
		}
		return dir, nil
	})
}

// fixerGate answers the full gate program a fixer runs, with its range since the last
// green. No watermark yet is the whole tree (`--all`): a first fixer has nothing proved
// behind it, and a range from an empty base would plan nothing and pass.

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
	cmd := s.stepCommand(ctx, argv, nil)
	cmd.Dir = dir
	env := s.checkProcessEnv(os.Environ(), "TILLR_GATE_SPANS_OUT="+spans)
	env = append(withoutProcessEnv(env, "TILLR_LOOP_ADAPTER_BUILD", "TILLR_DRAIN_EXEC_LOOP_ADAPTER"), stepBuildEnv())
	if s.standardAdapterArg(argv) >= 0 {
		env = append(env, execLoopAdapterEnv)
	}
	cmd.Env = env
	var stderr stderrTail
	cmd.Stderr = &stderr
	rollingLog := ""
	if s.LaneRoot != "" && tipSHA != "" {
		logDir := filepath.Join(s.LaneRoot, ".rolling", "logs", tipSHA)
		if os.MkdirAll(logDir, 0o755) == nil {
			rollingLog = filepath.Join(logDir, "rolling.log")
			if logFile, openErr := os.OpenFile(rollingLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o644); openErr == nil {
				defer logFile.Close()
				cmd.Stdout = logFile
				cmd.Stderr = io.MultiWriter(logFile, &stderr)
			} else {
				rollingLog = ""
			}
		}
	}
	runErr := runOwnedHeavy(ctx, cmd, "legacy")
	if ctx.Err() != nil {
		return RollingOutcome{}, ctx.Err()
	}
	var notStarted *heavyCommandNotStarted
	if cmd.ProcessState == nil || errors.As(runErr, &notStarted) {
		_ = classifyCheck(checkresult.Observation{Source: checkresult.JudgingProgram})
		return RollingOutcome{Known: false, ExitCode: -1,
			Detail: fmt.Sprintf("rolling program did not start: %v: %s", runErr, stderr.String())}, nil
	}
	exitNum := cmd.ProcessState.ExitCode()
	classified := classifyCheck(checkresult.Observation{Source: checkresult.JudgingProgram, ExitCode: &exitNum})
	coverage := readRollingCoverage(spans)
	var declared map[string][]string
	if classified.Outcome == checkresult.Fail || (classified.Outcome == checkresult.Unknown && classified.Detail == checkresult.DetailStalled) {
		declared, err = legacyCheckWatches(ctx, dir)
		if err != nil {
			return RollingOutcome{Known: false, ExitCode: exitNum, Detail: err.Error() + "; " + stderr.String(), Coverage: coverage}, nil
		}
	}
	switch {
	case classified.Outcome == checkresult.Pass:
		return RollingOutcome{Green: true, Known: true, ExitCode: exitNum, Coverage: coverage}, nil
	case classified.Outcome == checkresult.Unknown && classified.Detail == checkresult.DetailStalled:
		stalled, watches, err := readSpansWithWatches(spans, "STALLED", declared)
		if err != nil {
			return RollingOutcome{Known: false, ExitCode: exitNum, Detail: err.Error() + "; " + stderr.String(), Coverage: coverage}, nil
		}
		return RollingOutcome{Stalled: len(stalled) > 0, Known: false, ExitCode: exitNum,
			FailingChecks: stalled, CheckWatches: watches, Coverage: coverage, Detail: stderr.String()}, nil
	case classified.Outcome == checkresult.Fail:
		failing, watches, err := readSpansWithWatches(spans, "FAIL", declared)
		if err != nil {
			return RollingOutcome{Known: false, ExitCode: exitNum, Detail: err.Error() + "; " + stderr.String(), Coverage: coverage}, nil
		}
		if len(failing) == 0 {
			return RollingOutcome{Known: false, ExitCode: exitNum, Coverage: coverage,
				Detail: "it named no failing check. " + stderr.String()}, nil
		}
		var checkLogs map[string]string
		if s.LaneRoot != "" && tipSHA != "" {
			logDir := filepath.Join(s.LaneRoot, ".rolling", "logs", tipSHA)
			perCheck := extractForkCheckLogs(ctx, dir, tipSHA, spans, rollingLog, "", logDir)
			keptRolling := legacyKeptLog(rollingLog, "fail")
			for _, id := range failing {
				ref := perCheck[id]
				if ref == "" {
					ref = keptRolling
				}
				if ref != "" {
					if checkLogs == nil {
						checkLogs = map[string]string{}
					}
					checkLogs[id] = ref
				}
			}
		}
		return RollingOutcome{Green: false, Known: true, ExitCode: exitNum, FailingChecks: failing, CheckWatches: watches, CheckLogs: checkLogs, Coverage: coverage}, nil
	default:
		return RollingOutcome{Known: false, ExitCode: exitNum, Detail: stderr.String(), Coverage: coverage}, nil
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

// runStartError is a run the face could not start: it answered with no record. It is that
// run's failure, not the loop's, so the loop holds the run's item with the cause and goes on
// (holdSettleFailure) instead of ending.
type runStartError struct {
	*ExitError
	RunID string
}

func (e *runStartError) Unwrap() error { return e.ExitError }
