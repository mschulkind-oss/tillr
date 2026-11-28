package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func insertQueuedItemForTrunkTest(t *testing.T, s *store.Store, id, title, words string) {
	t.Helper()
	insertOpenTestFixItem(t, s, id, title, StateInQueue, words)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO events(kind, ts, object_type, object_id, payload) VALUES('item.item', 1000, 'item', ?, '{"state":"in-queue"}')`,
			id,
		); err != nil {
			return err
		}
		_, err := tx.Exec(
			`INSERT INTO item_criteria(item_id, criterion_id, text, state) VALUES(?, 'c1', 'change.txt exists', 'open')`,
			id,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func initNoMainRepo(t *testing.T, branch string) (root string, git func(...string) string) {
	t.Helper()
	root = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(boundaryGitEnv(),
			"GIT_AUTHOR_NAME=Trunk Test",
			"GIT_AUTHOR_EMAIL=trunk-test@example.invalid",
			"GIT_COMMITTER_NAME=Trunk Test",
			"GIT_COMMITTER_EMAIL=trunk-test@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", branch)
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[trunk]\nbranch = \""+branch+"\"\n[push]\nwhat = \"green\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".tillr.toml", "README.md")
	git("commit", "-m", "initial on "+branch)
	return root, git
}

type trunkBranchTestSteps struct {
	t           *testing.T
	root        string
	store       string
	laneRoot    string
	trunkBranch string
	onLanded    func(Candidate, string)
}

func (s *trunkBranchTestSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	wt := filepath.Join(s.laneRoot, req.LaneID, "worktree")
	p := filepath.Join(wt, "change.txt")
	if err := os.WriteFile(p, []byte("from "+req.RunID+"\n"), 0o644); err != nil {
		return run.Record{}, "", err
	}
	cmd := exec.Command("git", "-C", wt, "add", "change.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		return run.Record{}, "", Externalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-C", wt, "commit", "-m", "work for "+req.ItemID)
	cmd.Env = append(boundaryGitEnv(),
		"GIT_AUTHOR_NAME=Lane Test",
		"GIT_AUTHOR_EMAIL=lane-test@example.invalid",
		"GIT_COMMITTER_NAME=Lane Test",
		"GIT_COMMITTER_EMAIL=lane-test@example.invalid",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return run.Record{}, "", Externalf("git commit: %v: %s", err, out)
	}
	s.t.Logf("steps.Run called for %s", req.ItemID)
	zero := 0
	rec := run.Record{
		RunID:      req.RunID,
		ItemID:     req.ItemID,
		LaneID:     req.LaneID,
		Backend:    req.Backend,
		State:      run.StateExited,
		ExitCode:   &zero,
		StartedAt:  "2026-11-25T00:00:00Z",
		FinishedAt: "2026-11-25T00:00:00Z",
	}
	if db, err := store.Open(context.Background(), s.store, true); err == nil {
		_ = db.WriteTx(context.Background(), func(tx *store.Tx) error {
			return applyTerminal(tx, rec)
		})
		db.Close()
	}
	return rec, "RESULT: done\n", nil
}

func (s *trunkBranchTestSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *trunkBranchTestSteps) Replay(_ context.Context, _ Candidate, _ string, _ []string) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *trunkBranchTestSteps) JudgeHeavy(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
	s.t.Logf("steps.JudgeHeavy called in %s", dir)
	return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
}

func (s *trunkBranchTestSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	s.t.Logf("steps.Land called for %s head=%s", c.ItemID, c.HeadSHA)
	ref := "refs/heads/" + s.trunkBranch
	if c.BaseRef != "" {
		ref = c.BaseRef
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", s.root}, args...)...)
		cmd.Env = append(boundaryGitEnv(),
			"GIT_AUTHOR_NAME=Trunk Test",
			"GIT_AUTHOR_EMAIL=trunk-test@example.invalid",
			"GIT_COMMITTER_NAME=Trunk Test",
			"GIT_COMMITTER_EMAIL=trunk-test@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			panic(string(out))
		}
		return strings.TrimSpace(string(out))
	}
	before := git("rev-parse", ref)
	tree := git("rev-parse", c.HeadSHA+"^{tree}")
	after := git("commit-tree", tree, "-p", before, "-m", "land "+c.ItemID)
	git("update-ref", ref, after, before)
	if headBranch := git("symbolic-ref", "--short", "HEAD"); headBranch == s.trunkBranch {
		git("reset", "--hard", "-q", after)
	}
	if db, err := store.Open(context.Background(), s.store, true); err == nil {
		_ = db.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO landings(landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after) VALUES(?, ?, ?, 'v1', 'landed', ?, ?, ?, ?)`,
				"land-"+c.RunID, c.ItemID, c.RunID, before, c.HeadSHA, before, after,
			)
			return err
		})
		db.Close()
	}
	if s.onLanded != nil {
		s.onLanded(c, after)
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: after}, nil
}

func (s *trunkBranchTestSteps) Verdict(_ context.Context, _ Candidate, _ string) error {
	return nil
}

func (s *trunkBranchTestSteps) ItemState(_ context.Context, itemID, runID, state string, _ ...item.WaitingOn) error {
	s.t.Logf("steps.ItemState(%s, %s, %s)", itemID, runID, state)
	db, err := store.Open(context.Background(), s.store, true)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, state, itemID)
		return err
	})
}

func (s *trunkBranchTestSteps) Sync(_ context.Context) (SyncOutcome, error) {
	return SyncOutcome{}, nil
}

func TestTrunkBranchFullLoopWithoutMain(t *testing.T) {
	root, git := initNoMainRepo(t, "dev")
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/dev")
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", "dev")
	initialDev := git("rev-parse", "refs/heads/dev")
	git("update-ref", GreenRef, initialDev)

	if out := git("branch", "--list", "main"); out != "" {
		t.Fatalf("unexpected local main branch: %q", out)
	}

	// The deadline is a hang guard, not a time limit: the loop reads any cancelled context,
	// its deadline included, as an operator stop, so a short deadline under load ends a
	// passing run as "operator-stop" before the drained-queue exit is taken.
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	insertQueuedItemForTrunkTest(t, s, "item-dev-1", "Land on dev", "Add change.txt.\n")
	s.Close()

	cfg, err := settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	laneRoot := scratchUnderDurable(t)
	steps := &trunkBranchTestSteps{t: t, root: root, store: dbPath, laneRoot: laneRoot, trunkBranch: "dev"}
	tpl := filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")
	stager := &LaneStager{Root: root, LaneRoot: laneRoot, Store: dbPath, Template: tpl, Now: time.Now, TrunkRef: cfg.TrunkBranch()}
	p := twoBackends()
	var log bytes.Buffer
	l, err := NewLoop(LoopConfig{
		Root:              root,
		Store:             dbPath,
		LaneRoot:          laneRoot,
		Lanes:             []string{"lane-1"},
		NoNotify:          true,
		StorePollInterval: 50 * time.Millisecond,
		ExitWhenDrained:   true,
		Policy:            &p,
		Steps:             steps,
		Stage:             stager,
		Now:               time.Now,
		Report:            &log,
		PostLanding:       cfg,
		TrunkBranch:       cfg.TrunkBranch(),
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.gatePolicy.LandingStrategy = "after-merge"

	res, err := l.Run(ctx)
	if err != nil {
		t.Fatalf("Loop.Run: %v\n%s", err, log.String())
	}
	if res.Stop != StopQueueDrained {
		t.Fatalf("stop = %q, want %q\n%s", res.Stop, StopQueueDrained, log.String())
	}
	waitAction(t, &l.pushAction)

	devTip := git("rev-parse", "refs/heads/dev")
	if devTip == initialDev {
		t.Fatalf("refs/heads/dev did not advance\n%s", log.String())
	}
	greenTip := git("rev-parse", GreenRef)
	if greenTip != devTip {
		t.Fatalf("green = %s, want dev tip %s", greenTip, devTip)
	}
	remoteDev := git("--git-dir="+remote, "rev-parse", "refs/heads/dev")
	if remoteDev != devTip {
		t.Fatalf("remote dev = %s, want %s", remoteDev, devTip)
	}
	if out := git("branch", "--list", "main"); out != "" {
		t.Fatalf("local main branch appeared: %q", out)
	}
	if out := git("--git-dir="+remote, "branch", "--list", "main"); out != "" {
		t.Fatalf("remote main branch appeared: %q", out)
	}
}

func TestTrunkBranchMainCheckoutOnOtherBranch(t *testing.T) {
	root, git := initNoMainRepo(t, "dev")
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/dev")
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", "dev")
	initialDev := git("rev-parse", "refs/heads/dev")
	git("update-ref", GreenRef, initialDev)

	// Switch main checkout to an unrelated branch `work` and keep it there.
	git("checkout", "-b", "work", initialDev)

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	insertQueuedItemForTrunkTest(t, s, "item-work-1", "Land on dev while checkout on work", "Add change.txt.\n")
	s.Close()

	cfg, err := settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	laneRoot := scratchUnderDurable(t)
	steps := &trunkBranchTestSteps{t: t, root: root, store: dbPath, laneRoot: laneRoot, trunkBranch: "dev"}
	tpl := filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")
	stager := &LaneStager{Root: root, LaneRoot: laneRoot, Store: dbPath, Template: tpl, Now: time.Now, TrunkRef: cfg.TrunkBranch()}
	p := twoBackends()
	l, err := NewLoop(LoopConfig{
		Root:              root,
		Store:             dbPath,
		LaneRoot:          laneRoot,
		Lanes:             []string{"lane-1"},
		NoNotify:          true,
		StorePollInterval: 50 * time.Millisecond,
		ExitWhenDrained:   true,
		Policy:            &p,
		Steps:             steps,
		Stage:             stager,
		Now:               time.Now,
		Report:            io.Discard,
		PostLanding:       cfg,
		TrunkBranch:       cfg.TrunkBranch(),
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.gatePolicy.LandingStrategy = "after-merge"

	if _, err := l.Run(ctx); err != nil {
		t.Fatalf("Loop.Run: %v", err)
	}
	waitAction(t, &l.pushAction)

	devTip := git("rev-parse", "refs/heads/dev")
	if devTip == initialDev {
		t.Fatal("refs/heads/dev did not advance")
	}
	if got := git("symbolic-ref", "--short", "HEAD"); got != "work" {
		t.Fatalf("main checkout moved to %q, want work", got)
	}
	if got := git("rev-parse", "HEAD"); got != initialDev {
		t.Fatalf("work branch moved to %s, want %s", got, initialDev)
	}
	if got := git("--git-dir="+remote, "rev-parse", "refs/heads/dev"); got != devTip {
		t.Fatalf("remote dev = %s, want %s", got, devTip)
	}

	// Verify remote-sync replay (healPushRefused) also advances refs/heads/dev without touching `work`.
	// Advance remote dev with an independent commit so local dev is behind/diverged.
	other := t.TempDir()
	exec.Command("git", "clone", "-b", "dev", remote, other).Run()
	_ = os.WriteFile(filepath.Join(other, "remote.txt"), []byte("remote\n"), 0o644)
	for _, args := range [][]string{
		{"-C", other, "add", "remote.txt"},
		{"-C", other, "-c", "user.name=Remote", "-c", "user.email=remote@example.invalid", "commit", "-m", "remote commit"},
		{"-C", other, "push", "origin", "dev"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Make a local commit directly on dev (without touching work checkout).
	localTree := git("rev-parse", devTip+"^{tree}")
	localAhead := git("commit-tree", localTree, "-p", devTip, "-m", "local ahead on dev")
	git("update-ref", "refs/heads/dev", localAhead, devTip)

	res, err := l.healRefusedPushResult(ctx, cfg.Push, errorsForTest("non-fast-forward"))
	if err != nil {
		t.Fatalf("healRefusedPushResult: %v", err)
	}
	if res.newTip == "" || res.newTip == localAhead {
		t.Fatalf("healRefusedPushResult newTip = %q, want replayed tip", res.newTip)
	}
	if got := git("rev-parse", "refs/heads/dev"); got != res.newTip {
		t.Fatalf("refs/heads/dev = %s, want %s", got, res.newTip)
	}
	if got := git("symbolic-ref", "--short", "HEAD"); got != "work" {
		t.Fatalf("main checkout moved to %q after replay, want work", got)
	}
}

func errorsForTest(msg string) error { return Externalf("%s", msg) }

func TestTrunkBranchStoreMismatchRefusal(t *testing.T) {
	root, _ := initNoMainRepo(t, "dev")
	ctx := context.Background()

	// 1. Legacy store with a landed row (no meta['trunk.branch']) defaults to main and refuses dev.
	legacyDB := filepath.Join(t.TempDir(), "legacy.db")
	s, err := store.InitFixture(ctx, legacyDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO landings(landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after) VALUES('l1','i1','r1','v1','landed','a','b','a','c')`); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	cfg, err := settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	p := twoBackends()
	laneRoot := scratchUnderDurable(t)
	_, err = NewLoop(LoopConfig{
		Root:        root,
		Store:       legacyDB,
		LaneRoot:    laneRoot,
		Lanes:       []string{"lane-1"},
		Policy:      &p,
		Steps:       &trunkBranchTestSteps{root: root, store: legacyDB, trunkBranch: "dev"},
		Stage:       &LaneStager{Root: root, LaneRoot: laneRoot, Store: legacyDB},
		PostLanding: cfg,
		TrunkBranch: cfg.TrunkBranch(),
	})
	if err == nil || !strings.Contains(err.Error(), "main") || !strings.Contains(err.Error(), "dev") {
		t.Fatalf("legacy mismatch err = %v, want both main and dev named", err)
	}

	// 2. Fresh store with zero landings accepts dev, records trunk.branch=dev on landing, then refuses release.
	freshDB := filepath.Join(t.TempDir(), "fresh.db")
	s2, err := store.InitFixture(ctx, freshDB)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()

	l, err := NewLoop(LoopConfig{
		Root:        root,
		Store:       freshDB,
		LaneRoot:    laneRoot,
		Lanes:       []string{"lane-1"},
		Policy:      &p,
		Steps:       &trunkBranchTestSteps{root: root, store: freshDB, trunkBranch: "dev"},
		Stage:       &LaneStager{Root: root, LaneRoot: laneRoot, Store: freshDB},
		PostLanding: cfg,
		TrunkBranch: cfg.TrunkBranch(),
	})
	if err != nil {
		t.Fatalf("fresh store NewLoop: %v", err)
	}

	if err := RecordTrunkBranchInStore(ctx, freshDB, "dev"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[trunk]\nbranch = \"release\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewLoop(LoopConfig{
		Root:        root,
		Store:       freshDB,
		LaneRoot:    laneRoot,
		Lanes:       []string{"lane-1"},
		Policy:      &p,
		Steps:       &trunkBranchTestSteps{root: root, store: freshDB, trunkBranch: "release"},
		Stage:       &LaneStager{Root: root, LaneRoot: laneRoot, Store: freshDB},
		PostLanding: cfg2,
		TrunkBranch: cfg2.TrunkBranch(),
	})
	if err == nil || !strings.Contains(err.Error(), "dev") || !strings.Contains(err.Error(), "release") {
		t.Fatalf("NewLoop mismatch err = %v, want both dev and release named", err)
	}
	_ = l
}

func TestTrunkBranchUnsetDefaultsToMain(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-b", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	cfg, err := settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TrunkBranch() != "main" {
		t.Fatalf("TrunkBranch = %q, want main", cfg.TrunkBranch())
	}
	l := &Loop{cfg: LoopConfig{Root: root, PostLanding: cfg}}
	if l.trunkRef() != "main" {
		t.Fatalf("l.trunkRef() = %q, want main", l.trunkRef())
	}
	_ = json.Marshal
}

type delayedHeavyTrunkSteps struct {
	*trunkBranchTestSteps
	delay time.Duration
}

func (s *delayedHeavyTrunkSteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.trunkBranchTestSteps.JudgeHeavy(ctx, dir, only)
}

func TestExitWhenDrainedWaitsForInFlightHeavyProofBeforeCancel(t *testing.T) {
	root, git := initNoMainRepo(t, "dev")
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("--git-dir="+remote, "symbolic-ref", "HEAD", "refs/heads/dev")
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", "dev")
	initialDev := git("rev-parse", "refs/heads/dev")
	git("update-ref", GreenRef, initialDev)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	insertQueuedItemForTrunkTest(t, s, "item-work-1", "Land on dev with delayed heavy proof", "Add change.txt.\n")
	s.Close()

	cfg, err := settings.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	laneRoot := scratchUnderDurable(t)
	steps := &delayedHeavyTrunkSteps{
		trunkBranchTestSteps: &trunkBranchTestSteps{t: t, root: root, store: dbPath, laneRoot: laneRoot, trunkBranch: "dev"},
		delay:                150 * time.Millisecond,
	}
	tpl := filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")
	stager := &LaneStager{Root: root, LaneRoot: laneRoot, Store: dbPath, Template: tpl, Now: time.Now, TrunkRef: cfg.TrunkBranch()}
	p := twoBackends()
	var report bytes.Buffer
	l, err := NewLoop(LoopConfig{
		Root:              root,
		Store:             dbPath,
		LaneRoot:          laneRoot,
		Lanes:             []string{"lane-1"},
		NoNotify:          true,
		StorePollInterval: 50 * time.Millisecond,
		ExitWhenDrained:   true,
		Policy:            &p,
		Steps:             steps,
		Stage:             stager,
		Now:               time.Now,
		Report:            &report,
		PostLanding:       cfg,
		TrunkBranch:       cfg.TrunkBranch(),
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}
	l.gatePolicy.LandingStrategy = "after-merge"

	if _, err := l.Run(ctx); err != nil {
		t.Fatalf("Loop.Run: %v\n%s", err, report.String())
	}
	waitAction(t, &l.pushAction)

	devTip := git("rev-parse", "refs/heads/dev")
	if devTip == initialDev {
		t.Fatalf("refs/heads/dev did not advance\n%s", report.String())
	}
	if got := git("--git-dir="+remote, "rev-parse", "refs/heads/dev"); got != devTip {
		t.Fatalf("remote dev = %s, want %s\n%s", got, devTip, report.String())
	}
}
