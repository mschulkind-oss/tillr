package drain

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const remoteSyncReplayKind = "drain.remote-sync-replay"

// The exact replay tip, not merely the fetched base, proves the reset occurred.
type remoteSyncReplay struct {
	Candidate Candidate
	Rewrite   rewriteAbsorption
	Tree      string
	Verdict   land.Verdict
	Stage     remoteSyncState
}

func (l *Loop) writeRemoteSyncReplay(ctx context.Context, intent *remoteSyncReplay) error {
	return l.writeRemoteSync(ctx, func(tx *store.Tx) error {
		return appendEffectOn(tx, remoteSyncReplayKind, "trunk", TrunkRef, intent, l.cfg.Now().UTC())
	})
}

func (l *Loop) readRemoteSyncReplay(ctx context.Context) (*remoteSyncReplay, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var intent *remoteSyncReplay
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT COALESCE((SELECT payload FROM events WHERE kind=? AND object_type='trunk' AND object_id=? ORDER BY seq DESC LIMIT 1), 'null')`, remoteSyncReplayKind, TrunkRef).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &intent)
	})
	return intent, err
}

func (l *Loop) recoverRemoteSyncReplay(ctx context.Context) (*remoteSyncReplay, error) {
	if l.nonLanding || l.cfg.Store == "" {
		return nil, nil
	}
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return l.recoverRemoteSyncReplayLocked(ctx)
}

func (l *Loop) recoverRemoteSyncReplayLocked(ctx context.Context) (*remoteSyncReplay, error) {
	intent, err := l.readRemoteSyncReplay(ctx)
	if err != nil || intent == nil {
		return nil, err
	}
	if applied, err := trunkAncestor(ctx, l.cfg.Root, intent.Rewrite.Tip, "refs/heads/"+TrunkRef); err != nil {
		return nil, err
	} else if !applied {
		return nil, l.writeRemoteSyncReplay(ctx, nil)
	}
	return intent, l.finishRemoteSyncReplayLocked(ctx, intent)
}

// Standalone push has no loop adapters. Use the item writer with the saved gate evidence.
func (l *Loop) completeRemoteSyncWithoutSteps(ctx context.Context, c Candidate) error {
	return l.writeRemoteSync(ctx, func(tx *store.Tx) error {
		var text string
		if err := tx.QueryRow(`SELECT text FROM item_criteria WHERE item_id=? AND criterion_id=?`, c.ItemID, RemoteSyncCriterion).Scan(&text); err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{"item_id": c.ItemID, "criterion_id": RemoteSyncCriterion,
			"criterion_hash": item.CriterionHash(text), "verdict": "done", "claim_class": item.ClaimVerification,
			"evidence":       []string{filepath.Join(RunDir(l.cfg.Root, c.RunID), GateVerdictRecord), "run:" + c.RunID, "head:" + c.HeadSHA},
			"evaluated_head": c.HeadSHA, "actor_kind": item.ActorMechanical})
		if err != nil {
			return err
		}
		at := l.cfg.Now().UTC()
		if _, err := item.RecordVerdict(ctx, tx, record.Envelope{Kind: item.VerdictRecordKind, CreatedAt: at, Payload: payload}); err != nil {
			return err
		}
		if _, err := item.SetState(ctx, tx, c.ItemID, StateLanded, at); err != nil {
			return err
		}
		return endItemUnfinishedRunsInTx(ctx, tx, c.ItemID, c.RunID, at)
	})
}

func (r *remoteSyncReplay) publication() healResult {
	return healResult{replayed: len(r.Rewrite.Order), remoteTip: r.Stage.RemoteTip, base: r.Stage.MergeBase, newTip: r.Rewrite.Tip}
}
