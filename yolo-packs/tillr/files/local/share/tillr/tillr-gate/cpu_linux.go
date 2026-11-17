//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

// Linux perf_event_open's inherited task clock counts CPU across descendants and
// retains exited-child counts independently of wait/SIGCHLD disposition.
// https://man7.org/linux/man-pages/man2/perf_event_open.2.html
// The initial exec stop is essential: attaching after execution starts can miss
// already-forked children, making a later quiet sample unsafe.
type taskClockAttribute struct {
	Type, Size                                          uint32
	Config, SamplePeriod, SampleType, ReadFormat, Flags uint64
	WakeupEvents, BPType                                uint32
	Config1                                             uint64
}

const taskClockInherit = uint64(1 << 1)

func taskClockConfig() taskClockAttribute {
	attr := taskClockAttribute{Type: 1, Config: 1, Flags: taskClockInherit}
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
func startWithTaskClock(cmd *exec.Cmd, open func(int) (*os.File, error)) (func(int, *os.File) (watchSample, error), io.Closer, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	cmd.SysProcAttr.Ptrace = true
	if err := cmd.Start(); err != nil {
		if !errors.Is(err, syscall.EPERM) {
			return nil, nil, err
		}
		// A denied exec stop must not prevent the check from running. Detection is
		// explicitly unavailable, not replaced with incomplete process snapshots.
		cmd.SysProcAttr.Ptrace = false
		if startErr := cmd.Start(); startErr != nil {
			return nil, nil, startErr
		}
		return clockSampler(nil, fmt.Errorf("CPU counter could not be attached before exec: %w", err)), nil, nil
	}
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(cmd.Process.Pid, &status, 0, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil || !status.Stopped() {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			cmd.Wait()
			return nil, nil, fmt.Errorf("waiting for CPU-counter exec stop: %v (status %v)", err, status)
		}
		break
	}
	counter, unavailable := open(cmd.Process.Pid)
	if err := syscall.PtraceDetach(cmd.Process.Pid); err != nil {
		if counter != nil {
			counter.Close()
		}
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
		return nil, nil, fmt.Errorf("resuming check after CPU-counter attachment: %w", err)
	}
	var closer io.Closer
	if counter != nil {
		closer = counter
	}
	return clockSampler(counter, unavailable), closer, nil
}
