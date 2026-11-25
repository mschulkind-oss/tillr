package roll

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Sharded lists the modules whose declared suite runs through tools/go-test-shards.py.
var Sharded = []string{
	"tools/gate",
	"tools/tillr-store",
	"tools/tillr-item",
	"tools/tillr-run",
	"tools/tillr-gate",
	"tools/tillr-view",
	"tools/tillr-land",
	"tools/tillr-drain",
}

const (
	// Rolled is the commit stamp file beside the live bin directory.
	Rolled = "tillr-roll.commit"
	// WatchRetire is the pending watch-retirement marker beside the live bin directory.
	WatchRetire = "tillr-watch.retire"
	// SchemaCheck is the marker line printed by tillr-drain loop --check when a schema migration is required.
	SchemaCheck = "tillr-drain: schema migration required"
	// SchemaStage is the directory prefix for staged schema-roll sibling binaries.
	SchemaStage = ".tillr-schema-roll-"
	// ExitIncompatible is tillr/record.ExitIncompatible.
	ExitIncompatible = 3
)

// Modules lists the tillr modules built and installed by tillr-roll.
var Modules = []string{"cli", "drain", "gate", "item", "land", "loop", "run", "store", "view", "watch"}

// TracePrefixes are diagnostic trace line prefixes filtered from failure details.
var TracePrefixes = []string{"tillr-sqlite ", "tillr-sqlite-stack ", "tillr-git "}

// BuildFact represents a drain.build-started event payload and sequence number.
type BuildFact struct {
	Seq      int64  `json:"seq,omitempty"`
	Role     string `json:"role"`
	Revision string `json:"revision"`
	Digest   string `json:"digest"`
	Modified bool   `json:"modified"`
}

// RollFailedError records a failed roll step; its Step name is recorded in the store event.
type RollFailedError struct {
	Step   string
	Cause  error
	Detail string
}

func (e *RollFailedError) Error() string {
	return e.Step + ": " + e.Detail
}

func (e *RollFailedError) Unwrap() error {
	return e.Cause
}

// CmdResult holds captured stdout and stderr from a command invocation.
type CmdResult struct {
	Stdout string
	Stderr string
}

// ExitError is returned when a subprocess exits with a non-zero status.
type ExitError struct {
	Cause    error
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExitError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return fmt.Sprintf("exit status %d", e.ExitCode)
}

func (e *ExitError) Unwrap() error {
	return e.Cause
}

// RunOpts configures a subprocess invocation.
type RunOpts struct {
	Dir           string
	Env           []string
	CaptureOutput bool
	DiscardStdout bool
	DiscardStderr bool
}

// Deps holds optional injectable dependencies for testing.
type Deps struct {
	Run           func(args []string, opts RunOpts) (CmdResult, error)
	Git           func(dir string, args ...string) (string, error)
	Builds        func(store string) (map[string]BuildFact, error)
	RecordFailure func(store, commit, step, detail string)
	TestCommit    func(root, source, work, tip, store string, old map[string]BuildFact, buildEnv []string) error
	CleanCheckout func(root, tip, dest, shared string) error
	RollCommit    func(root, live, tip string, noRequest bool, store string) (int, error)
	Stepped       func(step string, fn func() error) error
	Restart       func(live, root, role string) string
	Remove        func(path string) error
	Stdout        io.Writer
	Stderr        io.Writer
	OnWait        func()
}

func (d *Deps) stdout() io.Writer {
	if d != nil && d.Stdout != nil {
		return d.Stdout
	}
	return os.Stdout
}

func (d *Deps) stderr() io.Writer {
	if d != nil && d.Stderr != nil {
		return d.Stderr
	}
	return os.Stderr
}

func (d *Deps) run(args []string, opts RunOpts) (CmdResult, error) {
	if d != nil && d.Run != nil {
		return d.Run(args, opts)
	}
	return Run(args, opts, d)
}

func (d *Deps) git(dir string, args ...string) (string, error) {
	if d != nil && d.Git != nil {
		return d.Git(dir, args...)
	}
	return Git(dir, d, args...)
}

func (d *Deps) builds(store string) (map[string]BuildFact, error) {
	if d != nil && d.Builds != nil {
		return d.Builds(store)
	}
	return Builds(store)
}

func (d *Deps) recordFailure(store, commit, step, detail string) {
	if d != nil && d.RecordFailure != nil {
		d.RecordFailure(store, commit, step, detail)
		return
	}
	RecordFailure(store, commit, step, detail, d.stderr())
}

func (d *Deps) testCommit(root, source, work, tip, store string, old map[string]BuildFact, buildEnv []string) error {
	if d != nil && d.TestCommit != nil {
		return d.TestCommit(root, source, work, tip, store, old, buildEnv)
	}
	return TestCommit(root, source, work, tip, store, old, buildEnv, d)
}

func (d *Deps) cleanCheckout(root, tip, dest, shared string) error {
	if d != nil && d.CleanCheckout != nil {
		return d.CleanCheckout(root, tip, dest, shared)
	}
	return CleanCheckout(root, tip, dest, shared, d)
}

func (d *Deps) rollCommit(root, live, tip string, noRequest bool, store string) (int, error) {
	if d != nil && d.RollCommit != nil {
		return d.RollCommit(root, live, tip, noRequest, store)
	}
	return RollCommit(root, live, tip, noRequest, store, d)
}

func (d *Deps) stepped(step string, fn func() error) error {
	if d != nil && d.Stepped != nil {
		return d.Stepped(step, fn)
	}
	return Stepped(step, fn)
}

func (d *Deps) restart(live, root, role string) string {
	if d != nil && d.Restart != nil {
		return d.Restart(live, root, role)
	}
	return defaultRestart(live, root, role)
}

func (d *Deps) remove(path string) error {
	if d != nil && d.Remove != nil {
		return d.Remove(path)
	}
	return os.Remove(path)
}

func (d *Deps) onWait() {
	if d != nil && d.OnWait != nil {
		d.OnWait()
	}
}

func flushWriter(w io.Writer) error {
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// Run executes a subprocess, capturing stdout/stderr and streaming to Deps.Stdout/Stderr
// when CaptureOutput is false.
func Run(args []string, opts RunOpts, deps *Deps) (CmdResult, error) {
	if len(args) == 0 {
		return CmdResult{}, errors.New("roll.Run: empty command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Env != nil {
		cmd.Env = opts.Env
	}

	var outBuf, errBuf bytes.Buffer
	if opts.CaptureOutput {
		if !opts.DiscardStdout {
			cmd.Stdout = &outBuf
		}
		if !opts.DiscardStderr {
			cmd.Stderr = &errBuf
		}
		err := cmd.Run()
		res := CmdResult{Stdout: outBuf.String(), Stderr: errBuf.String()}
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				return res, &ExitError{
					Cause:    err,
					ExitCode: exitErr.ExitCode(),
					Stdout:   res.Stdout,
					Stderr:   res.Stderr,
				}
			}
			return res, err
		}
		return res, nil
	}

	if !opts.DiscardStdout {
		cmd.Stdout = &outBuf
	}
	if !opts.DiscardStderr {
		cmd.Stderr = &errBuf
	}
	err := cmd.Run()
	if !opts.DiscardStdout && outBuf.Len() > 0 {
		w := deps.stdout()
		_, _ = io.WriteString(w, outBuf.String())
		_ = flushWriter(w)
	}
	if !opts.DiscardStderr && errBuf.Len() > 0 {
		w := deps.stderr()
		_, _ = io.WriteString(w, errBuf.String())
		_ = flushWriter(w)
	}
	res := CmdResult{Stdout: outBuf.String(), Stderr: errBuf.String()}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return res, &ExitError{
				Cause:    err,
				ExitCode: exitErr.ExitCode(),
				Stdout:   res.Stdout,
				Stderr:   res.Stderr,
			}
		}
		return res, err
	}
	return res, nil
}

// Git runs a git command in dir and returns its trimmed stdout.
func Git(dir string, deps *Deps, args ...string) (string, error) {
	cmdArgs := append([]string{"git"}, args...)
	res, err := deps.run(cmdArgs, RunOpts{Dir: dir, CaptureOutput: true})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}
