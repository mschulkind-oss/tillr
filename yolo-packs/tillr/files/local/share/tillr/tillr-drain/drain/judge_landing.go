package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// A handed-off judge may outlive the supervisor and mutate its checkout before
// terminating. Recover that run before generic worker-head or retirement guards.
// A completed checked receipt instead follows the ordinary prepared-head guards.
func (l *Loop) recoverJudgeHandoff(ctx context.Context, c Candidate, result *LoopResult) (Candidate, bool, error) {
	raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, c.RunID), judgeReceiptFile))
	if os.IsNotExist(err) {
		return c, true, nil
	}
	if err != nil {
		return c, false, err
	}
	var receipt JudgeReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return c, false, err
	}
	if len(receipt.Raw) != 0 {
		return c, true, nil
	}
	return l.judgeCandidate(ctx, c, result)
}

// checkJudgeForLanding also covers gated work resumed across the migration.
// Adopt legacy receipts in place, validating their saved verdict bytes without
// rerunning the evaluator or the gate.
func (l *Loop) checkJudgeForLanding(ctx context.Context, c Candidate) (*JudgeReceipt, error) {
	receipt := *c.Judge
	head := c.HeadSHA
	if c.WorkerHeadSHA != "" {
		head = c.WorkerHeadSHA
	}
	tree, err := l.revParse(ctx, c.Worktree, head+"^{tree}")
	if err != nil {
		return nil, err
	}
	if receipt.Options.ItemID != c.ItemID || receipt.Options.Head != head || receipt.Options.Tree != tree {
		return nil, item.Rejectionf("evaluator-candidate", c.ItemID, "saved judgment belongs to a different candidate")
	}
	err = l.readStore(ctx, func(r *store.Reader) error {
		if receipt.Options.EvaluatorBinding == (item.EvaluatorBinding{}) {
			if err := bindSavedJudge(r, c, c.Judge, &receipt.Options); err != nil {
				return err
			}
			checked, err := item.CheckJudge(ctx, r, receipt.Raw, receipt.Options)
			if err != nil {
				return err
			}
			if checked.Outcome != "pass" {
				return item.Rejectionf("evaluator-outcome", c.ItemID, "saved judgment did not pass")
			}
			return nil
		}
		binding, err := item.ReadEvaluatorBinding(r, c.ItemID)
		if err == nil && binding != receipt.Options.EvaluatorBinding {
			return item.Rejectionf("evaluator-binding", c.ItemID, "saved judgment does not match workflow %q evaluator %q", binding.Workflow, binding.EvaluatorNode)
		}
		return err
	})
	return &receipt, err
}

// The successful landing's exact commit is retained before retirement intake.
// Recovery settles that publication only; it never judges, gates or lands again.
func (l *Loop) recoverJudgePublication(ctx context.Context, c Candidate, result *LoopResult) (bool, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	var raw []byte
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, judgeLandedKind, c.RunID)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return rows.Scan(&raw)
		}
		return rows.Err()
	})
	db.Close()
	if err != nil || raw == nil {
		return false, err
	}
	var published Candidate
	if err := json.Unmarshal(raw, &published); err != nil {
		return true, err
	}
	if published.ItemID != c.ItemID || published.Judge == nil || published.Judge.Options.Landed == "" {
		return true, Internalf("invalid saved judge publication for %s", c.RunID)
	}
	if err := l.recordLandedJudge(ctx, published); err != nil {
		return true, err
	}
	l.mu.Lock()
	result.Landed++
	l.mu.Unlock()
	if err := l.bumpLandings(ctx); err != nil {
		l.reportf("loop: settled landed %s but could not record the landing count: %v\n", c.ItemID, err)
	}
	return true, nil
}
