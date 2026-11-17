package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// LoopOwner identifies one OS process without mistaking a reused PID for it.
// Domain binds the evidence to the boot and PID namespace where it was observed.
type LoopOwner struct {
	PID    int    `json:"pid"`
	Start  string `json:"start"`
	Domain string `json:"domain"`
	UID    uint32 `json:"uid"`
}

func processStart(pid int) (string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return "", fmt.Errorf("process %d has malformed stat", pid)
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return "", fmt.Errorf("process %d stat has no start time", pid)
	}
	if fields[0] == "Z" {
		return "", nil
	}
	return fields[19], nil
}

// processOwner is this process's identity, read from /proc once: the boot, PID
// namespace, PID, start time and user never change for a running process.
var processOwner = sync.OnceValues(readLoopOwner)

// currentLoopOwner answers a copy of this process's identity. Only the first call
// reads /proc, and NewLoop makes that call before any transaction, so no write
// transaction pays for it.
func currentLoopOwner() (*LoopOwner, error) {
	owner, err := processOwner()
	if err != nil {
		return nil, err
	}
	copy := *owner
	return &copy, nil
}

// ownerReads counts /proc identity reads, so a test can pin that claims and reaps
// inside a write transaction never make one.
var ownerReads atomic.Int64

func readLoopOwner() (*LoopOwner, error) {
	ownerReads.Add(1)
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return nil, fmt.Errorf("identifying loop boot: %w", err)
	}
	ns, err := os.Readlink("/proc/self/ns/pid")
	if err != nil {
		return nil, fmt.Errorf("identifying loop PID namespace: %w", err)
	}
	start, err := processStart(os.Getpid())
	if err != nil {
		return nil, err
	}
	return &LoopOwner{PID: os.Getpid(), Start: start, Domain: strings.TrimSpace(string(boot)) + ":" + ns, UID: uint32(os.Geteuid())}, nil
}

func processGone(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH)
}

func ownerDead(owner, here *LoopOwner) (bool, error) {
	if owner == nil || here == nil || owner.Domain != here.Domain || owner.UID != here.UID {
		return false, nil
	}
	start, err := processStart(owner.PID)
	if processGone(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return start != owner.Start, nil
}

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

func processStoreIdentity(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// Workers inherit the loop's effective UID. A process owned by another UID
// cannot be one of those workers, and its private environment is not ours to read.
func runProcessEnvironment(dir string, uid uint32) ([]byte, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("process ownership unavailable for %s", dir)
	}
	if stat.Uid != uid {
		return nil, nil
	}
	return os.ReadFile(filepath.Join(dir, "environ"))
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
	// reclaimed collects the lines the write's reclaims print once its transaction has
	// closed: no report is written inside a write transaction.
	reclaimed *[]string
}

func (ev reapEvidence) ownerDead(owner *LoopOwner) bool {
	return owner != nil && ev.dead[*owner]
}

// gatherReapEvidence reads the pending claims, then judges their owners and scans
// the process table once, with no transaction open. The caller holds the run-start
// lock from here through its write, so no run face starts in between.
func (l *Loop) gatherReapEvidence(ctx context.Context, s *store.Store) (reapEvidence, error) {
	ev := reapEvidence{dead: map[LoopOwner]bool{}, live: map[string]bool{}, faces: map[string]bool{}, stuck: map[string]string{}, reclaimed: &[]string{}}
	var owners []LoopOwner
	stuck := map[string]*pendingWork{}
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
			if work.Done && work.Decision.RunRequest != nil && !l.active[work.Decision.RunRequest.RunID] {
				if unbounded[work.Decision.RunRequest.RunID] == work.Decision.LaneID {
					stuck[work.Decision.RunRequest.RunID] = work
					if work.Examiner != nil {
						owners = append(owners, *work.Examiner)
					}
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
		return nil
	}); err != nil {
		return ev, err
	}
	here, err := currentLoopOwner()
	if err != nil {
		return ev, err
	}
	anyDead := false
	for _, owner := range owners {
		if _, seen := ev.dead[owner]; seen {
			continue
		}
		o := owner
		dead, err := ownerDead(&o, here)
		if err != nil {
			return ev, err
		}
		ev.dead[owner] = dead
		anyDead = anyDead || dead
	}
	if anyDead || len(stuck) > 0 {
		if ev.live, ev.faces, err = liveRunProcessIDs(l.cfg.Store); err != nil {
			return ev, err
		}
		for id, work := range stuck {
			if stuckExaminationLease(work, ev.faces[id], here, ev) {
				ev.stuck[id] = work.Decision.LaneID
			}
		}
	}
	return ev, nil
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

// reapDeadLoopRuns closes only proven-dead loop-owned claims. Foreign/legacy
// leases and claims whose worker is still alive keep their existing protection.
//
// It reads no process table: every liveness answer comes from ev, gathered before
// the transaction, and each write is guarded by the exact claim it re-reads here.
func (l *Loop) reapDeadLoopRuns(ctx context.Context, tx *store.Tx, at time.Time, ev reapEvidence) error {
	works, err := readPending(tx.Reader())
	if err != nil {
		return err
	}
	for _, work := range works {
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
			if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id IN (?,?) AND state=?`, run.LaneStateIdle, work.Decision.LaneID, id, work.ParentRunID, run.LaneStateLeased); err != nil {
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
	}
	return nil
}
