package drain

// Architecture and invariants: docs/reference/tillr-restart.md

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// StopRestarted is reachable only through a test replacement that returns. Real exec
// never returns successfully, so it writes no stop record and signals no worker.
const StopRestarted = "restarted"

// RestartConfig supplies the running process's executable and original arguments.
// Tests can replace Check and Exec; production Exec is syscall.Exec.
type RestartConfig struct {
	Executable string
	Argv       []string
	Check      func(context.Context, string) error
	Exec       func(string, []string, []string) error
}

func (l *Loop) restarter() *restart.Restarter {
	rc := l.cfg.Restart
	check := rc.Check
	if check != nil {
		check = func(ctx context.Context, executable string) error {
			err := rc.Check(ctx, executable)
			var exit *ExitError
			if errors.As(err, &exit) && ExitCode(err) == record.ExitIncompatible {
				if roll, readErr := readSchemaRoll(schemaRollDir(executable, DiskDigest(executable))); readErr == nil {
					l.alarmStoreRoll(ctx, roll.Commit, "restart store check", err)
				}
			}
			return err
		}
	}
	return &restart.Restarter{Store: l.cfg.Store, Role: restart.RoleDrain,
		Running:    restart.Build{Line: l.build.String(), Digest: l.build.Digest},
		Executable: rc.Executable, Disk: DiskDigest, Check: check,
		Candidate: restart.LauncherCandidate(rc.Executable), Now: l.cfg.Now}
}

func (l *Loop) publishDrainBuild(ctx context.Context) error {
	now := l.cfg.Now().UTC()
	sessionID, err := l.ensureSessionID(now)
	if err != nil {
		return err
	}
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	carriedRestart := os.Getenv(restart.RequestEnv)
	fact := restart.BuildFact{
		Role: restart.RoleDrain, Revision: l.build.Revision, Time: l.build.Time,
		Modified: l.build.Modified, Digest: l.build.Digest, Executable: l.build.Executable,
		PID: os.Getpid(), At: now,
		RestartRequest: carriedRestart,
		SessionID:      sessionID,
		LoopOwner:      here,
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := holdLoopPresence(l.cfg.Store); err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if fact.RestartRequest == "" {
			pendingReq, hasPendingReq, err := restart.PendingAfterBuild(tx.Reader(), restart.RoleDrain)
			if err != nil {
				return err
			}
			if hasPendingReq {
				fact.RestartRequest = pendingReq.ID
			}
		}
		if carriedRestart == "" {
			r := tx.Reader()
			var seq int64
			var kind string
			var payload []byte
			err := r.QueryRow(
				`SELECT seq, kind, payload FROM events WHERE kind = ? OR (kind = ? AND json_extract(payload, '$.role') = ?) ORDER BY seq DESC LIMIT 1`,
				StopRequestedKind, restart.BuildKind, restart.RoleDrain,
			).Scan(&seq, &kind, &payload)
			switch {
			case err == nil && kind == StopRequestedKind:
				if fact.RestartRequest == "" {
					var stop struct {
						ID string `json:"id"`
					}
					if err := json.Unmarshal(payload, &stop); err != nil {
						return err
					}
					if stop.ID != "" {
						fact.StopRequest = stop.ID
					} else {
						fact.StopRequest = "stop-drain"
					}
				}
			case err == nil:
				var prevBuild restart.BuildFact
				if err := json.Unmarshal(payload, &prevBuild); err != nil {
					return err
				}
				relaunch, priorDomain, err := l.classifySessionRelaunch(r, now, here, seq, &prevBuild)
				if err != nil {
					return err
				}
				fact.SessionRelaunch = relaunch
				fact.PriorDomain = priorDomain
			case errors.Is(err, sql.ErrNoRows):
				var prevSessionID string
				var stopRequested int
				var ownerRaw sql.NullString
				var nextPassAt sql.NullInt64
				var endedAt sql.NullInt64
				sErr := r.QueryRow(
					`SELECT session_id, COALESCE(stop_requested, 0), loop_owner, next_pass_at, ended_at FROM drain_sessions ORDER BY started_at DESC, rowid DESC LIMIT 1`,
				).Scan(&prevSessionID, &stopRequested, &ownerRaw, &nextPassAt, &endedAt)
				switch {
				case errors.Is(sErr, sql.ErrNoRows):
					if fact.RestartRequest == "" {
						fact.Initial = true
					}
				case sErr != nil:
					return sErr
				case stopRequested > 0:
					if fact.RestartRequest == "" {
						fact.StopRequest = "drain_sessions"
					}
				default:
					var prevOwner *presence.Owner
					if ownerRaw.Valid && ownerRaw.String != "" {
						var parsed presence.Owner
						if err := json.Unmarshal([]byte(ownerRaw.String), &parsed); err == nil {
							prevOwner = &parsed
						}
					}
					relaunch, priorDomain, err := l.classifySessionRelaunchState(r, now, here, false, 0, prevSessionID, prevOwner, true, nextPassAt, endedAt)
					if err != nil {
						return err
					}
					fact.SessionRelaunch = relaunch
					fact.PriorDomain = priorDomain
				}
			default:
				return err
			}
		}
		return restart.PublishBuildTx(tx, fact)
	})
}

func (l *Loop) classifySessionRelaunch(r *store.Reader, now time.Time, here *presence.Owner, prevSeq int64, prevBuild *restart.BuildFact) (bool, string, error) {
	prevSessionID := prevBuild.SessionID
	prevOwner := prevBuild.LoopOwner
	var hasSessionRow bool
	var nextPassAt, endedAt sql.NullInt64
	if prevOwner == nil {
		var rowSessionID string
		var ownerRaw sql.NullString
		err := r.QueryRow(
			`SELECT session_id, loop_owner, next_pass_at, ended_at FROM drain_sessions ORDER BY started_at DESC, rowid DESC LIMIT 1`,
		).Scan(&rowSessionID, &ownerRaw, &nextPassAt, &endedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, "", err
		}
		if err == nil {
			if prevSessionID == "" {
				prevSessionID = rowSessionID
			}
			if ownerRaw.Valid && ownerRaw.String != "" {
				var parsed presence.Owner
				if err := json.Unmarshal([]byte(ownerRaw.String), &parsed); err == nil {
					prevOwner = &parsed
				}
			}
			if rowSessionID == prevSessionID {
				hasSessionRow = true
			}
		}
	}
	return l.classifySessionRelaunchState(r, now, here, true, prevSeq, prevSessionID, prevOwner, hasSessionRow, nextPassAt, endedAt)
}

func (l *Loop) classifySessionRelaunchState(r *store.Reader, now time.Time, here *presence.Owner, hasPrevBuild bool, prevSeq int64, prevSessionID string, prevOwner *presence.Owner, hasSessionRow bool, nextPassAt, endedAt sql.NullInt64) (bool, string, error) {
	if prevOwner == nil || prevOwner.Domain == "" || here == nil || here.Domain == "" || prevOwner.Domain == here.Domain {
		return false, "", nil
	}
	var faulted int
	var faultErr error
	if hasPrevBuild {
		faultErr = r.QueryRow(
			`SELECT 1 FROM events WHERE kind = ? AND json_extract(payload, '$.stop') = 'fault' AND (seq > ? OR (? != '' AND json_extract(payload, '$.session_id') = ?)) LIMIT 1`,
			loopEndedKind, prevSeq, prevSessionID, prevSessionID,
		).Scan(&faulted)
	} else if prevSessionID != "" {
		faultErr = r.QueryRow(
			`SELECT 1 FROM events WHERE kind = ? AND json_extract(payload, '$.stop') = 'fault' AND json_extract(payload, '$.session_id') = ? LIMIT 1`,
			loopEndedKind, prevSessionID,
		).Scan(&faulted)
	} else {
		faultErr = r.QueryRow(
			`SELECT 1 FROM events WHERE kind = ? AND json_extract(payload, '$.stop') = 'fault' LIMIT 1`,
			loopEndedKind,
		).Scan(&faulted)
	}
	if faultErr == nil {
		return false, "", nil
	}
	if !errors.Is(faultErr, sql.ErrNoRows) {
		return false, "", faultErr
	}
	if !presence.SameBoot(prevOwner.Domain, here.Domain) {
		return true, prevOwner.Domain, nil
	}
	if !hasSessionRow && prevSessionID != "" {
		err := r.QueryRow(
			`SELECT next_pass_at, ended_at FROM drain_sessions WHERE session_id = ? LIMIT 1`,
			prevSessionID,
		).Scan(&nextPassAt, &endedAt)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, "", err
		}
		hasSessionRow = err == nil
	}
	if hasSessionRow && endedAt.Valid {
		return true, prevOwner.Domain, nil
	}
	identity, err := processStoreIdentity(l.cfg.Store)
	if err != nil {
		return false, "", err
	}
	state, err := openForWriting(loopPresencePath(identity, *prevOwner))
	if err != nil {
		return false, "", err
	}
	switch state {
	case streamUnwritten:
		return true, prevOwner.Domain, nil
	case streamWritten:
		return false, "", nil
	case streamAbsent:
		if hasSessionRow && nextPassAt.Valid && now.UnixMilli() > nextPassAt.Int64+SessionGrace.Milliseconds() {
			return true, prevOwner.Domain, nil
		}
		return false, "", nil
	default:
		return false, "", nil
	}
}

// adoptRestart publishes the live build and answers only the request carried across exec.
// It does not pause, resume or record a starting build for any run.
func (l *Loop) adoptRestart(ctx context.Context) error {
	if l.cfg.Restart == nil {
		return nil
	}
	if err := l.publishDrainBuild(ctx); err != nil {
		return err
	}
	if err := alarms.ClearStoreRollFallback(l.cfg.Store); err != nil {
		l.reportf("loop: could not clear the store roll alarm file: %v\n", err)
	}
	adopted, err := l.restarter().Adopt(ctx)
	if err != nil {
		return err
	}
	if adopted {
		l.heavyReuseRed = true
		// This runs before the new image starts any subprocess. Wait only on children
		// inherited from the old image, never children whose exec.Cmd owns their exit.
		reapCtx, cancel := context.WithCancel(ctx)
		pids := inheritedChildren()
		done := make(chan struct{})
		go func() {
			defer close(done)
			reapInherited(reapCtx, pids)
		}()
		l.stopInheritedReaper = func() {
			cancel()
			<-done // The collector makes one last nonblocking sweep on cancellation.
		}
		l.reportf("loop: restarted in place onto build %s\n", l.build)
	}
	return nil
}

func (l *Loop) restartActions() []*landingAction {
	return []*landingAction{&l.rollAction, &l.piRollAction, &l.extRollAction, &l.pushAction}
}

func (l *Loop) restartActionDeferred() bool {
	for _, a := range l.restartActions() {
		if a.busy() {
			return true
		}
	}
	return false
}

func (l *Loop) busyRestartAction() <-chan struct{} {
	for _, a := range l.restartActions() {
		if done := a.idle(); done != nil {
			return done
		}
	}
	return nil
}

// restartDeferred keeps the queue working while a post-landing action still
// owns work that exec would drop. Use the same rule at worker handoff.
func (l *Loop) restartDeferred() bool {
	return l.restartActionDeferred()
}

func (l *Loop) isRestartReplaced() bool {
	return l.restartReplaced.Load()
}

// gateRestart is the first action of a pass and is also called by unbounded waits.
// A refused or current-build request leaves their work running. Only accepted exec
// relinquishes examination ownership, after bounded mechanical steps finish.
func (l *Loop) gateRestart(ctx context.Context, result *LoopResult) (handled bool, stop string, err error) {
	return l.gateRestartWith(ctx, result, false, nil)
}

func (l *Loop) gateRestartWith(ctx context.Context, result *LoopResult, stopChecked bool, scope *pollReaderScope) (handled bool, stop string, err error) {
	defer func() {
		if err == nil && stop == "" && !l.nonLanding && !l.isRestartReplaced() {
			err = l.reclaimDetachedGateExaminationsWith(ctx, stopChecked, scope)
			if err == nil {
				l.reclaimDetachedHeavyProof(ctx)
			}
		}
	}()
	if l.cfg.Restart == nil {
		return false, "", nil
	}
	l.restartMu.Lock()
	defer l.restartMu.Unlock()
	if l.restartReplaced.Load() {
		return true, StopRestarted, nil
	}
	// Exec would discard the pending follow-up actions, even if the current
	// command survived as an inherited child. Keep processing passes meanwhile.
	if l.restartActionDeferred() {
		l.clearRestartPending()
		return false, "", nil
	}
	rs := l.restarter()
	pending, err := l.restarterPendingWith(ctx, scope)
	if err != nil || !pending {
		if err == nil {
			l.clearRestartPending()
		}
		return false, "", err
	}
	// Prevent new gates and landings from taking restartMechanical while this
	// restart is pending, so already-held locks drain instead of starving exec.
	l.markRestartPending()
	// Do not queue an exclusive waiter behind another item's gate or landing.
	// The request stays unanswered and a subsequent pass can take it.
	if !l.restartMechanical.TryLock() {
		l.markRestartWaitingOnHold()
		return false, "", nil
	}
	defer l.clearRestartPending()
	defer l.restartMechanical.Unlock()
	request, replace, err := rs.Settle(ctx)
	if err != nil {
		return true, "", err
	}
	if !replace {
		l.reportf("loop: restart %s answered without replacing the process\n", request.ID)
		return true, "", nil
	}
	// A landing may have queued a follow-up before we acquired the lock,
	// or a proof may have taken ownership while checking the replacement.
	if l.restartDeferred() {
		return false, "", nil
	}
	l.reportf("loop: restart %s: replacing build %s in place\n", request.ID, l.build)
	execute := l.cfg.Restart.Exec
	if execute == nil {
		execute = syscall.Exec
	}
	execReplacement := func(path string, argv, env []string) error {
		if err := l.releaseRestartExaminations(ctx); err != nil {
			return err
		}
		wasEnabled := l.disableAndClosePollReader()
		l.endSession()
		if err := execute(path, argv, env); err != nil {
			if wasEnabled {
				l.enablePollReader()
			}
			return err
		}
		return nil
	}
	if err := rs.Replace(ctx, request, l.cfg.Restart.Argv, execReplacement); err != nil {
		if errors.Is(err, restart.ErrSuperseded) {
			return true, "", nil
		}
		if ctx.Err() != nil {
			return true, StopOperatorStop, nil
		}
		return true, "", rs.Refuse(ctx, request, "exec: "+err.Error())
	}
	l.restartReplaced.Store(true)
	return true, StopRestarted, nil
}

// inheritedChildren lists this process's existing children on Linux. Exec preserves
// their parent PID, but the old image's exec.Cmd waiters no longer exist.
func inheritedChildren() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		// comm may contain spaces and parentheses; ppid follows its last closing paren.
		text := string(raw)
		fields := strings.Fields(text[strings.LastIndexByte(text, ')')+1:])
		if len(fields) >= 2 {
			if ppid, err := strconv.Atoi(fields[1]); err == nil && ppid == os.Getpid() {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// reapInherited collects only the named PIDs, without stealing new exec.Cmd statuses.
func reapInherited(ctx context.Context, pids []int) {
	for len(pids) > 0 {
		stopping := ctx.Err() != nil
		remaining := pids[:0]
		for _, pid := range pids {
			var status syscall.WaitStatus
			if done, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil); done == 0 || err == syscall.EINTR {
				remaining = append(remaining, pid)
			}
		}
		if stopping || len(remaining) == 0 {
			return
		}
		pids = remaining
		select {
		case <-ctx.Done(): // Sweep the remaining PIDs once more; never wait for a live run.
		case <-time.After(200 * time.Millisecond):
		}
	}
}
