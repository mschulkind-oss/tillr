package drain

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

	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// LoopOwner identifies one OS process without mistaking a reused PID for it.
// Domain binds the evidence to the boot and PID namespace where it was observed.
type LoopOwner = presence.Owner

var (
	processStart          = presence.ProcessStart
	currentLoopOwner      = presence.CurrentOwner
	ownerReads            = &presence.OwnerReads
	processGone           = presence.ProcessGone
	ownerDead             = presence.OwnerDead
	processStoreIdentity  = presence.StoreIdentity
	runProcessEnvironment = presence.RunProcessEnvironment
)

// A loop can die while its worker survives. Do not release that worker's tree.
// The run face and its descendants carry both the run ID and the store identity.
func withoutProcessEnv(env []string, keys ...string) []string {
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

// procScans counts full process-table scans, so a test can pin how many a pass makes.
var procScans atomic.Int64

// liveRunIDs answers, from one process-table scan, every run ID a live process
// carries for this store: run faces and their adapters inherit both markers.
func liveRunIDs(storePath string) (map[string]bool, error) {
	live, _, err := liveRunProcessIDs(storePath)
	return live, err
}

// liveRunProcessIDs is liveRunIDs and, from the same scan, the run IDs whose run face
// itself is alive (isRunFaceArgv), as opposed to only an adapter or a stray child of one.
func liveRunProcessIDs(storePath string) (live, faces map[string]bool, err error) {
	storePath, err = processStoreIdentity(storePath)
	if err != nil {
		return nil, nil, err
	}
	procScans.Add(1)
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
		env, err := runProcessEnvironment(filepath.Join("/proc", entry.Name()), uint32(os.Geteuid()))
		if processGone(err) {
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
			if processGone(err) {
				continue
			}
			if err != nil {
				return nil, nil, fmt.Errorf("checking process %d before recovering runs: %w", pid, err)
			}
			faces[runID] = isRunFaceArgv(argv)
		}
	}
	return live, faces, nil
}

// reapEvidence is the process-table evidence a pass reaps from, gathered before its
// write transaction opens. An owner absent from dead was not judged, so it is not
// proven dead and its claim keeps its protection.
type reapEvidence struct {
	dead map[LoopOwner]bool
	live map[string]bool
	// faces is every run ID whose run face itself is alive (isRunFaceArgv), from the same
	// scan as live. Only a face keeps a dead examiner's lease: a stray child that merely
	// carries the run's markers is not the run.
	faces map[string]bool
	// stuck is each examined run whose lane is still leased to it with no expiry and
	// whose run face is gone (stuckExaminationLease), keyed by run ID to its lane. The
	// write re-verifies every one before it reclaims it.
	stuck map[string]string
	// resumeStuck is each run whose last recorded event is a run.resume that nothing
	// finished, whose lane is still leased to it with no expiry, and whose run face is
	// gone (orphanedResume), keyed by run ID to its lane.
	resumeStuck map[string]string
	// reclaimed collects the lines the write's reclaims print once its transaction has
	// closed: no report is written inside a write transaction.
	reclaimed *[]string
	// reapedSingle collects single-worker runs reaped from a dead loop so the caller
	// can finish their preservation, lane reset, and examination in Pass 1.
	reapedSingle *[]*pendingWork
}

func (ev reapEvidence) ownerDead(owner *LoopOwner) bool {
	return owner != nil && ev.dead[*owner]
}

// gatherReapEvidence reads the pending claims, then judges their owners and scans
// the process table once, with no transaction open. The caller holds the run-start
// lock from here through its write, so no run face starts in between.
func (l *Loop) gatherReapEvidence(ctx context.Context, s *store.Store) (reapEvidence, error) {
	ev := reapEvidence{dead: map[LoopOwner]bool{}, live: map[string]bool{}, faces: map[string]bool{}, stuck: map[string]string{}, resumeStuck: map[string]string{}, reclaimed: &[]string{}, reapedSingle: &[]*pendingWork{}}
	var owners []LoopOwner
	stuck := map[string]*pendingWork{}
	resumed := map[string]*pendingWork{}
	here, err := currentLoopOwner()
	if err != nil {
		return ev, err
	}
	// foreignRuns maps each run of a claim whose owner or examiner is this user's loop in
	// another process domain (a relaunched jail) to that owner's domain: this loop's
	// process table cannot see those runs, so their liveness is judged from their stream
	// files (presence.go) or boot mismatch.
	foreignRuns := map[string]string{}
	var ended map[LoopOwner]bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		unbounded, err := leasesWithNoExpiry(r)
		if err != nil {
			return err
		}
		for _, work := range works {
			if work.Decision.RunRequest != nil && (fromOtherDomain(work.Owner, here) || fromOtherDomain(work.Examiner, here)) {
				ownerDomain := ""
				if fromOtherDomain(work.Owner, here) {
					ownerDomain = work.Owner.Domain
				} else if fromOtherDomain(work.Examiner, here) {
					ownerDomain = work.Examiner.Domain
				}
				foreignRuns[work.Decision.RunRequest.RunID] = ownerDomain
				foreignRuns[work.ParentRunID] = ownerDomain
				for _, node := range parallelNodes(work.Decision) {
					if node.RunRequest != nil {
						foreignRuns[node.RunRequest.RunID] = ownerDomain
					}
				}
			}
			if work.Done && work.Decision.RunRequest != nil && !l.active[work.Decision.RunRequest.RunID] {
				if unbounded[work.Decision.RunRequest.RunID] == work.Decision.LaneID {
					stuck[work.Decision.RunRequest.RunID] = work
					if work.Examiner != nil {
						owners = append(owners, *work.Examiner)
					}
				}
			}
			if work.Decision.RunRequest != nil && !work.Done && !work.Examining && (work.Owner == nil || (here != nil && *work.Owner == *here)) && len(work.Decision.Parallel) == 0 && !l.active[work.Decision.RunRequest.RunID] && unbounded[work.Decision.RunRequest.RunID] == work.Decision.LaneID {
				// A continuation's run.resume event clears the dispatch owner, so no
				// owner's death can ever release its lane. Whether the resume is still
				// the run's last word is read here; the face is judged below.
				if open, err := resumeUnfinished(r, work.Decision.RunRequest.RunID); err != nil {
					return err
				} else if open {
					resumed[work.Decision.RunRequest.RunID] = work
				}
			}
			if (work.Done && !work.Examining) || work.Decision.RunRequest == nil {
				continue
			}
			for _, owner := range []*LoopOwner{work.Owner, work.Examiner} {
				if owner != nil {
					owners = append(owners, *owner)
				}
			}
		}
		delete(foreignRuns, "")
		if len(foreignRuns) > 0 {
			if ended, err = endedSessionOwners(r); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return ev, err
	}
	identity := ""
	if len(foreignRuns) > 0 {
		if identity, err = processStoreIdentity(l.cfg.Store); err != nil {
			return ev, err
		}
	}
	anyDead := false
	for _, owner := range owners {
		if _, seen := ev.dead[owner]; seen {
			continue
		}
		o := owner
		var dead bool
		if fromOtherDomain(&o, here) {
			dead = foreignOwnerGone(identity, o, *here, ended)
		} else if dead, err = ownerDead(&o, here); err != nil {
			return ev, err
		}
		ev.dead[owner] = dead
		anyDead = anyDead || dead
	}
	if anyDead || len(stuck) > 0 || len(resumed) > 0 {
		if ev.live, ev.faces, err = liveRunProcessIDs(l.cfg.Store); err != nil {
			return ev, err
		}
		// A run started from another domain is live, and its face counted live, while
		// its streams are written: added to what this namespace's scan saw, never
		// subtracted from it.
		locator, _ := l.cfg.Steps.(runStreamLocator)
		for id, ownerDomain := range foreignRuns {
			if foreignRunLive(locator, id, ownerDomain, here.Domain) {
				ev.live[id], ev.faces[id] = true, true
			}
		}
		for id, work := range stuck {
			if stuckExaminationLease(work, ev.faces[id], here, ev) {
				ev.stuck[id] = work.Decision.LaneID
			}
		}
		for id, work := range resumed {
			if !ev.faces[id] && !(locator != nil && foreignRunLive(locator, id, foreignRuns[id], here.Domain)) {
				ev.resumeStuck[id] = work.Decision.LaneID
			}
		}
	}
	return ev, nil
}

// resumeUnfinished answers whether the run's last run.resume or run.finish event is a
// run.resume: a continuation was claimed and has recorded no end since.
func resumeUnfinished(r interface {
	QueryRow(string, ...any) *sql.Row
}, id string) (bool, error) {
	var kind string
	err := r.QueryRow(`SELECT kind FROM events WHERE object_type = 'run' AND object_id = ? AND kind IN (?, ?) ORDER BY seq DESC LIMIT 1`, id, run.ResumeKind, run.FinishKind).Scan(&kind)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return kind == run.ResumeKind, err
}

// reclaimOrphanedResume releases the lane of a continuation whose run face died with
// nothing recorded (orphanedResume), guarded by the exact lease and the same last event the
// evidence saw. The run keeps its paused record, so the next plan settles it, and a
// settlement of a paused run resumes it again. This is what moves a run a loop restart
// paused, resumed, and then killed again before the resumed segment could write its finish.
func (l *Loop) reclaimOrphanedResume(tx *store.Tx, work *pendingWork, at time.Time) error {
	id, lane := work.Decision.RunRequest.RunID, work.Decision.LaneID
	if open, err := resumeUnfinished(tx, id); err != nil || !open {
		return err
	}
	res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, lane, id, run.LaneStateLeased)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if err := appendEffect(tx, laneReleasedKind, id, laneReleased{LaneID: lane, Cause: laneReleaseReap}, at); err != nil {
		return err
	}
	l.reportf("loop: reclaimed lane %s: run %s resumed after a restart and its run face is gone with no end recorded; it settles and resumes again\n", lane, id)
	return nil
}

// leasesWithNoExpiry answers every lane leased with no expiry, keyed by the run it is
// leased to: the shape an examination's claim leaves, which ReapExpiredLeases never
// reclaims.
func leasesWithNoExpiry(r *store.Reader) (map[string]string, error) {
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

// stuckExaminationLease answers whether an examined run's lease is stranded: its
// examination is recorded done, no examination in this loop holds it (the caller excluded
// l.active), its examiner is absent, dead or this loop, and its run face is gone. A stray
// child of the run's adapter is not a face and does not keep the lease; a live face — a
// continuation resuming the run — does, and releases it itself when it ends.
func stuckExaminationLease(work *pendingWork, faceLive bool, here *LoopOwner, ev reapEvidence) bool {
	if faceLive {
		return false
	}
	examiner := work.Examiner
	return examiner == nil || ev.ownerDead(examiner) || (examiner.PID == here.PID && examiner.Start == here.Start && examiner.Domain == here.Domain)
}

// reclaimStuckLease releases an examined run's stranded lane (stuckExaminationLease), guarded
// by the exact lease the evidence saw, and records it as drain.lane-released with cause reap.
func (l *Loop) reclaimStuckLease(tx *store.Tx, work *pendingWork, at time.Time, ev reapEvidence) error {
	id, lane := work.Decision.RunRequest.RunID, work.Decision.LaneID
	res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, lane, id, run.LaneStateLeased)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	if err := appendEffect(tx, laneReleasedKind, id, laneReleased{LaneID: lane, Cause: laneReleaseReap}, at); err != nil {
		return err
	}
	*ev.reclaimed = append(*ev.reclaimed, fmt.Sprintf("loop: reclaimed lane %s: run %s was examined but its lane was still leased to it, and its run face is gone\n", lane, id))
	return nil
}

// reapDeadLoopRuns closes only proven-dead loop-owned claims. A claim from another
// process domain is proven dead only by the evidence presence.go reads; legacy leases,
// unjudged owners and claims whose worker is still alive keep their protection.
//
// It reads no process table: every liveness answer comes from ev, gathered before
// the transaction, and each write is guarded by the exact claim it re-reads here.
func (l *Loop) reapDeadLoopRuns(ctx context.Context, tx *store.Tx, at time.Time, ev reapEvidence) error {
	if len(ev.dead) > 0 {
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
			if err := json.Unmarshal(raw, &owner); err == nil && ev.dead[owner] {
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
	works, err := readPending(tx.Reader())
	if err != nil {
		return err
	}
	for _, work := range works {
		if work.Decision.RunRequest != nil {
			if lane, ok := ev.resumeStuck[work.Decision.RunRequest.RunID]; ok && lane == work.Decision.LaneID {
				if err := l.reclaimOrphanedResume(tx, work, at); err != nil {
					return err
				}
			}
		}
		if work.Done && work.Decision.RunRequest != nil {
			if lane, ok := ev.stuck[work.Decision.RunRequest.RunID]; ok && lane == work.Decision.LaneID {
				if err := l.reclaimStuckLease(tx, work, at, ev); err != nil {
					return err
				}
			}
		}
		if (work.Done && !work.Examining) || work.Decision.RunRequest == nil {
			continue
		}
		if work.Examining && len(work.Decision.Parallel) > 0 {
			live := false
			for _, node := range parallelNodes(work.Decision) {
				live = live || ev.faces[node.RunRequest.RunID]
			}
			if live {
				continue
			}
		}
		if work.Examiner != nil {
			if ev.ownerDead(work.Examiner) {
				// An examination is claimed only for a run that has ended, so the run face
				// is the one process that can still own this lease (a continuation resuming
				// the run). A stray child carrying the run's markers does not: on 2026-11-17
				// one kept a dead examiner's lane leased, and its candidate unjudged, for
				// hours (TestADeadExaminersCandidateIsJudgedInItsOwnLaneDespiteAStrayChild).
				if !ev.faces[work.Decision.RunRequest.RunID] {
					res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ?`, run.LaneStateIdle, work.Decision.LaneID, work.Decision.RunRequest.RunID)
					if err != nil {
						return err
					}
					if err := appendEffect(tx, examinationOwnerKind, work.Decision.RunRequest.RunID, (*LoopOwner)(nil), at); err != nil {
						return err
					}
					if n, err := res.RowsAffected(); err != nil {
						return err
					} else if n > 0 {
						if err := appendEffect(tx, laneReleasedKind, work.Decision.RunRequest.RunID, laneReleased{LaneID: work.Decision.LaneID, Cause: laneReleaseReap}, at); err != nil {
							return err
						}
					}
				}
			}
		}
		if !ev.ownerDead(work.Owner) {
			continue
		}
		if !work.Examining {
			if handled, err := l.reapParallel(ctx, tx, work, ev); err != nil {
				return err
			} else if handled {
				continue
			}
		}
		id := work.Decision.RunRequest.RunID
		if ev.live[id] {
			continue
		}
		if work.Examining {
			// Examination is not another execution of the node. Recover its
			// lease without inventing a terminal node record or spending a run.
			if work.ParentRunID != "" && ev.live[work.ParentRunID] {
				continue
			}
			if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id IN (?,?,?) AND state=?`, run.LaneStateIdle, work.Decision.LaneID, id, work.ParentRunID, work.Decision.RunRequest.ReadOnlyGroup, run.LaneStateLeased); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationReleasedKind, id, struct{}{}, at); err != nil {
				return err
			}
			continue
		}
		var terminal int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id = ?`, id).Scan(&terminal); err != nil {
			return err
		}
		if terminal > 0 {
			resumed, err := runHasActiveResume(tx.Reader(), id)
			if err != nil {
				return err
			}
			if resumed {
				result, err := tx.Exec(`UPDATE lanes SET leased_until = ? WHERE lane_id = ? AND run_id = ? AND state = ?`, at.UnixMilli(), work.Decision.LaneID, id, run.LaneStateLeased)
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
				prev, err := terminalRecord(tx.Reader(), id)
				if err != nil {
					return err
				}
				backend := ""
				var route, harness, startedAt string
				var artifactRefs []run.ArtifactRef
				var usage *run.Usage
				if prev != nil {
					backend = prev.Backend
					route = prev.Route
					harness = prev.Harness
					startedAt = prev.StartedAt
					artifactRefs = prev.ArtifactRefs
					usage = prev.Usage
				}
				if work.Decision.RunRequest != nil && work.Decision.RunRequest.Backend != "" {
					backend = work.Decision.RunRequest.Backend
				}
				if startedAt == "" {
					startedAt = at.UTC().Format(time.RFC3339)
				}
				if usage != nil && usage.Tokens == nil && usage.CostUSD == nil && usage.Details == nil {
					usage = nil
				}
				interruptedRec := run.Record{
					RunID:        id,
					ItemID:       work.Decision.ItemID,
					LaneID:       work.Decision.LaneID,
					Backend:      backend,
					Route:        route,
					Harness:      harness,
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
					Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
					CreatedAt:     at.UTC(),
					Subject:       &record.Subject{Kind: "run", ID: id},
					Payload:       body,
				}); err != nil {
					return err
				}
				if err := appendEffect(tx, loopOrphanedKind, id, struct{}{}, at); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ? AND outcome = 'running'`, id); err != nil {
					return err
				}
				if ev.reapedSingle != nil && isSingleWorkerRecoverable(work) {
					cp := *work
					*ev.reapedSingle = append(*ev.reapedSingle, &cp)
				}
				continue
			}
			// A terminal result alone does not establish that the supervisor
			// interrupted it. Ordinary stops intentionally leave workers alive.
			if work.Decision.Workflow != nil && !work.Orphaned {
				rec, err := terminalRecord(tx.Reader(), id)
				if err != nil {
					return err
				}
				// The runner can record its signal after the loop has died,
				// leaving no loop-stop marker. Proven owner death and no live
				// worker still make this interrupted node safe to replay.
				if rec != nil && rec.EndCause == run.EndSignal {
					if err := appendEffect(tx, loopOrphanedKind, id, struct{}{}, at); err != nil {
						return err
					}
				}
			}
			if work.Orphaned && ev.reapedSingle != nil && isSingleWorkerRecoverable(work) {
				cp := *work
				*ev.reapedSingle = append(*ev.reapedSingle, &cp)
			}
			continue
		}
		// Reap under the same transaction and guarded by this exact claim. No
		// unbounded lease becomes expiring merely because another loop started.
		result, err := tx.Exec(`UPDATE lanes SET leased_until = ? WHERE lane_id = ? AND run_id = ? AND state = ?`, at.UnixMilli(), work.Decision.LaneID, id, run.LaneStateLeased)
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
		if err := appendEffect(tx, loopOrphanedKind, id, struct{}{}, at); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ? AND outcome = 'running'`, id); err != nil {
			return err
		}
		if ev.reapedSingle != nil && isSingleWorkerRecoverable(work) {
			cp := *work
			*ev.reapedSingle = append(*ev.reapedSingle, &cp)
		}
	}
	return nil
}

func runHasActiveResume(r *store.Reader, id string) (bool, error) {
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

func isSingleWorkerRecoverable(work *pendingWork) bool {
	d := work.Decision
	return d.Workflow == nil && !work.Done && work.Candidate == nil && work.DirtyHold == nil && work.RepairHold == nil && work.GateStall == nil && !work.BoundaryFailed
}
