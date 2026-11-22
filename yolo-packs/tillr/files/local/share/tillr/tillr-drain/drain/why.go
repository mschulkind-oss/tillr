package drain

import (
	"database/sql"
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
	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
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
	works, err := readPending(r)
	if err != nil {
		return answer, err
	}
	liveRuns, runFaces, err := observeFirstSegmentFaces(r, tick.StorePath, tick, works)
	if err != nil {
		return answer, err
	}
	tick.runFaces = runFaces
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
	configured := offered
	tick.AvailableLanes = &configured
	// Match the fresh-start planner: an idle store row is not an offer if
	// the last rotation event was a refusal.
	refused, err := shadowRotation(r, &tick)
	if err != nil {
		return answer, readFailure(err)
	}
	offered = *tick.AvailableLanes
	answer.Lanes = offered
	answer.Draining = drainingLanes(works, configured)
	details := planDetails{maskedWorkers: make(map[string]Decision)}
	envs, err := planWithDetails(r, tick, policy, questions, def, &details)
	if err != nil {
		return answer, err
	}
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
	var runningLoop *liveLoopSession
	var lookedForLoop bool
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
		var observation workerObservation
		var liveWorkers []reportedWorker
		// A wait on the next dispatch, or the evaluator's retained handoff,
		// can hide a worker that the local process scan cannot inspect.
		pending, masked := details.maskedWorkers[it.ItemID]
		if !masked {
			pending = d
		}
		if mayMaskPendingWorker(pending) && tick.StorePath != "" {
			observation, err = observeWorkers(r, tick.StorePath, work, &liveRuns, &runFaces)
			if err != nil {
				return answer, err
			}
			if (masked || pending.Reason != ReasonRunLive) && (observation.foreign || observation.faces != nil) &&
				!work.Done && !work.LaneReleased && !work.BoundaryFailed && pending.LaneID == work.Decision.LaneID {
				liveWorkers, err = reportLiveWorkers(r, work, work.Decision, at, observation.faces)
				if err != nil {
					return answer, err
				}
				if len(liveWorkers) != 0 {
					d = work.Decision
					d.From, d.To, d.Action, d.Reason = "running", "", ActionHold, ReasonRunLive
					identifyDecision(&d)
				}
			}
			if !masked && d.Reason == ReasonRunLive && observation.endedAt.Valid && observation.live != nil && !slices.ContainsFunc(parallelNodes(work.Decision), func(node Decision) bool {
				return observation.live[node.RunRequest.RunID]
			}) {
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
		// Use the recorded fault for both its detail and its sequence-bound mover.
		if fault, held := itemFaults[it.ItemID]; held && d.Action == ActionHold && d.Reason == fault.Reason {
			if fault.Reason == ReasonPreserveFailed && tick.StorePath != "" {
				row.Mover += "; then " + fault.ClearCommand(tick.StorePath)
			} else if fault.Reason == ReasonDecisionCollision && tick.StorePath != "" {
				row.Mover = "operator; fix the drain defect, then " + fault.ClearCommand(tick.StorePath)
			} else {
				row.Mover = fault.CompleteMover(row.Mover, tick.StorePath)
			}
			if fault.Detail != "" {
				detail := fault.Detail
				if (fault.Reason == ReasonPreserveFailed || fault.Reason == ReasonDecisionCollision) && tick.StorePath != "" {
					detail = faultClearDetail(fault, tick.StorePath)
				}
				row.Why += ": " + detail
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
		if len(liveWorkers) == 0 && d.Reason == ReasonRunLive && d.RunRequest != nil && d.LaneID != "" && work != nil && !work.Examining && !work.Ready {
			liveWorkers, err = reportLiveWorkers(r, work, d, at, observation.faces)
			if err != nil {
				return answer, err
			}
		}
		liveWorker := len(liveWorkers) != 0
		var closedBlockers []string
		switch {
		case liveWorker:
			// The item's own run is doing the work, not blocking it.
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
		case d.Reason == ReasonConflictDesign && d.DeferredBy != "":
			rec, err := terminalRecord(r, d.DeferredBy)
			if err != nil {
				return answer, err
			}
			exit := ""
			if rec != nil && rec.ExitCode != nil {
				exit = fmt.Sprintf(" (exit %d)", *rec.ExitCode)
			}
			row.Why = "design run " + d.DeferredBy + " was interrupted" + exit + " without answering"
			row.tableReason = row.Why
			if slices.ContainsFunc(offered, func(lane string) bool {
				return !tick.OutLanes[lane] && !strings.HasPrefix(lane, "desk-") && (it.Express || !isExpressLane(lane))
			}) {
				row.Mover = "restart design when an eligible lane is free"
			} else {
				row.Class = "needs you"
				row.Mover = "offer an eligible lane with --lanes so drain can restart design"
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
			row.WaitingOn = laneWaitHolders(lanes, it.Express, busy, works, laneLeases, assigned, details.workerLanes)
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
			if !documentDesk(d.LaneID) && !slices.Contains(configured, d.LaneID) {
				// The lane is above the loop's count: the loop keeps serving it until this
				// work lands or is released, and offers it nothing new (drainingLanes).
				row.Why += ": lane " + d.LaneID + " is draining: above the loop's lane count, it finishes " + it.ItemID + " and takes nothing new"
			}
		case d.Reason == ReasonNoLane:
			if excluded := refusedLaneWaits(configured, it.Express, refused); len(excluded) != 0 {
				row.tableReason = "waiting for lane capacity (lanes out of rotation)"
				row.WaitingOn = append(laneWaitHolders(offered, it.Express, busy, works, leases, assigned, details.workerLanes), excluded...)
				row.Why = row.tableReason + ": " + strings.Join(row.WaitingOn, "; ")
				break
			}
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
		if liveWorker {
			locations, movers := []string{}, []string{}
			for _, worker := range liveWorkers {
				location := worker.id + " on " + d.LaneID
				movers = append(movers, "worker "+location)
				if worker.resumed {
					location += " (resumed)"
				}
				locations = append(locations, location)
			}
			row.Why += ": " + strings.Join(locations, ", ")
			row.Mover = strings.Join(movers, ", ")
			if observation.foreign {
				if len(d.Parallel) != 0 {
					row.Mover = "reader group on " + d.LaneID + " (individual processes are not observable here)"
					row.Why = d.Reason + ": unsettled reader claims: " + strings.Join(locations, ", ") + "; individual processes are not observable here"
				} else {
					row.Why += "; worker process is not observable here"
					row.Mover += " (worker process is not observable here)"
				}
			}
			if conflicts[it.ItemID].HasIndependentWait(waiting) {
				next := "; next dispatch waits on " + waiting.Waiter + ": " + waiting.Reason
				row.Why += next
				row.Mover += next // The queue-wide CLI table only shows the mover.
			}
			// Unbounded worker and examination leases have the same shape.
			// Here the pending work identifies the worker as the holder.
			if examining[d.LaneID] {
				delete(examining, d.LaneID)
				answer.Summary.LanesExamining--
				answer.Summary.LanesWorkers++
			}
			if tick.StorePath != "" && work.LoopID != "" {
				if !lookedForLoop {
					runningLoop, err = findLiveLoopSession(r, tick.StorePath, at)
					if err != nil {
						return answer, err
					}
					lookedForLoop = true
				}
				var earlierRuns []string
				for _, worker := range liveWorkers {
					predates, err := segmentPredatesReplacement(r, runningLoop, work.LoopID, worker.startedAt)
					if err != nil {
						return answer, err
					}
					if predates {
						earlierRuns = append(earlierRuns, worker.id)
					}
				}
				if len(earlierRuns) != 0 {
					timing := "; worker dispatch or resume precedes the current loop: " + strings.Join(earlierRuns, ", ")
					next := "; drain waits for its recorded finish"
					if len(earlierRuns) > 1 {
						next = "; drain acts on recorded worker finishes"
					}
					if observation.foreign && len(d.Parallel) != 0 {
						timing = "; reader allocations precede the current loop"
						next = "; drain acts on recorded worker finishes"
					}
					continuation := "; timing does not establish takeover"
					if observation.faces != nil {
						continuation = "; the observed worker continues executing its recorded segment; timing does not establish historical takeover"
						if confirmedReplacement(work.Owner, observation.endedAt, runningLoop) {
							continuation = "; the worker continues executing; the replacement loop does not take over that execution"
						}
					}
					why := timing + continuation + next
					row.Why += why
					row.Mover += why // The queue-wide CLI table has no separate explanation column.
				}
			}
		}
		if len(closedBlockers) > 0 {
			row.Class = "needs you"
			row.Mover = "remove the dependency or reopen the closed item (" + strings.Join(row.WaitingOn, ", ") + ")"
		}
		if d.Reason == ReasonBlocked || d.Reason == ReasonToolingDefect {
			if waiting != nil && waiting.Source == item.SplitWaitSource {
				required, err := item.SplitRequiredParts(r, it.ItemID)
				if err != nil {
					return WhyAnswer{}, readFailure(err)
				}
				splitWait(&row, required, states, def)
			} else if waiting != nil && waiting.Waiter != "" {
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
		if work != nil && !work.Done && d.Action == ActionHold && !liveWorker {
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
			if liveWorker && len(d.Parallel) != 0 && observation.faces != nil {
				if len(liveWorkers) == 1 {
					row.Where += " / node " + liveWorkers[0].nodeID
				}
			} else {
				row.Where += " / node " + d.Workflow.Stages[d.NodeIndex].ID
			}
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
			} else if liveWorker {
				// The worker has the next move, not drain.
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
	if tick.StorePath != "" {
		if !lookedForLoop {
			runningLoop, err = findLiveLoopSession(r, tick.StorePath, at)
			if err != nil {
				return answer, err
			}
		}
		if alarm := staleLoopBuildAlarm(runningLoop); alarm != nil {
			answer.Alarms = append(answer.Alarms, *alarm)
		}
	}
	answer.Summary.Alarms = answer.Alarms
	return answer, nil
}

type workerObservation struct {
	endedAt sql.NullInt64
	foreign bool
	live    map[string]bool
	faces   map[string]bool
}

// Check the same process markers as the reaper. Do not infer absence while a
// session could still be launching, but for shared claims require an actual
// run face before naming a particular reader as the worker.
func observeWorkers(r *store.Reader, path string, work *pendingWork, live, faces *map[string]bool) (workerObservation, error) {
	var obs workerObservation
	if work == nil || work.Owner == nil || work.Examining || work.Ready || work.LoopID == "" {
		return obs, nil
	}
	err := r.QueryRow(`SELECT (SELECT ended_at FROM drain_sessions WHERE session_id = ?)`, work.LoopID).Scan(&obs.endedAt)
	if err != nil || (!obs.endedAt.Valid && len(work.Decision.Parallel) == 0) {
		return obs, err
	}
	here, err := currentLoopOwner()
	if err != nil {
		return obs, err
	}
	obs.foreign = work.Owner.Domain != here.Domain || work.Owner.UID != here.UID
	if !obs.foreign {
		if *live == nil {
			*live, *faces, err = liveRunProcessIDs(path)
			if err != nil {
				return obs, err
			}
		}
		obs.live, obs.faces = *live, *faces
	}
	return obs, nil
}

func whyPresentation(d Decision) (class, where, mover string) {
	if d.Reason == whyRunAwaitsRecovery {
		return "waits on work", "lane", "reconcile the ended session's run with no surviving process"
	}
	if strings.HasPrefix(d.Reason, "prose-criteria-count: ") {
		return "needs you", "queue", "the owner; reconcile the original criteria"
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

type reportedWorker struct {
	id        string
	nodeID    string
	startedAt int64
	resumed   bool
}

// Report each actual worker without changing the planner's primary request,
// which identifies the pending group for dispatch and settlement. A sibling
// can keep the group's lease even if other claimed readers never started.
func reportLiveWorkers(r *store.Reader, work *pendingWork, d Decision, at time.Time, survivingRuns map[string]bool) ([]reportedWorker, error) {
	var workers []reportedWorker
	for _, node := range parallelNodes(d) {
		req := node.RunRequest
		if req == nil || (survivingRuns != nil && !survivingRuns[req.RunID]) {
			continue
		}
		segment := work.resumes[req.RunID]
		continued, err := liveContinuation(r, segment, node, at)
		if err != nil {
			return nil, err
		}
		if continued {
			workers = append(workers, reportedWorker{id: req.RunID, nodeID: req.NodeID, resumed: true, startedAt: segment.at.UnixMilli()})
			continue
		}
		leased, err := activeWorkerLease(r, req, node.LaneID, at)
		if err != nil {
			return nil, err
		}
		if !leased {
			continue
		}
		rec, err := terminalRecord(r, req.RunID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			continue
		}
		worker := reportedWorker{id: req.RunID, nodeID: req.NodeID}
		if work.LoopID != "" {
			// Each reader has its own allocation, including readers replaced
			// under a new build while the group keeps the same lane lease.
			err := r.QueryRow(`SELECT COALESCE((SELECT ts FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ?
			 AND json_extract(payload, '$.run_id') = ? ORDER BY seq DESC LIMIT 1), 0)`,
				run.AllocateKind, node.LaneID, req.RunID).Scan(&worker.startedAt)
			if err != nil {
				return nil, err
			}
		}
		workers = append(workers, worker)
	}
	return workers, nil
}

// Session ordering is not evidence of a takeover or a decision not to take
// over. ended_at is when the old session was observed to end, possibly later.
func segmentPredatesReplacement(r *store.Reader, current *liveLoopSession, dispatchID string, segmentStartedAt int64) (bool, error) {
	if current == nil || current.id == dispatchID || segmentStartedAt == 0 || segmentStartedAt >= current.startedAt {
		return false, nil
	}
	var prior bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM drain_sessions WHERE session_id = ? AND started_at <= ? AND ended_at IS NOT NULL)`, dispatchID, segmentStartedAt).Scan(&prior)
	return prior, err
}

// With a locally observed prior segment, distinct local process identities
// distinguish its owner from the replacement, even if the replacement recorded
// the old session's end after starting. For the same process identity, require
// the closed-before-open session boundary of an in-place exec.
func confirmedReplacement(segmentOwner *LoopOwner, endedAt sql.NullInt64, current *liveLoopSession) bool {
	return segmentOwner != nil && current != nil && endedAt.Valid &&
		segmentOwner.Domain == current.owner.Domain && segmentOwner.UID == current.owner.UID &&
		(*segmentOwner != current.owner || endedAt.Int64 <= current.startedAt)
}

type liveLoopSession struct {
	id        string
	startedAt int64
	lanes     []string
	owner     LoopOwner
}

// staleLoopBuildAlarm compares the loop session's start with the mtime of the
// executable serving why. This is the installed build the operator invoked;
// resolving it again through the launcher could itself build a different one.
func staleLoopBuildAlarm(loop *liveLoopSession) *alarms.Alarm {
	if loop == nil {
		return nil
	}
	here, err := currentLoopOwner()
	if err != nil || loop.owner.Domain != here.Domain || loop.owner.UID != here.UID {
		return nil // The recorded PID does not identify a process we can inspect here.
	}
	started := time.UnixMilli(loop.startedAt).UTC()
	installed, err := os.Stat("/proc/self/exe")
	if err != nil || !installed.ModTime().After(started) {
		return nil
	}
	image, err := os.Stat(fmt.Sprintf("/proc/%d/exe", loop.owner.PID))
	if err != nil || os.SameFile(image, installed) {
		return nil // A changed timestamp alone does not make the running build stale.
	}
	if dead, err := ownerDead(&loop.owner, here); err != nil || dead {
		return nil // Recheck identity after reading the process's executable.
	}
	builtAt := installed.ModTime().UTC().Format(time.RFC3339Nano)
	return &alarms.Alarm{
		Kind: "stale-loop-build", Backend: "drain", Since: builtAt,
		Cause:   fmt.Sprintf("loop started at %s; installed tillr-drain was built at %s", started.Format(time.RFC3339Nano), builtAt),
		Clears:  "the running loop uses the installed build",
		Command: "no automatic restart",
	}
}

// LiveLoopLanes returns the lane set recorded by a currently live loop session for
// storePath, if any.
func LiveLoopLanes(r *store.Reader, storePath string, now time.Time) ([]string, bool, error) {
	s, err := findLiveLoopSession(r, storePath, now)
	if s == nil {
		return nil, false, err
	}
	return s.lanes, true, nil
}

func findLiveLoopSession(r *store.Reader, storePath string, now time.Time) (*liveLoopSession, error) {
	rows, err := r.Query(`SELECT session_id, started_at, loop_owner, next_pass_at, COALESCE(lanes, '') FROM drain_sessions WHERE ended_at IS NULL AND loop_owner IS NOT NULL ORDER BY started_at DESC, rowid DESC`)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column") {
			return nil, nil
		}
		return nil, readFailure(err)
	}
	type candidate struct {
		id         string
		startedAt  int64
		ownerRaw   string
		nextPassAt int64
		lanesRaw   string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.startedAt, &c.ownerRaw, &c.nextPassAt, &c.lanesRaw); err != nil {
			rows.Close()
			return nil, readFailure(err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, readFailure(err)
	}
	rows.Close()

	here, _ := currentLoopOwner()
	identity, _ := processStoreIdentity(storePath)
	for _, c := range candidates {
		if c.lanesRaw == "" {
			continue
		}
		var lanes []string
		if err := json.Unmarshal([]byte(c.lanesRaw), &lanes); err != nil || lanes == nil {
			continue
		}
		var owner LoopOwner
		if err := json.Unmarshal([]byte(c.ownerRaw), &owner); err != nil {
			continue
		}
		session := &liveLoopSession{id: c.id, startedAt: c.startedAt, lanes: lanes, owner: owner}
		if here != nil && owner.Domain == here.Domain && owner.UID == here.UID {
			dead, err := ownerDead(&owner, here)
			if err == nil && !dead {
				return session, nil
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
				return session, nil
			}
			if err == nil && state == streamUnwritten {
				continue
			}
		}
		if now.UnixMilli() <= c.nextPassAt+SessionGrace.Milliseconds() {
			return session, nil
		}
	}
	return nil, nil
}

// laneWaitHolders names the holders in offer order, excluding desks and
// express lanes an ordinary item cannot take (the same eligibility as freeLane).
func laneWaitHolders(offered []string, express bool, busy map[string]bool, works map[string]*pendingWork, leases []LaneLease, assigned, reserved map[string]string) []string {
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

func refusedLaneWaits(configured []string, express bool, refused map[string]laneRefusal) []string {
	var waits []string
	for _, lane := range configured {
		refusal, ok := refused[lane]
		if !ok || strings.HasPrefix(lane, "desk-") || (!express && isExpressLane(lane)) {
			continue
		}
		wait := lane + " out of rotation (" + refusal.Reason + ")"
		if refusal.Detail != "" {
			wait += ": " + refusal.Detail
		}
		waits = append(waits, wait)
	}
	return waits
}

// acceptedConflictRetryCommand returns the exact `tillr drain retry` invocation
// accepted for a conflict-owner hold: when the conflict names a design run
// (`c.DesignRunID != ""`), `authorizeDesignRetry` expects `--run <c.DesignRunID>`
// with no `--acknowledge`; otherwise `AuthorizeRetry` expects the build retry
// command for `c.RunID` (including `--acknowledge` when the run was judged or unjudged).
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
