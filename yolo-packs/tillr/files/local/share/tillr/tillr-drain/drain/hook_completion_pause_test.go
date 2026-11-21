package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type countedCompletion struct {
	Steps
	calls   int
	resumed []run.Request
}

func (s *countedCompletion) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	s.resumed = append(s.resumed, req)
	return run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z"}, "", nil
}

func (s *countedCompletion) CompleteItem(context.Context, string, string, definition.Definition) (bool, error) {
	s.calls++
	return true, nil
}

func TestHookCompletionWaitsForPausedParallelMember(t *testing.T) {
	t.Run("missing-session", func(t *testing.T) { testHookCompletionPausedParallelMember(t, false) })
	t.Run("staged-session", func(t *testing.T) { testHookCompletionPausedParallelMember(t, true) })
}

func testHookCompletionPausedParallelMember(t *testing.T, staged bool) {
	f, l, _ := parallelFixture(t)
	steps := &countedCompletion{Steps: l.cfg.Steps}
	l.cfg.Steps, l.nonLanding = steps, true
	d := f.decisions()[0]
	d.Attempt = 1
	d.Workflow = &Workflow{Stages: []WorkflowStage{{ID: "first", Kind: "agent", Access: "read_only"}, {ID: "second", Kind: "agent", Access: "read_only"}}}
	second := d
	second.NodeIndex = 1
	req := *d.RunRequest
	req.RunID += "-second"
	req.ReadOnlyGroup = "group"
	second.RunRequest = &req
	d.Parallel = []Decision{second}
	if staged {
		dir := run.NodeDir(l.laneDirPath(d.LaneID), req.RunID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(dir, BriefFile), "a staged reader\n")
		writeForkFile(t, filepath.Join(dir, SessionIDFile), "33b5f2d2-0cd4-4a49-b293-c58f5635561f\n")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, node := range parallelNodes(d) {
			if err := startNode(tx, node); err != nil {
				return err
			}
			rec := run.Record{RunID: node.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z"}
			if i == 1 {
				rec.State, rec.ExitCode = run.StatePaused, nil
			}
			if err := applyTerminal(tx, rec); err != nil {
				return err
			}
		}
		return nil
	})
	// Initialization may use Git; settlement of the hook definition may not.
	bin := filepath.Join(t.TempDir(), "git")
	writeForkFile(t, bin, "#!/bin/sh\nexit 99\n")
	if err := os.Chmod(bin, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(bin)+":"+os.Getenv("PATH"))
	outcome := l.settleHookGroup(context.Background(), &pendingWork{Decision: d}, &LoopResult{}, &PassTiming{})
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settlement: %+v", outcome)
	}
	if staged {
		if steps.calls != 1 || len(steps.resumed) != 1 || !steps.resumed[0].Resume || steps.resumed[0].RunID != req.RunID {
			t.Fatalf("hook did not wait for the same reader to continue: calls=%d resumed=%+v", steps.calls, steps.resumed)
		}
		return
	}
	if steps.calls != 0 || len(steps.resumed) != 0 {
		t.Fatalf("completion hook called %d times, continuations %d, without a staged session", steps.calls, len(steps.resumed))
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		reason, err := commandNodeHold(r, d.ItemID)
		if err == nil && reason != ReasonRunUnjudged {
			t.Errorf("hold: %q, want %q", reason, ReasonRunUnjudged)
		}
		return err
	})
}
