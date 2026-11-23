package drain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	// RemoteSyncItemPrefix identifies generated remote-sync repairs.
	RemoteSyncItemPrefix = remotesync.RemoteSyncItemPrefix
	// RemoteSyncCriterion is the one criterion carried by a remote-sync repair.
	RemoteSyncCriterion  = remotesync.RemoteSyncCriterion
	remoteSyncRefPrefix  = remotesync.RemoteSyncRefPrefix
	remoteSyncStagedKind = remotesync.RemoteSyncStagedKind
	pushKindConflict     = remotesync.PushKindConflict
)

// RemoteSyncItemID names the repair for a fetched remote tip.
func RemoteSyncItemID(remoteTip string) string {
	return remotesync.RemoteSyncItemID(remoteTip)
}

// IsRemoteSyncItem reports whether an item is a generated remote-sync repair.
func IsRemoteSyncItem(itemID string) bool {
	return remotesync.IsRemoteSyncItem(itemID)
}

func remoteSyncPinRef(itemID string) string {
	return remotesync.RemoteSyncPinRef(itemID)
}

type remoteSyncState = remotesync.RemoteSyncState

func parseMergeTreeConflict(stdout string) (paths []string, conflictText string) {
	return remotesync.ParseMergeTreeConflict(stdout)
}

func remoteSyncProtectedPaths(paths []string) string {
	return remotesync.RemoteSyncProtectedPaths(paths, GateCode)
}

func (l *Loop) collectRemoteSyncContext(ctx context.Context, mergeBase, remoteTip, conflictCommit string) (remoteMsgs, commitMsg, landingSummary string) {
	if mergeBase != "" && remoteTip != "" {
		if out, err := rawGit(ctx, l.cfg.Root, nil, "log", "--format=%h %s", mergeBase+".."+remoteTip); err == nil {
			remoteMsgs = clip(out, 1500)
		}
	}
	if conflictCommit != "" {
		if out, err := rawGit(ctx, l.cfg.Root, nil, "log", "-1", "--format=%B", conflictCommit); err == nil {
			commitMsg = clip(out, 1500)
		}
	}
	if l.cfg.Store != "" && conflictCommit != "" {
		if s, err := store.OpenRead(ctx, l.cfg.Store); err == nil {
			defer s.Close()
			_ = s.ReadTx(ctx, func(r *store.Reader) error {
				var id, title string
				err := r.QueryRow(`SELECT l.item_id, COALESCE(i.title, '') FROM landings l LEFT JOIN items i USING(item_id) WHERE l.trunk_after = ? AND l.result = 'landed' LIMIT 1`, conflictCommit).Scan(&id, &title)
				if err == nil && id != "" {
					landingSummary = id
					if title != "" {
						landingSummary += ": " + title
					}
				}
				return nil
			})
		}
	}
	return remoteMsgs, commitMsg, landingSummary
}

func remoteSyncCriterionText(st remoteSyncState) string {
	return remotesync.RemoteSyncCriterionText(st)
}

func (l *Loop) readRemoteSyncState(ctx context.Context, itemID string) (remoteSyncState, bool, error) {
	return readRemoteSyncStateAt(ctx, l.cfg.Store, itemID)
}

func readRemoteSyncStateAt(ctx context.Context, storePath, itemID string) (st remoteSyncState, found bool, err error) {
	return remotesync.ReadRemoteSyncStateAt(ctx, storePath, itemID)
}

func readRemoteSyncState(r *store.Reader, itemID string) (remoteSyncState, bool, error) {
	return remotesync.ReadRemoteSyncState(r, itemID)
}

func remoteSyncDispatchBase(r *store.Reader, itemID string) (string, error) {
	base, err := remotesync.RemoteSyncDispatchBase(r, itemID)
	if err != nil {
		return "", Externalf("%v", err)
	}
	return base, nil
}

// remoteSyncBriefFacts renders the context recorded when the repair was filed.
func remoteSyncBriefFacts(ctx context.Context, storePath, itemID string) (string, error) {
	facts, err := remotesync.RemoteSyncBriefFacts(ctx, storePath, itemID)
	if err != nil {
		return "", Externalf("%v", err)
	}
	return facts, nil
}

// fileOrUpdateRemoteSyncItem files the first conflict for a remote tip. Push
// retries attach to the current stage without moving its pin or changing its fence.
func (l *Loop) fileOrUpdateRemoteSyncItem(ctx context.Context, st remoteSyncState) (*pushStateError, error) {
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return l.fileOrUpdateRemoteSyncItemLocked(ctx, st, "")
}

// Only landing may supply priorBase to advance a gated step. Keep the history
// lock across the original stage check, suffix replay and step publication.
func (l *Loop) fileOrUpdateRemoteSyncItemLocked(ctx context.Context, st remoteSyncState, priorBase string) (*pushStateError, error) {
	if _, err := l.recoverRemoteSyncReplayLocked(ctx); err != nil {
		return nil, err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, Incompatiblef("%v", err)
	}
	defer s.Close()

	var superseded []remoteSyncState
	if priorBase == "" {
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			open, err := openRemoteSyncStages(r)
			if err != nil {
				return err
			}
			for _, row := range open {
				old := row.stage
				if old.Remote == st.Remote && old.Ref == st.Ref && old.RemoteTip != st.RemoteTip {
					if older, _ := trunkAncestor(ctx, l.cfg.Root, old.RemoteTip, st.RemoteTip); older {
						superseded = append(superseded, old)
					}
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	var created, advanced bool
	var conflict *pushStateError
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		previous, found, err := readRemoteSyncState(tx.Reader(), st.ItemID)
		if err != nil {
			return err
		}
		if priorBase != "" {
			if !found || previous.BaseSHA != priorBase {
				return Externalf("remote-sync stage for %s changed before advancing", st.ItemID)
			}
			if err := refuseTerminalItem(tx.Reader(), st.ItemID, "advance remote-sync stage"); err != nil {
				return err
			}
			if err := l.advanceRemoteSyncStep(ctx, tx, st, priorBase); err != nil {
				return err
			}
			advanced = true
		} else if found {
			if previous.RemoteTip != st.RemoteTip {
				return Internalf("remote-sync item %s already names tip %s, not %s", st.ItemID, previous.RemoteTip, st.RemoteTip)
			}
			st = previous
		} else {
			if err := l.fileRemoteSyncStep(ctx, tx, st); err != nil {
				return err
			}
			created = true
		}
		var state string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id=?`, st.ItemID).Scan(&state); err != nil {
			return err
		}
		for _, old := range superseded {
			if _, err := item.Close(ctx, tx, old.ItemID, "superseded by "+st.ItemID, Utility, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		conflict, err = readRemoteSyncConflict(tx.Reader(), st, state)
		return err
	})
	if err != nil {
		if advanced {
			// Restore the previous pin if the store did not publish the stage.
			if _, restoreErr := rawGit(context.WithoutCancel(ctx), l.cfg.Root, nil, "update-ref", remoteSyncPinRef(st.ItemID), priorBase, st.BaseSHA); restoreErr != nil {
				err = errors.Join(err, restoreErr)
			}
		}
		return nil, classifyWrite(err)
	}
	if created {
		l.reportf("loop: filed %s for rebase conflict onto %s on %s\n", st.ItemID, remoteSyncDestination(st), strings.Join(st.ConflictPaths, ", "))
	}
	return conflict, nil
}

func (l *Loop) fileRemoteSyncStep(ctx context.Context, tx *store.Tx, st remoteSyncState) error {
	problem := remoteSyncProtectedPaths(st.ConflictPaths)
	payload, err := json.Marshal(map[string]any{
		"item_id": st.ItemID, "title": "resolve rebase conflict onto " + remoteSyncDestination(st), "state": StateInQueue,
		"rank": TrunkFixerRank, "seat": TrunkFixerSeat, "express": true,
		"criteria": []map[string]string{{"criterion_id": RemoteSyncCriterion, "text": remoteSyncCriterionText(st), "state": "not-done"}},
	})
	if err != nil {
		return Internalf("encoding remote-sync item: %v", err)
	}
	at := l.cfg.Now().UTC()
	if _, err := item.Apply(ctx, tx, record.Envelope{
		RecordVersion: 1, Kind: item.RecordKind, ID: "item-" + st.ItemID,
		Producer: record.Producer{System: "tillr", Utility: Utility, Version: Version}, CreatedAt: at, Payload: payload,
	}); err != nil {
		return err
	}
	if problem != "" {
		if _, err := item.SetState(ctx, tx, st.ItemID, StateBlocked, at, item.WaitingOn{
			Waiter: "owner", Reason: "rebase conflict onto " + remoteSyncDestination(st) + " includes " + problem,
		}); err != nil {
			return err
		}
	}
	if err := appendEffect(tx, remoteSyncStagedKind, st.ItemID, st, at); err != nil {
		return err
	}
	// Write under the history lock before making the stage visible. If the store
	// commit fails, the sweep treats the pin as belonging to a missing item.
	_, err = rawGit(ctx, l.cfg.Root, nil, "update-ref", "--no-deref", remoteSyncPinRef(st.ItemID), st.BaseSHA)
	return err
}

func (l *Loop) advanceRemoteSyncStep(ctx context.Context, tx *store.Tx, st remoteSyncState, priorBase string) error {
	if _, err := tx.Exec(`UPDATE item_criteria SET text=? WHERE item_id=? AND criterion_id=?`, remoteSyncCriterionText(st), st.ItemID, RemoteSyncCriterion); err != nil {
		return err
	}
	at := l.cfg.Now().UTC()
	if problem := remoteSyncProtectedPaths(st.ConflictPaths); problem != "" {
		if _, err := item.SetState(ctx, tx, st.ItemID, StateBlocked, at, item.WaitingOn{
			Waiter: "owner", Reason: "rebase conflict onto " + remoteSyncDestination(st) + " includes " + problem,
		}); err != nil {
			return err
		}
	}
	if err := appendEffect(tx, remoteSyncStagedKind, st.ItemID, st, at); err != nil {
		return err
	}
	_, err := rawGit(ctx, l.cfg.Root, nil, "update-ref", "--no-deref", remoteSyncPinRef(st.ItemID), st.BaseSHA, priorBase)
	return err
}

func remoteSyncDestination(st remoteSyncState) string {
	return remotesync.RemoteSyncDestination(st)
}

func remoteSyncConflictError(st remoteSyncState, who, reason string) *pushStateError {
	return &pushStateError{
		kind: pushKindConflict, remoteTip: st.RemoteTip, escalated: who,
		detail: remotesync.RemoteSyncConflictError(st, TrunkRef, who, reason),
	}
}
