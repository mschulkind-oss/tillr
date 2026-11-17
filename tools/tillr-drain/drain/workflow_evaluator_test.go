package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const evaluatorWorkflow = `{"name":"scout-then-build","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"},{"id":"second","kind":"agent","seat":"build","access":"write"},{"id":"evaluate","kind":"agent","seat":"review","access":"read_only"}],"edges":[{"from_node":"work","to_node":"second"},{"from_node":"second","to_node":"evaluate"}],"exit":{"requires_nodes":["work","second","evaluate"],"evaluator_slot":"evaluate"}}`

func TestWorkflowEvaluatorValidation(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement, rule string }{
		{"access", `"seat":"review","access":"read_only"`, `"seat":"review","access":"write"`, "workflow-evaluator-access"},
		{"order", `{"from_node":"second","to_node":"evaluate"}`, `{"from_node":"work","to_node":"evaluate"}`, "workflow-evaluator-order"},
		{"required", `"requires_nodes":["work","second","evaluate"]`, `"requires_nodes":["work","second"]`, "workflow-evaluator-required"},
		{"valid", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			l.cfg.Policy.Seats["review"] = "reviewer"
			l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture"}
			raw := evaluatorWorkflow
			if tc.old != "" {
				raw = strings.Replace(raw, tc.old, tc.replacement, 1)
			}
			path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
			writeForkFile(t, path, raw)
			_, err := loadWorkflow(f.root, "scout-then-build@v1", l.cfg.Policy)
			var out bytes.Buffer
			ok := CheckWorkflows(f.root, path, &out)
			if tc.rule == "" {
				if err != nil || !ok {
					t.Fatalf("loader %v; check %s", err, out.String())
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.rule) || ok || !strings.Contains(out.String(), tc.rule) {
				t.Fatalf("loader %v; check %s; want %s", err, out.String(), tc.rule)
			}
		})
	}
}

func TestWorkflowEvaluatorJudgeRoute(t *testing.T) {
	for _, mode := range []string{"pass", "mechanical", "not-done", "no-grant"} {
		t.Run(mode, func(t *testing.T) {
			f, l, s := judgeRouteFixture(t, mode, mode != "no-grant")
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
			runGit(t, f.root, "add", "config/tillr/workflows")
			runGit(t, f.root, "commit", "-qm", "evaluator workflow")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
				return err
			})
			s.afterLand = func() {
				fixtureRead(t, f, func(r *store.Reader) error {
					var n int
					err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n)
					if n != 0 {
						t.Errorf("verdicts recorded before landing: %d", n)
					}
					return err
				})
			}
			result := &LoopResult{}
			for i := 0; i < 12; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			want := []string{"worker", "worker", "judge"}
			if mode == "pass" || mode == "mechanical" {
				want = append(want, "gate", "landing")
			}
			if mode == "no-grant" {
				want = nil
			}
			if !reflect.DeepEqual(s.order, want) {
				t.Fatalf("order %v, want %v", s.order, want)
			}
			if mode != "no-grant" {
				raw, err := os.ReadFile(filepath.Join(RunDir(f.root, s.runs[2].RunID), judgeReceiptFile))
				if err != nil {
					t.Fatal(err)
				}
				var receipt JudgeReceipt
				if err := json.Unmarshal(raw, &receipt); err != nil {
					t.Fatal(err)
				}
				if len(receipt.Writers) != 2 {
					t.Fatalf("writers %+v", receipt.Writers)
				}
				for i, writer := range receipt.Writers {
					if writer.RunID != s.runs[i].RunID || writer.SessionID == "" || writer.Route != s.runs[i].Route() {
						t.Errorf("writer provenance %+v", writer)
					}
				}
				if receipt.Request.RunID != s.runs[2].RunID || receipt.Request.NodeID != "evaluate" {
					t.Errorf("evaluator provenance %+v", receipt.Request)
				}
			}
			if s.mechanical != 0 {
				t.Errorf("mechanical verdicts %d", s.mechanical)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n); err != nil {
					return err
				}
				expected := 0
				if mode == "pass" || mode == "mechanical" {
					expected = 1
				}
				if n != expected {
					t.Errorf("verdicts %d, want %d", n, expected)
				}
				if expected == 1 {
					var actor string
					if err := r.QueryRow(`SELECT json_extract(payload,'$.judged_actor') FROM events WHERE kind='item.verdict'`).Scan(&actor); err != nil {
						return err
					}
					for _, req := range s.runs[:2] {
						if !strings.Contains(actor, req.RunID) {
							t.Errorf("writer missing from %s", actor)
						}
					}
				}
				return nil
			})
			reason := map[string]string{"not-done": ReasonEvalFailed, "no-grant": ReasonJudgeGrantAbsent}[mode]
			if reason != "" {
				for _, p := range mustPlan(t, l) {
					if p.decision.ItemID == "item-a" && p.decision.Reason != reason {
						t.Errorf("hold %s, want %s", p.decision.Reason, reason)
					}
				}
			}
		})
	}
}

func TestWorkflowEvaluatorRefusesWriterRun(t *testing.T) {
	f, l, s, c := prepareJudgeCandidate(t, "pass")
	req := l.cfg.Policy.request(c.ItemID, c.LaneID, c.RunID, "reviewer")
	req.NodeID = "evaluate"
	c.Evaluator = req
	c.Writers = []JudgeWriter{{NodeID: "work", RunID: c.RunID, SessionID: "writer-session", Backend: "fixture", Route: "fixture"}}
	_, ok, err := l.judgeCandidate(context.Background(), c, &LoopResult{})
	if err != nil || ok {
		t.Fatalf("same-run evaluator accepted: %v %v", ok, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		detail, err := judgeFailureDetail(r, c.ItemID, ReasonJudgeUnreadable)
		if !strings.Contains(detail, "on its own work") {
			t.Errorf("intake refusal %s", detail)
		}
		return err
	})
	if s.gates != 0 {
		t.Fatal("gate ran for self-judging evaluator")
	}
}

func TestWorkflowEvaluatorBranchesAndAuthority(t *testing.T) {
	for _, mode := range []string{"skipped", "expired", "writerless", "later-reader", "nested"} {
		t.Run(mode, func(t *testing.T) {
			f, l, s := judgeRouteFixture(t, "mechanical", true)
			raw := evaluatorWorkflow
			switch mode {
			case "skipped":
				raw = strings.Replace(raw, `"from_node":"work","to_node":"second"`, `"from_node":"work","to_node":"second","when_outcome":"fail"`, 1)
				raw = strings.Replace(raw, `"requires_nodes":["work","second","evaluate"]`, `"requires_nodes":["work","evaluate"]`, 1)
				raw = strings.Replace(raw, `"edges":[`, `"edges":[{"from_node":"work","to_node":"evaluate"},`, 1)
			case "expired":
				at := l.cfg.Now()
				s.afterJudge = func() { l.cfg.Now = func() time.Time { return at.Add(2 * time.Hour) } }
			case "writerless":
				raw = strings.Replace(raw, `"access":"write"`, `"access":"read_only"`, -1)
				raw = strings.Replace(raw, `"exit":{`, `"exit":{"requires_candidate":"no_commit",`, 1)
				s.readOnlyRuns = map[string]bool{"run-item-a-a1-n1": true, "run-item-a-a1-n2": true}
			case "later-reader":
				raw = strings.Replace(raw, `"stages":[`, `"stages":[{"id":"observe","kind":"agent","seat":"build","access":"read_only"},`, 1)
				raw = strings.Replace(raw, `"edges":[`, `"edges":[{"from_node":"evaluate","to_node":"observe"},`, 1)
				raw = strings.Replace(raw, `"requires_nodes":[`, `"requires_nodes":["observe",`, 1)
				s.readOnlyRuns = map[string]bool{"run-item-a-a1-n4": true}
			case "nested":
				inner := strings.Replace(raw, `"name":"scout-then-build"`, `"name":"inner"`, 1)
				writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/inner@v1.json"), inner)
				raw = `{"name":"scout-then-build","version":1,"stages":[{"id":"nested","kind":"workflow","workflow":"inner@v1"}],"edges":[]}`
			}
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
			runGit(t, f.root, "add", "config/tillr/workflows")
			runGit(t, f.root, "commit", "-qm", "evaluator workflow")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
				return err
			})
			result := &LoopResult{}
			for i := 0; i < 14; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if mode == "expired" {
				for _, p := range mustPlan(t, l) {
					if p.decision.ItemID == "item-a" && p.decision.Reason != ReasonJudgeGrantAbsent {
						t.Errorf("hold %s", p.decision.Reason)
					}
				}
				if s.gates != 0 {
					t.Fatal("expired grant reached gate")
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE item_grants SET expires_at=?`, l.cfg.Now().Add(time.Hour).UnixMilli())
					return err
				})
				for i := 0; i < 6; i++ {
					if _, err := l.pass(context.Background(), result); err != nil {
						t.Fatal(err)
					}
					drainOwned(t, l, result)
				}
			}
			expectedRuns := 3
			if mode == "skipped" {
				expectedRuns = 2
			}
			if mode == "later-reader" {
				expectedRuns = 4
			}
			if len(s.runs) != expectedRuns {
				t.Fatalf("runs %v", s.runs)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var state string
				err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state)
				if state != StateLanded {
					t.Errorf("unsettled graph state %s", state)
				}
				return err
			})
			if mode == "writerless" {
				if s.gates != 0 {
					t.Fatal("no-commit graph gated")
				}
			} else if s.gates != 1 {
				t.Fatalf("gate count %d; order %v", s.gates, s.order)
			}
		})
	}
}

func TestWorkflowEvaluatorProposalKeepsSlot(t *testing.T) {
	w := &Workflow{Stages: []WorkflowStage{{ID: "propose", Kind: "agent", Access: "read_only"}, {ID: "target", Kind: "agent", Access: "write"}}, Edges: []WorkflowEdge{{From: "propose", To: "target"}}}
	inner, err := decodeWorkflow([]byte(evaluatorWorkflow), "scout-then-build@v1")
	if err != nil {
		t.Fatal(err)
	}
	next, err := replaceProposalGraph(w, "target", inner)
	if err != nil {
		t.Fatal(err)
	}
	if evaluatorSlot(next) != "target/evaluate" {
		t.Fatalf("replacement lost evaluator: %+v", next.Exit)
	}
	if err := validateEvaluator(next); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowEvaluatorHoldCleanupReplay(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "not-done", true)
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), evaluatorWorkflow)
	runGit(t, f.root, "add", "config/tillr/workflows")
	runGit(t, f.root, "commit", "-qm", "evaluator workflow")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
		return err
	})
	result := &LoopResult{}
	for i := 0; i < 10; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	var d Decision
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, history, err := readPendingHistory(tx.Reader())
		if err != nil {
			return err
		}
		work := *history[s.runs[2].RunID]
		work.Done = false
		d = work.Decision
		// Re-open only the examination to model a crash after cleanup but before its acknowledgement.
		return appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, l.cfg.Now())
	})
	handled, err := l.settleEvaluator(context.Background(), d, run.Record{RunID: d.RunRequest.RunID}, "", result)
	if err != nil || !handled {
		t.Fatalf("hold replay %v %v", handled, err)
	}
	if result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
		t.Errorf("cleanup replay reapplied intake to reset tree: %+v", result.RefusedSteps)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		detail, err := judgeFailureDetail(r, d.ItemID, ReasonJudgeUnreadable)
		if detail != "" {
			t.Errorf("hold changed during cleanup replay: %s", detail)
		}
		return err
	})
	if head := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+d.RunRequest.RunID); head != s.workerHead {
		t.Errorf("pin %s, want %s", head, s.workerHead)
	}
	if len(s.runs) != 3 {
		t.Fatalf("repeated evaluator: %v", s.runs)
	}
}

func TestWorkflowEvaluatorGrantRenewalDoesNotMaskFailure(t *testing.T) {
	f, l, s, c := prepareJudgeCandidate(t, "not-done")
	writer := c.RunID
	c.RunID += "-evaluate"
	c.Evaluator = l.cfg.Policy.request(c.ItemID, c.LaneID, c.RunID, "reviewer")
	c.Evaluator.NodeID = "evaluate"
	c.Writers = []JudgeWriter{{NodeID: "work", RunID: writer, SessionID: "writer-session", Backend: "fixture", Route: "fixture"}}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_grants SET expires_at=?`, l.cfg.Now().UnixMilli())
		return err
	})
	_, ok, err := l.judgeCandidate(context.Background(), c, &LoopResult{})
	if ok || !errors.Is(err, errJudgeGrantHeld) {
		t.Fatalf("grant hold %v %v", ok, err)
	}
	if len(s.runs) != 1 {
		t.Fatal("evaluator started without grant")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_grants SET expires_at=?`, l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
	_, ok, err = l.judgeCandidate(context.Background(), c, &LoopResult{})
	if ok || err != nil {
		t.Fatalf("failing evaluator %v %v", ok, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		history, err := readRunHistory(r)
		if err != nil {
			return err
		}
		if reason := history.refusals[c.RunID]; reason != ReasonEvalFailed {
			t.Errorf("hold %s, want %s", reason, ReasonEvalFailed)
		}
		return nil
	})
	if len(s.runs) != 2 {
		t.Fatalf("run count %d", len(s.runs))
	}
}

func TestWorkflowEvaluatorSingleVerdictReader(t *testing.T) {
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	readers := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		readers += strings.Count(string(raw), `"eval.output"`)
	}
	// One path is written into the judge brief; the other is the existing intake read.
	if readers != 2 {
		t.Fatalf("eval.output references %d, want only the judge's two references", readers)
	}
}
