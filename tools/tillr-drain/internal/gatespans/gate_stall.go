package gatespans

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
)

const (
	GateStalled          = "stalled"
	GateStallExit        = 6
	ReasonGateStallRerun = "gate-stall-rerun"
	ReasonGateStalled    = "gate-stalled"
	GateStallHeldKind    = "drain.gate-stall"
	// ReasonGateNoVerdict is the wait between a gate that answered "error" -- it ran and
	// reached no verdict, so nothing about the candidate was judged -- and the next pass,
	// which gates the same candidate again on the same lane (gate_no_verdict.go). It is
	// uncounted and its mover is the loop, never a person.
	ReasonGateNoVerdict = "gate-no-verdict"
)

var (
	ErrGateStallHeld = errors.New("candidate waits for a gate stall rerun")
	// ErrGateNoVerdict is the examination outcome of a gate that ran and answered "error":
	// no verdict exists, so the candidate is kept on its lane and the next pass gates it again.
	ErrGateNoVerdict = errors.New("candidate waits for the gate to reach a verdict")
)

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

func init() {
	if err := event.Register(GateStallHeldKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// CanRetryGateStall reports whether a stalled candidate may rerun its gate.
func CanRetryGateStall(secondCount, gates, gatesInFlight int) bool {
	return secondCount > 0 || gates == 0 || gates < gatesInFlight
}

// GateStallContext reads stalled check watchdog observations from a run's gate verdict.
func GateStallContext(root, runID, headSHA, worktree string) ([]StallObservation, error) {
	body, err := os.ReadFile(filepath.Join(drainmodel.RunDir(root, runID), storelog.GateVerdictRecord))
	if err != nil {
		return nil, drainmodel.Externalf("reading stalled gate verdict: %v", err)
	}
	var rec struct {
		Payload struct {
			Result string             `json:"result"`
			Checks []StallObservation `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, drainmodel.Externalf("reading stalled gate verdict: %v", err)
	}
	observations := []StallObservation{}
	for _, check := range rec.Payload.Checks {
		res := drainmodel.ClassifyCheck(checkresult.Observation{
			Check:    check.CheckID,
			Commit:   headSHA,
			Worktree: worktree,
			Source:   checkresult.ResultWord,
			Word:     check.Result,
		})
		if res.Outcome == checkresult.Unknown && res.Detail == checkresult.DetailStalled {
			observations = append(observations, check)
		}
	}
	runRes := drainmodel.ClassifyCheck(checkresult.Observation{
		Commit:   headSHA,
		Worktree: worktree,
		Source:   checkresult.ResultWord,
		Word:     rec.Payload.Result,
	})
	if runRes.Outcome != checkresult.Unknown || runRes.Detail != checkresult.DetailStalled || len(observations) == 0 {
		return nil, drainmodel.Externalf("gate exited 6 without a stalled check observation")
	}
	return observations, nil
}
