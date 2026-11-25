package run

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	runExecIDEnv       = "TILLR_RUN_EXEC_ID"
	runCleanupInterval = 10 * time.Millisecond
)

func newExecID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Internalf("generating execution id: %v", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// execMark names the processes one execution owns. A process is the execution's when it
// carries its TILLR_RUN_EXEC_ID, or when it carries the run's TILLR_RUN_ID together with
// the TILLR_LOOP_STORE this runner itself was started under. The second form reaches the
// processes of an earlier execution of the same run (a resume) whose runner died without
// cleaning up: they hold the old execution id, so the id alone never finds them. Without
// a loop store the run id alone is not unique, so only the execution id is used.
type execMark struct {
	execID string
	runID  string
	store  string
}

func newExecMark(execID, runID string) execMark {
	return execMark{execID: execID, runID: runID, store: os.Getenv("TILLR_LOOP_STORE")}
}

func (m execMark) empty() bool { return m.execID == "" && !m.byRun() }

func (m execMark) byRun() bool { return m.runID != "" && m.store != "" }

// matches reports whether the NUL-separated environment carries the mark.
func (m execMark) matches(env []byte) bool {
	hasRun, hasStore := false, false
	for _, item := range bytes.Split(env, []byte{0}) {
		s := string(item)
		switch {
		case m.execID != "" && s == runExecIDEnv+"="+m.execID:
			return true
		case m.byRun() && s == "TILLR_RUN_ID="+m.runID:
			hasRun = true
		case m.byRun() && s == "TILLR_LOOP_STORE="+m.store:
			hasStore = true
		}
	}
	return hasRun && hasStore
}

type markedProcess struct {
	pid   int
	pgid  int
	start string
}

func processGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func readProcStat(pid int) (state string, pgid int, start string, err error) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, "", err
	}
	text := string(raw)
	end := strings.LastIndexByte(text, ')')
	if end < 0 {
		return "", 0, "", fmt.Errorf("process %d has invalid stat", pid)
	}
	fields := strings.Fields(text[end+1:])
	if len(fields) < 20 {
		return "", 0, "", fmt.Errorf("process %d has incomplete stat", pid)
	}
	pgid, err = strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, "", fmt.Errorf("process %d has invalid pgid %q: %w", pid, fields[2], err)
	}
	return fields[0], pgid, fields[19], nil
}

// runnerLineage returns this process and its ancestors. They carry the run's markers by
// inheritance (the drain sets them on the runner it starts) and are never the run's
// leftovers, so a sweep by run id must not end them.
func runnerLineage() map[int]bool {
	lineage := map[int]bool{}
	for pid := os.Getpid(); pid > 1 && !lineage[pid]; {
		lineage[pid] = true
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			break
		}
		text := string(raw)
		end := strings.LastIndexByte(text, ')')
		if end < 0 {
			break
		}
		fields := strings.Fields(text[end+1:])
		if len(fields) < 2 {
			break
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			break
		}
		pid = ppid
	}
	return lineage
}

func scanMarkedProcesses(mark execMark) ([]markedProcess, error) {
	if mark.empty() {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, Externalf("reading /proc: %v", err)
	}
	lineage := runnerLineage()
	uid := uint32(os.Geteuid())
	var found []markedProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || lineage[pid] {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		info, err := os.Stat(dir)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, Externalf("inspecting process %d: %v", pid, err)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uid {
			continue
		}
		state, pgid, start, err := readProcStat(pid)
		if processGone(err) {
			continue
		}
		if err != nil {
			return nil, Externalf("reading stat for process %d: %v", pid, err)
		}
		if state == "Z" || state == "X" {
			continue
		}
		env, err := os.ReadFile(filepath.Join(dir, "environ"))
		if processGone(err) || os.IsPermission(err) {
			continue
		}
		if err != nil {
			return nil, Externalf("reading environ for process %d: %v", pid, err)
		}
		if mark.matches(env) {
			found = append(found, markedProcess{pid: pid, pgid: pgid, start: start})
		}
	}
	return found, nil
}

func signalMarkedProcess(p markedProcess, sig syscall.Signal, selfPgid int) error {
	fd, err := unix.PidfdOpen(p.pid, 0)
	if err == unix.ESRCH {
		return nil
	}
	if err != nil && err != unix.ENOSYS {
		return err
	}
	if err == nil {
		defer unix.Close(fd)
	}
	state, pgid, start, err := readProcStat(p.pid)
	if processGone(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if start != p.start || state == "Z" || state == "X" {
		return nil
	}
	if pgid > 1 && pgid != selfPgid {
		if kerr := syscall.Kill(-pgid, sig); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
			return kerr
		}
	}
	if fd >= 0 {
		if serr := unix.PidfdSendSignal(fd, sig, nil, 0); serr != nil && serr != unix.ESRCH {
			return serr
		}
		return nil
	}
	if kerr := syscall.Kill(p.pid, sig); kerr != nil && !errors.Is(kerr, syscall.ESRCH) {
		return kerr
	}
	return nil
}

// signalRunProcesses sends sig to the worker process group and any non-zombie
// processes carrying execID, including those in detached sessions or process groups.
func signalRunProcesses(pid int, mark execMark, sig syscall.Signal) error {
	if pid > 0 {
		if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return Externalf("signaling worker process group %d: %v", pid, err)
		}
	}
	procs, err := scanMarkedProcesses(mark)
	if err != nil {
		return err
	}
	selfPgid := syscall.Getpgrp()
	for _, p := range procs {
		if err := signalMarkedProcess(p, sig, selfPgid); err != nil {
			return Externalf("signaling marked run process %d: %v", p.pid, err)
		}
	}
	return nil
}

// endRunProcesses terminates the worker process group and all non-zombie processes
// carrying execID across detached sessions and process groups, repeating briefly so
// a dying parent racing a fork cannot leak a child.
func endRunProcesses(pid int, mark execMark) error {
	if pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return Externalf("ending worker process group %d: %v", pid, err)
		}
	}
	selfPgid := syscall.Getpgrp()
	for {
		procs, err := scanMarkedProcesses(mark)
		if err != nil {
			return err
		}
		if len(procs) == 0 {
			return nil
		}
		for _, p := range procs {
			if err := signalMarkedProcess(p, syscall.SIGKILL, selfPgid); err != nil {
				return Externalf("ending marked run process %d: %v", p.pid, err)
			}
		}
		time.Sleep(runCleanupInterval)
	}
}
