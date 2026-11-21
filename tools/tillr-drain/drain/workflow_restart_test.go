package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type restartWorkflowSteps struct {
	*workflowSteps
	heads []string
}

func (s *restartWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	s.heads = append(s.heads, runGit(s.t, target, "rev-parse", "HEAD"))
	if s.firstHead == "" {
		s.firstHead = s.heads[0]
	}
	writeForkFile(s.t, filepath.Join(target, "tools/item-a/"+req.RunID+".txt"), "finished\n")
	runGit(s.t, target, "add", ".")
	runGit(s.t, target, "commit", "-qm", "node")
	report := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err := os.WriteFile(report, []byte("RESULT: done\n"), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", err
}
func restartGraphFixture(t *testing.T) (refusalLoopFixture, *Loop, *restartWorkflowSteps) {
	f, l, old := workflowLoopFixture(t, "")
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), `{"name":"scout-then-build","version":1,"stages":[{"id":"one","kind":"agent","seat":"build","access":"write"},{"id":"two","kind":"agent","seat":"build","access":"write"},{"id":"three","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"one","to_node":"two"},{"from_node":"two","to_node":"three"}]}`)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "three nodes")
	steps := &restartWorkflowSteps{workflowSteps: old}
	l.cfg.Steps = steps
	return f, l, steps
}
func graphPass(t *testing.T, l *Loop, result *LoopResult) {
	t.Helper()
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
}
func replaceGraphLoop(t *testing.T, f refusalLoopFixture, l *Loop, steps Steps) *Loop {
	next := f.loop(t, steps, l.cfg.Stage)
	next.cfg.Lanes = l.cfg.Lanes
	next.cfg.Policy = l.cfg.Policy
	return next
}
func TestWorkflowRestartAfterTwoNodesLandsOnce(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	before, _ := strconv.Atoi(runGit(t, f.root, "rev-list", "--count", "HEAD"))
	for i := 0; i < 4; i++ {
		graphPass(t, l, result)
	}
	if len(steps.runs) != 2 {
		t.Fatalf("runs before stop: %+v", steps.runs)
	}
	head := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD")
	next := replaceGraphLoop(t, f, l, steps)
	for i := 0; i < 4 && result.Landed == 0; i++ {
		graphPass(t, next, result)
	}
	if len(steps.runs) != 3 || steps.runs[2].RunID != "run-item-a-a1-n3" || steps.runs[2].LaneID != "lane-1" || steps.heads[2] != head {
		t.Fatalf("restart reran nodes or lost head: %+v heads %v", steps.runs, steps.heads)
	}
	after, _ := strconv.Atoi(runGit(t, f.root, "rev-list", "--count", "HEAD"))
	if result.Landed != 1 || steps.gates != 1 || after != before+1 {
		t.Fatalf("landing %+v gates %d commits %d -> %d", result, steps.gates, before, after)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, RefusalKind).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("owned commits refused: %d", n)
		}
		return nil
	})
}
func TestWorkflowRestartReplaysOnlyUnfinishedNode(t *testing.T) {
	for _, index := range []int{0, 1} {
		for _, committed := range []bool{false, true} {
			t.Run(fmt.Sprintf("node%d/commit%v", index, committed), func(t *testing.T) {
				f, l, steps := restartGraphFixture(t)
				result := &LoopResult{}
				for i := 0; i < index*2; i++ {
					graphPass(t, l, result)
				}
				p := mustPlan(t, l)[0]
				deadOwnerClaim(t, f, l, p)
				work := pendingForTest(t, f, "item-a")
				staged, err := l.stageDispatches(context.Background(), []Decision{work.Decision}, work.BaseSHA, result)
				if err != nil || len(staged) != 1 {
					t.Fatalf("stage %v %v", staged, err)
				}
				target := l.laneTarget("lane-1")
				head := runGit(t, target, "rev-parse", "HEAD")
				if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(target, "tools/item-a/unfinished.txt"), "discard\n")
				if committed {
					runGit(t, target, "add", ".")
					runGit(t, target, "commit", "-qm", "unfinished")
					head = runGit(t, target, "rev-parse", "HEAD")
				}
				writeForkFile(t, filepath.Join(target, "tools/item-a/untracked.txt"), "discard\n")
				next := replaceGraphLoop(t, f, l, steps)
				graphPass(t, next, result)
				if len(steps.runs) != index+1 {
					t.Fatalf("unfinished node not replayed: %+v", steps.runs)
				}
				got := steps.runs[index]
				if got.RunID == p.decision.RunRequest.RunID || !strings.HasPrefix(got.RunID, p.decision.RunRequest.RunID) || got.LaneID != "lane-1" || steps.heads[index] != head {
					t.Fatalf("restart identity/head %+v %v want %s", got, steps.heads, head)
				}
				names := runGit(t, target, "ls-files", "--others", "--exclude-standard")
				tracked := runGit(t, target, "ls-files")
				if strings.Contains(names, "untracked.txt") || strings.Contains(tracked, "unfinished.txt") != committed {
					t.Fatalf("restart did not keep exactly the committed work: untracked=%s tracked=%s", names, tracked)
				}
				if w := pendingForTest(t, f, "item-a"); w.Decision.Attempt != 1 || w.Decision.NodeIndex != index {
					t.Fatalf("attempt/node changed: %+v", w)
				}
			})
		}
	}
}
func pendingForTest(t *testing.T, f refusalLoopFixture, id string) *pendingWork {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var work *pendingWork
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error { works, err := readPending(r); work = works[id]; return err }); err != nil {
		t.Fatal(err)
	}
	if work == nil {
		t.Fatal("missing pending work")
	}
	return work
}

// An edit to the item's definition is not among these: the owner ruled on 2026-11-17 that
// it releases nothing (TestAnInFlightWorkflowIsNotInvalidatedByABlockOrFenceEdit).
func TestWorkflowRestartReleasesInvalidItem(t *testing.T) {
	for _, mode := range []string{"closed", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			f, l, steps := restartGraphFixture(t)
			result := &LoopResult{}
			for i := 0; i < 4; i++ {
				graphPass(t, l, result)
			}
			old := pendingForTest(t, f, "item-a")
			head := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='item-a'`, mode)
				return err
			})
			next := replaceGraphLoop(t, f, l, steps)
			graphPass(t, next, result)
			if len(steps.runs) != 2 {
				t.Fatalf("invalid item ran: %+v", steps.runs)
			}
			if !pendingForTest(t, f, "item-a").Done {
				t.Fatal("invalid graph still owns lane")
			}
			if got := runGit(t, f.root, "rev-parse", pinRef(old.Decision.RunRequest.RunID)); got != head {
				t.Fatalf("unlanded head not pinned %s / %s", got, head)
			}
		})
	}
}
func TestWorkflowBetweenNodesShowsCurrentNode(t *testing.T) {
	f, l, _ := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{Build: &l.build, AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		row := answer.Rows[0]
		if row.Class != "moving" || !strings.Contains(row.Where, "two") || row.Mover == "—" || answer.Summary.Running != 1 {
			t.Fatalf("between nodes waiting on nothing: %+v", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
