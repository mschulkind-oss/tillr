package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func forkNodeBaseFixture(t *testing.T) (refusalLoopFixture, *Loop, string, string, string) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: l.cfg.LaneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	if err := os.MkdirAll(fork, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-q", "-b", "main")
	runGit(t, fork, "commit", "--allow-empty", "-qm", "fork base")
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id='item-a'`); err != nil {
			return err
		}
		return recordFixtureFork(tx, "item-a")
	})
	return f, l, fork, runGit(t, f.root, "rev-parse", "HEAD"), runGit(t, fork, "rev-parse", "HEAD")
}

func TestForkNodeBriefMatchesStagedFence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{name: "repository scope", paths: []string{"tools/tillr-drain/"}, want: ".forks/lanes/lane-1/pi/\n"},
		{name: "inherited scope", want: ".forks/lanes/lane-1/pi/\n"},
		{name: "narrowed fork scope", paths: []string{".forks/lanes/old-lane/pi/src/"}, want: ".forks/lanes/lane-1/pi/src/\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, l, _, trunk, _ := forkNodeBaseFixture(t)
			spec := StageSpec{
				LaneID: "lane-1", ItemID: "item-a", RunID: "run-fork-node", Backend: "fixture", BaseSHA: trunk, WorkingDir: WorktreeDir,
				Node: &WorkflowStage{ID: "build", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: tc.paths}},
			}
			if err := l.cfg.Stage.Stage(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
			lane := filepath.Join(l.cfg.LaneRoot, spec.LaneID)
			fence, err := os.ReadFile(filepath.Join(lane, FenceFile))
			if err != nil || string(fence) != tc.want {
				t.Fatalf("staged fence = %q, want %q (error: %v)", fence, tc.want, err)
			}
			brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
			if err != nil {
				t.Fatal(err)
			}
			_, scope, ok := strings.Cut(string(brief), "This node may write only these paths:\n")
			scope, _, _ = strings.Cut(scope, "Write declared outputs only")
			if !ok || scope != string(fence) {
				t.Fatalf("brief scope = %q (present: %t), want staged fence %q", scope, ok, fence)
			}
		})
	}
}

func TestLaterForkNodeRecordsStagedBase(t *testing.T) {
	f, l, fork, trunk, base := forkNodeBaseFixture(t)
	ctx := context.Background()
	p := mustPlan(t, l)[0]
	p.decision.Workflow = &Workflow{Name: "fork", Version: 1, Stages: []WorkflowStage{{ID: "first"}, {ID: "second"}}}
	p.decision.NodeIndex = 1
	p.decision.ReusedNodes = []string{"first"}
	ok, work, err := l.applyPlan(ctx, p, trunk, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v / %v", ok, err)
	}
	staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, trunk, &LoopResult{})
	if err != nil || len(staged) != 1 {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			faults, faultErr := ReadItemFaults(tx.Reader())
			t.Logf("staging holds: %+v", faults)
			return faultErr
		})
		t.Fatalf("stage: %+v / %v", staged, err)
	}
	if target := l.laneTarget("lane-1"); target != fork {
		t.Fatalf("target = %s, want %s", target, fork)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		pending, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		recorded := pending[p.decision.ItemID]
		if recorded == nil || recorded.Decision.NodeBaseSHA != base {
			t.Fatalf("recorded work = %+v, want node base %s (trunk %s)", recorded, base, trunk)
		}
		return nil
	})
}

func TestMissingCandidateBaseHoldsOnlyTheItem(t *testing.T) {
	f, l, fork, trunk, _ := forkNodeBaseFixture(t)
	ctx := context.Background()
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	p := mustPlan(t, l)[0]
	ok, work, err := l.applyPlan(ctx, p, trunk, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v / %v", ok, err)
	}
	staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, trunk, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %+v / %v", staged, err)
	}
	runGit(t, fork, "commit", "--allow-empty", "-qm", "candidate")
	req := p.decision.RunRequest
	c := Candidate{ItemID: req.ItemID, LaneID: req.LaneID, RunID: req.RunID, Worktree: fork, BaseSHA: trunk, HeadSHA: runGit(t, fork, "rev-parse", "HEAD"), Request: req}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		at := l.cfg.Now().Format("2006-01-02T15:04:05Z07:00")
		return applyTerminal(tx, run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: at, FinishedAt: at, ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := appendJudgment(ctx, f.storePath, candidateReadyKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	var result LoopResult
	historyPass(t, l, &result)
	d := mustPlan(t, l)[0].decision
	if d.Reason != ReasonCandidateMismatch {
		t.Fatalf("candidate was not held: %+v", d)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		detail := faults[c.ItemID].Detail
		for _, want := range []string{trunk, c.HeadSHA, fork} {
			if !strings.Contains(detail, want) {
				t.Errorf("hold detail %q missing %s", detail, want)
			}
		}
		return nil
	})
	if steps.gates != 0 || steps.lands != 0 {
		t.Fatalf("bad candidate was judged: %+v", steps)
	}
}
