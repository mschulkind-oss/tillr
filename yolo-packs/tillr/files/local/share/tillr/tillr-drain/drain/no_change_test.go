package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
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
}
