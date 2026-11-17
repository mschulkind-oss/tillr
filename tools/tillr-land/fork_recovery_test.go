package main_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

func TestForkAdvanceWhileWaitingForTheLockCannotBeOverwritten(t *testing.T) {
	f := newContentionFixture(t)
	git(t, f.f.dir, "branch", "patch/pi", f.f.baseSHA)
	cache := filepath.Join(f.f.dir, ".forks/pi")
	git(t, f.f.dir, "clone", "--quiet", "--no-checkout", f.f.dir, cache)
	git(t, cache, "checkout", "-b", "main", f.f.baseSHA)
	f.req.BaseRef = "refs/heads/patch/pi"
	f.req.Verdict.Checks = []land.VerdictCheck{{CheckID: "fork-build", Result: "pass"}}
	f.req.LockPath = filepath.Join(scratch(t, "fork-lock"), "land.lock")
	f.req.LockWait = 10 * time.Second
	lock, err := land.OpenLock(f.req.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Acquire(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	entered, release := stallGit(t, "merge-base")
	done := startLanding(t, context.Background(), f)
	awaitGit(t, entered) // the initial ancestor check already captured the old patch tip
	write(t, filepath.Join(f.f.dir, "external.txt"), "external patch delivery\n")
	git(t, f.f.dir, "add", "external.txt")
	git(t, f.f.dir, "commit", "-m", "external delivery")
	external := git(t, f.f.dir, "rev-parse", "HEAD")
	git(t, f.f.dir, "update-ref", "refs/heads/patch/pi", external)
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	lock.Release()
	answer := finishLanding(t, done)
	if answer.err != nil || answer.landing.Reason != "patch-rewind" {
		t.Fatalf("locked patch advance: %+v error=%v", answer.landing, answer.err)
	}
	if tip := git(t, f.f.dir, "rev-parse", "patch/pi"); tip != external {
		t.Fatalf("external delivery %s overwritten by %s", external, tip)
	}
}

func TestForkReceiptRecoveryKeepsTheLaterDeliveryInTheCache(t *testing.T) {
	for _, behind := range []bool{false, true} {
		t.Run(fmt.Sprintf("cache-behind-%v", behind), func(t *testing.T) {
			f := newFixture(t)
			git(t, f.dir, "checkout", "candidate")
			write(t, filepath.Join(f.dir, "second.txt"), "later delivery\n")
			git(t, f.dir, "add", "second.txt")
			git(t, f.dir, "commit", "-m", "later fork delivery")
			second := git(t, f.dir, "rev-parse", "HEAD")
			git(t, f.dir, "checkout", f.ref)
			git(t, f.dir, "branch", "patch/pi", second)
			git(t, f.dir, "update-ref", "refs/tillr/landed/run-old", f.headSHA)
			cache := filepath.Join(f.dir, ".forks/pi")
			git(t, f.dir, "clone", "--quiet", "--no-checkout", f.dir, cache)
			cacheTip := second
			if behind {
				cacheTip = f.baseSHA
			}
			git(t, cache, "checkout", "-b", "main", cacheTip)
			_, code, out := f.land(t, "run-old", "pass", "--base-ref", "patch/pi")
			if code != 0 || decodeLanding(t, out).Payload.TrunkAfter != f.headSHA {
				t.Fatalf("old receipt recovery exited %d: %s", code, out)
			}
			if tip := git(t, cache, "rev-parse", "main"); tip != second {
				t.Fatalf("old recovery rewound or left cache at %s want %s", tip, second)
			}
			if tip := git(t, cache, "rev-parse", "patch/pi"); tip != second {
				t.Fatalf("cache delivery at %s want %s", tip, second)
			}
		})
	}
}

func TestForkCachePreparationFailureIsAtomicAndRetryable(t *testing.T) {
	f := newFixture(t)
	git(t, f.dir, "branch", "patch/pi", f.baseSHA)
	source := filepath.Join(scratch(t, "gated-fork"), "checkout")
	git(t, f.dir, "clone", "--quiet", "--branch", "candidate", f.dir, source)
	write(t, filepath.Join(f.dir, ".tillr/loop/run-cache/fork-cache-source"), source+"\n")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	shim := scratch(t, "interrupted-cache")
	write(t, filepath.Join(shim, "git"), "#!/bin/sh\nfor arg do\n if [ \"$arg\" = checkout ]; then echo 'interrupted cache preparation' >&2; exit 1; fi\ndone\nexec '"+realGit+"' \"$@\"\n")
	if err := os.Chmod(filepath.Join(shim, "git"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+originalPath)
	db := filepath.Join(scratch(t, "atomic-cache-store"), "state.db")
	code, out, stderr := passBoth(t, f.passArgs(t, db, "item_042", "run-cache", "--base-ref", "patch/pi"))
	if code != 4 || !strings.Contains(stderr, "interrupted cache preparation") {
		t.Fatalf("interrupted cache exited %d: %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".forks/pi")); !os.IsNotExist(err) {
		t.Fatalf("partial cache was published: %v", err)
	}
	if tip := git(t, f.dir, "rev-parse", "patch/pi"); tip != f.baseSHA {
		t.Fatalf("incomplete cache moved delivery to %s", tip)
	}
	t.Setenv("PATH", originalPath)
	code, out, stderr = passBoth(t, f.passArgs(t, db, "item_042", "run-cache", "--base-ref", "patch/pi"))
	if code != 0 {
		t.Fatalf("cache recovery exited %d: %s", code, out)
	}
	if tip := git(t, filepath.Join(f.dir, ".forks/pi"), "rev-parse", "main"); tip != f.headSHA {
		t.Fatalf("recovered cache at %s", tip)
	}
}
