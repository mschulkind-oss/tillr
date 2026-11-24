package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// JudgeWriter is the retained identity and route of a writing node's run.
type JudgeWriter struct {
	NodeID    string `json:"node_id"`
	RunID     string `json:"run_id"`
	SessionID string `json:"session_id"`
	Backend   string `json:"backend"`
	Route     string `json:"route"`
}

func judgeWriterRun(c Candidate) string {
	writers := c.Writers
	if len(writers) == 0 {
		writers = c.Producers
	}
	if c.Judge != nil {
		writers = c.Judge.Writers
		if len(writers) == 0 {
			writers = c.Judge.Producers
		}
	}
	if len(writers) > 0 {
		return writers[len(writers)-1].RunID
	}
	return c.RunID
}

func evaluatorSlot(w *Workflow) string {
	return workflow.EvaluatorSlot(w)
}
func evaluatorNode(d Decision) bool {
	return evaluatorSlot(d.Workflow) != "" && d.Workflow.Stages[d.NodeIndex].ID == evaluatorSlot(d.Workflow)
}

func validateEvaluator(w *Workflow) error {
	return workflow.ValidateEvaluator(w)
}

// Evaluator receipts keep their existing string-valued input format. Repeated
// inputs list every accepted producer's file on its own line in copy order.
func evaluatorInputFiles(inputs map[string][]NodeInput) map[string]string {
	return workflow.EvaluatorInputFiles(inputs)
}

func (l *Loop) evaluatorCandidate(ctx context.Context, d Decision) (Candidate, error) {
	req := *d.RunRequest
	req.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	stampDecisionRequest(d, &req)
	c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: l.laneTarget(d.LaneID), BaseSHA: l.laneBase(d.LaneID), BaseRef: l.lanePatchRef(d.LaneID), Workflow: d.workflowRef(), Evaluator: &req}
	var err error
	c.HeadSHA = d.NodeBaseSHA
	if c.HeadSHA == "" {
		c.HeadSHA, err = l.revParse(ctx, c.Worktree, "HEAD")
		if err != nil {
			return c, err
		}
	}
	inputs, err := l.nodeInputPaths(ctx, d)
	if err != nil {
		return c, err
	}
	c.EvaluatorInputs = evaluatorInputFiles(inputs)
	c.EvaluatorTask = d.Workflow.Stages[d.NodeIndex].Prompt
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return c, err
	}
	defer db.Close()
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		_, history, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		rows, err := effectiveNodeRuns(r, d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		for _, n := range rows {
			if n.Outcome != "passed" && n.Outcome != "handled-fail" {
				continue
			}
			node, id := n.NodeID, n.RunID
			writing := false
			var producer WorkflowStage
			for _, stage := range d.Workflow.Stages {
				if stage.ID == node {
					producer = stage
					writing = stage.Access == "write"
					break
				}
			}
			if node == evaluatorSlot(d.Workflow) {
				continue
			}
			work := history[id]
			if work == nil || work.Decision.RunRequest == nil {
				return fmt.Errorf("writer %s has no recorded request", id)
			}
			raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, id), SessionIDFile))
			if err != nil {
				return err
			}
			req := work.Decision.RunRequest
			identity := JudgeWriter{NodeID: node, RunID: id, SessionID: strings.TrimSpace(string(raw)), Backend: req.Backend, Route: req.Route()}
			c.Producers = append(c.Producers, identity)
			c.EvaluatorTask += fmt.Sprintf("\nNode %s: seat=%s; access=%s; prompt=%s", node, producer.Seat, producer.Access, producer.Prompt)
			if writing {
				c.Writers = append(c.Writers, identity)
			}
		}
		return nil
	})
	return c, err
}

func (l *Loop) settleEvaluator(ctx context.Context, d Decision, rec run.Record, base string, result *LoopResult) (bool, error) {
	db, e := store.OpenRead(ctx, l.cfg.Store)
	if e != nil {
		return true, e
	}
	var outcome string
	var refused bool
	var nonQuotaPaused bool
	e = db.ReadTx(ctx, func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, d.RunRequest.RunID).Scan(&outcome); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_type='run' AND object_id=? AND json_extract(payload,'$.step')=?)`, RefusalKind, d.RunRequest.RunID, RefusalVerifyNotDone).Scan(&refused); err != nil {
			return err
		}
		var pauseErr error
		_, nonQuotaPaused, pauseErr = pausedRunNonQuota(r, d.RunRequest.RunID)
		return pauseErr
	})
	db.Close()
	if e != nil {
		return true, e
	}
	if outcome == "evaluated-fail" && nonQuotaPaused && !refused {
		if err := l.nodeOutcome(ctx, d, "running"); err != nil {
			return true, err
		}
		outcome = "running"
	}
	// A checked refusal is durable before cleanup and examination. Replaying it
	// must not recheck the lane after cleanup has started.
	if outcome == "evaluated-fail" || refused {
		return true, l.finishEvaluatorHold(ctx, d, Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, HeadSHA: d.NodeBaseSHA})
	}
	c, err := l.evaluatorCandidate(ctx, d)
	if err != nil {
		return true, err
	}
	c, accepted, err := l.judgeCandidate(ctx, c, result)
	if errors.Is(err, errJudgeBackendHeld) || errors.Is(err, errJudgePaused) {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	if !accepted {
		// The judge step has already recorded its own hold reason. Do not replace it
		// with a generic failed-node hold or follow a workflow fail edge.
		return true, l.finishEvaluatorHold(ctx, d, c)
	}
	if err := l.nodeOutcome(ctx, d, "passed"); err != nil {
		return true, err
	}
	if rec.State == run.StatePaused {
		if err := l.readStore(ctx, func(r *store.Reader) error {
			fresh, readErr := terminalRecord(r, d.RunRequest.RunID)
			if readErr == nil && fresh != nil {
				rec = *fresh
			}
			return readErr
		}); err != nil {
			return true, err
		}
	}
	next, err := l.nextWorkflowNode(ctx, d)
	if err != nil {
		return true, err
	}
	if next >= 0 {
		return true, l.advanceNode(ctx, d, c.BaseSHA, "")
	}
	return l.finishWorkflow(ctx, d, rec, c.BaseSHA, c.HeadSHA, result)
}

func (l *Loop) finishEvaluatorHold(ctx context.Context, d Decision, c Candidate) error {
	if err := l.nodeOutcome(ctx, d, "evaluated-fail"); err != nil {
		return err
	}
	if stop := l.abandon(ctx, d.LaneID, c.RunID, c.HeadSHA); stop != "" {
		return Externalf("evaluator lane cleanup failed: %s", stop)
	}
	return l.markExamined(ctx, d)
}

// Carry the already checked receipt, not a second verdict reader, into admission.
// A later read-only node may be the graph's last node, so use the stored node run.
func (l *Loop) attachWorkflowJudge(ctx context.Context, d Decision, c *Candidate) error {
	id := evaluatorSlot(d.Workflow)
	if id == "" {
		return nil
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	defer db.Close()
	var runID, outcome string
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		source, err := nodeSourceAttempt(r, d.ItemID, d.Attempt, id)
		if err != nil {
			return err
		}
		return r.QueryRow(`SELECT run_id, outcome FROM node_runs WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, source, id).Scan(&runID, &outcome)
	})
	if err != nil {
		return err
	}
	if outcome == "skipped" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, runID), judgeReceiptFile))
	if err != nil {
		return err
	}
	receipt := &JudgeReceipt{}
	if err := json.Unmarshal(raw, receipt); err != nil {
		return err
	}
	c.Judge = receipt
	return nil
}
