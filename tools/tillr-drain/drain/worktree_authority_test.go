package drain_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
)

type benchNoopStager struct{}

func (benchNoopStager) Stage(context.Context, drain.StageSpec) error { return nil }

func cleanWorkerEnvForAuthorityTest(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"TILLR_RUN_ID",
		"TILLR_RUN_EXEC_ID",
		"BOARD_DRAIN_RUN",
		"TILLR_REPO_ROOT",
		"TILLR_LOOP_STORE",
		"BOARD_DRAIN_BRIEF",
	} {
		t.Setenv(name, "")
	}
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "git"))
		if err == nil && strings.Contains(string(raw), "# tillr-git-guard\n") {
			continue
		}
		kept = append(kept, dir)
	}
	t.Setenv("PATH", strings.Join(kept, string(os.PathListSeparator)))
}

func saveCommonGuard(t *testing.T, next func(string) error) {
	t.Helper()
	prev := heavyrun.CommonGuard
	heavyrun.CommonGuard = next
	t.Cleanup(func() { heavyrun.CommonGuard = prev })
}

func TestWorkerCannotRegisterWorktreesOrBranchesInDrainRepository(t *testing.T) {
	adapter := stub(t, "adapter", "exit 0")
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, fencedItemRecord("item_bench", "not-started", "candidate.txt"))
	baseSHA := git(t, f.root, "rev-parse", "main")

	workerWorktree := filepath.Join(f.root, ".tillr", "lanes", "lane-5", "worktree")
	if err := os.MkdirAll(filepath.Dir(workerWorktree), 0o755); err != nil {
		t.Fatalf("creating worker lane parent: %v", err)
	}
	git(t, f.root, "worktree", "add", "-B", "tillr/lanes-fixture/lane-5", workerWorktree, baseSHA)
	t.Cleanup(func() {
		_, _ = exec.Command("git", "-C", f.root, "worktree", "remove", "--force", workerWorktree).CombinedOutput()
	})

	wantWorktrees := git(t, f.root, "worktree", "list", "--porcelain")
	wantBranches := git(t, f.root, "for-each-ref", "--format=%(refname)", "refs/heads/")

	assertDrainRepoUntouched := func(t *testing.T) {
		t.Helper()
		if got := git(t, f.root, "worktree", "list", "--porcelain"); got != wantWorktrees {
			t.Fatalf("worktrees registered in drain repository:\ngot:\n%s\nwant:\n%s", got, wantWorktrees)
		}
		if got := git(t, f.root, "for-each-ref", "--format=%(refname)", "refs/heads/"); got != wantBranches {
			t.Fatalf("branches registered in drain repository:\ngot:\n%s\nwant:\n%s", got, wantBranches)
		}
		for _, checkout := range []string{f.root, workerWorktree} {
			resultPath := filepath.Join(checkout, ".tillr", "loop", "loop.result.json")
			if _, err := os.Stat(resultPath); !os.IsNotExist(err) {
				t.Fatalf("refused loop wrote result file %s (err=%v)", resultPath, err)
			}
		}
	}

	runBenchLoop := func(t *testing.T, root, laneRoot string) (drain.LoopResult, error) {
		t.Helper()
		steps := f.steps(t, "/bin/true", "/bin/true", "/bin/true")
		steps.Root = root
		steps.LaneRoot = laneRoot
		loop, err := drain.NewLoop(drain.LoopConfig{
			Root:            root,
			Store:           f.store,
			LaneRoot:        laneRoot,
			Lanes:           []string{"lane-1", "express-1"},
			ExitWhenDrained: true,
			Policy:          f.policy,
			Steps:           steps,
			Stage:           benchNoopStager{},
			Now:             steps.Now,
			Report:          io.Discard,
		})
		if err != nil {
			return drain.LoopResult{}, err
		}
		return loop.Run(context.Background())
	}

	t.Run("linked_worktree_root_refused", func(t *testing.T) {
		cleanWorkerEnvForAuthorityTest(t)
		saveCommonGuard(t, nil)

		laneRoot := filepath.Join(scratch(t, "bench-after"), "lanes-after")
		res, err := runBenchLoop(t, workerWorktree, laneRoot)
		if err == nil {
			t.Fatalf("Loop.Run on linked lane worktree succeeded (%+v); want refusal", res)
		}
		assertDrainRepoUntouched(t)
	})

	t.Run("worker_env_drain_root_refused_in_process_and_cli", func(t *testing.T) {
		cleanWorkerEnvForAuthorityTest(t)
		saveCommonGuard(t, nil)
		t.Setenv("TILLR_RUN_ID", "run-bench-worker")
		t.Setenv("TILLR_REPO_ROOT", f.root)
		t.Setenv("TILLR_LOOP_STORE", filepath.Join(f.root, ".tillr", "state.db"))

		laneRoot := filepath.Join(scratch(t, "live-measure"), "after-lanes-1")
		res, err := runBenchLoop(t, f.root, laneRoot)
		if err == nil {
			t.Fatalf("worker Loop.Run on drain root succeeded (%+v); want refusal", res)
		}
		assertDrainRepoUntouched(t)

		policyPath := filepath.Join(scratch(t, "cli-policy"), "policy.json")
		writeFile(t, policyPath, fmt.Sprintf(`{"default_backend":"fixture","backends":{"fixture":{"adapter":%q,"model":"fixture-model","timeout_seconds":0}}}`, adapter))
		cliLanes := filepath.Join(scratch(t, "cli-measure"), "cli-lanes")
		code, out := invoke(t, "loop",
			"--exit-when-drained",
			"--root", f.root,
			"--store", f.store,
			"--lane-root", cliLanes,
			"--lanes", "1",
			"--policy", policyPath,
			"--run-bin", sibling(t, "run"),
			"--item-bin", sibling(t, "item"),
			"--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			"--gate", "/bin/true",
			"--land", "/bin/true",
			"--verdict", "/bin/true",
			"--rolling=",
			"--fixer-gate=",
		)
		if code != 2 {
			t.Fatalf("CLI loop on drain root under worker env exited %d (%s); want exit 2", code, out)
		}
		assertDrainRepoUntouched(t)
	})

	t.Run("git_guard_on_path_refuses_drain_root", func(t *testing.T) {
		cleanWorkerEnvForAuthorityTest(t)
		saveCommonGuard(t, nil)

		realGit, err := exec.LookPath("git")
		if err != nil {
			t.Fatalf("locating git: %v", err)
		}
		guardDir := filepath.Join(scratch(t, "git-guard"), "guard")
		wrapperDir := filepath.Join(scratch(t, "git-wrapper"), "wrapper")
		if err := os.MkdirAll(guardDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(wrapperDir, 0o755); err != nil {
			t.Fatal(err)
		}
		commonDir := filepath.Join(f.root, ".git")
		guardScript := fmt.Sprintf("#!/bin/sh\n# tillr-git-guard\nREAL_GIT='%s'\nPROTECTED_COMMON_DIR='%s'\nexec \"$REAL_GIT\" \"$@\"\n", realGit, commonDir)
		wrapperScript := fmt.Sprintf("#!/bin/sh\nexec '%s' \"$@\"\n", filepath.Join(guardDir, "git"))
		if err := os.WriteFile(filepath.Join(guardDir, "git"), []byte(guardScript), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wrapperDir, "git"), []byte(wrapperScript), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", wrapperDir+string(os.PathListSeparator)+guardDir+string(os.PathListSeparator)+os.Getenv("PATH"))

		laneRoot := filepath.Join(scratch(t, "guard-measure"), "debug-lanes")
		res, err := runBenchLoop(t, f.root, laneRoot)
		if err == nil {
			t.Fatalf("Loop.Run with tillr-git-guard on PATH succeeded (%+v); want refusal", res)
		}
		assertDrainRepoUntouched(t)
	})

	t.Run("common_guard_refuses_loop_uninitialized_subdir_and_stager", func(t *testing.T) {
		cleanWorkerEnvForAuthorityTest(t)
		realCommon := filepath.Clean(filepath.Join(f.root, ".git"))
		if resolved, err := filepath.EvalSymlinks(realCommon); err == nil {
			realCommon = filepath.Clean(resolved)
		}
		saveCommonGuard(t, func(common string) error {
			got := filepath.Clean(common)
			if resolved, err := filepath.EvalSymlinks(got); err == nil {
				got = filepath.Clean(resolved)
			}
			if got == realCommon {
				return fmt.Errorf("guarded common dir %s", common)
			}
			return nil
		})

		if res, err := runBenchLoop(t, f.root, filepath.Join(scratch(t, "cg-loop"), "lanes")); err == nil {
			t.Fatalf("Loop.Run under CommonGuard succeeded (%+v); want refusal", res)
		}
		assertDrainRepoUntouched(t)

		uninitSubdir := filepath.Join(f.root, "scratch-without-git-init")
		if err := os.MkdirAll(uninitSubdir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(uninitSubdir) })
		if _, err := runBenchLoop(t, uninitSubdir, filepath.Join(scratch(t, "cg-uninit"), "lanes")); err == nil {
			t.Fatalf("Loop.Run on uninitialized subdir inside drain repo succeeded; want refusal")
		}
		if err := os.RemoveAll(uninitSubdir); err != nil {
			t.Fatal(err)
		}
		assertDrainRepoUntouched(t)

		stager := &drain.LaneStager{
			Root:     f.root,
			Store:    f.store,
			LaneRoot: filepath.Join(scratch(t, "cg-stager"), "lanes"),
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		}
		err := stager.Stage(context.Background(), drain.StageSpec{
			LaneID:     "lane-1",
			ItemID:     "item_bench",
			RunID:      "run-item_bench-1",
			Backend:    "pi",
			BaseSHA:    baseSHA,
			WorkingDir: "worktree",
			TimeoutMS:  60000,
		})
		if err == nil {
			t.Fatalf("LaneStager.Stage under CommonGuard succeeded; want refusal")
		}
		assertDrainRepoUntouched(t)
	})

	t.Run("worker_isolated_scratch_repository_still_stages", func(t *testing.T) {
		cleanWorkerEnvForAuthorityTest(t)
		saveCommonGuard(t, nil)
		t.Setenv("TILLR_RUN_ID", "run-bench-worker")
		t.Setenv("TILLR_REPO_ROOT", f.root)
		t.Setenv("TILLR_LOOP_STORE", filepath.Join(f.root, ".tillr", "state.db"))

		isolated := newLoopFixture(t, adapter)
		fixtureAt(t, isolated.store, fencedItemRecord("item_iso", "landed", "candidate.txt"))
		steps := isolated.steps(t, "/bin/true", "/bin/true", "/bin/true")
		loop, err := drain.NewLoop(drain.LoopConfig{
			Root:            isolated.root,
			Store:           isolated.store,
			LaneRoot:        isolated.laneRoot,
			Lanes:           []string{"lane-1", "express-1"},
			ExitWhenDrained: true,
			Policy:          isolated.policy,
			Steps:           steps,
			Stage:           benchNoopStager{},
			Now:             steps.Now,
			Report:          io.Discard,
		})
		if err != nil {
			t.Fatalf("NewLoop on isolated repo: %v", err)
		}
		res, err := loop.Run(context.Background())
		if err != nil {
			t.Fatalf("Loop.Run on isolated scratch repo failed: %v (%+v)", err, res)
		}
		isolatedWorktrees := git(t, isolated.root, "worktree", "list", "--porcelain")
		for _, lane := range []string{"lane-1", "express-1"} {
			wantPath := filepath.Join(isolated.laneRoot, lane, "worktree")
			if !strings.Contains(isolatedWorktrees, wantPath) {
				t.Fatalf("isolated repo missing staged worktree %s:\n%s", wantPath, isolatedWorktrees)
			}
		}
		assertDrainRepoUntouched(t)
	})
}
