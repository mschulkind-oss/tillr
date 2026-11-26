package gatespans

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/procguard"
	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/settings"
)

// HeavyStep supplies the judge used by the one asynchronous trunk runner.
type HeavyStep interface {
	JudgeHeavy(context.Context, string, string) ([]HeavyCheck, error)
}

// HeavyJudgeConfig supplies step-runner configuration for heavy proof and replay calls.
type HeavyJudgeConfig struct {
	LaneRoot             string
	SelfExe              string
	Priority             *procguard.ProcessPriority
	CheckProcessEnv      func(base []string, extra ...string) []string
	CommandArgv          func(program string) []string
	GateProgram          string
	HeavyLegacyProgram   string
	ReplayProgram        string
	HeavyReplaySelectors *sync.Map
	LogSampleInterval    time.Duration
}

func (cfg HeavyJudgeConfig) env(base []string, extra ...string) []string {
	if cfg.CheckProcessEnv != nil {
		return cfg.CheckProcessEnv(base, extra...)
	}
	return append(base, extra...)
}

func (cfg HeavyJudgeConfig) argv(program string) []string {
	if cfg.CommandArgv != nil {
		return cfg.CommandArgv(program)
	}
	return strings.Fields(program)
}

func (cfg HeavyJudgeConfig) applyPriority(ctx context.Context, cmd *exec.Cmd) error {
	if cfg.Priority == nil {
		return nil
	}
	return cfg.Priority.ApplyHeavyToCmd(ctx, cmd)
}

type HeavyDeclaration struct {
	Reads, Needs, Tools []string
	Remember            *bool
}

func HeavyCheckDeclarations(dir string) (map[string]HeavyDeclaration, error) {
	s, err := settings.Read(dir)
	if err != nil {
		return nil, err
	}
	checksDir := filepath.Join(dir, filepath.FromSlash(s.ChecksDir()))
	entries, err := os.ReadDir(checksDir)
	if err != nil {
		return nil, err
	}
	declared := map[string]HeavyDeclaration{}
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
			Remember            *bool
		}
		if err := json.Unmarshal(raw, &check); err != nil {
			return nil, err
		}
		if check.Scope == "tree" && check.Tier == "heavy" {
			declared[check.ID] = HeavyDeclaration{check.Reads, check.Needs, check.Tools, check.Remember}
		}
	}
	return declared, nil
}

// JudgeHeavy judges declaration-file heavy checks plus the legacy rolling checks
// that have not yet migrated to declarations. Attribution runs exactly one named check.
func JudgeHeavy(ctx context.Context, cfg HeavyJudgeConfig, dir, only string) ([]HeavyCheck, error) {
	if strings.HasPrefix(only, GoTestPrefix) {
		return JudgeHeavyGo(ctx, cfg, dir, only)
	}
	declared, err := HeavyCheckDeclarations(dir)
	if err != nil {
		return nil, drainmodel.Externalf("heavy declarations unavailable: %v", err)
	}
	if only == "" {
		ids := []string{}
		for id := range declared {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		paths, err := HeavyChangedPaths(ctx, dir)
		if err != nil {
			return nil, err
		}
		var first, rest []string
		for _, id := range ids {
			if HeavyReadsChanged(declared[id].Reads, paths) {
				first = append(first, id)
			} else {
				rest = append(rest, id)
			}
		}
		legacyFirst := false
		if cfg.HeavyLegacyProgram != "" && len(paths) > 0 && len(rest) > 0 {
			watches, err := LegacyCheckWatches(ctx, dir)
			if err != nil {
				return nil, err
			}
			for _, reads := range watches {
				if HeavyReadsChanged(reads, paths) {
					legacyFirst = true
					break
				}
			}
		}
		result := []HeavyCheck{}
		runLegacy := func() error {
			legacy, err := RunHeavyLegacy(ctx, cfg, dir, "")
			result = append(result, legacy...)
			if err == nil {
				failed := []string{}
				logs := []string{}
				silent := []string{}
				for _, check := range legacy {
					if drainmodel.ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome == checkresult.Fail {
						failed = append(failed, check.CheckID)
						if check.LogPath == "" {
							silent = append(silent, check.CheckID)
						} else if !slices.Contains(logs, check.LogPath) {
							logs = append(logs, check.LogPath)
						}
					}
				}
				if len(failed) == 0 {
					failed = append(failed, "none")
				}
				line := fmt.Sprintf("loop: heavy legacy program judged %d checks; failing: %s", len(legacy), strings.Join(failed, ", "))
				if len(logs) > 0 {
					line += "; output: " + strings.Join(logs, ", ")
				}
				HeavyProgressReport(ctx, "%s\n", line)
				if len(silent) > 0 {
					HeavyProgressReport(ctx, "loop: heavy DEFECT: failed legacy check %s left no output, so the fixer has nothing to read; the legacy program must write its failure to stdout or stderr\n", strings.Join(silent, ", "))
				}
				for _, check := range legacy {
					HeavyCheckEnded(ctx, check)
				}
			}
			return err
		}
		for position, id := range append(first, rest...) {
			if legacyFirst && position == len(first) {
				if err := runLegacy(); err != nil {
					return result, err
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if declared[id].Remember == nil || *declared[id].Remember {
				if check, ok := ReuseHeavyCheck(ctx, id); ok {
					result = append(result, check)
					continue
				}
			}
			checks, err := JudgeHeavy(ctx, cfg, dir, id)
			if err != nil {
				return nil, err
			}
			if len(checks) != 1 || checks[0].CheckID != id {
				return nil, drainmodel.Externalf("heavy check %s gave no verdict", id)
			}
			if err := SaveHeavyCheck(ctx, checks[0]); err != nil {
				return nil, err
			}
			HeavyProgressReport(ctx, "loop: heavy %s %s in %s\n", id, checks[0].Result, (time.Duration(checks[0].DurationMS) * time.Millisecond).Round(time.Second))
			HeavyStoreWholeStepProgress(ctx, checks[0].CheckID, checks[0].Result)
			HeavyCheckEnded(ctx, checks[0])
			result = append(result, checks...)
		}
		if !legacyFirst {
			err = runLegacy()
		}
		return result, err
	}
	result := []HeavyCheck{}
	if _, exists := declared[only]; only == "" || exists {
		rev, err := HeavyGitOutput(ctx, dir, "rev-parse", "HEAD")
		if err != nil {
			return nil, err
		}
		bin := cfg.GateProgram
		if bin == "" {
			bin = "tillr-gate"
		}
		repoCfg, err := settings.Read(dir)
		if err != nil {
			return nil, drainmodel.Externalf("heavy declarations unavailable: %v", err)
		}
		logs := filepath.Join(cfg.LaneRoot, ".rolling", "logs", rev)
		args := []string{"checks", "--heavy-tree", "--base", rev, "--head", rev, "--worktree", dir, "--checks", repoCfg.ChecksDir(), "--logs", logs}
		if only != "" {
			args = append(args, "--only", only)
		}
		var extraEnv []string
		var checkProgressPath string
		if only != "" && HasHeavyStoreProgress(ctx) {
			rollingDir, err := filepath.Abs(filepath.Join(cfg.LaneRoot, ".rolling"))
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(rollingDir, 0o755); err != nil {
				return nil, err
			}
			checkProgressPath = filepath.Join(rollingDir, "heavy-check-progress-"+NextHeavyLegacyNonce()+".json")
			extraEnv = append(extraEnv, "TILLR_GO_SHARDS_PROGRESS_OUT="+checkProgressPath)
		}
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = cfg.env(os.Environ(), extraEnv...)
		if err := cfg.applyPriority(ctx, cmd); err != nil {
			return nil, err
		}
		var stderr drainmodel.StderrTail
		cmd.Stderr = &stderr
		var stdout strings.Builder
		cmd.Stdout = &stdout
		stopProgressWatch := StartHeavyReplayProgressWatcher(ctx, checkProgressPath, only)
		stopLogWatch := StartHeavyCheckLogProgressWatcher(ctx, logs, only, cfg.LogSampleInterval)
		runErr := RunOwnedHeavy(ctx, cmd, "declaration")
		stopProgressWatch()
		stopLogWatch()
		if checkProgressPath != "" {
			_ = os.Remove(checkProgressPath)
		}
		if CleanupLost(runErr) {
			return nil, runErr
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		env, err := DecodeHeavyVerdict([]byte(stdout.String()))
		if err != nil {
			return nil, drainmodel.Externalf("heavy declaration judge gave no verdict: %v (%v): %s", err, runErr, stderr.String())
		}
		errTail := strings.TrimSpace(stderr.String())
		for i := range env {
			meta := declared[env[i].CheckID]
			env[i].Reads, env[i].Needs, env[i].Tools, env[i].Remember = meta.Reads, meta.Needs, meta.Tools, meta.Remember
			if env[i].Detail == "" && env[i].LogRef == "" && env[i].LogPath == "" && errTail != "" {
				c := drainmodel.ClassifyCheck(checkresult.Observation{Check: env[i].CheckID, Source: checkresult.HeavyCheckWord, Word: env[i].Result})
				if c.Outcome == checkresult.Unavailable || (c.Outcome == checkresult.Unknown && (c.Detail == checkresult.DetailError || c.Detail == checkresult.DetailStalled)) {
					env[i].Detail = errTail
				}
			}
		}
		result = append(result, env...)
		if only != "" {
			return result, nil
		}
	}
	return RunHeavyLegacy(ctx, cfg, dir, only)
}

// HeavyChangedPaths compares the commit under proof with the green pointer.
func HeavyChangedPaths(ctx context.Context, dir string) ([]string, error) {
	green, err := HeavyGreen(ctx, dir)
	if err != nil || green == "" {
		return nil, err
	}
	raw, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--name-only", "--no-renames", "-z", green, "HEAD", "--").Output()
	if err != nil {
		return nil, drainmodel.Externalf("reading the paths changed since %s: %v", green, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), nil
}

func RunHeavyLegacy(ctx context.Context, cfg HeavyJudgeConfig, dir, only string) ([]HeavyCheck, error) {
	result := []HeavyCheck{}
	program := cfg.HeavyLegacyProgram
	selector := ""
	if only != "" {
		if cfg.ReplayProgram == "" {
			return nil, drainmodel.Externalf("cannot attribute legacy check %s: no replay program", only)
		}
		var value any
		var ok bool
		if cfg.HeavyReplaySelectors != nil {
			value, ok = cfg.HeavyReplaySelectors.Load(only)
		}
		if !ok {
			return nil, drainmodel.Externalf("legacy check %s has no kind/label replay metadata", only)
		}
		selector = value.(string)
		program = cfg.ReplayProgram
	} else {
		program = strings.ReplaceAll(program, "{since}", "--all")
	}
	if program == "" {
		return result, nil
	}
	// Substitute after tokenization: a legacy label may contain spaces, but
	// its kind:label selector is one argv value, never multiple arguments.
	argv := cfg.argv(program)
	for i := range argv {
		argv[i] = strings.ReplaceAll(argv[i], "{only}", selector)
	}
	spans := filepath.Join(cfg.LaneRoot, ".rolling", "heavy.spans.json")
	if only != "" {
		// A replay can run beside the proof's own program, whose evidence this must not remove.
		spans = filepath.Join(cfg.LaneRoot, ".rolling", "heavy.replay.spans.json")
	}

	if err := os.MkdirAll(filepath.Dir(spans), 0755); err != nil {
		return nil, err
	}
	if err := os.Remove(spans); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	extraEnv := []string{"TILLR_GATE_SPANS_OUT=" + spans}
	var replayProgressPath string
	if only != "" && HasHeavyStoreProgress(ctx) {
		replayProgressPath = filepath.Join(cfg.LaneRoot, ".rolling", "heavy-replay-progress.json")
		if err := os.Remove(replayProgressPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		extraEnv = append(extraEnv, "TILLR_GO_SHARDS_PROGRESS_OUT="+replayProgressPath)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = cfg.env(os.Environ(), extraEnv...)
	if err := cfg.applyPriority(ctx, cmd); err != nil {
		return nil, err
	}
	var stderr drainmodel.StderrTail
	cmd.Stderr = &stderr
	// The program's whole output goes to a file under the proof's log directory for
	// this commit, so a failure that cannot be reproduced still leaves something to read.
	logPath := LegacyLogPath(ctx, cfg.LaneRoot, dir, only)
	if logPath != "" {
		if logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
			defer logFile.Close()
			cmd.Stdout = logFile
			cmd.Stderr = io.MultiWriter(logFile, &stderr)
		} else {
			logPath = ""
		}
	}
	stopReplayWatch := StartHeavyReplayProgressWatcher(ctx, replayProgressPath, only)
	stopLogWatch := func() {}
	if only != "" && logPath != "" {
		stopLogWatch = StartHeavyLogFileProgressWatcher(ctx, logPath, only, cfg.LogSampleInterval)
	}
	runErr := RunOwnedHeavy(ctx, cmd, "legacy")
	stopReplayWatch()
	stopLogWatch()
	if replayProgressPath != "" {
		_ = os.Remove(replayProgressPath)
	}
	if CleanupLost(runErr) {
		return nil, runErr
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if cmd.ProcessState == nil {
		return nil, drainmodel.Externalf("legacy heavy program did not start: %v: %s", runErr, stderr.String())
	}
	exitNum := cmd.ProcessState.ExitCode()
	exitClassified := drainmodel.ClassifyCheck(checkresult.Observation{Check: only, Source: checkresult.BinaryProgram, ExitCode: &exitNum})
	if only != "" {
		verdict := "error"
		switch exitClassified.Outcome {
		case checkresult.Pass:
			verdict = "pass"
		case checkresult.Fail:
			verdict = "fail"
		}
		kept := LegacyKeptLog(logPath, verdict)
		if exitClassified.Outcome == checkresult.Fail && kept == "" {
			HeavyProgressReport(ctx, "loop: heavy DEFECT: the replay of legacy check %s failed and left no output\n", only)
		}
		return []HeavyCheck{{CheckID: only, Result: verdict, Detail: stderr.String(), LogPath: kept}}, nil
	}
	failures, err := ReadLegacyEvidence(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(spans)
	if err != nil {
		return nil, drainmodel.Externalf("legacy heavy program gave no check evidence (exit %d): %v: %s", exitNum, err, stderr.String())
	}
	var file GateSpansFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	watches, err := LegacyCheckWatches(ctx, dir)
	if err != nil {
		return nil, err
	}
	rev, _ := HeavyGitOutput(ctx, dir, "rev-parse", "HEAD")
	var perCheckLogs map[string]string
	if cfg.LaneRoot != "" && rev != "" {
		perCheckLogs = remotesync.ExtractForkCheckLogs(ctx, dir, rev, spans, logPath, "", filepath.Join(cfg.LaneRoot, ".rolling", "logs", rev))
	}
	for _, span := range file.CheckSpans {
		if span.CheckID == "" {
			continue
		}
		spanClassified := drainmodel.ClassifyCheck(checkresult.Observation{Check: span.CheckID, Source: checkresult.LegacySpan, Word: span.Status})
		verdict := "error"
		switch spanClassified.Outcome {
		case checkresult.Pass:
			verdict = "pass"
		case checkresult.Unavailable:
			verdict = "unavailable"
		case checkresult.Fail:
			verdict = "fail"
		case checkresult.Unknown:
			if spanClassified.Detail == checkresult.DetailStalled {
				verdict = "stalled"
			}
		}
		var attrs struct {
			Reads   []string `json:"reads"`
			Watches []string `json:"watches"`
			Writes  []string `json:"writes"`
		}
		if err := json.Unmarshal([]byte(span.AttributesJSON), &attrs); err != nil {
			return nil, err
		}
		reads := append(append(attrs.Reads, attrs.Watches...), attrs.Writes...)
		if watches != nil {
			declared, ok := watches[span.CheckID]
			if !ok {
				declared, ok = watches[span.Kind+"\x00"+span.Label]
			}
			if !ok {
				return nil, drainmodel.Externalf("legacy check %s has no declared watches", span.CheckID)
			}
			reads = declared
		}
		if span.Kind != "" && span.Label != "" && cfg.HeavyReplaySelectors != nil {
			cfg.HeavyReplaySelectors.Store(span.CheckID, span.Kind+":"+span.Label)
		}
		failure := failures[span.CheckID]
		kept, logRef := "", ""
		if spanClassified.Outcome == checkresult.Fail || spanClassified.Outcome == checkresult.Unknown {
			kept, logRef = failure.Log, failure.Log
			if logRef == "" {
				// Custom legacy programs need not use the observation adapter.
				logRef = perCheckLogs[span.CheckID]
				kept = LegacyKeptLog(logPath, verdict)
				if logRef == "" {
					logRef = kept
				}
				if kept == "" {
					kept = logRef
				}
			}
		}
		check := HeavyCheck{CheckID: span.CheckID, Result: verdict, DurationMS: (span.EndNS - span.StartNS) / 1000000, Reads: reads, LogPath: kept, LogRef: logRef}
		if span.Kind == "go-tool" && spanClassified.Outcome == checkresult.Fail {
			result = append(result, GoSuiteFailures(span.Label, failure, check)...)
			continue
		}
		result = append(result, check)
	}
	for _, skip := range file.SkippedChecks {
		if skip.Owed || skip.CheckID == "" {
			continue
		}
		result = append(result, HeavyCheck{CheckID: skip.CheckID, Result: "unavailable", Detail: skip.Reason})
	}
	if exitClassified.Outcome != checkresult.Pass && exitClassified.Outcome != checkresult.Fail {
		result = append(result, HeavyCheck{CheckID: "legacy-runner", Result: "error", Detail: fmt.Sprintf("exit %d: %s", exitNum, stderr.String()), LogPath: LegacyKeptLog(logPath, "error")})
	}
	if only == "" {
		for _, check := range result {
			HeavyStoreWholeStepProgress(ctx, check.CheckID, check.Result)
		}
	}
	return result, nil
}

// LegacyLogPath is where the legacy program's output for the commit checked out in dir
// goes: the proof's log directory for that commit, the same one the declaration judge
// writes to. A proof of the whole program is legacy-proof.log; a replay of one check is
// legacy-replay-<check>.log. It answers "" when the directory cannot be made.
func LegacyLogPath(ctx context.Context, laneRoot, dir, only string) string {
	rev, err := HeavyGitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil || rev == "" {
		return ""
	}
	logs := filepath.Join(laneRoot, ".rolling", "logs", rev)
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return ""
	}
	name := "legacy-proof.log"
	if only != "" {
		name = "legacy-replay-" + strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
				return r
			}
			return '_'
		}, only) + ".log"
	}
	return filepath.Join(logs, name)
}

// LegacyKeptLog answers the log path for a failing verdict, or "" when the file is
// missing or empty: a path to nothing would send the fixer to read nothing.
func LegacyKeptLog(path, verdict string) string {
	if path == "" || drainmodel.ClassifyCheck(checkresult.Observation{Source: checkresult.HeavyCheckWord, Word: verdict}).Outcome == checkresult.Pass {
		return ""
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		return ""
	}
	return path
}

// RunHeavyDeclarationGroup runs a declaration check in its own process group.
// Declaration checks have their own process groups for CPU observation. Give the
// gate a termination signal so it cancels and reaps those groups before tree removal.
func RunHeavyDeclarationGroup(cmd *exec.Cmd) error {
	return RunHeavyDeclarationGroupStarted(cmd, nil)
}

func DecodeHeavyVerdict(raw []byte) ([]HeavyCheck, error) {
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
	for i := range env.Payload.Checks {
		c := &env.Payload.Checks[i]
		classified := drainmodel.ClassifyCheck(checkresult.Observation{Check: c.CheckID, Source: checkresult.HeavyCheckWord, Word: c.Result})
		if (classified.Outcome == checkresult.Fail || (classified.Outcome == checkresult.Unknown && (classified.Detail == checkresult.DetailError || classified.Detail == checkresult.DetailStalled))) && c.LogPath == "" && c.LogRef != "" {
			whole := strings.TrimSuffix(c.LogRef, ".triage.log")
			if whole != c.LogRef {
				if kept := LegacyKeptLog(whole+".log", c.Result); kept != "" {
					c.LogPath = kept
				}
			}
			if c.LogPath == "" {
				c.LogPath = LegacyKeptLog(c.LogRef, c.Result)
			}
		}
	}
	return env.Payload.Checks, nil
}
