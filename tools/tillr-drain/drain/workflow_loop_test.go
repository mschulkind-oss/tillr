package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type workflowSteps struct {
	recordingSteps
	mu        sync.Mutex
	landMu    sync.Mutex
	t         *testing.T
	f         refusalLoopFixture
	runs      []run.Request
	gates     int
	mode      string
	fork      string
	firstHead string
	marker    string
}

func (s *workflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.runs = append(s.runs, req)
	s.mu.Unlock()
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if s.fork != "" {
		target = s.fork
	}
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
		return run.Record{}, "", err
	}
	head := runGit(s.t, target, "rev-parse", "HEAD")
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	report := "Scout findings.\nRESULT: done\n"
	code := 0
	if strings.HasSuffix(req.RunID, "-n1") {
		s.firstHead = head
		s.marker = filepath.Join(target, ".git-workflow-marker")
		// Git's private directory holds a sentinel the stager would remove with the worktree.
		gitdir := strings.TrimSpace(runGit(s.t, target, "rev-parse", "--absolute-git-dir"))
		s.marker = filepath.Join(gitdir, "workflow-marker")
		if err = os.WriteFile(s.marker, []byte("preserved"), 0644); err != nil {
			return run.Record{}, "", err
		}
		if !strings.Contains(string(brief), "read_only") {
			s.t.Errorf("read-only brief missing: %s", brief)
		}
		if s.mode == "dirty" || s.mode == "commit" {
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/escape.txt"), "escape\n")
			if s.mode == "commit" {
				runGit(s.t, target, "add", ".")
				runGit(s.t, target, "commit", "-qm", "escape")
			}
		}
	} else {
		if head != s.firstHead {
			s.t.Errorf("HEAD changed between nodes: %s / %s", head, s.firstHead)
		}
		if _, err = os.Stat(s.marker); err != nil {
			s.t.Errorf("worktree reset: %v", err)
		}
		prior := filepath.Join(RunDir(s.f.root, "run-item-a-a1-n1"), "node-report.md")
		if !strings.Contains(string(brief), prior) {
			s.t.Errorf("brief lacks prior report %s", brief)
		}
		if raw, err := os.ReadFile(prior); err != nil || !strings.Contains(string(raw), "Scout findings") {
			s.t.Errorf("prior report: %s %v", raw, err)
		}
		if s.mode == "fail-work" {
			report = "Build failed.\nRESULT: failed\n"
			code = 1
		} else {
			writeForkFile(s.t, filepath.Join(target, "tools/item-a/result.txt"), "built\n")
			runGit(s.t, target, "add", ".")
			runGit(s.t, target, "commit", "-qm", "build")
		}
	}
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if err = os.WriteFile(path, []byte(report), 0644); err != nil {
		return run.Record{}, "", err
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(code), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}
func (s *workflowSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	s.gates++
	firstHead := s.firstHead
	s.mu.Unlock()
	if c.BaseSHA != firstHead {
		s.t.Errorf("candidate base changed: %s", c.BaseSHA)
	}
	return GateOutcome{Outcome: GatePassed}, nil
}
func (s *workflowSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	// Real landings serialize trunk writes behind tillr-land's lock; two
	// lanes landing concurrently in a fixture repo must serialize their
	// merge and commit on HEAD the same way.
	s.landMu.Lock()
	defer s.landMu.Unlock()
	runGit(s.t, s.f.root, "merge", "--squash", c.HeadSHA)
	runGit(s.t, s.f.root, "commit", "-qm", "land workflow")
	fixtureLanding(s.t, s.f.storePath, c)
	return LandOutcome{Result: LandLanded}, nil
}
func (s *workflowSteps) ItemState(ctx context.Context, id, runID, state string, waiting ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, id)
		return err
	})
}

func workflowLoopFixture(t *testing.T, mode string) (refusalLoopFixture, *Loop, *workflowSteps) {
	t.Helper()
	f := newRefusalLoopFixture(t, "item-a", "item-b")
	for _, p := range []string{"config/tillr/workflows", "briefs"} {
		if err := os.MkdirAll(filepath.Join(f.root, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), workflowFixture)
	writeForkFile(t, filepath.Join(f.root, "briefs/item-a.md"), "# Task\nBuild a result.\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "workflow fixture")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id='item-a'`)
		return err
	})
	stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	steps := &workflowSteps{t: t, f: f, mode: mode}
	l := f.loop(t, steps, stage)
	holdWALKeeper(t, f.storePath)
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.Seats = map[string]string{"scout": "fixture", "build": "fixture"}
	return f, l, steps
}

func TestWorkflowPlanningKeepsBackendPolicy(t *testing.T) {
	for _, mode := range []string{"override", "window", "backend-closed"} {
		t.Run(mode, func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
			switch mode {
			case "override":
				l.cfg.Policy.Overrides = map[string]string{"item-a": "alternate"}
			case "window":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES('fixture','weekly',1,'refused',?,'window')`, l.cfg.Now().Add(time.Hour).UnixMilli())
					return err
				})
			case "backend-closed":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return applyTerminal(tx, run.Record{RunID: "run-item-a-a1-n1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(1), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{}})
				})
			}
			for _, p := range mustPlan(t, l) {
				if p.decision.ItemID == "item-a" {
					d := p.decision
					if mode == "backend-closed" {
						if d.Action != ActionHold || d.Reason != ReasonBackendClosed || d.DeferredBy != "fixture" || d.RunRequest != nil || d.Seat != "scout" {
							t.Fatalf("workflow escaped its failed backend: %+v", d)
						}
					} else if mode == "window" {
						if d.Action != ActionHold || d.Reason != ReasonBackendWindow || d.DeferredBy != "fixture" || d.RunRequest != nil || d.Seat != "scout" {
							t.Fatalf("workflow escaped its closed window: %+v", d)
						}
					} else if d.Action != ActionDispatch || d.RunRequest == nil || d.RunRequest.Backend != "alternate" {
						t.Fatalf("policy bypassed: %+v", d)
					}
					return
				}
			}
			t.Fatal("missing item")
		})
	}
}

func TestWorkflowContinuationKeepsBackendPolicy(t *testing.T) {
	for _, mode := range []string{"override", "window"} {
		t.Run(mode, func(t *testing.T) {
			f, l, steps := workflowLoopFixture(t, "")
			l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
			result := &LoopResult{}
			for i := 0; i < 2; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if mode == "override" {
				l.cfg.Policy.Overrides = map[string]string{"item-a": "alternate"}
			} else {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES('fixture','weekly',1,'refused',?,'window')`, l.cfg.Now().Add(time.Hour).UnixMilli())
					return err
				})
			}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
			if mode == "window" {
				if len(steps.runs) != 1 {
					t.Fatalf("next node escaped its closed window: %+v", steps.runs)
				}
				afterReset := l.cfg.Now().Add(2 * time.Hour)
				l.cfg.Now = func() time.Time { return afterReset }
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
				if len(steps.runs) != 2 || steps.runs[1].Backend != "fixture" {
					t.Fatalf("next node did not run on its backend after reset: %+v", steps.runs)
				}
			} else if len(steps.runs) != 2 || steps.runs[1].Backend != "alternate" {
				t.Fatalf("next node policy bypassed: %+v", steps.runs)
			}
		})
	}
}

func TestWorkflowForkContinuationKeepsItsOwnReservation(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	l.pollOnly = true // Exercise pre-gate continuation through store polling, not the live handoff.
	fork := filepath.Join(f.root, ".forks/lanes/lane-1/pi")
	if err := os.MkdirAll(fork, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(fork, "base.txt"), "fork base\n")
	runGit(t, fork, "add", ".")
	runGit(t, fork, "commit", "-qm", "fork base")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "ignore fork checkout")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return recordFixtureFork(tx, "item-a", "item-b")
	})
	steps.fork = fork
	base := runGit(t, fork, "rev-parse", "HEAD")
	runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.runs) != 2 {
		t.Fatalf("fork blocked its own continuation: %+v", steps.runs)
	}
	if got := l.laneBase("lane-1"); got != base {
		t.Fatalf("fork base changed %s -> %s", base, got)
	}
	if got := l.laneTarget("lane-1"); got != fork {
		t.Fatalf("target %s", got)
	}
}

func TestWorkflowPlanningWithoutPolicy(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(strconv.FormatBool(ready), func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			if ready {
				result := &LoopResult{}
				for i := 0; i < 2; i++ {
					if _, err := l.pass(context.Background(), result); err != nil {
						t.Fatal(err)
					}
					drainOwned(t, l, result)
				}
			}
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("nil policy panicked: %v", p)
				}
			}()
			s, err := store.OpenRead(context.Background(), f.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				envs, err := Plan(r, Pass{AvailableLanes: &l.cfg.Lanes}, nil, &VantageQuestions{Root: f.root})
				if err != nil {
					return err
				}
				for _, env := range envs {
					var d Decision
					if err := json.Unmarshal(env.Payload, &d); err != nil {
						return err
					}
					if d.ItemID == "item-a" && (d.Action != ActionHold || d.Reason != ReasonNoPolicy) {
						t.Errorf("no-policy hold: %+v", d)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkflowLegacyReadOnlyPlan(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`ALTER TABLE items DROP COLUMN workflow`); err != nil {
			return err
		}
		_, err := tx.Exec(`DROP TABLE node_runs`)
		return err
	})
	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		envs, err := Plan(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		var d Decision
		if err := json.Unmarshal(envs[0].Payload, &d); err != nil {
			return err
		}
		if d.RunRequest == nil || d.RunRequest.RunID != "run-item-a-a1" || d.Workflow != nil {
			t.Fatalf("legacy decision %+v", d)
		}
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('items') WHERE name='workflow'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("read migrated store")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRunsTwoNodesAndLandsOnce(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "")
	result := &LoopResult{}
	base := runGit(t, f.root, "rev-list", "--count", "HEAD")
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
		if result.Landed > 0 {
			break
		}
	}
	if len(steps.runs) != 2 || steps.runs[0].RunID != "run-item-a-a1-n1" || steps.runs[1].RunID != "run-item-a-a1-n2" || steps.runs[0].LaneID != steps.runs[1].LaneID {
		t.Fatalf("runs: %+v", steps.runs)
	}
	if steps.gates != 1 || result.Landed != 1 {
		t.Fatalf("gates %d result %+v", steps.gates, result)
	}
	before, _ := strconv.Atoi(strings.TrimSpace(base))
	after, _ := strconv.Atoi(strings.TrimSpace(runGit(t, f.root, "rev-list", "--count", "HEAD")))
	if after != before+1 {
		t.Fatalf("trunk commit count %d -> %d", before, after)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND attempt=1 AND outcome='passed'`).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("passed nodes: %d", n)
		}
		var nodes int
		for _, want := range []struct{ node, id string }{{"scout", "run-item-a-a1-n1"}, {"work", "run-item-a-a1-n2"}} {
			if err := tx.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND attempt=1 AND node_id=? AND run_id=? AND outcome='passed'`, want.node, want.id).Scan(&nodes); err != nil {
				return err
			}
			if nodes != 1 {
				t.Errorf("missing node run %s / %s", want.node, want.id)
			}
		}
		var state string
		if err := tx.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
			return err
		}
		if state != StateLanded {
			t.Errorf("state %s", state)
		}
		var refusals int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=?`, RefusalKind).Scan(&refusals); err != nil {
			return err
		}
		if refusals != 0 {
			t.Errorf("refusals %d", refusals)
		}
		return nil
	})
}

func TestWorkflowReadOnlyEscapeStopsNextNode(t *testing.T) {
	for _, mode := range []string{"dirty", "commit"} {
		t.Run(mode, func(t *testing.T) {
			f, l, steps := workflowLoopFixture(t, mode)
			result := &LoopResult{}
			for i := 0; i < 2; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			plans := mustPlan(t, l)
			if len(steps.runs) != 1 || steps.gates != 0 {
				t.Fatalf("runs %+v gates %d", steps.runs, steps.gates)
			}
			for _, p := range plans {
				if p.decision.ItemID == "item-a" && p.decision.Reason != ScopeEscape {
					t.Errorf("hold %+v", p.decision)
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var outcome string
				err := tx.QueryRow(`SELECT outcome FROM node_runs WHERE item_id='item-a'`).Scan(&outcome)
				if outcome != "refused" {
					t.Errorf("outcome %s", outcome)
				}
				return err
			})
		})
	}
}

func TestWorkflowNextAttemptCountsItemsNotNodes(t *testing.T) {
	f, l, steps := workflowLoopFixture(t, "fail-work")
	// Only item-a participates; the unrelated sibling must not take its freed lane.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if len(steps.runs) != 2 {
		t.Fatalf("runs %+v", steps.runs)
	}
	// The operator authorizes a fresh attempt after the second node fails.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO retries(retry_id,item_id,supersedes_run_id,requested_by,reason,requested_at) VALUES('retry-workflow','item-a','run-item-a-a1-n2','owner','repair',?)`, l.cfg.Now().UnixMilli())
		return err
	})
	plans := mustPlan(t, l)
	for _, p := range plans {
		if p.decision.ItemID == "item-a" {
			if p.decision.RunRequest == nil || !strings.HasPrefix(p.decision.RunRequest.RunID, "run-item-a-a2-") {
				t.Fatalf("next attempt %+v", p.decision)
			}
			if _, err := l.pass(context.Background(), result); err != nil {
				t.Fatal(err)
			}
			drainOwned(t, l, result)
			if len(steps.runs) != 3 || !strings.HasPrefix(steps.runs[2].RunID, "run-item-a-a2-") {
				t.Fatalf("next run: %+v", steps.runs)
			}
			return
		}
	}
	t.Fatal("missing item")
}

func TestWorkflowStepsLandSerializesConcurrentCommits(t *testing.T) {
	f, _, steps := workflowLoopFixture(t, "")
	base := runGit(t, f.root, "rev-parse", "HEAD")

	makeCandidate := func(itemID, laneID, runID, file string) Candidate {
		t.Helper()
		wt := filepath.Join(f.laneRoot, laneID, WorktreeDir)
		runGit(t, wt, "checkout", "--force", "-B", "tillr/"+laneID, base)
		runGit(t, wt, "reset", "--hard", base)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(wt, file), itemID+"\n")
		runGit(t, wt, "add", file)
		runGit(t, wt, "commit", "-qm", "build "+itemID)
		head := runGit(t, wt, "rev-parse", "HEAD")
		return Candidate{ItemID: itemID, LaneID: laneID, RunID: runID, BaseSHA: base, HeadSHA: head, Worktree: wt}
	}

	// Stage two disjoint candidates on separate lanes and land them at the same instant.
	cA := makeCandidate("item-a", "lane-1", "run-item-a-a1", "tools/item-a/a.txt")
	cB := makeCandidate("item-b", "lane-2", "run-item-b-a1", "tools/item-b/b.txt")

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, c := range []Candidate{cA, cB} {
		wg.Add(1)
		go func(c Candidate) {
			defer wg.Done()
			<-start
			_, err := steps.Land(context.Background(), c)
			errs <- err
		}(c)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Land failed: %v", err)
		}
	}

	for _, file := range []string{"tools/item-a/a.txt", "tools/item-b/b.txt"} {
		if _, err := os.Stat(filepath.Join(f.root, file)); err != nil {
			t.Fatalf("expected landed file %s on trunk: %v", file, err)
		}
	}
}
