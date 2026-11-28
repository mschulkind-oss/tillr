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

// modelStrictSteps rejects any run whose command still names a retired model,
// reproducing a harness exit 1 after the provider/model policy changed.
type modelStrictSteps struct {
	*restartWorkflowSteps
	allowedRoute string
}

func (s *modelStrictSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.Route() != s.allowedRoute {
		s.runs = append(s.runs, req)
		log := "Warning: Model \"" + req.Route() + "\" not found for provider \"amazon-bedrock\". Using custom model id.\n"
		report := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
		if err := os.MkdirAll(filepath.Dir(report), 0755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(report, []byte(log), 0644); err != nil {
			return run.Record{}, "", err
		}
		rec := run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       req.LaneID,
			Backend:      req.Backend,
			Route:        req.Route(),
			State:        run.StateExited,
			ExitCode:     countPtr(1),
			StartedAt:    "2026-11-21T23:47:19Z",
			FinishedAt:   "2026-11-21T23:47:20Z",
			ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}},
		}
		st, err := store.Open(ctx, s.f.storePath, true)
		if err != nil {
			return rec, log, err
		}
		defer st.Close()
		err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, log, err
	}
	return s.restartWorkflowSteps.Run(ctx, req)
}

func TestWorkflowRestartUsesPolicyModelInForce(t *testing.T) {
	f, l, baseSteps := restartGraphFixture(t)
	piBackend := l.cfg.Policy.Backends["fixture"]
	piBackend.Model = "amazon-bedrock/old-model"
	l.cfg.Policy.Backends["pi"] = piBackend
	l.cfg.Policy.DefaultBackend = "pi"
	l.cfg.Policy.Seats["build"] = "pi"

	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)
	original := pendingForTest(t, f, "item-a").Decision.RunRequest
	if original.Backend != "pi" || original.Route() != "amazon-bedrock/old-model" {
		t.Fatalf("original request = %+v", original)
	}

	// Switch backend pi in the policy to a new model before the loop restarts.
	piBackend.Model = "github-copilot/new-model"
	l.cfg.Policy.Backends["pi"] = piBackend

	steps := &modelStrictSteps{restartWorkflowSteps: baseSteps, allowedRoute: "github-copilot/new-model"}
	next := replaceGraphLoop(t, f, l, steps)
	result := &LoopResult{}
	graphPass(t, next, result)

	if len(steps.runs) != 1 {
		t.Fatalf("expected 1 restarted run, got %d: %+v", len(steps.runs), steps.runs)
	}
	if got := steps.runs[0]; got.Backend != "pi" || got.Route() != "github-copilot/new-model" {
		t.Errorf("restart ran backend=%q route=%q (cmd=%v), want pi / github-copilot/new-model", got.Backend, got.Route(), got.Command)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		closed, err := readInfrastructureClosed(r)
		if err != nil {
			return err
		}
		if closed["pi"] {
			t.Errorf("backend pi was closed by restarted run")
		}
		return nil
	})
}

func TestParallelRestartUsesPolicyModelInForce(t *testing.T) {
	f, l, steps := parallelFixture(t)
	piBackend := l.cfg.Policy.Backends["fixture"]
	piBackend.Model = "amazon-bedrock/old-model"
	l.cfg.Policy.Backends["pi"] = piBackend
	l.cfg.Policy.DefaultBackend = "pi"
	l.cfg.Policy.Seats["scout"] = "pi"
	l.cfg.Policy.Seats["build"] = "pi"
	steps.interrupt = true
	steps.writerAfter = 6

	result := &LoopResult{}
	graphPass(t, l, result)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, loopOrphanedKind, "run-item-a-a1-n1", struct{}{}, l.cfg.Now())
	})

	piBackend.Model = "github-copilot/new-model"
	l.cfg.Policy.Backends["pi"] = piBackend

	p := mustPlan(t, l)[0].decision
	if p.Action != ActionDispatch || !p.WorkflowRestart {
		t.Fatalf("parallel restart decision = %+v, want dispatch WorkflowRestart", p)
	}
	checked := 0
	fixtureRead(t, f, func(r *store.Reader) error {
		for _, node := range parallelNodes(p) {
			rec, err := terminalRecord(r, node.RunRequest.RunID)
			if err != nil {
				return err
			}
			if rec != nil {
				continue
			}
			checked++
			if node.RunRequest.Backend != "pi" || node.RunRequest.Route() != "github-copilot/new-model" {
				t.Errorf("unfinished parallel sibling %s ran backend=%q route=%q, want pi / github-copilot/new-model", node.RunRequest.RunID, node.RunRequest.Backend, node.RunRequest.Route())
			}
		}
		return nil
	})
	if checked == 0 {
		t.Fatalf("expected at least one unfinished parallel sibling to check")
	}
}

func TestWorkflowRestartHoldsWhenBackendRemovedFromPolicy(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	piBackend := l.cfg.Policy.Backends["fixture"]
	piBackend.Model = "amazon-bedrock/old-model"
	l.cfg.Policy.Backends["pi"] = piBackend
	l.cfg.Policy.DefaultBackend = "pi"
	l.cfg.Policy.Seats["build"] = "pi"

	p := mustPlan(t, l)[0]
	deadOwnerClaim(t, f, l, p)

	delete(l.cfg.Policy.Backends, "pi")
	l.cfg.Policy.DefaultBackend = "fixture"
	l.cfg.Policy.Seats["build"] = "fixture"

	next := replaceGraphLoop(t, f, l, steps)
	plans, _, err := next.passPlans(context.Background(), next.cfg.Now())
	if err != nil {
		t.Fatalf("passPlans exited after recorded backend was removed from policy: %v", err)
	}
	var itemA *Decision
	for i := range plans {
		if plans[i].decision.ItemID == "item-a" {
			itemA = &plans[i].decision
			break
		}
	}
	if itemA == nil || itemA.Action != ActionDispatch || !itemA.WorkflowRestart ||
		itemA.RunRequest == nil || itemA.RunRequest.Backend != "fixture" ||
		itemA.BackendMove == nil || itemA.BackendMove.FromBackend != "pi" || itemA.BackendMove.ToBackend != "fixture" {
		t.Fatalf("removed backend passPlans item-a = %+v, want dispatch on fixture with move pi -> fixture", itemA)
	}
	work := pendingForTest(t, f, "item-a")
	fixtureRead(t, f, func(r *store.Reader) error {
		got, err := pendingDecision(r, work, next.cfg.Now(), nil, nil, next.cfg.Policy, map[string]bool{}, 0)
		if err != nil {
			return err
		}
		if got.Action != ActionDispatch || !got.WorkflowRestart ||
			got.RunRequest == nil || got.RunRequest.Backend != "fixture" ||
			got.BackendMove == nil || got.BackendMove.FromBackend != "pi" || got.BackendMove.ToBackend != "fixture" {
			t.Errorf("removed backend restart decision = %+v, want dispatch on fixture with move pi -> fixture", got)
		}
		return nil
	})
}

func TestWorkflowRestartHoldsWhenItemSeatRemovedFromPolicy(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='tricky' WHERE item_id='item-a'`)
		return err
	})
	l.cfg.Policy.Seats["tricky"] = "fixture"
	deadOwnerClaim(t, f, l, decisionFor(t, l, "item-a"))
	delete(l.cfg.Policy.Seats, "tricky") // Leave the workflow's seat and backend usable.

	next := replaceGraphLoop(t, f, l, steps)
	got := planFor(t, next, "item-a")
	if got.Action != ActionHold || got.RunRequest != nil || got.Seat != "tricky" || !strings.HasPrefix(got.Reason, ReasonSeatUnmapped+": ") {
		t.Fatalf("restart with an unmapped item seat = %+v, want a hold with no request", got)
	}

	next.cfg.Policy.Seats["tricky"] = "fixture"
	got = planFor(t, next, "item-a")
	if got.Action != ActionDispatch || !got.WorkflowRestart || got.RunRequest == nil || got.RunRequest.Backend != "fixture" {
		t.Fatalf("restart with a restored seat mapping = %+v, want the original workflow restarted on fixture", got)
	}
}

func TestWorkflowRestartDropsSessionTurnWhenRouteChanges(t *testing.T) {
	policy := &Policy{
		DefaultBackend: "pi",
		Backends: map[string]Backend{
			"pi": {Adapter: "fixture", Model: "amazon-bedrock/old-model"},
		},
	}
	prev := policy.Request("item-a", "lane-1", "run-1-restart", "pi")
	prev.SessionTurn = true
	prev.ResumePrompt = "continue"

	same, ok := refreshRestartRequest(policy, Decision{}, prev)
	if !ok || !same.SessionTurn || same.ResumePrompt != "continue" {
		t.Fatalf("unchanged route dropped session turn: ok=%v req=%+v", ok, same)
	}

	policy.Backends["pi"] = Backend{Adapter: "fixture", Model: "github-copilot/new-model"}
	changed, ok := refreshRestartRequest(policy, Decision{}, prev)
	if !ok || changed.Route() != "github-copilot/new-model" || changed.SessionTurn || changed.ResumePrompt != "" {
		t.Fatalf("changed route kept stale session turn or route: ok=%v req=%+v", ok, changed)
	}
}
