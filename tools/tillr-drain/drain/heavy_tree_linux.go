//go:build linux

package drain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Linux child adoption makes this process the parent of abandoned descendants,
// even when fork-check starts checks in separate sessions. The kernel preserves
// that ownership while the original drain process is gone.
func prepareHeavyTree() error {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		return err
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err := unix.PidfdSendSignal(fd, 0, nil, 0); err != nil {
		return err
	}
	_, err = heavyTree()
	return err
}
func heavySupervisorIdentity() (string, error) {
	p, err := readHeavyProcess(os.Getpid())
	return fmt.Sprintf("supervisor_pid=%d\nstart_ticks=%s\n", p.pid, p.start), err
}

type heavyProcess struct {
	pid, parent  int
	state, start string
}

// heavyProcessDisappeared distinguishes a process that exited during observation
// from a failure to inspect it. Both absent /proc entries and ESRCH mean it exited.
func heavyProcessDisappeared(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ESRCH)
}

func readHeavyProcess(pid int) (heavyProcess, error) {
	p := heavyProcess{pid: pid}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return p, err
	}
	text := string(raw)
	end := strings.LastIndex(text, ")")
	if end < 0 {
		return p, fmt.Errorf("process %d has no lifetime fields", pid)
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 20 {
		return p, fmt.Errorf("process %d has incomplete lifetime fields", pid)
	}
	p.parent, err = strconv.Atoi(fields[1])
	p.state, p.start = fields[0], fields[19]
	return p, err
}
func heavyTree() ([]heavyProcess, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	all := map[int]heavyProcess{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		p, err := readHeavyProcess(pid)
		if heavyProcessDisappeared(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		all[pid] = p
	}
	owned := []heavyProcess{}
	self := os.Getpid()
	for _, p := range all {
		seen := map[int]bool{}
		ancestor := p.parent
		for ancestor > 0 && !seen[ancestor] {
			if ancestor == self {
				owned = append(owned, p)
				break
			}
			seen[ancestor] = true
			parent, ok := all[ancestor]
			if !ok {
				break
			}
			ancestor = parent.parent
		}
	}
	return owned, nil
}
func killHeavyProcess(p heavyProcess) error {
	// A pidfd addresses one process incarnation, not a PID that could be reused
	// between observation and signaling. Recheck its birth time after opening it.
	fd, err := unix.PidfdOpen(p.pid, 0)
	if err == unix.ESRCH {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	current, err := readHeavyProcess(p.pid)
	if heavyProcessDisappeared(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.start != p.start {
		return nil
	}
	err = unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
	if err == unix.ESRCH {
		return nil
	}
	return err
}
func finishHeavyTree() error {
	for {
		children, err := heavyTree()
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := killHeavyProcess(child); err != nil {
				return err
			}
		}
		// ECHILD, not an empty /proc snapshot, is the kernel's acknowledgement that
		// nothing remains owned. Adoption can race a snapshot while parents exit.
		for {
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if err == unix.ECHILD {
				return nil
			}
			if err == unix.EINTR {
				continue
			}
			if err != nil {
				return err
			}
			if pid == 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}
