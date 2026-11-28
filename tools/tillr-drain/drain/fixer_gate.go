package drain

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/disabledtests"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func fixerCheckOverlaps(a, b, root string) bool {
	return fixer.FixerCheckOverlaps(a, b, root, forkCheckDeclID)
}

func fixerWaitReaches(r *store.Reader, from, target string) (bool, error) {
	return fixer.FixerWaitReaches(r, from, target)
}

// fixerUpstreamDropped is the named replay protocol's explicit omission, not a pass.
const fixerUpstreamDropped = 77

// fixerCheckStep runs one proof selector against the candidate's checked out tree.
type fixerCheckStep interface {
	fixerCheck(context.Context, Candidate, string) (result, output string)
}

func (s *ProcessSteps) fixerCheck(ctx context.Context, c Candidate, id string) (string, string) {
	if _, err := settings.ReadChecksDir(c.Worktree); err != nil {
		return "error", err.Error()
	}
	// Modern proof selectors also include focused Go tests and declaration-file
	// heavy checks. Use their proof judge, not a whole legacy suite or range.
	if strings.HasPrefix(id, goTestPrefix) || fixerHeavyDeclaration(c.Worktree, id) {
		check, err := s.JudgeRequired(context.WithValue(ctx, heavyRepairKey{}, true), c.Worktree, id, "")
		if err != nil {
			return "error", err.Error()
		}
		switch check.Detail {
		case goTestAbsent:
			return "absent", check.Detail
		case goTestSkipped:
			return "error", check.Detail
		}
		for _, path := range []string{check.LogRef, check.LogPath} {
			if raw, err := os.ReadFile(path); err == nil {
				return check.Result, string(raw)
			}
		}
		return check.Result, check.Detail
	}
	if s.FixerGateProgram == "" {
		return "error", "no named-check program configured"
	}
	fixerEnv := s.checkProcessEnv(nil)
	code, out, span, err := s.programWithEnv(ctx, "fixer-gate", c.Worktree, s.FixerGateProgram, c, fixerEnv, "--only", id)
	output := strings.TrimSpace(out + "\n" + span.StderrTail)
	if err != nil {
		return "error", strings.TrimSpace(output + "\n" + err.Error())
	}
	classified := classifyCheck(checkresult.Observation{
		Check:    id,
		Commit:   c.HeadSHA,
		Worktree: c.Worktree,
		Source:   checkresult.JudgingProgram,
		ExitCode: &code,
	})
	switch classified.Outcome {
	case checkresult.Pass:
		return string(checkresult.Pass), output
	case checkresult.Fail:
		return string(checkresult.Fail), output
	case checkresult.Unavailable:
		if classified.Detail == checkresult.DetailDropped {
			return checkresult.DetailDropped, output
		}
	}
	return checkresult.DetailError, fmt.Sprintf("exit %d: %s", code, output)
}

func fixerHeavyDeclaration(dir, id string) bool {
	return fixer.FixerHeavyDeclaration(dir, id)
}

// settleFixerGate finishes the same gate judgment; the ordinary gate has accepted
// the diff, but its verdict cannot authorize landing until the named checks answer.
func (l *Loop) settleFixerGate(ctx context.Context, c *Candidate, gate *GateOutcome, result *LoopResult) (bool, error) {
	ids, err := l.fixerNamedChecks(ctx, c.ItemID)
	if err != nil {
		return false, err
	}
	if len(ids) == 0 {
		return true, gate.finishJudgment()
	}
	runDir := RunDir(l.cfg.Root, c.RunID)
	path := filepath.Join(runDir, GateVerdictRecord)
	writeVerdict, wholeLog, err := pendingFixerVerdict(path)
	if err != nil {
		return false, err
	}
	step, _ := l.cfg.Steps.(fixerCheckStep)
	finalize := func(outcome, verdict string, checks []map[string]string) error {
		if err := writeVerdict(verdict, checks); err != nil {
			return err
		}
		gate.Outcome = outcome
		return gate.finishJudgment()
	}
	logDir := filepath.Join(runDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return false, err
	}
	if wholeLog == "" {
		wholeLog = filepath.Join(logDir, "fixer-named.log")
	}
	log, err := os.OpenFile(wholeLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer log.Close()
	var failed, unknown []string
	var checks []map[string]string
	for i, id := range ids {
		answer, output := "error", "no named-check runner configured"
		if step != nil {
			started := time.Now()
			answer, output = step.fixerCheck(ctx, *c, id)
			gate.DurationMS += time.Since(started).Milliseconds()
			l.sweepDisabledMarkers(ctx)
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		classified := classifyCheck(checkresult.Observation{
			Check:    id,
			Commit:   c.HeadSHA,
			Worktree: c.Worktree,
			Source:   checkresult.FixerCheckWord,
			Word:     answer,
		})
		if classified.Outcome == checkresult.Unavailable {
			reason := "reads upstream"
			if classified.Detail == checkresult.DetailAbsent {
				reason, output = output, ""
			}
			if _, err := fmt.Fprintf(log, "fixer named check %s: dropped (%s)\n%s\n", id, reason, output); err != nil {
				return false, err
			}
			continue
		}
		verdictWord, label := string(checkresult.Pass), string(checkresult.Pass)
		switch classified.Outcome {
		case checkresult.Pass:
		case checkresult.Fail:
			verdictWord, label = string(checkresult.Fail), string(checkresult.Fail)
		default:
			verdictWord, label = checkresult.DetailError, "could not judge"
		}
		line := fmt.Sprintf("fixer named check %s: %s\n%s\n", id, label, output)
		if _, err := io.WriteString(log, line); err != nil {
			return false, err
		}
		entry := map[string]string{"check_id": id, "result": verdictWord}
		switch classified.Outcome {
		case checkresult.Fail:
			failed = append(failed, id)
			checkLog := filepath.Join(logDir, fmt.Sprintf("fixer-named-%02d.log", i))
			if err := os.WriteFile(checkLog, []byte(line), 0644); err != nil {
				return false, err
			}
			entry["log_ref"] = checkLog
		case checkresult.Pass:
		default:
			unknown = append(unknown, id+": "+heavyBrief(output))
		}
		checks = append(checks, entry)
	}
	if len(unknown) > 0 {
		if err := finalize(GateErrored, "error", checks); err != nil {
			return false, err
		}
		return false, l.gateFault(ctx, *c, "named checks could not judge: "+strings.Join(unknown, "; ")+"; output: "+wholeLog)
	}
	if len(failed) > 0 {
		if err := finalize(GateFailed, GateFailed, checks); err != nil {
			return false, err
		}
		c.RefusalDetail = "the fixer's named checks failed: " + strings.Join(failed, ", ") + "; output: " + wholeLog
		return false, l.refuse(ctx, *c, result, RefusalGate)
	}
	return true, finalize(gate.Outcome, "", checks)
}

func (g *GateOutcome) finishJudgment() error {
	if g.finish == nil {
		return nil
	}
	finish := g.finish
	g.finish = nil
	return finish(*g)
}

func pendingFixerVerdict(path string) (func(string, []map[string]string) error, string, error) {
	return fixer.PendingFixerVerdict(path)
}

// Words a repair check answers. Absent and skipped are refusals of their own: a
// removed entry re-enables a test, so the test must exist and run.
const (
	repairPass    = "pass"
	repairFail    = "fail"
	repairAbsent  = "absent"
	repairSkipped = "skipped"
	repairError   = "error"
)

// repairCheckStep runs one re-enabled selector once against the candidate's tree.
type repairCheckStep interface {
	repairCheck(context.Context, Candidate, string) (answer, output string)
}

// repairCheck runs a removed go-test selector with `go test -count=1 -run`, which never
// reads the disabled list (so it needs no --prove-disabled), and a removed whole-check
// entry through the fixer's named-check runner.
func (s *ProcessSteps) repairCheck(ctx context.Context, c Candidate, id string) (string, string) {
	if !strings.HasPrefix(id, goTestPrefix) {
		answer, output := s.fixerCheck(ctx, c, id)
		switch answer {
		case repairPass, repairFail:
			return answer, output
		case repairAbsent, checkresult.DetailDropped:
			return repairAbsent, output
		}
		return repairError, output
	}
	check, err := s.JudgeRequired(context.WithValue(ctx, heavyRepairKey{}, true), c.Worktree, id, "")
	if err != nil {
		return repairError, err.Error()
	}
	switch check.Detail {
	case goTestAbsent, checkresult.DetailPackageAbsent:
		return repairAbsent, check.Detail
	case goTestSkipped:
		return repairSkipped, check.Detail
	}
	output := check.Detail
	for _, path := range []string{check.LogRef, check.LogPath} {
		if raw, err := os.ReadFile(path); err == nil {
			output = string(raw)
			break
		}
	}
	switch check.Result {
	case repairPass, repairFail:
		return check.Result, output
	}
	return repairError, output
}

// reenabledSelectors returns the entries of the disabled list at base that the list at
// head no longer holds, in the base list's order. The list path is the trunk's own
// `[gate] disabled_tests`, so a candidate cannot move the list out of the gate's sight:
// a candidate that deletes or moves the file removes every entry. A fork-series
// candidate delivers to a fork, whose tree has no tillr list.
func (l *Loop) reenabledSelectors(ctx context.Context, c Candidate) ([]string, error) {
	if c.BaseRef != "" || c.BaseSHA == "" || c.HeadSHA == "" {
		return nil, nil
	}
	cfg, err := settings.Read(l.cfg.Root)
	if err != nil {
		return nil, err
	}
	path := filepath.ToSlash(cfg.DisabledTestsPath())
	base, err := disabledListAt(ctx, l.cfg.Root, c.BaseSHA, path)
	if err != nil {
		return nil, fmt.Errorf("disabled-test list at base %s: %w", shortSHA(c.BaseSHA), err)
	}
	if len(base.Disabled) == 0 {
		return nil, nil
	}
	head, err := disabledListAt(ctx, l.cfg.Root, c.HeadSHA, path)
	if err != nil {
		return nil, fmt.Errorf("disabled-test list at head %s: %w", shortSHA(c.HeadSHA), err)
	}
	kept := make(map[string]bool, len(head.Disabled))
	for _, e := range head.Disabled {
		kept[e.Selector] = true
	}
	var removed []string
	for _, e := range base.Disabled {
		if !kept[e.Selector] {
			removed = append(removed, e.Selector)
		}
	}
	return removed, nil
}

// disabledListAt reads the list at commit:path. A path absent at the commit is an empty
// list, as disabledtests.Load treats a missing file.
func disabledListAt(ctx context.Context, dir, commit, path string) (disabledtests.List, error) {
	var listed, lsErr bytes.Buffer
	ls := exec.CommandContext(ctx, "git", "-C", dir, "ls-tree", "--name-only", commit, "--", path)
	ls.Stdout, ls.Stderr = &listed, &lsErr
	if err := ls.Run(); err != nil {
		return disabledtests.List{}, fmt.Errorf("git ls-tree %s: %v: %s", shortSHA(commit), err, strings.TrimSpace(lsErr.String()))
	}
	if strings.TrimSpace(listed.String()) != path {
		return disabledtests.List{Version: disabledtests.Version}, nil
	}
	var stdout, stderr bytes.Buffer
	show := exec.CommandContext(ctx, "git", "-C", dir, "show", commit+":"+path)
	show.Stdout, show.Stderr = &stdout, &stderr
	if err := show.Run(); err != nil {
		return disabledtests.List{}, fmt.Errorf("git show %s:%s: %v: %s", shortSHA(commit), path, err, strings.TrimSpace(stderr.String()))
	}
	return disabledtests.Parse(stdout.Bytes())
}

// settleRepairGate is the one-run repair gate (D-TPF4, OQ-TPF3): after the ordinary gate
// passes, a candidate that removes entries from the disabled list runs each removed
// selector once. One pass of each accepts. A failure, or a removed test that is absent
// or skipped, refuses the candidate back to its worker. When the candidate is also a
// trunk fixer, the fixer gate finishes the judgment after this one.
func (l *Loop) settleRepairGate(ctx context.Context, c *Candidate, gate *GateOutcome, result *LoopResult) (bool, error) {
	if c.reenabledErr != nil {
		if err := gate.finishFailed(); err != nil {
			return false, err
		}
		c.RefusalDetail = "the disabled-test list could not be read: " + c.reenabledErr.Error()
		return false, l.refuse(ctx, *c, result, RefusalGate)
	}
	ids := c.reenabled
	if len(ids) == 0 {
		return true, nil
	}
	runDir := RunDir(l.cfg.Root, c.RunID)
	writeVerdict, _, err := pendingFixerVerdict(filepath.Join(runDir, GateVerdictRecord))
	if err != nil {
		return false, err
	}
	finalize := func(outcome, verdict string, checks []map[string]string) error {
		if err := writeVerdict(verdict, checks); err != nil {
			return err
		}
		gate.Outcome = outcome
		return gate.finishJudgment()
	}
	logDir := filepath.Join(runDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return false, err
	}
	wholeLog := filepath.Join(logDir, "repair-reenabled.log")
	log, err := os.OpenFile(wholeLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer log.Close()
	step, _ := l.cfg.Steps.(repairCheckStep)
	var failed, refused, unknown []string
	var checks []map[string]string
	for i, id := range ids {
		answer, output := repairError, "no repair-check runner configured"
		if step != nil {
			started := time.Now()
			answer, output = step.repairCheck(ctx, *c, id)
			gate.DurationMS += time.Since(started).Milliseconds()
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		line := fmt.Sprintf("re-enabled test %s: %s\n%s\n", id, answer, output)
		if _, err := io.WriteString(log, line); err != nil {
			return false, err
		}
		entry := map[string]string{"check_id": id, "result": string(checkresult.Pass)}
		switch answer {
		case repairPass:
		case repairFail, repairAbsent, repairSkipped:
			entry["result"] = string(checkresult.Fail)
			checkLog := filepath.Join(logDir, fmt.Sprintf("repair-reenabled-%02d.log", i))
			if err := os.WriteFile(checkLog, []byte(line), 0644); err != nil {
				return false, err
			}
			entry["log_ref"] = checkLog
			if answer == repairFail {
				failed = append(failed, id)
			} else {
				refused = append(refused, id+" ("+answer+")")
			}
		default:
			entry["result"] = checkresult.DetailError
			unknown = append(unknown, id+": "+heavyBrief(output))
		}
		checks = append(checks, entry)
	}
	if len(unknown) > 0 {
		if err := finalize(GateErrored, "error", checks); err != nil {
			return false, err
		}
		return false, l.gateFault(ctx, *c, "re-enabled tests could not be judged: "+strings.Join(unknown, "; ")+"; output: "+wholeLog)
	}
	if len(failed) > 0 || len(refused) > 0 {
		if err := finalize(GateFailed, GateFailed, checks); err != nil {
			return false, err
		}
		var parts []string
		if len(failed) > 0 {
			parts = append(parts, "a re-enabled test failed its one run: "+strings.Join(failed, ", "))
		}
		if len(refused) > 0 {
			parts = append(parts, "a re-enabled test must exist and run; deleting, renaming or skipping it needs an owner obstacle: "+strings.Join(refused, ", "))
		}
		c.RefusalDetail = strings.Join(parts, "; ") + "; output: " + wholeLog
		return false, l.refuse(ctx, *c, result, RefusalGate)
	}
	if err := writeVerdict("", checks); err != nil {
		return false, err
	}
	if IsTrunkFixer(c.ItemID) {
		return true, nil
	}
	return true, gate.finishJudgment()
}

// finishFailed publishes a deferred judgment as failed.
func (g *GateOutcome) finishFailed() error {
	g.Outcome = GateFailed
	return g.finishJudgment()
}

// reenablesTests reports whether the candidate removes disabled-list entries, or whether
// the list could not be read, so the repair gate must answer before the gate verdict.
func (c Candidate) reenablesTests() bool { return len(c.reenabled) > 0 || c.reenabledErr != nil }
