package keep

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ResolveRestartArgs determines policy and lanes from the running loop, runtime config,
// or recorded launch, plus extra.
func ResolveRestartArgs(root string, extra []string, loopPID int) (args []string, policy, lanes string, hasPolicy, hasLanes bool, err error) {
	return resolveRestartArgs(root, extra, loopPID, LoopArgv)
}

func resolveRestartArgs(
	root string,
	extra []string,
	loopPID int,
	loopArgvFn func(int) ([]string, bool),
) (args []string, policy, lanes string, hasPolicy, hasLanes bool, err error) {
	extraPolicy, extraLanes, hasExtraPolicy, hasExtraLanes := ExtractPolicyAndLanes(extra)
	var runningPolicy, runningLanes string
	var hasRunningPolicy, hasRunningLanes bool
	if loopPID > 0 {
		if rest, ok := loopArgvFn(loopPID); ok && len(rest) > 0 && rest[0] == "loop" {
			runningPolicy, runningLanes, hasRunningPolicy, hasRunningLanes = ExtractPolicyAndLanes(rest[1:])
		}
	}
	recPolicy, recLanes, hasRecPolicy, hasRecLanes, err := ReadRecordedLaunch(root)
	if err != nil {
		return nil, "", "", false, false, err
	}
	savedLanes, _, hasSavedLanes, _ := ReadSavedOrdinaryLanes(root)

	switch {
	case hasExtraPolicy:
		policy, hasPolicy = extraPolicy, true
	case hasRunningPolicy:
		policy, hasPolicy = runningPolicy, true
	case hasRecPolicy:
		policy, hasPolicy = recPolicy, true
	}

	switch {
	case hasExtraLanes:
		lanes, hasLanes = extraLanes, true
	case hasSavedLanes:
		lanes, hasLanes = savedLanes, true
	case hasRunningLanes:
		lanes, hasLanes = runningLanes, true
	case hasRecLanes:
		lanes, hasLanes = recLanes, true
	}

	args = make([]string, len(extra))
	copy(args, extra)
	if hasPolicy && !hasExtraPolicy {
		args = append(args, "--policy", policy)
	}
	if hasLanes && !hasExtraLanes && !hasSavedLanes {
		args = append(args, "--lanes", lanes)
	}
	return args, policy, lanes, hasPolicy, hasLanes, nil
}

// WaitOpts configures WaitForExit; zero values use production defaults.
type WaitOpts struct {
	ReportInterval time.Duration
	DisableReport  bool
	PollInterval   time.Duration
	Clock          func() time.Time
	Sleep          func(time.Duration)
	ProcessFn      func(int) (ProcInfo, bool)
}

// WaitForExit waits until pid exits or becomes a zombie, printing what is being waited on.
func WaitForExit(pid int, opts WaitOpts, out io.Writer) {
	reportInterval := opts.ReportInterval
	if !opts.DisableReport && reportInterval == 0 {
		reportInterval = 30 * time.Second
	}
	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = 50 * time.Millisecond
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	procFn := opts.ProcessFn
	if procFn == nil {
		procFn = Process
	}

	original, ok := procFn(pid)
	if !ok || original.State == "Z" {
		return
	}
	fmt.Fprintf(out, "restart: waiting for loop %d to exit\n", pid)
	lastReport := clock()
	for {
		current, ok := procFn(pid)
		if !ok || current.State == "Z" || current.Comm != original.Comm || current.Start != original.Start {
			return
		}
		now := clock()
		if !opts.DisableReport && now.Sub(lastReport) >= reportInterval {
			fmt.Fprintf(out, "restart: still waiting for loop %d to exit\n", pid)
			lastReport = now
		}
		sleep(pollInterval)
	}
}

// WaitForKeeperExit waits until the keeper lock at path is released.
func WaitForKeeperExit(path string, timeout time.Duration) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		flockErr := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		f.Close()
		if flockErr == nil {
			return nil
		}
		if errors.Is(flockErr, unix.EWOULDBLOCK) || errors.Is(flockErr, unix.EAGAIN) {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		return flockErr
	}
	return fmt.Errorf("keeper lock %s stayed held after %ds", path, int(timeout/time.Second))
}

// StopKeeper signals the running keeper holding path with SIGTERM, leaving the loop running.
func StopKeeper(path string, out io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(out, "keeper: not running")
			return nil
		}
		return err
	}
	defer f.Close()

	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		fmt.Fprintln(out, "keeper: not running")
		return nil
	}
	if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		return err
	}

	var rec lockRecord
	if err := json.NewDecoder(f).Decode(&rec); err != nil {
		return err
	}
	// Open a kernel process handle BEFORE checking birth time: even if the PID
	// is reused between verification and signaling, only this process is signaled.
	pidfd, err := unix.PidfdOpen(rec.PID, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			fmt.Fprintln(out, "keeper: already exited")
			return nil
		}
		return err
	}
	defer unix.Close(pidfd)

	info, ok := Process(rec.PID)
	if !ok || info.Start != rec.Start {
		return errors.New("keeper identity changed; refusing to signal a reused PID")
	}
	if err := unix.PidfdSendSignal(pidfd, unix.SIGTERM, nil, 0); err != nil {
		return err
	}
	fmt.Fprintln(out, "keeper: stop requested; loop left running")
	return nil
}

// RestartOpts holds optional overrides for RestartDrain (primarily for testing).
type RestartOpts struct {
	BuildCmd         []string
	DrainBin         string
	KeeperBin        string
	FindLoopFn       func(string) (int, error)
	LoopArgvFn       func(int) ([]string, bool)
	StopKeeperFn     func(string, io.Writer) error
	WaitKeeperExitFn func(string, time.Duration) error
	WaitOpts         WaitOpts
	RunCmdFn         func(argv []string, dir string, env []string) (exitCode int, stdout, stderr string, err error)
	GitHeadFn        func(root string) (string, error)
	StartKeeperFn    func(argv []string, dir string, logPath string) (pid int, err error)
}

var pathProgramNames = []string{
	"tillr", "tillr-drain", "tillr-gate", "tillr-item", "tillr-land",
	"tillr-run", "tillr-store", "tillr-view", "tillr-watch",
}

func lookPathInRoot(root, pathEnv, name string) string {
	for _, entry := range filepath.SplitList(pathEnv) {
		var dir string
		if entry == "" {
			dir = root
		} else if filepath.IsAbs(entry) {
			dir = entry
		} else {
			dir = filepath.Join(root, entry)
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

// RestartDrain stands the keeper down, stops the loop, builds if in a source checkout,
// and restarts the keeper onto the available programs.
func RestartDrain(root string, extra []string, lockPath string, opts RestartOpts, out io.Writer) error {
	findLoopFn := opts.FindLoopFn
	if findLoopFn == nil {
		findLoopFn = FindLoop
	}
	loopArgvFn := opts.LoopArgvFn
	if loopArgvFn == nil {
		loopArgvFn = LoopArgv
	}
	stopKeeperFn := opts.StopKeeperFn
	if stopKeeperFn == nil {
		stopKeeperFn = StopKeeper
	}
	waitKeeperExitFn := opts.WaitKeeperExitFn
	if waitKeeperExitFn == nil {
		waitKeeperExitFn = WaitForKeeperExit
	}
	drainBin := opts.DrainBin
	if drainBin == "" {
		drainBin = "tillr-drain"
	}
	keeperBin := opts.KeeperBin
	if keeperBin == "" {
		// Resolve before step 4 so that if `tillr-build` replaces our binary in place,
		// step 5 execs the newly installed path rather than "<path> (deleted)".
		if exe, err := os.Executable(); err == nil {
			keeperBin = strings.TrimSuffix(exe, " (deleted)")
		} else {
			keeperBin = drainBin
		}
	}
	runCmdFn := opts.RunCmdFn
	if runCmdFn == nil {
		runCmdFn = defaultRunCmd
	}
	gitHeadFn := opts.GitHeadFn
	if gitHeadFn == nil {
		gitHeadFn = defaultGitHead
	}
	startKeeperFn := opts.StartKeeperFn
	if startKeeperFn == nil {
		startKeeperFn = defaultStartKeeper
	}

	// 1. Read policy and lane count from the running loop (if any) or recorded launch BEFORE stopping.
	loopPID, err := findLoopFn(root)
	if err != nil {
		return err
	}
	restartArgs, policy, lanes, hasPolicy, hasLanes, err := resolveRestartArgs(root, extra, loopPID, loopArgvFn)
	if err != nil {
		return err
	}

	// 2. Stand the keeper down.
	if err := stopKeeperFn(lockPath, out); err != nil {
		return err
	}
	if err := waitKeeperExitFn(lockPath, 30*time.Second); err != nil {
		return err
	}

	// 3. Stop the loop if running, and wait for it to exit.
	if loopPID > 0 {
		fmt.Fprintf(out, "restart: stopping loop %d\n", loopPID)
		code, stdoutStr, stderrStr, err := runCmdFn([]string{drainBin, "stop", "--root", root}, "", nil)
		if err != nil && code == -1 {
			return err
		}
		if code != 0 && code != 2 {
			detail := strings.TrimSpace(stderrStr)
			if detail == "" {
				detail = strings.TrimSpace(stdoutStr)
			}
			return fmt.Errorf("tillr-drain stop failed (%d): %s", code, detail)
		}
		WaitForExit(loopPID, opts.WaitOpts, out)
		fmt.Fprintf(out, "restart: loop %d exited\n", loopPID)
	} else {
		fmt.Fprintln(out, "restart: no loop running")
	}

	// 4. A source checkout builds its own programs; a consumer uses the installed
	// pack launchers on PATH, which build their rendered source when necessary.
	goModInfo, goModErr := os.Stat(filepath.Join(root, "tools", "tillr-drain", "go.mod"))
	if len(opts.BuildCmd) > 0 || (goModErr == nil && !goModInfo.IsDir()) {
		head, err := gitHeadFn(root)
		if err != nil {
			return err
		}
		head = strings.TrimSpace(head)
		shortHead := head
		if len(shortHead) > 12 {
			shortHead = shortHead[:12]
		}
		fmt.Fprintf(out, "restart: building from HEAD %s\n", shortHead)
		buildCmd := opts.BuildCmd
		if len(buildCmd) == 0 {
			buildCmd = []string{"just", "--justfile", filepath.Join(root, ".justfile"), "tillr-build"}
		}
		env := append(os.Environ(), "TILLR_BUILD_UNPROVED="+head)
		code, stdoutStr, stderrStr, err := runCmdFn(buildCmd, root, env)
		if err != nil || code != 0 {
			if err != nil {
				return err
			}
			detail := strings.TrimSpace(stderrStr)
			if detail == "" {
				detail = strings.TrimSpace(stdoutStr)
			}
			return fmt.Errorf("build command failed (%d): %s", code, detail)
		}
	}

	// PATH entries are interpreted in the keeper's working directory, not ours.
	pathEnv := os.Getenv("PATH")
	programs := make([]string, 0, len(pathProgramNames))
	for _, name := range pathProgramNames {
		found := lookPathInRoot(root, pathEnv, name)
		if found == "" {
			found = "not found"
		}
		programs = append(programs, fmt.Sprintf("%s=%s", name, found))
	}
	fmt.Fprintf(out, "restart: PATH programs (launchers or binaries): %s\n", strings.Join(programs, ", "))

	// 5. Start the keeper again in the background with the resolved policy and lane count.
	keeperCmd := []string{keeperBin, "keep", "--root", root}
	if len(restartArgs) > 0 {
		keeperCmd = append(keeperCmd, "--")
		keeperCmd = append(keeperCmd, restartArgs...)
	}
	var logDir string
	if durable := os.Getenv("YOLO_DURABLE_DIR"); durable != "" {
		logDir = resolvePath(durable)
	} else {
		logDir = filepath.Join(root, ".tillr")
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	logPath := filepath.Join(logDir, "keeper.log")
	pid, err := startKeeperFn(keeperCmd, root, logPath)
	if err != nil {
		return err
	}

	var summary []string
	if hasPolicy {
		summary = append(summary, "policy="+policy)
	}
	if hasLanes {
		summary = append(summary, "lanes="+lanes)
	}
	detail := ""
	if len(summary) > 0 {
		detail = " (" + strings.Join(summary, ", ") + ")"
	}
	fmt.Fprintf(out, "restart: started keeper %d%s; log: %s\n", pid, detail, logPath)
	return nil
}

func defaultRunCmd(argv []string, dir string, env []string) (int, string, string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	if env != nil {
		cmd.Env = env
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if err == nil {
		return 0, stdoutBuf.String(), stderrBuf.String(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdoutBuf.String(), stderrBuf.String(), nil
	}
	return -1, stdoutBuf.String(), stderrBuf.String(), err
}

func defaultGitHead(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func defaultStartKeeper(argv []string, dir string, logPath string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer logFile.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	return cmd.Process.Pid, nil
}
