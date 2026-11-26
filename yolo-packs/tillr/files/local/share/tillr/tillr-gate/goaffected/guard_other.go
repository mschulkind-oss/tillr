//go:build !linux

package goaffected

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

// InvocationGuardFlag is the internal flag used to launch the invocation guard.
const InvocationGuardFlag = "--invocation-guard"

var (
	liveGuardsMu sync.Mutex
	liveGuards   = make(map[int]*os.File)
)

// SetChildSubreaper is a no-op on non-Linux platforms.
func SetChildSubreaper() {}

// InvocationGuardMain runs command with lifeline and signal monitoring on non-Linux platforms.
func InvocationGuardMain(lifelineFD int, command []string) int {
	if len(command) == 0 {
		return 2
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 1
	}
	pgid := cmd.Process.Pid

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() {
		sig := <-sigCh
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
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
				if _, err := lifelineFile.Read(buf); err != nil {
					break
				}
			}
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			os.Exit(1)
		}()
	}

	err := cmd.Wait()
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
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

// RunGuardedCommand runs cmd under the invocation guard, streaming stdout+stderr lines to onLine.
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
	proc.ExtraFiles = []*os.File{lifelineR}
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
