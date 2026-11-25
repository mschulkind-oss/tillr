package drain

import (
	"bytes"
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

type scoutedDesignSteps struct {
	*parallelSteps
	mode     string
	briefs   map[int]string
	requests map[int]run.Request
}

func (s *scoutedDesignSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	index, err := strconv.Atoi(strings.Split(req.RunID[strings.LastIndex(req.RunID, "-n")+2:], "-")[0])
	if err != nil {
		return run.Record{}, "", err
	}
	briefPath := filepath.Join(s.f.laneRoot, req.LaneID, BriefFile)
	for i, arg := range req.Command {
		if arg == "--brief" && i+1 < len(req.Command) {
			briefPath = req.Command[i+1]
		}
	}
	brief, err := os.ReadFile(briefPath)
	if err != nil {
		return run.Record{}, "", err
	}
	s.mu.Lock()
	s.briefs[index] = string(brief)
	s.requests[index] = req
	s.mu.Unlock()
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if index == 1 {
		s.firstHead = runGit(s.t, target, "rev-parse", "HEAD")
		if s.mode != "none" {
			proposal := `{"target":"write","workflow":"design-from-scouts@v1","args":{"prompts":["prompt-alpha","prompt-beta","prompt-gamma","prompt-delta"]}}`
			if err := os.WriteFile(filepath.Join(nodeOutputsDir(s.f.root, req.RunID), "proposal"), []byte(proposal), 0644); err != nil {
				return run.Record{}, "", err
			}
		}
		report := "Opened design.\nRESULT: done\n"
		path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
		if err := os.WriteFile(path, []byte(report), 0644); err != nil {
			return run.Record{}, "", err
		}
		rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-17T00:00:00Z", FinishedAt: "2026-11-17T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
		st, err := store.Open(ctx, s.f.storePath, true)
		if err != nil {
			return rec, report, err
		}
		defer st.Close()
		err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, report, err
	}
	if s.mode == "none" {
		return s.writeRun(ctx, req)
	}
	if index == 6 {
		s.mu.Lock()
		ended := s.ended
		s.builders++
		s.mu.Unlock()
		if ended != 4 {
			return run.Record{}, "", fmt.Errorf("join started before all scouts ended: %d", ended)
		}
		return s.writeRun(ctx, req)
	}
	if s.mode != "missing" || index != 2 {
		if err := os.WriteFile(filepath.Join(nodeOutputsDir(s.f.root, req.RunID), "findings"), []byte(fmt.Sprintf("Findings of scout %d", index-1)), 0644); err != nil {
			return run.Record{}, "", err
		}
	}
	rec, report, err := s.parallelSteps.Run(ctx, req)
	if s.mode == "commit" && index == 2 && err == nil {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return rec, report, err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/escape.txt"), "committed scout\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "scout escape")
	}
	return rec, report, err
}

func TestScoutedDesignWorkflowLoop(t *testing.T) {
	for _, mode := range []string{"scouts", "none", "commit", "missing"} {
		t.Run(mode, func(t *testing.T) {
			f, l, base := workflowLoopFixtureWithLanes(t, 4, "")
			l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4"}
			l.cfg.Policy.Seats["design"] = "fixture"
			for _, ref := range []string{"scouted-design@v1", "design-from-scouts@v1"} {
				raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "tillr", "workflows", ref+".json"))
				if err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows", ref+".json"), string(raw))
			}
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "tracked design workflows")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id!='item-a'`); err != nil {
					return err
				}
				_, err := tx.Exec(`UPDATE items SET seat='design',workflow='scouted-design@v1' WHERE item_id='item-a'`)
				return err
			})
			var checked bytes.Buffer
			if !CheckWorkflows(f.root, filepath.Join(f.root, "config/tillr/workflows"), &checked) {
				t.Fatal(checked.String())
			}
			steps := &scoutedDesignSteps{parallelSteps: &parallelSteps{workflowSteps: base, overlap: make(chan struct{}), builderSuffix: "-n6"}, mode: mode, briefs: map[int]string{}, requests: map[int]run.Request{}}
			l.cfg.Steps = steps
			before := runGit(t, f.root, "rev-list", "--count", "HEAD")
			result := &LoopResult{}
			for i := 0; i < 12 && result.Landed == 0; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			open := steps.briefs[1]
			for _, rule := range []string{"planned, fenced, or judged apart", "Otherwise propose nothing", "write use sub-agents of its own"} {
				if !strings.Contains(open, rule) {
					t.Errorf("open lacks rule %q", rule)
				}
			}
			if mode == "commit" || mode == "missing" {
				if len(steps.requests) != 5 || steps.builders != 0 || result.Landed != 0 {
					t.Fatalf("consumer released: runs=%d builders=%d result=%+v", len(steps.requests), steps.builders, result)
				}
				reason := "node-output-missing"
				if mode == "commit" {
					reason = "boundary-scope-escape"
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					var payload string
					if err := tx.QueryRow(`SELECT payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`, RefusalKind).Scan(&payload); err != nil {
						return err
					}
					if mode == "missing" && !strings.Contains(payload, reason) {
						t.Errorf("missing refusal %s: %s", reason, payload)
					}

					return nil
				})
				found := false
				for _, planned := range mustPlan(t, l) {
					if planned.decision.ItemID == "item-a" {
						found = true
						if planned.decision.Action != ActionHold || planned.decision.Reason != reason {
							t.Errorf("want hold %s: %+v", reason, planned.decision)
						}
					}
				}
				if !found {
					t.Error("held item disappeared from plan")
				}
				return
			}
			want := 6
			nodes := []string{"open", "write/scout-1", "write/scout-2", "write/scout-3", "write/scout-4", "write/join"}
			if mode == "none" {
				want = 2
				nodes = []string{"open", "write"}
			}
			if len(steps.requests) != want || result.Landed != 1 || steps.gates != 1 {
				t.Fatalf("runs=%d gates=%d result=%+v", len(steps.requests), steps.gates, result)
			}
			beforeN, _ := strconv.Atoi(before)
			afterN, _ := strconv.Atoi(runGit(t, f.root, "rev-list", "--count", "HEAD"))
			if afterN != beforeN+1 {
				t.Fatalf("commits %d -> %d", beforeN, afterN)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var count int
				if err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a'`).Scan(&count); err != nil {
					return err
				}
				if count != want {
					t.Errorf("node runs=%d want=%d", count, want)
				}
				for i, node := range nodes {
					var id, outcome string
					if err := tx.QueryRow(`SELECT run_id,outcome FROM node_runs WHERE item_id='item-a' AND node_id=?`, node).Scan(&id, &outcome); err != nil {
						return err
					}
					if id != steps.requests[i+1].RunID || outcome != "passed" {
						t.Errorf("node %s: %s %s", node, id, outcome)
					}
				}
				return nil
			})
			if mode == "scouts" {
				if steps.started != 4 || steps.ended != 4 || steps.builders != 1 {
					t.Fatalf("overlap/order: started=%d ended=%d builders=%d", steps.started, steps.ended, steps.builders)
				}
				prompts := []string{"prompt-alpha", "prompt-beta", "prompt-gamma", "prompt-delta"}
				previous := -1
				for i, prompt := range prompts {
					scout := steps.briefs[i+2]
					for j, other := range prompts {
						if strings.Contains(scout, other) != (i == j) {
							t.Errorf("scout %d carries wrong prompt %q", i, other)
						}
					}
					path := filepath.Join(nodeOutputsDir(f.root, steps.requests[i+2].RunID), "findings")
					consumer := steps.briefs[6]
					entry := fmt.Sprintf("Input findings from node write/scout-%d: %s", i+1, path)
					index := strings.Index(consumer, entry)
					if index <= previous {
						t.Errorf("join lacks ordered producer entry %s", entry)
					}
					previous = index
					if !strings.Contains(scout, prompt) {
						t.Errorf("missing own prompt %s", prompt)
					}
					for _, forbidden := range []string{"Read the previous node report:", "Previous node report:", "node-report.md", "Opened design."} {
						if strings.Contains(scout, forbidden) {
							t.Errorf("scout %d unexpectedly carried %q", i+1, forbidden)
						}
					}
				}
				for _, forbidden := range []string{"Read the previous node report:", "Previous node report:", "node-report.md", "Opened design."} {
					if strings.Contains(steps.briefs[6], forbidden) {
						t.Errorf("join unexpectedly carried %q", forbidden)
					}
				}
			}
		})
	}
}
