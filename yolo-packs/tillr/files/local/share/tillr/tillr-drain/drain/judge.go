package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// JudgeReceipt retains the caller-owned identities and the exact checked bytes.
// It travels with the gated candidate so a retained landing needs no second judge.
type JudgeReceipt struct {
	Options       item.JudgeOptions `json:"options"`
	Raw           json.RawMessage   `json:"raw,omitempty"`
	Request       run.Request       `json:"request"`
	WorkerBrief   []byte            `json:"worker_brief"`
	WorkerSession []byte            `json:"worker_session"`
	Writers       []JudgeWriter     `json:"writers,omitempty"`
	Producers     []JudgeWriter     `json:"producers,omitempty"`
	Inputs        map[string]string `json:"inputs,omitempty"`
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
	var mode, grant string
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
		if mode == "agent" || c.Evaluator != nil {
			grant, err = judgeGrant(r, c.ItemID, l.cfg.Now())
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
	var executionError error
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
	if receipt == nil {
		if grant == "" {
			if c.Evaluator != nil {
				// The pending node retains its run id across regrant. A transient
				// grant hold must not consume that run's terminal refusal record.
				return c, false, errJudgeGrantHeld
			}
			return c, false, l.holdJudge(ctx, c, c.RunID+"-judge", ReasonJudgeGrantAbsent, "no unexpired grant covers the item's criteria", result)
		}
		req := c.Evaluator
		if req == nil {
			seat := l.cfg.Policy.seatList("review")
			if len(seat) == 0 {
				return c, false, Usagef("agent judge requires the review seat")
			}
			req = l.cfg.Policy.request(c.ItemID, c.LaneID, c.RunID+"-judge", seat[0])
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
		receipt = &JudgeReceipt{Request: *req, WorkerBrief: brief, WorkerSession: session, Options: item.JudgeOptions{ItemID: c.ItemID, Head: head, Tree: tree, Actor: "run=" + req.RunID + ";session=" + judgeSession + ";route=" + req.Route(), JudgedActor: "run=" + c.RunID + ";session=" + strings.TrimSpace(string(session)), GrantID: grant, At: l.cfg.Now()}}
		receipt.Writers, receipt.Inputs, receipt.Producers = c.Writers, c.EvaluatorInputs, c.Producers
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
		rec, _, runErr := l.cfg.Steps.Run(ctx, *req)
		if runErr != nil {
			if ctx.Err() != nil {
				return c, false, ctx.Err()
			}
			executionError = runErr
		}
		l.countSpend(result, rec)
	} else if len(receipt.Raw) == 0 {
		// The request was handed off before a restart. Do not start it a second time.
		// A live face owns its lifetime; wait for its terminal record before restoring
		// the lane inputs or inspecting its worktree.
		for {
			db, err := store.OpenRead(ctx, l.cfg.Store)
			if err != nil {
				return c, false, err
			}
			var rec *run.Record
			err = db.ReadTx(ctx, func(r *store.Reader) error { var e error; rec, e = terminalRecord(r, receipt.Request.RunID); return e })
			db.Close()
			if err != nil {
				return c, false, err
			}
			if rec != nil && rec.State != "running" {
				l.countSpend(result, *rec)
				break
			}
			var active int
			db, err = store.OpenRead(ctx, l.cfg.Store)
			if err != nil {
				return c, false, err
			}
			err = db.ReadTx(ctx, func(r *store.Reader) error {
				return r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id=? AND state='running'`, receipt.Request.RunID).Scan(&active)
			})
			db.Close()
			if err != nil {
				return c, false, err
			}
			if active == 0 {
				// A face may have started but not yet committed run.allocate.
				// Never restore its inputs while that handoff is still live.
				here, err := currentLoopOwner()
				if err != nil {
					return c, false, err
				}
				marked, err := liveRunWorkers(l.cfg.Store, receipt.Request.RunID, here)
				if err != nil {
					return c, false, err
				}
				live := false
				for _, process := range marked {
					face, err := processIsRunFace(process.PID)
					if err != nil {
						return c, false, err
					}
					live = live || face
				}
				if !live {
					executionError = Externalf("judge handoff has no terminal record")
					break
				}
			}
			if !waitFor(ctx, 100*time.Millisecond) {
				return c, false, ctx.Err()
			}
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
		receipt.Raw, err = os.ReadFile(filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), receipt.Request.RunID), "eval.output"))
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
	if err != nil {
		return c, false, l.holdJudge(ctx, c, receipt.Request.RunID, ReasonJudgeUnreadable, err.Error(), result)
	}
	if err := writeJudgeReceipt(receiptPath, receipt); err != nil {
		return c, false, err
	}
	if checked.Outcome != "pass" {
		// CheckJudge owns precedence. Select the first entry of that kind,
		// not the first failing entry, which may describe a lesser outcome.
		for _, entry := range checked.Output.Verdicts {
			if entry.Verdict == checked.Outcome {
				return c, false, l.settleJudgeFailure(ctx, c, entry, options, result)
			}
		}
		return c, false, Internalf("checked judge outcome %q has no matching entry", checked.Outcome)
	}
	c.Judge = receipt
	return c, true, nil
}

// settleJudgeFailure routes a checked ruling without running the gate or landing.
// Defective intake and closure share a transaction: neither can survive alone.
func (l *Loop) settleJudgeFailure(ctx context.Context, c Candidate, entry item.JudgeEntry, options item.JudgeOptions, result *LoopResult) error {
	reason := ReasonEvalFailed
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
reason, and evidence (nonempty locator strings). A blocked entry also needs waiting_on
with waiter (owner, design, plan, or item:<id> for a stored item). Its reason explains
why that waiter must act. Do not include actor or judged_actor.
Pass requires every criterion done. Return RESULT: done after writing the file.

## Fence
No worktree writes. Only the verdict file named above may be written.

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
`, c.ItemID, receipt.Request.RunID, c.Worktree, output, receipt.Options.Head, receipt.Options.Tree, diffPath, criteria.String())
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
	brief += fmt.Sprintf("\nEvaluator: run=%s; backend=%s; route=%s\n", receipt.Request.RunID, receipt.Request.Backend, receipt.Request.Route())
	if err := os.WriteFile(filepath.Join(dir, BriefFile), []byte(brief), 0644); err != nil {
		return err
	}
	lane := l.laneDirPath(c.LaneID)
	if err := os.WriteFile(filepath.Join(lane, BriefFile), []byte(brief), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(lane, SessionIDFile), []byte(session+"\n"), 0644)
}
func (l *Loop) restoreJudgeInputs(c Candidate, r *JudgeReceipt) error {
	lane := l.laneDirPath(c.LaneID)
	if err := os.WriteFile(filepath.Join(lane, BriefFile), r.WorkerBrief, 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(lane, SessionIDFile), r.WorkerSession, 0644)
}
func (l *Loop) holdJudge(ctx context.Context, c Candidate, judgeID, reason, detail string, result *LoopResult) error {
	l.mu.Lock()
	result.Rejected++
	if result.RefusedSteps == nil {
		result.RefusedSteps = map[string]int{}
	}
	result.RefusedSteps[reason]++
	l.mu.Unlock()
	return appendJudgment(ctx, l.cfg.Store, RefusalKind, "run", judgeID, map[string]any{"item_id": c.ItemID, "lane_id": c.LaneID, "tick_id": c.TickID, "writer_run_id": judgeWriterRun(c), "step": reason, "detail": detail}, l.cfg.Now())
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
		_, err = item.SetState(ctx, tx, c.ItemID, StateLanded, l.cfg.Now())
		return err
	})
}
