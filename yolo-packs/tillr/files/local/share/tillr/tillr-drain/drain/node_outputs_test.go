package drain

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const namedWorkflow = `{"name":"scout-then-build","version":1,"stages":[{"id":"first","kind":"agent","seat":"scout","access":"read_only","outputs":{"findings":"json"}},{"id":"second","kind":"agent","seat":"scout","access":"read_only","outputs":{"findings":"markdown"}},{"id":"work","kind":"agent","seat":"build","access":"write","inputs":{"evidence":"json"}}],"edges":[{"from_node":"first","to_node":"second"},{"from_node":"second","to_node":"work"},{"from_node":"first","from_output":"findings","to_node":"work","to_input":"evidence"}]}`

func TestNamedOutputLoader(t *testing.T) {
	for _, tc := range []struct {
		name, rule string
		edit       func(map[string]any)
	}{
		{"valid", "", func(map[string]any) {}},
		{"absent output", "workflow-output", func(w map[string]any) { w["edges"].([]any)[2].(map[string]any)["from_output"] = "absent" }},
		{"duplicate input", "workflow-input", func(w map[string]any) { w["edges"] = append(w["edges"].([]any), w["edges"].([]any)[2]) }},
		{"unfed input", "workflow-input", func(w map[string]any) { w["edges"] = w["edges"].([]any)[:2] }},
		{"undeclared input", "workflow-input", func(w map[string]any) { w["edges"].([]any)[2].(map[string]any)["to_input"] = "absent" }},
		{"half wire", "workflow-input", func(w map[string]any) { delete(w["edges"].([]any)[2].(map[string]any), "to_input") }},
		{"type", "workflow-output", func(w map[string]any) {
			w["stages"].([]any)[0].(map[string]any)["outputs"] = map[string]any{"findings": "binary"}
		}},
		{"path escape", "workflow-output", func(w map[string]any) {
			w["stages"].([]any)[0].(map[string]any)["outputs"] = map[string]any{"../escape": "json"}
		}},
		{"type mismatch", "workflow-input", func(w map[string]any) {
			w["stages"].([]any)[2].(map[string]any)["inputs"] = map[string]any{"evidence": "patch"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var w map[string]any
			if err := json.Unmarshal([]byte(namedWorkflow), &w); err != nil {
				t.Fatal(err)
			}
			tc.edit(w)
			raw, _ := json.Marshal(w)
			_, err := decodeWorkflow(raw, "scout-then-build@v1")
			if tc.rule == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.rule) {
				t.Fatalf("wanted %s: %v", tc.rule, err)
			}
		})
	}
}

type outputSteps struct {
	*workflowSteps
	mode   string
	briefs []string
}

func (s *outputSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	s.briefs = append(s.briefs, string(brief))
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if len(s.runs) == 1 {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
	}
	if len(s.runs) < 3 {
		dir := filepath.Join(RunDir(s.f.root, req.RunID), "artifacts")
		if !strings.Contains(string(brief), dir) {
			s.t.Errorf("producer brief lacks output directory %s", dir)
		}
		if s.mode != "missing" {
			body := `{"found":true}`
			if len(s.runs) == 2 {
				body = "second scout findings"
			}
			if len(s.runs) == 1 {
				switch s.mode {
				case "invalid":
					body = `{"found":`
				case "duplicate":
					body = `{"found":true,"found":false}`
				case "nested-duplicate":
					body = `{"nested":{"x":1,"x":2}}`
				case "trailing":
					body = `{} {}`
				}
			}
			// Staging, not the worker, must create the directory.
			if s.mode == "directory" {
				err = os.Mkdir(filepath.Join(dir, "findings"), 0755)
			} else if s.mode == "symlink" {
				err = os.Symlink(filepath.Join(target, ".git"), filepath.Join(dir, "findings"))
			} else {
				err = os.WriteFile(filepath.Join(dir, "findings"), []byte(body), 0644)
			}
			if err != nil {
				return run.Record{}, "", err
			}
		}
	} else {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "built\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build")
	}
	report := "RESULT: done\n"
	reportPath := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(reportPath, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}
func TestNamedOutputsProducerBindingAndValidation(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "invalid", "duplicate", "nested-duplicate", "trailing", "directory", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f, l, baseSteps := workflowLoopFixture(t, "")
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), namedWorkflow)
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "named outputs fixture")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='done' WHERE item_id='item-b'`)
				return err
			})
			steps := &outputSteps{workflowSteps: baseSteps, mode: mode}
			l.cfg.Steps = steps
			result := &LoopResult{}
			for i := 0; i < 12; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
				if result.Landed > 0 {
					break
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var outcome string
				if err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE node_id='first'`).Scan(&outcome); err != nil {
					return err
				}
				if mode != "valid" {
					if len(steps.runs) != 1 || outcome != "failed" {
						t.Errorf("bad output released consumer: %d runs, %s", len(steps.runs), outcome)
					}
					var n int
					if err := tx.QueryRow(`SELECT COUNT(*) FROM node_artifacts`).Scan(&n); err != nil {
						return err
					}
					if n != 0 {
						t.Errorf("accepted invalid output: %d", n)
					}
					var payload string
					if err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=?`, RefusalKind, steps.runs[0].RunID).Scan(&payload); err != nil {
						return err
					}
					if !strings.Contains(payload, "node-output-missing") || !strings.Contains(payload, "first.findings") {
						t.Errorf("output failure lost reason or name: %s", payload)
					}
					return nil
				}
				if len(steps.runs) != 3 || result.Landed != 1 {
					t.Fatalf("runs=%d result=%+v", len(steps.runs), result)
				}
				first := filepath.Join(RunDir(f.root, steps.runs[0].RunID), "artifacts", "findings")
				second := filepath.Join(RunDir(f.root, steps.runs[1].RunID), "artifacts", "findings")
				if strings.Contains(steps.briefs[1], first) || strings.Contains(steps.briefs[1], "node-report.md") {
					t.Error("ordering-only edge leaked a producer file")
				}
				consumer := steps.briefs[2]
				if !strings.Contains(consumer, first) || strings.Contains(consumer, second) {
					t.Errorf("wrong producer paths: %s", consumer)
				}
				var hash, location, typ, runID string
				if err := tx.QueryRow(`SELECT sha256,location,type,run_id FROM node_artifacts WHERE item_id='item-a' AND attempt=1 AND node_id='first' AND name='findings'`).Scan(&hash, &location, &typ, &runID); err != nil {
					return err
				}
				raw, err := os.ReadFile(first)
				if err != nil {
					return err
				}
				if hash != fmt.Sprintf("%x", sha256.Sum256(raw)) || location != first || typ != "json" || runID != steps.runs[0].RunID {
					t.Errorf("artifact: %s %s %s %s", hash, location, typ, runID)
				}
				diff := runGit(t, f.root, "diff", "HEAD^", "HEAD", "--name-only")
				if strings.Contains(diff, "artifacts") || strings.Contains(diff, "findings") {
					t.Errorf("output in diff: %s", diff)
				}
				return nil
			})
		})
	}
}
