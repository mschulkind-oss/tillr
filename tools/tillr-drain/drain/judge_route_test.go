package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type judgeRouteSteps struct {
	*workflowSteps
	order        []string
	mode         string
	workerHead   string
	mechanical   int
	afterLand    func()
	afterGate    func()
	entries      []item.JudgeEntry
	afterJudge   func()
	readOnlyRuns map[string]bool
}

func (s *judgeRouteSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if !strings.HasSuffix(req.RunID, "-judge") && req.NodeID != "evaluate" && !strings.HasSuffix(req.NodeID, "/evaluate") {
		s.order = append(s.order, "worker")
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		content := "built " + req.RunID + "\n"
		if s.mode == "preflight" {
			content = "built{{literal}}\n"
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), content)
		if !s.readOnlyRuns[req.RunID] {
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "build")
		} else {
			// A fixture reader leaves the checkout exactly as it found it.
			runGit(s.t, target, "reset", "--hard", "HEAD")
			runGit(s.t, target, "clean", "-fd")
		}
		s.workerHead = runGit(s.t, target, "rev-parse", "HEAD")
	} else {
		s.order = append(s.order, "judge")
		if req.Backend != "reviewer" {
			s.t.Errorf("judge backend %s", req.Backend)
		}
		brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
		if err != nil {
			return run.Record{}, "", err
		}
		if s.mode == "preflight" {
			preflight, _ := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
			cmd := exec.Command("sh", "-c", `PROG=fixture; . "$1"; preflight_brief "$2"`, "fixture", preflight, filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
			if out, err := cmd.CombinedOutput(); err != nil {
				return run.Record{}, "", fmt.Errorf("adapter preflight: %v: %s", err, out)
			}
		}
		for _, want := range []string{"the first criterion of item-a", "candidate.diff", "eval.output", "read-only"} {
			if !strings.Contains(string(brief), want) {
				s.t.Errorf("judge brief lacks %q", want)
			}
		}
		if s.mode == "dirty" || s.mode == "commit" {
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/escape.txt"), "escape\n")
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "changed\n")
			if s.mode == "commit" {
				runGit(s.t, target, "add", ".")
				runGit(s.t, target, "commit", "-qm", "judge escape")
			}
		}
		if s.mode != "missing" {
			verdict, outcome := "done", "pass"
			if s.mode == "not-done" {
				verdict, outcome = "not-done", "fail"
			}
			hash := item.CriterionHash("the first criterion of item-a")
			if s.mode == "invalid" {
				hash = "wrong"
			}
			output := item.JudgeOutput{EvaluatedHead: s.workerHead, EvaluatedTree: runGit(s.t, target, "rev-parse", s.workerHead+"^{tree}"), Outcome: outcome, Verdicts: []item.JudgeEntry{{CriterionID: "c1", CriterionHash: hash, Verdict: verdict, ClaimClass: item.ClaimVerification, Reason: "the first judge reason", Evidence: []string{"commit:" + s.workerHead}}}}
			if s.entries != nil {
				output.Outcome, output.Verdicts = "fail", s.entries
			}
			raw, _ := json.Marshal(output)
			writeForkFile(s.t, filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "eval.output"), string(raw))
		}
		if s.afterJudge != nil {
			s.afterJudge()
		}
	}
	reportPath := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	writeForkFile(s.t, reportPath, "RESULT: done\n")
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", err
}
func (s *judgeRouteSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.order = append(s.order, "gate")
	s.gates++
	outcome := GatePassed
	if s.mode == "gate-fail" {
		outcome = GateFailed
	}
	if s.afterGate != nil {
		s.afterGate()
	}
	return GateOutcome{Outcome: outcome}, nil
}
func (s *judgeRouteSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.order = append(s.order, "landing")
	if s.mode == "land-fail" {
		return LandOutcome{Result: LandRefused}, nil
	}
	outcome, err := s.workflowSteps.Land(ctx, c)
	if s.afterLand != nil {
		s.afterLand()
	}
	return outcome, err
}
func (s *judgeRouteSteps) Verdict(context.Context, Candidate, string) error {
	s.mechanical++
	return nil
}

func judgeRouteFixture(t *testing.T, mode string, grant bool) (refusalLoopFixture, *Loop, *judgeRouteSteps) {
	f, l, old := workflowLoopFixture(t, "")
	s := &judgeRouteSteps{workflowSteps: old, mode: mode}
	l.cfg.Steps = s
	l.cfg.Policy.Seats["review"] = "reviewer"
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		if err != nil {
			return err
		}
		judge := "agent"
		if mode == "mechanical" {
			judge = ""
		}
		_, err = tx.Exec(`UPDATE items SET workflow='',judge_mode=? WHERE item_id='item-a'`, judge)
		if err != nil {
			return err
		}
		if grant {
			_, err = tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-review','verification-only','owner',?,?)`, l.cfg.Now().Add(-time.Hour).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
		}
		return err
	})
	return f, l, s
}
func TestJudgeRouteBeforeGate(t *testing.T) {
	for _, mode := range []string{"pass", "preflight", "not-done", "missing", "invalid", "dirty", "commit", "gate-fail", "land-fail", "mechanical", "no-grant"} {
		t.Run(mode, func(t *testing.T) {
			f, l, s := judgeRouteFixture(t, mode, mode != "no-grant")
			result := &LoopResult{}
			for i := 0; i < 4; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			want := []string{"worker", "judge"}
			switch mode {
			case "pass", "preflight":
				want = append(want, "gate", "landing")
			// A refused gate or landing goes back to the worker with its evidence (the
			// owner's ruling of 2026-11-17), so the worker runs again within the passes.
			case "land-fail":
				want = append(want, "gate", "landing", "worker")
			case "gate-fail":
				want = append(want, "gate", "worker")
			case "mechanical":
				want = []string{"worker", "gate", "landing"}
			case "no-grant":
				want = nil
			}
			if !reflect.DeepEqual(s.order, want) {
				t.Fatalf("order %v, want %v", s.order, want)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				expected := 0
				if mode == "pass" || mode == "preflight" {
					expected = 1
				}
				if n != expected {
					t.Errorf("%d verdicts, want %d", n, expected)
				}
				if mode == "pass" || mode == "preflight" {
					var state string
					if err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
						return err
					}
					if state != StateLanded {
						t.Errorf("item state %s", state)
					}
					var payload string
					if err := r.QueryRow(`SELECT payload FROM events WHERE kind='item.verdict'`).Scan(&payload); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(payload, `"actor_kind":"agent"`) || !strings.Contains(payload, runGit(t, f.root, "rev-parse", "HEAD")) {
						t.Errorf("unbound verdict %s", payload)
					}
				}
				if mode == "missing" || mode == "invalid" || mode == "dirty" || mode == "commit" {
					var id string
					if err := r.QueryRow(`SELECT object_id FROM events WHERE kind='drain.refusal' ORDER BY seq DESC LIMIT 1`).Scan(&id); err != nil {
						t.Fatal(err)
					}
					if !strings.HasSuffix(id, "-judge") {
						t.Errorf("refusal attributed to worker %s", id)
					}
				}
				if mode == "not-done" {
					answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), Build: &l.build, BriefTemplate: &l.cfg.Stage.(*LaneStager).Template}, l.cfg.Policy, &VantageQuestions{Root: f.root})
					if err != nil {
						return err
					}
					for _, row := range answer.Rows {
						if row.ItemID == "item-a" && (!strings.Contains(row.Why, "the first judge reason") || row.Where != "judge") {
							t.Errorf("waiting row %+v", row)
						}
					}
				}
				return nil
			})
			if mode != "mechanical" && s.mechanical != 0 {
				t.Errorf("mechanical verdict ran %d times", s.mechanical)
			}
			if mode == "dirty" || mode == "commit" {
				if head := runGit(t, f.root, "rev-parse", "refs/tillr/runs/run-item-a-a1"); head != s.workerHead {
					t.Errorf("candidate changed %s != %s", head, s.workerHead)
				}
			}
			holds := map[string]string{"not-done": "eval-failed", "missing": "judge-unreadable", "invalid": "judge-unreadable", "dirty": "judge-unreadable", "commit": "judge-unreadable", "no-grant": "judge-grant-absent"}
			// gate-fail and land-fail hold nothing: the refusal went back to the worker, whose
			// returned run the order above shows.
			if reason := holds[mode]; reason != "" {
				for _, p := range mustPlan(t, l) {
					if p.decision.ItemID == "item-a" && p.decision.Reason != reason {
						t.Errorf("hold %s, want %s", p.decision.Reason, reason)
					}
				}
			}
		})
	}
}
