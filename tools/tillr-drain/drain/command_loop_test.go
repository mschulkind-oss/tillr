package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type commandSteps struct {
	workflowSteps
	repairThird bool
}

func (s *commandSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if s.firstHead == "" {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer st.Close()
	if req.Backend == "command" {
		env, execErr := run.Exec(ctx, st, req, run.ExecOptions{LaneRoot: s.f.laneRoot, BackendsDir: filepath.Join(s.f.root, "absent-backends"), Now: time.Now()})
		if len(env.Payload) == 0 {
			return run.Record{}, "", execErr
		}
		var rec run.Record
		err = json.Unmarshal(env.Payload, &rec)
		return rec, logTail(rec.ArtifactRefs), err
	}
	contents := "built\n"
	if strings.HasSuffix(req.RunID, "-n4") || (s.repairThird && strings.HasSuffix(req.RunID, "-n3")) {
		contents = "recovered\n"
	}
	writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), contents)
	runGit(s.t, target, "add", ".")
	runGit(s.t, target, "commit", "-qm", "build")
	report := "RESULT: done\n"
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(path, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func commandLoopFixture(t *testing.T, mode, access string, commandFirst bool) (refusalLoopFixture, *Loop, *commandSteps) {
	f, l, _ := workflowLoopFixture(t, "")
	script := `import os, sys, subprocess, time
mode=sys.argv[1]
if mode=='write' or mode=='dirty' or mode=='escape-commit':
    open('tools/item-a/generated.txt','w').write('generated\n')
    if mode!='dirty':
        subprocess.run(['git','add','.'],check=True)
        subprocess.run(['git','commit','-qm','regenerate'],check=True)
if mode=='wall': time.sleep(10)
if mode=='report-pass': print('RESULT: failed')
if mode=='report-blocked-pass': print('RESULT: blocked')
if mode=='report-fail': print('RESULT: done')
sys.exit(1 if mode in ('fail','report-fail') else 2 if mode=='fault' else 0)
`
	scriptPath := filepath.Join(f.root, "tools/item-a/check.py")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, scriptPath, script)
	argv := []string{"python3", scriptPath, mode}
	if mode == "missing" {
		argv = []string{filepath.Join(f.root, "does-not-exist")}
	}
	raw, _ := json.Marshal(map[string]any{"check": CommandEntry{Argv: argv, Flags: map[string]string{}}})
	writeForkFile(t, filepath.Join(f.root, "config/tillr/commands.json"), string(raw))
	stage := map[string]any{"id": "check", "kind": "command", "command_ref": "check", "access": access}
	if mode == "wall" {
		stage["timeout_ms"] = 50
	}
	stages := []any{map[string]any{"id": "work", "kind": "agent", "seat": "build", "access": "write"}, stage}
	edges := []any{map[string]any{"from_node": "work", "to_node": "check"}}
	if !commandFirst && mode != "pass" && mode != "report-pass" && mode != "report-blocked-pass" && mode != "write" {
		stages = append(stages, map[string]any{"id": "after", "kind": "agent", "seat": "build", "access": "write"})
		edges = append(edges, map[string]any{"from_node": "check", "to_node": "after"})
	}
	if commandFirst {
		stages = []any{stage, stages[0]}
		edges = []any{map[string]any{"from_node": "check", "to_node": "work"}}
	}
	raw, _ = json.Marshal(map[string]any{"name": "scout-then-build", "version": 1, "stages": stages, "edges": edges})
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), string(raw))
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "command fixture")
	// Only this item participates; a sibling landing would obscure trunk invariance.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='retired' WHERE item_id='item-b'`)
		return err
	})
	steps := &commandSteps{workflowSteps: workflowSteps{t: t, f: f}}
	l.cfg.Steps = steps
	return f, l, steps
}

func TestCommandWorkflowOutcomesAndPreservation(t *testing.T) {
	for _, mode := range []string{"pass", "report-pass", "report-blocked-pass", "fail", "report-fail", "fault", "missing", "wall", "write", "dirty", "escape-commit"} {
		t.Run(mode, func(t *testing.T) {
			access := "read_only"
			if mode == "write" || mode == "dirty" {
				access = "write"
			}
			f, l, steps := commandLoopFixture(t, mode, access, false)
			base := runGit(t, f.root, "rev-parse", "HEAD")
			count := runGit(t, f.root, "rev-list", "--count", "HEAD")
			result := &LoopResult{}
			for i := 0; i < 6; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if len(steps.runs) != 2 || steps.runs[1].Backend != "command" {
				t.Fatalf("runs %+v", steps.runs)
			}
			good := mode == "pass" || mode == "report-pass" || mode == "report-blocked-pass" || mode == "write"
			if good {
				if steps.gates != 1 || result.Landed != 1 {
					t.Fatalf("gate %d result %+v", steps.gates, result)
				}
				if runGit(t, f.root, "rev-list", "--count", base+"..HEAD") != "1" {
					t.Fatal("not one landed commit")
				}
				if mode == "write" {
					for _, name := range []string{"result.txt", "generated.txt"} {
						if _, err := os.Stat(filepath.Join(f.root, "tools/item-a", name)); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else {
				if steps.gates != 0 || runGit(t, f.root, "rev-parse", "HEAD") != base || runGit(t, f.root, "rev-list", "--count", "HEAD") != count {
					t.Fatal("failed command gated or moved trunk")
				}
				want := "node-failed: check"
				if mode == "fault" || mode == "missing" || mode == "wall" {
					want = ReasonInfraExhausted
				}
				if mode == "dirty" || mode == "escape-commit" {
					want = ScopeEscape
				}
				found := false
				for _, p := range mustPlan(t, l) {
					if p.decision.ItemID == "item-a" {
						found = true
						if p.decision.Action != ActionHold || p.decision.Reason != want {
							t.Fatalf("want %s: %+v", want, p.decision)
						}
					}
				}
				if !found {
					t.Fatal("failed item missing from planner")
				}
				if blob := runGit(t, f.root, "show", "refs/tillr/runs/"+steps.runs[1].RunID+":tools/item-a/result.txt"); blob != "built" {
					t.Fatalf("builder not pinned: %s", blob)
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var backend, route string
				var usage int
				if err := tx.QueryRow(`SELECT backend,COALESCE(route,''), usage_tokens IS NOT NULL OR usage_cost_usd IS NOT NULL FROM runs WHERE run_id=?`, steps.runs[1].RunID).Scan(&backend, &route, &usage); err != nil {
					return err
				}
				if backend != "command" || route != "" || usage != 0 {
					t.Errorf("command metadata %s %s %d", backend, route, usage)
				}
				var n int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM backend_windows`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Errorf("quota windows %d", n)
				}
				return nil
			})
		})
	}
}

func TestCommandFirstIgnoresBackendQuota(t *testing.T) {
	f, l, steps := commandLoopFixture(t, "pass", "read_only", true)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES('fixture','weekly',1,'refused',?,'window')`, l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && (p.decision.RunRequest == nil || p.decision.RunRequest.Backend != "command" || p.decision.Action != ActionDispatch) {
			t.Fatalf("command quota-routed %+v", p.decision)
		}
	}
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.runs) != 1 || !strings.HasSuffix(steps.runs[0].RunID, "-n1") {
		t.Fatalf("runs %+v", steps.runs)
	}
}

func TestCommandFailureEdgeRunsOnlyFailureSuccessor(t *testing.T) {
	f, l, steps := commandLoopFixture(t, "fail", "read_only", false)
	raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"check","kind":"command","command_ref":"check","access":"read_only"},{"id":"success","kind":"agent","seat":"build","access":"write"},{"id":"recover","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"work","to_node":"check"},{"from_node":"check","to_node":"success","when_outcome":"pass"},{"from_node":"check","to_node":"recover","when_outcome":"fail"}],"exit":{"requires_nodes":["recover"],"requires_candidate":"commit_required"}}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "failure edge fixture")
	result := &LoopResult{}
	for i := 0; i < 8; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.runs) != 3 || !strings.HasSuffix(steps.runs[2].RunID, "-n4") || steps.gates != 1 || result.Landed != 1 {
		t.Fatalf("runs %+v gates %d result %+v", steps.runs, steps.gates, result)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE node_id='success' AND outcome!='skipped'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("pass branch ran after failure")
		}
		return nil
	})
}
