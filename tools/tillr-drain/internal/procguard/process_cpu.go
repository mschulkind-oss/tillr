package procguard

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ProcessCPUKind records the CPU percent over the preceding minute for the drain loop
// itself and each visible tillr watch wait and tillr-view serve process in /proc.
const ProcessCPUKind = "drain.process-cpu"

const (
	ProcessRoleDrainLoop = "drain-loop"
	ProcessRoleWatchWait = "tillr-watch-wait"
	ProcessRoleViewServe = "tillr-view-serve"
)

func init() {
	if err := event.Register(ProcessCPUKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

const (
	processCPUInterval      = time.Minute
	procClockTicksPerSecond = 100
)

// ProcessCPUEntry is one monitored process's CPU percent over the sample window.
type ProcessCPUEntry struct {
	PID        int    `json:"pid"`
	Role       string `json:"role"`
	CPUPercent int    `json:"cpu_percent"`
	ProcessKey string `json:"process_key"`
}

// ProcessCPUFact is the payload of a ProcessCPUKind event.
type ProcessCPUFact struct {
	SessionID           string            `json:"session_id"`
	WindowMS            int64             `json:"window_ms"`
	SelfPID             int               `json:"self_pid"`
	SelfCPUPercent      int               `json:"self_cpu_percent"`
	WatchWaitPID        int               `json:"watch_wait_pid,omitempty"`
	WatchWaitCPUPercent int               `json:"watch_wait_cpu_percent"`
	ViewServePID        int               `json:"view_serve_pid,omitempty"`
	ViewServeCPUPercent int               `json:"view_serve_cpu_percent"`
	MaxCPUPercent       int               `json:"max_cpu_percent"`
	MaxRole             string            `json:"max_role"`
	MaxPID              int               `json:"max_pid"`
	Processes           []ProcessCPUEntry `json:"processes"`
}

type procIdentity struct {
	pid       int
	startTime uint64
}

type procSample struct {
	pid       int
	role      string
	startTime uint64
	ticks     uint64
}

type ProcessCPUSnapshot struct {
	at    time.Time
	ticks map[procIdentity]uint64
}

// CPUTrackerParams provides the loop state and callbacks needed by RecordProcessCPUIfDue.
type CPUTrackerParams struct {
	Mu             *sync.Mutex
	Last           **ProcessCPUSnapshot
	ProcRoot       string
	SelfPID        int
	Now            func() time.Time
	StorePath      string
	SessionInfo    func() (id string, initialized bool)
	SessionOptions []store.OpenOption
	Reportf        func(format string, args ...any)
}

func resolveProcRoot(procRoot string) string {
	if procRoot != "" {
		return procRoot
	}
	return "/proc"
}

func resolveSelfPID(selfPID int) int {
	if selfPID > 0 {
		return selfPID
	}
	return os.Getpid()
}

func PrimeProcessCPU(mu *sync.Mutex, last **ProcessCPUSnapshot, procRoot string, selfPID int, now time.Time) {
	mu.Lock()
	defer mu.Unlock()
	if *last != nil {
		return
	}
	at := now.UTC()
	if samples, ok := readProcessCPUSamples(resolveProcRoot(procRoot), resolveSelfPID(selfPID)); ok {
		*last = snapshotFromSamples(at, samples)
	}
}

// RecordProcessCPUIfDue samples /proc once per processCPUInterval and appends one
// ProcessCPUKind event covering the elapsed minute. The first call captures the
// baseline ticks without appending an event; write errors are logged and never stop
// the loop.
func RecordProcessCPUIfDue(ctx context.Context, p CPUTrackerParams) {
	p.Mu.Lock()
	defer p.Mu.Unlock()

	procRoot := resolveProcRoot(p.ProcRoot)
	selfPID := resolveSelfPID(p.SelfPID)
	at := p.Now().UTC()
	if *p.Last == nil {
		if samples, ok := readProcessCPUSamples(procRoot, selfPID); ok {
			*p.Last = snapshotFromSamples(at, samples)
		}
		return
	}
	elapsed := at.Sub((*p.Last).at)
	if elapsed < processCPUInterval || p.StorePath == "" {
		return
	}
	samples, ok := readProcessCPUSamples(procRoot, selfPID)
	if !ok {
		return
	}

	id, initialized := p.SessionInfo()
	if id == "" {
		id = "loop"
	}

	fact := buildProcessCPUFact(id, elapsed, *p.Last, samples)
	body, err := json.Marshal(fact)
	if err != nil {
		if ctx.Err() == nil && p.Reportf != nil {
			p.Reportf("loop: process cpu was not recorded: %v\n", err)
		}
		return
	}

	s, err := store.Open(ctx, p.StorePath, !initialized, p.SessionOptions...)
	if err != nil {
		if ctx.Err() == nil && p.Reportf != nil {
			p.Reportf("loop: process cpu was not recorded: %v\n", err)
		}
		return
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       ProcessCPUKind,
			ObjectType: "session",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	})
	if closeErr := s.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		if ctx.Err() == nil && p.Reportf != nil {
			p.Reportf("loop: process cpu was not recorded: %v\n", err)
		}
		return
	}
	*p.Last = snapshotFromSamples(at, samples)
}

func snapshotFromSamples(at time.Time, samples []procSample) *ProcessCPUSnapshot {
	ticks := make(map[procIdentity]uint64, len(samples))
	for _, s := range samples {
		ticks[procIdentity{pid: s.pid, startTime: s.startTime}] = s.ticks
	}
	return &ProcessCPUSnapshot{at: at, ticks: ticks}
}

func buildProcessCPUFact(sessionID string, elapsed time.Duration, prev *ProcessCPUSnapshot, samples []procSample) ProcessCPUFact {
	elapsedSec := elapsed.Seconds()
	processes := make([]ProcessCPUEntry, 0, len(samples))
	fact := ProcessCPUFact{
		SessionID: sessionID,
		WindowMS:  elapsed.Milliseconds(),
	}
	for i, s := range samples {
		id := procIdentity{pid: s.pid, startTime: s.startTime}
		var delta uint64
		if prevTicks, seen := prev.ticks[id]; seen {
			if s.ticks >= prevTicks {
				delta = s.ticks - prevTicks
			}
		} else {
			delta = s.ticks
		}
		pct := 0
		if elapsedSec > 0 {
			pct = int(math.Round(float64(delta) * 100.0 / (float64(procClockTicksPerSecond) * elapsedSec)))
		}
		entry := ProcessCPUEntry{
			PID:        s.pid,
			Role:       s.role,
			CPUPercent: pct,
			ProcessKey: s.role + ":" + strconv.Itoa(s.pid) + ":" + strconv.FormatUint(s.startTime, 10),
		}
		processes = append(processes, entry)

		switch s.role {
		case ProcessRoleDrainLoop:
			fact.SelfPID = s.pid
			fact.SelfCPUPercent = pct
		case ProcessRoleWatchWait:
			if fact.WatchWaitPID == 0 || pct > fact.WatchWaitCPUPercent {
				fact.WatchWaitPID = s.pid
				fact.WatchWaitCPUPercent = pct
			}
		case ProcessRoleViewServe:
			if fact.ViewServePID == 0 || pct > fact.ViewServeCPUPercent {
				fact.ViewServePID = s.pid
				fact.ViewServeCPUPercent = pct
			}
		}
		if i == 0 || pct > fact.MaxCPUPercent {
			fact.MaxCPUPercent = pct
			fact.MaxRole = s.role
			fact.MaxPID = s.pid
		}
	}
	fact.Processes = processes
	return fact
}

func readProcessCPUSamples(procRoot string, selfPID int) ([]procSample, bool) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, false
	}
	_, selfTicks, selfStart, ok := readProcStat(filepath.Join(procRoot, strconv.Itoa(selfPID), "stat"))
	if !ok {
		return nil, false
	}

	var watchSamples, viewSamples []procSample
	for _, entry := range entries {
		name := entry.Name()
		pid, err := strconv.Atoi(name)
		if err != nil || pid <= 0 || strconv.Itoa(pid) != name || pid == selfPID {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(procRoot, name, "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		role, matched := classifyMonitoredCmdline(cmdline)
		if !matched {
			continue
		}
		state, ticks, startTime, ok := readProcStat(filepath.Join(procRoot, name, "stat"))
		if !ok || state == "Z" || state == "X" {
			continue
		}
		sample := procSample{pid: pid, role: role, startTime: startTime, ticks: ticks}
		if role == ProcessRoleWatchWait {
			watchSamples = append(watchSamples, sample)
		} else {
			viewSamples = append(viewSamples, sample)
		}
	}
	byPID := func(a, b procSample) int { return cmp.Compare(a.pid, b.pid) }
	slices.SortFunc(watchSamples, byPID)
	slices.SortFunc(viewSamples, byPID)

	out := make([]procSample, 0, 1+len(watchSamples)+len(viewSamples))
	out = append(out, procSample{pid: selfPID, role: ProcessRoleDrainLoop, startTime: selfStart, ticks: selfTicks})
	out = append(out, watchSamples...)
	out = append(out, viewSamples...)
	return out, true
}

func readProcStat(path string) (state string, ticks uint64, startTime uint64, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", 0, 0, false
	}
	text := string(raw)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return "", 0, 0, false
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) <= 19 {
		return "", 0, 0, false
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	start, err3 := strconv.ParseUint(fields[19], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return "", 0, 0, false
	}
	return fields[0], utime + stime, start, true
}

func classifyMonitoredCmdline(raw []byte) (string, bool) {
	trimmed := strings.TrimRight(string(raw), "\x00")
	if trimmed == "" {
		return "", false
	}
	argv := strings.Split(trimmed, "\x00")
	if len(argv) < 2 {
		return "", false
	}
	switch {
	case matchesTillrBinary(argv[0], "tillr-watch") && argv[1] == "wait":
		return ProcessRoleWatchWait, true
	case len(argv) >= 3 && matchesTillrBinary(argv[0], "tillr") && argv[1] == "watch" && argv[2] == "wait":
		return ProcessRoleWatchWait, true
	case matchesTillrBinary(argv[0], "tillr-view") && argv[1] == "serve":
		return ProcessRoleViewServe, true
	case len(argv) >= 3 && matchesTillrBinary(argv[0], "tillr") && argv[1] == "view" && argv[2] == "serve":
		return ProcessRoleViewServe, true
	default:
		return "", false
	}
}

func matchesTillrBinary(arg0, prog string) bool {
	base := filepath.Base(arg0)
	if base == prog {
		return true
	}
	return hasHexDigestSuffix(base, prog+"-") || hasHexDigestSuffix(base, "current-"+prog+"-")
}

func hasHexDigestSuffix(name, prefix string) bool {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok || len(rest) < 8 {
		return false
	}
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
