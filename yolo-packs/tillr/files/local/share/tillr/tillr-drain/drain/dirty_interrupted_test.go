package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type dirtyContinuationCase struct {
	endCause string
	quickWay bool
}

type dirtyWorkflowContinuationSteps struct {
	*restartWorkflowSteps
	cases         []dirtyContinuationCase
	briefs        []string
	backends      []string
	preservedSeen map[string]string
	savedCounts   []int
}

func (s *dirtyWorkflowContinuationSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs = append(s.briefs, string(brief))
	s.backends = append(s.backends, req.Backend)

	fixtureRead(s.t, s.f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		active := 0
		for _, p := range saved {
			if !p.Pruned && p.ItemID == req.ItemID {
				active++
			}
		}
		s.savedCounts = append(s.savedCounts, active)
		return nil
	})

	idx := len(s.briefs) - 1
	if idx >= len(s.cases) {
		// A fresh plan turn that commits must opt into the quick way again;
		// the interrupted run's marker belongs to its own run directory.
		if req.Seat == "plan" && len(s.cases) > 0 && s.cases[len(s.cases)-1].quickWay {
			writeForkFile(s.t, filepath.Join(nodeOutputsDir(s.f.root, req.RunID), quickWayFile), "do-it-now\n")
		}
		return s.restartWorkflowSteps.Run(ctx, req)
	}

	s.runs = append(s.runs, req)
	tc := s.cases[idx]
	if tc.quickWay {
		writeForkFile(s.t, filepath.Join(nodeOutputsDir(s.f.root, req.RunID), quickWayFile), "do-it-now\n")
	}
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	content := "dirty work from " + req.RunID
	if idx == 1 {
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/committed-"+req.RunID+".txt"), "committed-"+req.RunID+"\n")
		runGit(s.t, target, "add", "tools/item-a/committed-"+req.RunID+".txt")
		runGit(s.t, target, "commit", "-qm", "partial commit before loop")
	}
	writeForkFile(s.t, filepath.Join(target, "base.txt"), content+"\n")
	writeForkFile(s.t, filepath.Join(target, "tools/item-a/uncommitted-"+req.RunID+".txt"), content+"\n")
	if s.preservedSeen == nil {
		s.preservedSeen = map[string]string{}
	}
	s.preservedSeen[req.RunID] = content

	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
		EndCause: tc.endCause, ArtifactRefs: []run.ArtifactRef{},
	}
	switch tc.endCause {
	case run.EndRepeatingCall:
		rec.ExitCode = countPtr(0)
		rec.RepeatingCall = &run.RepeatingCall{Tool: "read", Args: repeatedArgs, ArgsDigest: "digest", Streak: 40}
	case run.EndSignal:
		rec.ExitCode = countPtr(143)
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}

func TestRepeatingAndSignaledWorkflowNodesWithDirtyWorktreePreserveAndContinue(t *testing.T) {
	t.Run("repeating_call", func(t *testing.T) {
		f, l, restart := restartGraphFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
			return err
		})
		l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
		l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}
		steps := &dirtyWorkflowContinuationSteps{
			restartWorkflowSteps: restart,
			cases: []dirtyContinuationCase{
				{endCause: run.EndRepeatingCall},
				{endCause: run.EndRepeatingCall},
			},
		}
		l.cfg.Steps = steps

		result := &LoopResult{}
		for i := 0; i < 5; i++ {
			graphPass(t, l, result)
		}
		if result.Rejected != 0 || result.RefusedSteps[RefusalFence] != 0 {
			t.Fatalf("dirty repeating-call node refused as escape: %+v", result)
		}
		wantRuns := []string{"run-item-a-a1-n1", "run-item-a-a1-n1-fresh"}
		wantBackends := []string{"fixture", "fixture"}
		if len(steps.runs) != len(wantRuns) {
			t.Fatalf("runs = %+v, want %v", steps.runs, wantRuns)
		}
		for i, want := range wantRuns {
			if steps.runs[i].RunID != want || steps.backends[i] != wantBackends[i] {
				t.Fatalf("run %d = %s on %s, want %s on %s", i, steps.runs[i].RunID, steps.backends[i], want, wantBackends[i])
			}
			if i == 0 {
				continue
			}
			prev := wantRuns[i-1]
			ref := preservedPrefix + prev
			if got := runGit(t, f.root, "show", ref+":base.txt"); got != steps.preservedSeen[prev] {
				t.Fatalf("preserved %s:base.txt = %q, want %q", ref, got, steps.preservedSeen[prev])
			}
			if got := runGit(t, f.root, "show", ref+":tools/item-a/uncommitted-"+prev+".txt"); got != steps.preservedSeen[prev] {
				t.Fatalf("preserved untracked file = %q, want %q", got, steps.preservedSeen[prev])
			}
			if !strings.Contains(steps.briefs[i], ref) || !strings.Contains(steps.briefs[i], "## Your previous session") {
				t.Fatalf("brief %d missing preserved ref %s:\n%s", i, ref, steps.briefs[i])
			}
			if steps.savedCounts[i] != i {
				t.Fatalf("active preserved record count on run %d = %d, want %d (duplicate preservation events?)", i, steps.savedCounts[i], i)
			}
		}
		prev := wantRuns[1]
		if got := runGit(t, f.root, "show", "refs/tillr/runs/"+prev+":tools/item-a/committed-"+prev+".txt"); got != "committed-"+prev {
			t.Fatalf("held run's partial commit = %q, want %q", got, "committed-"+prev)
		}
		if d := mustPlan(t, l)[0].decision; d.Reason != ReasonRepeatingCall || d.Action != ActionHold {
			t.Fatalf("workflow not held after fresh session repeated: %+v", d)
		}
	})

	t.Run("signal", func(t *testing.T) {
		f, l, restart := restartGraphFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
			return err
		})
		l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
		l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}
		steps := &dirtyWorkflowContinuationSteps{
			restartWorkflowSteps: restart,
			cases: []dirtyContinuationCase{
				{endCause: run.EndSignal},
			},
		}
		l.cfg.Steps = steps

		result := &LoopResult{}
		for i := 0; i < 3; i++ {
			graphPass(t, l, result)
		}
		if result.Rejected != 0 || result.InfraFailures != 0 || result.RefusedSteps[RefusalFence] != 0 {
			t.Fatalf("dirty signaled node refused or counted as infra: %+v", result)
		}
		if len(steps.runs) < 2 || steps.runs[0].RunID != "run-item-a-a1-n1" || steps.runs[1].RunID != "run-item-a-a1-n1-interrupted" {
			t.Fatalf("runs = %+v, want run-item-a-a1-n1 then run-item-a-a1-n1-interrupted", steps.runs)
		}
		if steps.backends[1] != "fixture" {
			t.Fatalf("replacement backend = %s, want fixture", steps.backends[1])
		}
		ref := preservedPrefix + "run-item-a-a1-n1"
		want := steps.preservedSeen["run-item-a-a1-n1"]
		if got := runGit(t, f.root, "show", ref+":base.txt"); got != want {
			t.Fatalf("preserved %s:base.txt = %q, want %q", ref, got, want)
		}
		if got := runGit(t, f.root, "show", ref+":tools/item-a/uncommitted-run-item-a-a1-n1.txt"); got != want {
			t.Fatalf("preserved untracked = %q, want %q", got, want)
		}
		if !strings.Contains(steps.briefs[1], "Earlier work exists at "+ref) {
			t.Fatalf("restarted node brief missing preserved ref %s:\n%s", ref, steps.briefs[1])
		}
		if steps.savedCounts[1] != 1 {
			t.Fatalf("active preserved record count = %d, want 1", steps.savedCounts[1])
		}

		for i := 0; i < 6 && result.Landed == 0; i++ {
			graphPass(t, l, result)
		}
		if result.Landed != 1 || result.Rejected != 0 {
			t.Fatalf("signaled workflow did not finish landing: %+v", result)
		}
	})

	t.Run("quick_way_plan_node", func(t *testing.T) {
		f, l, ws := workflowLoopFixture(t, "")
		l.pollOnly = true // Inspect preserved work before a successful landing prunes it.
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), `{"name":"scout-then-build","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"plan","to_node":"work"}]}`)
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-qm", "plan then work")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE items SET allowed_paths='["tools/item-a/","base.txt"]' WHERE item_id='item-a'`)
			return err
		})
		l.cfg.Policy.Seats["plan"] = "fixture"
		l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
		l.cfg.Policy.SeatLists = map[string][]string{"plan": {"fixture", "alternate"}, "build": {"fixture", "alternate"}}
		steps := &dirtyWorkflowContinuationSteps{
			restartWorkflowSteps: &restartWorkflowSteps{workflowSteps: ws},
			cases: []dirtyContinuationCase{
				{endCause: run.EndRepeatingCall, quickWay: true},
			},
		}
		l.cfg.Steps = steps

		result := &LoopResult{}
		for i := 0; i < 3; i++ {
			graphPass(t, l, result)
		}
		if result.Rejected != 0 || result.RefusedSteps[RefusalFence] != 0 {
			t.Fatalf("dirty quick-way plan node refused as escape: %+v", result)
		}
		ref := preservedPrefix + "run-item-a-a1-n1"
		if got := runGit(t, f.root, "show", ref+":base.txt"); got != steps.preservedSeen["run-item-a-a1-n1"] {
			t.Fatalf("preserved %s:base.txt = %q", ref, got)
		}
		if len(steps.briefs) < 2 || !strings.Contains(steps.briefs[1], ref) {
			t.Fatalf("restarted plan brief missing %s", ref)
		}
	})

	t.Run("single_run_repeating_call_and_signal", func(t *testing.T) {
		f, l, ws := workflowLoopFixture(t, "")
		ws.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE items SET workflow='' WHERE item_id='item-a'`)
			return err
		})
		l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
		l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}

		var briefs []string
		var runIDs []string
		var savedCounts []int
		var seenRefs []string
		custom := &singleRunDirtyContinuationSteps{
			workflowSteps: ws,
			onRun: func(req run.Request, brief string, activeSaved int) {
				if len(runIDs) > 0 {
					prev := runIDs[len(runIDs)-1]
					ref := preservedPrefix + prev
					if got := runGit(t, f.root, "show", ref+":tools/item-a/dirty-"+prev+".txt"); got != "dirty-"+prev {
						t.Errorf("preserved %s = %q, want %q", ref, got, "dirty-"+prev)
					}
					seenRefs = append(seenRefs, ref)
				}
				runIDs = append(runIDs, req.RunID)
				briefs = append(briefs, brief)
				savedCounts = append(savedCounts, activeSaved)
			},
		}
		l.cfg.Steps = custom

		result, err := l.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Rejected != 0 || result.RefusedSteps[RefusalFence] != 0 {
			t.Fatalf("single-run dirty repeating/signal refused: %+v", result)
		}
		if len(runIDs) != 3 || len(seenRefs) != 2 {
			t.Fatalf("single-run IDs = %v, want 3 runs", runIDs)
		}
		if !strings.Contains(briefs[1], seenRefs[0]) {
			t.Fatalf("second brief missing %s:\n%s", seenRefs[0], briefs[1])
		}
		if !strings.Contains(briefs[2], "Earlier work exists at "+seenRefs[1]) {
			t.Fatalf("third brief missing %s:\n%s", seenRefs[1], briefs[2])
		}
		if savedCounts[1] != 1 || savedCounts[2] != 2 {
			t.Fatalf("active preserved counts = %v, want [0 1 2]", savedCounts)
		}
		if result.Landed != 1 || result.Rejected != 0 {
			t.Fatalf("single-run continuation did not land: %+v", result)
		}
	})
}

type singleRunDirtyContinuationSteps struct {
	*workflowSteps
	count int
	onRun func(req run.Request, brief string, activeSaved int)
}

func (s *singleRunDirtyContinuationSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	active := 0
	fixtureRead(s.t, s.f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		for _, p := range saved {
			if !p.Pruned && p.ItemID == req.ItemID {
				active++
			}
		}
		return nil
	})
	s.onRun(req, string(brief), active)
	s.count++

	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	switch s.count {
	case 1:
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/dirty-"+req.RunID+".txt"), "dirty-"+req.RunID+"\n")
		rec := run.Record{
			RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
			State: run.StateExited, ExitCode: countPtr(0), EndCause: run.EndRepeatingCall,
			StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
			RepeatingCall: &run.RepeatingCall{Tool: "read", Args: repeatedArgs, ArgsDigest: "digest", Streak: 40},
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "", nil
	case 2:
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/dirty-"+req.RunID+".txt"), "dirty-"+req.RunID+"\n")
		rec := run.Record{
			RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
			State: run.StateExited, ExitCode: countPtr(143), EndCause: run.EndSignal,
			StartedAt: "2026-11-19T00:00:02Z", FinishedAt: "2026-11-19T00:00:03Z",
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "", nil
	default:
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/final.txt"), "done\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "finish")
		report := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
		writeForkFile(s.t, report, "RESULT: done\n")
		rec := run.Record{
			RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
			State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: "2026-11-19T00:00:04Z", FinishedAt: "2026-11-19T00:00:05Z",
			ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}},
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "RESULT: done\n", nil
	}
}

func TestRestartInterruptedNodeAndRetryWorkflowNodePreserveWorkBeforeRestart(t *testing.T) {
	t.Run("restartInterruptedNode", func(t *testing.T) {
		f, l, _ := restartGraphFixture(t)
		ctx := context.Background()
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		p := mustPlan(t, l)[0]
		if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		d := p.decision
		if _, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		work := pendingForTest(t, f, d.ItemID)
		d = work.Decision
		target := l.laneTarget(d.LaneID)
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(target, "base.txt"), "interrupted tracked edit\n")
		writeForkFile(t, filepath.Join(target, "tools/item-a/interrupted-new.txt"), "interrupted untracked edit\n")

		rec := run.Record{
			RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
			State: run.StateExited, ExitCode: countPtr(143), EndCause: run.EndSignal,
			StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
		}
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

		if err := l.restartInterruptedNode(ctx, d, work.BaseSHA, rec); err != nil {
			t.Fatal(err)
		}
		ref := preservedPrefix + rec.RunID
		if got := runGit(t, f.root, "show", ref+":base.txt"); got != "interrupted tracked edit" {
			t.Fatalf("preserved %s:base.txt before restart = %q, want %q", ref, got, "interrupted tracked edit")
		}
		if got := runGit(t, f.root, "show", ref+":tools/item-a/interrupted-new.txt"); got != "interrupted untracked edit" {
			t.Fatalf("preserved %s untracked file before restart = %q", ref, got)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			saved, err := ReadPreservedWork(r)
			if err != nil {
				return err
			}
			if len(saved) != 1 || saved[0].Ref != ref {
				t.Fatalf("preserved records immediately after restartInterruptedNode = %+v", saved)
			}
			return nil
		})

		restarted := pendingForTest(t, f, d.ItemID)
		if _, err := l.stageDispatches(ctx, []Decision{restarted.Decision}, restarted.BaseSHA, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		brief, err := os.ReadFile(filepath.Join(f.laneRoot, d.LaneID, BriefFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(brief), "Earlier work exists at "+ref) {
			t.Fatalf("restarted brief missing preserved ref %s:\n%s", ref, string(brief))
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			saved, err := ReadPreservedWork(r)
			if err != nil {
				return err
			}
			if len(saved) != 1 {
				t.Fatalf("restaging duplicated preserved records: %+v", saved)
			}
			return nil
		})
	})

	t.Run("retryWorkflowNode", func(t *testing.T) {
		f, l, _ := restartGraphFixture(t)
		l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
		l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}

		ctx := context.Background()
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		p := mustPlan(t, l)[0]
		if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		d := p.decision
		if _, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		work := pendingForTest(t, f, d.ItemID)
		d = work.Decision
		target := l.laneTarget(d.LaneID)
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(target, "base.txt"), "faulted tracked edit\n")
		writeForkFile(t, filepath.Join(target, "tools/item-a/faulted-new.txt"), "faulted untracked edit\n")

		rec := run.Record{
			RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
			State: run.StateExited, ExitCode: countPtr(2),
			StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
		}
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

		if err := l.holdFailedWorkflowNode(ctx, d, work.BaseSHA, rec, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		ref := preservedPrefix + rec.RunID
		if got := runGit(t, f.root, "show", ref+":base.txt"); got != "faulted tracked edit" {
			t.Fatalf("preserved %s:base.txt before failover restart = %q, want %q", ref, got, "faulted tracked edit")
		}
		if got := runGit(t, f.root, "show", ref+":tools/item-a/faulted-new.txt"); got != "faulted untracked edit" {
			t.Fatalf("preserved %s untracked file before failover restart = %q", ref, got)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			saved, err := ReadPreservedWork(r)
			if err != nil {
				return err
			}
			if len(saved) != 1 || saved[0].Ref != ref {
				t.Fatalf("preserved records immediately after retryWorkflowNode = %+v", saved)
			}
			return nil
		})

		restarted := pendingForTest(t, f, d.ItemID)
		if restarted.Decision.RunRequest.Backend != "fixture" {
			t.Fatalf("restart backend = %s, want fixture", restarted.Decision.RunRequest.Backend)
		}
		if d := mustPlan(t, l)[0].decision; d.Action != ActionHold || d.Reason != ReasonBackendClosed {
			t.Fatalf("failed node not held before backend recovery: %+v", d)
		}
		if err := ProbeBackend(ctx, f.storePath, f.root, "fixture", []string{"/bin/true"}); err != nil {
			t.Fatal(err)
		}
		if _, err := l.stageDispatches(ctx, []Decision{restarted.Decision}, restarted.BaseSHA, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		brief, err := os.ReadFile(filepath.Join(f.laneRoot, d.LaneID, BriefFile))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(brief), "Earlier work exists at "+ref) {
			t.Fatalf("failover brief missing preserved ref %s:\n%s", ref, string(brief))
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			saved, err := ReadPreservedWork(r)
			if err != nil {
				return err
			}
			if len(saved) != 1 {
				t.Fatalf("restaging duplicated preserved records: %+v", saved)
			}
			return nil
		})
	})
}

type dirtyNormalExitSteps struct {
	*workflowSteps
	mu        sync.Mutex
	requests  []run.Request
	dirtyPath string
}

func (s *dirtyNormalExitSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	// Each run commits its own file. A run that committed the same tree on the same parent
	// as the run before it, in the same second, would make the identical commit, and the
	// drain rightly reads an unchanged head as "no new commit" and continues the session.
	// What this test pins is a run that does commit, so its commit must always be new.
	committed := "tools/item-a/committed-" + req.RunID + ".txt"
	if err := os.WriteFile(filepath.Join(target, committed), []byte("committed\n"), 0o644); err != nil {
		return run.Record{}, "", err
	}
	dirtyPath := s.dirtyPath
	if dirtyPath == "" {
		dirtyPath = "tools/item-a/dirty-after-commit.txt"
	}
	if err := os.WriteFile(filepath.Join(target, dirtyPath), []byte("uncommitted leftover\n"), 0o644); err != nil {
		return run.Record{}, "", err
	}
	for _, args := range [][]string{{"add", committed}, {"commit", "-qm", "candidate commit", "--", committed}} {
		cmd := exec.Command("git", append([]string{"-C", target}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=tillr", "GIT_AUTHOR_EMAIL=tillr@example.invalid",
			"GIT_COMMITTER_NAME=tillr", "GIT_COMMITTER_EMAIL=tillr@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			// An error returned here fails the run at once; a t.Fatal from this goroutine
			// would leave the loop waiting forever.
			return run.Record{}, "", fmt.Errorf("git %v in %s: %v\n%s", args, target, err, out)
		}
	}

	report := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if req.NodeID != "" {
		report = filepath.Join(RunDir(s.f.root, req.RunID), "report.md")
	}
	writeForkFile(s.t, report, "RESULT: done\n")
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: countPtr(0), EndCause: run.EndNormalExit,
		StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", nil
}

func assertFenceEscapeRefusal(t *testing.T, f refusalLoopFixture, runID string) {
	t.Helper()
	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.EventsOfKind(0, RefusalKind)
		if err != nil {
			return err
		}
		for _, ev := range events {
			var payload struct {
				Step   string `json:"step"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				return err
			}
			if ev.ObjectID != nil && *ev.ObjectID == runID && payload.Step == RefusalFence &&
				strings.Contains(payload.Detail, "boundary-scope-escape: the candidate worktree is not clean, so the commit is not the change that was judged") {
				return nil
			}
		}
		t.Fatalf("missing boundary-scope-escape RefusalFence for %s", runID)
		return nil
	})
}

func TestNormalExitClaimingCommitWithDirtyWorktreeRefusesBoundaryScopeEscape(t *testing.T) {
	t.Run("workflow_write_node_outside_fence", func(t *testing.T) {
		f, l, ws := restartGraphFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
			return err
		})
		l.cfg.Steps = &dirtyNormalExitSteps{workflowSteps: ws.workflowSteps, dirtyPath: "outside-fence.txt"}

		result, err := l.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Rejected != 1 || result.RefusedSteps[RefusalFence] != 1 || result.Landed != 0 {
			t.Fatalf("expected normal-exit dirty workflow commit to be refused as RefusalFence, got %+v", result)
		}
		assertFenceEscapeRefusal(t, f, "run-item-a-a1-n1")
	})

	t.Run("single_run", func(t *testing.T) {
		// What decides continuing in place against refusing at the fence is settle's
		// `noNewCommit`: the head equals the staged base, the design continuation head, or
		// the head the item's previous worker session ended on. A run that exits normally
		// on a head none of those names is a candidate, and its dirty fenced file is refused
		// as boundary-scope-escape; it is never continued. The step runner gives every run
		// a commit of its own, so the outcome is fixed: no run here is a continuation.
		f, l, ws := workflowLoopFixture(t, "")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE items SET workflow='' WHERE item_id='item-a'`)
			return err
		})
		steps := &dirtyNormalExitSteps{workflowSteps: ws}
		l.cfg.Steps = steps

		result, err := l.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.Rejected < 1 || result.RefusedSteps[RefusalFence] < 1 || result.Landed != 0 {
			t.Fatalf("expected normal-exit dirty single-run commit to be refused as RefusalFence, got %+v", result)
		}
		steps.mu.Lock()
		defer steps.mu.Unlock()
		for _, req := range steps.requests {
			if req.ResumePrompt != "" {
				t.Fatalf("run %s was continued in place (resume prompt %q); a run that committed must be refused at the fence", req.RunID, req.ResumePrompt)
			}
		}
		if len(steps.requests) != result.RefusedSteps[RefusalFence] {
			t.Fatalf("%d runs for %d fence refusals: every run must end at the fence, none by continuation", len(steps.requests), result.RefusedSteps[RefusalFence])
		}
		assertFenceEscapeRefusal(t, f, "run-item-a-a1")
	})
}
