package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// conflictTestCommit writes content to path in dir and commits it, returning the new HEAD.
func conflictTestCommit(t *testing.T, dir, path, content, msg string) string {
	t.Helper()
	writeForkFile(t, filepath.Join(dir, path), content)
	runGit(t, dir, "add", path)
	runGit(t, dir, "commit", "-qm", msg)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// conflictTestFinishRun writes a terminal exited record for runID and returns a Candidate
// pointing at headSHA on baseSHA.
func conflictTestFinishRun(t *testing.T, f refusalLoopFixture, l *Loop, d Decision, baseSHA, headSHA string) Candidate {
	t.Helper()
	id := d.RunRequest.RunID
	now := l.cfg.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{
			RunID: id, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
			State: run.StateExited, ExitCode: countPtr(0), StartedAt: now, FinishedAt: now,
			ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit,
		}); err != nil {
			return err
		}
		return nil
	})
	return Candidate{
		ItemID:   d.ItemID,
		RunID:    id,
		LaneID:   d.LaneID,
		TickID:   d.PassID,
		Seat:     d.Seat,
		BaseSHA:  baseSHA,
		HeadSHA:  headSHA,
		Worktree: l.laneTarget(d.LaneID),
		Request:  d.RunRequest,
	}
}

type conflictWorkflowSteps struct {
	recordingSteps
	t    *testing.T
	f    refusalLoopFixture
	runs []run.Request
}

func (s *conflictWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if strings.HasSuffix(req.RunID, "-n2") {
		conflictTestCommit(s.t, target, "base.txt", "candidate\n", "candidate 1")
	}
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(path, []byte("RESULT: done\n"), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: countPtr(0),
		StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "RESULT: done\n", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", err
}

// TestSecondIdenticalConflictDiscardsCandidateResetsLaneAndRestartsWorkflowFromFirstStage
// verifies D-JL12 at the conflictFix / planner / LaneStager seam:
//   - on the second identical squash-conflict within the current retry cycle, conflictFix
//     discards the candidate (pins it, clears replayed dirty/repair holds, settles any open
//     landing call on it), resets the lane to the current trunk, and returns the item;
//   - the planner (ActionDispatch) starts a fresh attempt of the workflow from its first
//     stage (NOT an inline -cfix run copying the last stage's request and seat);
//   - LaneStager renders a brief carrying the item's source reference (body_ref or saved
//     request words), stating that the earlier attempt conflicted twice at landing, naming
//     the conflicting paths, and containing no conflict-fix instructions or markers.
func TestSecondIdenticalConflictDiscardsCandidateResetsLaneAndRestartsWorkflowFromFirstStage(t *testing.T) {
	for _, isRequest := range []bool{false, true} {
		name := "document-block"
		if isRequest {
			name = "filed-request"
		}
		t.Run(name, func(t *testing.T) {
			f, l := designSeatFixture(t)
			l.pollOnly = true // This fixture injects the landing conflict after the worker finishes.
			l.cfg.Lanes = []string{"lane-1"}
			l.cfg.Policy.Seats = map[string]string{"plan": "fixture", "build": "fixture", "design": "fixture"}
			l.cfg.Stage = &LaneStager{
				Root:     f.root,
				LaneRoot: f.laneRoot,
				Store:    f.storePath,
				Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
				Now:      l.cfg.Now,
			}
			// Two-stage workflow: plan -> build. The conflict occurs on stage 2 (build).
			wfPath := filepath.Join(f.root, "config/tillr/workflows/plan-then-build@v1.json")
			if err := os.MkdirAll(filepath.Dir(wfPath), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, wfPath,
				`{"name":"plan-then-build","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"build","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"plan","to_node":"build"}]}`)
			docPath := filepath.Join(f.root, "docs/work.md")
			if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, docPath, "# Work\n\nBody.\n")
			writeForkFile(t, filepath.Join(f.root, "base.txt"), "initial\n")
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "fixture workflow and base")
			itemID := "item-a"
			if isRequest {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
					return err
				})
				itemID = fileFixtureRequestWith(t, f, "Saved request words: update base.txt cleanly.", "plan", "plan-then-build@v1", []string{"base.txt"}, false)
			} else {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET workflow = 'plan-then-build@v1', allowed_paths = '["base.txt"]' WHERE item_id = 'item-a'`)
					return err
				})
			}

			// Stage 1 (plan) completes cleanly via graphPass; pass 2 settles plan and runs stage 2 (build).
			steps := &conflictWorkflowSteps{t: t, f: f}
			l.cfg.Steps = steps
			trunk0 := runGit(t, f.root, "rev-parse", TrunkRef)
			for i := 0; i < 4 && len(steps.runs) < 2; i++ {
				graphPass(t, l, &LoopResult{})
			}
			if len(steps.runs) < 2 || !strings.HasSuffix(steps.runs[1].RunID, "-n2") {
				t.Fatalf("expected stage 1 (build) to run, got runs=%+v", steps.runs)
			}
			w := pendingForTest(t, f, itemID)
			if w == nil || w.Decision.Seat != "build" || w.Decision.NodeIndex != 1 {
				t.Fatalf("expected pending work at stage 1 (build), got %+v", w)
			}
			pBuild := plannedApplication{decision: w.Decision}
			wt := l.laneTarget(pBuild.decision.LaneID)
			cand1Head := runGit(t, wt, "rev-parse", "HEAD")

			// Trunk moves so the squash conflicts on base.txt.
			trunk1 := conflictTestCommit(t, f.root, "base.txt", "trunk-1\n", "trunk move 1")

			// First conflict on stage 2 (build): records conflict (with markers), no repeat yet.
			c1 := conflictTestFinishRun(t, f, l, pBuild.decision, trunk0, cand1Head)
			landed := LandOutcome{Result: LandSquashConflict, ConflictPaths: []string{"CONFLICT (content): Merge conflict in base.txt"}}
			// Run conflictFix with trunk moved, or simulate first conflict on unmoved trunk then repeat.
			// Here trunk moved, so conflictFix stages a -cfix run on trunk1.
			_, _, err := l.conflictFix(context.Background(), c1, landed, &LoopResult{}, &PassTiming{})
			if err != nil && err != errRepairPending {
				t.Fatalf("first conflictFix: %v", err)
			}

			// Simulate the -cfix run failing with a conflict on the unmoved trunk1 (key has empty markers).
			// We mark the -cfix run terminal and record its conflict on unmoved trunk1.
			fixRunID := pBuild.decision.RunRequest.RunID + ConflictFixSuffix
			fixDec := pBuild.decision
			fixDec.RunRequest = l.cfg.Policy.Request(itemID, pBuild.decision.LaneID, fixRunID, "fixture")
			fixCandHead := conflictTestCommit(t, wt, "base.txt", "candidate-fix\n", "candidate fix")
			cFix := conflictTestFinishRun(t, f, l, fixDec, trunk1, fixCandHead)
			if _, _, err := l.conflictFix(context.Background(), cFix, landed, &LoopResult{}, &PassTiming{}); err != nil {
				t.Fatalf("second conflictFix (unmoved trunk): %v", err)
			}
			if err := l.markExamined(context.Background(), fixDec); err != nil {
				t.Fatal(err)
			}

			// Planner returns the item to build (ReasonReturned) on trunk1.
			pRet := mustPlan(t, l)[0]
			if pRet.decision.Action != ActionDispatch || pRet.decision.Reason != ReasonReturned {
				t.Fatalf("expected ReasonReturned after unmoved conflict, got %+v", pRet.decision)
			}
			if ok, _, err := l.applyPlan(context.Background(), pRet, trunk1, l.cfg.Now()); err != nil || !ok {
				t.Fatalf("apply returned: %v %v", ok, err)
			}
			cand3Head := conflictTestCommit(t, wt, "base.txt", "candidate-3\n", "candidate 3")
			c3 := conflictTestFinishRun(t, f, l, pRet.decision, trunk1, cand3Head)

			// Attach a replayed dirty hold, repair hold, and open landing call onto c3 to verify
			// conflictFix clears/settles all three when discarding c3 on the second identical conflict.
			const callID = "call-c3"
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := appendEffect(tx, dirtyHeldKind, c3.RunID, dirtyHold{Root: f.root, Candidate: c3}, l.cfg.Now()); err != nil {
					return err
				}
				if err := appendEffect(tx, repairHeldKind, c3.RunID, repairHold{Candidate: c3, TrunkSHA: trunk1, Cause: "repair"}, l.cfg.Now()); err != nil {
					return err
				}
				return appendLandingCallEvent(tx, callOffered, callID, landingCall{
					landingOffer: landingOffer{CallID: callID, Tool: "land", Commit: cand3Head, Base: trunk1},
					ItemID:       itemID, RunID: c3.RunID,
				}, l.cfg.Now())
			})

			// Third conflict (second identical conflict with cFix on unmoved trunk1):
			// must discard c3, reset lane to trunk1, and NOT raise a design/owner conflict.
			if _, _, err := l.conflictFix(context.Background(), c3, landed, &LoopResult{}, &PassTiming{}); err != nil {
				t.Fatalf("third conflictFix (identical repeat): %v", err)
			}
			if err := l.markExamined(context.Background(), pRet.decision); err != nil {
				t.Fatal(err)
			}

			// 1. Candidate c3 must be pinned.
			pinned := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+c3.RunID)
			if pinned != cand3Head {
				t.Fatalf("discarded candidate not pinned: got %q want %q", pinned, cand3Head)
			}

			// 2. Replayed dirty and repair holds on c3 must be cleared.
			if w := pendingForTest(t, f, itemID); w != nil && (w.DirtyHold != nil || w.RepairHold != nil) {
				t.Fatalf("discarded candidate still holds dirty/repair state: dirty=%+v repair=%+v", w.DirtyHold, w.RepairHold)
			}

			// 3. Open landing call on c3 must be settled.
			fixtureRead(t, f, func(r *store.Reader) error {
				var settled int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind IN (?,?) AND object_id = ?`, callAnswered, callClosed, callID).Scan(&settled); err != nil {
					return err
				}
				if settled == 0 {
					t.Fatalf("open landing call %q was not settled when candidate was discarded", callID)
				}
				return nil
			})

			// 4. Lane worktree must be reset to the current trunk.
			if got := runGit(t, wt, "rev-parse", "HEAD"); got != trunk1 {
				t.Fatalf("lane worktree not reset to trunk %s: got %s", trunk1, got)
			}

			// 5. No conflict should be parked for design or owner yet.
			fixtureRead(t, f, func(r *store.Reader) error {
				cs, err := item.ReadConflicts(r)
				if err != nil {
					return err
				}
				if c := cs[itemID]; c.ConflictID != "" && c.Status != "closed" && c.Status != "answered" {
					t.Fatalf("second identical conflict parked instead of restarting: %+v", c)
				}
				return nil
			})

			// 6. Planner must dispatch a fresh attempt of the workflow from its FIRST stage (plan),
			//    NOT an inline -cfix run copying the last stage's request and seat.
			restarted := mustPlan(t, l)[0]
			d := restarted.decision
			if d.Action != ActionDispatch || d.RunRequest == nil {
				t.Fatalf("expected ActionDispatch for fresh restart, got %+v", d)
			}
			if strings.HasSuffix(d.RunRequest.RunID, ConflictFixSuffix) {
				t.Fatalf("restarted run must not be a -cfix run: %q", d.RunRequest.RunID)
			}
			if d.NodeIndex != 0 || d.Seat != "plan" {
				t.Fatalf("restarted run did not start from first stage (plan, node 0): seat=%q node=%d", d.Seat, d.NodeIndex)
			}

			// 7. Apply & inspect the rendered brief: must carry source reference, state the
			//    earlier attempt conflicted twice at landing, name the conflicting paths,
			//    and have no conflict-fix instructions/Settled decisions/markers.
			if ok, _, err := l.applyPlan(context.Background(), restarted, trunk1, l.cfg.Now()); err != nil || !ok {
				t.Fatalf("apply restarted: %v %v", ok, err)
			}
			// applyPlan only records the transition; stageDispatches renders the brief.
			if _, err := l.stageDispatches(context.Background(), []Decision{d}, trunk1, &LoopResult{}); err != nil {
				t.Fatalf("stage restarted: %v", err)
			}
			briefBytes, err := os.ReadFile(filepath.Join(f.laneRoot, d.LaneID, BriefFile))
			if err != nil {
				t.Fatalf("read brief: %v", err)
			}
			brief := string(briefBytes)
			if isRequest {
				if !strings.Contains(brief, "Saved request words: update base.txt cleanly.") {
					t.Fatalf("restarted brief missing saved request words:\n%s", brief)
				}
			} else {
				if !strings.Contains(brief, "docs/work.md#work") {
					t.Fatalf("restarted brief missing body_ref:\n%s", brief)
				}
			}
			if !strings.Contains(strings.ToLower(brief), "conflicted twice") {
				t.Fatalf("restarted brief does not state earlier attempt conflicted twice:\n%s", brief)
			}
			if !strings.Contains(brief, "base.txt") {
				t.Fatalf("restarted brief does not name conflicting path base.txt:\n%s", brief)
			}
			for _, forbidden := range []string{
				"This is the one conflict-fix run",
				"This is a conflict-fix run",
				"conflict-markers.txt",
				"<<<<<<<",
				"Re-apply",
			} {
				if strings.Contains(brief, forbidden) {
					t.Fatalf("restarted brief contains forbidden conflict-fix text %q:\n%s", forbidden, brief)
				}
			}
		})
	}
}

// TestFreshAttemptConflictParksForOwnerRegardlessOfBlockDesignSeatOrHunks verifies D-JL12:
// once a conflict restart has occurred in the current retry cycle, a conflict on the fresh
// attempt parks for the operator (WaitingOn: "owner", conflict-owner) with no further
// dispatch, regardless of whether the item has a document block or design seat, and
// regardless of whether the conflicting paths or hunks changed.
func TestFreshAttemptConflictParksForOwnerRegardlessOfBlockDesignSeatOrHunks(t *testing.T) {
	for _, diffHunks := range []bool{false, true} {
		name := "same-hunks"
		if diffHunks {
			name = "differing-hunks"
		}
		t.Run(name, func(t *testing.T) {
			f, l := designSeatFixture(t)
			l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
			writeForkFile(t, filepath.Join(f.root, "base.txt"), "initial\n")
			writeForkFile(t, filepath.Join(f.root, "other.txt"), "initial\n")
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "base files")

			// Two conflicts on unmoved trunk to trigger the conflict restart.
			trunk0 := runGit(t, f.root, "rev-parse", TrunkRef)
			landedBase := LandOutcome{Result: LandSquashConflict, ConflictPaths: []string{"CONFLICT (content): Merge conflict in base.txt"}}

			p1 := mustPlan(t, l)[0]
			if ok, _, err := l.applyPlan(context.Background(), p1, trunk0, l.cfg.Now()); err != nil || !ok {
				t.Fatalf("apply 1: %v %v", ok, err)
			}
			wt := l.laneTarget(p1.decision.LaneID)
			h1 := conflictTestCommit(t, wt, "base.txt", "cand-1\n", "cand 1")
			c1 := conflictTestFinishRun(t, f, l, p1.decision, trunk0, h1)
			if _, _, err := l.conflictFix(context.Background(), c1, landedBase, &LoopResult{}, &PassTiming{}); err != nil {
				t.Fatalf("conflict 1: %v", err)
			}
			if err := l.markExamined(context.Background(), p1.decision); err != nil {
				t.Fatal(err)
			}

			p2 := mustPlan(t, l)[0]
			if ok, _, err := l.applyPlan(context.Background(), p2, trunk0, l.cfg.Now()); err != nil || !ok {
				t.Fatalf("apply 2: %v %v", ok, err)
			}
			h2 := conflictTestCommit(t, wt, "base.txt", "cand-2\n", "cand 2")
			c2 := conflictTestFinishRun(t, f, l, p2.decision, trunk0, h2)
			if _, _, err := l.conflictFix(context.Background(), c2, landedBase, &LoopResult{}, &PassTiming{}); err != nil {
				t.Fatalf("conflict 2: %v", err)
			}
			if err := l.markExamined(context.Background(), p2.decision); err != nil {
				t.Fatal(err)
			}

			// Planner dispatches fresh attempt (p3).
			p3 := mustPlan(t, l)[0]
			if p3.decision.Action != ActionDispatch {
				t.Fatalf("expected fresh restart dispatch, got %+v", p3.decision)
			}
			if ok, _, err := l.applyPlan(context.Background(), p3, trunk0, l.cfg.Now()); err != nil || !ok {
				t.Fatalf("apply 3: %v %v", ok, err)
			}

			// Fresh attempt conflicts again (either same path/hunks, or different path/hunks on a moved trunk).
			landedFresh := landedBase
			if diffHunks {
				trunk0 = conflictTestCommit(t, f.root, "other.txt", "trunk-other\n", "trunk other")
				landedFresh = LandOutcome{Result: LandSquashConflict, ConflictPaths: []string{"CONFLICT (content): Merge conflict in other.txt"}}
			}
			h3 := conflictTestCommit(t, wt, "other.txt", "cand-other\n", "cand 3")
			c3 := conflictTestFinishRun(t, f, l, p3.decision, trunk0, h3)
			if _, _, err := l.conflictFix(context.Background(), c3, landedFresh, &LoopResult{}, &PassTiming{}); err != nil && err != errRepairPending {
				t.Fatalf("conflict 3: %v", err)
			}
			if err := l.markExamined(context.Background(), p3.decision); err != nil {
				t.Fatal(err)
			}

			// Must park for owner (conflict-owner) even though body_ref and design seat are present.
			c := itemConflict(t, f)
			if c.WaitingOn != "owner" || c.Status != "owner" {
				t.Fatalf("expected owner park after fresh attempt conflict, got %+v", c)
			}
			next := mustPlan(t, l)[0].decision
			if next.Action == ActionDispatch || next.Reason != ReasonConflictOwner {
				t.Fatalf("expected conflict-owner hold with no further dispatch, got %+v", next)
			}
		})
	}
}

// TestConflictsBeforeRetryDoNotCountTowardRestart verifies that `tillr drain retry`
// starts a fresh retry cycle: conflicts (and any conflict restart) before the retry do not
// count toward the restart or owner-park checks after the retry.
func TestConflictsBeforeRetryDoNotCountTowardRestart(t *testing.T) {
	f, l := designSeatFixture(t)
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "initial\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "base file")
	trunk0 := runGit(t, f.root, "rev-parse", TrunkRef)
	landed := LandOutcome{Result: LandSquashConflict, ConflictPaths: []string{"CONFLICT (content): Merge conflict in base.txt"}}

	// Cycle 1: two identical conflicts -> restart -> third conflict -> owner park.
	for i := 0; i < 3; i++ {
		p := mustPlan(t, l)[0]
		if p.decision.Action != ActionDispatch {
			t.Fatalf("cycle 1 step %d: expected dispatch, got %+v", i+1, p.decision)
		}
		if ok, _, err := l.applyPlan(context.Background(), p, trunk0, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("apply %d: %v %v", i+1, ok, err)
		}
		wt := l.laneTarget(p.decision.LaneID)
		h := conflictTestCommit(t, wt, "base.txt", fmt.Sprintf("cand-%d\n", i+1), fmt.Sprintf("cand %d", i+1))
		c := conflictTestFinishRun(t, f, l, p.decision, trunk0, h)
		if _, _, err := l.conflictFix(context.Background(), c, landed, &LoopResult{}, &PassTiming{}); err != nil {
			t.Fatalf("conflict %d: %v", i+1, err)
		}
		if err := l.markExamined(context.Background(), p.decision); err != nil {
			t.Fatal(err)
		}
	}
	held := mustPlan(t, l)[0].decision
	if held.Action != ActionHold || held.Reason != ReasonConflictOwner {
		t.Fatalf("expected conflict-owner hold at end of cycle 1, got %+v", held)
	}
	latestRun := itemConflict(t, f).RunID

	// Operator runs `tillr drain retry` to start cycle 2.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			ItemID: "item-a",
			RunID:  latestRun,
			By:     "operator",
			Why:    "Try a different approach for base.txt",
			At:     time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
		})
		return err
	})

	pRetry := mustPlan(t, l)[0]
	if pRetry.decision.Action != ActionRetry {
		t.Fatalf("expected ActionRetry, got %+v", pRetry.decision)
	}
	if ok, _, err := l.applyPlan(context.Background(), pRetry, trunk0, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("apply retry: %v %v", ok, err)
	}
	wt := l.laneTarget(pRetry.decision.LaneID)
	hRetry := conflictTestCommit(t, wt, "base.txt", "cand-retry\n", "cand retry")
	cRetry := conflictTestFinishRun(t, f, l, pRetry.decision, trunk0, hRetry)
	if _, _, err := l.conflictFix(context.Background(), cRetry, landed, &LoopResult{}, &PassTiming{}); err != nil {
		t.Fatalf("first conflict after retry: %v", err)
	}
	if err := l.markExamined(context.Background(), pRetry.decision); err != nil {
		t.Fatal(err)
	}

	// First conflict after retry must NOT immediately park or restart: it must return to
	// the worker (ReasonReturned) because conflicts before `tillr drain retry` do not count.
	afterFirst := mustPlan(t, l)[0].decision
	if afterFirst.Action != ActionDispatch || afterFirst.Reason != ReasonReturned {
		t.Fatalf("first conflict after retry should return to worker, got %+v", afterFirst)
	}
	// Verify JSON marshaling of conflict if any exists is not parked.
	if c := itemConflict(t, f); c.ConflictID != "" && c.Status == "owner" {
		raw, _ := json.Marshal(c)
		t.Fatalf("item parked on first conflict after retry: %s", raw)
	}
}
