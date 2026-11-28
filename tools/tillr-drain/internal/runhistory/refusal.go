package runhistory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/store"
)

// RepeatedRefusalThreshold is how many refused items, with no landing, make the loop's own
// report call the run a repeated refusal rather than list one more rejected candidate.
const RepeatedRefusalThreshold = 2

// The steps that can refuse a candidate, as the report names them.
const (
	RefusalNoOp          = "no-op"
	RefusalFence         = "fence"
	RefusalGate          = "gate"
	RefusalVerifyNotDone = "verify-not-done"
	// RefusalVerifyNoVerdict is a machinery ending, not a finding about the candidate.
	RefusalVerifyNoVerdict = "verify-no-verdict"
	// RefusalPlanWrong is a build step's report that it cannot meet the verifier's findings
	// within the plan (or that the plan's narrowed piece cannot hold the work).
	RefusalPlanWrong = "plan-wrong"
	RefusalLand      = "land"
	RefusalConflict  = "conflict"
	// RefusalNodeOutput means a successful run did not supply a usable declared file.
	RefusalNodeOutput = "node-output-missing"
	// RefusalReportMissing means a workflow agent node exited 0 and left no report that
	// states an ending: the run is returned to its builder rather than restarted again.
	RefusalReportMissing = "report-missing"
	// RefusalPlanOutput means a plan or classification step wrote a delivery output that
	// could not be parsed; it returns to the same session with the parse error and shape.
	RefusalPlanOutput = "plan-output-unparseable"
	// RefusalStaging is the step that refused before anything ran: the item's own record
	// could not be rendered into a brief.
	RefusalStaging = "staging"
	// RefusalWordsMissing, RefusalWordsAltered, and RefusalIdeaNoOutcome are idea-intake refusals.
	RefusalWordsMissing  = "words-missing"
	RefusalWordsAltered  = "words-altered"
	RefusalIdeaNoOutcome = "idea-no-outcome"
	// RefusalReverted is the ending a reverted landing's run is given.
	RefusalReverted = "reverted"
	// ReasonLandingReverted is the planner's hold spelling of that ending.
	ReasonLandingReverted = "landing-reverted"
	// ReasonEvalFailed is the judge evaluation failure reason.
	ReasonEvalFailed = "eval-failed"
)

// RepeatedRefusal is the drain's own answer to "has this loop ever landed anything?".
type RepeatedRefusal struct {
	Candidates int            `json:"candidates"`
	Rejected   int            `json:"rejected"`
	Steps      map[string]int `json:"steps"`
	Items      []string       `json:"items"`
	Ticks      int            `json:"ticks"`
	Tokens     int64          `json:"tokens"`
	SpendUSD   float64        `json:"spend_usd"`
}

// FormatRepeatedRefusalLine formats report as one line for operator output, or "" when nil.
func FormatRepeatedRefusalLine(report *RepeatedRefusal) string {
	if report == nil {
		return ""
	}
	var line string
	if report.Candidates == 0 {
		line = fmt.Sprintf("this drain has produced no candidate: %d run(s) produced no commit", report.Rejected)
	} else {
		line = fmt.Sprintf("this drain has landed nothing: every one of its %d candidate(s) was refused (%s)",
			report.Candidates, RefusalStepsWords(report.Steps))
		if noOps := report.Rejected - report.Candidates; noOps > 0 {
			line += fmt.Sprintf(", and %d run(s) produced no commit", noOps)
		}
	}
	return line + fmt.Sprintf("; window: %d tick(s), %d tokens, $%f; items left in the queue: %s",
		report.Ticks, report.Tokens, report.SpendUSD, strings.Join(report.Items, ", "))
}

// RefusalStepsWords is the steps breakdown as one phrase: `fence=2`, `gate=1, fence=1`.
func RefusalStepsWords(steps map[string]int) string {
	order := []string{RefusalNoOp, RefusalFence, RefusalGate, RefusalVerifyNotDone, RefusalPlanWrong, RefusalLand, drainmodel.RefusalDeliveryOrder, RefusalConflict, RefusalNodeOutput, RefusalReportMissing, RefusalPlanOutput, RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome}
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

// ConcludingRefusals are the refusal steps that end their run's examination.
var ConcludingRefusals = []string{RefusalLand, drainmodel.RefusalDeliveryOrder, RefusalGate, RefusalVerifyNotDone, RefusalFence, RefusalNoOp, RefusalNodeOutput, RefusalReportMissing, RefusalPlanOutput}

// GateRefusalCauses derives the countable cross-item cause keys for a gate refusal's
// failing checks (D-TW-RRS1, D-TW-RRS2, D-TW-RRS5). A named failing case keys on check,
// case and the first line of its assertion text, so two candidates failing different
// assertions of one test share no cause; a named case with no assertion text keys nothing,
// with no fallback to the test or check name. A failing check with no named case keys on
// the check alone. The gate adapter's own wrapper entry (for example, `gate:python3`) is
// omitted when it has no recorded failing checks.
func GateRefusalCauses(checks []EndingCheck) []storelog.RefusalCause {
	seen := map[string]struct{}{}
	sawCheck := false
	for _, check := range checks {
		found := false
		for _, failing := range check.Failing {
			id := strings.TrimSpace(failing.ID)
			if id == "" {
				continue
			}
			found, sawCheck = true, true
			caseFound := false
			for _, c := range failing.Cases {
				name := strings.TrimSpace(c.Name)
				if name == "" {
					continue
				}
				caseFound = true
				assertion, _, _ := strings.Cut(c.Assertion, "\n")
				if assertion = strings.TrimSpace(assertion); assertion != "" {
					seen[id+":"+name+":"+assertion] = struct{}{}
				}
			}
			if !caseFound {
				seen[id] = struct{}{}
			}
		}
		if !found && check.CheckID != "" && !strings.HasPrefix(check.CheckID, "gate:") {
			sawCheck = true
			seen[check.CheckID] = struct{}{}
		}
	}
	if !sawCheck {
		return []storelog.RefusalCause{{Cause: "gate:no-verdict"}}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]storelog.RefusalCause, 0, len(ids))
	for _, id := range ids {
		out = append(out, storelog.RefusalCause{Cause: "gate:" + id})
	}
	return out
}

// ConcludedByRefusal answers whether runID's examination already ended in a refusal: a
// concluding drain.refusal recorded after the run's latest candidate, retirement, or
// caller-supplied extra reopening kinds (such as candidateAssignedKind).
func ConcludedByRefusal(ctx context.Context, storePath, runID string, extraReopenKinds ...string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return false, err
	}
	defer s.Close()
	concluded := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var step sql.NullString
		var refusalSeq sql.NullInt64
		if err := r.QueryRow(`SELECT seq, json_extract(payload, '$.step') FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, storelog.RefusalKind, runID).Scan(&refusalSeq, &step); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if !slices.Contains(ConcludingRefusals, step.String) {
			return nil
		}
		kinds := make([]string, 0, 2+len(extraReopenKinds))
		kinds = append(kinds, CandidateReadyKind, CandidateRetirementKind)
		kinds = append(kinds, extraReopenKinds...)
		args := make([]any, 0, 2+len(kinds))
		args = append(args, runID, refusalSeq.Int64)
		for _, k := range kinds {
			args = append(args, k)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
		var later int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE object_type = 'run' AND object_id = ? AND seq > ? AND kind IN (`+placeholders+`)`, args...).Scan(&later); err != nil {
			return err
		}
		concluded = later == 0
		return nil
	})
	return concluded, err
}
