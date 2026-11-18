package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
)

// landOver builds a trunk from base, a candidate from cand (on top of the fixture's own a.txt
// edit), the owner's checkout state from owner, and lands once.
func landOver(t *testing.T, base, cand, owner func(d string)) (contentionFixture, land.Landing) {
	t.Helper()
	resetHooks(t)
	f := newOwnerFixtureWith(t, base, cand)
	if owner != nil {
		owner(f.f.dir)
	}
	return f, mustLand(t, f)
}

func wantHeld(t *testing.T, l land.Landing, paths ...string) {
	t.Helper()
	if l.Reason != land.ReasonTrunkUncommitted || strings.Join(l.ConflictPaths, ",") != strings.Join(paths, ",") {
		t.Fatalf("landing %s/%s %v, want a hold on %v", l.Result, l.Reason, l.ConflictPaths, paths)
	}
}

func TestDeletionsAndRenamesOnEitherSide(t *testing.T) {
	base := func(d string) { write(t, filepath.Join(d, "b.txt"), "b\n"); write(t, filepath.Join(d, "c.txt"), "c\n") }
	cand := func(d string) { os.Remove(filepath.Join(d, "b.txt")); git(t, d, "mv", "c.txt", "d.txt") }
	t.Run("the owner edited a deleted file", func(t *testing.T) {
		f, l := landOver(t, base, cand, func(d string) { write(t, filepath.Join(d, "b.txt"), "mine\n") })
		wantHeld(t, l, "b.txt")
		if _, err := os.Stat(filepath.Join(f.f.dir, "d.txt")); err == nil {
			t.Error("a file was written under a hold")
		}
	})
	t.Run("the owner edited a renamed-away file", func(t *testing.T) {
		_, l := landOver(t, base, cand, func(d string) { write(t, filepath.Join(d, "c.txt"), "mine\n") })
		wantHeld(t, l, "c.txt")
	})
	t.Run("a clean checkout takes the deletion and the rename", func(t *testing.T) {
		f, l := landOver(t, base, cand, nil)
		d := f.f.dir
		if !l.Landed() {
			t.Fatalf("landing: %+v", l)
		}
		for _, gone := range []string{"b.txt", "c.txt"} {
			if _, err := os.Stat(filepath.Join(d, gone)); err == nil {
				t.Errorf("%s was not removed", gone)
			}
		}
		if statOf(t, filepath.Join(d, "d.txt")).body != "c\n" || git(t, d, "status", "--porcelain") != "" {
			t.Errorf("status %q", git(t, d, "status", "--porcelain"))
		}
	})
	t.Run("the owner deleted a file the landing edits", func(t *testing.T) {
		_, l := landOver(t, nil, nil, func(d string) { os.Remove(filepath.Join(d, "a.txt")) })
		wantHeld(t, l, "a.txt")
	})
	t.Run("both deleted the same file", func(t *testing.T) {
		f, l := landOver(t, base, cand, func(d string) { os.Remove(filepath.Join(d, "b.txt")) })
		if !l.Landed() {
			t.Fatalf("landing: %+v", l)
		}
		if _, err := os.Stat(filepath.Join(f.f.dir, "b.txt")); err == nil {
			t.Error("b.txt came back")
		}
	})
}

func TestModeChangesMergeWithTheOwnersContent(t *testing.T) {
	base := func(d string) { write(t, filepath.Join(d, "s.sh"), "one\ntwo\nthree\nfour\nfive\nsix\nseven\n") }
	cand := func(d string) {
		if err := os.Chmod(filepath.Join(d, "s.sh"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f, l := landOver(t, base, cand, func(d string) {
		write(t, filepath.Join(d, "s.sh"), "one\ntwo\nthree\nfour\nfive\nsix\nSEVEN\n")
	})
	d := f.f.dir
	if !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	info, err := os.Stat(filepath.Join(d, "s.sh"))
	if err != nil || info.Mode()&0o111 == 0 {
		t.Errorf("the executable bit was not landed: %v %v", info, err)
	}
	if statOf(t, filepath.Join(d, "s.sh")).body != "one\ntwo\nthree\nfour\nfive\nsix\nSEVEN\n" {
		t.Error("the owner's content was lost")
	}
	if got := git(t, d, "ls-files", "-s", "s.sh"); !strings.HasPrefix(got, "100755") {
		t.Errorf("the index mode is %q", got)
	}
}

func TestOverlappingStagedAndUnstagedEditsKeepTheirSplit(t *testing.T) {
	f := newOwnerFixture(t, nil)
	resetHooks(t)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nS5\nl6\nl7\nl8\nl9\n")
	git(t, d, "add", "a.txt")
	write(t, filepath.Join(d, "a.txt"), "l1\nl2\nl3\nl4\nS5 more\nl6\nl7\nl8\nl9\n")
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	staged, unstaged := git(t, d, "diff", "--cached", "--", "a.txt"), git(t, d, "diff", "--", "a.txt")
	if !strings.Contains(staged, "+S5\n") || strings.Contains(staged, "S5 more") {
		t.Errorf("staged:\n%s", staged)
	}
	if !strings.Contains(unstaged, "-S5\n") || !strings.Contains(unstaged, "+S5 more") {
		t.Errorf("unstaged:\n%s", unstaged)
	}
}

func TestFileAndDirectoryExchangesFollowTheOwnersContent(t *testing.T) {
	base := func(d string) { write(t, filepath.Join(d, "f"), "file\n") }
	cand := func(d string) {
		os.Remove(filepath.Join(d, "f"))
		os.MkdirAll(filepath.Join(d, "f", "deep"), 0o755)
		write(t, filepath.Join(d, "f", "deep", "x.txt"), "x\n")
	}
	t.Run("a file becomes a directory over a clean file", func(t *testing.T) {
		f, l := landOver(t, base, cand, nil)
		if !l.Landed() || statOf(t, filepath.Join(f.f.dir, "f", "deep", "x.txt")).body != "x\n" || git(t, f.f.dir, "status", "--porcelain") != "" {
			t.Fatalf("landing %+v status %q", l, git(t, f.f.dir, "status", "--porcelain"))
		}
	})
	t.Run("a file becomes a directory over the owner's edit", func(t *testing.T) {
		f, l := landOver(t, base, cand, func(d string) { write(t, filepath.Join(d, "f"), "mine\n") })
		wantHeld(t, l, "f")
		if statOf(t, filepath.Join(f.f.dir, "f")).body != "mine\n" {
			t.Error("the owner's file changed")
		}
	})
	t.Run("a directory with tracked subdirectories becomes a file", func(t *testing.T) {
		base := func(d string) {
			os.MkdirAll(filepath.Join(d, "g", "s", "t"), 0o755)
			write(t, filepath.Join(d, "g", "s", "t", "y.txt"), "y\n")
			write(t, filepath.Join(d, "g", "z.txt"), "z\n")
		}
		cand := func(d string) { os.RemoveAll(filepath.Join(d, "g")); write(t, filepath.Join(d, "g"), "now a file\n") }
		f, l := landOver(t, base, cand, nil)
		if !l.Landed() || statOf(t, filepath.Join(f.f.dir, "g")).body != "now a file\n" {
			t.Fatalf("landing: %+v", l)
		}
		_, l = landOver(t, base, cand, func(d string) { write(t, filepath.Join(d, "g", "s", "t", "y.txt"), "mine\n") })
		wantHeld(t, l, "g/s/t/y.txt")
	})
}

func TestBinaryFilesAreDetectedByContentAndIdenticalOnesLand(t *testing.T) {
	base := func(d string) { os.WriteFile(filepath.Join(d, "bin.dat"), []byte("a\x00b\n"), 0o644) }
	cand := func(d string) { os.WriteFile(filepath.Join(d, "bin.dat"), []byte("a\x00c\n"), 0o644) }
	t.Run("a NUL byte makes any overlap a conflict", func(t *testing.T) {
		_, l := landOver(t, base, cand, func(d string) { os.WriteFile(filepath.Join(d, "bin.dat"), []byte("a\x00d\n"), 0o644) })
		wantHeld(t, l, "bin.dat")
	})
	t.Run("identical bytes are not rewritten", func(t *testing.T) {
		f := newOwnerFixtureWith(t, base, cand)
		resetHooks(t)
		p := filepath.Join(f.f.dir, "bin.dat")
		os.WriteFile(p, []byte("a\x00c\n"), 0o644)
		before := statOf(t, p)
		if l := mustLand(t, f); !l.Landed() {
			t.Fatalf("landing: %+v", l)
		}
		if got := statOf(t, p); got != before {
			t.Errorf("an identical binary was rewritten: %+v", got)
		}
	})
}

func TestASecondChangeLandsWhileAnotherIsHeldByAConflict(t *testing.T) {
	resetHooks(t)
	f := newOwnerFixture(t, nil)
	d := f.f.dir
	write(t, filepath.Join(d, "a.txt"), "OWN1\n"+strings.TrimPrefix(lines, "l1\n"))
	git(t, d, "checkout", "-q", "-b", "cand2", f.f.baseSHA)
	write(t, filepath.Join(d, "other.txt"), "other\n")
	git(t, d, "add", "other.txt")
	git(t, d, "commit", "-qm", "second")
	second := f.req
	second.RunID, second.HeadSHA = "run-second", git(t, d, "rev-parse", "HEAD")
	second.Verdict = land.Verdict{VerdictID: "verdict-second", BaseSHA: f.f.baseSHA, TreeSHA: git(t, d, "rev-parse", "HEAD^{tree}"), Result: land.VerdictPass}
	git(t, d, "checkout", "-q", "master")
	owner := statOf(t, filepath.Join(d, "a.txt"))
	if l := mustLand(t, f); l.Reason != land.ReasonTrunkUncommitted {
		t.Fatalf("the conflicting change did not hold: %+v", l)
	}
	got, err := land.Land(context.Background(), f.s, f.trunk, second)
	if err != nil || !got.Landed() {
		t.Fatalf("the second change did not land: %+v %v", got, err)
	}
	if statOf(t, filepath.Join(d, "a.txt")) != owner || statOf(t, filepath.Join(d, "other.txt")).body != "other\n" {
		t.Error("the second landing disturbed the owner's file or did not write its own")
	}
	git(t, d, "checkout", "--", "a.txt")
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("the held change did not land once the owner restored: %+v", l)
	}
}

// dirToFile is a landing that replaces the directory g, holding tracked files at two depths,
// with a file.
func dirToFile(t *testing.T) contentionFixture {
	t.Helper()
	base := func(d string) {
		os.MkdirAll(filepath.Join(d, "g", "s", "t"), 0o755)
		write(t, filepath.Join(d, "g", "s", "t", "y.txt"), "y\n")
		write(t, filepath.Join(d, "g", "z.txt"), "z\n")
	}
	cand := func(d string) { os.RemoveAll(filepath.Join(d, "g")); write(t, filepath.Join(d, "g"), "now a file\n") }
	f := newOwnerFixtureWith(t, base, cand)
	resetHooks(t)
	return f
}

// displacedDir answers the temporary name the directory g stands under after the exchange.
func displacedDir(t *testing.T, d string) string {
	t.Helper()
	all, _ := filepath.Glob(filepath.Join(d, ".tillr-*.tmp"))
	var found []string
	for _, p := range all {
		if st, err := os.Lstat(p); err == nil && st.IsDir() {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("displaced directories: %v, want one", found)
	}
	return found[0]
}

func TestADirectoryIsReplacedByOneExchangeAndLeavesNothingBehind(t *testing.T) {
	f := dirToFile(t)
	d := f.f.dir
	var during []string
	land.OwnerHooks.AfterInstall = func() {
		tmp := displacedDir(t, d)
		for _, p := range []string{"z.txt", "s/t/y.txt"} {
			if _, err := os.Stat(filepath.Join(tmp, p)); err != nil {
				t.Errorf("the displaced directory does not hold %s whole: %v", p, err)
			}
		}
		inner, _ := filepath.Glob(filepath.Join(tmp, "*", "*", ".tillr-*"))
		top, _ := filepath.Glob(filepath.Join(tmp, ".tillr-*"))
		during = append(inner, top...)
	}
	if l := mustLand(t, f); !l.Landed() {
		t.Fatalf("landing: %+v", l)
	}
	if len(during) != 0 {
		t.Errorf("the directory was emptied file by file: %v", during)
	}
	if left, _ := filepath.Glob(filepath.Join(d, ".tillr-*")); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if statOf(t, filepath.Join(d, "g")).body != "now a file\n" || git(t, d, "status", "--porcelain") != "" {
		t.Errorf("status %q", git(t, d, "status", "--porcelain"))
	}
}

func TestAFileAppearingInADisplacedDirectoryBeforeTheTrunkMovesIsPutBack(t *testing.T) {
	for name, when := range map[string]func(func()){
		"before the exchange": func(save func()) {
			land.OwnerHooks.BeforeSwap = func(p string) {
				if p == "g" {
					save()
				}
			}
		},
		"after the exchange": func(save func()) { land.OwnerHooks.AfterInstall = save },
	} {
		t.Run(name, func(t *testing.T) {
			f := dirToFile(t)
			d := f.f.dir
			saved := false
			when(func() {
				if saved {
					return
				}
				saved = true
				dir := filepath.Join(d, "g")
				if st, err := os.Stat(dir); err != nil || !st.IsDir() {
					dir = displacedDir(t, d)
				}
				write(t, filepath.Join(dir, "new.txt"), "mine\n")
			})
			l := mustLand(t, f)
			wantHeld(t, l, "g", "g/new.txt")
			for p, want := range map[string]string{"g/new.txt": "mine\n", "g/z.txt": "z\n", "g/s/t/y.txt": "y\n"} {
				if got := statOf(t, filepath.Join(d, p)).body; got != want {
					t.Errorf("%s = %q, want %q", p, got, want)
				}
			}
			if left, _ := filepath.Glob(filepath.Join(d, ".tillr-*")); len(left) != 0 {
				t.Errorf("left behind: %v", left)
			}
			if tip := git(t, d, "rev-parse", "HEAD"); tip != f.f.baseSHA {
				t.Errorf("the trunk moved to %s", tip)
			}
		})
	}
}

func TestAFileAppearingInADisplacedDirectoryAfterItsLastListingIsKeptWithAFault(t *testing.T) {
	f := dirToFile(t)
	d := f.f.dir
	var kept string
	land.OwnerHooks.BeforeTrunkMove = func() {
		kept = filepath.Join(displacedDir(t, d), "new.txt")
		write(t, kept, "mine\n")
	}
	l, err := land.Land(context.Background(), f.s, f.trunk, f.req)
	if err != nil || !l.Landed() {
		t.Fatalf("landing: %+v, %v", l, err)
	}
	if got := statOf(t, kept).body; got != "mine\n" {
		t.Fatalf("the owner's file is gone: %q", got)
	}
	if len(l.Faults) != 1 || !strings.Contains(l.Faults[0], filepath.Base(filepath.Dir(kept))) {
		t.Fatalf("faults = %v, want one naming %s", l.Faults, filepath.Dir(kept))
	}
	if n := eventsOfKind(t, f.db, land.FaultKind); n != 1 {
		t.Fatalf("fault events = %d, want 1", n)
	}
	if statOf(t, filepath.Join(d, "g")).body != "now a file\n" {
		t.Error("the landed file is not at the path")
	}
	for _, p := range []string{"z.txt", "s"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(kept), p)); err == nil {
			t.Errorf("%s, which the landing deleted, is still in the displaced directory", p)
		}
	}
}
