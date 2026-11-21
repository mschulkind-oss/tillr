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

func workerSessionReason(s *WorkerSession, req *run.Request) string {
	if s == nil {
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
		writingNode := w.Decision.Workflow != nil && w.Decision.NodeIndex >= 0 && w.Decision.NodeIndex < len(w.Decision.Workflow.Stages) && w.Decision.Workflow.Stages[w.Decision.NodeIndex].Access == "write"
		eligible := w.Decision.Seat == "" || w.Decision.Seat == "build" || (writingNode && w.Decision.BlockConflict == nil && w.Decision.DeskDocument == "")
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
	return &WorkerSession{RunID: req.RunID, SessionID: session, LaneDir: l.laneDirPath(c.LaneID), LaneID: c.LaneID, BaseSHA: base, HeadSHA: head, Backend: req.Backend, Route: req.Route(), Workflow: decision.Workflow, NodeIndex: decision.NodeIndex, NodeBaseSHA: decision.NodeBaseSHA, Attempt: decision.Attempt}, nil
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
	return &WorkerSession{RunID: req.RunID, SessionID: id, LaneDir: l.laneDirPath(req.LaneID), LaneID: req.LaneID, BaseSHA: base, HeadSHA: head, Backend: req.Backend, Route: req.Route(), Workflow: work.Decision.Workflow, NodeIndex: work.Decision.NodeIndex, NodeBaseSHA: work.Decision.NodeBaseSHA, Attempt: work.Decision.Attempt}, nil
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
		if session == nil || p.Step != RefusalVerifyNotDone {
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

func init() {
	if err := store.RegisterDDL("drain-worker-returns", `CREATE TABLE IF NOT EXISTS node_reused (
 item_id TEXT NOT NULL, attempt INTEGER NOT NULL, node_id TEXT NOT NULL,
 source_attempt INTEGER NOT NULL, PRIMARY KEY(item_id,attempt,node_id)
 );`); err != nil {
		panic(err)
	}
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
	} else if d.Returned != nil && d.RunRequest != nil && (d.RunRequest.SessionTurn || d.WorkerSessionReason == "resume-failed" || verifyBuildReturn(d.Returned) || verifyNoVerdictReturn(d.Returned)) {
		old = d.Returned.WorkerSession
	}
	if d.RunRequest == nil || d.Workflow == nil || old == nil {
		return nil
	}
	for i := 0; i < d.NodeIndex; i++ {
		id := d.Workflow.Stages[i].ID
		source, err := nodeSourceAttempt(tx.Reader(), d.ItemID, old.Attempt, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_reused(item_id,attempt,node_id,source_attempt) VALUES(?,?,?,?) ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, id, source); err != nil {
			return err
		}
	}
	return nil
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
	if s == nil || s.HeadSHA == "" || s.HeadSHA == s.BaseSHA || s.HeadSHA == s.NodeBaseSHA {
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
	// A landed lane was released and reset; a revert continues the conversation
	// on the current trunk, not the now-stale checkout of the landed candidate.
	if d.Returned.Cause != RefusalReverted {
		spec.Continue = true
		spec.BaseSHA = s.BaseSHA
		if judgedCandidateReturn(*d) != nil || verifyBuildReturn(d.Returned) || verifyNoVerdictReturn(d.Returned) {
			spec.KeptItemBaseSHA = s.BaseSHA
		}
	}
	spec.SessionID = s.SessionID
	return nil
}
