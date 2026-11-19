package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// Tests for OQ-GP7:
// 1. While a trunk check is red and a fixer is open, non-fixer items may start and run,
//    and their staged briefs note the red check and fixer so they don't chase it.
// 2. When a candidate's gate fails only on checks red on the trunk tip:
//    - If candidate's changed paths do NOT intersect any red check's declared reads
//      (defaulting to ["."] when undeclared), it gets pass-inherited and lands.
//    - If candidate's changed paths DO intersect a red check's declared reads
//      (including whole-tree "."), and that check does not pass with it, it waits
//      for the fixer, held with a reason naming the check, fixer, and intersecting paths.
//    - If candidate touches a red check's inputs and makes that check pass, it lands.

func oqgp7Fixture(t *testing.T, items ...string) (refusalLoopFixture, *Loop) {
	t.Helper()
	f, l := passFixture(t, items...)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}
	return f, l
}

func TestOQGP7OpenFixerDoesNotHoldOrdinaryItemsAndBriefNotifiesWorker(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-doc", "item-blocked")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/request.md"), triageBlock)
	runGit(t, f.root, "add", "docs")
	runGit(t, f.root, "commit", "-qm", "seed docs")
	red := runGit(t, f.root, "rev-parse", TrunkRef)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='build', body_ref='docs/request.md#item-doc', allowed_paths='["docs/"]' WHERE item_id='item-doc'`)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE items SET state='blocked', seat='build', allowed_paths='["src/"]' WHERE item_id='item-blocked'`)
		if err != nil {
			return err
		}
		return item.RecordConflict(tx, item.Conflict{
			ConflictID: "c-blocked", ItemID: "item-blocked", RunID: "run-blocked-1", Status: "raised", WaitingOn: "design",
			BodyRef: "docs/request.md#item-blocked", Text: "needs design",
		}, l.cfg.Now())
	})
	l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "designer"}
	l.cfg.Policy.Backends["designer"] = Backend{Adapter: "fixture"}
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", "printf '%s' '{\"index\":{\"documents\":[]}}'"}}

	fixer, err := l.fileTrunkFixer(ctx, red, []string{"pack-render"}, GateOutcome{SuspectPaths: []string{"yolo-packs/a.txt"}})
	if err != nil {
		t.Fatal(err)
	}

	plans := mustPlan(t, l)
	byID := map[string]Decision{}
	for _, p := range plans {
		byID[p.decision.ItemID] = p.decision
	}
	if d := byID["item-doc"]; d.Action != ActionDispatch {
		t.Fatalf("item-doc held while fixer open: %+v", d)
	}
	if d := byID["item-blocked"]; d.Action != ActionDispatch || d.Seat != "design" {
		t.Fatalf("item-blocked design conflict held while fixer open: %+v", d)
	}

	// Stage item-doc and verify its brief tells the worker about the red check and fixer.
	spec := StageSpec{
		LaneID:     "lane-1",
		ItemID:     "item-doc",
		RunID:      byID["item-doc"].RunRequest.RunID,
		Backend:    "fixture",
		BaseSHA:    red,
		WorkingDir: WorktreeDir,
	}
	if err := l.cfg.Stage.Stage(ctx, spec); err != nil {
		t.Fatalf("Stage failed: %v", err)
	}
	briefBytes, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(briefBytes)
	if !strings.Contains(brief, "pack-render") || !strings.Contains(brief, fixer) {
		t.Fatalf("staged brief does not name red check and fixer:\n%s", brief)
	}
}

type oqgp7ReplaySteps struct {
	*recordingSteps
	storePath string
	replays   map[string]ReplayOutcome
	gateFn    func(Candidate) (GateOutcome, error)
	landFn    func(Candidate) (LandOutcome, error)
	landed    int
	verdicts  []string
}

func (s *oqgp7ReplaySteps) ItemState(ctx context.Context, itemID, _, state string, waiting ...item.WaitingOn) error {
	if s.storePath == "" {
		return nil
	}
	st, err := store.Open(ctx, s.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, itemID, state, time.Now().UTC(), waiting...)
		return err
	})
}

func (s *oqgp7ReplaySteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	return s.gateFn(c)
}

func (s *oqgp7ReplaySteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.landed++
	if s.storePath != "" {
		st, err := store.Open(ctx, s.storePath, true)
		if err != nil {
			return LandOutcome{}, err
		}
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO landings(landing_id, item_id, run_id, verdict_id, result, reason, base_sha, head_sha, trunk_before, trunk_after) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				"land-"+c.RunID, c.ItemID, c.RunID, "v-1", LandLanded, "", c.BaseSHA, c.HeadSHA, c.BaseSHA, c.HeadSHA); err != nil {
				return err
			}
			return nil
		}); err != nil {
			st.Close()
			return LandOutcome{}, err
		}
		st.Close()
	}
	if s.landFn != nil {
		return s.landFn(c)
	}
	return LandOutcome{Result: LandLanded}, nil
}

func (s *oqgp7ReplaySteps) Verdict(_ context.Context, c Candidate, v string) error {
	s.verdicts = append(s.verdicts, c.ItemID+":"+v)
	return nil
}

func (s *oqgp7ReplaySteps) Replay(_ context.Context, _ Candidate, rev string, checks []string) (ReplayOutcome, error) {
	if out, ok := s.replays[rev]; ok {
		return out, nil
	}
	return ReplayOutcome{Rev: rev, Known: true}, nil
}

func TestOQGP7DisjointCandidateGetsPassInheritedAndLands(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-doc")
	if err := os.MkdirAll(filepath.Join(f.root, "tools/gate/checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Narrow check reading only packs/
	writeForkFile(t, filepath.Join(f.root, "tools/gate/checks/pack-render.json"), `{"id":"pack-render","scope":"tree","tier":"fast","reads":["packs/"]}`)
	runGit(t, f.root, "add", "tools/gate/checks/pack-render.json")
	runGit(t, f.root, "commit", "-qm", "add pack-render check")
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	// Commit a change in docs/ (disjoint from packs/)
	lane := l.lanePath("lane-1")
	runGit(t, lane, "reset", "--hard", base)
	if err := os.MkdirAll(filepath.Join(lane, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, "docs/guide.md"), "# Guide\n")
	runGit(t, lane, "add", "docs/guide.md")
	runGit(t, lane, "commit", "-qm", "update guide")
	head := runGit(t, lane, "rev-parse", "HEAD")

	cand := Candidate{
		ItemID: "item-doc", RunID: "run-doc-1", LaneID: "lane-1",
		Worktree: lane, BaseSHA: base, HeadSHA: head,
	}
	if err := os.WriteFile(filepath.Join(l.laneDirPath("lane-1"), FenceFile), []byte("docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.laneDirPath("lane-1"), ItemFenceFile), []byte("docs/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), []byte(base+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var savedVerdict string
	steps := &oqgp7ReplaySteps{
		recordingSteps: &recordingSteps{},
		storePath:      f.storePath,
		replays:        map[string]ReplayOutcome{base: {Rev: base, Red: []string{"pack-render"}, Known: true}},
		gateFn: func(c Candidate) (GateOutcome, error) {
			return GateOutcome{
				Outcome:       GateFailed,
				FailingChecks: []string{"pack-render"},
				SuspectPaths:  []string{"docs/guide.md"},
				CheckWatches:  map[string][]string{"pack-render": {"packs/"}},
			}, nil
		},
		landFn: func(c Candidate) (LandOutcome, error) {
			// Check the gate.verdict.json written in RunDir if any, or check verdict outcome
			if payload, ok := readRecordPayload(filepath.Join(RunDir(f.root, c.RunID), GateVerdictRecord)); ok {
				var v struct {
					Result            string             `json:"result"`
					InheritedFailures []InheritedFailure `json:"inherited_failures"`
				}
				if json.Unmarshal(payload, &v) == nil {
					savedVerdict = v.Result
				}
			}
			return LandOutcome{Result: LandLanded, TrunkAfter: head}, nil
		},
	}
	l.cfg.Steps = steps

	// RunItem in recordingSteps needs to be avoided or we just let ItemState update the store:
	// Actually recordingSteps.ItemState does nothing! Let's check why waiter was "" in Intersecting test.
	runDir := RunDir(f.root, cand.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	verdictJSON := `{"record_version":1,"kind":"gate.verdict","id":"v1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-19T00:00:00Z","payload":{"verdict_id":"v1","plan_id":"p1","registry_digest":"d1","base_sha":"` + base + `","tree_sha":"t1","result":"fail","checks":[{"check_id":"pack-render","result":"fail"}]}}`
	writeForkFile(t, filepath.Join(runDir, GateVerdictRecord), verdictJSON)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET state='done' WHERE item_id='item-doc'`)
		return err
	})
	var result LoopResult
	_, landed, err := l.verify(ctx, &cand, &result, nil)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !landed || steps.landed != 1 {
		t.Fatalf("disjoint candidate did not land: landed=%v steps.landed=%d result=%+v", landed, steps.landed, result)
	}
	if savedVerdict != GatePassedInherited {
		t.Fatalf("saved verdict = %q, want %q", savedVerdict, GatePassedInherited)
	}
	if len(steps.verdicts) != 1 || steps.verdicts[0] != "item-doc:"+VerdictPassed {
		t.Fatalf("verdicts = %v, want [item-doc:pass]", steps.verdicts)
	}
}

func TestOQGP7IntersectingCandidateWaitsForFixerWithPaths(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		reads     []string
		changed   string
		wantPaths string
	}{
		{
			name:      "narrow check intersection",
			reads:     []string{"packs/"},
			changed:   "packs/foo.txt",
			wantPaths: "packs/foo.txt",
		},
		{
			name:      "undeclared reads defaults to whole tree dot",
			reads:     nil,
			changed:   "docs/guide.md",
			wantPaths: "docs/guide.md",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := oqgp7Fixture(t, "item-change")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			lane := l.lanePath("lane-1")
			writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), tc.changed+"\n")
			writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), ItemFenceFile), tc.changed+"\n")
			writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
			if err := os.MkdirAll(filepath.Join(lane, filepath.Dir(tc.changed)), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(lane, tc.changed), "changed\n")
			runGit(t, lane, "add", tc.changed)
			runGit(t, lane, "commit", "-qm", "change "+tc.changed)
			cand := Candidate{
				ItemID: "item-change", RunID: "run-change-1", LaneID: "lane-1",
				Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD"),
			}
			watches := map[string][]string{}
			if tc.reads != nil {
				watches["pack-render"] = tc.reads
			}
			steps := &oqgp7ReplaySteps{
				recordingSteps: &recordingSteps{},
				storePath:      f.storePath,
				replays:        map[string]ReplayOutcome{base: {Rev: base, Red: []string{"pack-render"}, Known: true}},
				gateFn: func(c Candidate) (GateOutcome, error) {
					return GateOutcome{
						Outcome:       GateFailed,
						FailingChecks: []string{"pack-render"},
						SuspectPaths:  []string{tc.changed},
						CheckWatches:  watches,
					}, nil
				},
			}
			l.cfg.Steps = steps

			var result LoopResult
			_, landed, err := l.verify(ctx, &cand, &result, nil)
			if err != nil {
				t.Fatalf("verify failed: %v", err)
			}
			if landed || steps.landed != 0 {
				t.Fatalf("intersecting candidate landed unexpectedly")
			}
			if result.TrunkAcquitted != 1 {
				t.Fatalf("TrunkAcquitted = %d, want 1", result.TrunkAcquitted)
			}
			var waitingReason, waitingWaiter string
			fixtureWrite(t, f, func(tx *store.Tx) error {
				w, _, err := item.ReadWaitingOn(tx.Reader(), "item-change")
				if err != nil {
					return err
				}
				if w != nil {
					waitingReason = w.Reason
					waitingWaiter = w.Waiter
				}
				return nil
			})
			fixer := TrunkFixerID(base)
			if waitingWaiter != "item:"+fixer {
				t.Fatalf("waiter = %q, want item:%s", waitingWaiter, fixer)
			}
			if !strings.Contains(waitingReason, "pack-render") || !strings.Contains(waitingReason, tc.wantPaths) {
				t.Fatalf("waiting reason %q does not name check and intersecting path %q", waitingReason, tc.wantPaths)
			}
		})
	}
}

func TestOQGP7CandidateThatFixesRedCheckLands(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-fix")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if _, err := l.fileTrunkFixer(ctx, base, []string{"pack-render"}, GateOutcome{SuspectPaths: []string{"packs/a.txt"}}); err != nil {
		t.Fatal(err)
	}
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "packs_fix.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), ItemFenceFile), "packs_fix.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "packs_fix.txt"), "fixed\n")
	runGit(t, lane, "add", "packs_fix.txt")
	runGit(t, lane, "commit", "-qm", "fix pack-render")
	cand := Candidate{
		ItemID: "item-fix", RunID: "run-fix-1", LaneID: "lane-1",
		Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD"),
	}
	steps := &oqgp7ReplaySteps{
		recordingSteps: &recordingSteps{},
		gateFn: func(c Candidate) (GateOutcome, error) {
			return GateOutcome{Outcome: GatePassed}, nil
		},
	}
	l.cfg.Steps = steps
	var result LoopResult
	_, landed, err := l.verify(ctx, &cand, &result, nil)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !landed || steps.landed != 1 {
		t.Fatalf("candidate fixing red check did not land: landed=%v steps.landed=%d", landed, steps.landed)
	}
}
