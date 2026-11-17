package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

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

func (l *Loop) checkLandingJudgeGrant(ctx context.Context, c Candidate) error {
	if c.Judge == nil {
		return nil
	}
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.ReadTx(ctx, func(r *store.Reader) error {
		grant, err := judgeGrant(r, c.ItemID, l.cfg.Now())
		if err != nil {
			return err
		}
		if grant == "" {
			return errJudgeGrantHeld
		}
		return nil
	})
}
