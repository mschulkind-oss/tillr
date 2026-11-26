package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gatespans"
	"github.com/mschulkind-oss/tillr/store"
)

type gateStallHold struct {
	Candidate          Candidate          `json:"candidate"`
	HeadTree           string             `json:"head_tree"`
	GatesInFlight      int                `json:"gates_in_flight"`
	Observations       []StallObservation `json:"observations"`
	SecondObservations []StallObservation `json:"-"`
}

func (l *Loop) gateCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gatesInFlight
}

// First-stall evidence and the waiting transition share the verdict's transaction.
func recordGateStallHold(tx *store.Tx, hold gateStallHold, at time.Time) error {
	c := hold.Candidate
	if err := appendEffect(tx, gateStallHeldKind, c.RunID, hold, at); err != nil {
		return err
	}
	d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: ReasonGateStallRerun, PassID: c.TickID}
	identifyDecision(&d)
	return recordDirtyDecision(tx, d, at)
}

func (s *ProcessSteps) firstStallHold(ctx context.Context, c Candidate, observations []StallObservation) (*gateStallHold, error) {
	if s.Root == "" {
		return nil, nil
	}
	count := 1
	if c.gateCount != nil {
		count = c.gateCount()
	}
	body, err := exec.CommandContext(ctx, "git", "-C", c.Worktree, "rev-parse", c.HeadSHA+"^{tree}").Output()
	if err != nil {
		return nil, Externalf("reading stalled candidate tree: %v", err)
	}
	tree := strings.TrimSpace(string(body))
	st, err := store.OpenRead(ctx, s.Store)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	var first bool
	err = st.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err == nil {
			work := works[c.ItemID]
			first = work == nil || work.GateStall == nil || work.GateStall.HeadTree != tree
		}
		return err
	})
	if err != nil || !first {
		return nil, err
	}
	return &gateStallHold{Candidate: c, HeadTree: tree, GatesInFlight: count, Observations: observations}, nil
}

func (l *Loop) holdGateStall(ctx context.Context, c Candidate, gate GateOutcome, count int) error {
	if gate.StallHeld {
		l.reportf("loop: holding %s (%s): first stall retained with its judgment; %d gates in flight\n", c.ItemID, ReasonGateStallRerun, count)
		return errGateStallHeld
	}
	tree, err := l.revParse(ctx, c.Worktree, c.HeadSHA+"^{tree}")
	if err != nil {
		return err
	}
	hold := gateStallHold{Candidate: c, HeadTree: tree, GatesInFlight: count, Observations: gate.StalledChecks}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var prior *gateStallHold
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, ReasonGateStallRerun); err != nil {
			return err
		}
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		if work := works[c.ItemID]; work != nil && work.GateStall != nil && work.GateStall.HeadTree == tree {
			prior = work.GateStall
			return nil
		}
		return recordGateStallHold(tx, hold, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	if prior != nil {
		return l.secondGateStallFault(ctx, c, prior, gate.StalledChecks)
	}
	evidence, _ := json.Marshal(hold.Observations)
	l.reportf("loop: holding %s (%s): %s; %d gates in flight; rerun when fewer gates or none other are in flight\n", c.ItemID, ReasonGateStallRerun, evidence, count)
	return errGateStallHeld
}

func (h *gateStallHold) canRetry(gates int) bool {
	return gatespans.CanRetryGateStall(len(h.SecondObservations), gates, h.GatesInFlight)
}

func (l *Loop) secondGateStallFault(ctx context.Context, c Candidate, first *gateStallHold, second []StallObservation) error {
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	detail := fmt.Sprintf("second stall on head tree %s; first: %s; second: %s", first.HeadTree, firstJSON, secondJSON)
	if acquitted, err := l.acquitInstalledGateFault(ctx, c, ReasonGateStalled, detail); err != nil {
		return err
	} else if acquitted {
		return nil
	}
	if err := l.holdItemFault(ctx, c, ReasonGateStalled, detail); err != nil {
		return err
	}
	return errItemFaultHeld
}

// Finish the fault after a crash between the second gate's recorded verdict
// and verify's fault effect, without spending a third examination of that tree.
func (l *Loop) recoverSecondGateStall(ctx context.Context, c Candidate) (bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var hold *gateStallHold
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if work := works[c.ItemID]; work != nil {
			hold = work.GateStall
		}
		return nil
	})
	if err != nil || hold == nil || len(hold.SecondObservations) == 0 {
		return false, err
	}
	tree, err := l.revParse(ctx, c.Worktree, c.HeadSHA+"^{tree}")
	if err != nil {
		return false, err
	}
	if tree != hold.HeadTree {
		return false, nil
	}
	return true, l.secondGateStallFault(ctx, c, hold, hold.SecondObservations)
}
