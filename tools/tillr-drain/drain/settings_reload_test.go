package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

type settingsReloadSyncSteps struct {
	*recordingSteps
	root string
	land func(context.Context, Candidate) (LandOutcome, error)
}

func (s *settingsReloadSyncSteps) Sync(_ context.Context) (SyncOutcome, error) {
	if _, err := settings.Read(s.root); err != nil {
		return SyncOutcome{}, Usagef("tillr-item sync could not run: %v", err)
	}
	return SyncOutcome{}, nil
}

func (s *settingsReloadSyncSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if s.land != nil {
		return s.land(ctx, c)
	}
	return s.recordingSteps.Land(ctx, c)
}

func readSettingsAlarms(t *testing.T, storePath string) string {
	t.Helper()
	ctx := context.Background()
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

func commitSettingsFile(t *testing.T, root string, git func(...string) string, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "--", ".tillr.toml")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", msg)
	return git("rev-parse", "HEAD")
}

func TestLandedRollCommandReachesRunningLoop(t *testing.T) {
	ctx := context.Background()
	l, git, _ := actionFixture(t)

	initial, err := settings.Read(l.cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding = initial
	if l.cfg.PostLanding.Roll.Command != "" {
		t.Fatalf("expected empty initial roll command, got %q", l.cfg.PostLanding.Roll.Command)
	}

	marker := filepath.Join(t.TempDir(), "rolled.txt")
	script := filepath.Join(l.cfg.Root, "roll.sh")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + marker + "\n"
	if err := writeForkSafe(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. A trunk landing adds [roll] to .tillr.toml.
	settingsTip := commitSettingsFile(t, l.cfg.Root, git,
		"[roll]\ncommand = \"./roll.sh --no-request\"\npaths = [\"tools/tillr*/\"]\n",
		"add roll command")

	l.cfg.Steps = landStepsStub{land: func(_ context.Context, c Candidate) (LandOutcome, error) {
		if c.BaseRef == piForkPatchRef {
			return LandOutcome{Result: LandLanded, TrunkAfter: "54175817b6000000000000000000000000000000"}, nil
		}
		return LandOutcome{Result: LandLanded, TrunkAfter: settingsTip}, nil
	}}

	if _, err := l.landWithActions(ctx, Candidate{ItemID: "item-add-roll", RunID: "run-add-roll"}); err != nil {
		t.Fatal(err)
	}
	waitAction(t, &l.rollAction, &l.piRollAction)

	if whyOut := readSettingsAlarms(t, l.cfg.Store); strings.Contains(whyOut, alarms.SettingsDrift) {
		t.Fatalf("unexpected settings-drift alarm after reloadable [roll] change:\n%s", whyOut)
	}

	// 2. A subsequent pi fork landing must use the landed [roll] command rather than
	// skipping with "no [roll] command configured".
	if _, err := l.landWithActions(ctx, Candidate{ItemID: "item-pi-fork", RunID: "run-pi-fork", BaseRef: piForkPatchRef}); err != nil {
		t.Fatal(err)
	}
	waitAction(t, &l.piRollAction)

	gotBytes, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("expected landed [roll] command to run on subsequent fork landing, but marker was not written (%v);\nloop log:\n%s", err, l.reported())
	}
	got := strings.TrimSpace(string(gotBytes))
	if !strings.Contains(got, "--pi") {
		t.Fatalf("expected --pi roll invocation, got %q", got)
	}
	if strings.Contains(l.reported(), "no [roll] command configured") {
		t.Fatalf("unexpected skip message after [roll] landed:\n%s", l.reported())
	}
}

func TestLandedStartupOnlySettingsRaiseAlarmUntilRestart(t *testing.T) {
	ctx := context.Background()
	l, git, _ := actionFixture(t)

	initial, err := settings.Read(l.cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding = initial
	l.cfg.Lanes = []string{"lane-1"}

	// Landing changes restart-only [lanes] express alongside live-reloadable [push].
	tip1 := commitSettingsFile(t, l.cfg.Root, git,
		"[lanes]\nexpress = 2\n[push]\nwhat = \"green\"\n",
		"add express lanes and green push")

	l.afterLanding(ctx, tip1)

	if got := l.postLandingSettings().Push.What; got != settings.PushGreen {
		t.Fatalf("expected live-reloadable push.what = %q, got %q", settings.PushGreen, got)
	}
	whyOut := readSettingsAlarms(t, l.cfg.Store)
	for _, want := range []string{alarms.SettingsDrift, "lanes.express", "running 0, file 2"} {
		if !strings.Contains(whyOut, want) {
			t.Fatalf("expected settings-drift alarm naming %q, got:\n%s", want, whyOut)
		}
	}

	// A drain restart (drain.build-started for role drain) clears the alarm.
	if err := restart.PublishBuild(ctx, l.cfg.Store, restart.BuildFact{
		Role:     restart.RoleDrain,
		Revision: "abcdef123456",
		Digest:   "sha256:test",
		PID:      1,
		At:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if cleared := readSettingsAlarms(t, l.cfg.Store); strings.Contains(cleared, alarms.SettingsDrift) {
		t.Fatalf("expected settings-drift alarm to clear after drain restart, got:\n%s", cleared)
	}

	// Landing changes restart-only [trunk] branch; running trunk stays "main" and alarm names trunk.branch.
	tip2 := commitSettingsFile(t, l.cfg.Root, git,
		"[trunk]\nbranch = \"dev\"\n",
		"change trunk branch")
	l.afterLanding(ctx, tip2)

	if got := l.trunkRef(); got != settings.DefaultTrunkBranch {
		t.Fatalf("expected running trunk branch to stay %q, got %q", settings.DefaultTrunkBranch, got)
	}
	whyOut2 := readSettingsAlarms(t, l.cfg.Store)
	for _, want := range []string{alarms.SettingsDrift, "trunk.branch", `running "main", file "dev"`} {
		if !strings.Contains(whyOut2, want) {
			t.Fatalf("expected settings-drift alarm naming %q, got:\n%s", want, whyOut2)
		}
	}

	// A later landing restoring matching settings clears the alarm.
	tip3 := commitSettingsFile(t, l.cfg.Root, git,
		"[trunk]\nbranch = \"main\"\n",
		"restore trunk branch")
	l.afterLanding(ctx, tip3)

	if cleared := readSettingsAlarms(t, l.cfg.Store); strings.Contains(cleared, alarms.SettingsDrift) {
		t.Fatalf("expected settings-drift alarm to clear after matching landing, got:\n%s", cleared)
	}
}

func TestLandedInvalidSettingsKeepsRunningValuesAndRaisesAlarm(t *testing.T) {
	ctx := context.Background()
	l, git, _ := actionFixture(t)

	l.cfg.PostLanding = settings.Settings{
		Trunk: settings.Trunk{Branch: settings.DefaultTrunkBranch},
		Roll:  settings.Roll{Command: "./keep-me.sh", Paths: []string{"tools/"}},
	}

	badTip := commitSettingsFile(t, l.cfg.Root, git,
		"[unknown_table]\nfoo = 1\n",
		"land invalid settings")
	l.afterLanding(ctx, badTip)

	if got := l.postLandingSettings().Roll.Command; got != "./keep-me.sh" {
		t.Fatalf("expected running roll command to remain %q after invalid .tillr.toml, got %q", "./keep-me.sh", got)
	}
	whyOut := readSettingsAlarms(t, l.cfg.Store)
	for _, want := range []string{alarms.SettingsDrift, "unknown_table"} {
		if !strings.Contains(whyOut, want) {
			t.Fatalf("expected settings-drift alarm naming %q, got:\n%s", want, whyOut)
		}
	}

	goodTip := commitSettingsFile(t, l.cfg.Root, git,
		"[roll]\ncommand = \"./new-roll.sh\"\npaths = [\"tools/\"]\n",
		"fix settings")
	l.afterLanding(ctx, goodTip)

	if got := l.postLandingSettings().Roll.Command; got != "./new-roll.sh" {
		t.Fatalf("expected roll command to update to %q after valid .tillr.toml, got %q", "./new-roll.sh", got)
	}
	if cleared := readSettingsAlarms(t, l.cfg.Store); strings.Contains(cleared, alarms.SettingsDrift) {
		t.Fatalf("expected settings-drift alarm to clear after valid landing, got:\n%s", cleared)
	}
}

func TestUnrelatedLandingDoesNotOverwriteInMemorySettings(t *testing.T) {
	ctx := context.Background()
	l, _, commit := actionFixture(t)

	l.cfg.PostLanding = settings.Settings{
		Trunk: settings.Trunk{Branch: settings.DefaultTrunkBranch},
		Roll:  settings.Roll{Command: "./configured.sh", Paths: []string{"other/"}},
	}

	tip := commit("README.md")
	l.afterLanding(ctx, tip)

	if got := l.postLandingSettings().Roll.Command; got != "./configured.sh" {
		t.Fatalf("expected in-memory roll command to stay %q when .tillr.toml did not change, got %q", "./configured.sh", got)
	}
}

func TestLoopSurvivesLandingWithUnknownSettingsTable(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "held-item")
	l.cfg.ExitWhenDrained = false
	l.cfg.StorePollInterval = time.Millisecond
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked)
		return err
	})

	git := func(args ...string) string {
		t.Helper()
		out, err := actionCommand(ctx, f.root, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}

	marker := filepath.Join(t.TempDir(), "rolled.txt")
	script := filepath.Join(f.root, "roll.sh")
	body := "#!/bin/sh\necho rolled >> " + marker + "\n"
	if err := writeForkSafe(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	commitSettingsFile(t, f.root, git,
		"[roll]\ncommand = \"./roll.sh\"\npaths = [\"tools/\"]\n",
		"initial valid settings")

	initial, err := settings.Read(f.root)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding = initial

	var landTip string
	l.cfg.Steps = &settingsReloadSyncSteps{
		recordingSteps: &recordingSteps{},
		root:           f.root,
		land: func(_ context.Context, _ Candidate) (LandOutcome, error) {
			return LandOutcome{Result: LandLanded, TrunkAfter: landTip}, nil
		},
	}

	var logBuf bytes.Buffer
	l.cfg.Report = &logBuf
	var result LoopResult
	if stop, err := l.pass(ctx, &result); err != nil || stop != "" {
		t.Fatalf("initial pass stopped: %q / %v\nlog:\n%s", stop, err, logBuf.String())
	}

	// Land a commit that adds both code under tools/ and a new .tillr.toml table
	// that the running build does not know.
	if err := os.MkdirAll(filepath.Join(f.root, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "tools", "future.go"), []byte("package tools\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, ".tillr.toml"), []byte("[roll]\ncommand = \"./roll.sh\"\npaths = [\"tools/\"]\n[future_table]\noption = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "--", ".tillr.toml", "tools/future.go")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "add future_table setting")
	landTip = git("rev-parse", "HEAD")

	if _, err := l.landWithActions(ctx, Candidate{ItemID: "item-add-table", RunID: "run-add-table"}); err != nil {
		t.Fatalf("landWithActions failed: %v", err)
	}
	waitAction(t, &l.rollAction)

	// The post-landing roll still ran with the kept running settings.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected post-landing roll to run with kept settings, marker missing: %v", err)
	}
	if got := l.postLandingSettings().Roll.Command; got != "./roll.sh" {
		t.Fatalf("expected running roll command to remain %q, got %q", "./roll.sh", got)
	}

	// Subsequent loop passes must keep running rather than exiting on the unread table.
	for i := 0; i < 2; i++ {
		stop, err := l.pass(ctx, &result)
		if err != nil || stop != "" {
			t.Fatalf("pass %d after unreadable .tillr.toml landing exited: stop=%q err=%v\nlog:\n%s", i+1, stop, err, logBuf.String())
		}
	}

	// Helpers consulted during a live pass must also use the kept running settings.
	if _, err := ReadHeavyStatusForBranch(ctx, f.root, l.cfg.Store, l.trunkRef()); err != nil {
		t.Fatalf("ReadHeavyStatusForBranch failed after unreadable .tillr.toml landing: %v", err)
	}
	if _, _, err := l.gateCheckInventory(ctx); err != nil {
		t.Fatalf("gateCheckInventory failed after unreadable .tillr.toml landing: %v", err)
	}
	if _, err := l.unformattedRepoFiles(ctx, f.root); err != nil {
		t.Fatalf("unformattedRepoFiles failed after unreadable .tillr.toml landing: %v", err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return l.pauseQuotaPeers(tx, "fixture", "run-refusing")
	})

	// Exactly one settings-drift alarm is raised, naming .tillr.toml and future_table.
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	var ans WhyAnswer
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		ans, err = WhyForItem(r, Pass{Now: time.Now().UTC().Format(time.RFC3339), StorePath: l.cfg.Store}, nil, nil, "")
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	var driftAlarms []alarms.Alarm
	for _, a := range ans.Alarms {
		if a.Kind == alarms.SettingsDrift {
			driftAlarms = append(driftAlarms, a)
		}
	}
	if len(driftAlarms) != 1 {
		t.Fatalf("expected 1 %s alarm, got %d: %+v", alarms.SettingsDrift, len(driftAlarms), driftAlarms)
	}
	for _, want := range []string{".tillr.toml", "future_table"} {
		if !strings.Contains(driftAlarms[0].Cause, want) {
			t.Fatalf("alarm cause %q missing %q", driftAlarms[0].Cause, want)
		}
	}

	// After the loop rolls to a build that reads the new settings, the alarm clears.
	if err := restart.PublishBuild(ctx, l.cfg.Store, restart.BuildFact{
		Role:     restart.RoleDrain,
		Revision: landTip,
		Digest:   "sha256:rolled",
		PID:      2,
		At:       time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if cleared := readSettingsAlarms(t, l.cfg.Store); strings.Contains(cleared, alarms.SettingsDrift) {
		t.Fatalf("expected settings-drift alarm to clear after roll, got:\n%s", cleared)
	}
}

func TestLandedWorktreesBoundsReloadWithoutRestart(t *testing.T) {
	ctx := context.Background()
	l, git, _ := actionFixture(t)

	initial, err := settings.Read(l.cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding = initial

	tip := commitSettingsFile(t, l.cfg.Root, git,
		"[worktrees]\ndisk_budget_bytes = 21474836480\nmax_count = 16\n",
		"configure worktrees bounds")

	l.afterLanding(ctx, tip)

	got := l.postLandingSettings().Worktrees
	if !got.DiskBudgetSet || got.DiskBudgetBytes != 21474836480 {
		t.Fatalf("expected reloaded disk_budget_bytes = 21474836480, got %+v", got)
	}
	if !got.MaxCountSet || got.MaxCount != 16 {
		t.Fatalf("expected reloaded max_count = 16, got %+v", got)
	}
	if whyOut := readSettingsAlarms(t, l.cfg.Store); strings.Contains(whyOut, alarms.SettingsDrift) {
		t.Fatalf("unexpected settings-drift alarm after reloadable [worktrees] change:\n%s", whyOut)
	}
}
