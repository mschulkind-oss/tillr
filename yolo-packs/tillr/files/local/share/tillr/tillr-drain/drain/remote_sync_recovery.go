package drain

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const remoteSyncReplayKind = remotesync.RemoteSyncReplayKind

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
		return remotesync.WriteRemoteSyncReplay(tx, l.trunkRef(), Utility, intent, l.cfg.Now().UTC())
	})
}

func (l *Loop) readRemoteSyncReplay(ctx context.Context) (*remoteSyncReplay, error) {
	raw, err := remotesync.ReadRemoteSyncReplayJSON(ctx, l.cfg.Store, l.trunkRef())
	if err != nil {
		return nil, err
	}
	var intent *remoteSyncReplay
	if err := json.Unmarshal(raw, &intent); err != nil {
		return nil, err
	}
	return intent, nil
}

// A pass with no interrupted replay to recover does not need land.lock. A
// writer of a new intent already owns recovery until it drops that lock; the
// next pass observes any intent it left. If there is an intent, reread under
// the lock before applying it, in case its owner finished in the meantime.
func (l *Loop) recoverPendingRemoteSyncReplay(ctx context.Context) (*remoteSyncReplay, error) {
	if l.nonLanding || l.cfg.Store == "" {
		return nil, nil
	}
	if intent, err := l.readRemoteSyncReplay(ctx); err != nil || intent == nil {
		return nil, err
	}
	return l.recoverRemoteSyncReplay(ctx)
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
	if applied, err := trunkAncestor(ctx, l.cfg.Root, intent.Rewrite.Tip, "refs/heads/"+l.trunkRef()); err != nil {
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
	p := remotesync.PublicationFor(len(r.Rewrite.Order), r.Stage.RemoteTip, r.Stage.MergeBase, r.Rewrite.Tip)
	return healResult{replayed: p.Replayed, remoteTip: p.RemoteTip, base: p.Base, newTip: p.NewTip}
}
