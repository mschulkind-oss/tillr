package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	landingOfferFile  = "landing-offer.json"
	landingAnswerFile = "landing-answer.json"
	callOffered       = "call.offered"
	callAnswered      = "call.answered"
	callLanded        = "call.landed"
	callDelivered     = "call.delivered"
	callClosed        = "call.closed"
)

type landingOfferObstacle struct {
	Kind   string            `json:"kind"`
	Fields map[string]string `json:"fields"`
}

type landingOfferFinding struct {
	Cause       string `json:"cause"`
	Evidence    string `json:"evidence"`
	WhyNoChange string `json:"why_no_change"`
}

type landingOffer struct {
	CallID     string                `json:"call_id"`
	Tool       string                `json:"tool,omitempty"`
	Commit     string                `json:"commit,omitempty"`
	Base       string                `json:"base"`
	Obstacle   *landingOfferObstacle `json:"obstacle,omitempty"`
	Finding    *landingOfferFinding  `json:"finding,omitempty"`
	Transcript string                `json:"transcript,omitempty"`
}

func (o landingOffer) toolName() string {
	switch o.Tool {
	case "obstacle":
		return "obstacle"
	case "no_change":
		return "no_change"
	default:
		return "land"
	}
}

func (o landingOffer) equalOffer(other landingOffer) bool {
	if o.CallID != other.CallID || o.toolName() != other.toolName() || o.Commit != other.Commit || o.Base != other.Base || o.Transcript != other.Transcript {
		return false
	}
	return reflect.DeepEqual(o.Obstacle, other.Obstacle) && reflect.DeepEqual(o.Finding, other.Finding)
}

type landingCall struct {
	landingOffer
	ItemID      string `json:"item_id"`
	RunID       string `json:"run_id"`
	TerminalSeq int64  `json:"terminal_seq"`
	NodeID      string `json:"node_id,omitempty"`
	SessionID   string `json:"session_id"`
	RunLog      string `json:"run_log"`
	Worktree    string `json:"worktree"`
	Scratch     string `json:"scratch"`
	Adapter     string `json:"adapter"`
	Capability  string `json:"ending_capability"`
	Tool        string `json:"tool"`
}

// recordedOffer restores Tool from landingCall.Tool, which shadows the embedded
// landingOffer.Tool during JSON unmarshaling.
func (c landingCall) recordedOffer() landingOffer {
	o := c.landingOffer
	if o.Tool == "" {
		o.Tool = c.Tool
	}
	return o
}

type landingFinding struct {
	CriterionID string `json:"criterion_id"`
	Scope       string `json:"scope"`
}

type landingAnswer struct {
	CallID   string           `json:"call_id"`
	Kind     string           `json:"kind"`
	Stage    string           `json:"stage,omitempty"`
	Text     string           `json:"text"`
	Evidence []string         `json:"evidence,omitempty"`
	Findings []landingFinding `json:"findings,omitempty"`
}

// The event stream is the call record. The existence check and append share the
// store's writer transaction, so replay cannot insert a second offer or answer.
func appendLandingCallEvent(tx *store.Tx, kind, id string, payload any, at time.Time) error {
	seen, err := judgmentSeen(tx, kind, "call", id)
	if err != nil {
		return err
	}
	if seen != 0 {
		if kind == callOffered {
			return nil
		}
		return fmt.Errorf("call %q already has a %s event", id, kind)
	}
	if kind != callOffered {
		offered, err := judgmentSeen(tx, callOffered, "call", id)
		if err != nil {
			return err
		}
		if offered != 1 {
			return fmt.Errorf("call %q has no offer", id)
		}
	}
	switch kind {
	case callAnswered, callLanded, callDelivered:
		closed, err := judgmentSeen(tx, callClosed, "call", id)
		if err != nil {
			return err
		}
		if closed != 0 {
			return fmt.Errorf("call %q is closed", id)
		}
	}
	if kind == callDelivered || kind == callClosed {
		answered, err := judgmentSeen(tx, callAnswered, "call", id)
		if err != nil {
			return err
		}
		if (kind == callDelivered) != (answered == 1) {
			return fmt.Errorf("call %q cannot record %s in its current state", id, kind)
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: "call", ObjectID: &id, Payload: body, At: at})
	return err
}

type landingOfferIntake int

const (
	landingOfferAbsent landingOfferIntake = iota
	landingOfferSubmitted
	landingOfferDuplicate
	landingOfferSettled
)

func (l *Loop) intakeLandingOffer(ctx context.Context, d Decision, rec run.Record, mainSHA string) (landingOfferIntake, error) {
	if len(d.RunRequest.Command) == 0 || d.RunRequest.Backend == "command" || evaluatorNode(d) {
		return landingOfferAbsent, nil
	}
	laneDir := l.laneDirPath(d.LaneID)
	path := filepath.Join(run.DurableDir(laneDir, rec.RunID), landingOfferFile)
	// Stat only checks presence; do not read an offer while a marked child can write it.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			runPath := filepath.Join(RunDir(l.cfg.Root, rec.RunID), landingOfferFile)
			if _, rErr := os.Stat(runPath); rErr == nil {
				path = runPath
			} else if os.IsNotExist(rErr) {
				return landingOfferAbsent, nil
			} else {
				return landingOfferAbsent, rErr
			}
		} else {
			return landingOfferAbsent, err
		}
	}
	procs, err := awaitSurvivingRunProcesses(ctx, l.cfg.Store, rec.RunID)
	if err != nil {
		return landingOfferAbsent, err
	}
	if len(procs) > 0 {
		return landingOfferAbsent, &survivingProcessesError{RunID: rec.RunID, Step: "read landing offer", Processes: procs}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return landingOfferAbsent, err
	}
	var offer landingOffer
	if err := json.Unmarshal(raw, &offer); err != nil {
		return landingOfferAbsent, fmt.Errorf("reading landing offer: %w", err)
	}
	if strings.TrimSpace(offer.CallID) == "" || offer.Base == "" {
		return landingOfferAbsent, Externalf("landing offer for %s is missing its call id or base", rec.RunID)
	}
	switch offer.toolName() {
	case "obstacle":
		if offer.Obstacle == nil || strings.TrimSpace(offer.Obstacle.Kind) == "" {
			return landingOfferAbsent, Externalf("obstacle call for %s is missing its obstacle kind", rec.RunID)
		}
	case "no_change":
		if offer.Commit != "" {
			return landingOfferAbsent, Externalf("no_change call for %s must not name a commit", rec.RunID)
		}
		if offer.Finding == nil || strings.TrimSpace(offer.Finding.Cause) == "" || strings.TrimSpace(offer.Finding.Evidence) == "" || strings.TrimSpace(offer.Finding.WhyNoChange) == "" {
			return landingOfferAbsent, Externalf("no_change call for %s is missing required finding fields", rec.RunID)
		}
	default:
		if offer.Commit == "" {
			return landingOfferAbsent, Externalf("landing offer for %s is missing its commit", rec.RunID)
		}
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return landingOfferAbsent, err
	}
	defer db.Close()
	// The validation reads the lane's HEAD and its session file: I/O, so it runs here,
	// with no write open. Its inputs are all in hand, and the write below uses its answer
	// only where it used to call it.
	validated, validateErr := l.validateLandingOffer(ctx, d, rec, mainSHA, offer)
	if validateErr == nil && validated.Commit != "" {
		if err := l.pinLane(ctx, d.LaneID, d.ItemID, rec.RunID, validated.Commit); err != nil {
			return landingOfferAbsent, err
		}
	}
	var intake landingOfferIntake
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		intake = landingOfferAbsent
		var raw []byte
		err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND object_id=?`, callOffered, offer.CallID).Scan(&raw)
		if err == nil {
			var existing landingCall
			if err := json.Unmarshal(raw, &existing); err != nil {
				return err
			}
			if existing.RunID != rec.RunID {
				intake = landingOfferDuplicate
				return nil
			}
			if !existing.recordedOffer().equalOffer(offer) {
				return Externalf("run %s changed its recorded landing offer %s", rec.RunID, offer.CallID)
			}
			intake = landingOfferSubmitted
			var settled int
			if err := tx.QueryRow(`SELECT count(*) FROM events WHERE (object_type='call' AND object_id=? AND kind IN (?,?,?)) OR (object_type='run' AND object_id=? AND kind=?)`, offer.CallID, callAnswered, callClosed, callLanded, rec.RunID, examinationDoneKind).Scan(&settled); err != nil {
				return err
			}
			if settled > 0 {
				intake = landingOfferSettled
				return nil
			}
			// An unfinished replay still submits the recorded commit. Never judge a
			// new lane head or base under an earlier call's immutable offer.
			return validateErr
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if validateErr != nil {
			return validateErr
		}
		intake = landingOfferSubmitted
		return appendLandingCallEvent(tx, callOffered, offer.CallID, validated, l.cfg.Now().UTC())
	})
	return intake, err
}

func (l *Loop) validateLandingOffer(ctx context.Context, d Decision, rec run.Record, mainSHA string, offer landingOffer) (landingCall, error) {
	laneDir := l.laneDirPath(d.LaneID)
	target := l.laneTarget(d.LaneID)
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return landingCall{}, err
	}
	settledBase := l.settleBase(d.LaneID, mainSHA)
	base := l.laneFile(d.LaneID, BaseSHAFile)
	if base == "" {
		base = settledBase
		if d.Workflow != nil && d.NodeBaseSHA != "" {
			base = d.NodeBaseSHA
		}
	}
	// A writer staged against the whole item can offer a kept candidate even
	// when it has not moved HEAD past its node's own start.
	wholeItemWriter := false
	if d.Workflow != nil && base == settledBase {
		node := d.Workflow.Stages[d.NodeIndex]
		wholeItemWriter = node.Kind == "agent" && node.Access == "write"
	}
	switch offer.toolName() {
	case "obstacle":
		if offer.Base != base {
			return landingCall{}, Externalf("obstacle call for %s does not match its staged base", rec.RunID)
		}
		obs := obstacleFromOffer(offer.Obstacle)
		if obs.Problem != "" {
			return landingCall{}, Externalf("obstacle call for %s is incomplete: %s", rec.RunID, obs.Problem)
		}
	case "no_change":
		if !l.allowsNoChangeDecision(d) {
			return landingCall{}, Externalf("no_change call for %s is not allowed on this workflow node", rec.RunID)
		}
		if offer.Commit != "" || offer.Base != base || head != base {
			return landingCall{}, Externalf("no_change call for %s does not match its staged base at HEAD", rec.RunID)
		}
		if offer.Finding == nil || strings.TrimSpace(offer.Finding.Cause) == "" || strings.TrimSpace(offer.Finding.Evidence) == "" || strings.TrimSpace(offer.Finding.WhyNoChange) == "" {
			return landingCall{}, Externalf("no_change call for %s is missing required finding fields", rec.RunID)
		}
		dirty, err := l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return landingCall{}, err
		}
		if strings.TrimSpace(dirty) != "" {
			return landingCall{}, Externalf("no_change call for %s requires a clean worktree", rec.RunID)
		}
	default:
		if offer.Commit != head || offer.Base != base || head == base || head == settledBase ||
			(d.Workflow != nil && d.NodeBaseSHA != "" && head == d.NodeBaseSHA && !wholeItemWriter) {
			return landingCall{}, Externalf("landing offer for %s does not name a call, its staged base, and new HEAD", rec.RunID)
		}
	}
	artifactDir := laneDir
	if d.RunRequest.ReadOnlyGroup != "" {
		artifactDir = run.NodeDir(laneDir, rec.RunID)
	}
	session, err := ReadSessionID(artifactDir)
	if err != nil {
		return landingCall{}, err
	}
	if session == "" {
		return landingCall{}, Externalf("landing offer for %s has no staged session", rec.RunID)
	}
	adapter := d.RunRequest.Command[0]
	capability, err := stagedEndingCapability(RunDir(l.cfg.Root, rec.RunID))
	if err != nil {
		return landingCall{}, err
	}
	call := landingCall{landingOffer: offer, ItemID: d.RunRequest.ItemID, RunID: rec.RunID, TerminalSeq: rec.EventSeq, SessionID: session, RunLog: filepath.Join(artifactDir, rec.RunID+".log"), Worktree: target, Scratch: run.DurableDir(laneDir, rec.RunID), Adapter: adapter, Capability: capability, Tool: offer.toolName()}
	if d.Workflow != nil {
		call.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	}
	return call, nil
}

func obstacleFromOffer(raw *landingOfferObstacle) Obstacle {
	if raw == nil {
		return Obstacle{Problem: "missing obstacle"}
	}
	fields := make(map[string]string, len(raw.Fields))
	for k, v := range raw.Fields {
		fields[strings.ToUpper(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	o := Obstacle{Kind: strings.ToLower(strings.TrimSpace(raw.Kind)), Fields: fields}
	o.Problem = o.problem()
	return o
}

func formatObstacleReport(o Obstacle) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", ObstacleField, o.Kind)
	for _, name := range obstacleRequired[o.Kind] {
		val := o.Field(name)
		if strings.Contains(val, "\n") {
			fmt.Fprintf(&b, "%s:\n%s\n", name, val)
		} else {
			fmt.Fprintf(&b, "%s: %s\n", name, val)
		}
	}
	b.WriteString(ReportFooter + " blocked\n")
	return b.String()
}

func (l *Loop) offeredObstacle(ctx context.Context, runID string) (Obstacle, bool, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return Obstacle{}, false, err
	}
	defer db.Close()
	var raw []byte
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, runID).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Obstacle{}, false, nil
	}
	if err != nil {
		return Obstacle{}, false, err
	}
	var call landingCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return Obstacle{}, false, err
	}
	if call.Tool != "obstacle" || call.Obstacle == nil {
		return Obstacle{}, false, nil
	}
	return obstacleFromOffer(call.Obstacle), true, nil
}

func (l *Loop) offeredNoChange(ctx context.Context, runID string) (landingOfferFinding, bool, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return landingOfferFinding{}, false, err
	}
	defer db.Close()
	var raw []byte
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, runID).Scan(&raw)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return landingOfferFinding{}, false, nil
	}
	if err != nil {
		return landingOfferFinding{}, false, err
	}
	var call landingCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return landingOfferFinding{}, false, err
	}
	if call.Tool != "no_change" || call.Finding == nil {
		return landingOfferFinding{}, false, nil
	}
	return landingOfferFinding{
		Cause:       strings.TrimSpace(call.Finding.Cause),
		Evidence:    strings.TrimSpace(call.Finding.Evidence),
		WhyNoChange: strings.TrimSpace(call.Finding.WhyNoChange),
	}, true, nil
}

const (
	endingCapabilitiesKind = "drain.ending-capabilities"
	piFeaturesKind         = "drain.pi-features"
)

var (
	piFeaturesProbeTimeout = 10 * time.Second
	piFeaturesWaitDelay    = time.Second
)

// Probe the executable, not its version or an adapter declaration: a rejected or
// malformed feature response means the original footer protocol remains in force.
func probePiEndingCapability(ctx context.Context) (string, string, []string) {
	path, err := exec.LookPath("pi")
	if err != nil {
		return run.EndingFooter, "", nil
	}
	probe, cancel := context.WithTimeout(ctx, piFeaturesProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probe, path, "--features")
	cmd.WaitDelay = piFeaturesWaitDelay
	out, err := cmd.Output()
	var features []string
	if err == nil && json.Unmarshal(out, &features) == nil {
		for _, feature := range features {
			if feature == "parked-tool-calls" {
				return run.EndingCallSuspends, path, features
			}
		}
		return run.EndingFooter, path, features
	}
	return run.EndingFooter, path, nil
}

const claudeDeferredMarker = "No deferred tool marker found in the resumed session"

// Probe the executable, as pi's probe does: a binary that predates deferred-tool resume, an
// absent one, or one with the feature switched off in the environment keeps the footer.
// `--continue` in an empty directory has no session to continue, so the probe starts no
// model turn; a binary that knows deferred-tool resume names its marker on standard error.
func probeClaudeEndingCapability(ctx context.Context) (string, string) {
	path, err := exec.LookPath("claude")
	if err != nil || os.Getenv("CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME") != "" {
		return run.EndingFooter, ""
	}
	dir, err := os.MkdirTemp("", "tillr-claude-probe-")
	if err != nil {
		return run.EndingFooter, path
	}
	defer os.RemoveAll(dir)
	probe, cancel := context.WithTimeout(ctx, piFeaturesProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(probe, path, "-p", "--setting-sources", "", "--continue")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "YOLO_NO_LAUNCH_FLAGS=1")
	cmd.Stdin = strings.NewReader("")
	cmd.WaitDelay = piFeaturesWaitDelay
	var stderr strings.Builder
	cmd.Stderr = &stderr
	_ = cmd.Run()
	if probe.Err() == nil && strings.Contains(stderr.String(), claudeDeferredMarker) {
		return run.EndingCallSuspends, path
	}
	return run.EndingFooter, path
}

func (l *Loop) recordEndingCapabilities(ctx context.Context) error {
	capabilities := make(map[string]string)
	var (
		probed       bool
		piCapability string
		piPath       string
		piFeatures   []string

		claudeProbed     bool
		claudeCapability string
	)
	if l.cfg.Policy == nil {
		l.endingCapabilities = capabilities
		return nil
	}
	for _, backend := range l.cfg.Policy.Backends {
		adapter := resolveAdapterPath(l.cfg.Root, backend.Adapter)
		if _, ok := capabilities[adapter]; ok {
			continue
		}
		path := adapter
		if !filepath.IsAbs(path) {
			path = filepath.Join(l.cfg.Root, path)
		}
		capability := run.EndingFooter
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), "ending-capability")); err == nil {
			capability, err = run.ReadEndingCapability("", path)
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Base(filepath.Dir(path)) == "pi" {
			if !probed {
				piCapability, piPath, piFeatures = probePiEndingCapability(ctx)
				probed = true
			}
			capability = piCapability
		}
		if filepath.Base(filepath.Dir(path)) == "claude" && capability == run.EndingCallSuspends {
			if !claudeProbed {
				claudeCapability, _ = probeClaudeEndingCapability(ctx)
				claudeProbed = true
			}
			capability = claudeCapability
		}
		capabilities[adapter] = capability
	}
	body, err := json.Marshal(capabilities)
	if err != nil {
		return err
	}
	var featuresBody []byte
	if probed {
		featuresBody, err = json.Marshal(struct {
			Path       string   `json:"path"`
			Capability string   `json:"capability"`
			Features   []string `json:"features,omitempty"`
		}{
			Path:       piPath,
			Capability: piCapability,
			Features:   piFeatures,
		})
		if err != nil {
			return err
		}
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		at := l.cfg.Now().UTC()
		if _, err := tx.Append(event.Event{Kind: endingCapabilitiesKind, ObjectType: "drain", Payload: body, At: at}); err != nil {
			return err
		}
		if probed {
			if _, err := tx.Append(event.Event{Kind: piFeaturesKind, ObjectType: "drain", Payload: featuresBody, At: at}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	l.endingCapabilities = capabilities
	return nil
}

// refusalEvidence is what an answer to a refused landing call quotes from files: the gate
// plan's command for each failing check, and each check's log. It depends only on the run
// and the refusal's checks, so it is read before the write that records the answer, with
// no store write open (tools/tillr/store/ioguard.go).
type refusalEvidence struct {
	commands map[string][]string
	logs     map[string]refusalLog
}

type refusalLog struct {
	body []byte
	err  error
}

// readRefusalEvidence reads the files answerLandingRefusal quotes. A refusal with no checks
// reads nothing.
func (l *Loop) readRefusalEvidence(runID string, refusal refusalPayload) refusalEvidence {
	ev := refusalEvidence{commands: map[string][]string{}, logs: map[string]refusalLog{}}
	if len(refusal.Checks) == 0 {
		return ev
	}
	planPath := filepath.Join(RunDir(l.cfg.Root, runID), GatePlanRecord)
	if raw, ok := readRecordPayload(planPath); ok {
		var plan struct {
			Checks []struct {
				CheckID string   `json:"check_id"`
				Command []string `json:"command"`
			} `json:"checks"`
		}
		if json.Unmarshal(raw, &plan) == nil {
			for _, check := range plan.Checks {
				ev.commands[check.CheckID] = check.Command
			}
		}
	}
	for _, check := range refusal.Checks {
		if check.LogRef == "" {
			continue
		}
		if _, seen := ev.logs[check.LogRef]; !seen {
			body, err := os.ReadFile(check.LogRef)
			ev.logs[check.LogRef] = refusalLog{body: body, err: err}
		}
	}
	return ev
}

// A judge's negative verdict returns to a parked landing call. Footer workers
// keep the ordinary judge hold; they have no call to reopen.
func (l *Loop) captureJudgeLandingSession(ctx context.Context, c Candidate) (*WorkerSession, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var suspends bool
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.ending_capability')=?)`, callOffered, c.RunID, run.EndingCallSuspends).Scan(&suspends)
	})
	if err != nil || !suspends {
		return nil, err
	}
	return l.captureWorkerSession(ctx, c)
}

// answerLandingRefusal answers the run's open landing call with the refusal, quoting ev,
// which the caller read before its write (readRefusalEvidence). It reads no file.
func (l *Loop) answerLandingRefusal(tx *store.Tx, candidate Candidate, refusal refusalPayload, ev refusalEvidence, at time.Time) error {
	outcome := ""
	switch refusal.Step {
	case RefusalFence:
		outcome = "fence-refused"
	case RefusalDeliveryOrder:
		outcome = RefusalDeliveryOrder
	case RefusalGate:
		outcome = "gate-refused"
	case RefusalConflict:
		outcome = "conflict"
	case RefusalReverted:
		outcome = "reverted"
	case RefusalVerifyNotDone:
		if refusal.WorkerSession != nil {
			outcome = "judge-refused"
		}
	case ReasonEvalFailed:
		outcome = "judge-refused"
	case IdenticalRepeatPrefix:
		outcome = "identical-repeat"
	}
	writerID := judgeWriterRun(candidate)
	choice := chooseLandingCallAnswer(outcome)
	if choice.Kind == "" {
		if refusal.Step == RefusalNoOp || refusal.Step == RefusalPlanWrong {
			var tool string
			if err := tx.QueryRow(`SELECT COALESCE(json_extract(payload,'$.tool'),'') FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, writerID).Scan(&tool); err == nil && tool == "obstacle" {
				choice = landingCallChoice{Kind: "obstacle-answer"}
			}
		}
		if choice.Kind == "" {
			return nil
		}
	}
	var raw []byte
	err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?`, callOffered, writerID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var call landingCall
	if err := json.Unmarshal(raw, &call); err != nil {
		return err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "ANSWER: %s\n", choice.Kind)
	if choice.Stage != "" {
		fmt.Fprintf(&text, "Stage: %s\n", choice.Stage)
	}
	if refusal.Step == RefusalGate {
		// What failed comes first, in the gate's own words; the logs below are the supplement.
		text.WriteString(failureSummary(refusal.Checks))
	}
	if refusal.Detail != "" {
		fmt.Fprintln(&text, refusal.Detail)
	}
	head := candidate.HeadSHA
	if head == "" {
		head = call.Commit
	}
	base := candidate.BaseSHA
	if base == "" {
		base = call.Base
	}
	if call.Tool == "no_change" {
		fmt.Fprintf(&text, "Offer tool: no_change\nBranch head: %s\nLast base: %s\nOffer run: %s\nOffer scratch: %s\n", head, base, call.RunID, call.Scratch)
	} else {
		fmt.Fprintf(&text, "Offer commit: %s\nBranch head: %s\nLast base: %s\nOffer run: %s\nOffer scratch: %s\n", call.Commit, head, base, call.RunID, call.Scratch)
	}
	commands := ev.commands
	answer := landingAnswer{CallID: call.CallID, Kind: choice.Kind, Stage: choice.Stage}
	if c := candidate.Judge; c != nil && choice.Stage == "judge" {
		var output item.JudgeOutput
		if json.Unmarshal(c.Raw, &output) == nil {
			for _, verdict := range output.Verdicts {
				if verdict.Verdict != item.VerdictNotDone {
					continue
				}
				answer.Findings = append(answer.Findings, landingFinding{CriterionID: verdict.CriterionID, Scope: "content"})
				fmt.Fprintf(&text, "Criterion %s (content): %s\n", verdict.CriterionID, strings.TrimSpace(verdict.Reason))
				answer.Evidence = append(answer.Evidence, verdict.Evidence...)
			}
		}
	}
	for _, check := range refusal.Checks {
		fmt.Fprintf(&text, "Check: %s\n", check.CheckID)
		if argv := commands[check.CheckID]; len(argv) > 0 {
			fmt.Fprintf(&text, "Command: %q\n", argv)
		}
		if check.LogRef == "" {
			continue
		}
		answer.Evidence = append(answer.Evidence, check.LogRef)
		read := ev.logs[check.LogRef]
		log, err := read.body, read.err
		if read.body == nil && read.err == nil {
			err = os.ErrNotExist // not read before the write: quoted by path only
		}
		switch {
		case err != nil:
			fmt.Fprintf(&text, "Full log: %s\n", check.LogRef)
		case utf8.RuneCount(log) <= 30000:
			fmt.Fprintln(&text, string(log))
		default:
			chars := []rune(string(log))
			fmt.Fprintf(&text, "%s\n[Full log: %s]\n%s\n", string(chars[:5000]), check.LogRef, string(chars[len(chars)-25000:]))
		}
	}
	answer.Text = text.String()
	closed, err := judgmentSeen(tx, callClosed, "call", call.CallID)
	if err != nil || closed != 0 {
		return err
	}
	return appendLandingCallEvent(tx, callAnswered, call.CallID, answer, at)
}

// writeLandingArtifact publishes a complete record before Pi can open the session.
func writeLandingArtifact(dir, name string, value any) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".landing-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(value)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, name))
}

type landingCallDelivery struct {
	Form  string `json:"form"`
	RunID string `json:"run_id"`
}

func (l *Loop) returnedLandingAnswer(ctx context.Context, d Decision) (landingCall, landingAnswer, error) {
	var call landingCall
	var answer landingAnswer
	if d.Returned == nil || d.Returned.RunID == "" {
		return call, answer, nil
	}
	callRunID := d.Returned.RunID
	if d.Returned.WorkerSession != nil && d.Returned.WorkerSession.RunID != "" {
		callRunID = d.Returned.WorkerSession.RunID
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return call, answer, err
	}
	defer db.Close()
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var offerRaw, answerRaw []byte
		err := r.QueryRow(`SELECT o.payload,a.payload FROM events o JOIN events a
   ON a.object_type=o.object_type AND a.object_id=o.object_id AND a.kind=?
   WHERE o.object_type='call' AND o.kind=? AND json_extract(o.payload,'$.run_id')=?`, callAnswered, callOffered, callRunID).Scan(&offerRaw, &answerRaw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(offerRaw, &call); err != nil {
			return err
		}
		if err := json.Unmarshal(answerRaw, &answer); err != nil {
			return err
		}
		if answer.CallID != call.CallID || call.ItemID != d.ItemID || strings.TrimSpace(answer.Text) == "" {
			return Externalf("invalid stored landing answer for %s", callRunID)
		}
		return nil
	})
	return call, answer, err
}

func landingAnswerForTurn(answer landingAnswer, runID, dir string) landingAnswer {
	answer.Text += fmt.Sprintf("\nTurn: %s\nScratch: %s\n", runID, dir)
	return answer
}

type landingRefusal struct {
	Tool    string               `json:"tool,omitempty"`
	Commit  string               `json:"commit,omitempty"`
	Base    string               `json:"base,omitempty"`
	Finding *landingOfferFinding `json:"finding,omitempty"`
	Answer  string               `json:"answer"`
}

// Keep historical report findings in the event log, but never use them to
// disqualify an offer in a new turn.
func (a landingAnswer) legacyReportOnly() bool {
	if a.Kind != "refused" || a.Stage != "judge" || len(a.Findings) == 0 {
		return false
	}
	for _, finding := range a.Findings {
		if finding.Scope != "report" {
			return false
		}
	}
	return true
}

func (l *Loop) landingState(ctx context.Context, itemID, session string) (map[string]any, error) {
	state := map[string]any{"open": []landingOffer{}, "refused": []landingRefusal{}}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT o.payload, a.payload,
    EXISTS(SELECT 1 FROM events e WHERE e.object_type='call' AND e.object_id=o.object_id AND e.kind IN (?,?))
    FROM events o LEFT JOIN events a ON a.object_type=o.object_type AND a.object_id=o.object_id AND a.kind=?
    WHERE o.object_type='call' AND o.kind=? AND json_extract(o.payload,'$.item_id')=? ORDER BY o.seq`,
			callDelivered, callClosed, callAnswered, callOffered, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		open := []landingOffer{}
		refused := []landingRefusal{}
		for rows.Next() {
			var raw, answerRaw []byte
			var finished bool
			if err := rows.Scan(&raw, &answerRaw, &finished); err != nil {
				return err
			}
			var call landingCall
			if err := json.Unmarshal(raw, &call); err != nil {
				return err
			}
			if session != "" && call.SessionID == session && !finished {
				open = append(open, call.recordedOffer())
			}
			if len(answerRaw) > 0 {
				var answer landingAnswer
				if err := json.Unmarshal(answerRaw, &answer); err != nil {
					return err
				}
				if (answer.Kind == "refused" || answer.Kind == "reverted") && !answer.legacyReportOnly() {
					if call.Tool == "no_change" {
						refused = append(refused, landingRefusal{Tool: "no_change", Base: call.Base, Finding: call.Finding, Answer: answer.Text})
					} else {
						refused = append(refused, landingRefusal{Commit: call.Commit, Answer: answer.Text})
					}
				}
			}
		}
		state["open"], state["refused"] = open, refused
		return rows.Err()
	})
	return state, err
}

func (l *Loop) stageLandingAnswer(ctx context.Context, d Decision, spec *StageSpec) error {
	state, err := l.landingState(ctx, d.ItemID, spec.SessionID)
	if err != nil {
		return err
	}
	if retry := d.RetryCandidate; retry != nil && retry.Available {
		// An authorized retry explicitly reopens this retained candidate.
		refused := state["refused"].([]landingRefusal)
		kept := refused[:0]
		for _, entry := range refused {
			if entry.Commit != retry.HeadSHA {
				kept = append(kept, entry)
			}
		}
		state["refused"] = kept
	}
	dir := run.DurableDir(l.laneDirPath(d.LaneID), d.RunRequest.RunID)
	if err := writeLandingArtifact(dir, "landing-state.json", state); err != nil {
		return err
	}
	call, answer, err := l.returnedLandingAnswer(ctx, d)
	if err != nil || answer.CallID == "" {
		return err
	}
	answer = landingAnswerForTurn(answer, d.RunRequest.RunID, dir)
	if call.Capability != run.EndingCallSuspends {
		return nil
	}
	if !d.RunRequest.SessionTurn {
		returned := *spec.Returned
		returned.Detail = answer.Text
		spec.Returned = &returned
		return nil
	}
	if call.ItemID != d.ItemID || spec.SessionID != call.SessionID || call.Worktree != l.laneTarget(d.LaneID) {
		return Externalf("run %s does not reopen the session for landing call %s", d.RunRequest.RunID, call.CallID)
	}
	if call.Transcript != "" {
		if err := os.WriteFile(filepath.Join(dir, "landing-transcript"), []byte(call.Transcript+"\n"), 0600); err != nil {
			return err
		}
	}
	return writeLandingArtifact(dir, landingAnswerFile, answer)
}

// Reconcile from the adapter's delivery marker or Pi's persisted transcript.
// Settlement can be retried after the result is saved but before this transaction.
func (l *Loop) reconcileLandingDelivery(ctx context.Context, d Decision) error {
	if !d.RunRequest.SessionTurn || d.Returned == nil || d.Returned.WorkerSession == nil {
		return nil
	}
	dir := run.DurableDir(l.laneDirPath(d.LaneID), d.RunRequest.RunID)
	call, answer, err := l.returnedLandingAnswer(ctx, d)
	if err != nil || answer.CallID == "" || call.Capability != run.EndingCallSuspends {
		return err
	}
	session := d.Returned.WorkerSession.SessionID
	if call.SessionID != session || call.Worktree != l.laneTarget(d.LaneID) || call.ItemID != d.ItemID {
		return Externalf("landing delivery identity changed for %s", d.RunRequest.RunID)
	}
	marker, err := os.ReadFile(filepath.Join(dir, "landing-delivery"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	form := "tool-result"
	if strings.TrimSpace(string(marker)) == "message" {
		form = "message"
	}
	var path []byte
	if form == "tool-result" {
		path, err = os.ReadFile(filepath.Join(dir, "landing-transcript"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	procs, err := awaitSurvivingRunProcesses(ctx, l.cfg.Store, d.RunRequest.RunID)
	if err != nil {
		return err
	}
	if len(procs) > 0 {
		return &survivingProcessesError{RunID: d.RunRequest.RunID, Step: "reconcile landing answer", Processes: procs}
	}
	if form == "tool-result" {
		answer = landingAnswerForTurn(answer, d.RunRequest.RunID, dir)
		filename := strings.TrimSuffix(string(path), "\n")
		info, err := os.Stat(filename)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return Externalf("Pi transcript for %s is not a regular file", d.RunRequest.RunID)
		}
		transcript, err := os.ReadFile(filename)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !landingResultPersisted(transcript, session, answer) {
			return nil
		}
	}
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.WriteTx(ctx, func(tx *store.Tx) error {
		seen, err := judgmentSeen(tx, callDelivered, "call", answer.CallID)
		if err != nil || seen != 0 {
			return err
		}
		return appendLandingCallEvent(tx, callDelivered, answer.CallID, landingCallDelivery{Form: form, RunID: d.RunRequest.RunID}, l.cfg.Now().UTC())
	})
}

// Claude Code's session file is one JSON row per line with no session header: a user row
// carries the tool_result block the harness saved when it replayed the deferred call.
// Its content is a string or text blocks, and the harness trims a trailing newline from an
// error result, so the comparison trims one from both sides.
func claudeLandingResultPersisted(transcript []byte, session string, answer landingAnswer) bool {
	type block struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		IsError   bool            `json:"is_error"`
		Content   json.RawMessage `json:"content"`
	}
	type row struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	want := strings.TrimSuffix(answer.Text, "\n")
	for _, line := range strings.Split(string(transcript), "\n") {
		var r row
		if json.Unmarshal([]byte(line), &r) != nil || r.Type != "user" || (r.SessionID != "" && r.SessionID != session) {
			continue
		}
		var blocks []block
		if json.Unmarshal(r.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type != "tool_result" || b.ToolUseID != answer.CallID || !b.IsError {
				continue
			}
			var text string
			if json.Unmarshal(b.Content, &text) != nil {
				var parts []struct{ Type, Text string }
				if json.Unmarshal(b.Content, &parts) != nil || len(parts) != 1 || parts[0].Type != "text" {
					continue
				}
				text = parts[0].Text
			}
			if strings.TrimSuffix(text, "\n") == want {
				return true
			}
		}
	}
	return false
}

func landingResultPersisted(transcript []byte, session string, answer landingAnswer) bool {
	first, _, _ := strings.Cut(string(transcript), "\n")
	var header struct{ Type string }
	if json.Unmarshal([]byte(first), &header) != nil || header.Type != "session" {
		return claudeLandingResultPersisted(transcript, session, answer)
	}
	type entry struct {
		Type, ID, ParentID string
		Message            struct {
			Role, ToolName, ToolCallID string
			IsError                    bool
			Content                    []struct{ Type, Text string }
			Details                    landingAnswer
		}
	}
	entries := map[string]entry{}
	var leaf string
	for i, line := range strings.Split(string(transcript), "\n") {
		var e entry
		if json.Unmarshal([]byte(line), &e) != nil {
			if i == 0 {
				return false
			}
			continue
		}
		if i == 0 && (e.Type != "session" || e.ID != session) {
			return false
		}
		if i > 0 && e.ID != "" {
			entries[e.ID] = e
			leaf = e.ID
		}
	}
	for remaining := len(entries); leaf != "" && remaining > 0; remaining-- {
		e := entries[leaf]
		m := e.Message
		if e.Type == "message" && m.Role == "toolResult" && (m.ToolName == "land" || m.ToolName == "obstacle" || m.ToolName == "no_change") && m.ToolCallID == answer.CallID {
			return m.IsError && len(m.Content) == 1 && m.Content[0].Type == "text" && m.Content[0].Text == answer.Text && reflect.DeepEqual(m.Details, answer)
		}
		leaf = e.ParentID
	}
	return false
}

// Observe the same comparison as markExamined before persisting an irreversible
// refusal answer. The read is out of the writer transaction; only the drain
// owns the item's examination, and an existing native refusal is idempotent.
func (l *Loop) landingRefusalRepeats(ctx context.Context, c Candidate, refusal refusalPayload) (bool, error) {
	if refusal.Evidence == "" {
		return false, nil
	}
	switch refusal.Step {
	case RefusalGate, RefusalFence:
	case ReasonEvalFailed:
		if refusal.WorkerSession == nil {
			return false, nil
		}
	default:
		return false, nil
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var repeated bool
	writerID := judgeWriterRun(c)
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var offered bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=?)`, callOffered, writerID).Scan(&offered); err != nil || !offered {
			return err
		}
		history, err := readRunHistory(r)
		if err != nil {
			return err
		}
		chain, decisions, err := readItemChain(r, history, c.ItemID)
		if err != nil {
			return err
		}
		d := decisions[c.RunID]
		if d.BlockConflict != nil {
			return nil
		}
		_, repeated = repeatedEnding(chain, chainEnding{RunID: c.RunID, Producer: producerOf(d), Cause: refusal.Step, Key: refusal.Evidence})
		return nil
	})
	return repeated, err
}
