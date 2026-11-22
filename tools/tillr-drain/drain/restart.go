package drain

// Architecture and invariants: docs/reference/tillr-restart.md

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/record"
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

// adoptRestart publishes the live build and answers only the request carried across exec.
// It does not pause, resume or record a starting build for any run.
func (l *Loop) adoptRestart(ctx context.Context) error {
	if l.cfg.Restart == nil {
		return nil
	}
	if err := restart.PublishBuild(ctx, l.cfg.Store, restart.BuildFact{
		Role: restart.RoleDrain, Revision: l.build.Revision, Time: l.build.Time,
		Modified: l.build.Modified, Digest: l.build.Digest, Executable: l.build.Executable,
		PID: os.Getpid(), At: l.cfg.Now().UTC(),
		RestartRequest: os.Getenv(restart.RequestEnv),
	}); err != nil {
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

// restartDeferred keeps the queue working while a tip proof or a post-landing
// action still owns work that exec would drop. Use the same rule at worker handoff.
func (l *Loop) restartDeferred() bool {
	l.heavyMu.Lock()
	proofActive := l.heavyProofActive
	l.heavyMu.Unlock()
	return proofActive || l.rollAction.busy() || l.piRollAction.busy() || l.extRollAction.busy() || l.pushAction.busy()
}

// gateRestart is the first action of a pass and is also called by unbounded waits.
// A refused or current-build request leaves their work running. Only accepted exec
// relinquishes examination ownership, after bounded mechanical steps finish.
func (l *Loop) gateRestart(ctx context.Context, result *LoopResult) (bool, string, error) {
	if l.cfg.Restart == nil {
		return false, "", nil
	}
	l.restartMu.Lock()
	defer l.restartMu.Unlock()
	if l.restartReplaced {
		return true, StopRestarted, nil
	}
	// Exec would discard the pending follow-up actions, even if the current
	// command survived as an inherited child. Keep processing passes meanwhile.
	if l.restartDeferred() {
		return false, "", nil
	}
	rs := l.restarter()
	pending, err := rs.Pending(ctx)
	if err != nil || !pending {
		return false, "", err
	}
	// Do not queue an exclusive waiter behind another item's gate or landing.
	// The request stays unanswered and a subsequent pass can take it.
	if !l.restartMechanical.TryLock() {
		return false, "", nil
	}
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
		l.endSession()
		return execute(path, argv, env)
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
	l.restartReplaced = true
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
