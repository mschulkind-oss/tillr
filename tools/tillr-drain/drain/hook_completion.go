package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonCompleteItemFault = "complete-item-fault"
	ReasonCriteriaUnjudged  = "criteria-unjudged"
)

type completionFault struct{ reason, detail string }

func (e *completionFault) Error() string { return e.reason + ": " + e.detail }

type completionSteps interface {
	CompleteItem(context.Context, string, string, definition.Definition) (bool, error)
}

// ConfigureNonLanding is called when a definition completes without landing.
func (s *ProcessSteps) ConfigureNonLanding() {}

// CompleteItem calls the definition outside any store transaction. The state
// subprocess alone checks verdict receipts and moves the item in one write.
func (s *ProcessSteps) CompleteItem(ctx context.Context, itemID, runID string, d definition.Definition) (bool, error) {
	fault := func(err error) (bool, error) { return false, &completionFault{ReasonCompleteItemFault, err.Error()} }
	if d.Hooks == nil || !slices.Contains(d.Hooks.Names, "complete-item") {
		return fault(fmt.Errorf("definition %s has no complete-item hook", d.Name))
	}
	// Paths are caller-relative until the hook changes directories. Pass absolute
	// paths so the hook and the state utility address the same root and store.
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return fault(err)
	}
	db, err := filepath.Abs(s.Store)
	if err != nil {
		return fault(err)
	}
	argv := append(append([]string{}, d.Hooks.Argv...), "complete-item", "--item", itemID, "--run", runID, "--store", db, "--root", root)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = withGitCeilingEnv(os.Environ(), s.LaneRoot)
	var stderr stderrTail
	cmd.Stderr = &stderr
	started := time.Now().UnixMilli()
	out, err := cmd.Output()
	if spanErr := s.recordStep(runID, stepSpan("complete-item", argv, cmd, started, stderr.String())); spanErr != nil {
		return false, spanErr
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return fault(fmt.Errorf("hook: %v: %s", err, stderr.String()))
	}
	var answer struct {
		State       string `json:"state"`
		Reason      string `json:"reason"`
		ResolvesVia string `json:"resolves_via,omitempty"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return fault(fmt.Errorf("hook answer: %w", err))
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fault(fmt.Errorf("hook must print exactly one JSON object"))
	}
	if !d.IsFinished(answer.State) || strings.TrimSpace(answer.Reason) == "" {
		return fault(fmt.Errorf("hook must name a finished state and a nonblank reason"))
	}
	args := []string{"--require-verdicts", "--reason", answer.Reason, "--by", "definition:" + d.Name}
	if answer.ResolvesVia != "" {
		args = append(args, "--resolves-via", answer.ResolvesVia)
	}
	if err := s.itemState(ctx, itemID, runID, answer.State, args); err != nil {
		var rejection *completionFault
		if errors.As(err, &rejection) {
			return false, err
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return fault(err)
	}
	return true, nil
}

// settleHookGroup completes an ended reader group once, without the Git-only
// parallel boundary or candidate policy. Each run still gets its spend receipt.
func (l *Loop) settleHookGroup(ctx context.Context, work *pendingWork, result *LoopResult, timing *PassTiming) laneOutcome {
	d := work.Decision
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return laneOutcome{err: err}
	}
	nodes := parallelNodes(d)
	var records []run.Record
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		for _, node := range nodes {
			rec, err := terminalRecord(r, node.RunRequest.RunID)
			if err != nil {
				return err
			}
			if rec == nil {
				return fmt.Errorf("parallel node %s has no terminal record", node.RunRequest.RunID)
			}
			records = append(records, *rec)
		}
		return nil
	})
	s.Close()
	if err != nil {
		return laneOutcome{err: err}
	}
	paused := false
	for i, rec := range records {
		if err := l.recordSettlementSpend(ctx, timing.TickID, work.LoopID, rec); err != nil {
			return laneOutcome{err: err}
		}
		l.countSettledSpend(result, rec, work.LoopID)
		if rec.State == run.StatePaused {
			node := nodes[i]
			resumed, _, stop, err := l.resumeAfterWall(ctx, node.LaneID, *node.RunRequest, l.lanePath(node.LaneID), rec, timing.TickID, l.cfg.Now(), result, nil)
			if err != nil || stop != "" {
				return laneOutcome{err: err, stop: stop}
			}
			if resumed.State == run.StatePaused {
				return laneOutcome{}
			}
			if resumed.RunID != "" {
				continue
			}
			// No session to resume: retain the unjudged-node hold rather
			// than asking the definition to finish a paused session.
			if err := l.nodeOutcome(ctx, node, "unjudged"); err != nil {
				return laneOutcome{err: err}
			}
			paused = true
		}
	}
	if paused {
		return laneOutcome{err: l.markExamined(ctx, d)}
	}
	outcome := l.settleHook(ctx, d, result)
	if terminal := asItemTerminal(outcome.err); terminal != nil {
		outcome.err = l.settleTerminalItem(ctx, work, terminal)
	} else if outcome.err == nil && outcome.stop == "" {
		outcome.err = l.markExamined(ctx, d)
	}
	return outcome
}

func (l *Loop) settleHook(ctx context.Context, d Decision, result *LoopResult) laneOutcome {
	// Recovery reads the item before invoking the hook: a committed finished move
	// is the durable completion receipt even if the loop never wrote its records.
	if err := l.checkItemTerminal(ctx, d.ItemID, "complete-item"); err != nil {
		return laneOutcome{err: err}
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return laneOutcome{err: err}
	}
	var declared definition.Definition
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rec, err := definition.ForReader(r)
		if err == nil {
			declared = rec.Parsed
		}
		return err
	})
	s.Close()
	if err != nil {
		return laneOutcome{err: err}
	}
	steps, ok := l.cfg.Steps.(completionSteps)
	if !ok {
		return laneOutcome{err: Usagef("hook completion requires a complete-item step")}
	}
	done, err := steps.CompleteItem(ctx, d.ItemID, d.RunRequest.RunID, declared)
	if err != nil {
		var fault *completionFault
		if !errors.As(err, &fault) {
			return laneOutcome{err: err}
		}
		c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID}
		return laneOutcome{err: l.holdItemFault(ctx, c, fault.reason, fault.detail)}
	}
	if !done {
		// A negative hook answer follows the existing no-candidate refusal rule,
		// without calling a candidate program or cleaning a Git worktree.
		c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID}
		return laneOutcome{err: l.refuse(ctx, c, result, RefusalNoOp)}
	}
	return laneOutcome{}
}
