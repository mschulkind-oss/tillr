package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// keepGoingStep is what keep-going.ts answered at one scripted ending.
type keepGoingStep struct {
	Continue   bool   `json:"continue"`
	Type       string `json:"type"`
	CustomType string `json:"customType"`
	Message    string `json:"message"`
	Reason     string `json:"reason"`
}

// A worker whose goal is a commit is answered in the same session when it ends without
// one and without naming a complete obstacle, and only goal-met, a complete obstacle, an identical
// repeat, or an ending that is not the model's lets it settle. Measured 2026-11-18: Sol 6.2
// at medium ended 19 of 61 drain runs "RESULT: failed" with no blocker after a handful of
// read-only commands, and nothing answered them.
func TestKeepGoingAnswersAnEndingThatMissesTheGoal(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node is not installed; the pi adapter cannot run without it, so this extension's behavior is unverified")
	}
	extension, err := filepath.Abs(filepath.Join("backends", "pi", "keep-going.ts"))
	if err != nil {
		t.Fatal(err)
	}
	driver, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "keep-going-driver.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "keep-going")
	repo := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README")
	git("commit", "-q", "-m", "base")
	base := git("rev-parse", "HEAD")

	cases, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "obstacle-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, driver, extension, cases)
	cmd.Dir = repo
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "BOARD_DRAIN_BRIEF=") &&
			!strings.HasPrefix(entry, "TILLR_CALL_SUSPENDS=") &&
			!strings.HasPrefix(entry, "TILLR_ENDING_CAPABILITY=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env,
		"TILLR_GOAL_BASE="+base,
		"TILLR_PAUSE_REQUEST="+filepath.Join(dir, "pause"),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("the driver failed: %v: %s", err, stderr.String())
	}
	var got struct {
		Quit, Reworded, Repeat, Edited, DoneUncommitted, Blocked, Error, Aborted, Paused keepGoingStep
		BlockedNoObstacle, SeatNoAttempt, SeatAttempted                                  keepGoingStep
		Cases                                                                            map[string]string
		CommittedNoFooter, Goal, CommittedDirty                                          keepGoingStep
		NoGoalHandlers                                                                   int
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}

	nudged := func(name string, step keepGoingStep, wants ...string) {
		t.Helper()
		if !step.Continue || step.Type != "custom_message" || step.CustomType != "tillr-goal" {
			t.Errorf("%s: want a same-session continuation with a tillr-goal message, got %+v", name, step)
			return
		}
		for _, want := range append(wants, "Your next message must be a tool call") {
			if !strings.Contains(step.Message, want) {
				t.Errorf("%s: the message does not say %q:\n%s", name, want, step.Message)
			}
		}
	}
	settled := func(name string, step keepGoingStep, reason string) {
		t.Helper()
		if step.Continue {
			t.Errorf("%s: want the run to settle, but it was continued: %s", name, step.Message)
		}
		if !strings.Contains(step.Reason, reason) {
			t.Errorf("%s: want a settle reason containing %q, got %q", name, reason, step.Reason)
		}
	}

	nudged("an ending with no commit and no blocker", got.Quit, "there is no commit", base[:12])
	nudged("a different report on the same tree", got.Reworded, "there is no commit")
	settled("the same report on the same tree", got.Repeat, "identical repeat")
	nudged("the same report after the tree changed", got.Edited, "a.txt")
	nudged("RESULT: done with nothing committed", got.DoneUncommitted, "nothing is committed")
	nudged("RESULT: blocked with no obstacle named", got.BlockedNoObstacle, "names no obstacle", "OBSTACLE: <kind>")
	settled("a complete obstacle", got.Blocked, "complete obstacle")
	nudged("a seat obstacle on an untouched tree", got.SeatNoAttempt, "names no attempt")
	settled("a seat obstacle after an attempt", got.SeatAttempted, "complete obstacle")
	raw, err = os.ReadFile(cases)
	if err != nil {
		t.Fatal(err)
	}
	var want []struct {
		Name   string `json:"name"`
		Report string `json:"report"`
		Ending bool   `json:"ending"`
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for _, c := range want {
		problem, ok := got.Cases[c.Name]
		if !ok {
			t.Errorf("case %q: the extension did not read it", c.Name)
			continue
		}
		// A done report and a failed one are the footer's business, not the obstacle's:
		// only a blocked report is read for an obstacle.
		if !strings.Contains(c.Report, "RESULT: blocked") && !strings.Contains(c.Report, "RESULT: trunk-broken") {
			continue
		}
		if (problem == "") != c.Ending {
			t.Errorf("case %q: complete=%v, want %v (%s)", c.Name, problem == "", c.Ending, problem)
		}
	}
	settled("a provider error", got.Error, "not by the model")
	settled("an aborted run", got.Aborted, "aborted")
	settled("a requested pause", got.Paused, "pause")
	nudged("a commit with no report", got.CommittedNoFooter, "no RESULT footer")
	settled("a commit, a clean tree and a report", got.Goal, "goal is met")
	nudged("a commit with a file left behind", got.CommittedDirty, "b.txt")
	if got.NoGoalHandlers != 0 {
		t.Errorf("with no TILLR_GOAL_BASE the extension registered %d handlers, want none", got.NoGoalHandlers)
	}
}

// run-pi loads keep-going.ts, with the lane's base as TILLR_GOAL_BASE, only for a writer:
// a lane worktree brief with the base sha beside it, outside a read-only group. The same
// writer's session is trimmed: no extension, skill or context-file discovery, with only
// the provider extensions, sub-agents and background tasks loaded by name and the
// target's own AGENTS.md appended. Any other run keeps the full session.
func TestThePiAdapterLoadsKeepGoingOnlyForALaneWorktreeWriter(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	const base = "3da00ddcfa4e958bb43edd694018da26ba760c8d"
	for _, tc := range []struct {
		name         string
		kind         string
		baseFile     bool
		readOnly     string
		readOnlyNode bool
		wantGoal     bool
	}{
		{"writer", "lane worktree", true, "", false, true},
		{"no base sha", "lane worktree", false, "", false, false},
		{"read-only node", "lane worktree", true, "group-review", false, false},
		{"read-only workflow node", "lane worktree", true, "", true, false},
		{"fork checkout", "fork checkout", true, "", false, false},
		{"plain directory", "plain directory", true, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := scratch(t, "keep-going-wiring")
			bin := filepath.Join(dir, "bin")
			record := filepath.Join(dir, "record")
			writeExecutable(t, filepath.Join(bin, "pi"),
				"#!/bin/sh\n{ printf 'goal=%s\\n' \"${TILLR_GOAL_BASE-unset}\"; for a in \"$@\"; do printf 'arg=%s\\n' \"$a\"; done; } > \"$STUB_RECORD\"\n")
			lane := filepath.Join(dir, "lane")
			worktree := filepath.Join(lane, "worktree")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(dir, "agent")
			for _, path := range []string{"extensions/yolo-openai-auth.js", "extensions/bedrock-flex.ts", "extensions/steering-timer.ts", "extensions/subagent/config.json", "yolo-patched/pi-subagents/index.ts", "yolo-patched/pi-background-tasks/index.ts", "yolo-patched/pi-session-search/index.ts"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(agentDir, path)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(agentDir, path), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("# rules\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			brief := strings.Replace(filledBrief(t, worktree), "("+briefValues["TARGET_KIND"]+")", "("+tc.kind+")", 1)
			if tc.readOnlyNode {
				brief += "\n## Workflow node\n\nRun only node plan on the plan seat; access: read_only.\n"
			}
			if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(brief), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.baseFile {
				if err := os.WriteFile(filepath.Join(lane, "base_sha"), []byte(base+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
			cmd.Dir = worktree
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "TILLR_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env,
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"STUB_RECORD="+record,
				"TILLR_GOAL_BASE=stale-parent-value",
				"TILLR_READ_ONLY_GROUP="+tc.readOnly,
				"PI_CODING_AGENT_DIR="+agentDir,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the adapter failed: %v: %s", err, out)
			}
			body, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			extension, _ := filepath.EvalSymlinks(filepath.Join(filepath.Dir(adapter), "keep-going.ts"))
			loads := strings.Contains(text, "arg=-e\narg="+extension+"\n")
			if tc.wantGoal {
				if !loads || !strings.Contains(text, "goal="+base+"\n") {
					t.Fatalf("a writer must load %s with TILLR_GOAL_BASE=%s:\n%s", extension, base, text)
				}
			} else if loads || !strings.Contains(text, "goal=unset\n") {
				t.Fatalf("a %s run must neither load keep-going nor inherit a goal:\n%s", tc.name, text)
			}
			realWorktree, _ := filepath.EvalSymlinks(worktree)
			trimmed := []string{"arg=--no-extensions\n", "arg=--no-skills\n", "arg=--no-context-files\n",
				"arg=-e\narg=" + filepath.Join(agentDir, "extensions", "yolo-openai-auth.js") + "\n",
				"arg=-e\narg=" + filepath.Join(agentDir, "extensions", "bedrock-flex.ts") + "\n",
				"arg=-e\narg=" + filepath.Join(agentDir, "yolo-patched", "pi-subagents") + "\n",
				"arg=-e\narg=" + filepath.Join(agentDir, "yolo-patched", "pi-background-tasks") + "\n",
				"arg=--append-system-prompt\narg=" + filepath.Join(realWorktree, "AGENTS.md") + "\n"}
			for _, want := range trimmed {
				if strings.Contains(text, want) != tc.wantGoal {
					t.Fatalf("%s: trimmed-session argument %q present=%v, want %v:\n%s", tc.name, want, !tc.wantGoal, tc.wantGoal, text)
				}
			}
			for _, never := range []string{"steering-timer.ts", "extensions/subagent", "pi-session-search"} {
				if strings.Contains(text, never) {
					t.Fatalf("%s: %s was loaded by name:\n%s", tc.name, never, text)
				}
			}
		})
	}
}
