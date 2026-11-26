package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// RepeatedRefusalThreshold is how many refused items, with no landing, make the loop's own
// report call the run a repeated refusal rather than list one more rejected candidate. Two
// is the smallest number that is a pattern: one refused item is a bad item, and every drain
// has one. It counts items, not refusals: since the owner's ruling of 2026-11-17 one item's
// refusal goes back to its worker, so one bad item is refused more than once. It is a
// report and holds nothing.
const RepeatedRefusalThreshold = 2

// The steps that can refuse a candidate, as the report names them. A refusal is a verdict
// about the candidate, and which step made it is the difference between a fence that
// admits nothing and a gate that fails everything.
const (
	RefusalNoOp          = "no-op"
	RefusalFence         = "fence"
	RefusalGate          = "gate"
	RefusalVerifyNotDone = "verify-not-done"
	// RefusalPlanWrong is a build step's report that it cannot meet the verifier's findings
	// within the plan (or that the plan's narrowed piece cannot hold the work). It sends the
	// item back to the plan step, the one other route a failed verify has.
	RefusalPlanWrong = "plan-wrong"
	RefusalLand      = "land"
	RefusalConflict  = "conflict"
	// RefusalNodeOutput means a successful run did not supply a usable declared file.
	RefusalNodeOutput = "node-output-missing"
	// RefusalPlanOutput means a plan or classification step wrote a delivery output that
	// could not be parsed; it returns to the same session with the parse error and shape.
	RefusalPlanOutput = "plan-output-unparseable"
	// RefusalStaging is the step that refused before anything ran: the item's own record
	// could not be rendered into a brief. It is the one refusal with no run to name, so its
	// `drain.refusal` event is keyed by the item rather than by a run, and the loop holds
	// that item with ReasonItemUnstagleable.
	RefusalStaging = "staging"
)

// RepeatedRefusal is the drain's own answer to "has this loop ever landed anything?".
//
// It is set when the loop has landed nothing and refused at least
// RepeatedRefusalThreshold items: every candidate the drain produced was refused, which
// is a fact about the loop's judging — a fence that admits nothing, a gate that fails
// everything — and not N unlucky items. It is a report, not a stop: the design's stop
// set is closed and a refusal is a verdict about the
// candidate's code, so the loop keeps running. What it must not do is let a run that has
// never landed look like a run that is merely unlucky.
type RepeatedRefusal struct {
	// Candidates is the number of runs that committed a candidate. Every one was
	// refused.
	Candidates int `json:"candidates"`
	// Rejected is the number of refusals, which includes runs that produced no commit.
	Rejected int `json:"rejected"`
	// Steps counts the refusals by the step that made them, so a reader can tell a
	// fence that admits nothing from a gate that fails everything.
	Steps map[string]int `json:"steps"`
	// Items are the items the refusals left in the queue, in the order they were refused
	// and without repeats.
	Items []string `json:"items"`
	// Ticks, Tokens and SpendUSD are the window's cost: the whole run, because the loop
	// landed nothing to divide it.
	Ticks    int     `json:"ticks"`
	Tokens   int64   `json:"tokens"`
	SpendUSD float64 `json:"spend_usd"`
}

// refuse records one candidate's refusal: the step that made it, and the loop's own
// aggregate of how many candidates it has refused without landing one. The four refusal
// sites differ only in the step they name, which is what lets the report say a fence refused
// every candidate rather than that every candidate failed.
//
// A gate failure is a refusal and not a verdict (MQ1): no judge retired the item's
// criteria, the item stays `in-queue` with no verdict frozen against it, and the
// `drain.refusal` event is what the planner's named hold reads. A land that did not take
// follows the same rule: the gate proves tree health, not criterion satisfaction.
func (l *Loop) refuse(ctx context.Context, candidate Candidate, result *LoopResult, step string) error {
	// A refusal holds an item in the queue; a terminal item is not refused or counted.
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "refusal"); err != nil {
		return err
	}
	l.mu.Lock()
	result.Rejected++
	if result.RefusedSteps == nil {
		result.RefusedSteps = map[string]int{}
	}
	result.RefusedSteps[step]++
	if !slices.Contains(result.RefusedItems, candidate.ItemID) {
		result.RefusedItems = append(result.RefusedItems, candidate.ItemID)
	}
	var line string
	if result.Landed == 0 && len(result.RefusedItems) >= RepeatedRefusalThreshold {
		result.RepeatedRefusal = repeatedRefusal(result)
		line = RepeatedRefusalLine(*result)
	}
	l.mu.Unlock()
	if line != "" {
		l.reportf("loop: repeated-refusal: %s\n", line)
	}
	switch step {
	case RefusalLand, RefusalDeliveryOrder, RefusalGate, RefusalVerifyNotDone, RefusalPlanWrong, RefusalFence, RefusalNoOp, RefusalNodeOutput, RefusalPlanOutput, RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome:
		return l.recordRefusal(ctx, candidate, step)
	}
	return nil
}

// finalizeRepeatedRefusal sets or clears the report from the run's final totals. It runs
// on every way out of Run, so a loop that stopped after its last refusal still reports the
// window it spent, and a loop that landed something never reports a refusal it recovered
// from.
func (l *Loop) finalizeRepeatedRefusal(result *LoopResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if result.Landed > 0 || len(result.RefusedItems) < RepeatedRefusalThreshold {
		result.RepeatedRefusal = nil
		return
	}
	result.RepeatedRefusal = repeatedRefusal(result)
}

// repeatedRefusal is the report's payload, built from the loop's own totals.
func repeatedRefusal(result *LoopResult) *RepeatedRefusal {
	return &RepeatedRefusal{
		Candidates: result.Candidates,
		Rejected:   result.Rejected,
		Steps:      result.RefusedSteps,
		Items:      result.RefusedItems,
		Ticks:      result.Ticks,
		Tokens:     result.Tokens,
		SpendUSD:   result.SpendUSD,
	}
}

// RepeatedRefusalLine is the repeated-refusal report as one line: the blast radius the
// loop measured, in the words an operator reads at exit. It answers "" when the run is
// not a repeated refusal.
func RepeatedRefusalLine(result LoopResult) string {
	report := result.RepeatedRefusal
	if report == nil {
		return ""
	}
	var line string
	if report.Candidates == 0 {
		line = fmt.Sprintf("this drain has produced no candidate: %d run(s) produced no commit", report.Rejected)
	} else {
		line = fmt.Sprintf("this drain has landed nothing: every one of its %d candidate(s) was refused (%s)",
			report.Candidates, refusalStepsWords(report.Steps))
		if noOps := report.Rejected - report.Candidates; noOps > 0 {
			line += fmt.Sprintf(", and %d run(s) produced no commit", noOps)
		}
	}
	return line + fmt.Sprintf("; window: %d tick(s), %d tokens, $%f; items left in the queue: %s",
		report.Ticks, report.Tokens, report.SpendUSD, strings.Join(report.Items, ", "))
}

// refusalStepsWords is the steps breakdown as one phrase: `fence=2`, `gate=1, fence=1`.
// The order is fixed rather than the map's, so the same report reads the same way twice.
func refusalStepsWords(steps map[string]int) string {
	order := []string{RefusalNoOp, RefusalFence, RefusalGate, RefusalVerifyNotDone, RefusalPlanWrong, RefusalLand, RefusalDeliveryOrder, RefusalConflict, RefusalNodeOutput, RefusalPlanOutput, RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome}
	parts := make([]string, 0, len(order))
	for _, step := range order {
		if steps[step] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", step, steps[step]))
		}
	}
	if len(parts) == 0 {
		return "no step recorded"
	}
	return strings.Join(parts, ", ")
}

// concludingRefusals are the refusal steps that end their run's examination: refuse()
// records them, the caller then abandons the lane, and the item's next attempt is a new
// run.
var concludingRefusals = []string{RefusalLand, RefusalDeliveryOrder, RefusalGate, RefusalVerifyNotDone, RefusalFence, RefusalNoOp, RefusalNodeOutput, RefusalPlanOutput}

// concludedByRefusal answers whether runID's examination already ended in a refusal: a
// concluding drain.refusal recorded after the run's latest candidate, assignment or
// retirement. A run re-staged after its refusal (candidate-assigned) is a new examination
// and is not concluded.
func (l *Loop) concludedByRefusal(ctx context.Context, runID string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	concluded := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var step sql.NullString
		var refusalSeq sql.NullInt64
		if err := r.QueryRow(`SELECT seq, json_extract(payload, '$.step') FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, RefusalKind, runID).Scan(&refusalSeq, &step); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if !slices.Contains(concludingRefusals, step.String) {
			return nil
		}
		var later int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE object_type = 'run' AND object_id = ? AND seq > ? AND kind IN (?, ?, ?)`, runID, refusalSeq.Int64, candidateReadyKind, candidateAssignedKind, candidateRetirementKind).Scan(&later); err != nil {
			return err
		}
		concluded = later == 0
		return nil
	})
	return concluded, err
}
