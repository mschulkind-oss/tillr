package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestKeptCandidateStagesItsItemRangeOnAnotherLane(t *testing.T) {
	for _, route := range []string{"operator retry", "verify return"} {
		t.Run(route, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			original := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
			base := l.laneBase(original.LaneID)
			head := conflictTestCommit(t, l.laneTarget(original.LaneID), "candidate.txt", "more kept work\n", "second kept commit")
			tip := conflictTestCommit(t, f.root, "trunk-only.txt", "later trunk\n", "advance trunk")
			w := &Workflow{Name: "build", Version: 1, Stages: []WorkflowStage{
				{ID: "plan", Kind: "agent", Seat: "plan", Access: "read_only"},
				{ID: "build", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"candidate.txt"}}},
			}}
			// The earlier repair started at the kept head. That is its node boundary,
			// not the original item base that the worker must use to offer the work.
			s := &WorkerSession{RunID: original.RunRequest.RunID, LaneID: original.LaneID, BaseSHA: base, HeadSHA: head, NodeBaseSHA: head, Workflow: w}
			d := Decision{ItemID: original.ItemID, LaneID: "lane-2", Attempt: 2, Workflow: w, NodeIndex: 1, NodeBaseSHA: head}
			d.RunRequest = &run.Request{ItemID: d.ItemID, LaneID: d.LaneID, RunID: "run-kept-candidate-n2", Backend: "fixture", Command: []string{"fixture"}, WorkingDir: WorktreeDir}
			if route == "operator retry" {
				d.Action, d.Reason = ActionRetry, ReasonRetry
				d.RetryCandidate = &RetryCandidate{RunID: s.RunID, BaseSHA: base, HeadSHA: head, Available: true, WorkerSession: s}
			} else {
				d.Action, d.Reason = ActionDispatch, ReasonReturned
				d.Returned = &ReturnedEnding{RunID: s.RunID, Cause: RefusalVerifyNotDone, WorkerSession: s}
			}
			if err := appendJudgment(ctx, f.storePath, dispatchStartedKind, "run", d.RunRequest.RunID, pendingWork{Decision: d}, l.cfg.Now()); err != nil {
				t.Fatal(err)
			}
			var report bytes.Buffer
			l.cfg.Report = &report
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			staged, err := l.stageDispatches(ctx, []Decision{d}, tip, &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage kept candidate: %v, staged=%d, report=%s", err, len(staged), &report)
			}
			target := l.laneTarget(d.LaneID)
			if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
				t.Fatalf("HEAD=%s, want kept head %s", got, head)
			}
			for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
				if got := l.laneFile(d.LaneID, name); got != base {
					t.Errorf("lane %s=%s, want item base %s", name, got, base)
				}
			}
			if got := staged[0].NodeBaseSHA; got != head {
				t.Errorf("node base=%s, want kept node start %s", got, head)
			}
			// run-pi reads the base_sha beside the run's archived brief for TILLR_GOAL_BASE.
			raw, err := os.ReadFile(filepath.Join(RunDir(f.root, d.RunRequest.RunID), BaseSHAFile))
			if err != nil {
				t.Fatal(err)
			}
			goalBase := strings.TrimSpace(string(raw))
			if goalBase != base {
				t.Errorf("goal base=%s, want item base %s", goalBase, base)
			}
			if got := runGit(t, target, "rev-list", "--count", goalBase+"..HEAD"); got != "2" {
				t.Errorf("offerable range has %s commits, want 2", got)
			}
			offer := landingOffer{CallID: "kept-offer", Commit: head, Base: goalBase, Summary: "kept work"}
			if _, err := l.validateLandingOffer(ctx, staged[0], run.Record{RunID: d.RunRequest.RunID}, tip, offer); err != nil {
				t.Errorf("kept candidate offer refused: %v", err)
			}
		})
	}
}
