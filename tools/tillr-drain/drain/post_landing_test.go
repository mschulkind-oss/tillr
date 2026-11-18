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
	l := &Loop{cfg: LoopConfig{Root: root, Store: filepath.Join(t.TempDir(), "store.db")}}
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
	l.cfg.PostLanding.Push.AfterLanding = true
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
