package drain

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-drain/judgegrants"
	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	whyRunAwaitsRecovery = "run-awaits-recovery"
	whyDispatchDeclined  = "dispatch-declined"
)

// WhyRow decorates a planner decision with references a human can follow. For a
// lost worker, Why reports the observed hold instead of the planner's run-live
// assumption. The presentation fields do not decide readiness.
type WhyRow struct {
	Place       int                    `json:"place,omitempty"`
	ItemID      string                 `json:"item_id"`
	State       string                 `json:"state"`
	Position    string                 `json:"position"`
	Reason      string                 `json:"reason"`
	Why         string                 `json:"why"`
	Class       string                 `json:"class"`
	Where       string                 `json:"where"`
	Mover       string                 `json:"mover"`
	Since       string                 `json:"since"`
	ClassSource string                 `json:"class_source"`
	WaitingOn   []string               `json:"waiting_on"`
	Decision    Decision               `json:"decision"`
	LaneStatus  string                 `json:"lane_status,omitempty"`
	LaneReason  string                 `json:"lane_reason,omitempty"`
	Refusals    map[string]int         `json:"refusals,omitempty"`
	Landing     *item.LandingReference `json:"landing,omitempty"`
	tableReason string
}

// TableReason is the short reason for the human-readable queue table. The
// machine-readable reason code remains unchanged.
func (row WhyRow) TableReason() string {
	if row.tableReason != "" {
		return row.tableReason
	}
	return row.Reason
}

type WhySummary struct {
	Alarms         []alarms.Alarm `json:"alarms,omitempty"`
	LanesBusy      int            `json:"lanes_busy"`
	LanesReserved  int            `json:"lanes_reserved"`
	LanesWorkers   int            `json:"lanes_workers"`
	LanesExamining int            `json:"lanes_examining"`
	LanesOffered   int            `json:"lanes_offered"`
	Running        int            `json:"running"`
	CouldRun       int            `json:"could_run"`
	Held           map[string]int `json:"held"`
	OwnerWaiting   int            `json:"owner_waiting"`
	DrainNext      int            `json:"drain_next"`
	DesignNext     int            `json:"design_next"`
	OperatorNext   int            `json:"operator_next"`
}

type WhyAnswer struct {
	Alarms       []alarms.Alarm `json:"alarms,omitempty"`
	Definition   string         `json:"definition"`
	EventHeadSeq int64          `json:"event_head_seq"`
	Lanes        []string       `json:"lanes"`
	LanesSource  string         `json:"lanes_source,omitempty"`
	// Draining lists the lanes above the offered count that still hold work, each with the
	// item it holds. The loop serves them until that work lands or is released.
	Draining []DrainingLane `json:"draining,omitempty"`
	Rows     []WhyRow       `json:"rows"`
	Summary  WhySummary     `json:"summary"`
	// Trunk and TrunkLine are set only when the trunk is unproved: TrunkLine is the one line
	// that says so, for how long, and which check is failing.
	Trunk     *heavy.HeavyStatus `json:"trunk,omitempty"`
	TrunkLine string             `json:"trunk_line,omitempty"`
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
	p.Ref = ref
	if err := p.Check(); err != nil {
		return nil, err
	}
	return &p, nil
}

// Why runs Plan once, in the caller's pinned read transaction. Capacity is consumed
// in queue order exactly as it would be by the next pass, never independently per row.
func Why(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource) (WhyAnswer, error) {
	return why(r, tick, policy, questions, "")
}

// WhyForItem includes a named landed item without adding landed history to the
// queue-wide report. The plan still considers the full queue for lane placement.
func WhyForItem(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource, itemID string) (WhyAnswer, error) {
	return why(r, tick, policy, questions, itemID)
}

func why(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource, namedItem string) (WhyAnswer, error) {
	answer := WhyAnswer{Rows: []WhyRow{}, Summary: WhySummary{Held: map[string]int{}}}
	head, err := r.EventHeadSeq()
	if err != nil {
		return answer, readFailure(err)
	}
	answer.EventHeadSeq = head
	refusals, err := readWhyRefusals(r)
	if err != nil {
		return answer, readFailure(err)
	}
	declines, err := readDispatchDeclines(r)
	if err != nil {
		return answer, readFailure(err)
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return answer, readFailure(err)
	}
	answer.Definition = declared.Name
	def := declared.Parsed
	var details planDetails
	envs, err := planWithDetails(r, tick, policy, questions, def, &details)
	if err != nil {
		return answer, err
	}
	items, err := readItems(r)
	if err != nil {
		return answer, err
	}
	answerCounts, err := answeredObstacleCounts(r)
	if err != nil {
		return WhyAnswer{}, err
	}
	conflicts, err := item.ReadConflicts(r)
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
	windows, err := readBackendWindows(r, at)
	if err != nil {
		return answer, readFailure(err)
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
		if !work.Done && !work.LaneReleased {
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
	answer.Lanes = make([]string, len(offered))
	copy(answer.Lanes, offered)
	answer.Draining = drainingLanes(works, offered)
	leases, err := activeLeases(r, at, offered)
	if err != nil {
		return answer, err
	}
	workers, examining := map[string]bool{}, map[string]bool{}
	for _, lease := range leases {
		if lease.LeasedUntil != nil {
			workers[lease.LaneID] = true
		} else {
			examining[lease.LaneID] = true
		}
	}
	seen := map[string]bool{}
	for _, lane := range offered {
		if !seen[lane] {
			answer.Summary.LanesOffered++
			if busy[lane] {
				answer.Summary.LanesBusy++
				if workers[lane] {
					answer.Summary.LanesWorkers++
				} else if examining[lane] {
					answer.Summary.LanesExamining++
				} else {
					answer.Summary.LanesReserved++
				}
			}
			seen[lane] = true
		}
	}
	// The planner promotes repair work independently of the owner's order.
	// Places describe keys, not that dispatch preference.
	ordered := append([]itemRow(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if (a.Rank == "") != (b.Rank == "") {
			return b.Rank == ""
		}
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		return a.ItemID < b.ItemID
	})
	places := map[string]int{}
	for _, it := range ordered {
		if item.InOrder(it.State) {
			places[it.ItemID] = len(places) + 1
		}
	}
	itemFaults, err := ReadItemFaults(r)
	if err != nil {
		return answer, readFailure(err)
	}
	obstacleHolds, _, err := ReadObstacleHolds(r)
	if err != nil {
		return answer, readFailure(err)
	}
	landedItems, err := obstacles.LandedItems(r)
	if err != nil {
		return answer, readFailure(err)
	}
	assigned := map[string]string{}
	var liveRuns map[string]bool
	for i, env := range envs {
		it := items[i]
		if (def.Completes == "landing" && it.State == StateLanded && it.ItemID != namedItem) || (def.Completes != "landing" && finishedState(def, it.State)) {
			continue
		}
		var d Decision
		if err := json.Unmarshal(env.Payload, &d); err != nil {
			return answer, Internalf("reading planner decision: %v", err)
		}
		// Some holds are planned with the next dispatch ordinal. Explain the
		// attempt that actually ran instead, including when an independent wait
		// masks a running replacement's decision. A continuation's persisted
		// decision and any planned dispatch keep their own attempt and run ID.
		next := details.nextOrdinals[it.ItemID]
		work := works[it.ItemID]
		if d.Action == ActionHold && !details.retryLaneWait[it.ItemID] && d.RunRequest == nil && next > 1 && d.Attempt == next &&
			(work == nil || work.Done || work.Decision.Attempt != d.Attempt) {
			d.Attempt = next - 1
			identifyDecision(&d)
		}
		if d.Reason == ReasonRunLive && tick.StorePath != "" {
			lost, err := lostRunAfterSession(r, tick.StorePath, works[it.ItemID], &liveRuns)
			if err != nil {
				return answer, err
			}
			if lost {
				d.From, d.Reason = "held", whyRunAwaitsRecovery
				identifyDecision(&d)
			}
		}
		planned := d
		var waiting *item.WaitingOn
		if it.State == def.Blocked {
			waiting, _, err = item.ReadWaitingOn(r, it.ItemID)
			if err != nil {
				return answer, err
			}
			// Settling a run that already started does not make the item dispatchable.
			// Keep the actual decision, but show what holds its next dispatch.
			if d.Action != ActionHold && conflicts[it.ItemID].HasIndependentWait(waiting) {
				d = namedWaitHold(def, obstacleHolds[it.ItemID], it.ItemID, *waiting, d.Attempt)
			}
		}
		class, where, mover := whyPresentation(d)
		row := WhyRow{Place: places[it.ItemID], ItemID: it.ItemID, State: it.State, Position: d.Position, Reason: d.Reason, Why: d.Reason, Class: class, Where: where, Mover: mover, Since: "current plan", ClassSource: "planner", WaitingOn: []string{}, Decision: planned}
		// A fault hold says what is wrong in its recorded detail.
		if fault, held := itemFaults[it.ItemID]; held && d.Action == ActionHold && d.Reason == fault.Reason && fault.Detail != "" {
			if fault.Reason == ReasonPreserveFailed && tick.StorePath != "" {
				row.Why += ": " + faultClearDetail(fault, tick.StorePath)
				row.Mover += "; then " + fault.ClearCommand(tick.StorePath)
			} else if fault.Reason == ReasonDecisionCollision && tick.StorePath != "" {
				row.Why += ": " + faultClearDetail(fault, tick.StorePath)
				row.Mover = "operator; fix the drain defect, then " + fault.ClearCommand(tick.StorePath)
			} else {
				row.Why += ": " + fault.Detail
			}
		}
		if obsHold, held := obstacleHolds[it.ItemID]; held && d.Action == ActionHold && d.Reason == obsHold.ReasonCode() {
			row.Why = obsHold.WhyText()
			row.Mover = obsHold.Action
		}
		switch d.Reason {
		case ReasonEvalFailed, ReasonJudgeUnreadable, ReasonJudgeDefectiveMismatch, ReasonJudgeCloseRefused:
			detail, err := judgeFailureDetail(r, it.ItemID, d.Reason)
			if err != nil {
				return answer, err
			}
			if detail != "" {
				row.Why += ": " + detail
			}
		}
		var closedBlockers []string
		switch {
		case d.Reason == ReasonQuestionRemoved:
			ref := conflicts[it.ItemID].QuestionRef
			row.WaitingOn = append(row.WaitingOn, ref)
			row.Why += ": " + ref + ": the question was removed without a ruling"
			row.Mover = "restore " + ref + " under the item's block and land it"
		case d.Reason == ReasonQuestionOpen && d.QuestionID != "":
			row.WaitingOn = append(row.WaitingOn, d.QuestionID)
		case (d.Reason == ReasonBackendClosed || d.BackendHoldCause != "") && d.DeferredBy != "":
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		case (d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy || d.Reason == ReasonForkHeld || d.Reason == ReasonTrunkBroken || d.Reason == "awaits-rewrite" || strings.HasPrefix(d.Reason, ReasonRedCap+": ")) && d.DeferredBy != "":
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		case d.Reason == ReasonOwnerOnlyOwed && it.OwnerOwed != nil:
			row.Why, row.Mover = it.OwnerOwed.Explanation(), it.OwnerOwed.Mover()
		case d.Reason == ReasonBlocked:
			for _, id := range edges[it.ItemID] {
				if !blockerSatisfied(def, states[id]) {
					row.WaitingOn = append(row.WaitingOn, id)
					if states[id] == def.Closed || states[id] == "closed" {
						closedBlockers = append(closedBlockers, id)
					}
				}
			}
			if len(row.WaitingOn) > 0 {
				row.Why += ": waiting on " + strings.Join(row.WaitingOn, ", ")
				if len(closedBlockers) > 0 {
					row.Why += "; " + strings.Join(closedBlockers, ", ") + " closed without landing"
				}
			}
		case d.Action == ActionHold && (d.Reason == ReasonConflictDesign || d.Reason == ReasonConflictOwner) && d.ConflictID != "":
			row.WaitingOn = append(row.WaitingOn, d.ConflictID)
			if d.Reason == ReasonConflictOwner && d.DeferredBy != "" {
				row.WaitingOn = append(row.WaitingOn, "design run "+d.DeferredBy)
			}
		case d.Action == ActionRetry && d.RetryID != "" && (work == nil || work.Done):
			row.Why = "operator retry planned for " + d.LaneID + " (lane is free)"
			row.tableReason = row.Why
			row.Mover = "dispatch the authorized retry on " + d.LaneID
		case d.Reason == ReasonNoLane && details.retryLaneWait[it.ItemID]:
			lanes, laneLeases := offered, leases
			if d.LaneID != "" {
				lanes = []string{d.LaneID} // a retained candidate needs its own lane
				if !slices.Contains(offered, d.LaneID) {
					laneLeases, err = activeLeases(r, at, lanes)
					if err != nil {
						return answer, err
					}
				}
			}
			row.Why = "operator retry waiting for a lane"
			row.tableReason = row.Why
			row.WaitingOn = retryLaneHolders(lanes, it.Express, busy, works, laneLeases, assigned, details.workerLanes)
			if len(row.WaitingOn) == 0 {
				row.Why += ": no eligible lanes offered"
				row.WaitingOn = append(row.WaitingOn, "offer an eligible lane with --lanes")
			} else {
				row.Why += ": " + strings.Join(row.WaitingOn, "; ")
			}
		case d.Reason == ReasonNoLane && d.Returned != nil && d.Returned.Cause == ReasonEvalFailed && d.LaneID != "":
			row.WaitingOn = append(row.WaitingOn, d.LaneID)
			row.Why += ": own lane " + d.LaneID + " holds the offered commit; waiting for that lane to become available"
		case d.Reason == ReasonNoLane && works[it.ItemID] != nil && !works[it.ItemID].Done && !works[it.ItemID].LaneReleased && d.LaneID != "":
			// Work that still holds its lane continues only in that worktree: it is held
			// behind that lane's lease (its run, or its examination), not by the others.
			row.WaitingOn = append(row.WaitingOn, d.LaneID)
			if !documentDesk(d.LaneID) && !slices.Contains(offered, d.LaneID) {
				// The lane is above the loop's count: the loop keeps serving it until this
				// work lands or is released, and offers it nothing new (drainingLanes).
				row.Why += ": lane " + d.LaneID + " is draining: above the loop's lane count, it finishes " + it.ItemID + " and takes nothing new"
			}
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
		case (d.Reason == ReasonRetryJudged || d.Reason == ReasonRetryUnjudged) && d.SupersedesRunID != "":
			row.WaitingOn = append(row.WaitingOn, d.SupersedesRunID)
		}
		if d.Reason == ReasonBackendWindow && d.ConflictID != "" && policy != nil && d.BackendHoldCause == "" {
			seat := d.Seat
			for _, name := range policy.seatList(seat) {
				if reset, shut := windows[name]; shut {
					row.WaitingOn = append(row.WaitingOn, name+" reopens "+reset.Format(time.RFC3339))
				}
			}
			row.Why += ": seat " + seat + "; " + strings.Join(row.WaitingOn, ", ")
		}
		if len(row.WaitingOn) > 0 && d.Reason != ReasonQuestionRemoved {
			row.Mover = strings.Join(row.WaitingOn, ", ")
		}
		if d.BackendHoldCause != "" {
			row.Why += ": " + d.DeferredBy + ": " + d.BackendHoldCause
			row.Mover = d.BackendHoldMover
		}
		if len(closedBlockers) > 0 {
			row.Class = "needs you"
			row.Mover = "remove the dependency or reopen the closed item (" + strings.Join(row.WaitingOn, ", ") + ")"
		}
		if d.Reason == ReasonBlocked || d.Reason == ReasonToolingDefect {
			if waiting != nil && waiting.Waiter != "" {
				row.Class, row.Mover, row.Why = "waits on work", waiting.Waiter, waiting.Reason
				row.ClassSource = "store"
				if !slices.Contains(row.WaitingOn, waiting.Waiter) {
					row.WaitingOn = append(row.WaitingOn, waiting.Waiter)
				}
				if waiting.Waiter == "owner" {
					row.Class = "needs you"
				} else if depID, ok := strings.CutPrefix(waiting.Waiter, "item:"); ok && (states[depID] == def.Closed || states[depID] == "closed") {
					row.Class = "needs you"
					row.Why = depID + " closed without landing"
					if strings.TrimSpace(waiting.Reason) != "" {
						row.Why += ": " + waiting.Reason
					}
					row.Mover = "remove the dependency or reopen the closed item (" + depID + ")"
				} else if len(closedBlockers) > 0 {
					row.Why += "; " + strings.Join(closedBlockers, ", ") + " closed without landing"
				}
			}
		}
		if d.Action == ActionHold && (d.Reason == ReasonConflictDesign || d.Reason == ReasonConflictOwner) && d.BlockConflict != nil {
			row.Mover += "; " + d.BlockConflict.WaitingOn
		}
		if work := works[it.ItemID]; d.Reason == ReasonRepairStagingFailed && work != nil && work.RepairHold != nil {
			row.Why = d.Reason + ": " + work.RepairHold.Cause
			row.Mover = work.RepairHold.NextAction
		}
		if c := d.BlockConflict; c != nil && c.TriageVersion != "" {
			row.Why += ": " + c.Text
			if c.WaitingOn == "owner" {
				if landedItems[it.ItemID] {
					row.Mover = "the owner; answer the conflict"
				} else {
					row.Mover = "the owner; close the item or answer the conflict"
				}
			} else {
				row.Mover = "triage on the design seat"
			}
		}
		if d.Reason == ReasonJudgeGrantAbsent {
			expired, err := judgegrants.ExpiredForItem(r, it.ItemID, at)
			if err != nil {
				return answer, readFailure(err)
			}
			detail, mover := judgegrants.FormatHold(expired, tick.StorePath)
			row.Why = d.Reason + ": " + detail
			row.Mover = mover
		}
		if work := works[it.ItemID]; work != nil && !work.Done && d.Action == ActionHold {
			row.LaneStatus = "kept"
			if work.LaneReleased {
				row.LaneStatus = "released"
			} else {
				row.LaneReason = work.LaneKeepReason
				if row.LaneReason == "" {
					row.LaneReason = candidateKeepReason(work, d.Reason)
				}
				if row.LaneReason != "" && d.Reason == ReasonDecisionCollision && work.Candidate == nil && work.Decision.RunRequest != nil {
					row.LaneReason = fmt.Sprintf("%s still owns %s: %s", work.Decision.RunRequest.RunID, work.Decision.LaneID, row.LaneReason)
				}
				if row.LaneReason == "" {
					row.LaneReason = "candidate awaits pin verification and lane release"
				}
			}
			row.Why += "; lane " + row.LaneStatus
			if row.LaneReason != "" {
				row.Why += ": " + row.LaneReason
			}
		}
		if d.Reason == ReasonTrunkBroken && d.DeferredBy != "" {
			detail, owner, err := TrunkFixerWait(r, d.DeferredBy)
			if err != nil {
				return answer, err
			}
			row.Why += ": " + detail
			row.Mover = detail
			if owner {
				row.Class = "needs you"
			}
		}
		if d.Workflow != nil {
			row.Where += " / node " + d.Workflow.Stages[d.NodeIndex].ID
			outcomes, err := workflowOutcomes(r, d.ItemID, d.Attempt)
			if err != nil {
				return answer, err
			}
			passed := 0
			for _, outcome := range outcomes {
				if outcome == "passed" {
					passed++
				}
			}
			row.Why += fmt.Sprintf("; %d/%d nodes passed", passed, len(d.Workflow.Stages))
		}
		if planned.Action == ActionDispatch || planned.Action == ActionRetry {
			assigned[planned.LaneID] = planned.ItemID
		}
		if it.State == "closed" || it.State == "resolved-elsewhere" || it.State == StateLanded {
			row.Class, row.Mover = "done", "no further work: "+it.State
		}
		if it.State == StateLanded {
			row.Landing, err = item.CurrentLanding(r, it.ItemID)
			if err != nil {
				return answer, readFailure(err)
			}
		}
		if d.Action == ActionHold && row.Class != "done" {
			if d.Reason == ReasonQuestionOpen && d.QuestionID != "" {
				row.Mover = "owner; answer " + it.DesignDoc + "#" + d.QuestionID
				if questions != nil {
					if doc, err := questions.Document(it.DesignDoc); err == nil {
						for _, q := range doc.Questions {
							if q.ID == d.QuestionID && q.Title != "" {
								row.Why += ": " + q.Title
							}
						}
					}
				}
				answer.Summary.OwnerWaiting++
			} else if obsHold, held := obstacleHolds[it.ItemID]; held && d.Reason == obsHold.ReasonCode() {
				row.Mover = obsHold.MoverText()
				switch obsHold.Mover {
				case "owner":
					row.Class = "needs you"
					answer.Summary.OwnerWaiting++
				case "drain":
					row.Class = "waits on work"
					answer.Summary.DrainNext++
				default:
					row.Class = "needs you"
					answer.Summary.OperatorNext++
				}
			} else if d.Reason == ReasonOwnerOnlyOwed || (row.ClassSource == "store" && row.Mover == "owner") {
				answer.Summary.OwnerWaiting++
			} else if (d.Reason == ReasonForkHeld && d.DeferredBy != "") || (row.ClassSource == "store" && row.Class == "waits on work" && row.Mover != "plan" && row.Mover != "design") {
				// A fork holder or named waiter is the mover, not the drain.
				// Planning waits still use their existing design-seat action below.
			} else {
				actor := "drain"
				if d.Reason == ReasonConflictDesign || d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy {
					actor = "design seat"
				}
				if row.Mover == "plan" || row.Mover == "design" || strings.Contains(row.Mover, "design seat") {
					actor = "design seat"
				}
				if row.Class == "alarm" || row.Class == "needs you" || row.Class == "needs a cause" || strings.HasPrefix(d.Reason, ReasonTrunkPathDirty+": ") {
					actor = "operator"
				}
				if d.Reason == ReasonConflictOwner {
					c := conflicts[it.ItemID]
					isReq, _ := isFiledRequest(r, it.ItemID)
					hasLanded := landedItems[it.ItemID]
					switch {
					case IsGeneratedItem(it.ItemID):
						// A generated item never has a document, so it is never triaged for one: it
						// reaches the owner only by the loop's written question or an objection naming
						// him (reportedBlock). Show that question, not triage wording.
						actor = "owner"
						if q := loopOwnerQuestion(c.Text); q != "" {
							if hasLanded {
								q = obstacles.StripCloseRemedy(q)
							}
							row.Why += ": " + q
						} else {
							row.Why += ": run " + c.RunID + "'s report names the owner as its next actor (conflict " + c.ConflictID + ")"
						}
						if hasLanded {
							if cmd := acceptedConflictRetryCommand(r, it.ItemID, c); cmd != "" {
								row.Mover = "answer conflict " + c.ConflictID + " with " + cmd
							} else {
								row.Mover = "answer conflict " + c.ConflictID
							}
						} else {
							row.Mover = "answer conflict " + c.ConflictID + ", or close the item (tillr item close --item " + it.ItemID + ")"
						}
					case isReq:
						cmd := acceptedConflictRetryCommand(r, it.ItemID, c)
						if obs := parseObstacle(workerConflictReport(c.Text)); obs.Kind == ObstacleOwner && obs.Field("QUESTION") != "" {
							actor = "owner"
							row.Why += ": " + collapseSingleLine(obs.Field("QUESTION"))
							if hasLanded {
								row.Mover = "answer with " + cmd
							} else {
								row.Mover = "answer with " + cmd + ", or close the item (tillr item close --item " + it.ItemID + ")"
							}
						} else {
							actor = "operator"
							para := firstParagraph(c.Text)
							if hasLanded {
								para = obstacles.StripCloseRemedy(para)
							}
							row.Why += ": " + para
							if hasLanded {
								row.Mover = "deliver a revised instruction with " + cmd
							} else {
								row.Mover = "deliver a revised instruction with " + cmd + ", or close the item (tillr item close --item " + it.ItemID + ")"
							}
						}
					case c.BodyRef == "":
						actor, row.Why = "operator", row.Why+": no document exists; name or triage one for "+it.ItemID
						row.Mover = "name or triage the item's document"
					case strings.Contains(strings.ToLower(ownerReport(c)), "stale") || strings.Contains(strings.ToLower(ownerReport(c)), "restage"):
						actor, row.Mover = "drain", "repair the staging fault; no owner choice was reported"
					default:
						if _, ok := item.OwnerDecision(ownerReport(c)); ok {
							actor, row.Mover = "drain", "convert the decision into a gated document question (--migrate-owner-questions)"
						} else {
							actor, row.Mover = "operator", "request the design seat's question and alternatives, or resolve the recorded hold"
						}
					}
				}
				if strings.HasPrefix(d.Reason, ReasonRunLinkMissing) {
					runID, whyDetail, moverAction := runLinkMissingHold(r, it.ItemID, d.SupersedesRunID)
					actor = "operator"
					if runID != "" {
						row.WaitingOn = append(row.WaitingOn, runID)
					}
					if row.Why == ReasonRunLinkMissing {
						row.Why = ReasonRunLinkMissing + ": " + whyDetail
					} else {
						// The planner already named what it found; the retry command follows it.
						row.Why += "; " + whyDetail
					}
					row.Mover = moverAction
				}
				if d.Reason == ReasonQuestionRemoved {
					row.Mover = actor + "; " + row.Mover
				} else if !details.retryLaneWait[it.ItemID] || d.Reason != ReasonNoLane {
					row.Mover = actor + "; " + nextActorAction(row.Mover)
				}
				switch actor {
				case "owner":
					row.Class = "needs you"
					answer.Summary.OwnerWaiting++
				case "drain":
					answer.Summary.DrainNext++
				case "design seat":
					answer.Summary.DesignNext++
				case "operator":
					answer.Summary.OperatorNext++
				}
			}
			if landedItems[it.ItemID] {
				row.Why = obstacles.StripCloseRemedy(row.Why)
				row.Mover = obstacles.StripCloseRemedy(row.Mover)
			}
		}
		// Keep the planner's decision for inspection. The loop's observation
		// describes the last claim of this offer; it does not veto a new claim.
		decline := declines[it.ItemID]
		declined, err := decline.appliesTo(r, d)
		if err != nil {
			return answer, readFailure(err)
		}
		if declined {
			row.tableReason = ""
			row.Reason, row.Class, row.ClassSource = whyDispatchDeclined, "waits on work", "loop"
			row.Why = "last dispatch on " + decline.LaneID + " declined: " + collapseSingleLine(decline.Reason)
			row.Since = "last dispatch refusal"
			row.Mover = decline.WaitingOn
			row.WaitingOn = []string{decline.WaitingOn}
			answer.Summary.Held[row.Reason]++
		} else if d.From == "running" {
			answer.Summary.Running++
		} else if d.Action != ActionHold {
			answer.Summary.CouldRun++
		} else {
			answer.Summary.Held[d.Reason]++
		}
		if c := conflicts[it.ItemID]; d.Reason == ReasonConflictOwner && strings.Contains(c.Text, repeatedObstacleMarker) {
			row.Why = collapseSingleLine(c.Text)
			row.Mover = repeatedObstacleActor(c) + "; resolve the recorded hold"
			if cmd := acceptedConflictRetryCommand(r, it.ItemID, c); cmd != "" {
				row.Mover = repeatedObstacleActor(c) + "; answer with " + cmd
			}
			row.Mover += "; " + row.Why
		}
		if count := answerCounts[it.ItemID]; count > 0 {
			row.Mover += fmt.Sprintf("; answered obstacles: %d", count)
		}
		if steps := refusals[it.ItemID]; len(steps) > 0 {
			row.Refusals = steps
			row.Why += "; refusals: " + refusalStepsWords(steps)
		}
		answer.Rows = append(answer.Rows, row)
	}
	current := make([]alarms.Hold, 0, len(answer.Rows))
	for _, row := range answer.Rows {
		d := row.Decision
		current = append(current, alarms.Hold{Item: row.ItemID, Lane: d.LaneID, Reason: row.Reason, Backends: d.DeferredBy, Judge: d.Seat == "review" && d.From == "candidate"})
	}
	answer.Alarms, err = alarms.Read(r, at, alarmPolicy(policy, time.Time{}), current...)
	if err != nil {
		return answer, readFailure(err)
	}
	answer.Summary.Alarms = answer.Alarms
	return answer, nil
}

// An ended dispatch session can still have live workers. Check the same process
// markers the reaper uses, once per answer, and do not infer absence in another
// process domain or while a live session may still be launching the run.
func lostRunAfterSession(r *store.Reader, path string, work *pendingWork, live *map[string]bool) (bool, error) {
	if work == nil || work.Owner == nil || work.Examining || work.Ready || work.LoopID == "" {
		return false, nil
	}
	here, err := currentLoopOwner()
	if err != nil {
		return false, err
	}
	if work.Owner.Domain != here.Domain || work.Owner.UID != here.UID {
		return false, nil
	}
	ended, err := dispatchSessionEnded(r, work.LoopID)
	if err != nil || !ended {
		return false, err
	}
	if *live == nil {
		*live, err = liveRunIDs(path)
		if err != nil {
			return false, err
		}
	}
	for _, node := range parallelNodes(work.Decision) {
		if (*live)[node.RunRequest.RunID] {
			return false, nil
		}
	}
	return true, nil
}

func whyPresentation(d Decision) (class, where, mover string) {
	if d.Reason == whyRunAwaitsRecovery {
		return "waits on work", "lane", "reconcile the ended session's run with no surviving process"
	}
	if strings.HasPrefix(d.Reason, "prose-criteria-count: ") {
		return "needs you", "queue", "the owner; reconcile the original criteria"
	}
	if strings.HasPrefix(d.Reason, ReasonForeignIdentity+": ") {
		return "needs you", "trunk", "re-author the commit, or record its id with tillr-drain accept-identity"
	}
	if strings.HasPrefix(d.Reason, ReasonRedCap+": ") {
		return "waits on work", "land", "the heavy runner; prove trunk or file its fixer"
	}
	if d.Action == ActionHold && (d.Reason == ReasonBackendClosed || d.Reason == ReasonBackendWindow) {
		rule := holdrules.For(d.Reason)
		return rule.Class, rule.Where, rule.Mover
	}
	where = "queue"
	if c := d.BlockConflict; c != nil && c.TriageVersion != "" && c.WaitingOn == "design" {
		if d.From == "running" || d.From == "candidate" {
			return "moving", "lane", "triage on the design seat"
		}
		return "waits on work", where, "triage on the design seat"
	}
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
	if strings.HasPrefix(d.Reason, "node-failed: ") {
		return "needs you", "lane", "inspect the named node and authorize a retry"
	}
	if strings.HasPrefix(d.Reason, ReasonStagingFailed+": ") {
		lane := strings.TrimPrefix(d.Reason, ReasonStagingFailed+": ")
		return "needs you", "lane", "correct the reported staging cause on " + lane + "; then tillr drain retry or clear the lane condition"
	}
	switch d.Reason {
	case "awaits-rewrite":
		return "waits on work", where, "the prose rewrite to land"
	case ReasonBlocked:
		return "waits on work", where, "the owner; clear the item's blocked state"
	case ReasonDeskCapacity, ReasonDocumentDeskBusy:
		return "waits on work", "desk", d.DeferredBy
	case ReasonForkHeld:
		return "waits on work", where, d.DeferredBy
	case ReasonNoLane:
		return "waits on work", where, "the next pass"
	case ReasonTrunkBroken:
		return "waits on work", "trunk", "the trunk fixer"
	case ReasonTrunkUnproved:
		return "waits on work", "trunk", "the heavy runner; prove the trunk"
	case ReasonRepairStagingFailed:
		return "needs you", "land", "correct the reported staging cause"
	case ReasonConflictExhausted:
		// History only: the owner's ruling of 2026-11-17 returns such an item instead.
		return "ready", "queue", "the next pass"
	case ReasonJudgeGrantAbsent:
		return "needs you", where, "issue a grant covering the item's criteria: " + judgegrants.IssueCommand("")
	case ReasonJudgeUnreadable:
		return "needs a cause", "judge", "inspect the judge run"
	case ReasonEvalFailed:
		return "waits on work", "judge", "amend the candidate against the judge's reason"
	case ReasonGateFailed, ReasonRetryJudged:
		return "needs you", "gate", "inspect the verdict; tillr drain retry --help"
	case RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome:
		return "needs a cause", "fence", "inspect the idea outcome and authorize a retry"
	case ReasonBoundaryScopeEscape, StopHostBoundaryViolation:
		return "needs a cause", "fence", "inspect the boundary report"
	case ReasonInfraExhausted:
		return "waits on work", "backend", "a successful tillr-drain probe-backend or a non-infrastructure run"
	case ReasonCommandFault:
		return "needs a cause", "lane", "repair the command process and authorize a retry"
	case ReasonQuestionRemoved:
		rule := holdrules.For(d.Reason)
		return rule.Class, rule.Where, rule.Mover
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
	case ReasonDecisionCollision, ReasonSettleFailed:
		return "needs a cause", "loop", "fix the drain defect the fault names, then tillr drain clear-fault"
	default:
		// A reason with a recorded rule and no case above is shown by that rule. Only a
		// reason with no rule at all is a run to diagnose.
		if rule := holdrules.For(d.Reason); rule != holdrules.Unknown {
			return rule.Class, rule.Where, rule.Mover
		}
		return "needs a cause", "lane", "diagnose the run; tillr drain retry --help"
	}
}

func nextActorAction(mover string) string {
	for _, prefix := range []string{"the owner; ", "owner; ", "the operator; ", "operator; ", "operator: ", "design seat; ", "drain; "} {
		mover = strings.TrimPrefix(mover, prefix)
	}
	switch mover {
	case "owner", "the owner", "operator", "the operator":
		return "resolve the recorded hold"
	case "plan", "design":
		return "resolve the recorded planning hold"
	default:
		return mover
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
	return fmt.Sprintf("summary: lanes %d/%d busy; reserved %d; workers %d; examining %d; running %d; could run now %d; owner-waiting %d; next [drain=%d, design seat=%d, operator=%d]; held [%s]", s.LanesBusy, s.LanesOffered, s.LanesReserved, s.LanesWorkers, s.LanesExamining, s.Running, s.CouldRun, s.OwnerWaiting, s.DrainNext, s.DesignNext, s.OperatorNext, strings.Join(counts, ", "))
}

// loopOwnerQuestion is the last question the loop wrote into a conflict for the owner
// (generatedQuestion, generatedRepeatQuestion): the paragraph that starts with
// "Question for the owner". Empty when the conflict carries none.
func loopOwnerQuestion(text string) string {
	i := strings.LastIndex(text, "Question for the owner")
	if i < 0 {
		return ""
	}
	q := text[i:]
	if end := strings.Index(q, "\n\n"); end >= 0 {
		q = q[:end]
	}
	return strings.TrimSpace(q)
}

// LiveLoopLanes returns the lane set recorded by a currently live loop session for
// storePath, if any.
func LiveLoopLanes(r *store.Reader, storePath string, now time.Time) ([]string, bool, error) {
	rows, err := r.Query(`SELECT loop_owner, next_pass_at, COALESCE(lanes, '') FROM drain_sessions WHERE ended_at IS NULL AND loop_owner IS NOT NULL ORDER BY started_at DESC, rowid DESC`)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column") {
			return nil, false, nil
		}
		return nil, false, readFailure(err)
	}
	type candidate struct {
		ownerRaw   string
		nextPassAt int64
		lanesRaw   string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.ownerRaw, &c.nextPassAt, &c.lanesRaw); err != nil {
			rows.Close()
			return nil, false, readFailure(err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, false, readFailure(err)
	}
	rows.Close()

	here, _ := currentLoopOwner()
	identity, _ := processStoreIdentity(storePath)
	for _, c := range candidates {
		if c.lanesRaw == "" {
			continue
		}
		var lanes []string
		if err := json.Unmarshal([]byte(c.lanesRaw), &lanes); err != nil || len(lanes) == 0 {
			continue
		}
		var owner LoopOwner
		if err := json.Unmarshal([]byte(c.ownerRaw), &owner); err != nil {
			continue
		}
		if here != nil && owner.Domain == here.Domain && owner.UID == here.UID {
			dead, err := ownerDead(&owner, here)
			if err == nil && !dead {
				return lanes, true, nil
			}
			continue
		}
		if here != nil && !presence.SameBoot(owner.Domain, here.Domain) {
			// A session recorded before this boot cannot be live, whatever its grace.
			continue
		}
		if identity != "" {
			state, err := openForWriting(loopPresencePath(identity, owner))
			if err == nil && state == streamWritten {
				return lanes, true, nil
			}
			if err == nil && state == streamUnwritten {
				continue
			}
		}
		if now.UnixMilli() <= c.nextPassAt+SessionGrace.Milliseconds() {
			return lanes, true, nil
		}
	}
	return nil, false, nil
}

// acceptedConflictRetryCommand returns the exact `tillr drain retry` invocation
// accepted for a conflict-owner hold: when the conflict names a design run
// (`c.DesignRunID != ""`), `authorizeDesignRetry` expects `--run <c.DesignRunID>`
// with no `--acknowledge`; otherwise `AuthorizeRetry` expects the build retry
// command for `c.RunID` (including `--acknowledge` when the run was judged or unjudged).
// retryLaneHolders names the holders in offer order, excluding desks and
// express lanes an ordinary item cannot take (the same eligibility as freeLane).
func retryLaneHolders(offered []string, express bool, busy map[string]bool, works map[string]*pendingWork, leases []LaneLease, assigned, reserved map[string]string) []string {
	var waits []string
	seen := map[string]bool{}
	for _, lane := range offered {
		if seen[lane] || strings.HasPrefix(lane, "desk-") || (!express && isExpressLane(lane)) {
			continue
		}
		seen[lane] = true
		var holders []string
		if busy[lane] {
			for id, work := range works {
				if work.Done || work.LaneReleased || work.Decision.LaneID != lane {
					continue
				}
				holder := "unfinished work for " + id
				if req := work.Decision.RunRequest; req != nil && req.RunID != "" {
					holder += " (run " + req.RunID + ")"
				}
				holders = append(holders, holder)
			}
			sort.Strings(holders)
			for _, lease := range leases {
				if lease.LaneID != lane {
					continue
				}
				// A lease with no expiry can be an examination or an unbounded run.
				holder := "active lease"
				if lease.LeasedUntil != nil {
					holder = "run lease"
				}
				if lease.RunID != "" {
					holder += " for " + lease.RunID
				}
				holders = append(holders, holder)
			}
		}
		if id := assigned[lane]; id != "" {
			holders = append(holders, "earlier planned item "+id)
		} else if id := reserved[lane]; id != "" {
			holders = append(holders, "reserved for "+id)
		}
		if len(holders) == 0 {
			waits = append(waits, lane+" is unavailable in this plan")
		} else {
			waits = append(waits, lane+" held by "+strings.Join(holders, " and "))
		}
	}
	return waits
}

func acceptedConflictRetryCommand(r *store.Reader, itemID string, c item.Conflict) string {
	if c.DesignRunID != "" {
		return retryCommand(itemID, c.DesignRunID, "")
	}
	if c.RunID == "" {
		return ""
	}
	cmd, err := acceptedBuildRetryCommand(r, itemID, c.RunID)
	if err != nil {
		return retryCommand(itemID, c.RunID, "")
	}
	return cmd
}
