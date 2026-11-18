package drain

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// WhyRow decorates a planner decision with references a human can follow. Decision
// retains the full planner record; the presentation fields do not decide readiness.
type WhyRow struct {
	Place       int      `json:"place,omitempty"`
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
	LaneStatus  string   `json:"lane_status,omitempty"`
	LaneReason  string   `json:"lane_reason,omitempty"`
}

type WhySummary struct {
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
	Definition   string     `json:"definition"`
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
	declared, err := definition.ForReader(r)
	if err != nil {
		return answer, readFailure(err)
	}
	answer.Definition = declared.Name
	def := declared.Parsed
	envs, err := planDefinition(r, tick, policy, questions, def)
	if err != nil {
		return answer, err
	}
	items, err := readItems(r)
	if err != nil {
		return answer, err
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
	assigned := map[string]string{}
	for i, env := range envs {
		it := items[i]
		if (def.Completes == "landing" && it.State == StateLanded) || (def.Completes != "landing" && finishedState(def, it.State)) {
			continue
		}
		var d Decision
		if err := json.Unmarshal(env.Payload, &d); err != nil {
			return answer, Internalf("reading planner decision: %v", err)
		}
		class, where, mover := whyPresentation(d)
		row := WhyRow{Place: places[it.ItemID], ItemID: it.ItemID, State: it.State, Position: d.Position, Reason: d.Reason, Why: d.Reason, Class: class, Where: where, Mover: mover, Since: "current plan", ClassSource: "planner", WaitingOn: []string{}, Decision: d}
		if d.Reason == ReasonEvalFailed || d.Reason == ReasonJudgeUnreadable {
			detail, err := judgeFailureDetail(r, it.ItemID, d.Reason)
			if err != nil {
				return answer, err
			}
			if detail != "" {
				row.Why += ": " + detail
			}
		}
		switch {
		case d.Reason == ReasonQuestionOpen && d.QuestionID != "":
			row.WaitingOn = append(row.WaitingOn, d.QuestionID)
		case (d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy || d.Reason == ReasonForkHeld || d.Reason == ReasonTrunkBroken || d.Reason == "awaits-rewrite" || strings.HasPrefix(d.Reason, ReasonRedCap+": ")) && d.DeferredBy != "":
			row.WaitingOn = append(row.WaitingOn, d.DeferredBy)
		case d.Reason == ReasonBlocked:
			for _, id := range edges[it.ItemID] {
				if !finishedState(def, states[id]) {
					row.WaitingOn = append(row.WaitingOn, id)
				}
			}
		case d.Action == ActionHold && (d.Reason == ReasonConflictDesign || d.Reason == ReasonConflictOwner) && d.ConflictID != "":
			row.WaitingOn = append(row.WaitingOn, d.ConflictID)
			if d.Reason == ReasonConflictOwner && d.DeferredBy != "" {
				row.WaitingOn = append(row.WaitingOn, "design run "+d.DeferredBy)
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
		if d.Reason == ReasonBackendWindow && d.ConflictID != "" && policy != nil {
			seat := d.Seat
			for _, name := range policy.seatList(seat) {
				if reset, shut := windows[name]; shut {
					row.WaitingOn = append(row.WaitingOn, name+" reopens "+reset.Format(time.RFC3339))
				}
			}
			row.Why += ": seat " + seat + "; " + strings.Join(row.WaitingOn, ", ")
		}
		if len(row.WaitingOn) > 0 {
			row.Mover = strings.Join(row.WaitingOn, ", ")
		}
		if it.State == StateBlocked && d.Reason == ReasonBlocked {
			waiting, _, err := item.ReadWaitingOn(r, it.ItemID)
			if err != nil {
				return answer, err
			}
			if waiting != nil {
				row.Class, row.Mover, row.Why = "waits on work", waiting.Waiter, waiting.Reason
				row.ClassSource = "store"
				if waiting.Waiter == "owner" {
					row.Class = "needs you"
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
				row.Mover = "the owner; close the item or answer the conflict"
			} else {
				row.Mover = "triage on the design seat"
			}
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
		if d.Action == ActionDispatch || d.Action == ActionRetry {
			assigned[d.LaneID] = d.ItemID
		}
		if it.State == "closed" || it.State == "resolved-elsewhere" {
			row.Class, row.Mover = "done", "no further work: "+it.State
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
			} else {
				actor := "drain"
				if d.Reason == ReasonConflictDesign || d.Reason == ReasonDeskCapacity || d.Reason == ReasonDocumentDeskBusy {
					actor = "design seat"
				}
				if row.Mover == "plan" || row.Mover == "design" || strings.Contains(row.Mover, "design seat") {
					actor = "design seat"
				}
				if row.Class == "needs you" || row.Class == "needs a cause" || strings.HasPrefix(d.Reason, ReasonTrunkPathDirty+": ") {
					actor = "operator"
				}
				if d.Reason == ReasonConflictOwner {
					c := conflicts[it.ItemID]
					switch {
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
				row.Mover = actor + "; " + nextActorAction(row.Mover)
				switch actor {
				case "drain":
					answer.Summary.DrainNext++
				case "design seat":
					answer.Summary.DesignNext++
				case "operator":
					answer.Summary.OperatorNext++
				}
			}
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
	if strings.HasPrefix(d.Reason, "prose-criteria-count: ") {
		return "needs you", "queue", "the owner; reconcile the original criteria"
	}
	if strings.HasPrefix(d.Reason, ReasonForeignIdentity+": ") {
		return "needs you", "trunk", "re-author the commit, or record its id with tillr-drain accept-identity"
	}
	if strings.HasPrefix(d.Reason, ReasonRedCap+": ") {
		return "waits on work", "land", "the heavy runner; prove trunk or file its fixer"
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
	switch d.Reason {
	case "awaits-rewrite":
		return "waits on work", where, "the prose rewrite to land"
	case ReasonBlocked:
		return "waits on work", where, "the owner; clear the item's blocked state"
	case ReasonDeskCapacity, ReasonDocumentDeskBusy:
		return "waits on work", "desk", d.DeferredBy
	case ReasonNoLane, ReasonForkHeld:
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
		return "needs you", "queue", "issue a grant covering the item's criteria"
	case ReasonJudgeUnreadable:
		return "needs a cause", "judge", "inspect the judge run"
	case ReasonEvalFailed:
		return "waits on work", "judge", "amend the candidate against the judge's reason"
	case ReasonGateFailed, ReasonRetryJudged:
		return "needs you", "gate", "inspect the verdict; tillr drain retry --help"
	case ReasonBoundaryScopeEscape, StopHostBoundaryViolation:
		return "needs a cause", "fence", "inspect the boundary report"
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
	case ReasonDecisionCollision, ReasonSettleFailed:
		return "needs a cause", "loop", "fix the drain defect the fault names, then tillr drain clear-fault"
	default:
		return "needs a cause", "lane", "diagnose the run; tillr drain retry --help"
	}
}

func nextActorAction(mover string) string {
	for _, prefix := range []string{"the owner; ", "owner; ", "the operator; ", "operator; ", "design seat; ", "drain; "} {
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
