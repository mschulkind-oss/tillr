package drain

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// WhyRow decorates a planner decision with references a human can follow. Decision
// retains the full planner record; the presentation fields do not decide readiness.
type WhyRow struct {
	Rank        string   `json:"rank"`
	ItemID      string   `json:"item_id"`
	State       string   `json:"state"`
	Position    string   `json:"position"`
	Reason      string   `json:"reason"`
	Why         string   `json:"why"`
	Class       string   `json:"class"`
	Where       string   `json:"where"`
	Mover       string   `json:"mover"`
	Since       string   `json:"since"`
	ClassSource string   `json:"class_source"`
	WaitingOn   []string `json:"waiting_on"`
	Decision    Decision `json:"decision"`
}

type WhySummary struct {
	LanesBusy    int            `json:"lanes_busy"`
	LanesOffered int            `json:"lanes_offered"`
	Running      int            `json:"running"`
	CouldRun     int            `json:"could_run"`
	Held         map[string]int `json:"held"`
}

type WhyAnswer struct {
	EventHeadSeq int64      `json:"event_head_seq"`
	Rows         []WhyRow   `json:"rows"`
	Summary      WhySummary `json:"summary"`
}

// LoadPolicy is shared by planning and read faces. No policy is an explicit input:
// Plan reports no-policy, rather than pretending to know the loop's configuration.
func LoadPolicy(ref string) (*Policy, error) {
	if ref == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(ref)
	if err != nil {
		return nil, Usagef("reading the policy %s: %v", ref, err)
	}
	return decodePolicy(ref, raw)
}

func decodePolicy(ref string, raw []byte) (*Policy, error) {
	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, Usagef("reading the policy %s: %v", ref, err)
	}
	if err := p.Check(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Why runs Plan once, in the caller's pinned read transaction. Capacity is consumed
// in queue order exactly as it would be by the next pass, never independently per row.
func Why(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource) (WhyAnswer, error) {
	answer := WhyAnswer{Rows: []WhyRow{}, Summary: WhySummary{Held: map[string]int{}}}
	head, err := r.EventHeadSeq()
	if err != nil {
		return answer, readFailure(err)
	}
	answer.EventHeadSeq = head
	envs, err := Plan(r, tick, policy, questions)
	if err != nil {
		return answer, err
	}
	items, err := readItems(r)
	if err != nil {
		return answer, err
	}
	edges, err := readEdges(r)
	if err != nil {
		return answer, err
	}
	states := map[string]string{}
	for _, it := range items {
		states[it.ItemID] = it.State
	}
	at, err := tick.At()
	if err != nil {
		return answer, err
	}
	busy, err := readLanes(r, at)
	if err != nil {
		return answer, err
	}
	works, err := readPending(r)
	if err != nil {
		return answer, err
	}
	for _, work := range works {
		if !work.Done {
			busy[work.Decision.LaneID] = true
		}
	}
	var offered []string
	if tick.AvailableLanes != nil {
		offered = *tick.AvailableLanes
	} else {
		rows, err := r.Query(`SELECT lane_id FROM lanes ORDER BY lane_id`)
		if err != nil {
			return answer, readFailure(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return answer, readFailure(err)
			}
			offered = append(offered, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return answer, readFailure(err)
		}
	}
	seen := map[string]bool{}
	for _, lane := range offered {
		if !seen[lane] {
			answer.Summary.LanesOffered++
			if busy[lane] {
				answer.Summary.LanesBusy++
			}
			seen[lane] = true
		}
	}
	assigned := map[string]string{}
	for i, env := range envs {
		it := items[i]
		if it.State == StateLanded {
			continue
		}
		var d Decision
		if err := json.Unmarshal(env.Payload, &d); err != nil {
			return answer, Internalf("reading planner decision: %v", err)
		}
		class, where, mover := whyPresentation(d)
		row := WhyRow{Rank: it.Rank, ItemID: it.ItemID, State: it.State, Position: d.Position, Reason: d.Reason, Why: d.Reason, Class: class, Where: where, Mover: mover, Since: "current plan", ClassSource: "planner", WaitingOn: []string{}, Decision: d}
		switch {
		case d.Reason == ReasonQuestionOpen && d.QuestionID != "":
			row.WaitingOn = append(row.WaitingOn, d.QuestionID)
		case (d.Reason == ReasonForkHeld || d.Reason == ReasonTrunkBroken) && d.DeferredBy != "":
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		case d.Reason == ReasonBlocked:
			for _, id := range edges[it.ItemID] {
				if !terminalStates[states[id]] {
					row.WaitingOn = append(row.WaitingOn, id)
				}
			}
		case d.Action == ActionHold && (d.Reason == ReasonConflictDesign || d.Reason == ReasonConflictOwner) && d.ConflictID != "":
			row.WaitingOn = append(row.WaitingOn, d.ConflictID)
		case d.Reason == ReasonNoLane:
			for _, lane := range offered {
				if busy[lane] || assigned[lane] != "" {
					row.WaitingOn = append(row.WaitingOn, lane)
				}
			}
			if len(row.WaitingOn) == 0 {
				row.WaitingOn = append(row.WaitingOn, "offer a lane with --lanes")
			}
		case d.RunRequest != nil && (d.From == "running" || d.From == "candidate"):
			row.WaitingOn = append(row.WaitingOn, d.RunRequest.RunID)
		case (d.Reason == ReasonRetryJudged || d.Reason == ReasonRetryUnjudged || d.Reason == ReasonRetryStale) && d.SupersedesRunID != "":
			row.WaitingOn = append(row.WaitingOn, d.SupersedesRunID)
		}
		if len(row.WaitingOn) > 0 {
			row.Mover = strings.Join(row.WaitingOn, ", ")
		}
		if d.Action == ActionHold && (d.Reason == ReasonConflictDesign || d.Reason == ReasonConflictOwner) && d.BlockConflict != nil {
			row.Mover += "; " + d.BlockConflict.WaitingOn
		}
		if work := works[it.ItemID]; d.Reason == ReasonRepairStagingFailed && work != nil && work.RepairHold != nil {
			row.Why = d.Reason + ": " + work.RepairHold.Cause
			row.Mover = work.RepairHold.NextAction
		}
		if d.Action == ActionDispatch || d.Action == ActionRetry {
			assigned[d.LaneID] = d.ItemID
		}
		if it.State == "closed" || it.State == "resolved-elsewhere" {
			row.Class, row.Mover = "done", "no further work: "+it.State
		}
		if d.From == "running" {
			answer.Summary.Running++
		} else if d.Action != ActionHold {
			answer.Summary.CouldRun++
		} else {
			answer.Summary.Held[d.Reason]++
		}
		answer.Rows = append(answer.Rows, row)
	}
	return answer, nil
}

func whyPresentation(d Decision) (class, where, mover string) {
	where = "queue"
	if d.From == "running" || d.From == "candidate" {
		where = "lane"
		if d.From == "candidate" {
			where = "gate"
		}
		return "moving", where, "the next pass"
	}
	if d.Action != ActionHold {
		return "ready", where, "the next pass"
	}
	switch d.Reason {
	case ReasonBlocked:
		return "waits on work", where, "the owner; clear the item's blocked state"
	case ReasonNoLane, ReasonForkHeld:
		return "waits on work", where, "the next pass"
	case ReasonTrunkBroken:
		return "waits on work", "trunk", "the trunk fixer"
	case ReasonRepairStagingFailed:
		return "needs you", "land", "correct the reported staging cause"
	case ReasonConflictExhausted:
		return "needs a cause", "land", "diagnose the retained candidate"
	case ReasonGateFailed, ReasonRetryJudged:
		return "needs you", "gate", "inspect the verdict; tillr drain retry --help"
	case ReasonBoundaryScopeEscape, StopHostBoundaryViolation:
		return "needs a cause", "fence", "inspect the boundary report"
	case ReasonReadmissionExhausted:
		return "needs you", "lane", "automatic re-admission failed; run tillr drain retry after diagnosis"
	case ReasonBackendWindow:
		return "waits on work", "backend", "the backend reset time"
	case ReasonQuestionOpen:
		return "needs you", where, "answer the design question"
	case ReasonQuestionsUnknown:
		return "needs a cause", where, "restore the Vantage index command (--questions-cmd)"
	case ReasonDesignDocMissing:
		return "needs you", where, "restore the item's design document"
	case ReasonNoPolicy:
		return "needs you", where, "supply the loop's --policy file"
	case ReasonNoFence:
		return "needs you", where, "tillr item apply: give the item allowed_paths"
	case ReasonNotReady:
		return "needs you", where, "tillr item state: put the item in-queue"
	case ReasonConflictOwner:
		return "needs you", where, "the owner"
	case ReasonConflictDesign:
		return "waits on work", where, "design"
	default:
		return "needs a cause", "lane", "diagnose the run; tillr drain retry --help"
	}
}

// String prints a deterministic single-line summary, including every held reason.
func (s WhySummary) String() string {
	keys := make([]string, 0, len(s.Held))
	for reason := range s.Held {
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	counts := []string{}
	for _, reason := range keys {
		counts = append(counts, fmt.Sprintf("%s=%d", reason, s.Held[reason]))
	}
	return fmt.Sprintf("summary: lanes %d/%d busy; running %d; could run now %d; held [%s]", s.LanesBusy, s.LanesOffered, s.Running, s.CouldRun, strings.Join(counts, ", "))
}
