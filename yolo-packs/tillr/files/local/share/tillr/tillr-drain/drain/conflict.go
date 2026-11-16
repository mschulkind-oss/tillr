package drain

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// A squash conflict is a fact about a moved trunk, not a verdict about the candidate's
// work. The lander records `squash-conflict` with the CONFLICT lines git reported, and the
// drain answers it with one bounded conflict-fix run: the same item, the same lane, the
// same backend, staged from the new trunk with the conflicting paths and the three-way
// conflict markers in its brief. The fix run's result is judged like any other change,
// because the tree that lands is the tree the gate must see.
//
// At most one such run is spent per candidate. The fix run's id carries
// ConflictFixSuffix, so a conflict on the fix run is the second one: it holds the item with
// ReasonConflictExhausted instead of staging another run. That boundary is structural and
// does not depend on a budget a caller can forget to set.

// isConflictFixRun reports whether this run is itself a conflict-fix run. A run id is the
// one thing the loop can read from a candidate after the fact, and the suffix is the marker
// that makes a second conflict distinguishable without a second store row.
func isConflictFixRun(runID string) bool {
	return strings.HasSuffix(runID, ConflictFixSuffix)
}

// conflictFix records the conflict and, on a moved trunk, stages the one fix run the
// candidate is allowed. The first conflict answers with a fix run; a conflict on that run
// answers with the exhausted hold; a conflict on an unmoved trunk, a fork-series candidate,
// or a candidate whose request the loop does not hold answers with the conflict event alone
// and leaves the item held rather than failing it.
func (l *Loop) conflictFix(ctx context.Context, candidate Candidate, landed LandOutcome, result *LoopResult, timing *PassTiming) (string, bool, error) {
	if isConflictFixRun(candidate.RunID) {
		return l.holdConflictExhausted(ctx, candidate, landed, result)
	}
	mainSHA, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return "", false, err
	}
	// The fix run only makes sense when the trunk actually moved: a conflict against the
	// candidate's own base is not something a re-application onto the same tree can resolve.
	if mainSHA == "" || mainSHA == candidate.BaseSHA || candidate.Request == nil || candidate.BaseRef != "" {
		if err := l.recordConflict(ctx, candidate, landed, ""); err != nil {
			return "", false, err
		}
		l.mu.Lock()
		result.ConflictExhausted++
		l.mu.Unlock()
		l.reportf("loop: %s conflicted and no conflict-fix run is possible (trunk %s, base %s); holding it as %s\n",
			candidate.ItemID, shortSHA(mainSHA), shortSHA(candidate.BaseSHA), ReasonConflictExhausted)
		return "", false, nil
	}
	basis, err := l.repairBasis(ctx, candidate, landed)
	if err != nil {
		return "", false, err
	}
	basis.TrunkSHA = mainSHA
	fixRunID := candidate.RunID + ConflictFixSuffix
	fix := *candidate.Request
	if l.cfg.Policy != nil {
		if _, ok := l.cfg.Policy.Backends[fix.Backend]; !ok {
			return "", false, l.holdRepair(ctx, basis, fmt.Errorf("original repair backend %q is absent from the policy; restore that backend", fix.Backend))
		}
		fix = *l.cfg.Policy.request(candidate.ItemID, candidate.LaneID, fixRunID, fix.Backend)
	}
	fix.RunID = fixRunID
	fix.Resume = false
	fix.CorrelationID = candidate.TickID
	markers, err := l.conflictMarkers(ctx, candidate.BaseSHA, mainSHA, candidate.HeadSHA)
	if err != nil {
		return "", false, l.holdRepair(ctx, basis, err)
	}
	// Pin before resetting: the repair brief is not yet staged and its failure
	// must leave the original candidate reachable even after garbage collection.
	if err := l.pin(ctx, candidate.RunID, candidate.HeadSHA); err != nil {
		return "", false, err
	}
	// The lane still holds the candidate's commit. Reset it to the moved trunk before
	// staging, because the stager is right to refuse a lane whose unlanded commit nobody
	// accounted for; the candidate is pinned, so the reset loses nothing.
	if err := l.resetLane(ctx, candidate.LaneID); err != nil {
		return "", false, l.holdRepair(ctx, basis, err)
	}
	// Reapply only the worker's change. The pinned prepared head above retains
	// the gate evidence, but its loop-owned retirement must be regenerated after
	// repair, not attributed to the worker as an edit of its own item block.
	reapplyHead := candidate.HeadSHA
	if candidate.WorkerHeadSHA != "" {
		reapplyHead = candidate.WorkerHeadSHA
	}
	spec := StageSpec{
		LaneID: candidate.LaneID, ItemID: candidate.ItemID, RunID: fixRunID,
		Backend: fix.Backend, BaseSHA: mainSHA, WorkingDir: fix.WorkingDir,
		TimeoutMS: fix.TimeoutMS, BlockConflict: candidate.BlockConflict,
		Conflict: &ConflictBrief{
			Paths:         conflictPaths(landed.ConflictPaths),
			Reports:       landed.ConflictPaths,
			Markers:       markers,
			HeadSHA:       reapplyHead,
			TrunkSHA:      mainSHA,
			OriginalRunID: candidate.RunID,
		},
	}
	if err := l.cfg.Stage.Stage(ctx, spec); err != nil {
		return "", false, l.holdRepair(ctx, basis, err)
	}
	if err := l.recordConflict(ctx, candidate, landed, fixRunID); err != nil {
		return "", false, err
	}
	l.mu.Lock()
	result.ConflictFixes++
	l.mu.Unlock()
	l.reportf("loop: %s conflicted on %s; staging one conflict-fix run %s with %d path(s)\n",
		candidate.ItemID, shortSHA(mainSHA), fixRunID, len(spec.Conflict.Paths))
	// The fix keeps the candidate's id plus its suffix, but it is its own attempt.
	// Count the preceding run rows, including a failover of the original candidate.
	attempt, err := l.nextItemAttempt(ctx, candidate.ItemID)
	if err != nil {
		return "", false, err
	}
	decision := Decision{
		Attempt: attempt, From: "candidate", To: "running", PassID: candidate.TickID,
		ItemID: candidate.ItemID, LaneID: candidate.LaneID,
		Action: ActionDispatch, Reason: ReasonConflictFix,
		RunRequest: &fix, Seat: candidate.Seat, Inputs: &basis.Inputs, BlockConflict: candidate.BlockConflict,
	}
	identifyDecision(&decision)
	if err := l.handoffRepair(ctx, candidate, decision, mainSHA); err != nil {
		return "", false, err
	}
	outcome := l.runLane(ctx, decision, mainSHA, candidate.TickID, l.cfg.Now().UTC(), result, timing, nil)
	if outcome.err != nil {
		return "", false, outcome.err
	}
	if outcome.stop != "" {
		return outcome.stop, false, nil
	}
	return "", false, errRepairPending
}

// nextItemAttempt counts stored runs rather than parsing a run id: legacy ids and
// conflict-fix suffixes are both valid history and each row consumes one ordinal.
func (l *Loop) nextItemAttempt(ctx context.Context, itemID string) (int, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return 0, readFailure(err)
	}
	defer s.Close()
	var count int
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM runs WHERE item_id = ?`, itemID).Scan(&count)
	})
	if err != nil {
		return 0, readFailure(err)
	}
	return count + 1, nil
}

// holdConflictExhausted is the second conflict on one candidate: the one fix run is spent,
// so the item is held with a distinct reason rather than failing a verdict or spending
// another run.
func (l *Loop) holdConflictExhausted(ctx context.Context, candidate Candidate, landed LandOutcome, result *LoopResult) (string, bool, error) {
	if err := l.recordConflict(ctx, candidate, landed, ""); err != nil {
		return "", false, err
	}
	l.mu.Lock()
	result.ConflictExhausted++
	l.mu.Unlock()
	l.reportf("loop: %s conflicted again on its conflict-fix run; holding it as %s\n",
		candidate.ItemID, ReasonConflictExhausted)
	return "", false, nil
}

// recordConflict appends the `drain.refusal` that says the candidate hit a squash conflict
// and, on the first one, names the fix run that answers it. It is the record a planner
// reads to hold the item with ReasonConflictExhausted when no fix run can be staged, and
// the record a person reads to see which run was the fix.
func (l *Loop) recordConflict(ctx context.Context, candidate Candidate, landed LandOutcome, fixRunID string) error {
	return appendJudgment(ctx, l.cfg.Store, RefusalKind, "run", candidate.RunID, refusalPayload{
		Step:          RefusalConflict,
		ItemID:        candidate.ItemID,
		LaneID:        candidate.LaneID,
		TickID:        candidate.TickID,
		ConflictPaths: landed.ConflictPaths,
		FixRunID:      fixRunID,
	}, l.cfg.Now().UTC())
}

// conflictPaths answers the bare paths in the lander's conflict lines. The lander records
// the CONFLICT lines git printed, not a path list, so the brief names both the lines (the
// evidence) and the paths a model can open.
func conflictPaths(reports []string) []string {
	paths := make([]string, 0, len(reports))
	seen := map[string]bool{}
	for _, report := range reports {
		path := conflictPath(report)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// conflictPath answers the path in one CONFLICT line. git spells a content conflict as
// `CONFLICT (content): Merge conflict in <path>` and a modify/delete one as
// `CONFLICT (modify/delete): <path> deleted in ...`; both name the path once.
func conflictPath(report string) string {
	line := strings.TrimSpace(report)
	if i := strings.Index(line, "Merge conflict in "); i >= 0 {
		return strings.TrimSpace(line[i+len("Merge conflict in "):])
	}
	if i := strings.Index(line, "): "); i >= 0 {
		rest := strings.TrimSpace(line[i+len("): "):])
		if fields := strings.Fields(rest); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

// conflictMarkers runs the same three-way merge the lander ran and reads the conflicted
// blobs it wrote, so the fix run's brief carries the markers a model resolves rather than
// only the paths it would have to merge to see. The merge-tree output names the tree first;
// a conflict exits non-zero with that tree still on stdout, which is why the output is read
// even when the command fails.
func (l *Loop) conflictMarkers(ctx context.Context, base, trunk, head string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", l.cfg.Root,
		"merge-tree", "--write-tree", "--merge-base="+base, trunk, head)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", Externalf("reading the conflict markers for %s onto %s: %v: %s",
			shortSHA(head), shortSHA(trunk), err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(string(out), "\n")
	tree := ""
	if len(lines) > 0 {
		tree = strings.TrimSpace(lines[0])
	}
	if tree == "" {
		return "", nil
	}
	var markers strings.Builder
	for _, line := range lines[1:] {
		if !strings.HasPrefix(strings.TrimSpace(line), "CONFLICT") {
			continue
		}
		path := conflictPath(line)
		if path == "" {
			continue
		}
		body, err := l.git(ctx, l.cfg.Root, "show", tree+":"+path)
		if err != nil {
			// A path the merge deleted has no blob to show. The report above still names
			// it, so the brief keeps the path and omits the content rather than failing.
			continue
		}
		fmt.Fprintf(&markers, "### %s\n%s\n", path, body)
	}
	return markers.String(), nil
}
