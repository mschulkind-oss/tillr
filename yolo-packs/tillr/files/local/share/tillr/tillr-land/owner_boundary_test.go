package main_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
)

// Git's own add, commit and checkout must fail on the lander's index lock all the way through
// the ref update and a rollback, so a rollback never overwrites what the owner just did.
func TestOwnerGitCommandsAreRefusedByTheLockThroughPublicationAndRollback(t *testing.T) {
	for _, command := range [][]string{{"add", "owner.txt"}, {"checkout", "-q", "-b", "elsewhere"}} {
		t.Run(command[0], func(t *testing.T) {
			resetHooks(t)
			f := newOwnerFixture(t, nil)
			d := f.f.dir
			write(t, filepath.Join(d, "owner.txt"), "owner\n")
			var ran error
			lock := filepath.Join(git(t, d, "rev-parse", "--absolute-git-dir"), "refs", "heads", "master.lock")
			t.Cleanup(func() { os.Remove(lock) })
			done := false
			land.OwnerHooks.GitCall = func(_ string, args []string) {
				for _, a := range args {
					if a == "tillr.move=final" && !done {
						done = true
						// Refuse the guarded write without changing the tip: a real Git error,
						// not stale preparation to retry.
						write(t, lock, "")
						ran = exec.Command("git", append([]string{"-C", d}, command...)...).Run()
					}
				}
			}
			var failure *land.InstallFailure
			if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); !errors.As(err, &failure) {
				t.Fatalf("expected an install failure from the guarded ref update: %v", err)
			}
			if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
				t.Errorf("recorded %d faults for the failed update, want one", n)
			}
			if ran == nil {
				t.Errorf("git %v succeeded while the lander held the checkout", command)
			}
			if _, err := os.Stat(filepath.Join(d, "new.txt")); err == nil {
				t.Error("the landed file was left after the rollback")
			}
		})
	}
}

func TestAReadFailureWithASaveDuringInspectionKeepsTheSave(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	once := false
	land.OwnerHooks.FailDisplacedRead = func(name string) error {
		if name == "a.txt" && !once {
			once = true
			atomicSave(t, filepath.Join(d, "a.txt"), "SAVE-DURING-INSPECT\n")
			return errors.New("read refused")
		}
		return nil
	}
	_, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || strings.Contains(err.Error(), "put back exactly") {
		t.Fatalf("the error is %v", err)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "SAVE-DURING-INSPECT\n" {
		t.Errorf("the save was lost: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(d, ".tillr-*"))
	kept := allSavedBytes(t, f)
	for _, p := range left {
		kept += statOf(t, p).body
	}
	if !strings.Contains(kept, "W9") {
		t.Errorf("the owner's original is kept nowhere: %q", kept)
	}
}

func TestAFailedSelfRestorationIsReportedAndKeepsTheOwnersInode(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	land.OwnerHooks.FailDisplacedRead = func(name string) error { return errors.New("read refused") }
	land.OwnerHooks.FailRestoreExchange = func(name string) error { return errors.New("EIO") }
	_, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || strings.Contains(err.Error(), "put back exactly") || !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("the error is %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(d, ".tillr-*"))
	found := false
	for _, p := range left {
		if statOf(t, p).body == ownerLines {
			found = true
		}
	}
	if !found {
		t.Errorf("the owner's file is not retained beside the path: %v", left)
	}
}

func TestAnUndoInspectionErrorIsReportedAsUnrestored(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	land.OwnerHooks.InstallFault = failNewTxt()
	land.OwnerHooks.FailUndoInspect = func(path string) error { return errors.New("EIO") }
	_, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || strings.Contains(err.Error(), "put back exactly") || !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("the error is %v", err)
	}
	if saved := allSavedBytes(t, f); !strings.Contains(saved, "W9") {
		t.Errorf("the owner's original is kept nowhere: %q", saved)
	}
}

func TestACanceledRequestStillPreservesEveryRollbackSave(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "new.txt" {
			cancel()
			return os.ErrPermission
		}
		return nil
	}
	land.OwnerHooks.BeforeRollbackSwap = func(path string, step int) {
		if path == "a.txt" && step <= 2 {
			atomicSave(t, filepath.Join(d, "a.txt"), "SAVE"+string(rune('0'+step))+"\n")
		}
	}
	land.Land(ctx, f.s, f.trunk, f.req)
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "SAVE2\n" {
		t.Errorf("the newest save is not on disk: %q", got)
	}
	if saved := allSavedBytes(t, f); !strings.Contains(saved, "SAVE1") {
		t.Errorf("the superseded save disappeared with the canceled request: %q", saved)
	}
}

func TestReconciliationErrorsAreReturnedNotSwallowed(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	fd, err := os.OpenFile(filepath.Join(d, "a.txt"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	land.OwnerHooks.BeforeLastRead = func() { fd.WriteAt([]byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nLATE8\nW9\n"), 0) }
	land.OwnerHooks.FailFaultEvent = func() error { return errors.New("event refused") }
	land.Land(context.Background(), f.s, f.trunk, f.req)
	land.OwnerHooks.BeforeLastRead = nil
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the retry hid that no fault event could be recorded")
	}
}

func TestSurvivingCopyFaultsAreRecordedOnceDurably(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	land.OwnerHooks.BeforeLastRead = func() { panic("killed after the receipt") }
	func() {
		defer func() { recover() }()
		land.Land(context.Background(), f.s, f.trunk, f.req)
	}()
	land.OwnerHooks.BeforeLastRead = nil
	mustLand(t, f)
	mustLand(t, f)
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Errorf("%d fault events for the surviving copy, want 1", n)
	}
}

func TestARelocationAtTheSwapBoundaryKeepsTheOriginalInode(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixtureWith(t, func(dir string) {
		os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
		write(t, filepath.Join(dir, "sub", "x.txt"), "base x\n")
	}, func(dir string) {
		write(t, filepath.Join(dir, "sub", "x.txt"), "land x\n")
	})
	d := f.f.dir
	once := false
	var relocated stat
	land.OwnerHooks.AfterParentCheck = func(path string) {
		if path == "sub/x.txt" && !once {
			once = true
			os.Rename(filepath.Join(d, "sub"), filepath.Join(d, "relocated"))
			relocated = statOf(t, filepath.Join(d, "relocated", "x.txt"))
			os.MkdirAll(filepath.Join(d, "sub"), 0o755)
			write(t, filepath.Join(d, "sub", "x.txt"), "owner replacement\n")
		}
	}
	if l := mustLand(t, f); l.Landed() {
		t.Fatalf("landed over a relocated parent: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "relocated", "x.txt")); got != relocated {
		t.Errorf("the relocated file's inode or mtime changed: %+v vs %+v", got, relocated)
	}
}

func TestSupersededSymlinkSavesAreKeptAsSymlinks(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixtureWith(t, func(dir string) {
		os.Symlink("a.txt", filepath.Join(dir, "lnk"))
	}, func(dir string) {
		os.Remove(filepath.Join(dir, "lnk"))
		os.Symlink("landed-target", filepath.Join(dir, "lnk"))
	})
	d := f.f.dir
	land.OwnerHooks.InstallFault = failNewTxt()
	land.OwnerHooks.BeforeRollbackSwap = func(path string, step int) {
		if path == "lnk" && step <= 2 {
			tmp := filepath.Join(d, "lnk.editor")
			os.Symlink("OWNER-LINK-"+string(rune('0'+step)), tmp)
			os.Rename(tmp, filepath.Join(d, "lnk"))
		}
	}
	land.Land(context.Background(), f.s, f.trunk, f.req)
	if target, _ := os.Readlink(filepath.Join(d, "lnk")); target != "OWNER-LINK-2" {
		t.Errorf("the newest link is %q", target)
	}
	var tree string
	for _, ref := range strings.Fields(git(t, d, "for-each-ref", "--format=%(refname)", land.RecoveryPrefix+"saved/")) {
		tree += git(t, d, "ls-tree", "-r", ref)
	}
	if !strings.Contains(tree, "120000") {
		t.Errorf("the superseded symlink was not kept as a symlink: %q", tree)
	}
}

func indexLockPath(t *testing.T, d string) string {
	t.Helper()
	return filepath.Join(d, ".git", "index.lock")
}

func ownLockRecord(t *testing.T, pid int) string {
	t.Helper()
	host, _ := os.Hostname()
	return `{"kind":"tillr-land-index-lock","pid":` + itoa(pid) + `,"host":"` + host + `","token":"stale"}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

func TestADeadLandersIndexLockIsReclaimedWithoutAHuman(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	child := exec.Command("true")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	write(t, indexLockPath(t, d), ownLockRecord(t, child.Process.Pid))
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if _, err := os.Stat(indexLockPath(t, d)); err == nil {
		t.Error("the lock is still there")
	}
}

func TestALiveOrForeignIndexLockIsTransientContentionNotAHold(t *testing.T) {
	for name, body := range map[string]string{
		"a Git command's lock":  "index data of some git command\n",
		"a live process's lock": "",
	} {
		t.Run(name, func(t *testing.T) {
			resetHooks(t)
			f := newOwnerFixture(t, nil)
			d := f.f.dir
			if body == "" {
				body = ownLockRecord(t, os.Getpid())
			}
			write(t, indexLockPath(t, d), body)
			attempts := 0
			land.OwnerHooks.BeforeIndexLock = func() {
				attempts++
				if attempts == 3 {
					os.Remove(indexLockPath(t, d)) // the other command finished
				}
			}
			l := mustLand(t, f)
			if !l.Landed() || len(l.ConflictPaths) != 0 || l.Reason != "" {
				t.Fatalf("a held lock was not retried to a landing: %+v", l)
			}
			if attempts < 3 {
				t.Errorf("the lock was not retried: %d attempts", attempts)
			}
		})
	}
}
