package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func plantRepoFormatCheck(t *testing.T, root string) {
	t.Helper()
	justfile, err := os.ReadFile("../../../.justfile")
	if err != nil {
		t.Fatal(err)
	}
	decl, err := os.ReadFile("../../gate/checks/format.json")
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, ".justfile"), string(justfile))
	if err := os.MkdirAll(filepath.Join(root, "tools/gate/checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools/gate/checks/format.json"), string(decl))
	runGit(t, root, "add", ".justfile", "tools/gate/checks/format.json")
	runGit(t, root, "commit", "-qm", "declare format check")
}

func TestSingleWorkerStepStopsUnformattedGoFilesBeforeGate(t *testing.T) {
	for _, fixOnResume := range []bool{true, false} {
		name := "still-unformatted"
		if fixOnResume {
			name = "formatted-on-resume"
		}
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Lanes = []string{"lane-1"}
			plantRepoFormatCheck(t, f.root)
			base := strings.TrimSpace(runGit(t, f.root, "rev-parse", TrunkRef))
			laneWT := l.laneTarget("lane-1")
			runGit(t, laneWT, "reset", "--hard", base)

			command := `set -e
if [ "$TILLR_SESSION_MODE" = new ]; then
  mkdir -p tools/item-a
  printf 'package itema\nfunc bad(){println(1)}\n' > tools/item-a/new_file.go
  git add tools/item-a/new_file.go
  git commit -qm "unformatted new file"
  exit 0
fi
grep -q 'tools/item-a/new_file.go' "$TILLR_RESUME_PROMPT_FILE" || exit 72
`
			if fixOnResume {
				command += "just format\ngit add tools/item-a/new_file.go\ngit commit -q --amend --no-edit\n"
			} else {
				command += "exit 0\n"
			}

			orig := &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}
			steps := &commitContinuationSteps{continuationSteps: orig}
			l.cfg.Steps = steps
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths = '["tools/item-a/"]'`)
				return err
			})
			dir := l.laneDirPath("lane-1")
			writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
			writeForkFile(t, filepath.Join(dir, FenceFile), "tools/item-a/\n")
			writeForkFile(t, filepath.Join(dir, BriefFile), "continue this attempt\n")
			writeForkFile(t, filepath.Join(dir, SessionIDFile), "33b5f2d2-0cd4-4a49-b293-c58f5635561f\n")

			plan := mustPlan(t, l)[0]
			plan.decision.RunRequest.Command = []string{continuationShell(t), "-c", command}
			ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("dispatch: %v / %v", ok, err)
			}
			if _, _, err := steps.Run(context.Background(), *plan.decision.RunRequest); err != nil {
				t.Fatal(err)
			}

			var result LoopResult
			settleContinuation(t, l, &result)
			if fixOnResume {
				settleContinuation(t, l, &result)
			}

			if steps.resumed != 1 {
				t.Fatalf("resumed = %d, want 1", steps.resumed)
			}
			if fixOnResume {
				if steps.gates != 1 || steps.lands != 1 {
					t.Fatalf("formatted continuation did not gate and land once: gates=%d lands=%d", steps.gates, steps.lands)
				}
				got := runGit(t, steps.candidate.Worktree, "show", steps.candidate.HeadSHA+":tools/item-a/new_file.go")
				if !strings.Contains(got, "func bad() { println(1) }") {
					t.Fatalf("landed file is not gofmt-formatted:\n%s", got)
				}
				return
			}

			if steps.gates != 0 || steps.lands != 0 {
				t.Fatalf("unformatted candidate reached gate: gates=%d lands=%d", steps.gates, steps.lands)
			}
			db := openFixture(t, f.storePath)
			defer db.Close()
			if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
				var body []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, plan.decision.RunRequest.RunID).Scan(&body); err != nil {
					return err
				}
				var refusal refusalPayload
				if err := json.Unmarshal(body, &refusal); err != nil {
					return err
				}
				if refusal.Step != RefusalFence || !strings.Contains(refusal.Detail, "tools/item-a/new_file.go") {
					t.Fatalf("refusal = %+v, want step=%s naming tools/item-a/new_file.go", refusal, RefusalFence)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			next := mustPlan(t, l)[0].decision
			if next.Reason != ReasonReturned || next.Returned == nil || !strings.Contains(next.Returned.Detail, "tools/item-a/new_file.go") {
				t.Fatalf("next plan = %+v, want ReasonReturned naming tools/item-a/new_file.go", next)
			}
		})
	}
}

type formatWorkflowSteps struct {
	*workflowSteps
	mode        string
	requests    []run.Request
	verifyCalls int
	gateCalls   int
	landedHeads []string
}

func (s *formatWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.requests = append(s.requests, req)
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(laneDir, WorktreeDir)
	outDir := nodeOutputsDir(s.f.root, req.RunID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return run.Record{}, "", err
	}

	switch {
	case s.mode == "plan-do-it-now" && !req.Resume:
		writeForkFile(s.t, filepath.Join(outDir, quickWayFile), "do-it-now\n")
		writeForkFile(s.t, filepath.Join(outDir, deliveryOutput), `{"kind": "repo"}`)
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/item_terminal_test.go"), "package itema\nfunc bad(){println(1)}\n")
		runGit(s.t, target, "add", "tools/item-a/item_terminal_test.go")
		runGit(s.t, target, "commit", "-qm", "do-it-now unformatted")

	case s.mode == "plan-do-it-now" && req.Resume:
		if !strings.Contains(req.ResumePrompt, "tools/item-a/item_terminal_test.go") {
			s.t.Fatalf("resume prompt missing file name: %q", req.ResumePrompt)
		}
		if s.gateCalls != 0 {
			s.t.Fatalf("gate ran (%d) before unformatted do-it-now commit was fixed", s.gateCalls)
		}
		cmd := exec.CommandContext(ctx, "just", "format")
		cmd.Dir = target
		if out, err := cmd.CombinedOutput(); err != nil {
			s.t.Fatalf("just format: %v: %s", err, out)
		}
		runGit(s.t, target, "add", "tools/item-a/item_terminal_test.go")
		runGit(s.t, target, "commit", "-q", "--amend", "--no-edit")

	case (s.mode == "work-before-verify" || s.mode == "work-still-unformatted") && strings.HasSuffix(req.RunID, "-n1"):
		writeForkFile(s.t, filepath.Join(outDir, deliveryOutput), `{"kind": "repo"}`)

	case (s.mode == "work-before-verify" || s.mode == "work-still-unformatted") && strings.HasSuffix(req.RunID, "-n2") && !req.Resume:
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/holdrules.go"), "package itema\nvar m = map[string]int{\"a\":    1}\n")
		runGit(s.t, target, "add", "tools/item-a/holdrules.go")
		runGit(s.t, target, "commit", "-qm", "work unformatted")

	case (s.mode == "work-before-verify" || s.mode == "work-still-unformatted") && strings.HasSuffix(req.RunID, "-n2") && req.Resume:
		if !strings.Contains(req.ResumePrompt, "tools/item-a/holdrules.go") {
			s.t.Fatalf("resume prompt missing file name: %q", req.ResumePrompt)
		}
		if s.verifyCalls != 0 || s.gateCalls != 0 {
			s.t.Fatalf("verify (%d) or gate (%d) ran before unformatted work commit was fixed", s.verifyCalls, s.gateCalls)
		}
		if s.mode == "work-before-verify" {
			cmd := exec.CommandContext(ctx, "just", "format")
			cmd.Dir = target
			if out, err := cmd.CombinedOutput(); err != nil {
				s.t.Fatalf("just format: %v: %s", err, out)
			}
			runGit(s.t, target, "add", "tools/item-a/holdrules.go")
			runGit(s.t, target, "commit", "-q", "--amend", "--no-edit")
		}

	case s.mode == "work-before-verify" && strings.HasSuffix(req.RunID, "-n3"):
		s.verifyCalls++
		cmd := exec.CommandContext(ctx, "just", "format", "--check")
		cmd.Dir = target
		if out, err := cmd.CombinedOutput(); err != nil {
			s.t.Fatalf("unformatted tree reached verify: %v:\n%s", err, out)
		}
		head := strings.TrimSpace(runGit(s.t, target, "rev-parse", "HEAD"))
		tree := strings.TrimSpace(runGit(s.t, target, "rev-parse", head+"^{tree}"))
		output := item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts: []item.JudgeEntry{{
				CriterionID:   "c1",
				CriterionHash: item.CriterionHash("the first criterion of item-a"),
				Verdict:       "done",
				ClaimClass:    item.ClaimVerification,
				Reason:        "formatted candidate passes",
				Evidence:      []string{"node:work", "commit:" + head},
			}},
		}
		raw, err := json.Marshal(output)
		if err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output"), string(raw))
	}

	report := "RESULT: done\n"
	reportPath := filepath.Join(laneDir, "report.md")
	writeForkFile(s.t, reportPath, report)
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		EndCause:     run.EndNormalExit,
		Resumed:      req.Resume,
		StartedAt:    "2026-11-22T12:00:00Z",
		FinishedAt:   "2026-11-22T12:00:05Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, nil
}

func (s *formatWorkflowSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gateCalls++
	cmd := exec.CommandContext(ctx, "just", "format", "--check")
	cmd.Dir = c.Worktree
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Errorf("unformatted tree reached Steps.Gate: %v:\n%s", err, out)
		return GateOutcome{Outcome: GateFailed}, nil
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *formatWorkflowSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.landedHeads = append(s.landedHeads, c.HeadSHA)
	return s.workflowSteps.Land(ctx, c)
}

func TestWorkflowStepsStopUnformattedGoFilesBeforeVerifyAndGate(t *testing.T) {
	for _, mode := range []string{"plan-do-it-now", "work-before-verify", "work-still-unformatted"} {
		t.Run(mode, func(t *testing.T) {
			f, l, baseSteps := workflowLoopFixture(t, "")
			declareWorkerResumeFixture(t, l)
			b := l.cfg.Policy.Backends["fixture"]
			b.Model = "test-model"
			l.cfg.Policy.Backends["fixture"] = b
			l.cfg.Policy.Seats["plan"] = "fixture"
			l.cfg.Policy.Seats["verify"] = "fixture"
			l.cfg.Policy.Seats["build"] = "fixture"

			raw, err := os.ReadFile("../../../config/tillr/workflows/planned-build@v1.json")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(f.root, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json"), string(raw))
			writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
			writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
			runGit(t, f.root, "add", "config/tillr/workflows/planned-build@v1.json", "bin/pi-forks.list", ".gitignore")
			runGit(t, f.root, "commit", "-qm", "add workflow and fork catalog")
			plantRepoFormatCheck(t, f.root)

			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
					return err
				}
				_, err := tx.Exec(`UPDATE items SET workflow='planned-build@v1', seat='build', allowed_paths='["tools/item-a/"]' WHERE item_id='item-a'`)
				return err
			})

			steps := &formatWorkflowSteps{workflowSteps: baseSteps, mode: mode}
			l.cfg.Steps = steps

			var result LoopResult
			if mode == "work-still-unformatted" {
				for i := 0; i < 4; i++ {
					graphPass(t, l, &result)
				}
				if steps.verifyCalls != 0 || steps.gateCalls != 0 || len(steps.landedHeads) != 0 {
					t.Fatalf("unformatted work reached verify/gate: verify=%d gates=%d landed=%d", steps.verifyCalls, steps.gateCalls, len(steps.landedHeads))
				}
				db := openFixture(t, f.storePath)
				defer db.Close()
				if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
					var body []byte
					if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1`, RefusalKind).Scan(&body); err != nil {
						return err
					}
					var refusal refusalPayload
					if err := json.Unmarshal(body, &refusal); err != nil {
						return err
					}
					if refusal.Step != RefusalFence || !strings.Contains(refusal.Detail, "tools/item-a/holdrules.go") {
						t.Fatalf("refusal = %+v, want step=%s naming tools/item-a/holdrules.go", refusal, RefusalFence)
					}
					return nil
				}); err != nil {
					t.Fatalf("refusal query err=%v; requests=%+v", err, steps.requests)
				}
				return
			}
			for i := 0; i < 8 && len(steps.landedHeads) == 0; i++ {
				graphPass(t, l, &result)
			}
			if len(steps.landedHeads) != 1 || steps.gateCalls != 1 {
				db := openFixture(t, f.storePath)
				defer db.Close()
				_ = db.ReadTx(context.Background(), func(r *store.Reader) error {
					rows, err := r.Query(`SELECT seq, kind, object_id, payload FROM events ORDER BY seq DESC LIMIT 12`)
					if err != nil {
						return err
					}
					defer rows.Close()
					for rows.Next() {
						var seq int
						var kind, obj string
						var pay []byte
						_ = rows.Scan(&seq, &kind, &obj, &pay)
						t.Logf("event %d %s %s: %s", seq, kind, obj, string(pay))
					}
					return nil
				})
				t.Fatalf("landed=%d gates=%d verify=%d requests=%d", len(steps.landedHeads), steps.gateCalls, steps.verifyCalls, len(steps.requests))
			}
			if mode == "work-before-verify" && steps.verifyCalls != 1 {
				t.Fatalf("verifyCalls=%d, want 1", steps.verifyCalls)
			}
		})
	}
}
