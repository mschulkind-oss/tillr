package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

type recoveryWorkflowSteps struct {
	*restartWorkflowSteps
	file     string
	retained bool
}

func (s *recoveryWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	body, err := os.ReadFile(s.file)
	s.retained = err == nil && string(body) == "keep my work\n"
	return s.restartWorkflowSteps.Run(ctx, req)
}

func TestInPlaceRestartResumesGapNodeWithLostRunFace(t *testing.T) {
	f, old, steps := restartGraphFixture(t)
	ctx := context.Background()
	declarePauseKind(t, old, run.PauseAtGap)
	if err := old.refreshSession(ctx, old.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	result := &LoopResult{}
	for range 4 {
		graphPass(t, old, result)
	}
	p := mustPlan(t, old)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := old.applyPlan(ctx, p, base, old.cfg.Now())
	if err != nil || !ok || work.Decision.NodeIndex != 2 {
		t.Fatalf("third node claim: %v %v %+v", ok, err, work)
	}
	staged, err := old.stageDispatches(ctx, []Decision{work.Decision}, work.BaseSHA, result)
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage: %v %v", staged, err)
	}
	d := staged[0]
	id := d.RunRequest.RunID
	// The detached face disappeared without a finish record, after the adapter
	// started writing to the transcript. Its staged session still belongs to this node.
	lane := old.laneDirPath(d.LaneID)
	writeForkFile(t, filepath.Join(lane, id+".log"), "worker started\n")
	target := old.laneTarget(d.LaneID)
	file := filepath.Join(target, "tools/item-a/third-node.txt")
	writeForkFile(t, file, "keep my work\n")
	commits := runGit(t, target, "rev-parse", "HEAD")
	old.endSession()

	recovery := &recoveryWorkflowSteps{restartWorkflowSteps: steps, file: file}
	next := replaceGraphLoop(t, f, old, recovery)
	var trace strings.Builder
	next.cfg.Report = &trace
	if err := next.refreshSession(ctx, next.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	defer next.endSession()
	graphPass(t, next, result)
	if len(steps.runs) != 3 || steps.runs[2].RunID != id || !steps.runs[2].Resume || steps.heads[2] != commits {
		t.Fatalf("lost node restarted instead of resuming or lost prior commits: %+v %v\n%s", steps.runs, steps.heads, trace.String())
	}
	if !recovery.retained {
		t.Fatal("interrupted third node lost its uncommitted work")
	}
	if !strings.Contains(trace.String(), "continuing interrupted run "+id+" on lane-1 as the same attempt: the previous loop lost its run face") {
		t.Fatalf("missing restart reason:\n%s", trace.String())
	}
}
