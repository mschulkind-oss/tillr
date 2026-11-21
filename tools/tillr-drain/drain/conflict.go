package drain

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A squash conflict is a fact about a moved trunk, not a verdict about the candidate's
// work. The lander records `squash-conflict` with the CONFLICT lines git reported, and the
// drain answers it with a conflict-fix run: the same item, the same lane, the same backend,
// staged from the new trunk with the conflicting paths and the three-way conflict markers
// in its brief. The fix run's result is judged like any other change, because the tree
// that lands is the tree the gate must see.
//
// The owner ruled on 2026-11-17 that a conflict is never counted: the removed rule spent
// one fix run per candidate and then parked the item as conflict-exhausted. Now every
// conflict goes back to its producer with the conflicting paths, on the latest trunk — at
// once as a fix run when the trunk moved under the candidate, or else at the next pass as
// a returned run (ReasonReturned) cut from the trunk. What stops the repetition is the
// identical-repeat rule: a conflict whose paths and hunks digest (conflictEndingKey) equal
// those of an earlier conflict ending of the item's current retry cycle is not sent back
// again: the drain discards the candidate and the planner starts a fresh attempt from the
// first stage on the current trunk (restartConflicted, D-JL12). If that fresh attempt
// conflicts too, markExamined parks the item for the operator, whatever the paths and hunks.

// isConflictFixRun reports whether this run is itself a conflict-fix run. A run id is the
// one thing the loop can read from a candidate after the fact, and the suffix is the marker
// that makes a fix run distinguishable without a second store row.
func isConflictFixRun(runID string) bool {
	return strings.HasSuffix(runID, ConflictFixSuffix)
}

// conflictFixOriginal is the run a chain of conflict-fix runs started from: a fix run of a
// fix run carries the suffix once per conflict.
func conflictFixOriginal(runID string) string {
	for isConflictFixRun(runID) {
		runID = strings.TrimSuffix(runID, ConflictFixSuffix)
	}
	return runID
}

// conflictFix records the conflict with its evidence and answers it. An identical repeat of
// an earlier conflict ending of the retry cycle restarts the item (restartConflicted), and a
// conflict after that restart is recorded and left to markExamined, which parks it for the
// operator. Any other conflict on a moved trunk stages a fix run at once; a conflict on
// an unmoved trunk, a fork-series candidate, or a candidate whose request the loop does not
// hold is recorded, and the next pass returns it to the worker on the latest trunk.
func (l *Loop) conflictFix(ctx context.Context, candidate Candidate, landed LandOutcome, result *LoopResult, timing *PassTiming, repairReset ...string) (string, bool, error) {
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "repair"); err != nil {
		return "", false, err
	}
	// The marker names a conversation, not the current checkout. Never resume
	// it on, or reset, a branch that moved after this candidate was recorded.
	resetCheckout := false
	if candidate.Worktree != "" && candidate.HeadSHA != "" {
		head, err := l.revParse(ctx, candidate.Worktree, "HEAD")
		if err != nil {
			return "", false, err
		}
		if head != candidate.HeadSHA {
			// A failed earlier staging may have reset to its recorded trunk or
			// stripped the loop's prepared retirement back to the worker head.
			if len(repairReset) == 0 || (head != repairReset[0] && (candidate.WorkerHeadSHA == "" || head != candidate.WorkerHeadSHA)) {
				return "", false, l.candidateFault(ctx, candidate, fmt.Sprintf("conflict checkout moved from %s to %s", candidate.HeadSHA, head))
			}
			resetCheckout = head == repairReset[0] && head != candidate.WorkerHeadSHA
		}
	}
	mainSHA, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
	if err != nil {
		return "", false, err
	}
	// Reapply only the worker's change. The pinned prepared head retains the gate
	// evidence, but its loop-owned retirement must be regenerated after repair, not
	// attributed to the worker as an edit of its own item block.
	reapplyHead, preparedHead := candidate.HeadSHA, ""
	if candidate.WorkerHeadSHA != "" {
		reapplyHead, preparedHead = candidate.WorkerHeadSHA, candidate.HeadSHA
	}
	// The markers are the worker's change against the moved trunk, so the brief never
	// shows the worker a conflict the loop's own retirement made. They are also the
	// conflict's evidence: a merge that cannot be replayed keys on its paths alone.
	markers, markersErr := "", error(nil)
	if mainSHA != "" && candidate.BaseSHA != "" && reapplyHead != "" {
		markers, markersErr = l.conflictMarkers(ctx, candidate.BaseSHA, mainSHA, reapplyHead)
	}
	key := conflictEndingKey(landed.ConflictPaths, markers, candidate.RunID)
	worker := candidate.WorkerSession
	if worker == nil {
		worker, err = l.captureLandedWorkerSession(ctx, candidate)
		if err != nil {
			return "", false, err
		}
	}
	// With no run-keyed snapshot, the current lane could belong to another
	// worker. Never infer the old session from its mutable marker.
	candidate.WorkerSession = worker
	cycle, err := l.conflictCycleOf(ctx, candidate.ItemID)
	if err != nil {
		return "", false, err
	}
	if cycle.restarted {
		// The fresh attempt that followed a conflict restart conflicted too, whether or not
		// its paths or hunks changed: the item parks for the operator at its examination.
		if err := l.recordConflict(ctx, candidate, landed, "", key, true); err != nil {
			return "", false, err
		}
		l.reportf("loop: %s conflicted again after a restart from the current trunk; it waits on the operator\n", candidate.ItemID)
		return "", false, nil
	}
	if conflictRepeatsIn(cycle, candidate, key) {
		// The second identical conflict of the retry cycle: start over from the trunk.
		if err := l.restartConflicted(ctx, candidate, landed, key, mainSHA); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	// The fix run only makes sense when the trunk actually moved: a conflict against the
	// candidate's own base is not something a re-application onto the same tree can resolve.
	if mainSHA == "" || mainSHA == candidate.BaseSHA || candidate.Request == nil || candidate.BaseRef != "" {
		if err := l.recordConflict(ctx, candidate, landed, "", key, false); err != nil {
			return "", false, err
		}
		l.reportf("loop: %s conflicted and no conflict-fix run is possible (trunk %s, base %s); the next pass returns it to its worker on the latest trunk\n",
			candidate.ItemID, shortSHA(mainSHA), shortSHA(candidate.BaseSHA))
		return "", false, nil
	}
	policy := l.currentPolicy()
	basis, err := l.repairBasis(ctx, candidate, landed, policy)
	if err != nil {
		return "", false, err
	}
	basis.TrunkSHA = mainSHA
	fixRunID := conflictFixRunID(candidate.RunID)
	fix := *candidate.Request
	if policy != nil {
		if _, ok := policy.Backends[fix.Backend]; !ok {
			return "", false, l.holdRepair(ctx, basis, fmt.Errorf("original repair backend %q is absent from the policy; restore that backend", fix.Backend))
		}
		prev := fix
		fix = *policy.request(candidate.ItemID, candidate.LaneID, fixRunID, fix.Backend)
		fix.Seat = prev.Seat
		fix.Workflow = prev.Workflow
		fix.Node = prev.Node
		fix.NodeID = prev.NodeID
	}
	fix.RunID = fixRunID
	fix.Resume = false
	fix.SessionTurn = false
	fix.ResumePrompt = ""
	fix.CorrelationID = candidate.TickID
	ending := &ReturnedEnding{RunID: candidate.RunID, Cause: RefusalConflict, Detail: strings.Join(landed.ConflictPaths, "\n"), WorkerSession: worker}
	reason := conflictWorkerReason(worker, &fix, candidate.LaneID)
	if resetCheckout {
		// The session may survive, but its branch was already released for
		// the fresh repair. Do not open it on the trunk checkout.
		reason = "lane-unavailable"
	}
	// Pin before resetting: the repair brief is not yet staged and its failure
	// must leave the original candidate reachable even after garbage collection.
	if err := l.pinCandidateHead(ctx, candidate); err != nil {
		return "", false, err
	}
	// The lane still holds the candidate's commit. Reset it to the moved trunk before
	// staging, because the stager is right to refuse a lane whose unlanded commit nobody
	// accounted for; the candidate is pinned, so the reset loses nothing.
	if err := l.preserveWork(ctx, candidate.LaneID, candidate.RunID, "candidate conflicted during landing"); err != nil {
		return "", false, l.holdRepair(ctx, basis, err)
	}
	// Whatever the fix run commits is judged, fenced and landed against its recorded
	// base, so that base must be the trunk the fix is made on. A resumed session on an
	// ordinary lane therefore gets its commits replayed onto the trunk (the base moves
	// with them); when they do not replay cleanly, which is the usual shape of a squash
	// conflict, the session is not resumed and the lane is restaged on the trunk instead.
	// Either way no recorded base is older than the trunk the conflict was read on.
	base := mainSHA
	if reason == "resumed-worker" {
		if preparedHead != "" && reapplyHead != preparedHead {
			if _, err := l.git(ctx, candidate.Worktree, "reset", "--hard", reapplyHead); err != nil {
				return "", false, l.holdRepair(ctx, basis, err)
			}
		}
		if l.laneBasedOnTrunk(candidate.LaneID) {
			moved, err := l.moveLaneOntoTip(ctx, candidate.LaneID, reapplyHead, mainSHA)
			if err != nil {
				return "", false, l.holdRepair(ctx, basis, err)
			}
			if moved == "" {
				reason = "trunk-conflict"
			} else {
				reapplyHead, preparedHead = moved, ""
			}
		} else {
			// A fork or workflow-node lane keeps the base its boundary was cut from.
			base = worker.BaseSHA
		}
	}
	if reason == "resumed-worker" {
		fix.SessionTurn = true
		fix.ResumePrompt = returnedBrief(*ending) + fmt.Sprintf("\nThe trunk is %s and this lane's base is now %s. Reconcile your branch with it; the staged brief describes the conflict evidence.\n", mainSHA, base)
	} else if err := l.resetLaneTo(ctx, candidate.LaneID, mainSHA); err != nil {
		return "", false, l.holdRepair(ctx, basis, err)
	}
	if markersErr != nil {
		return "", false, l.holdRepair(ctx, basis, markersErr)
	}
	spec := StageSpec{
		Policy: l.currentPolicy(),
		LaneID: candidate.LaneID, ItemID: candidate.ItemID, RunID: fixRunID,
		Backend: fix.Backend, BaseSHA: base, WorkingDir: fix.WorkingDir,
		TimeoutMS: fix.TimeoutMS, BlockConflict: candidate.BlockConflict,
		Conflict: &ConflictBrief{
			Paths:         conflictPaths(landed.ConflictPaths),
			Reports:       landed.ConflictPaths,
			Markers:       markers,
			HeadSHA:       reapplyHead,
			TrunkSHA:      mainSHA,
			OriginalRunID: candidate.RunID,
			PreparedHead:  preparedHead,
		},
	}
	if fix.SessionTurn {
		spec.Continue = true
		spec.SessionID = worker.SessionID
	}
	if err := l.stageLane(ctx, spec); err != nil {
		return "", false, l.holdRepair(ctx, basis, err)
	}
	if err := l.recordConflict(ctx, candidate, landed, fixRunID, key, false); err != nil {
		return "", false, err
	}
	l.mu.Lock()
	result.ConflictFixes++
	l.mu.Unlock()
	l.reportf("loop: %s conflicted on %s; staging conflict-fix run %s with %d path(s)\n",
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
		RunRequest: &fix, Seat: candidate.Seat, SelectedWorkflow: candidate.Workflow, Inputs: &basis.Inputs, BlockConflict: candidate.BlockConflict,
		Returned: ending, WorkerSessionReason: reason,
	}
	stampDecisionRequest(decision, &fix)
	identifyDecision(&decision)
	if err := l.handoffRepair(ctx, candidate, decision, base); err != nil {
		return "", false, err
	}
	outcome := l.runLane(ctx, decision, base, candidate.TickID, l.cfg.Now().UTC(), result, timing, nil)
	if outcome.err != nil {
		return "", false, outcome.err
	}
	if outcome.stop != "" {
		return outcome.stop, false, nil
	}
	return "", false, errRepairPending
}

// A reassigned held candidate may no longer be in the worker's checkout. Never
// open its session in a different lane, even when the original handle survives.
func conflictWorkerReason(worker *WorkerSession, req *run.Request, lane string) string {
	if worker != nil && worker.LaneID != lane {
		return "lane-unavailable"
	}
	return workerSessionReason(worker, req)
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

// recordConflict appends the `drain.refusal` that says the candidate hit a squash conflict,
// with its evidence key (conflictEndingKey) and the conflict lines as its detail, and names
// the fix run that answers it when one was staged. It is the record the identical-repeat
// rule compares, the evidence a returned run's brief carries, and the record a person
// reads to see which run was the fix.
func (l *Loop) recordConflict(ctx context.Context, candidate Candidate, landed LandOutcome, fixRunID, key string, repeated bool) error {
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "conflict"); err != nil {
		return err
	}
	payload := refusalPayload{
		Detail:        strings.Join(landed.ConflictPaths, "\n"),
		Step:          RefusalConflict,
		ItemID:        candidate.ItemID,
		LaneID:        candidate.LaneID,
		TickID:        candidate.TickID,
		ConflictPaths: landed.ConflictPaths,
		FixRunID:      fixRunID,
		Evidence:      key,
		WorkerSession: candidate.WorkerSession,
	}
	if !repeated {
		payload.BaseSHA = candidate.BaseSHA
		payload.HeadSHA = candidate.HeadSHA
	}
	at := l.cfg.Now().UTC()
	evidence := l.readRefusalEvidence(candidate.RunID, payload)
	return appendJudgmentThen(ctx, l.cfg.Store, RefusalKind, "run", candidate.RunID, payload, at, func(tx *store.Tx) error {
		if repeated {
			return nil
		}
		return l.answerLandingRefusal(tx, candidate, payload, evidence, at)
	})
}

// conflictRepeatsIn answers whether a conflict with this key repeats an earlier conflict
// ending of the retry cycle: the identical-repeat rule (repeatedEnding) over the cycle's
// endings. A squash conflict is a fact about the candidate against the trunk, not about who
// produced it, so the producer is not compared: a returned run of a workflow restarts at its
// first stage and so acts for another seat than the run it repeats. An empty key repeats
// nothing.
func conflictRepeatsIn(cycle conflictCycle, candidate Candidate, key string) bool {
	if key == "" {
		return false
	}
	chain := make([]chainEnding, len(cycle.chain))
	for i, e := range cycle.chain {
		e.Producer = ""
		chain[i] = e
	}
	_, repeated := repeatedEnding(chain, chainEnding{RunID: candidate.RunID, Cause: RefusalConflict, Key: key})
	return repeated
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
