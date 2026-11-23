package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// startRunFace serializes startup with the interruption record. EndRuns can
// discover an already started face from its inherited process environment even
// if the supervisor crashes immediately after Start, before any later writes.
//
// A continuation (req.Resume) is a deliberate later segment of the same run and is
// not refused: the interruption ended the segment that was in flight, and the
// continuation's run.resume event reopens the run for judgment.
func (s *ProcessSteps) startRunFace(cmd *exec.Cmd, req run.Request) error {
	return s.startRunFaceContext(context.Background(), cmd, req)
}

func (s *ProcessSteps) startRunFaceContext(ctx context.Context, cmd *exec.Cmd, req run.Request) error {
	start := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		loop, _ := ctx.Value(runStartAdmissionKey{}).(*Loop)
		if loop == nil || loop.cfg.Restart == nil {
			return cmd.Start()
		}
		// The deferred settlement may finish after we read the request. Keep
		// exec excluded until the child is either started or refused.
		loop.restartMechanical.RLock()
		defer loop.restartMechanical.RUnlock()
		if loop.restartReplaced {
			return errRestartPending
		}
		db, err := store.Open(ctx, s.Store, true)
		if err != nil {
			return err
		}
		defer db.Close()
		// File and handoff are ordered by the same store writer. Hold it only
		// through Start (not Wait); the child performs its own allocation later.
		return db.WriteTx(ctx, func(tx *store.Tx) error {
			state, err := restart.ReadState(tx.Reader(), restart.RoleDrain)
			if err != nil {
				return err
			}
			if len(state.Pending) > 0 && !loop.restartDefersToSettlement() && !loop.restartDeferred() {
				return errRestartPending
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			return cmd.Start()
		})
	}
	if req.Resume {
		return withRunStartLock(ctx, s.Store, start)
	}
	return s.startRunContext(ctx, req.RunID, start)
}

func withRunStartLock(ctx context.Context, path string, action func() error) error {
	identity, err := processStoreIdentity(path)
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(identity+".drain-start.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return action()
}

func (s *ProcessSteps) startRun(id string, start func() error) error {
	return s.startRunContext(context.Background(), id, start)
}

func (s *ProcessSteps) startRunContext(ctx context.Context, id string, start func() error) error {
	return withRunStartLock(ctx, s.Store, func() error {
		db, err := store.OpenRead(ctx, s.Store)
		if err != nil {
			return err
		}
		stopped := 0
		err = db.ReadTx(ctx, func(r *store.Reader) error {
			if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ? AND object_id = ?`, loopOrphanedKind, id).Scan(&stopped); err != nil {
				return err
			}
			var member int
			if err := r.QueryRow(`SELECT COUNT(*) FROM read_only_runs WHERE run_id=?`, id).Scan(&member); err != nil {
				return err
			}
			if member == 0 {
				return nil
			}
			works, err := readPending(r)
			if err != nil {
				return err
			}
			for _, work := range works {
				for _, node := range parallelNodes(work.Decision) {
					if node.RunRequest != nil && node.RunRequest.RunID == id {
						if work.Orphaned || work.Done {
							stopped++
						}
						return nil
					}
				}
			}
			// An old member not in the current group may not launch after recovery.
			stopped++
			return nil
		})
		db.Close()
		if err != nil {
			return err
		}
		if stopped != 0 {
			return Externalf("run %s was ended by an explicit loop stop before startup", id)
		}
		// The OS lock covers launch, but no store transaction does: startup
		// must not hold off the child's migration or an unrelated writer.
		return start()
	})
}

// liveRunFaces finds only the run face, not its adapter descendants. The process
// markers bind it to both run and canonical store, while the exec face's arguments
// distinguish it from adapters inheriting those markers. Identity is revalidated
// before signaling, so PID reuse cannot target a different process.
type runFaceOwner struct {
	LoopOwner
	resume bool
	runID  string
}

func liveRunFaces(path, id string, here *LoopOwner) ([]*runFaceOwner, error) {
	return markedRunProcesses(path, id, here, true)
}

// liveRunWorkers finds every process of this user carrying the run and store
// markers: the adapter and its descendants when the run face is gone. Each is
// identified by pid and start time, so a recycled pid is rejected before a kill.
func liveRunWorkers(path, id string, here *LoopOwner) ([]*runFaceOwner, error) {
	return markedRunProcesses(path, id, here, false)
}

// isRunFaceArgv answers whether a process's raw cmdline is a run face's: `tillr-run exec`
// with the --input and --lane-root flags ProcessSteps.Run passes. An adapter and its
// children inherit the run's markers but never this argv, which is how a face is told
// from what it started.
func isRunFaceArgv(cmdline []byte) bool {
	args := bytes.Split(cmdline, []byte{0})
	if len(args) < 3 || string(args[1]) != "exec" {
		return false
	}
	flags := string(bytes.Join(args[2:], []byte{' '}))
	return strings.Contains(flags, "--input ") && strings.Contains(flags, "--lane-root ")
}

func markedRunProcesses(path, id string, here *LoopOwner, facesOnly bool) ([]*runFaceOwner, error) {
	path, err := processStoreIdentity(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var owners []*runFaceOwner
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		start, err := processStart(pid)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if start == "" {
			continue
		}
		env, err := runProcessEnvironment(dir, here.UID)
		if processGone(err) {
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
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if facesOnly && !isRunFaceArgv(argv) {
			continue
		}
		owner := *here
		owner.PID, owner.Start = pid, start
		owners = append(owners, &runFaceOwner{LoopOwner: owner, resume: resume, runID: runID})
	}
	return owners, nil
}

// Stop stops the registered supervisors. For --end-runs it ends current workers
// immediately, then repeats after the supervisors close or die, covering a
// dispatch racing the request and a supervisor dying before acknowledgment.
//
// StopConfig names the store and the clock; StopReport lists what was left alone.
type StopConfig struct {
	Store   string
	EndRuns bool
	// Now is the clock a session heartbeat is judged against (default time.Now).
	Now func() time.Time
}

// StopReport says what a stop could not act on, one line per session or run.
type StopReport struct {
	Skipped []string
}

func Stop(ctx context.Context, cfg StopConfig) (StopReport, error) {
	var report StopReport
	err := stop(ctx, cfg, &report)
	return report, err
}

// SessionGrace is how long past its recorded next pass a session still counts as
// live. It matches the observatory's allowance (tillr-view SessionGraceMS), so the
// stop face and the waiting face agree about which loop is running.
const SessionGrace = 5 * time.Second

func stop(ctx context.Context, cfg StopConfig, report *StopReport) error {
	path, endRuns, now := cfg.Store, cfg.EndRuns, cfg.Now
	if now == nil {
		now = time.Now
	}
	db, err := store.Open(ctx, path, true)
	if err != nil {
		return err
	}
	defer db.Close()
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	type session struct {
		id         string
		owner      *LoopOwner
		nextPassAt int64
	}
	// Read the open sessions, judge them with no transaction open, then flag the
	// local live ones in a short write that re-checks each is still open.
	var observed []session
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT session_id, loop_owner, next_pass_at FROM drain_sessions WHERE ended_at IS NULL AND loop_owner IS NOT NULL`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			var next int64
			if err := rows.Scan(&id, &raw, &next); err != nil {
				return err
			}
			var owner LoopOwner
			if err := json.Unmarshal(raw, &owner); err != nil {
				return err
			}
			observed = append(observed, session{id, &owner, next})
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	var sessions []session
	var foreignLive []string
	at := now()
	for _, s := range observed {
		if s.owner.Domain != here.Domain || s.owner.UID != here.UID {
			// This domain can neither see nor signal that process. Its heartbeat is
			// the only evidence: a stale one is a loop that died without ending its
			// session (a jail relaunch, a reboot, SIGKILL); a current one is live.
			if at.UnixMilli() > s.nextPassAt+SessionGrace.Milliseconds() {
				report.Skipped = append(report.Skipped, fmt.Sprintf("session %s: another process domain, heartbeat stale since %s; treated as dead", s.id, time.UnixMilli(s.nextPassAt).UTC().Format(time.RFC3339)))
			} else {
				foreignLive = append(foreignLive, s.id)
				report.Skipped = append(report.Skipped, fmt.Sprintf("session %s: a live loop in another process domain; stop it there", s.id))
			}
			continue
		}
		dead, err := ownerDead(s.owner, here)
		if err != nil {
			return err
		}
		if dead {
			report.Skipped = append(report.Skipped, fmt.Sprintf("session %s: its loop process is gone", s.id))
			continue
		}
		sessions = append(sessions, s)
	}
	if len(sessions) > 0 {
		flag := 1
		if endRuns {
			flag = 2
		}
		if err := db.WriteTx(ctx, func(tx *store.Tx) error {
			for _, s := range sessions {
				if _, err := tx.Exec(`UPDATE drain_sessions SET stop_requested = MAX(COALESCE(stop_requested,0), ?) WHERE session_id = ? AND ended_at IS NULL`, flag, s.id); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, s := range sessions {
		dead, err := ownerDead(s.owner, here)
		if err != nil {
			return err
		}
		if dead {
			continue
		}
		if err := syscall.Kill(s.owner.PID, syscall.SIGTERM); err != nil && !processGone(err) {
			return err
		}
	}
	if endRuns {
		skipped, err := EndRuns(ctx, path)
		report.Skipped = append(report.Skipped, skipped...)
		if err != nil {
			return err
		}
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for _, s := range sessions {
		for {
			dead, err := ownerDead(s.owner, here)
			if err != nil {
				return err
			}
			if dead {
				break
			}
			// An ended session is a bookkeeping receipt, not process exit.
			// The old supervisor may still own its descriptors during teardown.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	if endRuns && len(sessions) > 0 {
		// Again, after the supervisors are gone: a dispatch racing the request.
		skipped, err := EndRuns(ctx, path)
		report.Skipped = append(report.Skipped, skipped...)
		if err != nil {
			return err
		}
	}
	if len(foreignLive) > 0 {
		return Externalf("loop %s belongs to another process domain and is live; stop it there", strings.Join(foreignLive, ", "))
	}
	if len(sessions) == 0 && !endRuns {
		return Usagef("no live loop is registered in %s; surviving runs were left alone", path)
	}
	return nil
}

func (l *Loop) endRequestedRuns() error {
	if l.sessionID == "" {
		return nil
	}
	db, err := store.OpenRead(context.Background(), l.cfg.Store)
	if err != nil {
		return err
	}
	defer db.Close()
	flag := 0
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COALESCE(stop_requested,0) FROM drain_sessions WHERE session_id = ?`, l.sessionID).Scan(&flag)
	}); err != nil {
		return err
	}
	if flag == 2 {
		skipped, err := EndRuns(context.Background(), l.cfg.Store)
		for _, line := range skipped {
			l.reportf("loop: end-runs: %s\n", line)
		}
		return err
	}
	return nil
}

// EndRuns is the drain's only run-signaling function. It was built against a tree with
// no `tillr run pause`, so each live run face gets SIGTERM after its interruption is
// committed, and no later item has changed that to a pause request. A live
// continuation counts even when its earlier segment has a terminal record.
//
// The process scan happens outside every store transaction: the write transaction
// only re-reads the pending work and appends the interruptions. The start lock is
// held throughout, so no run face can start between the scan and the record.
func EndRuns(ctx context.Context, path string) ([]string, error) {
	return endRuns(ctx, path, nil)
}

// A non-nil selection restricts the stop to the items the current plan found
// closed or explicitly finished. Check the disposition again under the start lock.
func endRuns(ctx context.Context, path string, finishItems map[string]bool) ([]string, error) {
	var skipped []string
	err := withRunStartLock(ctx, path, func() error {
		db, err := store.Open(ctx, path, true)
		if err != nil {
			return err
		}
		defer db.Close()
		here, err := currentLoopOwner()
		if err != nil {
			return err
		}
		type candidate struct {
			work        *pendingWork
			rec         *run.Record
			control     string
			closedState string
		}
		var candidates []candidate
		if err := db.ReadTx(ctx, func(r *store.Reader) error {
			works, err := readPending(r)
			if err != nil {
				return err
			}
			for _, work := range works {
				if work.Done || work.Decision.RunRequest == nil {
					continue
				}
				var closedStateName string
				if finishItems != nil {
					if !finishItems[work.Decision.ItemID] {
						continue
					}
					closed, state, err := itemClosedInStore(r, work.Decision.ItemID)
					if err != nil {
						return err
					}
					if closed {
						closedStateName = state
					} else {
						finished, err := operatorFinishedLanding(r, work.Decision.ItemID)
						if err != nil {
							return err
						}
						if !finished {
							continue
						}
					}
				}
				for _, node := range parallelNodes(work.Decision) {
					rec, err := terminalRecord(r, node.RunRequest.RunID)
					if err != nil {
						return err
					}
					if rec != nil && rec.Resumed {
						continue
					}
					copy := *work
					copy.Decision = node
					candidates = append(candidates, candidate{&copy, rec, work.Decision.RunRequest.RunID, closedStateName})
				}
			}
			return nil
		}); err != nil {
			return err
		}
		type closedEndMeta struct {
			itemID string
			laneID string
			state  string
		}
		var owners []*runFaceOwner
		var ended []string
		ending := map[string]bool{}
		closedRuns := map[string]closedEndMeta{}
		for _, c := range candidates {
			work, id := c.work, c.work.Decision.RunRequest.RunID
			faces, err := liveRunFaces(path, id, here)
			if err != nil {
				return fmt.Errorf("locating run face %s: %w", id, err)
			}
			continuation := false
			if c.rec != nil {
				for _, face := range faces {
					continuation = continuation || face.resume
				}
				// A face can still be exiting after writing its terminal record. Only
				// a continuation whose terminal segment is not recorded is in flight.
				if !continuation {
					continue
				}
			}
			// A continuation's run.resume event clears the dispatch owner. Its face,
			// found in this process namespace under this user with this store's
			// identity, is then the evidence that the run belongs to this domain.
			ownerless := work.Owner == nil && !continuation
			foreign := work.Owner != nil && (work.Owner.Domain != here.Domain || work.Owner.UID != here.UID)
			if ownerless || foreign {
				// Another domain's run, or one with no owner to prove otherwise, is
				// left exactly as it is; the rest are still ended.
				skipped = append(skipped, fmt.Sprintf("run %s: its loop process domain is not this one", id))
				continue
			}
			if len(faces) == 0 {
				// The run face is gone but its adapter may survive in its own
				// process group. Same markers, same user, same store: end it, after
				// the interruption is recorded, like a face.
				workers, err := liveRunWorkers(path, id, here)
				if err != nil {
					return fmt.Errorf("locating workers of run %s: %w", id, err)
				}
				faces = workers
			}
			owners = append(owners, faces...)
			if c.closedState != "" && len(faces) > 0 {
				if _, seen := closedRuns[c.control]; !seen {
					closedRuns[c.control] = closedEndMeta{
						itemID: work.Decision.ItemID,
						laneID: work.Decision.LaneID,
						state:  c.closedState,
					}
				}
			}
			if !work.Orphaned && !ending[c.control] {
				ended = append(ended, c.control)
				ending[c.control] = true
			}
		}
		if len(ended) > 0 {
			if err := db.WriteTx(ctx, func(tx *store.Tx) error {
				works, err := readPending(tx.Reader())
				if err != nil {
					return err
				}
				open := map[string]bool{}
				for _, work := range works {
					if !work.Done && !work.Orphaned && work.Decision.RunRequest != nil {
						open[work.Decision.RunRequest.RunID] = true
					}
				}
				at := time.Now().UTC()
				for _, id := range ended {
					// A run examined or already marked since the scan is not
					// interrupted again.
					if !open[id] {
						continue
					}
					if err := appendEffect(tx, loopOrphanedKind, id, struct{}{}, at); err != nil {
						return err
					}
					if meta, ok := closedRuns[id]; ok {
						detail := fmt.Sprintf("ended live run %s because %s was closed", id, meta.itemID)
						if meta.laneID != "" {
							detail = fmt.Sprintf("ended live run %s on %s because %s was closed", id, meta.laneID, meta.itemID)
						}
						if err := appendEffect(tx, runEndedItemClosedKind, id, runEndedItemClosed{
							ItemID:    meta.itemID,
							RunID:     id,
							LaneID:    meta.laneID,
							ItemState: meta.state,
							Reason:    "item-closed",
							Detail:    detail,
						}, at); err != nil {
							return err
						}
					}
				}
				return nil
			}); err != nil {
				return err
			}
		}
		for _, owner := range owners {
			dead, err := ownerDead(&owner.LoopOwner, here)
			if err != nil {
				return err
			}
			if dead {
				continue
			}
			if err := syscall.Kill(owner.PID, syscall.SIGTERM); err != nil && !processGone(err) {
				return err
			}
		}
		return nil
	})
	return skipped, err
}
