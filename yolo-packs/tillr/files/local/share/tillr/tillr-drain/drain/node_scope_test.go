package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestNodeScopeLoader(t *testing.T) {
	for _, tc := range []struct{ name, access, scope, rule string }{
		{"inherit", "write", `"inherit"`, ""},
		{"paths", "write", `["src/"]`, ""},
		{"wrong-string", "write", `"all"`, "workflow-write-scope"},
		{"wrong-type", "write", `42`, "workflow-write-scope"},
		{"null", "write", `null`, "workflow-write-scope"},
		{"mixed", "write", `["src/",42]`, "workflow-write-scope"},
		{"null-path", "write", `[null]`, "workflow-write-scope"},
		{"empty-path", "write", `[""]`, "workflow-write-scope"},
		{"multiline-path", "write", `["src/\noutside/"]`, "workflow-write-scope"},
		{"empty", "write", `[]`, "workflow-write-scope-empty"},
		{"read-only", "read_only", `"inherit"`, "workflow-write-scope-read-only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"name":"scope","version":1,"stages":[{"id":"one","kind":"agent","seat":"build","access":"` + tc.access + `","write_scope":` + tc.scope + `}],"edges":[]}`
			_, err := decodeWorkflow([]byte(raw), "scope@v1")
			if tc.rule == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.rule+":") {
				t.Fatalf("want %s, got %v", tc.rule, err)
			}
		})
	}
	w, err := loadWorkflow("../../..", "scout-then-build@v1", &Policy{Seats: map[string]string{"scout": "fixture", "build": "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(w.Stages[1])
	if err != nil || !strings.Contains(string(encoded), `"write_scope":"inherit"`) {
		t.Fatalf("shipped writing node did not inherit: %s / %v", encoded, err)
	}
}

func TestNodeScopePreservesNamedOutputWiring(t *testing.T) {
	raw := strings.Replace(namedWorkflow, `"access":"write"`, `"access":"write","write_scope":["tools/item-a/"]`, 1)
	w, err := decodeWorkflow([]byte(raw), "scout-then-build@v1")
	if err != nil {
		t.Fatal(err)
	}
	if w.Stages[0].Outputs["findings"] != "json" || w.Stages[2].Inputs["evidence"] != "json" {
		t.Fatalf("named output wiring lost: %+v", w.Stages)
	}
	scope, err := w.Stages[2].scopePaths(nil)
	if err != nil || len(scope) != 1 || scope[0] != "tools/item-a/" {
		t.Fatalf("writing scope lost: %v / %v", scope, err)
	}
	if err := w.checkScopes([]string{"tools/item-a/"}); err != nil {
		t.Fatal(err)
	}
}

func TestNodeScopeLegacyContinuationKeepsItemBase(t *testing.T) {
	for _, mode := range []string{"legacy", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			f := newStagingFixture(t, "src/")
			stager := f.stager()
			spec := f.spec("first")
			spec.Node = &WorkflowStage{ID: "one", Access: "write"}
			if err := stager.Stage(context.Background(), spec); err != nil {
				t.Fatal(err)
			}
			lane := filepath.Join(f.laneRoot, spec.LaneID)
			for _, file := range []string{ItemBaseSHAFile, ItemFenceFile} {
				if err := os.Remove(filepath.Join(lane, file)); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "read-error" {
				if err := os.Mkdir(filepath.Join(lane, ItemBaseSHAFile), 0755); err != nil {
					t.Fatal(err)
				}
			}
			target := filepath.Join(lane, WorktreeDir)
			if err := os.MkdirAll(filepath.Join(target, "src"), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(target, "src/one.txt"), "first node\n")
			runGit(t, target, "add", ".")
			runGit(t, target, "commit", "-qm", "first node")
			head := runGit(t, target, "rev-parse", "HEAD")
			spec.RunID, spec.Continue = "second", true
			spec.Node = &WorkflowStage{ID: "two", Access: "write"}
			err := stager.Stage(context.Background(), spec)
			if mode == "read-error" {
				if err == nil || !strings.Contains(err.Error(), "reading the item's base") {
					t.Fatalf("non-missing read error ignored: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for file, want := range map[string]string{ItemBaseSHAFile: f.base, BaseSHAFile: head} {
				raw, err := os.ReadFile(filepath.Join(lane, file))
				if err != nil || strings.TrimSpace(string(raw)) != want {
					t.Fatalf("%s: %s != %s / %v", file, raw, want, err)
				}
			}
		})
	}
}

func TestNodeScopeUsesItemExpansion(t *testing.T) {
	w, err := decodeWorkflow([]byte(strings.Replace(workflowFixture, `"access":"write"`, `"access":"write","write_scope":["tools/tillr-drain/drain/"]`, 1)), "scout-then-build@v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.checkScopes([]string{"tools/tillr-drain/drain/"}); err != nil {
		t.Fatal(err)
	}
	scope, err := w.Stages[1].scopePaths(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"yolo-packs/tillr/files/local/share/tillr/tillr-drain/", "tools/tillr-drain/README.md", tillrSkillPath, tillrDocsTest} {
		if !insideFence(want, scope) {
			t.Fatalf("missing companion %s in %v", want, scope)
		}
	}
	if err := w.checkScopes([]string{"tools/tillr-drain/drain/workflow.go"}); err == nil || !strings.Contains(err.Error(), ReasonNodeScopeOutsideFence) {
		t.Fatalf("broader scope accepted: %v", err)
	}
}

func TestNodeScopeOutsideFenceHoldsBeforeDispatch(t *testing.T) {
	for _, scope := range []string{"outside/", "tools/item-a/../../outside/", "tools/tillr-view/"} {
		t.Run(scope, func(t *testing.T) {
			f, l, steps := workflowLoopFixture(t, "")
			raw := strings.Replace(workflowFixture, `"access":"write"`, `"access":"write","write_scope":["`+scope+`"]`, 1)
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
			found := false
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" {
					found = true
					if p.decision.Action != ActionHold || !strings.HasPrefix(p.decision.Reason, "node-scope-outside-fence:") || !strings.Contains(p.decision.Reason, "work") || p.decision.RunRequest != nil {
						t.Fatalf("not held: %+v", p.decision)
					}
				}
			}
			if !found || len(steps.runs) != 0 {
				t.Fatal("missing hold or ran a node")
			}
		})
	}
}

type scopedWorkflowSteps struct {
	*workflowSteps
	targets, bases, heads, changes []string
	escape                         bool
	lands                          int
}

func (s *scopedWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	n := len(s.runs)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if s.fork != "" {
		target = s.fork
	}
	s.targets = append(s.targets, target)
	base := runGit(s.t, target, "rev-parse", "HEAD")
	s.bases = append(s.bases, base)
	if n == 1 {
		s.firstHead = base
	}
	lane := filepath.Join(s.f.laneRoot, req.LaneID)
	raw, err := os.ReadFile(filepath.Join(lane, BaseSHAFile))
	if err != nil || strings.TrimSpace(string(raw)) != base {
		s.t.Fatalf("node %d staged base %s != %s: %v", n, raw, base, err)
	}
	dir := "one"
	if n == 2 {
		dir = "two"
	}
	prefix := ""
	if s.fork != "" {
		prefix = ".forks/lanes/" + req.LaneID + "/pi/"
	}
	wantFence := prefix + "tools/item-a/" + dir + "/"
	raw, err = os.ReadFile(filepath.Join(lane, FenceFile))
	if err != nil || string(raw) != wantFence+"\n" {
		s.t.Fatalf("node %d fence %s: %v", n, raw, err)
	}
	brief, err := os.ReadFile(filepath.Join(lane, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	_, fenceBlock, ok := strings.Cut(string(brief), "## Fence\n")
	fenceBlock, _, _ = strings.Cut(fenceBlock, "## Settled decisions")
	if !ok || !strings.Contains(fenceBlock, wantFence) || strings.Contains(fenceBlock, prefix+"tools/item-a/\n") {
		s.t.Fatalf("node fence not in brief: %s", fenceBlock)
	}
	if s.escape && n == 1 {
		dir = "two"
	}
	file := "tools/item-a/" + dir + "/result.txt"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(target, file)), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, filepath.Join(target, file), dir+"\n")
	runGit(s.t, target, "add", ".")
	runGit(s.t, target, "commit", "-qm", "write "+dir)
	head := runGit(s.t, target, "rev-parse", "HEAD")
	s.heads = append(s.heads, head)
	s.changes = append(s.changes, runGit(s.t, target, "diff", "--name-only", base+".."+head))
	report := "RESULT: done\n"
	reportPath := filepath.Join(lane, "report.md")
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

func (s *scopedWorkflowSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	if s.fork == "" {
		return s.workflowSteps.Land(ctx, c)
	}
	runGit(s.t, s.f.root, "fetch", "--no-tags", s.fork, c.HeadSHA)
	runGit(s.t, s.f.root, "update-ref", "refs/heads/patch/pi", c.HeadSHA, c.BaseSHA)
	return LandOutcome{Result: LandLanded}, nil
}

func TestNodeScopeOnNewerTrunk(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := &Loop{cfg: LoopConfig{Root: f.root, LaneRoot: f.laneRoot}}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(f.root, "outside.txt"), "trunk change\n")
	runGit(t, f.root, "add", "outside.txt")
	runGit(t, f.root, "commit", "-qm", "trunk landing after dispatch")

	worktree := filepath.Join(f.laneRoot, "rebased-worktree")
	runGit(t, f.root, "worktree", "add", "--detach", worktree, TrunkRef)
	writeForkFile(t, filepath.Join(worktree, "result.txt"), "worker change\n")
	runGit(t, worktree, "add", "result.txt")
	runGit(t, worktree, "commit", "-qm", "worker change")
	candidate := Candidate{Worktree: worktree, BaseSHA: base, HeadSHA: runGit(t, worktree, "rev-parse", "HEAD")}
	// Trunk can advance again before scope is checked. Its tip is not the comparison base.
	writeForkFile(t, filepath.Join(f.root, "outside.txt"), "another trunk change\n")
	runGit(t, f.root, "commit", "-qam", "trunk landing after the worker commit")
	fence := filepath.Join(f.laneRoot, "node-fence.txt")
	writeForkFile(t, fence, "result.txt\n")
	if escape, err := l.fenceViolationAt(context.Background(), candidate, fence); err != nil || escape != "" {
		t.Fatalf("trunk's outside path was charged to the worker: %q / %v", escape, err)
	}

	// Reverting the trunk landing is a worker change even though it restores the old base.
	runGit(t, worktree, "rm", "outside.txt")
	runGit(t, worktree, "commit", "--amend", "-q", "--no-edit")
	candidate.HeadSHA = runGit(t, worktree, "rev-parse", "HEAD")
	if escape, err := l.fenceViolationAt(context.Background(), candidate, fence); err != nil || !strings.Contains(escape, "outside.txt is outside allowed_paths") {
		t.Fatalf("worker's outside path was not refused: %q / %v", escape, err)
	}
}

func TestNodeScopeChangedPathsKeepWhitespace(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	runGit(t, f.root, "switch", "--detach")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(f.root, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "tools/item-a/result.txt "), "escaped exact-file scope\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "space in name")
	fence := filepath.Join(f.laneRoot, "node-fence.txt")
	writeForkFile(t, fence, "tools/item-a/result.txt\n")
	c := Candidate{Worktree: f.root, BaseSHA: base, HeadSHA: runGit(t, f.root, "rev-parse", "HEAD")}
	escape, err := l.fenceViolationAt(context.Background(), c, fence)
	if err != nil || escape == "" {
		t.Fatalf("trailing-space path treated as allowed file: %q / %v", escape, err)
	}
}

func TestNodeScopeRenameChecksSourceDeletion(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	runGit(t, f.root, "switch", "--detach")
	for _, dir := range []string{"tools/item-a/one", "tools/item-a/two"} {
		if err := os.MkdirAll(filepath.Join(f.root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	from, to := "tools/item-a/two/result.txt", "tools/item-a/one/result.txt"
	writeForkFile(t, filepath.Join(f.root, from), "unchanged content\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "rename base")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	if err := os.Rename(filepath.Join(f.root, from), filepath.Join(f.root, to)); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "rename into scope")
	fence := filepath.Join(f.laneRoot, "node-fence.txt")
	writeForkFile(t, fence, "tools/item-a/one/\n")
	c := Candidate{Worktree: f.root, BaseSHA: base, HeadSHA: runGit(t, f.root, "rev-parse", "HEAD")}
	escape, err := l.fenceViolationAt(context.Background(), c, fence)
	if err != nil || !strings.Contains(escape, from) {
		t.Fatalf("out-of-scope rename source deletion admitted: %q / %v", escape, err)
	}
}

func TestNodeScopeSerialBoundaries(t *testing.T) {
	for _, mode := range []string{"serial", "escape", "fork"} {
		t.Run(mode, func(t *testing.T) {
			f, l, old := workflowLoopFixture(t, "")
			steps := &scopedWorkflowSteps{workflowSteps: old, escape: mode == "escape"}
			l.cfg.Steps = steps
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
				return err
			})
			prefix := ""
			if mode == "fork" {
				fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
				if err := os.MkdirAll(fork, 0755); err != nil {
					t.Fatal(err)
				}
				runGit(t, fork, "init", "-q", "-b", "main")
				writeForkFile(t, filepath.Join(fork, "base.txt"), "base\n")
				runGit(t, fork, "add", ".")
				runGit(t, fork, "commit", "-qm", "base")
				writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
				runGit(t, f.root, "add", ".gitignore")
				runGit(t, f.root, "commit", "-qm", "ignore fork")
				runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
				fixtureWrite(t, f, func(tx *store.Tx) error {
					if _, err := tx.Exec(`UPDATE items SET allowed_paths='[".forks/lanes/lane-1/pi/tools/item-a/"]' WHERE item_id='item-a'`); err != nil {
						return err
					}
					return recordFixtureFork(tx, "item-a")
				})
				steps.fork = fork
				prefix = ".forks/lanes/lane-1/pi/"
			}
			raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"one","kind":"agent","seat":"build","access":"write","write_scope":["` + prefix + `tools/item-a/one/"]},{"id":"two","kind":"agent","seat":"build","access":"write","write_scope":["` + prefix + `tools/item-a/two/"]}],"edges":[{"from_node":"one","to_node":"two"}]}`
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
			runGit(t, f.root, "add", "config")
			runGit(t, f.root, "commit", "-qm", "writing graph")
			result := &LoopResult{}
			for i := 0; i < 6; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
				if result.Landed > 0 || result.Rejected > 0 {
					break
				}
			}
			if mode == "escape" {
				if len(steps.runs) != 1 || steps.gates != 0 || result.Rejected != 1 {
					t.Fatalf("escape ran successor: %+v / %+v", steps, result)
				}
				refusal := scopedNodeRefusal(t, f)
				if !strings.Contains(refusal.Detail, "boundary-scope-escape") || !strings.Contains(refusal.Detail, "node one") || !strings.Contains(refusal.Detail, "tools/item-a/two/result.txt") {
					t.Fatalf("missing attribution: %+v", refusal)
				}
				return
			}
			if len(steps.runs) != 2 || result.Landed != 1 || steps.lands != 1 || steps.gates != 1 {
				t.Fatalf("did not land once: %+v / %+v; refusal: %+v", steps, result, scopedNodeRefusal(t, f))
			}
			if steps.bases[1] != steps.heads[0] {
				t.Fatalf("second node did not start at first head: %+v", steps)
			}
			if steps.changes[0] != "tools/item-a/one/result.txt" || steps.changes[1] != "tools/item-a/two/result.txt" {
				t.Fatalf("changes not attributed separately: %v", steps.changes)
			}
			if steps.targets[0] != steps.targets[1] {
				t.Fatal("changed git target")
			}
			// The final candidate includes both nodes, even though fence.txt names only two/.
			candidate := Candidate{LaneID: "lane-1", Worktree: steps.targets[1], BaseSHA: steps.bases[0], HeadSHA: steps.heads[1]}
			if escape, err := l.fenceViolation(context.Background(), candidate); err != nil || escape != "" {
				t.Fatalf("item exit used last node fence: %s %v", escape, err)
			}
			writeForkFile(t, filepath.Join(f.laneRoot, "lane-1", ItemFenceFile), prefix+"tools/item-a/one/\n")
			if escape, err := l.fenceViolation(context.Background(), candidate); err != nil || !strings.Contains(escape, "tools/item-a/two/result.txt") {
				t.Fatalf("full-item exit check did not enforce item fence: %s / %v", escape, err)
			}
			if mode == "fork" {
				if steps.targets[0] != steps.fork || runGit(t, f.root, "rev-parse", "refs/heads/patch/pi") != steps.heads[1] {
					t.Fatal("fork was not landed to its branch")
				}
			}
		})
	}
}

func scopedNodeRefusal(t *testing.T, f refusalLoopFixture) refusalPayload {
	t.Helper()
	st := openFixture(t, f.storePath)
	defer st.Close()
	var payload refusalPayload
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, e := range events {
			if e.Kind == RefusalKind {
				if err := json.Unmarshal(e.Payload, &payload); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return payload
}
