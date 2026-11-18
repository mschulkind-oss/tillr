package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

const lines = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n"

// newOwnerFixture is a trunk with a.txt (nine lines), untouched.txt and keep.txt, and a
// candidate that rewrites a.txt's first line and adds new.txt.
func newOwnerFixture(t *testing.T, extra func(dir string)) contentionFixture {
	t.Helper()
	return newOwnerFixtureWith(t, extra, nil)
}

// newOwnerFixtureWith also lets the candidate's commit carry extra changes.
func newOwnerFixtureWith(t *testing.T, extra, candidate func(dir string)) contentionFixture {
	t.Helper()
	dir := filepath.Join(scratch(t, "repo"), "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-b", "master")
	write(t, filepath.Join(dir, "a.txt"), lines)
	write(t, filepath.Join(dir, "untouched.txt"), "u\n")
	write(t, filepath.Join(dir, "keep.txt"), "k\n")
	if extra != nil {
		extra(dir)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "candidate")
	write(t, filepath.Join(dir, "a.txt"), "L1\n"+strings.TrimPrefix(lines, "l1\n"))
	write(t, filepath.Join(dir, "new.txt"), "new\n")
	if candidate != nil {
		candidate(dir)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	git(t, dir, "checkout", "master")
	return newContentionFixtureFor(t, fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree})
}

type stat struct {
	ino   uint64
	mtime time.Time
	body  string
}

func statOf(t *testing.T, path string) stat {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	return stat{info.Sys().(*syscall.Stat_t).Ino, info.ModTime(), string(body)}
}

func mustLand(t *testing.T, f contentionFixture) land.Landing {
	t.Helper()
	l, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	return l
}

func recoveryRefs(t *testing.T, f contentionFixture) string {
	return git(t, f.f.dir, "for-each-ref", land.RecoveryPrefix)
}

func resetHooks(t *testing.T) { t.Cleanup(land.ResetOwnerHooks) }

func TestALandingLeavesUntouchedPathsAloneAndKeepsTheStagedSplitOnTouchedOnes(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nS5\nl6\nl7\nl8\nl9\n")
	git(t, d, "add", "a.txt")
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nS5\nl6\nl7\nl8\nW9\n")
	write(t, filepath.Join(d, "untouched.txt"), "staged\n")
	git(t, d, "add", "untouched.txt")
	write(t, filepath.Join(d, "keep.txt"), "modified\n")
	write(t, filepath.Join(d, "scratch.txt"), "untracked\n")
	before := map[string]stat{}
	for _, n := range []string{"untouched.txt", "keep.txt", "scratch.txt"} {
		before[n] = statOf(t, filepath.Join(d, n))
	}
	aBefore := statOf(t, filepath.Join(d, "a.txt"))
	indexBefore := git(t, d, "ls-files", "-s", "untouched.txt", "keep.txt")
	var calls []string
	txOpen := false
	var afterTx []string
	land.OwnerHooks.GitCall = func(_ string, args []string) {
		calls = append(calls, args[0])
		if txOpen {
			afterTx = append(afterTx, args[0])
		}
	}
	land.OwnerHooks.TxOpened = func() { txOpen = true }
	every := []string{"a.txt", "untouched.txt", "keep.txt", "scratch.txt"}
	snapshot := func() string {
		out := git(t, d, "ls-files", "-s") + git(t, d, "status", "--porcelain") + git(t, d, "rev-parse", "HEAD")
		for _, n := range every {
			out += n + "=" + statOf(t, filepath.Join(d, n)).body
		}
		return out
	}
	frozen := snapshot()
	frozenStats := map[string]stat{}
	for _, n := range every {
		frozenStats[n] = statOf(t, filepath.Join(d, n))
	}
	land.OwnerHooks.AfterPredict = func() {
		if got := snapshot(); got != frozen {
			t.Errorf("the tree, the index or HEAD changed during preparation")
		}
		for n, s := range frozenStats {
			if got := statOf(t, filepath.Join(d, n)); got != s {
				t.Errorf("%s changed during preparation: %+v", n, got)
			}
		}
		ref := strings.Fields(recoveryRefs(t, f))[2]
		if names := git(t, d, "ls-tree", "-r", "--name-only", ref); names != "index/a.txt\nworktree/a.txt" {
			t.Errorf("the recovery copy holds %q, want only the touched path's content", names)
		}
	}
	l := mustLand(t, f)
	if !l.Landed() || len(l.Faults) != 0 {
		t.Fatalf("landing: %+v", l)
	}
	for n, s := range before {
		if got := statOf(t, filepath.Join(d, n)); got != s {
			t.Errorf("%s changed: %+v -> %+v", n, s, got)
		}
	}
	if got := git(t, d, "ls-files", "-s", "untouched.txt", "keep.txt"); got != indexBefore {
		t.Errorf("untouched index entries changed: %q -> %q", indexBefore, got)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")); got.body != "L1\nl2\nl3\nl4\nS5\nl6\nl7\nl8\nW9\n" || got.ino == aBefore.ino {
		t.Errorf("a.txt = %+v", got)
	}
	if staged := git(t, d, "diff", "--cached", "--", "a.txt"); !strings.Contains(staged, "+S5") || strings.Contains(staged, "W9") {
		t.Errorf("the staged split is lost:\n%s", staged)
	}
	if unstaged := git(t, d, "diff", "--", "a.txt"); !strings.Contains(unstaged, "+W9") || strings.Contains(unstaged, "+S5") || strings.Contains(unstaged, "-S5") {
		t.Errorf("the unstaged split is lost:\n%s", unstaged)
	}
	if refs := recoveryRefs(t, f); refs != "" {
		t.Errorf("a clean landing left a recovery ref: %s", refs)
	}
	if git(t, d, "stash", "list") != "" {
		t.Error("a stash was made")
	}
	allowed := map[string]bool{"worktree": true, "rev-parse": true, "ls-files": true, "update-index": true, "-c": true, "update-ref": true}
	for _, c := range afterTx {
		if !allowed[c] {
			t.Errorf("git %s ran inside the write transaction; all calls: %v", c, calls)
		}
	}
	prep := strings.Join(calls, " ")
	for _, want := range []string{"diff-tree", "merge-file", "hash-object", "update-index"} {
		if !strings.Contains(prep, want) {
			t.Errorf("git %s never ran: %v", want, calls)
		}
	}
}

func TestAPredictedConflictChangesNothingAndNamesThePath(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "OWN1\n"+strings.TrimPrefix(lines, "l1\n"))
	before := statOf(t, filepath.Join(d, "a.txt"))
	l := mustLand(t, f)
	if l.Result != land.ResultRefused || l.Reason != land.ReasonTrunkUncommitted || len(l.ConflictPaths) != 1 || l.ConflictPaths[0] != "a.txt" {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")); got != before {
		t.Errorf("the conflicting file changed")
	}
	if _, err := os.Stat(filepath.Join(d, "new.txt")); err == nil {
		t.Error("a file was written")
	}
	if recoveryRefs(t, f) != "" {
		t.Error("a predicted conflict wrote a recovery ref")
	}
	if f.f.trunk(t) != f.f.baseSHA {
		t.Error("the trunk moved")
	}
}

func TestASaveBetweenPredictionAndTheCheckChangesNothingAndTheLandingPredictsAgain(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	once := false
	land.OwnerHooks.AfterPredict = func() {
		if once {
			return
		}
		once = true
		if recoveryRefs(t, f) == "" {
			t.Error("the recovery copy was not written before the hook")
		}
		write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nSAVED8\nW9\n")
	}
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "L1\nl2\nl3\nl4\nl5\nl6\nl7\nSAVED8\nW9\n" {
		t.Errorf("a.txt = %q", got)
	}
	if recoveryRefs(t, f) != "" {
		t.Error("a recovery ref is left")
	}
}

func TestASaveThroughAnOpenDescriptorBeforeTheTrunkMovesUndoesAndRetries(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	fd, err := os.OpenFile(filepath.Join(d, "a.txt"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	once := false
	land.OwnerHooks.AfterInstall = func() {
		if once {
			return
		}
		once = true
		if _, err := fd.WriteAt([]byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nFD8\nW9\n"), 0); err != nil {
			t.Error(err)
		}
	}
	l := mustLand(t, f)
	if !l.Landed() || len(l.Faults) != 0 {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "L1\nl2\nl3\nl4\nl5\nl6\nl7\nFD8\nW9\n" {
		t.Errorf("the descriptor's save was lost: %q", got)
	}
}

func TestASaveThroughAnOpenDescriptorAfterTheTrunkMovesIsKeptInARefAndUndoesNothing(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	fd, err := os.OpenFile(filepath.Join(d, "a.txt"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	land.OwnerHooks.BeforeLastRead = func() {
		fd.WriteAt([]byte("l1\nl2\nl3\nl4\nl5\nl6\nl7\nLATE8\nW9\n"), 0)
	}
	l := mustLand(t, f)
	if !l.Landed() || len(l.Faults) != 1 || !strings.Contains(l.Faults[0], "a.txt") || !strings.Contains(l.Faults[0], land.RecoveryPrefix) {
		t.Fatalf("landing: %+v", l)
	}
	if f.f.trunk(t) == f.f.baseSHA {
		t.Error("the trunk did not stay moved")
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "L1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n" {
		t.Errorf("the landed checkout changed: %q", got)
	}
	if git(t, d, "diff", "--cached", "--name-only") != "" {
		t.Error("the index was changed")
	}
	ref := strings.Fields(recoveryRefs(t, f))
	if len(ref) == 0 {
		t.Fatal("no ref keeps the saved bytes")
	}
	if got := git(t, d, "show", l.Faults[0][strings.Index(l.Faults[0], land.RecoveryPrefix):]+":worktree/a.txt"); !strings.Contains(got, "LATE8") {
		t.Errorf("the ref holds %q", got)
	}
}

func TestAFailedInstallPutsTheOwnersContentBackAndRecordsTheFault(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	before := statOf(t, filepath.Join(d, "a.txt"))
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "new.txt" {
			return os.ErrPermission
		}
		return nil
	}
	_, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err == nil || !strings.Contains(err.Error(), "fault") {
		t.Fatalf("the install failure was not reported: %v", err)
	}
	if got := statOf(t, filepath.Join(d, "a.txt")); got.body != before.body {
		t.Errorf("a.txt = %q", got.body)
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Errorf("%d durable fault events, want 1", n)
	}
	if strings.Contains(git(t, d, "status", "--porcelain"), "<<<<") || f.f.trunk(t) != f.f.baseSHA {
		t.Error("the trunk or the checkout changed")
	}
	if _, err := os.Stat(filepath.Join(d, "new.txt")); err == nil {
		t.Error("new.txt was left")
	}
}

func TestASaveAtAPathBetweenAnInstallAndItsUndoingSurvives(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	land.OwnerHooks.InstallFault = func(path string) error {
		if path == "new.txt" {
			return os.ErrPermission
		}
		return nil
	}
	land.OwnerHooks.BeforeUndoRead = func(path string) {
		if path == "a.txt" {
			write(t, filepath.Join(d, "a.txt"), "NEWER\n")
		}
	}
	land.Land(context.Background(), f.s, f.trunk, f.req)
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "NEWER\n" {
		t.Errorf("the save between install and undo was lost: %q", got)
	}
}

func TestASwitchedBranchAfterPredictionWritesNoFile(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	land.OwnerHooks.AfterPredict = func() { git(t, d, "checkout", "-q", "-b", "elsewhere") }
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if _, err := os.Stat(filepath.Join(d, "new.txt")); err == nil {
		t.Error("a file was written into the switched checkout")
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n" {
		t.Errorf("a.txt = %q", got)
	}
}

func TestACrashBetweenTheRecoveryCopyAndTheInstallIsSettledByTheNextLanding(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	land.OwnerHooks.AfterPredict = func() { panic("killed") }
	func() {
		defer func() { recover() }()
		land.Land(context.Background(), f.s, f.trunk, f.req)
	}()
	land.OwnerHooks.AfterPredict = nil
	if recoveryRefs(t, f) == "" {
		t.Fatal("the recovery copy was not written before the kill")
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; got != "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n" {
		t.Fatalf("the owner's content left the disk: %q", got)
	}
	l := mustLand(t, f)
	if !l.Landed() || len(l.Faults) != 0 || recoveryRefs(t, f) != "" {
		t.Fatalf("the next landing did not settle the ref: %+v %s", l, recoveryRefs(t, f))
	}
}

func TestACrashLeftRefThatTheDiskNoLongerMatchesIsReported(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	land.OwnerHooks.AfterPredict = func() { panic("killed") }
	func() {
		defer func() { recover() }()
		land.Land(context.Background(), f.s, f.trunk, f.req)
	}()
	land.OwnerHooks.AfterPredict = nil
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9 edited\n")
	f.req.RunID = "run-next"
	l := mustLand(t, f)
	if len(l.Faults) != 1 || !strings.Contains(l.Faults[0], "a.txt") || !strings.Contains(l.Faults[0], land.RecoveryPrefix) {
		t.Fatalf("the stale ref was not reported: %+v", l)
	}
}

func TestSideFilesBesideALandedFileAreNeverTouchedAndNeverPause(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	sides := []string{".a.txt.swp", ".a.txt.swo", "a.txt~", "4913", ".#a.txt", "#a.txt#"}
	before := map[string]stat{}
	for _, n := range sides {
		write(t, filepath.Join(d, n), "side "+n+"\n")
		before[n] = statOf(t, filepath.Join(d, n))
	}
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	for n, s := range before {
		if got := statOf(t, filepath.Join(d, n)); got != s {
			t.Errorf("%s changed", n)
		}
	}
	if got := statOf(t, filepath.Join(d, "a.txt")).body; !strings.HasPrefix(got, "L1\n") {
		t.Errorf("a.txt = %q", got)
	}
}

func TestAnUntrackedFileOnAPathTheLandingAddsConflictsUnlessIdentical(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "new.txt"), "new\n")
	before := statOf(t, filepath.Join(d, "new.txt"))
	l := mustLand(t, f)
	if !l.Landed() {
		t.Fatalf("an identical untracked file held the landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(d, "new.txt")); got != before {
		t.Error("an identical file was rewritten")
	}
	if status := git(t, d, "status", "--porcelain"); status != "" {
		t.Errorf("status after: %q", status)
	}
}

func TestADirectoryTurnedIntoAFileIsAConflictWhenAnIgnoredSideFileIsInTheWay(t *testing.T) {
	resetHooks(t)
	dir := filepath.Join(scratch(t, "repo"), "repo")
	os.MkdirAll(filepath.Join(dir, "d"), 0o755)
	git(t, dir, "init", "-b", "master")
	write(t, filepath.Join(dir, ".gitignore"), "*.swp\n")
	write(t, filepath.Join(dir, "d", "x.txt"), "x\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "candidate")
	git(t, dir, "rm", "-q", "-r", "d")
	write(t, filepath.Join(dir, "d"), "now a file\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	git(t, dir, "checkout", "master")
	write(t, filepath.Join(dir, "d", ".x.txt.swp"), "swap\n")
	f := newContentionFixtureFor(t, fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree})
	before := statOf(t, filepath.Join(dir, "d", ".x.txt.swp"))
	l := mustLand(t, f)
	if l.Reason != land.ReasonTrunkUncommitted || strings.Join(l.ConflictPaths, ",") != "d,d/.x.txt.swp" {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(dir, "d", ".x.txt.swp")); got != before {
		t.Error("the side file changed")
	}
}

func TestADirectoryTurnedIntoAFileLandsWhenTheOwnerHasNothingThere(t *testing.T) {
	resetHooks(t)
	dir := filepath.Join(scratch(t, "repo"), "repo")
	os.MkdirAll(filepath.Join(dir, "d", "sub"), 0o755)
	git(t, dir, "init", "-b", "master")
	write(t, filepath.Join(dir, "d", "sub", "x.txt"), "x\n")
	write(t, filepath.Join(dir, "d", "y.txt"), "y\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "candidate")
	git(t, dir, "rm", "-q", "-r", "d")
	write(t, filepath.Join(dir, "d"), "now a file\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	git(t, dir, "checkout", "master")
	f := newContentionFixtureFor(t, fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree})
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if got := statOf(t, filepath.Join(dir, "d")).body; got != "now a file\n" || git(t, dir, "status", "--porcelain") != "" {
		t.Errorf("d = %q, status %q", got, git(t, dir, "status", "--porcelain"))
	}
}

func TestABinaryFileWithAnyOverlapIsAConflictAndAParentSymlinkIsNeverWrittenThrough(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, func(dir string) {
		write(t, filepath.Join(dir, ".gitattributes"), "a.txt binary\n")
	})
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nW9\n")
	if l := mustLand(t, f); l.Reason != land.ReasonTrunkUncommitted || len(l.ConflictPaths) != 1 {
		t.Fatalf("a binary overlap landed: %+v", l)
	}
	// A symlinked parent directory.
	dir := filepath.Join(scratch(t, "repo"), "repo")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	git(t, dir, "init", "-b", "master")
	write(t, filepath.Join(dir, "sub", "x.txt"), "x\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "checkout", "-b", "candidate")
	write(t, filepath.Join(dir, "sub", "x.txt"), "y\n")
	git(t, dir, "commit", "-am", "candidate")
	head := git(t, dir, "rev-parse", "HEAD")
	tree := git(t, dir, "rev-parse", "HEAD^{tree}")
	git(t, dir, "checkout", "master")
	elsewhere := scratch(t, "elsewhere")
	write(t, filepath.Join(elsewhere, "x.txt"), "outside\n")
	os.RemoveAll(filepath.Join(dir, "sub"))
	os.Symlink(elsewhere, filepath.Join(dir, "sub"))
	g := newContentionFixtureFor(t, fixture{dir: dir, ref: "master", baseSHA: base, headSHA: head, treeSHA: tree})
	if l := mustLand(t, g); l.Reason != land.ReasonTrunkUncommitted {
		t.Fatalf("a symlinked parent did not pause: %+v", l)
	}
	if got := statOf(t, filepath.Join(elsewhere, "x.txt")).body; got != "outside\n" {
		t.Errorf("the landing wrote through the symlink: %q", got)
	}
}

func TestACheckedOutFileThatAFilterConvertsIsNotAFalseConflict(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, func(dir string) {
		write(t, filepath.Join(dir, ".gitattributes"), "a.txt text eol=crlf\n")
	})
	d := f.f.dir
	os.Remove(filepath.Join(d, "a.txt"))
	git(t, d, "checkout", "--", "a.txt")
	if body := statOf(t, filepath.Join(d, "a.txt")).body; !strings.Contains(body, "\r\n") {
		t.Skipf("this git did not apply eol=crlf: %q", body)
	}
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("a clean converted file was a false conflict: %+v", l)
	}
	if body := statOf(t, filepath.Join(d, "a.txt")).body; !strings.HasPrefix(body, "L1\r\n") {
		t.Errorf("a.txt was not written in its checked-out form: %q", body)
	}
	if status := git(t, d, "status", "--porcelain"); status != "" {
		t.Errorf("status %q", status)
	}
}

func TestAMergeInProgressPausesTheLanding(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	head := git(t, d, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(d, ".git", "MERGE_HEAD"), []byte(head+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := mustLand(t, f)
	if l.Reason != land.ReasonTrunkUncommitted || len(l.ConflictPaths) != 1 || !strings.Contains(l.ConflictPaths[0], "merge in progress") {
		t.Fatalf("landing: %+v", l)
	}
}

func TestAnOwnerCommitBetweenGateAndLandingIsReplayedOnto(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "keep.txt"), "owner commit\n")
	git(t, d, "commit", "-am", "owner")
	if l := mustLand(t, f); !l.Landed() || l.GatedTree != f.f.treeSHA {
		t.Fatalf("landing: %+v", l)
	}
}
