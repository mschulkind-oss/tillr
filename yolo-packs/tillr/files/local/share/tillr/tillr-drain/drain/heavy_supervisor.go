package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/store"
)

type heavyOwnershipKey struct{}
type heavyOwnership struct {
	Lock, Owner *os.File
	Guard       string
}

const heavyCleanupGuard = "heavy.cleanup-pending"

type heavyCommandNotStarted struct{ detail string }

func (e *heavyCommandNotStarted) Error() string { return e.detail }

type heavyCleanupUnconfirmed struct{ detail string }

func (e *heavyCleanupUnconfirmed) Error() string { return e.detail }
func cleanupLost(err error) bool                 { var lost *heavyCleanupUnconfirmed; return errors.As(err, &lost) }

// The internal subprocess entry is also usable by the module's test executable.
// It is not a public face: only the runner supplies its inherited lock and owner pipe.
func init() {
	if len(os.Args) > 1 && os.Args[1] == "--tillr-heavy-supervisor" {
		defer store.FlushSQLiteTraceAtExit()
		code := superviseHeavy(os.Args[2:])
		store.FlushSQLiteTraceAtExit()
		os.Exit(code)
	}
}
func superviseHeavy(args []string) int {
	if len(args) > 0 && args[0] == "step" {
		return superviseStep(args[1:])
	}
	if len(args) < 3 || (args[0] != "declaration" && args[0] != "legacy") {
		fmt.Fprintln(os.Stderr, "heavy supervisor needs a mode and command")
		return 2
	}
	lock := os.NewFile(3, "heavy ownership lock")
	owner := os.NewFile(4, "heavy owner pipe")
	defer lock.Close()
	defer owner.Close()
	if _, err := lock.Stat(); err != nil {
		fmt.Fprintln(os.Stderr, "heavy supervisor missing inherited lock:", err)
		return 4
	}
	if _, err := owner.Stat(); err != nil {
		fmt.Fprintln(os.Stderr, "heavy supervisor missing owner pipe:", err)
		return 4
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// EOF means the drain owner ended, including SIGKILL. The supervisor survives
	// long enough to terminate and reap its judge, retaining the inherited flock.
	go func() { var b [1]byte; _, _ = owner.Read(b[:]); cancel() }()
	if err := prepareHeavyTree(); err != nil {
		fmt.Fprintln(os.Stderr, "heavy lifetime supervision unavailable:", err)
		return 4
	}
	pid, startTicks, err := heavySupervisorIdentity()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	guard, err := os.OpenFile(args[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "heavy cleanup guard:", err)
		return 4
	}
	marker := heavyCleanupMarker{PID: pid, StartTicks: startTicks, Checkout: cwd}
	if _, err = guard.WriteString(marker.format()); err != nil {
		guard.Close()
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	if err = guard.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 4
	}
	cmd := exec.CommandContext(ctx, args[2], args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{lock}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "TILLR_HEAVY_LOCK_FD=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	if args[0] == "declaration" {
		cmd.Env = append(cmd.Env, "TILLR_HEAVY_LOCK_FD=3")
	}
	// The supervisor adopts every orphan the command leaves; it reaps them as they exit
	// rather than only after the command ends (reapWhileSupervising).
	started := make(chan int, 1)
	stopReaping := reapWhileSupervising(started)
	notify := func(pid int) { started <- pid }
	if args[0] == "declaration" {
		err = runHeavyDeclarationGroupStarted(cmd, notify)
	} else {
		err = runCheckGroupStarted(cmd, notify)
	}
	stopReaping()
	if cmd.Process == nil && err != nil {
		if reportErr := os.WriteFile(args[1]+".start-error", []byte(err.Error()), 0600); reportErr != nil {
			fmt.Fprintln(os.Stderr, "heavy startup diagnostic:", reportErr)
			return 4
		}
	}
	if cleanupErr := finishHeavyTree(); cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "heavy cleanup unconfirmed:", cleanupErr)
		return 4
	}
	if cleanupErr := os.Remove(args[1]); cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "heavy cleanup acknowledgement:", cleanupErr)
		return 4
	}
	if err == nil {
		return 0
	}
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() >= 0 {
		return cmd.ProcessState.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "heavy supervisor:", err)
	return 4
}

func superviseStep(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "step supervisor needs a start-error path and command")
		return 2
	}
	owner := os.NewFile(3, "step owner pipe")
	defer owner.Close()
	if _, err := owner.Stat(); err != nil {
		fmt.Fprintln(os.Stderr, "step supervisor missing owner pipe:", err)
		return 4
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { var b [1]byte; _, _ = owner.Read(b[:]); cancel() }()
	if err := prepareHeavyTree(); err != nil {
		fmt.Fprintln(os.Stderr, "step lifetime supervision unavailable:", err)
		return 4
	}
	cmd := exec.CommandContext(ctx, args[1], args[2:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	started := make(chan int, 1)
	stopReaping := reapWhileSupervising(started)
	err := runCheckGroupStarted(cmd, func(pid int) { started <- pid })
	stopReaping()
	if cmd.Process == nil && err != nil {
		if reportErr := os.WriteFile(args[0], []byte(err.Error()), 0600); reportErr != nil {
			fmt.Fprintln(os.Stderr, "step startup diagnostic:", reportErr)
			return 4
		}
	}
	if cleanupErr := finishHeavyTree(); cleanupErr != nil {
		fmt.Fprintln(os.Stderr, "step cleanup unconfirmed:", cleanupErr)
		return 4
	}
	if err == nil {
		return 0
	}
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() >= 0 {
		return cmd.ProcessState.ExitCode()
	}
	fmt.Fprintln(os.Stderr, "step supervisor:", err)
	return 4
}

func runSupervisedTree(cmd *exec.Cmd) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "tillr-step-supervisor-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	startError := filepath.Join(tmp, "start-error")
	owner, life, err := os.Pipe()
	if err != nil {
		return err
	}
	defer life.Close()
	argv := append([]string{"--tillr-heavy-supervisor", "step", startError}, cmd.Args...)
	cmd.Path = self
	cmd.Args = append([]string{self}, argv...)
	cmd.ExtraFiles = append(cmd.ExtraFiles, owner)
	err = func() error {
		defer owner.Close()
		return runHeavySupervisorGroup(cmd)
	}()
	if raw, readErr := os.ReadFile(startError); readErr == nil {
		return &heavyCommandNotStarted{detail: string(raw)}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	return err
}

func runOwnedHeavy(ctx context.Context, cmd *exec.Cmd, mode string) error {
	// A required check at a candidate's gate is not the runner's: its subreaper
	// supervisor ends the whole tree before the call returns.
	if unowned, _ := ctx.Value(heavyUnownedKey{}).(bool); unowned {
		return runSupervisedTree(cmd)
	}
	ownership, ok := ctx.Value(heavyOwnershipKey{}).(heavyOwnership)
	if !ok {
		return Externalf("heavy judge has no runner ownership")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	startError := ownership.Guard + ".start-error"
	if err := os.Remove(startError); err != nil && !os.IsNotExist(err) {
		return err
	}
	argv := append([]string{"--tillr-heavy-supervisor", mode, ownership.Guard}, cmd.Args...)
	// Keep the original command's streams, environment and checkout, replacing only
	// its execution with the lifetime supervisor. The pipe has no writer in children.
	cmd.Path = self
	cmd.Args = append([]string{self}, argv...)
	cmd.ExtraFiles = append(cmd.ExtraFiles, ownership.Lock, ownership.Owner)
	err = runHeavySupervisorGroup(cmd)
	if _, observeErr := os.Stat(ownership.Guard); observeErr == nil || !os.IsNotExist(observeErr) {
		return &heavyCleanupUnconfirmed{detail: fmt.Sprintf("cleanup not acknowledged; checkout retained; independently recover descendants named by %s before clearing that guard", ownership.Guard)}
	}
	if raw, readErr := os.ReadFile(startError); readErr == nil {
		return &heavyCommandNotStarted{detail: string(raw)}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	return err
}

// Cancellation requests cleanup; it does not impose a deadline on the process
// that owns and acknowledges that cleanup. Only its exit releases the checkout.
func runHeavySupervisorGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 0
	return cmd.Run()
}

type heavyCleanupMarker struct {
	PID        int
	StartTicks string
	Checkout   string
}

func (m heavyCleanupMarker) format() string {
	return fmt.Sprintf("supervisor_pid=%d\nstart_ticks=%s\ncheckout=%q\ncleanup=pending\n", m.PID, m.StartTicks, m.Checkout)
}

func parseHeavyCleanupMarker(raw []byte) (heavyCleanupMarker, error) {
	var m heavyCleanupMarker
	var hasPID, hasStart, hasCheckout, hasPending bool
	text := strings.TrimSuffix(string(raw), "\n")
	if text == "" {
		return m, fmt.Errorf("empty cleanup marker")
	}
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return m, fmt.Errorf("malformed line %q", line)
		}
		switch key {
		case "supervisor_pid":
			if hasPID {
				return m, fmt.Errorf("duplicate supervisor_pid")
			}
			pid, err := strconv.Atoi(val)
			if err != nil || pid <= 0 {
				return m, fmt.Errorf("invalid supervisor_pid %q", val)
			}
			m.PID, hasPID = pid, true
		case "start_ticks":
			if hasStart {
				return m, fmt.Errorf("duplicate start_ticks")
			}
			if _, err := strconv.ParseUint(val, 10, 64); err != nil {
				return m, fmt.Errorf("invalid start_ticks %q", val)
			}
			m.StartTicks, hasStart = val, true
		case "checkout":
			if hasCheckout {
				return m, fmt.Errorf("duplicate checkout")
			}
			checkout, err := strconv.Unquote(val)
			if err != nil || checkout == "" || !filepath.IsAbs(checkout) {
				return m, fmt.Errorf("invalid checkout %q", val)
			}
			m.Checkout, hasCheckout = filepath.Clean(checkout), true
		case "cleanup":
			if hasPending || val != "pending" {
				return m, fmt.Errorf("unexpected cleanup=%q", val)
			}
			hasPending = true
		default:
			return m, fmt.Errorf("unknown field %q", key)
		}
	}
	if !hasPID || !hasStart || !hasCheckout || !hasPending {
		return m, fmt.Errorf("incomplete cleanup marker")
	}
	return m, nil
}

// recoverHeavyCleanupGuards inspects any cleanup guard a killed runner left in dir.
// When a guard names a supervisor incarnation (supervisor_pid and start_ticks) that
// is no longer running, it finishes the checkout and marker cleanup the guard
// describes and continues. When the supervisor is still running or its state cannot
// be confirmed, it leaves the checkout and guard in place and returns why.
func recoverHeavyCleanupGuards(ctx context.Context, root, dir string) (string, error) {
	for _, name := range []string{heavyCleanupGuard, heavyReplayGuard} {
		guard := filepath.Join(dir, name)
		raw, err := os.ReadFile(guard)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Sprintf("cannot read %s: %v", guard, err), nil
		}
		marker, err := parseHeavyCleanupMarker(raw)
		if err != nil {
			return fmt.Sprintf("independent process recovery must inspect and clear %s (%v)", guard, err), nil
		}
		running, err := heavySupervisorRunning(marker.PID, marker.StartTicks)
		if err != nil {
			return fmt.Sprintf("cannot confirm supervisor %d (start_ticks=%s) for %s: %v", marker.PID, marker.StartTicks, guard, err), nil
		}
		if running {
			return fmt.Sprintf("supervisor %d (start_ticks=%s) is still running for %s", marker.PID, marker.StartTicks, guard), nil
		}
		if err := finishHeavyCleanupGuard(ctx, root, dir, guard, marker); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return fmt.Sprintf("finishing %s failed: %v", guard, err), nil
		}
		heavyProgressReport(ctx, "loop: heavy recovered %s after supervisor %d (%s) exited\n", name, marker.PID, marker.StartTicks)
	}
	return "", nil
}

func finishHeavyCleanupGuard(ctx context.Context, root, dir, guard string, marker heavyCleanupMarker) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	gitCommon := filepath.Clean(filepath.Dir(dir))
	if within(absRoot, []string{marker.Checkout}) || within(gitCommon, []string{marker.Checkout}) || within(marker.Checkout, []string{gitCommon}) {
		return fmt.Errorf("checkout %s overlaps repository %s", marker.Checkout, absRoot)
	}
	if realCheckout, err := filepath.EvalSymlinks(marker.Checkout); err == nil {
		if realRoot, err := filepath.EvalSymlinks(absRoot); err == nil && within(realRoot, []string{realCheckout}) {
			return fmt.Errorf("checkout %s overlaps repository %s", marker.Checkout, absRoot)
		}
		if realGitCommon, err := filepath.EvalSymlinks(gitCommon); err == nil {
			if within(realGitCommon, []string{realCheckout}) || within(realCheckout, []string{realGitCommon}) {
				return fmt.Errorf("checkout %s overlaps git directory %s", marker.Checkout, gitCommon)
			}
		}
	}
	if err := withWorktreeRegistryLock(ctx, root, func() error {
		_, _ = heavy.GitOutput(context.Background(), root, "worktree", "remove", "--force", marker.Checkout)
		return os.RemoveAll(marker.Checkout)
	}); err != nil {
		return err
	}
	if err := os.Remove(guard + ".start-error"); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(guard); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
