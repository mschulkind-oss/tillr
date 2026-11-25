package drain

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	mainSHA, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
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
	hasManifest, err := l.hasPackManifest(ctx, l.cfg.Root, mainSHA)
	if err != nil {
		return "", false, err
	}
	tree, mergeReports, markersErr := "", []string(nil), error(nil)
	if mainSHA != "" && candidate.BaseSHA != "" && reapplyHead != "" {
		mergeRepo := l.cfg.Root
		if candidate.Worktree != "" {
			mergeRepo = candidate.Worktree
		}
		tree, mergeReports, markersErr = l.mergeTree(ctx, mergeRepo, candidate.BaseSHA, mainSHA, reapplyHead)
	}
	mirrorOmitted := false
	if hasManifest {
		filteredReports, mirrorModules := filterMirrorConflictReports(landed.ConflictPaths)
		if len(landed.ConflictPaths) > 0 && len(filteredReports) == 0 && len(mirrorModules) > 0 {
			if candidate.Worktree != "" && candidate.BaseSHA != "" && reapplyHead != "" && mainSHA != "" && mainSHA != candidate.BaseSHA && candidate.BaseRef == "" && candidate.mirrorRepairCount < 2 && markersErr == nil && tree != "" {
				nonMirrorMerge, _ := filterMirrorConflictReports(mergeReports)
				if len(nonMirrorMerge) == 0 {
					return l.repairMirrorOnlyConflict(ctx, candidate, mainSHA, reapplyHead, tree, mirrorModules, result, timing)
				}
				landed.ConflictPaths = nonMirrorMerge
				mirrorOmitted = true
			}
		} else if len(filteredReports) > 0 && len(mirrorModules) > 0 {
			landed.ConflictPaths = filteredReports
			mirrorOmitted = true
		}
	}
	markers := ""
	if markersErr == nil && tree != "" {
		mergeRepo := l.cfg.Root
		if candidate.Worktree != "" {
			mergeRepo = candidate.Worktree
		}
		markers, markersErr = l.conflictMarkersFromTree(ctx, mergeRepo, tree, mergeReports, hasManifest)
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
	// Do not hand off a new conflict-fix run while a loop restart is pending:
	// startRunFaceContext would refuse to launch it, and leaving the gated
	// candidate intact lets the replacement loop stage and run conflict repair
	// cleanly after exec.
	if pending, err := l.runDispatchHeld(ctx); err != nil {
		return "", false, err
	} else if pending {
		l.noteConflictRestartWait(candidate.RunID, candidate.HeadSHA, mainSHA)
		return "", false, errRestartPending
	}
	policy := l.currentPolicy()
	basis, err := l.repairBasis(ctx, candidate, landed, policy)
	if err != nil {
		return "", false, err
	}
	basis.TrunkSHA = mainSHA
	fixRunID, err := l.nextConflictFixRunID(ctx, candidate.RunID)
	if err != nil {
		return "", false, err
	}
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
	// Capture the candidate's fence and stage metadata before resetting the lane
	// so that if the conflict-fix run ends at a restart, the loop can restage the
	// pinned candidate and resume conflict repair rather than restarting the item.
	heldFence := ""
	if path, fenceErr := l.itemFencePath(candidate.LaneID); fenceErr == nil {
		if raw, readErr := os.ReadFile(path); readErr == nil {
			heldFence = string(raw)
		}
	}
	var heldStage *heldLaneStage
	if candidate.BaseRef != "" || candidate.Workflow != "" {
		if saved, saveErr := l.saveHeldLaneStage(ctx, candidate); saveErr == nil {
			heldStage = saved
		}
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
			MirrorOmitted: mirrorOmitted,
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
	// Allocate the next attempt ordinal the same way the planner does, rather than
	// counting run rows across multi-node workflow attempts.
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
	if err := l.handoffRepairWithStage(ctx, candidate, decision, base, heldFence, heldStage); err != nil {
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

// nextConflictFixRunID allocates an unused -cfix run ID for candidate.RunID.
// When an earlier -cfix run ended at a restart (leaving a terminal runs row or
// drain.orphaned event), resuming conflict repair on the same built candidate
// must pick a fresh run ID so run allocation and start admission do not collide
// with the ended -cfix run.
func (l *Loop) nextConflictFixRunID(ctx context.Context, candidateRunID string) (string, error) {
	id := conflictFixRunID(candidateRunID)
	if l.cfg.Store == "" {
		return id, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", readFailure(err)
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		for {
			var used int
			if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE run_id = ?)`, id).Scan(&used); err != nil {
				return readFailure(err)
			}
			if used == 0 {
				if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind = ? AND object_id = ?)`, loopOrphanedKind, id).Scan(&used); err != nil {
					return readFailure(err)
				}
			}
			if used == 0 {
				return nil
			}
			next := conflictFixRunID(id)
			if next == id {
				return fmt.Errorf("could not allocate a fresh conflict-fix run id from %s", id)
			}
			id = next
		}
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// nextItemAttempt allocates the next attempt ordinal using the planner's attempt inputs
// rather than counting run rows: a workflow attempt with parallel nodes records multiple
// runs under one attempt, while legacy non-workflow runs without -a<N> IDs still consume
// one ordinal per run row.
func (l *Loop) nextItemAttempt(ctx context.Context, itemID string) (int, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return 0, readFailure(err)
	}
	defer s.Close()
	var next int
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var hasNodes bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&hasNodes); err != nil {
			return readFailure(err)
		}
		_, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		n, count, err := readAttemptInputs(r, hasNodes, itemID, nil, byRun)
		if err != nil {
			return err
		}
		if count == 0 {
			ids, err := readRunIDs(r, itemID)
			if err != nil {
				return readFailure(err)
			}
			if len(ids)+1 > n {
				n = len(ids) + 1
			}
		}
		next = n
		return nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
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

// mergeTree runs the same three-way merge the lander ran and returns the written tree SHA
// and any CONFLICT lines git reported. A conflict exits non-zero with the tree still on
// stdout, so stdout is read even when the command fails.
func (l *Loop) mergeTree(ctx context.Context, repo, base, trunk, head string) (string, []string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repo,
		"merge-tree", "--write-tree", "--merge-base="+base, trunk, head)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return "", nil, Externalf("reading the conflict markers for %s onto %s: %v: %s",
			shortSHA(head), shortSHA(trunk), err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(string(out), "\n")
	tree := ""
	if len(lines) > 0 {
		tree = strings.TrimSpace(lines[0])
	}
	if tree == "" {
		return "", nil, nil
	}
	var reports []string
	for _, line := range lines[1:] {
		if strings.HasPrefix(strings.TrimSpace(line), "CONFLICT") {
			reports = append(reports, strings.TrimSpace(line))
		}
	}
	return tree, reports, nil
}

// conflictMarkersFromTree reads the conflicted blobs mergeTree wrote, so the fix run's
// brief carries the markers a model resolves rather than only the paths it would have to
// merge to see. When skipPackMirror is true, generated pack-mirror copies are omitted.
func (l *Loop) conflictMarkersFromTree(ctx context.Context, repo, tree string, reports []string, skipPackMirror bool) (string, error) {
	var markers strings.Builder
	for _, line := range reports {
		path := conflictPath(line)
		if path == "" {
			continue
		}
		if skipPackMirror {
			if _, ok := packMirrorModule(path); ok {
				continue
			}
		}
		body, err := l.git(ctx, repo, "show", tree+":"+path)
		if err != nil {
			// A path the merge deleted has no blob to show. The report above still names
			// it, so the brief keeps the path and omits the content rather than failing.
			continue
		}
		fmt.Fprintf(&markers, "### %s\n%s\n", path, body)
	}
	return markers.String(), nil
}

// repairMirrorOnlyConflict mechanically repairs a conflict whose only conflicting paths
// are generated pack-mirror copies: it applies the cleanly merged non-mirror tree onto
// mainSHA, restores pack-mirror paths to mainSHA so prepareCandidate regenerates them via
// pack-sync, commits, and re-enters verify with no worker run.
func (l *Loop) repairMirrorOnlyConflict(ctx context.Context, candidate Candidate, mainSHA, reapplyHead, tree string, mirrorModules []string, result *LoopResult, timing *PassTiming) (string, bool, error) {
	if err := l.pinCandidateHead(ctx, candidate); err != nil {
		return "", false, err
	}
	if err := l.preserveWork(ctx, candidate.LaneID, candidate.RunID, "candidate conflicted only on pack mirror"); err != nil {
		return "", false, err
	}
	if err := l.resetLaneTo(ctx, candidate.LaneID, mainSHA); err != nil {
		return "", false, err
	}
	if _, err := l.git(ctx, candidate.Worktree, "read-tree", "--reset", "-u", tree); err != nil {
		return "", false, Externalf("applying merged tree for mirror-only conflict repair of %s: %v", candidate.ItemID, err)
	}
	if err := l.restorePackMirrorPaths(ctx, candidate.Worktree, mainSHA); err != nil {
		return "", false, err
	}
	changed, err := l.git(ctx, candidate.Worktree, "diff", "--cached", "--name-only")
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(changed) == "" {
		// Non-mirror changes are already on mainSHA. Regenerate the conflicted mirror
		// modules to see whether any mirror drift remains; otherwise settle as already-on-trunk.
		synced, err := l.syncAndStagePackMirrorModules(ctx, candidate, mirrorModules)
		if err != nil {
			return "", false, err
		}
		if !synced {
			l.reportf("loop: %s conflicted only on pack mirror paths (%s) and its change is already on %s\n",
				candidate.ItemID, strings.Join(mirrorModules, ", "), shortSHA(mainSHA))
			return "", false, l.settleAlreadyOnTrunk(ctx, candidate)
		}
	}
	msg, err := l.git(ctx, candidate.Worktree, "log", "-1", "--format=%B", reapplyHead)
	if err != nil || strings.TrimSpace(msg) == "" {
		msg = "chore: reapply candidate onto trunk"
	} else {
		msg = strings.TrimRight(msg, "\n")
	}
	if _, err := l.git(ctx, candidate.Worktree, "commit", "-m", msg); err != nil {
		return "", false, Externalf("committing mirror-only conflict repair for %s: %v", candidate.ItemID, err)
	}
	newHead, err := l.revParse(ctx, candidate.Worktree, "HEAD")
	if err != nil {
		return "", false, err
	}
	repaired := candidate
	repaired.BaseSHA = mainSHA
	repaired.HeadSHA = newHead
	repaired.WorkerHeadSHA = ""
	repaired.RetirementTreeSHA = ""
	repaired.mirrorRepairCount = candidate.mirrorRepairCount + 1
	if repaired.RebasedFromHead == "" && reapplyHead != "" && reapplyHead != newHead {
		repaired.RebasedFromHead = reapplyHead
	}
	if err := l.recordMirrorRebasedCandidate(ctx, repaired); err != nil {
		return "", false, err
	}
	l.reportf("loop: %s conflicted only on pack mirror paths (%s); repaired mechanically onto %s without a worker run\n",
		candidate.ItemID, strings.Join(mirrorModules, ", "), shortSHA(mainSHA))
	return l.verify(ctx, &repaired, result, timing)
}

func (l *Loop) restorePackMirrorPaths(ctx context.Context, worktree, mainSHA string) error {
	diffOut, err := l.git(ctx, worktree, "diff", "--cached", "--name-only", "-z", mainSHA, "--", tillrMirrorRoot)
	if err != nil || diffOut == "" {
		return err
	}
	var mirrorPaths []string
	for _, p := range strings.Split(diffOut, "\x00") {
		if p == "" {
			continue
		}
		if _, ok := packMirrorModule(p); ok {
			mirrorPaths = append(mirrorPaths, p)
		}
	}
	if len(mirrorPaths) == 0 {
		return nil
	}
	inMainOut, err := l.git(ctx, worktree, append([]string{"ls-tree", "-z", "-r", "--name-only", mainSHA, "--"}, mirrorPaths...)...)
	if err != nil {
		return err
	}
	inMain := map[string]bool{}
	for _, p := range strings.Split(inMainOut, "\x00") {
		if p != "" {
			inMain[p] = true
		}
	}
	var toCheckout, toRemove []string
	for _, p := range mirrorPaths {
		if inMain[p] {
			toCheckout = append(toCheckout, p)
		} else {
			toRemove = append(toRemove, p)
		}
	}
	if len(toCheckout) > 0 {
		if _, err := l.git(ctx, worktree, append([]string{"checkout", mainSHA, "--"}, toCheckout...)...); err != nil {
			return err
		}
	}
	if len(toRemove) > 0 {
		if _, err := l.git(ctx, worktree, append([]string{"rm", "-f", "--ignore-unmatch", "--"}, toRemove...)...); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) recordMirrorRebasedCandidate(ctx context.Context, candidate Candidate) error {
	if candidate.LaneID != "" && l.cfg.LaneRoot != "" {
		laneDir := l.laneDirPath(candidate.LaneID)
		for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
			path := filepath.Join(laneDir, name)
			if _, statErr := os.Stat(path); statErr == nil {
				if err := os.WriteFile(path, []byte(candidate.BaseSHA+"\n"), 0o644); err != nil {
					return Externalf("recording lane %s's base: %v", candidate.LaneID, err)
				}
			}
		}
	}
	if l.cfg.Store != "" {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			return appendEffect(tx, candidatePreparedKind, candidate.RunID, candidate, l.cfg.Now().UTC())
		})
		s.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
