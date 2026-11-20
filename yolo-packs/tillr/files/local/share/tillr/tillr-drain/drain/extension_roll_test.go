package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func lastExtRollDetail(t *testing.T, l *Loop, object string) (string, bool) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var detail string
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT json_extract(payload,'$.detail') FROM events WHERE kind='drain.post-landing-action' AND json_extract(payload,'$.action')='roll' AND json_extract(payload,'$.what')='pi-extensions' AND object_id=? ORDER BY seq DESC LIMIT 1`,
			object,
		).Scan(&detail)
	})
	return detail, err == nil
}

func restartRoles(t *testing.T, l *Loop) map[string]bool {
	t.Helper()
	s, err := store.OpenRead(context.Background(), l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]bool{}
	for _, e := range events {
		if e.Kind != restart.RequestKind {
			continue
		}
		var req restart.Request
		if err := json.Unmarshal(e.Payload, &req); err != nil {
			t.Fatal(err)
		}
		roles[req.Role] = true
	}
	return roles
}

func TestExtensionRollHoldRule(t *testing.T) {
	if ReasonPiExtensionsBuildFailed != "pi-extensions-build-failed" {
		t.Fatalf("ReasonPiExtensionsBuildFailed = %q", ReasonPiExtensionsBuildFailed)
	}
	rule := holdrules.For(ReasonPiExtensionsBuildFailed)
	if rule == holdrules.Unknown {
		t.Fatal("pi-extensions-build-failed has no hold rule")
	}
	if rule.Class != "waits on work" || rule.Where != "roll" || rule.Mover == "" {
		t.Fatalf("unexpected hold rule: %+v", rule)
	}
}

func TestExtensionRollTrunkPaths(t *testing.T) {
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	l.cfg.PostLanding.Roll = settings.Roll{
		Paths:   []string{"tools/tillr*/"},
		Command: `echo "cmd" >> calls; echo "$0 $@" > last_args`,
	}
	// Note: sh -c "<Command> --pi-extensions" passes --pi-extensions as part of the command string,
	// so test with a helper script that records its argv.
	script := filepath.Join(l.cfg.Root, "roll-helper.sh")
	if err := writeForkSafe(script, []byte("#!/bin/sh\necho \"$*\" >> calls\n"), 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding.Roll.Command = script + " --no-request"

	l.afterLanding(ctx, commit("docs/readme.md"))
	if l.rollAction.busy() || l.extRollAction.busy() {
		t.Fatal("unrelated path started a roll")
	}

	extPaths := []string{
		"yolo-packs/matt/files/extensions/bedrock-context.ts",
		"yolo-packs/matt/patches/pi-subagents/0001.patch",
		"yolo-packs/matt/pack.json",
		"bin/pi-forks.list",
	}
	for _, p := range extPaths {
		tip := commit(p)
		l.afterLanding(ctx, tip)
		waitAction(t, func() bool { return !l.extRollAction.busy() && !l.piRollAction.busy() })
		if l.rollAction.busy() {
			t.Fatalf("extension path %s triggered rollAction", p)
		}
		detail, ok := lastExtRollDetail(t, l, tip)
		if !ok || detail != "" {
			t.Fatalf("%s: detail=%q ok=%v", p, detail, ok)
		}
	}
	if roles := restartRoles(t, l); len(roles) != 0 {
		t.Fatalf("extension roll filed restart requests: %v", roles)
	}
	calls, err := os.ReadFile(filepath.Join(l.cfg.Root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	gotCalls := string(calls)
	lines := strings.Split(strings.TrimSpace(gotCalls), "\n")
	if strings.Count(gotCalls, "--no-request --pi-extensions\n") != len(extPaths) ||
		strings.Count(gotCalls, "--no-request --pi\n") != 1 ||
		len(lines) != len(extPaths)+1 {
		t.Fatalf("calls = %q, want %d --pi-extensions and 1 --pi", gotCalls, len(extPaths))
	}
}

func TestExtensionRollAlongsideToolRoll(t *testing.T) {
	l, git, commit := actionFixture(t)
	ctx := context.Background()
	script := filepath.Join(l.cfg.Root, "roll-helper.sh")
	if err := writeForkSafe(script, []byte("#!/bin/sh\necho \"$*\" >> calls\n"), 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding.Roll = settings.Roll{
		Paths:   []string{"tools/tillr*/"},
		Command: script + " --no-request",
	}

	// Stage a single commit touching both tools/tillr-drain/ and yolo-packs/matt/pack.json.
	for _, p := range []string{"tools/tillr-drain/x.go", "yolo-packs/matt/pack.json"} {
		full := filepath.Join(l.cfg.Root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(p), 0644); err != nil {
			t.Fatal(err)
		}
		git("add", "--", p)
	}
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "both")
	tip := git("rev-parse", "HEAD")
	_ = commit // keep helper usage consistent

	l.afterLanding(ctx, tip)
	waitAction(t, func() bool { return !l.rollAction.busy() && !l.extRollAction.busy() })

	calls, err := os.ReadFile(filepath.Join(l.cfg.Root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(calls)
	if !strings.Contains(got, "--no-request\n") || !strings.Contains(got, "--no-request --pi-extensions\n") {
		t.Fatalf("expected both tool roll and extension roll, got %q", got)
	}
	if roles := restartRoles(t, l); !roles[restart.RoleDrain] || !roles[restart.RoleView] {
		t.Fatalf("expected tool roll to file drain/view restarts, got %v", roles)
	}
}

type extRollSteps struct{ Steps }

func (extRollSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	return LandOutcome{Result: LandLanded, TrunkAfter: c.HeadSHA}, nil
}

func TestExtensionRollPatchBranch(t *testing.T) {
	l, git, commit := actionFixture(t)
	ctx := context.Background()
	script := filepath.Join(l.cfg.Root, "roll-helper.sh")
	if err := writeForkSafe(script, []byte("#!/bin/sh\necho \"$*\" >> calls\n"), 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding.Roll = settings.Roll{
		Paths:   []string{"tools/tillr*/"},
		Command: script + " --no-request",
	}

	// Create an unrelated commit whose diff does not match extension paths, and
	// verify that a patch/pi-subagents landing triggers extRollAction without
	// inspecting trunk diff-tree or filing restarts.
	unrelated := commit("unrelated/file")
	git("update-ref", "refs/heads/patch/pi-subagents", unrelated)
	git("update-ref", "refs/heads/patch/pi", unrelated)

	l.cfg.Steps = extRollSteps{}

	// refs/heads/patch/pi must trigger piRollAction (--pi), NOT extRollAction.
	if _, err := l.landWithActions(ctx, Candidate{BaseRef: "refs/heads/patch/pi", HeadSHA: unrelated}); err != nil {
		t.Fatal(err)
	}
	if l.extRollAction.busy() {
		t.Fatal("patch/pi triggered extRollAction")
	}
	waitAction(t, func() bool { return !l.piRollAction.busy() })

	// refs/heads/patch/pi-subagents MUST trigger extension roll.
	if _, err := l.landWithActions(ctx, Candidate{BaseRef: "refs/heads/patch/pi-subagents", HeadSHA: unrelated}); err != nil {
		t.Fatal(err)
	}
	waitAction(t, func() bool { return !l.extRollAction.busy() })

	if roles := restartRoles(t, l); len(roles) != 0 {
		t.Fatalf("patch-branch extension roll filed restart requests: %v", roles)
	}
	calls, err := os.ReadFile(filepath.Join(l.cfg.Root, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "--no-request --pi\n--no-request --pi-extensions\n" {
		t.Fatalf("calls = %q", string(calls))
	}
}

func TestExtensionRollCoalescing(t *testing.T) {
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	script := filepath.Join(l.cfg.Root, "roll-helper.sh")
	if err := writeForkSafe(script, []byte("#!/bin/sh\necho run >> ext_runs\nwhile [ ! -f release ]; do sleep .01; done\n"), 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding.Roll = settings.Roll{Command: script}

	l.afterLanding(ctx, commit("yolo-packs/matt/files/extensions/a.ts"))
	runsFile := filepath.Join(l.cfg.Root, "ext_runs")
	waitAction(t, func() bool { b, _ := os.ReadFile(runsFile); return string(b) == "run\n" })

	for _, name := range []string{"b.ts", "c.ts", "d.ts"} {
		l.afterLanding(ctx, commit("yolo-packs/matt/files/extensions/"+name))
	}
	if err := os.WriteFile(filepath.Join(l.cfg.Root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitAction(t, func() bool { return !l.extRollAction.busy() })
	if b, err := os.ReadFile(runsFile); err != nil || string(b) != "run\nrun\n" {
		t.Fatalf("ext_runs = %q, %v", b, err)
	}
}

func TestExtensionRollFailureAndClear(t *testing.T) {
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	script := filepath.Join(l.cfg.Root, "roll-helper.sh")
	if err := writeForkSafe(script, []byte("#!/bin/sh\nif [ -f fail ]; then echo 'build boom' >&2; exit 7; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding.Roll = settings.Roll{Command: script}
	if err := os.WriteFile(filepath.Join(l.cfg.Root, "fail"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	failedTip := commit("yolo-packs/matt/files/extensions/broken.ts")
	l.afterLanding(ctx, failedTip)
	waitAction(t, func() bool { return !l.extRollAction.busy() })

	mover := holdrules.For(ReasonPiExtensionsBuildFailed).Mover
	detail, ok := lastExtRollDetail(t, l, failedTip)
	if !ok || !strings.Contains(detail, ReasonPiExtensionsBuildFailed) || !strings.Contains(detail, mover) || !strings.Contains(detail, "build boom") {
		t.Fatalf("failure detail = %q, ok=%v", detail, ok)
	}
	logOut := l.reported()
	if !strings.Contains(logOut, ReasonPiExtensionsBuildFailed) || !strings.Contains(logOut, mover) {
		t.Fatalf("report log missing reason or mover: %q", logOut)
	}

	if err := os.Remove(filepath.Join(l.cfg.Root, "fail")); err != nil {
		t.Fatal(err)
	}
	okTip := commit("yolo-packs/matt/files/extensions/fixed.ts")
	l.afterLanding(ctx, okTip)
	waitAction(t, func() bool { return !l.extRollAction.busy() })

	detail, ok = lastExtRollDetail(t, l, okTip)
	if !ok || detail != "" {
		t.Fatalf("cleared detail = %q, ok=%v", detail, ok)
	}
}

func TestExtensionRollBlocksRestart(t *testing.T) {
	l, _, _ := actionFixture(t)
	l.cfg.Restart = &RestartConfig{}
	ctx := context.Background()
	if _, err := restart.File(ctx, l.cfg.Store, restart.RoleDrain, time.Now()); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		waitAction(t, func() bool { return !l.extRollAction.busy() })
	}()
	l.extRollAction.submit("tip", func(string) { <-release })

	if pending, err := l.buildRestartPending(ctx); err != nil || pending {
		t.Fatalf("buildRestartPending while extRollAction busy = %v, %v", pending, err)
	}
	var res LoopResult
	stopped, reason, err := l.gateRestart(ctx, &res)
	if err != nil || stopped || reason != "" {
		t.Fatalf("gateRestart while extRollAction busy = %v, %q, %v", stopped, reason, err)
	}

	close(release)
	waitAction(t, func() bool { return !l.extRollAction.busy() })
	if pending, err := l.buildRestartPending(ctx); err != nil || !pending {
		t.Fatalf("buildRestartPending after extRollAction idle = %v, %v", pending, err)
	}
}
