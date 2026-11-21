package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestOperatorRetryRestoresJudgeCandidate(t *testing.T) {
	for _, state := range []string{"retained", "missing", "cold"} {
		t.Run(state, func(t *testing.T) {
			missing := state == "missing"
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			d := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
			target := l.laneTarget(d.LaneID)
			base := l.laneBase(d.LaneID)
			head := runGit(t, target, "rev-parse", "HEAD")
			c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Worktree: target, BaseSHA: base, HeadSHA: head, EvidenceKey: "judge-finding"}
			if missing {
				c.HeadSHA = strings.Repeat("a", 40)
			}
			detail := "Criterion c1: supply the missing regression evidence"
			if err := l.holdJudge(ctx, c, "judge-run", ReasonEvalFailed, detail, &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			if err := l.markExamined(ctx, d); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := AuthorizeRetry(ctx, tx, RetryRequest{ItemID: d.ItemID, RunID: d.RunRequest.RunID, By: "operator", Why: "Recheck the retained work", At: l.cfg.Now()})
				return err
			})
			tip := conflictTestCommit(t, f.root, "trunk-only.txt", "later trunk\n", "advance trunk")
			// Release the old lane contents: a retry must recover the recorded commit, not
			// accidentally inherit whichever HEAD happens to be in its offered lane.
			runGit(t, target, "reset", "--hard", tip)
			if state == "cold" {
				runGit(t, f.root, "worktree", "remove", "--force", target)
			}
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			p := mustPlan(t, l)[0]
			if p.decision.Action != ActionRetry {
				t.Fatalf("retry plan: %+v", p.decision)
			}
			ok, work, err := l.applyPlan(ctx, p, tip, l.cfg.Now())
			if err != nil || !ok || work == nil {
				t.Fatalf("apply retry: %v %+v %v", ok, work, err)
			}
			var report bytes.Buffer
			l.cfg.Report = &report
			staged, stageErr := l.stageDispatches(ctx, []Decision{work.Decision}, tip, &LoopResult{})
			if stageErr != nil || len(staged) != 1 {
				t.Fatalf("stage retry: %v %+v %s decision=%+v", stageErr, staged, report.String(), work.Decision.RetryCandidate)
			}
			work.Decision = staged[0]
			target = l.laneTarget(work.Decision.LaneID)
			wantHead, wantBase := head, base
			if missing {
				wantHead, wantBase = tip, tip
			}
			if got := runGit(t, target, "rev-parse", "HEAD"); got != wantHead {
				t.Fatalf("retry HEAD=%s want %s", got, wantHead)
			}
			if got := l.settleBase(work.Decision.LaneID, tip); got != wantBase {
				t.Fatalf("retry item base=%s want %s", got, wantBase)
			}
			raw, err := os.ReadFile(filepath.Join(RunDir(f.root, work.Decision.RunRequest.RunID), BriefFile))
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{detail, "Recheck the retained work"} {
				if !strings.Contains(string(raw), text) {
					t.Fatalf("retry brief missing %q", text)
				}
			}
			if missing {
				if !strings.Contains(string(raw), "candidate is unavailable") || !strings.Contains(string(raw), "current trunk") {
					t.Fatalf("missing candidate fallback not explicit: %s", raw)
				}
				return
			}
			// Re-reporting the restored candidate is valid: the authorization can ask for
			// evidence rather than another code commit.
			steps := &countingJudgeSteps{storePath: f.storePath}
			l.cfg.Steps = steps
			rec := run.Record{RunID: work.Decision.RunRequest.RunID, ItemID: d.ItemID, LaneID: work.Decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), EndCause: run.EndNormalExit}
			out := l.settle(ctx, work.Decision, rec, "RESULT: done\n", tip, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
			if out.err != nil || out.stop != "" {
				t.Fatalf("settle retry: %+v", out)
			}
			if steps.gates.Load() != 1 {
				t.Fatalf("unchanged restored candidate was not gated: %d", steps.gates.Load())
			}
		})
	}
}

func TestJudgeRetryUsesOnlyItsRecordedLatestWriter(t *testing.T) {
	for _, latest := range []string{"writer", "newer"} {
		p := planner{
			latest:  map[string]string{"item": "writer"},
			retries: map[string]retryRow{"item": {RetryID: "retry", SupersedesRunID: "judge"}},
			history: runHistory{endings: map[string]refusalPayload{"judge": {ItemID: "item", Step: ReasonEvalFailed}}, judgeWriters: map[string]string{"judge": "writer"}, judged: map[string]bool{"judge": true}},
		}
		p.latest["item"] = latest
		got := p.retryFor("item")
		if latest == "newer" {
			if got != nil {
				t.Fatal("judge authorization outlived its writer")
			}
			continue
		}
		if got == nil || got.supersedesRunID != "judge" || got.refusal != ReasonRetryJudged {
			t.Fatalf("authorization lost attribution or ack rule: %+v", got)
		}
		r := p.retries["item"]
		r.Acknowledge = RetryAckGateFailed
		p.retries["item"] = r
		if got := p.retryFor("item"); got == nil || got.refusal != "" {
			t.Fatalf("acknowledged judge authorization refused: %+v", got)
		}
	}
}

func TestRetryStagingPreservesDirtyCandidateAndTrunkLanes(t *testing.T) {
	for _, atTrunk := range []bool{false, true} {
		t.Run(map[bool]string{false: "candidate head", true: "trunk head"}[atTrunk], func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			d := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
			target := l.laneTarget(d.LaneID)
			base := l.laneBase(d.LaneID)
			head := runGit(t, target, "rev-parse", "HEAD")
			if atTrunk {
				runGit(t, target, "reset", "--hard", base)
			}
			before := runGit(t, target, "rev-parse", "HEAD")
			writeForkFile(t, filepath.Join(target, "untracked.txt"), "do not discard\n")
			stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			err := stager.Stage(context.Background(), StageSpec{ItemID: d.ItemID, LaneID: d.LaneID, RunID: "retry-dirty", Backend: "fixture", WorkingDir: WorktreeDir, BaseSHA: base, KeptHeadSHA: head, KeptItemBaseSHA: base, RetryCandidate: &RetryCandidate{BaseSHA: base, HeadSHA: head, Available: true}})
			if err == nil || asLaneRefusal(err) == nil {
				t.Fatalf("dirty retry not refused: %v", err)
			}
			if got := runGit(t, target, "rev-parse", "HEAD"); got != before {
				t.Fatal("dirty retry moved HEAD")
			}
			raw, err := os.ReadFile(filepath.Join(target, "untracked.txt"))
			if err != nil || string(raw) != "do not discard\n" {
				t.Fatalf("dirty work lost: %q %v", raw, err)
			}
		})
	}
}

func TestRetryCandidateDoesNotRestoreUnrelatedRefusals(t *testing.T) {
	for _, cause := range []string{RefusalNoOp, RefusalGate, RefusalFence, RefusalLand, RefusalConflict, RefusalReverted} {
		p := planner{history: runHistory{endings: map[string]refusalPayload{"old": {Step: cause, BaseSHA: "base", HeadSHA: "head"}}}}
		if got := p.retryCandidate("item", "old"); got != nil {
			t.Fatalf("restored unrelated %s: %+v", cause, got)
		}
	}
	p := planner{history: runHistory{endings: map[string]refusalPayload{"old": {Step: ReasonEvalFailed, Restart: true, BaseSHA: "base", HeadSHA: "head"}}}}
	if got := p.retryCandidate("item", "old"); got != nil {
		t.Fatalf("restored deliberate restart: %+v", got)
	}
}
