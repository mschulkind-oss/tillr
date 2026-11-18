package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"os"
	"strconv"
)

// These events record effects, not a second stored item state: the request handed to
// a runner, the candidate a terminal run produced, and the completed examination.
// Positions are derived anew from these facts, the terminal record and the lease.
const (
	examinationClaimedKind  = "drain.examination-claimed"
	examinationReleasedKind = "drain.examination-released"
	dispatchStartedKind     = "drain.started"
	candidateReadyKind      = "drain.candidate"
	candidateRetirementKind = "drain.retirement"
	candidatePreparedKind   = "drain.prepared-candidate"
	examinationDoneKind     = "drain.examined"
	examinationOwnerKind    = "drain.examination.owner"
	loopOrphanedKind        = "drain.orphaned"
	// laneReleasedKind records a lane released after its run was examined, for every run,
	// not only a workflow node (whose drain.examination-released also clears its claim).
	laneReleasedKind = "drain.lane-released"
	ActionSettle     = "settle"
	ActionJudge      = "judge"
)

func init() {
	for _, kind := range []string{commentReplyStateKind, designerCompactedKind, examinationClaimedKind, examinationReleasedKind, dispatchStartedKind, candidateReadyKind, candidateRetirementKind, candidatePreparedKind, examinationDoneKind, examinationOwnerKind, loopOrphanedKind, laneReleasedKind, nodeAdvancedKind, graphChangedKind, candidateGatedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

type pendingWork struct {
	LoopID         string         `json:"loop_id,omitempty"`
	Examining      bool           `json:"examining,omitempty"`
	Ready          bool           `json:"ready,omitempty"`
	Gated          bool           `json:"-"`
	JudgeLanded    bool           `json:"-"`
	ParentRunID    string         `json:"parent_run_id,omitempty"`
	GateStall      *gateStallHold `json:"gate_stall,omitempty"`
	DirtyHold      *dirtyHold     `json:"dirty_hold,omitempty"`
	RedCapHold     *redCapHold    `json:"red_cap_hold,omitempty"`
	RepairHold     *repairHold    `json:"repair_hold,omitempty"`
	Decision       Decision       `json:"decision"`
	Examiner       *LoopOwner     `json:"examination_owner,omitempty"`
	Owner          *LoopOwner     `json:"loop_owner,omitempty"`
	BaseSHA        string         `json:"base_sha"`
	Candidate      *Candidate     `json:"candidate,omitempty"`
	Done           bool           `json:"-"`
	Orphaned       bool           `json:"-"`
	BoundaryFailed bool           `json:"boundary_failed,omitempty"`
	LaneReleased   bool           `json:"lane_released,omitempty"`
	LaneKeepReason string         `json:"lane_keep_reason,omitempty"`
	HeldFence      string         `json:"held_fence,omitempty"`
	NeedsStage     bool           `json:"needs_stage,omitempty"`
	ReleasePending string         `json:"release_pending,omitempty"`
}

func readPending(r *store.Reader) (map[string]*pendingWork, error) {
	works, _, err := readPendingHistory(r)
	return works, err
}

// Keep every attempt's effective examination state, including superseded dispatches.
// Second-failure routing needs the same continuation semantics as current planning.
func readPendingHistory(r *store.Reader) (map[string]*pendingWork, map[string]*pendingWork, error) {
	events, err := r.Events(0)
	if err != nil {
		return nil, nil, err
	}
	works := map[string]*pendingWork{}
	byRun := map[string]*pendingWork{}
	judged := map[string]bool{}
	segments := map[string]bool{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case dispatchStartedKind, nodeAdvancedKind, graphChangedKind:
			var work pendingWork
			if err := json.Unmarshal(e.Payload, &work); err != nil {
				return nil, nil, err
			}
			works[work.Decision.ItemID] = &work
			byRun[*e.ObjectID] = &work
			// Every sibling belongs to this pending set. Standalone continuation
			// may name any one of them, not just the primary dispatch run.
			for _, node := range parallelNodes(work.Decision) {
				if node.RunRequest != nil {
					byRun[node.RunRequest.RunID] = &work
				}
			}
		case GateVerdictKind:
			if e.ObjectType == "run" {
				judged[*e.ObjectID] = true
				if work := byRun[*e.ObjectID]; work != nil && work.GateStall != nil {
					var verdict struct {
						Result string             `json:"result"`
						Checks []StallObservation `json:"checks"`
					}
					if err := json.Unmarshal(e.Payload, &verdict); err != nil {
						return nil, nil, err
					}
					if verdict.Result == GateStalled {
						for _, check := range verdict.Checks {
							if check.Result == GateStalled {
								work.GateStall.SecondObservations = append(work.GateStall.SecondObservations, check)
							}
						}
					}
				}
			}
		case "land.landing":
			var landing struct {
				RunID string `json:"run_id"`
			}
			if err := json.Unmarshal(e.Payload, &landing); err != nil {
				return nil, nil, err
			}
			judged[landing.RunID] = true
		case run.ResumeKind:
			// Exec emits this in the claim transaction, after run.allocate and
			// before the continued process starts. A terminal record alone is
			// not continuation authorization. Replayed segment events must not
			// undo a later examination of that same segment.
			var segment struct {
				Request   *run.Request `json:"request,omitempty"`
				RunID     string       `json:"run_id"`
				ItemID    string       `json:"item_id"`
				LaneID    string       `json:"lane_id"`
				Backend   string       `json:"backend"`
				SessionID string       `json:"session_id"`
				ResumedAt string       `json:"resumed_at"`
			}
			if err := json.Unmarshal(e.Payload, &segment); err != nil {
				return nil, nil, err
			}
			key := fmt.Sprintf("%s@%d:%s", *e.ObjectID, e.At.UnixMilli(), e.Payload)
			if segments[key] {
				continue
			}
			segments[key] = true
			work := byRun[*e.ObjectID]
			if e.ObjectType != "run" || work == nil || judged[*e.ObjectID] || work.Gated || work.Decision.RunRequest == nil {
				continue
			}
			req := work.Decision.RunRequest
			for _, node := range parallelNodes(work.Decision) {
				if node.RunRequest != nil && node.RunRequest.RunID == *e.ObjectID {
					req = node.RunRequest
					break
				}
			}
			if segment.RunID != req.RunID || segment.ItemID != req.ItemID || segment.LaneID != work.Decision.LaneID || !run.IsSessionID(segment.SessionID) || segment.ResumedAt == "" {
				continue
			}
			if segment.Request != nil && segment.Backend != req.Backend && quotaSingleRun(work.Decision) {
				next := *segment.Request
				if next.RunID == req.RunID && next.ItemID == req.ItemID && next.LaneID == req.LaneID && next.Backend == segment.Backend && next.Resume && next.FreshSession {
					next.Resume, next.FreshSession, next.ResumePrompt = false, false, ""
					work.Decision.RunRequest = &next
					req = &next
				}
			}
			if segment.Backend != req.Backend {
				continue
			}
			work.Done, work.Orphaned, work.Examining = false, false, false
			work.Owner, work.Candidate, work.DirtyHold, work.RepairHold, work.GateStall = nil, nil, nil, nil, nil
		case examinationClaimedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				if err := json.Unmarshal(e.Payload, &work.Owner); err != nil {
					return nil, nil, err
				}
				work.Examining = true
			}
		case examinationReleasedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Examining = false
			}
			for _, work := range works {
				if work.Ready && work.ParentRunID == *e.ObjectID {
					work.Examining = false
				}
			}
		case candidateReadyKind, candidateRetirementKind, candidatePreparedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return nil, nil, err
				}
				c.Request = work.Decision.RunRequest
				c.Seat = work.Decision.Seat
				work.Candidate = &c
			}
		case judgeLandedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return nil, nil, err
				}
				c.Request, c.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.Candidate, work.Gated, work.JudgeLanded = &c, true, true
				work.DirtyHold, work.RedCapHold, work.RepairHold = nil, nil, nil
			}
		case candidateGatedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return nil, nil, err
				}
				c.Request, c.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.Candidate, work.Gated = &c, true
			}
		case BoundaryCheckKind:
			var check boundaryCheckPayload
			if err := json.Unmarshal(e.Payload, &check); err != nil {
				return nil, nil, err
			}
			if !check.Clean {
				// Continuation checks have segment identities; the payload retains
				// the attempt's run identity, including standalone resume checks.
				for _, ref := range check.Runs {
					if work := byRun[ref.RunID]; work != nil {
						work.BoundaryFailed = true
					}
				}
			}
		case loopOrphanedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Orphaned = true
			}
		case redCapHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold redCapHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return nil, nil, err
				}
				hold.Candidate.Request, hold.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.RedCapHold, work.Candidate, work.Gated = &hold, &hold.Candidate, true
			}
		case dirtyHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold dirtyHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return nil, nil, err
				}
				work.DirtyHold = &hold
				work.Candidate = &hold.Candidate
			}
		case gateStallHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold gateStallHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return nil, nil, err
				}
				hold.Candidate.Request, hold.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.GateStall, work.Candidate = &hold, &hold.Candidate
			}
		case repairHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold repairHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return nil, nil, err
				}
				work.RepairHold = &hold
			}
		case itemFaultClearedKind:
			var fault ItemFault
			if err := json.Unmarshal(e.Payload, &fault); err != nil {
				return nil, nil, err
			}
			if work := byRun[fault.RunID]; work != nil && fault.Reason == StopHostBoundaryViolation {
				work.BoundaryFailed = false
			}
			if work := byRun[fault.RunID]; work != nil && fault.Reason == ReasonGateStalled {
				work.GateStall = nil
			}
		case examinationOwnerKind:
			if work := byRun[*e.ObjectID]; work != nil {
				if err := json.Unmarshal(e.Payload, &work.Examiner); err != nil {
					return nil, nil, err
				}
			}
		case candidateReleaseStartedKind, candidateLaneKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var lane candidateLane
				if err := json.Unmarshal(e.Payload, &lane); err != nil {
					return nil, nil, err
				}
				lane.Candidate.Request, lane.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.Candidate = &lane.Candidate
				work.LaneReleased, work.LaneKeepReason, work.HeldFence = lane.Released, lane.Reason, lane.Fence
				if e.Kind == candidateReleaseStartedKind {
					work.ReleasePending = lane.ResetSHA
				} else if lane.Released {
					work.ReleasePending = ""
				}
			}
		case candidateAssignedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				// The payload is the pending state at assignment, and its omitempty
				// fields drop a false lane_released and an empty keep reason or release
				// intent. Decoding over the replayed value kept the earlier release's
				// lane_released, so the newly occupied lane read as free: its next hold
				// skipped the reset and the pin moved on, leaving a copy of the
				// candidate on every lane it passed through. The assignment ends that
				// release, so clear its state first. Everything else decodes over the
				// replayed value as before, keeping replay-only facts such as a gate
				// stall's second observations.
				work.LaneReleased, work.LaneKeepReason, work.ReleasePending = false, "", ""
				if err := json.Unmarshal(e.Payload, work); err != nil {
					return nil, nil, err
				}
				if work.Candidate != nil {
					work.Candidate.Request, work.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				}
			}
		case candidateStagedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.NeedsStage = false
			}
		case examinationDoneKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Done = true
			}
		}
	}
	return works, byRun, nil
}

func terminalRecord(r *store.Reader, id string) (*run.Record, error) {
	env, found, err := run.StatusRecord(r, id)
	if err != nil || !found {
		return nil, err
	}
	var status struct {
		Run *run.Record `json:"run"`
	}
	if err := json.Unmarshal(env.Payload, &status); err != nil {
		return nil, err
	}
	// The status row has no resumed column. The terminal event retains that
	// segment fact, so a replacement Loop does not continue it a second time.
	var payload []byte
	err = r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, run.FinishKind, id).Scan(&payload)
	if err == nil && status.Run != nil {
		var terminal run.Record
		if err := json.Unmarshal(payload, &terminal); err != nil {
			return nil, err
		}
		status.Run.Resumed = terminal.Resumed
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return status.Run, nil
}

func pendingDecision(r *store.Reader, work *pendingWork, at time.Time, inputs *RunInputs, template *string, policy *Policy, shut map[string]bool, gates int) (Decision, error) {
	d := work.Decision
	d.From, d.To = "running", ""
	d.Action, d.Reason = ActionHold, ReasonRunLive
	if evaluatorNode(d) {
		grant, err := judgeGrant(r, d.ItemID, at)
		if err != nil {
			return Decision{}, err
		}
		if grant == "" {
			d.Reason = ReasonJudgeGrantAbsent
			identifyDecision(&d)
			return d, nil
		}
	}
	if len(d.Parallel) > 0 && work.Orphaned && !work.BoundaryFailed {
		leases, err := activeLeases(r, at, []string{d.LaneID})
		if err != nil {
			return Decision{}, err
		}
		if len(leases) == 0 {
			restarted, changed, err := restartParallel(r, d)
			if err != nil {
				return Decision{}, err
			}
			if changed {
				copy := *work
				copy.Decision, copy.Ready, copy.Orphaned = restarted, true, false
				return pendingDecision(r, &copy, at, inputs, template, policy, shut, gates)
			}
		}
	}
	if d.Workflow != nil && len(d.Parallel) == 0 && work.Orphaned && !work.BoundaryFailed {
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if err != nil {
			return Decision{}, err
		}
		leases, err := activeLeases(r, at, []string{d.LaneID})
		if err != nil {
			return Decision{}, err
		}
		// A real successful terminal record wins over an operator-stop marker.
		// Reaped or interrupted work has no finished node to preserve.
		if len(leases) == 0 && (rec == nil || rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0) {
			workCopy := *work
			request := *d.RunRequest
			if !evaluatorNode(d) {
				request.RunID += "-restart"
			}
			d.RunRequest = &request
			d.WorkflowRestart = true
			workCopy.Decision, workCopy.Ready, workCopy.Orphaned = d, true, false
			return pendingDecision(r, &workCopy, at, inputs, template, policy, shut, gates)
		}
	}
	if work.Ready {
		if len(d.Parallel) > 0 && d.WorkflowRestart {
			for _, node := range parallelNodes(d) {
				rec, err := terminalRecord(r, node.RunRequest.RunID)
				if err != nil {
					return Decision{}, err
				}
				if rec == nil && !commandStage(node) && shut[node.RunRequest.Backend] {
					d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
					identifyDecision(&d)
					return d, nil
				}
			}
		}
		if len(d.Parallel) > 0 {
			open, err := dispatchQuotaOpen(r, d, shut, policy)
			if err != nil {
				return Decision{}, err
			}
			d.Action, d.Reason, d.From, d.To = ActionDispatch, "workflow-next-node", "running", "running"
			if !open {
				d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
			}
			d.Inputs = inputs
			identifyDecision(&d)
			return d, nil
		}
		if commandStage(d) {
			d.Action, d.Reason, d.From, d.To = ActionDispatch, "workflow-next-node", "running", "running"
			group := d.RunRequest.ReadOnlyGroup
			d.RunRequest = d.Workflow.Stages[d.NodeIndex].commandRequest(d.ItemID, d.LaneID, d.RunRequest.RunID)
			d.RunRequest.ReadOnlyGroup = group
			d.Inputs = inputs
			identifyDecision(&d)
			return d, nil
		}
		if policy == nil {
			d.Action, d.Reason, d.From, d.To = ActionHold, ReasonNoPolicy, "running", ""
			d.RunRequest = nil
			identifyDecision(&d)
			return d, nil
		}
		d.Action, d.Reason, d.From, d.To = ActionDispatch, "workflow-next-node", "running", "running"
		d.SelectSeat = d.NodeInfraFailures == 0 && !d.WorkflowRestart
		name := policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: d.Seat})
		if d.NodeInfraFailures > 0 {
			name = d.RunRequest.Backend
		}
		name, degraded, ok := policy.availableBackend(name, d.Seat, shut)
		if d.WorkflowRestart {
			name, degraded = d.RunRequest.Backend, ""
			ok = !shut[name]
		}
		if !ok {
			d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
		} else {
			request := policy.request(d.ItemID, d.LaneID, d.RunRequest.RunID, name)
			if d.WorkflowRestart {
				retained := *d.RunRequest
				request = &retained
			}
			if request == nil {
				return Decision{}, Usagef("workflow seat %s cannot build a request", d.Seat)
			}
			if !d.WorkflowRestart && d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
				request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
			}
			if len(d.Parallel) > 0 {
				request.ReadOnlyGroup = d.RunRequest.ReadOnlyGroup
				request.NodeID = d.RunRequest.NodeID
			}
			d.RunRequest = request
			if degraded != "" {
				d.Reason = seatDegradedReason(degraded, name)
			}
		}
		d.Inputs = inputs
		identifyDecision(&d)
		return d, nil
	}
	if work.BoundaryFailed {
		d.From, d.To, d.Action, d.Reason = "held", "", ActionHold, StopHostBoundaryViolation
		identifyDecision(&d)
		return d, nil
	}
	if work.RepairHold != nil {
		unchanged, err := work.RepairHold.unchanged(inputs, template)
		if err != nil {
			return Decision{}, err
		}
		if unchanged {
			d.From, d.To, d.Action, d.Reason = "held", "", ActionHold, ReasonRepairStagingFailed
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.Candidate != nil && work.Candidate.Judge == nil && work.Candidate.Evaluator == nil && policy != nil {
		rows, err := readItems(r)
		if err != nil {
			return Decision{}, err
		}
		for _, row := range rows {
			if row.ItemID != d.ItemID || row.JudgeMode != "agent" {
				continue
			}
			seat := policy.seatList("review")
			if len(seat) > 0 {
				_, open := firstOpenSeatBackend(*policy, "review", shut)
				if !open {
					d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, ReasonBackendWindow
					identifyDecision(&d)
					return d, nil
				}
			}
		}
	}
	if work.Candidate != nil && work.Candidate.Judge != nil {
		grant, err := judgeGrant(r, d.ItemID, at)
		if err != nil {
			return Decision{}, err
		}
		if grant == "" {
			d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, ReasonJudgeGrantAbsent
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.Candidate != nil && work.Gated && !work.JudgeLanded {
		h, err := readTrunkHistory(r)
		if err != nil {
			return Decision{}, err
		}
		if h.Lost != "" {
			d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, h.reason()
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.GateStall != nil && !work.GateStall.canRetry(gates) {
		d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, ReasonGateStallRerun
		identifyDecision(&d)
		return d, nil
	}
	if work.RedCapHold != nil {
		cap, err := work.RedCapHold.current(context.Background(), r, at)
		if err != nil {
			return Decision{}, err
		}
		if cap != nil {
			d.From, d.To, d.Action, d.Reason, d.DeferredBy = "candidate", "", ActionHold, cap.Reason(), cap.FixerItem
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.DirtyHold != nil {
		paths, err := work.DirtyHold.paths(context.Background())
		if err != nil {
			return Decision{}, err
		}
		if len(paths) > 0 {
			d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, dirtyReason(paths)
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.Candidate != nil {
		d.From, d.To, d.Action, d.Reason = "candidate", "landed", ActionJudge, ActionJudge
	} else {
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if err != nil {
			return Decision{}, err
		}
		finished := rec != nil
		if len(d.Parallel) > 0 {
			finished, err = parallelTerminal(r, d)
			if err != nil {
				return Decision{}, err
			}
		}
		if finished {
			if rec != nil && quotaSingleRun(d) {
				backend, err := quotaPauseBackend(r, rec.RunID)
				if err != nil {
					return Decision{}, err
				}
				_, _, _, refused := refusedWindow(*rec)
				if backend != "" && (rec.State == run.StatePaused || refused) && (policy == nil || shut[backend]) {
					open := false
					if policy != nil {
						_, _, open = policy.availableBackend(backend, d.Seat, shut)
					}
					if !open {
						d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
						identifyDecision(&d)
						return d, nil
					}
				}
			}
			if rec != nil {
				failed, err := failedDesignerResume(d, *rec)
				if err != nil {
					return Decision{}, err
				}
				if failed && policy != nil {
					if _, _, open := policy.availableBackend(d.RunRequest.Backend, d.Seat, shut); !open {
						d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
						identifyDecision(&d)
						return d, nil
					}
				}
			}
			d.To, d.Action, d.Reason = "candidate", ActionSettle, ActionSettle
		} else if evaluatorNode(d) && rec == nil {
			// Retry the judge's retained handoff, never allocate another evaluator id.
			d.To, d.Action, d.Reason, d.WorkflowRestart = "running", ActionDispatch, "workflow-next-node", true
		}
	}
	if d.Action != ActionHold {
		if !work.LaneReleased {
			leases, err := activeLeases(r, at, []string{d.LaneID})
			if err != nil {
				return Decision{}, err
			}
			if len(leases) != 0 {
				d.Action, d.Reason, d.To = ActionHold, ReasonNoLane, ""
			}
		}
	}
	identifyDecision(&d)
	return d, nil
}

// inputVersion binds an application to the item's state, latest terminal run and
// assigned lane's lease. JSON retains SQL NULL and all values; equality is an exact
// comparison, not an assertion that the precomputed decision is still appropriate.
// The pending facts and item fields are included so an intervening editor or pass also
// invalidates a plan. No event or decision is written on a failed comparison.
func inputVersion(r *store.Reader, d Decision) (string, error) {
	var itemState, rank, allowed, design, seat sql.NullString
	var updated int64
	var express bool
	var hasExpress bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
		return "", err
	}
	if hasExpress {
		if err := r.QueryRow(`SELECT express FROM items WHERE item_id=?`, d.ItemID).Scan(&express); err != nil {
			return "", err
		}
	}
	if err := r.QueryRow(`SELECT state, rank, allowed_paths, design_doc, seat, updated_at FROM items WHERE item_id = ?`, d.ItemID).
		Scan(&itemState, &rank, &allowed, &design, &seat, &updated); err != nil {
		return "", err
	}
	var latest, laneState, laneRun sql.NullString
	var seq, until sql.NullInt64
	err := r.QueryRow(`SELECT run_id, event_seq FROM runs WHERE item_id = ? ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`, d.ItemID).Scan(&latest, &seq)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if d.LaneID != "" {
		err = r.QueryRow(`SELECT state, run_id, leased_until FROM lanes WHERE lane_id = ?`, d.LaneID).Scan(&laneState, &laneRun, &until)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	works, err := readPending(r)
	if err != nil {
		return "", err
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return "", err
	}
	work := works[d.ItemID]
	triageVersion := ""
	if d.BlockConflict != nil && d.BlockConflict.TriageVersion != "" {
		triageVersion, _, err = requestVersion(r, d.ItemID)
		if err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal([]any{itemState, rank, allowed, design, seat, express, updated, latest, seq, laneState, laneRun, until, work, work != nil && work.Done, faults[d.ItemID], faults[d.ItemID].Seq, triageVersion})
	return string(raw), err
}

type plannedApplication struct {
	decision      Decision
	version       string
	parentRunID   string
	definitionSHA string
	definition    *definition.Definition
}

func (p plannedApplication) checksComments() bool {
	d := p.definition
	if d == nil {
		fallback := defaultDefinition() // Direct application fixtures predate definitions.
		d = &fallback
	}
	return holdEnabled(*d, ReasonVantageUnavailable)
}

// currentDefinition refuses a stale application before it claims a run or records
// a hold. The pass reads the declaration once; applications revalidate it in their
// write transaction alongside the item's other input versions.
func currentDefinition(r *store.Reader, p plannedApplication) (bool, error) {
	if p.definitionSHA == "" {
		return true, nil
	} // Direct application unit fixtures.
	declared, err := definition.ForReader(r)
	if err != nil {
		return false, err
	}
	return declared.SHA256 == p.definitionSHA, nil
}

type runCompletion struct {
	id      string
	itemID  string
	step    string
	outcome laneOutcome
	detail  string
	result  *LoopResult
	timing  *PassTiming
	// laneRefusal is a pinned candidate's staging refused by its assigned lane's own
	// state; the pass takes that lane out of rotation (lane_rotation.go).
	laneRefusal *LaneRefusalError
}

// passPlans takes one read of the world. Application takes one write transaction per
// item, in this same rank order. The clock is only an observation for lease checks.
func (l *Loop) passPlans(ctx context.Context, at time.Time) ([]plannedApplication, []LaneLease, error) {
	generation := ""
	return l.passPlansGeneration(ctx, at, &generation)
}

func (l *Loop) passPlansGeneration(ctx context.Context, at time.Time, generation *string) ([]plannedApplication, []LaneLease, error) {
	if err := l.refreshQuotas(ctx); err != nil {
		return nil, nil, err
	}
	at = l.cfg.Now()
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	nextGeneration := *generation
	// The process-table evidence is gathered with no transaction open. The run-start
	// lock spans it and the write, so no run face starts between the scan and the reap.
	if err := withRunStartLock(ctx, l.cfg.Store, func() error {
		ev, err := l.gatherReapEvidence(ctx, s)
		if err != nil {
			return err
		}
		defer func() {
			for _, line := range *ev.reclaimed {
				l.reportf("%s", line)
			}
		}()
		return s.WriteTx(ctx, func(tx *store.Tx) error {
			var before, after int64
			if err := tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&before); err != nil {
				return err
			}
			if err := l.reapDeadLoopRuns(ctx, tx, at, ev); err != nil {
				return err
			}
			released, err := run.ReapExpiredLeases(ctx, tx, at)
			if err != nil {
				return err
			}
			if err := tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&after); err != nil {
				return err
			}
			if (len(released) > 0 || after != before) && nextGeneration == "" {
				nextGeneration, err = allocateGeneration(tx)
			}
			return err
		})
	}); err != nil {
		return nil, nil, err
	}
	*generation = nextGeneration
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		return nil, nil, err
	}
	// Reaping above may have just ended a run and freed its lane. Discover
	// surviving children after those writes and before offering any lane, for
	// hook definitions too; only the Git fingerprint checks are landing-specific.
	if l.nonLanding {
		err = l.recheckLaneChildren(ctx)
	} else {
		err = l.recheckLanes(ctx)
	}
	if err != nil {
		return nil, nil, err
	}
	var plans []plannedApplication
	var leases []LaneLease
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		observed := append([]string(nil), l.cfg.Lanes...)
		held, err := readLanes(r, at)
		if err != nil {
			return err
		}
		for lane, busy := range held {
			if busy && documentDesk(lane) {
				observed = append(observed, lane)
			}
		}
		leases, err = activeLeases(r, at, observed)
		if err != nil {
			return err
		}
		// A lane a lane-owned staging refusal took out of rotation is offered to no item
		// until a re-check puts it back (lane_rotation.go).
		available := l.rotationLanes()
		tick := Pass{Comments: l.cfg.Comments, WorkflowDefaults: l.cfg.WorkflowDefaults, GatesInFlight: l.gateCount(), Build: &l.build, Now: at.Format(time.RFC3339Nano), AvailableLanes: &available}
		if stager, ok := l.cfg.Stage.(*LaneStager); ok {
			tick.BriefTemplate = &stager.Template
		}
		questions := l.questions
		if questions == nil {
			// Standalone planner callers do not own a document refresh lifecycle.
			questions = &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return readFailure(err)
		}
		envs, err := planDefinition(r, tick, l.cfg.Policy, questions, declared.Parsed)
		if err != nil {
			return err
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			if d.Reason == ReasonNoPolicy {
				return Usagef("the loop's policy cannot build a request for item %s (seat %s)", d.ItemID, d.Seat)
			}
			if reason, held := l.heldFor(r, d.ItemID); held && holdEnabled(declared.Parsed, reason) {
				d.Action, d.Reason, d.From, d.To = ActionHold, reason, "held", ""
				d.LaneID, d.RunRequest = "", nil
				identifyDecision(&d)
			}
			version, err := inputVersion(r, d)
			if err != nil {
				return err
			}
			parent := ""
			if work := works[d.ItemID]; work != nil {
				parent = work.ParentRunID
			}
			plans = append(plans, plannedApplication{decision: d, version: version, parentRunID: parent, definitionSHA: declared.SHA256, definition: &declared.Parsed})
		}
		return nil
	})
	return plans, leases, err
}

func appendEffect(tx *store.Tx, kind, id string, value any, at time.Time) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: &id, Payload: body, At: at})
	return err
}

// applyPlan records the transition and claims its resource atomically, after re-reading
// all three inputs. Subprocesses run after commit, never while the store is locked.
func (l *Loop) applyPlan(ctx context.Context, p plannedApplication, mainSHA string, at time.Time) (bool, *pendingWork, error) {
	generation := ""
	return l.applyPlanGeneration(ctx, p, mainSHA, at, &generation)
}

func (l *Loop) applyPlanGeneration(ctx context.Context, p plannedApplication, mainSHA string, at time.Time, generation *string) (bool, *pendingWork, error) {
	if p.decision.Action == ActionHold {
		if c := p.decision.BlockConflict; c != nil && c.TriageVersion != "" && c.WaitingOn == "owner" {
			s, err := store.Open(ctx, l.cfg.Store, true)
			if err != nil {
				return false, nil, err
			}
			defer s.Close()
			err = s.WriteTx(ctx, func(tx *store.Tx) error {
				current, err := currentDefinition(tx.Reader(), p)
				if err != nil || !current {
					return err
				}
				version, err := inputVersion(tx.Reader(), p.decision)
				if err != nil || version != p.version {
					return err
				}
				return item.RecordConflict(tx, *c, at)
			})
			return false, nil, err
		}
		if !strings.HasPrefix(p.decision.Reason, ReasonTrunkPathDirty+": ") && !strings.HasPrefix(p.decision.Reason, ReasonTrunkRewritten+": ") && !strings.HasPrefix(p.decision.Reason, ReasonRedCap+": ") {
			return false, nil, nil
		}
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return false, nil, err
		}
		defer s.Close()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			current, err := currentDefinition(tx.Reader(), p)
			if err != nil || !current {
				return err
			}
			version, err := inputVersion(tx.Reader(), p.decision)
			if err != nil || version != p.version {
				return err
			}
			return recordDirtyDecision(tx, p.decision, at)
		})
		return false, nil, err
	}
	// Refresh before claiming; a claimed dispatch is never checked after staging.
	if (p.decision.Action == ActionDispatch || p.decision.Action == ActionRetry) && p.decision.RunRequest != nil {
		if err := l.refreshQuotas(ctx); err != nil {
			return false, nil, err
		}
	}
	nextGeneration := *generation
	correlate := func(tx *store.Tx, d *Decision) error {
		if nextGeneration == "" {
			var err error
			nextGeneration, err = allocateGeneration(tx)
			if err != nil {
				return err
			}
		}
		d.PassID = nextGeneration
		if d.RunRequest != nil {
			request := *d.RunRequest
			request.CorrelationID = nextGeneration
			d.RunRequest = &request
		}
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, nil, err
	}
	defer s.Close()
	applied := false
	var work *pendingWork
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		current, err := currentDefinition(tx.Reader(), p)
		if err != nil || !current {
			return err
		}
		version, err := inputVersion(tx.Reader(), p.decision)
		if err != nil {
			return err
		}
		if version != p.version {
			return nil
		}
		d := p.decision
		if (d.Action == ActionDispatch || d.Action == ActionRetry) && d.RunRequest != nil {
			shut, err := readQuotaShutBackends(tx.Reader(), l.cfg.Now(), l.currentPolicy())
			if err != nil {
				return err
			}
			open, err := dispatchQuotaOpen(tx.Reader(), d, shut, l.currentPolicy())
			if err != nil {
				return err
			}
			if !open {
				return nil
			}
		}
		if d.Action == ActionDispatch || d.Action == ActionRetry {
			// A concurrent landing can publish an answered conflict after the pass
			// refreshed documents. Item versions alone do not bind that new decision
			// to the refreshed trunk; defer before claiming a run at the old base.
			head, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
			if err != nil {
				return err
			}
			if head != mainSHA {
				return nil
			}
		}
		if d.Inputs != nil && (d.Action == ActionDispatch || d.Action == ActionRetry) {
			rows, err := readItems(tx.Reader())
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.ItemID != d.ItemID {
					continue
				}
				now, err := currentRunInputs(tx.Reader(), row, l.cfg.Policy, l.cfg.Root, l.build)
				if err != nil {
					return err
				}
				if *now != *d.Inputs {
					return nil
				}
			}
		}
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work = works[d.ItemID]
		switch d.Action {
		case ActionDispatch, ActionRetry:
			for _, owner := range works {
				if !owner.Done && !owner.LaneReleased && owner.Decision.LaneID == d.LaneID && !(owner.Decision.ItemID == d.ItemID && (owner.Ready || d.WorkflowRestart)) {
					return nil
				}
			}
			leases, err := activeLeases(tx.Reader(), at, []string{d.LaneID})
			if err != nil {
				return err
			}
			if len(leases) != 0 {
				return nil
			}
			// Capacity is a dispatch invariant, not just a snapshot prediction.
			// The writer transaction excludes a competing claim until this one
			// records its lease and pending work.
			if d.DeskDocument != "" && l.cfg.Comments != nil {
				held, err := readLanes(tx.Reader(), at)
				if err != nil {
					return err
				}
				for _, owner := range works {
					if !owner.Done && !owner.LaneReleased {
						held[owner.Decision.LaneID] = true
					}
				}
				// A continuing workflow already owns this desk. Reusing that
				// reservation does not spend another slot; competing owners and
				// live leases on this desk were rejected above.
				delete(held, d.LaneID)
				if countDocumentDesks(held, nil) >= l.cfg.Comments.MaxRunning {
					return nil
				}
			}
			heldForks, err := liveForks(tx.Reader(), at)
			if err != nil {
				return err
			}
			items, err := readItems(tx.Reader())
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ItemID != d.ItemID {
					continue
				}
				fork := forkOf(item.Allowed)
				for _, held := range heldForks {
					if fork != "" && held.fork == fork && !(work != nil && (work.Ready || d.WorkflowRestart) && held.itemID == d.ItemID) {
						return nil
					}
				}
			}
			if work != nil && !work.Done && !work.Ready && !d.WorkflowRestart {
				return nil
			}
			base := mainSHA
			if !IsTrunkFixer(d.ItemID) {
				green, err := provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
				if err != nil {
					return err
				}
				if green != "" {
					states := map[string]string{}
					for _, row := range items {
						states[row.ItemID] = row.State
					}
					edges, err := readEdges(tx.Reader())
					if err != nil {
						return err
					}
					waits, err := unprovedBlockers(ctx, tx.Reader(), l.cfg.Root, green, states, edges)
					if err != nil {
						return err
					}
					if waits[d.ItemID] {
						return nil
					}
					base = green
					// Sync read the item's block at the tip. A proved commit carrying
					// another version would brief the worker on a block its tree does
					// not hold, so the lane starts from the oldest trunk commit that
					// carries it instead of waiting for a proof.
					if work == nil || !(work.Ready || d.WorkflowRestart) {
						advanced, err := blockBase(ctx, tx.Reader(), l.cfg.Root, green, d.ItemID)
						if err != nil {
							return err
						}
						if advanced != green {
							l.reportf("loop: %s's block is newer than green %s; cutting its lane from %s\n", d.ItemID, shortSHA(green), shortSHA(advanced))
						}
						base = advanced
					}
				} else if l.gatePolicy.LaneBase != "tip" && !l.laneBootstrapReported {
					l.reportf("loop: no green pointer yet; cutting lanes from trunk tip\n")
					l.laneBootstrapReported = true
				}
			}
			if work != nil && (work.Ready || d.WorkflowRestart) {
				base = work.BaseSHA
			}
			if err := correlate(tx, &d); err != nil {
				return err
			}
			owner, err := currentLoopOwner()
			if err != nil {
				return err
			}
			if d.BlockConflict != nil {
				c := *d.BlockConflict
				c.Status, c.DesignRunID = "awaiting", d.RunRequest.RunID
				d.BlockConflict = &c
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
			} else if d.ConflictID != "" {
				conflicts, err := item.ReadConflicts(tx.Reader())
				if err != nil {
					return err
				}
				c := conflicts[d.ItemID]
				if c.ConflictID != d.ConflictID || c.Status != "answered" {
					return nil
				}
				c.Status = "closed"
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
			}
			if d.Workflow != nil && d.NodeBaseSHA == "" {
				d.NodeBaseSHA = base
			}
			if err := l.claimParallel(tx, &d); err != nil {
				return err
			}
			shut, err := readQuotaShutBackends(tx.Reader(), at, l.currentPolicy())
			if err != nil {
				return err
			}
			nodes := parallelNodes(d)
			for i := range nodes {
				ok, err := selectSeatDispatch(tx, &nodes[i], l.cfg.Policy, shut, at)
				if err != nil {
					return err
				}
				if !ok {
					return errSeatWindowClosed
				}
			}
			d = nodes[0]
			d.Parallel = nodes[1:]
			if err := recordWorkflowSelection(tx, d); err != nil {
				return err
			}
			work = &pendingWork{Decision: d, BaseSHA: base, Owner: owner, LoopID: l.sessionID}
			for _, node := range parallelNodes(d) {
				if len(d.Parallel) > 0 {
					rec, err := terminalRecord(tx.Reader(), node.RunRequest.RunID)
					if err != nil {
						return err
					}
					if rec != nil {
						continue
					}
				}
				if _, err := run.ClaimLease(ctx, tx, *node.RunRequest, node.LaneID, at); err != nil {
					return err
				}
				if err := startNode(tx, node); err != nil {
					return err
				}
			}
			if err := appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, at); err != nil {
				return err
			}
		case ActionSettle, ActionJudge:
			if work == nil || work.Done {
				return nil
			}
			leases, err := activeLeases(tx.Reader(), at, []string{d.LaneID})
			if err != nil {
				return err
			}
			if len(leases) > 0 {
				return nil
			}
			if work.LaneReleased {
				for _, other := range works {
					if other != work && !other.Done && !other.LaneReleased && other.Decision.LaneID == d.LaneID {
						return nil
					}
				}
			}
			if err := correlate(tx, &d); err != nil {
				return err
			}
			if err := l.assignPinnedCandidate(tx, work, &d, at); err != nil {
				return err
			}
			owner, err := currentLoopOwner()
			if err != nil {
				return err
			}
			if d.Workflow != nil {
				work.Owner, work.Examining = owner, true
				if err := appendEffect(tx, examinationClaimedKind, d.RunRequest.RunID, owner, at); err != nil {
					return err
				}
			} else if err := appendEffect(tx, examinationOwnerKind, d.RunRequest.RunID, owner, at); err != nil {
				return err
			}
			// Settlement must retain the candidate's worktree too. A terminal record
			// released the run's lease, so reclaim it before inspecting or gating it.
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES(?,?,?,NULL) ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state,run_id=excluded.run_id,leased_until=NULL`, d.LaneID, run.LaneStateLeased, d.RunRequest.RunID); err != nil {
				return err
			}
		}
		if err := recordPassDecision(tx, d, at); err != nil {
			return err
		}
		applied = d.Action != ActionHold
		return nil
	})
	if err == errSeatWindowClosed {
		return false, nil, nil
	}
	if err == nil && applied {
		*generation = nextGeneration
	}
	return applied, work, err
}

// laneReleased is drain.lane-released's payload: the lane and why it was released.
type laneReleased struct {
	LaneID string `json:"lane_id"`
	Cause  string `json:"cause"`
}

// The causes a drain.lane-released names.
const (
	laneReleaseExamined = "examined"          // in the write that recorded the examination
	laneReleaseEnded    = "examination-ended" // by the examination's end, examined or not
	laneReleaseReap     = "reap"              // by a pass's reap: a stranded lease (stuckExaminationLease) or a dead examiner's
)

// releaseExamination releases the examination's lane once it has ended, on every path,
// unless the run's own face is alive: a canceled examination may have handed off a live
// continuation, and that face releases the lease when it actually finishes.
//
// Only the face keeps the lease. A process that merely carries the run's markers — an MCP
// server or other child the adapter left behind — is not the run, and on 2026-11-17 one
// kept a finished run's lane leased with no expiry indefinitely. Such strays are not
// signaled: EndRuns is the drain's only run-signaling function. A stray inside the lane
// instead holds it out of rotation, without keeping the store lease.
//
// The process table is read once, before the write transaction opens.
func (l *Loop) releaseExamination(ctx context.Context, d Decision) error {
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	var marked []*runFaceOwner
	for _, node := range parallelNodes(d) {
		workers, err := liveRunWorkers(l.cfg.Store, node.RunRequest.RunID, here)
		if err != nil {
			return err
		}
		marked = append(marked, workers...)
	}
	var strays []string
	for _, p := range marked {
		face, err := processIsRunFace(p.PID)
		if err != nil {
			return err
		}
		if face {
			return nil
		}
		strays = append(strays, strconv.Itoa(p.PID))
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, byRun, err := readPendingHistory(tx.Reader())
		if err != nil {
			return err
		}
		if w := byRun[d.RunRequest.RunID]; w != nil && w.Examiner != nil && *w.Examiner != *here {
			// A released candidate may already have been claimed by an operator.
			// The old examiner must not clear that same-run successor's lease.
			return nil
		}
		res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ?`, run.LaneStateIdle, d.LaneID, d.RunRequest.RunID)
		if err != nil {
			return err
		}
		if d.Workflow != nil {
			return appendEffect(tx, examinationReleasedKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now().UTC())
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		return appendEffect(tx, laneReleasedKind, d.RunRequest.RunID, laneReleased{LaneID: d.LaneID, Cause: laneReleaseEnded}, l.cfg.Now().UTC())
	})
	if err == nil && len(strays) > 0 {
		l.reportf("loop: run %s ended, but process(es) %s still carry its markers; lane %s's lease was released. Before offering it again, the next pass holds it out of rotation if any of these processes are still inside it\n", d.RunRequest.RunID, strings.Join(strays, ", "), d.LaneID)
	}
	return err
}

// processIsRunFace answers whether pid's cmdline is a run face's (isRunFaceArgv). A
// process that is already gone is not one.
func processIsRunFace(pid int) (bool, error) {
	argv, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if processGone(err) {
		return false, nil
	}
	return err == nil && isRunFaceArgv(argv), err
}

func (l *Loop) markExamined(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	// The routing reads the whole history, so it reads before the write and not under the
	// store's write lock (readSecondFailure); the write records what it read.
	var routed *secondFailure
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		routed, err = readSecondFailure(r, d)
		return err
	}); err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		at := l.cfg.Now().UTC()
		if err := appendEffect(tx, examinationDoneKind, d.RunRequest.RunID, struct{}{}, at); err != nil {
			return err
		}
		// The examination's own lease — the claim's, with no expiry — is released in the
		// write that records the examination, so no path can record it examined and leave
		// its lane leased. A run's own lease (with an expiry) is the run face's to release.
		res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, d.LaneID, d.RunRequest.RunID, run.LaneStateLeased)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n > 0 {
			if err := appendEffect(tx, laneReleasedKind, d.RunRequest.RunID, laneReleased{LaneID: d.LaneID, Cause: laneReleaseExamined}, at); err != nil {
				return err
			}
		}
		return recordSecondFailure(tx, routed, at)
	})
}

func (l *Loop) launchRun(ctx context.Context, d Decision, at time.Time, watched []string, result *LoopResult) {
	// The run outlives this supervisor. A canceled ctx ends only this goroutine's
	// wait: ProcessSteps.Run never signals the run face on cancellation, and the
	// next loop settles the run from its terminal record.
	id := d.RunRequest.RunID
	l.active[id] = true
	l.mu.Lock()
	l.laneChanges[d.LaneID]++
	epochs := map[string]int{}
	for _, lane := range watched {
		epochs[lane] = l.laneChanges[lane]
	}
	l.mu.Unlock()
	result.Dispatched++
	if d.Reason == ReasonFailover {
		l.reportf("loop: failover %s -> %s on %s\n", d.ItemID, d.RunRequest.Backend, d.LaneID)
	}
	if len(d.Reason) > len("seat-degraded") && d.Reason[:len("seat-degraded")] == "seat-degraded" {
		l.reportf("loop: %s on %s\n", d.Reason, d.LaneID)
	}
	l.reportf("loop: tick %d dispatch %s -> %s on %s\n", result.Ticks, d.ItemID, d.RunRequest.Backend, d.LaneID)
	go func() {
		start := time.Now()
		completion := runCompletion{id: id, itemID: d.ItemID, step: "run", timing: &PassTiming{TickID: d.RunRequest.CorrelationID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}}
		local := LoopResult{}
		var evaluatorHold *Candidate
		before, err := l.snapshot(ctx, []string{l.lanePath(d.LaneID)}, watched)
		if err == nil {
			if evaluatorNode(d) {
				c, candidateErr := l.evaluatorCandidate(ctx, d)
				err = candidateErr
				if err == nil {
					var accepted bool
					_, accepted, err = l.judgeCandidate(ctx, c, &local)
					if errors.Is(err, errJudgeGrantHeld) {
						err = l.releaseExamination(ctx, d)
					} else if err == nil && !accepted {
						evaluatorHold = &c
						completion.result = &local
					}
				}
			} else if len(d.Parallel) > 0 {
				err = l.runParallel(ctx, d)
			} else {
				_, _, err = l.cfg.Steps.Run(ctx, *d.RunRequest)
			}
			if err == nil {
				completion.outcome.stop, err = l.closeAsyncBracket(context.WithoutCancel(ctx), d, at, before, &local, watched, epochs)
			}
		}
		if err == nil && evaluatorHold != nil {
			err = l.finishEvaluatorHold(ctx, d, *evaluatorHold)
		}
		completion.outcome.err = err
		completion.detail = local.Detail
		if local.StopStep != "" {
			completion.step = local.StopStep
		}
		completion.timing.DispatchMS = time.Since(start).Milliseconds()
		l.completions <- completion
	}()
}

func (l *Loop) takeCompletion(c runCompletion, result *LoopResult) (string, error) {
	// The worker's durable outcome belongs to its already applied transition.
	// Index it even when no successor is dispatchable, or when shutdown is
	// consuming the completion. Empty polls never call this path.
	defer l.index()
	delete(l.active, c.id)
	if c.result != nil {
		mergeExamination(result, c.result)
		if c.result.Rejected > 0 {
			l.finalizeRepeatedRefusal(result)
			if line := RepeatedRefusalLine(*result); line != "" {
				l.reportf("loop: repeated-refusal: %s\n", line)
			}
		}
	}
	if c.timing != nil {
		if err := l.recordPass(c.timing); err != nil {
			result.StopItemID, result.StopStep = c.itemID, "record pass"
			return "", err
		}
	}
	if c.detail != "" {
		result.Detail = c.detail
	}
	if c.laneRefusal != nil && c.outcome.err == nil {
		if err := l.takeLaneOutOfRotation(context.Background(), c.laneRefusal); err != nil {
			result.StopItemID, result.StopStep = c.itemID, "lane rotation"
			return "", err
		}
	}
	if c.outcome.stop == stopLaneRetained && c.outcome.err == nil {
		// One lane's retained work is that lane's condition; its item is held.
		return "", nil
	}
	if c.outcome.stop != "" || c.outcome.err != nil {
		result.StopItemID, result.StopStep = c.itemID, c.step
		if c.result != nil && c.result.StopStep != "" {
			result.StopStep = c.result.StopStep
		}
	}
	return c.outcome.stop, c.outcome.err
}

// pass advances each item's one planned transition without waiting for a sibling.
// A fixed point waits on completion, expiry, polling, or operator cancellation.
func (l *Loop) pass(ctx context.Context, result *LoopResult) (stop string, err error) {
	if handled, stop, err := l.gateRestart(ctx, result); handled || err != nil {
		return stop, err
	}
	if !l.nonLanding {
		if err := l.prunePreservedWork(ctx); err != nil {
			return "", err
		}
	}
	if err := l.intakeComments(ctx); err != nil {
		return "", err
	}
	if err := l.confirmCommentReplies(ctx); err != nil {
		return "", err
	}
	if err := l.flushCommentDeliveries(ctx); err != nil {
		return "", err
	}
	if err := l.refreshPolicy(); err != nil {
		return "", err
	}
	for {
		select {
		case c := <-l.completions:
			if stop, err := l.takeCompletion(c, result); stop != "" || err != nil {
				return stop, err
			}
		default:
			goto planned
		}
	}
planned:
	if _, err := l.observeTrunk(ctx, ""); err != nil {
		return "", err
	}
	mainSHA, err := l.refreshDocuments(ctx, result)
	if err != nil {
		return "", err
	}
	if err := l.retireDesigners(ctx); err != nil {
		return "", err
	}
	if err := l.maybeRolling(ctx, mainSHA, false, result); err != nil {
		return "", err
	}
	if err := l.releaseUnlaunchedClaims(ctx); err != nil {
		return "", err
	}
	if !l.nonLanding {
		if err := l.releaseTrunkAcquittals(ctx); err != nil {
			return "", err
		}
		if err := l.releaseStaleBaseConflicts(ctx); err != nil {
			return "", err
		}
		if err := l.settleUnansweredDesignRuns(ctx); err != nil {
			return "", err
		}
		if err := l.settleMisreadDesignAnswers(ctx); err != nil {
			return "", err
		}
	}
	at := l.cfg.Now().UTC()
	timing := &PassTiming{AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	defer func() {
		if timing.TickID != "" {
			result.Ticks++
			if result.FirstGeneration == "" {
				result.FirstGeneration = strings.TrimPrefix(timing.TickID, "gen-")
			}
			result.LastGeneration = strings.TrimPrefix(timing.TickID, "gen-")
			l.index()
		}
	}()
	plans, leases, err := l.passPlansGeneration(ctx, at, &timing.TickID)
	if err != nil {
		return "", err
	}
	// Resolve batches that disappeared before applying this snapshot. Closing a
	// comment item changes dispatch eligibility for its document's build items,
	// so take a fresh snapshot in this same pass, not at the next wake.
	commentReady := map[string]bool{}
	for {
		closedBatch := false
		for _, p := range plans {
			d := p.decision
			if !p.checksComments() || !item.IsCommentItem(d.ItemID) || l.active[p.parentRunID] ||
				(d.Action != ActionDispatch && d.Action != ActionRetry && d.Action != ActionHold) {
				continue
			}
			ready, closed, checkErr := l.checkCommentDispatch(ctx, p, at)
			if checkErr != nil {
				return "", checkErr
			}
			commentReady[d.ItemID] = ready
			closedBatch = closedBatch || closed
		}
		if !closedBatch {
			break
		}
		plans, leases, err = l.passPlansGeneration(ctx, at, &timing.TickID)
		if err != nil {
			return "", err
		}
	}
	answers, counts, err := passAnswers(plans)
	if err != nil {
		return "", err
	}
	timing.DecisionCounts = counts
	defer func() {
		if timing.TickID == "" {
			return
		}
		if recordErr := l.observePass(timing, answers); recordErr != nil && err == nil {
			err = recordErr
		}
	}()
	var holds []Decision
	// Watch currently idle offered lanes. A sibling legitimately started later
	// is removed from both status comparisons when this run's bracket closes.
	watched := []string{}
	busy := map[string]bool{}
	for _, p := range plans {
		if p.decision.RunRequest != nil {
			busy[p.decision.LaneID] = true
		}
	}
	for _, lane := range l.cfg.Lanes {
		// A refused lane may have no worktree here to snapshot, even after a
		// re-check offers it again or changes the reason before recreating it.
		// A holding worktree elsewhere is not a sibling this bracket watches.
		if c := l.laneConditions[lane]; c != nil {
			if _, err := os.Stat(l.lanePath(lane)); os.IsNotExist(err) {
				continue
			}
		}
		if !busy[lane] {
			watched = append(watched, lane)
		}
	}
	stale := false
	// A staging refusal that took its lane out of rotation re-plans the item in this same
	// pass, so it is offered the next free lane; each re-plan removes a lane, so this is
	// bounded by the lane count.
	replans := 0
plans:
	for i := 0; i < len(plans); i++ {
		p := plans[i]
		d := p.decision
		d.PassID = timing.TickID
		p.decision = d
		if d.Action == ActionHold {
			holds = append(holds, d)
		}
		if l.active[p.parentRunID] || ((d.Action == ActionSettle || d.Action == ActionJudge) && l.active[d.RunRequest.RunID]) {
			continue
		}
		if p.checksComments() && item.IsCommentItem(d.ItemID) && (d.Action == ActionDispatch || d.Action == ActionRetry || d.Action == ActionHold) {
			if !commentReady[d.ItemID] {
				d.Action = ActionHold
				d.Reason = ReasonVantageUnavailable
				holds = append(holds, d)
				continue
			}
		}
		ok, work, err := l.applyPlanGeneration(ctx, p, mainSHA, at, &timing.TickID)
		if err != nil {
			result.StopItemID, result.StopStep = d.ItemID, "apply plan"
			return "", err
		}
		if !ok {
			if d.Action != ActionHold {
				stale = true
			}
			continue
		}
		timing.Applied++
		if d.RunRequest != nil {
			request := *d.RunRequest
			request.CorrelationID = timing.TickID
			d.RunRequest = &request
		}
		switch d.Action {
		case ActionDispatch, ActionRetry:
			start := time.Now()
			base := mainSHA
			if work != nil {
				base = work.BaseSHA
			}
			if work != nil {
				d = work.Decision
			}
			l.laneRefusedNow = false
			var staged []Decision
			var err error
			owned := ""
			if len(d.Parallel) > 0 && d.WorkflowRestart {
				owned, err = l.stagedNodeRun(d)
				if err != nil {
					return "", err
				}
			}
			if owned != "" && len(d.Parallel) > 0 && d.WorkflowRestart {
				if d.NodeIndex == 0 && d.NodeStartRunID == "" {
					raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, owned), BaseSHAFile))
					if err != nil {
						return "", err
					}
					d.NodeBaseSHA = strings.TrimSpace(string(raw))
					d.NodeStartRunID = owned
				}
				if stagedBase := l.laneBase(d.LaneID); stagedBase != "" {
					base = stagedBase
				}
				head, err := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
				if err != nil {
					return "", err
				}
				dirty, err := l.git(ctx, l.laneTarget(d.LaneID), "status", "--porcelain", "--untracked-files=all")
				if err != nil {
					return "", err
				}
				if head != d.NodeBaseSHA || strings.TrimSpace(dirty) != "" {
					if err = l.refuseParallelBoundary(ctx, d, base, head, result, true); err != nil {
						return "", err
					}
					continue
				}
				staged = []Decision{d}
			} else {
				staged, err = l.stageDispatches(ctx, []Decision{d}, base, result)
			}
			if err != nil {
				result.StopItemID, result.StopStep = d.ItemID, "stage"
				return "", err
			}
			timing.StageMS += time.Since(start).Milliseconds()
			if len(staged) == 0 {
				if err := l.cancelStaging(ctx, d); err != nil {
					result.StopItemID, result.StopStep = d.ItemID, "cancel staging"
					return "", err
				}
				if l.laneRefusedNow && replans < len(l.cfg.Lanes) {
					replans++
					fresh, freshLeases, err := l.passPlansGeneration(ctx, at, &timing.TickID)
					if err != nil {
						return "", err
					}
					leases = freshLeases
					for _, next := range fresh {
						if next.decision.ItemID == d.ItemID && (next.decision.Action == ActionDispatch || next.decision.Action == ActionRetry) {
							// Offer it again, now, on the lane the fresh plan chose. The
							// rest of this pass's plans stay as they were: one that
							// conflicts with the re-plan is refused as stale when applied.
							plans = append(plans[:i+1], append([]plannedApplication{next}, plans[i+1:]...)...)
							continue plans
						}
					}
				}
				continue
			}
			parallel, err := l.stageParallel(ctx, staged[0], base)
			if err != nil {
				return "", err
			}
			l.launchRun(ctx, parallel, at, watched, result)
		case ActionSettle, ActionJudge:
			l.launchExamination(ctx, d, work, at, timing, watched)
		}
	}
	if timing.Applied > 0 || timing.TickID != "" {
		l.waitingDetail = ""
		return "", nil
	}
	if len(l.active) > 0 {
		l.waitingDetail = ""
		return l.waitForTrigger(ctx, leases, at, result)
	}
	for _, hold := range holds {
		if hold.RunRequest != nil && hold.From == "running" && hold.Reason == ReasonBackendWindow {
			paused, err := l.quotaInterrupted(ctx, hold.RunRequest.RunID)
			if err != nil {
				return "", err
			}
			if paused {
				return l.waitForTrigger(ctx, leases, at, result)
			}
		}
		if hold.RunRequest != nil && (hold.Reason == ReasonRunLive || hold.Reason == ReasonNoLane) {
			// A replacement has no local completion channel for surviving runs.
			// Their durable claims are still work, not foreign leased lanes.
			return l.waitForTrigger(ctx, leases, at, result)
		}
	}
	if stale {
		return "", nil
	}
	before := l.rollingRuns(result)
	if err := l.maybeRolling(ctx, mainSHA, true, result); err != nil {
		return "", err
	}
	if l.rollingRuns(result) != before {
		return "", nil
	}
	if l.heavyInFlight() {
		return l.waitForTrigger(ctx, leases, at, result)
	}
	return l.planNothing(ctx, holds, leases, at, result)
}

// launchExamination runs the claimed settlement or judgment independently;
// the completion is a wake trigger and measurement, not its durable outcome.
func (l *Loop) launchExamination(ctx context.Context, d Decision, work *pendingWork, at time.Time, passTiming *PassTiming, watched []string) {
	l.active[d.RunRequest.RunID] = true
	l.examinations.Add(1)
	timing := PassTiming{TickID: passTiming.TickID, AtMS: passTiming.AtMS, GateBudgetMS: passTiming.GateBudgetMS}
	go func() {
		defer l.examinations.Done()
		local := &LoopResult{}
		completion := runCompletion{id: d.RunRequest.RunID, itemID: d.ItemID, step: d.Action, result: local, timing: &timing}
		if work.NeedsStage {
			if err := l.stagePinnedCandidate(ctx, work); err != nil {
				if refusal := asLaneRefusal(err); refusal != nil {
					// The lane's own state refused the pinned candidate: the lane leaves
					// rotation and the candidate, still pinned, is released to be offered
					// another lane. One lane's condition never stops the loop.
					completion.laneRefusal = refusal
					err = l.releaseRefusedAssignment(ctx, work)
				}
				completion.outcome.err = err
			}
		}
		if completion.outcome.err != nil || completion.laneRefusal != nil {
			// The claimed lane remains pending so a replacement retries staging.
		} else if d.Reason == ReasonItemAlreadyTerminal {
			// The plan read the terminal state and the claim's version check rebound it;
			// the settling write rereads it for the record.
			completion.outcome.err = l.settleTerminalItem(ctx, work, &itemTerminalError{ItemID: d.ItemID, Step: "plan"})
		} else if d.WorkflowRelease {
			owned, err := l.stagedNodeRun(work.Decision)
			if err == nil && (owned != "" || work.Decision.NodeIndex > 0) {
				// The run pin is the release's retained head. A crash after
				// reset but before markExamined must never replace it with
				// the reset trunk head on replay.
				pinned, pinErr := l.git(ctx, l.cfg.Root, "for-each-ref", "--format=%(objectname)", "refs/tillr/runs/"+d.RunRequest.RunID)
				err = pinErr
				if err == nil && strings.TrimSpace(pinned) == "" {
					head, headErr := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
					err = headErr
					if err == nil {
						err = l.pin(ctx, d.RunRequest.RunID, head)
					}
				}
				if err == nil {
					err = l.preserveWork(ctx, d.LaneID, d.RunRequest.RunID, "orphaned workflow released")
					if err == nil {
						err = l.resetLane(ctx, d.LaneID)
					}
				}
			}
			if err == nil {
				err = l.markExamined(ctx, work.Decision)
			}
			completion.outcome.err = err
		} else if d.Action == ActionSettle && len(work.Decision.Parallel) > 0 {
			completion.outcome = l.settleParallel(ctx, work, local, &timing, watched)
		} else if d.Action == ActionSettle {
			s, err := store.OpenRead(ctx, l.cfg.Store)
			var rec *run.Record
			if err == nil {
				err = s.ReadTx(ctx, func(r *store.Reader) error {
					var err error
					rec, err = terminalRecord(r, d.RunRequest.RunID)
					return err
				})
				s.Close()
			}
			if err == nil && rec == nil {
				err = fmt.Errorf("run %s lost its terminal record", d.RunRequest.RunID)
			}
			if err == nil {
				err = l.recordSettlementSpend(ctx, timing.TickID, work.LoopID, *rec)
			}
			if err == nil {
				l.countSettledSpend(local, *rec, work.LoopID)
				log := logTail(rec.ArtifactRefs)
				quotaPaused := false
				if quotaSingleRun(d) {
					quotaPaused, err = l.quotaInterrupted(ctx, rec.RunID)
					if err != nil {
						completion.outcome.err = err
					}
				}
				if rec.State == run.StatePaused && !quotaPaused && completion.outcome.err == nil {
					resumed, resumedLog, stop, resumeErr := l.resumeAfterWall(ctx, d.LaneID, *d.RunRequest, l.lanePath(d.LaneID), *rec, d.RunRequest.CorrelationID, at, local, watched)
					if resumeErr != nil || stop != "" {
						completion.outcome = laneOutcome{stop: stop, err: resumeErr}
					} else if resumed.RunID == "" {
						completion.outcome.err = Externalf("paused run %s cannot continue: its lane needs the staged brief and session-id", rec.RunID)
					} else {
						rec, log = &resumed, resumedLog
					}
				}
				if completion.outcome.err == nil && completion.outcome.stop == "" {
					completion.outcome.err = l.recordSettlementSpend(ctx, timing.TickID, work.LoopID, *rec)
				}
				if completion.outcome.err == nil && completion.outcome.stop == "" {
					completion.outcome = l.settle(ctx, work.Decision, *rec, log, work.BaseSHA, d.RunRequest.CorrelationID, at, local, &timing, watched, true)
				}
				if terminal := asItemTerminal(completion.outcome.err); terminal != nil {
					completion.outcome = laneOutcome{err: l.settleTerminalItem(ctx, work, terminal)}
				}
			} else {
				completion.outcome.err = err
			}
		} else {
			c := *work.Candidate
			c.TickID = timing.TickID
			var stop string
			var landed bool
			var err error
			if work.RepairHold != nil {
				// Retry the retained repair, not another gate and land of the same
				// original candidate. The original conflict remains the evidence.
				stop, landed, err = l.conflictFix(ctx, c, work.RepairHold.Conflict, local, &timing)
			} else if work.DirtyHold != nil || work.Gated {
				stop, landed, err = l.landCandidate(ctx, c, local, &timing)
			} else {
				stop, landed, err = l.verify(ctx, &c, local, &timing)
			}
			if terminal := asItemTerminal(err); terminal != nil {
				// The item went terminal by another route during this examination.
				completion.outcome = laneOutcome{err: l.settleTerminalItem(ctx, work, terminal)}
			} else {
				held, keep := retainLane(err)
				itemFault := errors.Is(err, errItemFaultHeld)
				repairHeld := errors.Is(err, errRepairHeld)
				if held {
					err = nil
					if !keep {
						copy := *work
						copy.Candidate = &c
						reason := ""
						if repairHeld {
							reason = ReasonRepairStagingFailed
						}
						// A candidate mismatch must retain the unexpected checkout.
						if itemFault {
							if head, headErr := l.revParse(ctx, c.Worktree, "HEAD"); headErr != nil || head != c.HeadSHA {
								reason = ReasonCandidateMismatch
							}
						}
						_, err = l.releaseCandidate(ctx, &copy, reason, true)
					}
				}
				if err != nil || stop != "" {
					l.pinQuietly(ctx, c.RunID, c.HeadSHA)
				}
				if err == nil && stop == "" && !held {
					if landed {
						stop = l.abandon(ctx, c.LaneID, "", "")
						err = l.prunePreservedWork(ctx)
					} else {
						stop = l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA)
					}
					if stop == "" && err == nil {
						err = l.markExamined(ctx, work.Decision)
					}
				}
				completion.outcome = laneOutcome{stop, err}
			}
		}
		// A retained lane keeps its lease: releasing it would let the next staging clear the
		// work that could not be preserved.
		if completion.outcome.stop != stopLaneRetained {
			if err := l.releaseExamination(context.WithoutCancel(ctx), d); err != nil && completion.outcome.err == nil {
				completion.outcome.err = err
			}
		}
		completion.detail = local.Detail
		l.completions <- completion
	}()
}

// Results cross the completion channel only after the worker stops writing them.
// The pass alone accumulates them, so running and judging different lanes never
// race on the loop's result or require the planning goroutine to wait for a sibling.
func mergeExamination(dst, src *LoopResult) {
	dst.Dispatched += src.Dispatched
	dst.Landed += src.Landed
	dst.TrunkAcquitted += src.TrunkAcquitted
	dst.ConflictFixes += src.ConflictFixes
	dst.ConflictExhausted += src.ConflictExhausted
	dst.Rejected += src.Rejected
	dst.Candidates += src.Candidates
	dst.InfraFailures += src.InfraFailures
	dst.StartedSpend.merge(src.StartedSpend)
	dst.CarriedSpend.merge(src.CarriedSpend)
	dst.SpendUSD += src.SpendUSD
	dst.Tokens += src.Tokens
	dst.SpendUnknown += src.SpendUnknown
	dst.CostUnknown += src.CostUnknown
	if dst.SpendUnknownExample == "" {
		dst.SpendUnknownExample = src.SpendUnknownExample
	}
	if dst.CostUnknownExample == "" {
		dst.CostUnknownExample = src.CostUnknownExample
	}
	mergeSpendRecords(dst, src)
	if dst.RefusedSteps == nil {
		dst.RefusedSteps = map[string]int{}
	}
	for step, count := range src.RefusedSteps {
		dst.RefusedSteps[step] += count
	}
	for _, id := range src.RefusedItems {
		seen := false
		for _, existing := range dst.RefusedItems {
			if id == existing {
				seen = true
				break
			}
		}
		if !seen {
			dst.RefusedItems = append(dst.RefusedItems, id)
		}
	}
}

// liveForks includes unfinished examinations and live leases, not only this
// plan's dispatches. Allocate events retain the item link for foreign runs before
// their terminal runs row exists. Examination keeps the fork during lease-free gaps.
func liveForks(r *store.Reader, at time.Time) ([]plannedFork, error) {
	held, err := readLanes(r, at)
	if err != nil {
		return nil, err
	}
	items, err := readItems(r)
	if err != nil {
		return nil, err
	}
	forks := map[string]string{}
	for _, item := range items {
		forks[item.ItemID] = forkOf(item.Allowed)
	}
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	owners := map[string]struct{ run, item string }{}
	for _, e := range events {
		if e.Kind != run.AllocateKind {
			continue
		}
		var claim struct {
			LaneID string `json:"lane_id"`
			RunID  string `json:"run_id"`
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(e.Payload, &claim); err != nil {
			return nil, err
		}
		owners[claim.LaneID] = struct{ run, item string }{claim.RunID, claim.ItemID}
	}
	var out []plannedFork
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	for id, work := range works {
		if !work.Done && forks[id] != "" {
			out = append(out, plannedFork{id, forks[id]})
		}
	}
	for lane, owner := range owners {
		if !held[lane] {
			continue
		}
		var current sql.NullString
		if err := r.QueryRow(`SELECT run_id FROM lanes WHERE lane_id = ?`, lane).Scan(&current); err != nil {
			return nil, err
		}
		if current.Valid && current.String == owner.run && forks[owner.item] != "" {
			out = append(out, plannedFork{owner.item, forks[owner.item]})
		}
	}
	return out, nil
}

// A sibling started after the bracket opened has its own writer and bracket.
// Ignore its status in both snapshots rather than convicting that authorized work.
func (l *Loop) closeAsyncBracket(ctx context.Context, d Decision, at time.Time, before *Boundary, result *LoopResult, watched []string, epochs map[string]int) (string, error) {
	worktree := l.lanePath(d.LaneID)
	after, err := l.snapshot(ctx, []string{worktree}, watched)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	for _, lane := range watched {
		if epochs[lane] == l.laneChanges[lane] {
			continue
		}
		path := l.lanePath(lane)
		for _, snapshot := range []*Boundary{before, after} {
			for i := range snapshot.Worktrees {
				if snapshot.Worktrees[i].Path == path {
					snapshot.Worktrees[i].Status = nil
				}
			}
		}
	}
	l.mu.Unlock()
	return l.finishRunBracket(ctx, worktree, *d.RunRequest, d.RunRequest.CorrelationID, at, before, after, result)
}
