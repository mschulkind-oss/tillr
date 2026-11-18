package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

const ownerLines = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n"

func savedRefs(t *testing.T, f contentionFixture) string {
	return git(t, f.f.dir, "for-each-ref", land.RecoveryPrefix+"saved/")
}

func TestTemporaryNameCollisionsNeverTouchUnrelatedFiles(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	outside := filepath.Join(scratch(t, "outside"), "target.txt")
	write(t, outside, "external\n")
	write(t, filepath.Join(d, ".tillr-install-0"), "keep me\n")
	if err := os.Symlink(outside, filepath.Join(d, "a.txt.tillr-undo")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(d, ".tillr-install-1")); err != nil {
		t.Fatal(err)
	}
	before := statOf(t, filepath.Join(d, ".tillr-install-0"))
	failed := false
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "new.txt" && !failed {
			failed = true
			return os.ErrPermission
		}
		return nil
	}
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the injected failure did not fail the landing")
	}
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, ".tillr-install-0")); got != before {
		t.Errorf("a file that shares the old temporary name changed: %+v", got)
	}
	if body := statOf(t, outside).body; body != "external\n" {
		t.Errorf("a temporary-name symlink was written through: %q", body)
	}
}

func TestAnAtomicEditorSaveOverATouchedFileIsNotLost(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	once := false
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "a.txt" && !once {
			once = true
			tmp := filepath.Join(d, "a.txt.editor")
			write(t, tmp, "l1\nl2\nl3\nl4\nl5\nl6\nl7\nSAVE8\nW9\n")
			return os.Rename(tmp, filepath.Join(d, "a.txt"))
		}
		return nil
	}
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "L1\nl2\nl3\nl4\nl5\nl6\nl7\nSAVE8\nW9\n" {
		t.Errorf("the atomic save was lost: %q", got)
	}
}

func TestAReceiptWriteFailureAfterPublicationStillReadsTheOldDescriptor(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	fd, err := os.OpenFile(filepath.Join(d, "a.txt"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER no_receipt BEFORE INSERT ON landings BEGIN SELECT RAISE(ABORT, 'receipt refused'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	land.OwnerHooks.AfterTrunkMove = func() {
		fd.WriteAt([]byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nLATE8\nW9\n"), 0)
	}
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil || !strings.Contains(err.Error(), "receipt refused") {
		t.Fatalf("the receipt failure was not reported: %v", err)
	}
	if f.f.trunk(t) == f.f.baseSHA {
		t.Fatal("the published trunk was undone")
	}
	ref := strings.Fields(savedRefs(t, f))
	if len(ref) < 3 {
		t.Fatalf("no ref keeps the late save: %q", savedRefs(t, f))
	}
	if got := git(t, d, "show", ref[2]+":worktree/a.txt"); !strings.Contains(got, "LATE8") {
		t.Errorf("the ref holds %q", got)
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Errorf("%d durable fault events, want 1", n)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; !strings.HasPrefix(got, "L1\n") {
		t.Errorf("the published checkout changed: %q", got)
	}
}

func TestACommitFailureAfterPublicationKeepsTheLateSaveAndTheRetryReportsIt(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	fd, err := os.OpenFile(filepath.Join(d, "a.txt"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, q := range []string{
			`CREATE TABLE fk_parent(id INTEGER PRIMARY KEY)`,
			`CREATE TABLE fk_child(pid INTEGER REFERENCES fk_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
			`CREATE TRIGGER fail_commit AFTER INSERT ON landings BEGIN INSERT INTO fk_child VALUES(7); END`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	once := false
	land.OwnerHooks.BeforeLastRead = func() {
		if !once {
			once = true
			fd.WriteAt([]byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nLATE8\nW9\n"), 0)
		}
	}
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the commit did not fail")
	}
	if savedRefs(t, f) == "" {
		t.Fatal("the late save was not kept")
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Fatalf("%d durable fault events, want 1", n)
	}
	if err := f.writer.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TRIGGER fail_commit`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	l := mustLand(t, f)
	if !l.Landed() || len(l.Faults) != 1 || !strings.Contains(l.Faults[0], land.RecoveryPrefix+"saved/") {
		t.Fatalf("the recovered landing did not report the saved ref: %+v", l)
	}
}

func TestAParentReplacedBySymlinkBetweenInstallsIsNeverWrittenThrough(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixtureWith(t, func(dir string) {
		os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
		write(t, filepath.Join(dir, "sub", "x.txt"), "base x\n")
	}, func(dir string) {
		write(t, filepath.Join(dir, "sub", "x.txt"), "land x\n")
	})
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	outside := scratch(t, "outside")
	write(t, filepath.Join(outside, "x.txt"), "base x\n")
	once := false
	land.OwnerHooks.AfterReplace = func(path string) {
		if path == "a.txt" && !once {
			once = true
			os.Rename(filepath.Join(d, "sub"), filepath.Join(d, "sub.moved"))
			os.Symlink(outside, filepath.Join(d, "sub"))
		}
	}
	l := mustLand(t, f)
	if l.Landed() {
		t.Fatalf("landed through a symlinked parent: %+v", l)
	}
	if got := statOf(t, filepath.Join(outside, "x.txt")).body; got != "base x\n" {
		t.Errorf("an external file was written: %q", got)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != ownerLines {
		t.Errorf("the first file was not put back: %q", got)
	}
}

func TestABranchSwitchDuringTheFinalComparisonIsRefusedByGitsLock(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	inTx := false
	var checkoutErr error
	land.OwnerHooks.TxOpened = func() { inTx = true }
	land.OwnerHooks.GitCall = func(_ string, args []string) {
		if inTx && checkoutErr == nil && args[0] == "ls-files" {
			checkoutErr = exec.Command("git", "-C", d, "checkout", "-q", "-b", "elsewhere").Run()
		}
	}
	l := mustLand(t, f)
	if !l.Landed() || checkoutErr == nil {
		t.Fatalf("landing %+v, checkout error %v", l, checkoutErr)
	}
	if git(t, d, "branch", "--show-current") != "master" || recoveryRefs(t, f) != "" {
		t.Error("the branch changed or a recovery ref is left")
	}
}

func TestABranchSwitchAfterPredictionAndAClosedItemLeaveNoRecoveryRef(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	land.OwnerHooks.AfterPredict = func() {
		if out, err := f.close(); err != nil {
			t.Errorf("close: %v %s", err, out)
		}
	}
	l := mustLand(t, f)
	if l.Reason != land.ReasonItemNotLandable {
		t.Fatalf("landing: %+v", l)
	}
	if refs := recoveryRefs(t, f); refs != "" {
		t.Errorf("a closed item's landing left a recovery ref: %s", refs)
	}
}

func TestARollbackRestoresAnOriginalSymlink(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixtureWith(t, func(dir string) {
		if err := os.Symlink("a.txt", filepath.Join(dir, "lnk")); err != nil {
			t.Fatal(err)
		}
	}, func(dir string) {
		os.Remove(filepath.Join(dir, "lnk"))
	})
	d := f.f.dir
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "new.txt" {
			return os.ErrPermission
		}
		return nil
	}
	if _, err := land.Land(context.Background(), f.s, f.trunk, f.req); err == nil {
		t.Fatal("the install did not fail")
	}
	target, err := os.Readlink(filepath.Join(d, "lnk"))
	if err != nil || target != "a.txt" {
		t.Fatalf("the symlink was not restored: %q %v", target, err)
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Errorf("%d durable fault events, want 1", n)
	}
}

func TestACandidateThatChangesTheSmudgeFilterIsRenderedUnderItsOwnAttributes(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixtureWith(t, func(dir string) {
		git(t, dir, "config", "filter.up.smudge", "tr a-z A-Z")
		git(t, dir, "config", "filter.up.clean", "tr A-Z a-z")
		write(t, filepath.Join(dir, ".gitattributes"), "a.txt filter=up\n")
		write(t, filepath.Join(dir, "a.txt"), "l1\n")
	}, func(dir string) {
		write(t, filepath.Join(dir, ".gitattributes"), "")
		write(t, filepath.Join(dir, "a.txt"), "landed\n")
	})
	d := f.f.dir
	os.Remove(filepath.Join(d, "a.txt"))
	git(t, d, "checkout", "--", "a.txt")
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "L1\n" {
		t.Fatalf("the fixture's smudge filter did not run: %q", got)
	}
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "landed\n" {
		t.Errorf("a.txt = %q, want the identity rendering", got)
	}
	if status := git(t, d, "status", "--porcelain"); status != "" {
		t.Errorf("status %q", status)
	}
}

func TestASurvivingCopyOfAKilledLandingIsNeverOverwrittenByTheSameRunsRetry(t *testing.T) {
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), ownerLines)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperLandThenKillBeforeInstall$")
	cmd.Env = append(os.Environ(), "TILLR_HELPER_KILL=1", "HELPER_DB="+f.db, "HELPER_REPO="+d,
		"HELPER_BASE="+f.f.baseSHA, "HELPER_HEAD="+f.f.headSHA, "HELPER_TREE="+f.f.treeSHA,
		"GIT_CONFIG_GLOBAL="+deployedConfig(t, fixtureName, fixtureEmail))
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the helper was not killed: %v", err)
	}
	refs := strings.Fields(recoveryRefs(t, f))
	if len(refs) < 3 {
		t.Fatal("the killed lander left no recovery copy")
	}
	killed := refs[2]
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != ownerLines {
		t.Fatalf("the owner's content left the disk: %q", got)
	}
	// The same run retries with the owner's content changed since the kill.
	write(t, filepath.Join(d, "a.txt"), strings.Replace(ownerLines, "W9", "W9 again", 1))
	l := mustLand(t, f)
	if !l.Landed() || len(l.Faults) != 1 || !strings.Contains(l.Faults[0], killed) {
		t.Fatalf("the stale copy was not reported by name: %+v", l)
	}
	if got := git(t, d, "show", killed+":worktree/a.txt"); got != strings.TrimSuffix(ownerLines, "\n") {
		t.Errorf("the reported ref no longer holds the owner's bytes: %q", got)
	}
}

func TestHelperLandThenKillBeforeInstall(t *testing.T) {
	if os.Getenv("TILLR_HELPER_KILL") == "" {
		t.Skip("run by a test that kills it")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, os.Getenv("HELPER_DB"), false)
	if err != nil {
		t.Fatal(err)
	}
	trunk, err := land.OpenTrunk(os.Getenv("HELPER_REPO"))
	if err != nil {
		t.Fatal(err)
	}
	base, head, tree := os.Getenv("HELPER_BASE"), os.Getenv("HELPER_HEAD"), os.Getenv("HELPER_TREE")
	req := land.LandingRequest{ItemID: "item_042", RunID: "run-contention", BaseRef: "master", BaseSHA: base, HeadSHA: head, At: time.Date(2026, 11, 16, 13, 0, 0, 0, time.UTC), Verdict: land.Verdict{VerdictID: "verdict-contention", BaseSHA: base, TreeSHA: tree, Result: land.VerdictPass}}
	land.OwnerHooks.AfterPredict = func() { syscall.Kill(os.Getpid(), syscall.SIGKILL) }
	land.Land(ctx, s, trunk, req)
	t.Fatal("the helper was not killed")
}
