// Architecture and invariants: docs/reference/tillr-designer-sessions.md
package drain

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// chooseDesigner applies the dispatch table in docs/reference/tillr-designer-sessions.md, in order.
// Unknown cache retention is expired, never an invented five-minute lifetime.
func chooseDesigner(d *lastDesigner, req *run.Request, at time.Time, previousAdapter string) (string, string) {
	if d == nil {
		return "", "no-designer"
	}
	if previousAdapter != "" {
		kind, err := os.ReadFile(filepath.Join(filepath.Dir(previousAdapter), "pause-kind"))
		if err == nil && strings.TrimSpace(string(kind)) == run.PauseCannotResume {
			return "", "cannot-resume"
		}
	}
	if d.Backend != req.Backend || d.Model != req.Route() {
		return "", "seat-changed"
	}
	if info, err := os.Stat(d.Transcript); err != nil || !info.Mode().IsRegular() {
		return "", "transcript-missing"
	}
	last, err := time.Parse(time.RFC3339Nano, d.LastRequest)
	if err == nil && d.CacheLifetimeSeconds != nil && *d.CacheLifetimeSeconds > 0 && at.Before(last.Add(time.Duration(*d.CacheLifetimeSeconds)*time.Second)) {
		return d.SessionID, "resumed-warm"
	}
	if d.ContextTokens != nil && *d.ContextTokens > 40000 && compactStep(req) != "" {
		return d.SessionID, "resumed-compacted"
	}
	return d.SessionID, "resumed-expired"
}

func compactStep(req *run.Request) string {
	if len(req.Command) == 0 {
		return ""
	}
	path := filepath.Join(filepath.Dir(req.Command[0]), "compact")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		return path
	}
	return ""
}

type designerCompaction struct {
	TokensBefore *int     `json:"tokens_before"`
	TokensAfter  *int     `json:"tokens_after"`
	Tokens       *int64   `json:"tokens"`
	CostUSD      *float64 `json:"cost_usd"`
}

// Compaction is dispatch work, not a timer. Its session/checkpoint journal
// reconciles interrupted completion before another dispatch can pay again.
func (l *Loop) compactDesigner(ctx context.Context, d Decision) error {
	if d.DesignerReason != "resumed-compacted" {
		return nil
	}
	journal, err := l.ensureDesignerCompaction(ctx, d)
	if err != nil {
		return err
	}
	receipt := *journal.Receipt
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var recorded bool
		if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM events WHERE kind=? AND object_id=?)", designerCompactedKind, journal.RunID).Scan(&recorded); err != nil {
			return err
		}
		if recorded {
			return nil
		}
		return appendEffect(tx, designerCompactedKind, journal.RunID, struct {
			ItemID    string `json:"item_id"`
			SessionID string `json:"session_id"`
			designerCompaction
		}{journal.ItemID, d.DesignerSessionID, receipt}, l.cfg.Now().UTC())
	})
}

// A partial model answer (including tool calls) is already a response. Never
// replay it as though no work happened. Error-only assistant messages are not answers.
func failedDesignerResume(d Decision, rec run.Record) (bool, error) {
	workerTurn := d.RunRequest != nil && d.RunRequest.SessionTurn && d.WorkerSessionReason == "resumed-worker"
	designTurn := d.DesignContinuationFrom != "" && d.RunRequest != nil && d.RunRequest.SessionTurn
	if (!strings.HasPrefix(d.DesignerReason, "resumed-") && !workerTurn && !designTurn) || rec.ExitCode == nil || *rec.ExitCode == 0 || rec.State != run.StateExited {
		return false, nil
	}
	if (workerTurn || designTurn) && rec.EndCause != run.EndNormalExit {
		return false, nil
	}
	sawLog, missingSession := false, false
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind != "log" {
			continue
		}
		sawLog = true
		f, err := os.Open(ref.Ref)
		if err != nil {
			return false, err
		}
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 64<<10), 16<<20)
		response := false
		for scan.Scan() {
			if designTurn && missingDesignSessionDiagnostic(scan.Text(), d.DesignerSessionID) {
				missingSession = true
			}
			var e struct {
				Type    string `json:"type"`
				Message struct {
					Role       string            `json:"role"`
					StopReason string            `json:"stopReason"`
					Content    []json.RawMessage `json:"content"`
				} `json:"message"`
				AssistantMessageEvent struct {
					Type string `json:"type"`
				} `json:"assistantMessageEvent"`
			}
			if json.Unmarshal(scan.Bytes(), &e) != nil {
				continue
			}
			if e.Type == "message_update" {
				switch e.AssistantMessageEvent.Type {
				case "text_delta", "thinking_delta", "toolcall_delta", "text_end", "thinking_end", "toolcall_end":
					response = true
				}
			}
			if (e.Type == "turn_end" || e.Type == "message_end" || e.Type == "assistant") && e.Message.Role == "assistant" {
				for _, raw := range e.Message.Content {
					var content struct {
						Type     string `json:"type"`
						Text     string `json:"text"`
						Thinking string `json:"thinking"`
						Data     string `json:"data"`
					}
					if json.Unmarshal(raw, &content) != nil {
						continue
					}
					if content.Text != "" || content.Thinking != "" || content.Data != "" || content.Type == "toolCall" || content.Type == "tool_use" {
						response = true
					}
				}
			}
		}
		err = scan.Err()
		f.Close()
		if err != nil {
			return false, err
		}
		if response {
			return false, nil
		}
	}
	if designTurn {
		return missingSession, nil
	}
	return sawLog, nil
}

// Match explicit harness diagnostics, not the absence of a model response.
func missingDesignSessionDiagnostic(line, id string) bool {
	return id != "" && (strings.Contains(line, "No session found matching '"+id+"'") || strings.Contains(line, "No conversation found with session ID: "+id))
}

// retireDesigners deletes only the loop's rows. Harness retention owns transcripts.
func (l *Loop) retireDesigners(ctx context.Context) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	docs := []string{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var exists bool
		if err := r.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='last_designers')").Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return nil
		}
		rows, err := r.Query("SELECT document FROM last_designers ORDER BY document")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var doc string
			if err := rows.Scan(&doc); err != nil {
				return err
			}
			docs = append(docs, doc)
		}
		return rows.Err()
	})
	s.Close()
	if err != nil || len(docs) == 0 {
		return err
	}
	if l.questions == nil {
		if l.syncedSHA != "" {
			l.questions = newSavedQuestions(l.cfg.Root, l.syncedSHA, l.cfg.QuestionsCmd, ctx)
		} else {
			l.questions = &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}
		}
	}
	retired := []string{}
	// refreshDocuments pins the trunk commit this pass read. Never consult the moving
	// ref here: a concurrent landing may have advanced it after that observation.
	trunkSHA := l.syncedSHA
	for _, doc := range docs {
		if _, err := os.Stat(filepath.Join(l.cfg.Root, doc)); os.IsNotExist(err) {
			// A landing rewrites the document in place, and git unlinks a file before it
			// writes the new one, so absence from the working tree is not a deletion. The
			// trunk's tree is what says the document is gone.
			if trunkSHA == "" {
				// Standalone retirement (outside a refreshed pass) still takes one snapshot.
				trunkSHA, err = l.revParse(ctx, l.cfg.Root, TrunkRef)
				if err != nil {
					return err
				}
			}
			listed, err := l.git(ctx, l.cfg.Root, "ls-tree", "--name-only", trunkSHA, "--", doc)
			if err != nil {
				return err
			}
			if strings.TrimSpace(listed) == "" {
				retired = append(retired, doc)
			}
			continue
		} else if err != nil {
			return err
		}
		var done bool
		if ds, ok := l.questions.(interface{ Done(string) (bool, error) }); ok {
			done, err = ds.Done(doc)
		}
		if err != nil {
			return err
		}
		if done {
			retired = append(retired, doc)
		}
	}
	if len(retired) == 0 {
		return nil
	}
	writable, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer writable.Close()
	return writable.WriteTx(ctx, func(tx *store.Tx) error {
		for _, doc := range retired {
			if _, err := tx.Exec("DELETE FROM last_designers WHERE document=?", doc); err != nil {
				return err
			}
		}
		return nil
	})
}

const designerCompactedKind = "drain.designer-compacted"

func (l *Loop) replaceFailedDesigner(ctx context.Context, old Decision, rec run.Record, mainSHA, tick string, at time.Time, result *LoopResult, timing *PassTiming, watched []string, deferJudge bool) laneOutcome {
	var held bool
	checkWait := func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, old.ItemID).Scan(&state); err != nil || state != StateBlocked {
			return err
		}
		wait, _, err := item.ReadWaitingOn(r, old.ItemID)
		if err != nil || wait == nil || wait.Waiter == "" {
			return err
		}
		conflicts, err := item.ReadConflicts(r)
		if err == nil {
			held = conflicts[old.ItemID].HasIndependentWait(wait)
		}
		return err
	}
	if err := l.readStore(ctx, checkWait); err != nil || held {
		return laneOutcome{err: err}
	}
	backend, open, err := l.quotaReplacementBackend(ctx, old.RunRequest.Backend, old.Seat)
	if err != nil {
		return laneOutcome{err: err}
	}
	if !open {
		l.reportf("loop: failed designer resume %s waits for backend quota\n", rec.RunID)
		// Keep the original terminal run pending. Planning holds it by quota, and
		// reopening retries this same replacement without another failed resume.
		return laneOutcome{}
	}
	next := old
	next.Action = ActionDispatch
	next.Attempt++
	next.Reason = "resume-failed"
	next.DesignerReason = "resume-failed"
	next.DesignerSessionID = ""
	if old.RunRequest.SessionTurn && old.DesignContinuationFrom == "" {
		next.WorkerSessionReason = "resume-failed"
	}
	req := *old.RunRequest
	req.RunID = attemptRunID(old.ItemID, next.Attempt)
	if backend != req.Backend {
		replacement := l.currentPolicy().request(old.ItemID, old.LaneID, req.RunID, backend)
		if replacement == nil {
			return laneOutcome{err: Usagef("designer replacement backend %s has no request", backend)}
		}
		replacement.CorrelationID = req.CorrelationID
		replacement.Seat = req.Seat
		replacement.Workflow = req.Workflow
		replacement.Node = req.Node
		replacement.NodeID = req.NodeID
		req = *replacement
	}
	req.Resume = false
	req.SessionTurn = false
	req.ResumePrompt = ""
	if old.RunRequest.SessionTurn && next.Workflow != nil {
		req.RunID += "-n" + strconv.Itoa(next.NodeIndex+1)
	}
	stampDecisionRequest(next, &req)
	next.RunRequest = &req
	identifyDecision(&next)
	spec := StageSpec{Policy: l.currentPolicy(), Continue: true, LaneID: next.LaneID, ItemID: next.ItemID, RunID: req.RunID, Backend: req.Backend, BaseSHA: l.laneBase(next.LaneID), WorkingDir: req.WorkingDir, TimeoutMS: req.TimeoutMS, Returned: old.Returned}
	if old.DesignContinuationFrom != "" {
		c := *old.BlockConflict
		c.DesignRunID = req.RunID
		c.Text += "\n\nThe recorded designer session rejected continuation; starting a fresh design session."
		next.BlockConflict, spec.BlockConflict = &c, &c
	}
	if old.RunRequest.SessionTurn && next.Workflow != nil {
		spec.Node = &next.Workflow.Stages[next.NodeIndex]
		var err error
		spec.InputPaths, err = l.nodeInputPaths(ctx, old)
		if err != nil {
			return laneOutcome{err: err}
		}
	}
	if old.RunRequest.SessionTurn {
		if err := l.preserveWork(ctx, old.LaneID, rec.RunID, "worker session could not resume"); err != nil {
			return laneOutcome{err: err}
		}
	}
	if err := l.stageComment(ctx, &next, &spec); err != nil {
		return laneOutcome{err: err}
	}
	if err := l.stageLandingAnswer(ctx, next, &spec); err != nil {
		return laneOutcome{err: err}
	}
	if err := l.cfg.Stage.Stage(ctx, spec); err != nil {
		return laneOutcome{err: err}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return laneOutcome{err: err}
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		// A block written during staging must also win over the replacement claim.
		if err := checkWait(tx.Reader()); err != nil || held {
			return err
		}
		if _, err := tx.Exec("UPDATE comment_runs SET outcome='resume-failed' WHERE run_id=?", rec.RunID); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationDoneKind, rec.RunID, struct{}{}, l.cfg.Now().UTC()); err != nil {
			return err
		}
		owner, err := currentLoopOwner()
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?", run.LaneStateIdle, next.LaneID, rec.RunID); err != nil {
			return err
		}
		if _, err = run.ClaimLease(ctx, tx, req, next.LaneID, l.cfg.Now().UTC()); err != nil {
			return err
		}
		if err := carryReturnedNodeInputs(tx, next); err != nil {
			return err
		}
		if err := startNode(tx, next); err != nil {
			return err
		}
		if err = recordPassDecision(tx, next, l.cfg.Now().UTC()); err != nil {
			return err
		}
		if next.DesignContinuationFrom != "" {
			if err := item.RecordConflict(tx, *next.BlockConflict, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		return appendEffect(tx, dispatchStartedKind, req.RunID, &pendingWork{Decision: next, BaseSHA: spec.BaseSHA, Owner: owner, ParentRunID: rec.RunID, LoopID: l.sessionID}, l.cfg.Now().UTC())
	})
	s.Close()
	if err != nil || held {
		return laneOutcome{err: err}
	}
	before, err := l.snapshot(ctx, []string{l.lanePath(next.LaneID)}, watched)
	if err != nil {
		return laneOutcome{err: err}
	}
	l.mu.Lock()
	result.Dispatched++
	l.mu.Unlock()
	l.reportf("loop: resume-failed %s -> new session %s on %s\n", rec.RunID, next.DesignerSessionID, next.LaneID)
	replacement, log, err := l.runRestartable(ctx, req)
	if err != nil {
		return laneOutcome{err: err}
	}
	l.countSpend(result, replacement)
	stop, err := l.closeRunBracket(ctx, l.lanePath(next.LaneID), req, tick, at, before, result, watched)
	if err != nil || stop != "" {
		return laneOutcome{stop: stop, err: err}
	}
	return l.settle(ctx, next, replacement, log, mainSHA, tick, at, result, timing, watched, deferJudge)
}
