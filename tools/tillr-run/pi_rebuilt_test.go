package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}

func initPatchPiRepo(t *testing.T, dir, tipCommitMessage string) (repoRoot, seriesKey string) {
	t.Helper()
	repoRoot = filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repoRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "bin", "pi-forks.list"), []byte("earendil-works/pi  patch/pi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoRoot, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("add", "bin/pi-forks.list")
	git("commit", "-q", "-m", "init trunk")
	git("checkout", "-q", "-b", "patch/pi")
	if err := os.WriteFile(filepath.Join(repoRoot, "patch.txt"), []byte(tipCommitMessage+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "patch.txt")
	git("commit", "-q", "-m", tipCommitMessage)
	tip := git("rev-parse", "refs/heads/patch/pi^{commit}")
	git("checkout", "-q", "main")
	return repoRoot, tip[:12]
}

func writeKeyedPiBuild(t *testing.T, buildsDir, key, marker string, verified bool) string {
	t.Helper()
	target := filepath.Join(buildsDir, key)
	piBin := filepath.Join(target, "home", ".local", "bin", "pi")
	writeExecutable(t, piBin, `#!/bin/sh
if [ "${1:-}" = "--version" ]; then
    echo "1.5.2-keyed-`+key+`"
    exit 0
fi
printf 'marker=`+marker+`\nsubagent_bin=%s\nagent_dir=%s\npolicy=%s\nprovider_key=%s\ncompile_cache=%s\nleaked_builds=%s\nleaked_record=%s\npath=%s\n' \
    "${PI_SUBAGENT_PI_BINARY:-}" "${PI_CODING_AGENT_DIR:-}" "${YOLO_PI_PROVIDER_POLICY:-}" "${TEST_PI_PROVIDER_KEY:-}" "${NODE_COMPILE_CACHE:-}" \
    "${TILLR_PI_BUILDS_DIR-unset}" "${TILLR_HARNESS_RECORD_FILE-unset}" "$PATH" > "$STUB_CHOSEN"
for a in "$@"; do printf 'arg=%s\n' "$a" >> "$STUB_CHOSEN"; done
cat "$STUB_STREAM"
`)
	if verified {
		if err := os.WriteFile(filepath.Join(target, ".verified"), []byte(key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return piBin
}

func TestPiAdapterResolvesRebuiltPiPrivatelyAndRecordsHarness(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	compact, err := filepath.Abs(filepath.Join("backends", "pi", "compact"))
	if err != nil {
		t.Fatal(err)
	}
	backends, err := filepath.Abs("backends")
	if err != nil {
		t.Fatal(err)
	}
	stream, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("prefers current series build over current.json and installed PATH, wiring yolo session env", func(t *testing.T) {
		dir := scratch(t, "pi-resolve-series")
		repoRoot, currentKey := initPatchPiRepo(t, dir, "series current")
		prevKey := "aaaaaaaaaaaa"
		durable := filepath.Join(dir, "durable")
		buildsDir := filepath.Join(durable, "pi-builds")

		currentBin := writeKeyedPiBuild(t, buildsDir, currentKey, "series-current", true)
		prevBin := writeKeyedPiBuild(t, buildsDir, prevKey, "previous-current", true)
		currentJSON, _ := json.Marshal(map[string]string{"key": prevKey, "pi": prevBin, "dir": filepath.Join(buildsDir, prevKey)})
		if err := os.WriteFile(filepath.Join(buildsDir, "current.json"), currentJSON, 0o644); err != nil {
			t.Fatal(err)
		}

		fakeHome := filepath.Join(dir, "home")
		sessionID := "0123456789abcdef0123456789abcdef"
		agentDir := filepath.Join(fakeHome, ".pi", ".yolo-sessions", sessionID, "agent")
		for _, rel := range []string{"extensions/yolo-provider-policy.js", "extensions/bedrock-context.ts", "extensions/steering-timer.ts", "yolo-patched/pi-subagents/index.ts", "yolo-patched/pi-background-tasks/index.ts"} {
			full := filepath.Join(agentDir, rel)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte("// ext\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		entryDir := filepath.Join(fakeHome, ".config", "yolo-agent-env", "sessions", sessionID)
		if err := os.MkdirAll(filepath.Join(entryDir, "provider-keys"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entryDir, "pi.sh"), []byte("export YOLO_PI_PROVIDER_POLICY=${YOLO_PI_PROVIDER_POLICY:-allowlist-fixture}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(entryDir, "provider-keys", "pi.sh"), []byte("export TEST_PI_PROVIDER_KEY=secret-from-yolo-entry\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		pathBin := filepath.Join(dir, "path-bin")
		writeExecutable(t, filepath.Join(pathBin, "pi"), "#!/bin/sh\nif [ \"${1:-}\" = \"--version\" ]; then echo 1.4.0-installed; exit 0; fi\necho 'marker=installed' > \"$STUB_CHOSEN\"\ncat \"$STUB_STREAM\"\n")
		t.Setenv("PATH", pathBin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("HOME", fakeHome)
		t.Setenv("YOLO_SESSION_ID", sessionID)
		t.Setenv("YOLO_SESSION_HOMES", "pi\tPI_CODING_AGENT_DIR\t"+agentDir)
		unsetEnvForTest(t, "PI_CODING_AGENT_DIR")
		unsetEnvForTest(t, "YOLO_PI_PROVIDER_POLICY")
		unsetEnvForTest(t, "NODE_COMPILE_CACHE")
		t.Setenv("YOLO_DURABLE_DIR", durable)
		unsetEnvForTest(t, "TILLR_PI_BUILDS_DIR")
		t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
		t.Setenv("TILLR_SESSION_MODE", "new")

		laneRoot := filepath.Join(dir, "lanes")
		laneDir := filepath.Join(laneRoot, "lane-1")
		worktree := filepath.Join(laneDir, "worktree")
		cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "-q", "-b", "lane-branch", worktree, "main")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v: %s", err, out)
		}

		chosen := filepath.Join(dir, "chosen.txt")
		t.Setenv("STUB_CHOSEN", chosen)
		t.Setenv("STUB_STREAM", stream)

		if err := os.WriteFile(filepath.Join(laneDir, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(laneDir, "base_sha"), []byte(strings.Repeat("0", 40)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		req := runner.Request{
			RunID:      "run-series",
			ItemID:     "item-1",
			LaneID:     "lane-1",
			Backend:    "pi",
			WorkingDir: "worktree",
			Command:    []string{adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "low"},
		}
		env, err := runner.Exec(context.Background(), s, req, runner.ExecOptions{LaneRoot: laneRoot, BackendsDir: backends, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		var rec runner.Record
		if err := json.Unmarshal(env.Payload, &rec); err != nil {
			t.Fatal(err)
		}
		wantHarness := currentBin + " (series " + currentKey + ")"
		if rec.Harness != wantHarness {
			t.Fatalf("record harness = %q, want %q", rec.Harness, wantHarness)
		}
		gotChosen, err := os.ReadFile(chosen)
		if err != nil {
			t.Fatal(err)
		}
		text := string(gotChosen)
		for _, wantLine := range []string{
			"marker=series-current",
			"subagent_bin=" + currentBin,
			"agent_dir=" + agentDir,
			"policy=allowlist-fixture",
			"provider_key=secret-from-yolo-entry",
			"compile_cache=" + filepath.Join(fakeHome, ".local", "state", "yolo", "compile-cache", "node"),
			"leaked_builds=unset",
			"leaked_record=unset",
		} {
			if !strings.Contains(text, wantLine+"\n") {
				t.Fatalf("missing %q in chosen output:\n%s", wantLine, text)
			}
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "path=") && strings.Contains(line, filepath.Dir(currentBin)) {
				t.Fatalf("rebuilt pi directory was placed on shared PATH:\n%s", line)
			}
		}
		for _, ext := range []string{
			"arg=" + filepath.Join(agentDir, "extensions", "yolo-provider-policy.js"),
			"arg=" + filepath.Join(agentDir, "extensions", "bedrock-context.ts"),
			"arg=" + filepath.Join(agentDir, "yolo-patched", "pi-subagents"),
			"arg=" + filepath.Join(agentDir, "yolo-patched", "pi-background-tasks"),
		} {
			if !strings.Contains(text, ext+"\n") {
				t.Fatalf("missing extension %q in args:\n%s", ext, text)
			}
		}
		if strings.Contains(text, "steering-timer.ts") {
			t.Fatalf("interactive steering-timer.ts should stay excluded:\n%s", text)
		}
	})

	t.Run("falls back to previous verified current.json build when new series has no verified build", func(t *testing.T) {
		dir := scratch(t, "pi-resolve-fallback-current")
		repoRoot, unbuiltKey := initPatchPiRepo(t, dir, "new series whose build failed")
		prevKey := "bbbbbbbbbbbb"
		durable := filepath.Join(dir, "durable")
		buildsDir := filepath.Join(durable, "pi-builds")

		// Unverified directory for unbuiltKey must be ignored.
		_ = writeKeyedPiBuild(t, buildsDir, unbuiltKey, "unverified", false)
		prevBin := writeKeyedPiBuild(t, buildsDir, prevKey, "previous-verified", true)
		currentJSON, _ := json.Marshal(map[string]string{"key": prevKey, "pi": prevBin, "dir": filepath.Join(buildsDir, prevKey)})
		if err := os.WriteFile(filepath.Join(buildsDir, "current.json"), currentJSON, 0o644); err != nil {
			t.Fatal(err)
		}

		pathBin := filepath.Join(dir, "path-bin")
		writeExecutable(t, filepath.Join(pathBin, "pi"), "#!/bin/sh\necho 'marker=installed' > \"$STUB_CHOSEN\"\ncat \"$STUB_STREAM\"\n")
		t.Setenv("PATH", pathBin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("YOLO_DURABLE_DIR", durable)
		unsetEnvForTest(t, "TILLR_PI_BUILDS_DIR")
		t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
		t.Setenv("TILLR_SESSION_MODE", "new")

		laneRoot := filepath.Join(dir, "lanes")
		laneDir := filepath.Join(laneRoot, "lane-1")
		worktree := filepath.Join(laneDir, "worktree")
		if out, err := exec.Command("git", "-C", repoRoot, "worktree", "add", "-q", "-b", "lane-branch", worktree, "main").CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v: %s", err, out)
		}
		if err := os.WriteFile(filepath.Join(laneDir, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
			t.Fatal(err)
		}

		chosen := filepath.Join(dir, "chosen.txt")
		t.Setenv("STUB_CHOSEN", chosen)
		t.Setenv("STUB_STREAM", stream)

		s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		req := runner.Request{
			RunID:      "run-fallback-current",
			LaneID:     "lane-1",
			Backend:    "pi",
			WorkingDir: "worktree",
			Command:    []string{adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "low"},
		}
		env, err := runner.Exec(context.Background(), s, req, runner.ExecOptions{LaneRoot: laneRoot, BackendsDir: backends, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		var rec runner.Record
		if err := json.Unmarshal(env.Payload, &rec); err != nil {
			t.Fatal(err)
		}
		wantHarness := prevBin + " (series " + prevKey + ")"
		if rec.Harness != wantHarness {
			t.Fatalf("record harness = %q, want %q", rec.Harness, wantHarness)
		}
		gotChosen, err := os.ReadFile(chosen)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(gotChosen), "marker=previous-verified\n") {
			t.Fatalf("did not fall back to previous verified current build:\n%s", gotChosen)
		}
	})

	t.Run("falls back to installed pi on PATH and records installed path and version", func(t *testing.T) {
		dir := scratch(t, "pi-resolve-installed")
		repoRoot, _ := initPatchPiRepo(t, dir, "series without any builds")
		durable := filepath.Join(dir, "durable")
		buildsDir := filepath.Join(durable, "pi-builds")
		if err := os.MkdirAll(buildsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// Corrupt current.json must not block fallback to installed PATH pi.
		if err := os.WriteFile(filepath.Join(buildsDir, "current.json"), []byte("{not-json"), 0o644); err != nil {
			t.Fatal(err)
		}

		pathBin := filepath.Join(dir, "path-bin")
		installedPi := filepath.Join(pathBin, "pi")
		writeExecutable(t, installedPi, `#!/bin/sh
if [ "${1:-}" = "--version" ]; then
    echo "1.5.2-installed"
    exit 0
fi
printf 'marker=installed\nsubagent_bin=%s\n' "${PI_SUBAGENT_PI_BINARY:-}" > "$STUB_CHOSEN"
cat "$STUB_STREAM"
`)
		t.Setenv("PATH", pathBin+string(os.PathListSeparator)+os.Getenv("PATH"))
		t.Setenv("YOLO_DURABLE_DIR", durable)
		unsetEnvForTest(t, "TILLR_PI_BUILDS_DIR")
		t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
		t.Setenv("TILLR_SESSION_MODE", "new")

		laneRoot := filepath.Join(dir, "lanes")
		laneDir := filepath.Join(laneRoot, "lane-1")
		worktree := filepath.Join(laneDir, "worktree")
		if out, err := exec.Command("git", "-C", repoRoot, "worktree", "add", "-q", "-b", "lane-branch", worktree, "main").CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v: %s", err, out)
		}
		if err := os.WriteFile(filepath.Join(laneDir, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
			t.Fatal(err)
		}

		chosen := filepath.Join(dir, "chosen.txt")
		t.Setenv("STUB_CHOSEN", chosen)
		t.Setenv("STUB_STREAM", stream)

		s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		req := runner.Request{
			RunID:      "run-installed",
			LaneID:     "lane-1",
			Backend:    "pi",
			WorkingDir: "worktree",
			Command:    []string{adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "low"},
		}
		env, err := runner.Exec(context.Background(), s, req, runner.ExecOptions{LaneRoot: laneRoot, BackendsDir: backends, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		var rec runner.Record
		if err := json.Unmarshal(env.Payload, &rec); err != nil {
			t.Fatal(err)
		}
		realInstalled, _ := filepath.EvalSymlinks(installedPi)
		wantHarness := realInstalled + " (installed 1.5.2-installed)"
		if rec.Harness != wantHarness {
			t.Fatalf("record harness = %q, want %q", rec.Harness, wantHarness)
		}
		gotChosen, err := os.ReadFile(chosen)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(gotChosen), "marker=installed\n") {
			t.Fatalf("did not fall back to installed pi:\n%s", gotChosen)
		}
	})

	t.Run("compact resolves rebuilt pi privately without needing pi on PATH", func(t *testing.T) {
		dir := scratch(t, "pi-compact-rebuilt")
		repoRoot, currentKey := initPatchPiRepo(t, dir, "compact series")
		buildsDir := filepath.Join(dir, "durable", "pi-builds")
		target := filepath.Join(buildsDir, currentKey)
		rebuiltPi := filepath.Join(target, "home", ".local", "bin", "pi")
		marker := filepath.Join(dir, "compact-ran-rebuilt")
		writeExecutable(t, rebuiltPi, `#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
Path(os.environ["COMPACT_MARKER"]).write_text("rebuilt:" + os.path.abspath(sys.argv[0]))
for line in sys.stdin.buffer:
    c = json.loads(line)
    data = {"sessionId": "11111111-1111-4111-8111-111111111111", "sessionFile": os.environ["TRANSCRIPT"]}
    if c["type"] == "compact":
        data = {"tokensBefore": 50000, "estimatedTokensAfter": 20000, "usage": {"totalTokens": 1000, "cost": {"total": 0.1}}}
    print(json.dumps({"type": "response", "id": c["id"], "success": True, "data": data}), flush=True)
`)
		if err := os.WriteFile(filepath.Join(target, ".verified"), []byte(currentKey+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		transcript := filepath.Join(dir, "session.jsonl")
		if err := os.WriteFile(transcript, []byte("session\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Scrub any pi from PATH so compact can succeed ONLY by resolving rebuilt pi.
		cleanDirs := []string{}
		for _, p := range filepath.SplitList(os.Getenv("PATH")) {
			if _, err := os.Stat(filepath.Join(p, "pi")); err == nil {
				continue
			}
			cleanDirs = append(cleanDirs, p)
		}
		cmd := exec.Command(compact, "--backend", "pi-designer", "--model", "fixture-model", "--effort", "medium", "--session-id", "11111111-1111-4111-8111-111111111111")
		cmd.Dir = repoRoot
		for _, entry := range os.Environ() {
			if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "YOLO_DURABLE_DIR=") || strings.HasPrefix(entry, "TILLR_PI_BUILDS_DIR=") {
				continue
			}
			cmd.Env = append(cmd.Env, entry)
		}
		cmd.Env = append(cmd.Env,
			"PATH="+strings.Join(cleanDirs, string(os.PathListSeparator)),
			"YOLO_DURABLE_DIR="+filepath.Join(dir, "durable"),
			"COMPACT_MARKER="+marker,
			"TRANSCRIPT="+transcript,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compact failed without PATH pi: %v: %s", err, out)
		}
		gotMarker, err := os.ReadFile(marker)
		if err != nil || !strings.HasPrefix(string(gotMarker), "rebuilt:") {
			t.Fatalf("compact did not run rebuilt pi: %q / %v", gotMarker, err)
		}
	})
}
