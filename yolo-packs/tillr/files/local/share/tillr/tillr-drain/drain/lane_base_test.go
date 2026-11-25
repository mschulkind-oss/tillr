package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestLaneBasePlannerRequiresGitForLandedBlockers(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_edges(item_id,blocked_by) VALUES('dependent','blocker')`)
		return err
	})
	// Observe the queued dependency with Git available: document triage also
	// reads the repository. A landed dependency's proof must still fail
	// explicitly when Git is unavailable.
	plans := mustPlan(t, l)
	for _, p := range plans {
		if p.decision.ItemID == "dependent" && p.decision.Reason != ReasonBlocked {
			t.Fatalf("queued blocker lost: %+v", p.decision)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='blocker'`)
		return err
	})
	t.Setenv("PATH", scratchUnderDurable(t))
	if _, err := provedLaneBase(context.Background(), f.root, "green"); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("missing Git silently bypassed lane-base proof: %v", err)
	}
	if _, _, err := l.passPlans(context.Background(), l.cfg.Now()); err == nil || !strings.Contains(err.Error(), "git") {
		t.Fatalf("missing Git silently bypassed proof: %v", err)
	}
}

func TestLaneBasePolicyStagesAndRecordsSelectedCommit(t *testing.T) {
	for _, mode := range []string{"green", "tip", "fixer", "bootstrap"} {
		t.Run(mode, func(t *testing.T) {
			id := "item-a"
			if mode == "fixer" {
				id = TrunkFixerID(strings.Repeat("a", 40))
			}
			ids := []string{id}
			if mode == "bootstrap" {
				ids = append(ids, "item-z")
			}
			f, l := passFixture(t, ids...)
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, f.root, "commit", "--allow-empty", "-qm", "unproved tip")
			tip := runGit(t, f.root, "rev-parse", TrunkRef)
			if mode != "bootstrap" {
				runGit(t, f.root, "update-ref", GreenRef, base)
			}
			if mode == "tip" {
				if err := os.MkdirAll(filepath.Join(f.root, "config", "tillr"), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(f.root, "config", "tillr", "gate-policy.json"), `{"lane_base":"tip"}`)
				l.gatePolicy.LaneBase = "tip"
			}
			want := base
			if mode != "green" {
				want = tip
			}
			var output bytes.Buffer
			l.cfg.Report = &output
			if err := l.stageLanes(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := l.resetLane(context.Background(), "lane-1"); err != nil {
				t.Fatal(err)
			}
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: l.cfg.LaneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
			ok, work, err := l.applyPlan(context.Background(), mustPlan(t, l)[0], tip, l.cfg.Now())
			if err != nil || !ok || work == nil {
				t.Fatalf("claim: %v %v %+v", err, ok, work)
			}
			if work.BaseSHA != want {
				t.Fatalf("recorded base=%s want=%s", work.BaseSHA, want)
			}
			staged, err := l.stageDispatches(context.Background(), []Decision{work.Decision}, work.BaseSHA, &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage: %v %+v", err, staged)
			}
			if head := runGit(t, l.laneTarget(work.Decision.LaneID), "rev-parse", "HEAD"); head != want {
				t.Fatalf("worktree=%s want=%s", head, want)
			}
			raw, err := os.ReadFile(filepath.Join(l.laneDirPath(work.Decision.LaneID), BaseSHAFile))
			if err != nil || strings.TrimSpace(string(raw)) != want {
				t.Fatalf("base file=%s error=%v", raw, err)
			}
			// A second fresh dispatch, not an already claimed attempt, must not
			// repeat the bootstrap notice.
			if mode == "bootstrap" {
				for _, plan := range mustPlan(t, l) {
					if plan.decision.ItemID == "item-z" {
						ok, next, err := l.applyPlan(context.Background(), plan, tip, l.cfg.Now())
						if err != nil || !ok || next == nil || next.BaseSHA != tip {
							t.Fatalf("second bootstrap dispatch: %v %v %+v", err, ok, next)
						}
					}
				}
			}
			count := strings.Count(output.String(), "no green pointer yet; cutting lanes from trunk tip")
			if mode == "bootstrap" && count != 1 {
				t.Fatalf("bootstrap notice count=%d: %s", count, output.String())
			}
			if mode != "bootstrap" && count != 0 {
				t.Fatalf("unexpected bootstrap notice: %s", output.String())
			}
		})
	}
}

func TestLaneBaseRejectsRewrittenPointer(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "checkout", "-qb", "other-history")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "not on trunk")
	runGit(t, f.root, "update-ref", GreenRef, "HEAD")
	runGit(t, f.root, "checkout", "-q", TrunkRef)
	if green, err := provedLaneBase(context.Background(), f.root, "green"); err == nil || green != "" {
		t.Fatalf("rewritten pointer accepted: %s %v (trunk %s)", green, err, base)
	}
}

func TestLaneBaseReuseRetainsUnlandedWork(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "newer trunk")
	stager := &LaneStager{Root: f.root}
	spec := StageSpec{LaneID: "lane-1", BaseSHA: base}
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err != nil {
		t.Fatalf("newer retained trunk refused: %v", err)
	}
	runGit(t, f.root, "checkout", "-qb", "unlanded")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unlanded work")
	if err := stager.refuseUnlandedCommit(context.Background(), spec, f.root); err == nil {
		t.Fatal("unlanded work may be lost")
	}
}

func TestLaneBaseForkBlockerUsesDeliveredPatchHistory(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent")
	main := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, main)
	runGit(t, f.root, "checkout", "--orphan", "fork-history")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "fork base")
	forkBase := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "proved fork delivery")
	forkHead := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "checkout", "-q", TrunkRef)
	runGit(t, f.root, "update-ref", "refs/heads/patch/pi", forkBase)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []string{
			`UPDATE items SET state='landed' WHERE item_id='blocker'`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('dependent','blocker')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		if err := appendEffectOn(tx, "land.landing", "item", "blocker", map[string]string{"base_ref": "refs/heads/patch/pi"}, l.cfg.Now()); err != nil {
			return err
		}
		var seq int
		if err := tx.QueryRow(`SELECT MAX(seq) FROM events`).Scan(&seq); err != nil {
			return err
		}
		return recordFixtureLanding(tx, "blocker", "fixture-run", forkHead, seq)
	})
	dependent := func() Decision {
		for _, plan := range mustPlan(t, l) {
			if plan.decision.ItemID == "dependent" {
				return plan.decision
			}
		}
		t.Fatal("dependent missing")
		return Decision{}
	}
	if d := dependent(); d.Action != ActionHold || d.Reason != ReasonTrunkUnproved {
		t.Fatalf("undelivered fork blocker accepted: %+v", d)
	}
	runGit(t, f.root, "update-ref", "refs/heads/patch/pi", forkHead)
	if d := dependent(); d.Action != ActionDispatch {
		t.Fatalf("proved fork delivery still held with unchanged main: %+v", d)
	}
	if tip := runGit(t, f.root, "rev-parse", TrunkRef); tip != main {
		t.Fatalf("main moved: %s", tip)
	}
}

func TestLaneBaseWaitingPresentationNamesProof(t *testing.T) {
	_, where, mover := whyPresentation(Decision{Action: ActionHold, Reason: ReasonTrunkUnproved})
	if where != "trunk" || mover != "the heavy runner; prove the trunk" {
		t.Fatalf("waiting presentation: %q %q", where, mover)
	}
}

func TestLaneBaseNamesLandingMissingFromGreenTrunk(t *testing.T) {
	f, l := passFixture(t, "blocker", "dependent")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "blocker landed")
	landed := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "reset", "--hard", base)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "rewritten blocker landing")
	replacement := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, replacement)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []string{
			`UPDATE items SET state='landed' WHERE item_id='blocker'`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('dependent','blocker')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return recordFixtureLanding(tx, "blocker", "fixture-run", landed, 1)
	})
	dependent := func() Decision {
		for _, p := range mustPlan(t, l) {
			if p.decision.ItemID == "dependent" {
				return p.decision
			}
		}
		t.Fatal("dependent missing")
		return Decision{}
	}
	for _, ahead := range []bool{false, true} {
		if ahead {
			runGit(t, f.root, "commit", "--allow-empty", "-qm", "next unproved trunk tip")
		}
		d := dependent()
		want := "blocker-landing-not-on-trunk: blocker at " + landed
		if d.Action != ActionHold || d.Reason != want {
			t.Fatalf("ahead=%t: want hold %q, got %s %q", ahead, want, d.Action, d.Reason)
		}
		class, where, mover := whyPresentation(d)
		if class != "needs you" || where != "trunk" || mover != "the owner; restore the named landing to the trunk, or map its replacement with tillr drain accept-rewrite" {
			t.Fatalf("waiting presentation: %q %q %q", class, where, mover)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at) VALUES(?,?,?)`, landed, replacement, l.cfg.Now().Format(time.RFC3339))
		return err
	})
	registerRewriteStore(f.root, l.cfg.Store)
	if d := dependent(); d.Action != ActionDispatch {
		t.Fatalf("known replacement already proved: %+v", d)
	}
}

func TestLaneBaseWaitsForBlockerLandingToBeProved(t *testing.T) {
	for _, mode := range []string{"green", "tip"} {
		t.Run(mode, func(t *testing.T) {
			f, l := passFixture(t, "blocker", "dependent")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, f.root, "commit", "--allow-empty", "-qm", "blocker landed")
			landed := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, f.root, "update-ref", GreenRef, base)
			if mode == "tip" {
				if err := os.MkdirAll(filepath.Join(f.root, "config", "tillr"), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(f.root, "config", "tillr", "gate-policy.json"), `{"lane_base":"tip"}`)
				l.gatePolicy.LaneBase = "tip"
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				for _, q := range []string{`UPDATE items SET state='landed' WHERE item_id='blocker'`, `INSERT INTO item_edges(item_id,blocked_by) VALUES('dependent','blocker')`} {
					if _, err := tx.Exec(q); err != nil {
						return err
					}
				}
				return recordFixtureLanding(tx, "blocker", "fixture-run", landed, 1)
			})
			decision := func() Decision {
				for _, p := range mustPlan(t, l) {
					if p.decision.ItemID == "dependent" {
						return p.decision
					}
				}
				t.Fatal("dependent missing")
				return Decision{}
			}
			d := decision()
			if mode == "green" {
				if d.Action != ActionHold || d.Reason != "trunk-unproved" || d.DeferredBy != "" {
					t.Fatalf("wait should name proof, not blocker: %+v", d)
				}
			} else if d.Action != ActionDispatch {
				t.Fatalf("tip held: %+v", d)
			}
			runGit(t, f.root, "update-ref", GreenRef, landed)
			if d := decision(); d.Action != ActionDispatch {
				t.Fatalf("proved blocker still held: %+v", d)
			}
		})
	}
}

func TestApplyPlannedChecksOnlyDispatchedItemsBlockers(t *testing.T) {
	f, l := passFixture(t, "unrelated-blocker-1", "unrelated-blocker-2", "unrelated-dependent", "target-blocker", "target")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated blocker 1")
	unrelatedSHA1 := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unrelated blocker 2")
	unrelatedSHA2 := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, unrelatedSHA2)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "target blocker")
	targetBlockerSHA := runGit(t, f.root, "rev-parse", TrunkRef)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, q := range []string{
			`UPDATE items SET state='landed' WHERE item_id IN ('unrelated-blocker-1', 'unrelated-blocker-2')`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('unrelated-dependent','unrelated-blocker-1')`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('unrelated-dependent','unrelated-blocker-2')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		if err := recordFixtureLanding(tx, "unrelated-blocker-1", "fixture-run-1", unrelatedSHA1, 1); err != nil {
			return err
		}
		return recordFixtureLanding(tx, "unrelated-blocker-2", "fixture-run-2", unrelatedSHA2, 2)
	})

	var targetPlan plannedApplication
	found := false
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "target" {
			targetPlan = p
			found = true
			break
		}
	}
	if !found || targetPlan.decision.Action != ActionDispatch {
		t.Fatalf("expected dispatch plan for target: found=%v plan=%+v", found, targetPlan.decision)
	}

	var fetches []string
	writeGatheredHooks.Fetch = func(key string) { fetches = append(fetches, key) }
	t.Cleanup(func() { writeGatheredHooks.Fetch = nil })

	tip := targetBlockerSHA
	generation := ""
	ok, work, err := l.applyPlanRecorded(context.Background(), targetPlan, &tip, l.cfg.Now(), &generation)
	writeGatheredHooks.Fetch = nil
	if err != nil || !ok || work == nil {
		t.Fatalf("applyPlanRecorded failed: ok=%v work=%+v err=%v", ok, work, err)
	}
	for _, key := range fetches {
		if strings.Contains(key, unrelatedSHA1) || strings.Contains(key, unrelatedSHA2) {
			t.Fatalf("applyPlanned gathered unrelated blocker ancestry %q (all fetches: %v)", key, fetches)
		}
	}

	// Verify target's own unproved blocker is still enforced on a fresh fixture.
	f2, l2 := passFixture(t, "target-blocker", "target")
	base2 := runGit(t, f2.root, "rev-parse", TrunkRef)
	runGit(t, f2.root, "update-ref", GreenRef, base2)
	runGit(t, f2.root, "commit", "--allow-empty", "-qm", "target blocker landed after green")
	unprovedSHA := runGit(t, f2.root, "rev-parse", TrunkRef)
	var targetPlan2 plannedApplication
	for _, p := range mustPlan(t, l2) {
		if p.decision.ItemID == "target" {
			targetPlan2 = p
			break
		}
	}
	if targetPlan2.decision.Action != ActionDispatch {
		t.Fatalf("expected dispatch plan before blocker edge: %+v", targetPlan2.decision)
	}
	fixtureWrite(t, f2, func(tx *store.Tx) error {
		for _, q := range []string{
			`UPDATE items SET state='landed' WHERE item_id='target-blocker'`,
			`INSERT INTO item_edges(item_id,blocked_by) VALUES('target','target-blocker')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return recordFixtureLanding(tx, "target-blocker", "fixture-run-tb", unprovedSHA, 1)
	})
	tip2 := unprovedSHA
	generation2 := ""
	ok2, work2, err2 := l2.applyPlanRecorded(context.Background(), targetPlan2, &tip2, l2.cfg.Now(), &generation2)
	if err2 != nil || ok2 || work2 != nil {
		t.Fatalf("expected unproved own blocker to decline dispatch: ok=%v work=%+v err=%v", ok2, work2, err2)
	}
}

func TestLaneBaseUnprovedBriefWarning(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}
	green := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unproved tip")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	stageAndRead := func(t *testing.T, lane, itemID, runID, base string) string {
		t.Helper()
		if err := l.cfg.Stage.Stage(ctx, StageSpec{
			LaneID:     lane,
			ItemID:     itemID,
			RunID:      runID,
			Backend:    "fixture",
			BaseSHA:    base,
			WorkingDir: WorktreeDir,
		}); err != nil {
			t.Fatalf("Stage(%s, %s): %v", itemID, shortSHA(base), err)
		}
		raw, err := os.ReadFile(filepath.Join(f.laneRoot, lane, BriefFile))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	assertHasWarning := func(t *testing.T, brief string) {
		t.Helper()
		for _, want := range []string{"Unproved base warning:", "unproved", "unrelated", "`trunk` obstacle", "CHECKS:", "BASE:"} {
			if !strings.Contains(brief, want) {
				t.Fatalf("brief missing %q:\n%s", want, brief)
			}
		}
	}

	t.Run("no_green_pointer_warns", func(t *testing.T) {
		brief := stageAndRead(t, "lane-1", "item-a", "run-item-a-a1", tip)
		assertHasWarning(t, brief)
	})

	runGit(t, f.root, "update-ref", GreenRef, green)

	t.Run("unproved_tip_warns", func(t *testing.T) {
		brief := stageAndRead(t, "lane-1", "item-a", "run-item-a-a2", tip)
		assertHasWarning(t, brief)
		if !strings.Contains(brief, shortSHA(green)) {
			t.Fatalf("brief does not name proved green commit %s:\n%s", shortSHA(green), brief)
		}
	})

	t.Run("proved_base_omits_warning", func(t *testing.T) {
		brief := stageAndRead(t, "lane-1", "item-a", "run-item-a-a3", green)
		if strings.Contains(brief, "Unproved base warning:") {
			t.Fatalf("proved base brief unexpectedly contains unproved-base warning:\n%s", brief)
		}
	})

	t.Run("trunk_fixer_omits_warning", func(t *testing.T) {
		fixerID := "fixer-main-" + shortSHA(tip)
		nowMS := l.cfg.Now().UnixMilli()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT INTO items(item_id,state,title,updated_at) VALUES(?,'in-queue','fix red trunk',?)`, fixerID, nowMS); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state) VALUES(?,'trunk-green','checks: unit','unverified')`, fixerID)
			return err
		})
		brief := stageAndRead(t, "lane-1", fixerID, "run-"+fixerID+"-a1", tip)
		if strings.Contains(brief, "Unproved base warning:") {
			t.Fatalf("trunk fixer brief unexpectedly contains unproved-base warning:\n%s", brief)
		}
	})

	t.Run("unproved_tip_warns_call_suspends", func(t *testing.T) {
		if err := l.cfg.Stage.Stage(ctx, StageSpec{
			LaneID:           "lane-1",
			ItemID:           "item-a",
			RunID:            "run-item-a-a4",
			Backend:          "fixture",
			BaseSHA:          tip,
			WorkingDir:       WorktreeDir,
			EndingCapability: run.EndingCallSuspends,
		}); err != nil {
			t.Fatalf("Stage call-suspends: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", BriefFile))
		if err != nil {
			t.Fatal(err)
		}
		brief := string(raw)
		for _, want := range []string{"Unproved base warning:", "unproved", "unrelated", "`trunk` obstacle", "`checks`", "`base`"} {
			if !strings.Contains(brief, want) {
				t.Fatalf("call-suspends brief missing %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, "Report shape") {
			t.Fatalf("call-suspends brief unexpectedly mentions Report shape:\n%s", brief)
		}
	})
}

func wrapGitLog(t *testing.T) (logPath string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "git.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", logPath, realGit)
	bin := filepath.Join(dir, "git")
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readGitLog(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func TestUnprovedBlockersUsesOneGitProcessPerTip(t *testing.T) {
	f, l := passFixture(t,
		"proved-1", "proved-2", "proved-3", "proved-4", "proved-5",
		"unproved-1", "unproved-2", "missing-1",
		"dep-proved-1", "dep-proved-2", "dep-proved-3", "dep-proved-4", "dep-proved-5",
		"dep-unproved-1", "dep-unproved-2", "dep-missing-1",
	)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	provedSHAs := make([]string, 5)
	for i := range provedSHAs {
		runGit(t, f.root, "commit", "--allow-empty", "-qm", fmt.Sprintf("proved %d", i+1))
		provedSHAs[i] = runGit(t, f.root, "rev-parse", TrunkRef)
	}
	green := provedSHAs[len(provedSHAs)-1]
	runGit(t, f.root, "update-ref", GreenRef, green)

	unprovedSHAs := make([]string, 2)
	for i := range unprovedSHAs {
		runGit(t, f.root, "commit", "--allow-empty", "-qm", fmt.Sprintf("unproved %d", i+1))
		unprovedSHAs[i] = runGit(t, f.root, "rev-parse", TrunkRef)
	}
	tip := unprovedSHAs[len(unprovedSHAs)-1]

	// Create a detached commit not on trunk.
	runGit(t, f.root, "checkout", "-qb", "side-history", base)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "not on trunk")
	missingSHA := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "checkout", "-q", TrunkRef)

	states := map[string]string{
		"proved-1": StateLanded, "proved-2": StateLanded, "proved-3": StateLanded, "proved-4": StateLanded, "proved-5": StateLanded,
		"unproved-1": StateLanded, "unproved-2": StateLanded, "missing-1": StateLanded,
	}
	edges := map[string][]string{
		"dep-proved-1": {"proved-1"}, "dep-proved-2": {"proved-2"}, "dep-proved-3": {"proved-3"},
		"dep-proved-4": {"proved-4"}, "dep-proved-5": {"proved-5"},
		"dep-unproved-1": {"unproved-1"}, "dep-unproved-2": {"unproved-2"},
		"dep-missing-1": {"missing-1"},
	}
	commits := map[string]landedBlockerCommit{
		"proved-1":   {sha: provedSHAs[0]},
		"proved-2":   {sha: provedSHAs[1]},
		"proved-3":   {sha: provedSHAs[2]},
		"proved-4":   {sha: provedSHAs[3]},
		"proved-5":   {sha: provedSHAs[4]},
		"unproved-1": {sha: unprovedSHAs[0]},
		"unproved-2": {sha: unprovedSHAs[1]},
		"missing-1":  {sha: missingSHA},
	}

	logPath := wrapGitLog(t)
	waits, err := unprovedBlockersFromCommits(context.Background(), f.root, green, tip, states, edges, commits)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("dep-proved-%d", i)
		if got := waits[id]; got != "" {
			t.Errorf("%s: got wait %q, want empty", id, got)
		}
	}
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("dep-unproved-%d", i)
		if got := waits[id]; got != ReasonTrunkUnproved {
			t.Errorf("%s: got wait %q, want %q", id, got, ReasonTrunkUnproved)
		}
	}
	wantMissing := fmt.Sprintf("%s: missing-1 at %s", ReasonBlockerLandingNotOnTrunk, missingSHA)
	if got := waits["dep-missing-1"]; got != wantMissing {
		t.Errorf("dep-missing-1: got wait %q, want %q", got, wantMissing)
	}

	lines := readGitLog(t, logPath)
	// Expect one git process for green and one for tip (2 total), not one per blocker (11).
	if len(lines) != 2 {
		t.Fatalf("git invocations = %d, want 2 (one per tip):\n%s", len(lines), strings.Join(lines, "\n"))
	}
	_ = l
}

func TestUnprovedBlockersBatchHonorsRewriteTranslate(t *testing.T) {
	f, l := passFixture(t, "direct", "rewritten-proved", "rewritten-unproved", "unmapped",
		"dep-direct", "dep-rewritten-proved", "dep-rewritten-unproved", "dep-unmapped")
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	// Old history that gets rewritten.
	runGit(t, f.root, "checkout", "-qb", "old-history", base)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "old proved")
	oldProved := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "old unproved")
	oldUnproved := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unmapped lost")
	unmapped := runGit(t, f.root, "rev-parse", "HEAD")

	// New trunk history.
	runGit(t, f.root, "checkout", "-q", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "direct proved")
	directProved := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "replacement proved")
	newProved := runGit(t, f.root, "rev-parse", TrunkRef)
	green := newProved
	runGit(t, f.root, "update-ref", GreenRef, green)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "replacement unproved")
	newUnproved := runGit(t, f.root, "rev-parse", TrunkRef)
	tip := newUnproved

	fixtureWrite(t, f, func(tx *store.Tx) error {
		now := l.cfg.Now().Format(time.RFC3339)
		if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at) VALUES(?,?,?)`, oldProved, newProved, now); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha,new_sha,recorded_at) VALUES(?,?,?)`, oldUnproved, newUnproved, now)
		return err
	})
	registerRewriteStore(f.root, l.cfg.Store)

	states := map[string]string{
		"direct":             StateLanded,
		"rewritten-proved":   StateLanded,
		"rewritten-unproved": StateLanded,
		"unmapped":           StateLanded,
	}
	edges := map[string][]string{
		"dep-direct":             {"direct"},
		"dep-rewritten-proved":   {"rewritten-proved"},
		"dep-rewritten-unproved": {"rewritten-unproved"},
		"dep-unmapped":           {"unmapped"},
	}
	commits := map[string]landedBlockerCommit{
		"direct":             {sha: directProved},
		"rewritten-proved":   {sha: oldProved},
		"rewritten-unproved": {sha: oldUnproved},
		"unmapped":           {sha: unmapped},
	}

	waits, err := unprovedBlockersFromCommits(context.Background(), f.root, green, tip, states, edges, commits)
	if err != nil {
		t.Fatal(err)
	}
	if got := waits["dep-direct"]; got != "" {
		t.Errorf("dep-direct: got %q, want empty", got)
	}
	if got := waits["dep-rewritten-proved"]; got != "" {
		t.Errorf("dep-rewritten-proved: got %q, want empty", got)
	}
	if got := waits["dep-rewritten-unproved"]; got != ReasonTrunkUnproved {
		t.Errorf("dep-rewritten-unproved: got %q, want %q", got, ReasonTrunkUnproved)
	}
	wantUnmapped := fmt.Sprintf("%s: unmapped at %s", ReasonBlockerLandingNotOnTrunk, unmapped)
	if got := waits["dep-unmapped"]; got != wantUnmapped {
		t.Errorf("dep-unmapped: got %q, want %q", got, wantUnmapped)
	}
}

func TestUnprovedBlockersBatchWorksWithGather(t *testing.T) {
	f, _ := passFixture(t, "b1", "b2", "b3", "dep")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "b1")
	s1 := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "b2")
	s2 := runGit(t, f.root, "rev-parse", TrunkRef)
	green := s2
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "b3")
	s3 := runGit(t, f.root, "rev-parse", TrunkRef)

	states := map[string]string{"b1": StateLanded, "b2": StateLanded, "b3": StateLanded}
	edges := map[string][]string{"dep": {"b1", "b2", "b3"}}
	commits := map[string]landedBlockerCommit{"b1": {sha: s1}, "b2": {sha: s2}, "b3": {sha: s3}}

	s := gatheredStore(t)
	logPath := wrapGitLog(t)

	// Read phase gathers once; write phase reuses the gathered batch with zero git processes.
	gctx, facts := gatheringFor(context.Background())
	waits, err := unprovedBlockersFromCommits(gctx, f.root, green, "", states, edges, commits)
	if err != nil || waits["dep"] != ReasonTrunkUnproved {
		t.Fatalf("read phase: waits=%v err=%v", waits, err)
	}
	if got := len(readGitLog(t, logPath)); got != 1 {
		t.Fatalf("read phase git invocations = %d, want 1", got)
	}
	err = writeGatheredFrom(context.Background(), s, facts, func(ctx context.Context, _ *store.Tx) error {
		w, err := unprovedBlockersFromCommits(ctx, f.root, green, "", states, edges, commits)
		if err != nil {
			return err
		}
		if w["dep"] != ReasonTrunkUnproved {
			t.Fatalf("write phase: waits=%v", w)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(readGitLog(t, logPath)); got != 1 {
		t.Fatalf("after writeGatheredFrom git invocations = %d, want 1", got)
	}
}
