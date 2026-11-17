package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The heavy supervisor's lock follows declaration groups too. If a supervisor is
// killed during cleanup, a surviving group still owns the checkout's lock.
var inheritedHeavyOnce sync.Once
var inheritedHeavyLock *os.File
var inheritedHeavyError error

func inheritHeavyOwnership(cmd *exec.Cmd) error {
	value := os.Getenv("TILLR_HEAVY_LOCK_FD")
	if value == "" {
		return nil
	}
	fd, err := strconv.Atoi(value)
	if err != nil || fd < 3 {
		return fmt.Errorf("invalid inherited heavy lock descriptor %q", value)
	}
	inheritedHeavyOnce.Do(func() {
		inheritedHeavyLock = os.NewFile(uintptr(fd), "heavy ownership lock")
		_, inheritedHeavyError = inheritedHeavyLock.Stat()
	})
	if inheritedHeavyError != nil {
		return fmt.Errorf("heavy ownership lock: %w", inheritedHeavyError)
	}
	// This descriptor protocol is for the gate itself, not for arbitrary check
	// commands (including a gate unit suite whose subprocesses close extra FDs).
	clean := cmd.Env[:0]
	for _, value := range cmd.Env {
		if !strings.HasPrefix(value, "TILLR_HEAVY_LOCK_FD=") {
			clean = append(clean, value)
		}
	}
	cmd.Env = clean
	cmd.ExtraFiles = append(cmd.ExtraFiles, inheritedHeavyLock)
	return nil
}
func endCheckGroup(pgid int) error {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err == syscall.ESRCH {
		return nil
	} else if err != nil {
		return err
	}
	// This observes reaping, not check duration. Ownership is never released while
	// the killed group still has live members; there is no verdict deadline here.
	for {
		alive, err := checkGroupAlive(pgid)
		if err != nil {
			return err
		}
		if !alive {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}
