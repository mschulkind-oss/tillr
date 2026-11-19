package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type exitSteps struct {
	*workflowSteps
	commit         bool
	failNode       int
	verdicts       int
	landedHead     string
	stateFault     bool
	realState      bool
	infraNode      int
	infraUsed      bool
	blockedFailure bool
	wallNode       int
}

func (s *exitSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if s.firstHead == "" {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
	}
	code := 0
	report := "RESULT: done\n"
	if strings.HasSuffix(req.RunID, fmt.Sprintf("-n%d", s.failNode)) || (s.failNode > 2 && strings.HasSuffix(req.RunID, "-n2")) {
		code = 1
		report = "RESULT: failed\n"
		if s.blockedFailure {
			report = "RESULT: blocked\n"
			code = 0
		}
	}
	if s.commit && len(s.runs) == 1 {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "built\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build")
	}
	if s.wallNode > 0 && strings.HasSuffix(req.RunID, fmt.Sprintf("-n%d", s.wallNode)) {
		code = 124
		report = ""
	}
	if !s.infraUsed && s.infraNode > 0 && strings.HasSuffix(req.RunID, fmt.Sprintf("-n%d", s.infraNode)) {
		code = 2
		report = "backend unavailable\n"
		s.infraUsed = true
	}
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(path, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, Resumed: req.Resume, State: run.StateExited, ExitCode: countPtr(code), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}
func (s *exitSteps) Verdict(_ context.Context, c Candidate, _ string) error {
	s.verdicts++
	s.landedHead = c.HeadSHA
	return nil
}
func exitFixture(t *testing.T, raw string, commit bool, fail int) (refusalLoopFixture, *Loop, *exitSteps) {
	f, l, old := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-a','verify','node checks','', 'verification-only'),('item-a','judge','human judgment','done', 'owner-only')`)
		return err
	})
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "exit workflow")
	steps := &exitSteps{workflowSteps: old, commit: commit, failNode: fail}
	l.cfg.Steps = steps
	return f, l, steps
}

const exitOne = `{"name":"scout-then-build","version":1,"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"}],"edges":[],"exit":{"requires_nodes":["one"],"requires_candidate":"%s"}}`

func TestWorkflowExitCandidatePolicies(t *testing.T) {
	for _, policy := range []string{"commit_required", "commit_optional", "no_commit"} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/commit=%v", policy, commit), func(t *testing.T) {
				f, l, steps := exitFixture(t, fmt.Sprintf(exitOne, policy), commit, 0)
				before := runGit(t, f.root, "rev-parse", "HEAD")
				result := &LoopResult{}
				for i := 0; i < 4; i++ {
					graphPass(t, l, result)
				}
				refused := policy == "commit_required" && !commit || policy == "no_commit" && commit
				if refused {
					fixtureWrite(t, f, func(tx *store.Tx) error {
						var reason string
						err := tx.QueryRow(`SELECT json_extract(payload,'$.step') FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`, RefusalKind).Scan(&reason)
						want := RefusalNoOp
						if commit {
							want = RefusalFence
						}
						if reason != want {
							t.Errorf("reason %q want %q", reason, want)
						}
						return err
					})
					if steps.verdicts != 0 || steps.gates != 0 {
						t.Fatalf("refused graph judged: %+v", steps)
					}
				} else {
					fixtureWrite(t, f, func(tx *store.Tx) error {
						var state string
						err := tx.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state)
						if state != StateLanded {
							t.Errorf("item remained %s", state)
						}
						return err
					})
					wantVerdicts := 0
					if commit {
						wantVerdicts = 1
					}
					if steps.verdicts != wantVerdicts {
						t.Fatalf("verdicts %d want %d", steps.verdicts, wantVerdicts)
					}
					if !commit {
						if steps.gates != 0 || result.Landed != 0 || steps.landedHead != "" || runGit(t, f.root, "rev-parse", "HEAD") != before {
							t.Fatalf("no-commit graph landed or gated: %+v %+v", result, steps)
						}
						assertNoCommitExit(t, f, steps, before, "done")
						fixtureWrite(t, f, func(tx *store.Tx) error {
							var n int
							err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='drain.graph-completed' AND object_id=? AND json_extract(payload,'$.landed')=0 AND json_extract(payload,'$.reason')='no commit by design'`, steps.runs[0].RunID).Scan(&n)
							if n != 1 {
								t.Errorf("no-commit run record count %d", n)
							}
							return err
						})
					} else if result.Landed != 1 || steps.gates != 1 {
						t.Fatalf("landing %+v gates %d", result, steps.gates)
					}
				}
			})
		}
	}
}

const exitRepair = `{"name":"scout-then-build","version":1,"stages":[{"id":"edit","kind":"agent","seat":"build","access":"write"},{"id":"check","kind":"agent","seat":"build","access":"read_only"},{"id":"repair","kind":"agent","seat":"build","access":"write"},{"id":"check-again","kind":"agent","seat":"build","access":"read_only"}],"edges":[{"from_node":"edit","to_node":"check"},{"from_node":"check","to_node":"repair","when_outcome":"fail"},{"from_node":"repair","to_node":"check-again"}],"exit":{"requires_nodes":["check-again"],"requires_candidate":"commit_required"}}`

func TestWorkflowExitRepairAndUnhandledFailure(t *testing.T) {
	for _, fail := range []int{2, 3} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			f, l, steps := exitFixture(t, exitRepair, true, fail)
			result := &LoopResult{}
			for i := 0; i < 10; i++ {
				graphPass(t, l, result)
			}
			want := 4
			if fail == 3 {
				want = 3
			}
			if len(steps.runs) != want {
				t.Fatalf("runs %v", steps.runs)
			}
			for i, req := range steps.runs {
				if req.RunID != fmt.Sprintf("run-item-a-a1-n%d", i+1) {
					t.Errorf("run %d = %s", i, req.RunID)
				}
			}
			if fail == 2 {
				if result.Landed != 1 || steps.gates != 1 {
					t.Fatalf("repair not landed %+v", result)
				}
			} else {
				st, err := store.Open(context.Background(), f.storePath, false)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				err = st.ReadTx(context.Background(), func(r *store.Reader) error {
					why, err := Why(r, Pass{Build: &l.build, AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, &VantageQuestions{Root: f.root})
					if err != nil {
						return err
					}
					for _, row := range why.Rows {
						if row.ItemID == "item-a" {
							if row.Reason != "node-failed: repair" {
								t.Errorf("hold %+v", row)
							}
							return nil
						}
					}
					return fmt.Errorf("no held item")
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
func TestWorkflowExitSkipsUnusedRepair(t *testing.T) {
	raw := strings.Replace(exitRepair, `["check-again"]`, `["check"]`, 1)
	f, l, steps := exitFixture(t, raw, true, 0)
	result := &LoopResult{}
	for i := 0; i < 6; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 || result.Landed != 1 {
		t.Fatalf("unused branch ran: %v %+v", steps.runs, result)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND outcome='skipped'`).Scan(&n)
		if n != 2 {
			t.Errorf("skipped %d", n)
		}
		return err
	})
}
func TestWorkflowExitRejectsContradiction(t *testing.T) {
	raw := strings.Replace(exitRepair, `["check-again"]`, `["check","check-again"]`, 1)
	if _, err := decodeWorkflow([]byte(raw), "scout-then-build@v1"); err == nil || !strings.Contains(err.Error(), "workflow-exit") {
		t.Fatalf("contradictory exit: %v", err)
	}
	for _, policy := range []string{"bad", ""} {
		if _, err := decodeWorkflow([]byte(fmt.Sprintf(exitOne, policy)), "scout-then-build@v1"); err == nil {
			t.Fatalf("invalid policy %q accepted", policy)
		}
	}
}
