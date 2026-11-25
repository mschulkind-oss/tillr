package drain

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// GeneratedBlockKind records the obstacle a generated repair item's blocked report was
// sent back to its worker with: the trunk the run was given and the obstacle kind its
// report named. It is the evidence key the identical-repeat rule (repeatedEnding) compares,
// so a repair that ends blocked the same way twice reaches the owner instead of its worker
// a third time. The key is the obstacle, not the prose: on 2026-11-18 a stale trunk fixer
// was sent back seven times with seven differently worded reports of one fact.
const GeneratedBlockKind = "drain.generated-block"

func init() {
	if err := event.Register(GeneratedBlockKind, Utility); err != nil {
		panic(err)
	}
}

type generatedBlock struct {
	ItemID string `json:"item_id"`
	RunID  string `json:"run_id"`
	Key    string `json:"key"`
}

// generatedBlockKey is the obstacle a blocked report stands for: the trunk tip it was
// reported against and the obstacle kind it named ("none" for prose). A changed trunk is
// a changed obstacle, so a repair blocked again after the trunk moved is not a repeat.
// An unreadable trunk keys nothing, and so repeats nothing.
func (l *Loop) generatedBlockKey(ctx context.Context, o Obstacle) string {
	tip, err := l.git(ctx, l.cfg.Root, "rev-parse", l.trunkRef()+"^{commit}")
	tip = strings.TrimSpace(tip)
	if err != nil || tip == "" {
		return ""
	}
	kind := o.Kind
	if kind == "" {
		kind = "none"
	}
	return "blocked:trunk=" + tip + ",obstacle=" + kind
}

// generatedBlockRepeat records this run's blocked ending and answers the earlier run of
// the same item whose ending it repeats, or "" when none does.
func (l *Loop) generatedBlockRepeat(ctx context.Context, itemID, runID string, o Obstacle) (string, string, error) {
	key := l.generatedBlockKey(ctx, o)
	if key == "" || runID == "" {
		return "", key, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", key, readFailure(err)
	}
	chain := []chainEnding{}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind=? ORDER BY seq`, GeneratedBlockKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var b generatedBlock
			if json.Unmarshal(raw, &b) == nil && b.ItemID == itemID {
				chain = append(chain, chainEnding{RunID: b.RunID, Producer: itemID, Cause: "blocked", Key: b.Key})
			}
		}
		return rows.Err()
	})
	s.Close()
	if err != nil {
		return "", key, err
	}
	if err := appendJudgment(ctx, l.cfg.Store, GeneratedBlockKind, "run", runID, generatedBlock{ItemID: itemID, RunID: runID, Key: key}, l.cfg.Now().UTC()); err != nil {
		return "", key, err
	}
	earlier, _ := repeatedEnding(chain, chainEnding{RunID: runID, Producer: itemID, Cause: "blocked", Key: key})
	return earlier, key, nil
}

// generatedRepeatQuestion is the loop's own objection for a repeated blocked ending.
func generatedRepeatQuestion(itemID, earlier, latest, key string) string {
	return "Question for the owner: generated item " + itemID + " ended blocked in run " + latest + " exactly as run " + earlier + " did (" + key + "), with no commit and no new obstacle, so its worker would only be sent the same tree again. Their reports are above. Should the item be closed (for a trunk fixer, resolved by a heavy pass of its check), its criterion changed, or its worker given something new with `tillr drain retry --run " + latest + "`?"
}
