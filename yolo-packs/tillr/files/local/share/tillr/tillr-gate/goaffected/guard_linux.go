//go:build linux

package goaffected

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// InvocationGuardFlag is the internal flag used to launch the subreaper guard.
	InvocationGuardFlag = "--invocation-guard"
	// PRSetChildSubreaper is the Linux prctl option PR_SET_CHILD_SUBREAPER.
	PRSetChildSubreaper = 36

	defaultWatchdogInterval      = 1000 * time.Millisecond
	defaultWatchdogSilentSamples = 60
)

var (
	watchdogIntervalRE = regexp.MustCompile(`(?m)^\s*const\s+WatchdogSampleInterval\s*=\s*(?:(\d+)\s*\*\s*)?time\.(Nanosecond|Microsecond|Millisecond|Second|Minute|Hour)\b`)
	watchdogSilentRE   = regexp.MustCompile(`(?m)^\s*const\s+WatchdogSilentSamples\s*=\s*(\d+)\b`)
	goTimeUnitDur      = map[string]time.Duration{
		"Nanosecond":  time.Nanosecond,
		"Microsecond": time.Microsecond,
		"Millisecond": time.Millisecond,
		"Second":      time.Second,
		"Minute":      time.Minute,
		"Hour":        time.Hour,
	}

	liveGuardsMu sync.Mutex
	liveGuards   = make(map[int]*os.File)
)

// SetChildSubreaper marks the current process as a Linux child subreaper.
func SetChildSubreaper() {
	_, _, _ = syscall.Syscall6(syscall.SYS_PRCTL, PRSetChildSubreaper, 1, 0, 0, 0, 0)
}

// ProcChildrenAndPgrp returns (all descendant/pgrp PIDs of rootPID, live non-zombie PIDs to kill).
func ProcChildrenAndPgrp(rootPID int) (map[int]bool, map[int]bool) {
	children := make(map[int][]int)
	states := make(map[int]string)
	samePgrp := make(map[int]bool)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return map[int]bool{}, map[int]bool{}
	}
	for _, ent := range entries {
		name := ent.Name()
		pid, err := strconv.Atoi(name)
		if err != nil || pid == rootPID {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", name, "stat"))
		if err != nil {
			continue
		}
		s := string(raw)
		rparen := strings.LastIndexByte(s, ')')
		if rparen < 0 || rparen+2 > len(s) {
			continue
		}
		fields := strings.Fields(s[rparen+2:])
		if len(fields) < 3 {
			continue
		}
		ppid, err1 := strconv.Atoi(fields[1])
		pgrp, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		states[pid] = fields[0]
		children[ppid] = append(children[ppid], pid)
		if pgrp == rootPID {
			samePgrp[pid] = true
		}
	}

	descendants := make(map[int]bool)
	stack := append([]int(nil), children[rootPID]...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if descendants[cur] {
			continue
		}
		descendants[cur] = true
		stack = append(stack, children[cur]...)
	}

	allTargets := make(map[int]bool, len(descendants)+len(samePgrp))
	live := make(map[int]bool)
	for p := range descendants {
		allTargets[p] = true
		if states[p] != "Z" {
			live[p] = true
		}
	}
	for p := range samePgrp {
		allTargets[p] = true
		if states[p] != "Z" {
			live[p] = true
		}
	}
	return allTargets, live
}

func reapNonblocking() bool {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if err != nil {
			return true
		}
		if pid == 0 {
			return false
		}
	}
}

// KillAndReapDescendants kills and reaps every descendant and process-group member of rootPID.
func KillAndReapDescendants(rootPID int, maxWait time.Duration) {
	if maxWait <= 0 {
		maxWait = 5 * time.Second
	}
	deadline := time.Now().Add(maxWait)
	for {
		allTargets, live := ProcChildrenAndPgrp(rootPID)
		for pid := range live {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		noChildren := reapNonblocking()
		if len(live) == 0 && len(allTargets) == 0 && noChildren {
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// LoadWatchdogConstants reads WatchdogSampleInterval and WatchdogSilentSamples from
// tools/tillr-gate/watchdog.go, falling back to its defaults when unavailable.
func LoadWatchdogConstants(watchdogPath string) (time.Duration, int) {
	var candidates []string
	if watchdogPath != "" {
		candidates = append(candidates, watchdogPath)
	} else {
		if wd, err := os.Getwd(); err == nil {
			candidates = append(candidates,
				filepath.Join(wd, "watchdog.go"),
				filepath.Join(wd, "tools", "tillr-gate", "watchdog.go"),
			)
		}
		if exe, err := os.Executable(); err == nil {
			candidates = append(candidates,
				filepath.Join(filepath.Dir(exe), "watchdog.go"),
				filepath.Join(filepath.Dir(exe), "tools", "tillr-gate", "watchdog.go"),
			)
		}
	}
	for _, p := range candidates {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		mInt := watchdogIntervalRE.FindSubmatch(raw)
		mSil := watchdogSilentRE.FindSubmatch(raw)
		if mInt == nil || mSil == nil {
			continue
		}
		mult := 1
		if len(mInt[1]) > 0 {
			if v, err := strconv.Atoi(string(mInt[1])); err == nil {
				mult = v
			}
		}
		unit, ok := goTimeUnitDur[string(mInt[2])]
		if !ok {
			continue
		}
		interval := time.Duration(mult) * unit
		silent, err := strconv.Atoi(string(mSil[1]))
		if err == nil && interval > 0 && silent > 0 {
			return interval, silent
		}
	}
	return defaultWatchdogInterval, defaultWatchdogSilentSamples
}

// ProcMemberID uniquely identifies a process by PID and /proc/<pid>/stat start time.
type ProcMemberID struct {
	PID       int
	StartTime string
}

// BinaryStallWatchdog applies tools/tillr-gate's watchdog sample rule to one test binary's process-tree CPU.
type BinaryStallWatchdog struct {
	SilentThreshold int
	PreviousMembers map[ProcMemberID]uint64
	HighCPUNS       uint64
	PreviousCPUNS   uint64
	Initialized     bool
	SilentSamples   int
	Unavailable     string
}

// NewBinaryStallWatchdog creates a BinaryStallWatchdog with silentThreshold.
func NewBinaryStallWatchdog(silentThreshold int) *BinaryStallWatchdog {
	return &BinaryStallWatchdog{SilentThreshold: silentThreshold}
}

// Observe updates stall state from one CPU sample and returns true when SilentThreshold is reached.
func (w *BinaryStallWatchdog) Observe(cpuNS uint64, cpuChanged bool, errStr string) bool {
	if w.Unavailable != "" {
		return false
	}
	if errStr != "" {
		w.Unavailable = "watchdog unavailable: " + errStr
		w.SilentSamples = 0
		return false
	}
	if !w.Initialized {
		w.PreviousCPUNS = cpuNS
		w.Initialized = true
		return false
	}
	if cpuChanged || cpuNS > w.PreviousCPUNS {
		w.SilentSamples = 0
	} else {
		w.SilentSamples++
	}
	w.PreviousCPUNS = cpuNS
	return w.SilentSamples >= w.SilentThreshold
}

// ObserveTree updates stall state from a process-tree CPU snapshot.
func (w *BinaryStallWatchdog) ObserveTree(members map[ProcMemberID]uint64, totalNS uint64, changed bool, errStr string) bool {
	if errStr != "" {
		return w.Observe(0, false, errStr)
	}
	if w.PreviousMembers != nil {
		if len(w.PreviousMembers) != len(members) {
			changed = true
		} else {
			for id, ns := range members {
				if prev, ok := w.PreviousMembers[id]; !ok || prev != ns {
					changed = true
					break
				}
			}
		}
	}
	w.PreviousMembers = members
	if totalNS > w.HighCPUNS {
		w.HighCPUNS = totalNS
	}
	return w.Observe(w.HighCPUNS, changed, "")
}

func isProcGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ESRCH)
}

func parseProcStatFields(raw string) []string {
	end := strings.LastIndexByte(raw, ')')
	if end < 0 {
		return nil
	}
	fields := strings.Fields(raw[end+1:])
	if len(fields) < 20 {
		return nil
	}
	return fields
}

// ScanProcStats scans /proc/<pid>/stat into maps by PID, parent PID, and process group.
func ScanProcStats(procRoot string) (byPID map[int][]string, childrenOf map[int][]int, byPgrp map[int][]int, ok bool) {
	if procRoot == "" {
		procRoot = "/proc"
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, nil, nil, false
	}
	byPID = make(map[int][]string)
	childrenOf = make(map[int][]int)
	byPgrp = make(map[int][]int)
	for _, ent := range entries {
		pid, err := strconv.Atoi(ent.Name())
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, ent.Name(), "stat"))
		if err != nil {
			continue
		}
		fields := parseProcStatFields(string(raw))
		if fields == nil {
			continue
		}
		ppid, err1 := strconv.Atoi(fields[1])
		pgrp, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			continue
		}
		byPID[pid] = fields
		childrenOf[ppid] = append(childrenOf[ppid], pid)
		byPgrp[pgrp] = append(byPgrp[pgrp], pid)
	}
	return byPID, childrenOf, byPgrp, true
}

// IsGoTestBinary reports whether pid is a compiled Go test binary (*.test with -test.* args).
func IsGoTestBinary(procRoot string, pid int) bool {
	if procRoot == "" {
		procRoot = "/proc"
	}
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	parts := bytes.Split(raw, []byte{0})
	var args []string
	for _, p := range parts {
		if len(p) > 0 {
			args = append(args, string(p))
		}
	}
	if len(args) == 0 || !strings.HasSuffix(args[0], ".test") {
		return false
	}
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "-test.") {
			return true
		}
	}
	return false
}

// SampleProcessTreeCPU samples schedstat and child CPU across rootPID's process tree.
func SampleProcessTreeCPU(
	procRoot string,
	rootPID int,
	byPID map[int][]string,
	childrenOf map[int][]int,
	byPgrp map[int][]int,
) (members map[ProcMemberID]uint64, totalNS uint64, changed bool, errStr string) {
	if procRoot == "" {
		procRoot = "/proc"
	}
	if _, ok := byPID[rootPID]; !ok {
		return map[ProcMemberID]uint64{}, 0, true, ""
	}
	queue := []int{rootPID}
	seen := map[int]bool{rootPID: true}
	for _, p := range byPgrp[rootPID] {
		if _, ok := byPID[p]; ok && !seen[p] {
			seen[p] = true
			queue = append(queue, p)
		}
	}
	type snap struct {
		pid    int
		fields []string
	}
	var snapshots []snap
	for idx := 0; idx < len(queue); idx++ {
		cur := queue[idx]
		snapshots = append(snapshots, snap{pid: cur, fields: byPID[cur]})
		for _, child := range childrenOf[cur] {
			if _, ok := byPID[child]; ok && !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}

	const tickNS uint64 = 10_000_000 // USER_HZ = 100 on Linux
	members = make(map[ProcMemberID]uint64)
	for _, s := range snapshots {
		pid := s.pid
		fields := s.fields
		memberID := ProcMemberID{PID: pid, StartTime: fields[19]}
		pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
		taskDir := filepath.Join(pidDir, "task")
		tasks, err := os.ReadDir(taskDir)
		if err != nil {
			if isProcGone(err) {
				if _, serr := os.Stat(pidDir); serr == nil {
					return map[ProcMemberID]uint64{}, 0, false, err.Error()
				}
				changed = true
				continue
			}
			return map[ProcMemberID]uint64{}, 0, false, err.Error()
		}
		var ns uint64
		taskGone := false
		for _, task := range tasks {
			tpath := filepath.Join(taskDir, task.Name())
			data, err := os.ReadFile(filepath.Join(tpath, "schedstat"))
			if err != nil {
				if isProcGone(err) {
					if _, serr := os.Stat(tpath); serr == nil {
						return map[ProcMemberID]uint64{}, 0, false, err.Error()
					}
					changed = true
					taskGone = true
					continue
				}
				return map[ProcMemberID]uint64{}, 0, false, err.Error()
			}
			vals := strings.Fields(string(data))
			if len(vals) == 0 {
				return map[ProcMemberID]uint64{}, 0, false, fmt.Sprintf("empty schedstat for %d", pid)
			}
			v, perr := strconv.ParseUint(vals[0], 10, 64)
			if perr != nil {
				return map[ProcMemberID]uint64{}, 0, false, perr.Error()
			}
			ns += v
		}
		_ = taskGone
		afterRaw, err := os.ReadFile(filepath.Join(pidDir, "stat"))
		if err != nil {
			if isProcGone(err) {
				changed = true
				continue
			}
			return map[ProcMemberID]uint64{}, 0, false, err.Error()
		}
		latest := parseProcStatFields(string(afterRaw))
		if latest == nil {
			return map[ProcMemberID]uint64{}, 0, false, fmt.Sprintf("malformed process stat for %d", pid)
		}
		if latest[19] != fields[19] || latest[2] != fields[2] {
			changed = true
			continue
		}
		cutime, err1 := strconv.ParseInt(fields[13], 10, 64)
		cstime, err2 := strconv.ParseInt(fields[14], 10, 64)
		if err1 != nil || err2 != nil {
			return map[ProcMemberID]uint64{}, 0, false, fmt.Sprintf("invalid child CPU for %d", pid)
		}
		if cutime < 0 || cstime < 0 {
			return map[ProcMemberID]uint64{}, 0, false, fmt.Sprintf("invalid child CPU for %d", pid)
		}
		childrenNS := uint64(cutime+cstime) * tickNS
		members[memberID] = ns
		totalNS += ns + childrenNS
	}
	return members, totalNS, changed, ""
}

func signalIfSameProcess(procRoot string, pid int, expectedStart string, sig syscall.Signal) bool {
	if procRoot == "" {
		procRoot = "/proc"
	}
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	fields := parseProcStatFields(string(raw))
	if fields == nil || fields[0] == "Z" || fields[19] != expectedStart {
		return false
	}
	return syscall.Kill(pid, sig) == nil
}

// StepGoTestWatchdog performs one watchdog observation tick for goPID's *.test children.
func StepGoTestWatchdog(
	goPID int,
	silentThreshold int,
	watchers map[ProcMemberID]*BinaryStallWatchdog,
	signalled map[ProcMemberID]bool,
	procRoot string,
	onSignal func(int),
) {
	if procRoot == "" {
		procRoot = "/proc"
	}
	byPID, childrenOf, byPgrp, ok := ScanProcStats(procRoot)
	if !ok {
		return
	}
	goFields, exists := byPID[goPID]
	if !exists || goFields[0] == "Z" {
		return
	}

	liveChildren := make(map[ProcMemberID]bool)
	for _, pid := range childrenOf[goPID] {
		fields := byPID[pid]
		if fields == nil || fields[0] == "Z" {
			continue
		}
		id := ProcMemberID{PID: pid, StartTime: fields[19]}
		liveChildren[id] = true
		if _, hasW := watchers[id]; !hasW && !signalled[id] {
			if IsGoTestBinary(procRoot, pid) {
				watchers[id] = NewBinaryStallWatchdog(silentThreshold)
			}
		}
	}

	for id := range watchers {
		if !liveChildren[id] {
			delete(watchers, id)
		}
	}

	for id, watcher := range watchers {
		if signalled[id] {
			continue
		}
		members, totalNS, changed, errStr := SampleProcessTreeCPU(procRoot, id.PID, byPID, childrenOf, byPgrp)
		if watcher.ObserveTree(members, totalNS, changed, errStr) {
			signalled[id] = true
			delete(watchers, id)
			if onSignal != nil {
				onSignal(id.PID)
			} else {
				signalIfSameProcess(procRoot, id.PID, id.StartTime, syscall.SIGQUIT)
			}
		}
	}
}

func watchGoTestBinaries(goPID int, sampleInterval time.Duration, silentThreshold int, stopCh <-chan struct{}, procRoot string, onSignal func(int)) {
	watchers := make(map[ProcMemberID]*BinaryStallWatchdog)
	signalled := make(map[ProcMemberID]bool)
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for {
		StepGoTestWatchdog(goPID, silentThreshold, watchers, signalled, procRoot, onSignal)
		select {
		case <-stopCh:
			return
		case <-ticker.C:
		}
	}
}

// InvocationGuardMain runs command under a Linux child subreaper, killing and reaping
// every descendant when the command exits, lifelineFD closes, or a termination signal arrives.
func InvocationGuardMain(lifelineFD int, command []string) int {
	if len(command) == 0 {
		return 2
	}
	SetChildSubreaper()
	me := os.Getpid()
	var cleanupMu sync.Mutex
	cleanupAll := func() {
		cleanupMu.Lock()
		defer cleanupMu.Unlock()
		KillAndReapDescendants(me, 5*time.Second)
	}

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		sig := <-sigCh
		cleanupAll()
		if s, ok := sig.(syscall.Signal); ok {
			os.Exit(128 + int(s))
		}
		os.Exit(1)
	}()

	lifelineFile := os.NewFile(uintptr(lifelineFD), "lifeline")
	if lifelineFile != nil {
		go func() {
			buf := make([]byte, 4096)
			for {
				_, err := lifelineFile.Read(buf)
				if err != nil {
					break
				}
			}
			cleanupAll()
			os.Exit(1)
		}()
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cleanupAll()
		return 1
	}
	childPID := cmd.Process.Pid

	stopWatcher := make(chan struct{})
	interval, silentSamples := LoadWatchdogConstants("")
	go watchGoTestBinaries(childPID, interval, silentSamples, stopWatcher, "/proc", nil)

	code := 0
	for {
		var ws syscall.WaitStatus
		wpid, err := syscall.Wait4(-1, &ws, 0, nil)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			break
		}
		if wpid == childPID {
			if ws.Signaled() {
				code = 128 + int(ws.Signal())
			} else {
				code = ws.ExitStatus()
			}
			break
		}
	}

	close(stopWatcher)
	cleanupAll()
	return code
}

// StopLiveGuards sends SIGTERM to all currently running invocation-guard processes.
func StopLiveGuards() {
	liveGuardsMu.Lock()
	pids := make([]int, 0, len(liveGuards))
	for pid := range liveGuards {
		pids = append(pids, pid)
	}
	liveGuardsMu.Unlock()
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

// InstallSignalHandlers installs SIGTERM, SIGHUP, and SIGINT handlers that stop live guards.
func InstallSignalHandlers() {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		StopLiveGuards()
		if s, ok := sig.(syscall.Signal); ok {
			os.Exit(128 + int(s))
		}
		os.Exit(1)
	}()
}

// RunGuardedCommand runs cmd under the subreaper guard, streaming stdout+stderr lines to onLine.
func RunGuardedCommand(cmd []string, cwd string, env []string, onLine func(string)) (int, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return 1, "", err
	}
	lifelineR, lifelineW, err := os.Pipe()
	if err != nil {
		return 1, "", err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = lifelineR.Close()
		_ = lifelineW.Close()
		return 1, "", err
	}

	var guardArgs []string
	if len(os.Args) > 1 && (os.Args[1] == "go-affected" || os.Args[1] == "goaffected") {
		guardArgs = append(guardArgs, os.Args[1])
	}
	guardArgs = append(guardArgs, InvocationGuardFlag, "3", "--")
	guardArgs = append(guardArgs, cmd...)

	proc := exec.Command(exe, guardArgs...)
	proc.Dir = cwd
	proc.Env = env
	proc.Stdout = outW
	proc.Stderr = outW
	proc.ExtraFiles = []*os.File{lifelineR} // fd 3 in child
	proc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := proc.Start(); err != nil {
		_ = lifelineR.Close()
		_ = lifelineW.Close()
		_ = outR.Close()
		_ = outW.Close()
		return 1, "", err
	}
	_ = lifelineR.Close()
	_ = outW.Close()

	pid := proc.Process.Pid
	liveGuardsMu.Lock()
	liveGuards[pid] = lifelineW
	liveGuardsMu.Unlock()

	defer func() {
		liveGuardsMu.Lock()
		delete(liveGuards, pid)
		liveGuardsMu.Unlock()
		_ = lifelineW.Close()
	}()

	var chunks strings.Builder
	reader := bufio.NewReader(outR)
	for {
		line, rerr := reader.ReadString('\n')
		if len(line) > 0 {
			chunks.WriteString(line)
			if onLine != nil {
				onLine(line)
			}
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				_ = outR.Close()
				_ = proc.Wait()
				return 1, chunks.String(), rerr
			}
			break
		}
	}
	_ = outR.Close()

	werr := proc.Wait()
	if werr == nil {
		return 0, chunks.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(werr, &exitErr) {
		return exitErr.ExitCode(), chunks.String(), nil
	}
	return 1, chunks.String(), werr
}
