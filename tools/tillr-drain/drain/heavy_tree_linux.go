//go:build linux

package drain

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

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

// siginfoPID reads si_pid, the first field of the union that follows si_signo, si_errno
// and si_code, aligned to a pointer.
func siginfoPID(info *unix.Siginfo) int {
	word := unsafe.Sizeof(uintptr(0))
	offset := (3*unsafe.Sizeof(int32(0)) + word - 1) &^ (word - 1)
	return int(*(*int32)(unsafe.Add(unsafe.Pointer(info), offset)))
}

// reapAdoptedOrphans reaps every exited child except direct, the supervised command,
// whose exit status is the command's verdict and belongs to its own Wait. It looks
// before it reaps (WNOWAIT), so an exited direct child is never taken: it stops there,
// and finishHeavyTree reaps whatever remains once the command's Wait has returned.
func reapAdoptedOrphans(direct int) error {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_ALL, 0, &info, unix.WEXITED|unix.WNOHANG|unix.WNOWAIT, nil)
		if err == unix.EINTR {
			continue
		}
		if err == unix.ECHILD {
			return nil
		}
		if err != nil {
			return err
		}
		pid := siginfoPID(&info)
		if pid == 0 || pid == direct {
			return nil
		}
		var status unix.WaitStatus
		if _, err := unix.Wait4(pid, &status, unix.WNOHANG, nil); err != nil && err != unix.EINTR && err != unix.ECHILD {
			return err
		}
	}
}

// reapWhileSupervising reaps adopted orphans on every SIGCHLD while the command runs.
// A child subreaper is the parent of every orphan its command leaves, and an orphan it
// does not wait for stays a zombie: on 2026-11-18 one supervisor of a heavy gate run
// held 6,408 of them. Nothing is reaped until started names the direct child, which a
// failed start never does. The returned stop ends the loop and waits for it.
func reapWhileSupervising(started <-chan int) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, unix.SIGCHLD)
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		defer signal.Stop(signals)
		var direct int
		select {
		case direct = <-started:
		case <-done:
			return
		}
		for {
			if err := reapAdoptedOrphans(direct); err != nil {
				fmt.Fprintln(os.Stderr, "heavy supervisor: reaping adopted processes:", err)
			}
			select {
			case <-signals:
			case <-done:
				return
			}
		}
	}()
	return func() { close(done); <-finished }
}
func heavySupervisorIdentity() (int, string, error) {
	p, err := readHeavyProcess(os.Getpid())
	return p.pid, p.start, err
}

// heavySupervisorRunning reports whether the supervisor incarnation (pid, startTicks)
// is still running. A reused PID has a different start_ticks, and a zombie has
// already closed its descriptors and ended execution.
func heavySupervisorRunning(pid int, startTicks string) (bool, error) {
	p, err := readHeavyProcess(pid)
	if heavyProcessDisappeared(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if p.start != startTicks || p.state == "Z" {
		return false, nil
	}
	return true, nil
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
