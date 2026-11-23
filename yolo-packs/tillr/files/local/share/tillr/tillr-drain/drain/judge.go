package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonJudgeUnreadable        = "judge-unreadable"
	ReasonEvalFailed             = "eval-failed"
	ReasonJudgeBlocked           = "judge-blocked"
	ReasonJudgeDefective         = "judge-defective"
	ReasonJudgeDefectiveMismatch = "judge-defective-evidence-mismatch"
	ReasonJudgeCloseRefused      = "judge-defective-close-refused"
	judgeRequestKind             = "drain.judge.request"
	judgeReceiptFile             = "judge.json"
	judgeLandedKind              = "drain.judge.landed"
)

var (
	errJudgeBackendHeld = errors.New("judge settlement waits for an open review backend")
	errJudgePaused      = errors.New("judge run paused; resumes when a lane frees")
)

// JudgeReceipt retains the caller-owned identities and the exact checked bytes.
// It travels with the gated candidate so a retained landing needs no second judge.
type JudgeReceipt struct {
	Build         string            `json:"tillr_build,omitempty"`
	Options       item.JudgeOptions `json:"options"`
	Raw           json.RawMessage   `json:"raw,omitempty"`
	Request       run.Request       `json:"request"`
	JudgeSession  string            `json:"judge_session,omitempty"`
	WorkerBrief   []byte            `json:"worker_brief"`
	WorkerSession []byte            `json:"worker_session"`
	Writers       []JudgeWriter     `json:"writers,omitempty"`
	Producers     []JudgeWriter     `json:"producers,omitempty"`
	Inputs        map[string]string `json:"inputs,omitempty"`
	EvaluatorTask string            `json:"evaluator_task,omitempty"`
	legacy        bool
}

// The retired field distinguishes pre-upgrade receipts from incomplete new
// receipts. Its value is never used as authority or written back.
func (r *JudgeReceipt) UnmarshalJSON(raw []byte) error {
	type receipt JudgeReceipt
	var decoded receipt
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var old struct {
		Options struct{ GrantID json.RawMessage } `json:"options"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		return err
	}
	*r = JudgeReceipt(decoded)
	r.legacy = old.Options.GrantID != nil
	return nil
}

func bindSavedJudge(r *store.Reader, c Candidate, receipt *JudgeReceipt, options *item.JudgeOptions) error {
	head := c.HeadSHA
	if c.WorkerHeadSHA != "" {
		head = c.WorkerHeadSHA
	}
	if options.ItemID != c.ItemID || options.Head != head || receipt.Request.ItemID != c.ItemID {
		return item.Rejectionf("evaluator-binding", c.ItemID, "saved judgment belongs to a different item or candidate")
	}
	if options.EvaluatorBinding != (item.EvaluatorBinding{}) || !receipt.legacy {
		return nil // Intake checks the supplied binding against the persisted graph.
	}
	binding, err := item.ReadEvaluatorBinding(r, c.ItemID)
	if err != nil {
		return err
	}
	node := receipt.Request.NodeID
	if node == "" {
		_, _, graph, err := item.ReadWorkflowSelection(r, c.ItemID)
		if err != nil {
			return err
		}
		node = compatibilityReviewSlot(graph)
	}
	if node != binding.EvaluatorNode {
		return item.Rejectionf("evaluator-binding", c.ItemID, "legacy receipt does not match workflow %q evaluator %q", binding.Workflow, binding.EvaluatorNode)
	}
	options.EvaluatorBinding = binding
	return nil
}

func (l *Loop) judgeCandidate(ctx context.Context, c Candidate, result *LoopResult) (Candidate, bool, error) {
	if pending, err := l.buildRestartPending(ctx); pending || err != nil {
		if err != nil {
			return c, false, err
		}
		return c, false, errRestartPending
	}
	var mode, compatibilityNode string
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return c, false, err
	}
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ItemID == c.ItemID {
				mode = row.JudgeMode
				break
			}
		}
		_, _, graph, err := item.ReadWorkflowSelection(r, c.ItemID)
		compatibilityNode = compatibilityReviewSlot(graph)
		if compatibilityNode != "" {
			mode = "agent"
		}
		return err
	})
	db.Close()
	if err != nil {
		return c, false, err
	}
	receiptPath := filepath.Join(RunDir(l.cfg.Root, c.RunID), judgeReceiptFile)
	if mode != "agent" && c.Evaluator == nil && c.Judge == nil {
		if _, err := os.Stat(receiptPath); os.IsNotExist(err) {
			return c, true, nil
		} else if err != nil {
			return c, false, err
		}
	}
	receipt := c.Judge
	if receipt == nil {
		raw, readErr := os.ReadFile(receiptPath)
		if readErr == nil {
			receipt = &JudgeReceipt{}
			if err = json.Unmarshal(raw, receipt); err != nil {
				return c, false, err
			}
		} else if !os.IsNotExist(readErr) {
			return c, false, readErr
		}
	}
	var binding item.EvaluatorBinding
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var err error
		binding, err = item.ReadEvaluatorBinding(r, c.ItemID)
		return err
	}); err != nil {
		return c, false, l.holdJudge(ctx, c, judgeRunID(c.RunID), ReasonJudgeUnreadable, err.Error(), result)
	}
	if c.Evaluator != nil && (c.Evaluator.NodeID != binding.EvaluatorNode || c.Workflow != binding.Workflow) {
		return c, false, l.holdJudge(ctx, c, c.Evaluator.RunID, ReasonJudgeUnreadable, "workflow evaluator does not match its persisted selection", result)
	}
	if receipt == nil && c.Evaluator == nil && compatibilityNode != binding.EvaluatorNode {
		detail := fmt.Sprintf("workflow %q must dispatch its evaluator %q; no compatibility review is selected", binding.Workflow, binding.EvaluatorNode)
		return c, false, l.holdJudge(ctx, c, judgeRunID(c.RunID), ReasonJudgeUnreadable, detail, result)
	}
	buildHash, err := l.build.Hash()
	if err != nil {
		return c, false, err
	}
	var replacementRequest *run.Request
	var replacementSession string
	var resumedRequest *run.Request
	workerHead := c.HeadSHA
	if c.WorkerHeadSHA != "" {
		workerHead = c.WorkerHeadSHA
	}
	if receipt != nil && c.RebasedFromHead != "" && receipt.Options.ItemID == c.ItemID && receipt.Options.Head == c.RebasedFromHead && receipt.Options.Head != workerHead {
		var diskReceipt *JudgeReceipt
		if c.Judge != nil {
			if raw, readErr := os.ReadFile(receiptPath); readErr == nil {
				var decoded JudgeReceipt
				if err := json.Unmarshal(raw, &decoded); err != nil {
					return c, false, err
				}
				if decoded.Options.ItemID == c.ItemID && decoded.Options.Head == workerHead {
					diskReceipt = &decoded
				}
			} else if !os.IsNotExist(readErr) {
				return c, false, readErr
			}
		}
		if diskReceipt != nil {
			receipt = diskReceipt
			c.Judge = diskReceipt
		} else {
			if c.Evaluator == nil {
				open, err := l.recordedBackendOpen(ctx, receipt.Request.Backend)
				if err != nil {
					return c, false, err
				}
				if !open {
					return c, false, errJudgeBackendHeld
				}
			}
			if len(c.Writers) == 0 {
				c.Writers = receipt.Writers
			}
			if len(c.Producers) == 0 {
				c.Producers = receipt.Producers
			}
			if len(c.EvaluatorInputs) == 0 {
				c.EvaluatorInputs = receipt.Inputs
			}
			if c.EvaluatorTask == "" {
				c.EvaluatorTask = receipt.EvaluatorTask
			}
			req := receipt.Request
			req.RunID = markRunID(req.RunID, "fresh")
			req = l.refreshJudgeRequest(req)
			req.SessionTurn, req.ResumePrompt = false, ""
			replacementRequest = &req
			receipt = nil
			c.Judge = nil
			l.reportf("loop: re-judging %s after pre-gate rebase onto %s (previous judgment was for %s)\n",
				c.ItemID, shortSHA(c.BaseSHA), shortSHA(c.RebasedFromHead))
		}
	}
	if receipt != nil && len(receipt.Raw) == 0 {
		var rec *run.Record
		db, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return c, false, err
		}
		err = db.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			rec, err = terminalRecord(r, receipt.Request.RunID)
			return err
		})
		db.Close()
		if err != nil {
			return c, false, err
		}
		evalPath := l.judgeEvalOutputPath(c.LaneID, receipt.Request.RunID)
		_, evalStatErr := os.Stat(evalPath)
		evalMissing := os.IsNotExist(evalStatErr)
		if evalStatErr != nil && !evalMissing {
			return c, false, evalStatErr
		}
		quotaRefusal := rec != nil && judgeQuotaRefusal(*rec)
		if evalMissing && rec != nil && (quotaRefusal || judgeBackendFailure(c, *rec, logTail(rec.ArtifactRefs))) {
			if quotaRefusal {
				if err := l.recordBackendWindow(ctx, *rec); err != nil {
					return c, false, err
				}
			} else if err := l.holdJudgeInfrastructure(ctx, c, receipt, *rec, logTail(rec.ArtifactRefs)); err != nil {
				return c, false, err
			}
			open, err := l.recordedBackendOpen(ctx, receipt.Request.Backend)
			if err != nil {
				return c, false, err
			}
			if !open {
				return c, false, errJudgeBackendHeld
			}
			if err := l.restoreJudgeInputs(c, receipt); err != nil {
				return c, false, err
			}
			req := receipt.Request
			req.RunID = markRunID(req.RunID, "restart")
			req = l.refreshJudgeRequest(req)
			req.SessionTurn, req.ResumePrompt = false, ""
			if !quotaRefusal && receipt.JudgeSession != "" && canResumeInfrastructure(rec, &req) {
				replacementSession = receipt.JudgeSession
				req.SessionTurn = true
				req.ResumePrompt = "The backend has recovered. Continue reviewing the same candidate and write the verdict."
			}
			replacementRequest = &req
			receipt = nil
		} else if evalMissing && rec != nil && rec.State == run.StatePaused && rec.EndCause == run.EndRestart && !quotaRefusal {
			if c.Evaluator == nil {
				open, err := l.recordedBackendOpen(ctx, receipt.Request.Backend)
				if err != nil {
					return c, false, err
				}
				if !open {
					return c, false, errJudgeBackendHeld
				}
			}
			if len(c.Writers) == 0 {
				c.Writers = receipt.Writers
			}
			if len(c.Producers) == 0 {
				c.Producers = receipt.Producers
			}
			if len(c.EvaluatorInputs) == 0 {
				c.EvaluatorInputs = receipt.Inputs
			}
			if c.EvaluatorTask == "" {
				c.EvaluatorTask = receipt.EvaluatorTask
			}
			judgeSession := receipt.JudgeSession
			if judgeSession == "" {
				judgeSession = judgeSessionFromActor(receipt.Options.Actor)
			}
			if err := l.stageJudge(ctx, c, receipt, judgeSession); err != nil {
				return c, false, err
			}
			req := receipt.Request
			req = l.refreshJudgeRequest(req)
			req.Resume = true
			req.SessionTurn, req.ResumePrompt, req.FreshSession = false, "", false
			resumedRequest = &req
		} else if evalMissing && rec != nil && ClassifyAttempt(*rec, logTail(rec.ArtifactRefs)) == AttemptInterrupted {
			if c.Evaluator == nil {
				open, err := l.recordedBackendOpen(ctx, receipt.Request.Backend)
				if err != nil {
					return c, false, err
				}
				if !open {
					return c, false, errJudgeBackendHeld
				}
			}
			if err := l.restoreJudgeInputs(c, receipt); err != nil {
				return c, false, err
			}
			req := receipt.Request
			req.RunID = markRunID(req.RunID, "interrupted")
			req = l.refreshJudgeRequest(req)
			replacementRequest = &req
			receipt = nil
		} else if l.cfg.Restart != nil {
			// A finished judge's output is retained. Only an interrupted session is
			// repeated, never translated into a failure or a spent attempt.
			missing := false
			if rec == nil && receipt.Build == buildHash {
				here, err := currentLoopOwner()
				if err != nil {
					return c, false, err
				}
				faces, err := liveRunFaces(l.cfg.Store, receipt.Request.RunID, here)
				if err != nil {
					return c, false, err
				}
				missing = len(faces) == 0
			}
			interrupted := receipt.Build != buildHash && (rec == nil || (rec.State != run.StatePaused && (rec.EndCause == run.EndSignal || rec.EndCause == run.EndLeaseReclaimed || rec.EndCause == run.EndRestart)))
			if interrupted || missing {
				if err := l.interruptBuildRun(receipt.Request.RunID); err != nil {
					return c, false, err
				}
				_, err := restartWork(ctx, l, func(child context.Context) (struct{}, error) {
					return struct{}{}, l.awaitBuildReader(child, receipt.Request.RunID)
				})
				if err != nil {
					return c, false, err
				}
				if err := l.restoreJudgeInputs(c, receipt); err != nil {
					return c, false, err
				}
				req := receipt.Request
				if !missing {
					req.RunID = markRunID(req.RunID, "restart")
				}
				req = l.refreshJudgeRequest(req)
				replacementRequest = &req
				receipt = nil
			}
		}
	}
	seenErrors := map[string]bool{}
	for {
		var executionError error
		var judgeRec run.Record
		var judgeLog string
		if resumedRequest != nil {
			req := *resumedRequest
			resumedRequest = nil
			rec, log, runErr := l.runRestartable(ctx, req)
			if errors.Is(runErr, errRestartPending) {
				return c, false, runErr
			}
			if runErr != nil {
				if ctx.Err() != nil {
					return c, false, ctx.Err()
				}
				executionError = runErr
			}
			judgeRec, judgeLog = rec, log
			l.countSpend(result, rec)
		} else if receipt == nil {
			req := c.Evaluator
			fromReplacement := replacementRequest != nil
			if fromReplacement {
				req = replacementRequest
				replacementRequest = nil
			}
			if req == nil {
				policy := l.currentPolicy()
				if policy == nil {
					return c, false, Usagef("agent judge requires the review seat")
				}
				seat := policy.seatList("review")
				if len(seat) == 0 {
					return c, false, Usagef("agent judge requires the review seat")
				}
				req = policy.request(c.ItemID, c.LaneID, judgeRunID(c.RunID), seat[0])
				if req != nil {
					req.Seat = "review"
					req.Workflow = binding.Workflow
					req.NodeID = binding.EvaluatorNode
					req.Node = "judge"
				}
			}
			if req == nil {
				return c, false, Usagef("cannot build review request")
			}
			// Legacy compatibility requests did not stamp a graph node on the
			// transport. Do not similarly adopt a different explicit evaluator.
			if req.NodeID == "" && compatibilityNode == binding.EvaluatorNode {
				req.NodeID, req.Workflow = binding.EvaluatorNode, binding.Workflow
			}
			if req.NodeID != binding.EvaluatorNode || req.Workflow != binding.Workflow {
				detail := fmt.Sprintf("review request does not match workflow %q evaluator %q", binding.Workflow, binding.EvaluatorNode)
				return c, false, l.holdJudge(ctx, c, req.RunID, ReasonJudgeUnreadable, detail, result)
			}
			lane := l.laneDirPath(c.LaneID)
			brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
			if err != nil {
				return c, false, err
			}
			session, err := os.ReadFile(filepath.Join(lane, SessionIDFile))
			if err != nil {
				return c, false, err
			}
			judgeSession := replacementSession
			replacementSession = ""
			if judgeSession == "" {
				judgeSession, err = NewSessionID()
				if err != nil {
					return c, false, err
				}
			}
			head := c.HeadSHA
			if c.WorkerHeadSHA != "" {
				head = c.WorkerHeadSHA
			}
			tree, err := l.revParse(ctx, c.Worktree, head+"^{tree}")
			if err != nil {
				return c, false, err
			}
			if c.Evaluator == nil && !fromReplacement {
				selected, open, err := l.selectReviewDispatch(ctx, *req)
				if err != nil {
					return c, false, err
				}
				if !open {
					return c, false, errJudgeBackendHeld
				}
				req = &selected
			}
			receipt = &JudgeReceipt{Build: buildHash, Request: *req, JudgeSession: judgeSession, WorkerBrief: brief, WorkerSession: session, Options: item.JudgeOptions{ItemID: c.ItemID, Head: head, Tree: tree, Actor: "run=" + req.RunID + ";session=" + judgeSession + ";route=" + req.Route(), JudgedActor: "run=" + c.RunID + ";session=" + strings.TrimSpace(string(session)), EvaluatorBinding: binding, At: l.cfg.Now()}}
			receipt.Writers, receipt.Inputs, receipt.Producers = c.Writers, c.EvaluatorInputs, c.Producers
			receipt.EvaluatorTask = c.EvaluatorTask
			judged := c.Writers
			if len(judged) == 0 {
				judged = c.Producers
			}
			if len(judged) > 0 {
				var runs, sessions []string
				for _, writer := range judged {
					runs = append(runs, writer.RunID)
					sessions = append(sessions, writer.SessionID)
				}
				receipt.Options.JudgedActor = "run=" + strings.Join(runs, ",") + ";session=" + strings.Join(sessions, ",")
			}
			if err := writeJudgeReceipt(receiptPath, receipt); err != nil {
				return c, false, err
			}
			if err := l.stageJudge(ctx, c, receipt, judgeSession); err != nil {
				return c, false, err
			}
			if err := appendJudgment(ctx, l.cfg.Store, judgeRequestKind, "run", req.RunID, map[string]any{"item_id": c.ItemID, "writer_run_id": judgeWriterRun(c), "seat": "review", "request": req}, l.cfg.Now()); err != nil {
				return c, false, err
			}
			rec, log, runErr := l.runRestartable(ctx, *req)
			if errors.Is(runErr, errRestartPending) {
				return c, false, runErr
			}
			if runErr != nil {
				if ctx.Err() != nil {
					return c, false, ctx.Err()
				}
				executionError = runErr
			}
			judgeRec, judgeLog = rec, log
			l.countSpend(result, rec)
		} else if len(receipt.Raw) == 0 {
			rec, waitErr := restartWork(ctx, l, func(child context.Context) (run.Record, error) {
				return l.awaitJudgeRecord(child, receipt.Request.RunID)
			})
			if errors.Is(waitErr, errRestartPending) || ctx.Err() != nil {
				return c, false, waitErr
			}
			if waitErr != nil {
				executionError = waitErr
			} else {
				judgeRec, judgeLog = rec, logTail(rec.ArtifactRefs)
				l.countSpend(result, rec)
			}
		}
		if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
			return c, false, err
		}
		if len(receipt.Raw) == 0 {
			if id, readErr := ReadSessionID(l.laneDirPath(c.LaneID)); readErr == nil && id != "" {
				receipt.JudgeSession = id
			}
		}
		if err := CheckTrunkWorktree(ctx, l.cfg.Root); err != nil {
			return c, false, err
		}
		if err := l.restoreJudgeInputs(c, receipt); err != nil {
			return c, false, err
		}
		head, err := l.revParse(ctx, c.Worktree, "HEAD")
		if err != nil {
			return c, false, err
		}
		dirty, err := l.git(ctx, c.Worktree, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return c, false, err
		}
		if head != c.HeadSHA || strings.TrimSpace(dirty) != "" {
			evidence, _ := l.git(ctx, c.Worktree, "diff", c.HeadSHA)
			dir := run.DurableDir(l.laneDirPath(c.LaneID), receipt.Request.RunID)
			if err := os.WriteFile(filepath.Join(dir, "worktree-change.patch"), []byte(evidence), 0644); err != nil {
				return c, false, err
			}
			if _, err := l.git(ctx, c.Worktree, "reset", "--hard", c.HeadSHA); err != nil {
				return c, false, err
			}
			if _, err := l.git(ctx, c.Worktree, "clean", "-fd"); err != nil {
				return c, false, err
			}
			return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, "read-only judge changed the worker's candidate", result)
		}
		outputPath := l.judgeEvalOutputPath(c.LaneID, receipt.Request.RunID)
		if len(receipt.Raw) == 0 && judgeRec.RunID != "" && ClassifyAttempt(judgeRec, judgeLog) == AttemptInterrupted && ctx.Err() == nil {
			_, evalStatErr := os.Stat(outputPath)
			evalMissing := os.IsNotExist(evalStatErr)
			if evalStatErr != nil && !evalMissing {
				return c, false, evalStatErr
			}
			if evalMissing {
				if pending, err := l.buildRestartPending(ctx); err != nil {
					return c, false, err
				} else if pending {
					return c, false, errRestartPending
				}
				if c.Evaluator == nil {
					open, err := l.recordedBackendOpen(ctx, receipt.Request.Backend)
					if err != nil {
						return c, false, err
					}
					if !open {
						return c, false, errJudgeBackendHeld
					}
				}
				req := receipt.Request
				req.RunID = markRunID(req.RunID, "interrupted")
				req = l.refreshJudgeRequest(req)
				replacementRequest = &req
				receipt = nil
				continue
			}
		}
		if len(receipt.Raw) == 0 && judgeQuotaRefusal(judgeRec) {
			if _, err := os.Lstat(outputPath); os.IsNotExist(err) {
				if err := l.recordBackendWindow(ctx, judgeRec); err != nil {
					return c, false, err
				}
				return c, false, errJudgeBackendHeld
			}
		}
		if len(receipt.Raw) == 0 && judgeRec.RunID != "" && judgeBackendFailure(c, judgeRec, judgeLog) {
			if _, err := os.Lstat(outputPath); os.IsNotExist(err) {
				l.countInfrastructureFailure(result, c.ItemID)
				if err := l.holdJudgeInfrastructure(ctx, c, receipt, judgeRec, judgeLog); err != nil {
					return c, false, err
				}
				return c, false, errJudgeBackendHeld
			}
		}
		if len(receipt.Raw) == 0 && judgeRec.State == run.StatePaused && judgeRec.EndCause == run.EndRestart && !judgeQuotaRefusal(judgeRec) {
			if _, err := os.Lstat(outputPath); os.IsNotExist(err) {
				if err := writeJudgeReceipt(receiptPath, receipt); err != nil {
					return c, false, err
				}
				return c, false, errJudgePaused
			}
		}
		if executionError != nil {
			return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, executionError.Error(), result)
		}
		workerHead := c.HeadSHA
		if c.WorkerHeadSHA != "" {
			workerHead = c.WorkerHeadSHA
		}
		if receipt.Options.Head != workerHead || receipt.Options.ItemID != c.ItemID {
			return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, "saved judgment belongs to a different candidate", result)
		}
		workerTree, err := l.revParse(ctx, c.Worktree, workerHead+"^{tree}")
		if err != nil {
			return c, false, err
		}
		if receipt.Options.Tree != workerTree {
			return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, "saved judgment belongs to a different candidate tree", result)
		}
		if len(receipt.Raw) == 0 {
			receipt.Raw, err = os.ReadFile(outputPath)
			if err != nil {
				_, entryErr := os.Lstat(outputPath)
				if c.Evaluator != nil && os.IsNotExist(err) && os.IsNotExist(entryErr) && judgeRec.State == run.StateExited && judgeRec.ExitCode != nil {
					detail := fmt.Sprintf("verify run %s exited with code %d and wrote no verdict", receipt.Request.RunID, *judgeRec.ExitCode)
					preflight := ClassifyAttempt(judgeRec, judgeLog) == AttemptRefused
					if preflight {
						words, _ := RecordAdapterRefusal(judgeRec, judgeLog)
						detail += ": " + words
					}
					c.RefusalDetail = detail
					return c, false, l.recordMissingVerify(ctx, c, receipt.Request.RunID, preflight)
				}
				if os.IsNotExist(err) && os.IsNotExist(entryErr) && ClassifyAttempt(judgeRec, judgeLog) == AttemptRefused {
					words, _ := RecordAdapterRefusal(judgeRec, judgeLog)
					faultCandidate := c
					faultCandidate.RunID = receipt.Request.RunID
					if err := l.holdItemFault(ctx, faultCandidate, ReasonAdapterRefused, words); err != nil {
						return c, false, err
					}
					return c, false, nil
				}
				return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, err.Error(), result)
			}
		}
		db, err = store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return c, false, err
		}
		var checked item.JudgeResult
		c.Judge = receipt
		options := receipt.Options
		options.At = l.cfg.Now()
		err = db.ReadTx(ctx, func(r *store.Reader) error {
			if err := bindSavedJudge(r, c, receipt, &options); err != nil {
				return err
			}
			var err error
			checked, err = item.CheckJudge(ctx, r, receipt.Raw, options)
			if err != nil {
				return err
			}
			return l.checkJudgeScope(c, checked.Output.Verdicts)
		})
		db.Close()
		if err == nil {
			receipt.Options.EvaluatorBinding = options.EvaluatorBinding
			if err := writeJudgeReceipt(receiptPath, receipt); err != nil {
				return c, false, err
			}
			if checked.Outcome != "pass" {
				// CheckJudge owns precedence. Select the first entry of that kind,
				// not the first failing entry, which may describe a lesser outcome.
				for _, entry := range checked.Output.Verdicts {
					if entry.Verdict == checked.Outcome {
						return c, false, l.settleJudgeFailure(ctx, c, entry, checked.Output.Verdicts, options, result)
					}
				}
				return c, false, Internalf("checked judge outcome %q has no matching entry", checked.Outcome)
			}
			c.Judge = receipt
			c.RebasedFromHead = ""
			return c, true, nil
		}
		if !isJudgeFormRejection(err) || seenErrors[err.Error()] {
			return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, err.Error(), result)
		}
		seenErrors[err.Error()] = true
		judgeSession := receipt.JudgeSession
		if judgeSession == "" {
			judgeSession = judgeSessionFromActor(receipt.Options.Actor)
		}
		if judgeSession == "" {
			return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, err.Error(), result)
		}
		l.reportf("loop: %s judge verdict refused (%s); resuming judge session %s\n", c.ItemID, err.Error(), receipt.Request.RunID)
		receipt.Raw = nil
		if err := l.stageJudge(ctx, c, receipt, judgeSession); err != nil {
			return c, false, err
		}
		resumed := receipt.Request
		resumed.Resume = true
		resumed.ResumePrompt = fmt.Sprintf(
			"Your verdict file %s was refused by the validator:\n\n%s\n\nFix the verdict file and rewrite %s, then return RESULT: done. Do not edit, stage, or commit anything in the worktree.",
			outputPath, err.Error(), outputPath,
		)
		resumedRequest = &resumed
	}
}

// defectiveCandidateEvidence finds a defective entry backed by the candidate rather
// than by a citation to the request. Check each entry separately: a premise citation
// for one criterion cannot justify another criterion's defective verdict.
func (l *Loop) defectiveCandidateEvidence(ctx context.Context, c Candidate, verdicts []item.JudgeEntry) (criterion, locator string, err error) {
	var changed map[string]bool
	for _, v := range verdicts {
		if v.Verdict != item.VerdictDefective {
			continue
		}
		premise := false
		for _, cite := range v.Evidence {
			premise = premise || cite == "request:words" || cite == "criterion:"+v.CriterionID
		}
		if premise {
			continue
		}
		for _, cite := range v.Evidence {
			if ref, ok := strings.CutPrefix(cite, "diff:"); ok {
				path, _, _ := strings.Cut(ref, ":")
				if filepath.Base(path) == "candidate.diff" {
					return v.CriterionID, cite, nil
				}
			}
		}
		if changed == nil {
			// Use the same base and judged head as the staged diff. Include both
			// sides of a rename and use NUL separators so Git does not quote paths.
			out, err := l.git(ctx, c.Worktree, "diff", "--name-only", "--no-renames", "-z", c.BaseSHA, c.Judge.Options.Head, "--")
			if err != nil {
				return "", "", err
			}
			changed = map[string]bool{}
			for _, path := range strings.Split(strings.TrimSuffix(out, "\x00"), "\x00") {
				if path != "" {
					changed[path] = true
				}
			}
		}
		for _, cite := range v.Evidence {
			path := strings.TrimPrefix(strings.TrimPrefix(cite, "file:"), "path:")
			path = strings.TrimPrefix(path, "./")
			name, _, _ := strings.Cut(path, ":") // also accept a line or line range
			if changed[path] || changed[name] {
				return v.CriterionID, cite, nil
			}
		}
	}
	return "", "", nil
}

// settleJudgeFailure routes a checked ruling without running the gate or landing.
// Defective intake and closure share a transaction: neither can survive alone.
func (l *Loop) settleJudgeFailure(ctx context.Context, c Candidate, entry item.JudgeEntry, verdicts []item.JudgeEntry, options item.JudgeOptions, result *LoopResult) error {
	if c.Evaluator != nil && entry.Verdict == item.VerdictNotDone {
		var findings []item.JudgeEntry
		for _, v := range verdicts {
			if v.Verdict == item.VerdictNotDone {
				findings = append(findings, v)
			}
		}
		c.verifyFeedback = l.checkedVerifyFeedback(ctx, c, options.Tree, verdicts)
		for _, v := range findings {
			// The verifier's words go to the builder as written. Only a reason that begins
			// plan-wrong: sends the item back to the plan step.
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(v.Reason)), PlanWrongPrefix) {
				c.VerifyReplan = true
			}
		}
		return l.refuse(ctx, c, result, RefusalVerifyNotDone)
	}
	reason := ReasonEvalFailed
	if entry.Verdict == item.VerdictNotDone {
		words, err := requestWordsFor(ctx, l.cfg.Store, c.ItemID)
		if err != nil {
			return err
		}
		var findings []item.JudgeEntry
		for _, v := range verdicts {
			if v.Verdict == item.VerdictNotDone {
				findings = append(findings, v)
			}
		}
		detail := entry.Reason
		if words != nil || len(findings) > 1 {
			sort.Slice(findings, func(i, j int) bool { return findings[i].CriterionID < findings[j].CriterionID })
			var lines []string
			for _, v := range findings {
				lines = append(lines, v.CriterionID+": "+v.Reason)
			}
			detail = strings.Join(lines, "\n")
		}
		if words != nil {
			writer := judgeWriterRun(c)
			var checks []EndingCheck
			for _, v := range findings {
				checks = append(checks, EndingCheck{CheckID: v.CriterionID, Digest: evidenceDigest(c.HeadSHA+"\n"+v.Reason, writer)})
			}
			c.RefusalDetail = detail
			c.EvidenceKey = endingKey(ReasonEvalFailed, checks, "", writer)
		}
		c.verifyFeedback = judgeRefusalFindings(findings)
		return l.holdJudge(ctx, c, c.Judge.Request.RunID, ReasonEvalFailed, detail, result)
	}
	if entry.Verdict == item.VerdictBlocked || entry.Verdict == item.VerdictDefective {
		var waiting item.WaitingOn
		if entry.Verdict == item.VerdictBlocked {
			if err := json.Unmarshal(entry.WaitingOn, &waiting); err != nil {
				return l.holdJudge(ctx, c, c.Judge.Request.RunID, ReasonJudgeUnreadable, "invalid blocked waiting_on: "+err.Error(), result)
			}
			waiting.Reason = entry.Reason
			reason = ReasonJudgeBlocked
		} else {
			reason = ReasonJudgeDefective
			criterion, cite, err := l.defectiveCandidateEvidence(ctx, c, verdicts)
			if err != nil {
				return err
			}
			if cite != "" {
				judge := c.Judge.Request.RunID
				detail := fmt.Sprintf("verifier run %s called %s defective but cited candidate evidence %q without citing a fault in the request; candidate faults are not-done", judge, criterion, cite)
				return l.holdJudge(ctx, c, judge, ReasonJudgeDefectiveMismatch, detail, result)
			}
		}
		db, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		closeRefused := false
		err = db.WriteTx(ctx, func(tx *store.Tx) error {
			if entry.Verdict == item.VerdictBlocked {
				_, err := item.SetState(ctx, tx, c.ItemID, StateBlocked, options.At, waiting)
				return err
			}
			if _, err := item.RecordJudge(ctx, tx, c.Judge.Raw, options); err != nil {
				return err
			}
			_, err := item.Close(ctx, tx, c.ItemID, "defective: "+entry.CriterionID+": "+entry.Reason, options.Actor, options.At)
			_, closeRefused = item.RejectionOf(err)
			return err
		})
		db.Close()
		if err != nil {
			if closeRefused {
				judge := c.Judge.Request.RunID
				detail := fmt.Sprintf("verifier run %s: close after defective verdict refused: %v", judge, err)
				return l.holdJudge(ctx, c, judge, ReasonJudgeCloseRefused, detail, result)
			}
			if _, rejected := item.RejectionOf(err); rejected {
				return l.holdJudge(ctx, c, c.Judge.Request.RunID, ReasonJudgeUnreadable, err.Error(), result)
			}
			return err
		}
	}
	if reason == ReasonJudgeBlocked {
		c.verifyFeedback = judgeRefusalFindings(verdicts)
	}
	return l.holdJudge(ctx, c, c.Judge.Request.RunID, reason, entry.Reason, result)
}

// Compatibility and blocked refusals carry classifications for display only.
// They do not participate in planned verify feedback, snapshots, or repeat detection.
// Defective dispositions record their findings via RecordJudge instead.
func judgeRefusalFindings(verdicts []item.JudgeEntry) *verifyFeedback {
	var records []verifyFindingRecord
	for _, v := range verdicts {
		if v.Verdict == item.VerdictNotDone && len(v.Findings) != 0 {
			records = append(records, verifyFindingRecord{Criterion: v.CriterionID, Reason: v.Reason, Findings: v.Findings})
		}
	}
	if len(records) == 0 {
		return nil
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Criterion < records[j].Criterion })
	return &verifyFeedback{Findings: records}
}

func writeJudgeReceipt(path string, receipt *JudgeReceipt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path+".new", raw, 0644); err != nil {
		return err
	}
	return os.Rename(path+".new", path)
}

// judgeRefusalHistory is item-wide. Only refusals backed by a not-done verdict
// are included; gate and verifier infrastructure failures are not findings.
func judgeRefusalHistory(r *store.Reader, itemID string) (string, error) {
	rows, err := r.Query(`WITH refusals AS (
 SELECT MIN(seq) AS first_seq FROM events
 WHERE kind=? AND object_type='run' AND json_valid(payload)
   AND json_extract(payload,'$.item_id')=? AND json_extract(payload,'$.step') IN (?,?)
   AND COALESCE(json_extract(payload,'$.detail'),'')<>''
 GROUP BY object_id
)
SELECT e.object_id, COALESCE((SELECT group_concat(json_extract(value,'$.criterion') || ': ' || json_extract(value,'$.reason'), char(10))
 FROM json_each(e.payload,'$.verify_feedback.findings') WHERE json_extract(e.payload,'$.step')=?), json_extract(e.payload,'$.detail'))
 FROM refusals f JOIN events e ON e.seq=f.first_seq ORDER BY f.first_seq`,
		RefusalKind, itemID, RefusalVerifyNotDone, ReasonEvalFailed, RefusalVerifyNotDone)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var history strings.Builder
	count := 0
	for rows.Next() {
		var runID, detail string
		if err := rows.Scan(&runID, &detail); err != nil {
			return "", err
		}
		// JSON quoting preserves complete multiline findings as data, not headings.
		// Escape brace pairs so a finding cannot look like an unfilled template to preflight.
		quoted, err := json.Marshal(detail)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&history, "- run %s: %s\n", runID, strings.ReplaceAll(string(quoted), "{{", `\u007b\u007b`))
		count++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count < 2 {
		return "", nil
	}
	return `
## Earlier verifier refusals

These are historical findings, not additional criteria. Recheck them against this
candidate and the scope above; do not carry forward fixed or out-of-scope findings.
Complete the review before refusing. State all remaining in-scope findings at once
in this verdict, including multiple findings under the same criterion.

Findings by run (JSON-quoted text):
` + history.String(), nil
}

func (l *Loop) stageJudge(ctx context.Context, c Candidate, receipt *JudgeReceipt, session string) error {
	dir := run.DurableDir(l.laneDirPath(c.LaneID), receipt.Request.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	diff, err := l.git(ctx, c.Worktree, "diff", c.BaseSHA, receipt.Options.Head)
	if err != nil {
		return err
	}
	diffPath := filepath.Join(dir, "candidate.diff")
	if err := os.WriteFile(diffPath, []byte(diff), 0644); err != nil {
		return err
	}
	changedPaths, err := l.git(ctx, c.Worktree, "diff", "--no-renames", "--name-only", c.BaseSHA, receipt.Options.Head, "--")
	if err != nil {
		return err
	}
	changedPaths = strings.TrimSpace(changedPaths)
	if changedPaths == "" {
		changedPaths = "(none)"
	}
	var criteria strings.Builder
	var history string
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, c.ItemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, text, class string
			if err := rows.Scan(&id, &text, &class); err != nil {
				return err
			}
			fmt.Fprintf(&criteria, "- %s: %s (claim_class=%s, criterion_hash=%s)\n", id, text, class, item.CriterionHash(text))
		}
		if err := rows.Err(); err != nil {
			return err
		}
		history, err = judgeRefusalHistory(r, c.ItemID)
		return err
	})
	db.Close()
	if err != nil {
		return err
	}
	output := filepath.Join(dir, "eval.output")
	candDir := filepath.Join(dir, "cand")
	brief := fmt.Sprintf(`# Judge candidate

## Header
Item: %s; run: %s; seat: review; target: %s

## Task
Act as an independent read-only judge. Do not commit, edit the worktree, or write the store.
Write your JSON verdict to %s. The object has evaluated_head=%q, evaluated_tree=%q,
outcome (pass or fail), and verdicts covering every criterion below. Each entry has
criterion_id, criterion_hash, verdict (done, not-done, blocked, defective), claim_class,
reason, and evidence (non-empty single-token locator strings with no spaces, e.g.
commit:<sha>, diff:candidate.diff, file:path/to/changed.go:42, node:<id>, or for your own
checks, cmd:go-test-tillr-run or test:TestExecRecordsQuickOutChoice; put full command
lines, arguments, and results in reason).

A verdict of defective means the request itself is wrong or impossible; it closes the item.
Example: the request requires incompatible outcomes for the same input.
Quote the faulty words in reason and cite request:words (saved request) or criterion:<id>
(the criterion judged). Candidate evidence alone cannot justify closing the item.
A fault in the candidate is not-done, not defective.
Example: the request is valid but the candidate drops input on error.

Each not-done entry must include a non-empty findings array, with one object for each
finding: kind (gap, regression, or unasked-change), case (one sentence), and paths
(file:<repo-relative-path> or file:<repo-relative-path>:<line> locators) identifying
the files that implement the case.
A gap means the named case behaves as on the base, without meeting the request and
without becoming worse. A regression means the named case behaves worse than on the
base, or something that passes on the base fails. An unasked-change changes something
no criterion needs.
Give evidence for each classification in reason and evidence; for a gap, compare the
named case with the base and show it behaves the same. If a finding cannot be classified,
report it as a regression.

A blocked entry also needs waiting_on
with waiter (owner, design, plan, or item:<id> for a stored item). Its reason explains
why that waiter must act. Do not include actor or judged_actor.
Pass requires every criterion done. Return RESULT: done after writing the file.

## Scope and boundaries
Do not modify the worktree, store, or refs. If a check writes files or needs a checkout
where Git commands work, create an independent local clone of evaluated_head under your
scratch directory (%s):
  git clone --shared --no-checkout . "%s" && git -C "%s" checkout --detach %s
Do not export GIT_DIR or GIT_WORK_TREE (for example around a git archive copy): child
processes such as git init inherit them and can write core.worktree into the trunk
repository's .git/config. The verdict file above is your only deliverable.

## Seats
These are seats tillr runs as nodes; a worker cannot start one.

%s

## Settled decisions
The item criteria are the bar; do not lower it.

## Facts carried
Read the exact candidate diff from this retained artifact:
%s

Changed paths (%s..%s):
%s

## Method
Name any changed path that the item's words do not account for.
Use the diff and candidate tree to judge the candidate commit. A refusal must rest on
the item's words or criteria or a regression this change introduces in existing behavior.
For each refusal, name the applicable words or criterion, or explain what worked before
and how the change breaks it.
Report defects outside that scope as proposed follow-ups, not refusals. In the relevant
criterion's reason, label them "Proposed follow-up:" and keep them separate from the
verdict justification; they do not change the verdict or outcome. If only the owner can
decide whether a finding is in scope, use a blocked verdict with waiter owner and state
the question and options.

Do not read or judge a worker's summary, report, or account of tests.
A finding must concern the change or the item, not the worker's account of either.
If an older standard criterion asks for a test inventory in a worker report, verify
that criterion's checks directly; do not assess the inventory.

## Criteria
%s

## Verification commands
Run the checks you need yourself against the candidate. For each check you use as
evidence, record your own command and result in the verdict.

## Report shape
Write the verdict file, then report RESULT: done.

## Stop conditions
Never change the worker's candidate.
`, c.ItemID, receipt.Request.RunID, c.Worktree, output, receipt.Options.Head, receipt.Options.Tree, candDir, candDir, candDir, receipt.Options.Head, seatLines(l.currentPolicy()), diffPath, c.BaseSHA, receipt.Options.Head, changedPaths, criteria.String())
	brief += history
	if len(receipt.Writers) == 0 {
		for _, producer := range receipt.Producers {
			brief += fmt.Sprintf("\nRead-only producer: node:%s\n", producer.NodeID)
		}
	}
	for _, writer := range receipt.Writers {
		brief += fmt.Sprintf("\nWriting node: node:%s\n", writer.NodeID)
	}
	for _, name := range sortedNames(receipt.Inputs) {
		brief += fmt.Sprintf("\nInput %s: %s\n", name, receipt.Inputs[name])
	}
	if receipt.EvaluatorTask != "" {
		brief += "\n## Evaluator task\n\n" + receipt.EvaluatorTask + "\n"
	}
	brief += fmt.Sprintf("\nEvaluator: run=%s; backend=%s; route=%s\n", receipt.Request.RunID, receipt.Request.Backend, receipt.Request.Route())
	if l.cfg.Store != "" {
		guidance, err := itemGuidanceBrief(ctx, l.cfg.Store, c.ItemID, c.Worktree)
		if err != nil {
			return err
		}
		brief += guidance
	}
	// A filed request's criteria judge the work against its words, so the judge reads them
	// too, in the same byte-counted suffix the worker's brief ends with.
	words, err := requestWordsFor(ctx, l.cfg.Store, c.ItemID)
	if err != nil {
		return err
	}
	if words != nil {
		brief += requestWordsSuffix(words)
	}
	braceCount := strconv.Itoa(braceLiteralCount(brief)) + "\n"
	if err := os.WriteFile(filepath.Join(dir, BriefFile), []byte(brief), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, braceLiteralsFile), []byte(braceCount), 0644); err != nil {
		return err
	}
	lane := l.laneDirPath(c.LaneID)
	if err := os.WriteFile(filepath.Join(lane, BriefFile), []byte(brief), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(lane, braceLiteralsFile), []byte(braceCount), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(lane, endingCapabilityFile), []byte(run.EndingFooter+"\n"), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(lane, SessionIDFile), []byte(session+"\n"), 0644)
}
func (l *Loop) restoreJudgeInputs(c Candidate, r *JudgeReceipt) error {
	lane := l.laneDirPath(c.LaneID)
	if err := os.WriteFile(filepath.Join(lane, BriefFile), r.WorkerBrief, 0644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(lane, braceLiteralsFile), []byte(strconv.Itoa(braceLiteralCount(string(r.WorkerBrief)))+"\n"), 0644); err != nil {
		return err
	}
	// An evaluator run's own staging says footer; the writer's capability is the one
	// a later resume of the writer must keep (a call-suspends writer ends by call).
	capability, err := stagedEndingCapability(RunDir(l.cfg.Root, judgeWriterRun(c)))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(lane, endingCapabilityFile), []byte(capability+"\n"), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(lane, SessionIDFile), r.WorkerSession, 0644)
}

func (l *Loop) judgeEvalOutputPath(laneID, runID string) string {
	return filepath.Join(run.DurableDir(l.laneDirPath(laneID), runID), "eval.output")
}

func judgeQuotaRefusal(rec run.Record) bool {
	_, _, _, refused := refusedWindow(rec)
	return rec.Backend != "command" && refused
}

// An exited workflow evaluator is judged from its verdict file, not its
// process footer. A failed start has no possible verdict and closes the backend.
func judgeBackendFailure(c Candidate, rec run.Record, log string) bool {
	return rec.Backend != "command" && (c.Evaluator == nil || rec.State == run.StateFailedToStart) && isBackendFailure(rec, log)
}

func (l *Loop) holdJudgeInfrastructure(ctx context.Context, c Candidate, receipt *JudgeReceipt, rec run.Record, log string) error {
	d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, Seat: "review", RunRequest: &receipt.Request}
	_, err := l.recordInfrastructureFailure(ctx, d, rec, log)
	return err
}

func (l *Loop) holdJudge(ctx context.Context, c Candidate, judgeID, reason, detail string, result *LoopResult) error {
	l.mu.Lock()
	result.Rejected++
	if result.RefusedSteps == nil {
		result.RefusedSteps = map[string]int{}
	}
	result.RefusedSteps[reason]++
	l.mu.Unlock()
	writer := judgeWriterRun(c)
	c.RunID = writer
	payload := struct {
		refusalPayload
		WriterRunID string `json:"writer_run_id"`
	}{refusalPayload: refusalPayload{ItemID: c.ItemID, LaneID: c.LaneID, TickID: c.TickID, Step: reason, Detail: detail, Evidence: c.EvidenceKey, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA}, WriterRunID: writer}
	if reason == ReasonEvalFailed || reason == ReasonJudgeBlocked {
		payload.VerifyFeedback = c.verifyFeedback
	}
	if reason == ReasonEvalFailed {
		var err error
		if c.EvidenceKey != "" {
			payload.WorkerSession, err = l.captureWorkerSession(ctx, c)
		} else {
			payload.WorkerSession, err = l.captureJudgeLandingSession(ctx, c)
		}
		if err != nil {
			return err
		}
	}
	repeated, err := l.landingRefusalRepeats(ctx, c, payload.refusalPayload)
	if err != nil {
		return err
	}
	at := l.cfg.Now()
	return appendJudgmentThen(ctx, l.cfg.Store, RefusalKind, "run", judgeID, payload, at, func(tx *store.Tx) error {
		if repeated {
			return nil
		}
		// A judge's refusal carries no checks, so it quotes no file.
		return l.answerLandingRefusal(tx, c, payload.refusalPayload, refusalEvidence{}, at)
	})
}
func judgeFailureDetail(r *store.Reader, id, reason string) (string, error) {
	rows, err := r.Query(`SELECT COALESCE(json_extract(payload,'$.detail'),'') FROM events
 WHERE kind='drain.refusal' AND json_valid(payload) AND json_extract(payload,'$.item_id')=?
 AND json_extract(payload,'$.step')=? ORDER BY seq DESC LIMIT 1`, id, reason)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		var detail string
		err := rows.Scan(&detail)
		return detail, err
	}
	return "", rows.Err()
}

func (l *Loop) recordLandedJudge(ctx context.Context, c Candidate) error {
	receipt := c.Judge
	db, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	options := receipt.Options
	options.At = l.cfg.Now()
	return db.WriteTx(ctx, func(tx *store.Tx) error {
		if err := bindSavedJudge(tx.Reader(), c, receipt, &options); err != nil {
			return err
		}
		if _, err := item.RecordJudge(ctx, tx, receipt.Raw, options); err != nil {
			return err
		}
		if _, err := item.SetState(ctx, tx, c.ItemID, StateLanded, l.cfg.Now()); err != nil {
			return err
		}
		return endItemUnfinishedRunsInTx(ctx, tx, c.ItemID, c.RunID, l.cfg.Now().UTC())
	})
}

// Reject the old report-only finding protocol and report citations as verifier-form
// errors. Recheck the same candidate, rather than treating an unchecked criterion
// as passed or sending the worker back to edit prose.
func (l *Loop) checkJudgeScope(c Candidate, entries []item.JudgeEntry) error {
	artifacts := map[string]bool{}
	runIDs := []string{c.RunID}
	if c.Judge != nil {
		for _, writer := range c.Judge.Writers {
			runIDs = append(runIDs, writer.RunID)
		}
		for _, producer := range c.Judge.Producers {
			runIDs = append(runIDs, producer.RunID)
		}
	}
	for _, runID := range runIDs {
		artifacts[filepath.Join(RunDir(l.cfg.Root, runID), "node-report.md")] = true
		scratch := run.DurableDir(l.laneDirPath(c.LaneID), runID)
		artifacts[filepath.Join(scratch, "report.md")] = true
		artifacts[filepath.Join(scratch, landingOfferFile)] = true
	}
	for _, entry := range entries {
		outside := strings.HasPrefix(strings.ToLower(strings.TrimSpace(entry.Reason)), "report-only:")
		for _, evidence := range entry.Evidence {
			ref := strings.TrimPrefix(strings.TrimPrefix(evidence, "file:"), "path:")
			path, _, _ := strings.Cut(ref, ":")
			path, _, _ = strings.Cut(path, "#")
			path = filepath.Clean(path)
			outside = outside || strings.HasPrefix(ref, "report:") || artifacts[path] ||
				(!filepath.IsAbs(path) && artifacts[filepath.Join(c.Worktree, path)])
		}
		if outside {
			return item.Rejectionf("judge-scope", c.ItemID, "criterion %s uses a worker summary or report outside the scope of judgment; verify the change against the item yourself", entry.CriterionID)
		}
	}
	return nil
}

func isJudgeFormRejection(err error) bool {
	rej, ok := item.RejectionOf(err)
	if !ok {
		return false
	}
	switch rej.Rule {
	case "judge-scope",
		"judge-output",
		"actor-in-output",
		"evaluated-head",
		"evaluated-tree",
		"judge-outcome",
		"criterion-unknown",
		"criterion-duplicate",
		"criterion-missing",
		"criterion-hash",
		"claim-class-mismatch",
		"verdict-value",
		"verdict-reason",
		"verdict-evidence",
		"outcome-pass":
		return true
	default:
		return false
	}
}

func (l *Loop) refreshJudgeRequest(req run.Request) run.Request {
	if fresh, ok := refreshRestartRequest(l.currentPolicy(), Decision{}, &req); ok {
		return *fresh
	}
	return req
}

func judgeSessionFromActor(actor string) string {
	for _, part := range strings.Split(actor, ";") {
		if after, ok := strings.CutPrefix(part, "session="); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}
