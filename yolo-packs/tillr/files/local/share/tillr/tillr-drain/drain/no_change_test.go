package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type singleWorkerNoChangeSteps struct {
	t           *testing.T
	storePath   string
	laneRoot    string
	callSuspend bool
	dirty       bool
	report      string
	brief       string
	runs        int
	cancelAfter int
	cancel      context.CancelFunc
	afterRun    func()
}

func (s *singleWorkerNoChangeSteps) Sync(context.Context) (SyncOutcome, error) {
	return SyncOutcome{}, nil
}
func (s *singleWorkerNoChangeSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.t.Fatal("gate must not run on no_change")
	return GateOutcome{}, nil
}
func (s *singleWorkerNoChangeSteps) Land(context.Context, Candidate) (LandOutcome, error) {
	s.t.Fatal("land must not run on no_change")
	return LandOutcome{}, nil
}
func (s *singleWorkerNoChangeSteps) Verdict(context.Context, Candidate, string) error {
	s.t.Fatal("verdict must not run on no_change")
	return nil
}
func (s *singleWorkerNoChangeSteps) ItemState(context.Context, string, string, string, ...item.WaitingOn) error {
	return nil
}

func (s *singleWorkerNoChangeSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	s.runs++
	laneDir := filepath.Join(s.laneRoot, req.LaneID)
	target := filepath.Join(laneDir, WorktreeDir)
	rawBrief, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.brief = string(rawBrief)
	rawBase, err := os.ReadFile(filepath.Join(laneDir, BaseSHAFile))
	if err != nil {
		return run.Record{}, "", err
	}
	base := strings.TrimSpace(string(rawBase))
	if s.dirty {
		writeForkFile(s.t, filepath.Join(target, "untracked.txt"), "dirty\n")
	}
	if s.callSuspend {
		offer := landingOffer{
			CallID: "call-nc-1", Tool: "no_change", Base: base,
			Finding: &landingOfferFinding{Cause: "caused by sibling candidate", Evidence: "ran targeted test on base, passed", WhyNoChange: "trunk needs no change"},
		}
		if err := writeLandingArtifact(run.DurableDir(laneDir, req.RunID), landingOfferFile, offer); err != nil {
			return run.Record{}, "", err
		}
	}
	var refs []run.ArtifactRef
	if !s.callSuspend && s.report != "" {
		reportPath := filepath.Join(run.DurableDir(laneDir, req.RunID), "report.md")
		writeForkFile(s.t, reportPath, s.report)
		refs = append(refs, run.ArtifactRef{Kind: "report", Ref: reportPath})
	}
	code := 0
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: &code,
		StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: refs,
	}
	st, err := store.Open(context.Background(), s.storePath, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer st.Close()
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		return run.Record{}, "", err
	}
	if s.cancelAfter > 0 && s.runs > s.cancelAfter && s.cancel != nil {
		s.cancel()
		return run.Record{}, "", context.Canceled
	}
	if s.afterRun != nil {
		s.afterRun()
	}
	if s.callSuspend {
		return rec, "", nil
	}
	return rec, s.report, nil
}

func setupSingleWorkerNoChange(t *testing.T, ctx context.Context, steps *singleWorkerNoChangeSteps, capability string) (refusalLoopFixture, *Loop) {
	t.Helper()
	f := newRefusalLoopFixture(t, "item-a")
	db, err := store.Open(ctx, f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='single-worker@v1', judge_mode='' WHERE item_id='item-a'`)
		return err
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	steps.t, steps.storePath, steps.laneRoot = t, f.storePath, f.laneRoot
	backends := filepath.Join("..", "..", "tillr-run", "backends")
	stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join(backends, "brief-template.md")}
	l := f.loop(t, steps, stager)
	if capability != "" {
		l.endingCapabilities = map[string]string{"fixture": capability}
	}
	return f, l
}

func TestSingleWorkerNoChangeClosesWithFindingAndNoHold(t *testing.T) {
	for _, tc := range []struct {
		name        string
		callSuspend bool
		capability  string
	}{
		{name: "footer", callSuspend: false, capability: run.EndingFooter},
		{name: "call-suspends", callSuspend: true, capability: run.EndingCallSuspends},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			steps := &singleWorkerNoChangeSteps{
				callSuspend: tc.callSuspend,
				report:      "CAUSE: caused by sibling candidate\nEVIDENCE: ran targeted test on base, passed\nWHY_NO_CHANGE: trunk needs no change\nRESULT: no_change\n",
			}
			f, l := setupSingleWorkerNoChange(t, ctx, steps, tc.capability)
			trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)
			if _, err := l.Run(ctx); err != nil {
				t.Fatalf("loop run: %v", err)
			}
			if steps.runs != 1 {
				t.Fatalf("runs = %d, want 1", steps.runs)
			}
			if !strings.Contains(steps.brief, noChangeMarkerLine) {
				t.Fatalf("brief missing no_change marker:\n%s", steps.brief)
			}
			if trunkAfter := runGit(t, f.root, "rev-parse", TrunkRef); trunkAfter != trunkBefore {
				t.Fatalf("trunk moved from %s to %s", trunkBefore, trunkAfter)
			}
			db, err := store.OpenRead(ctx, f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var shown struct {
				State       string        `json:"state"`
				CloseReason string        `json:"close_reason"`
				Finding     *item.Finding `json:"finding"`
			}
			var holds, closedCalls int
			if err := db.ReadTx(ctx, func(r *store.Reader) error {
				env, err := item.Show(r, "item-a", "show-1")
				if err != nil {
					return err
				}
				if err := json.Unmarshal(env.Payload, &shown); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, obstacles.HeldKind).Scan(&holds); err != nil {
					return err
				}
				return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.reason')='closed-no-change'`, callClosed).Scan(&closedCalls)
			}); err != nil {
				t.Fatal(err)
			}
			if shown.State != "closed" || shown.CloseReason != "no_change: trunk needs no change" {
				t.Fatalf("unexpected closed item: %+v", shown)
			}
			if shown.Finding == nil || shown.Finding.Cause != "caused by sibling candidate" || shown.Finding.Evidence != "ran targeted test on base, passed" || shown.Finding.WhyNoChange != "trunk needs no change" || shown.Finding.EvaluatedHead != trunkBefore || shown.Finding.WorkerRun == "" {
				t.Fatalf("unexpected finding: %+v", shown.Finding)
			}
			if holds != 0 {
				t.Fatalf("obstacle holds = %d, want 0", holds)
			}
			if tc.callSuspend && closedCalls != 1 {
				t.Fatalf("closed-no-change calls = %d, want 1", closedCalls)
			}
		})
	}
}

func TestSingleWorkerNoChangeRefusesDirtyOrIncompleteAndLeavesPlannedBuildUnchanged(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, report, step string
		dirty              bool
	}{
		{name: "dirty-worktree", dirty: true, report: "CAUSE: c\nEVIDENCE: e\nWHY_NO_CHANGE: w\nRESULT: no_change\n", step: RefusalFence},
		{name: "missing-fields", dirty: false, report: "CAUSE: c\nRESULT: no_change\n", step: RefusalNoOp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			steps := &singleWorkerNoChangeSteps{dirty: tc.dirty, report: tc.report, cancelAfter: 1, cancel: cancel}
			f, l := setupSingleWorkerNoChange(t, ctx, steps, "")
			_, _ = l.Run(runCtx)
			db, err := store.OpenRead(ctx, f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var state string
			var refused int
			if err := db.ReadTx(ctx, func(r *store.Reader) error {
				if err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
					return err
				}
				return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='drain.refusal' AND json_extract(payload,'$.step')=?`, tc.step).Scan(&refused)
			}); err != nil {
				t.Fatal(err)
			}
			if state != "in-queue" || refused == 0 {
				t.Fatalf("state=%q refused=%d, want in-queue and >0 for %q", state, refused, tc.step)
			}
		})
	}

	pb := &Workflow{Name: "planned-build", Version: 1}
	planStage := &WorkflowStage{ID: "plan", Kind: "agent", Seat: "plan", Access: "read_only"}
	workStage := &WorkflowStage{ID: "work", Kind: "agent", Seat: "build", Access: "write"}
	if allowsSingleWorkerNoChange(pb, planStage, "planned-build@v1", "item-a", false, nil, nil, "") ||
		allowsSingleWorkerNoChange(pb, workStage, "planned-build@v1", "item-a", false, nil, nil, "") {
		t.Fatal("planned-build@v1 must not enable single-worker no_change")
	}
	if !allowsPlanNoChange(pb, planStage, "item-a", false, nil, nil, "") {
		t.Fatal("planned-build@v1 plan stage must allow no_change")
	}
	if allowsPlanNoChange(pb, workStage, "item-a", false, nil, nil, "") {
		t.Fatal("planned-build@v1 work stage must not enable plan no_change")
	}
	sw := &Workflow{Name: "single-worker", Version: 1}
	if !allowsSingleWorkerNoChange(sw, workStage, "single-worker@v1", "item-a", true, nil, nil, "") {
		t.Fatal("single-worker@v1 must allow no_change on non-rebase fork items")
	}
	if !allowsPlanNoChange(pb, planStage, "item-a", true, nil, nil, "") {
		t.Fatal("planned-build@v1 plan stage must allow no_change on non-rebase fork items")
	}
	rebaseID := remotesync.ForkRebasePrefix + "pi"
	if allowsSingleWorkerNoChange(sw, workStage, "single-worker@v1", rebaseID, true, nil, nil, "") ||
		allowsPlanNoChange(pb, planStage, rebaseID, true, nil, nil, "") {
		t.Fatal("fork rebase items must not allow no_change")
	}
}

type plannedPlanNoChangeSteps struct {
	*workflowSteps
	writeProposal bool
	plans         int
	works         int
	verifies      int
	lands         int
	planBrief     string
	verifyBrief   string
}

func (s *plannedPlanNoChangeSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.t.Fatal("gate must not run on no_change")
	return GateOutcome{}, nil
}

func (s *plannedPlanNoChangeSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	return s.workflowSteps.Land(ctx, c)
}

func (s *plannedPlanNoChangeSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(laneDir, WorktreeDir)
	briefBytes, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	brief := string(briefBytes)

	switch req.NodeID {
	case "plan":
		s.plans++
		s.planBrief = brief
		if s.writeProposal {
			outDir := nodeOutputsDir(s.f.root, req.RunID)
			if err := os.MkdirAll(outDir, 0o755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(outDir, "proposal"), `{"target":"work","workflow":"build-pieces@v1","args":{"pieces":[{"id":"p1","seat":"build","access":"write","prompt":"do it","write_scope":["tools/item-a/"]}]}}`)
		}
		report := "CAUSE: issue already fixed on trunk\nEVIDENCE: ran go test ./..., exit 0\nWHY_NO_CHANGE: trunk already has the fix\nRESULT: no_change\n"
		return s.exitedRecord(req, report), report, nil

	case "work":
		s.works++
		s.t.Fatal("work must be skipped when plan ends RESULT: no_change")
		return s.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil

	case "verify":
		s.verifies++
		s.verifyBrief = brief
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, text, class string
				if err := rows.Scan(&id, &text, &class); err != nil {
					return err
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   id,
					CriterionHash: item.CriterionHash(text),
					Verdict:       item.VerdictDone,
					ClaimClass:    class,
					Reason:        "independently reproduced on base; no repository change needed",
					Evidence:      []string{"node:plan", "cmd:go-test-check"},
				})
			}
			return rows.Err()
		})
		raw, err := json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts:      verdicts,
		})
		if err != nil {
			return run.Record{}, "", err
		}
		outPath := filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output")
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(outPath, raw, 0o644); err != nil {
			return run.Record{}, "", err
		}
		return s.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil
	}
	return s.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil
}

func (s *plannedPlanNoChangeSteps) exitedRecord(req run.Request, report string) run.Record {
	code := 0
	var refs []run.ArtifactRef
	if report != "" {
		laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
		path := filepath.Join(run.DurableDir(laneDir, req.RunID), "report.md")
		writeForkFile(s.t, path, report)
		refs = append(refs, run.ArtifactRef{Kind: "report", Ref: path})
	}
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     &code,
		StartedAt:    "2026-11-15T00:00:00Z",
		FinishedAt:   "2026-11-15T00:00:00Z",
		ArtifactRefs: refs,
	}
	st, err := store.Open(context.Background(), s.f.storePath, true)
	if err != nil {
		s.t.Fatal(err)
	}
	defer st.Close()
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		s.t.Fatal(err)
	}
	return rec
}

func setupPlannedBuildWithForkCatalog(t *testing.T) (refusalLoopFixture, *Loop, *workflowSteps, string) {
	t.Helper()
	f, l, base := plannedFixture(t)
	if err := os.MkdirAll(filepath.Join(f.root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "pi-fork https://example.com/pi-fork.git\n")
	runGit(t, f.root, "add", "bin/pi-forks.list")
	runGit(t, f.root, "commit", "-m", "add fork catalog")
	return f, l, base, "item-a"
}

func itemStateInStore(t *testing.T, f refusalLoopFixture, id string) string {
	t.Helper()
	var state string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state)
	})
	return state
}

func TestPlannedBuildPlanNoChangeClosesWithoutDeliveryOutput(t *testing.T) {
	f, l, base, id := setupPlannedBuildWithForkCatalog(t)
	steps := &plannedPlanNoChangeSteps{workflowSteps: base}
	l.cfg.Steps = steps
	trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)

	result := &LoopResult{}
	for i := 0; i < 20 && itemStateInStore(t, f, id) == "in-queue"; i++ {
		graphPass(t, l, result)
	}

	if trunkAfter := runGit(t, f.root, "rev-parse", TrunkRef); trunkAfter != trunkBefore {
		t.Fatalf("trunk moved on no_change: %s -> %s", trunkBefore, trunkAfter)
	}
	if steps.plans != 1 || steps.works != 0 || steps.verifies != 1 || steps.lands != 0 {
		t.Fatalf("unexpected counts: plans=%d works=%d verifies=%d lands=%d result=%+v", steps.plans, steps.works, steps.verifies, steps.lands, result)
	}
	if !strings.Contains(steps.planBrief, noChangeMarkerLine) {
		t.Fatalf("plan brief missing no_change marker:\n%s", steps.planBrief)
	}
	if !strings.Contains(steps.planBrief, "Classify delivery") {
		t.Fatalf("plan brief did not require plan.delivery from bin/pi-forks.list:\n%s", steps.planBrief)
	}
	if !strings.Contains(steps.verifyBrief, "## No-change finding") || !strings.Contains(steps.verifyBrief, "node:plan") || !strings.Contains(steps.verifyBrief, "issue already fixed on trunk") {
		t.Fatalf("verify brief missing no_change finding:\n%s", steps.verifyBrief)
	}

	var shown struct {
		State       string        `json:"state"`
		CloseReason string        `json:"close_reason"`
		ClosedBy    string        `json:"closed_by"`
		Finding     *item.Finding `json:"finding"`
	}
	var holds, nodeOutputRefusals int
	fixtureRead(t, f, func(r *store.Reader) error {
		env, err := item.Show(r, id, f.root)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(env.Payload, &shown); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, obstacles.HeldKind, id).Scan(&holds); err != nil {
			return err
		}
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.step')=?`, RefusalKind, RefusalNodeOutput).Scan(&nodeOutputRefusals)
	})
	if shown.State != "closed" || shown.CloseReason != "no_change: trunk already has the fix" || shown.ClosedBy == "" || shown.ClosedBy == "operator" {
		t.Fatalf("unexpected closed item: %+v", shown)
	}
	if shown.Finding == nil || shown.Finding.Cause != "issue already fixed on trunk" || shown.Finding.Evidence != "ran go test ./..., exit 0" || shown.Finding.WhyNoChange != "trunk already has the fix" || shown.Finding.EvaluatedHead != trunkBefore || shown.Finding.WorkerRun == "" || shown.Finding.JudgeRun == "" {
		t.Fatalf("unexpected finding: %+v", shown.Finding)
	}
	if holds != 0 || nodeOutputRefusals != 0 {
		t.Fatalf("holds=%d nodeOutputRefusals=%d, want 0 and 0", holds, nodeOutputRefusals)
	}
}

func TestPlannedBuildPlanNoChangeRefusesProposalConflict(t *testing.T) {
	f, l, base, id := setupPlannedBuildWithForkCatalog(t)
	steps := &plannedPlanNoChangeSteps{workflowSteps: base, writeProposal: true}
	l.cfg.Steps = steps

	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)

	var detail string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.detail'),'') FROM events WHERE kind=? AND json_extract(payload,'$.step')=? ORDER BY seq DESC LIMIT 1`, RefusalKind, RefusalNodeOutput).Scan(&detail)
	})
	if !strings.Contains(detail, "no_change-conflict") {
		t.Fatalf("refusal detail = %q, want no_change-conflict", detail)
	}
	if got := itemStateInStore(t, f, id); got != "in-queue" {
		t.Fatalf("item state = %q, want in-queue", got)
	}
}

func TestSingleWorkerNoChangeSettledOnceAcrossTwoPasses(t *testing.T) {
	ctx := context.Background()
	steps := &singleWorkerNoChangeSteps{
		callSuspend: true,
		report:      "CAUSE: caused by sibling candidate\nEVIDENCE: ran targeted test on base, passed\nWHY_NO_CHANGE: trunk needs no change\nRESULT: no_change\n",
	}
	f, l := setupSingleWorkerNoChange(t, ctx, steps, run.EndingCallSuspends)
	l.cfg.Lanes = []string{"lane-1"}
	trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)

	// Hold judge dispatch as soon as the worker exits, simulating a binary roll
	// overlapping the first settlement pass.
	steps.afterRun = func() { l.rollChecking.Add(1) }

	result := &LoopResult{}
	// Pass 1 dispatches the worker; its immediate settlement runs while
	// rollChecking holds judge dispatch.
	if _, err := l.pass(ctx, result); err != nil {
		t.Fatalf("dispatch pass: %v", err)
	}
	for len(l.active) > 0 {
		select {
		case c := <-l.completions:
			if stop, err := l.takeCompletion(c, result); stop != "" || (err != nil && !errors.Is(err, errRestartPending)) {
				t.Fatalf("completion %s / %v", stop, err)
			}
		case <-time.After(hangGuard):
			t.Fatalf("owned transition did not finish%s", ownedTransitionTimeoutDiagnostic())
		}
	}
	if steps.runs != 1 {
		t.Fatalf("runs after dispatch pass = %d, want 1", steps.runs)
	}

	// Pass 2 runs after the roll clears and finishes judging the no-change
	// finding without re-settling or faulting.
	l.rollChecking.Add(-1)
	graphPass(t, l, result)

	if trunkAfter := runGit(t, f.root, "rev-parse", TrunkRef); trunkAfter != trunkBefore {
		t.Fatalf("trunk moved from %s to %s", trunkBefore, trunkAfter)
	}

	var shown struct {
		State       string        `json:"state"`
		CloseReason string        `json:"close_reason"`
		Finding     *item.Finding `json:"finding"`
	}
	var noChangeEvents, itemFaults, closedCalls int
	fixtureRead(t, f, func(r *store.Reader) error {
		env, err := item.Show(r, "item-a", "show-1")
		if err != nil {
			return err
		}
		if err := json.Unmarshal(env.Payload, &shown); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, noChangeKind).Scan(&noChangeEvents); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, itemFaultKind).Scan(&itemFaults); err != nil {
			return err
		}
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.reason')='closed-no-change'`, callClosed).Scan(&closedCalls)
	})
	if noChangeEvents != 1 {
		t.Fatalf("drain.no-change events = %d, want 1", noChangeEvents)
	}
	if itemFaults != 0 {
		t.Fatalf("drain.item-fault events = %d, want 0", itemFaults)
	}
	if shown.State != "closed" || shown.CloseReason != "no_change: trunk needs no change" {
		t.Fatalf("unexpected item state: %+v", shown)
	}
	if shown.Finding == nil || shown.Finding.Cause != "caused by sibling candidate" || shown.Finding.Evidence != "ran targeted test on base, passed" || shown.Finding.WhyNoChange != "trunk needs no change" {
		t.Fatalf("unexpected finding: %+v", shown.Finding)
	}
	if closedCalls != 1 {
		t.Fatalf("closed-no-change calls = %d, want 1", closedCalls)
	}
}

func TestIntakeLandingOfferReexaminesNoChangeOffer(t *testing.T) {
	ctx := context.Background()
	steps := &singleWorkerNoChangeSteps{callSuspend: true}
	f, l := setupSingleWorkerNoChange(t, ctx, steps, run.EndingCallSuspends)
	l.cfg.Lanes = []string{"lane-1"}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	d := mustPlan(t, l)[0].decision
	staged, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %v (len=%d)", err, len(staged))
	}
	d = staged[0]
	rec, _, err := steps.Run(ctx, *d.RunRequest)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := l.intakeLandingOffer(ctx, d, rec, base)
	if err != nil || got != landingOfferSubmitted {
		t.Fatalf("first intake = (%v, %v), want (submitted, nil)", got, err)
	}
	got, err = l.intakeLandingOffer(ctx, d, rec, base)
	if err != nil || got != landingOfferSubmitted {
		t.Fatalf("second intake = (%v, %v), want (submitted, nil)", got, err)
	}

	mutated := landingOffer{
		CallID:  "call-nc-1",
		Tool:    "no_change",
		Base:    base,
		Finding: &landingOfferFinding{Cause: "different cause", Evidence: "ran targeted test on base, passed", WhyNoChange: "trunk needs no change"},
	}
	if err := writeLandingArtifact(run.DurableDir(l.laneDirPath(d.LaneID), rec.RunID), landingOfferFile, mutated); err != nil {
		t.Fatalf("write mutated offer: %v", err)
	}
	if _, err := l.intakeLandingOffer(ctx, d, rec, base); err == nil || !strings.Contains(err.Error(), "changed its recorded landing offer") {
		t.Fatalf("mutated intake err = %v, want changed its recorded landing offer", err)
	}
}

type forkNoChangeSteps struct {
	t           *testing.T
	storePath   string
	laneRoot    string
	forkDir     string
	report      string
	judgePass   bool
	workerBrief string
	judgeBrief  string
	workerRuns  int
	judgeRuns   int
	cancelAfter int
	cancel      context.CancelFunc
}

func (s *forkNoChangeSteps) Sync(context.Context) (SyncOutcome, error) { return SyncOutcome{}, nil }
func (s *forkNoChangeSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.t.Fatal("gate must not run on fork no_change")
	return GateOutcome{}, nil
}
func (s *forkNoChangeSteps) Land(context.Context, Candidate) (LandOutcome, error) {
	s.t.Fatal("land must not run on fork no_change")
	return LandOutcome{}, nil
}
func (s *forkNoChangeSteps) Verdict(context.Context, Candidate, string) error {
	s.t.Fatal("verdict must not run on fork no_change")
	return nil
}
func (s *forkNoChangeSteps) ItemState(context.Context, string, string, string, ...item.WaitingOn) error {
	return nil
}

func (s *forkNoChangeSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	laneDir := filepath.Join(s.laneRoot, req.LaneID)
	code := 0
	var refs []run.ArtifactRef
	var out string
	if strings.HasSuffix(req.RunID, "-judge") || req.Seat == "verify" || req.Seat == "review" {
		s.judgeRuns++
		rawBrief, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
		if err != nil {
			return run.Record{}, "", err
		}
		s.judgeBrief = string(rawBrief)
		head := runGit(s.t, s.forkDir, "rev-parse", "HEAD")
		tree := runGit(s.t, s.forkDir, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		st, err := store.OpenRead(context.Background(), s.storePath)
		if err != nil {
			return run.Record{}, "", err
		}
		err = st.ReadTx(context.Background(), func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, text, class string
				if err := rows.Scan(&id, &text, &class); err != nil {
					return err
				}
				verdict := item.VerdictDone
				reason := "independently verified all fork commits are still needed"
				if !s.judgePass {
					verdict = item.VerdictNotDone
					reason = "fork commit is now covered by upstream and must be dropped"
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   id,
					CriterionHash: item.CriterionHash(text),
					Verdict:       verdict,
					ClaimClass:    class,
					Reason:        reason,
					Evidence:      []string{"cmd:git-log-upstream-check"},
				})
			}
			return rows.Err()
		})
		st.Close()
		if err != nil {
			return run.Record{}, "", err
		}
		outcome := "pass"
		if !s.judgePass {
			outcome = "fail"
		}
		raw, err := json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       outcome,
			Verdicts:      verdicts,
		})
		if err != nil {
			return run.Record{}, "", err
		}
		outPath := filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output")
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(outPath, raw, 0o644); err != nil {
			return run.Record{}, "", err
		}
		out = "RESULT: done\n"
	} else {
		s.workerRuns++
		if s.cancelAfter > 0 && s.workerRuns > s.cancelAfter && s.cancel != nil {
			s.cancel()
			return run.Record{}, "", context.Canceled
		}
		rawBrief, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
		if err != nil {
			return run.Record{}, "", err
		}
		s.workerBrief = string(rawBrief)
		reportPath := filepath.Join(run.DurableDir(laneDir, req.RunID), "report.md")
		writeForkFile(s.t, reportPath, s.report)
		refs = append(refs, run.ArtifactRef{Kind: "report", Ref: reportPath})
		out = s.report
	}
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: &code,
		StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: refs,
	}
	st, err := store.Open(context.Background(), s.storePath, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer st.Close()
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		return run.Record{}, "", err
	}
	return rec, out, nil
}

func TestForkSingleWorkerNoChangeClosesWithFindingAndRefusesNotDone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		judgePass bool
	}{
		{name: "verified-nothing-to-change", judgePass: true},
		{name: "work-still-owed-not-done", judgePass: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			f := newRefusalLoopFixture(t, "item-a")
			if err := os.MkdirAll(filepath.Join(f.root, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
			writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
			runGit(t, f.root, "add", "bin/pi-forks.list", ".gitignore")
			runGit(t, f.root, "commit", "-qm", "fork catalog")

			forkDir := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
			if err := os.MkdirAll(forkDir, 0o755); err != nil {
				t.Fatal(err)
			}
			runGit(t, forkDir, "init", "-qb", "main")
			writeForkFile(t, filepath.Join(forkDir, "fork.txt"), "base\n")
			runGit(t, forkDir, "add", "fork.txt")
			runGit(t, forkDir, "commit", "-qm", "fork base")
			forkBase := runGit(t, forkDir, "rev-parse", "HEAD")
			runGit(t, f.root, "fetch", "--no-tags", forkDir, "refs/heads/main:refs/heads/patch/pi")

			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET workflow='single-worker@v1', judge_mode='agent' WHERE item_id='item-a'`); err != nil {
					return err
				}
				return recordFixtureFork(tx, "item-a")
			})

			steps := &forkNoChangeSteps{
				t:           t,
				storePath:   f.storePath,
				laneRoot:    f.laneRoot,
				forkDir:     forkDir,
				report:      "CAUSE: upstream release does not cover any of the fork commits\nEVIDENCE: compared every fork commit against upstream tag v2.0\nWHY_NO_CHANGE: every fork commit is still required\nRESULT: no_change\n",
				judgePass:   tc.judgePass,
				cancelAfter: 1,
				cancel:      cancel,
			}
			backends := filepath.Join("..", "..", "tillr-run", "backends")
			stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join(backends, "brief-template.md")}
			l := f.loop(t, steps, stager)
			l.cfg.Lanes = []string{"lane-1"}
			if l.cfg.Policy.Seats == nil {
				l.cfg.Policy.Seats = map[string]string{}
			}
			if l.cfg.Policy.Backends == nil {
				l.cfg.Policy.Backends = map[string]Backend{}
			}
			l.cfg.Policy.Seats["review"] = "verify-backend"
			l.cfg.Policy.Seats["verify"] = "verify-backend"
			l.cfg.Policy.Backends["verify-backend"] = Backend{Adapter: "verify-backend", Model: "verify-model"}
			var reportBuf strings.Builder
			l.cfg.Report = &reportBuf

			_, err := l.Run(runCtx)
			if tc.judgePass && err != nil {
				t.Fatalf("loop run: %v\nreport:\n%s", err, reportBuf.String())
			}
			if !strings.Contains(steps.workerBrief, noChangeMarkerLine) {
				t.Fatalf("fork worker brief missing no_change marker:\n%s", steps.workerBrief)
			}
			if steps.judgeRuns != 1 {
				t.Fatalf("judgeRuns = %d, want 1", steps.judgeRuns)
			}
			if !strings.Contains(steps.judgeBrief, "## No-change finding") || !strings.Contains(steps.judgeBrief, "every fork commit is still required") {
				t.Fatalf("judge brief missing no_change finding:\n%s", steps.judgeBrief)
			}
			if tip := runGit(t, f.root, "rev-parse", "refs/heads/patch/pi"); tip != forkBase {
				t.Fatalf("patch/pi moved from %s to %s", forkBase, tip)
			}

			var shown struct {
				State       string        `json:"state"`
				CloseReason string        `json:"close_reason"`
				ClosedBy    string        `json:"closed_by"`
				Finding     *item.Finding `json:"finding"`
			}
			var holds, notDoneRefusals int
			var holdDetail string
			fixtureRead(t, f, func(r *store.Reader) error {
				env, err := item.Show(r, "item-a", f.root)
				if err != nil {
					return err
				}
				if err := json.Unmarshal(env.Payload, &shown); err != nil {
					return err
				}
				_ = r.QueryRow(`SELECT COALESCE(json_extract(payload,'$.detail'), '') FROM events WHERE kind='drain.refusal' ORDER BY seq DESC LIMIT 1`).Scan(&holdDetail)
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id='item-a'`, obstacles.HeldKind).Scan(&holds); err != nil {
					return err
				}
				return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND json_extract(payload,'$.step') IN (?, ?)`, RefusalKind, RefusalVerifyNotDone, ReasonEvalFailed).Scan(&notDoneRefusals)
			})
			if tc.judgePass {
				if shown.State != "closed" || shown.CloseReason != "no_change: every fork commit is still required" || shown.ClosedBy == "" || shown.ClosedBy == "operator" {
					t.Fatalf("unexpected closed fork item: %+v (holdDetail=%q)\nreport:\n%s", shown, holdDetail, reportBuf.String())
				}
				if shown.Finding == nil || shown.Finding.Cause != "upstream release does not cover any of the fork commits" ||
					shown.Finding.Evidence != "compared every fork commit against upstream tag v2.0" ||
					shown.Finding.WhyNoChange != "every fork commit is still required" ||
					shown.Finding.EvaluatedHead != forkBase || shown.Finding.WorkerRun == "" || shown.Finding.JudgeRun == "" {
					t.Fatalf("unexpected finding: %+v", shown.Finding)
				}
				if holds != 0 {
					t.Fatalf("obstacle holds = %d, want 0", holds)
				}
			} else {
				if shown.State != "in-queue" {
					t.Fatalf("state = %q, want in-queue when verify returned not-done", shown.State)
				}
				if shown.Finding != nil {
					t.Fatalf("finding = %+v, want nil when verify returned not-done", shown.Finding)
				}
				if notDoneRefusals == 0 {
					t.Fatalf("verify-not-done refusals = %d, want >0", notDoneRefusals)
				}
			}
		})
	}
}
