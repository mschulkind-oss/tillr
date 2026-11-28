package procguard

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Event kind constants shared between orphan reaping, worker listing, and lane-children checks.
const (
	DispatchStartedKind     = "drain.started"
	NodeAdvancedKind        = "drain.node-advanced"
	GraphChangedKind        = "drain.graph-changed"
	ExaminationClaimedKind  = "drain.examination-claimed"
	ExaminationOwnerKind    = "drain.examination.owner"
	ExaminationReleasedKind = "drain.examination-released"
	ExaminationDoneKind     = "drain.examined"
	LaneReleasedKind        = "drain.lane-released"
	LoopOrphanedKind        = "drain.orphaned"
	LaneReleaseReap         = "reap"
)

type laneReleasedPayload struct {
	LaneID string `json:"lane_id"`
	Cause  string `json:"cause,omitempty"`
}

type orphanedRunEffectPayload struct {
	ResumeAfterRestart bool `json:"resume_after_restart,omitempty"`
}

// LoopOwner identifies one OS process without mistaking a reused PID for it.
// Domain binds the evidence to the boot and PID namespace where it was observed.
type LoopOwner = presence.Owner

var (
	ProcessStart          = presence.ProcessStart
	CurrentLoopOwner      = presence.CurrentOwner
	OwnerReads            = &presence.OwnerReads
	ProcessGone           = presence.ProcessGone
	OwnerDead             = presence.OwnerDead
	ProcessStoreIdentity  = presence.StoreIdentity
	RunProcessEnvironment = presence.RunProcessEnvironment
)

// A loop can die while its worker survives. Do not release that worker's tree.
// The run face and its descendants carry both the run ID and the store identity.
func WithoutProcessEnv(env []string, keys ...string) []string {
	out := []string{}
	for _, value := range env {
		keep := true
		for _, key := range keys {
			if strings.HasPrefix(value, key+"=") {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, value)
		}
	}
	return out
}

// ProcScans counts full process-table scans, so a test can pin how many a pass makes.
var ProcScans atomic.Int64

// RunFaceOwner identifies one marked run process (face or worker) by PID, start time,
// and run ID.
type RunFaceOwner struct {
	LoopOwner
	Resume bool
	RunID  string
}

// LiveRunFaces finds only the run face, not its adapter descendants.
func LiveRunFaces(path, id string, here *LoopOwner) ([]*RunFaceOwner, error) {
	return markedRunProcesses(path, id, here, true)
}

// LiveRunWorkers finds every process of this user carrying the run and store
// markers: the adapter and its descendants when the run face is gone. Each is
// identified by pid and start time, so a recycled pid is rejected before a kill.
func LiveRunWorkers(path, id string, here *LoopOwner) ([]*RunFaceOwner, error) {
	return markedRunProcesses(path, id, here, false)
}

// IsRunFaceArgv answers whether a process's raw cmdline is a run face's: `tillr-run exec`
// with the --input and --lane-root flags ProcessSteps.Run passes. An adapter and its
// children inherit the run's markers but never this argv, which is how a face is told
// from what it started.
func IsRunFaceArgv(cmdline []byte) bool {
	args := bytes.Split(cmdline, []byte{0})
	if len(args) < 3 || string(args[1]) != "exec" {
		return false
	}
	flags := string(bytes.Join(args[2:], []byte{' '}))
	return strings.Contains(flags, "--input ") && strings.Contains(flags, "--lane-root ")
}

func markedRunProcesses(path, id string, here *LoopOwner, facesOnly bool) ([]*RunFaceOwner, error) {
	path, err := ProcessStoreIdentity(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var owners []*RunFaceOwner
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		start, err := ProcessStart(pid)
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if start == "" {
			continue
		}
		env, err := RunProcessEnvironment(dir, here.UID)
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		hasRun, hasStore, resume := false, false, false
		var runID string
		for _, v := range bytes.Split(env, []byte{0}) {
			if strings.HasPrefix(string(v), "TILLR_RUN_ID=") {
				runID = strings.TrimPrefix(string(v), "TILLR_RUN_ID=")
				hasRun = runID != "" && (id == "" || runID == id)
			}
			hasStore = hasStore || string(v) == "TILLR_LOOP_STORE="+path
			resume = resume || string(v) == "TILLR_RUN_RESUME=true"
		}
		if !hasRun || !hasStore {
			continue
		}
		argv, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if facesOnly && !IsRunFaceArgv(argv) {
			continue
		}
		owner := *here
		owner.PID, owner.Start = pid, start
		owners = append(owners, &RunFaceOwner{LoopOwner: owner, Resume: resume, RunID: runID})
	}
	return owners, nil
}

// LiveRunIDs answers, from one process-table scan, every run ID a live process
// carries for this store: run faces and their adapters inherit both markers.
func LiveRunIDs(storePath string) (map[string]bool, error) {
	live, _, err := LiveRunProcessIDs(storePath)
	return live, err
}

// LiveRunProcessIDs is LiveRunIDs and, from the same scan, the run IDs whose run face
// itself is alive (IsRunFaceArgv), as opposed to only an adapter or a stray child of one.
func LiveRunProcessIDs(storePath string) (live, faces map[string]bool, err error) {
	storePath, err = ProcessStoreIdentity(storePath)
	if err != nil {
		return nil, nil, err
	}
	ProcScans.Add(1)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, nil, err
	}
	live, faces = map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := RunProcessEnvironment(filepath.Join("/proc", entry.Name()), uint32(os.Geteuid()))
		if ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("checking process %d before recovering runs: %w", pid, err)
		}
		runID, hasStore := "", false
		for _, v := range bytes.Split(env, []byte{0}) {
			if id, ok := strings.CutPrefix(string(v), "TILLR_RUN_ID="); ok {
				runID = id
			}
			hasStore = hasStore || string(v) == "TILLR_LOOP_STORE="+storePath
		}
		if !hasStore || runID == "" {
			continue
		}
		live[runID] = true
		if !faces[runID] {
			argv, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
			if ProcessGone(err) {
				continue
			}
			if err != nil {
				return nil, nil, fmt.Errorf("checking process %d before recovering runs: %w", pid, err)
			}
			faces[runID] = IsRunFaceArgv(argv)
		}
	}
	return live, faces, nil
}

// ReapEvidence is the process-table evidence a pass reaps from, gathered before its
// write transaction opens. An owner absent from Dead was not judged, so it is not
// proven dead and its claim keeps its protection.
type ReapEvidence[W any] struct {
	Dead map[LoopOwner]bool
	// A prior session of this same PID can also lose its waiters (exec). Key
	// that evidence by run and session, never by PID: new claims are not dead.
	Prior map[string]string
	// A gap-capable serial workflow node with a staged session and output can
	// continue even when its detached face vanished without a pause record.
	RestartSessions map[string]bool
	// Unlaunched marks serial claims interrupted before the loop even opened
	// the run face's streams. No face could have started from those claims.
	Unlaunched map[string]bool
	Live       map[string]bool
	// Faces is every run ID whose run face itself is alive (IsRunFaceArgv), from the same
	// scan as Live. Only a face keeps a dead examiner's lease: a stray child that merely
	// carries the run's markers is not the run.
	Faces map[string]bool
	// Stuck is each examined run whose lane is still leased to it with no expiry and
	// whose run face is gone (StuckExaminationLease), keyed by run ID to its lane. The
	// write re-verifies every one before it reclaims it.
	Stuck map[string]string
	// ResumeStuck is each run whose last recorded event is a run.resume that nothing
	// finished, whose lane is still leased to it with no expiry, and whose run face is
	// gone (orphanedResume), keyed by run ID to its lane.
	ResumeStuck map[string]string
	// OffRotation is each unfinished run that ended with its lane still leased to it with
	// no expiry, on a lane this loop does not run (OffRotationStranded), whose run face is
	// gone and whose claimants are dead, absent or this loop, keyed by run ID to its lane.
	// The write re-verifies every one before it reclaims it.
	OffRotation map[string]string
	// Reclaimed collects the lines the write's reclaims print once its transaction has
	// closed: no report is written inside a write transaction.
	Reclaimed *[]string
	// ReapedSingle collects single-worker runs reaped from a dead loop so the caller
	// can finish their preservation, lane reset, and examination in Pass 1.
	ReapedSingle *[]W
}

func (ev ReapEvidence[W]) OwnerDead(owner *LoopOwner) bool {
	return owner != nil && ev.Dead[*owner]
}

// PendingWorkView is the narrow read-only projection of a pending work entry needed
// by GatherReapEvidence and ReapDeadLoopRuns without importing package drain.
type PendingWorkView[W any] struct {
	Raw               W
	ItemID            string
	LaneID            string
	RunID             string
	ParentRunID       string
	ReadOnlyGroup     string
	LoopID            string
	Owner             *LoopOwner
	Examiner          *LoopOwner
	Done              bool
	Ready             bool
	Examining         bool
	Orphaned          bool
	LaneReleased      bool
	HasWorkflow       bool
	HasParallel       bool
	ParallelRunIDs    []string
	SingleRecoverable bool
	StampedBackend    string
	StampedSeat       string
	StampedWorkflow   string
	StampedNode       string
}

// GatherHost provides the loop capabilities needed by GatherReapEvidence.
type GatherHost[W any] struct {
	StorePath                     string
	CurrentSessionID              func() string
	IsActive                      func(runID string) bool
	IsRotatedLane                 func(lane string) bool
	IsDocumentDesk                func(lane string) bool
	ReadPending                   func(r *store.Reader) ([]PendingWorkView[W], error)
	TerminalRecord                func(r *store.Reader, runID string) (*run.Record, error)
	HasInterruptedWorkflowSession func(ctx context.Context, raw W) (bool, error)
	RunOutputFiles                func(runID string) []string
}

// ReapTxHost provides the transactional capabilities needed by ReapDeadLoopRuns.
type ReapTxHost[W any] struct {
	ReadPending    func(r *store.Reader) ([]PendingWorkView[W], error)
	TerminalRecord func(r *store.Reader, runID string) (*run.Record, error)
	AppendEffect   func(tx *store.Tx, kind, objectID string, payload any, at time.Time) error
	ReapParallel   func(ctx context.Context, tx *store.Tx, raw W, ev ReapEvidence[W]) (bool, error)
	RetractClaim   func(tx *store.Tx, raw W, at time.Time) error
	CloneRaw       func(raw W) W
	Reportf        func(format string, args ...any)
}

func DispatchSessionEnded(r *store.Reader, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var ended bool
	err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM drain_sessions WHERE session_id = ? AND ended_at IS NOT NULL)`, id).Scan(&ended)
	return ended, err
}

// GatherReapEvidence reads the pending claims, then judges their owners and scans
// the process table once, with no transaction open. The caller holds the run-start
// lock from here through its write, so no run face starts in between.
func GatherReapEvidence[W any](ctx context.Context, s *store.Store, h GatherHost[W]) (ReapEvidence[W], error) {
	ev := ReapEvidence[W]{
		Dead:            map[LoopOwner]bool{},
		Prior:           map[string]string{},
		RestartSessions: map[string]bool{},
		Unlaunched:      map[string]bool{},
		Live:            map[string]bool{},
		Faces:           map[string]bool{},
		Stuck:           map[string]string{},
		ResumeStuck:     map[string]string{},
		OffRotation:     map[string]string{},
		Reclaimed:       &[]string{},
		ReapedSingle:    &[]W{},
	}
	var owners []LoopOwner
	stuck := map[string]PendingWorkView[W]{}
	resumed := map[string]PendingWorkView[W]{}
	stranded := map[string]PendingWorkView[W]{}
	here, err := CurrentLoopOwner()
	if err != nil {
		return ev, err
	}
	// foreignRuns maps each run of a claim whose owner or examiner is this user's loop in
	// another process domain (a relaunched jail) to that owner's domain: this loop's
	// process table cannot see those runs, so their liveness is judged from their stream
	// files (presence.go) or boot mismatch.
	foreignRuns := map[string]string{}
	var ended map[LoopOwner]bool
	priorWork := map[string]PendingWorkView[W]{}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := h.ReadPending(r)
		if err != nil {
			return err
		}
		unbounded, err := LeasesWithNoExpiry(r)
		if err != nil {
			return err
		}
		endedDispatches := map[string]bool{}
		for _, work := range works {
			if work.RunID != "" && !work.Done && !work.Ready && !work.Examining && work.Examiner == nil &&
				work.Owner != nil && *work.Owner == *here && work.LoopID != h.CurrentSessionID() && !h.IsActive(work.RunID) {
				endedSession, seen := endedDispatches[work.LoopID]
				if !seen {
					endedSession, err = DispatchSessionEnded(r, work.LoopID)
					if err != nil {
						return err
					}
					endedDispatches[work.LoopID] = endedSession
				}
				if endedSession {
					ev.Prior[work.RunID] = work.LoopID
					priorWork[work.RunID] = work
				}
			}
			if work.RunID != "" && (FromOtherDomain(work.Owner, here) || FromOtherDomain(work.Examiner, here)) {
				ownerDomain := ""
				if FromOtherDomain(work.Owner, here) {
					ownerDomain = work.Owner.Domain
				} else if FromOtherDomain(work.Examiner, here) {
					ownerDomain = work.Examiner.Domain
				}
				foreignRuns[work.RunID] = ownerDomain
				foreignRuns[work.ParentRunID] = ownerDomain
				for _, pid := range work.ParallelRunIDs {
					foreignRuns[pid] = ownerDomain
				}
			}
			if work.Done && work.RunID != "" && !h.IsActive(work.RunID) {
				if unbounded[work.RunID] == work.LaneID {
					stuck[work.RunID] = work
					if work.Examiner != nil {
						owners = append(owners, *work.Examiner)
					}
				}
			}
			if work.RunID != "" && !work.Done && !work.Examining && (work.Owner == nil || (here != nil && *work.Owner == *here)) && !work.HasParallel && !h.IsActive(work.RunID) && unbounded[work.RunID] == work.LaneID {
				// A continuation's run.resume event clears the dispatch owner, so no
				// owner's death can ever release its lane. Whether the resume is still
				// the run's last word is read here; the face is judged below.
				if open, err := ResumeUnfinished(r, work.RunID); err != nil {
					return err
				} else if open {
					resumed[work.RunID] = work
				}
			}
			if (work.Done && !work.Examining) || work.RunID == "" {
				continue
			}
			if OffRotationStranded(work, unbounded, h.IsActive, h.IsDocumentDesk, h.IsRotatedLane) {
				if rec, err := h.TerminalRecord(r, work.RunID); err != nil {
					return err
				} else if rec != nil {
					stranded[work.RunID] = work
				}
			}
			for _, owner := range []*LoopOwner{work.Owner, work.Examiner} {
				if owner != nil {
					owners = append(owners, *owner)
				}
			}
		}
		delete(foreignRuns, "")
		if len(foreignRuns) > 0 {
			if ended, err = EndedSessionOwners(r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return ev, err
	}
	identity := ""
	if len(foreignRuns) > 0 {
		if identity, err = ProcessStoreIdentity(h.StorePath); err != nil {
			return ev, err
		}
	}
	anyDead := false
	for _, owner := range owners {
		if _, seen := ev.Dead[owner]; seen {
			continue
		}
		o := owner
		var dead bool
		if FromOtherDomain(&o, here) {
			dead = ForeignOwnerGone(identity, o, *here, ended)
		} else if dead, err = OwnerDead(&o, here); err != nil {
			return ev, err
		}
		ev.Dead[owner] = dead
		anyDead = anyDead || dead
	}
	if anyDead || len(ev.Prior) > 0 || len(stuck) > 0 || len(resumed) > 0 || len(stranded) > 0 {
		if ev.Live, ev.Faces, err = LiveRunProcessIDs(h.StorePath); err != nil {
			return ev, err
		}
		// A run started from another domain is live, and its face counted live, while
		// its streams are written: added to what this namespace's scan saw, never
		// subtracted from it.
		for id, ownerDomain := range foreignRuns {
			if ForeignRunLive(h.RunOutputFiles, id, ownerDomain, here.Domain) {
				ev.Live[id], ev.Faces[id] = true, true
			}
		}
		for id, work := range priorWork {
			if !ev.Live[id] {
				if ready, err := h.HasInterruptedWorkflowSession(ctx, work.Raw); err != nil {
					return ev, err
				} else if ready {
					ev.RestartSessions[id] = true
				} else if !work.HasParallel && MissingRunStreams(h.RunOutputFiles, id) {
					ev.Unlaunched[id] = true
				}
			}
		}
		for id, work := range stuck {
			if StuckExaminationLease(work.Examiner, ev.Faces[id], here, ev) {
				ev.Stuck[id] = work.LaneID
			}
		}
		for id, work := range resumed {
			if !ev.Faces[id] && !(h.RunOutputFiles != nil && ForeignRunLive(h.RunOutputFiles, id, foreignRuns[id], here.Domain)) {
				ev.ResumeStuck[id] = work.LaneID
			}
		}
		for id, work := range stranded {
			if !ev.Faces[id] && !(h.RunOutputFiles != nil && ForeignRunLive(h.RunOutputFiles, id, foreignRuns[id], here.Domain)) && OffRotationClaimGone(work.Owner, work.Examiner, here, ev) {
				ev.OffRotation[id] = work.LaneID
			}
		}
	}
	return ev, nil
}

// OffRotationStranded answers whether an unfinished run holds a lane this loop does not run
// under a lease with no expiry.
func OffRotationStranded[W any](work PendingWorkView[W], unbounded map[string]string, isActive func(string) bool, isDocumentDesk func(string) bool, isRotatedLane func(string) bool) bool {
	if work.Done || work.LaneReleased || work.RunID == "" || work.HasParallel {
		return false
	}
	if isActive(work.RunID) || unbounded[work.RunID] != work.LaneID || isDocumentDesk(work.LaneID) {
		return false
	}
	return !isRotatedLane(work.LaneID)
}

// OffRotationClaimGone answers whether nobody who could still be working an off-rotation run
// holds a claim on it: its dispatch owner and its examiner are absent, proven dead, or this
// loop, which already knows the run is not in flight (OffRotationStranded).
func OffRotationClaimGone[W any](owner, examiner *LoopOwner, here *LoopOwner, ev ReapEvidence[W]) bool {
	for _, o := range []*LoopOwner{owner, examiner} {
		if o == nil || ev.OwnerDead(o) {
			continue
		}
		if o.PID == here.PID && o.Start == here.Start && o.Domain == here.Domain {
			continue
		}
		return false
	}
	return true
}

// ReclaimOffRotationLease releases the lane of a run that ended on a lane outside the loop's
// rotation, guarded by the exact lease the evidence saw, and clears the claim that kept it.
func ReclaimOffRotationLease[W any](tx *store.Tx, work PendingWorkView[W], at time.Time, h ReapTxHost[W]) error {
	id, lane := work.RunID, work.LaneID
	res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, lane, id, run.LaneStateLeased)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if work.Examiner != nil {
		if err := h.AppendEffect(tx, ExaminationOwnerKind, id, (*LoopOwner)(nil), at); err != nil {
			return err
		}
	}
	if err := h.AppendEffect(tx, LaneReleasedKind, id, laneReleasedPayload{LaneID: lane, Cause: LaneReleaseReap}, at); err != nil {
		return err
	}
	if h.Reportf != nil {
		h.Reportf("loop: reclaimed lane %s: run %s ended there, the lane is outside this loop's rotation, and nothing holds the run; it settles where it ended\n", lane, id)
	}
	return nil
}

// RestartClaimOverEndedRun answers whether the run's latest dispatch claim is a workflow
// restart recorded after the run's terminal record. A restarted evaluator node keeps its run
// id (pendingDecision), so after a relaunch reclaims a live verify run, the restart's claim
// leases the lane to a run whose terminal record is already written. When that claim's loop
// dies before the run starts, the terminal record speaks for the earlier segment only: on
// 2026-11-28 the reaper read it as the claim's end and kept the claim's lease, and the item
// sat `running  no-lane` behind a run that did not exist across every later restart.
func RestartClaimOverEndedRun(r interface {
	QueryRow(string, ...any) *sql.Row
}, id string) (bool, error) {
	var ended sql.NullInt64
	if err := r.QueryRow(`SELECT event_seq FROM runs WHERE run_id = ?`, id).Scan(&ended); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	var claimed int64
	var restart sql.NullBool
	err := r.QueryRow(`SELECT seq, json_extract(payload, '$.decision.workflow_restart') FROM events
		WHERE object_type = 'run' AND object_id = ? AND kind = ? ORDER BY seq DESC LIMIT 1`, id, DispatchStartedKind).Scan(&claimed, &restart)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return restart.Valid && restart.Bool && ended.Valid && ended.Int64 > 0 && claimed > ended.Int64, nil
}

// ResumeUnfinished answers whether the run's last run.resume or run.finish event is a
// run.resume: a continuation was claimed and has recorded no end since.
func ResumeUnfinished(r interface {
	QueryRow(string, ...any) *sql.Row
}, id string) (bool, error) {
	var kind string
	err := r.QueryRow(`SELECT kind FROM events WHERE object_type = 'run' AND object_id = ? AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`, id, run.ResumeKind, run.FinishKind).Scan(&kind)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return kind == run.ResumeKind, err
}

// ReclaimOrphanedResume releases the lane of a continuation whose run face died with
// nothing recorded (orphanedResume), guarded by the exact lease and the same last event the
// evidence saw.
func ReclaimOrphanedResume[W any](tx *store.Tx, work PendingWorkView[W], at time.Time, h ReapTxHost[W]) error {
	id, lane := work.RunID, work.LaneID
	if open, err := ResumeUnfinished(tx, id); err != nil || !open {
		return err
	}
	res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, lane, id, run.LaneStateLeased)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if err := h.AppendEffect(tx, LaneReleasedKind, id, laneReleasedPayload{LaneID: lane, Cause: LaneReleaseReap}, at); err != nil {
		return err
	}
	if h.Reportf != nil {
		h.Reportf("loop: reclaimed lane %s: run %s resumed after a restart and its run face is gone with no end recorded; it settles and resumes again\n", lane, id)
	}
	return nil
}

// LeasesWithNoExpiry answers every lane leased with no expiry, keyed by the run it is
// leased to: the shape an examination's claim leaves, which ReapExpiredLeases never
// reclaims.
func LeasesWithNoExpiry(r *store.Reader) (map[string]string, error) {
	rows, err := r.Query(`SELECT run_id, lane_id FROM lanes WHERE state = ? AND run_id IS NOT NULL AND leased_until IS NULL`, run.LaneStateLeased)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	leases := map[string]string{}
	for rows.Next() {
		var runID, lane string
		if err := rows.Scan(&runID, &lane); err != nil {
			return nil, err
		}
		leases[runID] = lane
	}
	return leases, rows.Err()
}

// StuckExaminationLease answers whether an examined run's lease is stranded: its
// examination is recorded done, no examination in this loop holds it (the caller excluded
// l.active), its examiner is absent, dead or this loop, and its run face is gone.
func StuckExaminationLease[W any](examiner *LoopOwner, faceLive bool, here *LoopOwner, ev ReapEvidence[W]) bool {
	if faceLive {
		return false
	}
	return examiner == nil || ev.OwnerDead(examiner) || (examiner.PID == here.PID && examiner.Start == here.Start && examiner.Domain == here.Domain)
}

// ReclaimStuckLease releases an examined run's stranded lane (StuckExaminationLease), guarded
// by the exact lease the evidence saw, and records it as drain.lane-released with cause reap.
func ReclaimStuckLease[W any](tx *store.Tx, work PendingWorkView[W], at time.Time, ev ReapEvidence[W], h ReapTxHost[W]) error {
	id, lane := work.RunID, work.LaneID
	res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, lane, id, run.LaneStateLeased)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if err := h.AppendEffect(tx, LaneReleasedKind, id, laneReleasedPayload{LaneID: lane, Cause: LaneReleaseReap}, at); err != nil {
		return err
	}
	*ev.Reclaimed = append(*ev.Reclaimed, fmt.Sprintf("loop: reclaimed lane %s: run %s was examined but its lane was still leased to it, and its run face is gone\n", lane, id))
	return nil
}

// ReapDeadLoopRuns closes only proven-dead loop-owned claims. A claim from another
// process domain is proven dead only by the evidence presence.go reads; legacy leases,
// unjudged owners and claims whose worker is still alive keep their protection.
//
// It reads no process table: every liveness answer comes from ev, gathered before
// the transaction, and each write is guarded by the exact claim it re-reads here.
func ReapDeadLoopRuns[W any](ctx context.Context, tx *store.Tx, at time.Time, ev ReapEvidence[W], h ReapTxHost[W]) error {
	if len(ev.Dead) > 0 {
		sRows, err := tx.Query(`SELECT session_id, loop_owner FROM drain_sessions WHERE ended_at IS NULL AND loop_owner IS NOT NULL`)
		if err != nil {
			return err
		}
		var deadSessions []string
		for sRows.Next() {
			var sid string
			var raw []byte
			if err := sRows.Scan(&sid, &raw); err != nil {
				sRows.Close()
				return err
			}
			var owner LoopOwner
			if err := json.Unmarshal(raw, &owner); err == nil && ev.Dead[owner] {
				deadSessions = append(deadSessions, sid)
			}
		}
		if err := sRows.Err(); err != nil {
			sRows.Close()
			return err
		}
		sRows.Close()
		for _, sid := range deadSessions {
			if _, err := tx.Exec(`UPDATE drain_sessions SET ended_at = ? WHERE session_id = ?`, at.UnixMilli(), sid); err != nil {
				return err
			}
		}
	}
	works, err := h.ReadPending(tx.Reader())
	if err != nil {
		return err
	}
	for _, work := range works {
		if work.RunID != "" {
			if lane, ok := ev.ResumeStuck[work.RunID]; ok && lane == work.LaneID {
				if err := ReclaimOrphanedResume(tx, work, at, h); err != nil {
					return err
				}
			}
		}
		if work.RunID != "" {
			if lane, ok := ev.OffRotation[work.RunID]; ok && lane == work.LaneID {
				if err := ReclaimOffRotationLease(tx, work, at, h); err != nil {
					return err
				}
			}
		}
		if work.Done && work.RunID != "" {
			if lane, ok := ev.Stuck[work.RunID]; ok && lane == work.LaneID {
				if err := ReclaimStuckLease(tx, work, at, ev, h); err != nil {
					return err
				}
			}
		}
		if (work.Done && !work.Examining) || work.RunID == "" {
			continue
		}
		if work.Examining && work.HasParallel {
			live := false
			for _, pid := range work.ParallelRunIDs {
				live = live || ev.Faces[pid]
			}
			if live {
				continue
			}
		}
		if work.Examiner != nil {
			if ev.OwnerDead(work.Examiner) {
				// An examination is claimed only for a run that has ended, so the run face
				// is the one process that can still own this lease (a continuation resuming
				// the run). A stray child carrying the run's markers does not: on 2026-11-17
				// one kept a dead examiner's lane leased, and its candidate unjudged, for
				// hours (TestADeadExaminersCandidateIsJudgedInItsOwnLaneDespiteAStrayChild).
				if !ev.Faces[work.RunID] {
					res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ?`, run.LaneStateIdle, work.LaneID, work.RunID)
					if err != nil {
						return err
					}
					if err := h.AppendEffect(tx, ExaminationOwnerKind, work.RunID, (*LoopOwner)(nil), at); err != nil {
						return err
					}
					if work.Examining {
						if err := h.AppendEffect(tx, ExaminationReleasedKind, work.RunID, struct{}{}, at); err != nil {
							return err
						}
					}
					if n, err := res.RowsAffected(); err != nil {
						return err
					} else if n > 0 {
						if err := h.AppendEffect(tx, LaneReleasedKind, work.RunID, laneReleasedPayload{LaneID: work.LaneID, Cause: LaneReleaseReap}, at); err != nil {
							return err
						}
					}
				}
			}
		}
		id := work.RunID
		priorSession := ev.Prior[id] != "" && ev.Prior[id] == work.LoopID && !work.Ready && !work.Examining && work.Examiner == nil
		if !ev.OwnerDead(work.Owner) && !priorSession {
			continue
		}
		if !work.Examining {
			if handled, err := h.ReapParallel(ctx, tx, work.Raw, ev); err != nil {
				return err
			} else if handled {
				continue
			}
		}
		if ev.Live[id] {
			continue
		}
		markOrphaned := func() error {
			effect := orphanedRunEffectPayload{ResumeAfterRestart: priorSession && ev.RestartSessions[id]}
			if err := h.AppendEffect(tx, LoopOrphanedKind, id, effect, at); err != nil {
				return err
			}
			if priorSession && !effect.ResumeAfterRestart && ev.Reclaimed != nil {
				*ev.Reclaimed = append(*ev.Reclaimed, fmt.Sprintf("loop: restarting run %s on %s: its run face vanished across the loop restart and no resumable session was confirmed\n", id, work.LaneID))
			}
			return nil
		}
		if work.Examining {
			// Examination is not another execution of the node. Recover its
			// lease without inventing a terminal node record or spending a run.
			if work.ParentRunID != "" && ev.Live[work.ParentRunID] {
				continue
			}
			if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id IN (?,?,?) AND state=?`, run.LaneStateIdle, work.LaneID, id, work.ParentRunID, work.ReadOnlyGroup, run.LaneStateLeased); err != nil {
				return err
			}
			if err := h.AppendEffect(tx, ExaminationReleasedKind, id, struct{}{}, at); err != nil {
				return err
			}
			continue
		}
		var terminal int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id = ?`, id).Scan(&terminal); err != nil {
			return err
		}
		if terminal > 0 {
			// A restarted evaluator node keeps its run id, so its restart claim names a run
			// that already ended. That earlier end is not this claim's: the claim is
			// reaped as one with no terminal record (below), or its lease outlives it.
			if over, err := RestartClaimOverEndedRun(tx.Reader(), id); err != nil {
				return err
			} else if over {
				terminal = 0
			}
		}
		if terminal > 0 {
			resumed, err := RunHasActiveResume(tx.Reader(), id)
			if err != nil {
				return err
			}
			if resumed {
				result, err := tx.Exec(`UPDATE lanes SET leased_until = ? WHERE lane_id = ? AND run_id = ? AND state = ?`, at.UnixMilli(), work.LaneID, id, run.LaneStateLeased)
				if err != nil {
					return err
				}
				n, err := result.RowsAffected()
				if err != nil {
					return err
				}
				if n > 0 {
					if _, err := run.ReapExpiredLeases(ctx, tx, at); err != nil {
						return err
					}
				}
				prev, err := h.TerminalRecord(tx.Reader(), id)
				if err != nil {
					return err
				}
				backend := ""
				var route, harness, seat, workflow, node, startedAt string
				var artifactRefs []run.ArtifactRef
				var usage *run.Usage
				if prev != nil {
					backend = prev.Backend
					route = prev.Route
					harness = prev.Harness
					seat = prev.Seat
					workflow = prev.Workflow
					node = prev.Node
					startedAt = prev.StartedAt
					artifactRefs = prev.ArtifactRefs
					usage = prev.Usage
				}
				if work.StampedBackend != "" {
					backend = work.StampedBackend
				}
				if seat == "" {
					seat = work.StampedSeat
				}
				if workflow == "" {
					workflow = work.StampedWorkflow
				}
				if node == "" {
					node = work.StampedNode
				}
				if startedAt == "" {
					startedAt = at.UTC().Format(time.RFC3339)
				}
				if usage != nil && usage.Tokens == nil && usage.CostUSD == nil && usage.Details == nil {
					usage = nil
				}
				interruptedRec := run.Record{
					RunID:        id,
					ItemID:       work.ItemID,
					LaneID:       work.LaneID,
					Backend:      backend,
					Route:        route,
					Harness:      harness,
					Seat:         seat,
					Workflow:     workflow,
					Node:         node,
					State:        run.StateTimedOut,
					EndCause:     run.EndLeaseReclaimed,
					Resumed:      true,
					StartedAt:    startedAt,
					FinishedAt:   at.UTC().Format(time.RFC3339),
					ArtifactRefs: artifactRefs,
					Usage:        usage,
				}
				if interruptedRec.Usage == nil {
					interruptedRec.SpendStatus = "unreported"
					interruptedRec.SpendNote = "the resumed run face left no terminal record; its lease was reclaimed"
				}
				body, err := json.Marshal(interruptedRec)
				if err != nil {
					return err
				}
				if _, err := run.Apply(ctx, tx, record.Envelope{
					RecordVersion: record.SupportedVersion,
					Kind:          run.RecordKind,
					ID:            "run-record-" + id + "-reap",
					Producer:      record.Producer{System: record.System, Utility: drainmodel.Utility, Version: drainmodel.Version},
					CreatedAt:     at.UTC(),
					Subject:       &record.Subject{Kind: "run", ID: id},
					Payload:       body,
				}); err != nil {
					return err
				}
				if err := markOrphaned(); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ? AND outcome = 'running'`, id); err != nil {
					return err
				}
				if ev.ReapedSingle != nil && work.SingleRecoverable {
					*ev.ReapedSingle = append(*ev.ReapedSingle, h.CloneRaw(work.Raw))
				}
				continue
			}
			// A terminal result alone does not establish that the supervisor
			// interrupted it. Ordinary stops intentionally leave workers alive.
			if work.HasWorkflow && !work.Orphaned {
				rec, err := h.TerminalRecord(tx.Reader(), id)
				if err != nil {
					return err
				}
				// The runner can record its signal after the loop has died,
				// leaving no loop-stop marker. Proven owner death and no live
				// worker still make this interrupted node safe to replay.
				if rec != nil && rec.EndCause == run.EndSignal {
					if err := markOrphaned(); err != nil {
						return err
					}
				}
			}
			if work.Orphaned && ev.ReapedSingle != nil && work.SingleRecoverable {
				*ev.ReapedSingle = append(*ev.ReapedSingle, h.CloneRaw(work.Raw))
			}
			continue
		}
		if priorSession && ev.Unlaunched[id] {
			// ClaimLease records an allocation before staging, not proof that a
			// process started. If exec overtook the handoff, restore the offer
			// without inventing a finish or consuming the node's run ID.
			if err := h.RetractClaim(tx, work.Raw, at); err != nil {
				return err
			}
			continue
		}
		// Reap under the same transaction and guarded by this exact claim. No
		// unbounded lease becomes expiring merely because another loop started.
		result, err := tx.Exec(`UPDATE lanes SET leased_until = ? WHERE lane_id = ? AND run_id = ? AND state = ?`, at.UnixMilli(), work.LaneID, id, run.LaneStateLeased)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := run.ReapExpiredLeases(ctx, tx, at); err != nil {
			return err
		}
		if err := markOrphaned(); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ? AND outcome = 'running'`, id); err != nil {
			return err
		}
		if ev.ReapedSingle != nil && work.SingleRecoverable {
			*ev.ReapedSingle = append(*ev.ReapedSingle, h.CloneRaw(work.Raw))
		}
	}
	return nil
}

func RunHasActiveResume(r *store.Reader, id string) (bool, error) {
	var latest string
	err := r.QueryRow(`SELECT kind FROM events WHERE object_type='run' AND object_id=? AND kind IN (?,?) ORDER BY seq DESC LIMIT 1`, id, run.ResumeKind, run.FinishKind).Scan(&latest)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return latest == run.ResumeKind, nil
}

// MissingRunStreams checks whether every stream file returned by locate is absent.
// ProcessSteps opens these streams before Start and keeps them after exit. Only
// their confirmed absence counts; a locator we don't know or an inaccessible
// path cannot distinguish an unlaunched claim from a lost worker.
func MissingRunStreams(locate func(runID string) []string, id string) bool {
	if locate == nil {
		return false
	}
	paths := locate(id)
	for _, path := range paths {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return false
		}
	}
	return len(paths) > 0
}

// HasInterruptedWorkflowSession checks local staging outside the write
// transaction. A staged id alone is not evidence that a worker ever started.
func HasInterruptedWorkflowSession(root, id, lane, laneTarget, briefFile string) (bool, error) {
	if info, err := os.Stat(laneTarget); err != nil || !info.IsDir() {
		return false, nil
	}
	log, err := os.Stat(filepath.Join(lane, id+".log"))
	if err != nil || log.Size() == 0 {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(lane, briefFile)); err != nil {
		return false, nil
	}
	session, err := drainmodel.ReadSessionID(lane)
	if err != nil || session == "" {
		return false, nil
	}
	// A reused lane's session file is not proof of ownership. Staging archived
	// the session of this exact run before handing it off.
	stagedSession, err := drainmodel.ReadSessionID(drainmodel.RunDir(root, id))
	return err == nil && session == stagedSession, nil
}
