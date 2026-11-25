// Package keep keeps the repository's drain loop alive without changing any item decision.
//
// It starts or adopts `tillr-drain loop` in this checkout, waits for exit, then restarts it.
// Short runs (under five minutes) wait 60s, 120s, 240s, ... up to 30 minutes.
// A run of at least five minutes resets the wait to 60s. --stop leaves the loop alive.
// Linux /proc supplies process names and identities. A process named tillr-drain is the
// loop only when its arguments are the loop face for this checkout: argv is parsed, never
// searched for a substring, so a `tillr-drain why` or `plan` beside the loop is not adopted.
package keep

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// ProcInfo holds the kernel name, birth time (starttime from /proc/<pid>/stat), and state.
type ProcInfo struct {
	Comm  string
	Start string
	State string
}

var (
	drainNameRE = regexp.MustCompile(`^tillr-drain(?:-[0-9a-f]+)?$`)
	drainCommRE = regexp.MustCompile(`^tillr-drain(?:-[0-9a-f]{3})?$`)
)

// Process returns the kernel name, birth time, and state for pid, or false after exit.
func Process(pid int) (ProcInfo, bool) {
	dir := filepath.Join("/proc", strconv.Itoa(pid))
	statRaw, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return ProcInfo{}, false
	}
	commRaw, err := os.ReadFile(filepath.Join(dir, "comm"))
	if err != nil {
		return ProcInfo{}, false
	}
	statStr := string(statRaw)
	idx := strings.LastIndex(statStr, ") ")
	if idx < 0 {
		return ProcInfo{}, false
	}
	fields := strings.Fields(statStr[idx+2:])
	if len(fields) <= 19 {
		return ProcInfo{}, false
	}
	return ProcInfo{
		Comm:  strings.TrimSpace(string(commRaw)),
		Start: fields[19],
		State: fields[0],
	}, true
}

// LoopArgv returns the arguments after `tillr-drain` for pid, or false.
func LoopArgv(pid int) ([]string, bool) {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil || len(raw) == 0 {
		return nil, false
	}
	parts := bytes.Split(raw, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	argv := make([]string, len(parts))
	for i, p := range parts {
		argv[i] = string(p)
	}
	limit := 2
	if len(argv) < limit {
		limit = len(argv)
	}
	for i := 0; i < limit; i++ {
		if drainNameRE.MatchString(filepath.Base(argv[i])) {
			return argv[i+1:], true
		}
	}
	return nil, false
}

// resolvePath mirrors Python's Path.resolve(): absolute path with symlinks resolved on the
// longest existing prefix, preserving any non-existent trailing components.
func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	// Resolve longest existing ancestor.
	var suffix []string
	cur := abs
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		suffix = append([]string{filepath.Base(cur)}, suffix...)
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			parts := append([]string{resolved}, suffix...)
			return filepath.Join(parts...)
		}
	}
	return filepath.Clean(abs)
}

// IsLoopFace reports whether pid's arguments are `tillr-drain loop` for root, read from /proc cmdline.
func IsLoopFace(pid int, root string) bool {
	rest, ok := LoopArgv(pid)
	if !ok || len(rest) == 0 || rest[0] != "loop" {
		return false
	}
	named := ""
	hasNamed := false
	for i := 1; i < len(rest); i++ {
		arg := rest[i]
		if (arg == "--root" || arg == "-root") && i+1 < len(rest) {
			named = rest[i+1]
			hasNamed = true
		} else if strings.HasPrefix(arg, "--root=") {
			named = strings.TrimPrefix(arg, "--root=")
			hasNamed = true
		} else if strings.HasPrefix(arg, "-root=") {
			named = strings.TrimPrefix(arg, "-root=")
			hasNamed = true
		}
	}
	if !hasNamed {
		return true
	}
	target := named
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	return resolvePath(target) == resolvePath(root)
}

// ExtractPolicyAndLanes extracts --policy and --lanes values from loop arguments.
func ExtractPolicyAndLanes(args []string) (policy, lanes string, hasPolicy, hasLanes bool) {
	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case (arg == "--policy" || arg == "-policy") && i+1 < len(args):
			policy, hasPolicy = args[i+1], true
			i += 2
		case strings.HasPrefix(arg, "--policy="):
			policy, hasPolicy = strings.TrimPrefix(arg, "--policy="), true
			i++
		case strings.HasPrefix(arg, "-policy="):
			policy, hasPolicy = strings.TrimPrefix(arg, "-policy="), true
			i++
		case (arg == "--lanes" || arg == "-lanes") && i+1 < len(args):
			lanes, hasLanes = args[i+1], true
			i += 2
		case strings.HasPrefix(arg, "--lanes="):
			lanes, hasLanes = strings.TrimPrefix(arg, "--lanes="), true
			i++
		case strings.HasPrefix(arg, "-lanes="):
			lanes, hasLanes = strings.TrimPrefix(arg, "-lanes="), true
			i++
		default:
			i++
		}
	}
	return policy, lanes, hasPolicy, hasLanes
}

// StripLanes returns loop arguments with any --lanes / -lanes option removed.
func StripLanes(args []string) []string {
	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case (arg == "--lanes" || arg == "-lanes") && i+1 < len(args):
			i += 2
		case strings.HasPrefix(arg, "--lanes=") || strings.HasPrefix(arg, "-lanes="):
			i++
		default:
			cleaned = append(cleaned, arg)
			i++
		}
	}
	return cleaned
}

// ReadSavedOrdinaryLanes reads (lanes, updatedAt) from .tillr/state.db without creating or mutating it.
func ReadSavedOrdinaryLanes(root string) (lanes string, updatedAt time.Time, hasLanes bool, hasUpdatedAt bool) {
	dbPath := filepath.Join(root, ".tillr", "state.db")
	info, err := os.Stat(dbPath)
	if err != nil || info.IsDir() {
		return "", time.Time{}, false, false
	}
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(1000)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return "", time.Time{}, false, false
	}
	defer db.Close()

	var valueRaw string
	var updatedRaw sql.NullString
	err = db.QueryRow(
		"SELECT value_json, updated_at FROM drain_runtime_config WHERE key='ordinary_lanes'",
	).Scan(&valueRaw, &updatedRaw)
	if err != nil {
		return "", time.Time{}, false, false
	}

	var parsed any
	if err := json.Unmarshal([]byte(valueRaw), &parsed); err != nil {
		return "", time.Time{}, false, false
	}
	var lanesInt int
	switch v := parsed.(type) {
	case float64:
		lanesInt = int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return "", time.Time{}, false, false
		}
		lanesInt = n
	default:
		return "", time.Time{}, false, false
	}
	lanes = strconv.Itoa(lanesInt)
	hasLanes = true

	if updatedRaw.Valid && updatedRaw.String != "" {
		if t, ok := parseISO8601(updatedRaw.String); ok {
			updatedAt = t
			hasUpdatedAt = true
		}
	}
	return lanes, updatedAt, hasLanes, hasUpdatedAt
}

func parseISO8601(s string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// LaunchExtraForAttempt returns loop extra args for launch; drops boot --lanes once
// runtime config was seeded in this session.
func LaunchExtraForAttempt(root string, extra []string, sessionStart time.Time, adopted bool, launches int) []string {
	if adopted {
		return StripLanes(extra)
	}
	if launches > 0 {
		_, updatedAt, hasLanes, hasUpdatedAt := ReadSavedOrdinaryLanes(root)
		if hasLanes && hasUpdatedAt && !updatedAt.Before(sessionStart) {
			return StripLanes(extra)
		}
	}
	out := make([]string, len(extra))
	copy(out, extra)
	return out
}

// FindLoop finds this user's own-named drain in this checkout, not another repository.
func FindLoop(root string) (int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	uid := uint32(os.Getuid())
	resolvedRoot := resolvePath(root)
	var found []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		st, err := os.Stat(dir)
		if err != nil {
			continue
		}
		sys, ok := st.Sys().(*syscall.Stat_t)
		if !ok || sys.Uid != uid {
			continue
		}
		info, ok := Process(pid)
		// The pack execs tillr-drain-<source digest>; Linux exposes only the
		// first 15 bytes of that executable name (three digest characters).
		if !ok || !drainCommRE.MatchString(info.Comm) || info.State == "Z" {
			continue
		}
		cwd, err := filepath.EvalSymlinks(filepath.Join(dir, "cwd"))
		if err != nil || cwd != resolvedRoot {
			continue
		}
		if IsLoopFace(pid, root) {
			found = append(found, pid)
		}
	}
	if len(found) > 1 {
		return 0, fmt.Errorf("multiple tillr-drain processes in %s: %v; stop the extra loops first", root, found)
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return 0, nil
}

// RestartDelay computes the wait and next backoff delay in seconds.
func RestartDelay(delay, elapsed int) (int, int) {
	if elapsed >= 300 {
		return 60, 60
	}
	next := delay * 2
	if next > 1800 {
		next = 1800
	}
	return delay, next
}

// LoopChild represents either a child process started by this keeper or an adopted PID.
type LoopChild struct {
	Cmd      *exec.Cmd
	PID      int
	done     chan struct{}
	waitErr  error
	exitCode int
	waitOnce sync.Once
}

// NewOwnedChild wraps a started *exec.Cmd so ObserveLoop can wait on its exit without polling.
func NewOwnedChild(cmd *exec.Cmd) *LoopChild {
	c := &LoopChild{
		Cmd:  cmd,
		PID:  cmd.Process.Pid,
		done: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		c.waitErr = err
		if cmd.ProcessState != nil {
			c.exitCode = cmd.ProcessState.ExitCode()
		}
		close(c.done)
	}()
	return c
}

// NewAdoptedChild wraps an existing PID adopted from /proc.
func NewAdoptedChild(pid int) *LoopChild {
	return &LoopChild{PID: pid}
}

// ExitCode returns the exit code of an owned child after ObserveLoop returns true.
func (c *LoopChild) ExitCode() int {
	return c.exitCode
}

// Keep runs the supervision loop until stop is closed or an error occurs.
func Keep(
	stop <-chan struct{},
	find func() (*LoopChild, error),
	launch func() (*LoopChild, error),
	observe func(*LoopChild) bool,
	wait func(seconds int) bool,
	clock func() float64,
	out io.Writer,
) error {
	if clock == nil {
		start := time.Now()
		clock = func() float64 { return time.Since(start).Seconds() }
	}
	delay := 60
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		child, err := find()
		if err != nil {
			return err
		}
		select {
		case <-stop:
			return nil
		default:
		}
		if child == nil {
			child, err = launch()
			if err != nil {
				return err
			}
		}
		started := clock()
		if !observe(child) {
			return nil
		}
		select {
		case <-stop:
			return nil
		default:
		}
		elapsed := int(clock() - started)
		seconds, nextDelay := RestartDelay(delay, elapsed)
		delay = nextDelay
		fmt.Fprintf(out, "keeper: loop exited; restart in %ds\n", seconds)
		if wait(seconds) {
			return nil
		}
	}
}

// ObserveLoop waits for a child or adopted process to exit without stopping it.
// Returns true if the process exited, false if stop was signaled first.
func ObserveLoop(stop <-chan struct{}, child *LoopChild, out io.Writer) bool {
	return observeLoopWithPoll(stop, child, out, time.Second)
}

func observeLoopWithPoll(stop <-chan struct{}, child *LoopChild, out io.Writer, poll time.Duration) bool {
	owned := child.Cmd != nil
	pid := child.PID
	original, ok := Process(pid)
	fmt.Fprintf(out, "keeper: watching loop %d\n", pid)
	if !owned && !ok {
		return true
	}
	if owned {
		select {
		case <-child.done:
			return true
		case <-stop:
			return false
		}
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		current, ok := Process(pid)
		if !ok || current.State == "Z" || current.Comm != original.Comm || current.Start != original.Start {
			return true
		}
		select {
		case <-stop:
			return false
		case <-ticker.C:
		}
	}
}

// LoopCommand composes the standard `tillr-drain loop` argv for root plus extra options.
func LoopCommand(root string, extra []string) []string {
	cmd := []string{
		"tillr-drain", "loop", "--root", root,
		"--gate", "builtin:tillr-loop gate",
		"--land", "builtin:tillr-loop land",
		"--verdict", "builtin:tillr-loop verdict",
	}
	return append(cmd, extra...)
}

type recordedLaunch struct {
	Policy *string `json:"policy,omitempty"`
	Lanes  *string `json:"lanes,omitempty"`
}

// ReadRecordedLaunch reads the last recorded policy and lane count from .tillr/keeper.launch.json.
func ReadRecordedLaunch(root string) (policy, lanes string, hasPolicy, hasLanes bool, err error) {
	path := filepath.Join(root, ".tillr", "keeper.launch.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", false, false, nil
		}
		return "", "", false, false, fmt.Errorf("unreadable recorded launch %s: %w", path, err)
	}
	var data recordedLaunch
	if err := json.Unmarshal(raw, &data); err != nil {
		return "", "", false, false, fmt.Errorf("unreadable recorded launch %s: %w", path, err)
	}
	if data.Policy != nil {
		policy, hasPolicy = *data.Policy, true
	}
	if data.Lanes != nil {
		lanes, hasLanes = *data.Lanes, true
	}
	return policy, lanes, hasPolicy, hasLanes, nil
}

// WriteRecordedLaunch records policy and lane count so a later restart can reuse them.
func WriteRecordedLaunch(root string, policy, lanes string, hasPolicy, hasLanes bool) error {
	path := filepath.Join(root, ".tillr", "keeper.launch.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload := make(map[string]string)
	if hasPolicy {
		payload["policy"] = policy
	}
	if hasLanes {
		payload["lanes"] = lanes
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

type lockRecord struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}

// Run executes the keeper CLI face, returning its process exit code (0 or 2).
func Run(args []string, stdout, stderr io.Writer) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "keeper: %v\n", err)
		return 2
	}

	fs := flag.NewFlagSet("tillr-drain keep", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	rootFlag := fs.String("root", cwd, "repository checkout (default: current directory)")
	stopFlag := fs.Bool("stop", false, "stop only the keeper for this checkout")
	restartFlag := fs.Bool("restart", false, "stand keeper down, stop loop, wait for exit, and restart onto available programs")

	printUsage := func(w io.Writer) {
		fmt.Fprintln(w, "usage: tillr-drain keep [--root PATH] [--stop] [--restart] [-- LOOP_ARGS...]")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Keep the repository's drain alive, without changing any item decision.")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "options:")
		fmt.Fprintln(w, "  --root PATH   repository checkout (default: current directory)")
		fmt.Fprintln(w, "  --stop        stop only the keeper for this checkout")
		fmt.Fprintln(w, "  --restart     stand keeper down, stop loop, wait for exit, and restart onto available programs")
		fmt.Fprintln(w, "  -- LOOP_ARGS  additional loop arguments after --")
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return 0
		}
		fmt.Fprintf(stderr, "keeper: %v\n", err)
		return 2
	}

	extra := fs.Args()
	if len(extra) > 0 && extra[0] == "--" {
		extra = extra[1:]
	}

	// Discovery is scoped by cwd, so moving the child's root elsewhere would break
	// adoption. The keeper, not a passthrough option, owns that one value.
	for _, value := range extra {
		if value == "--root" || value == "-root" ||
			strings.HasPrefix(value, "--root=") || strings.HasPrefix(value, "-root=") {
			fmt.Fprintln(stderr, "keeper: name the checkout with the keeper's --root, not a loop override")
			return 2
		}
	}
	if *stopFlag && len(extra) > 0 {
		fmt.Fprintln(stderr, "keeper: --stop does not take loop arguments")
		return 2
	}
	if *stopFlag && *restartFlag {
		fmt.Fprintln(stderr, "keeper: --stop and --restart cannot be combined")
		return 2
	}
	if info, err := os.Stat("/proc/self/comm"); err != nil || info.IsDir() {
		fmt.Fprintln(stderr, "keeper: the keeper needs Linux /proc and process handles")
		return 2
	}

	root := resolvePath(*rootFlag)
	lockPath := filepath.Join(root, ".tillr", "keeper.lock")

	if *stopFlag {
		if err := StopKeeper(lockPath, stdout); err != nil {
			fmt.Fprintf(stderr, "keeper: %v\n", err)
			return 2
		}
		return 0
	}
	if *restartFlag {
		if err := RestartDrain(root, extra, lockPath, RestartOpts{}, stdout); err != nil {
			fmt.Fprintf(stderr, "keeper: %v\n", err)
			return 2
		}
		return 0
	}

	if err := runForeground(root, extra, lockPath, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "keeper: %v\n", err)
		return 2
	}
	return 0
}

func runForeground(root string, extra []string, lockPath string, stdout, stderr io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return errors.New("keeper already running; use --stop to stop only it")
		}
		return err
	}

	stopCh := make(chan struct{})
	var stopOnce sync.Once
	triggerStop := func() { stopOnce.Do(func() { close(stopCh) }) }

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			triggerStop()
		case <-stopCh:
		}
	}()

	selfInfo, ok := Process(os.Getpid())
	if !ok {
		return errors.New("could not read own process identity from /proc")
	}
	recBytes, err := json.Marshal(lockRecord{PID: os.Getpid(), Start: selfInfo.Start})
	if err != nil {
		return err
	}
	if err := lockFile.Truncate(0); err != nil {
		return err
	}
	if _, err := lockFile.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := lockFile.Write(recBytes); err != nil {
		return err
	}

	extraPolicy, extraLanes, hasExtraPolicy, hasExtraLanes := ExtractPolicyAndLanes(extra)
	if hasExtraPolicy || hasExtraLanes {
		if err := WriteRecordedLaunch(root, extraPolicy, extraLanes, hasExtraPolicy, hasExtraLanes); err != nil {
			return err
		}
	}

	sessionStart := time.Now().UTC()
	adopted := false
	launches := 0

	launch := func() (*LoopChild, error) {
		// A separate session keeps terminal Ctrl-C and --stop away from the
		// loop. Never terminate, authorize a retry, or write to its store here.
		attemptExtra := LaunchExtraForAttempt(root, extra, sessionStart, adopted, launches)
		launches++
		argv := LoopCommand(root, attemptExtra)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = root
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		fmt.Fprintf(stdout, "keeper: started loop %d\n", cmd.Process.Pid)
		return NewOwnedChild(cmd), nil
	}

	findAndRecord := func() (*LoopChild, error) {
		pid, err := FindLoop(root)
		if err != nil {
			return nil, err
		}
		if pid == 0 {
			return nil, nil
		}
		adopted = true
		if rest, ok := LoopArgv(pid); ok && len(rest) > 0 && rest[0] == "loop" {
			p, l, hasP, hasL := ExtractPolicyAndLanes(rest[1:])
			savedLanes, _, hasSaved, _ := ReadSavedOrdinaryLanes(root)
			effectiveLanes, hasEffLanes := l, hasL
			if hasSaved {
				effectiveLanes, hasEffLanes = savedLanes, true
			}
			if hasP || hasEffLanes {
				if err := WriteRecordedLaunch(root, p, effectiveLanes, hasP, hasEffLanes); err != nil {
					return nil, err
				}
			}
		}
		return NewAdoptedChild(pid), nil
	}

	waitFn := func(seconds int) bool {
		timer := time.NewTimer(time.Duration(seconds) * time.Second)
		defer timer.Stop()
		select {
		case <-stopCh:
			return true
		case <-timer.C:
			return false
		}
	}

	err = Keep(
		stopCh,
		findAndRecord,
		launch,
		func(child *LoopChild) bool { return ObserveLoop(stopCh, child, stdout) },
		waitFn,
		nil,
		stdout,
	)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "keeper: stopped; loop left running")
	return nil
}
