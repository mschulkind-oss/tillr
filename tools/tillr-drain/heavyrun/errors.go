package heavyrun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// ClassifyCheck is the check-result classifier used by heavyrun.
// Tests in drain and heavyrun swap it via withClassifier.
var ClassifyCheck = checkresult.Classify

// CodeError carries an exit code without defining a zero-arg ExitCode() method
// (which classification_scan_test.go reserves for process exit status).
type CodeError struct {
	Code int
	Msg  string
}

func (e *CodeError) Error() string { return e.Msg }

func Usagef(format string, args ...any) error {
	return &CodeError{Code: record.ExitUsage, Msg: fmt.Sprintf(format, args...)}
}

func Externalf(format string, args ...any) error {
	return &CodeError{Code: record.ExitExternal, Msg: fmt.Sprintf(format, args...)}
}

func Internalf(format string, args ...any) error {
	return &CodeError{Code: record.ExitInternal, Msg: fmt.Sprintf(format, args...)}
}

var ErrRestartPending = errors.New("loop replacement pending")

const (
	DefaultTrunkRef = "refs/heads/" + settings.DefaultTrunkBranch
	GoTestPrefix    = "go-test:"
	RevertKind      = "drain.revert"
	CheckGroupGrace = 5 * time.Second
)

// HeavyUnownedKey marks a judge call made outside the heavy runner: its processes run in
// a subreaper supervisor that ends the whole tree before the call returns.
type HeavyUnownedKey struct{}

// HeavyInverseKey marks a counterfactual replay that must not overwrite logs for the unmodified commit.
type HeavyInverseKey struct{}

// HeavyProgressReportKey carries the runner's Progress into the judge, so a judge that
// runs checks one at a time reports each verdict as it ends.
type HeavyProgressReportKey struct{}

func HeavyProgressReport(ctx context.Context, format string, args ...any) {
	if report, ok := ctx.Value(HeavyProgressReportKey{}).(func(string)); ok && report != nil {
		report(fmt.Sprintf(format, args...))
	}
}

type HeavyCheckEndedKey struct{}

// HeavyCheckEnded reports one check of a whole-tier judge the moment it ends, so a red
// one starts its recovery while the judge runs the rest.
func HeavyCheckEnded(ctx context.Context, check HeavyCheck) {
	if report, ok := ctx.Value(HeavyCheckEndedKey{}).(func(HeavyCheck)); ok {
		report(check)
	}
}

func ShortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func JoinDetail(old, add string) string {
	if old = strings.TrimSpace(old); old == "" {
		return add
	}
	if add == "" {
		return old
	}
	return old + "; " + add
}

// HeavyBrief keeps a recorded detail readable in one criteria line: whitespace folded,
// and a long output tail cut to its end, where a failure's reason usually is.
func HeavyBrief(detail string) string {
	detail = strings.Join(strings.Fields(detail), " ")
	const keep = 1500
	if len(detail) > keep {
		detail = "..." + detail[len(detail)-keep:]
	}
	return detail
}

// HeavyReadsChanged matches exact files and directory reads ending in a slash.
func HeavyReadsChanged(reads, paths []string) bool {
	for _, read := range reads {
		for _, path := range paths {
			if path == read || strings.HasSuffix(read, "/") && strings.HasPrefix(path, read) {
				return true
			}
		}
	}
	return false
}

// RevertedBy answers the trunk commit that already reverted landing, or "" when none
// has landed.
func RevertedBy(ctx context.Context, storePath, landing string) (string, error) {
	if storePath == "" || landing == "" {
		return "", nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var revertSHA string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		shas, err := fixer.LandingShas(r, landing)
		if err != nil {
			return err
		}
		in, args := heavy.InList(shas)
		qargs := append([]any{RevertKind}, args...)
		err = r.QueryRow(`SELECT COALESCE(json_extract(payload, '$.revert'), '') FROM events WHERE kind = ? AND object_type = 'landing' AND object_id IN `+in+` AND json_extract(payload, '$.result') = 'reverted' ORDER BY seq DESC LIMIT 1`, qargs...).Scan(&revertSHA)
		if errors.Is(err, sql.ErrNoRows) || err != nil && strings.Contains(err.Error(), "no such table") {
			return nil
		}
		return err
	})
	return revertSHA, err
}

// RunCheckGroupStarted runs cmd in its own process group and tells started the command's pid once started.
func preserveSetpgid(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return
	}
	attr := *cmd.SysProcAttr
	attr.Setpgid = true
	cmd.SysProcAttr = &attr
}

func RunCheckGroupStarted(cmd *exec.Cmd, started func(pid int)) error {
	preserveSetpgid(cmd)
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = CheckGroupGrace
	err := StartAndWait(cmd, started)
	if cmd.Process != nil {
		EndProcessGroup(cmd.Process.Pid)
	}
	return err
}

// RunHeavyDeclarationGroupStarted runs cmd in its own process group (signaling the whole group on cancel)
// and tells started the command's pid once started.
func RunHeavyDeclarationGroupStarted(cmd *exec.Cmd, started func(pid int)) error {
	preserveSetpgid(cmd)
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = CheckGroupGrace
	err := StartAndWait(cmd, started)
	if cmd.Process != nil {
		EndProcessGroup(cmd.Process.Pid)
	}
	return err
}

// StartAndWait is cmd.Run, telling started the pid between the start and the wait.
func StartAndWait(cmd *exec.Cmd, started func(pid int)) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	if started != nil {
		started(cmd.Process.Pid)
	}
	return cmd.Wait()
}

// EndProcessGroup kills every process left in group pgid and waits, up to CheckGroupGrace,
// until none is left.
func EndProcessGroup(pgid int) {
	if pgid <= 0 {
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	deadline := time.Now().Add(CheckGroupGrace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func within(path string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
