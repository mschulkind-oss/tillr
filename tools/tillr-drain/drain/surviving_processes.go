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
)

// ReasonRunProcessesLive holds an item whose ended run still has live marked processes
// carrying its TILLR_RUN_ID and TILLR_LOOP_STORE markers. The lane is retained as it
// stands — not reset, and not examined — while every other item and lane carries on.
const ReasonRunProcessesLive = "run-processes-live"

type survivingProcess struct {
	PID     int
	Command string
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
		cmd, gone, err := readProcessCommand(dir)
		if gone {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("checking process %d: %w", pid, err)
		}
		procs = append(procs, survivingProcess{PID: pid, Command: cmd})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })
	return procs, nil
}

func readProcessCommand(dir string) (string, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if processGone(err) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	cmd := strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))
	if cmd != "" {
		return cmd, false, nil
	}
	comm, err := os.ReadFile(filepath.Join(dir, "comm"))
	if processGone(err) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(comm)), false, nil
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
		procs, err := survivingRunProcesses(l.cfg.Store, runID)
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
		return laneOutcome{stop: stopLaneRetained, laneRefusal: holdLaneChildrenRefusal(lane, pids)}
	}
	if err != nil {
		l.reportf("loop: lane %s retained, and its item could not be held: %v\n", lane, err)
		return laneOutcome{stop: StopFault}
	}
	return laneOutcome{stop: stopLaneRetained}
}
