package shards

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
)

// GuardFlag is the internal flag used when re-execing tillr-gate shards to
// supervise a shard process group.
const GuardFlag = "--shard-guard"

var (
	liveMu     sync.Mutex
	liveGroups = make(map[int]struct{})
)

// GuardMain runs command in the current process group and kills the entire
// process group with SIGKILL if the lifeline file descriptor closes.
func GuardMain(descriptor int, command []string) int {
	if len(command) == 0 {
		return 2
	}
	lifeline := os.NewFile(uintptr(descriptor), "lifeline")
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := lifeline.Read(buf)
			if err != nil {
				if err == io.EOF {
					break
				}
				break
			}
		}
		_ = syscall.Kill(0, syscall.SIGKILL)
	}()

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				return 128 + int(status.Signal())
			}
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

// KillGroup sends SIGKILL to the process group pgid, ignoring missing/permission errors.
func KillGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}

// KillLiveGroups kills every shard process group started by this runner and not yet reaped.
func KillLiveGroups() {
	liveMu.Lock()
	groups := make([]int, 0, len(liveGroups))
	for g := range liveGroups {
		groups = append(groups, g)
	}
	liveMu.Unlock()
	for _, g := range groups {
		KillGroup(g)
	}
}

func registerLiveGroup(pgid int) {
	liveMu.Lock()
	liveGroups[pgid] = struct{}{}
	liveMu.Unlock()
}

func unregisterLiveGroup(pgid int) {
	liveMu.Lock()
	delete(liveGroups, pgid)
	liveMu.Unlock()
}

// InstallSignalHandlers registers handlers for SIGTERM, SIGHUP, and SIGINT so
// no shard process group outlives a runner told to stop.
func InstallSignalHandlers() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		sig := <-ch
		KillLiveGroups()
		if s, ok := sig.(syscall.Signal); ok {
			os.Exit(128 + int(s))
		}
		os.Exit(1)
	}()
}

// startGuardedCommand starts command under a new session/process group via
// `exePath shards --shard-guard 3 -- <command...>`, returning the Cmd, a cleanup
// function that kills the process group and closes the lifeline pipe, and any error.
func startGuardedCommand(exePath, cwd string, env []string, stdout, stderr *os.File, command []string) (*exec.Cmd, func(), error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}

	args := make([]string, 0, 4+len(command))
	args = append(args, "shards", GuardFlag, strconv.Itoa(3), "--")
	args = append(args, command...)

	cmd := exec.Command(exePath, args...)
	cmd.Dir = cwd
	cmd.Env = env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.ExtraFiles = []*os.File{r}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, nil, err
	}
	_ = r.Close()

	pid := cmd.Process.Pid
	registerLiveGroup(pid)

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			KillGroup(pid)
			_ = w.Close()
			unregisterLiveGroup(pid)
			_ = cmd.Wait()
		})
	}
	return cmd, cleanup, nil
}
