package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func initTrunkAndLaneFixture(t *testing.T) (dir, trunk, laneRoot, worktree, configPath string, beforeConfig []byte) {
	t.Helper()
	dir = scratch(t)
	trunk = filepath.Join(dir, "trunk")
	laneRoot = filepath.Join(dir, "lanes")
	worktree = filepath.Join(laneRoot, "lane-1", "worktree")
	if err := os.MkdirAll(trunk, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit := func(cwd string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture",
			"GIT_AUTHOR_EMAIL=fixture@example.invalid",
			"GIT_COMMITTER_NAME=fixture",
			"GIT_COMMITTER_EMAIL=fixture@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, cwd, err, out)
		}
	}
	runGit(trunk, "init", "-q")
	if err := os.WriteFile(filepath.Join(trunk, "README.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(trunk, "add", "README.txt")
	runGit(trunk, "commit", "-qm", "base")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(trunk, "worktree", "add", "-q", "-b", "lane-1", worktree, "HEAD")
	configPath = filepath.Join(trunk, ".git", "config")
	var err error
	beforeConfig, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return dir, trunk, laneRoot, worktree, configPath, beforeConfig
}

func TestExecGitGuardRefusesTrunkConfigWrite(t *testing.T) {
	cases := []struct {
		name      string
		script    func(trunk, cand string) string
		exportEnv bool
	}{
		{
			name: "worktree GIT_DIR and GIT_WORK_TREE exported around archive",
			script: func(_, cand string) string {
				return `set -eu
mkdir -p "$1"
git archive HEAD | tar -x -C "$1"
GIT_DIR="$(git rev-parse --git-dir)"
GIT_DIR="$GIT_DIR" GIT_WORK_TREE="$1" sh -c 'cd "$1" && git init' _ "$1"
`
			},
		},
		{
			name: "trunk GIT_DIR and GIT_WORK_TREE exported around git init dir",
			script: func(trunk, _ string) string {
				return `set -eu
mkdir -p "$1" "$1-sub"
GIT_DIR="` + filepath.Join(trunk, ".git") + `" GIT_WORK_TREE="$1" git init "$1-sub"
`
			},
		},
		{
			name: "explicit --git-dir and --work-tree flags on git init",
			script: func(trunk, _ string) string {
				return `set -eu
mkdir -p "$1"
git --git-dir="` + filepath.Join(trunk, ".git") + `" --work-tree="$1" init
`
			},
		},
		{
			name: "GIT_DIR and GIT_WORK_TREE exported on git config core.worktree",
			script: func(_, _ string) string {
				return `set -eu
mkdir -p "$1"
GIT_DIR="$(git rev-parse --git-dir)"
GIT_DIR="$GIT_DIR" GIT_WORK_TREE="$1" git config core.worktree "$1"
`
			},
		},
		{
			name:      "caller exports GIT_DIR and GIT_WORK_TREE before run.Exec",
			exportEnv: true,
			script: func(_, _ string) string {
				return `set -eu
mkdir -p "$1"
cd "$1"
git init -q
`
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir, trunk, laneRoot, _, configPath, beforeConfig := initTrunkAndLaneFixture(t)
			s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()

			cand := filepath.Join(dir, "cand")
			if tc.exportEnv {
				t.Setenv("GIT_DIR", filepath.Join(trunk, ".git"))
				t.Setenv("GIT_WORK_TREE", cand)
			}
			req := run.Request{
				RunID:      "run-git-guard",
				ItemID:     "item-git-guard",
				LaneID:     "lane-1",
				Backend:    "command",
				WorkingDir: "worktree",
				Command:    []string{"/bin/sh", "-c", tc.script(trunk, cand), "sh", cand},
			}
			env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			var rec run.Record
			if err := json.Unmarshal(env.Payload, &rec); err != nil {
				t.Fatal(err)
			}
			afterConfig, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(beforeConfig, afterConfig) {
				t.Fatalf("trunk .git/config changed:\nbefore:\n%s\nafter:\n%s", beforeConfig, afterConfig)
			}
			if strings.Contains(string(afterConfig), "worktree") {
				t.Fatalf("trunk .git/config gained core.worktree:\n%s", afterConfig)
			}
			if !tc.exportEnv {
				if rec.ExitCode == nil || *rec.ExitCode == 0 {
					logBytes, _ := os.ReadFile(filepath.Join(laneRoot, req.LaneID, req.RunID+".log"))
					t.Fatalf("expected git guard refusal (non-zero exit), got exit=%v\nlog:\n%s", rec.ExitCode, logBytes)
				}
				logBytes, err := os.ReadFile(filepath.Join(laneRoot, req.LaneID, req.RunID+".log"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(logBytes), "tillr-git-guard: refusing") {
					t.Fatalf("expected refusal diagnostic in log, got:\n%s", logBytes)
				}
			}
		})
	}
}

func TestExecGitGuardPreservesNormalLaneAndCloneWorkflows(t *testing.T) {
	ctx := context.Background()
	dir, _, laneRoot, _, configPath, beforeConfig := initTrunkAndLaneFixture(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	script := `set -eu
# Normal lane work: status, add, commit.
git status -s
echo "lane change" > lane.txt
git add lane.txt
GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@example.invalid \
GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@example.invalid \
git commit -qm "lane change"
HEAD_SHA="$(git rev-parse HEAD)"

# Supported reviewer checkout: local shared clone under scratch.
git clone --shared --no-checkout . "$YOLO_DURABLE_DIR/cand"
git -C "$YOLO_DURABLE_DIR/cand" checkout -q --detach "$HEAD_SHA"
test "$(git -C "$YOLO_DURABLE_DIR/cand" rev-parse HEAD)" = "$HEAD_SHA"
git -C "$YOLO_DURABLE_DIR/cand" status -s

# Unrelated temp repo git init inside a test.
mkdir -p "$YOLO_DURABLE_DIR/unrelated"
git -C "$YOLO_DURABLE_DIR/unrelated" init -q
git -C "$YOLO_DURABLE_DIR/unrelated" config user.name test
`
	req := run.Request{
		RunID:      "run-git-normal",
		ItemID:     "item-git-normal",
		LaneID:     "lane-1",
		Backend:    "command",
		WorkingDir: "worktree",
		Command:    []string{"/bin/sh", "-c", script},
	}
	env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ExitCode == nil || *rec.ExitCode != 0 {
		logBytes, _ := os.ReadFile(filepath.Join(laneRoot, req.LaneID, req.RunID+".log"))
		t.Fatalf("expected normal lane and clone commands to succeed, got exit=%v\nlog:\n%s", rec.ExitCode, logBytes)
	}
	afterConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeConfig, afterConfig) {
		t.Fatalf("trunk .git/config changed:\nbefore:\n%s\nafter:\n%s", beforeConfig, afterConfig)
	}
}
