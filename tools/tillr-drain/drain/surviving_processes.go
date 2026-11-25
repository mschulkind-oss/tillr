package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ReasonRunProcessesLive holds an item whose ended run still has live marked processes
// carrying its TILLR_RUN_ID and TILLR_LOOP_STORE markers. The lane is retained as it
// stands — not reset, and not examined — while every other item and lane carries on.
const ReasonRunProcessesLive = "run-processes-live"

type survivingProcess struct {
	PID       int
	Start     string
	Command   string
	IsRunFace bool
}

func (p survivingProcess) String() string {
	if p.Command == "" {
		return strconv.Itoa(p.PID)
	}
	return fmt.Sprintf("%d (%s)", p.PID, p.Command)
}

func formatSurvivingProcesses(procs []survivingProcess) string {
	parts := make([]string, len(procs))
	for i, p := range procs {
		parts[i] = p.String()
	}
	return strings.Join(parts, ", ")
}

type survivingProcessesError struct {
	RunID     string
	Step      string
	Processes []survivingProcess
}

func (e *survivingProcessesError) Error() string {
	return fmt.Sprintf("cannot %s for %s while its processes survive: %s", e.Step, e.RunID, formatSurvivingProcesses(e.Processes))
}

// survivingRunProcesses scans /proc for live non-zombie processes of this user carrying
// TILLR_RUN_ID=<runID> and TILLR_LOOP_STORE=<resolved storePath>, returning them sorted
// by PID.
func survivingRunProcesses(storePath, runID string) ([]survivingProcess, error) {
	if runID == "" {
		return nil, nil
	}
	resolved, err := processStoreIdentity(storePath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	uid := uint32(os.Geteuid())
	self := os.Getpid()
	wantRun := "TILLR_RUN_ID=" + runID
	wantStore := "TILLR_LOOP_STORE=" + resolved
	var procs []survivingProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		start, err := processStart(pid)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		if start == "" {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		env, err := runProcessEnvironment(dir, uid)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		hasRun, hasStore := false, false
		for _, v := range bytes.Split(env, []byte{0}) {
			s := string(v)
			if s == wantRun {
				hasRun = true
			} else if s == wantStore {
				hasStore = true
			}
			if hasRun && hasStore {
				break
			}
		}
		if !hasRun || !hasStore {
			continue
		}
		cmd, isFace, gone, err := readProcessCommand(dir)
		if gone {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		procs = append(procs, survivingProcess{PID: pid, Start: start, Command: cmd, IsRunFace: isFace})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
	return procs, nil
}

func readProcessCommand(dir string) (string, bool, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if processGone(err) {
		return "", false, true, nil
	}
	if err != nil {
		return "", false, false, err
	}
	isFace := isRunFaceArgv(raw)
	cmd := strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))
	if cmd != "" {
		return cmd, isFace, false, nil
	}
	comm, err := os.ReadFile(filepath.Join(dir, "comm"))
	if processGone(err) {
		return "", false, true, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return strings.TrimSpace(string(comm)), isFace, false, nil
}

type processKey struct {
	PID   int
	Start string
}

// waitProcessExit blocks until the process identified by (pid, start) exits, or until
// ctx is canceled. It waits on the kernel's pidfd exit notification rather than polling
// on elapsed time.
func waitProcessExit(ctx context.Context, pid int, start string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("opening pidfd for process %d: %w", pid, err)
	}
	defer unix.Close(fd)

	// Recheck start time after opening pidfd so a PID reused before PidfdOpen is
	// never waited on as the original run face.
	currentStart, err := processStart(pid)
	if processGone(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verifying process %d: %w", pid, err)
	}
	if currentStart == "" || currentStart != start {
		return nil
	}

	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if ctx.Done() != nil {
		wakeFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
		if err != nil {
			return fmt.Errorf("creating wakeup eventfd for process %d: %w", pid, err)
		}
		done := make(chan struct{})
		waiterDone := make(chan struct{})
		go func() {
			defer close(waiterDone)
			select {
			case <-ctx.Done():
				var buf [8]byte
				buf[0] = 1
				_, _ = unix.Write(wakeFD, buf[:])
			case <-done:
			}
		}()
		defer func() {
			close(done)
			<-waiterDone
			_ = unix.Close(wakeFD)
		}()
		fds = append(fds, unix.PollFd{Fd: int32(wakeFD), Events: unix.POLLIN})
	}

	for {
		n, err := unix.Poll(fds, -1)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("waiting for process %d exit: %w", pid, err)
		}
		if n <= 0 {
			continue
		}
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0 {
			return nil
		}
		if len(fds) > 1 && fds[1].Revents != 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			return nil
		}
	}
}

// awaitSurvivingRunProcesses scans for live marked processes of runID. If the run's own
// run face (tillr-run exec) is still in the act of exiting after writing its terminal
// record, it waits for that face's exit before judging whether any marked processes
// actually survived.
func awaitSurvivingRunProcesses(ctx context.Context, storePath, runID string) ([]survivingProcess, error) {
	waited := map[processKey]bool{}
	for {
		procs, err := survivingRunProcesses(storePath, runID)
		if err != nil {
			return nil, err
		}
		if len(procs) == 0 {
			return nil, nil
		}
		var faces []survivingProcess
		for _, p := range procs {
			if p.IsRunFace && !waited[processKey{PID: p.PID, Start: p.Start}] {
				faces = append(faces, p)
			}
		}
		if len(faces) == 0 {
			return procs, nil
		}
		for _, face := range faces {
			if err := waitProcessExit(ctx, face.PID, face.Start); err != nil {
				return nil, err
			}
			waited[processKey{PID: face.PID, Start: face.Start}] = true
		}
	}
}

func examinationRunIDs(work *pendingWork) []string {
	var ids []string
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest != nil && node.RunRequest.RunID != "" {
			ids = append(ids, node.RunRequest.RunID)
		}
	}
	if work.Candidate != nil && work.Candidate.RunID != "" {
		ids = append(ids, work.Candidate.RunID)
	}
	return ids
}

func (l *Loop) checkSurvivingRunProcesses(ctx context.Context, itemID, lane string, runIDs ...string) (laneOutcome, bool) {
	seen := map[string]bool{}
	for _, runID := range runIDs {
		if runID == "" || seen[runID] {
			continue
		}
		seen[runID] = true
		procs, err := awaitSurvivingRunProcesses(ctx, l.cfg.Store, runID)
		if err != nil {
			return laneOutcome{err: err}, true
		}
		if len(procs) > 0 {
			return l.retainSurvivingProcesses(ctx, itemID, lane, runID, procs), true
		}
	}
	return laneOutcome{}, false
}

func (l *Loop) retainSurvivingProcesses(ctx context.Context, itemID, lane, runID string, procs []survivingProcess) laneOutcome {
	list := formatSurvivingProcesses(procs)
	detail := fmt.Sprintf("ended run %s still has live marked processes: %s", runID, list)
	l.reportf("loop: lane %s retained: %s\n", lane, detail)
	var err error
	if itemID == "" {
		err = errors.New("no item to hold for the retained lane")
	} else {
		err = l.holdItemFault(ctx, Candidate{ItemID: itemID, RunID: runID, LaneID: lane}, ReasonRunProcessesLive, detail)
	}
	if asItemTerminal(err) != nil {
		pids := make([]int, len(procs))
		for i, p := range procs {
			pids[i] = p.PID
		}
		return laneOutcome{stop: stopLaneRetained, laneRefusal: holdLaneChildrenRefusal(lane, pids, formatLeftBy(runID, itemID))}
	}
	if err != nil {
		l.reportf("loop: lane %s retained, and its item could not be held: %v\n", lane, err)
		return laneOutcome{stop: StopFault}
	}
	return laneOutcome{stop: stopLaneRetained}
}
