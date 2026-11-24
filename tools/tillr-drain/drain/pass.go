// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
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
	designQuotaRefusedKind  = "drain.design-quota-refused"
	examinationOwnerKind    = "drain.examination.owner"
	loopOrphanedKind        = "drain.orphaned"
	// laneReleasedKind records a lane released after its run was examined, for every run,
	// not only a workflow node (whose drain.examination-released also clears its claim).
	laneReleasedKind = "drain.lane-released"
	ActionSettle     = "settle"
	ActionJudge      = "judge"
)

func init() {
	for _, kind := range []string{backendProbeKind, backendOutcomeKind, quotaUpdatedKind, commentReplyStateKind, designerCompactedKind, examinationClaimedKind, examinationReleasedKind, dispatchStartedKind, candidateReadyKind, candidateRetirementKind, candidatePreparedKind, examinationDoneKind, designQuotaRefusedKind, examinationOwnerKind, loopOrphanedKind, laneReleasedKind, nodeAdvancedKind, graphChangedKind, candidateGatedKind, planQuickWayKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

// resumedSegment is set only by the validated, deduplicated run.resume fold.
// The runs row still describes the previous finish until this segment ends.
type resumedSegment struct {
	seq                 int64
	at                  time.Time
	itemID, runID, lane string
}

type pendingWork struct {
	resumes        map[string]resumedSegment
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
	Optimistic     bool           `json:"-"` // shadow claim, not folded from the store
	LaneKeepReason string         `json:"lane_keep_reason,omitempty"`
	HeldFence      string         `json:"held_fence,omitempty"`
	HeldStage      *heldLaneStage `json:"held_stage,omitempty"`
	NeedsStage     bool           `json:"needs_stage,omitempty"`
	ReleasePending string         `json:"release_pending,omitempty"`

	ResumeAfterRestart   bool      `json:"-"`
	DesignQuotaBackend   string    `json:"-"`
	examinationStartedAt time.Time `json:"-"`
}

type orphanedRunEffect struct {
	ResumeAfterRestart bool `json:"resume_after_restart,omitempty"`
}

func readPending(r *store.Reader) (map[string]*pendingWork, error) {
	works, _, err := readPendingHistory(r)
	return works, err
}

// pendingHistoryKinds are the kinds readPendingHistory folds, and the only ones it reads: it
// runs inside write transactions, where a read of the whole log (mostly worker records) held
// the write lock past a landing's busy timeout on 2026-11-19 (whole_log_test.go).
// TestEveryFoldReadsTheKindsItSelects holds this list to the fold's cases.
var pendingHistoryKinds = []string{
	dispatchStartedKind, nodeAdvancedKind, graphChangedKind,
	GateVerdictKind,
	"land.landing",
	run.ResumeKind,
	examinationClaimedKind,
	examinationReleasedKind,
	candidateReadyKind, candidateRetirementKind, candidatePreparedKind,
	judgeLandedKind,
	candidateGatedKind,
	BoundaryCheckKind,
	loopOrphanedKind,
	redCapHeldKind,
	dirtyHeldKind,
	gateStallHeldKind,
	repairHeldKind,
	itemFaultClearedKind,
	examinationOwnerKind,
	candidateReleaseStartedKind, candidateLaneKind,
	candidateAssignedKind,
	candidateStagedKind,
	designQuotaRefusedKind,
	examinationDoneKind,
	candidateDiscardedKind,
}

// pendingFolds counts full pending-history folds in this process: observation
// for tests that keep an application's fold in its read phase.
var pendingFolds atomic.Int64

// pendingHistoryFoldHook lets contention tests pause the expensive history fold.
var pendingHistoryFoldHook func()

// pendingDeltaFoldHook observes the fold itself, including incremental reads.
var pendingDeltaFoldHook func()

// pendingHistoryHead marks the event sequence covered by a read-phase fold.
// Each per-kind maximum is an indexed lookup.
func pendingHistoryHead(r *store.Reader) (int64, error) {
	queries := make([]string, len(pendingHistoryKinds))
	args := make([]any, len(pendingHistoryKinds))
	for i, kind := range pendingHistoryKinds {
		queries[i] = "SELECT MAX(seq) AS head FROM events WHERE kind=?"
		args[i] = kind
	}
	var head int64
	err := r.QueryRow("SELECT COALESCE(MAX(head),0) FROM ("+strings.Join(queries, " UNION ALL ")+")", args...).Scan(&head)
	return head, err
}

// pendingHistoryChanged checks only the delta since the read-phase fold. A clean
// boundary check is an observation: the fold changes state only for a failed check.
// Other folded events may change ownership, so a claim cannot proceed on stale history.
func pendingHistoryChanged(r *store.Reader, after int64) (bool, error) {
	args := make([]any, 0, len(pendingHistoryKinds)+2)
	args = append(args, after)
	for _, kind := range pendingHistoryKinds {
		args = append(args, kind)
	}
	args = append(args, BoundaryCheckKind)
	kinds := strings.TrimSuffix(strings.Repeat("?,", len(pendingHistoryKinds)), ",")
	var changed bool
	err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE seq>? AND kind IN (`+kinds+`)
		AND CASE WHEN kind=? THEN json_type(payload, '$.clean') IS NOT 'true' ELSE 1 END)`, args...).Scan(&changed)
	return changed, err
}

// Keep every attempt's effective examination state, including superseded dispatches.
// Second-failure routing needs the same continuation semantics as current planning.
func readPendingHistory(r *store.Reader) (map[string]*pendingWork, map[string]*pendingWork, error) {
	state, err := readPendingHistoryState(r)
	if err != nil {
		return nil, nil, err
	}
	return state.works, state.byRun, nil
}

type pendingHistoryState struct {
	works, byRun                map[string]*pendingWork
	judged, segments, discarded map[string]bool
}

func newPendingHistoryState() *pendingHistoryState {
	return &pendingHistoryState{
		works: map[string]*pendingWork{}, byRun: map[string]*pendingWork{},
		judged: map[string]bool{}, segments: map[string]bool{}, discarded: map[string]bool{},
	}
}

func readPendingHistoryState(r *store.Reader) (*pendingHistoryState, error) {
	pendingFolds.Add(1)
	if pendingHistoryFoldHook != nil {
		pendingHistoryFoldHook()
	}
	return readPendingHistoryStateCached(r)
}

// foldPendingHistory extends a private state with ordered events, or reads the
// relevant kinds after seq if no events were supplied.
func foldPendingHistory(r *store.Reader, seq int64, s *pendingHistoryState, events []event.Event) error {
	return foldPendingHistoryWithMeta(r, seq, s, events, nil)
}

type pendingFoldMeta struct {
	cow   bool
	state *pendingHistoryState
	last  event.Event
	count int
}

func foldPendingHistoryWithMeta(r *store.Reader, seq int64, s *pendingHistoryState, events []event.Event, meta *pendingFoldMeta) error {
	if pendingDeltaFoldHook != nil {
		pendingDeltaFoldHook()
	}
	if events == nil {
		var err error
		events, err = r.EventsOfKind(seq, pendingHistoryKinds...)
		if err != nil {
			return err
		}
	}
	if len(events) == 0 {
		if meta != nil {
			meta.state = s
		}
		return nil
	}
	if meta != nil && meta.cow {
		s = clonePendingHistoryForDelta(s, events)
	}
	pendingEventsFolded.Add(int64(len(events)))
	works, byRun, judged, segments, discarded := s.works, s.byRun, s.judged, s.segments, s.discarded
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case dispatchStartedKind, nodeAdvancedKind, graphChangedKind:
			var work pendingWork
			if err := json.Unmarshal(e.Payload, &work); err != nil {
				return err
			}
			if work.Examining || work.Examiner != nil {
				work.examinationStartedAt = e.At
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
						return err
					}
					runRes := classifyCheck(checkresult.Observation{
						Source: checkresult.ResultWord,
						Word:   verdict.Result,
					})
					if runRes.Outcome == checkresult.Unknown && runRes.Detail == checkresult.DetailStalled {
						for _, check := range verdict.Checks {
							res := classifyCheck(checkresult.Observation{
								Check:  check.CheckID,
								Source: checkresult.ResultWord,
								Word:   check.Result,
							})
							if res.Outcome == checkresult.Unknown && res.Detail == checkresult.DetailStalled {
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
				return err
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
				return err
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
			if work.resumes == nil {
				work.resumes = map[string]resumedSegment{}
			}
			work.resumes[req.RunID] = resumedSegment{seq: e.Seq, at: e.At, itemID: segment.ItemID, runID: segment.RunID, lane: segment.LaneID}
			resumingOwner := work.Examiner
			if resumingOwner == nil {
				resumingOwner = work.Owner
			}
			if work.Done {
				// Standalone resume after examination settled stays unowned.
				resumingOwner = nil
			}
			work.Done, work.Orphaned, work.Examining, work.ResumeAfterRestart = false, false, false, false
			work.Owner, work.Examiner, work.Candidate, work.DirtyHold, work.RepairHold, work.GateStall = resumingOwner, nil, nil, nil, nil, nil
			work.examinationStartedAt = time.Time{}
		case examinationClaimedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				if err := json.Unmarshal(e.Payload, &work.Owner); err != nil {
					return err
				}
				work.Examining = true
				work.examinationStartedAt = e.At
			}
		case examinationReleasedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Examining = false
				if work.Examiner == nil {
					work.examinationStartedAt = time.Time{}
				}
			}
			for _, work := range works {
				if work.Ready && work.ParentRunID == *e.ObjectID {
					work.Examining = false
					if work.Examiner == nil {
						work.examinationStartedAt = time.Time{}
					}
				}
			}
		case candidateReadyKind, candidateRetirementKind, candidatePreparedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return err
				}
				c.Request = work.Decision.RunRequest
				c.Seat = work.Decision.Seat
				work.Candidate = &c
			}
		case judgeLandedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return err
				}
				c.Request, c.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.Candidate, work.Gated, work.JudgeLanded = &c, true, true
				work.DirtyHold, work.RedCapHold, work.RepairHold = nil, nil, nil
			}
		case candidateGatedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return err
				}
				c.Request, c.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.Candidate, work.Gated = &c, true
			}
		case BoundaryCheckKind:
			var check boundaryCheckPayload
			if err := json.Unmarshal(e.Payload, &check); err != nil {
				return err
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
				var interrupted orphanedRunEffect
				if err := json.Unmarshal(e.Payload, &interrupted); err != nil {
					return err
				}
				work.Orphaned, work.ResumeAfterRestart = true, interrupted.ResumeAfterRestart
			}
		case redCapHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold redCapHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return err
				}
				hold.Candidate.Request, hold.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.RedCapHold, work.Candidate, work.Gated = &hold, &hold.Candidate, true
			}
		case candidateDiscardedKind:
			// A discarded candidate's holds speak no more, including a replayed one.
			discarded[*e.ObjectID] = true
			if work := byRun[*e.ObjectID]; work != nil {
				work.DirtyHold, work.RedCapHold, work.RepairHold, work.GateStall = nil, nil, nil, nil
			}
		case dirtyHeldKind:
			if work := byRun[*e.ObjectID]; work != nil && !discarded[*e.ObjectID] {
				var hold dirtyHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return err
				}
				work.DirtyHold = &hold
				work.Candidate = &hold.Candidate
			}
		case gateStallHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold gateStallHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return err
				}
				hold.Candidate.Request, hold.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.GateStall, work.Candidate = &hold, &hold.Candidate
			}
		case repairHeldKind:
			if work := byRun[*e.ObjectID]; work != nil && !discarded[*e.ObjectID] {
				var hold repairHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return err
				}
				work.RepairHold = &hold
			}
		case itemFaultClearedKind:
			var fault ItemFault
			if err := json.Unmarshal(e.Payload, &fault); err != nil {
				return err
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
					return err
				}
				if work.Examiner != nil {
					work.examinationStartedAt = e.At
				} else if !work.Examining {
					work.examinationStartedAt = time.Time{}
				}
			}
		case candidateReleaseStartedKind, candidateLaneKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var lane candidateLane
				if err := json.Unmarshal(e.Payload, &lane); err != nil {
					return err
				}
				lane.Candidate.Request, lane.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				work.Candidate = &lane.Candidate
				work.LaneReleased, work.LaneKeepReason, work.HeldFence, work.HeldStage = lane.Released, lane.Reason, lane.Fence, lane.Stage
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
					return err
				}
				if work.Candidate != nil {
					work.Candidate.Request, work.Candidate.Seat = work.Decision.RunRequest, work.Decision.Seat
				}
				if work.Examining || work.Examiner != nil {
					if work.examinationStartedAt.IsZero() {
						work.examinationStartedAt = e.At
					}
				} else {
					work.examinationStartedAt = time.Time{}
				}
			}
		case candidateStagedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.NeedsStage = false
			}
		case designQuotaRefusedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				if err := json.Unmarshal(e.Payload, &work.DesignQuotaBackend); err != nil {
					return err
				}
			}
		case examinationDoneKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Done = true
				work.examinationStartedAt = time.Time{}
			}
		}
	}
	if meta != nil {
		meta.state = s
		meta.last = events[len(events)-1]
		meta.count = len(events)
	}
	return nil
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
		status.Run.EndingException = terminal.EndingException
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return status.Run, nil
}

// liveContinuation requires both a newer segment and that segment's own active
// lease. An examination can also lease a lane under the run's ID, but only its
// finished segment is on record. A reaped continuation has no lease, so the
// previous finish can again authorize settlement and recovery.
func liveContinuation(r *store.Reader, segment resumedSegment, d Decision, at time.Time) (bool, error) {
	if d.RunRequest == nil || segment.seq == 0 || segment.itemID != d.ItemID || segment.runID != d.RunRequest.RunID || segment.lane != d.LaneID {
		return false, nil
	}
	leased, err := activeWorkerLease(r, d.RunRequest, d.LaneID, at)
	if err != nil || !leased {
		return false, err
	}
	var live bool
	err = r.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? AND seq > ?)`,
		run.FinishKind, segment.runID, segment.seq).Scan(&live)
	return live, err
}

// The lane is leased to the group, not to any one reader. A settled reader
// cannot borrow an active sibling's lease to appear still running.
func activeWorkerLease(r *store.Reader, req *run.Request, lane string, at time.Time) (bool, error) {
	var active bool
	if req.ReadOnlyGroup != "" {
		err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes l JOIN read_only_runs m ON m.group_id = l.run_id AND m.lane_id = l.lane_id
		 WHERE l.lane_id = ? AND l.state = ? AND l.run_id = ? AND m.run_id = ? AND m.item_id = ? AND m.settled = 0)`,
			lane, run.LaneStateLeased, req.ReadOnlyGroup, req.RunID, req.ItemID).Scan(&active)
		return active, err
	}
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes WHERE lane_id = ? AND run_id = ? AND state = ? AND (leased_until IS NULL OR leased_until > ?))`,
		lane, req.RunID, run.LaneStateLeased, at.UnixMilli()).Scan(&active)
	return active, err
}

// A claim can lease the lane before the evaluator starts. A live first segment
// needs its own lease, a run face for the same store, and no recorded finish.
func liveFirstSegmentWorker(r *store.Reader, work *pendingWork, at time.Time, faces map[string]bool) (bool, error) {
	if !mayHaveFirstSegmentWorker(work) {
		return false, nil
	}
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest == nil || !faces[node.RunRequest.RunID] {
			continue
		}
		leased, err := activeWorkerLease(r, node.RunRequest, node.LaneID, at)
		if err != nil {
			return false, err
		}
		if !leased {
			continue
		}
		rec, err := terminalRecord(r, node.RunRequest.RunID)
		if err != nil {
			return false, err
		}
		if rec == nil {
			return true, nil
		}
	}
	return false, nil
}

func mayHaveFirstSegmentWorker(work *pendingWork) bool {
	return !work.Done && !work.Ready && !work.Examining && work.Candidate == nil && !work.BoundaryFailed && !work.LaneReleased
}

// Only waits and evaluator handoffs can mask a live first segment. The loop
// and Why collect this evidence before planning, once per observation.
func observeFirstSegmentFaces(r *store.Reader, path string, tick Pass, works map[string]*pendingWork) (live, faces map[string]bool, err error) {
	if path == "" {
		return nil, nil, nil
	}
	for id, work := range works {
		if !tick.plansItem(id, work) || !mayHaveFirstSegmentWorker(work) {
			continue
		}
		if !evaluatorNode(work.Decision) {
			wait, _, err := item.ReadWaitingOn(r, id)
			if err != nil {
				return nil, nil, readFailure(err)
			}
			if wait == nil || wait.Waiter == "" {
				continue
			}
		}
		live, faces, err := liveRunProcessIDs(path)
		if err != nil {
			return nil, nil, readFailure(err)
		}
		return live, faces, nil
	}
	return nil, nil, nil
}

// refreshRestartRequest rebuilds a restarted run's request from the policy in
// force for its recorded backend so a policy model/provider change takes effect
// instead of replaying a retired model and closing the backend. Session-resume
// flags are kept only while the route is unchanged.
func refreshRestartRequest(policy *Policy, d Decision, prev *run.Request) (*run.Request, bool) {
	if policy == nil || prev == nil || !policyHasBackend(policy, prev.Backend) {
		return nil, false
	}
	fresh := policy.request(prev.ItemID, prev.LaneID, prev.RunID, prev.Backend)
	fresh.ReadOnlyGroup = prev.ReadOnlyGroup
	if prev.WorkingDir != "" {
		fresh.WorkingDir = prev.WorkingDir
	}
	if d.Workflow != nil && d.NodeIndex >= 0 && d.NodeIndex < len(d.Workflow.Stages) {
		stage := d.Workflow.Stages[d.NodeIndex]
		fresh.NodeID = stage.ID
		if stage.TimeoutMS > 0 {
			fresh.TimeoutMS = stage.TimeoutMS
		}
	} else if prev.NodeID != "" {
		fresh.NodeID = prev.NodeID
	}
	fresh.ScratchEnv = prev.ScratchEnv
	if fresh.Route() == prev.Route() {
		fresh.Resume = prev.Resume
		fresh.SessionTurn = prev.SessionTurn
		fresh.ResumePrompt = prev.ResumePrompt
		fresh.FreshSession = prev.FreshSession
	}
	if d.Workflow != nil {
		stampDecisionRequest(d, fresh)
	} else {
		fresh.Seat = prev.Seat
		fresh.Workflow = prev.Workflow
		fresh.Node = prev.Node
	}
	return fresh, true
}

func nonQuotaPausedRun(r *store.Reader, runID string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	rec, err := terminalRecord(r, runID)
	if err != nil || rec == nil {
		return false, err
	}
	if rec.State != run.StatePaused || rec.EndCause != run.EndRestart {
		return false, nil
	}
	backend, err := quotaPauseBackend(r, runID)
	if err != nil {
		return false, err
	}
	return backend == "", nil
}

func pausedPendingWork(r *store.Reader, work *pendingWork) (bool, error) {
	if work == nil || work.Done {
		return false, nil
	}
	if work.Candidate != nil {
		if work.Candidate.Judge != nil && work.Candidate.Judge.Request.RunID != "" {
			if ok, err := nonQuotaPausedRun(r, work.Candidate.Judge.Request.RunID); err != nil || ok {
				return ok, err
			}
		}
		if ok, err := nonQuotaPausedRun(r, judgeRunID(work.Candidate.RunID)); err != nil || ok {
			return ok, err
		}
	}
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest == nil {
			continue
		}
		if ok, err := nonQuotaPausedRun(r, node.RunRequest.RunID); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

func liveDrainingWorker(r *store.Reader, work *pendingWork, at time.Time) (bool, error) {
	if work == nil || work.Done || work.LaneReleased || work.Decision.LaneID == "" {
		return false, nil
	}
	if work.Candidate != nil {
		leases, err := activeLeases(r, at, []string{work.Decision.LaneID})
		return len(leases) > 0, err
	}
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest == nil {
			continue
		}
		req := *node.RunRequest
		if req.ItemID == "" {
			req.ItemID = node.ItemID
		}
		leased, err := activeWorkerLease(r, &req, node.LaneID, at)
		if err != nil || !leased {
			continue
		}
		rec, err := terminalRecord(r, req.RunID)
		if err != nil {
			return false, err
		}
		if rec == nil || work.Examining {
			return true, nil
		}
		nodeCopy := node
		nodeCopy.RunRequest = &req
		live, err := liveContinuation(r, work.resumes[req.RunID], nodeCopy, at)
		if err != nil || live {
			return live, err
		}
	}
	return false, nil
}

func pendingDecision(r *store.Reader, work *pendingWork, at time.Time, inputs *RunInputs, template *string, policy *Policy, shut map[string]bool, gates int) (Decision, error) {
	return pendingDecisionExcept(r, work, at, inputs, template, policy, shut, gates, "", "", false)
}

func pendingDecisionExcept(r *store.Reader, work *pendingWork, at time.Time, inputs *RunInputs, template *string, policy *Policy, shut map[string]bool, gates int, exceptLane, exceptRunID string, waiting bool) (Decision, error) {
	d := work.Decision
	d.From, d.To = "running", ""
	d.Action, d.Reason = ActionHold, ReasonRunLive
	if len(d.Parallel) > 0 && work.Orphaned && !work.BoundaryFailed {
		leases, err := activeLeasesExcept(r, at, []string{d.LaneID}, exceptLane, exceptRunID)
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
				return pendingDecisionExcept(r, &copy, at, inputs, template, policy, shut, gates, exceptLane, exceptRunID, waiting)
			}
		}
	}
	if d.Workflow != nil && len(d.Parallel) == 0 && work.Orphaned && !work.ResumeAfterRestart && !work.BoundaryFailed {
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if err != nil {
			return Decision{}, err
		}
		leases, err := activeLeasesExcept(r, at, []string{d.LaneID}, exceptLane, exceptRunID)
		if err != nil {
			return Decision{}, err
		}
		// A completed node or a saved pause wins over an operator-stop marker.
		// Otherwise replay only this node of the pinned attempt.
		if len(leases) == 0 && (rec == nil || (rec.State != run.StatePaused && (rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0))) {
			workCopy := *work
			request := *d.RunRequest
			if !evaluatorNode(d) {
				request.RunID = markRunID(request.RunID, "restart")
			}
			d.RunRequest = &request
			d.WorkflowRestart = true
			workCopy.Decision, workCopy.Ready, workCopy.Orphaned = d, true, false
			return pendingDecisionExcept(r, &workCopy, at, inputs, template, policy, shut, gates, exceptLane, exceptRunID, waiting)
		}
	}
	if work.Ready {
		// The original dispatch already consumed the design answer. Ready
		// continuations replay the node, not that one-time conflict transition.
		d.ConflictID = ""
		if len(d.Parallel) > 0 && d.WorkflowRestart {
			nodes := parallelNodes(d)
			for i, node := range nodes {
				rec, err := terminalRecord(r, node.RunRequest.RunID)
				if err != nil {
					return Decision{}, err
				}
				if rec != nil || commandStage(node) {
					continue
				}
				if policy == nil {
					d.Seat = node.Seat
					d.DeferredBy = node.RunRequest.Backend
					d.Action, d.Reason, d.From, d.To = ActionHold, ReasonNoPolicy, "running", ""
					d.RunRequest = nil
					identifyDecision(&d)
					return d, nil
				}
				if !policyHasBackend(policy, node.RunRequest.Backend) {
					d.Seat = node.Seat
					d.DeferredBy = node.RunRequest.Backend
					d.Action, d.Reason, d.From, d.To = ActionHold, backendMissingReason(node.Seat, node.RunRequest.Backend), "running", ""
					d.RunRequest = nil
					identifyDecision(&d)
					return d, nil
				}
				if shut[node.RunRequest.Backend] {
					d.Seat = node.Seat
					d.DeferredBy = node.RunRequest.Backend
					d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
					identifyDecision(&d)
					return d, nil
				}
				if fresh, ok := refreshRestartRequest(policy, node, node.RunRequest); ok {
					node.RunRequest = fresh
					nodes[i] = node
				}
			}
			d = nodes[0]
			d.Parallel = nodes[1:]
		}
		if len(d.Parallel) > 0 {
			if policy != nil {
				for _, node := range parallelNodes(d) {
					if node.RunRequest == nil || commandStage(node) {
						continue
					}
					rec, err := terminalRecord(r, node.RunRequest.RunID)
					if err != nil {
						return Decision{}, err
					}
					if rec == nil && !policyHasBackend(policy, node.RunRequest.Backend) {
						d.Seat = node.Seat
						d.DeferredBy = node.RunRequest.Backend
						d.Action, d.Reason, d.From, d.To = ActionHold, backendMissingReason(node.Seat, node.RunRequest.Backend), "running", ""
						d.RunRequest = nil
						identifyDecision(&d)
						return d, nil
					}
				}
			}
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
			previous := d.RunRequest
			d.RunRequest = commandRequest(d.Workflow.Stages[d.NodeIndex], d.ItemID, d.LaneID, previous.RunID)
			d.RunRequest.ReadOnlyGroup = previous.ReadOnlyGroup
			d.RunRequest.WorkingDir = previous.WorkingDir
			stampDecisionRequest(d, d.RunRequest)
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
		d.SelectSeat = !d.WorkflowRestart
		backend := policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: d.Seat})
		if d.WorkflowRestart {
			backend = d.RunRequest.Backend
		}
		if backend != "" && !policyHasBackend(policy, backend) {
			d.DeferredBy = backend
			d.Action, d.Reason, d.From, d.To = ActionHold, backendMissingReason(d.Seat, backend), "running", ""
			d.RunRequest = nil
			d.Inputs = inputs
			identifyDecision(&d)
			return d, nil
		}
		ok := !shut[backend]
		if !d.WorkflowRestart {
			ok = policy.seatMayStart(d.ItemID, d.Seat, backend, shut)
			if policy.usesSeatShares(d) {
				saved, err := waitingSeatBackend(r, d.RunRequest.RunID, d.Seat, policy.seatList(d.Seat))
				if err != nil {
					return Decision{}, err
				}
				if saved != "" {
					backend, ok = saved, !shut[saved]
				}
			}
		}
		if !ok {
			d.DeferredBy = backend
			d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
		} else {
			var request *run.Request
			if d.WorkflowRestart {
				request, _ = refreshRestartRequest(policy, d, d.RunRequest)
			} else {
				request = policy.request(d.ItemID, d.LaneID, d.RunRequest.RunID, backend)
				if request != nil {
					if d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
						request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
					}
					if len(d.Parallel) > 0 {
						request.ReadOnlyGroup = d.RunRequest.ReadOnlyGroup
						request.NodeID = d.RunRequest.NodeID
					}
					request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
					request.WorkingDir = d.RunRequest.WorkingDir
					stampDecisionRequest(d, request)
				}
			}
			if request == nil {
				return Decision{}, Usagef("workflow seat %s cannot build a request", d.Seat)
			}
			d.RunRequest = request
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
				blocked := seat[0]
				open := policy.seatMayStart("", "review", blocked, shut)
				if len(policy.SeatShares["review"]) > 0 {
					saved, err := waitingSeatBackend(r, judgeRunID(work.Candidate.RunID), "review", seat)
					if err != nil {
						return Decision{}, err
					}
					if saved != "" {
						blocked, open = saved, !shut[saved]
					}
				}
				// A recorded judge request, unlike a fresh draw, waits only for its own backend.
				var recorded string
				err := r.QueryRow(`SELECT json_extract(payload,'$.request.backend') FROM events
					WHERE object_type='run' AND object_id=? AND kind=? ORDER BY seq DESC LIMIT 1`,
					judgeRunID(work.Candidate.RunID), judgeRequestKind).Scan(&recorded)
				if err != nil && err != sql.ErrNoRows {
					return Decision{}, err
				}
				if recorded != "" {
					blocked, open = recorded, !shut[recorded]
				}
				failure, err := readRecordedInfrastructureFailure(r, d.ItemID, "$.node", "review:judge")
				if err != nil {
					return Decision{}, err
				}
				if stem, _, _ := splitRunID(failure.RunID); stem == judgeRunID(work.Candidate.RunID) {
					blocked, open = failure.Backend, !shut[failure.Backend]
				}
				if blocked != "" && !policyHasBackend(policy, blocked) {
					d.Seat, d.DeferredBy = "review", blocked
					d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, backendMissingReason(d.Seat, blocked)
					d.RunRequest = nil
					identifyDecision(&d)
					return d, nil
				}
				if !open {
					d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, ReasonBackendWindow
					d.Seat, d.DeferredBy = "review", blocked
					identifyDecision(&d)
					return d, nil
				}
			}
		}
	}
	if work.Candidate != nil && work.Gated && !work.JudgeLanded {
		h, err := readTrunkHistory(r)
		if err != nil {
			return Decision{}, err
		}
		if h.Lost != "" {
			d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, rewriteReason(r, h)
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
		if work.LaneReleased && (work.Examining || work.Examiner != nil) {
			d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, ReasonGateReplay
			identifyDecision(&d)
			return d, nil
		}
		d.From, d.To, d.Action, d.Reason = "candidate", "landed", ActionJudge, ActionJudge
	} else {
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if err != nil {
			return Decision{}, err
		}
		if !work.Examining && len(work.resumes) != 0 {
			for _, node := range parallelNodes(d) {
				if node.RunRequest == nil {
					continue
				}
				d.liveContinuation, err = liveContinuation(r, work.resumes[node.RunRequest.RunID], node, at)
				if err != nil {
					return Decision{}, err
				}
				if d.liveContinuation {
					identifyDecision(&d)
					return d, nil
				}
			}
		}
		finished := rec != nil
		if len(d.Parallel) > 0 {
			finished, err = parallelTerminal(r, d)
			if err != nil {
				return Decision{}, err
			}
		}
		if finished {
			if evaluatorNode(d) && rec != nil {
				if rec.State == run.StateFailedToStart || judgeQuotaRefusal(*rec) {
					if policy != nil && !policyHasBackend(policy, d.RunRequest.Backend) {
						d.Action, d.Reason, d.From, d.To = ActionHold, backendMissingReason(d.Seat, d.RunRequest.Backend), "running", ""
						d.DeferredBy = d.RunRequest.Backend
						d.RunRequest = nil
						identifyDecision(&d)
						return d, nil
					}
					if shut[d.RunRequest.Backend] {
						d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
						d.DeferredBy = d.RunRequest.Backend
						identifyDecision(&d)
						return d, nil
					}
				}
			}
			if rec != nil && quotaSingleRun(d) {
				backend, err := quotaPauseBackend(r, rec.RunID)
				if err != nil {
					return Decision{}, err
				}
				_, _, _, refused := refusedWindow(*rec)
				if backend != "" && (rec.State == run.StatePaused || refused) && (policy == nil || shut[backend]) {
					d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
					identifyDecision(&d)
					return d, nil
				}
			}
			if rec != nil {
				failed, err := failedDesignerResume(d, *rec)
				if err != nil {
					return Decision{}, err
				}
				if failed && waiting {
					// Settling a failed resume launches a replacement session. Leave the
					// old run pending so the replacement can be planned after release.
					return d, nil
				}
				if failed && shut[d.RunRequest.Backend] {
					d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
					d.DeferredBy = d.RunRequest.Backend
					identifyDecision(&d)
					return d, nil
				}
			}
			d.To, d.Action, d.Reason = "candidate", ActionSettle, ActionSettle
		} else if evaluatorNode(d) && rec == nil {
			// Retry the judge's retained handoff, never allocate another evaluator id.
			if policy != nil {
				if fresh, ok := refreshRestartRequest(policy, d, d.RunRequest); ok {
					d.RunRequest = fresh
				} else if d.RunRequest != nil && !commandStage(d) {
					d.DeferredBy = d.RunRequest.Backend
					d.Action, d.Reason, d.From, d.To = ActionHold, backendMissingReason(d.Seat, d.RunRequest.Backend), "running", ""
					d.RunRequest = nil
					identifyDecision(&d)
					return d, nil
				}
			}
			d.To, d.Action, d.Reason, d.WorkflowRestart = "running", ActionDispatch, "workflow-next-node", true
		}
	}
	if d.Action != ActionHold {
		if !work.LaneReleased {
			leases, err := activeLeasesExcept(r, at, []string{d.LaneID}, exceptLane, exceptRunID)
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
// versionFolds are the two event-log folds every decision's input version reads: the pending
// runs and the item faults. They describe the whole store, not one item, so a transaction
// that versions many decisions folds them once (readVersionFolds) and versions each decision
// against that one answer. On 2026-11-19 a pass folded them afresh for each of 342 decisions,
// 217 ms each against the live store's 1.3 million events: 74 seconds a plan.
type versionFolds struct {
	works           map[string]*pendingWork
	faults          map[string]ItemFault
	contentVersions map[string]string // optional bulk snapshot for starts
}

func readVersionFolds(r *store.Reader) (versionFolds, error) {
	works, err := readPending(r)
	if err != nil {
		return versionFolds{}, err
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return versionFolds{}, err
	}
	return versionFolds{works: works, faults: faults}, nil
}

// inputVersion answers d's input version in r, folding the log for it alone. A transaction
// that versions more than one decision reads the folds once and calls inputVersionFrom.
func inputVersion(r *store.Reader, d Decision) (string, error) {
	folds, err := readVersionFolds(r)
	if err != nil {
		return "", err
	}
	return inputVersionFrom(r, d, folds)
}

// inputVersionFrom answers d's input version in r, reading the pending runs and the item
// faults from folds, which must have been read in the same transaction as r.
func inputVersionFrom(r *store.Reader, d Decision, folds versionFolds) (string, error) {
	var itemInputs []any
	if d.Action == ActionDispatch || d.Action == ActionRetry {
		var version string
		if folds.contentVersions != nil {
			var ok bool
			if version, ok = folds.contentVersions[d.ItemID]; !ok {
				return "", sql.ErrNoRows
			}
		} else {
			schema, err := readVersionSchema(r)
			if err != nil {
				return "", err
			}
			version, err = readItemContentVersion(r, schema, d.ItemID)
			if err != nil {
				return "", err
			}
		}
		itemInputs = []any{version}
	} else {
		// Settlement, judging and recorded holds keep their existing validation.
		var itemState, rank, allowed, design, seat sql.NullString
		var updated, itemSeq int64
		var express, hasExpress bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('items') WHERE name='express')`).Scan(&hasExpress); err != nil {
			return "", err
		}
		if hasExpress {
			if err := r.QueryRow(`SELECT express FROM items WHERE item_id=?`, d.ItemID).Scan(&express); err != nil {
				return "", err
			}
		}
		if err := r.QueryRow(`SELECT state, rank, allowed_paths, design_doc, seat, updated_at, event_seq FROM items WHERE item_id = ?`, d.ItemID).
			Scan(&itemState, &rank, &allowed, &design, &seat, &updated, &itemSeq); err != nil {
			return "", err
		}
		itemInputs = []any{itemState, rank, allowed, design, seat, express, updated, itemSeq}
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
	work, faults := folds.works[d.ItemID], folds.faults
	// Bind the claim to the same owner-only readiness fact as the planner. A
	// criterion may be added or ruled on between planning and the write lock.
	var hasClaimClass, ownerOwed bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('item_criteria') WHERE name='claim_class')`).Scan(&hasClaimClass); err != nil {
		return "", err
	}
	if hasClaimClass {
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM item_criteria WHERE item_id=? AND claim_class='owner-only' AND state <> 'done')`, d.ItemID).Scan(&ownerOwed); err != nil {
			return "", err
		}
	}
	triageVersion := ""
	if d.BlockConflict != nil && d.BlockConflict.TriageVersion != "" {
		triageVersion, _, err = requestVersion(r, d.ItemID)
		if err != nil {
			return "", err
		}
	}
	var resumeSeqs map[string]int64
	if work != nil && len(work.resumes) != 0 {
		resumeSeqs = make(map[string]int64, len(work.resumes))
		for id, segment := range work.resumes {
			resumeSeqs[id] = segment.seq
		}
	}
	raw, err := json.Marshal(append(itemInputs, latest, seq, laneState, laneRun, until, work, work != nil && work.Done, resumeSeqs, faults[d.ItemID], faults[d.ItemID].Seq, triageVersion, ownerOwed))
	return string(raw), err
}

type heldExpressItem struct {
	decision Decision
	version  string
}

type plannedApplication struct {
	decision            Decision
	version             string
	parentRunID         string
	definitionSHA       string
	definition          *definition.Definition
	expressAheadChecked bool
	expressAheadHeld    map[string]heldExpressItem
	start               *plannedStart
}

// A fresh lane's base belongs to the decision, not the transaction that claims it.
type plannedStart struct{ tip, green, base string }

type startRefs struct {
	tip, green string
	readGreen  bool
}

func (s *startRefs) proved(ctx context.Context, root, mode string) (string, error) {
	if mode == "tip" || root == "" {
		return "", nil
	}
	if !s.readGreen {
		green, err := provedLaneBaseAt(ctx, root, mode, s.tip)
		if err != nil {
			return "", err
		}
		s.green, s.readGreen = green, true
	}
	return s.green, nil
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
	// A worker-end examination has its own generation, separate from dispatch.
	dispatchTiming *PassTiming
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
	return l.passPlansMode(ctx, at, generation, false, nil)
}

func (l *Loop) passPlansMode(ctx context.Context, at time.Time, generation *string, pendingOnly bool, commentChecks *[]plannedApplication) ([]plannedApplication, []LaneLease, error) {
	if !l.nonLanding {
		l.sweepDisabledMarkers(ctx)
	}
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
	var reapedSingle []*pendingWork
	reapedAny := false
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
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
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
			reapedAny = len(released) > 0 || after != before
			if reapedAny && nextGeneration == "" {
				nextGeneration, err = allocateGeneration(tx)
			}
			return err
		})
		if err == nil && ev.reapedSingle != nil {
			reapedSingle = *ev.reapedSingle
		}
		return err
	}); err != nil {
		return nil, nil, err
	}
	*generation = nextGeneration
	for _, work := range reapedSingle {
		d := work.Decision
		id := d.RunRequest.RunID
		_ = pipelineCloseGraph(l.cfg.Root, id)
		if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
			return nil, nil, err
		}
		var rec *run.Record
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var readErr error
			rec, readErr = terminalRecord(r, id)
			return readErr
		}); err != nil {
			return nil, nil, err
		}
		if rec != nil {
			if err := l.recordSettlementSpend(ctx, nextGeneration, work.LoopID, *rec); err != nil {
				return nil, nil, err
			}
		}
		if !l.nonLanding {
			base := work.BaseSHA
			if base == "" {
				base = l.laneBase(d.LaneID)
			}
			if err := l.preserveWorkFromBase(ctx, d.LaneID, id, base, "orphaned by a loop stop"); err != nil {
				l.retainUnpreserved(ctx, d.LaneID, id, l.headOf(ctx, d.LaneID), err)
				continue
			}
			if err := l.pinLane(ctx, d.LaneID, id, l.headOf(ctx, d.LaneID)); err != nil {
				if holdErr := l.holdItemFault(ctx, Candidate{ItemID: d.ItemID, RunID: id, LaneID: d.LaneID}, ReasonSettleFailed, err.Error()); holdErr != nil && asItemTerminal(holdErr) == nil {
					return nil, nil, holdErr
				}
				continue
			}
			if err := l.resetLane(ctx, d.LaneID); err != nil {
				return nil, nil, err
			}
		}
		if err := l.markExamined(ctx, d); err != nil {
			return nil, nil, err
		}
		if d.BlockConflict != nil {
			if rec != nil {
				if err := l.recordDesigner(ctx, d, *rec); err != nil {
					return nil, nil, err
				}
			}
			if err := l.settleUnansweredDesignRuns(ctx); err != nil {
				return nil, nil, err
			}
		}
	}
	if reapedAny {
		l.reconcileStartLanes(ctx, nil, at)
	}
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
	if err := l.releaseCountedVerifyHolds(ctx, s); err != nil {
		return nil, nil, err
	}
	var plans []plannedApplication
	var leases []LaneLease
	for {
		var refs *startRefs
		if !l.nonLanding {
			tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
			if err != nil {
				return nil, nil, err
			}
			refs = &startRefs{tip: tip}
		}
		err := s.ReadTx(ctx, func(r *store.Reader) error {
			return l.readPassPlans(ctx, r, at, "", refs, &plans, &leases, pendingOnly, commentChecks)
		})
		if err != nil || !slices.ContainsFunc(plans, func(p plannedApplication) bool { return p.start != nil }) {
			return plans, leases, err
		}
		// Planning also reads working-tree inputs. A landing during that read
		// can mix versions before a start has been decided; retry the snapshot.
		current, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil || current == refs.tip {
			return plans, leases, err
		}
		plans = nil
	}
}

// readPassPlans binds the planner's decisions to a read snapshot. A completion
// only plans an existing reservation, never a fresh attempt or another lane.
func (l *Loop) readPassPlans(ctx context.Context, r *store.Reader, at time.Time, onlyItem string, refs *startRefs, plans *[]plannedApplication, leases *[]LaneLease, pendingOnly bool, commentChecks *[]plannedApplication) error {
	var err error
	observed := append([]string(nil), l.lanes()...)
	held, err := readLanes(r, at)
	if err != nil {
		return err
	}
	for lane, busy := range held {
		if busy && documentDesk(lane) {
			observed = append(observed, lane)
		}
	}
	// A lane above the lane count that still holds work is observed too, so the loop
	// neither calls itself drained nor stops waiting while that work is unfinished.
	works, err := readPending(r)
	if err != nil {
		return err
	}
	if onlyItem != "" {
		work := works[onlyItem]
		if work == nil || work.Done || work.LaneReleased || l.hasLaneCondition(work.Decision.LaneID, "") {
			return nil
		}
	}
	for _, d := range drainingLanes(works, l.lanes()) {
		observed = append(observed, d.LaneID)
	}
	if leases != nil {
		*leases, err = activeLeases(r, at, observed)
		if err != nil {
			return err
		}
	}
	// A lane a lane-owned staging refusal took out of rotation is offered to no item
	// until a re-check puts it back (lane_rotation.go).
	available := l.rotationLanes()
	// The loop's own holds go to the planner, so it holds those items before it offers
	// lanes rather than having a lane it gave one overridden below and left unused.
	loopHolds := map[string]string{}
	l.mu.Lock()
	heldIDs := make([]string, 0, len(l.held))
	for id := range l.held {
		heldIDs = append(heldIDs, id)
	}
	l.mu.Unlock()
	for _, id := range heldIDs {
		if reason, held := l.heldFor(r, id); held {
			loopHolds[id] = reason
		}
	}
	tick := Pass{examineItem: onlyItem, pendingOnly: pendingOnly, ownerMigration: l.ownerMigrationItem != "", loopHolds: loopHolds, startRefs: refs, Comments: l.cfg.Comments, WorkflowDefaults: l.cfg.WorkflowDefaults, GatesInFlight: l.gateCount(), Build: &l.build, Now: at.Format(time.RFC3339Nano), AvailableLanes: &available, OutLanes: l.outLanes()}
	if l.ownerMigrationItem != "" {
		tick.examineItem = l.ownerMigrationItem
	}
	if _, tick.runFaces, err = observeFirstSegmentFaces(r, l.cfg.Store, tick, works); err != nil {
		return err
	}
	if stager, ok := l.cfg.Stage.(*LaneStager); ok {
		tick.BriefTemplate = &stager.Template
	}
	l.mu.Lock()
	questions := l.questions
	l.mu.Unlock()
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
	folds, err := readVersionFolds(r)
	if err != nil {
		return err
	}
	planItems, err := readItems(r)
	if err != nil {
		return err
	}
	expressByID := make(map[string]bool, len(planItems))
	if commentChecks != nil {
		*commentChecks = (*commentChecks)[:0]
	}
	for _, row := range planItems {
		expressByID[row.ItemID] = row.Express
		// Pending-only planning omits unseen batches, but those batches may
		// disappear without ever becoming eligible for a start proposal. Keep
		// closure checks separate from dispatch and durable hold decisions.
		if commentChecks == nil || !pendingOnly || !holdEnabled(declared.Parsed, ReasonVantageUnavailable) ||
			!item.IsCommentItem(row.ItemID) || (row.State != StateInQueue && row.State != StateBlocked) ||
			tick.plansItem(row.ItemID, folds.works[row.ItemID]) {
			continue
		}
		d := Decision{ItemID: row.ItemID, Action: ActionHold}
		version, err := inputVersionFrom(r, d, folds)
		if err != nil {
			return err
		}
		*commentChecks = append(*commentChecks, plannedApplication{decision: d, version: version, definition: &declared.Parsed})
	}
	heldExpressSoFar := map[string]heldExpressItem{}
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
		if onlyItem == "" && folds.contentVersions == nil && (d.Action == ActionDispatch || d.Action == ActionRetry) {
			schema, err := readVersionSchema(r)
			if err != nil {
				return err
			}
			folds.contentVersions, err = readAllItemContentVersions(r, schema)
			if err != nil {
				return err
			}
		}
		version, err := inputVersionFrom(r, d, folds)
		if err != nil {
			return err
		}
		parent := ""
		if work := folds.works[d.ItemID]; work != nil {
			parent = work.ParentRunID
		}
		var aheadHeld map[string]heldExpressItem
		aheadChecked := false
		if (d.Action == ActionDispatch || d.Action == ActionRetry) && isExpressLane(d.LaneID) {
			aheadChecked = true
			aheadHeld = make(map[string]heldExpressItem, len(heldExpressSoFar))
			for k, v := range heldExpressSoFar {
				aheadHeld[k] = v
			}
		}
		// The planner offered lanes in queue order. If an earlier item was held
		// for no-lane despite this later offer, it could not take that lane.
		// Like other holds, trust that answer only while its inputs match.
		if expressByID[d.ItemID] && d.Action == ActionHold {
			heldDec := Decision{ItemID: d.ItemID, BlockConflict: d.BlockConflict}
			heldVer := version
			if d.LaneID != "" {
				heldVer, err = inputVersionFrom(r, heldDec, folds)
				if err != nil {
					return err
				}
			}
			heldExpressSoFar[d.ItemID] = heldExpressItem{decision: heldDec, version: heldVer}
		}
		if onlyItem != "" && (d.ItemID != onlyItem || d.LaneID != works[onlyItem].Decision.LaneID || d.RunRequest == nil) {
			continue
		}
		var start *plannedStart
		work := folds.works[d.ItemID]
		if refs != nil && isDispatch(d) && !d.WorkflowRestart && (work == nil || !work.Ready) {
			start = &plannedStart{tip: refs.tip, base: refs.tip}
			if !IsTrunkFixer(d.ItemID) && !IsRemoteSyncItem(d.ItemID) {
				start.green, err = refs.proved(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
				if err == nil && start.green != "" {
					start.base, err = blockBaseAt(ctx, r, l.cfg.Root, start.green, refs.tip, d.ItemID)
				}
				if err != nil {
					return err
				}
			}
		}
		*plans = append(*plans, plannedApplication{
			decision: d, version: version, parentRunID: parent, start: start,
			definitionSHA: declared.SHA256, definition: &declared.Parsed,
			expressAheadChecked: aheadChecked, expressAheadHeld: aheadHeld,
		})
	}
	return nil
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

// applyPlanGeneration applies one planned decision. A decision whose id the store already
// holds with other columns is that one item's condition, not the loop's: the write is
// rolled back, the item is held with ReasonDecisionCollision naming both decisions, and the
// pass goes on (the owner's ruling: the loop never stops on one item's condition).
func (l *Loop) applyPlanGeneration(ctx context.Context, p plannedApplication, mainSHA string, at time.Time, generation *string) (bool, *pendingWork, error) {
	return l.applyPlanTrunk(ctx, p, &mainSHA, at, generation)
}

func (l *Loop) applyPlanTrunk(ctx context.Context, p plannedApplication, mainSHA *string, at time.Time, generation *string) (bool, *pendingWork, error) {
	applied, work, err := l.applyPlanRecorded(ctx, p, mainSHA, at, generation)
	var collision *decisionCollisionError
	if err == nil || !errors.As(err, &collision) {
		return applied, work, err
	}
	if holdErr := l.holdItemFault(ctx, Candidate{ItemID: p.decision.ItemID}, ReasonDecisionCollision, decisionCollisionDetail(collision, p.decision.Action)); holdErr != nil {
		return false, nil, err
	}
	return false, nil, nil
}

// trunkAdvancedUntouched reports whether trunk advanced fast-forward from base to head
// without changing paths that feed row's planning or fall inside its derived fence.
func (l *Loop) trunkAdvancedUntouched(ctx context.Context, r *store.Reader, d Decision, row itemRow, base, head string) (bool, error) {
	if base == "" || d.Inputs == nil {
		return false, nil
	}
	advanced, err := containsCommit(ctx, l.cfg.Root, base, head)
	if err != nil || !advanced {
		return false, err
	}
	diff, err := l.git(ctx, l.cfg.Root, "diff", "--no-renames", "--name-only", "-z", base+".."+head)
	if err != nil {
		return false, err
	}
	changed := false
	for _, entry := range strings.Split(diff, "\x00") {
		if entry != "" {
			changed = true
			break
		}
	}
	if !changed {
		return false, nil
	}
	var bodyRef string
	if err := r.QueryRow(`SELECT COALESCE(body_ref, '') FROM items WHERE item_id = ?`, row.ItemID).Scan(&bodyRef); err != nil {
		return false, err
	}
	bodyFile, _, _ := strings.Cut(bodyRef, "#")
	if bodyFile != "" {
		bodyFile = filepath.ToSlash(filepath.Clean(bodyFile))
	}
	designDoc := ""
	if row.DesignDoc != "" {
		designDoc = filepath.ToSlash(filepath.Clean(row.DesignDoc))
	}
	workflowDir := ""
	if row.Workflow != "" || d.Workflow != nil {
		workflowDir = "config/tillr/workflows"
	}
	commandsFile := ""
	if d.Workflow != nil {
		commandsFile = "config/tillr/commands.json"
	}
	fence := itemFence(row.Allowed)
	for _, entry := range strings.Split(diff, "\x00") {
		if entry == "" {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(entry))
		if insideFence(clean, fence) {
			return false, nil
		}
		if bodyFile != "" && clean == bodyFile {
			return false, nil
		}
		if designDoc != "" && clean == designDoc {
			return false, nil
		}
		if workflowDir != "" && insideFence(clean, []string{workflowDir}) {
			return false, nil
		}
		if commandsFile != "" && clean == commandsFile {
			return false, nil
		}
	}
	return true, nil
}

func writesOwnerConflict(d Decision) bool {
	c := d.BlockConflict
	return c != nil && c.TriageVersion != "" && c.WaitingOn == "owner"
}

func persistentHold(d Decision) bool {
	return writesOwnerConflict(d) || strings.HasPrefix(d.Reason, ReasonTrunkPathDirty+": ") ||
		strings.HasPrefix(d.Reason, ReasonTrunkRewritten+": ") || strings.HasPrefix(d.Reason, ReasonRedCap+": ")
}

// holdWriter returns nil for report-only holds. Persistent holds recheck their
// definition and input version at the write, just as a single hold would.
func holdWriter(p plannedApplication, at time.Time) func(*store.Tx) error {
	d := p.decision
	if !persistentHold(d) {
		return nil
	}
	return func(tx *store.Tx) error {
		current, err := currentDefinition(tx.Reader(), p)
		if err != nil || !current {
			return err
		}
		version, err := inputVersion(tx.Reader(), d)
		if err != nil || version != p.version {
			return err
		}
		if writesOwnerConflict(d) {
			return item.RecordConflict(tx, *d.BlockConflict, at)
		}
		return recordDirtyDecision(tx, d, at)
	}
}

// writeHolds commits a pass's persistent holds together, or none on error.
func (l *Loop) writeHolds(ctx context.Context, writes []func(*store.Tx) error) error {
	if len(writes) == 0 {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return l.writeTx(ctx, s, func(tx *store.Tx) error {
		for _, write := range writes {
			if err := write(tx); err != nil {
				return err
			}
		}
		return nil
	})
}

func (l *Loop) applyPlanRecorded(ctx context.Context, p plannedApplication, mainSHA *string, at time.Time, generation *string) (ok bool, result *pendingWork, applyErr error) {
	start := time.Now()
	var reason, waitingOn string
	if isDispatch(p.decision) {
		defer func() {
			if ok || (applyErr == nil && reason == "") {
				return
			}
			if applyErr != nil {
				reason, waitingOn = applyErr.Error(), ""
			}
			if err := l.recordDispatchDecline(ctx, p.decision, at, reason, waitingOn); err != nil {
				if applyErr == nil {
					applyErr = err
				} else {
					applyErr = errors.Join(applyErr, fmt.Errorf("recording dispatch decline: %w", err))
				}
				return
			}
			// A busy dispatch made no claim. Its decline was recorded or the
			// loss was reported, so continue without failing the pass.
			if store.IsBusy(applyErr) && ctx.Err() == nil {
				l.recordBusyWrite("dispatch-claim", time.Since(start), l.cfg.Now())
				applyErr = nil
			}
		}()
	}
	decline := func(detail, mover string) error {
		reason, waitingOn = detail, mover
		return nil
	}
	if p.decision.Action == ActionHold {
		if write := holdWriter(p, at); write != nil {
			return false, nil, l.writeHolds(ctx, []func(*store.Tx) error{write})
		}
		return false, nil, nil
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
	// Hook definitions also work in plain directories. Read file-backed inputs
	// before acquiring the write lock; inputVersion below still guards row changes.
	var plainInputs *RunInputs
	if l.nonLanding && p.decision.Inputs != nil && (p.decision.Action == ActionDispatch || p.decision.Action == ActionRetry) {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return false, nil, err
		}
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := readItems(r)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.ItemID == p.decision.ItemID {
					plainInputs, err = currentRunInputs(ctx, r, row, l.cfg.Policy, l.cfg.Root, l.build)
					return err
				}
			}
			return nil
		})
		s.Close()
		if err != nil {
			return false, nil, err
		}
	}
	// Fold once in a read snapshot, never while another writer is excluded.
	// Subsequent reads extend this application's private fold by the delta only.
	reader, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, nil, err
	}
	defer reader.Close()
	var pending *pendingHistoryState
	var historyHead int64
	refreshHistory := func(ctx context.Context) error {
		return reader.ReadTx(ctx, func(r *store.Reader) error {
			head, err := pendingHistoryHead(r)
			if err != nil {
				return err
			}
			if pending == nil {
				pending, err = readPendingHistoryState(r)
			} else {
				pending, err = readPendingHistoryStateCached(r)
			}
			if err == nil {
				historyHead = head
			}
			return err
		})
	}
	if err := refreshHistory(ctx); err != nil {
		return false, nil, err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, nil, err
	}
	defer s.Close()
	applied := false
	var work *pendingWork
	trunkSHA := *mainSHA
	// The decision asks git (the trunk tip, the green pointer, ancestry, a block's body
	// at a commit) and files (the item's definition body, its workflow) about rows it
	// reads here, so the write is gathered: each of those is fetched with no write open,
	// and the write runs again from its first statement with the answer (gathered.go).
	err = writeGathered(ctx, s, seatClaim(l.currentPolicy(), func(ctx context.Context, tx *store.Tx) error {
		applied, work, nextGeneration, trunkSHA = false, nil, *generation, *mainSHA
		reason, waitingOn = "", ""
		current, err := currentDefinition(tx.Reader(), p)
		if err != nil {
			return err
		}
		if !current {
			return decline("the store definition changed", "replan under the current store definition")
		}
		historyChanged, err := pendingHistoryChanged(tx.Reader(), historyHead)
		if err != nil {
			return err
		}
		if historyChanged {
			if !isDispatch(p.decision) {
				return decline("pending work changed during the claim", "replan with the current pending work")
			}
			// A sibling can append while this claim gathers Git/file facts. Refresh
			// outside the writer, then let the normal version and capacity checks
			// decide whether the new history actually invalidates this dispatch.
			_, err := gather(ctx, fmt.Sprintf("pending history after %d", historyHead), func(ctx context.Context) (string, error) {
				return "", refreshHistory(ctx)
			})
			return err
		}
		works, designHistory := pending.works, pending.byRun
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		version, err := inputVersionFrom(tx.Reader(), p.decision, versionFolds{works: works, faults: faults})
		if err != nil {
			return err
		}
		if version != p.version {
			return decline("item or lane state changed since planning", "replan with the current item and lane state")
		}
		d := p.decision
		if (d.Action == ActionDispatch || d.Action == ActionRetry) && d.RunRequest != nil {
			shut, err := readDispatchShutFrom(tx.Reader(), l.cfg.Now(), l.currentPolicy(), d, designHistory)
			if err != nil {
				return err
			}
			open, err := dispatchQuotaOpen(tx.Reader(), d, shut, l.currentPolicy())
			if err != nil {
				return err
			}
			if !open {
				return decline("the dispatch backend window closed", "an eligible backend window to open for the "+d.Seat+" seat")
			}
		}
		var items []itemRow
		if d.Action == ActionDispatch || d.Action == ActionRetry {
			items, err = readItems(tx.Reader())
			if err != nil {
				return err
			}
		}
		var targetRow itemRow
		hasTargetRow := false
		for _, row := range items {
			if row.ItemID == d.ItemID {
				targetRow, hasTargetRow = row, true
				break
			}
		}
		if !l.nonLanding && isDispatch(d) && p.start == nil {
			// Work already in flight retains the existing trunk and input checks.
			head, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
			if err != nil {
				return err
			}
			if head != trunkSHA {
				if !hasTargetRow {
					return decline("the item is no longer in the queue", "the item to return to the queue")
				}
				untouched, err := l.trunkAdvancedUntouched(ctx, tx.Reader(), d, targetRow, trunkSHA, head)
				if err != nil {
					return err
				}
				if !untouched {
					return decline("trunk moved and the planned inputs or fence are not known to be unchanged", "replan against the current trunk")
				}
				trunkSHA = head
			}
		}
		if d.Inputs != nil && (d.Action == ActionDispatch || d.Action == ActionRetry) && l.nonLanding {
			if plainInputs == nil || *plainInputs != *d.Inputs {
				return decline("run inputs changed since planning", "replan with the current run inputs")
			}
		}
		if d.Inputs != nil && isDispatch(d) && !l.nonLanding && hasTargetRow {
			now, err := currentRunInputs(ctx, tx.Reader(), targetRow, l.cfg.Policy, l.cfg.Root, l.build)
			if err != nil {
				return err
			}
			if *now != *d.Inputs {
				if p.start == nil {
					return decline("run inputs changed since planning", "replan with the current run inputs")
				}
				// Check the ref after reading the files. An edit at the same tip
				// is stale input; a later landing belongs to a later decision.
				head, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
				if err != nil {
					return err
				}
				if head == p.start.tip {
					return decline("run inputs changed since planning", "replan with the current run inputs")
				}
			}
		}
		work = works[d.ItemID]
		switch d.Action {
		case ActionDispatch, ActionRetry:
			for _, owner := range works {
				if !owner.Done && !owner.LaneReleased && owner.Decision.LaneID == d.LaneID && !(owner.Decision.ItemID == d.ItemID && (owner.Ready || d.WorkflowRestart)) {
					return decline("lane is held by "+owner.Decision.ItemID, owner.Decision.ItemID+" to release "+d.LaneID)
				}
			}
			leases, err := activeLeases(tx.Reader(), at, []string{d.LaneID})
			if err != nil {
				return err
			}
			if len(leases) != 0 {
				return decline("lane has an active lease", d.LaneID+"'s lease to end")
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
					return decline("document desks are at capacity", "a document desk to become free")
				}
			}
			heldForks, err := liveForks(tx.Reader(), at, works)
			if err != nil {
				return err
			}
			if p.expressAheadChecked && isExpressLane(d.LaneID) && (work == nil || work.Done || work.LaneReleased) {
				def := defaultDefinition()
				if p.definition != nil {
					def = *p.definition
				}
				folds := versionFolds{works: works, faults: faults}
				for _, row := range items {
					if row.ItemID == d.ItemID {
						break
					}
					if !row.Express || finishedState(def, row.State) {
						continue
					}
					if rowWork := works[row.ItemID]; rowWork != nil && !rowWork.Done && !rowWork.LaneReleased {
						continue
					}
					held, wasHeld := p.expressAheadHeld[row.ItemID]
					if !wasHeld {
						return decline("earlier express item "+row.ItemID+" has not been dispatched or held", row.ItemID+" to dispatch or be held")
					}
					curVer, err := inputVersionFrom(tx.Reader(), held.decision, folds)
					if err != nil {
						return err
					}
					if curVer != held.version {
						return decline("the hold for earlier express item "+row.ItemID+" changed", "replan the hold for "+row.ItemID)
					}
				}
			}
			if !l.nonLanding {
				conflicts, err := item.ReadConflicts(tx.Reader())
				if err != nil {
					return err
				}
				for _, row := range items {
					if row.ItemID == d.ItemID || row.State != StateBlocked {
						continue
					}
					c := conflicts[row.ItemID]
					if origin := designContinuationWork(c, designHistory[c.DesignRunID]); origin != nil && !isExpressLane(d.LaneID) && origin.Decision.LaneID == d.LaneID {
						waiting, _, err := item.ReadWaitingOn(tx.Reader(), row.ItemID)
						if err != nil {
							return err
						}
						if !c.HasIndependentWait(waiting) {
							return decline("lane is reserved for the design continuation of "+row.ItemID, row.ItemID+" to release its design lane")
						}
					}
				}
			}
			if hasTargetRow {
				fork := targetRow.Delivery.activeFork()
				if d.Workflow != nil && classificationNode(d.Workflow.Stages[d.NodeIndex]) {
					fork = ""
				}
				for _, held := range heldForks {
					if fork != "" && held.fork == fork && !(work != nil && (work.Ready || d.WorkflowRestart) && held.itemID == d.ItemID) {
						return decline("fork "+fork+" is held by "+held.itemID, held.itemID+" to release fork "+fork)
					}
				}
			}
			if work != nil && !work.Done && !work.Ready && !d.WorkflowRestart {
				return decline("the item still has unfinished work", "the previous work for "+d.ItemID+" to settle")
			}
			base := trunkSHA
			if p.start != nil {
				base = p.start.base
			}
			if IsRemoteSyncItem(d.ItemID) {
				syncBase, err := remoteSyncDispatchBase(tx.Reader(), d.ItemID)
				if err != nil {
					return err
				}
				base = syncBase
			} else if !l.nonLanding && !IsTrunkFixer(d.ItemID) {
				var green string
				if p.start != nil {
					green = p.start.green
				} else {
					green, err = provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
					if err != nil {
						return err
					}
				}
				if green != "" {
					itemEdges, err := readItemEdges(tx.Reader(), d.ItemID)
					if err != nil {
						return err
					}
					if len(itemEdges) > 0 {
						states := map[string]string{}
						for _, row := range items {
							states[row.ItemID] = row.State
						}
						waits, err := unprovedBlockers(ctx, tx.Reader(), l.cfg.Root, green, "", states, map[string][]string{d.ItemID: itemEdges})
						if err != nil {
							return err
						}
						if waits[d.ItemID] != "" {
							return decline("the proved lane base does not contain a dependency", "a trunk proof containing the item's dependencies")
						}
					}
					if p.start == nil {
						base = green
						if work == nil || !(work.Ready || d.WorkflowRestart) {
							base, err = blockBase(ctx, tx.Reader(), l.cfg.Root, green, d.ItemID)
							if err != nil {
								return err
							}
						}
					}
					if base != green {
						l.reportf("loop: %s's block is newer than green %s; cutting its lane from %s\n", d.ItemID, shortSHA(green), shortSHA(base))
					}
				} else if l.gatePolicy.LaneBase != "tip" && !l.laneBootstrapReported {
					l.reportf("loop: no green pointer yet; cutting lanes from trunk tip\n")
					l.laneBootstrapReported = true
				}
			}
			if work != nil && (work.Ready || d.WorkflowRestart) && work.BaseSHA != "" {
				base = work.BaseSHA
			}
			if d.BlockConflict != nil {
				origin := designHistory[d.BlockConflict.DesignRunID]
				if err := l.prepareDesignContinuation(ctx, tx.Reader(), &d, origin, &base); err != nil {
					return err
				}
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
					return decline("conflict "+d.ConflictID+" is no longer answered", "replan with the current conflict answer")
				}
				c.Status = "closed"
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
			} else if d.Action == ActionRetry && d.SupersedesRunID != "" {
				// The operator's retry answers the operator hold that the superseded run's
				// conflict raised (a conflict after a restart, D-JL12): a later conflict is
				// then read against the new retry cycle, not against that parked one.
				conflicts, err := item.ReadConflicts(tx.Reader())
				if err != nil {
					return err
				}
				if c := conflicts[d.ItemID]; c.ConflictID != "" && c.Status == "owner" && c.RunID == d.SupersedesRunID && strings.HasPrefix(c.Text, conflictAfterRestartPrefix) {
					c.Status = "closed"
					if err := item.RecordConflict(tx, c, at); err != nil {
						return err
					}
				}
			}
			// A fork's base comes from staging its checkout, not from the trunk observation.
			if d.Workflow != nil && d.NodeBaseSHA == "" && targetRow.Delivery.activeFork() == "" {
				d.NodeBaseSHA = base
			}
			if d.Workflow != nil {
				if err := clearSupersededNodeClaims(tx, d.ItemID); err != nil {
					return err
				}
			}
			// A retry's kept readers are recorded first, so the set claimed below is
			// chosen from the nodes this attempt still has to run.
			if err := carryRetainedReaders(tx, d); err != nil {
				return err
			}
			if err := l.claimParallelFrom(tx, &d, designHistory); err != nil {
				return err
			}
			shut, err := readDispatchShutFrom(tx.Reader(), at, l.currentPolicy(), d, designHistory)
			if err != nil {
				return err
			}
			if d.DesignContinuationFrom != "" && d.RunRequest.SessionTurn && shut[d.RunRequest.Backend] {
				return errSeatWindowClosed
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
			if err := carryReturnedNodeInputs(tx, d); err != nil {
				return err
			}
			work = &pendingWork{Decision: d, BaseSHA: base, Owner: owner, LoopID: l.currentSessionID()}
			nodes = parallelNodes(d)
			keep := make(map[string]bool, len(nodes))
			for _, node := range nodes {
				if node.RunRequest != nil {
					keep[node.RunRequest.RunID] = true
				}
			}
			if err := supersedeItemUnfinishedRunsInTx(ctx, tx, d.ItemID, d.Attempt, keep, "superseded by "+d.RunRequest.RunID, at); err != nil {
				return err
			}
			claimAt := l.cfg.Now().UTC()
			for _, node := range nodes {
				if len(d.Parallel) > 0 {
					rec, err := terminalRecord(tx.Reader(), node.RunRequest.RunID)
					if err != nil {
						return err
					}
					if rec != nil {
						continue
					}
				}
				stampDecisionRequest(node, node.RunRequest)
				if _, err := run.ClaimLease(ctx, tx, *node.RunRequest, node.LaneID, claimAt); err != nil {
					return err
				}
				if err := startNode(tx, node); err != nil {
					return err
				}
			}
			if err := appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, claimAt); err != nil {
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
			// Only this target is mutated; keep the read snapshot pristine for
			// another gathered attempt after a rollback.
			copy := *work
			work = &copy
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
	}))
	if err == errSeatWindowClosed {
		return false, nil, decline("a workflow seat's backend window closed during the claim", "an eligible backend window to open for the workflow seat")
	}
	if err == nil && applied {
		*generation = nextGeneration
		*mainSHA = trunkSHA
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
	releasedLane := false
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
		n, rowsErr := res.RowsAffected()
		releasedLane = rowsErr == nil && n > 0
		if d.Workflow != nil {
			if err := appendEffect(tx, examinationReleasedKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now().UTC()); err != nil {
				return err
			}
			if w := byRun[d.RunRequest.RunID]; w != nil && !w.Done && w.Examiner != nil {
				return appendEffect(tx, examinationOwnerKind, d.RunRequest.RunID, (*LoopOwner)(nil), l.cfg.Now().UTC())
			}
			return nil
		}
		if w := byRun[d.RunRequest.RunID]; w != nil && !w.Done && w.Examiner != nil {
			if err := appendEffect(tx, examinationOwnerKind, d.RunRequest.RunID, (*LoopOwner)(nil), l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		if rowsErr != nil || n == 0 {
			return rowsErr
		}
		return appendEffect(tx, laneReleasedKind, d.RunRequest.RunID, laneReleased{LaneID: d.LaneID, Cause: laneReleaseEnded}, l.cfg.Now().UTC())
	})
	if err == nil && releasedLane {
		l.notifyStartLaneReleased(d.LaneID, d.RunRequest.RunID, false, l.cfg.Now().UTC())
	}
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
	// store's write lock (readRepeatedEnding); the write records what it read.
	var routed *secondFailure
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		routed, err = readRepeatedEnding(r, d)
		return err
	}); err != nil {
		return err
	}
	if policy := l.currentPolicy(); routed != nil && (policy == nil || len(policy.seatList("design")) == 0) {
		routed.noDesignSeat = true
	}
	if routed != nil && routed.WorkerSession != nil {
		// Release only this still-pending worker, before releasing its
		// examination claim. Replayed examinations never reset a new owner.
		session, err := ReadSessionID(l.laneDirPath(d.LaneID))
		if err != nil {
			return err
		}
		// A verify return's build stage may have no session of its own; the lane then holds
		// only the candidate, and the head names it.
		if (session == routed.WorkerSession.SessionID || routed.WorkerSession.SessionID == "") && l.headOf(ctx, d.LaneID) == routed.WorkerSession.HeadSHA {
			if err := l.resetLane(ctx, d.LaneID); err != nil {
				return err
			}
		}
	}
	releasedLane := false
	var at time.Time
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		at = l.cfg.Now().UTC()
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
			releasedLane = true
			if err := appendEffect(tx, laneReleasedKind, d.RunRequest.RunID, laneReleased{LaneID: d.LaneID, Cause: laneReleaseExamined}, at); err != nil {
				return err
			}
		}
		return recordSecondFailure(tx, routed, at)
	})
	if err == nil {
		if releasedLane {
			l.notifyStartLaneReleased(d.LaneID, d.RunRequest.RunID, true, at)
		} else if v := l.startView.Load(); v != nil {
			// The lane may have been released before replay; wake startView so the
			// item's completed examination and refusal are observed without waiting.
			v.notifyUpdate()
		}
	}
	if err == nil && routed != nil && IsRemoteSyncItem(routed.itemID) {
		l.escalateRemoteSyncPushState(ctx, routed.itemID, "operator", "repeated ending")
	}
	return err
}

func (l *Loop) launchRun(ctx context.Context, d Decision, at time.Time, watch *laneWatch, result *LoopResult) {
	var watched []string
	var epochs map[string]int
	if watch != nil {
		watched, epochs = watch.lanes, watch.epochs
	}
	// The run outlives this supervisor. A canceled ctx ends only this goroutine's
	// wait: ProcessSteps.Run never signals the run face on cancellation, and the
	// next loop settles the run from its terminal record.
	id := d.RunRequest.RunID
	runCtx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	l.markStepActive(d.ItemID, id)
	if l.activeCancels == nil {
		l.activeCancels = map[string]context.CancelFunc{}
	}
	l.activeCancels[id] = cancel
	l.bumpLaneEpoch(d.LaneID)
	result.Dispatched++
	ticks := result.Ticks
	l.mu.Unlock()

	l.reportf("loop: tick %d dispatch %s -> %s on %s\n", ticks, d.ItemID, d.RunRequest.Backend, d.LaneID)
	handoff := !l.nonLanding && !l.pollOnly && l.ownerMigrationItem == ""
	go func() {
		defer cancel()
		start := time.Now()
		completion := runCompletion{id: id, itemID: d.ItemID, step: "run", timing: &PassTiming{TickID: d.RunRequest.CorrelationID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}}
		local := LoopResult{}
		var evaluatorHold *Candidate
		var record run.Record
		var log string
		endSnap := timeHandoffPhase(runCtx, "snapshot_idle_siblings")
		before, err := l.snapshotIdleSiblings(runCtx, l.lanePath(d.LaneID), watched, epochs)
		endSnap()
		if err != nil {
			finishStartHandoff(runCtx)
			err = fmt.Errorf("run boundary before: %w", err)
		}
		if err == nil {
			if evaluatorNode(d) {
				finishStartHandoff(runCtx)
				c, candidateErr := l.evaluatorCandidate(runCtx, d)
				err = candidateErr
				if err == nil {
					var accepted bool
					_, accepted, err = l.judgeCandidate(runCtx, c, &local)
					if errors.Is(err, errJudgeBackendHeld) || errors.Is(err, errJudgePaused) {
						err = l.releaseExamination(runCtx, d)
						completion.result = &local
					} else if err == nil && !accepted {
						evaluatorHold = &c
						completion.result = &local
					}
				}
			} else if len(d.Parallel) > 0 {
				finishStartHandoff(runCtx)
				err = l.runParallel(runCtx, d, &local, completion.timing, watched)
				completion.result = &local
			} else {
				req := *d.RunRequest
				stampDecisionRequest(d, &req)
				record, log, err = l.runRestartable(runCtx, req)
			}
			if err == nil {
				boundaryCtx, recordTiming := l.timeHandoff(context.WithoutCancel(ctx), d, "boundary")
				completion.outcome.stop, err = l.closeAsyncBracket(boundaryCtx, d, at, before, &local, watched, epochs)
				recordTiming()
				if err != nil {
					err = fmt.Errorf("run boundary after: %w", err)
				}
			}
		}
		if err == nil && evaluatorHold != nil {
			err = l.finishEvaluatorHold(runCtx, d, *evaluatorHold)
		}
		completion.outcome.err = err
		completion.detail = local.Detail
		if local.StopStep != "" {
			completion.step = local.StopStep
		}
		completion.timing.DispatchMS = time.Since(start).Milliseconds()
		answered := record.State == run.StateExited && ClassifyAttempt(record, log) == AttemptAnswered
		parallelReady := len(d.Parallel) > 0
		if parallelReady && err == nil {
			_ = l.readStore(runCtx, func(r *store.Reader) error {
				paused, pErr := pausedPendingWork(r, &pendingWork{Decision: d})
				if pErr == nil && paused {
					parallelReady = false
				}
				return nil
			})
		}
		if handoff && err == nil && completion.outcome.stop == "" && (parallelReady || answered) {
			l.examineWorkerEnd(runCtx, d, watched, &completion)
		}
		l.sendCompletion(ctx, completion)
	}()
}

func (l *Loop) takeCompletion(c runCompletion, result *LoopResult) (string, error) {
	l.mu.Lock()
	delete(l.activeCancels, c.id)
	stopped := l.stoppedRuns != nil && l.stoppedRuns[c.id]
	if stopped {
		delete(l.stoppedRuns, c.id)
	}
	delete(l.active, c.id)
	if l.activeItems[c.itemID] == c.id {
		delete(l.activeItems, c.itemID)
	}
	l.mu.Unlock()
	if c.dispatchTiming != nil {
		l.countGeneration(result, c.timing.TickID)
		if err := l.recordPass(c.timing); err != nil {
			result.StopItemID, result.StopStep = c.itemID, "record pass"
			return "", err
		}
		c.timing = c.dispatchTiming
	}
	if stopped || errors.Is(c.outcome.err, errRestartPending) {
		return "", nil
	}
	if c.result != nil {
		l.mu.Lock()
		mergeExamination(result, c.result)
		l.mu.Unlock()
		if c.result.Rejected > 0 {
			l.finalizeRepeatedRefusal(result)
			l.mu.Lock()
			line := RepeatedRefusalLine(*result)
			l.mu.Unlock()
			if line != "" {
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
	if c.outcome.err != nil && l.holdSettleFailure(c) {
		return c.outcome.stop, nil
	}
	if c.outcome.stop != "" || c.outcome.err != nil {
		result.StopItemID, result.StopStep = c.itemID, c.step
		if c.result != nil && c.result.StopStep != "" {
			result.StopStep = c.result.StopStep
		}
	}
	return c.outcome.stop, c.outcome.err
}

// holdSettleFailure handles specific run-completion errors outside the per-item
// examination boundary in examineClaimed. The operator can repair and clear the fault.
func (l *Loop) holdSettleFailure(c runCompletion) bool {
	var collision *decisionCollisionError
	var started *runStartError
	var pinErr *runPinError
	if c.itemID == "" {
		return false
	}
	reason := ReasonSettleFailed
	switch {
	case errors.As(c.outcome.err, &collision):
		reason = ReasonDecisionCollision
	case errors.As(c.outcome.err, &pinErr), c.step == "run" && errors.As(c.outcome.err, &started):
	default:
		return false
	}
	detail := c.step + ": " + c.outcome.err.Error()
	ctx := context.Background()
	if l.holdItemFault(ctx, Candidate{ItemID: c.itemID, RunID: c.id}, reason, detail) == nil {
		return true
	}
	// A terminal item's run is not judged, so its fault is recorded without the run.
	return l.holdItemFault(ctx, Candidate{ItemID: c.itemID}, reason, detail) == nil
}

// pass advances each item's one planned transition without waiting for a sibling.
// A fixed point waits on completion, expiry, polling, or operator cancellation.
func (l *Loop) pass(ctx context.Context, result *LoopResult) (string, error) {
	return l.passMode(ctx, result, false)
}

func (l *Loop) passMode(ctx context.Context, result *LoopResult, pendingOnly bool) (stop string, err error) {
	defer func() {
		if errors.Is(err, errRestartPending) {
			_, stop, err = l.gateRestart(ctx, result)
		}
	}()
	if handled, stop, err := l.gateRestart(ctx, result); handled || err != nil {
		return stop, err
	}
	if err := l.checkStopRequested(ctx); err != nil {
		return "", err
	}
	if !l.nonLanding {
		l.sweepDisabledMarkers(ctx)
	}
	if err := l.observeTrunkCoreWorktree(ctx); err != nil {
		l.reportf("loop: could not record trunk core.worktree observation: %v\n", err)
	}
	if err := l.refreshLaneCount(ctx, false); err != nil {
		return "", err
	}
	if stop, err := l.drainCompletions(ctx, result); stop != "" || err != nil {
		return stop, err
	}
	if !l.nonLanding {
		if err := l.reclaimEndedItemLanes(ctx); err != nil {
			l.reportf("loop: ended-item lane cleanup failed: %v\n", err)
		}
		l.reportRefSweepFailure(l.pruneRunRefs(ctx))
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
	// Settle an interrupted repair before trunk observation or the stale-stage sweep
	// can mistake its replay for a manual reconciliation.
	if replay, err := l.recoverPendingRemoteSyncReplay(ctx); err != nil {
		return "", err
	} else if replay != nil {
		l.afterRemoteSyncReplay(ctx, replay, result)
	}
	if _, err := l.observeTrunk(ctx, ""); err != nil {
		return "", err
	}
	if err := l.reconcileRewrittenLandings(ctx); err != nil {
		return "", err
	}
	// After-merge proof starts before document sync and landing reconciliation, so a fault
	// in either on every start cannot keep a restarting loop from ever proving the tip.
	if !l.nonLanding && l.gatePolicy.LandingStrategy == "after-merge" {
		early, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return "", err
		}
		if err := l.maybeRolling(ctx, early, false, result); err != nil {
			return "", err
		}
	}
	mainSHA, err := l.refreshDocuments(ctx, result)
	if err != nil {
		return "", err
	}
	if !l.nonLanding {
		if err := l.reconcileLandingCalls(ctx); err != nil {
			return "", err
		}
	}
	if err := l.retireDesigners(ctx); err != nil {
		return "", err
	}
	if err := l.checkStopRequested(ctx); err != nil {
		return "", err
	}
	if !l.nonLanding {
		if err := l.closeStaleRemoteSyncItems(ctx); err != nil {
			return "", err
		}
		l.maybeRecoverClearedPushHold(ctx)
	}
	if err := l.maybeRolling(ctx, mainSHA, false, result); err != nil {
		return "", err
	}
	if err := l.checkStopRequested(ctx); err != nil {
		return "", err
	}
	if err := l.releaseUnlaunchedClaims(ctx); err != nil {
		return "", err
	}
	if err := l.repairStaleRunRecords(ctx); err != nil {
		return "", err
	}
	if !l.nonLanding {
		if err := l.releaseGeneratedTriage(ctx); err != nil {
			return "", err
		}
		if err := l.resolveProvedFixers(ctx); err != nil {
			return "", err
		}
		if err := l.settleResolvedFixerRuns(ctx); err != nil {
			return "", err
		}
		if err := l.releaseTrunkAcquittals(ctx); err != nil {
			return "", err
		}
		if err := l.supersedeRetriedCandidates(ctx); err != nil {
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
		l.reportAlarms(ctx, at)
		l.countGeneration(result, timing.TickID)
	}()
	epochs := l.idleLaneEpochs()
	if err := l.retireOrderWork(ctx); err != nil {
		return "", err
	}
	// Maintenance may have overlapped a worker or examination finishing.
	// Any completion drained here missed this pass's top-of-pass ref sweep and
	// trunk/document sync, so the pass cannot stop as queue-drained without
	// another pass to run that maintenance.
	maintenanceDrained, stop, err := l.drainCompletionsCount(ctx, result)
	if stop != "" || err != nil {
		return stop, err
	}
	var commentChecks []plannedApplication
	plan := func(ctx context.Context, at time.Time, generation *string) ([]plannedApplication, []LaneLease, error) {
		return l.passPlansMode(ctx, at, generation, pendingOnly, &commentChecks)
	}
	plans, leases, err := plan(ctx, at, &timing.TickID)
	if err != nil {
		return "", err
	}
	// A close or explicit finish can arrive while a run face still owns its lease.
	// End only those runs, using normal interruption and recovery before settlement.
	var finished map[string]bool
	for _, p := range plans {
		if p.decision.Action == ActionHold && p.decision.Reason == ReasonItemAlreadyTerminal {
			if finished == nil {
				finished = map[string]bool{}
			}
			finished[p.decision.ItemID] = true
		}
	}
	if len(finished) != 0 {
		skipped, err := endRuns(ctx, l.cfg.Store, finished)
		if err != nil {
			return "", err
		}
		for _, line := range skipped {
			l.reportf("loop: end terminal item run: %s\n", line)
		}
	}
	// Resolve batches that disappeared before applying this snapshot. Closing a
	// comment item changes dispatch eligibility for its document's build items,
	// so take a fresh snapshot in this same pass, not at the next wake.
	commentReady := map[string]bool{}
	for {
		closedBatch := false
		for _, p := range slices.Concat(plans, commentChecks) {
			d := p.decision
			if !p.checksComments() || !item.IsCommentItem(d.ItemID) || l.planStillActive(p) ||
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
		plans, leases, err = plan(ctx, at, &timing.TickID)
		if err != nil {
			return "", err
		}
	}
	answers, counts, err := passAnswers(plans)
	if err != nil {
		return "", err
	}
	for action, count := range timing.DecisionCounts {
		counts[action] += count
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
	var holdWrites []func(*store.Tx) error
	if pendingOnly {
		holdWrites, err = startHoldWrites(l.startView.Load(), at)
		if err != nil {
			return "", err
		}
	}
	// Watch currently idle offered lanes. A sibling legitimately started later
	// is removed from both status comparisons when this run's bracket closes.
	watched := []string{}
	busy := map[string]bool{}
	for _, p := range plans {
		if p.decision.RunRequest != nil {
			busy[p.decision.LaneID] = true
		}
	}
	for _, lane := range l.lanes() {
		// A refused lane may have no worktree here to snapshot, even after a
		// re-check offers it again or changes the reason before recreating it.
		// A holding worktree elsewhere is not a sibling this bracket watches.
		if l.hasLaneCondition(lane, "") {
			if _, err := os.Stat(l.lanePath(lane)); os.IsNotExist(err) {
				continue
			}
		}
		if !busy[lane] {
			watched = append(watched, lane)
		}
	}
	stale := maintenanceDrained > 0
	// A staging refusal that took its lane out of rotation re-plans the item in this same
	// pass, so it is offered the next free lane; each re-plan removes a lane, so this is
	// bounded by the lane count.
	replans := 0
	drained, stop, err := l.drainCompletionsCount(ctx, result)
	if stop != "" || err != nil {
		return stop, err
	}
	if drained > 0 {
		stale = true
	}
plans:
	for i := 0; i < len(plans); i++ {
		if err := l.checkStopRequested(ctx); err != nil {
			return "", err
		}
		p := plans[i]
		d := p.decision
		d.PassID = timing.TickID
		p.decision = d
		if d.Action == ActionHold {
			holds = append(holds, d)
		}
		if l.planStillActive(p) {
			// The completion owns the next decision for this item. This snapshot
			// has nothing to decline: even a sent completion may still be queued.
			continue
		}
		if p.checksComments() && item.IsCommentItem(d.ItemID) && (isDispatch(d) || d.Action == ActionHold) {
			if !commentReady[d.ItemID] {
				if isDispatch(d) {
					if err := l.recordDispatchDecline(ctx, d, at, "the Vantage comment is not ready", "Vantage to make the comment available"); err != nil {
						return "", err
					}
				}
				d.Action = ActionHold
				d.Reason = ReasonVantageUnavailable
				holds = append(holds, d)
				continue
			}
		}
		if d.Action == ActionHold {
			if write := holdWriter(p, at); write != nil {
				holdWrites = append(holdWrites, write)
			}
			continue
		}
		cancelled, changed, err := l.applyReadyPlan(ctx, p, &mainSHA, at, timing, &laneWatch{watched, epochs}, result)
		if err != nil {
			return "", err
		}
		stale = stale || changed
		if cancelled {
			// A claim that could not launch is offered again in this pass, not the next:
			// the item on whatever lane the fresh plan chooses, and any item that
			// waited for the lane the cancellation freed. A refusal the lane's own
			// state caused, or a cancellation with no infrastructure fault behind
			// it, replans; an infrastructure fault would only repeat and count again.
			l.mu.Lock()
			canReplan := l.laneRefusedNow || len(l.stageFaults) == 0
			l.mu.Unlock()
			if canReplan && replans < len(l.lanes())+1 {
				replans++
				// A lane this pass just refused stays out until the next pass: a
				// re-check now would see its pinned HEAD as retained and offer it again.
				l.replanning = true
				fresh, freshLeases, err := plan(ctx, at, &timing.TickID)
				l.replanning = false
				if err != nil {
					return "", err
				}
				leases = freshLeases
				queued := map[string]bool{}
				for _, rest := range plans[i+1:] {
					if rest.decision.Action == ActionDispatch || rest.decision.Action == ActionRetry {
						queued[rest.decision.ItemID] = true
					}
				}
				var again []plannedApplication
				for _, next := range fresh {
					if next.decision.Action != ActionDispatch && next.decision.Action != ActionRetry {
						continue
					}
					if next.decision.ItemID == d.ItemID {
						// Offer it again, now, on the lane the fresh plan chose. The
						// rest of this pass's plans stay as they were: one that
						// conflicts with the re-plan is refused as stale when applied.
						again = append([]plannedApplication{next}, again...)
					} else if !queued[next.decision.ItemID] {
						again = append(again, next)
					}
				}
				if len(again) > 0 {
					plans = append(plans[:i+1], append(again, plans[i+1:]...)...)
					continue plans
				}
			}
		}
	}
	if err := l.writeHolds(ctx, holdWrites); err != nil {
		return "", err
	}
	if timing.Applied > 0 || timing.TickID != "" {
		l.waitingDetail = ""
		return "", nil
	}
	if l.activeCount() > 0 {
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
		if hold.RunRequest != nil && (hold.Reason == ReasonRunLive || hold.Reason == ReasonGateReplay || hold.Reason == ReasonNoLane) {
			// A replacement has no local completion channel for surviving runs.
			// Their durable claims are still work, not foreign leased lanes.
			return l.waitForTrigger(ctx, leases, at, result)
		}
	}
	if stale {
		return "", nil
	}
	if err := l.checkStopRequested(ctx); err != nil {
		return "", err
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
	if pendingOnly {
		idle, allHolds, err := l.startIdle(ctx, l.startView.Load(), holds)
		if err != nil {
			return "", err
		}
		if !idle {
			return l.waitForTrigger(ctx, leases, at, result)
		}
		holds = allHolds
	}
	return l.planNothing(ctx, holds, leases, at, result)
}

// prepareClaimedStage stages a fresh lane or checks an already staged parallel
// restart. A boundary refusal owns its own settlement, not cancelStaging.
func (l *Loop) prepareClaimedStage(ctx context.Context, d *Decision, base *string, result *LoopResult) ([]Decision, bool, error) {
	if len(d.Parallel) > 0 && d.WorkflowRestart {
		owned, err := l.stagedNodeRun(*d)
		if err != nil {
			return nil, false, l.holdStageFailed(ctx, *d, "read staged node run", err)
		}
		if owned != "" {
			if d.NodeIndex == 0 && d.NodeStartRunID == "" {
				raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, owned), BaseSHAFile))
				if err != nil {
					return nil, false, l.holdStageFailed(ctx, *d, "read staged base", err)
				}
				d.NodeBaseSHA = strings.TrimSpace(string(raw))
				d.NodeStartRunID = owned
			}
			if stagedBase := l.laneBase(d.LaneID); stagedBase != "" {
				*base = stagedBase
			}
			head, err := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
			if err != nil {
				return nil, false, l.holdStageFailed(ctx, *d, "inspect parallel lane", err)
			}
			dirty, err := l.git(ctx, l.laneTarget(d.LaneID), "status", "--porcelain", "--untracked-files=all")
			if err != nil {
				return nil, false, l.holdStageFailed(ctx, *d, "inspect parallel lane", err)
			}
			if head != d.NodeBaseSHA || strings.TrimSpace(dirty) != "" {
				return nil, true, l.refuseParallelBoundary(ctx, *d, *base, head, result, true)
			}
			return []Decision{*d}, false, nil
		}
	}
	staged, err := l.stageDispatches(ctx, []Decision{*d}, *base, result)
	return staged, false, err
}

// applyReadyPlan is shared by the queue pass and an item-local completion handoff.
// It leaves cancellation replanning to the queue pass, which owns lane offers.
func (l *Loop) applyReadyPlan(ctx context.Context, p plannedApplication, mainSHA *string, at time.Time, timing *PassTiming, watch *laneWatch, result *LoopResult) (cancelled, stale bool, err error) {
	d := p.decision
	ok, work, err := l.applyPlanTrunk(ctx, p, mainSHA, at, &timing.TickID)
	if err != nil {
		result.StopItemID, result.StopStep = d.ItemID, "apply plan"
		return false, false, err
	}
	if !ok {
		return false, d.Action != ActionHold, nil
	}
	timing.Applied++
	if d.RunRequest != nil {
		request := *d.RunRequest
		request.CorrelationID = timing.TickID
		d.RunRequest = &request
	}
	switch d.Action {
	case ActionDispatch, ActionRetry:
		base := *mainSHA
		if work != nil {
			base = work.BaseSHA
		}
		if work != nil {
			d = work.Decision
		}
		startCtx, startHandoff := l.beginStartHandoff(ctx, d)
		if v := l.startView.Load(); v != nil {
			endNote := timeHandoffPhase(startCtx, "note_start_claimed")
			v.noteStartClaimed(d, work)
			endNote()
		}
		start := time.Now()
		l.mu.Lock()
		l.laneRefusedNow = false
		l.mu.Unlock()
		staged, refused, err := l.prepareClaimedStage(startCtx, &d, &base, result)
		if refused {
			return false, false, err
		}
		if err != nil {
			if stoppedByOperator(ctx, err) {
				_ = l.cancelStaging(context.WithoutCancel(ctx), d)
			}
			result.StopItemID, result.StopStep = d.ItemID, "stage"
			return false, false, err
		}
		if len(staged) > 0 {
			endParallel := timeHandoffPhase(startCtx, "stage_parallel")
			parallel, pErr := l.stageParallel(startCtx, staged[0], base)
			endParallel()
			if pErr != nil {
				if stoppedByOperator(ctx, pErr) || ctx.Err() != nil {
					_ = l.cancelStaging(context.WithoutCancel(ctx), d)
					result.StopItemID, result.StopStep = d.ItemID, "stage"
					return false, false, pErr
				}
				if holdErr := l.holdStageFailed(ctx, d, "stage parallel", pErr); holdErr != nil {
					_ = l.cancelStaging(context.WithoutCancel(ctx), d)
					result.StopItemID, result.StopStep = d.ItemID, "stage"
					return false, false, holdErr
				}
				staged = nil
			} else {
				staged[0] = parallel
			}
		}
		timing.StageMS += time.Since(start).Milliseconds()
		if len(staged) == 0 {
			if err := l.cancelStaging(ctx, d); err != nil {
				result.StopItemID, result.StopStep = d.ItemID, "cancel staging"
				return false, false, err
			}
			return true, false, nil
		}
		endStop := timeHandoffPhase(startCtx, "check_stop_requested")
		stopErr := l.checkStopRequested(startCtx)
		endStop()
		if stopErr != nil {
			if cancelErr := l.cancelStaging(context.WithoutCancel(ctx), d); cancelErr != nil {
				return false, false, cancelErr
			}
			return false, false, stopErr
		}
		startHandoff.setDecision(staged[0])
		l.launchRun(withStartHandoff(ctx, startHandoff), staged[0], at, watch, result)
	case ActionSettle, ActionJudge:
		if err := l.checkStopRequested(ctx); err != nil {
			if releaseErr := l.releaseExamination(context.WithoutCancel(ctx), d); releaseErr != nil {
				return false, false, releaseErr
			}
			return false, false, err
		}
		var watched []string
		if watch != nil {
			watched = unmovedLanes(watch.lanes, watch.epochs, l.idleLaneEpochs())
		}
		l.launchExamination(ctx, d, work, at, timing, watched)
	}
	return false, false, nil
}

// launchExamination runs the claimed settlement or judgment independently;
// the completion is a wake trigger and measurement, not its durable outcome.
func (l *Loop) launchExamination(ctx context.Context, d Decision, work *pendingWork, at time.Time, passTiming *PassTiming, watched []string) {
	runCtx, cancel := context.WithCancel(ctx)
	runCtx = context.WithValue(runCtx, heavyParentCtxKey{}, ctx)
	l.mu.Lock()
	l.markStepActive(d.ItemID, d.RunRequest.RunID)
	if l.activeCancels == nil {
		l.activeCancels = map[string]context.CancelFunc{}
	}
	l.activeCancels[d.RunRequest.RunID] = cancel
	l.mu.Unlock()
	l.examinations.Add(1)
	timing := PassTiming{TickID: passTiming.TickID, AtMS: passTiming.AtMS, GateBudgetMS: passTiming.GateBudgetMS}
	go func() {
		defer cancel()
		defer l.examinations.Done()
		l.sendCompletion(ctx, l.examineClaimed(runCtx, d, work, at, &timing, watched))
	}()
}

func (l *Loop) examineClaimed(ctx context.Context, d Decision, work *pendingWork, at time.Time, timing *PassTiming, watched []string) runCompletion {
	local := &LoopResult{}
	completion := runCompletion{id: d.RunRequest.RunID, itemID: d.ItemID, step: d.Action, result: local, timing: timing}
	ctx, recordTiming := l.timeHandoff(ctx, work.Decision, "examination")
	defer recordTiming()
	if work.NeedsStage {
		endStage := timeHandoffPhase(ctx, "stage_pinned_candidate")
		if err := l.stagePinnedCandidate(ctx, work); err != nil {
			if refusal := asLaneRefusal(err); refusal != nil {
				// The lane's own state refused the pinned candidate: the lane leaves
				// rotation and the candidate, still pinned, is released to be offered
				// another lane. One lane's condition never stops the loop.
				if d.RunRequest != nil {
					refusal.ExcludedRunID = d.RunRequest.RunID
				}
				completion.laneRefusal = refusal
				err = l.releaseRefusedAssignment(ctx, work)
			}
			completion.outcome.err = err
		}
		endStage()
	}
	var survivingOut laneOutcome
	var survivingHeld bool
	if completion.outcome.err == nil && completion.laneRefusal == nil {
		endProc := timeHandoffPhase(ctx, "check_surviving_processes")
		survivingOut, survivingHeld = l.checkSurvivingRunProcesses(ctx, d.ItemID, d.LaneID, examinationRunIDs(work)...)
		endProc()
	}
	if completion.outcome.err != nil || completion.laneRefusal != nil {
		// The claimed lane remains pending so a replacement retries staging.
	} else if survivingHeld {
		completion.outcome = survivingOut
		completion.laneRefusal = survivingOut.laneRefusal
	} else if d.Reason == ReasonItemAlreadyTerminal {
		// The plan read the terminal state and the claim's version check rebound it;
		// the settling write rereads it for the record.
		endTerminal := timeHandoffPhase(ctx, "settle_terminal")
		completion.outcome.err = l.settleTerminalItem(ctx, work, &itemTerminalError{ItemID: d.ItemID, Step: "plan"})
		endTerminal()
	} else if d.WorkflowRelease {
		endRelease := timeHandoffPhase(ctx, "workflow_release")
		owned, err := l.stagedNodeRun(work.Decision)
		if err == nil && (owned != "" || work.Decision.NodeIndex > 0) {
			// The run pin is the release's retained head. A crash after
			// reset but before markExamined must never replace it with
			// the reset trunk head on replay.
			pinned, pinErr := l.git(ctx, l.lanePinRepository(d.LaneID), "for-each-ref", "--format=%(objectname)", pinRef(d.RunRequest.RunID))
			err = pinErr
			if err == nil && strings.TrimSpace(pinned) == "" {
				head, headErr := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
				err = headErr
				if err == nil {
					err = l.pinLane(ctx, d.LaneID, d.RunRequest.RunID, head)
				}
			}
			if err == nil {
				base := d.NodeBaseSHA
				if base == "" {
					base = work.BaseSHA
				}
				if state, ended, stateErr := l.terminalItemState(ctx, d.ItemID); stateErr != nil {
					err = stateErr
				} else if ended {
					saveRunID := d.RunRequest.RunID
					if owned == "" && work.ParentRunID != "" {
						saveRunID = work.ParentRunID
					}
					_, err = l.saveEndedItemWork(ctx, d.LaneID, saveRunID, d.ItemID, state, "workflow released after item ended ("+state+")")
				} else {
					err = l.preserveWorkFromBase(ctx, d.LaneID, d.RunRequest.RunID, base, "orphaned workflow released")
				}
				if err == nil {
					err = l.restoreForkDelivery(ctx, d.LaneID, d.RunRequest.RunID)
				}
				if err == nil {
					err = l.resetLane(ctx, d.LaneID)
				}
			}
		}
		if err == nil {
			err = l.markExamined(ctx, work.Decision)
		}
		endRelease()
		completion.outcome.err = err
	} else if d.Action == ActionSettle && len(work.Decision.Parallel) > 0 {
		endParallel := timeHandoffPhase(ctx, "settle_parallel")
		completion.outcome = l.settleParallel(ctx, work, local, timing, watched)
		endParallel()
		if completion.outcome.err == nil && completion.outcome.stop == "" {
			completion.outcome, completion.step = l.judgeSettledCandidate(ctx, d, local, timing)
		}
	} else if d.Action == ActionSettle {
		endRead := timeHandoffPhase(ctx, "read_terminal_record")
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
		endRead()
		if err == nil && rec == nil {
			err = fmt.Errorf("run %s lost its terminal record", d.RunRequest.RunID)
		}
		if err == nil {
			endSpend := timeHandoffPhase(ctx, "record_settlement_spend")
			err = l.recordSettlementSpend(ctx, timing.TickID, work.LoopID, *rec)
			endSpend()
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
			if !evaluatorNode(work.Decision) && (rec.State == run.StatePaused || work.ResumeAfterRestart) && !quotaPaused && completion.outcome.err == nil {
				reason := ""
				if work.ResumeAfterRestart {
					reason = "the previous loop lost its run face, but the staged session and worktree are intact"
				}
				endCont := timeHandoffPhase(ctx, "run_continuation")
				resumed, resumedLog, stop, resumeErr := l.runContinuation(ctx, d.LaneID, *d.RunRequest, l.lanePath(d.LaneID), *rec, d.RunRequest.CorrelationID, at, local, watched, reason)
				endCont()
				if resumeErr != nil || stop != "" {
					completion.outcome = laneOutcome{stop: stop, err: resumeErr}
				} else if resumed.RunID == "" {
					completion.outcome.err = Externalf("interrupted run %s cannot continue: its lane needs the staged brief and session-id", rec.RunID)
				} else {
					rec, log = &resumed, resumedLog
				}
			}
			if completion.outcome.err == nil && completion.outcome.stop == "" {
				endSpend := timeHandoffPhase(ctx, "record_settlement_spend")
				completion.outcome.err = l.recordSettlementSpend(ctx, timing.TickID, work.LoopID, *rec)
				endSpend()
			}
			if completion.outcome.err == nil && completion.outcome.stop == "" {
				endSettle := timeHandoffPhase(ctx, "settle")
				completion.outcome = l.settle(ctx, work.Decision, *rec, log, work.BaseSHA, d.RunRequest.CorrelationID, at, local, timing, watched, true)
				endSettle()
				if completion.outcome.laneRefusal != nil {
					completion.laneRefusal = completion.outcome.laneRefusal
				}
			}
			if terminal := asItemTerminal(completion.outcome.err); terminal != nil {
				endTerminal := timeHandoffPhase(ctx, "settle_terminal")
				completion.outcome = laneOutcome{err: l.settleTerminalItem(ctx, work, terminal)}
				endTerminal()
			}
		} else {
			completion.outcome.err = err
		}
		if completion.outcome.err == nil && completion.outcome.stop == "" && completion.laneRefusal == nil {
			completion.outcome, completion.step = l.judgeSettledCandidate(ctx, d, local, timing)
		}
	} else {
		completion.outcome = l.runCandidateExamination(ctx, work, local, timing)
	}
	completion.outcome = retainedOutcome(completion.outcome)
	if completion.outcome.stop == "" && completion.outcome.err != nil {
		endHold := timeHandoffPhase(ctx, "hold_examination_failure")
		completion.outcome.err = l.holdExaminationFailure(ctx, d, work, completion.step, completion.outcome.err)
		endHold()
		completion.outcome = retainedOutcome(completion.outcome)
	}
	// A retained lane keeps its lease: releasing it would let the next staging clear the
	// work that could not be preserved.
	if completion.outcome.stop != stopLaneRetained && !l.isRestartReplaced() {
		endRel := timeHandoffPhase(ctx, "release_examination")
		if err := l.releaseExamination(context.WithoutCancel(ctx), d); err != nil && completion.outcome.err == nil {
			completion.outcome.err = err
		}
		endRel()
	}
	completion.detail = local.Detail
	return completion
}

// Results cross the completion channel only after the worker stops writing them.
// The pass alone accumulates them, so running and judging different lanes never
// race on the loop's result or require the planning goroutine to wait for a sibling.
func mergeExamination(dst, src *LoopResult) {
	dst.Dispatched += src.Dispatched
	dst.Landed += src.Landed
	dst.TrunkAcquitted += src.TrunkAcquitted
	dst.ConflictFixes += src.ConflictFixes
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
// liveForks reads works, the pending runs its caller already folded (and, for a
// claim, validated against the event high-water), rather than folding them again.
func liveForks(r *store.Reader, at time.Time, works map[string]*pendingWork) ([]plannedFork, error) {
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
		forks[item.ItemID] = item.Delivery.activeFork()
	}
	events, err := r.EventsOfKind(0, run.AllocateKind)
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
	for id, work := range works {
		if !work.Done && forks[id] != "" && !awaitingForkClaim(work) {
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
		if current.Valid && current.String == owner.run && forks[owner.item] != "" && !awaitingForkClaim(works[owner.item]) {
			out = append(out, plannedFork{owner.item, forks[owner.item]})
		}
	}
	return out, nil
}

// Classification is read-only and holds no fork. Its ready successor must win
// the writer claim before it can reserve that fork, so two classifiers that
// choose the same fork cannot hold it against one another.
func awaitingForkClaim(work *pendingWork) bool {
	if work == nil || work.Decision.Workflow == nil {
		return false
	}
	d := work.Decision
	if d.NodeIndex < 0 || d.NodeIndex >= len(d.Workflow.Stages) {
		return false
	}
	// advanceNode can record the classifier's repository HEAD before the
	// successor claims a fork. A recorded node base is not a fork claim.
	return classificationNode(d.Workflow.Stages[d.NodeIndex]) ||
		(work.Ready && d.NodeIndex == 1 && d.Reason == "workflow-next-node" && d.NodeStartRunID == "" && classificationNode(d.Workflow.Stages[0]))
}

// If staging invalidated a sibling while Git was reading it, retry without that
// sibling. Failures reading an untouched worktree or the target still surface.
func (l *Loop) snapshotIdleSiblings(ctx context.Context, target string, watched []string, epochs map[string]int, opening ...*Boundary) (*Boundary, error) {
	watched = unmovedLanes(watched, epochs, l.idleLaneEpochs())
	for {
		b, err := l.snapshot(ctx, []string{target}, watched, opening...)
		if err == nil {
			return b, nil
		}
		still := unmovedLanes(watched, epochs, l.idleLaneEpochs())
		if len(still) == len(watched) {
			return nil, err
		}
		watched = still
	}
}

// A sibling started after the bracket opened has its own writer and bracket.
// Ignore its status in both snapshots rather than convicting that authorized work.
func (l *Loop) closeAsyncBracket(ctx context.Context, d Decision, at time.Time, before *Boundary, result *LoopResult, watched []string, epochs map[string]int) (string, error) {
	if l.nonLanding {
		return "", nil
	}
	worktree := l.lanePath(d.LaneID)
	after, err := l.snapshotIdleSiblings(ctx, worktree, watched, epochs, before)
	if err != nil {
		return "", err
	}
	now := l.idleLaneEpochs()
	for _, lane := range watched {
		epoch, seen := epochs[lane]
		if current, idle := now[lane]; idle && seen && current == epoch {
			continue
		}
		path := l.lanePath(lane)
		if canonical, err := canonicalDir(path); err == nil {
			path = canonical
		}
		for _, snapshot := range []*Boundary{before, after} {
			snapshot.Lanes = slices.DeleteFunc(snapshot.Lanes, func(p string) bool { return p == path })
			for i := range snapshot.Worktrees {
				if snapshot.Worktrees[i].Path == path {
					snapshot.Worktrees[i].Status = nil
				}
			}
		}
	}
	return l.finishRunBracket(ctx, worktree, *d.RunRequest, d.RunRequest.CorrelationID, at, before, after, result)
}
