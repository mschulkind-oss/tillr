package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const pushKindConflictCleared = "remote-sync-cleared"

type openRemoteSyncStage struct {
	stage remoteSyncState
	state string
}

func openRemoteSyncStages(r *store.Reader) ([]openRemoteSyncStage, error) {
	declared, err := definition.ForReader(r)
	if err != nil {
		return nil, err
	}
	rows, err := r.Query(`SELECT i.state, e.payload FROM items i JOIN events e ON e.seq = (
 SELECT newer.seq FROM events newer WHERE newer.kind=? AND newer.object_type='run' AND newer.object_id=i.item_id
 ORDER BY newer.seq DESC LIMIT 1) WHERE i.item_id LIKE ? ORDER BY e.seq DESC`, remoteSyncStagedKind, RemoteSyncItemPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var open []openRemoteSyncStage
	for rows.Next() {
		var st openRemoteSyncStage
		var raw []byte
		if err := rows.Scan(&st.state, &raw); err != nil {
			return nil, err
		}
		if finishedState(declared.Parsed, st.state) {
			continue
		}
		if err := json.Unmarshal(raw, &st.stage); err != nil {
			return nil, err
		}
		open = append(open, st)
	}
	return open, rows.Err()
}

// activeRemoteSyncConflict reads the newest open, pinned repair for this destination,
// regardless of the latest attempt's result or whether the remote is reachable.
func (l *Loop) activeRemoteSyncConflict(ctx context.Context) (conflict *pushStateError, err error) {
	if l.cfg.Store == "" {
		return nil, nil
	}
	remote, ref, err := pushDestination(ctx, l.cfg.Root, l.cfg.PostLanding.Push)
	if err != nil {
		return nil, nil // No destination to match; preserve the original push failure.
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		open, err := openRemoteSyncStages(r)
		if err != nil {
			return err
		}
		for _, row := range open {
			st := row.stage
			if st.Remote == remote && st.Ref == ref && st.BaseSHA != "" && optionalCommit(ctx, l.cfg.Root, remoteSyncPinRef(st.ItemID)) == st.BaseSHA {
				conflict, err = readRemoteSyncConflict(r, st, row.state)
				return err
			}
		}
		return nil
	})
	return conflict, err
}

// Both human movers are represented by an owner wait. Use the conflict that
// owns the wait to recover the more specific obstacle routing decision.
func readRemoteSyncConflict(r *store.Reader, st remoteSyncState, state string) (*pushStateError, error) {
	if state != StateBlocked {
		return remoteSyncConflictError(st, "", ""), nil
	}
	wait, _, err := item.ReadWaitingOn(r, st.ItemID)
	if err != nil {
		return nil, err
	}
	if wait == nil || wait.Waiter != "owner" {
		return remoteSyncConflictError(st, "", ""), nil
	}
	who, reason := wait.Waiter, wait.Reason
	if id, ok := strings.CutPrefix(wait.Source, "conflict:"); ok {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_type='item' AND object_id=? AND json_extract(payload,'$.conflict_id')=? ORDER BY seq DESC LIMIT 1`,
			item.ConflictKind, st.ItemID, id).Scan(&raw); err != nil {
			return nil, err
		}
		var c item.Conflict
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		who, reason = remoteSyncObstacleMover(parseObstacle(c.Text).Kind)
		if strings.HasPrefix(c.Text, IdenticalRepeatPrefix) {
			who, reason = "operator", "repeated ending"
		}
	}
	return remoteSyncConflictError(st, who, reason), nil
}

func (l *Loop) closeRemoteSyncItem(ctx context.Context, st remoteSyncState) error {
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return err
	}
	defer release()
	if intent, err := l.readRemoteSyncReplay(ctx); err != nil || (intent != nil && intent.Candidate.ItemID == st.ItemID) {
		return err
	}
	// Recheck ancestry under the same lock used by replay and filing.
	reached, err := trunkAncestor(ctx, l.cfg.Root, st.RemoteTip, "refs/heads/"+TrunkRef)
	if err != nil || !reached {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	closed := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), st.ItemID, "close-remote-sync"); err != nil {
			if asItemTerminal(err) != nil {
				return nil
			}
			return err
		}
		reason := "remote tip " + shortSHA(st.RemoteTip) + " is now an ancestor of " + TrunkRef
		_, err := item.Close(ctx, tx, st.ItemID, reason, "tillr-drain", l.cfg.Now().UTC())
		closed = err == nil
		return err
	})
	if err != nil || !closed {
		return err
	}
	_, err = rawGit(ctx, l.cfg.Root, nil, "update-ref", "-d", remoteSyncPinRef(st.ItemID))
	return err
}

// A manual reconciliation needs neither a landing nor a push to end the hold.
func (l *Loop) closeStaleRemoteSyncItems(ctx context.Context) error {
	if l.cfg.Store == "" {
		return nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var open []openRemoteSyncStage
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		open, err = openRemoteSyncStages(r)
		return err
	})
	s.Close()
	if err != nil {
		return err
	}
	if len(open) != 0 {
		tip := optionalCommit(ctx, l.cfg.Root, "refs/heads/"+TrunkRef)
		for _, row := range open {
			st := row.stage
			if tip == "" || st.RemoteTip == "" {
				continue
			}
			if reached, err := trunkAncestor(ctx, l.cfg.Root, st.RemoteTip, tip); err == nil && reached {
				if err := l.closeRemoteSyncItem(ctx, st); err != nil {
					return err
				}
			}
		}
	}
	return l.settleClosedRemoteSyncConflict(ctx)
}

// Compare and clear in the same writer transaction, so a newer push result
// cannot be replaced by an older sweep, and repeated sweeps are idempotent.
func (l *Loop) settleClosedRemoteSyncConflict(ctx context.Context) error {
	if l.lastPushRecord(ctx).Kind != pushKindConflict {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var previous struct {
			Kind, Remote, Ref, What string
			RemoteTip               string `json:"remote_tip"`
		}
		var raw []byte
		var tip string
		err := tx.QueryRow(`SELECT payload, COALESCE(object_id,'') FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw, &tip)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &previous); err != nil {
			return err
		}
		if previous.Kind != pushKindConflict || previous.RemoteTip == "" {
			return nil
		}
		err = refuseTerminalItem(tx.Reader(), RemoteSyncItemID(previous.RemoteTip), "clear-push-conflict")
		terminal := asItemTerminal(err)
		if terminal == nil {
			return err
		}
		if terminal.State == "landed" {
			return nil // The repair's landing is followed by proof or publication.
		}
		return appendEffectOn(tx, postLandingKind, "landing", tip, map[string]any{
			"action": "push", "kind": pushKindConflictCleared, "detail": "",
			"what": previous.What, "remote": previous.Remote, "ref": previous.Ref,
			"remote_tip": previous.RemoteTip,
		}, l.cfg.Now().UTC())
	})
}
