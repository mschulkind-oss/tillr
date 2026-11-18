package drain

// These tests use the real run claim/finish lifecycle and a real lane checkout.

import (
	"context"
	"os"

	"github.com/mschulkind-oss/tillr-run/run"
	"path/filepath"
	"strings"
	"testing"
)

func declareWorkerResumeFixture(t *testing.T, l *Loop) {
	t.Helper()
	dir := filepath.Join(scratchUnderDurable(t), "adapter")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(dir, "run-fixture")
	writeForkFile(t, adapter, "#!/bin/sh\nexit 0\n")
	writeForkFile(t, filepath.Join(dir, "pause-kind"), run.PauseAtGap+"\n")
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: adapter}
}

func TestReturnedWorkerKeepsLaneAndSession(t *testing.T) {
	for _, cause := range []string{RefusalGate, RefusalNoOp, RefusalFence, RefusalLand} {
		t.Run(cause, func(t *testing.T) {
			f, l := designSeatFixture(t)
			declareWorkerResumeFixture(t, l)
			first := mustPlan(t, l)[0]
			lane := first.decision.LaneID
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			dir := l.laneDirPath(lane)
			session := "11111111-1111-4111-8111-111111111111"
			writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
			writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
			target := l.laneTarget(lane)
			writeForkFile(t, filepath.Join(target, "candidate.txt"), "first\n")
			if cause != RefusalNoOp {
				runGit(t, target, "add", "candidate.txt")
				runGit(t, target, "commit", "-qm", "first")
			}
			head := runGit(t, target, "rev-parse", "HEAD")
			finishRefused(t, f, l, first, cause, "fixture refusal", "documents", "dead anchor")
			if stop := l.abandon(context.Background(), lane, first.decision.RunRequest.RunID, head); stop != "" {
				t.Fatal(stop)
			}
			if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
				t.Fatalf("refused lane was reset: %s != %s", got, head)
			}
			second := mustPlan(t, l)[0].decision
			if second.LaneID != lane || second.RunRequest.RunID == first.decision.RunRequest.RunID {
				t.Fatalf("returned turn lost lane or distinct record: %+v", second)
			}
			staged, err := l.stageDispatches(context.Background(), []Decision{second}, base, &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage: %v %+v", err, staged)
			}
			got, err := ReadSessionID(dir)
			if err != nil || got != session {
				t.Fatalf("lost session: %s %v", got, err)
			}
			if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
				t.Fatalf("staging reset candidate: %s", got)
			}
			// The returned prompt contains precisely the evidence used by a fresh brief.
			// Execution and settlement are covered by TestReturnedSessionTurn below.
			brief := returnedBrief(*second.Returned)
			if !strings.Contains(brief, "fixture refusal") || cause == RefusalGate && !strings.Contains(brief, "documents") {
				t.Fatal(brief)
			}
		})
	}
}
