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
	runCleanupTimeout  = 2 * time.Second
	runCleanupInterval = 10 * time.Millisecond
)

func newExecID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", Internalf("generating execution id: %v", err)
	}
	return hex.EncodeToString(raw[:]), nil
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

func scanMarkedProcesses(execID string) ([]markedProcess, error) {
	if execID == "" {
		return nil, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, Externalf("reading /proc: %v", err)
	}
	selfPID := os.Getpid()
	uid := uint32(os.Geteuid())
	marker := []byte(runExecIDEnv + "=" + execID)
	var found []markedProcess
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == selfPID {
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
		matched := false
		for _, item := range bytes.Split(env, []byte{0}) {
			if bytes.Equal(item, marker) {
				matched = true
				break
			}
		}
		if matched {
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
func signalRunProcesses(pid int, execID string, sig syscall.Signal) error {
	if pid > 0 {
		if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return Externalf("signaling worker process group %d: %v", pid, err)
		}
	}
	procs, err := scanMarkedProcesses(execID)
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
func endRunProcesses(pid int, execID string) error {
	if pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return Externalf("ending worker process group %d: %v", pid, err)
		}
	}
	selfPgid := syscall.Getpgrp()
	deadline := time.Now().Add(runCleanupTimeout)
	for {
		procs, err := scanMarkedProcesses(execID)
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
		if !time.Now().Before(deadline) {
			procs, err = scanMarkedProcesses(execID)
			if err != nil {
				return err
			}
			if len(procs) == 0 {
				return nil
			}
			pids := make([]string, len(procs))
			for i, p := range procs {
				pids[i] = strconv.Itoa(p.pid)
			}
			return Externalf("run processes survived cleanup: %s", strings.Join(pids, ", "))
		}
		time.Sleep(runCleanupInterval)
	}
}
