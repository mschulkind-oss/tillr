package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mschulkind-oss/tillr-run/run"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// defaultWorkflowSteps also supports the legacy one-run path; workflowSteps
// deliberately checks the two-node handoff and is not a single-run fixture.
type defaultWorkflowSteps struct{ *workflowSteps }

func (s *defaultWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if strings.Contains(req.RunID, "-n") {
		return s.workflowSteps.Run(ctx, req)
	}
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "built\n")
	runGit(s.t, target, "add", ".")
	runGit(s.t, target, "commit", "-qm", "build")
	report := "RESULT: done\n"
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(path, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}
func defaultWorkflowFixture(t *testing.T) (refusalLoopFixture, *Loop, *defaultWorkflowSteps) {
	f, l, old := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='done' WHERE item_id='item-b'`)
		return err
	})
	steps := &defaultWorkflowSteps{old}
	l.cfg.Steps = steps
	return f, l, steps
}

func TestSeatWorkflowDefaults(t *testing.T) {
	for _, tc := range []struct {
		block, want, source string
		nodes               int
	}{
		{"", "scout-then-build@v1", "default", 2},
		{"scout-then-build@v1", "scout-then-build@v1", "block", 2},
		{"single-worker@v1", "single-worker@v1", "block", 1},
	} {
		t.Run(tc.block, func(t *testing.T) {
			f, l, steps := defaultWorkflowFixture(t)
			l.cfg.WorkflowDefaults = map[string]string{"build": "scout-then-build@v1"}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET seat='build', workflow=? WHERE item_id='item-a'`, tc.block)
				return err
			})
			result := &LoopResult{}
			for i := 0; i < 8; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if len(steps.runs) != tc.nodes {
				t.Fatalf("runs: %d want %d", len(steps.runs), tc.nodes)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				env, err := item.Show(tx.Reader(), "item-a", f.root)
				if err != nil {
					return err
				}
				var payload struct {
					SelectedWorkflow string `json:"selected_workflow"`
					WorkflowSource   string `json:"workflow_source"`
				}
				if err = json.Unmarshal(env.Payload, &payload); err != nil {
					return err
				}
				if payload.SelectedWorkflow != tc.want || payload.WorkflowSource != tc.source {
					t.Fatalf("selection: %+v", payload)
				}
				return nil
			})
		})
	}
}

func TestSeatWorkflowDefaultInvalid(t *testing.T) {
	for _, tc := range []struct {
		ref, body string
		tracked   bool
	}{
		{"missing@v1", "", false},
		{"untracked@v1", strings.ReplaceAll(workflowFixture, "scout-then-build", "untracked"), false},
		{"invalid@v1", `{"name":"invalid","version":1,"stages":[],"edges":[]}`, true},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			f, l, _ := defaultWorkflowFixture(t)
			if tc.body != "" {
				writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows", tc.ref+".json"), tc.body)
			}
			if tc.tracked {
				runGit(t, f.root, "add", ".")
				runGit(t, f.root, "commit", "-qm", "invalid workflow")
			}
			l.cfg.WorkflowDefaults = map[string]string{"build": tc.ref}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET seat='build',workflow='' WHERE item_id='item-a'`)
				return err
			})
			var startup bytes.Buffer
			cfg := l.cfg
			cfg.Report = &startup
			if _, err := NewLoop(cfg); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(startup.String(), "workflow-invalid: workflows.default.build") {
				t.Fatalf("startup: %s", startup.String())
			}
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" && (p.decision.Action != ActionHold || !strings.Contains(p.decision.Reason, "workflow-invalid: workflows.default.build")) {
					t.Fatalf("hold: %+v", p.decision)
				}
			}
		})
	}
}

func TestSeatWorkflowDefaultPinned(t *testing.T) {
	f, l, steps := defaultWorkflowFixture(t)
	l.cfg.WorkflowDefaults = map[string]string{"build": "scout-then-build@v1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build',workflow='' WHERE item_id='item-a'`)
		return err
	})
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	l.cfg.WorkflowDefaults["build"] = "missing@v1"
	// Construct a fresh loop: the graph and selection must come from the store,
	// not from the old loop's memory or the repository's new default.
	var err error
	l, err = NewLoop(l.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.runs) != 2 {
		t.Fatalf("default changed graph: %+v", steps.runs)
	}
}

func TestSeatWorkflowTrunkFixerIsOneNode(t *testing.T) {
	f, l, steps := defaultWorkflowFixture(t)
	l.cfg.WorkflowDefaults = map[string]string{"build": "scout-then-build@v1"}
	id := TrunkFixerID(runGit(t, f.root, "rev-parse", "HEAD"))
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, id)
	s.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET seat='build',allowed_paths='["tools/item-a/"]',body_ref='briefs/item-a.md' WHERE item_id=?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='done' WHERE item_id='item-a'`)
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == id {
			if p.decision.Action != ActionDispatch || p.decision.Workflow != nil || p.decision.WorkflowSource != "repair" {
				t.Fatalf("fixer graph: %+v", p.decision)
			}
		}
	}
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if len(steps.runs) != 1 || strings.Contains(steps.runs[0].RunID, "-n1") {
		t.Fatalf("fixer runs: %+v", steps.runs)
	}
}

func TestSeatWorkflowNoDefault(t *testing.T) {
	f, l, steps := defaultWorkflowFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build',workflow='' WHERE item_id='item-a'`)
		return err
	})
	result := &LoopResult{}
	for i := 0; i < 8; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.runs) != 1 {
		t.Fatalf("legacy runs: %+v", steps.runs)
	}
}
