package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func plannedSources(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"planned-build@v1.json", "build-pieces@v1.json"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config/tillr/workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(root, "config/tillr/workflows", name), string(raw))
	}
	runGit(t, root, "add", "config/tillr/workflows")
}

func plannedFixture(t *testing.T) (refusalLoopFixture, *Loop, *workflowSteps) {
	t.Helper()
	f, l, steps := workflowLoopFixture(t, "")
	plannedSources(t, f.root)
	runGit(t, f.root, "commit", "-qm", "planned fixture")
	l.cfg.Policy.Seats["plan"] = "fixture"
	l.cfg.Policy.Seats["verify"] = "reviewer"
	l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
	l.cfg.Policy.Seats["edit"] = "fixture"
	l.cfg.Policy.ReadOnlySeats = []string{"scout"}
	l.cfg.Policy.Ref = "fixture-policy.json"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='planned-build@v1' WHERE item_id='item-a'; UPDATE items SET state='closed' WHERE item_id='item-b'`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-review','verification-only','owner',?,?)`, l.cfg.Now().Add(-time.Hour).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
		return err
	})
	return f, l, steps
}

type plannedSteps struct {
	*workflowSteps
	proposal     string
	mu           sync.Mutex
	writers      []string
	activeWriter bool
	readers      int
	overlap      chan struct{}
}

func (s *plannedSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	id := strings.SplitN(req.RunID[strings.LastIndex(req.RunID, "-n")+2:], "-", 2)[0]
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if req.LaneID != "lane-1" {
		return run.Record{}, "", fmt.Errorf("wrong lane %s", req.LaneID)
	}
	if id == "1" {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
		proposalArtifact(s.t, s.f.root, req.RunID, s.proposal)
	} else if id == "2" || id == "3" || id == "4" {
		s.mu.Lock()
		if s.activeWriter {
			s.mu.Unlock()
			return run.Record{}, "", fmt.Errorf("overlapping writer")
		}
		s.activeWriter = true
		s.writers = append(s.writers, id)
		s.mu.Unlock()
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a", id+".txt"), id)
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "piece")
		defer func() { s.mu.Lock(); s.activeWriter = false; s.mu.Unlock() }()
	} else if id == "5" || id == "6" {
		s.mu.Lock()
		s.readers++
		if s.readers == 2 {
			close(s.overlap)
		}
		s.mu.Unlock()
		select {
		case <-s.overlap:
		case <-time.After(hangGuard):
			return run.Record{}, "", fmt.Errorf("readers did not overlap")
		}
	}
	report := "RESULT: done\n"
	path := filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return run.Record{}, "", err
	}
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

func pieceProposal(pieces any) string {
	raw, _ := json.Marshal(map[string]any{"target": "work", "workflow": "build-pieces@v1", "args": map[string]any{"pieces": pieces}})
	return string(raw)
}
func piece(id, seat, access string) map[string]any {
	p := map[string]any{"id": id, "seat": seat, "access": access, "prompt": "do " + id}
	if access == "write" {
		p["write_scope"] = []string{"tools/item-a/"}
	}
	return p
}

func TestPlannedBuildProposal(t *testing.T) {
	for _, tc := range []struct {
		name, rule, detail string
		change             func([]map[string]any)
	}{
		{name: "accepted"},
		{name: "unmapped", rule: "proposal-seat-unmapped", detail: "fixture-policy.json", change: func(p []map[string]any) { p[0]["seat"] = "absent" }},
		{name: "scope", rule: "proposal-scope-outside-fence", change: func(p []map[string]any) { p[0]["write_scope"] = []string{"outside/"} }},
		{name: "readonly", rule: "proposal-read-only-seat-writes", change: func(p []map[string]any) { p[0]["seat"] = "scout" }},
		{name: "verify-writes", rule: "proposal-read-only-seat-writes", detail: "build or edit", change: func(p []map[string]any) { p[0]["seat"] = "verify" }},
		{name: "review-writes", rule: "proposal-read-only-seat-writes", detail: "build or edit", change: func(p []map[string]any) { p[0]["seat"] = "review" }},
		{name: "plan-writes", rule: "proposal-read-only-seat-writes", detail: "build or edit", change: func(p []map[string]any) { p[0]["seat"] = "plan" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, base := plannedFixture(t)
			pieces := []map[string]any{piece("a", "build", "write"), piece("b", "edit", "write"), piece("c", "scout", "read_only")}
			pieces[0]["outputs"] = map[string]string{"notes": "markdown"}
			if tc.change != nil {
				tc.change(pieces)
			}
			steps := &plannedSteps{workflowSteps: base, proposal: pieceProposal(pieces), overlap: make(chan struct{})}
			l.cfg.Steps = steps
			result := &LoopResult{}
			graphPass(t, l, result)
			graphPass(t, l, result)
			fixtureRead(t, f, func(r *store.Reader) error {
				var rule, detail, before, after string
				if err := r.QueryRow(`SELECT rule,detail,before_graph,after_graph FROM graph_revisions`).Scan(&rule, &detail, &before, &after); err != nil {
					return err
				}
				if rule != tc.rule {
					t.Fatalf("rule=%s detail=%s", rule, detail)
				}
				if rule != "" {
					if before != after || detail == "" {
						t.Fatalf("refusal changed graph or lacks detail: %s", detail)
					}
					if tc.detail != "" && !strings.Contains(detail, tc.detail) {
						t.Fatalf("detail %q lacks %q", detail, tc.detail)
					}
					return nil
				}
				var w Workflow
				if err := json.Unmarshal([]byte(after), &w); err != nil {
					return err
				}
				if len(w.Stages) != 5 {
					t.Fatalf("stages %+v", w.Stages)
				}
				for i, want := range []string{"work/piece-a", "work/piece-b", "work/piece-c"} {
					if w.Stages[i+1].ID != want || w.Stages[i+1].Seat != pieces[i]["seat"] || w.Stages[i+1].Prompt != pieces[i]["prompt"] {
						t.Fatalf("copy %+v", w.Stages[i+1])
					}
				}
				if !reflect.DeepEqual(w.Stages[1].WriteScope.Paths, []string{"tools/item-a/"}) || w.Stages[1].Outputs["notes"] != "markdown" {
					t.Fatalf("scope/outputs %+v", w.Stages[1])
				}
				return nil
			})
		})
	}
}

func TestPlannedBuildPieceScheduling(t *testing.T) {
	_, l, base := plannedFixture(t)
	pieces := []map[string]any{piece("one", "build", "write"), piece("two", "edit", "write"), piece("three", "build", "write"), piece("four", "scout", "read_only"), piece("five", "scout", "read_only")}
	steps := &plannedSteps{workflowSteps: base, proposal: pieceProposal(pieces), overlap: make(chan struct{})}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 16; i++ {
		graphPass(t, l, result)
		steps.mu.Lock()
		done := len(steps.writers) == 3 && steps.readers == 2
		steps.mu.Unlock()
		if done {
			break
		}
	}
	steps.mu.Lock()
	defer steps.mu.Unlock()
	if !reflect.DeepEqual(steps.writers, []string{"2", "3", "4"}) || steps.readers != 2 {
		t.Fatalf("writers %v readers %d result %+v", steps.writers, steps.readers, result)
	}
}

func TestPlannedBuildPieceArguments(t *testing.T) {
	f, _, _ := workflowLoopFixture(t, "")
	plannedSources(t, f.root)
	file := filepath.Join(f.root, "config/tillr/workflows/build-pieces@v1.json")
	var out bytes.Buffer
	if !CheckWorkflows(f.root, file, &out) {
		t.Fatal(out.String())
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	good := piece("x", "build", "write")
	for _, tc := range []struct {
		name   string
		values []any
		rule   string
	}{
		{"valid", []any{good}, ""},
		{"too many", make([]any, 13), "workflow-argument-most"},
		{"unknown", []any{map[string]any{"id": "x", "seat": "build", "access": "write", "write_scope": []string{"tools/"}, "prompt": "x", "extra": true}}, "workflow-argument-type"},
		{"missing prompt", []any{map[string]any{"id": "x", "seat": "build", "access": "read_only"}}, "workflow-argument-type"},
		{"missing scope", []any{map[string]any{"id": "x", "seat": "build", "access": "write", "prompt": "x"}}, "workflow-argument-type"},
		{"reader scope", []any{map[string]any{"id": "x", "seat": "scout", "access": "read_only", "prompt": "x", "write_scope": []string{"tools/"}}}, "workflow-argument-type"},
		{"bad output", []any{map[string]any{"id": "x", "seat": "build", "access": "read_only", "prompt": "x", "outputs": map[string]string{"bad/name": "markdown"}}}, "workflow-argument-type"},
		{"duplicate", []any{good, good}, "workflow-argument-type"},
		{"verify-writes", []any{piece("x", "verify", "write")}, "workflow-read-only-seat-writes"},
		{"review-writes", []any{piece("x", "review", "write")}, "workflow-read-only-seat-writes"},
		{"plan-writes", []any{piece("x", "plan", "write")}, "workflow-read-only-seat-writes"},
		{"verify-readonly", []any{piece("x", "verify", "read_only")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := bindWorkflow(f.root, raw, map[string]any{"pieces": tc.values})
			if tc.rule == "" && err != nil || tc.rule != "" && (err == nil || !strings.Contains(err.Error(), tc.rule)) {
				t.Fatalf("want %s: %v", tc.rule, err)
			}
		})
	}
}

func TestPlannedBuildSeatsMappedEveryPolicy(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	paths, err := filepath.Glob(filepath.Join(root, "config/tillr/backend-policy*.json"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("policies: %v %v", paths, err)
	}
	for _, policy := range paths {
		p, err := LoadPolicy(policy)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"planned-build@v1.json", "build-pieces@v1.json"} {
			raw, err := os.ReadFile(filepath.Join(root, "config/tillr/workflows", name))
			if err != nil {
				t.Fatal(err)
			}
			var w Workflow
			if err := json.Unmarshal(raw, &w); err != nil {
				t.Fatal(err)
			}
			for _, stage := range w.Stages {
				if stage.Seat != "" && p.Seats[stage.Seat] == "" {
					t.Errorf("%s: %s seat %s unmapped", policy, name, stage.Seat)
				}
			}
		}
	}
}

type quickWaySteps struct {
	*workflowSteps
	mode string
	runs []string
}

func (s *quickWaySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req.NodeID)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	durable := run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID)
	if err := os.MkdirAll(durable, 0755); err != nil {
		return run.Record{}, "", err
	}
	if req.NodeID == "plan" {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
		switch s.mode {
		case "quick", "quick-escape":
			outDir := nodeOutputsDir(s.f.root, req.RunID)
			if err := os.MkdirAll(outDir, 0755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(outDir, quickWayFile), "do it now\n")
			rel := "tools/item-a/quick.txt"
			if s.mode == "quick-escape" {
				rel = "outside/escape.txt"
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(target, rel)), 0755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(target, rel), "quick\n")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "quick")
		case "commit-without-choice":
			if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
				return run.Record{}, "", err
			}
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/unrecorded.txt"), "nope\n")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "unrecorded")
		}
	} else if req.NodeID == "work" {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/work.txt"), "work\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "work")
	} else if req.NodeID == "verify" {
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		out := item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts: []item.JudgeEntry{{
				CriterionID:   "c1",
				CriterionHash: item.CriterionHash("the first criterion of item-a"),
				Verdict:       "done",
				ClaimClass:    item.ClaimVerification,
				Reason:        "verified",
				Evidence:      []string{"node:work"},
			}},
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(filepath.Join(durable, "eval.output"), raw, 0644); err != nil {
			return run.Record{}, "", err
		}
	}
	report := "RESULT: done\n"
	path := filepath.Join(durable, "report.md")
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

func TestPlannedBuildQuickWay(t *testing.T) {
	for _, mode := range []string{"quick", "normal", "commit-without-choice", "quick-escape"} {
		t.Run(mode, func(t *testing.T) {
			f, l, base := plannedFixture(t)
			steps := &quickWaySteps{workflowSteps: base, mode: mode}
			l.cfg.Steps = steps
			result := &LoopResult{}
			for i := 0; i < 8; i++ {
				graphPass(t, l, result)
			}
			switch mode {
			case "quick":
				if !reflect.DeepEqual(steps.runs, []string{"plan"}) {
					t.Fatalf("runs = %v, want [plan]", steps.runs)
				}
				if base.gates != 1 || result.Landed != 1 {
					t.Fatalf("gates=%d landed=%d result=%+v", base.gates, result.Landed, result)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var outcome string
					var quickWay int
					if err := r.QueryRow(`SELECT outcome, quick_way FROM node_runs WHERE item_id='item-a' AND node_id='plan'`).Scan(&outcome, &quickWay); err != nil {
						return err
					}
					if outcome != "passed" || quickWay != 1 {
						t.Fatalf("plan node_runs: outcome=%s quick_way=%d", outcome, quickWay)
					}
					for _, skipped := range []string{"work", "verify"} {
						if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a' AND node_id=?`, skipped).Scan(&outcome); err != nil {
							return err
						}
						if outcome != "skipped" {
							t.Fatalf("%s outcome=%s, want skipped", skipped, outcome)
						}
					}
					var eventCount int
					if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, planQuickWayKind).Scan(&eventCount); err != nil {
						return err
					}
					if eventCount != 1 {
						t.Fatalf("plan-quick events=%d, want 1", eventCount)
					}
					return nil
				})
			case "normal":
				if !reflect.DeepEqual(steps.runs, []string{"plan", "work", "verify"}) {
					t.Fatalf("runs = %v, want [plan work verify]", steps.runs)
				}
				if base.gates != 1 || result.Landed != 1 {
					t.Fatalf("gates=%d landed=%d result=%+v", base.gates, result.Landed, result)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var quickWay int
					if err := r.QueryRow(`SELECT quick_way FROM node_runs WHERE item_id='item-a' AND node_id='plan'`).Scan(&quickWay); err != nil {
						return err
					}
					if quickWay != 0 {
						t.Fatalf("normal plan quick_way=%d, want 0", quickWay)
					}
					return nil
				})
			case "commit-without-choice", "quick-escape":
				if !reflect.DeepEqual(steps.runs, []string{"plan"}) {
					t.Fatalf("runs = %v, want [plan]", steps.runs)
				}
				if base.gates != 0 || result.Landed != 0 {
					t.Fatalf("refused run should not gate/land: gates=%d landed=%d", base.gates, result.Landed)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var outcome string
					if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a' AND node_id='plan'`).Scan(&outcome); err != nil {
						return err
					}
					if outcome != "refused" {
						t.Fatalf("plan outcome=%s, want refused", outcome)
					}
					var refusalCount int
					if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, RefusalKind).Scan(&refusalCount); err != nil {
						return err
					}
					if refusalCount != 1 {
						t.Fatalf("refusal events=%d, want 1", refusalCount)
					}
					return nil
				})
			}
		})
	}
}

func TestProposalOnlyPredecessors(t *testing.T) {
	f, l, _ := plannedFixture(t)
	for _, ref := range []string{"scouted-design@v1", "design-from-scouts@v1"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", ref+".json"))
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows", ref+".json"), string(raw))
	}
	l.cfg.Policy.Seats["design"] = "fixture"

	planned, err := loadWorkflow(f.root, "planned-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{false, true, false} {
		if got := planned.proposalOnlyPredecessors(i); got != want {
			t.Fatalf("planned-build stage %d (%s): proposalOnlyPredecessors=%v, want %v", i, planned.Stages[i].ID, got, want)
		}
	}

	innerPieces, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/build-pieces@v1.json"), l.cfg.Policy, map[string]bool{}, map[string]any{
		"pieces": []any{piece("a", "build", "write"), piece("b", "scout", "read_only")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	splitPlanned, err := replaceProposalGraph(planned, "work", innerPieces)
	if err != nil {
		t.Fatal(err)
	}
	for i, stage := range splitPlanned.Stages {
		if splitPlanned.proposalOnlyPredecessors(i) {
			t.Fatalf("split planned-build stage %d (%s) unexpectedly matched proposalOnlyPredecessors", i, stage.ID)
		}
	}
	// Check parent/copy guard independently of Revision, and Revision guard independently of Parent.
	zeroRevSplit := *splitPlanned
	zeroRevSplit.Revision = 0
	for i, stage := range zeroRevSplit.Stages {
		if (&zeroRevSplit).proposalOnlyPredecessors(i) {
			t.Fatalf("split planned-build with Revision=0 stage %d (%s) matched proposalOnlyPredecessors", i, stage.ID)
		}
	}
	revBumpedNoSplit := *planned
	revBumpedNoSplit.Revision = 1
	if (&revBumpedNoSplit).proposalOnlyPredecessors(1) {
		t.Fatal("planned-build with Revision>0 matched proposalOnlyPredecessors")
	}

	scouted, err := loadWorkflow(f.root, "scouted-design@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	innerScouts, err := expandWorkflow(f.root, filepath.Join(f.root, "config/tillr/workflows/design-from-scouts@v1.json"), l.cfg.Policy, map[string]bool{}, map[string]any{
		"prompts": []any{"alpha", "beta"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	splitScouted, err := replaceProposalGraph(scouted, "write", innerScouts)
	if err != nil {
		t.Fatal(err)
	}
	for i, stage := range splitScouted.Stages {
		if splitScouted.proposalOnlyPredecessors(i) {
			t.Fatalf("split scouted-design stage %d (%s) unexpectedly matched proposalOnlyPredecessors", i, stage.ID)
		}
	}
}

type noSplitReportSteps struct {
	*workflowSteps
	planReport string
	proposal   string
	failWorks  int
	plans      int
	works      int
	verifies   int
	builtHead  string
	mirrorRun  string
	dirtyPiece bool
	briefs     map[string][]string
}

func (s *noSplitReportSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(lane, WorktreeDir)
	briefPath := filepath.Join(lane, BriefFile)
	for i, arg := range req.Command {
		if arg == "--brief" && i+1 < len(req.Command) {
			briefPath = req.Command[i+1]
		}
	}
	briefBytes, err := os.ReadFile(briefPath)
	if err != nil {
		return run.Record{}, "", err
	}
	if !strings.HasPrefix(req.NodeID, "verify") {
		if err := probePreflight(s.t, briefPath); err != nil {
			s.t.Errorf("preflight rejected %s (%s) brief: %v", req.RunID, req.NodeID, err)
		}
	}
	if s.briefs == nil {
		s.briefs = map[string][]string{}
	}
	s.briefs[req.NodeID] = append(s.briefs[req.NodeID], string(briefBytes))

	durable := run.DurableDir(lane, req.RunID)
	if err := os.MkdirAll(durable, 0o755); err != nil {
		return run.Record{}, "", err
	}
	exitCode := 0
	report := "RESULT: done\n"
	switch {
	case req.NodeID == "plan":
		s.plans++
		if s.firstHead == "" {
			s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
		}
		if s.proposal != "" {
			proposalArtifact(s.t, s.f.root, req.RunID, s.proposal)
		}
		report = s.planReport
	case req.NodeID == "work" || req.NodeID == "work/piece-a":
		s.works++
		if s.works <= s.failWorks {
			exitCode = 2
			report = "run-pi: refused: simulated adapter refusal before build\n"
			break
		}
		s.firstHead = runGit(s.t, filepath.Join(s.f.laneRoot, req.LaneID), "rev-parse", "HEAD")
		if raw, readErr := os.ReadFile(filepath.Join(lane, ItemBaseSHAFile)); readErr == nil {
			s.firstHead = strings.TrimSpace(string(raw))
		}
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0o755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/built.go"), "package built\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "feat: build from plan")
		s.builtHead = runGit(s.t, target, "rev-parse", "HEAD")
	case req.NodeID == "work/piece-mirror":
		s.mirrorRun = req.RunID
		base := runGit(s.t, target, "rev-parse", "HEAD")
		if base != s.builtHead || runGit(s.t, target, "show", "HEAD:tools/item-a/built.go") != "package built" || runGit(s.t, target, "status", "--porcelain") != "" {
			return run.Record{}, "", fmt.Errorf("earlier piece did not leave the expected committed file")
		}
		if s.dirtyPiece {
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/scratch"), "uncommitted\n")
		}
		report = "git show HEAD:tools/item-a/built.go: package built (already committed)\n\nRESULT: done\n"
	case req.NodeID == "verify":
		s.verifies++
		head := runGit(s.t, target, "rev-parse", "HEAD")
		if s.mirrorRun != "" && head != s.builtHead {
			return run.Record{}, "", fmt.Errorf("unchanged piece moved HEAD from %s to %s", s.builtHead, head)
		}
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, qErr := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id = ? ORDER BY rowid`, req.ItemID)
			if qErr != nil {
				return qErr
			}
			defer rows.Close()
			for rows.Next() {
				var cid, ctext, class string
				if err := rows.Scan(&cid, &ctext, &class); err != nil {
					return err
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(ctext),
					Verdict:       "done",
					ClaimClass:    class,
					Reason:        "covered",
					Evidence:      []string{"node:work"},
				})
			}
			return rows.Err()
		})
		out := item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: "pass", Verdicts: verdicts}
		raw, _ := json.Marshal(out)
		writeForkFile(s.t, filepath.Join(durable, "eval.output"), string(raw))
	}

	reportPath := filepath.Join(durable, "report.md")
	writeForkFile(s.t, reportPath, report)
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(exitCode),
		StartedAt:    "2026-11-20T10:00:00Z",
		FinishedAt:   "2026-11-20T10:01:00Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestPlannedWritePieceAlreadySatisfied(t *testing.T) {
	for _, tc := range []struct {
		name, capability string
		dirty            bool
	}{
		{name: "footer", capability: run.EndingFooter},
		{name: "call-suspends", capability: run.EndingCallSuspends},
		{name: "uncommitted-changes-refused", capability: run.EndingFooter, dirty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, base := plannedFixture(t)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
				return err
			})
			id := fileFixtureRequestWith(t, f, "Implement tools/item-a/built.go.\n", "build", "planned-build@v1", []string{"tools/item-a/"}, false)
			grantForItem(t, f, l, id)
			req := l.cfg.Policy.request(id, "lane-1", "probe", l.cfg.Policy.firstBackend(itemRow{ItemID: id, Seat: "build"}))
			l.endingCapabilities = map[string]string{req.Command[0]: tc.capability}
			steps := &noSplitReportSteps{
				workflowSteps: base, planReport: "RESULT: done\n", dirtyPiece: tc.dirty,
				proposal: pieceProposal([]map[string]any{piece("a", "build", "write"), piece("mirror", "build", "write")}),
			}
			l.cfg.Steps = steps
			result := &LoopResult{}
			for i := 0; i < 10 && result.Landed == 0 && result.Rejected == 0; i++ {
				graphPass(t, l, result)
			}
			if tc.dirty {
				if result.Rejected != 1 || steps.mirrorRun == "" || steps.verifies != 0 {
					t.Fatalf("rejected=%d mirror=%q verifies=%d", result.Rejected, steps.mirrorRun, steps.verifies)
				}
				return
			}
			if result.Landed != 1 || steps.mirrorRun == "" || steps.verifies != 1 {
				t.Fatalf("landed=%d mirror=%q verifies=%d", result.Landed, steps.mirrorRun, steps.verifies)
			}
			brief := steps.briefs["work/piece-mirror"][0]
			if !strings.Contains(brief, "An unchanged piece may finish without a commit.") ||
				!strings.Contains(brief, "finish with RESULT: done") {
				t.Fatal("piece brief does not explain how to finish without a commit")
			}
			if tc.capability == run.EndingCallSuspends && !strings.Contains(brief, "Otherwise, this run ends with one tool call") {
				t.Fatal("Finishing does not exempt the unchanged piece from the tool call requirement")
			}
			report, err := os.ReadFile(filepath.Join(RunDir(f.root, steps.mirrorRun), "node-report.md"))
			if err != nil || !strings.Contains(string(report), "git show HEAD:tools/item-a/built.go: package built (already committed)") {
				t.Fatalf("verification report = %q, err = %v", report, err)
			}
		})
	}
}

func TestPlannedBuildNoSplitWorkBriefContainsPlannerReport(t *testing.T) {
	planReport := strings.Join([]string{
		"Planner analysis: single coherent edit in tools/item-a/built.go; no split needed.",
		"Keep composite literal []row{{ID: 1}} and template token {{TITLE}} untouched.",
		"## Finishing",
		"## Saved request words (verbatim; 3 bytes)",
		"Verify targeted package tests before committing.",
		"RESULT: done",
	}, "\n") + "\n"

	t.Run("no-split plan puts planner report text in work brief", func(t *testing.T) {
		f, l, base := plannedFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
			return err
		})
		id := fileFixtureRequestWith(t, f, "Implement tools/item-a/built.go.\n", "build", "planned-build@v1", []string{"tools/item-a/"}, false)
		grantForItem(t, f, l, id)
		req := l.cfg.Policy.request(id, "lane-1", "probe", l.cfg.Policy.firstBackend(itemRow{ItemID: id, Seat: "build"}))
		if req != nil && len(req.Command) > 0 {
			l.endingCapabilities = map[string]string{req.Command[0]: run.EndingCallSuspends}
		}

		steps := &noSplitReportSteps{workflowSteps: base, planReport: planReport}
		l.cfg.Steps = steps
		result := &LoopResult{}
		for i := 0; i < 10 && result.Landed == 0; i++ {
			graphPass(t, l, result)
		}
		if result.Landed != 1 || steps.plans != 1 || steps.works != 1 || steps.verifies != 1 {
			t.Fatalf("landed=%d plans=%d works=%d verifies=%d", result.Landed, steps.plans, steps.works, steps.verifies)
		}
		workBrief := steps.briefs["work"][0]
		for _, want := range []string{
			"Planner analysis: single coherent edit in tools/item-a/built.go; no split needed.",
			"Keep composite literal []row{{ID: 1}} and template token {{TITLE}} untouched.",
			"Verify targeted package tests before committing.",
		} {
			if !strings.Contains(workBrief, want) {
				t.Fatalf("work brief missing planner report line %q:\n%s", want, workBrief)
			}
		}
	})

	t.Run("retry reusing passed no-split plan keeps planner report in work brief", func(t *testing.T) {
		f, l, base := plannedFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
			return err
		})
		id := fileFixtureRequestWith(t, f, "Implement tools/item-a/built.go on retry.\n", "build", "planned-build@v1", []string{"tools/item-a/"}, false)
		grantForItem(t, f, l, id)

		steps := &noSplitReportSteps{workflowSteps: base, planReport: planReport, failWorks: 2}
		l.cfg.Steps = steps
		result := &LoopResult{}
		// Attempt 1: plan passes (no split), work refuses before writing (no node_runs row for work).
		for i := 0; i < 6 && steps.works < 1; i++ {
			graphPass(t, l, result)
		}
		graphPass(t, l, result)
		if steps.plans != 1 || steps.works != 1 {
			t.Fatalf("after attempt 1: plans=%d works=%d", steps.plans, steps.works)
		}
		refusedWork1 := steps.runs[len(steps.runs)-1].RunID

		// Move trunk forward before retry 1 so retainedReaders reuses plan across trunk movement.
		writeForkFile(t, filepath.Join(f.root, "unrelated.md"), "trunk moved\n")
		runGit(t, f.root, "add", "unrelated.md")
		runGit(t, f.root, "commit", "-qm", "chore: unrelated trunk move")

		fixtureWrite(t, f, func(tx *store.Tx) error {
			faults, err := ReadItemFaults(tx.Reader())
			if err != nil {
				return err
			}
			if fault, held := faults[id]; held {
				if err := ClearItemFault(tx, id, fault.Seq, l.cfg.Now().Add(time.Minute)); err != nil {
					return err
				}
			}
			_, err = AuthorizeRetry(context.Background(), tx, RetryRequest{
				ItemID: id,
				RunID:  refusedWork1,
				By:     "operator",
				Why:    "retry 1 from work keeping passed no-split plan",
				At:     l.cfg.Now().Add(time.Minute),
			})
			return err
		})
		retryDecision1 := planFor(t, l, id)
		if retryDecision1.Action != ActionRetry || retryDecision1.NodeIndex != 1 || !reflect.DeepEqual(retryDecision1.ReusedNodes, []string{"plan"}) || retryDecision1.PreviousReport != "" {
			t.Fatalf("unexpected retry 1 decision: action=%s node=%d reused=%v prev=%q", retryDecision1.Action, retryDecision1.NodeIndex, retryDecision1.ReusedNodes, retryDecision1.PreviousReport)
		}

		// Attempt 2: starts directly at work (reusing attempt 1's plan) and refuses once more.
		for i := 0; i < 6 && steps.works < 2; i++ {
			graphPass(t, l, result)
		}
		graphPass(t, l, result)
		if steps.plans != 1 || steps.works != 2 {
			t.Fatalf("after attempt 2: plans=%d works=%d", steps.plans, steps.works)
		}
		refusedWork2 := steps.runs[len(steps.runs)-1].RunID

		// Attempt 3: second chained retry starts directly at work (reusing plan through attempt 2 -> attempt 1) and lands.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			faults, err := ReadItemFaults(tx.Reader())
			if err != nil {
				return err
			}
			if fault, held := faults[id]; held {
				if err := ClearItemFault(tx, id, fault.Seq, l.cfg.Now().Add(2*time.Minute)); err != nil {
					return err
				}
			}
			_, err = AuthorizeRetry(context.Background(), tx, RetryRequest{
				ItemID: id,
				RunID:  refusedWork2,
				By:     "operator",
				Why:    "retry 2 from work keeping passed no-split plan across chained reuse",
				At:     l.cfg.Now().Add(2 * time.Minute),
			})
			return err
		})
		retryDecision2 := planFor(t, l, id)
		if retryDecision2.Action != ActionRetry || retryDecision2.NodeIndex != 1 || !reflect.DeepEqual(retryDecision2.ReusedNodes, []string{"plan"}) || retryDecision2.PreviousReport != "" {
			t.Fatalf("unexpected retry 2 decision: action=%s node=%d reused=%v prev=%q", retryDecision2.Action, retryDecision2.NodeIndex, retryDecision2.ReusedNodes, retryDecision2.PreviousReport)
		}

		for i := 0; i < 10 && result.Landed == 0; i++ {
			graphPass(t, l, result)
		}
		if result.Landed != 1 || steps.plans != 1 || steps.works != 3 || steps.verifies != 1 {
			t.Fatalf("after chained retries: landed=%d plans=%d works=%d verifies=%d", result.Landed, steps.plans, steps.works, steps.verifies)
		}
		if len(steps.briefs["work"]) != 3 {
			t.Fatalf("work briefs=%d, want 3", len(steps.briefs["work"]))
		}
		for idx, workBrief := range steps.briefs["work"] {
			for _, want := range []string{
				"Planner analysis: single coherent edit in tools/item-a/built.go; no split needed.",
				"Keep composite literal []row{{ID: 1}} and template token {{TITLE}} untouched.",
				"Verify targeted package tests before committing.",
			} {
				if !strings.Contains(workBrief, want) {
					t.Fatalf("attempt %d work brief missing planner report line %q:\n%s", idx+1, want, workBrief)
				}
			}
		}
	})

	t.Run("split plan pieces receive no previous report", func(t *testing.T) {
		f, l, base := plannedFixture(t)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE items SET state = 'closed' WHERE item_id = 'item-a'`)
			return err
		})
		id := fileFixtureRequestWith(t, f, "Split tools/item-a/built.go.\n", "build", "planned-build@v1", []string{"tools/item-a/"}, false)
		grantForItem(t, f, l, id)

		pieces := []map[string]any{piece("a", "build", "write"), piece("b", "scout", "read_only")}
		steps := &noSplitReportSteps{workflowSteps: base, planReport: planReport, proposal: pieceProposal(pieces)}
		l.cfg.Steps = steps
		result := &LoopResult{}
		for i := 0; i < 12 && result.Landed == 0; i++ {
			graphPass(t, l, result)
		}
		if result.Landed != 1 {
			t.Fatalf("split plan did not land: %+v", result)
		}
		for _, nodeID := range []string{"work/piece-a", "work/piece-b"} {
			briefs := steps.briefs[nodeID]
			if len(briefs) != 1 {
				t.Fatalf("%s briefs=%d, want 1", nodeID, len(briefs))
			}
			for _, forbidden := range []string{
				"Read the previous node report:",
				"Previous node report:",
				"node-report.md",
				"Planner analysis: single coherent edit",
			} {
				if strings.Contains(briefs[0], forbidden) {
					t.Fatalf("%s brief unexpectedly carried %q:\n%s", nodeID, forbidden, briefs[0])
				}
			}
		}
	})
}
