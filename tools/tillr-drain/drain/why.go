package drain

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-drain/internal/splitclosing"
	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watchlinks"
)

const (
	whyRunAwaitsRecovery = "run-awaits-recovery"
	whyDispatchDeclined  = "dispatch-declined"
)

// WhySplitHeldPart describes a held part surfaced on its split parent's row.
type WhySplitHeldPart struct {
	ItemID string `json:"item_id"`
	Label  string `json:"label"`
	Reason string `json:"reason"`
	Mover  string `json:"mover"`
}

// WhySplitParent summarizes a split parent's parts in one queue row.
type WhySplitParent struct {
	TotalParts      int                `json:"total_parts"`
	RequiredParts   int                `json:"required_parts"`
	RequiredLanded  int                `json:"required_landed"`
	Landed          int                `json:"landed"`
	Working         int                `json:"working"`
	Held            int                `json:"held"`
	Queued          int                `json:"queued"`
	Closed          int                `json:"closed"`
	ProgressSummary string             `json:"progress_summary"`
	HeldParts       []WhySplitHeldPart `json:"held_parts,omitempty"`
}

// WhySplitPart identifies the split parent and part label of an ordinary part row.
type WhySplitPart struct {
	ParentID    string `json:"parent_id"`
	ParentTitle string `json:"parent_title"`
	PartLabel   string `json:"part_label"`
	Required    bool   `json:"required"`
}

// WhyRow decorates a planner decision with references a human can follow. For a
// lost worker, Why reports the observed hold instead of the planner's run-live
// assumption. The presentation fields do not decide readiness.
type WhyRow struct {
	Place        int                      `json:"place,omitempty"`
	ItemID       string                   `json:"item_id"`
	State        string                   `json:"state"`
	Position     string                   `json:"position"`
	Reason       string                   `json:"reason"`
	Why          string                   `json:"why"`
	Class        string                   `json:"class"`
	Where        string                   `json:"where"`
	Mover        string                   `json:"mover"`
	Since        string                   `json:"since"`
	ClassSource  string                   `json:"class_source"`
	WaitingOn    []string                 `json:"waiting_on"`
	Decision     Decision                 `json:"decision"`
	LaneStatus   string                   `json:"lane_status,omitempty"`
	LaneReason   string                   `json:"lane_reason,omitempty"`
	Refusals     map[string]int           `json:"refusals,omitempty"`
	Landing      *item.LandingReference   `json:"landing,omitempty"`
	LinkedAlerts []watchlinks.LinkedAlert `json:"linked_alerts,omitempty"`
	SplitParent  *WhySplitParent          `json:"split_parent,omitempty"`
	SplitPart    *WhySplitPart            `json:"split_part,omitempty"`
	tableReason  string
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
	Alarms          []alarms.Alarm          `json:"alarms,omitempty"`
	Definition      string                  `json:"definition"`
	EventHeadSeq    int64                   `json:"event_head_seq"`
	Lanes           []string                `json:"lanes"`
	LanesSource     string                  `json:"lanes_source,omitempty"`
	ProcessPriority *ProcessPrioritySummary `json:"process_priority,omitempty"`
	// Draining lists the lanes above the offered count that still hold work, each with the
	// item it holds. The loop serves them until that work lands or is released.
	Draining []DrainingLane `json:"draining,omitempty"`
	Rows     []WhyRow       `json:"rows"`
	Summary  WhySummary     `json:"summary"`
	// Trunk and TrunkLine are set only when the trunk is unproved: TrunkLine is the one line
	// that says so, for how long, and which check is failing.
	Trunk     *heavy.HeavyStatus `json:"trunk,omitempty"`
	TrunkLine string             `json:"trunk_line,omitempty"`
	// LoopRestartLine is set when a newer build is installed and the live loop is waiting
	// its normal turn behind a gate or proof that was already running when the build landed.
	LoopRestartLine string `json:"loop_restart_line,omitempty"`
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
	works, designHistory, err := readPendingHistory(r)
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
	_, noSplitCalls, err := readSplitCallsForPlanner(r)
	if err != nil {
		return answer, readFailure(err)
	}
	edges, err := readEdges(r)
	if err != nil {
		return answer, err
	}
	splits, err := item.ReadAllSplits(r)
	if err != nil {
		return answer, readFailure(err)
	}
	partIndex := item.SplitPartIndex(splits)
	splitStates := make(map[string]splitclosing.State, len(splits))
	for parentID := range splits {
		cst, err := splitclosing.ReadState(r, parentID)
		if err != nil {
			return answer, readFailure(err)
		}
		splitStates[parentID] = cst
	}
	titles := map[string]string{}
	if len(splits) > 0 {
		titles, err = readItemTitles(r)
		if err != nil {
			return answer, readFailure(err)
		}
	}
	states := map[string]string{}
	for _, it := range items {
		states[it.ItemID] = it.State
	}
	closeReasons := map[string]string{}
	crRows, crErr := r.Query(`SELECT item_id, close_reason FROM items WHERE close_reason != ''`)
	if crErr != nil {
		if !strings.Contains(crErr.Error(), "no such column: close_reason") {
			return answer, readFailure(crErr)
		}
	} else {
		for crRows.Next() {
			var id, cr string
			if scanErr := crRows.Scan(&id, &cr); scanErr != nil {
				crRows.Close()
				return answer, readFailure(scanErr)
			}
			closeReasons[id] = cr
		}
		if crErr := crRows.Err(); crErr != nil {
			crRows.Close()
			return answer, readFailure(crErr)
		}
		crRows.Close()
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
	details := planDetails{
		maskedWorkers: make(map[string]Decision),
		works:         works,
		designHistory: designHistory,
		conflicts:     conflicts,
	}
	envs, err := planWithDetails(r, tick, policy, questions, def, &details)
	if err != nil {
		return answer, err
	}
	leaseLanes := append([]string(nil), offered...)
	for lane, isBusy := range busy {
		if isBusy && !documentDesk(lane) {
			leaseLanes = append(leaseLanes, lane)
		}
	}
	leases, err := activeLeases(r, at, leaseLanes)
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
	for lane, isBusy := range busy {
		if !isBusy || seen[lane] || documentDesk(lane) {
			continue
		}
		seen[lane] = true
		answer.Summary.LanesBusy++
		if workers[lane] {
			answer.Summary.LanesWorkers++
		} else if examining[lane] {
			answer.Summary.LanesExamining++
		} else {
			answer.Summary.LanesReserved++
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
	itemFaults := details.faults
	obstacleHolds := details.obstacleHolds
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
		if len(liveWorkers) == 0 && d.Reason == ReasonRunLive && d.RunRequest != nil && d.LaneID != "" && work != nil && !work.Examining && work.Examiner == nil && !work.Ready {
			liveWorkers, err = reportLiveWorkers(r, work, d, at, observation.faces)
			if err != nil {
				return answer, err
			}
		}
		if d.Reason == ReasonRunLive && len(liveWorkers) == 0 && d.RunRequest != nil {
			// A run with a final gate verdict, or a terminal run record with no open
			// continuation, and no live worker process never reports run-live.
			judged, err := runJudged(r, d.RunRequest.RunID)
			if err != nil {
				return answer, err
			}
			var finished bool
			if !judged {
				rec, err := terminalRecord(r, d.RunRequest.RunID)
				if err != nil {
					return answer, err
				}
				if rec != nil && !d.liveContinuation {
					openResume := false
					if work != nil {
						if seg, ok := work.resumes[d.RunRequest.RunID]; ok {
							openResume, err = liveContinuation(r, seg, d, at)
							if err != nil {
								return answer, err
							}
						}
					}
					finished = !openResume
				}
			}
			if judged || finished {
				if judged && work != nil && work.Candidate != nil && (work.Examining || work.Examiner != nil) {
					d.From, d.Reason = "candidate", ReasonGateReplay
				} else {
					d.From, d.Reason = "held", whyRunAwaitsRecovery
				}
				identifyDecision(&d)
			}
		}
		examinerDead := false
		if d.Reason == ReasonGateReplay && work != nil && tick.StorePath != "" {
			examiner := work.Examiner
			if examiner == nil && work.Examining {
				examiner = work.Owner
			}
			if examiner != nil {
				if here, hereErr := currentLoopOwner(); hereErr == nil && examiner.Domain == here.Domain && examiner.UID == here.UID {
					if dead, deadErr := ownerDead(examiner, here); deadErr == nil && dead {
						examinerDead = true
						d.From = "held"
						identifyDecision(&d)
					}
				}
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
		if m, ok := partIndex[it.ItemID]; ok {
			req := m.Part.Required != nil && *m.Part.Required
			row.SplitPart = &WhySplitPart{
				ParentID:    m.ParentID,
				ParentTitle: titles[m.ParentID],
				PartLabel:   m.Label,
				Required:    req,
			}
		}
		if d.Reason == ReasonSplitCallDesign {
			row.Why = splitCallWhyText(d.SplitCall)
			row.tableReason = row.Why
			if d.Action != ActionHold && d.From != "running" && d.From != "candidate" {
				row.Mover = "the design seat"
			}
		}
		if d.Reason == ReasonGateReplay && examinerDead {
			row.Class = "waits on the loop"
			row.Where = "gate"
			row.Why = ReasonGateReplay + ": examiner process ended before replay finished"
			row.Mover = "reconcile the ended examiner's candidate replay"
		}
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
			if fault.Reason == ReasonStageFailed && fault.RunID != "" {
				row.Why += "; run " + fault.RunID
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
		liveWorker := len(liveWorkers) != 0
		var closedBlockers []string
		ownLaneReason := ""
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
		case (d.Reason == ReasonBackendClosed || strings.HasPrefix(d.Reason, ReasonBackendMissing) || d.BackendHoldCause != "") && d.DeferredBy != "":
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		case (d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy || d.Reason == ReasonForkHeld || d.Reason == ReasonTrunkBroken || d.Reason == "awaits-rewrite" || strings.HasPrefix(d.Reason, ReasonRedCap+": ")) && d.DeferredBy != "":
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		case d.Reason == ReasonOwnerOnlyOwed && it.OwnerOwed != nil:
			row.Why, row.Mover = it.OwnerOwed.Explanation(), it.OwnerOwed.Mover()
		case d.Reason == ReasonBlocked:
			var waitingLabels []string
			for _, edge := range edges[it.ItemID] {
				if !edgeSatisfied(def, edge.Kind, states[edge.Target]) {
					row.WaitingOn = append(row.WaitingOn, edge.Target)
					waitingLabels = append(waitingLabels, edge.Target+" ("+edge.Kind+")")
					if edge.Kind != item.EdgeKindAfter && closedState(def, states[edge.Target]) {
						closedBlockers = append(closedBlockers, edge.Target)
					}
				}
			}
			if len(row.WaitingOn) > 0 {
				row.Why += ": waiting on " + strings.Join(waitingLabels, ", ")
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
		case d.Reason == ReasonAgentLimit:
			runs := splitDeferredRuns(d.DeferredBy)
			if excluded := refusedLaneWaits(configured, it.Express, refused); len(excluded) != 0 {
				row.tableReason = "waiting for lane capacity (lanes out of rotation)"
				row.WaitingOn = append(runs, excluded...)
				row.Why = row.tableReason + ": " + strings.Join(row.WaitingOn, "; ")
				break
			}
			row.WaitingOn = append(row.WaitingOn, runs...)
			if details.retryLaneWait[it.ItemID] {
				row.Why = "operator retry waiting for running-agent limit"
				row.tableReason = row.Why
				if len(row.WaitingOn) > 0 {
					row.Why += ": " + strings.Join(row.WaitingOn, "; ")
				}
			} else if works[it.ItemID] != nil && !works[it.ItemID].Done && !works[it.ItemID].LaneReleased && works[it.ItemID].Candidate != nil {
				row.Why += ": waiting for an ordinary agent slot to judge"
				ownLaneReason = "candidate awaits judge slot"
			}
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
			ownLeases, leaseErr := activeLeases(r, at, []string{d.LaneID})
			if leaseErr != nil {
				return answer, leaseErr
			}
			paused, pauseErr := pausedPendingWork(r, works[it.ItemID])
			if pauseErr != nil {
				return answer, pauseErr
			}
			if paused && (works[it.ItemID].LaneBusyByOther || (works[it.ItemID].ReopenedPausedJudge && len(ownLeases) > 0)) {
				row.WaitingOn = append(row.WaitingOn, d.LaneID)
			} else if paused && len(ownLeases) == 0 && !tick.OutLanes[d.LaneID] && !documentDesk(d.LaneID) {
				for _, lane := range offered {
					if lane == d.LaneID {
						continue
					}
					if busy[lane] || assigned[lane] != "" {
						row.WaitingOn = append(row.WaitingOn, lane)
					}
				}
				var extraLanes []string
				for _, work := range works {
					if work == nil || work.Done || work.LaneReleased {
						continue
					}
					lane := work.Decision.LaneID
					if lane == "" || lane == d.LaneID || slices.Contains(offered, lane) || documentDesk(lane) || tick.OutLanes[lane] {
						continue
					}
					live, liveErr := liveDrainingWorker(r, work, at)
					if liveErr != nil {
						return answer, liveErr
					}
					if live && !slices.Contains(extraLanes, lane) {
						extraLanes = append(extraLanes, lane)
					}
				}
				slices.Sort(extraLanes)
				row.WaitingOn = append(row.WaitingOn, extraLanes...)
				if len(row.WaitingOn) == 0 {
					row.WaitingOn = append(row.WaitingOn, "offer a lane with --lanes")
				}
				row.Why += ": paused on " + d.LaneID + ", waiting for a lane to free"
				break
			}
			// Work that still holds its lane continues only in that worktree: it is held
			// behind that lane's lease (its run, or its examination), not by the others.
			row.WaitingOn = append(row.WaitingOn, d.LaneID)
			if !paused {
				if len(ownLeases) > 0 && ownLeases[0].RunID != "" {
					holder := ownLeases[0].RunID
					isJudge, judgeErr := isJudgeRunForItem(r, holder, it.ItemID, d.LaneID)
					if judgeErr != nil {
						return answer, judgeErr
					}
					var waitDetail string
					switch {
					case isJudge && works[it.ItemID].Candidate != nil && works[it.ItemID].Candidate.RebasedFromHead != "":
						waitDetail = fmt.Sprintf("re-judgment run %s holds %s", holder, d.LaneID)
						ownLaneReason = "candidate awaits re-judgment"
					case isJudge:
						waitDetail = fmt.Sprintf("judge run %s holds %s", holder, d.LaneID)
						ownLaneReason = "candidate awaits judge"
					case works[it.ItemID].Decision.RunRequest != nil && holder == works[it.ItemID].Decision.RunRequest.RunID:
						waitDetail = fmt.Sprintf("examination lease %s holds %s", holder, d.LaneID)
						ownLaneReason = "candidate awaits examination"
					default:
						waitDetail = fmt.Sprintf("run %s holds %s", holder, d.LaneID)
						ownLaneReason = "lane leased by " + holder
					}
					row.Why += ": " + waitDetail
				} else if works[it.ItemID].Candidate != nil {
					if works[it.ItemID].LaneBusyByOther {
						row.Why += ": " + d.LaneID + " is busy with another run"
						ownLaneReason = "waiting for " + d.LaneID + " to free"
					} else {
						row.Why += ": waiting for an ordinary agent slot to judge"
						ownLaneReason = "candidate awaits judge slot"
					}
				}
			}
			if !documentDesk(d.LaneID) && !slices.Contains(configured, d.LaneID) && d.LaneID != it.ItemID {
				// The lane is above the loop's count: the loop keeps serving it until this
				// work lands or is released, and offers it nothing new (drainingLanes).
				sep := ": "
				if strings.Contains(row.Why, ":") {
					sep = "; "
				}
				row.Why += sep + "lane " + d.LaneID + " is draining: above the loop's lane count, it finishes " + it.ItemID + " and takes nothing new"
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
			for _, name := range policy.SeatList(seat) {
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
			allDefectiveSibling := false
			if partMem, isPart := partIndex[it.ItemID]; isPart {
				allDefectiveSibling = true
				for _, b := range closedBlockers {
					bMem, ok := partIndex[b]
					if !ok || bMem.ParentID != partMem.ParentID || bMem.Part.Required == nil || !*bMem.Part.Required || len(bMem.Part.ReplacedBy) > 0 || !item.IsDefectiveClose(states[b], closeReasons[b]) {
						allDefectiveSibling = false
						break
					}
				}
				if allDefectiveSibling {
					cst := splitStates[partMem.ParentID]
					if cst.Held != nil {
						if cst.Held.Mover == "owner" {
							row.Class = "needs you"
							row.Mover = "owner; " + cst.Held.Reason
						} else {
							row.Class = "waits on work"
							row.Mover = cst.Held.Mover
						}
						row.Why = cst.Held.Reason
					} else {
						row.Class = "waits on work"
						row.Mover = "design"
						row.Why = "waiting on design seat to replace defective required part (" + strings.Join(closedBlockers, ", ") + ")"
					}
				}
			}
			if !allDefectiveSibling {
				row.Class = "needs you"
				row.Mover = "remove the dependency or reopen the closed item (" + strings.Join(row.WaitingOn, ", ") + ")"
			}
		}
		if d.Reason == ReasonBlocked || d.Reason == ReasonToolingDefect {
			if waiting != nil && waiting.Source == item.SplitWaitSource {
				required, err := item.SplitRequiredParts(r, it.ItemID)
				if err != nil {
					return WhyAnswer{}, readFailure(err)
				}
				if err := splitWait(r, &row, it.ItemID, required, states, def); err != nil {
					return WhyAnswer{}, readFailure(err)
				}
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
					if testFixHeldWait(*waiting) {
						if cmd := testFixClosedRetryCommand(r, it.ItemID, work); cmd != "" {
							row.Mover = cmd
						}
					}
				} else if depID, ok := strings.CutPrefix(waiting.Waiter, "item:"); ok && testFixHeldWait(*waiting) && finishedState(def, states[depID]) && states[depID] != StateLanded {
					row.Class = "needs you"
					row.Why = depID + " finished without landing (" + states[depID] + ")"
					if strings.TrimSpace(waiting.Reason) != "" {
						row.Why += ": " + waiting.Reason
					}
					if cmd := testFixClosedRetryCommand(r, it.ItemID, work); cmd != "" {
						row.Mover = cmd
					} else {
						row.Mover = "remove the dependency or reopen the closed item (" + depID + ")"
					}
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
				if row.LaneReason == "" && ownLaneReason != "" {
					row.LaneReason = ownLaneReason
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
				if d.Reason == ReasonConflictDesign || d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy || d.Reason == ReasonSplitCallDesign {
					actor = "design seat"
				}
				if row.Mover == "plan" || row.Mover == "design" || strings.Contains(row.Mover, "design seat") {
					actor = "design seat"
				}
				if row.Class == "alarm" || row.Class == "needs you" || row.Class == "needs a cause" || strings.HasPrefix(d.Reason, ReasonTrunkPathDirty+": ") {
					actor = "operator"
				}
				if strings.HasPrefix(row.Mover, "owner;") {
					actor = "owner"
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
		declinedRunID := ""
		if d.RunRequest != nil {
			declinedRunID = d.RunRequest.RunID
		}
		declined, err := decline.AppliesTo(r, d.Action, d.DecisionID, d.LaneID, declinedRunID)
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
		if ns, ok := noSplitCalls[it.ItemID]; ok && ns.NoSplit != nil && ns.NoSplit.Reason != "" && d.Reason != ReasonSplitCallDesign {
			note := "no split: " + collapseSingleLine(ns.NoSplit.Reason)
			if row.tableReason != "" {
				row.tableReason += "; " + note
			} else if row.Reason != "" {
				row.tableReason = row.Reason + "; " + note
			} else {
				row.tableReason = note
			}
			if row.Why != "" {
				row.Why += "; " + note
			} else {
				row.Why = note
			}
		}
		linkedAlerts, err := watchlinks.LinkedAlertsForItem(r, it.ItemID)
		if err != nil {
			return answer, readFailure(err)
		}
		if len(linkedAlerts) > 0 {
			row.LinkedAlerts = linkedAlerts
		}
		answer.Rows = append(answer.Rows, row)
	}
	decorateSplitParents(&answer, splits, states, closeReasons, splitStates, def)
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
		alarm, restartLine, err := staleLoopBuild(r, tick.Root, tick.StorePath, runningLoop, works)
		if err != nil {
			return answer, err
		}
		if alarm != nil {
			answer.Alarms = append(answer.Alarms, *alarm)
		}
		answer.LoopRestartLine = restartLine
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
	if work == nil || work.Owner == nil || work.Examining || work.Examiner != nil || work.Ready || work.LoopID == "" {
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
	if d.Action == ActionHold && (d.Reason == ReasonBackendClosed || d.Reason == ReasonBackendWindow || strings.HasPrefix(d.Reason, ReasonBackendMissing)) {
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
	case ReasonAgentLimit:
		return "waits on work", where, "a running ordinary agent to finish"
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
	case ReasonSplitCallDesign:
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

func splitDeferredRuns(deferredBy string) []string {
	if strings.TrimSpace(deferredBy) == "" {
		return nil
	}
	parts := strings.Split(deferredBy, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
	id              string
	startedAt       int64
	lanes           []string
	owner           LoopOwner
	processPriority *ProcessPrioritySummary
}

type activeRestartBlocker struct {
	name      string
	startedAt time.Time
}

func activeCandidateGateStart(root, storePath, worktree, runID string) (time.Time, bool) {
	if runID == "" {
		return time.Time{}, false
	}
	dir := detachedGateDir(root, storePath, worktree, runID)
	if root == "" && storePath != "" && filepath.Base(filepath.Dir(storePath)) == TillrDir {
		loopDir := RunDir(filepath.Dir(filepath.Dir(storePath)), runID)
		if _, ok := readDetachedGateFace(loopDir); ok {
			dir = loopDir
		}
	}
	if d, ok := readDetachedGateDone(dir); ok && d.RunID == runID {
		return time.Time{}, false
	}
	face, ok := readDetachedGateFace(dir)
	if !ok || face.RunID != runID || face.StartedAtMS <= 0 {
		return time.Time{}, false
	}
	running, err := heavySupervisorRunning(face.PID, face.StartTicks)
	if err != nil || !running {
		return time.Time{}, false
	}
	return time.UnixMilli(face.StartedAtMS).UTC(), true
}

func activeRestartBlockers(r *store.Reader, root, storePath string, loop *liveLoopSession, works map[string]*pendingWork) ([]activeRestartBlocker, error) {
	var blockers []activeRestartBlocker
	for _, work := range works {
		if work == nil || work.Done || work.Decision.RunRequest == nil {
			continue
		}
		owner := work.Examiner
		if work.Examining && owner == nil {
			owner = work.Owner
		}
		if owner == nil || *owner != loop.owner {
			continue
		}
		runID := work.Decision.RunRequest.RunID
		worktree := ""
		if work.Candidate != nil {
			worktree = work.Candidate.Worktree
		}
		startedAt, ok := activeCandidateGateStart(root, storePath, worktree, runID)
		if !ok {
			continue
		}
		name := fmt.Sprintf("gate %s (%s)", work.Decision.ItemID, runID)
		if work.Decision.LaneID != "" {
			name = fmt.Sprintf("gate %s on %s (%s)", work.Decision.ItemID, work.Decision.LaneID, runID)
		}
		blockers = append(blockers, activeRestartBlocker{name: name, startedAt: startedAt})
	}
	rows, err := r.Query(`SELECT COALESCE(object_id, ''), ts, payload FROM events WHERE kind = ? AND ts >= ? ORDER BY seq ASC`, HeavyProgressKind, loop.startedAt)
	if err != nil {
		if !strings.Contains(err.Error(), "no such table") {
			return nil, readFailure(err)
		}
	} else {
		activeProofs := map[string]activeRestartBlocker{}
		for rows.Next() {
			var trunkID string
			var ts int64
			var payload []byte
			if err := rows.Scan(&trunkID, &ts, &payload); err != nil {
				rows.Close()
				return nil, readFailure(err)
			}
			var fact HeavyProgressFact
			if err := json.Unmarshal(payload, &fact); err != nil {
				continue
			}
			if trunkID == "" {
				trunkID = settings.DefaultTrunkBranch
			}
			switch fact.Phase {
			case "started":
				name := "trunk proof"
				if fact.TipSHA != "" {
					name = fmt.Sprintf("trunk proof at %s", shortSHA(fact.TipSHA))
				}
				activeProofs[trunkID] = activeRestartBlocker{name: name, startedAt: time.UnixMilli(ts).UTC()}
			case "ended":
				delete(activeProofs, trunkID)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, readFailure(err)
		}
		rows.Close()
		for _, proof := range activeProofs {
			blockers = append(blockers, proof)
		}
	}
	sort.Slice(blockers, func(i, j int) bool {
		if !blockers[i].startedAt.Equal(blockers[j].startedAt) {
			return blockers[i].startedAt.Before(blockers[j].startedAt)
		}
		return blockers[i].name < blockers[j].name
	})
	return blockers, nil
}

func joinRestartBlockerNames(blockers []activeRestartBlocker) string {
	names := make([]string, len(blockers))
	for i, b := range blockers {
		names[i] = b.name
	}
	return strings.Join(names, ", ")
}

// staleLoopBuild compares the loop session's start with the mtime of the
// executable serving why. This is the installed build the operator invoked;
// resolving it again through the launcher could itself build a different one.
// While the loop waits behind a gate or proof that was already running when the
// new build was installed, why reports a status line instead of an alarm; if a
// gate or proof started after the install is running, the alarm stands and names it.
func staleLoopBuild(r *store.Reader, root, storePath string, loop *liveLoopSession, works map[string]*pendingWork) (*alarms.Alarm, string, error) {
	if loop == nil {
		return nil, "", nil
	}
	here, err := currentLoopOwner()
	if err != nil || loop.owner.Domain != here.Domain || loop.owner.UID != here.UID {
		return nil, "", nil // The recorded PID does not identify a process we can inspect here.
	}
	started := time.UnixMilli(loop.startedAt).UTC()
	installed, err := os.Stat("/proc/self/exe")
	if err != nil || !installed.ModTime().After(started) {
		return nil, "", nil
	}
	image, err := os.Stat(fmt.Sprintf("/proc/%d/exe", loop.owner.PID))
	if err != nil || os.SameFile(image, installed) {
		return nil, "", nil // A changed timestamp alone does not make the running build stale.
	}
	if dead, err := ownerDead(&loop.owner, here); err != nil || dead {
		return nil, "", nil // Recheck identity after reading the process's executable.
	}
	installedAt := installed.ModTime().UTC()
	builtAt := installedAt.Format(time.RFC3339Nano)
	blockers, err := activeRestartBlockers(r, root, storePath, loop, works)
	if err != nil {
		return nil, "", err
	}
	var starving, waiting []activeRestartBlocker
	for _, b := range blockers {
		if b.startedAt.After(installedAt) {
			starving = append(starving, b)
		} else {
			waiting = append(waiting, b)
		}
	}
	if len(starving) > 0 {
		return &alarms.Alarm{
			Kind: "stale-loop-build", Backend: "drain", Since: builtAt,
			Cause:   fmt.Sprintf("loop started at %s; installed tillr-drain was built at %s; restart starved by %s", started.Format(time.RFC3339Nano), builtAt, joinRestartBlockerNames(starving)),
			Clears:  "the running loop uses the installed build",
			Command: "no automatic restart",
		}, "", nil
	}
	if len(waiting) > 0 {
		line := fmt.Sprintf("loop restart: waiting for %s (loop started at %s; installed tillr-drain was built at %s)", joinRestartBlockerNames(waiting), started.Format(time.RFC3339Nano), builtAt)
		return nil, line, nil
	}
	return &alarms.Alarm{
		Kind: "stale-loop-build", Backend: "drain", Since: builtAt,
		Cause:   fmt.Sprintf("loop started at %s; installed tillr-drain was built at %s", started.Format(time.RFC3339Nano), builtAt),
		Clears:  "the running loop uses the installed build",
		Command: "no automatic restart",
	}, "", nil
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

// LiveLoopProcessPriority returns the process priority summary recorded by a currently
// live loop session for storePath, if any.
func LiveLoopProcessPriority(r *store.Reader, storePath string, now time.Time) (*ProcessPrioritySummary, bool, error) {
	s, err := findLiveLoopSession(r, storePath, now)
	if s == nil || s.processPriority == nil {
		return nil, false, err
	}
	return s.processPriority, true, nil
}

func findLiveLoopSession(r *store.Reader, storePath string, now time.Time) (*liveLoopSession, error) {
	rows, err := r.Query(`SELECT session_id, started_at, loop_owner, next_pass_at, COALESCE(lanes, ''), COALESCE(process_priority, '') FROM drain_sessions WHERE ended_at IS NULL AND loop_owner IS NOT NULL ORDER BY started_at DESC, rowid DESC`)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such table") || strings.Contains(msg, "no such column") {
			return nil, nil
		}
		return nil, readFailure(err)
	}
	type candidate struct {
		id          string
		startedAt   int64
		ownerRaw    string
		nextPassAt  int64
		lanesRaw    string
		priorityRaw string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.startedAt, &c.ownerRaw, &c.nextPassAt, &c.lanesRaw, &c.priorityRaw); err != nil {
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
		var procPriority *ProcessPrioritySummary
		if c.priorityRaw != "" {
			var parsed ProcessPrioritySummary
			if err := json.Unmarshal([]byte(c.priorityRaw), &parsed); err == nil && parsed.Mechanism != "" {
				procPriority = &parsed
			}
		}
		session := &liveLoopSession{id: c.id, startedAt: c.startedAt, lanes: lanes, owner: owner, processPriority: procPriority}
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

// testFixClosedRetryCommand returns the exact `tillr drain retry` invocation that
// releases a test-fix-open hold after its named fix item closed without landing.
func testFixClosedRetryCommand(r *store.Reader, itemID string, work *pendingWork) string {
	var runID string
	_ = r.QueryRow(
		`SELECT run_id FROM runs WHERE item_id = ? AND state IN (?, ?, ?)
		  ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`,
		itemID, run.StateExited, run.StateFailedToStart, run.StateTimedOut,
	).Scan(&runID)
	if runID == "" && work != nil && work.Decision.RunRequest != nil {
		runID = work.Decision.RunRequest.RunID
	}
	if runID == "" {
		return ""
	}
	cmd, err := acceptedBuildRetryCommand(r, itemID, runID)
	if err != nil {
		return retryCommand(itemID, runID, "")
	}
	return cmd
}
