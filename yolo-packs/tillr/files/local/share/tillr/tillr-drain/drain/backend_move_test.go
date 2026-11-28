package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestProfileChangeBetweenBuildAndVerifyDispatchesVerifyOnCurrentBackend(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	ref := filepath.Join(f.root, "policy.json")
	p := Policy{
		DefaultBackend: "claude",
		Seats:          map[string]string{"build": "claude", "verify": "claude"},
		Backends: map[string]Backend{
			"claude": {Adapter: "claude", Model: "model-claude", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, p), ref

	// Seed a commit built by the earlier build step on claude in item-a's lane.
	laneID := "item-a"
	laneWT := l.lanePath(laneID)
	runGit(t, f.root, "worktree", "add", "--detach", laneWT, TrunkRef)
	baseSHA := runGit(t, laneWT, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(laneWT, "built.txt"), "built on claude\n")
	runGit(t, laneWT, "add", "built.txt")
	runGit(t, laneWT, "commit", "-qm", "feat: build work")
	builtSHA := runGit(t, laneWT, "rev-parse", "HEAD")

	wf := &Workflow{
		Name:    "build-then-verify",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "build", Seat: "build", Access: "write"},
			{ID: "verify", Seat: "verify", Access: "read_only"},
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat = 'build' WHERE item_id = 'item-a'`); err != nil {
			return err
		}
		// Build finished on claude and advanced the workflow to verify, stamping
		// verify's pending request with claude under the old profile.
		req := l.cfg.Policy.Request("item-a", laneID, "run-item-a-a1-n2", "claude")
		req.NodeID = "verify"
		d := Decision{
			ItemID:      "item-a",
			Attempt:     1,
			Action:      ActionDispatch,
			Reason:      "workflow-next-node",
			From:        "running",
			To:          "running",
			LaneID:      laneID,
			Seat:        "verify",
			Workflow:    wf,
			NodeIndex:   1,
			NodeBaseSHA: baseSHA,
			RunRequest:  req,
		}
		stampDecisionRequest(d, req)
		identifyDecision(&d)
		return appendEffect(tx, nodeAdvancedKind, req.RunID, pendingWork{
			Decision:    d,
			BaseSHA:     baseSHA,
			Ready:       true,
			ParentRunID: "run-item-a-a1-n1",
		}, l.cfg.Now().UTC())
	})

	// Switch the profile to one with no Claude backends before verify runs.
	p = Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi", "verify": "pi"},
		Backends: map[string]Backend{
			"pi": {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
		},
	}
	savePolicyFile(t, ref, p)

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps

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
		t.Fatalf("pass after profile switch: %v", err)
	}

	select {
	case req := <-steps.requests:
		if req.ItemID != "item-a" || req.NodeID != "verify" || req.Backend != "pi" {
			t.Fatalf("verify dispatch = %+v, want item-a verify on pi", req)
		}
	case c := <-l.completions:
		t.Fatalf("verify completed without running: %+v", c.outcome)
	case <-time.After(hangGuard):
		t.Fatalf("verify did not dispatch after profile switch; result=%+v active=%d plan=%+v", result, len(l.active), planFor(t, l, "item-a"))
	}

	// The work already built in lane-1 must still be intact.
	if gotSHA := runGit(t, laneWT, "rev-parse", "HEAD"); gotSHA != builtSHA {
		t.Fatalf("lane-1 HEAD = %s, want built commit %s", gotSHA, builtSHA)
	}
	raw, err := os.ReadFile(filepath.Join(laneWT, "built.txt"))
	if err != nil || string(raw) != "built on claude\n" {
		t.Fatalf("built.txt = %q (err=%v), want preserved build output", string(raw), err)
	}

	moves := readBackendMoves(t, f, "item-a")
	if len(moves) != 1 || moves[0].ItemID != "item-a" || moves[0].FromBackend != "claude" || moves[0].ToBackend != "pi" || moves[0].Seat != "verify" {
		t.Fatalf("backend moves = %+v, want one move claude -> pi for verify", moves)
	}
}

func TestQuotaPausedRunContinuesOnCurrentBackendAfterProfileChange(t *testing.T) {
	for _, removeOld := range []bool{true, false} {
		name := "old-backend-still-shut"
		if removeOld {
			name = "old-backend-removed"
		}
		t.Run(name, func(t *testing.T) {
			f, l, steps, d, rec := quotaContinuationFixture(t, true)
			var result LoopResult
			// Record the quota hold on alpha first while alpha is still active.
			out := l.settle(context.Background(), d, rec, logTail(rec.ArtifactRefs), l.laneBase(d.LaneID), "tick", steps.at, &result, &PassTiming{}, nil, false)
			if out.err != nil || out.stop != "" {
				t.Fatalf("initial settle: %+v", out)
			}
			if len(steps.requests) != 1 {
				t.Fatalf("expected initial hold on alpha, got %d requests", len(steps.requests))
			}

			// Switch the profile so the item's seat now maps to open beta.
			l.cfg.Policy.DefaultBackend = "beta"
			if removeOld {
				delete(l.cfg.Policy.Backends, "alpha")
			}

			// Without advancing the clock past alpha's quota reset and with no
			// operator action, the item must continue on beta in a fresh session.
			if got := planFor(t, l, "item-a"); got.Action != ActionSettle {
				t.Fatalf("plan after profile change = %+v, want ActionSettle", got)
			}
			settleContinuation(t, l, &result)
			if len(steps.requests) != 2 {
				t.Fatalf("requests = %+v, want continuation on beta", steps.requests)
			}
			next := steps.requests[1]
			if next.RunID != d.RunRequest.RunID || next.LaneID != d.LaneID || next.Backend != "beta" || !next.Resume || !next.FreshSession {
				t.Fatalf("continuation request = %+v, want beta fresh session on same run/lane", next)
			}
			// Verify the files written by the earlier alpha run were preserved and committed
			// before the stub land resets the lane.
			for _, file := range []string{"one.txt", "two.txt"} {
				if out := runGit(t, l.lanePath(d.LaneID), "show", "HEAD:"+file); out == "" {
					t.Fatalf("lane missing preserved file %s", file)
				}
			}
			for i := 0; i < 3; i++ {
				settleContinuation(t, l, &result)
			}
			if steps.gates != 1 || steps.lands != 1 || result.InfraFailures != 0 {
				t.Fatalf("gates=%d lands=%d result=%+v", steps.gates, steps.lands, result)
			}
			moves := readBackendMoves(t, f, "item-a")
			if len(moves) != 1 || moves[0].ItemID != "item-a" || moves[0].FromBackend != "alpha" || moves[0].ToBackend != "beta" {
				t.Fatalf("backend moves = %+v, want one move alpha -> beta", moves)
			}
		})
	}
}

func TestQuotaHeldItemMovesToCurrentBackendAfterProfileChange(t *testing.T) {
	for _, removeOld := range []bool{true, false} {
		name := "old-backend-still-shut"
		if removeOld {
			name = "old-backend-removed"
		}
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Lanes = []string{"lane-1"}
			ref := filepath.Join(f.root, "policy.json")
			p := Policy{
				DefaultBackend: "claude",
				Seats:          map[string]string{"build": "claude"},
				Backends: map[string]Backend{
					"claude": {Adapter: "claude", Model: "model-claude", TimeoutSeconds: 60, Quota: []string{"fixture-quota"}},
					"pi":     {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
				},
			}
			l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, p), ref

			at := l.cfg.Now().UTC()
			reportPath := filepath.Join(f.root, "quota-report.md")
			writeForkFile(t, reportPath, fmt.Sprintf("# run report\nrate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: %d\n---\n", at.Add(2*time.Hour).Unix()))
			exitCode := 1
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET seat = 'build' WHERE item_id = 'item-a'`); err != nil {
					return err
				}
				if err := applyTerminal(tx, run.Record{
					RunID:        "run-item-a-a1",
					ItemID:       "item-a",
					LaneID:       "lane-1",
					Backend:      "claude",
					State:        run.StateExited,
					ExitCode:     &exitCode,
					StartedAt:    at.Add(-time.Minute).Format(time.RFC3339),
					FinishedAt:   at.Add(-30 * time.Second).Format(time.RFC3339),
					ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
				}); err != nil {
					return err
				}
				windows := `[{"kind":"five_hour","fraction":1,"reset_at":"` + at.Add(2*time.Hour).Format(time.RFC3339) + `"}]`
				_, err := tx.Exec(`INSERT OR REPLACE INTO backend_quota(backend,command,observed_at,reserve,windows,error) VALUES(?,?,?,?,?,'')`, "claude", `["fixture-quota"]`, at.UnixMilli(), 0.2, windows)
				return err
			})

			// Under the old profile, item-a is held as backend-window on claude.
			held := planFor(t, l, "item-a")
			if held.Action != ActionHold || held.Reason != ReasonBackendWindow || held.DeferredBy != "claude" {
				t.Fatalf("before profile change = %+v, want hold/backend-window on claude", held)
			}

			// Switch the profile so build maps to open pi.
			p.DefaultBackend = "pi"
			p.Seats["build"] = "pi"
			if removeOld {
				delete(p.Backends, "claude")
			}
			savePolicyFile(t, ref, p)

			steps := &policyRunSteps{requests: make(chan run.Request, 2), release: make(chan struct{})}
			l.cfg.Steps = steps
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
				t.Fatalf("pass after profile change: %v", err)
			}
			select {
			case req := <-steps.requests:
				if req.ItemID != "item-a" || req.Backend != "pi" {
					t.Fatalf("dispatched = %+v, want item-a on pi", req)
				}
			case <-time.After(hangGuard):
				t.Fatal("item-a did not dispatch on pi after profile change")
			}
			moves := readBackendMoves(t, f, "item-a")
			if len(moves) != 1 || moves[0].ItemID != "item-a" || moves[0].FromBackend != "claude" || moves[0].ToBackend != "pi" {
				t.Fatalf("backend moves = %+v, want one move claude -> pi", moves)
			}
		})
	}
}

func TestProfileChangeWithRecordedVerifyRunDispatchesOnNewBackendWithoutRefusal(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	ref := filepath.Join(f.root, "policy.json")
	p := Policy{
		DefaultBackend: "claude",
		Seats:          map[string]string{"build": "claude", "verify": "claude"},
		Backends: map[string]Backend{
			"claude": {Adapter: "claude", Model: "model-claude", TimeoutSeconds: 60},
		},
	}
	l.cfg.Policy, l.cfg.PolicyRef = savePolicyFile(t, ref, p), ref

	// Seed a built commit in item-a's lane worktree.
	laneID := "item-a"
	laneWT := l.lanePath(laneID)
	runGit(t, f.root, "worktree", "add", "--detach", laneWT, TrunkRef)
	baseSHA := runGit(t, laneWT, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(laneWT, "built.txt"), "built on claude\n")
	runGit(t, laneWT, "add", "built.txt")
	runGit(t, laneWT, "commit", "-qm", "feat: build work")
	builtSHA := runGit(t, laneWT, "rev-parse", "HEAD")

	wf := &Workflow{
		Name:    "build-then-verify",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "build", Seat: "build", Access: "write"},
			{ID: "verify", Seat: "verify", Access: "read_only"},
		},
	}
	const runID = "run-item-a-a1-n2"
	at := l.cfg.Now().UTC()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat = 'build' WHERE item_id = 'item-a'`); err != nil {
			return err
		}
		// Verify was previously recorded on claude in seat_dispatches and is
		// ready to dispatch again with the same run id after a profile switch.
		req := l.cfg.Policy.Request("item-a", laneID, runID, "claude")
		req.NodeID = "verify"
		d := Decision{
			ItemID:      "item-a",
			Attempt:     1,
			Action:      ActionDispatch,
			Reason:      "workflow-next-node",
			From:        "running",
			To:          "running",
			LaneID:      laneID,
			Seat:        "verify",
			Workflow:    wf,
			NodeIndex:   1,
			NodeBaseSHA: builtSHA,
			RunRequest:  req,
		}
		stampDecisionRequest(d, req)
		identifyDecision(&d)
		if err := recordSeatDispatch(tx, d); err != nil {
			return err
		}
		return appendEffect(tx, nodeAdvancedKind, runID, pendingWork{
			Decision:    d,
			BaseSHA:     baseSHA,
			Ready:       true,
			ParentRunID: "run-item-a-a1-n1",
		}, at)
	})

	// Switch the profile to one where verify maps to pi and claude is absent.
	p = Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"build": "pi", "verify": "pi"},
		Backends: map[string]Backend{
			"pi": {Adapter: "pi", Model: "model-pi", TimeoutSeconds: 60},
		},
	}
	savePolicyFile(t, ref, p)

	steps := &policyRunSteps{requests: make(chan run.Request, 4), release: make(chan struct{})}
	l.cfg.Steps = steps

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
		t.Fatalf("pass after profile switch with recorded verify run: %v", err)
	}

	select {
	case req := <-steps.requests:
		if req.ItemID != "item-a" || req.NodeID != "verify" || req.Backend != "pi" || req.RunID != runID {
			t.Fatalf("verify dispatch = %+v, want item-a verify %s on pi", req, runID)
		}
	case c := <-l.completions:
		t.Fatalf("verify completed without running: %+v", c.outcome)
	case <-time.After(hangGuard):
		t.Fatalf("verify did not dispatch after profile switch; result=%+v active=%d plan=%+v", result, len(l.active), planFor(t, l, "item-a"))
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		var seat, backend string
		if err := r.QueryRow(`SELECT seat, backend FROM seat_dispatches WHERE run_id = ?`, runID).Scan(&seat, &backend); err != nil {
			return err
		}
		if seat != "verify" || backend != "pi" {
			t.Errorf("seat_dispatches(%s) = (%s, %s), want (verify, pi)", runID, seat, backend)
		}
		var declines int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = 'item-a'`, dispatchDeclinedKind).Scan(&declines); err != nil {
			return err
		}
		if declines != 0 {
			t.Errorf("dispatch declines = %d, want 0", declines)
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if f, ok := faults["item-a"]; ok {
			t.Errorf("unexpected item fault on item-a: %+v", f)
		}
		return nil
	})

	moves := readBackendMoves(t, f, "item-a")
	if len(moves) != 1 || moves[0].ItemID != "item-a" || moves[0].RunID != runID || moves[0].FromBackend != "claude" || moves[0].ToBackend != "pi" || moves[0].Seat != "verify" {
		t.Fatalf("backend moves = %+v, want one move claude -> pi for verify run %s", moves, runID)
	}
}
