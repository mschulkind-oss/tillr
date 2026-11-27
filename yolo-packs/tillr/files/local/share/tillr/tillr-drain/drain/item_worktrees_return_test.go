package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestItemWorktreesVerifierRefusalReturnsToItemDirectory(t *testing.T) {
	l, steps, f := verifyReturnFixture(t, false, func(base *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: base, verdict: verifyFinding}
	})
	l.cfg.ItemWorktrees = true
	runVerifyReturn(t, f, l, steps, func() bool { return steps.lands == 1 })

	if steps.verifies != 2 || len(steps.repairs) != 1 {
		t.Fatalf("verifies=%d repairs=%d, want 2 verifies and 1 repair", steps.verifies, len(steps.repairs))
	}
	for _, req := range steps.runs {
		if req.LaneID != "item-a" {
			t.Fatalf("run %s (node %s) used lane %q, want item-a", req.RunID, req.NodeID, req.LaneID)
		}
	}
	if steps.repairs[0].LaneID != "item-a" {
		t.Fatalf("repair run used lane %q, want item-a", steps.repairs[0].LaneID)
	}
	if steps.repairHead[0] != steps.verified {
		t.Fatalf("repair started at %s, want verified head %s", steps.repairHead[0], steps.verified)
	}
	// The directory the verifier ran in is neither stranded nor held twice.
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		var runID *string
		if err := r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id = 'item-a'`).Scan(&state, &runID); err != nil {
			t.Fatalf("read item-a lane row: %v", err)
		}
		if state != run.LaneStateIdle || runID != nil {
			t.Fatalf("item-a lane row = (%q, %v), want idle with no run_id", state, runID)
		}
		return nil
	})
}

func TestItemWorktreesRevertedLandingReturnsToItemDirectory(t *testing.T) {
	ctx := context.Background()
	f, l := designSeatFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.ItemWorktrees = true
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id='item-a'`)
		return err
	})

	first := mustPlan(t, l)[0]
	if first.decision.LaneID != "item-a" {
		t.Fatalf("first dispatch lane = %q, want item-a", first.decision.LaneID)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	lane := first.decision.LaneID
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}
	if err := stager.Stage(ctx, StageSpec{
		ItemID: "item-a", LaneID: lane, RunID: first.decision.RunRequest.RunID,
		Backend: "fixture", WorkingDir: WorktreeDir, BaseSHA: base,
	}); err != nil {
		t.Fatalf("stage item-a: %v", err)
	}
	dir := l.laneDirPath(lane)
	session := "11111111-1111-4111-8111-111111111111"
	writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	target := l.laneTarget(lane)
	writeForkFile(t, filepath.Join(target, "candidate.txt"), "broken\n")
	runGit(t, target, "add", "candidate.txt")
	runGit(t, target, "commit", "-qm", "candidate")
	head := runGit(t, target, "rev-parse", "HEAD")

	ok, _, err := l.applyPlan(ctx, first, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan: %v %v", ok, err)
	}
	id := first.decision.RunRequest.RunID
	if err := os.MkdirAll(RunDir(f.root, id), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(RunDir(f.root, id), SessionIDFile), session+"\n")
	writeForkFile(t, filepath.Join(RunDir(f.root, id), BaseSHAFile), base+"\n")
	stamp := l.cfg.Now().Format(time.RFC3339)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID: id, ItemID: "item-a", LaneID: lane, Backend: "fixture",
			State: run.StateExited, ExitCode: countPtr(0), StartedAt: stamp, FinishedAt: stamp, EndCause: run.EndNormalExit,
		})
	})

	candidate := Candidate{ItemID: "item-a", LaneID: lane, RunID: id, Worktree: target, BaseSHA: base, HeadSHA: head}
	candidate.WorkerSession, err = l.captureLandedWorkerSession(ctx, candidate)
	if err != nil || candidate.WorkerSession == nil {
		t.Fatalf("captureLandedWorkerSession: %+v %v", candidate.WorkerSession, err)
	}
	if err := appendJudgment(ctx, f.storePath, candidateGatedKind, "run", id, candidate, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "cherry-pick", head)
	landing := runGit(t, f.root, "rev-parse", TrunkRef)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := recordFixtureLanding(tx, "item-a", id, landing, 100); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='item-a'`, StateLanded)
		return err
	})
	if err := l.markExamined(ctx, first.decision); err != nil {
		t.Fatal(err)
	}
	if stop := l.abandon(ctx, lane, "", ""); stop != "" {
		t.Fatal(stop)
	}

	steps := &revertSteps{root: f.root, store: f.storePath, mu: &sync.Mutex{}, gated: &[]Candidate{}, returned: &[]string{}}
	l.cfg.Steps, l.cfg.Report = steps, &bytes.Buffer{}
	out, err := l.revertLanding(ctx, landing, landing, []HeavyCheck{{CheckID: "unit", Result: "fail", Detail: "unique failing output"}}, func(context.Context, string, string) (bool, error) { return false, nil })
	if err != nil || out.Reverted == "" {
		t.Fatalf("revertLanding: %+v %v", out, err)
	}

	next := mustPlan(t, l)
	if len(next) != 1 {
		t.Fatalf("next plan len = %d, want 1", len(next))
	}
	d := next[0].decision
	if d.Action != ActionDispatch || d.Reason != ReasonReturned || d.LaneID != "item-a" {
		t.Fatalf("reverted plan = (%s, %s, %s), want dispatch/returned-with-evidence on item-a", d.Action, d.Reason, d.LaneID)
	}
	if d.Returned == nil || d.Returned.Cause != RefusalReverted {
		t.Fatalf("reverted decision Returned = %+v, want RefusalReverted", d.Returned)
	}
	if d.RunRequest == nil || d.RunRequest.LaneID != "item-a" {
		t.Fatalf("reverted RunRequest = %+v, want LaneID item-a", d.RunRequest)
	}
}

func TestItemWorktreesHeldAndGateStalledCandidatesKeepDirectoryPinsAndFreeAgentLimit(t *testing.T) {
	for _, mode := range []string{"gate-stalled", "held-fault"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a", "item-b")
			l.cfg.Lanes = []string{"lane-1"}
			l.cfg.ItemWorktrees = true
			l.cfg.Stage = &LaneStager{
				Root:     f.root,
				LaneRoot: f.laneRoot,
				Store:    f.storePath,
				Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
				Now:      l.cfg.Now,
			}

			base := runGit(t, f.root, "rev-parse", TrunkRef)
			p := mustPlan(t, l)[0]
			d := p.decision
			if d.ItemID != "item-a" || d.LaneID != "item-a" {
				t.Fatalf("initial plan = (%s on %s), want item-a on item-a", d.ItemID, d.LaneID)
			}
			if _, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{}); err != nil {
				t.Fatalf("stageDispatches: %v", err)
			}
			target := l.laneTarget("item-a")
			writeForkFile(t, filepath.Join(target, "candidate.txt"), mode+"\n")
			runGit(t, target, "add", "candidate.txt")
			runGit(t, target, "commit", "-qm", "candidate "+mode)
			head := runGit(t, target, "rev-parse", "HEAD")

			ok, work, err := l.applyPlan(ctx, p, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("applyPlan: %v %v", ok, err)
			}
			runID := d.RunRequest.RunID
			stamp := l.cfg.Now().Format(time.RFC3339)
			c := Candidate{
				ItemID: "item-a", LaneID: "item-a", RunID: runID,
				Worktree: target, BaseSHA: base, HeadSHA: head,
				Request: d.RunRequest, Seat: d.Seat,
			}
			owner, err := currentLoopOwner()
			if err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := applyTerminal(tx, run.Record{
					RunID: runID, ItemID: "item-a", LaneID: "item-a", Backend: d.RunRequest.Backend,
					State: run.StateExited, ExitCode: countPtr(0), StartedAt: stamp, FinishedAt: stamp, EndCause: run.EndNormalExit,
				}); err != nil {
					return err
				}
				if err := appendEffect(tx, candidateReadyKind, runID, c, l.cfg.Now()); err != nil {
					return err
				}
				if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id='item-a'`, run.LaneStateLeased, runID); err != nil {
					return err
				}
				return appendEffect(tx, examinationOwnerKind, runID, owner, l.cfg.Now())
			})
			work.Candidate = &c
			work.Examiner = owner

			switch mode {
			case "gate-stalled":
				if err := l.holdGateStall(ctx, c, GateOutcome{
					StalledChecks: []StallObservation{{CheckID: "unit", DurationMS: 5000}},
				}, 2); err != errGateStallHeld {
					t.Fatalf("holdGateStall err = %v, want errGateStallHeld", err)
				}
			case "held-fault":
				if err := l.holdItemFault(ctx, c, ReasonGateNoVerdict, "held candidate"); err != nil {
					t.Fatalf("holdItemFault: %v", err)
				}
			}

			// Releasing the held candidate under ItemWorktrees must keep its directory and pins.
			released, err := l.releaseCandidate(ctx, work, "", true)
			if err != nil || released {
				t.Fatalf("releaseCandidate = (%v, %v), want (false, nil)", released, err)
			}
			if err := l.releaseExamination(ctx, d); err != nil {
				t.Fatalf("releaseExamination: %v", err)
			}
			// Idle reconciliation must also keep the directory and pins intact.
			if err := l.releaseIdleHeldCandidates(ctx); err != nil {
				t.Fatalf("releaseIdleHeldCandidates: %v", err)
			}

			if gotHead := runGit(t, target, "rev-parse", "HEAD"); gotHead != head {
				t.Fatalf("item-a worktree HEAD = %s, want kept candidate %s", gotHead, head)
			}
			if gotPin := runGit(t, f.root, "rev-parse", pinRef(runID)); gotPin != head {
				t.Fatalf("run pin = %s, want %s", gotPin, head)
			}
			if gotItemPin := runGit(t, f.root, "rev-parse", itemPinRef("item-a")); gotItemPin != head {
				t.Fatalf("item pin = %s, want %s", gotItemPin, head)
			}

			// Verify item-a does not count against the running-agent limit (1), so item-b dispatches.
			lanes := []string{"lane-1"}
			gatesInFlight := 0
			if mode == "gate-stalled" {
				gatesInFlight = 2
			}
			got := planDecisionByItem(t, f, Pass{
				Now:            l.cfg.Now().Format(time.RFC3339Nano),
				AvailableLanes: &lanes,
				ItemWorktrees:  true,
				GatesInFlight:  gatesInFlight,
				Build:          &l.build,
			}, l.cfg.Policy)
			if got["item-a"].Action != ActionHold {
				t.Fatalf("item-a decision = %+v, want ActionHold while held", got["item-a"])
			}
			if got["item-b"].Action != ActionDispatch || got["item-b"].LaneID != "item-b" {
				t.Fatalf("item-b decision = %+v, want dispatch on item-b (held item-a must not consume agent slot)", got["item-b"])
			}
		})
	}
}

func TestItemWorktreesReleasedCandidateStartsBeforeFastStartCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.ItemWorktrees = true
	at := l.cfg.Now().UTC()
	req := l.cfg.Policy.Request("item-a", "lane-4", "run-item-a-a2-n1", "fixture")
	startDecision := Decision{
		ItemID:     "item-a",
		LaneID:     "lane-4",
		Action:     ActionRetry,
		Reason:     "operator-retry",
		From:       "queued",
		To:         "running",
		Attempt:    2,
		Seat:       "build",
		RunRequest: req,
		PassID:     "gen-11750",
	}
	identifyDecision(&startDecision)
	cand := Candidate{
		ItemID:  "item-a",
		LaneID:  "lane-4",
		RunID:   req.RunID,
		BaseSHA: "base-sha",
		HeadSHA: "head-sha",
		Request: req,
		Seat:    "build",
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET rank='00m' WHERE item_id='item-a'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET rank='02m' WHERE item_id='item-b'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES ('lane-1', 'idle') ON CONFLICT(lane_id) DO UPDATE SET state='idle', run_id=NULL, leased_until=NULL`); err != nil {
			return err
		}
		if err := appendEffect(tx, dispatchStartedKind, req.RunID, pendingWork{Decision: startDecision, BaseSHA: "base-sha"}, at.Add(-time.Hour)); err != nil {
			return err
		}
		exit := 0
		stamp := at.Add(-50 * time.Minute).Format(time.RFC3339Nano)
		if err := applyTerminal(tx, run.Record{
			RunID: req.RunID, ItemID: "item-a", LaneID: "lane-4", Backend: req.Backend,
			State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: []run.ArtifactRef{},
		}); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateReadyKind, req.RunID, cand, at.Add(-45*time.Minute)); err != nil {
			return err
		}
		hold := gateStallHold{Candidate: cand, HeadTree: "tree-sha", GatesInFlight: 4}
		if err := appendEffect(tx, gateStallHeldKind, req.RunID, hold, at.Add(-40*time.Minute)); err != nil {
			return err
		}
		if err := appendEffect(tx, candidateLaneKind, req.RunID, candidateLane{Candidate: cand, Released: true}, at.Add(-39*time.Minute)); err != nil {
			return err
		}
		return appendEffect(tx, examinationReleasedKind, req.RunID, struct{}{}, at.Add(-38*time.Minute))
	})

	// Confirm the planner itself dispatches place-1 item-a onto item-a.
	st, err := store.OpenRead(t.Context(), f.storePath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	available := []string{"lane-1"}
	var got map[string]Decision
	if err := st.ReadTx(t.Context(), func(r *store.Reader) error {
		envs, err := Plan(r, Pass{
			Now:            at.Format(time.RFC3339Nano),
			AvailableLanes: &available,
			ItemWorktrees:  true,
			GatesInFlight:  1,
			Build:          &l.build,
		}, l.cfg.Policy, &VantageQuestions{Root: f.root, Context: t.Context()})
		if err != nil {
			return err
		}
		got = map[string]Decision{}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			got[d.ItemID] = d
		}
		return nil
	}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if d := got["item-a"]; d.Action != ActionJudge || d.LaneID != "item-a" {
		t.Fatalf("Plan item-a = (%s, %s, %s), want judge on item-a", d.Action, d.Reason, d.LaneID)
	}
}

func TestItemWorktreesPoolLaneWorkMovesToItemDirectoryAndReleasesPoolLane(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}

	// First run happened on pool lane lane-1 before ItemWorktrees was turned on.
	d1 := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
	poolTarget, base := l.laneTarget("lane-1"), l.laneBase("lane-1")
	head := runGit(t, poolTarget, "rev-parse", "HEAD")
	if err := l.pinLane(ctx, "lane-1", "item-a", d1.RunRequest.RunID, head); err != nil {
		t.Fatalf("pinLane: %v", err)
	}
	c := Candidate{
		ItemID: "item-a", RunID: d1.RunRequest.RunID, LaneID: "lane-1",
		Worktree: poolTarget, BaseSHA: base, HeadSHA: head, EvidenceKey: "judge-finding",
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	if err := l.holdJudge(ctx, c, "judge-run", ReasonEvalFailed, "Criterion c1: missing evidence", &LoopResult{}); err != nil {
		t.Fatalf("holdJudge: %v", err)
	}
	if err := l.markExamined(ctx, d1); err != nil {
		t.Fatalf("markExamined: %v", err)
	}
	tip := conflictTestCommit(t, f.root, "trunk-only.txt", "later trunk\n", "advance trunk")

	// Turn ItemWorktrees on: the next dispatch must move from lane-1 to item-a,
	// stage item-a from its pinned head, and release/reset pool lane lane-1.
	l.cfg.ItemWorktrees = true
	next := mustPlan(t, l)
	if len(next) != 1 {
		t.Fatalf("next plan len = %d, want 1", len(next))
	}
	d2 := next[0].decision
	if d2.Action != ActionDispatch || d2.Reason != ReasonReturned || d2.LaneID != "item-a" {
		t.Fatalf("migrated decision = (%s, %s, %s), want dispatch/returned-with-evidence on item-a", d2.Action, d2.Reason, d2.LaneID)
	}
	if d2.RetryCandidate == nil || !d2.RetryCandidate.Available || d2.RetryCandidate.HeadSHA != head {
		t.Fatalf("migrated RetryCandidate = %+v, want available head %s", d2.RetryCandidate, head)
	}

	ok, work, err := l.applyPlan(ctx, next[0], tip, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan: %v %v", ok, err)
	}
	staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, tip, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stageDispatches: %+v %v", staged, err)
	}
	itemTarget := l.laneTarget("item-a")
	if gotHead := runGit(t, itemTarget, "rev-parse", "HEAD"); gotHead != head {
		t.Fatalf("item-a staged head = %s, want pinned head %s", gotHead, head)
	}
	if gotBase := l.settleBase("item-a", tip); gotBase != base {
		t.Fatalf("item-a staged base = %s, want %s", gotBase, base)
	}
	// Pool lane lane-1 must be released and reset to trunk.
	if gotPoolHead := runGit(t, poolTarget, "rev-parse", "HEAD"); gotPoolHead != tip {
		t.Fatalf("pool lane-1 head = %s, want reset to trunk %s", gotPoolHead, tip)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		var runID *string
		if err := r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id = 'lane-1'`).Scan(&state, &runID); err != nil {
			t.Fatalf("read lane-1 row: %v", err)
		}
		if state != run.LaneStateIdle || runID != nil {
			t.Fatalf("pool lane-1 row = (%q, %v), want idle with no run_id", state, runID)
		}
		return nil
	})
	if gotDir := filepath.Base(filepath.Dir(l.lanePath("item-a"))); gotDir != "item-a" {
		t.Fatalf("item-a directory = %q, want item-a", gotDir)
	}
}
