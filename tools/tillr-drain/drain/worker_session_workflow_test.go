package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type returnedGraphSteps struct{ *returnedLandingSteps }

func (s *returnedGraphSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	rec, _, err := s.continuationSteps.Run(ctx, req)
	if err != nil {
		return rec, "", err
	}
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind == "log" {
			body, err := os.ReadFile(ref.Ref)
			return rec, string(body), err
		}
	}
	return rec, "", nil
}

func TestReturnedWorkflowResumesProducingNode(t *testing.T) {
	for _, gone := range []bool{false, true} {
		t.Run(map[bool]string{false: "continued", true: "gone-session"}[gone], func(t *testing.T) { testReturnedWorkflow(t, gone, false, false) })
	}
}

func TestReturnedWorkflowKeepsRepairForSuccessor(t *testing.T) {
	testReturnedWorkflow(t, false, true, false)
}
func TestReturnedWorkflowWaitsWhenItsBackendIsShut(t *testing.T) {
	testReturnedWorkflow(t, false, false, true)
}

func testReturnedWorkflow(t *testing.T, gone, successor, quota bool) {
	f, l := designSeatFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Policy.Seats["read"] = "fixture"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id='item-a'`)
		return err
	})
	first := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	w := &Workflow{Name: "scout-build", Version: 1, Stages: []WorkflowStage{
		{ID: "scout", Kind: "agent", Seat: "read", Access: "read_only", Outputs: map[string]string{"notes": "text"}},
		{ID: "builder", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"candidate.txt"}}, Inputs: map[string]string{"notes": "text"}},
	}, Edges: []WorkflowEdge{{From: "scout", To: "builder", WhenOutcome: "pass", FromOutput: "notes", ToInput: "notes"}}}
	if successor {
		w.Stages = append(w.Stages, WorkflowStage{ID: "observe", Kind: "agent", Seat: "read", Access: "read_only"})
		w.Edges = append(w.Edges, WorkflowEdge{From: "builder", To: "observe", WhenOutcome: "pass"})
	}
	first.decision.Workflow = w
	first.decision.NodeIndex = 1
	first.decision.NodeBaseSHA = base
	dir := l.laneDirPath(first.decision.LaneID)
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, ItemBaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	target := l.laneTarget(first.decision.LaneID)
	writeForkFile(t, filepath.Join(target, "candidate.txt"), "first\n")
	runGit(t, target, "add", "candidate.txt")
	runGit(t, target, "commit", "-qm", "first")
	notes := filepath.Join(scratchUnderDurable(t), "notes")
	writeForkFile(t, notes, "accepted scout output\n")
	finishRefused(t, f, l, first, RefusalGate, "fixture refusal", "documents", "dead anchor")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,'scout','old-scout','passed')`, "item-a", first.decision.Attempt); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO node_artifacts(item_id,attempt,node_id,run_id,name,type,sha256,location) VALUES(?,?,'scout','old-scout','notes','text','digest',?)`, "item-a", first.decision.Attempt, notes)
		return err
	})
	if stop := l.abandon(context.Background(), first.decision.LaneID, first.decision.RunRequest.RunID, runGit(t, target, "rev-parse", "HEAD")); stop != "" {
		t.Fatal(stop)
	}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	if quota {
		dir := filepath.Join(f.root, "config", "tillr", "workflows")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(dir, "scout-build@v1.json"), mustJSON(w))
		runGit(t, f.root, "add", "config")
		runGit(t, f.root, "commit", "-qm", "workflow")
		base = runGit(t, f.root, "rev-parse", TrunkRef)
		l.cfg.Policy.Backends["alternate"] = l.cfg.Policy.Backends["fixture"]
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET workflow='scout-build@v1' WHERE item_id='item-a'`); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('fixture','weekly',1,'rejected',?,'refused')`, l.cfg.Now().Add(time.Hour).UnixMilli())
			return err
		})
		p := mustPlan(t, l)[0]
		if p.decision.Action != ActionHold || p.decision.Reason != ReasonBackendWindow || p.decision.DeferredBy != "fixture" {
			t.Fatalf("returned workflow escaped its closed backend: %+v", p.decision)
		}
		return
	}
	p := mustPlan(t, l)[0]
	if p.decision.NodeIndex != 1 || p.decision.Workflow == nil || !p.decision.RunRequest.SessionTurn {
		t.Fatalf("restarted at node 1 instead of writer: %+v", p.decision)
	}
	if p.decision.NodeBaseSHA != base {
		t.Fatalf("node boundary lost: %+v", p.decision)
	}
	modeCheck := `test "$TILLR_SESSION_MODE" = resume || exit 71` + "\n"
	if gone {
		modeCheck = `if [ "$TILLR_SESSION_MODE" = resume ]; then echo 'session not found'; exit 1; fi` + "\n"
	}
	p.decision.RunRequest.Command = []string{continuationShell(t), "-c", modeCheck + `printf 'second\n' > candidate.txt
git add candidate.txt
git commit -qm second
echo 'RESULT: done'
`}
	d := applyReturnedTurn(t, l, p, base)
	brief, err := os.ReadFile(filepath.Join(dir, BriefFile))
	if err != nil || !strings.Contains(string(brief), notes) || !strings.Contains(string(brief), "node builder") {
		t.Fatalf("node input or scope missing: %v %s", err, brief)
	}
	steps := &returnedGraphSteps{returnedLandingSteps: &returnedLandingSteps{continuationSteps: &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}, root: f.root}}
	l.cfg.Steps = steps
	rec, log, err := steps.Run(context.Background(), *d.RunRequest)
	if err != nil {
		t.Fatal(err)
	}
	outcome := l.settle(context.Background(), d, rec, log, base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle returned graph: %+v", outcome)
	}
	if successor {
		if steps.gates != 0 || steps.lands != 0 {
			t.Fatal("writer bypassed successor")
		}
		p := mustPlan(t, l)[0]
		if p.decision.NodeIndex != 2 || p.decision.Returned != nil || p.decision.RunRequest.SessionTurn {
			t.Fatalf("successor kept return mode: %+v", p.decision)
		}
		repaired := runGit(t, target, "rev-parse", "HEAD")
		p.decision.RunRequest.Command = []string{continuationShell(t), "-c", "echo 'RESULT: done'"}
		next := applyReturnedTurn(t, l, p, base)
		if got := runGit(t, target, "rev-parse", "HEAD"); got != repaired {
			t.Fatalf("successor erased repair: %s", got)
		}
		rec, log, err := steps.Run(context.Background(), *next.RunRequest)
		if err != nil {
			t.Fatal(err)
		}
		outcome := l.settle(context.Background(), next, rec, log, base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
		if outcome.err != nil || outcome.stop != "" {
			t.Fatalf("successor: %+v", outcome)
		}
	}
	if steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("returned writer not landed: gates=%d lands=%d", steps.gates, steps.lands)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		source, err := nodeSourceAttempt(r, d.ItemID, d.Attempt, "scout")
		if err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE item_id='item-a' AND attempt=? AND node_id='scout' AND run_id='old-scout' AND outcome='passed'`, source).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Error("accepted predecessor was rerun or lost")
		}
		return nil
	})
}

func TestReturnedWorkerRequiresResumeDeclaration(t *testing.T) {
	_, l, _, _ := returnedSessionFixture(t, RefusalGate)
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "undeclared-adapter"}
	if d := mustPlan(t, l)[0].decision; d.WorkerSessionReason != "cannot-resume" || d.RunRequest.SessionTurn {
		t.Fatalf("undeclared adapter labeled resumed: %+v", d)
	}
}

func TestReturnedWorkerRetiredLaneUsesOfferedLane(t *testing.T) {
	f, l, _, base := returnedSessionFixture(t, RefusalGate)
	l.cfg.Lanes = []string{"lane-2"}
	// This offered lane is genuinely present in the fixture.
	path := filepath.Join(f.laneRoot, "lane-2", WorktreeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "worktree", "add", "-q", "-b", LaneBranch(f.laneRoot, "lane-2"), path, TrunkRef)
	p := mustPlan(t, l)[0]
	if p.decision.LaneID != "lane-2" || p.decision.WorkerSessionReason != "lane-unavailable" || p.decision.RunRequest.SessionTurn {
		t.Fatalf("dispatched retired lane: %+v", p.decision)
	}
	d := applyReturnedTurn(t, l, p, base)
	id, err := ReadSessionID(l.laneDirPath(d.LaneID))
	if err != nil || id == "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("retired lane reused session: %s %v", id, err)
	}
}
