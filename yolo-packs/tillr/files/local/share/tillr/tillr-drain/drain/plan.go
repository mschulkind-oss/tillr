// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/ownercriteria"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/checkresult"
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
	// examineItems narrows planning to an explicit set of items (for example,
	// a freshly settled candidate plus active interactive requests that could
	// contend for its lane) without scanning finished history or unrelated items.
	examineItems map[string]bool
	// pendingOnly plans unfinished attempts, including their resume and retry
	// decisions, but leaves new attempts to the starter.
	pendingOnly bool
	// ownerMigration permits only the deterministic conversion of a legacy owner report.
	ownerMigration bool
	// loopHolds is the running loop's own in-memory holds (Loop.held), keyed by item, with
	// the reason. The planner holds these items before it offers any lane, so a lane is
	// never given to an item the loop will not stage and kept from the next item in order.
	loopHolds map[string]string
	// The legacy loop shares one ref observation between dependency planning and
	// base selection for new starts. Standalone and settlement plans need no snapshot.
	startRefs *startRefs
	// ownExaminationLane and ownExaminationRunID let an in-flight settle examination
	// ask what the planner would decide once its own lease ends, without clearing
	// that lease before the gate claim commits.
	ownExaminationLane  string
	ownExaminationRunID string
	// GatesInFlight is the planning pass's observation, not a concurrency cap.
	GatesInFlight int `json:"gates_in_flight,omitempty"`
	// Build is supplied by a loop so the snapshot identifies that running binary.
	Build *BuildStamp `json:"-"`
	// WorkflowDefaults comes from settings.Read, never from a caller-supplied pass.
	WorkflowDefaults map[string]string `json:"-"`
	// BriefTemplate is the loop's actual staging input, not a stored tick field.
	// A standalone plan with no stager compares the retained template instead.
	BriefTemplate *string `json:"-"`
	Root          string  `json:"-"`
	StorePath     string  `json:"-"`
	// runFaces is a fixed process observation supplied by the live loop.
	// The planner does not consult the process table, even with StorePath set.
	runFaces       map[string]bool
	Now            string    `json:"now"`
	AvailableLanes *[]string `json:"available_lanes"`
	// OutLanes are the lanes a staging refusal took out of rotation. A node waiting to
	// start on one is planned onto a free lane.
	OutLanes      map[string]bool `json:"-"`
	MaxDispatches *int            `json:"max_dispatches"`
	PolicyRef     string          `json:"policy_ref"`
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
	UnreadFooter      string         `json:"unread_footer,omitempty"`
	NodeInfraFailures int            `json:"node_infra_failures,omitempty"`
	WorkflowRelease   bool           `json:"workflow_release,omitempty"`
	FreshNodeStage    bool           `json:"fresh_node_stage,omitempty"`
	Workflow          *Workflow      `json:"workflow,omitempty"`
	SelectedWorkflow  string         `json:"selected_workflow,omitempty"`
	WorkflowSource    string         `json:"workflow_source,omitempty"`
	NodeIndex         int            `json:"node_index,omitempty"`
	NodeBaseSHA       string         `json:"node_base_sha,omitempty"`
	PreviousReport    string         `json:"previous_report,omitempty"`
	ConflictID        string         `json:"conflict_id,omitempty"`
	BlockConflict     *item.Conflict `json:"block_conflict,omitempty"`
	Inputs            *RunInputs     `json:"inputs,omitempty"`

	// ReusedNodes are the read-only nodes a retry keeps from attempt ReusedAttempt: they
	// passed there, no write node had run, so the new attempt starts at the first node not
	// kept and records each kept one in node_reused at claim (retainedReaders).
	ReusedNodes   []string `json:"reused_nodes,omitempty"`
	ReusedAttempt int      `json:"reused_attempt,omitempty"`

	DesignContinuationFrom string `json:"design_continuation_from,omitempty"`
	DesignContinuationHead string `json:"design_continuation_head,omitempty"`

	// DispatchBuild is the loop build that planned a dispatch whose decision carries no
	// Inputs (a design-seat or owner-question dispatch). The dispatch fact records it as
	// its TillrBuild, so the dispatch records which build started it.
	DispatchBuild string `json:"dispatch_build,omitempty"`
	// Readmitted marked a dispatch the removed input-change rule allowed. No pass writes
	// it since the owner's ruling of 2026-11-17; recorded decisions still carry it.
	Readmitted bool `json:"readmitted,omitempty"`
	// Returned is the earlier run's ending a dispatch carries back to the item's worker:
	// a gate refusal, a run that committed nothing, a fence escape or a refused landing.
	// Staging writes it into the brief (returnedBrief). It is nil on every other decision.
	Returned *ReturnedEnding `json:"returned,omitempty"`
	// RetryCandidate restores recorded judge/obstacle work without resuming its session
	// or requiring a new code commit. Detail remains visible when the work is missing.
	RetryCandidate *RetryCandidate `json:"retry_candidate,omitempty"`
	// RepeatedCall is the repeating-call ending this dispatch answers: the earlier run
	// whose session pi ended because the model kept repeating one identical call. Staging
	// writes it into the brief (repeatedCallBrief), and a run dispatched with it that ends
	// the same way holds rather than starting another session (repeatingRoute).
	RepeatedCall *RepeatedCall `json:"repeated_call,omitempty"`
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
	// liveContinuation distinguishes a running segment from its previous finish.
	liveContinuation bool
	// RetryID names the operator's authorization a `retry` decision spends, and the
	// SupersedesRunID names the run that authorization supersedes. A hold that refuses the
	// claim — `retry-judged`, `retry-unjudged` — names the same pair, so an
	// operator reading why their authorization did not hold can see what it named. A first
	// dispatch and every other hold leave both empty.
	RetryID         string `json:"retry_id,omitempty"`
	SupersedesRunID string `json:"supersedes_run_id,omitempty"`
	// DeferredBy names the resource hold's blocker, such as a fork owner,
	// the trunk's fixer, or a closed backend.
	DeferredBy string `json:"deferred_by,omitempty"`
	// Backend holds carry the closure evidence and what can reopen the backend.
	BackendHoldCause string `json:"backend_hold_cause,omitempty"`
	BackendHoldMover string `json:"backend_hold_mover,omitempty"`
	// InfrastructureFrom links a reopened dispatch to the run it can resume.
	InfrastructureFrom string `json:"infrastructure_from,omitempty"`
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
	// SelectSeat marks a fresh backend choice, as opposed to a continuation.
	SelectSeat bool `json:"select_seat,omitempty"`
}

// itemRow is the item state this face reads: the columns a decision turns on, and the
// rank that decides the order the decisions come in.
type itemRow struct {
	OwnerOwed *ownercriteria.OwnerCriterion
	// Express is read from the stored definition before workflow seat routing.
	Express   bool
	JudgeMode string
	Designer  *lastDesigner
	Workflow  string
	// WorkflowArgs binds the item's own workflow's top-level args (item's workflow_args):
	// nil for an item that supplies none, which is every document-declared item.
	WorkflowArgs map[string]any
	Command      *WorkflowStage
	ItemID       string
	State        string
	Rank         string
	// Allowed is the item's allowed_paths, the source of the fence its run is held to.
	Allowed  []string
	Delivery deliveryDecision
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

// planDefinitionCalls is an optional test hook counting planDefinition invocations.
var planDefinitionCalls atomic.Pointer[atomic.Int64]

func planDefinition(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource, def definition.Definition) ([]record.Envelope, error) {
	if counter := planDefinitionCalls.Load(); counter != nil {
		counter.Add(1)
	}
	return planWithDetails(r, tick, policy, questions, def, nil)
}

func (t Pass) plansItem(id string, work *pendingWork) bool {
	if t.examineItem != "" && t.examineItem != id {
		return false
	}
	if len(t.examineItems) > 0 && !t.examineItems[id] {
		return false
	}
	return !t.pendingOnly || (work != nil && !work.Done)
}

// planDetails supplies read faces with facts that are not part of the decisions.
// A no-lane decision alone does not say whether it is an unspent retry waiting
// for capacity, a continuation waiting for its own lane, or a dispatch cap.
type planDetails struct {
	nextOrdinals  map[string]int
	retryLaneWait map[string]bool
	workerLanes   map[string]string
	maskedWorkers map[string]Decision
	works         map[string]*pendingWork
	designHistory map[string]*pendingWork
	conflicts     map[string]item.Conflict
	faults        map[string]ItemFault
	obstacleHolds map[string]obstacles.Hold
}

func planWithDetails(r *store.Reader, tick Pass, policy *Policy, questions QuestionSource, def definition.Definition, details *planDetails) ([]record.Envelope, error) {
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
	var works, designHistory map[string]*pendingWork
	if details != nil && details.works != nil && details.designHistory != nil {
		works, designHistory = details.works, details.designHistory
	} else {
		works, designHistory, err = readPendingHistory(r)
		if err != nil {
			return nil, readFailure(err)
		}
	}
	for i := range items {
		if !tick.plansItem(items[i].ItemID, works[items[i].ItemID]) {
			continue
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
	latestFinished := map[string]string{}
	priors, err := readPriorRunsWithOutcomes(r, designHistory, nil, latestFinished)
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
	escalations, err := readEscalations(r)
	if err != nil {
		return nil, readFailure(err)
	}
	escalated := map[string]string{}
	for id, list := range escalations {
		escalated[id] = escalatedBackend(list)
	}
	inputDecisions := map[string]Decision{}
	inputDecisionsLoaded := map[string]bool{}
	var build BuildStamp
	if tick.Build != nil {
		build = *tick.Build
	} else {
		build = ReadBuildStamp()
	}
	root := ""
	switch source := questions.(type) {
	case *VantageQuestions:
		root = source.Root
	case interface{ RootDir() string }:
		root = source.RootDir()
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
		if !tick.plansItem(row.ItemID, works[row.ItemID]) || row.State != def.Ready || unshared[row.ItemID] {
			continue
		}
		inputs[row.ItemID], err = currentRunInputs(context.Background(), r, row, policy, root, build)
		if err != nil {
			return nil, readFailure(err)
		}
	}

	states := make(map[string]string, len(items))
	for _, item := range items {
		states[item.ItemID] = item.State
	}
	plannedEdges := make(map[string][]string)
	for id, blockers := range edges {
		if tick.plansItem(id, works[id]) && !finishedState(def, states[id]) {
			plannedEdges[id] = blockers
		}
	}
	var green string
	if def.Completes == "landing" && root != "" {
		gatePolicy, _, e := ReadGatePolicy(root)
		if e != nil {
			return nil, e
		}
		// The planner needs green to prove landed dependencies. The loop can
		// reuse that observation to select fresh starts' bases.
		if hasLandedBlocker(states, plannedEdges) {
			if tick.startRefs != nil {
				green, err = tick.startRefs.proved(context.Background(), root, gatePolicy.LaneBase)
			} else {
				green, err = provedLaneBase(context.Background(), root, gatePolicy.LaneBase)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	unproved := map[string]string{}
	if def.Completes == "landing" {
		tip := trunkBranchFor(root)
		if tick.startRefs != nil {
			tip = tick.startRefs.tip
		}
		unproved, err = unprovedBlockers(context.Background(), r, root, green, tip, states, plannedEdges)
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
	forks, err := liveForks(r, at, works)
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
		if !work.Done && !work.LaneReleased && !work.LaneBusyByOther {
			lanes[work.Decision.LaneID] = true
			deskOwners[work.Decision.LaneID] = work.Decision.ItemID
		}
	}
	var conflicts map[string]item.Conflict
	if details != nil && details.conflicts != nil {
		conflicts = details.conflicts
	} else {
		conflicts, err = item.ReadConflicts(r)
		if err != nil {
			return nil, readFailure(err)
		}
	}
	waits := map[string]item.WaitingOn{}
	for _, row := range items {
		if row.State != def.Blocked {
			continue
		}
		waiting, _, err := item.ReadWaitingOn(r, row.ItemID)
		if err != nil {
			return nil, readFailure(err)
		}
		if conflicts[row.ItemID].HasIndependentWait(waiting) {
			waits[row.ItemID] = *waiting
		}
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
	var hasNodes bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil {
		return nil, readFailure(err)
	}
	requests, err := readRequestIDs(r)
	if err != nil {
		return nil, readFailure(err)
	}
	runIDsByItem, err := readAllRunIDs(r)
	if err != nil {
		return nil, readFailure(err)
	}
	var nodeMaxByItem map[string]int
	if hasNodes {
		nodeMaxByItem, err = readNodeAttemptMaxes(r)
		if err != nil {
			return nil, readFailure(err)
		}
	}
	attempts, nodeCounts := map[string]int{}, map[string]int{}
	for _, row := range items {
		if !tick.plansItem(row.ItemID, works[row.ItemID]) {
			continue
		}
		attempts[row.ItemID], nodeCounts[row.ItemID] = computeAttemptInputs(row.ItemID, nodeMaxByItem[row.ItemID], priors[row.ItemID], runIDsByItem[row.ItemID], designHistory)
	}
	if details != nil {
		details.nextOrdinals = attempts
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return nil, readFailure(err)
	}
	obstacleHolds, releasedObstacles, err := ReadObstacleHolds(r)
	if err != nil {
		return nil, readFailure(err)
	}
	if details != nil {
		details.faults = faults
		details.obstacleHolds = obstacleHolds
	}
	// released names the lanes a returned worker or a design continuation reserved but did
	// not take in an earlier round of this plan. A reservation keeps a checkout for the one
	// item that can continue in it; when that item cannot start this pass (it is held, blocked,
	// retried without its session, or running elsewhere) and another item found no free lane,
	// the reservation is released and the plan is made again, so the lane goes to an item that
	// can start. Each round releases at least one reservation, so the rounds are bounded by
	// their number. Staging the lane for someone else preserves what it holds the way any
	// restaging does (preserved work, and a lane refusal for an unpinned commit).
	var maskedWorkers map[string]Decision
	if details != nil {
		maskedWorkers = details.maskedWorkers
	}
	initialActiveOrdinary, err := collectActiveOrdinaryAgents(r, at, works, designHistory, expressByIDFromRows(items))
	if err != nil {
		return nil, err
	}
	runningAgentLimit := ordinaryLaneCount(*tick.AvailableLanes)
	released := map[string]bool{}
	for {
		clear(maskedWorkers)
		owners := make(map[string]string, len(deskOwners))
		for lane, owner := range deskOwners {
			owners[lane] = owner
		}
		p := &planner{
			definition:   &def,
			commentHolds: commentHolds, documentRoot: root,
			conflicts: conflicts,
			shut:      shut,
			quotaShut: quotaShut,
			forks:     append([]plannedFork(nil), forks...),
			tick:      tick, policy: policy, held: lanes, deskOwners: owners, itemStates: states, history: history,
			priors: attemptPriors, retries: retries, classes: classes, latest: latest, latestFinished: latestFinished,
			obstacleHolds: obstacleHolds, releasedObstacles: releasedObstacles, questions: questions,
			waits: waits, escalated: escalated, maskedWorkers: maskedWorkers,
			at: at, assigned: map[string]bool{}, runningAgentLimit: runningAgentLimit, activeOrdinaryAgents: maps.Clone(initialActiveOrdinary),
			fixerOpen: trunkFixerOpen(items, def), unproved: unproved,
			designHistory: designHistory, inputs: inputs, inputDecisions: inputDecisions, inputDecisionsLoaded: inputDecisionsLoaded, inputDecisionReader: r, works: works, attempts: attempts, nodeCounts: nodeCounts, requests: requests,
		}
		if details != nil {
			p.retryLaneWait = make(map[string]bool)
		}
		p.reserveWorkerLanes(items, released)
		if _, err := p.reserveDrainingWork(r, items, edges); err != nil {
			return nil, readFailure(err)
		}
		decisions, claimed, err := p.planItems(r, items, edges, faults, works, inputs, retries, rewriteOwnerHolds, rewriteHolds, unshared, hasNodes, buildHash, policy, shut, tick, root, def, at)
		if err != nil {
			return nil, err
		}
		unused := false
		for lane, owner := range p.workerLanes {
			if claimed[lane] != owner {
				released[lane] = true
				unused = true
			}
		}
		if !unused || !p.laneShort {
			if details != nil {
				details.retryLaneWait = p.retryLaneWait
				details.workerLanes = p.workerLanes
			}
			return decisions, nil
		}
	}
}

func (p *planner) reserveWorkerLanes(items []itemRow, released map[string]bool) {
	p.workerLanes = map[string]string{}
	for _, row := range items {
		if row.State != p.rules().Ready {
			continue
		}
		if c, ok := p.conflicts[row.ItemID]; ok && c.Status != "answered" {
			continue
		}
		if lane := p.returnedJudgeLane(row.ItemID, p.returnedEnding(row.ItemID)); lane != "" && !released[lane] && (row.Express || !isExpressLane(lane)) {
			p.workerLanes[lane] = row.ItemID
			continue
		}
		if e := p.returnedEnding(row.ItemID); e != nil && e.WorkerSession != nil && !isExpressLane(e.WorkerSession.LaneID) && !released[e.WorkerSession.LaneID] && p.retainedLaneFree(e.WorkerSession.LaneID) {
			// Several reverted landings can name the same recycled lane. Only
			// the item whose handle remains there can reserve that checkout.
			id, err := ReadSessionID(e.WorkerSession.LaneDir)
			if err == nil && id != "" && id == e.WorkerSession.SessionID {
				p.workerLanes[e.WorkerSession.LaneID] = row.ItemID
			} else if verifyBuildReturn(e) && e.WorkerSession.SessionID == "" {
				// A build stage with no session of its own still holds the candidate.
				p.workerLanes[e.WorkerSession.LaneID] = row.ItemID
			}
		}
	}
	for _, row := range items {
		if row.State != p.rules().Blocked || p.waits[row.ItemID].Waiter != "" {
			continue
		}
		c := p.conflicts[row.ItemID]
		if origin := designContinuationWork(c, p.designHistory[c.DesignRunID]); origin != nil && !isExpressLane(origin.Decision.LaneID) && !released[origin.Decision.LaneID] {
			p.workerLanes[origin.Decision.LaneID] = row.ItemID
		}
	}
}

func (p *planner) reserveDrainingWork(r *store.Reader, items []itemRow, edges map[string][]string) ([]bool, error) {
	p.pausedBlocked = map[string]bool{}
	p.pausedReserved = map[string]string{}
	if p.tick.AvailableLanes == nil || p.tick.examineItem != "" {
		return nil, nil
	}
	pausedByItem := map[string]bool{}
	var pausedOfferedLanes []string
	for _, row := range items {
		work := p.works[row.ItemID]
		if work == nil || work.Done || work.LaneReleased {
			continue
		}
		var (
			paused    bool
			ownLeases []LaneLease
			err       error
		)
		if p.preRead != nil {
			paused = p.preRead.pending[row.ItemID].paused
			if paused {
				ownLeases = p.preRead.activeLeases(p.at, []string{work.Decision.LaneID}, "", "")
			}
		} else {
			paused, err = pausedPendingWork(r, work)
			if err != nil {
				return nil, err
			}
			if paused {
				ownLeases, err = activeLeases(r, p.at, []string{work.Decision.LaneID})
				if err != nil {
					return nil, err
				}
			}
		}
		if !paused {
			continue
		}
		if work.LaneBusyByOther || (work.ReopenedPausedJudge && len(ownLeases) > 0) {
			p.pausedBlocked[row.ItemID] = true
			if p.captureStart == nil {
				p.laneShort = true
			}
			continue
		}
		if len(ownLeases) > 0 {
			continue
		}
		pausedByItem[row.ItemID] = true
		if lane := work.Decision.LaneID; lane != "" && p.offersLane(lane) {
			pausedOfferedLanes = append(pausedOfferedLanes, row.ItemID)
		}
	}
	if len(pausedByItem) == 0 {
		return nil, nil
	}
	capacityUsed := maps.Clone(p.assigned)
	if capacityUsed == nil {
		capacityUsed = map[string]bool{}
	}
	claimCapacity := func(express bool) (string, bool) {
		if express {
			for _, lane := range *p.tick.AvailableLanes {
				if isExpressLane(lane) && !capacityUsed[lane] && !p.held[lane] {
					capacityUsed[lane] = true
					return lane, true
				}
			}
		}
		for _, lane := range *p.tick.AvailableLanes {
			if !strings.HasPrefix(lane, "desk-") && !isExpressLane(lane) && !capacityUsed[lane] && !p.held[lane] {
				capacityUsed[lane] = true
				return lane, true
			}
		}
		return "", false
	}
	type drainingSlot struct {
		lane    string
		express bool
	}
	var drainingUsed []drainingSlot
	seenWork := map[string]bool{}
	checkLiveDraining := func(itemID string, express bool, work *pendingWork) error {
		if work == nil || work.Done || work.LaneReleased || seenWork[itemID] {
			return nil
		}
		seenWork[itemID] = true
		lane := work.Decision.LaneID
		if lane == "" || p.offersLane(lane) || documentDesk(lane) || p.tick.OutLanes[lane] {
			return nil
		}
		var (
			live bool
			err  error
		)
		if p.preRead != nil {
			live = p.preRead.pending[itemID].liveDraining
		} else {
			live, err = liveDrainingWorker(r, work, p.at)
		}
		if err != nil || !live {
			return err
		}
		if slot, ok := claimCapacity(express); ok {
			drainingUsed = append(drainingUsed, drainingSlot{lane: slot, express: express})
		} else if len(pausedOfferedLanes) > 0 {
			last := len(pausedOfferedLanes) - 1
			p.pausedBlocked[pausedOfferedLanes[last]] = true
			pausedOfferedLanes = pausedOfferedLanes[:last]
		}
		return nil
	}
	for _, row := range items {
		if err := checkLiveDraining(row.ItemID, row.Express, p.works[row.ItemID]); err != nil {
			return nil, err
		}
	}
	for itemID, work := range p.works {
		if err := checkLiveDraining(itemID, false, work); err != nil {
			return nil, err
		}
	}
	var reservedSlots []bool
	drainingCommitted := false
	for _, row := range items {
		if !pausedByItem[row.ItemID] {
			continue
		}
		work := p.works[row.ItemID]
		lane := work.Decision.LaneID
		if lane == "" || documentDesk(lane) {
			continue
		}
		if p.tick.OutLanes[lane] {
			p.pausedBlocked[row.ItemID] = true
			continue
		}
		_, waiting := p.waits[row.ItemID]
		var (
			d   Decision
			err error
		)
		if p.preRead != nil {
			d, err = finishPendingDecision(p.preRead.pending[row.ItemID].drainingDecision, work, p.at, p.inputs[row.ItemID], p.tick.BriefTemplate)
		} else {
			d, err = pendingDecisionExcept(r, work, p.at, p.inputs[row.ItemID], p.tick.BriefTemplate, p.policy, p.shut, p.tick.GatesInFlight, p.tick.ownExaminationLane, p.tick.ownExaminationRunID, waiting)
		}
		if err != nil {
			return nil, err
		}
		if (d.Action != ActionSettle && d.Action != ActionJudge) || d.WorkflowRelease {
			continue
		}
		if row.State == p.rules().Ready && p.dependencyBlocked(edges[row.ItemID]) && d.Action == ActionJudge {
			continue
		}
		if p.offersLane(lane) {
			continue
		}
		if slot, ok := claimCapacity(row.Express); ok {
			if p.captureStart == nil {
				p.assigned[slot] = true
				p.pausedReserved[row.ItemID] = slot
				if !row.Express {
					p.claimOrdinaryAgentSlot(false, false, slot, "paused:"+row.ItemID)
				}
			}
			reservedSlots = append(reservedSlots, row.Express)
		} else {
			p.pausedBlocked[row.ItemID] = true
			if p.captureStart == nil {
				p.laneShort = true
			}
			if !drainingCommitted {
				drainingCommitted = true
				for _, ds := range drainingUsed {
					if p.captureStart == nil {
						p.assigned[ds.lane] = true
					}
					reservedSlots = append(reservedSlots, ds.express)
				}
			}
		}
	}
	return reservedSlots, nil
}

// planItems decides every item in order with the planner's lane state, and answers the
// decisions and, for each lane a dispatch or retry took, the item that took it.
func (p *planner) planItems(r *store.Reader, items []itemRow, edges map[string][]string, faults map[string]ItemFault, works map[string]*pendingWork, inputs map[string]*RunInputs, retries map[string]retryRow, rewriteOwnerHolds, rewriteHolds map[string]string, unshared map[string]bool, hasNodes bool, buildHash string, policy *Policy, shut map[string]bool, tick Pass, root string, def definition.Definition, at time.Time) ([]record.Envelope, map[string]string, error) {
	claimed := map[string]string{}
	known, err := deliveryCatalog(root)
	if err != nil {
		return nil, nil, err
	}
	iceboxDocs, err := readIceboxDesignDocs(root)
	if err != nil {
		return nil, nil, err
	}
	var iceboxSchema versionSchema
	if len(iceboxDocs) > 0 {
		if p.preRead != nil {
			iceboxSchema = p.preRead.versionSchema
		} else {
			iceboxSchema, err = readVersionSchema(r)
			if err != nil {
				return nil, nil, readFailure(err)
			}
		}
	}
	decisions := make([]record.Envelope, 0, len(items))
	var itemOrder map[string]int
	for idx, item := range items {
		work := works[item.ItemID]
		if !tick.plansItem(item.ItemID, work) {
			continue
		}
		var decision Decision
		ownerOwed := item.State == StateInQueue && item.OwnerOwed != nil
		wait, waiting := p.waits[item.ItemID]
		fault, faulted := faults[item.ItemID]
		faultHeld := faulted && !p.finished(item.State) && holdEnabled(def, fault.Reason)
		obsHold, held := p.obstacleHolds[item.ItemID]
		if waiting && (work == nil || work.Done || faultHeld || (work.Candidate != nil && candidateHeldWait(wait))) {
			decision = p.namedWaitDecision(item.ItemID, wait)
		} else if faultHeld {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: fault.Reason, LaneID: fault.LaneID, From: "held"}
			identifyDecision(&decision)
		} else if reason := tick.loopHolds[item.ItemID]; reason != "" && (works[item.ItemID] == nil || works[item.ItemID].Done) && holdEnabled(def, reason) {
			// The loop will not stage this item (Loop.heldFor), so it is held before any
			// lane is offered: a lane given to it would sit unused for the pass while the
			// next item in order is held as no-lane.
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: reason, From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if work != nil && !work.Done {
			// An already dispatched attempt advances before readiness and retry
			// rules: a terminal record is work to settle, not a retry to authorize.
			// An item already terminal by another route settles it unjudged instead
			// (terminal_item.go); a workflow item is released below on the same fact.
			settled := false
			if p.finished(item.State) && work.Decision.Workflow == nil {
				if p.preRead != nil {
					decision, settled = p.preRead.pending[item.ItemID].terminalDecision, p.preRead.pending[item.ItemID].terminalSettled
				} else {
					decision, settled, err = terminalSettlementExcept(r, work, at, tick.ownExaminationLane, tick.ownExaminationRunID)
					if err != nil {
						return nil, nil, readFailure(err)
					}
				}
			}
			if !settled && !work.BoundaryFailed {
				endLive := closedState(def, item.State)
				if !endLive && item.State == StateLanded {
					if p.preRead != nil {
						endLive = p.preRead.pending[item.ItemID].operatorFinished
					} else {
						endLive, err = operatorFinishedLanding(r, item.ItemID)
						if err != nil {
							return nil, nil, readFailure(err)
						}
					}
				}
				if endLive {
					// Do not resume a worker or offer another node while the drain
					// is ending the superseded run.
					decision = work.Decision
					decision.Action, decision.Reason, decision.From, decision.To = ActionHold, ReasonItemAlreadyTerminal, "held", ""
					identifyDecision(&decision)
					settled = true
				}
			}
			var liveFirstSegment bool
			if !settled {
				// The evaluator's retained handoff can be retried if it never
				// launched. A wait bars the next launch, not the live face;
				// treating it as an ordinary hold can stop the loop before it
				// settles the worker's result.
				if len(tick.runFaces) != 0 && (waiting || evaluatorNode(work.Decision)) {
					if p.preRead != nil {
						liveFirstSegment = p.preRead.pending[item.ItemID].liveFirstSegment
					} else {
						liveFirstSegment, err = liveFirstSegmentWorker(r, work, at, tick.runFaces)
						if err != nil {
							return nil, nil, readFailure(err)
						}
					}
				}
				if liveFirstSegment {
					decision = work.Decision
					decision.From, decision.To, decision.Action, decision.Reason = "running", "", ActionHold, ReasonRunLive
					identifyDecision(&decision)
				} else if p.preRead != nil {
					decision, err = finishPendingDecision(p.preRead.pending[item.ItemID].planDecision, definitionWork(def, work), at, inputs[item.ItemID], tick.BriefTemplate)
				} else {
					decision, err = pendingDecisionExcept(r, definitionWork(def, work), at, inputs[item.ItemID], tick.BriefTemplate, policy, shut, tick.GatesInFlight, tick.ownExaminationLane, tick.ownExaminationRunID, waiting)
				}
			}
			ownerNextNode := ownerOwed && (work.Ready || decision.Action == ActionDispatch || decision.Action == ActionRetry)
			// A dependency added while a step is running blocks after that step:
			// the running or finished step still settles, and only a next dispatch
			// or candidate judgment waits on the dependency.
			depBlocked := item.State == def.Ready && p.dependencyBlocked(edges[item.ItemID])
			depHold := depBlocked && (work.Ready || isDispatch(decision) || decision.Action == ActionJudge)
			// An unmapped item seat bars another launch, not settlement of work
			// already started under an earlier policy.
			if err == nil && !ownerNextNode && !depHold && isDispatch(decision) && !unshared[item.ItemID] {
				if hold, ok := unmappedSeatHold(policy, item, decision.Attempt); ok {
					decision = hold
				}
			}
			// A node waiting to start whose decision lane has left the rotation (a refused
			// claim took it out) cannot be offered that lane again: it is planned onto a
			// free one, as a released lane is.
			laneOut := work.Ready && !work.Done && tick.OutLanes[work.Decision.LaneID]
			detachedNode := work.Ready && work.LaneReleased
			// A successor to classification (or a restarted node) may be ready to
			// run without owning its fork. Check before it can take a free lane,
			// just as for a fresh dispatch. Running work and settlement still proceed.
			var fork, forkHolder string
			if err == nil && !depHold && p.lands() && isDispatch(decision) &&
				!(decision.Workflow != nil && classificationNode(decision.Workflow.Stages[decision.NodeIndex])) {
				fork = item.Delivery.activeFork()
				self := ""
				if work.Ready || decision.WorkflowRestart {
					self = item.ItemID
				}
				forkHolder, _ = p.forkHeldBy(fork, self)
			}
			if err == nil && (work.LaneReleased || laneOut) && !waiting && !ownerNextNode && !depHold && decision.Action != ActionHold && forkHolder == "" {
				if detachedNode {
					// A released first node has no staged state to resume. Keep the
					// selected request, but stage it fresh on the newly assigned lane.
					decision.WorkflowRestart = false
				}
				isCommand := isCommandDecision(decision) || (decision.Action == ActionJudge && item.JudgeMode != "agent")
				runKey := ""
				if decision.RunRequest != nil {
					runKey = decision.RunRequest.RunID
				}
				var lane string
				slotOpen := p.ordinaryAgentSlotOpen(item.Express, isCommand, runKey)
				limitFull := !slotOpen && p.runningAgentLimit > 0
				ok := slotOpen
				if ok {
					lane, ok = p.freeLane(item.Express)
					if !ok && decision.Action == ActionJudge {
						if itemOrder == nil {
							itemOrder = make(map[string]int, len(items))
							for i, row := range items {
								itemOrder[row.ItemID] = i
							}
						}
						lane, ok = p.freeLaneOverLaterReservation(item.Express, itemOrder, idx)
					}
				}
				if !ok {
					if limitFull {
						decision.Action, decision.Reason, decision.To = ActionHold, ReasonAgentLimit, ""
						decision.DeferredBy = activeOrdinaryAgentSummary(p.activeOrdinaryAgents)
					} else {
						decision.Action, decision.Reason, decision.To = ActionHold, ReasonNoLane, ""
					}
				} else {
					decision.LaneID = lane
					req := *decision.RunRequest
					req.LaneID = lane
					decision.RunRequest = &req
					if p.captureStart == nil {
						p.assigned[lane] = true
						p.claimOrdinaryAgentSlot(item.Express, isCommand, lane, runKey)
					}
				}
				identifyDecision(&decision)
			}
			if err == nil && work.Decision.Workflow != nil && !work.BoundaryFailed {
				// Settle a completed node before holding the next one: its result is
				// already work done, not a new dispatch. An unmet dependency holds the
				// next step below instead of discarding the settled workflow.
				invalid := ownerNextNode || item.State != def.Ready || p.questionState(item).reason != ""
				// The persisted graph, including source digests, owns this attempt.
				// Editing its source does not invalidate or replace an in-flight node, and
				// neither does an edit to the item's block or fence: the owner ruled on
				// 2026-11-17 that such an edit is no fact for the work (audit finding F-07).
				if invalid {
					laneID := decision.LaneID
					if ownerOwed {
						laneID = work.Decision.LaneID
					}
					var leases []LaneLease
					if !ownerNextNode || !work.LaneReleased {
						if p.preRead != nil {
							leases = p.preRead.activeLeases(at, []string{laneID}, tick.ownExaminationLane, tick.ownExaminationRunID)
						} else {
							var leaseErr error
							leases, leaseErr = activeLeasesExcept(r, at, []string{laneID}, tick.ownExaminationLane, tick.ownExaminationRunID)
							if leaseErr != nil {
								return nil, nil, readFailure(leaseErr)
							}
						}
					}
					if len(leases) == 0 {
						keepEnding := false
						if decision.Action == ActionHold && decision.Reason == ReasonItemAlreadyTerminal {
							if p.preRead != nil {
								keepEnding = p.preRead.pending[item.ItemID].unfinishedRun
							} else {
								var unfinishedErr error
								keepEnding, unfinishedErr = workHasUnfinishedRun(r, work)
								if unfinishedErr != nil {
									return nil, nil, readFailure(unfinishedErr)
								}
							}
						}
						if !keepEnding {
							decision = work.Decision
							decision.From = "running"
							decision.Action, decision.Reason, decision.To = ActionSettle, "workflow-invalidated", "held"
							decision.WorkflowRelease = true
							identifyDecision(&decision)
						}
					} else if ownerNextNode && (decision.Action == ActionDispatch || decision.Action == ActionRetry) {
						decision = work.Decision
						decision.From, decision.To = "running", ""
						decision.Action, decision.Reason = ActionHold, ReasonRunLive
						identifyDecision(&decision)
					}
				}
			}
			if err != nil {
				return nil, nil, readFailure(err)
			}
			if p.pausedBlocked[item.ItemID] && (decision.Action == ActionSettle || decision.Action == ActionJudge) && !decision.WorkflowRelease {
				from := "running"
				if decision.Action == ActionJudge {
					from = "candidate"
				}
				decision.From, decision.To = from, ""
				decision.Action, decision.Reason = ActionHold, ReasonNoLane
				identifyDecision(&decision)
			}
			if !(work.LaneReleased || laneOut) && decision.Action == ActionJudge && item.JudgeMode == "agent" && !decision.WorkflowRelease && !item.Express && !isExpressLane(decision.LaneID) && !strings.HasPrefix(decision.LaneID, "desk-") && p.pausedReserved[item.ItemID] == "" {
				isCommand := isCommandDecision(decision)
				runKey := "judge:" + item.ItemID
				if decision.RunRequest != nil && decision.RunRequest.RunID != "" {
					runKey = decision.RunRequest.RunID
				}
				if !p.ordinaryAgentSlotOpen(item.Express, isCommand, runKey) {
					decision.From, decision.To = "candidate", ""
					if p.runningAgentLimit > 0 {
						decision.Action, decision.Reason = ActionHold, ReasonAgentLimit
						decision.DeferredBy = activeOrdinaryAgentSummary(p.activeOrdinaryAgents)
					} else {
						decision.Action, decision.Reason = ActionHold, ReasonNoLane
					}
					identifyDecision(&decision)
				} else {
					p.claimOrdinaryAgentSlot(item.Express, isCommand, decision.LaneID, runKey)
				}
			}
			if depHold && !decision.WorkflowRelease {
				decision = Decision{ItemID: item.ItemID, Attempt: decision.Attempt, From: "held", Action: ActionHold, Reason: ReasonBlocked}
				identifyDecision(&decision)
			}
			if forkHolder != "" && isDispatch(decision) {
				decision = Decision{ItemID: item.ItemID, Attempt: decision.Attempt, From: "held", Action: ActionHold, Reason: ReasonForkHeld, DeferredBy: forkHolder}
				identifyDecision(&decision)
			}
			if detachedNode && decision.Action == ActionHold {
				// The recorded lane is historical, not a reservation for the waiting node.
				decision.LaneID = ""
				if decision.RunRequest != nil {
					req := *decision.RunRequest
					req.LaneID = ""
					decision.RunRequest = &req
				}
				identifyDecision(&decision)
			}
			// Keep a live segment's running decision and report the wait separately.
			if waiting && !liveFirstSegment && !decision.liveContinuation && (decision.Action == ActionHold || isDispatch(decision)) {
				if p.maskedWorkers != nil && mayMaskPendingWorker(decision) {
					p.maskedWorkers[item.ItemID] = decision
				}
				decision = p.namedWaitDecision(item.ItemID, wait)
			}
			if p.captureStart == nil && p.lands() && fork != "" && isDispatch(decision) {
				p.forks = append(p.forks, plannedFork{itemID: item.ItemID, fork: fork})
			}
		} else if ownerOwed {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: ReasonOwnerOnlyOwed, From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if held && !p.finished(item.State) && holdEnabled(def, obsHold.ReasonCode()) && !(item.State == def.Ready && retries[item.ItemID].SupersedesRunID == obsHold.RunID && p.latest[item.ItemID] == obsHold.RunID) {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: obsHold.ReasonCode(), From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if rewriteOwnerHolds[item.ItemID] != "" && !p.finished(item.State) {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: rewriteOwnerHolds[item.ItemID], From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if rewriteHolds[item.ItemID] != "" && !p.finished(item.State) {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: ReasonAwaitsRewrite, DeferredBy: rewriteHolds[item.ItemID], From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if iceboxReason, iceboxErr := checkIceboxHold(r, p.preRead, item, root, def.Ready, holdEnabled(def, ReasonIcebox), iceboxDocs, iceboxSchema); iceboxErr != nil {
			return nil, nil, readFailure(iceboxErr)
		} else if iceboxReason != "" {
			decision = Decision{ItemID: item.ItemID, Action: ActionHold, Reason: iceboxReason, From: "held", Attempt: p.nextAttempt(item.ItemID)}
			identifyDecision(&decision)
		} else if unshared[item.ItemID] {
			decision, err = p.triageDecision(r, item)
			if err != nil {
				return nil, nil, err
			}
		} else if hold, ok := unmappedSeatHold(policy, item, p.nextAttempt(item.ItemID)); ok && !p.finished(item.State) {
			decision = hold
		} else {
			var (
				w                *Workflow
				selected, source string
				loadErr          error
			)
			if p.preRead != nil {
				w, selected, source, loadErr = selectWorkflowFromStored(p.preRead.workflowSelections[item.ItemID], root, item, tick.WorkflowDefaults, policy)
			} else {
				w, selected, source, loadErr = selectWorkflow(r, root, item, tick.WorkflowDefaults, policy)
			}
			if loadErr == nil && def.Completes == "landing" {
				w = planDelivery(w, selected, item, known)
			}
			if loadErr == nil && def.Completes != "landing" && evaluatorSlot(w) != "" {
				loadErr = fmt.Errorf("workflow-evaluator-requires-landing: evaluator_slot judges a Git candidate; use an ordinary agent node and the definition's completion hook instead")
			}
			routed := item
			resumeNode := 0
			retainedGraph := false
			e := p.returnedEnding(item.ItemID)
			if retry := p.retryFor(item.ItemID); retry != nil && retry.refusal == "" {
				c := p.retryCandidate(item.ItemID, retry.supersedesRunID)
				if c != nil && !c.Available {
					e = nil
				}
				if c != nil && c.Available && c.WorkerSession != nil && c.WorkerSession.Workflow != nil {
					s := c.WorkerSession
					if s.NodeIndex >= 0 && s.NodeIndex < len(s.Workflow.Stages) && s.Workflow.Stages[s.NodeIndex].Access == "write" {
						w, resumeNode, loadErr = s.Workflow, s.NodeIndex, nil
						retainedGraph = true
					}
				}
			}
			if e != nil && e.WorkerSession != nil && e.WorkerSession.Workflow != nil && policy != nil && (item.Express || !isExpressLane(e.WorkerSession.LaneID)) && p.retainedLaneFreeFor(e.WorkerSession.LaneID, item.ItemID) {
				s := e.WorkerSession
				if s.NodeIndex >= 0 && s.NodeIndex < len(s.Workflow.Stages) {
					stage := s.Workflow.Stages[s.NodeIndex]
					backend := policy.firstBackend(itemRow{ItemID: item.ItemID, Seat: stage.Seat})
					if !p.shut[backend] && workerSessionReason(s, policy.request(item.ItemID, s.LaneID, attemptRunID(item.ItemID, p.nextAttempt(item.ItemID)), backend)) == "resumed-worker" {
						w, resumeNode, loadErr = s.Workflow, s.NodeIndex, nil
						retainedGraph = true
					}
				}
			}
			// The build step runs again on the candidate the verifier or gate read, in a fresh
			// session when the writer's cannot be resumed, and on whatever lane the plan
			// offers: the graph (with any expanded pieces and the inserted work stage) is
			// retained even when the original lane is unavailable.
			if !retainedGraph && (verifyBuildReturn(e) || verifyNoVerdictReturn(e)) && e.WorkerSession.NodeIndex >= 0 && e.WorkerSession.NodeIndex < len(e.WorkerSession.Workflow.Stages) {
				w, resumeNode, loadErr = e.WorkerSession.Workflow, e.WorkerSession.NodeIndex, nil
				retainedGraph = true
			}
			if !retainedGraph && gateBuildReturn(e) {
				if gw, gnode := buildStageForGateReturn(e.WorkerSession.Workflow, e.WorkerSession.NodeIndex); gw != nil {
					w, resumeNode, loadErr = gw, gnode, nil
					retainedGraph = true
				} else if gw, gnode := buildStageForGateReturn(w, -1); gw != nil && loadErr == nil {
					w, resumeNode = gw, gnode
					retainedGraph = true
				}
			}
			var reused []string
			reusedFrom := 0
			if _, retrying := retries[item.ItemID]; retrying && !retainedGraph && w != nil && loadErr == nil && hasNodes {
				reusedFrom = p.nextAttempt(item.ItemID) - 1
				if n := p.nodeCounts[item.ItemID]; n > 0 {
					reusedFrom = n
				}
				if p.preRead != nil {
					resumeNode, reused = retainedReadersFromOutcomes(p.preRead.retainedOutcomes[item.ItemID], reusedFrom, w)
				} else {
					resumeNode, reused, err = retainedReaders(r, item.ItemID, reusedFrom, w)
					if err != nil {
						return nil, nil, readFailure(err)
					}
				}
			}
			if w != nil {
				if loadErr == nil {
					routed.Seat = w.Stages[resumeNode].Seat
					if w.Stages[resumeNode].Kind == "command" {
						routed.Command = &w.Stages[resumeNode]
					}
				}
			}
			hold := ""
			_, hasRetry := retries[item.ItemID]
			obstacleReleased := p.latest[item.ItemID] != "" && p.releasedObstacles[item.ItemID] == p.latest[item.ItemID]
			if w != nil && hasNodes && !p.finished(item.State) && !hasRetry && !obstacleReleased && !p.waitReleased(item.ItemID) {
				if p.preRead != nil {
					hold = p.preRead.commandHolds[item.ItemID]
				} else {
					hold, err = commandNodeHold(r, item.ItemID)
					if err != nil {
						return nil, nil, readFailure(err)
					}
				}
			}
			if loadErr == nil && w != nil {
				if scopeErr := w.CheckScopes(nil); scopeErr != nil {
					hold = scopeErr.Error()
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
				if decision.RunRequest != nil && decision.Returned != nil && decision.Returned.WorkerSession != nil && w != nil && !retainedGraph {
					decision.RunRequest.SessionTurn = false
					decision.RunRequest.ResumePrompt = ""
					decision.SelectSeat = true
					if decision.WorkerSessionReason == "resumed-worker" {
						decision.WorkerSessionReason = "workflow-restarted"
					}
				}
				if decision.BlockConflict == nil {
					decision.SelectedWorkflow, decision.WorkflowSource = selected, source
				}
				if len(reused) > 0 && decision.Action == ActionRetry {
					decision.ReusedNodes, decision.ReusedAttempt = reused, reusedFrom
				} else if len(reused) > 0 {
					resumeNode = 0
				}
				if w != nil && decision.RunRequest != nil && decision.BlockConflict == nil {
					decision.Workflow = w
					decision.NodeIndex = resumeNode
					if c := decision.RetryCandidate; c != nil && c.Available {
						decision.NodeBaseSHA = c.BaseSHA
						if c.WorkerSession != nil && c.WorkerSession.NodeBaseSHA != "" {
							decision.NodeBaseSHA = c.WorkerSession.NodeBaseSHA
						}
					}
					if decision.RunRequest.SessionTurn && decision.Returned.WorkerSession.Workflow != nil {
						decision.NodeBaseSHA = decision.Returned.WorkerSession.NodeBaseSHA
					}
					if verifyBuildReturn(decision.Returned) || verifyNoVerdictReturn(decision.Returned) ||
						(gateBuildReturn(decision.Returned) && !decision.RunRequest.SessionTurn) {
						// Per-node checks measure the repair commit atop the candidate; the
						// whole item still diffs against its own base.
						decision.NodeBaseSHA = decision.Returned.WorkerSession.HeadSHA
					}
					request := *decision.RunRequest
					request.RunID += "-n" + strconv.Itoa(resumeNode+1)
					if request.SessionTurn && decision.Returned != nil && decision.Returned.Cause == RefusalPlanOutput && p.documentRoot != "" {
						outPath := filepath.Join(nodeOutputsDir(p.documentRoot, request.RunID), deliveryOutput)
						request.ResumePrompt += "\nWrite the corrected `" + deliveryOutput + "` file at: " + outPath + " (any other outputs from your previous turn, such as `" + quickWayFile + "` or `proposal`, and any commit on this lane are already preserved). End with RESULT: done.\n"
					}
					decision.RunRequest = &request
					if routed.Command != nil {
						decision.RunRequest = commandRequest(*routed.Command, item.ItemID, decision.LaneID, request.RunID)
					} else if w.Stages[resumeNode].TimeoutMS > 0 {
						decision.RunRequest.TimeoutMS = w.Stages[resumeNode].TimeoutMS
					}
				} else if decision.RunRequest != nil && decision.BlockConflict == nil && gateBuildReturn(decision.Returned) && !decision.RunRequest.SessionTurn {
					// Record the refused candidate commit in drain.started even for non-workflow items.
					decision.NodeBaseSHA = decision.Returned.WorkerSession.HeadSHA
				}
			}
			identifyDecision(&decision)
		}
		if decision.RunRequest != nil {
			request := *decision.RunRequest
			stampDecisionRequest(decision, &request)
			decision.RunRequest = &request
		}
		if decision.Action == ActionHold && decision.Reason == ReasonBackendWindow && policy != nil && p.infrastructureClosed == nil {
			var err error
			if p.infrastructureClosed, err = readInfrastructureClosedFrom(r, p.designHistory); err != nil {
				return nil, nil, err
			}
		}
		if err := describeBackendHoldWithPreRead(r, p.preRead, &decision, policy, at, p.shut, p.infrastructureClosed); err != nil {
			return nil, nil, err
		}
		if def.Completes != "landing" && decision.RunRequest != nil && decision.RunRequest.WorkingDir == WorktreeDir {
			request := *decision.RunRequest
			request.WorkingDir = PlainDir
			decision.RunRequest = &request
		}
		if decision.Inputs == nil && decision.RunRequest != nil && (decision.Action == ActionDispatch || decision.Action == ActionRetry) {
			decision.DispatchBuild = buildHash
		}
		if p.captureStart != nil {
			if err := p.captureStart(item, decision); err != nil {
				return nil, nil, err
			}
			continue
		}
		env, err := decisionEnvelope(decision, at)
		if err != nil {
			return nil, nil, err
		}
		decisions = append(decisions, env)
		if decision.Action == ActionDispatch || decision.Action == ActionRetry {
			claimed[decision.LaneID] = decision.ItemID
		} else if reserved := p.pausedReserved[item.ItemID]; reserved != "" && (decision.Action == ActionSettle || decision.Action == ActionJudge) {
			claimed[reserved] = item.ItemID
		}
	}
	return decisions, claimed, nil
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
	// Shadow evaluation shares routing without consuming lanes, desks or forks.
	captureStart  func(itemRow, Decision) error
	preRead       *startRepairInputs
	workerLanes   map[string]string
	retryLaneWait map[string]bool // only populated for why, in the final planning round
	maskedWorkers map[string]Decision
	definition    *definition.Definition
	// An open review batch holds new build dispatches for its document; workers
	// already running settle normally, without being interrupted by intake.
	commentHolds map[string]string
	documentRoot string
	attempts     map[string]int
	// nodeCounts is each item's highest attempt backed by a node run, which an operator retry reuses.
	nodeCounts map[string]int
	// requests holds the ids of filed requests, which have no item block.
	requests map[string]bool
	shut     map[string]bool
	// quotaShut leaves out backends closed by an infrastructure ending: a retry
	// the owner authorized is the one dispatch that may go to one.
	quotaShut            map[string]bool
	infrastructureClosed map[string]backendOutcome
	conflicts            map[string]item.Conflict
	inputs               map[string]*RunInputs
	inputDecisions       map[string]Decision
	inputDecisionsLoaded map[string]bool
	inputDecisionReader  *store.Reader
	works                map[string]*pendingWork
	designHistory        map[string]*pendingWork
	tick                 Pass
	policy               *Policy
	held                 map[string]bool
	deskOwners           map[string]string
	itemStates           map[string]string
	history              runHistory
	priors               map[string][]priorRun
	// retries is each item's latest operator authorization, keyed by item; classes is
	// every prior run's attempt class, keyed by run id; latest is each item's latest
	// terminal run id. Together they let decide answer "is this authorization's run one
	// the harness refused?" from the record rather than from the claim alone.
	retries           map[string]retryRow
	classes           map[string]AttemptClass
	latest            map[string]string
	latestFinished    map[string]string
	obstacleHolds     map[string]obstacles.Hold
	releasedObstacles map[string]string
	// questions answers what each item's design document still asks. It is the planner's
	// only read outside the store, and it is nil-safe: a nil source answers "unknown".
	questions            QuestionSource
	at                   time.Time
	dispatched           int
	assigned             map[string]bool
	runningAgentLimit    int
	activeOrdinaryAgents map[string]string
	pausedBlocked        map[string]bool
	pausedReserved       map[string]string
	// laneShort records that some item asked this plan for a free lane and got none.
	laneShort bool
	// forks includes live leases and unfinished examinations, then adds this plan's
	// own dispatches. Disjoint item fences can still deliver one patch branch.
	forks []plannedFork
	// fixerOpen is the open fixer item's id, or "" when the trunk is green. While it is
	// set, every ordinary item holds with ReasonTrunkBroken: staging ordinary work from a
	// base whose checks never passed is the defect the fixer exists to repair.
	fixerOpen string
	unproved  map[string]string
	// Independent blocked waits take precedence over conflict routing and new launches.
	waits map[string]item.WaitingOn
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

// A planned hold can describe the next action rather than the current worker.
// Preserve these decisions for Why even when an independent wait masks them.
func mayMaskPendingWorker(d Decision) bool {
	if d.Reason == ReasonRunLive {
		return true
	}
	return (d.Reason == ReasonNoLane || d.Reason == ReasonAgentLimit) && d.From == "running" && d.WorkflowRestart && evaluatorNode(d)
}

func (p *planner) namedWaitDecision(id string, wait item.WaitingOn) Decision {
	return namedWaitHold(p.rules(), p.obstacleHolds[id], id, wait, p.nextAttempt(id))
}

func namedWaitHold(def definition.Definition, hold obstacles.Hold, id string, wait item.WaitingOn, attempt int) Decision {
	d := Decision{ItemID: id, Action: ActionHold, Reason: ReasonBlocked, From: "held", Attempt: attempt}
	if hold.Mover == wait.Waiter && hold.Reason == wait.Reason && holdEnabled(def, hold.ReasonCode()) {
		d.Reason = hold.ReasonCode()
	} else if dep, ok := strings.CutPrefix(wait.Waiter, "item:"); ok && def.Completes == "landing" {
		// Keep the specialized repair holds and their existing release/reporting rules.
		if IsTrunkFixer(dep) {
			d.Reason, d.DeferredBy = ReasonTrunkBroken, dep
		} else if IsDefectItem(dep) {
			d.Reason, d.DeferredBy = ReasonToolingDefect, dep
		} else if testFixHeldWait(wait) {
			d.Reason, d.DeferredBy = ReasonBlocked, dep
		}
	}
	identifyDecision(&d)
	return d
}

// decide answers one item's decision. The rules are applied in the order the brief
// fixes, first match wins: the item's own properties, then the tick's configuration,
// then the per-item resource answers. A caller asking "why won't this dispatch" gets the
// answer that would still hold if every resource were free.
//
// Under OQ-GP7, work that a red check does not read may start and run while a fixer is
// open; only landing waits (via path intersection at gate-acquittal time).
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
	// Resolve the last run's disposition and any operator retry before testing resources.
	attempt := p.attempt(item)
	retry := p.retryFor(item.ItemID)
	// No item is re-admitted because an input changed: the owner ruled on 2026-11-17 that
	// nothing re-runs because a hash of the block, the workflow, the fence, the backend
	// policy or the tillr build changed without knowing what the change means (audit
	// finding F-01). An item moves on an event that names it: the operator's retry, a
	// design answer for it, or the recovery of a killed run.
	waitReleased := p.waitReleased(item.ItemID)
	if waitReleased {
		// The named blocker landed. Start fresh, not from the run that was held.
		attempt.infrastructureFailure = false
		if p.policy != nil {
			attempt.backend = p.firstBackend(item)
		}
		retry = nil
	} else if acquittedRefusal(p.history.refusals[p.latest[item.ItemID]]) {
		// A released acquittal starts fresh, including one whose dispatch had
		// consumed an owner retry.
		retry = nil
		attempt.infrastructureFailure = false
		if p.policy != nil {
			attempt.backend = p.firstBackend(item)
		}
	}
	// An answered conflict from an obstacle call reopens the obstacle call's session
	// when the session is still on its lane, delivering the design answer as the call result.
	var answeredObstacle *ReturnedEnding
	if hasConflict && conflict.Status == "answered" && conflict.RunID != "" {
		if call := p.history.obstacleCalls[conflict.RunID]; call != nil {
			answeredObstacle = &ReturnedEnding{RunID: conflict.RunID, Cause: RefusalNoOp, Detail: conflict.Text, WorkerSession: call}
		}
	}
	// A gate refusal, a run with no commit, a fence escape and a refused landing go back to
	// the item's worker with their evidence, every time and uncounted (returned.go). An
	// operator's retry and the recovery of an interrupted run are their own moves.
	returned := p.returnedEnding(item.ItemID)
	if returned == nil && answeredObstacle != nil {
		returned = answeredObstacle
	}
	interrupted, restaged := false, false
	if priors := p.priors[item.ItemID]; !waitReleased && len(priors) > 0 {
		interrupted = priors[len(priors)-1].Class == AttemptInterrupted
		restaged = priors[len(priors)-1].Restaged
	}
	if interrupted {
		// Repeat the interrupted execution, not an authorized retry or a
		// re-admission. Preserve its backend.
		attempt = attemptForInterruption(p.priors[item.ItemID])
		retry = nil
	}
	// A run pi ended for a repeating call routes as its settlement recorded: a fresh
	// session or a hold for the operator (repeating_call.go). An operator's
	// retry outranks it.
	var repeated *RepeatedCall
	if priors := p.priors[item.ItemID]; !waitReleased && retry == nil && !interrupted && len(priors) > 0 && priors[len(priors)-1].Class == AttemptRepeating {
		repeated = priors[len(priors)-1].Repeated
	}
	if repeated != nil && repeated.Route != repeatRouteHold {
		attempt.backend = repeated.Backend
	}
	// An operator's retry of a refused verify still repairs the candidate the verifier
	// read, so that one ending outlives the retry that otherwise clears it.
	var retriedVerify *ReturnedEnding
	if retry != nil && !interrupted && repeated == nil && verifyBuildReturn(returned) {
		retriedVerify = returned
	}
	if retry != nil || interrupted || repeated != nil {
		returned = nil
	}
	if returned != nil && p.policy != nil {
		// A returned ending is about the candidate, not the machinery: the next run starts
		// on the item's own backend.
		attempt.backend = p.firstBackend(item)
		attempt.infrastructureFailure = false
	}
	if hasConflict && conflict.Status == "answered" && p.policy != nil {
		attempt.backend = p.firstBackend(item)
		attempt.infrastructureFailure = false
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
			if attempt.infrastructureFailure || repeated != nil || interrupted {
				windowShut = shut[attempt.backend]
			} else {
				windowShut = !p.policy.seatMayStart(item.ItemID, item.Seat, attempt.backend, shut)
			}
		}
	}
	// One planner-level exclusion survives: two items that deliver one fork's patch branch
	// cannot run at once, however disjoint their own fences are, because the second would
	// move a branch the first is still building.
	heldBy, forkHeld := p.forkHeldBy(item.Delivery.activeFork(), "")
	// The design document is read before the tick's resources, and before the item's attempt
	// history: an item whose design still asks something is not dispatchable whatever else
	// is true, and answering `no-lane` would send an operator looking for a lane to solve a
	// problem a ruling solves. Skip the read for items already decided by an earlier hold
	// (especially finished items, whose design documents never affect readiness).
	var questions questionState
	if !p.isBlocked(item, blockedBy) && !(p.lands() && p.unproved[item.ItemID] != "") && item.State == p.rules().Ready &&
		!(hasPrerequisite(p.rules(), definition.PrerequisiteDesignQuestions) && !itemIsComment(item.ItemID) && p.commentHolds[plannerDocumentKey(p.documentRoot, item.DesignDoc)] != "") {
		questions = p.questionState(item)
	}
	switch {
	case p.isBlocked(item, blockedBy):
		decision.Action, decision.Reason = ActionHold, ReasonBlocked
	case p.lands() && p.unproved[item.ItemID] != "":
		decision.Action, decision.Reason = ActionHold, p.unproved[item.ItemID]
	case item.State != p.rules().Ready:
		decision.Action, decision.Reason = ActionHold, ReasonNotReady
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
	case repeated != nil && repeated.Route == repeatRouteHold:
		// Every backend this chain could reach repeated one identical call: the
		// operator is the mover (holdrules "repeating-call").
		decision.Action, decision.Reason = ActionHold, ReasonRepeatingCall
		decision.SupersedesRunID = repeated.RunID
	case retry == nil && !interrupted && repeated == nil && !attempt.infrastructureFailure && returned == nil && p.mayBeARetry(item.ItemID) && decision.ConflictID == "" && !(item.Delivery.ForkRun != "" && item.Delivery.ForkRun == p.latest[item.ItemID]) && holdEnabled(p.rules(), p.retryHoldReason(item.ItemID)):
		decision.Action, decision.Reason = ActionHold, p.retryHoldReason(item.ItemID)
		decision.SupersedesRunID = p.latest[item.ItemID]
		if p.latestFinished[item.ItemID] != "" {
			decision.SupersedesRunID = p.latestFinished[item.ItemID]
		}
	case p.lands() && forkHeld:
		decision.Action, decision.Reason = ActionHold, ReasonForkHeld
		decision.DeferredBy = heldBy
	case p.policy == nil:
		decision.Action, decision.Reason = ActionHold, ReasonNoPolicy
	case item.Command == nil && attempt.backend != "" && !policyHasBackend(p.policy, attempt.backend):
		decision.Action, decision.Reason, decision.DeferredBy = ActionHold, backendMissingReason(item.Seat, attempt.backend), attempt.backend
	case windowShut:
		decision.Action, decision.Reason = ActionHold, ReasonBackendWindow
		decision.DeferredBy = backendWaitStart
	case p.tick.MaxDispatches != nil && p.dispatched >= *p.tick.MaxDispatches:
		decision.Action, decision.Reason = ActionHold, ReasonNoLane
	default:
		isCommand := item.Command != nil
		runKey := attemptRunID(item.ItemID, p.nextAttempt(item.ItemID))
		slotOpen := p.ordinaryAgentSlotOpen(item.Express, isCommand, runKey)
		limitFull := !slotOpen && p.runningAgentLimit > 0
		var lane string
		var ok bool
		if slotOpen {
			lane, ok = p.freeLane(item.Express)
		}
		ownJudgeLane := p.returnedJudgeLane(item.ItemID, returned)
		if ownJudgeLane != "" && (item.Express || !isExpressLane(ownJudgeLane)) {
			ownLaneFree := p.retainedLaneFreeFor(ownJudgeLane, item.ItemID) && !p.tick.OutLanes[ownJudgeLane]
			lane, ok = ownJudgeLane, slotOpen && ownLaneFree
			if !ok {
				if ownLaneFree && limitFull {
					decision.Action, decision.Reason = ActionHold, ReasonAgentLimit
					decision.DeferredBy = activeOrdinaryAgentSummary(p.activeOrdinaryAgents)
				} else {
					decision.Action, decision.Reason = ActionHold, ReasonNoLane
				}
				decision.LaneID, decision.Returned = ownJudgeLane, returned
				if retry != nil && p.retryLaneWait != nil {
					p.retryLaneWait[item.ItemID] = true
				}
				break
			}
		}
		if ownJudgeLane == "" && returned != nil && returned.WorkerSession != nil && (item.Express || !isExpressLane(returned.WorkerSession.LaneID)) && slotOpen && p.retainedLaneFreeFor(returned.WorkerSession.LaneID, item.ItemID) {
			lane = returned.WorkerSession.LaneID
			ok = true
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
			if p.captureStart == nil {
				p.deskOwners[lane] = item.ItemID
			}
			ok = true
		} else if !ok && (IsTrunkFixer(item.ItemID) || IsRemoteSyncItem(item.ItemID)) {
			// Retained candidates can occupy every offered lane. One serial
			// repair desk lets repairs proceed without discarding those
			// worktrees or admitting ordinary work.
			lane = "desk-trunk-fixer"
			ok = !p.held[lane] && !p.assigned[lane]
		}
		if !ok {
			if limitFull {
				decision.Action, decision.Reason = ActionHold, ReasonAgentLimit
				decision.DeferredBy = activeOrdinaryAgentSummary(p.activeOrdinaryAgents)
			} else {
				decision.Action, decision.Reason = ActionHold, ReasonNoLane
			}
			if retry != nil && p.retryLaneWait != nil {
				p.retryLaneWait[item.ItemID] = true
			}
			break
		}
		p.assignLane(lane)
		p.claimOrdinaryAgentSlot(item.Express, isCommand, lane, runKey)
		if p.captureStart == nil {
			p.forks = append(p.forks, plannedFork{itemID: item.ItemID, fork: item.Delivery.activeFork()})
		}
		decision.LaneID = lane
		decision.Inputs = p.inputs[item.ItemID]
		if retry != nil {
			// A retry is a fresh attempt explicitly authorized by an operator.
			decision.Action = ActionRetry
			decision.Reason = ReasonRetry
			decision.RetryID = retry.retryID
			decision.SupersedesRunID = retry.supersedesRunID
			decision.RunRequest = p.request(item.ItemID, lane, attempt.backend)
			decision.RetryCandidate = p.retryCandidate(item.ItemID, retry.supersedesRunID)
			if retriedVerify != nil {
				// The build step runs with the findings, and the nodes before it are
				// carried rather than planned again.
				decision.Returned = retriedVerify
			}
			break
		}
		decision.Action = ActionDispatch
		decision.Reason = ReasonReady
		if attempt.infrastructureFailure && !attempt.quotaRoute {
			decision.InfrastructureFrom = attempt.failedRun
		}
		decision.RunRequest = p.request(item.ItemID, lane, attempt.backend)
		if interrupted {
			decision.Reason = "earlier run orphaned by a loop stop"
		}
		if repeated != nil {
			answered := *repeated
			answered.NextBackend = decision.RunRequest.Backend
			decision.RepeatedCall = &answered
			decision.Reason = ReasonRepeatingCallFresh
		}
		if restaged {
			decision.Reason = ReasonRestagedStaleBase
		}
		if returned != nil {
			if ownJudgeLane != "" {
				decision.RetryCandidate = p.retryCandidate(item.ItemID, returned.RunID)
			}
			decision.Returned = returned
			decision.Reason = ReasonReturned
			decision.WorkerSessionReason = workerSessionReason(returned.WorkerSession, decision.RunRequest)
			if returned.WorkerSession != nil && lane != returned.WorkerSession.LaneID {
				decision.WorkerSessionReason = "lane-unavailable"
			}
			if decision.WorkerSessionReason == "resumed-worker" {
				decision.RunRequest.SessionTurn = true
				decision.RunRequest.ResumePrompt = returnedBrief(*returned)
			}
		}
		if decision.ConflictID != "" {
			decision.Reason = ReasonConflictAnswered
			if returned != nil && answeredObstacle != nil && decision.WorkerSessionReason == "resumed-worker" {
				decision.RunRequest.SessionTurn = true
				decision.RunRequest.ResumePrompt = conflict.Text
			}
		}
	}
	decision.Attempt = p.nextAttempt(item.ItemID)
	decision.From = "held"
	if item.State == StateLanded {
		decision.From = "landed"
	}
	if decision.Action == ActionDispatch || decision.Action == ActionRetry {
		decision.From, decision.To = "queued", "running"
		decision.SelectSeat = !decision.RunRequest.SessionTurn && !interrupted && repeated == nil && (!attempt.infrastructureFailure || retry != nil || decision.ConflictID != "")
	}
	if decision.DeskDocument != "" && decision.RunRequest != nil {
		previousAdapter := ""
		if item.Designer != nil {
			previousAdapter = p.policy.Backends[item.Designer.Backend].Adapter
		}
		decision.Designer = item.Designer
		decision.DesignerSessionID, decision.DesignerReason = chooseDesigner(item.Designer, decision.RunRequest, p.at, previousAdapter)
	}
	if decision.RunRequest != nil {
		stampDecisionRequest(decision, decision.RunRequest)
	}
	identifyDecision(&decision)
	return decision
}

func (d Decision) workflowRef() string {
	if d.SelectedWorkflow != "" {
		return d.SelectedWorkflow
	}
	if d.Workflow != nil && d.Workflow.Name != "" {
		return fmt.Sprintf("%s@v%d", d.Workflow.Name, d.Workflow.Version)
	}
	if d.ItemID != "" {
		return singleWorkerWorkflow
	}
	return ""
}

func stampDecisionRequest(d Decision, req *run.Request) {
	if req == nil {
		return
	}
	if wf := d.workflowRef(); wf != "" {
		req.Workflow = wf
	}
	if d.Workflow != nil && d.NodeIndex >= 0 && d.NodeIndex < len(d.Workflow.Stages) {
		stage := d.Workflow.Stages[d.NodeIndex]
		if stage.Kind == "command" {
			req.Seat = ""
		} else if d.Seat != "" {
			req.Seat = d.Seat
		} else {
			req.Seat = stage.Seat
		}
		req.NodeID = stage.ID
		req.Node = stage.ID
		return
	}
	if d.Seat != "" {
		req.Seat = d.Seat
	}
	if d.Workflow == nil && req.Node == "" {
		req.Node = "worker"
	}
}

// identifyDecision names the decision's current position, including loop-only holds
// that replace a dispatch after staging refused the item's record.
func identifyDecision(decision *Decision) {
	// A resource deferral does not move an otherwise queued item into held.
	// Pending runs and candidates retain their independently derived positions.
	if decision.Action == ActionHold && decision.From == "held" && (decision.Reason == ReasonNoLane || decision.Reason == ReasonAgentLimit || decision.Reason == ReasonForkHeld || decision.Reason == ReasonDeskCapacity || decision.Reason == ReasonDocumentDeskBusy) {
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
	if req := decision.RunRequest; req != nil {
		// A detached quota retry stages fresh and clears WorkflowRestart. Its run
		// still has its own turn at this attempt/node, including when it settles.
		if decision.WorkflowRestart || (decision.Workflow != nil && runIDMarker.MatchString(req.RunID)) {
			decision.TransitionID += "-" + req.RunID
		}
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
		fields := []string{decision.Reason, decision.DeferredBy, decision.Seat,
			decision.QuestionID, decision.RetryID, decision.SupersedesRunID}
		if decision.BackendHoldCause != "" {
			fields = append(fields, decision.BackendHoldCause, decision.BackendHoldMover)
		}
		payload, _ := json.Marshal(fields)
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
	if source, ok := p.questions.(interface {
		DocumentPresence(string) (DocumentQuestions, error)
	}); ok && !checkQuestions {
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

// forkHeldBy finds the live or planned holder. A continuing node may reuse its
// own claim but may not take a fork held by another item.
func (p *planner) forkHeldBy(fork, exceptItem string) (string, bool) {
	if fork == "" {
		return "", false
	}
	for _, planned := range p.forks {
		if planned.fork == fork && planned.itemID != exceptItem {
			return planned.itemID, true
		}
	}
	return "", false
}

// dependencyBlocked answers whether any blocked_by edge on the item is not yet satisfied.
// A blocker the store does not hold or one closed without landing blocks, which is the safe
// direction: neither is evidence that the dependency's change landed.
func (p *planner) dependencyBlocked(blockedBy []string) bool {
	for _, blocker := range blockedBy {
		state, found := p.itemStates[blocker]
		if !found || !blockerSatisfied(p.rules(), state) {
			return true
		}
	}
	return false
}

// isBlocked answers whether the item is held by a blocker: its own state says so, or
// something it is blocked by has not landed or been resolved elsewhere.
func (p *planner) isBlocked(item itemRow, blockedBy []string) bool {
	return item.State == p.rules().Blocked || p.dependencyBlocked(blockedBy)
}

func (p *planner) waitReleased(itemID string) bool {
	return p.latest[itemID] != "" && p.history.waitReleases[itemID] == p.latest[itemID]
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
// The planner handles recorded backend recovery separately: it admits a failed
// run on its own backend, rather than treating machinery failure as judged work.
func (p *planner) mayBeARetry(itemID string) bool {
	if p.waitReleased(itemID) || acquittedRefusal(p.history.refusals[p.latest[itemID]]) {
		return false
	}
	if p.latest[itemID] != "" && p.releasedObstacles[itemID] == p.latest[itemID] {
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
		return ReasonRunLinkMissing + ": no run record names the item, though its history says it ran"
	}
	if reason := p.history.judgeHolds[latest]; reason != "" {
		return reason
	}
	switch p.history.refusals[latest] {
	case ReasonEvalFailed, ReasonJudgeUnreadable, ReasonJudgeDefectiveMismatch, ReasonJudgeCloseRefused,
		RefusalWordsMissing, RefusalWordsAltered, RefusalIdeaNoOutcome:
		return p.history.refusals[latest]
	case RefusalVerifyNotDone:
		return RefusalVerifyNotDone
	case RefusalVerifyNoVerdict:
		return RefusalVerifyNoVerdict
	case RefusalPlanWrong:
		return RefusalPlanWrong
	case RefusalPlanOutput:
		return RefusalPlanOutput
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
	case RefusalDeliveryOrder:
		return RefusalDeliveryOrder
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
	if raw := p.history.gateResults[latest]; raw != "" && classifyCheck(checkresult.Observation{
		Source: checkresult.ResultWord,
		Word:   raw,
	}).Outcome == checkresult.Fail {
		return ReasonGateFailed
	}
	if p.classes[latest] == AttemptUnjudged {
		return ReasonRunUnjudged
	}
	return p.unclassifiedRunReason(itemID, latest)
}

// unclassifiedRunReason is the last fallthrough of retryHoldReason: the run found no
// refusal, gate verdict or landing to name. The reason says what the run's own records
// do say, so the hold is never the bare run-link-missing a person cannot act on.
func (p *planner) unclassifiedRunReason(itemID, latest string) string {
	var found *priorRun
	for i := range p.priors[itemID] {
		if p.priors[itemID][i].RunID == latest {
			found = &p.priors[itemID][i]
		}
	}
	if found == nil {
		return ReasonRunLinkMissing + ": latest run " + latest + " has no run row"
	}
	if !found.Finished {
		return fmt.Sprintf("%s: run %s has no finish record (state %s)", ReasonRunLinkMissing, latest, found.State)
	}
	detail := found.State
	if found.EndCause != "" {
		detail += ", " + found.EndCause
	}
	if found.Node != "" {
		detail += ", node " + found.Node + " " + found.NodeOutcome
	}
	return fmt.Sprintf("%s: latest run %s (%s) recorded no refusal or landing", ReasonRunLinkMissing, latest, detail)
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
	reason := p.retryHoldReason(itemID)
	cause, ok := returnedReasons[reason]
	e := p.history.endings[latest]
	if reason == ReasonEvalFailed && e.Step == reason && (e.WorkerSession != nil || e.Evidence != "") {
		cause, ok = reason, true
	}
	if !ok {
		return nil
	}
	detail := e.Detail
	if cause == RefusalConflict && detail == "" {
		detail = strings.Join(e.ConflictPaths, "\n")
	}
	if cause == RefusalConflict && e.Restart {
		// The candidate was discarded: no session, lane or work of it carries over.
		return &ReturnedEnding{RunID: latest, Cause: cause, Detail: detail, Restart: true}
	}
	return &ReturnedEnding{RunID: latest, Cause: cause, Detail: detail, Checks: e.Checks, WorkerSession: e.WorkerSession, Replan: e.Replan}
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
		if d := p.inputDecision(c.RunID); d.Action == ActionRetry && d.RetryID == row.RetryID {
			return nil
		}
	}
	r := &retry{retryID: row.RetryID, supersedesRunID: row.SupersedesRunID}
	latestMatches := p.latest[itemID] == row.SupersedesRunID ||
		(p.latestFinished[itemID] != "" && p.latestFinished[itemID] == row.SupersedesRunID)
	if e := p.history.endings[row.SupersedesRunID]; !latestMatches && e.Step == ReasonEvalFailed && e.ItemID == itemID {
		// The finished judge is the operator face's latest run, while prior runs
		// track the producer. Only that exact durable judge-to-writer link speaks.
		writer := p.history.judgeWriters[row.SupersedesRunID]
		latestMatches = writer != "" && writer == p.latest[itemID]
	}
	if !latestMatches {
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

func (p *planner) retainedLaneFree(id string) bool {
	return p.offersLane(id) && !p.held[id] && !p.assigned[id]
}

func (p *planner) retainedLaneFreeFor(id, itemID string) bool {
	owner := p.workerLanes[id]
	return p.retainedLaneFree(id) && (owner == "" || owner == itemID)
}

func isExpressLane(id string) bool {
	return strings.HasPrefix(id, "express-")
}

func (p *planner) assignLane(lane string) {
	if p.captureStart == nil {
		p.assigned[lane] = true
		p.dispatched++
	}
}

func (p *planner) ordinaryAgentSlotOpen(express, isCommand bool, runKey string) bool {
	if p.captureStart != nil || express || isCommand {
		return true
	}
	if runKey != "" && p.activeOrdinaryAgents[runKey] != "" {
		return true
	}
	if len(p.activeOrdinaryAgents) < p.runningAgentLimit {
		return true
	}
	p.laneShort = true
	return false
}

func (p *planner) claimOrdinaryAgentSlot(express, isCommand bool, lane, runKey string) {
	if p.captureStart != nil || express || isCommand || isExpressLane(lane) || strings.HasPrefix(lane, "desk-") {
		return
	}
	if p.activeOrdinaryAgents == nil {
		p.activeOrdinaryAgents = map[string]string{}
	}
	if runKey == "" {
		runKey = "planned:" + lane
	}
	p.activeOrdinaryAgents[runKey] = lane
}

func (p *planner) freeLane(express ...bool) (string, bool) {
	if p.captureStart != nil {
		return "", true
	}
	if len(express) > 0 && express[0] {
		for _, lane := range *p.tick.AvailableLanes {
			if isExpressLane(lane) && !p.assigned[lane] && !p.held[lane] && p.workerLanes[lane] == "" {
				return lane, true
			}
		}
	}
	for _, lane := range *p.tick.AvailableLanes {
		if strings.HasPrefix(lane, "desk-") || isExpressLane(lane) {
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
	p.laneShort = true
	return "", false
}

// freeLaneOverLaterReservation lets a waiting finished candidate take a free lane
// whose worker-lane reservation belongs to an item placed after it in queue order,
// when no unreserved eligible lane is free.
func (p *planner) freeLaneOverLaterReservation(express bool, itemOrder map[string]int, before int) (string, bool) {
	if p.captureStart != nil {
		return "", true
	}
	takes := func(lane string) bool {
		if p.assigned[lane] || p.held[lane] {
			return false
		}
		owner := p.workerLanes[lane]
		if owner == "" {
			return true
		}
		pos, ok := itemOrder[owner]
		return ok && pos > before
	}
	if express {
		for _, lane := range *p.tick.AvailableLanes {
			if isExpressLane(lane) && takes(lane) {
				return lane, true
			}
		}
	}
	for _, lane := range *p.tick.AvailableLanes {
		if strings.HasPrefix(lane, "desk-") || isExpressLane(lane) {
			continue
		}
		if takes(lane) {
			return lane, true
		}
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

// readAttemptInputs is shared with the shadow seed so it cannot reuse an ordinal
// the legacy planner already considers spent.
func readAttemptInputs(r *store.Reader, hasNodes bool, id string, priors []priorRun, byRun map[string]*pendingWork) (int, int, error) {
	var count int
	if hasNodes {
		// Claiming a node writes node_runs before a run exists. Neither a
		// claim nor its supersession is evidence that an attempt ran.
		if err := r.QueryRow(`SELECT COALESCE(MAX(n.attempt),0) FROM node_runs n
 JOIN runs r ON r.run_id=n.run_id AND r.item_id=n.item_id WHERE n.item_id=?`, id).Scan(&count); err != nil {
			return 0, 0, readFailure(err)
		}
	}
	ids, err := readRunIDs(r, id)
	if err != nil {
		return 0, 0, readFailure(err)
	}
	next, nodeCount := computeAttemptInputs(id, count, priors, ids, byRun)
	return next, nodeCount, nil
}

func computeAttemptInputs(id string, count int, priors []priorRun, ids []string, byRun map[string]*pendingWork) (int, int) {
	// A run that is not a node run (a design run for a block conflict is named
	// run-<item>-a<N>) still holds its ordinal, so the next attempt is past it.
	maxAttempt := count
	for _, prior := range priors {
		if n := attemptFromRunID(id, prior.RunID); n > maxAttempt {
			maxAttempt = n
		}
	}
	for _, runID := range ids {
		// Judged non-node runs can be absent from priors. Also, an inline
		// landing repair's ID encodes its parent's attempt, not the ordinal
		// its dispatch and settlement used. Read that ordinal from its handoff.
		if n := attemptFromRunID(id, runID); n > maxAttempt {
			maxAttempt = n
		}
		if work := byRun[runID]; work != nil && work.Decision.Attempt > maxAttempt {
			maxAttempt = work.Decision.Attempt
		}
	}
	next := maxAttempt + 1
	if count == 0 && len(priors)+1 > next {
		// With no node runs the count of prior runs is the ordinal, as it always was;
		// a run id ordinal can only move it further.
		next = len(priors) + 1
	}
	return next, count
}

func readAllRunIDs(r *store.Reader) (map[string][]string, error) {
	rows, err := r.Query(`SELECT item_id, run_id FROM runs WHERE item_id IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var itemID, runID string
		if err := rows.Scan(&itemID, &runID); err != nil {
			return nil, err
		}
		out[itemID] = append(out[itemID], runID)
	}
	return out, rows.Err()
}

func readNodeAttemptMaxes(r *store.Reader) (map[string]int, error) {
	rows, err := r.Query(`SELECT n.item_id, MAX(n.attempt) FROM node_runs n
 JOIN runs r ON r.run_id=n.run_id AND r.item_id=n.item_id GROUP BY n.item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var itemID string
		var maxAttempt int
		if err := rows.Scan(&itemID, &maxAttempt); err != nil {
			return nil, err
		}
		out[itemID] = maxAttempt
	}
	return out, rows.Err()
}

func (p *planner) inputDecision(runID string) Decision {
	if runID == "" {
		return Decision{}
	}
	if d, ok := p.inputDecisions[runID]; ok {
		return d
	}
	if p.inputDecisionReader == nil || p.inputDecisionsLoaded[runID] {
		return Decision{}
	}
	if p.inputDecisionsLoaded == nil {
		p.inputDecisionsLoaded = map[string]bool{}
	}
	p.inputDecisionsLoaded[runID] = true
	var payload []byte
	if err := p.inputDecisionReader.QueryRow(`SELECT payload FROM events WHERE object_id = ? AND +kind = ? ORDER BY seq DESC LIMIT 1`, runID, inputRecordedKind).Scan(&payload); err != nil {
		return Decision{}
	}
	var d Decision
	if json.Unmarshal(payload, &d) == nil {
		if p.inputDecisions == nil {
			p.inputDecisions = map[string]Decision{}
		}
		p.inputDecisions[runID] = d
		return d
	}
	return Decision{}
}

// attemptFromRunID answers the attempt N of `run-<item>-a<N>` or `run-<item>-a<N>-n<K>`,
// or 0 for any other run id.
func attemptFromRunID(itemID, runID string) int {
	rest, ok := strings.CutPrefix(runID, "run-"+itemID+"-a")
	if !ok {
		if slug := runItemSlug(itemID); slug != itemID {
			rest, ok = strings.CutPrefix(runID, "run-"+slug+"-a")
		}
	}
	if !ok {
		return 0
	}
	digits := rest
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		digits = rest[:i]
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// readRunIDs answers every run id the store holds for one item.
func readRunIDs(r *store.Reader, itemID string) ([]string, error) {
	rows, err := r.Query(`SELECT run_id FROM runs WHERE item_id = ?`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// readRequestIDs answers the ids of filed requests, which have no item block.
func readRequestIDs(r *store.Reader) (map[string]bool, error) {
	out := map[string]bool{}
	var present int
	if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='request_words'`).Scan(&present); err != nil || present == 0 {
		return out, err
	}
	rows, err := r.Query(`SELECT item_id FROM request_words`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// attemptRunID names one item's ordinal attempt.
func attemptRunID(itemID string, attempt int) string {
	return boundRunIDBase("run-" + runItemSlug(itemID) + "-a" + strconv.Itoa(attempt))
}

// An interruption retains the same backend.
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
	prior := priors[len(priors)-1]
	a.backend, a.failedRun = prior.Backend, prior.RunID
	a.infrastructureFailure = true
	a.quotaRoute = !prior.BackendFailure
	return a
}

// attempt is one item's next dispatch as its run history resolves it.
type attempt struct {
	// backend is the name the request's backend field carries.
	backend string
	// Infrastructure settlement pins the backend and waits on recorded availability.
	infrastructureFailure bool
	// Non-closing infrastructure classifications retain their quota admission path.
	quotaRoute bool
	failedRun  string
}

func policyHasBackend(policy *Policy, backend string) bool {
	_, ok := policy.Backends[backend]
	return ok
}

func backendMissingReason(seat, backend string) string {
	if seat == "" {
		return fmt.Sprintf("%s: backend %s is not in the policy", ReasonBackendMissing, backend)
	}
	return fmt.Sprintf("%s: seat %s needs backend %s, which is not in the policy", ReasonBackendMissing, seat, backend)
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
const queueOrderSQL = `ORDER BY CASE WHEN item_id LIKE 'fixer-main-%' THEN 0 WHEN item_id LIKE 'rewrite-item-%' THEN 1 ELSE 2 END,
		  COALESCE(rank, '') = '', COALESCE(rank, ''), item_id`

func itemSelect(r *store.Reader) (string, error) {
	var hasWorkflow bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='workflow')`).Scan(&hasWorkflow); err != nil {
		return "", readFailure(err)
	}
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return "", readFailure(err)
	}
	expressColumn := "0"
	if hasExpress {
		expressColumn = "COALESCE(express, 0)"
	}
	workflowColumn := "''"
	if hasWorkflow {
		workflowColumn = "COALESCE(workflow, '')"
	}
	var hasArgs bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='workflow_args')`).Scan(&hasArgs); err != nil {
		return "", readFailure(err)
	}
	argsColumn := "''"
	if hasArgs {
		argsColumn = "COALESCE(workflow_args, '')"
	}
	var hasJudge bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='judge_mode')`).Scan(&hasJudge); err != nil {
		return "", readFailure(err)
	}
	judgeColumn := "''"
	if hasJudge {
		judgeColumn = "COALESCE(judge_mode, '')"
	}
	return `SELECT item_id, state, COALESCE(rank, ''), COALESCE(allowed_paths, ''), COALESCE(design_doc, ''),
	        COALESCE(seat, ''), ` + workflowColumn + `, ` + judgeColumn + `, ` + expressColumn + `, ` + argsColumn + ` FROM items `, nil
}

func scanItemRow(scanner interface{ Scan(...any) error }) (itemRow, error) {
	var row itemRow
	var allowed, args string
	if err := scanner.Scan(&row.ItemID, &row.State, &row.Rank, &allowed, &row.DesignDoc, &row.Seat, &row.Workflow, &row.JudgeMode, &row.Express, &args); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return itemRow{}, err
		}
		return itemRow{}, readFailure(err)
	}
	if args != "" {
		if err := json.Unmarshal([]byte(args), &row.WorkflowArgs); err != nil {
			return itemRow{}, Internalf("reading item %s's workflow_args %q: %v", row.ItemID, args, err)
		}
	}
	var err error
	if row.Allowed, err = decodeFence(allowed); err != nil {
		return itemRow{}, err
	}
	row.Express = !itemIsComment(row.ItemID) && (row.Express || strings.HasPrefix(row.ItemID, "item-idea-") || IsRemoteSyncItem(row.ItemID))
	return row, nil
}

func readItems(r *store.Reader) ([]itemRow, error) {
	deliveries, err := readDeliveries(r)
	if err != nil {
		return nil, readFailure(err)
	}
	owed, err := ownercriteria.ReadOwnerCriteria(r)
	if err != nil {
		return nil, readFailure(err)
	}
	query, err := itemSelect(r)
	if err != nil {
		return nil, err
	}
	rows, err := r.Query(query + queueOrderSQL)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	items := []itemRow{}
	for rows.Next() {
		row, err := scanItemRow(rows)
		if err != nil {
			return nil, err
		}
		row.OwnerOwed = owed[row.ItemID]
		row.Delivery = deliveries[row.ItemID]
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return items, nil
}

func readItemByID(r *store.Reader, id string) (itemRow, bool, error) {
	query, err := itemSelect(r)
	if err != nil {
		return itemRow{}, false, err
	}
	row, err := scanItemRow(r.QueryRow(query+`WHERE item_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return itemRow{}, false, nil
	}
	if err != nil {
		return itemRow{}, false, err
	}
	row.OwnerOwed, err = ownercriteria.ReadOwnerCriteriaForItem(r, id)
	if err != nil {
		return itemRow{}, false, readFailure(err)
	}
	return row, true, nil
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

// unmappedSeatHold checks the item's explicit seat before it can take a lane.
// It is computed from the active policy each pass, not stored as an item fault.
func unmappedSeatHold(policy *Policy, it itemRow, attempt int) (Decision, bool) {
	if policy == nil || it.Seat == "" || len(policy.seatList(it.Seat)) != 0 {
		return Decision{}, false
	}
	detail := fmt.Sprintf("seat %q is not mapped; %s", it.Seat, policy.seatDeclaration())
	d := Decision{ItemID: it.ItemID, Seat: it.Seat, Action: ActionHold, From: "held", Attempt: attempt,
		Reason: ReasonSeatUnmapped + ": " + detail}
	identifyDecision(&d)
	return d, true
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

// readItemEdges answers the blocked_by edges for one item.
func readItemEdges(r *store.Reader, itemID string) ([]string, error) {
	rows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id = ? ORDER BY blocked_by`, itemID)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	var blockers []string
	for rows.Next() {
		var blockedBy string
		if err := rows.Scan(&blockedBy); err != nil {
			return nil, readFailure(err)
		}
		blockers = append(blockers, blockedBy)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return blockers, nil
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
	return readLanesWithRuns(r, now, nil)
}

func readLanesWithRuns(r *store.Reader, now time.Time, runs *map[string]string) (map[string]bool, error) {
	query := `SELECT lane_id, state, leased_until FROM lanes`
	if runs != nil {
		query = `SELECT lane_id, state, leased_until, run_id FROM lanes`
		*runs = map[string]string{}
	}
	rows, err := r.Query(query)
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
		var runID sql.NullString
		dest := []any{&laneID, &state, &leasedUntil}
		if runs != nil {
			dest = append(dest, &runID)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, readFailure(err)
		}
		// A leased lane with no expiry cannot be judged by the clock, so it is held: an
		// operator releases it by hand, which is the one answer that does not need a
		// clock.
		held[laneID] = state == LaneStateLeased && (!leasedUntil.Valid || leasedUntil.Int64 > now.UnixMilli())
		if runs != nil && held[laneID] {
			(*runs)[laneID] = runID.String
		}
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
	RunID       string
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
	return activeLeasesExcept(r, now, lanes, "", "")
}

type rawLaneRow struct {
	laneID      string
	state       string
	runID       sql.NullString
	leasedUntil sql.NullInt64
}

func readRawLanes(r *store.Reader) ([]rawLaneRow, error) {
	rows, err := r.Query(`SELECT lane_id, state, run_id, leased_until FROM lanes ORDER BY lane_id`)
	if err != nil {
		return nil, readFailure(err)
	}
	defer rows.Close()
	var out []rawLaneRow
	for rows.Next() {
		var row rawLaneRow
		if err := rows.Scan(&row.laneID, &row.state, &row.runID, &row.leasedUntil); err != nil {
			return nil, readFailure(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return out, nil
}

func activeLeasesFromRows(raw []rawLaneRow, now time.Time, lanes []string, exceptLane, exceptRunID string) []LaneLease {
	offered := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		offered[lane] = true
	}
	leases := []LaneLease{}
	for _, row := range raw {
		if !offered[row.laneID] || row.state != LaneStateLeased {
			continue
		}
		if !row.leasedUntil.Valid {
			if exceptLane != "" && row.laneID == exceptLane && row.runID.Valid && row.runID.String == exceptRunID {
				continue
			}
			leases = append(leases, LaneLease{LaneID: row.laneID, RunID: row.runID.String})
			continue
		}
		until := time.UnixMilli(row.leasedUntil.Int64).UTC()
		if until.After(now) {
			leases = append(leases, LaneLease{LaneID: row.laneID, RunID: row.runID.String, LeasedUntil: &until})
		}
	}
	return leases
}

func activeLeasesExcept(r *store.Reader, now time.Time, lanes []string, exceptLane, exceptRunID string) ([]LaneLease, error) {
	rows, err := readRawLanes(r)
	if err != nil {
		return nil, err
	}
	return activeLeasesFromRows(rows, now, lanes, exceptLane, exceptRunID), nil
}

// runHistory is what the event log says about items' prior runs: whether an item's history
// is known at all, whether it has ever been in progress, and what the loop recorded about a
// run's outcome.
type runHistory struct {
	known     map[string]bool
	ranBefore map[string]bool
	// waitReleases binds the last fulfilled named wait to the run it answered.
	waitReleases map[string]string
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
	endings         map[string]refusalPayload
	obstacleCalls   map[string]*WorkerSession
	judgeWriters    map[string]string
	obstacleEndings map[string]obstacles.Hold
}

// readRunHistory reads the item-object events and answers, per item, whether its history
// is known and whether it has ever been in progress.
//
// Item-object state writes are recognized by the state in their payload, regardless of
// kind. Other evidence (including a wait release without a state) is folded by kind.
func readRunHistory(r *store.Reader) (runHistory, error) {
	events, err := r.EventsOfKind(0, GateVerdictKind, RefusalKind, callOffered, obstacles.HeldKind, item.WaitReleasedKind)
	if err != nil {
		return runHistory{}, readFailure(err)
	}
	history := runHistory{
		known: map[string]bool{}, ranBefore: map[string]bool{}, judged: map[string]bool{}, waitReleases: map[string]string{},
		refusals: map[string]string{}, judgeHolds: map[string]string{}, gateResults: map[string]string{},
		endings: map[string]refusalPayload{}, obstacleCalls: map[string]*WorkerSession{}, judgeWriters: map[string]string{}, obstacleEndings: map[string]obstacles.Hold{},
	}
	var obstacleRunIDs []string
	obstacleSeen := map[string]bool{}
	for _, e := range events {
		if e.Kind == callOffered && e.ObjectType == "call" {
			var call landingCall
			if err := json.Unmarshal(e.Payload, &call); err == nil && call.Tool == "obstacle" && call.RunID != "" && call.SessionID != "" {
				if !obstacleSeen[call.RunID] {
					obstacleSeen[call.RunID] = true
					obstacleRunIDs = append(obstacleRunIDs, call.RunID)
				}
				history.obstacleCalls[call.RunID] = &WorkerSession{
					RunID:     call.RunID,
					SessionID: call.SessionID,
					LaneDir:   filepath.Dir(call.RunLog),
					LaneID:    filepath.Base(filepath.Dir(call.RunLog)),
					BaseSHA:   call.Base,
					HeadSHA:   call.Base,
				}
			}
			continue
		}
		if e.Kind == obstacles.HeldKind {
			var h obstacles.Hold
			if err := json.Unmarshal(e.Payload, &h); err != nil {
				return history, err
			}
			if h.RunID != "" {
				history.obstacleEndings[h.RunID] = h
			}
		}
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
			if err := json.Unmarshal(e.Payload, &payload); err == nil && payload.Step != "" && payload.Step != "judge-grant-absent" {
				history.refusals[*e.ObjectID] = payload.Step
				history.endings[*e.ObjectID] = payload.refusalPayload
				if payload.WriterRunID != "" {
					history.judgeWriters[*e.ObjectID] = payload.WriterRunID
					history.judgeHolds[payload.WriterRunID] = payload.Step
					if payload.WorkerSession != nil || payload.Evidence != "" {
						history.endings[payload.WriterRunID] = payload.refusalPayload
					}
				}
			}
			continue
		}
		if e.ObjectType != "item" || e.ObjectID == nil {
			continue
		}
		if e.Kind == item.WaitReleasedKind {
			var release struct {
				RunID string `json:"run_id"`
			}
			if err := json.Unmarshal(e.Payload, &release); err != nil {
				return history, readFailure(err)
			}
			history.waitReleases[*e.ObjectID] = release.RunID
			continue
		}
	}
	if len(obstacleRunIDs) > 0 {
		if err := populateObstacleCallBackends(r, obstacleRunIDs, history.obstacleCalls); err != nil {
			return history, readFailure(err)
		}
	}
	if err := readItemStateHistory(r, &history); err != nil {
		return history, readFailure(err)
	}
	return history, nil
}

func populateObstacleCallBackends(r *store.Reader, runIDs []string, calls map[string]*WorkerSession) error {
	const chunk = 500
	for start := 0; start < len(runIDs); start += chunk {
		end := start + chunk
		if end > len(runIDs) {
			end = len(runIDs)
		}
		batch := runIDs[start:end]
		args := make([]any, 0, len(batch)+1)
		args = append(args, dispatchStartedKind)
		for _, id := range batch {
			args = append(args, id)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT object_id, json_extract(payload,'$.decision.run_request') FROM events WHERE kind=? AND object_type='run' AND object_id IN (`+placeholders+`) ORDER BY seq`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var runID string
			var reqJSON []byte
			if err := rows.Scan(&runID, &reqJSON); err != nil {
				rows.Close()
				return err
			}
			if len(reqJSON) == 0 {
				continue
			}
			var req run.Request
			if json.Unmarshal(reqJSON, &req) == nil {
				if ws := calls[runID]; ws != nil {
					ws.Backend = req.Backend
					ws.Route = req.Route()
				}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

func readItemStateHistory(r *store.Reader, history *runHistory) error {
	rows, err := r.Query(`SELECT object_id,
 MAX(CASE WHEN json_extract(payload, '$.state') = ? THEN 1 ELSE 0 END)
 FROM events
 WHERE object_id IN (SELECT item_id FROM items)
  AND object_type = 'item'
  AND json_valid(payload)
  AND json_type(payload, '$.state') = 'text'
  AND json_extract(payload, '$.state') <> ''
 GROUP BY object_id`, StateInProgress)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: items") {
			events, selErr := r.EventsSelected(0, store.EventSelection{ObjectTypes: []string{"item"}})
			if selErr != nil {
				return selErr
			}
			for _, e := range events {
				if e.ObjectType != "item" || e.ObjectID == nil {
					continue
				}
				var payload struct {
					State string `json:"state"`
				}
				if json.Unmarshal(e.Payload, &payload) != nil || payload.State == "" {
					continue
				}
				history.known[*e.ObjectID] = true
				if payload.State == StateInProgress {
					history.ranBefore[*e.ObjectID] = true
				}
			}
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID string
		var ran int
		if err := rows.Scan(&itemID, &ran); err != nil {
			return err
		}
		history.known[itemID] = true
		if ran != 0 {
			history.ranBefore[itemID] = true
		}
	}
	return rows.Err()
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
