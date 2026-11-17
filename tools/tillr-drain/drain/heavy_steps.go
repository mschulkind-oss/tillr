package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// HeavyStep supplies the judge used by the one asynchronous trunk runner.
type HeavyStep interface {
	JudgeHeavy(context.Context, string, string) ([]HeavyCheck, error)
}

// JudgeHeavy judges declaration-file heavy checks plus the legacy rolling checks
// that have not yet migrated to declarations. Attribution runs exactly one named check.
func (s *ProcessSteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	checksDir := filepath.Join(dir, "tools", "gate", "checks")
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		return nil, Externalf("heavy declarations unavailable: %v", err)
	}
	declared := map[string]struct{ Reads, Needs, Tools []string }{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(checksDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var check struct {
			ID, Scope, Tier     string
			Reads, Needs, Tools []string
		}
		if err := json.Unmarshal(raw, &check); err != nil {
			return nil, err
		}
		if check.Scope == "tree" && check.Tier == "heavy" {
			declared[check.ID] = struct{ Reads, Needs, Tools []string }{check.Reads, check.Needs, check.Tools}
		}
	}
	result := []HeavyCheck{}
	if _, exists := declared[only]; only == "" || exists {
		rev, err := heavyGitOutput(ctx, dir, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		bin := s.HeavyGateBin
		if bin == "" {
			bin = "tillr-gate"
		}
		logs := filepath.Join(s.LaneRoot, ".rolling", "logs", rev)
		args := []string{"checks", "--heavy-tree", "--base", rev, "--head", rev, "--worktree", dir, "--logs", logs}
		if only != "" {
			args = append(args, "--only", only)
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), s.forkRootEnv()...)
		var stderr stderrTail
		cmd.Stderr = &stderr
		var stdout strings.Builder
		cmd.Stdout = &stdout
		runErr := runOwnedHeavy(ctx, cmd, "declaration")
		if cleanupLost(runErr) {
			return nil, runErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		env, err := decodeHeavyVerdict([]byte(stdout.String()))
		if err != nil {
			return nil, Externalf("heavy declaration judge gave no verdict: %v (%v): %s", err, runErr, stderr.String())
		}
		for i := range env {
			meta := declared[env[i].CheckID]
			env[i].Reads, env[i].Needs, env[i].Tools = meta.Reads, meta.Needs, meta.Tools
		}
		result = append(result, env...)
		if only != "" {
			return result, nil
		}
	}
	program := s.RollingProgram
	selector := ""
	if only != "" {
		if s.ReplayProgram == "" {
			return nil, Externalf("cannot attribute legacy check %s: no replay program", only)
		}
		value, ok := s.heavyReplaySelectors.Load(only)
		if !ok {
			return nil, Externalf("legacy check %s has no kind/label replay metadata", only)
		}
		selector = value.(string)
		program = s.ReplayProgram
	} else {
		program = strings.ReplaceAll(program, "{since}", "--all")
	}
	if program == "" {
		return result, nil
	}
	// Substitute after tokenization: a legacy label may contain spaces, but
	// its kind:label selector is one argv value, never multiple arguments.
	argv := strings.Fields(program)
	for i := range argv {
		argv[i] = strings.ReplaceAll(argv[i], "{only}", selector)
	}
	spans := filepath.Join(s.LaneRoot, ".rolling", "heavy.spans.json")
	if err := os.Remove(spans); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), s.forkRootEnv()...), "TILLR_GATE_SPANS_OUT="+spans)
	var stderr stderrTail
	cmd.Stderr = &stderr
	runErr := runOwnedHeavy(ctx, cmd, "legacy")
	if cleanupLost(runErr) {
		return nil, runErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if cmd.ProcessState == nil {
		return nil, Externalf("legacy heavy program did not start: %v: %s", runErr, stderr.String())
	}
	code := cmd.ProcessState.ExitCode()
	if only != "" {
		verdict := "error"
		switch code {
		case 0:
			verdict = "pass"
		case 1:
			verdict = "fail"
		}
		return []HeavyCheck{{CheckID: only, Result: verdict, Detail: stderr.String()}}, nil
	}
	raw, err := os.ReadFile(spans)
	if err != nil {
		return nil, Externalf("legacy heavy program gave no check evidence (exit %d): %v: %s", code, err, stderr.String())
	}
	var file gateSpansFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	for _, span := range file.CheckSpans {
		if span.CheckID == "" {
			continue
		}
		verdict := "error"
		switch span.Status {
		case "PASS", "ok", "CACHED":
			verdict = "pass"
		case "skip", "QUARANTINED":
			verdict = "unavailable"
		case "FAIL":
			verdict = "fail"
		case "STALLED":
			verdict = "stalled"
		}
		var attrs struct {
			Writes []string `json:"writes"`
		}
		if err := json.Unmarshal([]byte(span.AttributesJSON), &attrs); err != nil {
			return nil, err
		}
		if span.Kind != "" && span.Label != "" {
			s.heavyReplaySelectors.Store(span.CheckID, span.Kind+":"+span.Label)
		}
		result = append(result, HeavyCheck{CheckID: span.CheckID, Result: verdict, DurationMS: (span.EndNS - span.StartNS) / 1000000, Reads: attrs.Writes})
	}
	for _, skip := range file.SkippedChecks {
		if skip.Owed || skip.CheckID == "" {
			continue
		}
		result = append(result, HeavyCheck{CheckID: skip.CheckID, Result: "unavailable", Detail: skip.Reason})
	}
	if code != 0 && code != 1 {
		result = append(result, HeavyCheck{CheckID: "legacy-runner", Result: "error", Detail: fmt.Sprintf("exit %d: %s", code, stderr.String())})
	}
	return result, nil
}

// Declaration checks have their own process groups for CPU observation. Give the
// gate a termination signal so it cancels and reaps those groups before tree removal.
func runHeavyDeclarationGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = checkGroupGrace
	err := cmd.Run()
	if cmd.Process != nil {
		endProcessGroup(cmd.Process.Pid)
	}
	return err
}

func decodeHeavyVerdict(raw []byte) ([]HeavyCheck, error) {
	var env struct {
		Kind    string `json:"kind"`
		Payload struct {
			Checks []HeavyCheck `json:"checks"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env.Kind != "gate.verdict" || env.Payload.Checks == nil {
		return nil, fmt.Errorf("expected gate.verdict with checks")
	}
	return env.Payload.Checks, nil
}
