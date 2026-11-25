package drain

import (
	"context"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const ReasonRedCap = "red-cap"
const redCapHeldKind = "drain.red-cap"

var errRedCapHeld = errors.New("landing waits on green proof at red cap")

type redCapHold struct {
	Root      string      `json:"root"`
	Candidate Candidate   `json:"candidate"`
	Cap       redcap.Hold `json:"cap"`
}

func init() {
	if err := event.Register(redCapHeldKind, Utility); err != nil {
		panic(err)
	}
}

func (h *redCapHold) current(ctx context.Context, r *store.Reader, at time.Time) (*redcap.Hold, error) {
	ref := h.Candidate.BaseRef
	if ref == "" {
		ref = trunkBranchFor(h.Root)
	}
	if redcap.Exempt(ref, h.Candidate.ItemID) {
		return nil, nil
	}
	tip, err := heavyGitOutput(ctx, h.Root, "rev-parse", ref+"^{commit}")
	if err != nil {
		return nil, err
	}
	// The lander already found a nonempty change at ObservedTrunk. A different
	// tip may now carry that change; let the locked lander check supersession
	// once on each new tip rather than holding completed work until green moves.
	if tip != h.Candidate.ObservedTrunk {
		return nil, nil
	}
	return redcap.Check(ctx, r, h.Root, ref, tip, h.Candidate.ItemID, at)
}
func (l *Loop) holdRedCap(ctx context.Context, c Candidate, cap *redcap.Hold) error {
	if cap == nil {
		return Internalf("red-cap refusal for %s carries no bound or fixer evidence", c.ItemID)
	}
	if err := l.pinCandidateHead(ctx, c); err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	at := l.cfg.Now().UTC()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, "red-cap"); err != nil {
			return err
		}
		if err := appendEffect(tx, redCapHeldKind, c.RunID, redCapHold{Root: l.cfg.Root, Candidate: c, Cap: *cap}, at); err != nil {
			return err
		}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: cap.Reason(), DeferredBy: cap.FixerItem, PassID: c.TickID}
		identifyDecision(&d)
		return recordDirtyDecision(tx, d, at)
	})
	if err != nil {
		return err
	}
	l.reportf("loop: holding %s (%s)\n", c.ItemID, cap.Reason())
	return errRedCapHeld
}
