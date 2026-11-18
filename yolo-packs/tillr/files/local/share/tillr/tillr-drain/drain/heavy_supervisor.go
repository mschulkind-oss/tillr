package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr/store"
)

type heavyOwnershipKey struct{}
type heavyOwnership struct {
	Lock, Owner *os.File
	Guard       string
}

const heavyCleanupGuard = "heavy.cleanup-pending"

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
	identity, err := heavySupervisorIdentity()
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
	if _, err = fmt.Fprintf(guard, "%scheckout=%q\ncleanup=pending\n", identity, cwd); err != nil {
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
	if args[0] == "declaration" {
		err = runHeavyDeclarationGroup(cmd)
	} else {
		err = runCheckGroup(cmd)
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
func runOwnedHeavy(ctx context.Context, cmd *exec.Cmd, mode string) error {
	// A required check at a candidate's gate is not the runner's: its process group is
	// its own, ended before the call returns (runCheckGroup).
	if unowned, _ := ctx.Value(heavyUnownedKey{}).(bool); unowned {
		return runCheckGroup(cmd)
	}
	ownership, ok := ctx.Value(heavyOwnershipKey{}).(heavyOwnership)
	if !ok {
		return Externalf("heavy judge has no runner ownership")
	}
	self, err := os.Executable()
	if err != nil {
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
