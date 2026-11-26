package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func savePolicyFile(t *testing.T, ref string, p Policy) *Policy {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, ref, string(raw))
	loaded, err := LoadPolicy(ref)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func TestPolicyChangeRemovingBackendHoldsInProgressWorkflowItemAndContinuesLoop(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	ref := filepath.Join(f.root, "policy.json")
	p := Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi", "verify": "pi-judge"},
		Backends: map[string]Backend{
			"pi":       {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
			"pi-judge": {Adapter: "pi-judge", Model: "model-judge", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, p), ref

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat = 'build' WHERE item_id = 'item-b'`); err != nil {
			return err
		}
		// Seed item-a as an in-progress workflow item whose verify node was dispatched
		// on pi-judge under the earlier policy and is ready to restart on pi-judge.
		wf := &Workflow{
			Name:    "test-wf",
			Version: 1,
			Stages: []WorkflowStage{
				{ID: "work", Seat: "build", Access: "write"},
				{ID: "verify", Seat: "verify", Access: "read_only"},
			},
		}
		req := l.cfg.Policy.Request("item-a", "lane-1", "run-item-a-a1-n2-restart", "pi-judge")
		req.NodeID = "verify"
		d := Decision{
			ItemID:          "item-a",
			Attempt:         1,
			Action:          ActionDispatch,
			Reason:          "workflow-node-restart",
			From:            "running",
			To:              "running",
			LaneID:          "lane-1",
			Seat:            "verify",
			Workflow:        wf,
			NodeIndex:       1,
			WorkflowRestart: true,
			RunRequest:      req,
		}
		stampDecisionRequest(d, req)
		identifyDecision(&d)
		return appendEffect(tx, nodeAdvancedKind, req.RunID, pendingWork{
			Decision: d,
			Ready:    true,
		}, l.cfg.Now().UTC())
	})

	// Change the session policy so verify maps to pi and pi-judge no longer exists.
	p.Seats["verify"] = "pi"
	delete(p.Backends, "pi-judge")
	savePolicyFile(t, ref, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()

	var result LoopResult
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("loop exited after policy removed backend: %v", err)
	}

	// item-b must still dispatch on pi; item-a must not fall back to pi.
	select {
	case req := <-steps.requests:
		if req.ItemID != "item-b" || req.Backend != "pi" {
			t.Fatalf("unexpected dispatch: %+v", req)
		}
	case <-time.After(hangGuard):
		t.Fatal("other item did not dispatch")
	}
	select {
	case extra := <-steps.requests:
		t.Fatalf("item-a fell back instead of holding: %+v", extra)
	default:
	}

	held := planFor(t, l, "item-a")
	if held.Action != ActionHold || !strings.HasPrefix(held.Reason, ReasonBackendMissing+": ") ||
		!strings.Contains(held.Reason, "verify") || !strings.Contains(held.Reason, "pi-judge") ||
		held.Seat != "verify" || held.DeferredBy != "pi-judge" || held.RunRequest != nil {
		t.Fatalf("unexpected hold decision for item-a: %+v", held)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		answer, err := Why(r, Pass{Build: &l.build, AvailableLanes: &l.cfg.Lanes}, l.currentPolicy(), &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID != "item-a" {
				continue
			}
			if row.Class != "needs you" || !strings.HasPrefix(row.Where, "backend") ||
				!strings.Contains(row.Reason, "verify") || !strings.Contains(row.Reason, "pi-judge") ||
				!slices.Contains(row.WaitingOn, "pi-judge") {
				t.Errorf("unexpected why row for item-a: %+v", row)
			}
			return nil
		}
		t.Error("item-a missing from why answer")
		return nil
	})
}

func TestPolicyChangeRemovingBackendHoldsFailedItemAndContinuesLoop(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	ref := filepath.Join(f.root, "policy.json")
	p := Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi", "verify": "pi-judge"},
		Backends: map[string]Backend{
			"pi":       {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
			"pi-judge": {Adapter: "pi-judge", Model: "model-judge", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, p), ref

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat = CASE WHEN item_id = 'item-a' THEN 'verify' ELSE 'build' END`); err != nil {
			return err
		}
		return applyTerminal(tx, run.Record{
			RunID:      "run-item-a-a1",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "pi-judge",
			State:      run.StateFailedToStart,
			StartedAt:  "2026-11-22T05:20:00Z",
			FinishedAt: "2026-11-22T05:20:01Z",
		})
	})

	// Remove pi-judge and map verify back to pi.
	p.Seats["verify"] = "pi"
	delete(p.Backends, "pi-judge")
	savePolicyFile(t, ref, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()

	var result LoopResult
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("loop exited after policy removed backend: %v", err)
	}

	select {
	case req := <-steps.requests:
		if req.ItemID != "item-b" || req.Backend != "pi" {
			t.Fatalf("unexpected dispatch: %+v", req)
		}
	case <-time.After(hangGuard):
		t.Fatal("other item did not dispatch")
	}
	select {
	case extra := <-steps.requests:
		t.Fatalf("item-a fell back instead of holding: %+v", extra)
	default:
	}

	held := planFor(t, l, "item-a")
	if held.Action != ActionHold || !strings.HasPrefix(held.Reason, ReasonBackendMissing+": ") ||
		!strings.Contains(held.Reason, "verify") || !strings.Contains(held.Reason, "pi-judge") ||
		held.Seat != "verify" || held.DeferredBy != "pi-judge" || held.RunRequest != nil {
		t.Fatalf("unexpected hold decision for item-a: %+v", held)
	}
}

func TestPolicyChangeRemovingDesignBackendHoldsConflictAndContinuesLoop(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	ref := filepath.Join(f.root, "policy.json")
	p := Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi", "design": "pi-design"},
		Backends: map[string]Backend{
			"pi":        {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
			"pi-design": {Adapter: "pi-design", Model: "model-design", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, p), ref

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat = 'build', state = CASE WHEN item_id = 'item-a' THEN 'blocked' ELSE 'in-queue' END`); err != nil {
			return err
		}
		if err := item.RecordConflict(tx, item.Conflict{
			ConflictID:  "conflict-1",
			ItemID:      "item-a",
			RunID:       "run-item-a-a0",
			Text:        "needs design",
			Status:      "raised",
			WaitingOn:   "design",
			DesignRunID: "run-item-a-a1",
			BodyRef:     "docs/design.md",
		}, l.cfg.Now().UTC()); err != nil {
			return err
		}
		return applyTerminal(tx, run.Record{
			RunID:      "run-item-a-a1",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "pi-design",
			State:      run.StateFailedToStart,
			StartedAt:  "2026-11-22T05:20:00Z",
			FinishedAt: "2026-11-22T05:20:01Z",
		})
	})

	p.Seats["design"] = "pi"
	delete(p.Backends, "pi-design")
	savePolicyFile(t, ref, p)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		close(steps.release)
		for len(l.active) > 0 {
			c := <-l.completions
			delete(l.active, c.id)
		}
	}()

	var result LoopResult
	if _, err := l.pass(ctx, &result); err != nil {
		t.Fatalf("loop exited after policy removed design backend: %v", err)
	}

	select {
	case req := <-steps.requests:
		if req.ItemID != "item-b" || req.Backend != "pi" {
			t.Fatalf("unexpected dispatch: %+v", req)
		}
	case <-time.After(hangGuard):
		t.Fatal("other item did not dispatch")
	}

	held := planFor(t, l, "item-a")
	if held.Action != ActionHold || !strings.HasPrefix(held.Reason, ReasonBackendMissing+": ") ||
		!strings.Contains(held.Reason, "design") || !strings.Contains(held.Reason, "pi-design") ||
		held.Seat != "design" || held.DeferredBy != "pi-design" || held.RunRequest != nil {
		t.Fatalf("unexpected hold decision for item-a: %+v", held)
	}
}
