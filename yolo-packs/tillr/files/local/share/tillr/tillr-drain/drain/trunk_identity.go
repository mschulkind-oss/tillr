package drain

import (
	"context"
	"errors"
	"reflect"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

const ReasonForeignIdentity = land.ReasonForeignIdentity

var errForeignIdentity = errors.New("landing waits on a trunk identity decision")

// observeIdentity is serialized with the locked lander but never stops running lanes.
// Immutable history avoids rescanning old commits; unanswered identities and their
// acceptance records are read again even when the tip has not moved.
func (l *Loop) observeIdentity(ctx context.Context) error {
	t, err := land.OpenTrunk(l.cfg.Root)
	if err != nil {
		return err
	}
	t.SetContext(ctx)
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var before, next land.IdentityHistory
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		var err error
		before, err = land.ReadIdentityHistory(tx)
		if err != nil {
			return err
		}
		tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return err
		}
		next, err = t.CheckIdentities(tx, before, before.Tip, tip)
		return err
	})
	r.Close()
	if err != nil || reflect.DeepEqual(before, next) {
		return err
	}
	release, err := l.lockTrunkHistory(ctx)
	if err != nil {
		return err
	}
	defer release()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	// Re-read under the repository lock; an earlier check cannot overwrite another
	// observer's tip. Git computation precedes the SQLite writer transaction.
	err = s.ReadTx(ctx, func(tx *store.Reader) error {
		var err error
		before, err = land.ReadIdentityHistory(tx)
		if err != nil {
			return err
		}
		tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return err
		}
		next, err = t.CheckIdentities(tx, before, before.Tip, tip)
		return err
	})
	if err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error { return land.SaveIdentityHistory(tx, next) })
}

func (l *Loop) holdIdentity(ctx context.Context, c Candidate) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		h, err := land.ReadIdentityHistory(tx)
		if err != nil {
			return err
		}
		if len(h.Pending) == 0 {
			return nil
		}
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, ReasonForeignIdentity); err != nil {
			return err
		}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: h.Reason(), PassID: c.TickID}
		identifyDecision(&d)
		return recordDirtyDecision(tx, d, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	return errForeignIdentity
}

func (l *Loop) identityHeld(ctx context.Context) (bool, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer r.Close()
	held := false
	err = r.ReadTx(ctx, func(tx *store.Reader) error {
		h, err := land.ReadIdentityHistory(tx)
		held = len(h.Pending) != 0
		return err
	})
	return held, err
}
