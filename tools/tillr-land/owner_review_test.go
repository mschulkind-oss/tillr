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
	"github.com/mschulkind-oss/tillr/store"
)

func atomicSave(t *testing.T, path, body string) {
	t.Helper()
	tmp := path + ".editor"
	write(t, tmp, body)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func failNewTxt() func(string) error {
	return func(path string) error {
		if path == "new.txt" {
			return os.ErrPermission
		}
		return nil
	}
}

func allSavedBytes(t *testing.T, f contentionFixture) string {
	t.Helper()
	var out string
	for _, ref := range strings.Fields(git(t, f.f.dir, "for-each-ref", "--format=%(refname)", land.RecoveryPrefix+"saved/")) {
		for _, name := range strings.Fields(git(t, f.f.dir, "ls-tree", "-r", "--name-only", ref)) {
			out += name + "=" + git(t, f.f.dir, "show", ref+":"+name) + "\n"
		}
	}
	return out
}

func TestACorrectiveRollbackExchangeKeepsEveryNewerSave(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	land.OwnerHooks.InstallFault = failNewTxt()
	land.OwnerHooks.BeforeRollbackSwap = func(path string, step int) {
		if path == "a.txt" && step == 1 {
			atomicSave(t, filepath.Join(d, "a.txt"), "SAVE1\n")
		}
		if path == "a.txt" && step == 2 {
			atomicSave(t, filepath.Join(d, "a.txt"), "SAVE2\n")
		}
	}
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the install did not fail")
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "SAVE2\n" {
		t.Errorf("the newest save is not on disk: %q", got)
	}
	if saved := allSavedBytes(t, f); !strings.Contains(saved, "SAVE1") {
		t.Errorf("the superseded save is nowhere: %q", saved)
	}
}

func TestAFailedDisplacedReadRestoresTheOwnersFile(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	before := statOf(t, filepath.Join(d, "a.txt"))
	land.OwnerHooks.FailDisplacedRead = func(name string) error { return errors.New("read refused") }
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the landing did not fail")
	}
	if got := statOf(t, filepath.Join(d, "a.txt")); got != before {
		t.Errorf("the owner's file was not restored: %+v", got)
	}
	if f.f.trunk(t) != f.f.baseSHA {
		t.Error("the trunk moved")
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Errorf("%d durable fault events, want 1", n)
	}
	if recoveryRefs(t, f) == "" {
		t.Error("the recovery copy was dropped")
	}
}

func TestAFailedRollbackIsNeverReportedAsExact(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	land.OwnerHooks.InstallFault = failNewTxt()
	land.OwnerHooks.FailRollbackSwap = func(path string) error { return errors.New("rollback refused") }
	_, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || strings.Contains(err.Error(), "put back exactly") || !strings.Contains(err.Error(), "a.txt") {
		t.Fatalf("the error is %v", err)
	}
	if saved := allSavedBytes(t, f); !strings.Contains(saved, "W9") {
		t.Errorf("the owner's original bytes are kept nowhere: %q", saved)
	}
}

func TestAFailedIndexRestorationKeepsTheOriginalEntries(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nS5\nl6\nl7\nl8\nl9\n")
	git(t, d, "add", "a.txt")
	land.OwnerHooks.BeforeTrunkMove = func() {
		// Git's own commit would wait on the lander's index lock; a ref write does not.
		moved := git(t, d, "-c", "user.name=o", "-c", "user.email=o@example.invalid", "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "owner")
		git(t, d, "update-ref", "refs/heads/master", moved)
	}
	land.OwnerHooks.FailIndexRestore = func() error { return errors.New("index refused") }
	_, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || !strings.Contains(err.Error(), "index") || strings.Contains(err.Error(), "put back exactly") {
		t.Fatalf("the error is %v", err)
	}
	if saved := allSavedBytes(t, f); !strings.Contains(saved, "index/a.txt=") || !strings.Contains(saved, "S5") {
		t.Errorf("the original staged entry is kept nowhere: %q", saved)
	}
}

func TestAReceiptThatIsDurableStillReconcilesTheSurvivingCopy(t *testing.T) {
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
	if recoveryRefs(t, f) == "" {
		t.Fatal("no copy survived")
	}
	l := mustLand(t, f)
	if len(l.Faults) == 0 || !strings.Contains(strings.Join(l.Faults, "\n"), land.RecoveryPrefix+"copy/") {
		t.Fatalf("the stored receipt did not report the surviving copy: %+v", l)
	}
}

func TestAFaultEventThatCouldNotBeWrittenIsRecordedByTheNextAnswer(t *testing.T) {
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
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the lost fault event was not reported")
	}
	land.OwnerHooks.BeforeLastRead, land.OwnerHooks.FailFaultEvent = nil, nil
	l := mustLand(t, f)
	joined := strings.Join(l.Faults, "\n")
	if !strings.Contains(joined, land.RecoveryPrefix+"saved/") || !strings.Contains(joined, "a.txt") {
		t.Fatalf("the saved ref and its path are not reported: %+v", l)
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Errorf("%d fault events, want 1 recorded by the retry", n)
	}
}

func TestARenamedParentIsNeverWrittenThroughItsOldDescriptor(t *testing.T) {
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
	land.OwnerHooks.BeforeSwap = func(path string) {
		if path == "sub/x.txt" && !once {
			once = true
			os.Rename(filepath.Join(d, "sub"), filepath.Join(d, "relocated"))
			relocated = statOf(t, filepath.Join(d, "relocated", "x.txt"))
			os.MkdirAll(filepath.Join(d, "sub"), 0o755)
			write(t, filepath.Join(d, "sub", "x.txt"), "owner replacement\n")
		}
	}
	l := mustLand(t, f)
	if l.Landed() {
		t.Fatalf("landed over a relocated parent: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "relocated", "x.txt")); got != relocated {
		t.Errorf("the relocated file was rewritten: %+v", got)
	}
	if got := statOf(t, filepath.Join(d, "sub", "x.txt")).body; got != "owner replacement\n" {
		t.Errorf("the replacement changed: %q", got)
	}
}

func TestASwitchBeforeTheIndexLockWinsAndNoFileIsRewrittenInTheOtherBranch(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	before := statOf(t, filepath.Join(d, "a.txt"))
	switched := false
	land.OwnerHooks.BeforeIndexLock = func() {
		if !switched {
			switched = true
			git(t, d, "checkout", "-q", "-b", "elsewhere")
		}
	}
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")); got != before {
		t.Errorf("the other branch's file was rewritten: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(d, "new.txt")); err == nil {
		t.Error("a file was installed in the other branch")
	}
}

func TestACheckoutDuringTheInstallIsRefusedByGitsOwnLock(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	var checkoutErr error
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "a.txt" && checkoutErr == nil {
			checkoutErr = exec.Command("git", "-C", d, "checkout", "-q", "-b", "elsewhere").Run()
		}
		return nil
	}
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if checkoutErr == nil {
		t.Error("a checkout ran in the middle of the install")
	}
	if git(t, d, "branch", "--show-current") != "master" {
		t.Error("the checkout switched branches mid-install")
	}
}

func TestACandidateThatAddsABinaryAttributeConflictsWithTheOwnersEdit(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixtureWith(t, nil, func(dir string) {
		write(t, filepath.Join(dir, ".gitattributes"), "a.txt binary\n")
	})
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	l := mustLand(t, f)
	if l.Reason != land.ReasonTrunkUncommitted || len(l.ConflictPaths) != 1 || l.ConflictPaths[0] != "a.txt" {
		t.Fatalf("landing: %+v", l)
	}
}

func TestADirectoryAppearingBeforeTheExchangeLeavesNoTemporary(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	once := false
	land.OwnerHooks.BeforeSwap = func(path string) {
		if path == "a.txt" && !once {
			once = true
			os.Remove(filepath.Join(d, "a.txt"))
			os.Mkdir(filepath.Join(d, "a.txt"), 0o755)
			write(t, filepath.Join(d, "a.txt", "inner"), "owner\n")
		}
	}
	l := mustLand(t, f)
	if l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	left, _ := filepath.Glob(filepath.Join(d, ".tillr-*"))
	if len(left) != 0 {
		t.Errorf("temporaries were left: %v", left)
	}
}

func TestACanceledRequestStillPreservesAPublishedDescriptorSave(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	fd, err := os.OpenFile(filepath.Join(d, "a.txt"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	land.OwnerHooks.AfterTrunkMove = func() {
		cancel()
		fd.WriteAt([]byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nCANCEL8\nW9\n"), 0)
	}
	land.Land(ctx, f.s, f.trunk, f.req)
	if saved := allSavedBytes(t, f); !strings.Contains(saved, "CANCEL8") {
		t.Errorf("the save disappeared with the canceled request: %q", saved)
	}
}

func TestOwnerRenamesAreDeletionsAndAdditions(t *testing.T) {
	base := func(d string) { write(t, filepath.Join(d, "b.txt"), "b\n") }
	t.Run("the owner renamed a file the landing edits", func(t *testing.T) {
		_, l := landOver(t, nil, nil, func(d string) { os.Rename(filepath.Join(d, "a.txt"), filepath.Join(d, "z.txt")) })
		wantHeld(t, l, "a.txt")
	})
	t.Run("the owner staged a rename of a file the landing deletes", func(t *testing.T) {
		var moved stat
		f, l := landOver(t, base, func(d string) { os.Remove(filepath.Join(d, "b.txt")) }, func(d string) {
			git(t, d, "mv", "b.txt", "b2.txt")
			moved = statOf(t, filepath.Join(d, "b2.txt"))
		})
		if !l.Landed() {
			t.Fatalf("landing: %+v", l)
		}
		if got := statOf(t, filepath.Join(f.f.dir, "b2.txt")); got != moved {
			t.Errorf("the renamed file was rewritten: %+v", got)
		}
		if got := git(t, f.f.dir, "status", "--porcelain"); !strings.Contains(got, "A  b2.txt") {
			t.Errorf("the staged add was lost: %q", got)
		}
	})
}

func TestACleanOpenUntouchedFileKeepsItsMetadataAndIndexEntry(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	open, err := os.Open(filepath.Join(d, "keep.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	before := statOf(t, filepath.Join(d, "keep.txt"))
	entry := git(t, d, "ls-files", "--debug", "keep.txt")
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "keep.txt")); got != before {
		t.Errorf("the open file changed: %+v", got)
	}
	if got := git(t, d, "ls-files", "--debug", "keep.txt"); got != entry {
		t.Errorf("the index entry changed:\n%s\n%s", entry, got)
	}
}

var _ = store.Open
