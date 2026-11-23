package drain

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

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPostLandingPiRollMovedBranchBuildsNewTipAndWorkerRecordsKey(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	l, git, _ := actionFixture(t)
	ctx := context.Background()

	durable := t.TempDir()
	buildsDir := filepath.Join(durable, "pi-builds")
	t.Setenv("YOLO_DURABLE_DIR", durable)
	t.Setenv("TILLR_PI_BUILDS_DIR", buildsDir)

	// Copy bin/pi-forks and fork_input.py into the fixture repo.
	copyExec := func(src, dst string, mode os.FileMode) {
		t.Helper()
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	writeFile := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copyExec(filepath.Join(repoRoot, "bin", "pi-forks"), filepath.Join(l.cfg.Root, "bin", "pi-forks"), 0o755)
	copyExec(filepath.Join(repoRoot, "tools", "gate", "checks", "fork_input.py"), filepath.Join(l.cfg.Root, "tools", "gate", "checks", "fork_input.py"), 0o755)

	writeFile(filepath.Join(l.cfg.Root, "bin", "pi-forks.list"), "earendil-works/pi  patch/pi\n")
	writeFile(filepath.Join(l.cfg.Root, "yolo-packs", "pi-fork", "pack.json"), `{
  "name": "pi-fork",
  "contributes": [
    {
      "kind": "program",
      "bin": "pi",
      "via": "source",
      "source": "git+https://github.com/earendil-works/pi?ref=main",
      "patch_branch": "git+ssh://git@github.com/fixture/tillr?ref=patch/pi"
    }
  ]
}
`)

	// Build a minimal upstream + patch/pi fork fixture with a fake scripts/fork-build.mjs.
	forkWork := t.TempDir()
	gitAt := func(dir string, args ...string) string {
		t.Helper()
		out, err := actionCommand(ctx, dir, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	gitAt(forkWork, "init", "-q", "-b", "main")
	writeFile(filepath.Join(forkWork, "package.json"), `{"name":"pi-monorepo","private":true,"scripts":{"build":"true"}}`+"\n")
	writeFile(filepath.Join(forkWork, "package-lock.json"), "{}\n")
	writeFile(filepath.Join(forkWork, "scripts", "fork-build.mjs"), `import { mkdirSync, symlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const home = process.env.HOME;
const share = join(home, ".local/share/pi-fork");
const binDir = join(share, "node_modules/.bin");
const pkgDir = join(share, "node_modules/@earendil-works/pi-coding-agent/dist");
mkdirSync(binDir, { recursive: true });
mkdirSync(pkgDir, { recursive: true });
mkdirSync(join(home, ".local/bin"), { recursive: true });
writeFileSync(join(binDir, "pi"), "#!/usr/bin/env node\nif (process.argv.includes('--version')) { console.log('1.6.0-test'); process.exit(0); }\nif (process.argv.includes('--help')) { console.log('pi help'); process.exit(0); }\n", { mode: 0o755 });
writeFileSync(join(pkgDir, "index.js"), "export class DefaultResourceLoader { async reload() {} getExtensions() { return { extensions: [], errors: [] }; } }\nexport class SettingsManager { static create() { return {}; } }\n");
writeFileSync(join(share, "node_modules/@earendil-works/pi-coding-agent/package.json"), JSON.stringify({ name: "@earendil-works/pi-coding-agent", version: "1.6.0", main: "./dist/index.js" }));
symlinkSync("../share/pi-fork/node_modules/.bin/pi", join(home, ".local/bin/pi"));
`)
	gitAt(forkWork, "add", ".")
	gitAt(forkWork, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "upstream base")
	baseSHA := gitAt(forkWork, "rev-parse", "HEAD")

	gitAt(forkWork, "checkout", "-q", "-b", "patch/pi")
	writeFile(filepath.Join(forkWork, "patch1.txt"), "patch1\n")
	gitAt(forkWork, "add", "patch1.txt")
	gitAt(forkWork, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "ours: patch 1")
	tip1 := gitAt(forkWork, "rev-parse", "refs/heads/patch/pi")
	key1 := tip1[:12]

	// Seed refs/heads/patch/pi at tip1 and pin yolo-packs/fork-pins/pi at tip1.
	git("fetch", "-q", forkWork, "+refs/heads/patch/pi:refs/heads/patch/pi")
	writeFile(filepath.Join(l.cfg.Root, "yolo-packs", "fork-pins", "pi"), tip1+"\n"+forkWork+"\n"+baseSHA+"\n")
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "seed pi fork fixture")

	// Fake npm on PATH so build-pi does not hit the network.
	fakeBin := t.TempDir()
	fakeNpm := filepath.Join(fakeBin, "npm")
	if err := os.WriteFile(fakeNpm, []byte("#!/bin/sh\nmkdir -p node_modules\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Seed an initial verified build for key1 so current.json starts on key1.
	if out, err := actionCommand(ctx, l.cfg.Root, "bash", filepath.Join(l.cfg.Root, "bin", "pi-forks"), "build-pi"); err != nil {
		t.Fatalf("initial build-pi failed: %v\n%s", err, out)
	}
	if got := readCurrentPiBuildKey(buildsDir); got != key1 {
		t.Fatalf("initial current.json key = %q, want %q", got, key1)
	}

	// Create a new patch/pi commit (tip2) with NO existing build while fork-pins/pi stays on tip1.
	writeFile(filepath.Join(forkWork, "patch2.txt"), "patch2\n")
	gitAt(forkWork, "add", "patch2.txt")
	gitAt(forkWork, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "ours: patch 2")
	tip2 := gitAt(forkWork, "rev-parse", "refs/heads/patch/pi")
	key2 := tip2[:12]
	if key2 == key1 {
		t.Fatalf("expected distinct keys, both %s", key1)
	}

	// Roll wrapper mimics `just tillr-roll --no-request --pi` -> `bin/pi-forks build-pi`.
	rollScript := filepath.Join(l.cfg.Root, "roll.sh")
	if err := writeForkSafe(rollScript, []byte("#!/bin/sh\nset -eu\nexec bash \"$PWD/bin/pi-forks\" build-pi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding.Roll = settings.Roll{Command: "./roll.sh --no-request"}
	l.cfg.Steps = landStepsStub{land: func(context.Context, Candidate) (LandOutcome, error) {
		git("fetch", "-q", forkWork, "+refs/heads/patch/pi:refs/heads/patch/pi")
		return LandOutcome{Result: LandLanded, TrunkAfter: tip2}, nil
	}}

	if _, err := l.landWithActions(ctx, Candidate{ItemID: "item-pi-move", RunID: "run-pi-move", BaseRef: piForkPatchRef}); err != nil {
		t.Fatal(err)
	}
	waitAction(t, func() bool { return !l.piRollAction.busy() })

	if !hasVerifiedPiBuild(buildsDir, key2) {
		t.Fatalf("expected verified build for moved tip %s under %s; report:\n%s", key2, buildsDir, l.reported())
	}
	if got := readCurrentPiBuildKey(buildsDir); got != key2 {
		t.Fatalf("current.json key after roll = %q, want %q", got, key2)
	}
	if !strings.Contains(l.reported(), key2) {
		t.Fatalf("loop log did not mention built key %s:\n%s", key2, l.reported())
	}

	// Verify the next worker run resolves and records key2 via pi-harness.sh.
	harnessScript := filepath.Join(repoRoot, "tools", "tillr-run", "backends", "pi", "pi-harness.sh")
	cmd := exec.CommandContext(ctx, "sh", "-c", `. "$1" && pi_resolve_harness && printf '%s\n' "$PI_HARNESS_RECORD"`, "sh", harnessScript)
	cmd.Dir = l.cfg.Root
	cmd.Env = append(os.Environ(), "PI_HARNESS_SOURCED=1", "TILLR_PI_BUILDS_DIR="+buildsDir)
	recordOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pi_resolve_harness failed: %v\n%s", err, recordOut)
	}
	wantRecord := filepath.Join(buildsDir, key2, "home", ".local", "bin", "pi") + " (series " + key2 + ")"
	if got := strings.TrimSpace(string(recordOut)); got != wantRecord {
		t.Fatalf("next worker harness record = %q, want %q", got, wantRecord)
	}

	// Confirm no pi-build-stale alarm is raised.
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var active []alarms.Alarm
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		active, err = alarms.Read(r, time.Now().UTC(), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range active {
		if a.Kind == alarms.PiBuildStale {
			t.Fatalf("unexpected pi-build-stale alarm after successful build: %s", a)
		}
	}
}

func TestPostLandingPiRollFailureAndSkipRaiseWhyCondition(t *testing.T) {
	ctx := context.Background()
	const staleKey = "24c7e4f89e08"
	const wantedTip = "54175817b6000000000000000000000000000000"
	const wantedKey = "54175817b600"

	seedBuilds := func(t *testing.T) string {
		t.Helper()
		buildsDir := filepath.Join(t.TempDir(), "pi-builds")
		staleDir := filepath.Join(buildsDir, staleKey)
		staleBin := filepath.Join(staleDir, "home", ".local", "bin", "pi")
		if err := os.MkdirAll(filepath.Dir(staleBin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(staleBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(staleDir, ".verified"), []byte(staleKey+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cur, err := json.Marshal(map[string]string{"key": staleKey, "pi": staleBin, "dir": staleDir})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(buildsDir, "current.json"), cur, 0o644); err != nil {
			t.Fatal(err)
		}
		return buildsDir
	}

	readWhyAlarms := func(t *testing.T, storePath string) string {
		t.Helper()
		s, err := store.OpenRead(ctx, storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var ans WhyAnswer
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			ans, err = WhyForItem(r, Pass{Now: time.Now().UTC().Format(time.RFC3339), StorePath: storePath}, nil, nil, "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := alarms.Print(&buf, ans.Alarms); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	for _, tc := range []struct {
		name       string
		command    string
		scriptBody string
		wantReason string
	}{
		{
			name:       "failed",
			command:    "./roll.sh --no-request",
			scriptBody: "#!/bin/sh\necho 'build-pi: scripts/fork-build.mjs failed' >&2\nexit 2\n",
			wantReason: "failed",
		},
		{
			name:       "skipped_no_roll_command",
			command:    "",
			wantReason: "skipped",
		},
		{
			name:       "skipped_exit_zero_without_build",
			command:    "./roll.sh --no-request",
			scriptBody: "#!/bin/sh\nexit 0\n",
			wantReason: "skipped",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _, _ := actionFixture(t)
			buildsDir := seedBuilds(t)
			t.Setenv("TILLR_PI_BUILDS_DIR", buildsDir)

			if tc.scriptBody != "" {
				script := filepath.Join(l.cfg.Root, "roll.sh")
				if err := writeForkSafe(script, []byte(tc.scriptBody), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			l.cfg.PostLanding.Roll = settings.Roll{Command: tc.command}
			l.cfg.Steps = landStepsStub{land: func(context.Context, Candidate) (LandOutcome, error) {
				return LandOutcome{Result: LandLanded, TrunkAfter: wantedTip}, nil
			}}

			if _, err := l.landWithActions(ctx, Candidate{ItemID: "item-pi-" + tc.name, RunID: "run-pi-" + tc.name, BaseRef: piForkPatchRef}); err != nil {
				t.Fatal(err)
			}
			waitAction(t, func() bool { return !l.piRollAction.busy() })

			report := l.reported()
			for _, want := range []string{tc.wantReason, staleKey, wantedKey} {
				if !strings.Contains(report, want) {
					t.Fatalf("loop log missing %q:\n%s", want, report)
				}
			}

			whyOut := readWhyAlarms(t, l.cfg.Store)
			for _, want := range []string{alarms.PiBuildStale, tc.wantReason, staleKey, wantedKey} {
				if !strings.Contains(whyOut, want) {
					t.Fatalf("tillr drain why output missing %q:\n%s", want, whyOut)
				}
			}

			// A subsequent successful roll that produces the verified build clears the condition.
			okScript := filepath.Join(l.cfg.Root, "roll-ok.sh")
			okBody := "#!/bin/sh\nset -eu\ndir=\"" + filepath.Join(buildsDir, wantedKey) + "\"\nmkdir -p \"$dir/home/.local/bin\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"$dir/home/.local/bin/pi\"\nchmod +x \"$dir/home/.local/bin/pi\"\nprintf '%s\\n' '" + wantedKey + "' > \"$dir/.verified\"\n"
			if err := writeForkSafe(okScript, []byte(okBody), 0o755); err != nil {
				t.Fatal(err)
			}
			l.cfg.PostLanding.Roll = settings.Roll{Command: "./roll-ok.sh --no-request"}
			if _, err := l.landWithActions(ctx, Candidate{ItemID: "item-pi-clear", RunID: "run-pi-clear", BaseRef: piForkPatchRef}); err != nil {
				t.Fatal(err)
			}
			waitAction(t, func() bool { return !l.piRollAction.busy() })

			if cleared := readWhyAlarms(t, l.cfg.Store); strings.Contains(cleared, alarms.PiBuildStale) {
				t.Fatalf("expected pi-build-stale to clear after successful build, got:\n%s", cleared)
			}
		})
	}
}

type syncHookSteps struct {
	*recordingSteps
	onSync func()
}

func (s *syncHookSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	if s.onSync != nil {
		s.onSync()
	}
	return s.recordingSteps.Sync(ctx)
}

func TestLoopStartupAndPassPiBuildStaleAlarm(t *testing.T) {
	ctx := context.Background()
	const staleKey = "24c7e4f89e08"

	f := newRefusalLoopFixtureWithLanes(t, 1)
	if err := os.MkdirAll(filepath.Join(f.root, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, ".git", "info", "exclude"), ".tillr/\n")

	forkWork := t.TempDir()
	runGit(t, forkWork, "init", "-q", "-b", "patch/pi")
	writeForkFile(t, filepath.Join(forkWork, "pi.txt"), "pi fork v1.6.0\n")
	runGit(t, forkWork, "add", "pi.txt")
	runGit(t, forkWork, "commit", "-qm", "ours: pi fork tip")
	wantedTip := runGit(t, forkWork, "rev-parse", "refs/heads/patch/pi")
	wantedKey := wantedTip[:12]
	if wantedKey == staleKey {
		t.Fatalf("unexpected collision with staleKey %s", staleKey)
	}
	runGit(t, f.root, "fetch", "-q", "--no-tags", forkWork, "+refs/heads/patch/pi:refs/heads/patch/pi")

	buildsDir := filepath.Join(t.TempDir(), "pi-builds")
	writeVerifiedBuild := func(key string) string {
		dir := filepath.Join(buildsDir, key)
		bin := filepath.Join(dir, "home", ".local", "bin", "pi")
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".verified"), []byte(key+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return bin
	}
	writeCurrentJSON := func(key, bin string) {
		cur, err := json.Marshal(map[string]string{
			"key": key,
			"pi":  bin,
			"dir": filepath.Join(buildsDir, key),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(buildsDir, "current.json"), cur, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	staleBin := writeVerifiedBuild(staleKey)
	writeCurrentJSON(staleKey, staleBin)
	t.Setenv("TILLR_PI_BUILDS_DIR", buildsDir)

	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	readWhy := func() string {
		var ans WhyAnswer
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			ans, err = WhyForItem(r, Pass{Now: now.Format(time.RFC3339), StorePath: f.storePath}, nil, nil, "")
			return err
		})
		var buf bytes.Buffer
		if err := alarms.Print(&buf, ans.Alarms); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	countKind := func(kind string) int {
		var n int
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, kind).Scan(&n)
		})
		return n
	}

	var startupWhy string
	steps := &syncHookSteps{
		recordingSteps: &recordingSteps{},
		onSync: func() {
			if startupWhy == "" {
				startupWhy = readWhy()
			}
		},
	}

	var log bytes.Buffer
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.Report = &log

	res, err := l.Run(ctx)
	if err != nil {
		t.Fatalf("Loop.Run failed: %v", err)
	}
	if res.Stop != StopQueueDrained {
		t.Fatalf("Loop.Run stop = %q (%s), want %q", res.Stop, res.Detail, StopQueueDrained)
	}

	// Verified at loop start (before runPasses) and after Loop.Run returns.
	for _, want := range []string{alarms.PiBuildStale, staleKey, wantedKey} {
		if !strings.Contains(startupWhy, want) {
			t.Fatalf("why alarms during loop startup missing %q:\n%s", want, startupWhy)
		}
	}
	whyOut := readWhy()
	for _, want := range []string{alarms.PiBuildStale, staleKey, wantedKey} {
		if !strings.Contains(whyOut, want) {
			t.Fatalf("why alarms after loop start missing %q:\n%s", want, whyOut)
		}
	}
	for _, want := range []string{"alarm started: ALARM pi-build-stale (pi)", staleKey, wantedKey} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("loop report missing %q:\n%s", want, log.String())
		}
	}
	if got := countKind(alarms.PiBuildStaleKind); got != 1 {
		t.Fatalf("%s count = %d, want 1", alarms.PiBuildStaleKind, got)
	}

	// Another pass while still unbuilt keeps the alarm standing without duplicating the event.
	holdWALKeeper(t, f.storePath)
	if _, err := l.pass(ctx, &res); err != nil {
		t.Fatalf("second pass failed: %v", err)
	}
	if got := countKind(alarms.PiBuildStaleKind); got != 1 {
		t.Fatalf("%s count after unchanged pass = %d, want 1", alarms.PiBuildStaleKind, got)
	}

	// Once a verified build for the patch/pi tip exists, the alarm clears on pass evaluation without a fork landing.
	builtBin := writeVerifiedBuild(wantedKey)
	writeCurrentJSON(wantedKey, builtBin)
	if _, err := l.pass(ctx, &res); err != nil {
		t.Fatalf("pass after verified build failed: %v", err)
	}
	if cleared := readWhy(); strings.Contains(cleared, alarms.PiBuildStale) {
		t.Fatalf("expected pi-build-stale to clear when verified build exists, got:\n%s", cleared)
	}
	if !strings.Contains(log.String(), "alarm cleared: pi-build-stale:pi") {
		t.Fatalf("expected loop report to record alarm cleared, got:\n%s", log.String())
	}
	if got := countKind(alarms.PiBuildCurrentKind); got != 1 {
		t.Fatalf("%s count after clear = %d, want 1", alarms.PiBuildCurrentKind, got)
	}

	// Moving patch/pi to another unbuilt tip without a fork landing raises the alarm on the next pass,
	// and starting a fresh loop after building that tip clears it at loop start.
	writeForkFile(t, filepath.Join(forkWork, "pi.txt"), "pi fork v1.6.1\n")
	runGit(t, forkWork, "add", "pi.txt")
	runGit(t, forkWork, "commit", "-qm", "ours: second pi fork tip")
	wantedTip2 := runGit(t, forkWork, "rev-parse", "refs/heads/patch/pi")
	wantedKey2 := wantedTip2[:12]
	if wantedKey2 == wantedKey || wantedKey2 == staleKey {
		t.Fatalf("unexpected key collision %s", wantedKey2)
	}
	runGit(t, f.root, "fetch", "-q", "--no-tags", forkWork, "+refs/heads/patch/pi:refs/heads/patch/pi")

	if _, err := l.pass(ctx, &res); err != nil {
		t.Fatalf("pass after moving patch/pi failed: %v", err)
	}
	whyMoved := readWhy()
	for _, want := range []string{alarms.PiBuildStale, wantedKey, wantedKey2} {
		if !strings.Contains(whyMoved, want) {
			t.Fatalf("why alarms after moving patch/pi missing %q:\n%s", want, whyMoved)
		}
	}

	writeVerifiedBuild(wantedKey2)
	l.reportAlarms(ctx, l.cfg.Now())
	if cleared := readWhy(); strings.Contains(cleared, alarms.PiBuildStale) {
		t.Fatalf("expected pi-build-stale to clear after building second tip, got:\n%s", cleared)
	}
}
