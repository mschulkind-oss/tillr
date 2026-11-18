package drain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	ReasonDeskCapacity     = "waits on a desk"
	ReasonDocumentDeskBusy = "waits on the document's desk"
)

// Pass is the pure planner's input. The store supplies lanes when none are offered.
// MaxDispatches optionally limits a simulation; the live loop never sets it.
// Now optionally supplies observation metadata, never an identity.
type Pass struct {
	// Comments is the repository's shared settings, not a caller-supplied pass field.
	Comments *settings.Comments `json:"-"`
	// Operator examination considers one item's next step while still reading
	// every existing lane reservation and readiness dependency. It must not
	// allocate free lanes to sibling plans that this command will not execute.
	examineItem string
	// ownerMigration permits only the deterministic conversion of a legacy owner report.
	ownerMigration bool
	// GatesInFlight is the planning pass's observation, not a concurrency cap.
	GatesInFlight int `json:"gates_in_flight,omitempty"`
	// Build is supplied by a loop so the snapshot identifies that running binary.
	Build *BuildStamp `json:"-"`
	// WorkflowDefaults comes from settings.Read, never from a caller-supplied pass.
	WorkflowDefaults map[string]string `json:"-"`
	// BriefTemplate is the loop's actual staging input, not a stored tick field.
	// A standalone plan with no stager compares the retained template instead.
	BriefTemplate  *string   `json:"-"`
	Now            string    `json:"now"`
	AvailableLanes *[]string `json:"available_lanes"`
	MaxDispatches  *int      `json:"max_dispatches"`
	PolicyRef      string    `json:"policy_ref"`
}

// At is the tick's injected clock, parsed. It is the only clock this face reads: every
// decision's created_at is stamped from it. Decision, run and transition identities
// instead come from the item's attempt and positions.
func (t Pass) At() (time.Time, error) {
	if t.Now == "" {
		return time.Unix(0, 0).UTC(), nil
	}
	at, err := time.Parse(time.RFC3339, t.Now)
	if err != nil {
		return time.Time{}, Usagef("now %q is not an RFC 3339 timestamp: %v", t.Now, err)
	}
	return at.UTC(), nil
}

// check refuses a tick the face cannot honour, before the store is read.
func (t Pass) check() error {
	if _, err := t.At(); err != nil {
		return err
	}
	if t.MaxDispatches != nil && *t.MaxDispatches < 0 {
		return Usagef("max_dispatches %d is negative: a tick dispatches zero or more items", *t.MaxDispatches)
	}
	return nil
}

// Policy is the document `policy_ref` names: the command a dispatch's run.request
// carries, and the three fields run's request takes from the same place.
//
// Decision is the drain.decision payload: design §5.5's field list.
//
// RunRequest is run's own type rather than a copy of it, so the two utilities cannot
// drift on the shape of the request drain emits and run consumes.
type Decision struct {
	WorkerSessionReason string `json:"worker_session_reason,omitempty"`
	// Parallel lists sibling readers claimed with this node; the item retains one pending owner.
	Parallel []Decision `json:"parallel,omitempty"`
	// BuildRestartFrom is the interrupted reader whose face must end before this
	// replacement uses its shared lane. It is drain-owned, never a run field.
	BuildRestartFrom string `json:"build_restart_from,omitempty"`
	// Designer is the row read when the session choice was made, not a later reread.
	DeskDocument      string         `json:"desk_document,omitempty"`
	Designer          *lastDesigner  `json:"-"`
	DesignerSessionID string         `json:"designer_session_id,omitempty"`
	DesignerReason    string         `json:"designer_reason,omitempty"`
	NodeStartRunID    string         `json:"node_start_run_id,omitempty"`
	WorkflowRestart   bool           `json:"workflow_restart,omitempty"`
	NodeInfraFailures int            `json:"node_infra_failures,omitempty"`
	WorkflowRelease   bool           `json:"workflow_release,omitempty"`
	Workflow          *Workflow      `json:"workflow,omitempty"`
	SelectedWorkflow  string         `json:"selected_workflow,omitempty"`
	WorkflowSource    string         `json:"workflow_source,omitempty"`
	NodeIndex         int            `json:"node_index,omitempty"`
	NodeBaseSHA       string         `json:"node_base_sha,omitempty"`
	PreviousReport    string         `json:"previous_report,omitempty"`
	ConflictID        string         `json:"conflict_id,omitempty"`
	BlockConflict     *item.Conflict `json:"block_conflict,omitempty"`
	Inputs            *RunInputs     `json:"inputs,omitempty"`
	// DispatchBuild is the loop build that planned a dispatch whose decision carries no
	// Inputs (a design-seat or owner-question dispatch). The dispatch fact records it as
	// its TillrBuild, so build migration knows the run's build (migrateBuildRuns).
	DispatchBuild string `json:"dispatch_build,omitempty"`
	// Readmitted marked a dispatch the removed input-change rule allowed. No pass writes
	// it since the owner's ruling of 2026-11-17; recorded decisions still carry it.
	Readmitted bool `json:"readmitted,omitempty"`
	// Returned is the earlier run's ending a dispatch carries back to the item's worker:
	// a gate refusal, a run that committed nothing, a fence escape or a refused landing.
	// Staging writes it into the brief (returnedBrief). It is nil on every other decision.
	Returned *ReturnedEnding `json:"returned,omitempty"`
	// TransitionID identifies (item, attempt, from, to), independently of the tick's
	// clock. Attempt is one more than the item's count of terminal runs, including
	// failover runs. Positions use the design's closed set: queued, running, candidate,
	// landed, held. A hold has no destination: it records a position, not a transition.
	Position     string `json:"position"`
	Transition   string `json:"transition"`
	TransitionID string `json:"transition_id"`
	Attempt      int    `json:"attempt"`
	From         string `json:"from"`
	To           string `json:"to,omitempty"`
	// PassID names the observing pass; it does not change the transition's identity.
	PassID     string       `json:"pass_id,omitempty"`
	DecisionID string       `json:"decision_id"`
	ItemID     string       `json:"item_id"`
	Action     string       `json:"action"`
	Reason     string       `json:"reason"`
	LaneID     string       `json:"lane_id,omitempty"`
	RunRequest *run.Request `json:"run_request,omitempty"`
	// RetryID names the operator's authorization a `retry` decision spends, and the
	// SupersedesRunID names the run that authorization supersedes. A hold that refuses the
	// claim — `retry-judged`, `retry-unjudged` — names the same pair, so an
	// operator reading why their authorization did not hold can see what it named. A first
	// dispatch and every other hold leave both empty.
	RetryID         string `json:"retry_id,omitempty"`
	SupersedesRunID string `json:"supersedes_run_id,omitempty"`
	// DeferredBy names the item behind a resource deferral: a live fork owner,
	// the trunk's fixer, or an open review batch for the requested design document.
	// It is empty for an item's own hold.
	DeferredBy string `json:"deferred_by,omitempty"`
	// QuestionID names the question a `question-open` hold is waiting on: the first
	// unanswered question in the design document's own order, as Vantage's index reports it.
	// It is empty for every other hold and for every dispatch, so an operator who reads one
	// decision can follow the hold to the question rather than to the section.
	QuestionID string `json:"question_id,omitempty"`
	// Seat is the seat the item asked for, recorded beside the request it produced. It is
	// what explains the request's backend and the model in its argv: the policy mapped this
	// seat to that backend, and the backend names its model, so a decision read back later
	// answers "why this model" without the policy that happened to be live when it was
	// planned. Empty for an item that asked for no seat.
	Seat string `json:"seat,omitempty"`
	// SelectSeat marks a fresh choice, as opposed to an ordered failover or continuation.
	SelectSeat bool `json:"select_seat,omitempty"`
}

// itemRow is the item state this face reads: the columns a decision turns on, and the
// rank that decides the order the decisions come in.
type itemRow struct {
	// Express is read from the stored definition before workflow seat routing.
	Express    bool
	JudgeMode  string
	JudgeGrant string
	Designer   *lastDesigner
	Workflow   string
	Command    *WorkflowStage
	ItemID     string
	State      string
	Rank       string
	// Allowed is the item's allowed_paths, the source of the fence its run is held to.
	Allowed []string
	// DesignDoc is the design document the item names, relative to the repository or
	// absolute. It is empty for an item that names none, and such an item has no question
	// state to read: an item with no document is not an item whose document is missing.
	DesignDoc string
	// Seat is the role the item asks for (the item model's seat.go vocabulary), empty when it
	// asks for none. The policy maps it to the backend its dispatch runs on, so the item never
	// names a model.
	Seat string
}

// Plan computes one tick's decisions: one drain.decision record per item in the store, in
// the queue's order (readItems), and nothing written.
//
// It is a pure function of the store's rows, the tick and the questions source, so two
// calls with the same store and tick produce the same records — which is what makes the
// face a capacity simulator (vary available_lanes and max_dispatches, diff the decisions)
// rather than a report on a moment. The source is where the one fact the store does not
// carry comes from: whether an item's design document is in the tree and what it still
// asks. Its answers are read once per item, in decision order, so the plan is still a
// function of its inputs and a test can state them.
//
// A nil questions source answers every document as unknown, which holds the item rather
// than dispatching it on a guess. That is the deliberate direction: a caller that forgot
// to read the index gets held items with a reason, not a silent "no questions".
func Plan(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource) ([]record.Envelope, error) {
	d, err := definition.ForReader(r)
	if err != nil {
		return nil, readFailure(err)
	}
	return planDefinition(r, tick, policy, questions, d.Parsed)
}

func planDefinition(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource, def definition.Definition) ([]record.Envelope, error) {
	if err := tick.check(); err != nil {
		return nil, err
	}
	at, err := tick.At()
	if err != nil {
		return nil, err
	}
	shut, err := readQuotaShutBackends(r, at, policy)
	if err != nil {
		return nil, readFailure(err)
	}
	quotaShut, err := readQuotaOnlyShut(r, at, policy)
	if err != nil {
		return nil, readFailure(err)
	}
	items, err := readItems(r)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].JudgeMode == "agent" {
			items[i].JudgeGrant, err = judgeGrant(r, items[i].ItemID, at)
			if err != nil {
				return nil, err
			}
		}
		if doc := deskDocument(items[i], tick.Comments); doc != "" {
			items[i].Designer, err = readDesigner(r, doc)
			if err != nil {
				return nil, readFailure(err)
			}
		}
	}
	edges, err := readEdges(r)
	if err != nil {
		return nil, err
	}
	lanes, err := readLanes(r, at)
	if err != nil {
		return nil, err
	}
	history, err := readRunHistory(r)
	if err != nil {
		return nil, err
	}
	priors, err := readPriorRuns(r)
	if err != nil {
		return nil, err
	}
	retries, err := readRetries(r)
	if err != nil {
		return nil, err
	}
	classes, latest := indexPriorRuns(priors)
	// Acquitted executions still count toward run ids, but not the failure walk.
	// Keep their ordinal history intact and remove them only from attempt policy.
	attemptPriors := map[string][]priorRun{}
	for id, runs := range priors {
		for _, prior := range runs {
			if !acquittedRefusal(history.refusals[prior.RunID]) {
				attemptPriors[id] = append(attemptPriors[id], prior)
			}
		}
	}
	trunkWaiters, defectWaiters := map[string]string{}, map[string]string{}
	for _, row := range items {
		if def.Completes != "landing" || row.State != def.Blocked {
			continue
		}
		waiting, _, err := item.ReadWaitingOn(r, row.ItemID)
		if err != nil {
			return nil, readFailure(err)
		}
		if waiting != nil {
			fixer := strings.TrimPrefix(waiting.Waiter, "item:")
			if strings.HasPrefix(waiting.Waiter, "item:") && IsTrunkFixer(fixer) {
				trunkWaiters[row.ItemID] = fixer
			}
			if strings.HasPrefix(waiting.Waiter, "item:") && IsDefectItem(fixer) {
				defectWaiters[row.ItemID] = fixer
			}
		}
	}
	escalations, err := readEscalations(r)
	if err != nil {
		return nil, readFailure(err)
	}
	escalated := map[string]string{}
	for id, list := range escalations {
		escalated[id] = escalatedBackend(list)
	}
	inputDecisions, err := readInputDecisions(r)
	if err != nil {
		return nil, readFailure(err)
	}
	var build BuildStamp
	if tick.Build != nil {
		build = *tick.Build
	} else {
		build = ReadBuildStamp()
	}
	root := ""
	if source, ok := questions.(*VantageQuestions); ok {
		root = source.Root
	}
	unshared := map[string]bool{}
	rewriteHolds, rewriteOwnerHolds := map[string]string{}, map[string]string{}
	if hasPrerequisite(def, definition.PrerequisiteDesignDocument) && def.Completes == "landing" {
		unshared, rewriteHolds, rewriteOwnerHolds, err = documentPrerequisites(r, root)
		if err != nil {
			return nil, err
		}
	}
	buildHash, err := build.Hash()
	if err != nil {
		return nil, err
	}
	inputs := map[string]*RunInputs{}
	for _, row := range items {
		if row.State != def.Ready || unshared[row.ItemID] {
			continue
		}
		inputs[row.ItemID], err = currentRunInputs(r, row, policy, root, build)
		if err != nil {
			return nil, readFailure(err)
		}
	}

	states := make(map[string]string, len(items))
	for _, item := range items {
		states[item.ItemID] = item.State
	}
	var green string
	if def.Completes == "landing" && root != "" {
		gatePolicy, _, e := ReadGatePolicy(root)
		if e != nil {
			return nil, e
		}
		// This read-only face needs Git only to prove a landed dependency.
		// The dispatch claim still validates the selected lane base before staging,
		// and advances it to carry the item's own block (blockBase).
		if hasLandedBlocker(states, edges) {
			green, err = provedLaneBase(context.Background(), root, gatePolicy.LaneBase)
			if err != nil {
				return nil, err
			}
		}
	}
	unproved := map[string]bool{}
	if def.Completes == "landing" {
		unproved, err = unprovedBlockers(context.Background(), r, root, green, states, edges)
		if err != nil {
			return nil, err
		}
	}
	// An override naming an item the store does not hold is refused rather than ignored:
	// the override map names items, so a stale entry is the rot design §2 prices and
	// refuses mechanically instead of letting it fall through a silent default.
	if err := checkOverrides(policy, states); err != nil {
		return nil, err
	}
	seatItems := make([]itemRow, 0, len(items))
	for _, row := range items {
		if !unshared[row.ItemID] {
			seatItems = append(seatItems, row)
		}
	}
	if err := checkSeats(policy, seatItems); err != nil {
		return nil, err
	}
	works, err := readPending(r)
	if err != nil {
		return nil, readFailure(err)
	}
	forks, err := liveForks(r, at)
	if err != nil {
		return nil, readFailure(err)
	}
	deskOwners, err := readDeskOwners(r, lanes)
	if err != nil {
		return nil, readFailure(err)
	}
	// A terminal run released its lease, but its candidate still occupies the
	// worktree until settlement and judgment have accounted for it.
	for _, work := range works {
		if !work.Done && !work.LaneReleased {
			lanes[work.Decision.LaneID] = true
			deskOwners[work.Decision.LaneID] = work.Decision.ItemID
		}
	}
	conflicts, err := item.ReadConflicts(r)
	if err != nil {
		return nil, readFailure(err)
	}
	if tick.AvailableLanes == nil {
		rows, err := r.Query(`SELECT lane_id FROM lanes ORDER BY lane_id`)
		if err != nil {
			return nil, readFailure(err)
		}
		var offered []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, readFailure(err)
			}
			offered = append(offered, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, readFailure(err)
		}
		rows.Close()
		tick.AvailableLanes = &offered
	}
	commentHolds := map[string]string{}
	for _, row := range items {
		if hasPrerequisite(def, definition.PrerequisiteDesignQuestions) && item.IsCommentItem(row.ItemID) && !finishedState(def, row.State) {
			commentHolds[plannerDocumentKey(root, row.DesignDoc)] = row.ItemID
		}
	}
	p := &planner{
		definition:   &def,
		commentHolds: commentHolds, documentRoot: root,
		conflicts: conflicts,
		shut:      shut,
		quotaShut: quotaShut,
		forks:     forks,
		tick:      tick, policy: policy, held: lanes, deskOwners: deskOwners, itemStates: states, history: history,
		priors: attemptPriors, retries: retries, classes: classes, latest: latest, questions: questions,
		trunkWaiters: trunkWaiters, defectWaiters: defectWaiters, escalated: escalated,
		at: at, assigned: map[string]bool{}, fixerOpen: trunkFixerOpen(items, def), unproved: unproved,
		inputs: inputs, inputDecisions: inputDecisions, works: works, attempts: map[string]int{},
	}
	p.workerLanes = map[string]string{}
	for _, row := range items {
		if row.State != p.rules().Ready {
			continue
		}
		if c, ok := conflicts[row.ItemID]; ok && c.Status != "answered" {
			continue
		}
		if e := p.returnedEnding(row.ItemID); e != nil && e.WorkerSession != nil && p.offersLane(e.WorkerSession.LaneID) {
			p.workerLanes[e.WorkerSession.LaneID] = row.ItemID
		}
	}
	var hasNodes bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil {
		return nil, readFailure(err)
	}
	for _, row := range items {
		var count int
		if hasNodes {
			if err := r.QueryRow(`SELECT COALESCE(MAX(attempt),0) FROM node_runs WHERE item_id=?`, row.ItemID).Scan(&count); err != nil {
				return nil, readFailure(err)
			}
		}
		if count > 0 {
			p.attempts[row.ItemID] = count + 1
		} else {
			p.attempts[row.ItemID] = len(priors[row.ItemID]) + 1
		}
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, readFailure(err)
	}
	decisions := make([]record.Envelope, 0, len(items))
	for _, item := range items {
		if tick.examineItem != "" && item.ItemID != tick.examineItem {
			continue
		}
		var decision Decision
		if fault, held := faults[item.ItemID]; held && !p.finished(item.State) && holdEnabled(def, fault.Reason) {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: fault.Reason, LaneID: fault.LaneID, From: "held"}
			identifyDecision(&decision)
		} else if work := works[item.ItemID]; work != nil && !work.Done {
			// An already dispatched attempt advances before readiness and retry
			// rules: a terminal record is work to settle, not a retry to authorize.
			// An item already terminal by another route settles it unjudged instead
			// (terminal_item.go); a workflow item is released below on the same fact.
			settled := false
			if p.finished(item.State) && work.Decision.Workflow == nil {
				decision, settled, err = terminalSettlement(r, work, at)
				if err != nil {
					return nil, readFailure(err)
				}
			}
			if !settled {
				decision, err = pendingDecision(r, definitionWork(def, work), at, inputs[item.ItemID], tick.BriefTemplate, policy, shut, tick.GatesInFlight)
			}
			if err == nil && !settled && decision.Action == ActionJudge && item.JudgeMode == "agent" && item.JudgeGrant == "" {
				decision.Action, decision.Reason, decision.To = ActionHold, ReasonJudgeGrantAbsent, ""
				identifyDecision(&decision)
			}
			if err == nil && work.LaneReleased && decision.Action != ActionHold {
				lane, ok := p.freeLane(item.Express)
				if !ok {
					decision.Action, decision.Reason, decision.To = ActionHold, ReasonNoLane, ""
				} else {
					decision.LaneID = lane
					req := *decision.RunRequest
					req.LaneID = lane
					decision.RunRequest = &req
					p.assigned[lane] = true
				}
				identifyDecision(&decision)
			}
			if err == nil && work.Decision.Workflow != nil && !work.BoundaryFailed {
				invalid := item.State != def.Ready || p.isBlocked(item, edges[item.ItemID]) || p.questionState(item).reason != ""
				// The persisted graph, including source digests, owns this attempt.
				// Editing its source does not invalidate or replace an in-flight node, and
				// neither does an edit to the item's block or fence: the owner ruled on
				// 2026-11-17 that such an edit is no fact for the work (audit finding F-07).
				if invalid {
					leases, leaseErr := activeLeases(r, at, []string{decision.LaneID})
					if leaseErr != nil {
						return nil, readFailure(leaseErr)
					}
					if len(leases) == 0 {
						decision = work.Decision
						decision.From = "running"
						decision.Action, decision.Reason, decision.To = ActionSettle, "workflow-invalidated", "held"
						decision.WorkflowRelease = true
						identifyDecision(&decision)
					}
				}
			}
			if err != nil {
				return nil, readFailure(err)
			}
		} else if rewriteOwnerHolds[item.ItemID] != "" && !p.finished(item.State) {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: rewriteOwnerHolds[item.ItemID], From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if rewriteHolds[item.ItemID] != "" && !p.finished(item.State) {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: ReasonAwaitsRewrite, DeferredBy: rewriteHolds[item.ItemID], From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if unshared[item.ItemID] {
			decision, err = p.triageDecision(r, item)
			if err != nil {
				return nil, err
			}
		} else {
			w, selected, source, loadErr := selectWorkflow(r, root, item, tick.WorkflowDefaults, policy)
			if loadErr == nil && def.Completes != "landing" && evaluatorSlot(w) != "" {
				loadErr = fmt.Errorf("workflow-evaluator-requires-landing: evaluator_slot judges a Git candidate; use an ordinary agent node and the definition's completion hook instead")
			}
			routed := item
			resumeNode := 0
			retainedGraph := false
			if e := p.returnedEnding(item.ItemID); e != nil && e.WorkerSession != nil && e.WorkerSession.Workflow != nil && policy != nil && p.offersLane(e.WorkerSession.LaneID) {
				s := e.WorkerSession
				if s.NodeIndex >= 0 && s.NodeIndex < len(s.Workflow.Stages) {
					stage := s.Workflow.Stages[s.NodeIndex]
					backend := policy.firstBackend(itemRow{ItemID: item.ItemID, Seat: stage.Seat})
					backend, _, open := policy.availableBackend(backend, stage.Seat, p.shut)
					if open && workerSessionReason(s, policy.request(item.ItemID, s.LaneID, attemptRunID(item.ItemID, p.nextAttempt(item.ItemID)), backend)) == "resumed-worker" {
						w, resumeNode, loadErr = s.Workflow, s.NodeIndex, nil
						retainedGraph = true
					}
				}
			}
			if w != nil {
				if loadErr == nil {
					if evaluatorSlot(w) != "" {
						routed.JudgeMode = "agent"
						routed.JudgeGrant, err = judgeGrant(r, item.ItemID, at)
						if err != nil {
							return nil, readFailure(err)
						}
					}
					routed.Seat = w.Stages[resumeNode].Seat
					if w.Stages[resumeNode].Kind == "command" {
						routed.Command = &w.Stages[resumeNode]
					}
				}
			}
			hold := ""
			_, hasRetry := retries[item.ItemID]
			if w != nil && hasNodes && !p.finished(item.State) && !hasRetry {
				hold, err = commandNodeHold(r, item.ItemID)
				if err != nil {
					return nil, readFailure(err)
				}
			}
			if loadErr == nil && w != nil {
				if scopeErr := w.checkScopes(item.Allowed); scopeErr != nil {
					hold = scopeErr.Error()
				} else {
					loadErr = inheritWorkflowScope(w, WorkflowScope{Paths: fencePaths(item.Allowed)})
				}
			}
			if hold != "" {
				decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: hold, From: "held", Attempt: p.nextAttempt(item.ItemID), Workflow: w}
				if work := works[item.ItemID]; work != nil && work.Decision.Workflow != nil {
					decision.Workflow = work.Decision.Workflow
					decision.NodeIndex = work.Decision.NodeIndex
					decision.Attempt = work.Decision.Attempt
				}
			} else if loadErr != nil {
				decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: ReasonWorkflowInvalid + ": " + strings.SplitN(loadErr.Error(), "\n", 2)[0], From: "held", Attempt: p.nextAttempt(item.ItemID)}
			} else {
				decision = p.decide(routed, edges[item.ItemID])
				if decision.RunRequest != nil && decision.Returned != nil && decision.Returned.WorkerSession != nil && decision.Returned.WorkerSession.Workflow != nil && !retainedGraph {
					decision.RunRequest.SessionTurn = false
					decision.RunRequest.ResumePrompt = ""
					decision.SelectSeat = true
					if decision.WorkerSessionReason == "resumed-worker" {
						decision.WorkerSessionReason = "workflow-restarted"
					}
				}
				decision.SelectedWorkflow, decision.WorkflowSource = selected, source
				if w != nil && decision.RunRequest != nil {
					decision.Workflow = w
					decision.NodeIndex = resumeNode
					if decision.RunRequest.SessionTurn && decision.Returned.WorkerSession.Workflow != nil {
						decision.NodeBaseSHA = decision.Returned.WorkerSession.NodeBaseSHA
					}
					request := *decision.RunRequest
					request.RunID += "-n" + strconv.Itoa(resumeNode+1)
					decision.RunRequest = &request
					if routed.Command != nil {
						decision.RunRequest = routed.Command.commandRequest(item.ItemID, decision.LaneID, request.RunID)
					} else if w.Stages[resumeNode].TimeoutMS > 0 {
						decision.RunRequest.TimeoutMS = w.Stages[resumeNode].TimeoutMS
					}
				}
			}
			identifyDecision(&decision)
		}
		if decision.Workflow != nil && decision.RunRequest != nil {
			request := *decision.RunRequest
			request.NodeID = decision.Workflow.Stages[decision.NodeIndex].ID
			decision.RunRequest = &request
		}
		if err := describeBackendHold(r, &decision, policy); err != nil {
			return nil, err
		}
		if def.Completes != "landing" && decision.RunRequest != nil && decision.RunRequest.WorkingDir == WorktreeDir {
			request := *decision.RunRequest
			request.WorkingDir = PlainDir
			decision.RunRequest = &request
		}
		if decision.Inputs == nil && decision.RunRequest != nil && (decision.Action == ActionDispatch || decision.Action == ActionRetry) {
			decision.DispatchBuild = buildHash
		}
		env, err := decisionEnvelope(decision, at)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, env)
	}
	return decisions, nil
}

// planner is one tick's decision state: the tick, the policy, the store's item states and
// the lanes it holds, the clock, and the lanes and capacity already spent by earlier
// items in this tick.
func plannerDocumentKey(root, doc string) string {
	if root != "" && !filepath.IsAbs(doc) {
		doc = filepath.Join(root, doc)
	}
	return filepath.Clean(doc)
}

type planner struct {
	workerLanes map[string]string
	definition  *definition.Definition
	// An open review batch holds new build dispatches for its document; workers
	// already running settle normally, without being interrupted by intake.
	commentHolds map[string]string
	documentRoot string
	attempts     map[string]int
	shut         map[string]bool
	// quotaShut leaves out backends closed by an infrastructure ending: a retry
	// the owner authorized is the one dispatch that may go to one.
	quotaShut      map[string]bool
	conflicts      map[string]item.Conflict
	inputs         map[string]*RunInputs
	inputDecisions map[string]Decision
	works          map[string]*pendingWork
	tick           Pass
	policy         *Policy
	held           map[string]bool
	deskOwners     map[string]string
	itemStates     map[string]string
	history        runHistory
	priors         map[string][]priorRun
	// retries is each item's latest operator authorization, keyed by item; classes is
	// every prior run's attempt class, keyed by run id; latest is each item's latest
	// terminal run id. Together they let decide answer "is this authorization's run one
	// the harness refused?" from the record rather than from the claim alone.
	retries map[string]retryRow
	classes map[string]AttemptClass
	latest  map[string]string
	// questions answers what each item's design document still asks. It is the planner's
	// only read outside the store, and it is nil-safe: a nil source answers "unknown".
	questions  QuestionSource
	at         time.Time
	dispatched int
	assigned   map[string]bool
	// forks includes live leases and unfinished examinations, then adds this plan's
	// own dispatches. Disjoint item fences can still deliver one patch branch.
	forks []plannedFork
	// fixerOpen is the open fixer item's id, or "" when the trunk is green. While it is
	// set, every ordinary item holds with ReasonTrunkBroken: staging ordinary work from a
	// base whose checks never passed is the defect the fixer exists to repair.
	fixerOpen string
	unproved  map[string]bool
	// A blocked acquittal names its own fixer even after the fixer closed unlanded.
	trunkWaiters map[string]string
	// defectWaiters names, per item, the tooling-defect item its tooling obstacle holds it
	// behind (defect.go).
	defectWaiters map[string]string
	// escalated is each item's backend after a seat obstacle (seat_escalation.go).
	escalated map[string]string
}

// firstBackend is the item's first backend: its seat escalation's when it has one, else the
// policy's.
func (p *planner) firstBackend(row itemRow) string {
	if name := p.escalated[row.ItemID]; name != "" {
		if _, ok := p.policy.Backends[name]; ok {
			return name
		}
	}
	return p.policy.firstBackend(row)
}

// plannedFork is one dispatched item and the fork whose patch branch it delivers, when it
// delivers one.
type plannedFork struct {
	itemID string
	fork   string
}

// decide answers one item's decision. The rules are applied in the order the brief
// fixes, first match wins: the item's own properties, then the tick's configuration,
// then the per-item resource answers. A caller asking "why won't this dispatch" gets the
// answer that would still hold if every resource were free.
func (p *planner) decide(item itemRow, blockedBy []string) Decision {
	decision := Decision{ItemID: item.ItemID, Seat: item.Seat}
	conflict, hasConflict := p.conflicts[item.ItemID]
	// An explicit owner disposition (apply/state) can release a blocked item.
	// Historical conflicts do not undo that act or hold terminal items.
	if !p.lands() || (item.State != p.rules().Blocked && conflict.Status != "answered" && conflict.Status != "question") || (conflict.Status == "question" && item.State != p.rules().Ready && item.State != p.rules().Blocked) {
		hasConflict = false
	}
	if hasConflict && conflict.Status != "closed" && conflict.Status != "answered" {
		return p.conflictDecision(item, conflict)
	}
	// The item's attempt and its authorization come first: whether its last run left a
	// failover to walk, and whether a person asked for a retry of that run, are both
	// properties of the item's own history. The authorization is resolved before the
	// switch so a claim the store refuses can name its reason and the run it names.
	attempt := p.attempt(item)
	retry := p.retryFor(item.ItemID)
	// No item is re-admitted because an input changed: the owner ruled on 2026-11-17 that
	// nothing re-runs because a hash of the block, the workflow, the fence, the backend
	// policy or the tillr build changed without knowing what the change means (audit
	// finding F-01). An item moves on an event that names it: the operator's retry, a
	// design answer for it, or the recovery of a killed run.
	if acquittedRefusal(p.history.refusals[p.latest[item.ItemID]]) {
		// A released acquittal starts fresh, including one whose dispatch had
		// consumed an owner retry.
		retry = nil
		attempt.failover, attempt.degraded = false, ""
		if p.policy != nil {
			attempt.backend = p.firstBackend(item)
		}
	}
	// A gate refusal, a run with no commit, a fence escape and a refused landing go back to
	// the item's worker with their evidence, every time and uncounted (returned.go). An
	// operator's retry and the recovery of an interrupted run are their own moves.
	returned := p.returnedEnding(item.ItemID)
	interrupted, restaged := false, false
	if priors := p.priors[item.ItemID]; len(priors) > 0 {
		interrupted = priors[len(priors)-1].Class == AttemptInterrupted
		restaged = priors[len(priors)-1].Restaged
	}
	if interrupted {
		// Repeat the interrupted execution, not an authorized retry or a
		// re-admission. Preserve its backend and do not walk the failover chain.
		attempt = attemptForInterruption(p.priors[item.ItemID])
		retry = nil
	}
	if retry != nil || interrupted {
		returned = nil
	}
	if returned != nil && p.policy != nil {
		// A returned ending is about the candidate, not the machinery: the next run starts
		// on the item's own backend.
		attempt.backend = p.firstBackend(item)
		attempt.failover, attempt.degraded = false, ""
	}
	if hasConflict && conflict.Status == "answered" && p.policy != nil {
		attempt.backend = p.firstBackend(item)
		attempt.failover, attempt.degraded = false, ""
		decision.ConflictID = conflict.ConflictID
	}
	windowShut := false
	backendWaitStart := ""
	if p.policy != nil && item.Command == nil {
		if retry != nil {
			attempt.backend = p.firstBackend(item)
		}
		if attempt.backend != "" {
			backendWaitStart = attempt.backend
			shut := p.shut
			if retry != nil && retry.refusal == "" && !p.quotaShut[attempt.backend] {
				shut = p.quotaShut
			}
			next, degraded, ok := p.policy.availableBackend(attempt.backend, item.Seat, shut)
			windowShut = !ok
			attempt.backend = next
			if degraded != "" {
				attempt.degraded = degraded
			}
		}
	}
	// The fence is read before the tick's resources because it is the item's own scope, and
	// a fence that names nothing is a refusal rather than a deferral: a rule that treated
	// it as overlapping everything would serialise the whole queue behind one blanket.
	fence := itemFence(item.Allowed)
	// One planner-level exclusion survives: two items that deliver one fork's patch branch
	// cannot run at once, however disjoint their own fences are, because the second would
	// move a branch the first is still building.
	heldBy, forkHeld := p.forkHeldBy(item.Allowed)
	// The design document is read before the tick's resources, and before the item's attempt
	// history: an item whose design still asks something is not dispatchable whatever else
	// is true, and answering `no-lane` would send an operator looking for a lane to solve a
	// problem a ruling solves.
	questions := p.questionState(item)
	switch {
	case p.lands() && p.trunkWaiters[item.ItemID] != "":
		decision.Action, decision.Reason = ActionHold, ReasonTrunkBroken
		decision.DeferredBy = p.trunkWaiters[item.ItemID]
	case p.lands() && p.defectWaiters[item.ItemID] != "":
		decision.Action, decision.Reason = ActionHold, ReasonToolingDefect
		decision.DeferredBy = p.defectWaiters[item.ItemID]
	case p.isBlocked(item, blockedBy):
		decision.Action, decision.Reason = ActionHold, ReasonBlocked
	case p.lands() && p.unproved[item.ItemID]:
		decision.Action, decision.Reason = ActionHold, ReasonTrunkUnproved
	case p.lands() && p.fixerOpen != "" && !IsTrunkFixer(item.ItemID):
		// The trunk is red and a fixer owns the repair. Every ordinary item waits: a run
		// staged from a base that never passed would produce a candidate the same red gate
		// refuses, and the queue would spend itself proving the same breakage one item at a
		// time. The fixer itself is exempt and dispatches normally.
		decision.Action, decision.Reason = ActionHold, ReasonTrunkBroken
		decision.DeferredBy = p.fixerOpen
	case item.State != p.rules().Ready:
		decision.Action, decision.Reason = ActionHold, ReasonNotReady
	case item.JudgeMode == "agent" && item.JudgeGrant == "":
		decision.Action, decision.Reason = ActionHold, ReasonJudgeGrantAbsent
	case hasPrerequisite(p.rules(), definition.PrerequisiteDesignQuestions) && !itemIsComment(item.ItemID) && p.commentHolds[plannerDocumentKey(p.documentRoot, item.DesignDoc)] != "":
		decision.Action, decision.Reason = ActionHold, ReasonDesignUnderComment
		decision.DeferredBy = p.commentHolds[plannerDocumentKey(p.documentRoot, item.DesignDoc)]
	case questions.reason != "":
		decision.Action, decision.Reason = ActionHold, questions.reason
		decision.QuestionID = questions.questionID
	case retry != nil && retry.refusal != "":
		// The operator claimed this run was the harness's failure and the store says
		// otherwise: the claim is recorded, and the decision names why it does not hold.
		// Refusing here is what keeps a judged candidate from being retried by link.
		decision.Action, decision.Reason = ActionHold, retry.refusal
		decision.RetryID = retry.retryID
		decision.SupersedesRunID = retry.supersedesRunID
	case retry == nil && !interrupted && !attempt.failover && returned == nil && p.mayBeARetry(item.ItemID) && decision.ConflictID == "" && holdEnabled(p.rules(), p.retryHoldReason(item.ItemID)):
		decision.Action, decision.Reason = ActionHold, p.retryHoldReason(item.ItemID)
	case holdEnabled(p.rules(), ReasonNoFence) && fenceAdmitsNothing(fence):
		decision.Action, decision.Reason = ActionHold, ReasonNoFence
	case p.lands() && forkHeld:
		decision.Action, decision.Reason = ActionHold, ReasonForkHeld
		decision.DeferredBy = heldBy
	case p.policy == nil:
		decision.Action, decision.Reason = ActionHold, ReasonNoPolicy
	case windowShut:
		decision.Action, decision.Reason = ActionHold, ReasonBackendWindow
		decision.DeferredBy = backendWaitStart
	case p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches:
		decision.Action, decision.Reason = ActionHold, ReasonNoLane
	default:
		lane, ok := p.freeLane(item.Express)
		if returned != nil && returned.WorkerSession != nil && p.offersLane(returned.WorkerSession.LaneID) {
			lane = returned.WorkerSession.LaneID
			ok = !p.held[lane] && !p.assigned[lane]
		}
		if doc := deskDocument(item, p.tick.Comments); doc != "" {
			lane = commentDesk(doc)
			if p.held[lane] || p.assigned[lane] {
				decision.Action, decision.Reason = ActionHold, ReasonDocumentDeskBusy
				decision.DeferredBy = p.deskOwners[lane]
				break
			}
			if p.tick.Comments != nil && countDocumentDesks(p.held, p.assigned) >= p.tick.Comments.MaxRunning {
				decision.Action, decision.Reason = ActionHold, ReasonDeskCapacity
				decision.DeferredBy = doc
				break
			}
			decision.DeskDocument = doc
			p.deskOwners[lane] = item.ItemID
			ok = true
		} else if !ok && IsTrunkFixer(item.ItemID) {
			// Retained candidates can occupy every offered lane while waiting
			// on this fixer. One serial repair desk breaks that dependency cycle
			// without discarding their worktrees or admitting ordinary work.
			lane = "desk-trunk-fixer"
			ok = !p.held[lane] && !p.assigned[lane]
		}
		if !ok {
			decision.Action, decision.Reason = ActionHold, ReasonNoLane
			break
		}
		p.assigned[lane] = true
		p.dispatched++
		p.forks = append(p.forks, plannedFork{itemID: item.ItemID, fork: forkOf(item.Allowed)})
		decision.LaneID = lane
		decision.Inputs = p.inputs[item.ItemID]
		if retry != nil {
			// A retry is a fresh attempt, not a failover: it starts on the backend the
			// item's first dispatch would have used, and the operator's authorization is
			// what overrides the failover budget rather than a second automatic walk.
			decision.Action = ActionRetry
			decision.Reason = ReasonRetry
			decision.RetryID = retry.retryID
			decision.SupersedesRunID = retry.supersedesRunID
			decision.RunRequest = p.request(item.ItemID, lane, attempt.backend)
			break
		}
		decision.Action = ActionDispatch
		decision.Reason = ReasonReady
		if attempt.failover {
			decision.Reason = ReasonFailover
		}
		if attempt.degraded != "" {
			// The walk left the backend the item's seat names. That is a model switch,
			// and the reason says which seat fell back and onto which backend rather
			// than letting it read as an ordinary failover.
			decision.Reason = seatDegradedReason(attempt.degraded, attempt.backend)
		}
		decision.RunRequest = p.request(item.ItemID, lane, attempt.backend)
		if interrupted {
			decision.Reason = "earlier run orphaned by a loop stop"
		}
		if restaged {
			decision.Reason = ReasonRestagedStaleBase
		}
		if returned != nil {
			decision.Returned = returned
			decision.Reason = ReasonReturned
			decision.WorkerSessionReason = workerSessionReason(returned.WorkerSession, decision.RunRequest)
			if returned.WorkerSession != nil && !p.offersLane(returned.WorkerSession.LaneID) {
				decision.WorkerSessionReason = "lane-unavailable"
			}
			if decision.WorkerSessionReason == "resumed-worker" {
				decision.RunRequest.SessionTurn = true
				decision.RunRequest.ResumePrompt = returnedBrief(*returned)
			}
		}
		if decision.ConflictID != "" {
			decision.Reason = ReasonConflictAnswered
		}
	}
	decision.Attempt = p.nextAttempt(item.ItemID)
	decision.From = "held"
	if item.State == StateLanded {
		decision.From = "landed"
	}
	if decision.Action == ActionDispatch || decision.Action == ActionRetry {
		decision.From, decision.To = "queued", "running"
		decision.SelectSeat = !decision.RunRequest.SessionTurn && !interrupted && (!attempt.failover || retry != nil || decision.ConflictID != "")
	}
	if decision.DeskDocument != "" && decision.RunRequest != nil {
		previousAdapter := ""
		if item.Designer != nil {
			previousAdapter = p.policy.Backends[item.Designer.Backend].Adapter
		}
		decision.Designer = item.Designer
		decision.DesignerSessionID, decision.DesignerReason = chooseDesigner(item.Designer, decision.RunRequest, p.at, previousAdapter)
	}
	identifyDecision(&decision)
	return decision
}

// identifyDecision names the decision's current position, including loop-only holds
// that replace a dispatch after staging refused the item's record.
func identifyDecision(decision *Decision) {
	// A resource deferral does not move an otherwise queued item into held.
	// Pending runs and candidates retain their independently derived positions.
	if decision.Action == ActionHold && decision.From == "held" && (decision.Reason == ReasonNoLane || decision.Reason == ReasonForkHeld || decision.Reason == ReasonDeskCapacity || decision.Reason == ReasonDocumentDeskBusy) {
		decision.From = "queued"
	}
	decision.Position, decision.Transition = decision.From, decision.Action
	if decision.Action == ActionHold {
		decision.Transition = "none"
	}
	decision.TransitionID = "transition-" + decision.ItemID + "-a" + strconv.Itoa(decision.Attempt)
	if decision.Workflow != nil {
		decision.TransitionID += "-n" + strconv.Itoa(decision.NodeIndex+1)
	}
	if decision.WorkflowRestart && decision.RunRequest != nil {
		decision.TransitionID += "-" + decision.RunRequest.RunID
	}
	decision.TransitionID += "-" + decision.From
	if decision.To != "" {
		decision.TransitionID += "-" + decision.To
	}
	decision.DecisionID = "decision-" + strings.TrimPrefix(decision.TransitionID, "transition-")
	// One candidate can be examined on several lanes without starting another
	// attempt. The decision identity must distinguish those resource claims.
	if decision.From == "candidate" && decision.Action != ActionHold {
		decision.DecisionID += "-lane-" + decision.LaneID
	}
	if decision.Action == ActionHold {
		// A hold is not a transition. Its reason and the names explaining that reason
		// distinguish it from other holds at this position. Hash the JSON array so
		// punctuation in a name cannot make two different payloads share an id.
		payload, _ := json.Marshal([]string{decision.Reason, decision.DeferredBy, decision.Seat,
			decision.QuestionID, decision.RetryID, decision.SupersedesRunID})
		decision.DecisionID += "-" + fmt.Sprintf("%x", sha256.Sum256(payload))
	}
}

// questionState is what the planner learned about one item's design document: the hold the
// document answers with, if any, and the question that hold names.
type questionState struct {
	reason     string
	questionID string
}

// questionState answers what the item's design document still asks, from the questions
// source. Four answers, and the three that hold are kept apart on purpose:
//
//   - the document is in the tree and the index reports an unanswered question: hold
//     `question-open`, naming that question;
//   - the document is in the tree and nothing is open: no hold;
//   - the tree does not hold the document: hold `design-doc-missing`, which is a fact the
//     source read from the tree and not from the tool, so it stays the answer even when the
//     tool is absent;
//   - the source cannot say — the tool is absent, failed, timed out, or the planner was
//     given no source — hold `questions-unknown`. Unknown is never read as "no questions".
//
// An item that names no document at all is not one of the four: it has no document to be
// missing, and its question state is simply not a fact this planner needs.
func (p *planner) questionState(item itemRow) questionState {
	checkDocument := hasPrerequisite(p.rules(), definition.PrerequisiteDesignDocument)
	checkQuestions := hasPrerequisite(p.rules(), definition.PrerequisiteDesignQuestions)
	if (!checkDocument && !checkQuestions) || itemIsComment(item.ItemID) || item.DesignDoc == "" {
		return questionState{}
	}
	if p.questions == nil && !checkQuestions {
		return questionState{reason: ReasonDesignDocMissing}
	}
	if p.questions == nil {
		return questionState{reason: ReasonQuestionsUnknown}
	}
	var answer DocumentQuestions
	var err error
	if source, ok := p.questions.(*VantageQuestions); ok && !checkQuestions {
		answer, err = source.DocumentPresence(item.DesignDoc)
	} else {
		answer, err = p.questions.Document(item.DesignDoc)
	}
	if err != nil {
		// The reason is the report. The error text is the tool's own and would be printed
		// once per item per tick, which is the log spam the decision record replaces.
		if checkQuestions {
			return questionState{reason: ReasonQuestionsUnknown}
		}
		return questionState{reason: ReasonDesignDocMissing}
	}
	if !answer.Present && checkDocument {
		return questionState{reason: ReasonDesignDocMissing}
	}
	if question, ok := firstUnanswered(withoutQuestions(answer.Questions, p.publishedForOthers(item))); checkQuestions && ok {
		return questionState{reason: ReasonQuestionOpen, questionID: question.ID}
	}
	return questionState{}
}

// itemFence is the fence the planner compares: the text staging writes to the lane
// (fenceText), parsed by the same parser the escape check reads it with (fenceEntries).
// Reading the derived text rather than the item's raw allowed_paths is what makes a fence
// that names a tillr module and a fence that names that module's pack copy overlap.
func itemFence(allowed []string) []string {
	return fenceEntries(fenceText(allowed))
}

// fenceAdmitsNothing answers whether a fence is unusable: it names no path at all, or it
// names the repository root. The store refuses the root spellings when an item is applied
// (item's checkAllowedPaths calls them a wildcard), but a plan reads a store rather than an
// apply, and a hand-seeded or pre-column row can still carry one. Under the escape check's
// own containment such an entry admits nothing, so a candidate would be refused whatever it
// changed. Both are refused rather than deferred: a fence that would fence nothing is not
// one, and treating it as "overlaps everything" would serialise the whole queue behind one
// blanket.
func fenceAdmitsNothing(fence []string) bool {
	if len(fence) == 0 {
		return true
	}
	for _, entry := range fence {
		switch entry {
		case "", ".", "./", "/", "..", "../":
			return true
		}
	}
	return false
}

// forkOf answers the fork an item delivers: the path component under
// `.forks/lanes/<lane>/`, or "" when the item names no fork checkout. The fork is the
// whole of the patch branch's identity — `.forks/lanes/lane-1/pi` and
// `.forks/lanes/lane-2/pi` both deliver `refs/heads/patch/pi` — so two items naming one
// fork cannot run at once whatever lanes they were staged on.
func forkOf(allowed []string) string {
	for _, raw := range allowed {
		cleaned := path.Clean(filepath.ToSlash(raw))
		rest, ok := strings.CutPrefix(cleaned, ".forks/lanes/")
		if !ok {
			continue
		}
		_, after, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		fork, _, _ := strings.Cut(after, "/")
		if fork == "" || fork == "." || fork == ".." {
			continue
		}
		return fork
	}
	return ""
}

// forkHeldBy answers the item this tick already dispatched whose fork-series delivery moves
// the same patch branch, and whether there is one. Only a dispatch that delivers a fork
// counts, and an item that names no fork is never held behind one.
func (p *planner) forkHeldBy(allowed []string) (string, bool) {
	fork := forkOf(allowed)
	if fork == "" {
		return "", false
	}
	for _, planned := range p.forks {
		if planned.fork == fork {
			return planned.itemID, true
		}
	}
	return "", false
}

// isBlocked answers whether the item is held by a blocker: its own state says so, or
// something it is blocked by has not reached a terminal state. A blocker the store does
// not hold blocks, which is the safe direction: an edge whose target cannot be read is
// not evidence that the target is done.
func (p *planner) isBlocked(item itemRow, blockedBy []string) bool {
	if item.State == p.rules().Blocked {
		return true
	}
	for _, blocker := range blockedBy {
		state, found := p.itemStates[blocker]
		if !found || !p.finished(state) {
			return true
		}
	}
	return false
}

// mayBeARetry answers whether dispatching this item could be a retry rather than a first
// dispatch. It is the conservative half of the retry rule: an item that may be one holds
// unless a person authorized the second attempt, so a run nobody asked to repeat never
// spends a second attempt by accident.
//
// Two kinds of evidence, and either one holds the item. A terminal run in the store is the
// direct evidence, now that D-UT19's runs.item_id carries the item→run link: an item with
// any run already recorded has been dispatched, so dispatching it again is a retry. The
// item's state history is the indirect evidence, kept because a run row can be missing
// where a state event is not: an item that has been in progress ran before, and an item
// whose history is not in the log at all is unknown rather than never-run.
//
// A failover is the one dispatch that is not a retry: it re-stages work whose attempt
// failed for the machinery's reasons, and decide applies it before this rule. A retry an
// operator authorized is applied before that too: the person's decision outranks the
// automatic walk.
func (p *planner) mayBeARetry(itemID string) bool {
	if acquittedRefusal(p.history.refusals[p.latest[itemID]]) {
		return false
	}
	return len(p.priors[itemID]) > 0 || !p.history.known[itemID] || p.history.ranBefore[itemID]
}

// retryHoldReason answers why a previously run item is held: the outcome of its latest
// terminal run, when the store can name it, and run-link-missing when the history is
// unclassifiable. The four named reasons are the outcomes a person acts on differently — a
// candidate the fence refused, a run that produced no commit, a run a wall cut short, and a
// gate failure — so the hold says which rather than the `not-ready` dead end the retired
// `in-progress` write left behind.
func (p *planner) retryHoldReason(itemID string) string {
	latest := p.latest[itemID]
	if latest == "" {
		return ReasonRunLinkMissing
	}
	if reason := p.history.judgeHolds[latest]; reason != "" {
		return reason
	}
	switch p.history.refusals[latest] {
	case ReasonEvalFailed, ReasonJudgeUnreadable, ReasonJudgeGrantAbsent, RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome:
		return p.history.refusals[latest]
	case RefusalNodeOutput:
		return ReasonNodeOutputMissing
	case RefusalFence:
		return ReasonBoundaryScopeEscape
	case RefusalNoOp:
		return ReasonCandidateNoOp
	case RefusalGate:
		return ReasonGateFailed
	case RefusalReverted:
		return ReasonLandingReverted
	case RefusalLand:
		return ReasonLandRefused
	case RefusalConflict:
		// The latest run hit a squash conflict that no conflict-fix run answered. Since the
		// owner's ruling of 2026-11-17 it goes back to the worker on the latest trunk
		// (returnedReasons); the name is the removed one-fix-run rule's.
		return ReasonConflictExhausted
	}
	// The gate's verdict outranks the attempt class. A run a wall cut short can still have
	// committed, and a committed candidate is judged before its wall status (design
	// D-RW2): once `gate.verdict` exists for the run, `gate-failed` is the hold, because
	// that is the action a person takes.
	if p.history.gateResults[latest] == GateFailed {
		return ReasonGateFailed
	}
	if p.classes[latest] == AttemptUnjudged {
		return ReasonRunUnjudged
	}
	return ReasonRunLinkMissing
}

// returnedEnding answers the item's latest run's ending when it goes back to the item's
// worker (ReasonReturned), or nil. It is the latest run's hold reason, read the way
// retryHoldReason reads it, for the four outcomes a worker can act on; the refusal's own
// record supplies the evidence.
func (p *planner) returnedEnding(itemID string) *ReturnedEnding {
	latest := p.latest[itemID]
	if latest == "" || !p.mayBeARetry(itemID) {
		return nil
	}
	cause, ok := returnedReasons[p.retryHoldReason(itemID)]
	if !ok {
		return nil
	}
	e := p.history.endings[latest]
	detail := e.Detail
	if cause == RefusalConflict && detail == "" {
		detail = strings.Join(e.ConflictPaths, "\n")
	}
	return &ReturnedEnding{RunID: latest, Cause: cause, Detail: detail, Checks: e.Checks, WorkerSession: e.WorkerSession}
}

// retry is one item's operator authorization, resolved against the store: which run it
// claims, and why that claim does not hold when it does not.
type retry struct {
	retryID         string
	supersedesRunID string
	// refusal is the hold's reason when the claim cannot be honoured, and "" when the
	// item may be retried.
	refusal string
}

// retryFor answers the item's authorization resolved against the store, or nil when the
// item has none. It is the distinction criterion: the claim names a run, and the record —
// the item's latest terminal run, the gate's `gate.verdict` event, the attempt class —
// decides whether it is retryable. A claim, not a guess.
func (p *planner) retryFor(itemID string) *retry {
	row, ok := p.retries[itemID]
	if !ok {
		return nil
	}
	// A pending build against a design answer takes precedence over any earlier
	// retry. The retry may have been consumed before the run that raised the conflict,
	// such as by the build whose conflict-fix run then needed a design ruling.
	if c, ok := p.conflicts[itemID]; ok && (c.Status == "answered" || c.Status == "closed") {
		if c.Status == "answered" {
			return nil
		}
		// Closed means a build already consumed the answer; preserve the existing
		// retirement of the authorization used by the conflict's raising run.
		if d := p.inputDecisions[c.RunID]; d.Action == ActionRetry && d.RetryID == row.RetryID {
			return nil
		}
	}
	r := &retry{retryID: row.RetryID, supersedesRunID: row.SupersedesRunID}
	if p.latest[itemID] != row.SupersedesRunID {
		// The authorization names a run that is no longer the item's latest: it was
		// consumed by the retry it authorized, or a newer run ended after it was written.
		// Either way it no longer speaks, and the item's latest ending routes it, as it
		// would with no authorization at all. Holding the item as `retry-stale` was a
		// one-run allowance per authorization; the owner's ruling of 2026-11-17 removes it.
		return nil
	}
	// The acknowledgment a retry carries is what makes a judged or unjudged run retryable:
	// the operator has named the outcome, and the plan honours that acknowledgment only
	// when it matches the store's own record of the run. An ack for another outcome is not
	// honored, so a retry row that somehow names the wrong one still refuses.
	if p.history.judged[row.SupersedesRunID] && row.Acknowledge != RetryAckGateFailed {
		r.refusal = ReasonRetryJudged
		return r
	}
	// The unjudged branch is guarded by `!judged` so a walled run that also reached the
	// gate keeps `gate-failed` authoritative: a run can now be both judged and
	// `AttemptUnjudged`, and demanding `unjudged` here would make every acknowledgment
	// unsatisfiable (design D-RW2).
	if !p.history.judged[row.SupersedesRunID] && p.classes[row.SupersedesRunID] == AttemptUnjudged && row.Acknowledge != RetryAckUnjudged {
		r.refusal = ReasonRetryUnjudged
		return r
	}
	return r
}

// indexPriorRuns answers two views of the run history the retry rule needs: every run's
// attempt class keyed by run id, and every item's latest terminal run id. The latest run is
// the last one readPriorRuns answered, which is ordered oldest first.
func indexPriorRuns(priors map[string][]priorRun) (map[string]AttemptClass, map[string]string) {
	classes := map[string]AttemptClass{}
	latest := map[string]string{}
	for itemID, runs := range priors {
		for _, prior := range runs {
			classes[prior.RunID] = prior.Class
		}
		if len(runs) > 0 {
			latest[itemID] = runs[len(runs)-1].RunID
		}
	}
	return classes, latest
}

// freeLane answers the first lane the tick offered that is free, in the tick's order so
// the choice is the caller's preference rather than the store's row order. A lane is
// free when the store does not hold it under a lease that is still live and this tick has
// not already assigned it.
func (p *planner) offersLane(id string) bool {
	for _, lane := range *p.tick.AvailableLanes {
		if lane == id {
			return true
		}
	}
	return false
}

func (p *planner) freeLane(express ...bool) (string, bool) {
	if len(express) > 0 && express[0] {
		for _, lane := range *p.tick.AvailableLanes {
			if strings.HasPrefix(lane, "express-") && !p.assigned[lane] && !p.held[lane] && p.workerLanes[lane] == "" {
				return lane, true
			}
		}
	}
	for _, lane := range *p.tick.AvailableLanes {
		if strings.HasPrefix(lane, "desk-") || strings.HasPrefix(lane, "express-") {
			continue
		}
		if p.assigned[lane] || p.workerLanes[lane] != "" {
			continue
		}
		if p.held[lane] {
			continue
		}
		return lane, true
	}
	return "", false
}

// request is the complete run.request a dispatch carries: design §5.6's field list,
// built from the backend the item's attempt resolved to.
func (p *planner) request(itemID, laneID, backendName string) *run.Request {
	return p.policy.request(itemID, laneID, attemptRunID(itemID, p.nextAttempt(itemID)), backendName)
}

func (p *planner) nextAttempt(id string) int {
	if n := p.attempts[id]; n > 0 {
		return n
	}
	return len(p.priors[id]) + 1
}

// attemptRunID names one item's ordinal attempt, including terminal failover runs.
func attemptRunID(itemID string, attempt int) string {
	return "run-" + itemID + "-a" + strconv.Itoa(attempt)
}

// An interruption retains the same backend; it is not a failover.
func attemptForInterruption(priors []priorRun) attempt {
	return attempt{backend: priors[len(priors)-1].Backend}
}

func (p *planner) attempt(row itemRow) attempt {
	var a attempt
	if p.policy == nil {
		return a
	}
	priors := p.priors[row.ItemID]
	if len(priors) == 0 || priors[len(priors)-1].Class != AttemptInfrastructure {
		a.backend = p.firstBackend(row)
		return a
	}
	next, degraded, ok := p.policy.nextBackend(priors[len(priors)-1].Backend, row.Seat)
	if !ok {
		a.backend = p.policy.firstBackend(row)
		return a
	}
	a.backend = next
	a.failover = true
	a.degraded = degraded
	return a
}

// attempt is one item's next dispatch as its run history resolves it.
type attempt struct {
	// backend is the name the request's backend field carries.
	backend string
	// failover says this dispatch re-stages an item whose last run failed in the
	// infrastructure class, so the decision's reason says so and the spend is visible.
	failover bool
	// degraded names the seat whose backend this walk leaves, or "": the failed backend
	// was the one the item's seat names, so the re-dispatch reaches another model and the
	// decision says so (seatDegradedReason).
	degraded string
}

// checkOverrides refuses a policy whose override map names an item the store does not
// hold. It is the half of the override check that needs the item set; Policy.Check refuses
// the half that does not (an unknown backend name).
func checkOverrides(policy *Policy, items map[string]string) error {
	if policy == nil {
		return nil
	}
	for itemID := range policy.Overrides {
		if _, ok := items[itemID]; !ok {
			return Usagef("the policy overrides item %q, which the store does not hold: a stale override is refused rather than ignored", itemID)
		}
	}
	return nil
}

// stamp is the tick's clock in the compact form the derived ids carry, so a decision and
// the run it requests are traceable to the tick that made them.
func stamp(at time.Time) string { return at.UTC().Format("20060102T150405Z") }

// decisionEnvelope wraps a decision in design §5.1's envelope. The subject is the item,
// so a reader of the record stream can find the decisions about one item without
// decoding every payload.
func decisionEnvelope(decision Decision, at time.Time) (record.Envelope, error) {
	body, err := json.Marshal(decision)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the decision for %q: %v", decision.ItemID, err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          DecisionKind,
		ID:            decision.DecisionID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at,
		Subject:       &record.Subject{Kind: "item", ID: decision.ItemID},
		Payload:       body,
	}, nil
}

// readItems answers every item in the store in the queue's order, which is the order this
// face decides in: ranked items first, by rank, and unranked items last in item_id order —
// the tiebreak that keeps two ticks over one store comparable files, and the rule an
// unranked store keeps from before rank existed.
//
// The rank is the sparse lexical token the capacity queue carries (QUEUE_RANK_DIGITS in
// pi-subagents' src/runs/background/queue-rank.ts): a plain string over lowercase letters
// and digits, which item's RankAlphabet admits and refuses anything else from, so the
// column's BINARY collation and the queue's code-unit comparison put every rank in the same
// place. Its sparseness is the point — a rank may be any length, so a value between two
// existing ones is inserted without renumbering either (`a`, then `am` between `a` and `b`).
//
// The first sort key is the unranked test rather than the rank itself, so an empty rank is
// its own class instead of a token at the start of the alphabet: an unranked item sorts
// after every ranked one whatever its token, and item_id keeps the order total among the
// unranked. Both halves are the query's because both are comparisons on the rows it reads.
func readItems(r *store.Reader) ([]itemRow, error) {
	var hasWorkflow bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='workflow')`).Scan(&hasWorkflow); err != nil {
		return nil, readFailure(err)
	}
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return nil, readFailure(err)
	}
	expressColumn := "0"
	if hasExpress {
		expressColumn = "COALESCE(express, 0)"
	}
	workflowColumn := "''"
	if hasWorkflow {
		workflowColumn = "COALESCE(workflow, '')"
	}
	var hasJudge bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='judge_mode')`).Scan(&hasJudge); err != nil {
		return nil, readFailure(err)
	}
	judgeColumn := "''"
	if hasJudge {
		judgeColumn = "COALESCE(judge_mode, '')"
	}
	rows, err := r.Query(
		`SELECT item_id, state, COALESCE(rank, ''), COALESCE(allowed_paths, ''), COALESCE(design_doc, ''),
		        COALESCE(seat, ''), ` + workflowColumn + `, ` + judgeColumn + `, ` + expressColumn + `
		   FROM items
		  ORDER BY CASE WHEN item_id LIKE 'fixer-main-%' THEN 0 WHEN item_id LIKE 'rewrite-item-%' THEN 1 ELSE 2 END,
		  COALESCE(rank, '') = '', COALESCE(rank, ''), item_id`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	items := []itemRow{}
	for rows.Next() {
		var (
			item    itemRow
			allowed string
		)
		if err := rows.Scan(&item.ItemID, &item.State, &item.Rank, &allowed, &item.DesignDoc, &item.Seat, &item.Workflow, &item.JudgeMode, &item.Express); err != nil {
			return nil, readFailure(err)
		}
		if item.Allowed, err = decodeFence(allowed); err != nil {
			return nil, err
		}
		item.Express = !itemIsComment(item.ItemID) && (item.Express || strings.HasPrefix(item.ItemID, "item-idea-"))
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return items, nil
}

// decodeFence reads allowed_paths out of its column. The column is item's: a JSON list,
// empty when the item names none (item's encodeAllowedPaths), so the planner reads it the
// same way item's own decoder does. A malformed value is a defect in the store rather than
// a missing fence, so it is refused instead of read as one.
func decodeFence(stored string) ([]string, error) {
	if stored == "" {
		return nil, nil
	}
	var paths []string
	if err := json.Unmarshal([]byte(stored), &paths); err != nil {
		return nil, Internalf("reading an item's allowed_paths %q: %v", stored, err)
	}
	return paths, nil
}

// checkSeats refuses a plan whose items ask for a seat this policy cannot route, before a
// single decision is built. Two defects are refused, and both name the seat and the seats
// the policy does map:
//
//   - an item names a seat the item model does not define, which is a typo in the store
//     rather than a dispatch; and
//   - an item names a seat the policy does not map, which is the one outcome the seat layer
//     exists to forbid: a silent fall-through to the tick's default backend would send
//     design work to the build model and record it as though it were intended.
//
// The whole plan is refused rather than one item, because a seat the policy cannot route is
// a document to fix, not a per-item decision: the policy is the only place a seat may be
// silenced, and doing it by omission is exactly what this check refuses.
func checkSeats(policy *Policy, items []itemRow) error {
	if policy == nil {
		return nil
	}
	for _, it := range items {
		if it.JudgeMode == "agent" && it.JudgeGrant != "" && len(policy.seatList("review")) == 0 {
			return Usagef("item %q asks for an agent judge, but the policy does not map the review seat; %s", it.ItemID, policy.seatDeclaration())
		}
		if it.Seat == "" {
			continue
		}
		if len(policy.seatList(it.Seat)) == 0 {
			return Usagef("item %q asks for seat %q, which the policy does not map: the policy maps %s; %s; a seat is never defaulted to the tick's backend",
				it.ItemID, it.Seat, mappedSeats(policy), policy.seatDeclaration())
		}
	}
	return nil
}

// mappedSeats answers the seats a policy maps in a stable order, so a refusal reads the
// same twice and names what the operator can choose from. A policy that maps none is not
// an empty string in the message: "none" is the answer, and it is what makes the refusal
// say plainly that the seat layer has not been set up at all.
func mappedSeats(policy *Policy) string {
	names := policy.seatNames()
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// readEdges answers every blocked_by edge in the store, keyed by the blocked item.
func readEdges(r *store.Reader) (map[string][]string, error) {
	rows, err := r.Query(`SELECT item_id, blocked_by FROM item_edges ORDER BY item_id, blocked_by`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	edges := map[string][]string{}
	for rows.Next() {
		var itemID, blockedBy string
		if err := rows.Scan(&itemID, &blockedBy); err != nil {
			return nil, readFailure(err)
		}
		edges[itemID] = append(edges[itemID], blockedBy)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return edges, nil
}

// readLanes answers the lanes the store holds under a live lease, keyed by lane id, at
// the tick's clock. A lane the store does not hold has no lease, which is why the map is
// read rather than required to be complete.
//
// A lease at or before now has expired, and the lane is not held (design §5.5): the same
// rule `run`'s reaper acts on, read here because `plan` is a pure function that writes
// nothing and must answer about the lane it would find after the reap. `once` reaps
// before it plans for that reason, and the two cannot disagree about which lanes are
// expired because they compare the same column against the same clock.
func readLanes(r *store.Reader, now time.Time) (map[string]bool, error) {
	rows, err := r.Query(`SELECT lane_id, state, leased_until FROM lanes`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	held := map[string]bool{}
	for rows.Next() {
		var (
			laneID      string
			state       string
			leasedUntil sql.NullInt64
		)
		if err := rows.Scan(&laneID, &state, &leasedUntil); err != nil {
			return nil, readFailure(err)
		}
		// A leased lane with no expiry cannot be judged by the clock, so it is held: an
		// operator releases it by hand, which is the one answer that does not need a
		// clock.
		held[laneID] = state == LaneStateLeased && (!leasedUntil.Valid || leasedUntil.Int64 > now.UnixMilli())
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return held, nil
}

// LaneLease is one lane the store holds under a live lease at a tick's clock. A lease
// with no expiry is a different fact from a finite one: no clock will free it, so it is
// held until an operator releases the lane.
//
// It is the stop check's read and not the planner's: the planner only needs to know
// whether a lane is free, while the stop check needs to tell a lane waiting on the clock
// from a lane waiting on a person.
type LaneLease struct {
	LaneID      string
	LeasedUntil *time.Time
}

// activeLeases answers the leases the store holds on the given lanes at now, in lane-id
// order so two reads over one store answer the same sequence. A lease at or before now is
// expired and not returned: the caller reads this after the reap, and a lease the reap
// left behind is one a clock can still free.
//
// Only the lanes the loop offers are read. A lane outside that list cannot make an item
// wait, so a lease on it is not a reason to hold the loop: releasing it would not let the
// loop dispatch anything it could not already dispatch.
func activeLeases(r *store.Reader, now time.Time, lanes []string) ([]LaneLease, error) {
	offered := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		offered[lane] = true
	}
	rows, err := r.Query(`SELECT lane_id, state, leased_until FROM lanes ORDER BY lane_id`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	leases := []LaneLease{}
	for rows.Next() {
		var (
			laneID      string
			state       string
			leasedUntil sql.NullInt64
		)
		if err := rows.Scan(&laneID, &state, &leasedUntil); err != nil {
			return nil, readFailure(err)
		}
		if !offered[laneID] || state != LaneStateLeased {
			continue
		}
		if !leasedUntil.Valid {
			leases = append(leases, LaneLease{LaneID: laneID})
			continue
		}
		until := time.UnixMilli(leasedUntil.Int64).UTC()
		if until.After(now) {
			leases = append(leases, LaneLease{LaneID: laneID, LeasedUntil: &until})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return leases, nil
}

// runHistory is what the event log says about items' prior runs: whether an item's history
// is known at all, whether it has ever been in progress, and what the loop recorded about a
// run's outcome.
type runHistory struct {
	known     map[string]bool
	ranBefore map[string]bool
	// judged is every run the gate answered a `gate.verdict` for. It is the evidence a
	// retry claim is checked against: a candidate refused at the fence exits 0 and was
	// judged by nobody, while a candidate the gate judged carries the event whether it
	// passed or failed.
	judged map[string]bool
	// refusals is every run a `drain.refusal` event names, keyed by run id, valued by the
	// step that refused it (`fence` or `no-op`). It is what tells a fence refusal and a
	// candidate with no commit apart from an ordinary answered run, whose own record says
	// neither.
	refusals map[string]string
	// Judge holds gate item progress without classifying its writer as failed.
	judgeHolds map[string]string
	// gateResults is every run the gate answered, keyed by run id, valued by the gate's own
	// `result` (`pass`, `fail`, `error`). `judged` says the gate ran; this says what it
	// said, which is the difference between a hold for a red gate and one for a land that
	// declined a passing candidate.
	gateResults map[string]string
	// endings is every refusal's own record, keyed by run id: its detail, its failing
	// checks and its evidence key. A returned ending is built from it.
	endings map[string]refusalPayload
}

// readRunHistory reads the item-object events and answers, per item, whether its history
// is known and whether it has ever been in progress.
//
// The events are filtered by object type and by the presence of a state in the payload
// rather than by kind, so this reader does not carry a second copy of item's kind names:
// an item state write is an event about an item that says what state the item is in,
// whatever kind item chose to append.
func readRunHistory(r *store.Reader) (runHistory, error) {
	events, err := r.Events(0)
	if err != nil {
		return runHistory{}, readFailure(err)
	}
	history := runHistory{
		known: map[string]bool{}, ranBefore: map[string]bool{}, judged: map[string]bool{},
		refusals: map[string]string{}, judgeHolds: map[string]string{}, gateResults: map[string]string{},
		endings: map[string]refusalPayload{},
	}
	for _, e := range events {
		if e.Kind == GateVerdictKind && e.ObjectType == "run" && e.ObjectID != nil {
			history.judged[*e.ObjectID] = true
			var payload struct {
				Result string `json:"result"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil && payload.Result != "" {
				history.gateResults[*e.ObjectID] = payload.Result
			}
			continue
		}
		if e.Kind == RefusalKind && e.ObjectType == "run" && e.ObjectID != nil {
			var payload struct {
				refusalPayload
				WriterRunID string `json:"writer_run_id"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil && payload.Step != "" {
				history.refusals[*e.ObjectID] = payload.Step
				history.endings[*e.ObjectID] = payload.refusalPayload
				if payload.WriterRunID != "" {
					history.judgeHolds[payload.WriterRunID] = payload.Step
				}
			}
			continue
		}
		if e.ObjectType != "item" || e.ObjectID == nil {
			continue
		}
		var payload struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.State == "" {
			// An item event that does not say what state the item is in is not evidence
			// about the item's runs, so it leaves the item's history as it was.
			continue
		}
		history.known[*e.ObjectID] = true
		if payload.State == StateInProgress {
			history.ranBefore[*e.ObjectID] = true
		}
	}
	return history, nil
}

// readFailure classifies a failed read. A store that does not carry item's or run's
// tables is incompatible with this binary rather than a bug in it: exit 3 sends the
// caller to the store it named, and exit 5 would send it looking for a defect that is
// not there.
func readFailure(err error) error {
	if strings.Contains(err.Error(), "no such table") {
		return Incompatiblef("%v", err)
	}
	return Internalf("%v", err)
}

// publishedForOthers answers the ids of the questions published in the item's design
// document for other items' open conflicts. Each holds only the item it was published for.
func (p *planner) publishedForOthers(row itemRow) []string {
	var ids []string
	for itemID, c := range p.conflicts {
		file, id, _ := strings.Cut(c.QuestionRef, "#")
		if itemID != row.ItemID && c.Status == "question" && file == row.DesignDoc && id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
