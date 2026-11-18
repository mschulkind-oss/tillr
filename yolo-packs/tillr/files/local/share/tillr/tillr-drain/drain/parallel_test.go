package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type parallelSteps struct {
	*workflowSteps
	mu            sync.Mutex
	started       int
	ended         int
	overlap       chan struct{}
	dirty         bool
	failNode      string
	builders      int
	interrupt     bool
	writerAfter   int
	wantOverlap   int
	builderSuffix string
	badReportNode string
	badReport     string
	failedStart   bool
	writerFirst   bool
	wallNode      string
	wallAgain     bool
	resumes       int
}

func (s *parallelSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	builderSuffix := s.builderSuffix
	if builderSuffix == "" {
		builderSuffix = "-n5"
	}
	if strings.HasSuffix(req.RunID, builderSuffix) {
		s.mu.Lock()
		ended := s.ended
		s.mu.Unlock()
		want := s.writerAfter
		if want == 0 {
			want = 4
		}
		if s.writerFirst {
			want = 0
		}
		if ended != want {
			return run.Record{}, "", fmt.Errorf("builder started after only %d scouts ended", ended)
		}
		s.mu.Lock()
		s.builders++
		s.mu.Unlock()
		return s.writeRun(ctx, req)
	}
	s.mu.Lock()
	s.started++
	wantOverlap := s.wantOverlap
	if wantOverlap == 0 {
		wantOverlap = 4
	}
	if s.started == wantOverlap {
		close(s.overlap)
	}
	s.mu.Unlock()
	select {
	case <-s.overlap:
	// A serial scheduler never overlaps, so this wait is a hang guard, not a timing bound:
	// load slows every scout alike and cannot make four overlapping ones miss each other.
	case <-time.After(hangGuard):
		return run.Record{}, "", fmt.Errorf("scouts did not overlap: hung")
	}
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if req.LaneID != "lane-1" {
		return run.Record{}, "", fmt.Errorf("extra lane %s", req.LaneID)
	}
	if s.dirty && strings.HasSuffix(req.RunID, "-n2") {
		if err := os.WriteFile(filepath.Join(target, "escape.txt"), []byte("escape"), 0644); err != nil {
			return run.Record{}, "", err
		}
	}
	report := "Scout findings.\nRESULT: done\n"
	if s.badReportNode != "" && strings.HasSuffix(req.RunID, s.badReportNode) {
		report = s.badReport
	}
	dir := run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return run.Record{}, "", err
	}
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	code := 0
	if strings.HasSuffix(req.RunID, s.failNode) && s.failNode != "" {
		code = 1
		report = "Scout failed.\nRESULT: failed\n"
		if err := os.WriteFile(path, []byte(report), 0644); err != nil {
			return run.Record{}, "", err
		}
	}
	tokens := int64(10)
	cost := 0.01
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}, Usage: &run.Usage{Tokens: &tokens, CostUSD: &cost}}
	if req.Resume {
		s.mu.Lock()
		s.resumes++
		s.mu.Unlock()
		rec.Resumed = true
		tokens = 20
		cost = 0.02
	}
	if s.wallNode != "" && strings.HasSuffix(req.RunID, s.wallNode) && (!req.Resume || s.wallAgain) {
		code = 124
	}
	if s.failedStart && strings.HasSuffix(req.RunID, "-n2") {
		rec.State = run.StateFailedToStart
		rec.EndCause = run.EndFailedToStart
		rec.ExitCode = nil
		rec.Usage = nil
	}
	if s.interrupt && (strings.HasSuffix(req.RunID, "-n3") || strings.HasSuffix(req.RunID, "-n4")) {
		rec.State = run.StateTimedOut
		rec.EndCause = run.EndSignal
		rec.ExitCode = nil
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	s.mu.Lock()
	s.ended++
	s.mu.Unlock()
	return rec, report, err
}

func (s *parallelSteps) writeRun(ctx context.Context, req run.Request) (run.Record, string, error) {
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "built\n")
	runGit(s.t, target, "add", ".")
	runGit(s.t, target, "commit", "-qm", "build")
	report := "Build complete.\nRESULT: done\n"
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(path, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	code := 0
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func parallelFixture(t *testing.T) (refusalLoopFixture, *Loop, *parallelSteps) {
	f, l, serial := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id!='item-a'`)
		return err
	})
	raw := `{"name":"scout-then-build","version":1,"stages":[`
	for i := 1; i <= 4; i++ {
		if i > 1 {
			raw += ","
		}
		raw += fmt.Sprintf(`{"id":"scout%d","kind":"agent","seat":"scout","access":"read_only"}`, i)
	}
	raw += `,{"id":"build","kind":"agent","seat":"build","access":"write"}],"edges":[`
	for i := 1; i <= 4; i++ {
		if i > 1 {
			raw += ","
		}
		raw += fmt.Sprintf(`{"from_node":"scout%d","to_node":"build"}`, i)
	}
	raw += `]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "parallel fixture")
	serial.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	// The builder fixture's serial handoff sentinel is unrelated to this test.
	gitdir := strings.TrimSpace(runGit(t, f.root, "rev-parse", "--absolute-git-dir"))
	serial.marker = filepath.Join(gitdir, "parallel-marker")
	writeForkFile(t, serial.marker, "preserved")
	steps := &parallelSteps{workflowSteps: serial, overlap: make(chan struct{})}
	l.cfg.Steps = steps
	return f, l, steps
}

func TestParallelReadersOverlapBeforeWriter(t *testing.T) {
	_, l, steps := parallelFixture(t)
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	steps.mu.Lock()
	started, ended := steps.started, steps.ended
	steps.mu.Unlock()
	if started != 4 || ended != 4 || steps.builders != 1 {
		t.Fatalf("scouts started=%d ended=%d builders=%d; want four on one lane before one builder", started, ended, steps.builders)
	}
	if result.Tokens != 40 || result.SpendUSD != 0.04 {
		t.Fatalf("spend counted incorrectly: %+v", result)
	}
}

func changeParallelWorkflow(t *testing.T, f refusalLoopFixture, change func(*Workflow)) {
	t.Helper()
	path := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var w Workflow
	if err = json.Unmarshal(raw, &w); err != nil {
		t.Fatal(err)
	}
	change(&w)
	raw, err = json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	// WorkflowScope's zero value serializes as inherit, but a read-only
	// template must omit write_scope rather than explicitly inherit writes.
	var object map[string]any
	if err = json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	for _, value := range object["stages"].([]any) {
		stage := value.(map[string]any)
		if stage["access"] == "read_only" {
			delete(stage, "write_scope")
		}
	}
	raw, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, string(raw))
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "configure parallel fixture")
}

func TestParallelReadersHonorDeclaredLimit(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.wantOverlap = 2
	changeParallelWorkflow(t, f, func(w *Workflow) { w.MaxParallel = 2 })
	steps.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	if steps.started != 2 {
		t.Fatalf("first pass started %d readers, want limit 2", steps.started)
	}
	for i := 0; i < 4; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 4 || steps.builders != 1 {
		t.Fatalf("started=%d builders=%d", steps.started, steps.builders)
	}
}

func TestParallelReadersFollowFailureEdgeAfterSiblingsFinish(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.failNode = "-n2"
	changeParallelWorkflow(t, f, func(w *Workflow) {
		w.Edges[1].WhenOutcome = "fail"
		w.Exit = &WorkflowExit{RequiresNodes: []string{"build"}}
	})
	steps.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.ended != 4 || steps.builders != 1 {
		t.Fatalf("ended=%d builders=%d", steps.ended, steps.builders)
	}
}

func TestParallelReadersKeepSiblingsAfterFailure(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.failNode = "-n2"
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.ended != 4 || steps.builders != 0 {
		t.Fatalf("ended=%d builders=%d", steps.ended, steps.builders)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var passed int
		err := r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE outcome='passed'`).Scan(&passed)
		if passed != 3 {
			t.Errorf("passed=%d", passed)
		}
		return err
	})
	for i := 1; i <= 4; i++ {
		if _, err := os.Stat(filepath.Join(RunDir(f.root, fmt.Sprintf("run-item-a-a1-n%d", i)), "node-report.md")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParallelReadersAttributeDirtyTreeToItem(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.dirty = true
	steps.wantOverlap = 3
	steps.builderSuffix = "-n4"
	changeParallelWorkflow(t, f, func(w *Workflow) { w.Stages = append(w.Stages[:3], w.Stages[4:]...); w.Edges = w.Edges[:3] })
	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.ended != 3 || steps.builders != 0 || result.Rejected != 1 {
		t.Fatalf("ended=%d builders=%d result=%+v", steps.ended, steps.builders, result)
	}
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && p.decision.Reason != ReasonBoundaryScopeEscape {
			t.Fatalf("wrong item hold: %+v", p.decision)
		}
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var detail string
		err := r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events WHERE kind='drain.refusal' ORDER BY seq DESC LIMIT 1`).Scan(&detail)
		for i := 1; i <= 3; i++ {
			if !strings.Contains(detail, fmt.Sprintf("scout%d", i)) {
				t.Errorf("missing attribution: %s", detail)
			}
		}
		return err
	})
}
