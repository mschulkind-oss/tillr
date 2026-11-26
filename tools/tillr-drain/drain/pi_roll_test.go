package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

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
			waitAction(t, &l.piRollAction)

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
			waitAction(t, &l.piRollAction)

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

func TestPiBuildStaleNotRaisedWhileRollInProgress(t *testing.T) {
	ctx := context.Background()

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

	countStaleEvents := func(t *testing.T, storePath string) int {
		t.Helper()
		s, err := store.OpenRead(ctx, storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var n int
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, alarms.PiBuildStaleKind).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}

	waitForFile := func(t *testing.T, path string) {
		t.Helper()
		deadline := time.Now().Add(hangGuard)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(path); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", path)
	}

	for _, tc := range []struct {
		name     string
		wantFail bool
	}{
		{name: "roll_succeeds", wantFail: false},
		{name: "roll_fails", wantFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, git, _ := actionFixture(t)
			buildsDir := filepath.Join(t.TempDir(), "pi-builds")
			t.Setenv("TILLR_PI_BUILDS_DIR", buildsDir)

			// Seed a fork repo with two commits so patch/pi can advance from staleTip to wantedTip.
			forkWork := t.TempDir()
			runGit(t, forkWork, "init", "-q", "-b", "patch/pi")
			writeForkFile(t, filepath.Join(forkWork, "pi.txt"), "pi fork v1\n")
			runGit(t, forkWork, "add", "pi.txt")
			runGit(t, forkWork, "commit", "-qm", "ours: pi fork v1")
			staleTip := runGit(t, forkWork, "rev-parse", "refs/heads/patch/pi")
			staleKey := staleTip[:12]

			writeForkFile(t, filepath.Join(forkWork, "pi.txt"), "pi fork v2\n")
			runGit(t, forkWork, "add", "pi.txt")
			runGit(t, forkWork, "commit", "-qm", "ours: pi fork v2")
			wantedTip := runGit(t, forkWork, "rev-parse", "refs/heads/patch/pi")
			wantedKey := wantedTip[:12]
			if wantedKey == staleKey {
				t.Fatalf("unexpected key collision %s", wantedKey)
			}

			// Fetch both commits into the loop repo, pointing patch/pi at staleTip initially.
			git("fetch", "-q", "--no-tags", forkWork, "+refs/heads/patch/pi:refs/heads/patch/pi")
			git("update-ref", piForkPatchRef, staleTip)

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

			startedFile := filepath.Join(t.TempDir(), "roll-started")
			releaseFile := filepath.Join(t.TempDir(), "roll-release")
			var scriptBody string
			if tc.wantFail {
				scriptBody = "#!/bin/sh\nset -eu\n: > \"" + startedFile + "\"\nwhile [ ! -f \"" + releaseFile + "\" ]; do sleep 0.01; done\necho 'build-pi: failed' >&2\nexit 2\n"
			} else {
				targetDir := filepath.Join(buildsDir, wantedKey)
				scriptBody = "#!/bin/sh\nset -eu\n: > \"" + startedFile + "\"\nwhile [ ! -f \"" + releaseFile + "\" ]; do sleep 0.01; done\nmkdir -p \"" + targetDir + "/home/.local/bin\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"" + targetDir + "/home/.local/bin/pi\"\nchmod +x \"" + targetDir + "/home/.local/bin/pi\"\nprintf '%s\\n' '" + wantedKey + "' > \"" + targetDir + "/.verified\"\n"
			}
			script := filepath.Join(l.cfg.Root, "roll.sh")
			if err := writeForkSafe(script, []byte(scriptBody), 0o755); err != nil {
				t.Fatal(err)
			}
			l.cfg.PostLanding.Roll = settings.Roll{Command: "./roll.sh --no-request"}

			refUpdated := make(chan struct{})
			allowLandReturn := make(chan struct{})
			l.cfg.Steps = landStepsStub{land: func(context.Context, Candidate) (LandOutcome, error) {
				git("update-ref", piForkPatchRef, wantedTip)
				close(refUpdated)
				<-allowLandReturn
				return LandOutcome{Result: LandLanded, TrunkAfter: wantedTip}, nil
			}}

			landDone := make(chan error, 1)
			go func() {
				_, err := l.landWithActions(ctx, Candidate{
					ItemID:  "item-pi-" + tc.name,
					RunID:   "run-pi-" + tc.name,
					BaseRef: piForkPatchRef,
				})
				landDone <- err
			}()

			// 1. Right after Land moves refs/heads/patch/pi and before Land returns / submitPiRoll runs:
			//    an alarm pass must not raise pi-build-stale.
			<-refUpdated
			l.reportAlarms(ctx, time.Now().UTC())
			if got := countStaleEvents(t, l.cfg.Store); got != 0 {
				close(allowLandReturn)
				_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
				<-landDone
				waitAction(t, &l.piRollAction)
				t.Fatalf("pi-build-stale raised during land before roll submission (%d events):\n%s", got, l.reported())
			}
			if whyOut := readWhyAlarms(t, l.cfg.Store); strings.Contains(whyOut, alarms.PiBuildStale) {
				close(allowLandReturn)
				_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
				<-landDone
				waitAction(t, &l.piRollAction)
				t.Fatalf("why reported pi-build-stale during land before roll submission:\n%s", whyOut)
			}

			// 2. Allow Land to return and wait for the roll command itself to be running;
			//    an alarm pass while the roll runs must not raise pi-build-stale.
			close(allowLandReturn)
			if err := <-landDone; err != nil {
				_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
				t.Fatal(err)
			}
			waitForFile(t, startedFile)
			l.reportAlarms(ctx, time.Now().UTC())
			if got := countStaleEvents(t, l.cfg.Store); got != 0 {
				_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
				waitAction(t, &l.piRollAction)
				t.Fatalf("pi-build-stale raised while roll was running (%d events):\n%s", got, l.reported())
			}
			if whyOut := readWhyAlarms(t, l.cfg.Store); strings.Contains(whyOut, alarms.PiBuildStale) {
				_ = os.WriteFile(releaseFile, []byte("go\n"), 0o644)
				waitAction(t, &l.piRollAction)
				t.Fatalf("why reported pi-build-stale while roll was running:\n%s", whyOut)
			}

			// 3. Let the roll finish and run another alarm pass.
			if err := os.WriteFile(releaseFile, []byte("go\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			waitAction(t, &l.piRollAction)
			l.reportAlarms(ctx, time.Now().UTC())

			report := l.reported()
			if tc.wantFail {
				whyOut := readWhyAlarms(t, l.cfg.Store)
				for _, want := range []string{alarms.PiBuildStale, "failed", staleKey, wantedKey} {
					if !strings.Contains(whyOut, want) {
						t.Fatalf("why alarms after failed roll missing %q:\n%s", want, whyOut)
					}
				}
				if !strings.Contains(report, "alarm started: ALARM pi-build-stale (pi)") {
					t.Fatalf("loop log missing alarm started after failed roll:\n%s", report)
				}
			} else {
				if got := countStaleEvents(t, l.cfg.Store); got != 0 {
					t.Fatalf("expected 0 pi-build-stale events across successful roll, got %d:\n%s", got, report)
				}
				if strings.Contains(report, "alarm started: ALARM pi-build-stale") {
					t.Fatalf("unexpected pi-build-stale alarm in log for successful roll:\n%s", report)
				}
				if want := "post-landing pi roll at " + wantedKey + " built key " + wantedKey; !strings.Contains(report, want) {
					t.Fatalf("loop log missing %q:\n%s", want, report)
				}
				if whyOut := readWhyAlarms(t, l.cfg.Store); strings.Contains(whyOut, alarms.PiBuildStale) {
					t.Fatalf("unexpected pi-build-stale in why after successful roll:\n%s", whyOut)
				}
			}
		})
	}
}
