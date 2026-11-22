//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// An inherited perf task clock retains CPU from exited descendants. Disabled
// plus enable_on_exec lets the child start counting at exec without counting the
// launching thread, or stopping the check. See perf_event_open(2).
type taskClockAttribute struct {
	Type, Size                                          uint32
	Config, SamplePeriod, SampleType, ReadFormat, Flags uint64
	WakeupEvents, BPType                                uint32
	Config1                                             uint64
}

const taskClockInherit = uint64(1 << 1)

func taskClockConfig() taskClockAttribute {
	attr := taskClockAttribute{Type: 1, Config: 1, Flags: 1 | taskClockInherit | 1<<12}
	attr.Size = uint32(unsafe.Sizeof(attr))
	return attr
}
func openTaskClock(pid int) (*os.File, error) {
	attr := taskClockConfig()
	fd, _, errno := syscall.Syscall6(syscall.SYS_PERF_EVENT_OPEN, uintptr(unsafe.Pointer(&attr)), uintptr(pid), ^uintptr(0), ^uintptr(0), 8, 0)
	if errno != 0 {
		return nil, fmt.Errorf("kernel task-clock counter: %w", errno)
	}
	return os.NewFile(fd, "check task clock"), nil
}
func startAccountedCommand(cmd *exec.Cmd) (func(int, *os.File) (watchSample, error), io.Closer, error) {
	return startWithTaskClock(cmd, openTaskClock)
}

type taskClockCloser struct {
	counter  *os.File
	released chan struct{}
	once     sync.Once
	err      error
}

func (c *taskClockCloser) Close() error {
	c.once.Do(func() {
		c.err = c.counter.Close()
		close(c.released)
	})
	return c.err
}

func startWithTaskClock(cmd *exec.Cmd, open func(int) (*os.File, error)) (func(int, *os.File) (watchSample, error), io.Closer, error) {
	type launch struct {
		sample func(int, *os.File) (watchSample, error)
		closer io.Closer
		err    error
	}
	ready := make(chan launch)
	go func() {
		// StartProcess forks on this thread. Keep it reserved until the event is
		// closed: reusing it could make another command inherit this check's clock.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		counter, denied := open(syscall.Gettid())
		if err := cmd.Start(); err != nil {
			if counter != nil {
				counter.Close()
			}
			ready <- launch{err: err}
			return
		}
		if denied != nil {
			if counter != nil {
				counter.Close()
			}
			ready <- launch{sample: processSampler("/proc", cmd.Process.Pid, denied)}
			return
		}
		closer := &taskClockCloser{counter: counter, released: make(chan struct{})}
		ready <- launch{sample: clockSampler(counter, nil), closer: closer}
		<-closer.released
	}()
	result := <-ready
	return result.sample, result.closer, result.err
}

type processIdentity struct{ pid, start string }

// Linux's /proc stat child times use USER_HZ (100 on Go's Linux targets),
// independent of the scheduler's configured HZ. schedstat is in nanoseconds.
const procTickNS = uint64(10000000)

func processSampler(root string, group int, denied error) func(int, *os.File) (watchSample, error) {
	var previous map[processIdentity]uint64
	var high uint64
	return func(_ int, log *os.File) (watchSample, error) {
		members, total, changed, err := readProcessGroup(root, group)
		if err != nil {
			return watchSample{}, fmt.Errorf("%v; process accounting: %w", denied, err)
		}
		if previous != nil {
			if len(previous) != len(members) {
				changed = true
			}
			for id, ns := range members {
				before, exists := previous[id]
				if !exists || before != ns {
					changed = true
				}
			}
		}
		previous = members
		if total > high {
			high = total
		}
		sample, err := sampleOutput(log, high, nil)
		sample.CPUSource = "process accounting"
		sample.CPUChanged = changed
		return sample, err
	}
}

func processGone(err error) bool { return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH) }

func readProcessGroup(root string, group int) (map[processIdentity]uint64, uint64, bool, error) {
	return readProcessGroupWithRead(root, group, os.ReadFile)
}

func readProcessGroupWithRead(root string, group int, readFile func(string) ([]byte, error)) (map[processIdentity]uint64, uint64, bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, false, err
	}
	members := map[processIdentity]uint64{}
	var total uint64
	changed := false
	type memberStat struct {
		pid    string
		fields []string
	}
	byPID := map[int]memberStat{}
	childrenOf := map[int][]int{}
	var queue []int
	seen := map[int]bool{}
	// Capture all reaped-child times before reading any live thread runtime.
	// Otherwise a child read before its parent can be counted twice if reaped
	// between those reads. Missing later runtime is progress, not a total.
	// Follow descendants across session/process-group boundaries (e.g. subreaper
	// guards started with setsid) so process accounting matches the inherited
	// kernel task clock.
	for _, entry := range entries {
		pid := entry.Name()
		pidNum, err := strconv.Atoi(pid)
		if err != nil {
			continue
		}
		body, err := readFile(filepath.Join(root, pid, "stat"))
		if processGone(err) {
			continue
		} // membership comparison catches a known disappearance
		if err != nil {
			return nil, 0, false, err
		}
		// comm may contain spaces and ')'; fields after its last ')' are fixed.
		end := strings.LastIndexByte(string(body), ')')
		if end < 0 {
			return nil, 0, false, fmt.Errorf("malformed process stat for %s", pid)
		}
		fields := strings.Fields(string(body)[end+1:])
		if len(fields) < 20 {
			return nil, 0, false, fmt.Errorf("short process stat for %s", pid)
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, 0, false, err
		}
		pg, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, 0, false, err
		}
		byPID[pidNum] = memberStat{pid, fields}
		childrenOf[ppid] = append(childrenOf[ppid], pidNum)
		if pg == group || pidNum == group {
			seen[pidNum] = true
			queue = append(queue, pidNum)
		}
	}
	var snapshots []memberStat
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		snapshots = append(snapshots, byPID[cur])
		for _, child := range childrenOf[cur] {
			if !seen[child] {
				seen[child] = true
				queue = append(queue, child)
			}
		}
	}
	for _, snapshot := range snapshots {
		pid, fields := snapshot.pid, snapshot.fields
		id := processIdentity{pid, fields[19]}
		tasks, err := os.ReadDir(filepath.Join(root, pid, "task"))
		if processGone(err) {
			if _, aliveErr := os.Stat(filepath.Join(root, pid)); aliveErr == nil {
				return nil, 0, false, err
			}
			changed = true
			continue
		}
		if err != nil {
			return nil, 0, false, err
		}
		var ns uint64
		for _, task := range tasks {
			data, err := readFile(filepath.Join(root, pid, "task", task.Name(), "schedstat"))
			if processGone(err) {
				if _, aliveErr := os.Stat(filepath.Join(root, pid, "task", task.Name())); aliveErr == nil {
					return nil, 0, false, err
				}
				changed = true
				continue
			}
			if err != nil {
				return nil, 0, false, err
			}
			values := strings.Fields(string(data))
			if len(values) == 0 {
				return nil, 0, false, fmt.Errorf("empty schedstat for %s", pid)
			}
			value, err := strconv.ParseUint(values[0], 10, 64)
			if err != nil {
				return nil, 0, false, err
			}
			ns += value
		}
		// Check identity again: a disappearing/reused PID must be progress, not idle.
		after, err := readFile(filepath.Join(root, pid, "stat"))
		if processGone(err) {
			changed = true
			continue
		}
		if err != nil {
			return nil, 0, false, err
		}
		last := strings.LastIndexByte(string(after), ')')
		if last < 0 {
			return nil, 0, false, fmt.Errorf("malformed process stat for %s", pid)
		}
		latest := strings.Fields(string(after)[last+1:])
		if len(latest) < 20 {
			return nil, 0, false, fmt.Errorf("short process stat for %s", pid)
		}
		if latest[19] != id.start || latest[2] != fields[2] {
			changed = true
			continue
		}
		children := uint64(0)
		for _, field := range fields[13:15] {
			value, err := strconv.ParseInt(field, 10, 64)
			if err != nil || value < 0 {
				return nil, 0, false, fmt.Errorf("invalid child CPU for %s", pid)
			}
			children += uint64(value) * procTickNS
		}
		members[id] = ns
		total += ns + children
	}
	return members, total, changed, nil
}
