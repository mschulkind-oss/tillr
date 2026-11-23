package drain

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr/checkresult"
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
	// Modern proof selectors also include focused Go tests and declaration-file
	// heavy checks. Use their proof judge, not a whole legacy suite or range.
	if strings.HasPrefix(id, goTestPrefix) || fixerHeavyDeclaration(c.Worktree, id) {
		check, err := s.JudgeRequired(ctx, c.Worktree, id, "")
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
	fixerEnv := append(withoutProcessEnv(s.checkProcessEnv(nil), "GO_TEST_SHARDS_CLASS"), "GO_TEST_SHARDS_CLASS=repair")
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
