package drain

import (
	"context"
	"encoding/json"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func actionFixture(t *testing.T) (*Loop, func(...string) string, func(string) string) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := actionCommand(context.Background(), root, "git", args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	git("init", "-b", TrunkRef)
	git("config", "core.hooksPath", filepath.Join(root, ".git", "hooks"))
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "initial")
	commit := func(name string) string {
		t.Helper()
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(name), 0644); err != nil {
			t.Fatal(err)
		}
		git("add", "--", name)
		git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", name)
		return git("rev-parse", "HEAD")
	}
	l := &Loop{cfg: LoopConfig{Root: root, Store: filepath.Join(t.TempDir(), "store.db"), Report: &strings.Builder{}}}
	s, err := store.Open(context.Background(), l.cfg.Store, true)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return l, git, commit
}

func waitAction(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for post-landing action")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPostLandingRoll(t *testing.T) {
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	l.cfg.PostLanding.Roll = settings.Roll{Paths: []string{"tools/tillr*/"}, Command: `echo run >> runs; while [ ! -f release ]; do sleep .01; done`}
	l.afterLanding(ctx, commit("docs/no-roll"))
	if l.rollAction.busy() {
		t.Fatal("unmatched landing started a roll")
	}
	l.afterLanding(ctx, commit("tools/tillr-one/first"))
	runs := filepath.Join(l.cfg.Root, "runs")
	waitAction(t, func() bool { b, _ := os.ReadFile(runs); return string(b) == "run\n" })
	for _, name := range []string{"second", "third", "fourth"} {
		l.afterLanding(ctx, commit("tools/tillr-one/"+name))
	}
	if err := os.WriteFile(filepath.Join(l.cfg.Root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitAction(t, func() bool { return !l.rollAction.busy() })
	if b, err := os.ReadFile(runs); err != nil || string(b) != "run\nrun\n" {
		t.Fatalf("runs = %q, %v", b, err)
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(ctx, 0)
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
	if !roles[restart.RoleDrain] || !roles[restart.RoleView] {
		t.Fatalf("restart roles = %v", roles)
	}
}

func TestPostLandingFailureAndDisabledDefaults(t *testing.T) {
	l, _, commit := actionFixture(t)
	ctx := context.Background()
	cfg, err := settings.Read(l.cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.PostLanding = cfg
	for i := range 10 {
		l.afterLanding(ctx, commit("default-"+string(rune('a'+i))))
	}
	if l.rollAction.busy() || l.pushAction.busy() {
		t.Fatal("missing settings enabled an action")
	}
	l.cfg.PostLanding.Roll = settings.Roll{Paths: []string{"src"}, Command: `echo discard; seq 1 20; exit 7`}
	tip := commit("src/change")
	l.afterLanding(ctx, tip)
	waitAction(t, func() bool { return !l.rollAction.busy() })
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var detail string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events WHERE kind='drain.post-landing-action' AND json_extract(payload,'$.action')='roll' AND object_id=?`, tip).Scan(&detail)
	})
	if err != nil || strings.Contains(detail, "discard") || !strings.Contains(detail, "exit status 7: 1\n2\n") || !strings.HasSuffix(detail, "\n20") {
		t.Fatalf("failure = %q, %v", detail, err)
	}
}

func TestPostLandingPushDoesNotBlockRestartAdmission(t *testing.T) {
	l, _, _ := actionFixture(t)
	l.cfg.Restart = &RestartConfig{}
	ctx := context.Background()
	if _, err := restart.File(ctx, l.cfg.Store, restart.RoleDrain, time.Now()); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer func() { close(release); waitAction(t, func() bool { return !l.pushAction.busy() }) }()
	l.pushAction.submit("tip", func(string) { <-release })
	_, unlock, err := l.lockRestartMechanical(ctx)
	if err != nil {
		t.Fatalf("push prevented another pass from admitting a landing: %v", err)
	}
	unlock()
}

func TestPostLandingPush(t *testing.T) {
	l, git, commit := actionFixture(t)
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", TrunkRef)
	hook := filepath.Join(l.cfg.Root, ".git", "hooks", "pre-push")
	script := `#!/bin/sh
 echo hook >> pushes
 if [ ! -f allow ]; then echo 'fixture rejected push' >&2; exit 1; fi
 while [ ! -f release ]; do sleep .01; done
 `
	if err := os.WriteFile(hook, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l.cfg.PostLanding.Push.What = settings.PushTip
	failed := commit("failed")
	l.afterLanding(ctx, failed)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var detail string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events WHERE kind='drain.post-landing-action' AND json_extract(payload,'$.action')='push' AND object_id=?`, failed).Scan(&detail)
	})
	if err != nil || !strings.Contains(detail, "fixture rejected push") {
		t.Fatalf("failure = %q, %v", detail, err)
	}
	touch := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(l.cfg.Root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	touch("allow")
	l.cfg.PostLanding.Push.Remote = "origin"
	l.cfg.PostLanding.Push.Ref = "refs/heads/published"
	l.afterLanding(ctx, commit("retry"))
	pushes := func() int {
		b, _ := os.ReadFile(filepath.Join(l.cfg.Root, "pushes"))
		return strings.Count(string(b), "hook\n")
	}
	waitAction(t, func() bool { return pushes() == 2 })
	var tip string
	for _, name := range []string{"arrival-a", "arrival-b", "arrival-c"} {
		tip = commit(name)
		l.afterLanding(ctx, tip)
	}
	touch("release")
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if pushes() != 3 || git("--git-dir="+remote, "rev-parse", "refs/heads/published") != tip {
		t.Fatal("pending landings were not covered by exactly one follow-up push")
	}
	// Rewind only the fixture's local trunk and land a divergent commit. A
	// forced push would overwrite the published tip; an ordinary push refuses.
	git("reset", "--hard", failed)
	diverged := commit("diverged")
	l.afterLanding(ctx, diverged)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if git("rev-parse", "HEAD") != diverged || git("--git-dir="+remote, "rev-parse", "refs/heads/published") != tip {
		t.Fatal("rejected push moved either trunk")
	}
}

func (l *Loop) reported() string {
	l.reportMu.Lock()
	defer l.reportMu.Unlock()
	return l.cfg.Report.(*strings.Builder).String()
}

func lastPushDetail(t *testing.T, l *Loop, object string) (string, bool) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), l.cfg.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var detail string
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events WHERE kind='drain.post-landing-action' AND json_extract(payload,'$.action')='push' AND object_id=? ORDER BY seq DESC LIMIT 1`, object).Scan(&detail)
	})
	return detail, err == nil
}

// In green mode a landing pushes nothing; the green pointer's advance pushes the
// proven commit, never the newer tip, and a refused push is retried at the next advance.
func TestPostLandingPushGreen(t *testing.T) {
	l, git, commit := actionFixture(t)
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", TrunkRef)
	hook := filepath.Join(l.cfg.Root, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho hook >> pushes\nif [ -f refuse ]; then echo 'fixture rejected push' >&2; exit 1; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	l.cfg.PostLanding.Push.What = settings.PushGreen
	remoteTip := func() string { return git("--git-dir="+remote, "rev-parse", "refs/heads/"+TrunkRef) }
	initial := remoteTip()
	pushes := func() int {
		b, _ := os.ReadFile(filepath.Join(l.cfg.Root, "pushes"))
		return strings.Count(string(b), "hook\n")
	}

	// Landings alone push nothing, however many.
	proved := commit("proved")
	l.afterLanding(ctx, proved)
	unproved := commit("unproved")
	l.afterLanding(ctx, unproved)
	if l.pushAction.busy() || pushes() != 0 || remoteTip() != initial {
		t.Fatal("a landing pushed in green mode")
	}

	// The pointer advances to the first commit while the tip is one commit past it.
	git("update-ref", GreenRef, proved)
	l.afterGreen(ctx, proved)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if remoteTip() != proved {
		t.Fatalf("remote = %s, want the proved commit %s (tip %s)", remoteTip(), proved, unproved)
	}
	if d, ok := lastPushDetail(t, l, proved); !ok || d != "" {
		t.Fatalf("success recorded %q, %v", d, ok)
	}
	if !strings.Contains(l.reported(), "loop: pushed green "+proved[:12]) {
		t.Fatalf("loop log lacks the push line: %q", l.reported())
	}

	// Already on the remote: the condition "remote is behind green" is false, so no push.
	before := pushes()
	l.afterGreen(ctx, proved)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if pushes() != before {
		t.Fatal("pushed a green commit the remote already holds")
	}

	// A refused push is reported and holds nothing; the next advance recomputes and retries.
	second := unproved
	git("update-ref", GreenRef, second)
	if err := os.WriteFile(filepath.Join(l.cfg.Root, "refuse"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	l.afterGreen(ctx, second)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if d, _ := lastPushDetail(t, l, second); !strings.Contains(d, "fixture rejected push") || remoteTip() != proved {
		t.Fatalf("refusal detail = %q, remote %s", d, remoteTip())
	}
	if !strings.Contains(l.reported(), "loop: push of green "+second[:12]+" failed") || !strings.Contains(l.reported(), "retried at the next green advance") {
		t.Fatalf("loop log lacks the failure line: %q", l.reported())
	}
	if err := os.Remove(filepath.Join(l.cfg.Root, "refuse")); err != nil {
		t.Fatal(err)
	}
	third := commit("third")
	git("update-ref", GreenRef, third)
	l.afterGreen(ctx, third)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if remoteTip() != third {
		t.Fatalf("retry did not publish the new green: remote %s want %s", remoteTip(), third)
	}
}

// A green commit that would not fast-forward the remote is refused by git and is
// never forced.
func TestPostLandingPushGreenNeverForces(t *testing.T) {
	l, git, commit := actionFixture(t)
	remote := filepath.Join(t.TempDir(), "upstream.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", TrunkRef)
	base := git("rev-parse", "HEAD")
	ahead := commit("remote-only")
	git("push", "origin", TrunkRef)
	git("reset", "--hard", base)
	green := commit("diverged-green")
	git("update-ref", GreenRef, green)
	l.cfg.PostLanding.Push.What = settings.PushGreen
	l.afterGreen(context.Background(), green)
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if got := git("--git-dir="+remote, "rev-parse", "refs/heads/"+TrunkRef); got != ahead {
		t.Fatalf("remote moved to %s", got)
	}
	if d, _ := lastPushDetail(t, l, green); d == "" {
		t.Fatal("refused push was not recorded")
	}
}

// Without a green pointer there is nothing proven to push; the other modes ignore the advance.
func TestAfterGreenNeedsGreenMode(t *testing.T) {
	l, git, commit := actionFixture(t)
	git("remote", "add", "origin", t.TempDir())
	for _, what := range []string{settings.PushNone, settings.PushTip} {
		l.cfg.PostLanding.Push.What = what
		l.afterGreen(context.Background(), commit("x-"+what))
		if l.pushAction.busy() {
			t.Fatalf("what=%s pushed on a green advance", what)
		}
	}
	l.cfg.PostLanding.Push.What = settings.PushGreen
	l.afterGreen(context.Background(), git("rev-parse", "HEAD"))
	waitAction(t, func() bool { return !l.pushAction.busy() })
	if d, ok := lastPushDetail(t, l, git("rev-parse", "HEAD")); ok && d != "" {
		t.Fatalf("no pointer must not be a failure: %q", d)
	}
}
