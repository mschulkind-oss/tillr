package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Request is design §5.6's run.request payload. Command is an argv array, never a
// shell string: a request that carried a string would make the caller's quoting the
// utility's problem.
//
// WorkerNiceness is the target niceness for a run's process group. The heavy
// proof keeps the drain's niceness.
const WorkerNiceness = 10

// Backend is the adapter program that produces this run, named by staging: it is what
// lets a reader answer "which backend ran this" from the record instead of guessing it
// from Command[0], so a request without one is refused.
//
// ItemID is the item the run was dispatched for. It is copied onto the record and the
// runs row (D-UT19), and it is optional: a run dispatched for no item names none, and
// the record then carries no item_id rather than an empty string read as an id.
type Request struct {
	// ReadOnlyGroup shares one lane lease with sibling readers of the same item.
	ReadOnlyGroup string `json:"read_only_group,omitempty"`
	RunID         string `json:"run_id"`
	ItemID        string `json:"item_id"`
	// NodeID optionally identifies the node whose program this run executes.
	NodeID     string   `json:"node_id,omitempty"`
	Seat       string   `json:"seat,omitempty"`
	Workflow   string   `json:"workflow,omitempty"`
	Node       string   `json:"node,omitempty"`
	LaneID     string   `json:"lane_id"`
	Backend    string   `json:"backend"`
	Command    []string `json:"command"`
	WorkingDir string   `json:"working_dir"`
	// TimeoutMS is the run's wall, and 0 means there is none: the run is bounded by the
	// operator's own stop and the lane's lease carries no expiry (`leased_until = NULL`,
	// design D-RW1). A negative value is refused rather than read as unbounded.
	TimeoutMS int      `json:"timeout_ms,omitempty"`
	EnvRefs   []string `json:"env_refs"`
	// ScratchEnv defaults to true when omitted. False preserves the caller's scratch
	// environment; the run still creates and records its own scratch directory.
	ScratchEnv *bool `json:"scratch_env,omitempty"`
	// CorrelationID is the tick this request belongs to, in the tick's own spelling
	// (`tick-<stamp>-<run ids>`). Drain stamps it when it emits the request, RequestRecord
	// copies it onto the record envelope, and the run writes it into the run.allocate
	// event, so the first hop of a run is joinable by key rather than by a payload search.
	// It is empty for a request no tick emitted, which is a request with no timeline to join.
	CorrelationID string `json:"correlation_id,omitempty"`
	// Resume says this execution CONTINUES the run rather than starting it: the same run
	// id, the same lane, the same brief and fence, and the same harness session staging
	// pinned. It is a property of the execution and not of the work, which is why the run id
	// is unchanged: a resumed attempt is one attempt continued, not a second attempt.
	//
	// A resume is refused unless the lane carries a session id (session-id in the lane
	// directory), because a resume with no session to continue is a cold start wearing the
	// wrong name: it would pay for the whole transcript again and record itself as a
	// continuation.
	Resume bool `json:"resume,omitempty"`
	// SessionTurn starts a new run record in the session staging retained after
	// a refusal. Unlike Resume, it claims a new run and preserves both endings.
	SessionTurn bool `json:"session_turn,omitempty"`
	// ResumePrompt supplies a continuation-specific instruction. Empty uses the
	// restart notice or the default interrupted-session prompt. Only read on resume.
	ResumePrompt string `json:"resume_prompt,omitempty"`
	// FreshSession continues the attempt with a new harness session, for example
	// when quota forces it onto another backend. Only meaningful with Resume.
	FreshSession bool `json:"fresh_session,omitempty"`
}

func (r Request) effectiveNode() string {
	if r.Node != "" {
		return r.Node
	}
	return r.NodeID
}

// ExecOptions is what the face supplies that the request does not: where the lane root
// is, where the backends' extractors live, and the clock.
//
// BackendsDir is how the run's spend is read: `run exec` runs the `extract-report` beside
// the adapter the request's `Command[0]` names over the run's log once the process is done,
// and copies the two spend keys of the report header onto the record. It is a directory and
// not a program because the adapter is what says which dialect the log is in, and the
// backend's name is not a directory: `pi-designer` is the same adapter on another model, so
// its extractor is `backends/pi/extract-report`. A relative adapter is resolved against this
// directory by its own last component (a policy names it relative to the repository root).
// An empty BackendsDir reads no spend: a fixture run needs no extractor, and the record then
// says the spend is unknown.
type ExecOptions struct {
	LaneRoot    string
	BackendsDir string
	// Now is the injected clock. It is the run's start for the claim, which happens
	// before there is a process to take one from, and it stamps the record only when the
	// run did not actually start a process.
	Now time.Time
	// PauseAfter is the injected straggler timer: once a pause is requested, the runner
	// waits on the channel it returns for the bound before it kills the harness. Nil is
	// time.After. A test supplies one to observe the bound the runner chose and to decide
	// when it elapses, so "the runner waited for the bound" is an ordering, not a duration.
	PauseAfter func(time.Duration) <-chan time.Time
	// SettledAfter supplies sample instants after a settled report is observed.
	// Nil uses time.After; natural immediate exits never arm it.
	SettledAfter func(time.Duration) <-chan time.Time
}

// SessionIDFile is the lane layout's harness session handle: one UUID, in the lane directory
// beside the brief. Staging mints it, every adapter reads it (`run-pi` hands it to `pi
// --session-id`), and a resume reads it back to prove there is a session to continue.
//
// The name is stated here, in the utility that reads it back, and drain's own SessionIDFile
// aliases it: the lane layout has one writer (staging) and one reader (this), and a second
// spelling is how a lane ends up with a session id nothing continues.
const SessionIDFile = "session-id"

// sessionIDShape is the one form a session id may take: a UUID. The value is handed straight
// to a harness flag, so a truncated write or a shell word in the file is refused here rather
// than passed on.
var sessionIDShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ReadSessionID answers the session id a lane directory carries, or "" when it carries none.
func ReadSessionID(laneDir string) (string, error) {
	path := filepath.Join(laneDir, SessionIDFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", Externalf("reading the session id at %s: %v", path, err)
	}
	session := strings.TrimSpace(string(raw))
	if session == "" {
		return "", nil
	}
	if !IsSessionID(session) {
		return "", Externalf("%s holds %q, which is not a session id: a session id is a UUID, and every adapter hands this value to a harness flag", path, session)
	}
	return session, nil
}

// IsSessionID answers whether s is the one form a session id may take: a UUID. Every adapter
// hands the value to a harness flag that keys a session by it, so a truncated write or a
// shell word is refused rather than passed on.
func IsSessionID(s string) bool { return sessionIDShape.MatchString(s) }

// openRunLog opens the run's log: truncated for a fresh run, appended to for a resume. A
// resume that truncated would delete the killed segment's transcript, which is the evidence
// the resume continues and the report the run is classified from.
func openRunLog(path string, resume bool) (*os.File, error) {
	if resume {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, Internalf("opening the run log %q for a resume: %v", path, err)
		}
		return file, nil
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, Internalf("creating the run log %q: %v", path, err)
	}
	return file, nil
}

// DurableDir is the run's own scratch directory: <laneDir>/durable/<run_id>. It is named
// once, here, because two places have to agree on the spelling: `exec` exports it as the
// child's YOLO_DURABLE_DIR, and staging states it in the brief so a worker knows where its
// scratch belongs. It sits outside the lane's worktree on purpose: the fence describes
// tracked paths, and a scratch directory inside the tree would be a change the fence has to
// admit.
func DurableDir(laneDir, runID string) string {
	return filepath.Join(laneDir, "durable", runID)
}

// NodeDir keeps a parallel node's brief, session, log and report apart from siblings.
func NodeDir(laneDir, runID string) string { return filepath.Join(laneDir, "nodes", runID) }

// Exec runs one request to a terminal state and records it.
//
// The lane root is the caller's: exec creates it and the run's lane directory under
// it, and the command runs with its working directory inside the lane directory. That
// is what makes a run's artifacts findable, and what lets §5.6's replay scenario run
// against a fixture lane root instead of the real checkout.
//
// now is the injected clock. It is the run's start for the claim, which happens before
// there is a process to take one from, and it stamps the record only when the run did not
// actually start a process: a run that started one takes its timestamps from the process,
// because a fabricated started_at would make the record a lie about what happened.
func Exec(ctx context.Context, s *store.Store, req Request, opts ExecOptions) (record.Envelope, error) {
	if err := checkRequest(req); err != nil {
		return record.Envelope{}, err
	}
	// With no backend directory this package can execute a raw fixture command.
	// The CLI always supplies a directory and requires an adapter declaration.
	pauseKind := ""
	if opts.BackendsDir != "" && req.Backend != "command" {
		var err error
		pauseKind, err = ReadPauseKind(opts.BackendsDir, req.Command[0])
		if err != nil {
			return record.Envelope{}, err
		}
	}
	now := opts.Now
	laneID := req.LaneID
	if laneID == "" {
		// Deriving the lane from the run id rather than from a counter keeps a replayed
		// request producing the same lane, which is what makes §5.6's replay comparable.
		laneID = "lane_" + req.RunID
	}
	root, err := filepath.Abs(opts.LaneRoot)
	if err != nil {
		return record.Envelope{}, Internalf("resolving the lane root %q: %v", opts.LaneRoot, err)
	}
	laneDir := filepath.Join(root, laneID)
	workDir := filepath.Join(laneDir, req.WorkingDir)
	artifactDir := laneDir
	if req.ReadOnlyGroup != "" {
		artifactDir = NodeDir(laneDir, req.RunID)
	}
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		return record.Envelope{}, err
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return record.Envelope{}, Internalf("creating the lane directory %q: %v", workDir, err)
	}
	// The run's scratch is its own and it exists before the process does, because a child
	// that has to create it races its sibling for the same tree and one of them loses.
	durableDir := DurableDir(laneDir, req.RunID)
	if err := os.MkdirAll(durableDir, 0o755); err != nil {
		return record.Envelope{}, Internalf("creating the run's scratch directory %q: %v", durableDir, err)
	}
	// A resumed run continues the session staging pinned, and a resume with no session to
	// continue is refused before anything is claimed: it would pay for the whole transcript
	// again while recording itself as a continuation.
	sessionID := ""
	if req.SessionTurn && !req.Resume {
		sessionID, err = ReadSessionID(artifactDir)
		if err != nil {
			return record.Envelope{}, err
		}
		if sessionID == "" {
			return record.Envelope{}, Usagef("run %q cannot continue a worker session: the lane has no session id", req.RunID)
		}
	}
	if req.Resume {
		sessionID, err = continuationSessionID(ctx, s, req, artifactDir)
		if err != nil {
			return record.Envelope{}, err
		}
		if sessionID == "" {
			return record.Envelope{}, Usagef("run %q cannot be resumed: %s carries no session id, so there is no session to continue and a resume would start cold", req.RunID, filepath.Join(artifactDir, SessionIDFile))
		}
	}
	mode, prompt, freshSession, err := prepareContinuationSession(req, pauseKind, artifactDir, workDir, durableDir)
	if err != nil {
		return record.Envelope{}, err
	}
	pausePath := filepath.Join(durableDir, "pause-request")
	if err := os.Remove(pausePath); err != nil && !os.IsNotExist(err) {
		return record.Envelope{}, Internalf("clearing prior pause request: %v", err)
	}
	harnessPath := filepath.Join(durableDir, "harness")
	if err := os.Remove(harnessPath); err != nil && !os.IsNotExist(err) {
		return record.Envelope{}, Internalf("clearing prior harness record: %v", err)
	}
	// The new id exists only in memory until the handoff is durably claimed.
	if freshSession != "" {
		sessionID = freshSession
	}
	logPath := filepath.Join(artifactDir, req.RunID+".log")
	// A resume appends to the run's log instead of truncating it. The log is the run's record
	// of what its harness said, and a killed attempt's half of it is the evidence the resume
	// exists to keep: `os.Create` here would delete the transcript the second segment is
	// continuing, and the report the attempt is classified from along with it.
	logFile, err := openRunLog(logPath, req.Resume)
	if err != nil {
		return record.Envelope{}, err
	}

	payload := Record{
		RunID:    req.RunID,
		ItemID:   req.ItemID,
		LaneID:   laneID,
		Backend:  req.Backend,
		Seat:     req.Seat,
		Workflow: req.Workflow,
		Node:     req.effectiveNode(),
		Resumed:  req.Resume,
		// The route is read from the argv this run is about to hand the adapter, not
		// drawn again: the request is the record's only source, so the record names the
		// model id that process received.
		Route: req.Route(),
		ArtifactRefs: []ArtifactRef{
			{Kind: "lane-root", Ref: laneDir},
			{Kind: "log", Ref: logPath},
			{Kind: "scratch", Ref: durableDir},
		},
	}
	if req.Backend == "command" {
		payload.Route = ""
	}
	// The lane is claimed before the process exists: the lease has to be held for as
	// long as the run can be doing anything, and it is the only thing that tells a
	// planner or a reaper the lane is taken (design §5.6). Its clock is the injected
	// one, because at claim time there is no process to take a start from; a run that
	// does start one records that process's own timestamps in the terminal record.
	if err := claimLane(ctx, s, req, laneID, sessionID, now); err != nil {
		logFile.Close()
		return record.Envelope{}, err
	}
	if req.Resume {
		if err := os.WriteFile(filepath.Join(artifactDir, SessionIDFile), []byte(sessionID+"\n"), 0600); err != nil {
			logFile.Close()
			return record.Envelope{}, Internalf("publishing claimed session: %v", err)
		}
	}

	// An earlier execution of this run (a resume) may have left processes behind when its
	// runner died before cleaning up; they are this run's, and the lane is about to be
	// worked again, so end them before the worker starts. This runs before the wall's clock
	// starts, so a scan of a busy process table never spends the run's own time.
	if sweep := newExecMark("", req.RunID); sweep.byRun() {
		if err := endRunProcesses(0, sweep); err != nil {
			logFile.Close()
			return record.Envelope{}, err
		}
	}

	// A wall is a bound on total run duration, and it exists only when the request names
	// one: 0 is an unbounded run, whose lane lease carries no expiry either
	// (`leased_until = NULL`, design D-RW1). The context is still cancellable, because a
	// stop has to reach the process group whether or not a clock would have.
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if req.TimeoutMS > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
	}
	defer cancel()
	adapterPath := resolveExecAdapter(opts.BackendsDir, req.Backend, req.Command[0])
	cmd := exec.CommandContext(runCtx, adapterPath, req.Command[1:]...)
	cmd.Dir = workDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// The run's identity and its scratch travel in the adapter's environment, because the
	// adapter is what names the run's record directory for the shells and git calls it
	// spawns (design §4.2). `env_refs` is the store-resolved spelling of the same
	// propagation and is SUB-2, refused above; the values exec already holds are the ones it
	// can honestly pass. A backend that ignores them records nothing and behaves exactly as
	// before.
	//
	// Replace inherited identities, including an optional node absent from this request:
	// a nested run must never claim its parent's item or node. Scratch is isolated by
	// default, but an explicit opt-out preserves the caller's assignment (or absence).
	cmd.Env = os.Environ()
	piBuildsDir, hasPiBuilds := os.LookupEnv("TILLR_PI_BUILDS_DIR")
	if !hasPiBuilds {
		if callerDurable := os.Getenv("YOLO_DURABLE_DIR"); callerDurable != "" {
			piBuildsDir = filepath.Join(callerDurable, "pi-builds")
			hasPiBuilds = true
		}
	}
	execID, err := newExecID()
	if err != nil {
		logFile.Close()
		return record.Envelope{}, err
	}
	for _, key := range []string{"TILLR_RUN_ID", "TILLR_ITEM_ID", "TILLR_NODE_ID", "TILLR_LANE_ID", "TILLR_SESSION_MODE", "TILLR_RESUME_PROMPT_FILE", "TILLR_PAUSE_REQUEST", "TILLR_PROGRESS_FILE", "TILLR_CHILD_SESSIONS_ROOT", "TILLR_CHILD_SESSIONS_MARKER", "TILLR_READ_ONLY_GROUP", "TILLR_PI_BUILDS_DIR", "TILLR_HARNESS_RECORD_FILE", "TILLR_RUN_EXEC_ID"} {
		cmd.Env = withoutEnv(cmd.Env, key)
	}
	if req.ScratchEnv == nil || *req.ScratchEnv {
		cmd.Env = append(withoutEnv(cmd.Env, "YOLO_DURABLE_DIR"), "YOLO_DURABLE_DIR="+durableDir)
	}
	if hasPiBuilds {
		cmd.Env = append(cmd.Env, "TILLR_PI_BUILDS_DIR="+piBuildsDir)
	}
	agentDirsDir, hasAgentDirsDir := os.LookupEnv("TILLR_PI_AGENT_DIRS_DIR")
	if !hasAgentDirsDir {
		if callerDurable := os.Getenv("YOLO_DURABLE_DIR"); callerDurable != "" {
			agentDirsDir = filepath.Join(callerDurable, "pi-agent-dirs")
			hasAgentDirsDir = true
		}
	}
	extSetPath := filepath.Join(durableDir, "extension-set")
	if err := os.Remove(extSetPath); err != nil && !os.IsNotExist(err) {
		logFile.Close()
		return record.Envelope{}, Internalf("clearing prior extension-set record: %v", err)
	}
	cmd.Env = withoutEnv(cmd.Env, "TILLR_PI_AGENT_DIRS_DIR")
	if hasAgentDirsDir {
		cmd.Env = append(cmd.Env, "TILLR_PI_AGENT_DIRS_DIR="+agentDirsDir)
	}
	cmd.Env = append(withoutEnv(cmd.Env, "TILLR_EXTENSION_SET_RECORD_FILE"), "TILLR_EXTENSION_SET_RECORD_FILE="+extSetPath)
	if req.NodeID != "" {
		cmd.Env = append(cmd.Env, "TILLR_NODE_ID="+req.NodeID)
	}
	// A read-only node is told it is one, so an adapter that keeps a writer going until it
	// has committed (backends/pi/keep-going.ts) leaves a reader to end when it is done.
	if req.ReadOnlyGroup != "" {
		cmd.Env = append(cmd.Env, "TILLR_READ_ONLY_GROUP="+req.ReadOnlyGroup)
	}
	cmd.Env = append(cmd.Env,
		"TILLR_SESSION_MODE="+mode,
		"TILLR_RESUME_PROMPT_FILE="+prompt,
		"TILLR_PAUSE_REQUEST="+pausePath,
		"TILLR_HARNESS_RECORD_FILE="+harnessPath,
		"TILLR_PROGRESS_FILE="+filepath.Join(durableDir, "progress"),
		"TILLR_CHILD_SESSIONS_ROOT="+filepath.Join(durableDir, "pi-subagents"),
		"TILLR_CHILD_SESSIONS_MARKER="+filepath.Join(durableDir, "child-sessions-recorded"),
		"TILLR_RUN_ID="+req.RunID,
		"TILLR_ITEM_ID="+req.ItemID,
		"TILLR_LANE_ID="+laneID,
		"TILLR_RUN_EXEC_ID="+execID,
	)
	cmd.Env, err = installGitGuard(workDir, durableDir, cmd.Env)
	if err != nil {
		logFile.Close()
		return record.Envelope{}, err
	}
	// The child leads its own process group, and the bound kills the group plus any
	// detached sessions carrying this execution's marker.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	mark := newExecMark(execID, req.RunID)
	// The terminal write is the record a stop must not lose: the caller's context is
	// canceled exactly when the run is being stopped, so the write is detached from it. A
	// stop that left no row and no release would be the orphan the operator cannot see.
	writeCtx := context.WithoutCancel(ctx)

	streamOffset := int64(0)
	if req.Resume {
		if info, err := logFile.Stat(); err == nil {
			streamOffset = info.Size()
		}
	}
	workerRequest := req
	workerRequest.LaneID = laneID
	workerRecords, recordsErr := newWorkerRecords(writeCtx, s, workerRequest, durableDir, logPath)
	if recordsErr != nil {
		logFile.Close()
		return record.Envelope{}, recordsErr
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		// There was no process, so the injected clock is the only clock there is.
		payload.State = StateFailedToStart
		payload.EndCause = EndFailedToStart
		payload.StartedAt = now.UTC().Format(time.RFC3339)
		payload.FinishedAt = payload.StartedAt
		stored, writeErr := writeTerminal(writeCtx, s, payload, now, "")
		if writeErr != nil {
			return record.Envelope{}, writeErr
		}
		return stored, Externalf("starting %q: %v", req.Command[0], err)
	}
	// Lower the group once; later children inherit their parent's niceness.
	// A group that has already exited needs no adjustment.
	if err := syscall.Setpriority(syscall.PRIO_PGRP, cmd.Process.Pid, WorkerNiceness); err != nil && err != syscall.ESRCH {
		os.Stderr.WriteString("tillr-run: the worker's CPU priority was not lowered: " + err.Error() + "\n")
	}
	var note string
	startedAt := time.Now().UTC()
	monitor := &settledMonitor{log: logPath, stderr: filepath.Join(artifactDir, "stream.err"), backendDir: opts.BackendsDir, adapter: req.Command[0], backend: req.Backend, durable: durableDir, after: opts.SettledAfter}
	if monitor.after == nil {
		monitor.after = time.After
	}
	if req.Backend == "command" {
		monitor.disabled = true
	}
	monitor.records = func() error { return workerRecords.sample(writeCtx, false) }
	monitor.offset = streamOffset
	monitor.startOffset = streamOffset
	waitErr, paused, interrupted, pauseErr := waitForPause(runCtx, s, cmd, req.RunID, mark, pauseKind, pausePath, opts.PauseAfter, monitor)
	// Wait reaps the worker, not its background children or detached sessions. End all
	// remaining marked processes before releasing the lane in the terminal write.
	if cleanupErr := endRunProcesses(cmd.Process.Pid, mark); cleanupErr != nil {
		logFile.Close()
		return record.Envelope{}, cleanupErr
	}
	finishedAt := time.Now().UTC()
	logFile.Close()
	// The adapter's last line can arrive after the final sample; read it now.
	if monitor.observe() == nil && monitor.harnessEnded != "" && !paused && runCtx.Err() == nil {
		payload.EndingException = monitor.harnessEnded
	}
	if err := workerRecords.sample(writeCtx, true); err != nil && pauseErr == nil {
		pauseErr = Internalf("recording worker progress and sessions: %v", err)
	}

	switch {
	case paused:
		payload.State = StatePaused
		payload.EndCause = EndRestart
		notice := "Continue the same attempt after the restart, from where you stopped.\n"
		if interrupted {
			notice = "The session was interrupted by a restart. Preserve existing work and continue the same attempt.\n"
			if pauseKind == PauseAtGap {
				notice += "The command was interrupted by a restart and may be run again.\n"
			}
		}
		if err := os.WriteFile(filepath.Join(durableDir, "pause-notice"), []byte(notice), 0600); err != nil {
			return record.Envelope{}, Internalf("writing restart notice: %v", err)
		}
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		// A timed-out run is a completed run: the utility ran and observed the bound.
		payload.State = StateTimedOut
		payload.EndCause = EndTimeLimit
	case monitor.repeating != nil:
		// The harness ended the session itself because the model kept repeating one
		// identical call. The exit is the harness's own; the cause names why.
		code := cmd.ProcessState.ExitCode()
		payload.State = StateExited
		payload.ExitCode = &code
		payload.EndCause = EndRepeatingCall
		payload.RepeatingCall = monitor.repeating
	case monitor.end != nil:
		code := cmd.ProcessState.ExitCode()
		payload.State = StateExited
		payload.ExitCode = &code
		payload.EndCause = EndSettledIdle
		payload.SettledAt = monitor.end.settledAt
		payload.HarnessEndedAt = monitor.end.endedAt
	case waitErr != nil:
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return record.Envelope{}, Internalf("waiting for %q: %v", req.Command[0], waitErr)
		}
		code := exitErr.ExitCode()
		payload.State = StateExited
		payload.ExitCode = &code
		payload.EndCause = EndNormalExit
		status, hasStatus := exitErr.Sys().(syscall.WaitStatus)
		var sig syscall.Signal
		switch {
		case hasStatus && status.Signaled():
			sig = status.Signal()
		case code > 128 && code < 128+65:
			// An adapter forwards its harness's death as 128 plus the signal number.
			sig = syscall.Signal(code - 128)
		}
		name := SignalName(sig)
		payload.Signal = name
		// Only an external SIGTERM or SIGINT is an interruption. Every other signal,
		// crash or SIGKILL, keeps normal-exit so a broken backend still counts against
		// itself; the Signal field says which one it was.
		if payload.EndingException == "" && (sig == syscall.SIGTERM || sig == syscall.SIGINT ||
			errors.Is(runCtx.Err(), context.Canceled)) {
			payload.EndCause = EndSignal
		}
	default:
		code := 0
		payload.EndCause = EndNormalExit
		payload.State = StateExited
		payload.ExitCode = &code
	}
	if monitor.loopRecovery != nil {
		payload.LoopRecovery = monitor.loopRecovery
	}
	payload.StartedAt = startedAt.Format(time.RFC3339)
	payload.FinishedAt = finishedAt.Format(time.RFC3339)
	if rawHarness, err := os.ReadFile(harnessPath); err == nil {
		payload.Harness = strings.TrimSpace(string(rawHarness))
	}
	// The spend is read after the process is done and before the record is written, so
	// the record that says how the run ended also says what it cost. A failed extraction
	// leaves the usage unknown and travels as a note, never as a run failure: a run that
	// produced a candidate must not be reported as an infrastructure failure because a
	// report could not be parsed.
	reportPath := filepath.Join(durableDir, "report.md")
	if err := os.Remove(reportPath); err != nil && !os.IsNotExist(err) {
		return record.Envelope{}, Internalf("clearing the prior extracted report: %v", err)
	}
	// Final accounting must survive every cancellation of the face. readSpend still
	// applies its own bounded timeout to this detached context.
	payload.Usage, note = readSpend(context.WithoutCancel(ctx), opts.BackendsDir, req.Command[0], req.Backend, durableDir, logPath, payload.ExitCode, true)
	payload.SpendNote = note
	// The extracted report is run evidence, not the reusable lane's latest output.
	// Keep a pinned artifact for settlement and history, and the conventional copy
	// the lane driver reads. Never attach a report left by a previous extraction.
	if body, err := os.ReadFile(reportPath); err == nil {
		payload.ArtifactRefs = append(payload.ArtifactRefs, ArtifactRef{Kind: "report", Ref: reportPath})
		if err := os.WriteFile(filepath.Join(artifactDir, "report.md"), body, 0o644); err != nil {
			return record.Envelope{}, Internalf("writing the lane report: %v", err)
		}
	}
	if rawExtSet, err := os.ReadFile(extSetPath); err == nil {
		payload.ExtensionSet = strings.TrimSpace(string(rawExtSet))
	}
	stored, terminalErr := writeTerminal(writeCtx, s, payload, finishedAt, note)
	if terminalErr != nil {
		return stored, terminalErr
	}
	return stored, pauseErr
}

// withoutEnv answers env with every assignment to key removed, so the value appended after
// it is the only one the child sees. A later assignment does not overwrite an earlier one in
// the child — the first match wins — so removal is the only way to replace an inherited
// value.
func withoutEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// SpendExtractorTimeout bounds the extractor: it reads a file the run already wrote, so a
// minute is generous, and an extractor that hangs must not hold a finished run's record
// hostage.
const SpendExtractorTimeout = 60 * time.Second

// readSpend runs the adapter's extractor over the run's log and answers the spend it
// reports, plus a note when the spend could not be read at all.
//
// The report header is the contract, and the extractor is the only thing that knows the
// adapter's stream dialect: `tokens` and `cost usd` are the two keys it copies. `unknown`
// in either is the report saying nobody measured it, which stays nil here — a zero would
// be a measurement this schema never made.
//
// The extractor is resolved from the adapter and not from the backend: a backend's name is a
// dispatch-time choice among invocations of one adapter (`pi-designer` runs the pi adapter
// on the design seat's model), so `<BackendsDir>/<backend>/extract-report` names a directory
// that does not exist and the spend would read unknown for every seated backend.
//
// The note is what makes a failed extraction visible without touching the run's outcome:
// the caller appends it as a run.usage event, so the log says the spend is missing and
// why, and the record says the spend is unknown. The extractor's own non-zero exit is not
// by itself a failure: exit 4 is "the report is incomplete", and an incomplete report
// still carries the header and still writes the file.
func readSpend(ctx context.Context, backendsDir, adapter, backend, laneDir, logPath string, exitCode *int, final ...bool) (*Usage, string) {
	if backend == "command" {
		return nil, ""
	}
	if backendsDir == "" {
		return nil, "no backend extractor was configured"
	}
	extractor := extractorFor(backendsDir, adapter)
	if extractor == "" {
		return nil, "the adapter " + adapter + " names no directory to read a dialect from: the backend's spend is unknown"
	}
	if info, err := os.Stat(extractor); err != nil || info.IsDir() {
		return nil, "no extractor at " + extractor + ": the backend's spend is unknown"
	}
	// Give each extraction a fresh destination. A killed retry (or resume) whose
	// extractor writes nothing must not read the preceding attempt's lane report.
	reportDir, err := os.MkdirTemp(laneDir, "spend-")
	if err != nil {
		return nil, "creating a fresh spend report directory: " + err.Error()
	}
	reportPath := filepath.Join(reportDir, "report.md")
	exitText := "unknown"
	if exitCode != nil {
		exitText = strconv.Itoa(*exitCode)
	}
	extractCtx, cancel := context.WithTimeout(ctx, SpendExtractorTimeout)
	defer cancel()
	args := []string{"--backend", backend, "--exit-code", exitText, "--out", reportPath}
	// Only the final extraction reads the run-local offer; live probes still
	// determine whether the stream itself has ended.
	if len(final) != 0 && final[0] {
		offer := filepath.Join(laneDir, "landing-offer.json")
		if _, err := os.Stat(offer); err == nil {
			args = append(args, "--landing-offer", offer)
		}
	}
	out, runErr := exec.CommandContext(extractCtx, extractor, append(args, logPath)...).CombinedOutput()
	body, readErr := os.ReadFile(reportPath)
	if readErr != nil {
		return nil, "the extractor wrote no report at " + reportPath + " (" + strings.TrimSpace(string(out)) + "): the backend's spend is unknown"
	}
	// Publish only this fresh extraction for the run's pinned report artifact.
	// Keep it even when spend parsing fails: the blocked result is independent
	// of whether the extractor measured usage.
	if err := os.WriteFile(filepath.Join(laneDir, "report.md"), body, 0o644); err != nil {
		return nil, "preserving the extracted report: " + err.Error()
	}
	usage, parseErr := parseSpend(string(body))
	if parseErr != nil {
		return nil, parseErr.Error()
	}
	if detailBody, err := os.ReadFile(reportPath + ".usage.json"); err == nil {
		var details *UsageDetails
		if err := json.Unmarshal(detailBody, &details); err != nil {
			return usage, "reading usage details: " + err.Error()
		}
		if details != nil {
			if usage == nil {
				usage = &Usage{}
			}
			usage.Details = details
		}
	} else if !os.IsNotExist(err) {
		return usage, "reading usage details: " + err.Error()
	}
	if runErr != nil && usage == nil {
		return nil, "the extractor answered " + runErr.Error() + " and its report carries no spend"
	}
	if usage == nil {
		return nil, "the backend report carries no spend measurement"
	}
	return usage, ""
}

// extractorFor answers the `extract-report` program for a run's adapter: the file beside the
// adapter, because the dialect belongs to the adapter and not to the backend's name.
//
// An absolute adapter carries its directory. A relative one is how a policy names it, from
// the repository root; the backends directory is the same tree's, so the adapter's own
// directory is the last component of the path it was named by — `tools/tillr-run/backends/pi`
// under `<repo>/tools/tillr-run/backends` is `<BackendsDir>/pi`. A backend's name never
// becomes a directory name, which is what makes `pi-designer` find the pi extractor.
//
// A bare adapter name answers "", because PATH is where that program is and no directory
// beside it is known; guessing one would be the same defect this function replaces.
func extractorFor(backendsDir, adapter string) string {
	dir := filepath.Dir(adapter)
	if !filepath.IsAbs(dir) {
		if dir == "." {
			return ""
		}
		dir = filepath.Join(backendsDir, filepath.Base(dir))
	}
	return filepath.Join(dir, "extract-report")
}

func resolveExecAdapter(backendsDir, backend, adapter string) string {
	if backendsDir == "" || backend == "command" || adapter == "" || filepath.IsAbs(adapter) || !strings.ContainsRune(adapter, filepath.Separator) {
		return adapter
	}
	extractor := extractorFor(backendsDir, adapter)
	if extractor == "" {
		return adapter
	}
	candidate := filepath.Join(filepath.Dir(extractor), filepath.Base(adapter))
	if abs, err := filepath.Abs(candidate); err == nil {
		candidate = abs
	}
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return adapter
}

// parseSpend reads the two spend keys of the report header, and nothing else: the header
// ends at the `---` rule, so a body that happens to say `tokens:` is not read as a header.
func parseSpend(report string) (*Usage, error) {
	head, _, _ := strings.Cut(report, "\n---\n")
	var (
		usage     *Usage
		sawTokens bool
		sawCost   bool
	)
	usage = &Usage{}
	for _, line := range strings.Split(head, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch key {
		case "tokens":
			if value == "unknown" {
				continue
			}
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return nil, Internalf("the report's token total %q is not a number: %v", value, err)
			}
			usage.Tokens, sawTokens = &parsed, true
		case "cost usd":
			if value == "unknown" {
				continue
			}
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, Internalf("the report's cost %q is not a number: %v", value, err)
			}
			usage.CostUSD, sawCost = &parsed, true
		}
	}
	if !sawTokens && !sawCost {
		return nil, nil
	}
	return usage, nil
}

// checkRequest refuses a request the face cannot honour, before anything is created or
// written: a refused request leaves no lane directory, no row and no event.
func checkRequest(req Request) error {
	if req.RunID == "" {
		return Usagef("a run.request needs a run_id")
	}
	if req.ReadOnlyGroup != "" && (req.ItemID == "" || req.NodeID == "" || req.RunID == "." || req.RunID == ".." || strings.ContainsAny(req.RunID, "/\\")) {
		return Usagef("a read-only group needs an item, node and safe run id")
	}
	if req.Backend == "" {
		return Usagef("a run.request needs a backend: the adapter program that produced the run, named by staging rather than guessed from argv")
	}
	if len(req.Command) == 0 {
		return Usagef("a run.request needs a command: an argv array, never a shell string")
	}
	if filepath.IsAbs(req.WorkingDir) {
		return Usagef("working_dir %q is absolute: a run's working directory is relative to its lane directory, because a run that can write anywhere is not confined to a lane root", req.WorkingDir)
	}
	if req.TimeoutMS < 0 {
		// A wall of zero is a choice — the run is unbounded and its lane lease carries no
		// expiry (design D-RW1) — but a negative wall is only ever a mistake in staging.
		return Usagef("a run.request names timeout_ms %d: a wall is zero or more milliseconds, and 0 is an unbounded run", req.TimeoutMS)
	}
	if len(req.EnvRefs) > 0 {
		return Usagef("env_refs %v cannot be resolved: the substrate config loader that resolves them is SUB-2 (tools/tillr/config) and has not landed, and resolving them from this process's environment would be a silent divergence from design §5.6", req.EnvRefs)
	}
	return nil
}

// claimLane takes the lane for this run in one transaction, before the process exists.
//
// The transaction is the point: the lease and the record of the run's start commit
// together, so a lane is never leased by a run whose start nobody can read, and a
// refused claim leaves neither.
func claimLane(ctx context.Context, s *store.Store, req Request, laneID, sessionID string, startedAt time.Time) error {
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := ClaimLease(ctx, tx, req, laneID, startedAt); err != nil {
			return err
		}
		if !req.Resume {
			return nil
		}
		if _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id = ?`, req.RunID); err != nil {
			return err
		}
		return appendResume(ctx, tx, req, laneID, sessionID, startedAt)
	})
}

// resume is the run.resume event's payload: which run was continued, on which lane and
// backend, from which harness session, and when. It is the resume's own record — a new span
// on the same run rather than a new runs row — because a resumed attempt is one attempt
// continued: the run's row stays one row, and this event says the run has a second segment.
//
// It is appended in the claim's transaction, before the process exists, so a resume that is
// itself killed is still a recorded resume: the row would otherwise be the only evidence, and
// a row rewritten twice cannot say it was resumed at all.
type resume struct {
	// Request records a backend handoff so a replacement loop continues the current
	// command, not the command of the original segment.
	Request   *Request `json:"request,omitempty"`
	RunID     string   `json:"run_id"`
	ItemID    string   `json:"item_id,omitempty"`
	LaneID    string   `json:"lane_id"`
	Backend   string   `json:"backend"`
	SessionID string   `json:"session_id"`
	ResumedAt string   `json:"resumed_at"`
}

// appendResume writes one run.resume event on the run it continues.
// Keep subsecond precision in the payload: readers distinguish executions from
// event replay even when the store's millisecond timestamps are identical.
func appendResume(ctx context.Context, tx *store.Tx, req Request, laneID, sessionID string, at time.Time) error {
	body, err := json.Marshal(resume{
		Request:   &req,
		RunID:     req.RunID,
		ItemID:    req.ItemID,
		LaneID:    laneID,
		Backend:   req.Backend,
		SessionID: sessionID,
		ResumedAt: at.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return Internalf("encoding the %s event for %q: %v", ResumeKind, req.RunID, err)
	}
	if _, err := tx.Append(event.Event{
		Kind:       ResumeKind,
		ObjectType: "run",
		ObjectID:   &req.RunID,
		Payload:    body,
		At:         at.UTC(),
	}); err != nil {
		return Internalf("appending the %s event for %q: %v", ResumeKind, req.RunID, err)
	}
	return nil
}

// writeTerminal records the terminal state in one transaction: the lane and run rows and
// the finish event commit together, so a reader never sees a run row without the events
// that describe it. The lane's release is part of that transaction (Apply writes it), so
// the record that says a run finished is the record that frees its lane.
func writeTerminal(ctx context.Context, s *store.Store, payload Record, at time.Time, usageNote string) (record.Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the run record: %v", err)
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          RecordKind,
		ID:            "run-record-" + payload.RunID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at.UTC(),
		Subject:       &record.Subject{Kind: "run", ID: payload.RunID},
		Payload:       body,
	}
	var stored record.Envelope
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		// The usage event is appended first, so the row's event_seq still names the event
		// that last wrote it: Apply's finish event is the final write.
		if usageNote != "" {
			body, err := json.Marshal(map[string]string{"run_id": payload.RunID, "backend": payload.Backend, "error": usageNote})
			if err != nil {
				return Internalf("encoding the usage note for %q: %v", payload.RunID, err)
			}
			if _, err := tx.Append(event.Event{
				Kind:       UsageKind,
				ObjectType: "run",
				ObjectID:   &payload.RunID,
				Payload:    body,
				At:         at.UTC(),
			}); err != nil {
				return Internalf("appending the %s event: %v", UsageKind, err)
			}
		}
		var err error
		stored, err = Apply(ctx, tx, env)
		return err
	})
	if err != nil {
		return record.Envelope{}, err
	}
	return stored, nil
}
