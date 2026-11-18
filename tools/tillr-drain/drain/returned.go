package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
}

// EndingCheck is one failing gate check: its id, the log the gate wrote for it, and the
// digest of that log after normalizeEvidence.
type EndingCheck struct {
	CheckID string `json:"check_id"`
	LogRef  string `json:"log_ref,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// returnedCauses are the refusal steps whose ending goes back to the item's worker. Each
// is a statement about the candidate the worker can act on. A squash conflict is one: on a
// moved trunk it is answered at once by a conflict-fix run (conflictFix), and otherwise the
// next pass returns it like the others, on the latest trunk. The other refusal steps keep
// their own routes: a missing node output its node rule, and a trunk already red its fixer.
var returnedCauses = map[string]bool{
	RefusalGate:          true,
	RefusalVerifyNotDone: true,
	RefusalNoOp:          true,
	RefusalFence:         true,
	RefusalLand:          true,
	RefusalConflict:      true,
	RefusalReverted:      true,
}

// returnedReasons are the hold reasons retryHoldReason answers for those causes, the
// planner's spelling of the same outcomes. conflict-exhausted is the removed one-fix-run
// rule's name for a conflict; an item it parked is returned at the next pass.
var returnedReasons = map[string]string{
	ReasonGateFailed:          RefusalGate,
	RefusalVerifyNotDone:      RefusalVerifyNotDone,
	ReasonCandidateNoOp:       RefusalNoOp,
	ReasonBoundaryScopeEscape: RefusalFence,
	ReasonLandRefused:         RefusalLand,
	ReasonConflictExhausted:   RefusalConflict,
	ReasonLandingReverted:     RefusalReverted,
}

var (
	evidenceTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	evidenceDuration  = regexp.MustCompile(`\b\d+(\.\d+)?\s?(ns|µs|us|ms|s|m|h)\b`)
	evidenceSHA       = regexp.MustCompile(`\b[0-9a-f]{40}\b|\b[0-9a-f]{64}\b`)
	evidenceTempDir   = regexp.MustCompile(`/(tmp|var/folders)/[^\s:'"]+`)
)

// normalizeEvidence strips from a check's output what differs between two runs that failed
// the same way: the run's own id (its record directory is named for it), timestamps,
// durations, full commit and digest hashes, and temporary directories. Line numbers and
// messages are kept, because they are what tells two failures apart. A normalization that
// proves too coarse or too fine is fixed here; it is never compensated with a count.
func normalizeEvidence(output, runID string) string {
	if runID != "" {
		output = strings.ReplaceAll(output, runID, "<run>")
	}
	output = evidenceTimestamp.ReplaceAllString(output, "<time>")
	output = evidenceSHA.ReplaceAllString(output, "<sha>")
	output = evidenceTempDir.ReplaceAllString(output, "<tmp>")
	output = evidenceDuration.ReplaceAllString(output, "<duration>")
	lines := []string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// The gate's watchdog note is a fact about the host, not about the candidate.
		if line == "" || strings.HasPrefix(line, "watchdog unavailable:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// evidenceDigest is the digest of one normalized output.
func evidenceDigest(output, runID string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(normalizeEvidence(output, runID))))
}

// endingKey is an ending's evidence key: its cause with the sorted failing check ids and
// their output digests, or the normalized refusal detail when no check is named. It is ""
// when the ending carries no evidence at all, and an empty key never repeats: an ending
// recorded before keys existed is never read as a repeat of anything.
func endingKey(cause string, checks []EndingCheck, detail, runID string) string {
	if cause == "" {
		return ""
	}
	parts := []string{}
	for _, c := range checks {
		parts = append(parts, c.CheckID+"="+c.Digest)
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		parts = append(parts, "detail="+evidenceDigest(detail, runID))
	}
	return cause + ":" + strings.Join(parts, ",")
}

// conflictEndingKey is a squash conflict's evidence key: the conflicting paths, sorted, and
// the digest of the normalized three-way hunks the merge wrote for them. The same conflict
// against another trunk sha is the same key; a trunk that moved again inside the hunk is
// another. It is "" when the lander named no conflicting path.
func conflictEndingKey(reports []string, markers, runID string) string {
	paths := conflictPaths(reports)
	if len(paths) == 0 {
		return ""
	}
	sort.Strings(paths)
	return RefusalConflict + ":paths=" + strings.Join(paths, ",") + ",hunks=" + evidenceDigest(markers, runID)
}

// chainEnding is one ending of an item's chain as the repeat rule reads it: the run, who
// produced it (the seat the run acted for), the ending's cause and its evidence key.
type chainEnding struct {
	RunID, Producer, Cause, Key string
}

// repeatedEnding answers the earlier run whose ending latest repeats: the same producer,
// the same cause and the same evidence key as any earlier ending in the chain, not only
// the last, so a producer that alternates between two failures is caught on the third.
// An ending with an empty key repeats nothing. It is the rule's whole comparison and holds
// no count: two different endings are never a repeat however many there are.
func repeatedEnding(chain []chainEnding, latest chainEnding) (string, bool) {
	if latest.Key == "" || latest.Cause == "" {
		return "", false
	}
	for i := len(chain) - 1; i >= 0; i-- {
		earlier := chain[i]
		if earlier.RunID == latest.RunID {
			continue
		}
		if earlier.Producer == latest.Producer && earlier.Cause == latest.Cause && earlier.Key == latest.Key {
			return earlier.RunID, true
		}
	}
	return "", false
}

// returnedBrief is the brief's section for a run dispatched with an earlier run's ending:
// what refused it, in the refusal's own words and the failing checks' own logs, and where
// the earlier run's work is kept.
func returnedBrief(e ReturnedEnding) string {
	var b strings.Builder
	b.WriteString("\n## Your previous attempt\n\n")
	fmt.Fprintf(&b, "This item's previous run, %s, did not land. ", e.RunID)
	switch e.Cause {
	case RefusalGate:
		b.WriteString("The gate refused its candidate.")
	case RefusalVerifyNotDone:
		b.WriteString("The workflow verifier found criteria not done; revise the plan to cover them.")
	case RefusalNoOp:
		b.WriteString("It ended without committing anything, so there was no candidate to judge.")
	case RefusalFence:
		b.WriteString("Its candidate changed paths outside the item's fence, so it was refused before the gate.")
	case RefusalLand:
		b.WriteString("The gate passed its candidate, but the landing was refused.")
	case RefusalReverted:
		b.WriteString("It landed, and the full suite then proved that it broke the trunk, so tillr reverted it. Your next gate runs the failing check below, and the item lands again only when it passes.")
	case RefusalConflict:
		b.WriteString("Its candidate no longer applied to the trunk: the squash onto the trunk conflicted in the paths below. This run starts from the latest trunk; redo the change on it.")
	default:
		fmt.Fprintf(&b, "It was refused at %s.", e.Cause)
	}
	b.WriteString(" This is not a count against the item: fix what is named below and finish the item's work as this brief states it.\n")
	if strings.TrimSpace(e.Detail) != "" {
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
	if c.EvidenceKey != "" {
		return nil, c.EvidenceKey
	}
	switch step {
	case RefusalGate:
		checks := gateFailingChecks(RunDir(l.cfg.Root, c.RunID), c.RunID)
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
			if check.Result != "fail" || check.CheckID == "" {
				continue
			}
			if _, seen := byID[check.CheckID]; seen {
				continue
			}
			c := EndingCheck{CheckID: check.CheckID, LogRef: check.LogRef}
			if output, err := os.ReadFile(check.LogRef); err == nil && check.LogRef != "" {
				c.Digest = evidenceDigest(string(output), runID)
			}
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
