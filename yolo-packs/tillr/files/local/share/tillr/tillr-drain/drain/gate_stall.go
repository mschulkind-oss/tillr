package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	GateStalled          = "stalled"
	GateStallExit        = 6
	ReasonGateStallRerun = "gate-stall-rerun"
	ReasonGateStalled    = "gate-stalled"
	gateStallHeldKind    = "drain.gate-stall"
	// ReasonGateNoVerdict is the wait between a gate that answered "error" -- it ran and
	// reached no verdict, so nothing about the candidate was judged -- and the next pass,
	// which gates the same candidate again on the same lane (gate_no_verdict.go). It is
	// uncounted and its mover is the loop, never a person.
	ReasonGateNoVerdict = "gate-no-verdict"
)

var errGateStallHeld = errors.New("candidate waits for a gate stall rerun")

// StallObservation is the check's watchdog evidence, not a candidate verdict.
type StallObservation struct {
	CheckID             string `json:"check_id"`
	Result              string `json:"result,omitempty"`
	LastOutputLine      string `json:"last_output_line"`
	CPUTimeMS           int64  `json:"cpu_time_ms"`
	SilentSamples       int    `json:"silent_samples"`
	WatchdogUnavailable string `json:"watchdog_unavailable,omitempty"`
	DurationMS          int64  `json:"duration_ms"`
}

type gateStallHold struct {
	Candidate          Candidate          `json:"candidate"`
	HeadTree           string             `json:"head_tree"`
	GatesInFlight      int                `json:"gates_in_flight"`
	Observations       []StallObservation `json:"observations"`
	SecondObservations []StallObservation `json:"-"`
}

func init() {
	if err := event.Register(gateStallHeldKind, Utility); err != nil {
		panic(err)
	}
}

func (s *ProcessSteps) gateStallContext(c Candidate) ([]StallObservation, error) {
	body, err := os.ReadFile(filepath.Join(RunDir(s.Root, c.RunID), GateVerdictRecord))
	if err != nil {
		return nil, Externalf("reading stalled gate verdict: %v", err)
	}
	var rec struct {
		Payload struct {
			Result string             `json:"result"`
			Checks []StallObservation `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, Externalf("reading stalled gate verdict: %v", err)
	}
	observations := []StallObservation{}
	for _, check := range rec.Payload.Checks {
		res := classifyCheck(checkresult.Observation{
			Check:    check.CheckID,
			Commit:   c.HeadSHA,
			Worktree: c.Worktree,
			Source:   checkresult.ResultWord,
			Word:     check.Result,
		})
		if res.Outcome == checkresult.Unknown && res.Detail == checkresult.DetailStalled {
			observations = append(observations, check)
		}
	}
	runRes := classifyCheck(checkresult.Observation{
		Commit:   c.HeadSHA,
		Worktree: c.Worktree,
		Source:   checkresult.ResultWord,
		Word:     rec.Payload.Result,
	})
	if runRes.Outcome != checkresult.Unknown || runRes.Detail != checkresult.DetailStalled || len(observations) == 0 {
		return nil, Externalf("gate exited 6 without a stalled check observation")
	}
	return observations, nil
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
	return len(h.SecondObservations) > 0 || gates == 0 || gates < h.GatesInFlight
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
