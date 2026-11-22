package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPlanPromptShowsExactDeliveryObjectShape(t *testing.T) {
	for _, want := range []string{
		`{"kind": "repo"}`,
		`{"kind": "fork", "fork": "<name>"}`,
		`{"kind": "both", "fork": "<name>"}`,
		"JSON object",
	} {
		if !strings.Contains(deliveryPrompt, want) {
			t.Errorf("deliveryPrompt missing %q:\n%s", want, deliveryPrompt)
		}
	}

	raw, err := os.ReadFile("../../../config/tillr/workflows/planned-build@v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var w Workflow
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	planned := planDelivery(&w, "planned-build@v1", itemRow{ItemID: "item-a"}, map[string]bool{"pi": true})
	if len(planned.Stages) == 0 || !strings.Contains(planned.Stages[0].Prompt, `{"kind": "repo"}`) {
		t.Fatalf("planned-build plan stage prompt missing exact delivery object shape:\n%s", planned.Stages[0].Prompt)
	}
}

func TestClassifyDeliveryRejectsBareRepoString(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	path := filepath.Join(t.TempDir(), deliveryOutput)
	writeForkFile(t, path, `"repo"`)

	d, err := classifyDelivery(root, []nodeArtifact{{Name: deliveryOutput, Type: "json", Location: path}}, true)
	if err == nil || d != nil {
		t.Fatalf("bare string \"repo\" must be refused, got decision=%+v err=%v", d, err)
	}
	var parseErr deliveryParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("want deliveryParseError, got %T: %v", err, err)
	}
	const wantErr = "json: cannot unmarshal string into Go value of type drain.deliveryDecision"
	if err.Error() != wantErr {
		t.Fatalf("parse error = %q, want %q", err.Error(), wantErr)
	}
}

type malformedDeliveryPlanSteps struct {
	*workflowSteps
	requests     []run.Request
	sessions     []string
	commitSHA    string
	landedHeads  []string
	sawPreserved bool
}

func (s *malformedDeliveryPlanSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.requests = append(s.requests, req)
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	sess, err := ReadSessionID(laneDir)
	if err != nil {
		return run.Record{}, "", err
	}
	s.sessions = append(s.sessions, sess)

	target := filepath.Join(laneDir, WorktreeDir)
	outDir := nodeOutputsDir(s.f.root, req.RunID)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return run.Record{}, "", err
	}

	if len(s.requests) == 1 {
		// Turn 1: planner does the work in do-it-now mode, commits it, and writes
		// the bare JSON string "repo" as its delivery output.
		writeForkFile(s.t, filepath.Join(outDir, quickWayFile), "do-it-now\n")
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/work.txt"), "finished in do-it-now\n")
		runGit(s.t, target, "add", "tools/item-a/work.txt")
		runGit(s.t, target, "commit", "-qm", "do-it-now work")
		s.commitSHA = strings.TrimSpace(runGit(s.t, target, "rev-parse", "HEAD"))
		writeForkFile(s.t, filepath.Join(outDir, deliveryOutput), `"repo"`)
	} else {
		// Turn 2: same session resumes; verify preserved commit and do-it-now file,
		// then write the corrected delivery JSON object without redoing the commit.
		head := strings.TrimSpace(runGit(s.t, target, "rev-parse", "HEAD"))
		if head != s.commitSHA {
			s.t.Errorf("resumed planner HEAD = %s, want preserved commit %s", head, s.commitSHA)
		}
		if _, err := os.Stat(filepath.Join(outDir, quickWayFile)); err == nil {
			s.sawPreserved = true
		}
		writeForkFile(s.t, filepath.Join(outDir, deliveryOutput), `{"kind": "repo"}`)
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
		StartedAt:    "2026-11-22T12:39:00Z",
		FinishedAt:   "2026-11-22T12:39:05Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, nil
}

func (s *malformedDeliveryPlanSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *malformedDeliveryPlanSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.landedHeads = append(s.landedHeads, c.HeadSHA)
	return s.workflowSteps.Land(ctx, c)
}

func TestPlanDeliveryBareStringReturnsToSameSession(t *testing.T) {
	f, l, baseSteps := workflowLoopFixture(t, "")
	declareWorkerResumeFixture(t, l)
	l.cfg.Policy.Seats["plan"] = "fixture"
	l.cfg.Policy.Seats["verify"] = "fixture"
	l.cfg.Policy.Seats["build"] = "fixture"

	raw, err := os.ReadFile("../../../config/tillr/workflows/planned-build@v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json"), string(raw))
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", "config/tillr/workflows/planned-build@v1.json", "bin/pi-forks.list", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "add workflow and fork catalog")

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET workflow='planned-build@v1', seat='build', allowed_paths='["tools/item-a/"]' WHERE item_id='item-a'`)
		return err
	})

	steps := &malformedDeliveryPlanSteps{workflowSteps: baseSteps}
	l.cfg.Steps = steps

	// Pass 1: planner runs, commits in do-it-now mode, writes bare "repo", gets refused with RefusalPlanOutput.
	var firstResult LoopResult
	graphPass(t, l, &firstResult)
	if firstResult.RefusedSteps[RefusalPlanOutput] != 1 {
		t.Fatalf("want 1 %s refusal, got %+v", RefusalPlanOutput, firstResult.RefusedSteps)
	}
	if firstResult.RefusedSteps[RefusalNodeOutput] != 0 {
		t.Fatalf("unparseable delivery output must not use %s, got %+v", RefusalNodeOutput, firstResult.RefusedSteps)
	}

	const wantParseErr = "json: cannot unmarshal string into Go value of type drain.deliveryDecision"
	var recordedStep, recordedDetail string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return tx.Reader().QueryRow(
			`SELECT json_extract(payload,'$.step'), json_extract(payload,'$.detail') FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`,
			RefusalKind,
		).Scan(&recordedStep, &recordedDetail)
	})
	if recordedStep != RefusalPlanOutput || recordedDetail != wantParseErr {
		t.Fatalf("recorded refusal = (%q, %q), want (%q, %q)", recordedStep, recordedDetail, RefusalPlanOutput, wantParseErr)
	}

	// Inspect the next plan: item must NOT be held for the operator; it must resume the same planner session.
	plans := mustPlan(t, l)
	var next *Decision
	for i := range plans {
		if plans[i].decision.ItemID == "item-a" {
			d := plans[i].decision
			next = &d
			break
		}
	}
	if next == nil {
		t.Fatal("missing next plan decision for item-a")
	}
	if next.Action != ActionDispatch || next.Reason != ReasonReturned {
		t.Fatalf("item must return to planner, not hold: action=%s reason=%s", next.Action, next.Reason)
	}
	if next.RunRequest == nil || !next.RunRequest.SessionTurn || next.WorkerSessionReason != "resumed-worker" {
		t.Fatalf("expected resumed planner session turn, got sessionTurn=%v reason=%q", next.RunRequest != nil && next.RunRequest.SessionTurn, next.WorkerSessionReason)
	}
	if next.LaneID != steps.requests[0].LaneID {
		t.Fatalf("resumed on lane %s, want %s", next.LaneID, steps.requests[0].LaneID)
	}
	for _, want := range []string{
		wantParseErr,
		`{"kind": "repo"}`,
		`{"kind": "fork", "fork": "<name>"}`,
		`{"kind": "both", "fork": "<name>"}`,
		filepath.Join(nodeOutputsDir(f.root, next.RunRequest.RunID), deliveryOutput),
	} {
		if !strings.Contains(next.RunRequest.ResumePrompt, want) {
			t.Errorf("resume prompt missing %q:\n%s", want, next.RunRequest.ResumePrompt)
		}
	}

	// Pass 2: resumed planner writes {"kind": "repo"}; original commit settles and lands.
	var secondResult LoopResult
	graphPass(t, l, &secondResult)
	if len(steps.requests) != 2 {
		t.Fatalf("want 2 planner runs, got %d", len(steps.requests))
	}
	if !steps.requests[1].SessionTurn {
		t.Fatal("second planner run was not a resumed SessionTurn")
	}
	if steps.sessions[0] == "" || steps.sessions[1] != steps.sessions[0] {
		t.Fatalf("planner session changed: first=%q second=%q", steps.sessions[0], steps.sessions[1])
	}
	if !steps.sawPreserved {
		t.Fatal("resumed planner did not receive its preserved do-it-now output")
	}
	if len(steps.landedHeads) != 1 || steps.landedHeads[0] != steps.commitSHA {
		t.Fatalf("landed heads = %v, want [%s]; secondResult=%+v", steps.landedHeads, steps.commitSHA, secondResult)
	}
}
