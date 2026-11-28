package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// WorkerSession records the lane and harness handle a refused worker left. Each
// returned turn has a new run id, so its ending remains a separate chain record.
type WorkerSession struct {
	RunID       string    `json:"run_id,omitempty"`
	SessionID   string    `json:"session_id"`
	LaneDir     string    `json:"lane_dir"`
	LaneID      string    `json:"lane_id"`
	BaseSHA     string    `json:"base_sha"`
	HeadSHA     string    `json:"head_sha,omitempty"`
	Backend     string    `json:"backend"`
	Route       string    `json:"route,omitempty"`
	Workflow    *Workflow `json:"workflow,omitempty"`
	NodeIndex   int       `json:"node_index,omitempty"`
	NodeBaseSHA string    `json:"node_base_sha,omitempty"`
	Attempt     int       `json:"attempt,omitempty"`
}

// verifyBuildReturn reports whether an ending sends the item to its build step on the
// candidate a verifier read: a verify-not-done whose findings did not say the plan was wrong.
func verifyBuildReturn(e *ReturnedEnding) bool {
	return e != nil && e.Cause == RefusalVerifyNotDone && !e.Replan && e.WorkerSession != nil && e.WorkerSession.Workflow != nil
}

func verifyNoVerdictReturn(e *ReturnedEnding) bool {
	return e != nil && e.Cause == RefusalVerifyNoVerdict && e.WorkerSession != nil && e.WorkerSession.Workflow != nil
}

// gateBuildReturn reports whether a gate refusal carries a candidate commit that a fresh
// build run can stage on when the worker's session cannot be resumed.
func gateBuildReturn(e *ReturnedEnding) bool {
	return e != nil && e.Cause == RefusalGate && e.WorkerSession != nil && e.WorkerSession.BaseSHA != "" && e.WorkerSession.HeadSHA != ""
}

func workerSessionReason(s *WorkerSession, req *run.Request) string {
	if s == nil || s.SessionID == "" {
		return "session-missing"
	}
	id, err := ReadSessionID(s.LaneDir)
	if err != nil || id != s.SessionID {
		return "session-missing"
	}
	if s.Backend != req.Backend || s.Route != req.Route() {
		return "seat-changed"
	}
	if len(req.Command) > 0 {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(req.Command[0]), "pause-kind"))
		if err == nil {
			switch strings.TrimSpace(string(body)) {
			case run.PauseAtGap, run.PauseWhereItStands:
				return "resumed-worker"
			}
		}
		return "cannot-resume"
	}
	return "cannot-resume"
}

// captureWorkerSession snapshots a worker while its lane still belongs to the run.
func (l *Loop) captureWorkerSession(ctx context.Context, c Candidate) (*WorkerSession, error) {
	return l.captureSession(ctx, c, false)
}

// capturePlanSession snapshots a planner or classifier session while its lane still belongs to the run.
func (l *Loop) capturePlanSession(ctx context.Context, c Candidate) (*WorkerSession, error) {
	return l.captureSession(ctx, c, true)
}

func (l *Loop) startedDecision(ctx context.Context, runID string) (Decision, bool, error) {
	if runID == "" {
		return Decision{}, false, nil
	}
	var decision Decision
	found := false
	err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, runID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var w pendingWork
		if err := json.Unmarshal(raw, &w); err != nil {
			return err
		}
		decision, found = w.Decision, true
		return nil
	})
	return decision, found, err
}

// buildStageForGateReturn picks the build stage to run on a refused candidate when a gate
// refusal cannot resume its worker session: an unscoped build stage if the graph already
// has one (including planned-build's work stage after plan took do-it-now), a new unscoped
// work stage before the evaluator when pieces were expanded, or the writing stage on a
// workflow without an evaluator.
func buildStageForGateReturn(w *Workflow, currentIndex int) (*Workflow, int) {
	if w == nil || len(w.Stages) == 0 {
		return nil, 0
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return nil, 0
	}
	var cp Workflow
	if err := json.Unmarshal(raw, &cp); err != nil {
		return nil, 0
	}
	isUnscopedBuild := func(s WorkflowStage) bool {
		return s.Kind == "agent" && s.Access == "write" && (s.Seat == "build" || s.Seat == "") && s.WriteScope.Paths == nil
	}
	if currentIndex >= 0 && currentIndex < len(cp.Stages) && isUnscopedBuild(cp.Stages[currentIndex]) {
		return &cp, currentIndex
	}
	for i, s := range cp.Stages {
		if isUnscopedBuild(s) {
			return &cp, i
		}
	}
	if slot := evaluatorSlot(&cp); slot != "" {
		for i, s := range cp.Stages {
			if s.ID == slot {
				insertRepairWorkStage(&cp, i, "the gate's refusal")
				return &cp, i
			}
		}
	}
	if currentIndex >= 0 && currentIndex < len(cp.Stages) && cp.Stages[currentIndex].Access == "write" {
		return &cp, currentIndex
	}
	for i := len(cp.Stages) - 1; i >= 0; i-- {
		if cp.Stages[i].Access == "write" {
			return &cp, i
		}
	}
	return nil, 0
}

// gateBuildSession captures the worker session for a gate refusal, and when no resumable
// session exists (for example after a replay released the lane, a plan do-it-now run, or a
// conflict-repair run whose dispatch carries no workflow graph), still records the refused
// candidate commit and the build step that will repair it.
func (l *Loop) gateBuildSession(ctx context.Context, c Candidate) (*WorkerSession, error) {
	s, err := l.captureWorkerSession(ctx, c)
	if err != nil {
		return nil, err
	}
	writerID := c.RunID
	if c.Evaluator != nil || len(c.Writers) > 0 {
		writerID = judgeWriterRun(c)
	}
	if s != nil {
		if s.Workflow == nil && isConflictFixRun(writerID) {
			orig, found, err := l.startedDecision(ctx, conflictFixOriginal(writerID))
			if err != nil {
				return nil, err
			}
			if found && orig.Workflow != nil {
				if gw, gnode := buildStageForGateReturn(orig.Workflow, orig.NodeIndex); gw != nil {
					s.Workflow, s.NodeIndex = gw, gnode
					if orig.Attempt > 0 {
						s.Attempt = orig.Attempt
					}
				}
			}
		}
		return s, nil
	}
	head := c.WorkerHeadSHA
	if head == "" {
		head = c.HeadSHA
	}
	if head == "" && !c.laneReleasedForReplay() {
		head = l.headOf(ctx, c.LaneID)
	}
	if head == "" {
		return nil, nil
	}
	base := c.BaseSHA
	if base == "" && !c.laneReleasedForReplay() {
		base = l.laneBase(c.LaneID)
	}
	if base == "" {
		dir := RunDir(l.cfg.Root, c.RunID)
		for _, name := range []string{ItemBaseSHAFile, BaseSHAFile} {
			if raw, readErr := os.ReadFile(filepath.Join(dir, name)); readErr == nil && strings.TrimSpace(string(raw)) != "" {
				base = strings.TrimSpace(string(raw))
				break
			}
		}
	}
	if base == "" {
		return nil, nil
	}
	decision, found, err := l.startedDecision(ctx, writerID)
	if err != nil {
		return nil, err
	}
	if found && (decision.BlockConflict != nil || decision.DeskDocument != "") {
		return nil, nil
	}
	wf := decision.Workflow
	nodeIndex := decision.NodeIndex
	attempt := decision.Attempt
	if wf == nil && isConflictFixRun(writerID) {
		orig, origFound, err := l.startedDecision(ctx, conflictFixOriginal(writerID))
		if err != nil {
			return nil, err
		}
		if origFound {
			if orig.BlockConflict != nil || orig.DeskDocument != "" {
				return nil, nil
			}
			wf = orig.Workflow
			nodeIndex = orig.NodeIndex
			if orig.Attempt > 0 {
				attempt = orig.Attempt
			}
		}
	}
	session := &WorkerSession{
		RunID:       c.RunID,
		LaneDir:     l.laneDirPath(c.LaneID),
		LaneID:      c.LaneID,
		BaseSHA:     base,
		HeadSHA:     head,
		NodeBaseSHA: head,
		Attempt:     attempt,
	}
	if wf != nil {
		if gw, gnode := buildStageForGateReturn(wf, nodeIndex); gw != nil {
			session.Workflow, session.NodeIndex = gw, gnode
		}
	}
	return session, nil
}

func (l *Loop) captureSession(ctx context.Context, c Candidate, allowClassification bool) (*WorkerSession, error) {
	if c.laneReleasedForReplay() {
		return nil, nil
	}
	writerID := c.RunID
	if c.Evaluator != nil || len(c.Writers) > 0 {
		writerID = judgeWriterRun(c)
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var req *run.Request
	var decision Decision
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, writerID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var w pendingWork
		if err := json.Unmarshal(raw, &w); err != nil {
			return err
		}
		var writingNode, planNode bool
		if w.Decision.Workflow != nil && w.Decision.NodeIndex >= 0 && w.Decision.NodeIndex < len(w.Decision.Workflow.Stages) {
			stage := w.Decision.Workflow.Stages[w.Decision.NodeIndex]
			writingNode = stage.Access == "write"
			planNode = allowClassification && classificationNode(stage)
		}
		eligible := w.Decision.Seat == "" || w.Decision.Seat == "build" || ((writingNode || planNode) && w.Decision.BlockConflict == nil && w.Decision.DeskDocument == "")
		if w.Decision.RunRequest != nil && w.Decision.RunRequest.RunID == writerID && eligible {
			req = w.Decision.RunRequest
			decision = w.Decision
		}
		return nil
	})
	if err != nil || req == nil {
		return nil, err
	}
	var session string
	if len(c.Writers) > 0 {
		session = strings.TrimSpace(c.Writers[len(c.Writers)-1].SessionID)
	}
	if session == "" && writerID != "" {
		if id, err := ReadSessionID(RunDir(l.cfg.Root, writerID)); err == nil {
			session = id
		}
	}
	if session == "" {
		session, err = ReadSessionID(l.laneDirPath(c.LaneID))
		if err != nil {
			return nil, err
		}
	}
	base := l.laneBase(c.LaneID)
	if session == "" || base == "" {
		return nil, nil
	}
	if c.Evaluator != nil || len(c.Writers) > 0 {
		if err := os.WriteFile(filepath.Join(l.laneDirPath(c.LaneID), SessionIDFile), []byte(session+"\n"), 0644); err != nil {
			return nil, err
		}
		capability, err := stagedEndingCapability(RunDir(l.cfg.Root, writerID))
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(l.laneDirPath(c.LaneID), endingCapabilityFile), []byte(capability+"\n"), 0644); err != nil {
			return nil, err
		}
	}
	head := c.WorkerHeadSHA
	if head == "" {
		head = c.HeadSHA
	}
	if head == "" {
		head = l.headOf(ctx, c.LaneID)
	}
	nodeBase := decision.NodeBaseSHA
	if !l.nonLanding && nodeBase != "" {
		target := c.Worktree
		if target == "" {
			target = l.laneTarget(c.LaneID)
		}
		nodeBase = l.nodeBaseOnCandidate(ctx, target, base, head, nodeBase)
	}
	return &WorkerSession{RunID: req.RunID, SessionID: session, LaneDir: l.laneDirPath(c.LaneID), LaneID: c.LaneID, BaseSHA: base, HeadSHA: head, Backend: req.Backend, Route: req.Route(), Workflow: decision.Workflow, NodeIndex: decision.NodeIndex, NodeBaseSHA: nodeBase, Attempt: decision.Attempt}, nil
}

// captureLandedWorkerSession uses the run's immutable staging copies. A held
// candidate can be moved to another lane before landing; that lane's current
// session is not evidence of the worker that produced it.
func (l *Loop) captureLandedWorkerSession(ctx context.Context, c Candidate) (*WorkerSession, error) {
	dir := RunDir(l.cfg.Root, c.RunID)
	id, err := ReadSessionID(dir)
	if err != nil || id == "" {
		return nil, err
	}
	rawBase, err := os.ReadFile(filepath.Join(dir, ItemBaseSHAFile))
	if os.IsNotExist(err) {
		rawBase, err = os.ReadFile(filepath.Join(dir, BaseSHAFile))
	}
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(string(rawBase))
	if base == "" {
		return nil, nil
	}
	var work pendingWork
	err = l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, c.RunID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		return json.Unmarshal(raw, &work)
	})
	if err != nil || work.Decision.RunRequest == nil {
		return nil, err
	}
	req := work.Decision.RunRequest
	if req.RunID != c.RunID {
		return nil, nil
	}
	head := c.WorkerHeadSHA
	if head == "" {
		head = c.HeadSHA
	}
	nodeBase := work.Decision.NodeBaseSHA
	if !l.nonLanding && nodeBase != "" && c.Worktree != "" {
		itemBase := c.BaseSHA
		if itemBase == "" {
			itemBase = base
		}
		nodeBase = l.nodeBaseOnCandidate(ctx, c.Worktree, itemBase, head, nodeBase)
	}
	return &WorkerSession{RunID: req.RunID, SessionID: id, LaneDir: l.laneDirPath(req.LaneID), LaneID: req.LaneID, BaseSHA: base, HeadSHA: head, Backend: req.Backend, Route: req.Route(), Workflow: work.Decision.Workflow, NodeIndex: work.Decision.NodeIndex, NodeBaseSHA: nodeBase, Attempt: work.Decision.Attempt}, nil
}

// landedWorkerSession reads the snapshot made before the landing released its lane.
// Legacy gated candidates have no snapshot and must start a fresh session.
func (l *Loop) landedWorkerSession(ctx context.Context, runID string) (*WorkerSession, error) {
	var candidate Candidate
	err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, candidateGatedKind, runID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		return json.Unmarshal(raw, &candidate)
	})
	return candidate.WorkerSession, err
}

// retainedWorkerSession reports whether the refusal still needs its checkout.
// A triage hold keeps the session in the refusal for retry, but not the lane:
// the authorizing retry stages the returned session's head on an available lane.
func (l *Loop) retainedWorkerSession(ctx context.Context, runID string) (*WorkerSession, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var session *WorkerSession
	var triaged bool
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var raw []byte
		// A judge refusal belongs to the judge run. Read its writer link only
		// after this writer was dispatched, not across the board's history.
		if err := r.QueryRow(`SELECT payload FROM (
 SELECT seq,payload FROM events WHERE kind=? AND object_type='run' AND object_id=?
 UNION ALL
 SELECT seq,payload FROM events WHERE kind=? AND object_type='run' AND object_id<>?
   AND seq > (SELECT MAX(seq) FROM events WHERE kind=? AND object_type='run' AND object_id=?)
   AND json_extract(payload,'$.writer_run_id')=? AND json_type(payload,'$.worker_session')='object'
) ORDER BY seq DESC LIMIT 1`, RefusalKind, runID, RefusalKind, runID, dispatchStartedKind, runID, runID).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		var p refusalPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		session = p.WorkerSession
		if session == nil {
			return nil
		}
		if p.Step == RefusalGate && session.SessionID == "" {
			// No resumable session is held on the lane; the refused candidate is pinned
			// for fresh staging on whichever lane the next run receives.
			session = nil
			return nil
		}
		if p.Step != RefusalVerifyNotDone {
			return nil
		}
		// Use the recorded hold, not the current fault: an operator may have
		// authorized retry between the hold and settlement (or its replay).
		return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='item' AND object_id=?
 AND json_extract(payload,'$.run_id')=? AND json_extract(payload,'$.reason')=?)`, itemFaultKind, p.ItemID, runID, reasonVerifyTriage).Scan(&triaged)
	})
	if err != nil || !triaged {
		return session, err
	}
	head, err := l.revParse(ctx, l.lanePinRepository(session.LaneID), pinRef(runID)+"^{commit}")
	if err != nil {
		return nil, err
	}
	if head != session.HeadSHA {
		return nil, Externalf("triage pin for %s names %s, want %s", runID, head, session.HeadSHA)
	}
	return nil, nil
}

func reusedNodesExist(r *store.Reader) (bool, error) {
	var exists bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_reused')`).Scan(&exists)
	return exists, err
}

func nodeSourceAttempt(r *store.Reader, itemID string, attempt int, node string) (int, error) {
	exists, err := reusedNodesExist(r)
	if err != nil || !exists {
		return attempt, err
	}
	source := attempt
	err = r.QueryRow(`SELECT source_attempt FROM node_reused WHERE item_id=? AND attempt=? AND node_id=?`, itemID, attempt, node).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return attempt, nil
	}
	return source, err
}

type workflowNodeRow struct{ NodeID, RunID, Outcome string }

func effectiveNodeRuns(r *store.Reader, itemID string, attempt int) ([]workflowNodeRow, error) {
	exists, err := reusedNodesExist(r)
	if err != nil {
		return nil, err
	}
	query := `SELECT node_id,run_id,outcome,rowid AS position FROM node_runs WHERE item_id=? AND attempt=?`
	args := []any{itemID, attempt}
	if exists {
		query += ` UNION ALL SELECT n.node_id,n.run_id,n.outcome,n.rowid AS position FROM node_runs n JOIN node_reused x ON x.item_id=n.item_id AND x.source_attempt=n.attempt AND x.node_id=n.node_id WHERE x.item_id=? AND x.attempt=?`
		args = append(args, itemID, attempt)
	}
	rows, err := r.Query(query+` ORDER BY position`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []workflowNodeRow{}
	for rows.Next() {
		var n workflowNodeRow
		var position int64
		if err := rows.Scan(&n.NodeID, &n.RunID, &n.Outcome, &position); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

// Reuse references to accepted predecessor records, never synthetic runs or
// copied terminal records. The writer and its successors are judged again.
func carryReturnedNodeInputs(tx *store.Tx, d Decision) error {
	var old *WorkerSession
	if c := d.RetryCandidate; c != nil && c.Available {
		old = c.WorkerSession
	} else if d.Returned != nil && d.RunRequest != nil && (d.RunRequest.SessionTurn || d.WorkerSessionReason == "resume-failed" || verifyBuildReturn(d.Returned) || verifyNoVerdictReturn(d.Returned) || gateBuildReturn(d.Returned) || pieceScopeReturn(d.Returned)) {
		old = d.Returned.WorkerSession
	}
	if d.RunRequest == nil || d.Workflow == nil || old == nil {
		return nil
	}
	for i := 0; i < d.NodeIndex; i++ {
		id := d.Workflow.Stages[i].ID
		source, ok, err := returnedNodeSource(tx.Reader(), d.ItemID, old.Attempt, d.Attempt, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO node_reused(item_id,attempt,node_id,source_attempt) VALUES(?,?,?,?) ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, id, source); err != nil {
			return err
		}
	}
	return nil
}

// returnedNodeSource answers the attempt whose run of node a returned resume reuses: the
// returned session's attempt, or the attempt that one reused the node from, provided a run
// of the node is recorded there. A session recorded without an attempt, or a reuse that
// names an attempt where the node never ran, falls back to the latest earlier attempt in
// which the node passed. Reusing an attempt with no run of the node would leave the node
// without an outcome, so the workflow would dispatch it again after the resumed writer,
// and a first node's staging resets the lane to the base, away from the offered commit.
// ok is false when no earlier attempt ran the node.
func returnedNodeSource(r *store.Reader, itemID string, from, before int, node string) (int, bool, error) {
	if from > 0 {
		source, err := nodeSourceAttempt(r, itemID, from, node)
		if err != nil {
			return 0, false, err
		}
		var ran bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM node_runs WHERE item_id=? AND attempt=? AND node_id=?)`, itemID, source, node).Scan(&ran); err != nil {
			return 0, false, err
		}
		if ran {
			return source, true, nil
		}
	}
	var source int
	err := r.QueryRow(`SELECT attempt FROM node_runs WHERE item_id=? AND node_id=? AND outcome='passed' AND attempt<? ORDER BY attempt DESC LIMIT 1`, itemID, node, before).Scan(&source)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return source, true, nil
}

// judgedCandidateReturn identifies a retained candidate, not merely a resumed conversation.
func judgedCandidateReturn(d Decision) *WorkerSession {
	if c := d.RetryCandidate; c != nil && c.Available && c.Cause == ReasonEvalFailed {
		return &WorkerSession{RunID: c.RunID, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA}
	}
	if d.Returned == nil || d.Returned.Cause != ReasonEvalFailed || d.WorkerSessionReason != "resumed-worker" {
		return nil
	}
	s := d.Returned.WorkerSession
	if s == nil || s.HeadSHA == "" || s.HeadSHA == s.BaseSHA {
		return nil
	}
	return s
}

func (l *Loop) stageWorkerSession(d *Decision, spec *StageSpec) error {
	if d.Returned == nil {
		return nil
	}
	s := d.Returned.WorkerSession
	if d.WorkerSessionReason != "resumed-worker" || s == nil {
		return nil
	}
	// Missing handles are selected fresh by the planner. If the handle changes
	// after the claim, refuse staging so re-planning can choose the fresh path,
	// rather than opening the wrong session.
	session, err := ReadSessionID(l.laneDirPath(d.LaneID))
	if err != nil {
		return err
	}
	if session != s.SessionID {
		return Externalf("retained worker session %s disappeared from lane %s before staging", s.SessionID, d.LaneID)
	}
	// A landed lane was released and reset; a revert or squash-conflict return
	// continues the conversation on the current trunk, not the now-stale checkout
	// of the earlier candidate.
	if d.Returned.Cause != RefusalReverted && d.Returned.Cause != RefusalConflict {
		spec.Continue = true
		spec.BaseSHA = s.BaseSHA
		if judgedCandidateReturn(*d) != nil || verifyBuildReturn(d.Returned) || verifyNoVerdictReturn(d.Returned) {
			spec.KeptItemBaseSHA = s.BaseSHA
		}
	}
	spec.SessionID = s.SessionID
	if d.Returned.Cause == RefusalPlanOutput && d.Returned.RunID != "" && d.RunRequest != nil {
		if err := copyPreservedPlanOutputs(nodeOutputsDir(l.cfg.Root, d.Returned.RunID), nodeOutputsDir(l.cfg.Root, d.RunRequest.RunID)); err != nil {
			return err
		}
	}
	return nil
}

// copyPreservedPlanOutputs carries regular non-delivery files (such as do-it-now or
// proposal) into a resumed planner turn's output directory so finished work is kept.
func copyPreservedPlanOutputs(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == deliveryOutput {
			continue
		}
		src := filepath.Join(srcDir, name)
		info, err := os.Lstat(src)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		dst := filepath.Join(dstDir, name)
		if _, err := os.Lstat(dst); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, raw, 0644); err != nil {
			return err
		}
	}
	return nil
}
