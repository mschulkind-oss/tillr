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
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Pass is the pure planner's input. The store supplies lanes when none are offered.
// MaxDispatches optionally limits a simulation; the live loop never sets it.
// Now optionally supplies observation metadata, never an identity.
type Pass struct {
	// Operator examination considers one item's next step while still reading
	// every existing lane reservation and readiness dependency. It must not
	// allocate free lanes to sibling plans that this command will not execute.
	examineItem string
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
	// Parallel lists sibling readers claimed with this node; the item retains one pending owner.
	Parallel []Decision `json:"parallel,omitempty"`
	// Designer is the row read when the session choice was made, not a later reread.
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
	Readmitted        bool           `json:"readmitted,omitempty"`
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
	// claim — `retry-judged`, `retry-unjudged`, `retry-stale` — names the same pair, so an
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
}

// itemRow is the item state this face reads: the columns a decision turns on, and the
// rank that decides the order the decisions come in.
type itemRow struct {
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
	if err := tick.check(); err != nil {
		return nil, err
	}
	at, err := tick.At()
	if err != nil {
		return nil, err
	}
	shut, err := readShutBackends(r, at)
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
		if item.IsCommentItem(items[i].ItemID) {
			items[i].Designer, err = readDesigner(r, items[i].DesignDoc)
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
			if history.refusals[prior.RunID] != RefusalTrunkAlreadyRed {
				attemptPriors[id] = append(attemptPriors[id], prior)
			}
		}
	}
	trunkWaiters := map[string]string{}
	for _, row := range items {
		if row.State != StateBlocked {
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
		}
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
	unshared, err := documentPrerequisites(r, root)
	if err != nil {
		return nil, err
	}
	inputs := map[string]*RunInputs{}
	for _, row := range items {
		if row.State != StateInQueue || unshared[row.ItemID] {
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
	if root != "" {
		gatePolicy, _, e := ReadGatePolicy(root)
		if e != nil {
			return nil, e
		}
		// This read-only face needs Git only to prove a landed dependency.
		// The dispatch claim still validates the selected lane base before staging.
		if hasLandedBlocker(states, edges) {
			green, err = provedLaneBase(context.Background(), root, gatePolicy.LaneBase)
			if err != nil {
				return nil, err
			}
		}
	}
	unproved, err := unprovedBlockers(context.Background(), r, root, green, states, edges)
	if err != nil {
		return nil, err
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
	// A terminal run released its lease, but its candidate still occupies the
	// worktree until settlement and judgment have accounted for it.
	for _, work := range works {
		if !work.Done && !work.LaneReleased {
			lanes[work.Decision.LaneID] = true
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
		if item.IsCommentItem(row.ItemID) && row.State != StateLanded && row.State != "closed" && row.State != StateResolvedElsewhere {
			commentHolds[plannerDocumentKey(root, row.DesignDoc)] = row.ItemID
		}
	}
	p := &planner{
		commentHolds: commentHolds, documentRoot: root,
		conflicts: conflicts,
		shut:      shut,
		forks:     forks,
		tick:      tick, policy: policy, held: lanes, itemStates: states, history: history,
		priors: attemptPriors, retries: retries, classes: classes, latest: latest, questions: questions,
		trunkWaiters: trunkWaiters,
		at:           at, assigned: map[string]bool{}, fixerOpen: trunkFixerOpen(items), unproved: unproved,
		inputs: inputs, inputDecisions: inputDecisions, works: works, attempts: map[string]int{},
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
		if fault, held := faults[item.ItemID]; held && !terminalStates[item.State] {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: fault.Reason, LaneID: fault.LaneID, From: "held"}
			identifyDecision(&decision)
		} else if work := works[item.ItemID]; work != nil && !work.Done {
			// An already dispatched attempt advances before readiness and retry
			// rules: a terminal record is work to settle, not a retry to authorize.
			// An item already terminal by another route settles it unjudged instead
			// (terminal_item.go); a workflow item is released below on the same fact.
			settled := false
			if terminalStates[item.State] && work.Decision.Workflow == nil {
				decision, settled, err = terminalSettlement(r, work, at)
				if err != nil {
					return nil, readFailure(err)
				}
			}
			if !settled {
				decision, err = pendingDecision(r, work, at, inputs[item.ItemID], tick.BriefTemplate, policy, shut, tick.GatesInFlight)
			}
			if err == nil && !settled && decision.Action == ActionJudge && item.JudgeMode == "agent" && item.JudgeGrant == "" {
				decision.Action, decision.Reason, decision.To = ActionHold, ReasonJudgeGrantAbsent, ""
				identifyDecision(&decision)
			}
			if err == nil && work.LaneReleased && decision.Action != ActionHold {
				lane, ok := p.freeLane()
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
				invalid := item.State != StateInQueue || p.isBlocked(item, edges[item.ItemID]) || p.questionState(item).reason != ""
				before, now := work.Decision.Inputs, inputs[item.ItemID]
				if before != nil && now != nil && (before.ItemBlock != now.ItemBlock || before.AllowedPaths != now.AllowedPaths) {
					invalid = true
				}
				// The persisted graph, including source digests, owns this attempt.
				// Editing its source does not invalidate or replace an in-flight node.
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
		} else if unshared[item.ItemID] {
			decision, err = p.triageDecision(r, item)
			if err != nil {
				return nil, err
			}
		} else {
			w, selected, source, loadErr := selectWorkflow(r, root, item, tick.WorkflowDefaults, policy)
			routed := item
			if w != nil {
				if loadErr == nil {
					routed.Seat = w.Stages[0].Seat
					if w.Stages[0].Kind == "command" {
						routed.Command = &w.Stages[0]
					}
				}
			}
			hold := ""
			_, hasRetry := retries[item.ItemID]
			if w != nil && hasNodes && !terminalStates[item.State] && !hasRetry {
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
				decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: "workflow-invalid: " + strings.SplitN(loadErr.Error(), "\n", 2)[0], From: "held", Attempt: p.nextAttempt(item.ItemID)}
			} else {
				decision = p.decide(routed, edges[item.ItemID])
				decision.SelectedWorkflow, decision.WorkflowSource = selected, source
				if w != nil && decision.RunRequest != nil {
					decision.Workflow = w
					request := *decision.RunRequest
					request.RunID += "-n1"
					decision.RunRequest = &request
					if routed.Command != nil {
						decision.RunRequest = routed.Command.commandRequest(item.ItemID, decision.LaneID, request.RunID)
					} else if w.Stages[0].TimeoutMS > 0 {
						decision.RunRequest.TimeoutMS = w.Stages[0].TimeoutMS
					}
				}
			}
			identifyDecision(&decision)
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
	// An open review batch holds new build dispatches for its document; workers
	// already running settle normally, without being interrupted by intake.
	commentHolds   map[string]string
	documentRoot   string
	attempts       map[string]int
	shut           map[string]bool
	conflicts      map[string]item.Conflict
	inputs         map[string]*RunInputs
	inputDecisions map[string]Decision
	works          map[string]*pendingWork
	tick           Pass
	policy         *Policy
	held           map[string]bool
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
	if item.State != StateBlocked && conflict.Status != "answered" {
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
	changed, spent := "", false
	if inputs := p.inputs[item.ItemID]; inputs != nil {
		changed, spent = p.readmission(item.ItemID, inputs)
	}
	if p.history.refusals[p.latest[item.ItemID]] == RefusalTrunkAlreadyRed {
		// A released acquittal starts fresh, including one whose dispatch had
		// consumed an owner retry or the automatic input-change allowance.
		retry, changed, spent = nil, "", false
		attempt.failover, attempt.exhausted, attempt.degraded = false, false, ""
		if p.policy != nil {
			attempt.backend = p.policy.firstBackend(item)
		}
	}
	interrupted := false
	if priors := p.priors[item.ItemID]; len(priors) > 0 {
		interrupted = priors[len(priors)-1].Class == AttemptInterrupted
	}
	if interrupted {
		// Repeat the interrupted execution, not an authorized retry or a
		// re-admission. Preserve its backend and do not walk the failover chain.
		attempt = attemptForInterruption(p.priors[item.ItemID])
		retry, changed, spent = nil, "", false
	}
	if changed != "" && p.policy != nil {
		attempt.backend = p.policy.firstBackend(item)
		attempt.failover, attempt.exhausted, attempt.degraded = false, false, ""
	}
	if hasConflict && conflict.Status == "answered" && p.policy != nil {
		attempt.backend = p.policy.firstBackend(item)
		attempt.failover, attempt.exhausted, attempt.degraded = false, false, ""
		decision.ConflictID = conflict.ConflictID
	}
	windowShut := false
	if p.policy != nil && item.Command == nil {
		if retry != nil {
			attempt.backend = p.policy.firstBackend(item)
		}
		if attempt.backend != "" {
			next, degraded, ok := p.policy.availableBackend(attempt.backend, item.Seat, p.shut)
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
	case p.trunkWaiters[item.ItemID] != "":
		decision.Action, decision.Reason = ActionHold, ReasonTrunkBroken
		decision.DeferredBy = p.trunkWaiters[item.ItemID]
	case p.isBlocked(item, blockedBy):
		decision.Action, decision.Reason = ActionHold, ReasonBlocked
	case p.unproved[item.ItemID]:
		decision.Action, decision.Reason = ActionHold, ReasonTrunkUnproved
	case p.fixerOpen != "" && !IsTrunkFixer(item.ItemID):
		// The trunk is red and a fixer owns the repair. Every ordinary item waits: a run
		// staged from a base that never passed would produce a candidate the same red gate
		// refuses, and the queue would spend itself proving the same breakage one item at a
		// time. The fixer itself is exempt and dispatches normally.
		decision.Action, decision.Reason = ActionHold, ReasonTrunkBroken
		decision.DeferredBy = p.fixerOpen
	case item.State != StateInQueue:
		decision.Action, decision.Reason = ActionHold, ReasonNotReady
	case item.JudgeMode == "agent" && item.JudgeGrant == "":
		decision.Action, decision.Reason = ActionHold, ReasonJudgeGrantAbsent
	case !itemIsComment(item.ItemID) && p.commentHolds[plannerDocumentKey(p.documentRoot, item.DesignDoc)] != "":
		decision.Action, decision.Reason = ActionHold, "design-under-comment"
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
	case retry == nil && spent && decision.ConflictID == "":
		decision.Action, decision.Reason = ActionHold, ReasonReadmissionExhausted
	case retry == nil && changed == "" && attempt.exhausted:
		decision.Action, decision.Reason = ActionHold, ReasonInfraExhausted
	case retry == nil && !interrupted && changed == "" && !attempt.failover && p.mayBeARetry(item.ItemID) && decision.ConflictID == "":
		decision.Action, decision.Reason = ActionHold, p.retryHoldReason(item.ItemID)
	case fenceAdmitsNothing(fence):
		decision.Action, decision.Reason = ActionHold, ReasonNoFence
	case forkHeld:
		decision.Action, decision.Reason = ActionHold, ReasonForkHeld
		decision.DeferredBy = heldBy
	case p.policy == nil:
		decision.Action, decision.Reason = ActionHold, ReasonNoPolicy
	case windowShut:
		decision.Action, decision.Reason = ActionHold, ReasonBackendWindow
	case p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches:
		decision.Action, decision.Reason = ActionHold, ReasonNoLane
	default:
		lane, ok := p.freeLane()
		if itemIsComment(item.ItemID) {
			lane = commentDesk(item.DesignDoc)
			ok = !p.held[lane] && !p.assigned[lane]
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
		if changed != "" {
			decision.Reason, decision.Readmitted = changed, true
		}
		if interrupted {
			decision.Reason = "earlier run orphaned by a loop stop"
		}
		if decision.ConflictID != "" {
			decision.Reason = ReasonConflictAnswered
			decision.Readmitted = false
		}
	}
	decision.Attempt = p.nextAttempt(item.ItemID)
	decision.From = "held"
	if item.State == StateLanded {
		decision.From = "landed"
	}
	if decision.Action == ActionDispatch || decision.Action == ActionRetry {
		decision.From, decision.To = "queued", "running"
	}
	if itemIsComment(item.ItemID) && decision.RunRequest != nil {
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
	if decision.Action == ActionHold && decision.From == "held" && (decision.Reason == ReasonNoLane || decision.Reason == ReasonForkHeld) {
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
	if itemIsComment(item.ItemID) || item.DesignDoc == "" {
		return questionState{}
	}
	if p.questions == nil {
		return questionState{reason: ReasonQuestionsUnknown}
	}
	answer, err := p.questions.Document(item.DesignDoc)
	if err != nil {
		// The reason is the report. The error text is the tool's own and would be printed
		// once per item per tick, which is the log spam the decision record replaces.
		return questionState{reason: ReasonQuestionsUnknown}
	}
	if !answer.Present {
		return questionState{reason: ReasonDesignDocMissing}
	}
	if question, ok := firstUnanswered(answer.Questions); ok {
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
	if item.State == StateBlocked {
		return true
	}
	for _, blocker := range blockedBy {
		state, found := p.itemStates[blocker]
		if !found || !terminalStates[state] {
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
	if p.history.refusals[p.latest[itemID]] == RefusalTrunkAlreadyRed {
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
	case ReasonEvalFailed, ReasonJudgeUnreadable, ReasonJudgeGrantAbsent:
		return p.history.refusals[latest]
	case RefusalNodeOutput:
		return RefusalNodeOutput
	case RefusalFence:
		return ReasonBoundaryScopeEscape
	case RefusalNoOp:
		return ReasonCandidateNoOp
	case RefusalGate:
		return ReasonGateFailed
	case RefusalLand:
		return ReasonLandRefused
	case RefusalConflict:
		// The latest run hit a squash conflict and no further fix run is staged, either
		// because the one fix run already spent its turn or because the trunk did not
		// move. Either way the item waits for a person rather than spending another run.
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
		for _, prior := range p.priors[itemID] {
			d := p.inputDecisions[prior.RunID]
			if d.Action == ActionRetry && d.RetryID == row.RetryID {
				if now := p.inputs[itemID]; now != nil {
					changed, spent := p.readmission(itemID, now)
					if changed != "" || spent {
						return nil
					}
				}
			}
		}
		r.refusal = ReasonRetryStale
		return r
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
func (p *planner) freeLane() (string, bool) {
	for _, lane := range *p.tick.AvailableLanes {
		if strings.HasPrefix(lane, "desk-") {
			continue
		}
		if p.assigned[lane] {
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

// attempt is what the item's own run history says its next dispatch is: which backend it
// names, whether that dispatch is a failover re-dispatch, whether that walk leaves the
// backend the item's seat names, and whether its failover budget is spent so the item must
// hold instead.
//
// The chain is read from the item's terminal runs, oldest first. An item with no
// infrastructure-class failure starts at its first backend — its override, else the
// policy's default (design §2's precedence). An item whose runs include an
// infrastructure-class failure is a failover in progress: it walks to the backend after
// the last run's backend, and holds once the walk has no hop left or the policy's budget
// is spent. Counting every infrastructure-class run rather than only the last one is the
// conservative direction the budget wants: a chain is refused early rather than walked
// again. A walk that leaves the backend the item's seat names is a model switch and is
// recorded as one (nextBackend answers the seat), never read as an ordinary failover.
//
// A run that failed for a candidate reason leaves no chain here. Such an item holds with
// ReasonRunLinkMissing unless a person authorized a retry, and the retry rule then checks
// the run's class: nothing in this face spends a second backend on judged work. The
// failover chain is the machinery's automatic answer; a retry is the operator's, and it
// starts from the first backend rather than walking a chain the budget already spent.
func attemptForInterruption(priors []priorRun) attempt {
	return attempt{backend: priors[len(priors)-1].Backend}
}

func (p *planner) attempt(row itemRow) attempt {
	var a attempt
	if p.policy == nil {
		return a
	}
	priors := p.priors[row.ItemID]
	failures := 0
	for _, prior := range priors {
		if prior.Class == AttemptInfrastructure {
			failures++
		}
	}
	if failures == 0 {
		a.backend = p.policy.firstBackend(row)
		return a
	}
	if failures > p.policy.failovers() || len(priors) == 0 {
		a.exhausted = true
		return a
	}
	next, degraded, ok := p.policy.nextBackend(priors[len(priors)-1].Backend, row.Seat)
	if !ok {
		a.exhausted = true
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
	// exhausted says the item's failover chain has no hop left: hold, do not walk.
	exhausted bool
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
		        COALESCE(seat, ''), ` + workflowColumn + `, ` + judgeColumn + `
		   FROM items
		  ORDER BY COALESCE(rank, '') = '', COALESCE(rank, ''), item_id`)
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
		if err := rows.Scan(&item.ItemID, &item.State, &item.Rank, &allowed, &item.DesignDoc, &item.Seat, &item.Workflow, &item.JudgeMode); err != nil {
			return nil, readFailure(err)
		}
		if item.Allowed, err = decodeFence(allowed); err != nil {
			return nil, err
		}
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
			return Usagef("item %q asks for an agent judge, but the policy does not map the review seat", it.ItemID)
		}
		if it.Seat == "" {
			continue
		}
		if !item.KnownSeat(it.Seat) {
			return Usagef("item %q asks for seat %q, which does not exist: the item model's seats are %s; the policy maps %s",
				it.ItemID, it.Seat, item.SeatList(), mappedSeats(policy))
		}
		if len(policy.seatList(it.Seat)) == 0 {
			return Usagef("item %q asks for seat %q, which the policy does not map: the policy maps %s; a seat is never defaulted to the tick's backend",
				it.ItemID, it.Seat, mappedSeats(policy))
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
				Step        string `json:"step"`
				WriterRunID string `json:"writer_run_id"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err == nil && payload.Step != "" {
				history.refusals[*e.ObjectID] = payload.Step
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
