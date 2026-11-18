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
		name, rule string
		change     func([]map[string]any)
	}{
		{name: "accepted"},
		{name: "unmapped", rule: "proposal-seat-unmapped", change: func(p []map[string]any) { p[0]["seat"] = "absent" }},
		{name: "scope", rule: "proposal-scope-outside-fence", change: func(p []map[string]any) { p[0]["write_scope"] = []string{"outside/"} }},
		{name: "readonly", rule: "proposal-read-only-seat-writes", change: func(p []map[string]any) { p[0]["seat"] = "scout" }},
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
					if tc.name == "unmapped" && !strings.Contains(detail, "fixture-policy.json") {
						t.Fatal(detail)
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
