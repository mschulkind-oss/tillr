package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/tillr/checkresult"
)

// The owner ruled on 2026-11-17 that a worker run's failure is not counted and does not
// park its item for the owner (audit findings F-02, F-03 and F-09). A gate refusal, a run
// that committed nothing, a fence escape and a refused landing go back to the item's
// worker, with the evidence, every time: the next run of the item is dispatched with the
// ending in its brief (ReasonReturned). What stops endless repetition is not a count but
// an identical repeat: an ending whose producer, cause and evidence key equal those of an
// earlier ending of the same item goes to the design seat with both records instead
// (repeatedEnding, readRepeatedEnding).

// ReturnedEnding is one worker run's ending as the item's next run is given it: the run,
// the step that refused it, the refusal's own words, and the gate checks that failed with
// where their output is.
type ReturnedEnding struct {
	WorkerSession *WorkerSession `json:"worker_session,omitempty"`
	RunID         string         `json:"run_id"`
	Cause         string         `json:"cause"`
	Detail        string         `json:"detail,omitempty"`
	// Checks are the gate checks that failed, for a gate refusal.
	Checks []EndingCheck `json:"checks,omitempty"`
	// Restart marks the ending of a candidate the drain discarded after its second
	// identical squash conflict: the next run is a fresh attempt from the current trunk,
	// and its brief says so instead of asking for a reconciliation (D-JL12).
	Restart bool `json:"restart,omitempty"`
	// Replan is a verify-not-done ending whose findings say the plan was wrong; its next
	// run is the plan step, not the build step.
	Replan bool `json:"replan,omitempty"`
	// ScopeEscapes are the paths a piece wrote outside its own write_scope but inside its
	// item's; the ending returns to that piece (pieceScopeReturn).
	ScopeEscapes []string `json:"scope_escapes,omitempty"`
}

// returnedBrief is the brief's section for a run dispatched with an earlier run's ending:
// what refused it, in the refusal's own words and the failing checks' own logs, and where
// the earlier run's work is kept.
func returnedBrief(e ReturnedEnding) string {
	if e.Restart {
		return restartBrief(e)
	}
	if verifyNoVerdictReturn(&e) {
		return "\n## Previous verify run\n\n" + e.Detail + ". No finding was made against the candidate; verify the retained candidate.\n"
	}
	var b strings.Builder
	// A gate refusal's reply opens the turn, before the restated brief; an answer text that
	// already carries it (Detail, from a parked call's answer) is not repeated.
	if e.Cause == RefusalGate && !strings.Contains(e.Detail, failureHeading) {
		b.WriteString("\n" + failureSummary(e.Checks))
	}
	b.WriteString("\n## Your previous attempt\n\n")
	fmt.Fprintf(&b, "This item's previous run, %s, did not land. ", e.RunID)
	switch e.Cause {
	case RefusalGate:
		if gateBuildReturn(&e) {
			b.WriteString("The gate refused the candidate commit this run starts from. Fix the findings on this candidate commit and keep the commit clean; if they cannot be met within the plan, end with RESULT: blocked, OBSTACLE: definition, CRITERIA: plan, and EVIDENCE: plan-wrong: <reason>.")
		} else {
			b.WriteString("The gate refused its candidate.")
		}
	case RefusalVerifyNotDone:
		switch {
		case verifyBuildReturn(&e):
			b.WriteString("The workflow verifier read the candidate commit this run starts from and found the criteria below not done. Fix the findings on this candidate commit and keep the commit clean; if they cannot be met within the plan, end with RESULT: blocked, OBSTACLE: definition, CRITERIA: plan, and EVIDENCE: plan-wrong: <reason>.")
		case e.Replan:
			b.WriteString("The workflow verifier found criteria not done and said the plan was wrong; revise the plan to cover them.")
		case e.WorkerSession != nil:
			b.WriteString("The judge refused its candidate.")
		default:
			b.WriteString("The workflow verifier found criteria not done; revise the plan to cover them.")
		}
	case ReasonEvalFailed:
		b.WriteString("The judge refused its candidate.")
	case RefusalPlanWrong:
		b.WriteString("The build step reported that it cannot meet the verifier's findings within the plan; revise the plan so the work can be built.")
	case RefusalPlanOutput:
		b.WriteString("Its `delivery` output did not parse. Write the `delivery` output again as a JSON object (not a bare string) matching one of: " + deliveryShape + ` (omit "fork" for "repo").`)
	case RefusalNoOp:
		b.WriteString("It ended without committing anything, so there was no candidate to judge.")
	case RefusalFence:
		if pieceScopeReturn(&e) {
			b.WriteString("Its piece changed paths outside the piece's own write_scope, though inside the item's. This run is that piece again, from the piece's own start: do its work inside its write_scope. If it cannot be finished without the paths named below, end with RESULT: blocked, OBSTACLE: definition, CRITERIA: plan, and EVIDENCE: plan-wrong: <the paths and why the piece needs them>.")
		} else {
			b.WriteString("Its candidate failed a boundary check before the gate. Read the specific refusal below.")
		}
	case RefusalLand:
		b.WriteString("The gate passed its candidate, but the landing was refused.")
	case RefusalDeliveryOrder:
		b.WriteString("It offered the repository candidate before the fork was delivered.")
	case RefusalReverted:
		b.WriteString("It landed, and the full suite then proved that it broke the trunk, so tillr reverted it. Your next gate runs the failing check below, and the item lands again only when it passes.")
	case RefusalConflict:
		b.WriteString("Its candidate no longer applied to the trunk: the squash onto the trunk conflicted in the paths below. Reconcile the change with the latest trunk.")
	default:
		fmt.Fprintf(&b, "It was refused at %s.", e.Cause)
	}
	b.WriteString(" This is not a count against the item: fix what is named below and finish the item's work as this brief states it.\n")
	if strings.TrimSpace(e.Detail) != "" {
		// The detail is the refusal's own words, in the order and spelling it was recorded.
		b.WriteString("\nThe refusal said:\n\n```\n" + strings.TrimSpace(e.Detail) + "\n```\n")
	}
	if len(e.Checks) > 0 {
		b.WriteString("\nFailing gate checks, with each one's output:\n\n")
		for _, c := range e.Checks {
			if c.LogRef != "" {
				fmt.Fprintf(&b, "- `%s`: %s\n", c.CheckID, c.LogRef)
			} else {
				fmt.Fprintf(&b, "- `%s`\n", c.CheckID)
			}
		}
	}
	fmt.Fprintf(&b, "\nThe previous run's committed head, when it committed, is pinned at `refs/tillr/runs/%s`, and uncommitted work it left is kept at `%s%s`. Read them with `git show` or `git diff`, and reuse what is sound; do not move either ref. Commit your own work on this lane as usual.\n", e.RunID, preservedPrefix, e.RunID)
	b.WriteString("\n" + ObstacleContract + "\n")
	return b.String()
}

// endingEvidence reads a returned ending's evidence when its refusal is recorded, while the
// run's record directory and its lane still hold it: the failing gate checks with their
// logs' digests, and the ending's key. A gate refusal is keyed by its failing checks; a run
// that committed nothing by the uncommitted paths it left and their content; a fence
// escape and a refused landing by the refusal's own words. Evidence that cannot be read
// leaves the key built from what could be, never a guess.
func (l *Loop) endingEvidence(ctx context.Context, c Candidate, step string) ([]EndingCheck, string) {
	if c.EvidenceKey != "" && step != RefusalGate {
		return nil, c.EvidenceKey
	}
	switch step {
	case RefusalGate:
		checks := c.requiredFailures
		if len(checks) == 0 {
			checks = gateFailingChecks(RunDir(l.cfg.Root, c.RunID), c.RunID)
		}
		if c.EvidenceKey != "" {
			return checks, c.EvidenceKey
		}
		return checks, endingKey(step, checks, c.RefusalDetail, c.RunID)
	case RefusalNoOp:
		return nil, endingKey(step, nil, l.uncommittedEvidence(ctx, c.Worktree), c.RunID)
	}
	return nil, endingKey(step, nil, c.RefusalDetail, c.RunID)
}

// gateFailingChecks answers the failing checks of a run's gate verdicts, each with its log
// and the digest of that log's normalized content, sorted by check id.
func gateFailingChecks(runDir, runID string) []EndingCheck {
	byID := map[string]EndingCheck{}
	for _, name := range []string{GateFastVerdictFile, "gate.verdict.json"} {
		body, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			continue
		}
		var file struct {
			Payload struct {
				Checks []struct {
					CheckID string `json:"check_id"`
					Result  string `json:"result"`
					LogRef  string `json:"log_ref"`
				} `json:"checks"`
			} `json:"payload"`
		}
		if json.Unmarshal(body, &file) != nil {
			continue
		}
		for _, check := range file.Payload.Checks {
			if check.CheckID == "" || classifyCheck(checkresult.Observation{
				Check:  check.CheckID,
				Source: checkresult.ResultWord,
				Word:   check.Result,
			}).Outcome != checkresult.Fail {
				continue
			}
			if _, seen := byID[check.CheckID]; seen {
				continue
			}
			c := EndingCheck{CheckID: check.CheckID, LogRef: check.LogRef}
			if output, err := os.ReadFile(check.LogRef); err == nil && check.LogRef != "" {
				c.Digest = evidenceDigest(string(output), runID)
			}
			c.Failing = readTriageBlock(check.LogRef)
			byID[check.CheckID] = c
		}
	}
	checks := make([]EndingCheck, 0, len(byID))
	for _, c := range byID {
		checks = append(checks, c)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].CheckID < checks[j].CheckID })
	return checks
}

// uncommittedEvidence is the evidence of a run that committed nothing: the uncommitted
// paths it left in its checkout and a digest of their content, so two runs that left the
// same work are one ending and a run that changed its work is another.
func (l *Loop) uncommittedEvidence(ctx context.Context, worktree string) string {
	if worktree == "" {
		return "no checkout"
	}
	paths, err := l.uncommittedPaths(ctx, worktree)
	if err != nil {
		return "paths unreadable"
	}
	sum := sha256.New()
	if diff, err := l.git(ctx, worktree, "diff", "HEAD", "--binary"); err == nil {
		sum.Write([]byte(diff))
	}
	for _, path := range paths {
		if body, err := os.ReadFile(filepath.Join(worktree, path)); err == nil {
			sum.Write([]byte(path + "\x00"))
			sum.Write(body)
		}
	}
	return fmt.Sprintf("paths: %s\ncontent: %x", strings.Join(paths, ", "), sum.Sum(nil))
}
