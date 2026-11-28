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
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// ResolveRestartArgs determines policy and lanes from the running loop, runtime config,
// or recorded launch, plus extra.
func ResolveRestartArgs(root string, extra []string, loopPID int) (args []string, policy, lanes string, hasPolicy, hasLanes bool, err error) {
	var pids []int
	if loopPID > 0 {
		pids = []int{loopPID}
	}
	return resolveRestartArgs(root, extra, pids, LoopArgv)
}

func resolveRestartArgs(
	root string,
	extra []string,
	loopPIDs []int,
	loopArgvFn func(int) ([]string, bool),
) (args []string, policy, lanes string, hasPolicy, hasLanes bool, err error) {
	extraPolicy, extraLanes, hasExtraPolicy, hasExtraLanes := ExtractPolicyAndLanes(extra)
	var runningPolicy, runningLanes string
	var hasRunningPolicy, hasRunningLanes bool
	for _, loopPID := range loopPIDs {
		if loopPID <= 0 {
			continue
		}
		if rest, ok := loopArgvFn(loopPID); ok && len(rest) > 0 && rest[0] == "loop" {
			p, l, hasP, hasL := ExtractPolicyAndLanes(rest[1:])
			if hasP && !hasRunningPolicy {
				runningPolicy, hasRunningPolicy = p, true
			}
			if hasL && !hasRunningLanes {
				runningLanes, hasRunningLanes = l, true
			}
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
	Timeout        time.Duration
	Clock          func() time.Time
	Sleep          func(time.Duration)
	ProcessFn      func(int) (ProcInfo, bool)
}

// WaitForExit waits until pid exits or becomes a zombie, printing what is being waited on.
func WaitForExit(pid int, opts WaitOpts, out io.Writer) {
	waitForExit(pid, "loop", opts, out)
}

func waitForExit(pid int, role string, opts WaitOpts, out io.Writer) bool {
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
		return true
	}
	fmt.Fprintf(out, "restart: waiting for %s %d to exit\n", role, pid)
	started := clock()
	lastReport := started
	for {
		current, ok := procFn(pid)
		if !ok || current.State == "Z" || current.Comm != original.Comm || current.Start != original.Start {
			return true
		}
		now := clock()
		if opts.Timeout > 0 && now.Sub(started) >= opts.Timeout {
			return false
		}
		if !opts.DisableReport && now.Sub(lastReport) >= reportInterval {
			fmt.Fprintf(out, "restart: still waiting for %s %d to exit\n", role, pid)
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
	FindLoopsFn      func(string) ([]int, error)
	FindKeepersFn    func(string) ([]int, error)
	LoopArgvFn       func(int) ([]string, bool)
	StopKeeperFn     func(string, io.Writer) error
	WaitKeeperExitFn func(string, time.Duration) error
	SignalProcFn     func(pid int, start string) error
	WaitOpts         WaitOpts
	RunCmdFn         func(argv []string, dir string, env []string) (exitCode int, stdout, stderr string, err error)
	GitHeadFn        func(root string) (string, error)
	StartKeeperFn    func(argv []string, dir string, logPath string) (pid int, err error)
}

type builtProgram struct {
	name string
	path string
}

func parseBuiltPrograms(stdout string) []builtProgram {
	var built []builtProgram
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "tillr-build: ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "tillr-build: "))
		if filepath.IsAbs(rest) {
			built = append(built, builtProgram{
				name: filepath.Base(rest),
				path: rest,
			})
		}
	}
	return built
}

func checkPathMatchesBuilt(root, pathEnv string, built []builtProgram) error {
	if len(built) == 0 {
		return nil
	}
	type mismatch struct {
		name     string
		resolved string
		built    string
	}
	var mismatches []mismatch
	var binDir string
	for _, bp := range built {
		if binDir == "" {
			binDir = filepath.Dir(bp.path)
		}
		resolved := lookPathInRoot(root, pathEnv, bp.name)
		if resolved == "" {
			mismatches = append(mismatches, mismatch{
				name:     bp.name,
				resolved: "not found",
				built:    bp.path,
			})
			continue
		}
		if resolvePath(resolved) != resolvePath(bp.path) {
			mismatches = append(mismatches, mismatch{
				name:     bp.name,
				resolved: resolved,
				built:    bp.path,
			})
		}
	}
	if len(mismatches) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("restart: PATH does not resolve the programs just built (refusing before stopping anything):\n")
	for _, m := range mismatches {
		fmt.Fprintf(&b, "  %s: PATH resolved %s, built %s\n", m.name, m.resolved, m.built)
	}
	if binDir != "" {
		fmt.Fprintf(&b, "prepend %s to PATH and retry: PATH=%s:$PATH", binDir, binDir)
	}
	return errors.New(strings.TrimSpace(b.String()))
}

func signalProcessSIGTERM(pid int, start string) error {
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		return err
	}
	defer unix.Close(pidfd)

	info, ok := Process(pid)
	if !ok || info.State == "Z" {
		return nil
	}
	if start != "" && info.Start != start {
		return nil
	}
	if err := unix.PidfdSendSignal(pidfd, unix.SIGTERM, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return err
	}
	return nil
}

func uniqueSortedPIDs(sets ...[]int) []int {
	seen := make(map[int]struct{})
	for _, s := range sets {
		for _, pid := range s {
			if pid > 0 {
				seen[pid] = struct{}{}
			}
		}
	}
	out := make([]int, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
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

// RestartDrain builds if in a source checkout (refusing before stopping anything if PATH
// does not resolve the binaries just built), stands down every keeper and stops every loop
// running in the checkout, and restarts the keeper onto the available programs.
func RestartDrain(root string, extra []string, lockPath string, opts RestartOpts, out io.Writer) error {
	var findLoopsFn func(string) ([]int, error)
	canRescanLoops := true
	switch {
	case opts.FindLoopsFn != nil:
		findLoopsFn = opts.FindLoopsFn
	case opts.FindLoopFn != nil:
		canRescanLoops = false
		findLoopsFn = func(r string) ([]int, error) {
			pid, err := opts.FindLoopFn(r)
			if err != nil {
				return nil, err
			}
			if pid <= 0 {
				return nil, nil
			}
			return []int{pid}, nil
		}
	default:
		findLoopsFn = FindLoops
	}

	findKeepersFn := opts.FindKeepersFn
	if findKeepersFn == nil {
		if opts.StopKeeperFn != nil {
			findKeepersFn = func(string) ([]int, error) { return nil, nil }
		} else {
			findKeepersFn = FindKeepers
		}
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
	procFn := opts.WaitOpts.ProcessFn
	if procFn == nil {
		procFn = Process
	}
	signalProcFn := opts.SignalProcFn
	if signalProcFn == nil {
		if opts.WaitOpts.ProcessFn != nil {
			signalProcFn = func(int, string) error { return nil }
		} else {
			signalProcFn = signalProcessSIGTERM
		}
	}
	drainBin := opts.DrainBin
	if drainBin == "" {
		drainBin = "tillr-drain"
	}
	keeperBin := opts.KeeperBin
	if keeperBin == "" {
		// Resolve before building so that if `tillr-build` replaces our binary in place,
		// the relaunch execs the newly installed path rather than "<path> (deleted)".
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

	// 1. Read policy and lane count from any running loop or recorded launch BEFORE stopping.
	loopPIDs, err := findLoopsFn(root)
	if err != nil {
		return err
	}
	restartArgs, policy, lanes, hasPolicy, hasLanes, err := resolveRestartArgs(root, extra, loopPIDs, loopArgvFn)
	if err != nil {
		return err
	}

	// 2. A source checkout builds its own programs BEFORE stopping anything, and refuses
	// before stopping anything if PATH does not resolve every binary just built.
	// A consumer uses the installed pack launchers on PATH without building.
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
		built := parseBuiltPrograms(stdoutStr)
		if err := checkPathMatchesBuilt(root, os.Getenv("PATH"), built); err != nil {
			return err
		}
		for _, bp := range built {
			if bp.name == "tillr-drain" {
				if opts.KeeperBin == "" {
					keeperBin = bp.path
				}
				if opts.DrainBin == "" {
					drainBin = bp.path
				}
				break
			}
		}
	}

	// 3. Stand down every keeper running in this checkout.
	if err := stopKeeperFn(lockPath, out); err != nil {
		return err
	}
	if err := waitKeeperExitFn(lockPath, 30*time.Second); err != nil {
		return err
	}
	keeperWaitOpts := opts.WaitOpts
	if keeperWaitOpts.Timeout == 0 {
		keeperWaitOpts.Timeout = 30 * time.Second
	}
	var stuckKeepers []int
	extraKeepers, err := findKeepersFn(root)
	if err != nil {
		return err
	}
	for _, kpid := range extraKeepers {
		info, ok := procFn(kpid)
		if !ok || info.State == "Z" {
			continue
		}
		fmt.Fprintf(out, "restart: stopping keeper %d\n", kpid)
		if err := signalProcFn(kpid, info.Start); err != nil {
			return err
		}
		if !waitForExit(kpid, "keeper", keeperWaitOpts, out) {
			stuckKeepers = append(stuckKeepers, kpid)
		} else {
			fmt.Fprintf(out, "restart: keeper %d exited\n", kpid)
		}
	}

	// 4. Stop every loop running in this checkout, and wait for all of them to exit.
	if canRescanLoops {
		if freshLoops, err := findLoopsFn(root); err == nil {
			loopPIDs = uniqueSortedPIDs(loopPIDs, freshLoops)
		}
	}
	var stuckLoops []int
	if len(loopPIDs) > 0 {
		type loopTarget struct {
			pid   int
			start string
		}
		targets := make([]loopTarget, 0, len(loopPIDs))
		for _, pid := range loopPIDs {
			var start string
			if opts.WaitOpts.ProcessFn == nil || opts.SignalProcFn != nil {
				info, ok := procFn(pid)
				if opts.WaitOpts.ProcessFn == nil && (!ok || info.State == "Z") {
					continue
				}
				start = info.Start
			}
			targets = append(targets, loopTarget{pid: pid, start: start})
			fmt.Fprintf(out, "restart: stopping loop %d\n", pid)
		}
		if len(targets) > 0 {
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
			for _, t := range targets {
				if err := signalProcFn(t.pid, t.start); err != nil {
					return err
				}
			}
			for _, t := range targets {
				if waitForExit(t.pid, "loop", opts.WaitOpts, out) {
					fmt.Fprintf(out, "restart: loop %d exited\n", t.pid)
				} else {
					stuckLoops = append(stuckLoops, t.pid)
				}
			}
		} else {
			fmt.Fprintln(out, "restart: no loop running")
		}
	} else {
		fmt.Fprintln(out, "restart: no loop running")
	}

	// Verify no loop or keeper remains running before starting a new keeper.
	if canRescanLoops {
		if remainingLoops, err := findLoopsFn(root); err == nil && len(remainingLoops) > 0 {
			stuckLoops = uniqueSortedPIDs(stuckLoops, remainingLoops)
		}
	}
	if remainingKeepers, err := findKeepersFn(root); err == nil && len(remainingKeepers) > 0 {
		stuckKeepers = uniqueSortedPIDs(stuckKeepers, remainingKeepers)
	}
	if len(stuckLoops) > 0 || len(stuckKeepers) > 0 {
		var parts []string
		if len(stuckLoops) > 0 {
			parts = append(parts, fmt.Sprintf("loops %v", stuckLoops))
		}
		if len(stuckKeepers) > 0 {
			parts = append(parts, fmt.Sprintf("keepers %v", stuckKeepers))
		}
		return fmt.Errorf("restart: refusing to start new keeper while %s still running in %s", strings.Join(parts, " and "), root)
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

	// 5. Record the operator restart request before starting the keeper so the loop's
	// drain.build-started records who asked and why, then start the keeper in the background.
	if err := recordOperatorStartRequest(root, restartArgs, "operator restart (tillr-drain keep --restart)"); err != nil {
		return err
	}
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
