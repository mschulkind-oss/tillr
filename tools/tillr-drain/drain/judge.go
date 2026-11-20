package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/judgegrants"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonJudgeGrantAbsent = "judge-grant-absent"
	ReasonJudgeUnreadable  = "judge-unreadable"
	ReasonEvalFailed       = "eval-failed"
	ReasonJudgeBlocked     = "judge-blocked"
	ReasonJudgeDefective   = "judge-defective"
	judgeRequestKind       = "drain.judge.request"
	judgeReceiptFile       = "judge.json"
	judgeLandedKind        = "drain.judge.landed"
)

var errJudgeGrantHeld = errors.New("judge settlement waits for a covering grant")
var errJudgeBackendHeld = errors.New("judge settlement waits for an open review backend")

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
}

// judgeGrant selects one unexpired grant covering every stored criterion class.
// Separate grants cannot be combined: the intake takes one grant identity.
func judgeGrant(r *store.Reader, id string, at time.Time) (string, error) {
	rows, err := r.Query(`SELECT g.grant_id FROM item_grants g WHERE g.expires_at > ? AND g.issued_at <= ?
 AND EXISTS(SELECT 1 FROM item_criteria c WHERE c.item_id=?)
 AND NOT EXISTS(SELECT 1 FROM item_criteria c WHERE c.item_id=? AND
 (c.claim_class='owner-only' OR instr(',' || g.claim_classes || ',', ',' || c.claim_class || ',')=0))
 ORDER BY g.expires_at DESC,g.grant_id`, at.UnixMilli(), at.UnixMilli(), id, id)
	if err != nil {
		return "", readFailure(err)
	}
	defer rows.Close()
	if rows.Next() {
		var grant string
		if err := rows.Scan(&grant); err != nil {
			return "", err
		}
		return grant, nil
	}
	return "", rows.Err()
}

func (l *Loop) judgeCandidate(ctx context.Context, c Candidate, result *LoopResult) (Candidate, bool, error) {
	if pending, err := l.buildRestartPending(ctx); pending || err != nil {
		if err != nil {
			return c, false, err
		}
		return c, false, errRestartPending
	}
	var mode, grant string
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return c, false, err
	}
	var expiredGrant *judgegrants.Grant
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
		if mode == "agent" || c.Evaluator != nil {
			now := l.cfg.Now()
			grant, err = judgeGrant(r, c.ItemID, now)
			if err == nil && grant == "" {
				expiredGrant, err = judgegrants.ExpiredForItem(r, c.ItemID, now)
			}
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
	buildHash, err := l.build.Hash()
	if err != nil {
		return c, false, err
	}
	var replacementRequest *run.Request
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
		if evalMissing && rec != nil && ClassifyAttempt(*rec, logTail(rec.ArtifactRefs)) == AttemptInterrupted {
			if c.Evaluator == nil {
				shut, err := l.judgeQuotaShutBackends(ctx)
				if err != nil {
					return c, false, err
				}
				if shut[receipt.Request.Backend] {
					return c, false, errJudgeBackendHeld
				}
			}
			if err := l.restoreJudgeInputs(c, receipt); err != nil {
				return c, false, err
			}
			req := receipt.Request
			req.RunID = markRunID(req.RunID, "interrupted")
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
			interrupted := receipt.Build != buildHash && (rec == nil || rec.EndCause == run.EndSignal || rec.EndCause == run.EndLeaseReclaimed || rec.EndCause == run.EndRestart)
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
				replacementRequest = &req
				receipt = nil
			}
		}
	}
	seenErrors := map[string]bool{}
	var resumedRequest *run.Request
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
			if grant == "" {
				if c.Evaluator != nil {
					// The pending node retains its run id across regrant. A transient
					// grant hold must not consume that run's terminal refusal record.
					return c, false, errJudgeGrantHeld
				}
				detail, _ := judgegrants.FormatHold(expiredGrant, l.cfg.Store)
				return c, false, l.holdJudge(ctx, c, c.RunID+"-judge", ReasonJudgeGrantAbsent, detail, result)
			}
			req := c.Evaluator
			fromReplacement := replacementRequest != nil
			if fromReplacement {
				req = replacementRequest
				replacementRequest = nil
			}
			if req == nil {
				seat := l.cfg.Policy.seatList("review")
				if len(seat) == 0 {
					return c, false, Usagef("agent judge requires the review seat")
				}
				req = l.cfg.Policy.request(c.ItemID, c.LaneID, c.RunID+"-judge", seat[0])
				if req != nil {
					req.Seat = "review"
					req.Workflow = c.Workflow
					if req.Workflow == "" && c.Request != nil {
						req.Workflow = c.Request.Workflow
					}
					if req.Workflow == "" {
						req.Workflow = singleWorkerWorkflow
					}
					req.Node = "judge"
				}
			}
			if req == nil {
				return c, false, Usagef("cannot build review request")
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
			judgeSession, err := NewSessionID()
			if err != nil {
				return c, false, err
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
			receipt = &JudgeReceipt{Build: buildHash, Request: *req, JudgeSession: judgeSession, WorkerBrief: brief, WorkerSession: session, Options: item.JudgeOptions{ItemID: c.ItemID, Head: head, Tree: tree, Actor: "run=" + req.RunID + ";session=" + judgeSession + ";route=" + req.Route(), JudgedActor: "run=" + c.RunID + ";session=" + strings.TrimSpace(string(session)), GrantID: grant, At: l.cfg.Now()}}
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
					shut, err := l.judgeQuotaShutBackends(ctx)
					if err != nil {
						return c, false, err
					}
					if shut[receipt.Request.Backend] {
						return c, false, errJudgeBackendHeld
					}
				}
				req := receipt.Request
				req.RunID = markRunID(req.RunID, "interrupted")
				replacementRequest = &req
				receipt = nil
				continue
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
		if len(receipt.Raw) == 0 {
			receipt.Raw, err = os.ReadFile(outputPath)
			if err != nil {
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
			grant, e := judgeGrant(r, c.ItemID, options.At)
			if e != nil {
				return e
			}
			if grant == "" {
				return errJudgeGrantHeld
			}
			options.GrantID, receipt.Options.GrantID = grant, grant
			checked, e = item.CheckJudge(ctx, r, receipt.Raw, options)
			return e
		})
		db.Close()
		if errors.Is(err, errJudgeGrantHeld) {
			return c, false, err
		}
		if err == nil {
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

// verifyFindingDigest is the digest of one not-done finding as a worker would act on it:
// the candidate it judged and the finding's text. The candidate is hashed beside the
// normalized text, not inside it, because normalizeEvidence strips full commit hashes.
// Two refusals that share a criterion id but differ in candidate or finding are not an
// identical repeat.
func verifyFindingDigest(head, reason, runID string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(head+"\n"+normalizeEvidence(reason, runID))))
}

// verifyNotDoneEvidence is a verify-not-done refusal's detail and evidence key: one line
// per not-done criterion, and a key of each criterion id with the digest of its candidate
// and finding text.
func verifyNotDoneEvidence(head, runID string, findings []item.JudgeEntry) (string, string) {
	sort.Slice(findings, func(i, j int) bool { return findings[i].CriterionID < findings[j].CriterionID })
	var lines []string
	var checks []EndingCheck
	for _, v := range findings {
		lines = append(lines, v.CriterionID+": "+v.Reason)
		checks = append(checks, EndingCheck{CheckID: v.CriterionID, Digest: verifyFindingDigest(head, v.Reason, runID)})
	}
	return strings.Join(lines, "\n"), endingKey(RefusalVerifyNotDone, checks, "", runID)
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
		c.RefusalDetail, c.EvidenceKey = verifyNotDoneEvidence(c.HeadSHA, c.RunID, findings)
		return l.refuse(ctx, c, result, RefusalVerifyNotDone)
	}
	reason := ReasonEvalFailed
	if entry.Verdict == item.VerdictNotDone {
		words, err := requestWordsFor(ctx, l.cfg.Store, c.ItemID)
		if err != nil {
			return err
		}
		if words != nil {
			writer := judgeWriterRun(c)
			var findings []item.JudgeEntry
			for _, v := range verdicts {
				if v.Verdict == item.VerdictNotDone {
					findings = append(findings, v)
				}
			}
			sort.Slice(findings, func(i, j int) bool { return findings[i].CriterionID < findings[j].CriterionID })
			var lines []string
			var checks []EndingCheck
			for _, v := range findings {
				lines = append(lines, v.CriterionID+": "+v.Reason)
				checks = append(checks, EndingCheck{CheckID: v.CriterionID, Digest: evidenceDigest(c.HeadSHA+"\n"+v.Reason, writer)})
			}
			c.RefusalDetail = strings.Join(lines, "\n")
			c.EvidenceKey = endingKey(ReasonEvalFailed, checks, "", writer)
			return l.holdJudge(ctx, c, c.Judge.Request.RunID, ReasonEvalFailed, c.RefusalDetail, result)
		}
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
		}
		db, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		err = db.WriteTx(ctx, func(tx *store.Tx) error {
			if entry.Verdict == item.VerdictBlocked {
				_, err := item.SetState(ctx, tx, c.ItemID, StateBlocked, options.At, waiting)
				return err
			}
			if _, err := item.RecordJudge(ctx, tx, c.Judge.Raw, options); err != nil {
				return err
			}
			_, err := item.Close(ctx, tx, c.ItemID, "defective: "+entry.CriterionID+": "+entry.Reason, options.Actor, options.At)
			return err
		})
		db.Close()
		if err != nil {
			if _, rejected := item.RejectionOf(err); rejected {
				return l.holdJudge(ctx, c, c.Judge.Request.RunID, ReasonJudgeUnreadable, err.Error(), result)
			}
			return err
		}
	}
	return l.holdJudge(ctx, c, c.Judge.Request.RunID, reason, entry.Reason, result)
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
	var criteria strings.Builder
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
		return rows.Err()
	})
	db.Close()
	if err != nil {
		return err
	}
	output := filepath.Join(dir, "eval.output")
	brief := fmt.Sprintf(`# Judge candidate

## Header
Item: %s; run: %s; seat: review; target: %s

## Task
Act as an independent read-only judge. Do not commit, edit the worktree, or write the store.
Write your JSON verdict to %s. The object has evaluated_head=%q, evaluated_tree=%q,
outcome (pass or fail), and verdicts covering every criterion below. Each entry has
criterion_id, criterion_hash, verdict (done, not-done, blocked, defective), claim_class,
reason, and evidence (non-empty single-token locator strings with no spaces, e.g.
commit:<sha>, diff:candidate.diff, node:<id>, or for a command that was run,
cmd:go-test-tillr-run or test:TestExecRecordsQuickOutChoice; put full command lines,
arguments, and results in reason). A blocked entry also needs waiting_on
with waiter (owner, design, plan, or item:<id> for a stored item). Its reason explains
why that waiter must act. Do not include actor or judged_actor.
Pass requires every criterion done. Return RESULT: done after writing the file.

## Fence
No worktree writes. Only the verdict file named above may be written.

## Seats
These are seats tillr runs as nodes; a worker cannot start one.

%s

## Settled decisions
The item criteria are the bar; do not lower it.

## Facts carried
Read the exact candidate diff from this retained artifact:
%s

## Method
Review the diff and candidate tree against every criterion.

## Verification commands
%s

## Report shape
Write the verdict file, then report RESULT: done.

## Stop conditions
Never change the worker's candidate.
`, c.ItemID, receipt.Request.RunID, c.Worktree, output, receipt.Options.Head, receipt.Options.Tree, seatLines(l.currentPolicy()), diffPath, criteria.String())
	if len(receipt.Writers) == 0 {
		for _, producer := range receipt.Producers {
			brief += fmt.Sprintf("\nRead-only producer %s: run=%s; session=%s; backend=%s; route=%s\n", producer.NodeID, producer.RunID, producer.SessionID, producer.Backend, producer.Route)
		}
	}
	for _, writer := range receipt.Writers {
		brief += fmt.Sprintf("\nWriting node %s: run=%s; session=%s; backend=%s; route=%s\n", writer.NodeID, writer.RunID, writer.SessionID, writer.Backend, writer.Route)
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
	if err := os.WriteFile(filepath.Join(dir, BriefFile), []byte(brief), 0644); err != nil {
		return err
	}
	lane := l.laneDirPath(c.LaneID)
	if err := os.WriteFile(filepath.Join(lane, BriefFile), []byte(brief), 0644); err != nil {
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
	capability, err := stagedEndingCapability(RunDir(l.cfg.Root, c.RunID))
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

func (l *Loop) judgeQuotaShutBackends(ctx context.Context) (map[string]bool, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var shut map[string]bool
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		var e error
		shut, e = readQuotaShutBackends(r, l.cfg.Now(), l.currentPolicy())
		return e
	})
	return shut, err
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
	}{refusalPayload: refusalPayload{ItemID: c.ItemID, LaneID: c.LaneID, TickID: c.TickID, Step: reason, Detail: detail, Evidence: c.EvidenceKey}, WriterRunID: writer}
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
		grant, err := judgeGrant(tx.Reader(), c.ItemID, options.At)
		if err != nil {
			return err
		}
		if grant == "" {
			return errJudgeGrantHeld
		}
		options.GrantID = grant
		if _, err := item.RecordJudge(ctx, tx, receipt.Raw, options); err != nil {
			return err
		}
		if _, err := item.SetState(ctx, tx, c.ItemID, StateLanded, l.cfg.Now()); err != nil {
			return err
		}
		return endItemUnfinishedRunsInTx(ctx, tx, c.ItemID, c.RunID, l.cfg.Now().UTC())
	})
}

func isJudgeFormRejection(err error) bool {
	rej, ok := item.RejectionOf(err)
	if !ok {
		return false
	}
	switch rej.Rule {
	case "judge-output",
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

func judgeSessionFromActor(actor string) string {
	for _, part := range strings.Split(actor, ";") {
		if after, ok := strings.CutPrefix(part, "session="); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}
