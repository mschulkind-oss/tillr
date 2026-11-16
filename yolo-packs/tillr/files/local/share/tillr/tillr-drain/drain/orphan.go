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

func currentLoopOwner() (*LoopOwner, error) {
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

func liveRunProcess(storePath, runID string) (bool, error) {
	storePath, err := processStoreIdentity(storePath)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
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
			return false, fmt.Errorf("checking process %d before recovering %s: %w", pid, runID, err)
		}
		values := bytes.Split(env, []byte{0})
		hasRun, hasStore := false, false
		for _, v := range values {
			hasRun = hasRun || string(v) == "TILLR_RUN_ID="+runID
			hasStore = hasStore || string(v) == "TILLR_LOOP_STORE="+storePath
		}
		if hasRun && hasStore {
			return true, nil
		}
	}
	return false, nil
}

// reapDeadLoopRuns closes only proven-dead loop-owned claims. Foreign/legacy
// leases and claims whose worker is still alive keep their existing protection.
func (l *Loop) reapDeadLoopRuns(ctx context.Context, tx *store.Tx, at time.Time) error {
	works, err := readPending(tx.Reader())
	if err != nil {
		return err
	}
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	for _, work := range works {
		if (work.Done && !work.Examining) || work.Decision.RunRequest == nil {
			continue
		}
		dead, err := ownerDead(work.Owner, here)
		if err != nil {
			return err
		}
		if !dead {
			continue
		}
		id := work.Decision.RunRequest.RunID
		live, err := liveRunProcess(l.cfg.Store, id)
		if err != nil {
			return err
		}
		if live {
			continue
		}
		if work.Examining {
			// Examination is not another execution of the node. Recover its
			// lease without inventing a terminal node record or spending a run.
			if work.ParentRunID != "" {
				live, err := liveRunProcess(l.cfg.Store, work.ParentRunID)
				if err != nil {
					return err
				}
				if live {
					continue
				}
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
