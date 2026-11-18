package drain

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestHistoryObservationUsesLandersCrossProcessLock(t *testing.T) {
	f, first := passFixture(t)
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(f.root, "later.txt"), "later\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "later")
	lost := runGit(t, f.root, "rev-parse", "HEAD")
	if _, err := first.observeTrunk(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "reset", "--hard", base)
	if h, err := first.observeTrunk(context.Background(), ""); err != nil || h.Lost != lost {
		t.Fatalf("rewrite: %+v %v", h, err)
	}
	// Hold the same descriptor the lander holds after releasing its guard.
	common := runGit(t, f.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	lock, err := os.OpenFile(filepath.Join(common, "land.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	// Simulate the landing writer's exclusive section. A separate Loop must not
	// replace/delete shared refs based on its snapshot before obtaining that lock.
	writeForkFile(t, filepath.Join(f.root, "other.txt"), "other\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "other")
	replacement := f.loop(t, &dirtyPathSteps{t: t, root: f.root, store: f.storePath}, &itemContentStager{})
	// The short deadline only ends the observer's wait for a lock that stays held; the
	// assertions (an error, no ref moved) are the same wherever in the attempt it falls, so
	// load cannot change the verdict.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := replacement.observeTrunk(ctx, ""); err == nil {
		t.Fatal("observer bypassed landing lock")
	}
	if got := runGit(t, f.root, "rev-parse", observedTrunkRef); got != base {
		t.Fatalf("cancelled observer moved observed ref: %s", got)
	}
	if got := runGit(t, f.root, "rev-parse", lostTrunkRef); got != lost {
		t.Fatalf("cancelled observer moved lost ref: %s", got)
	}
	runGit(t, f.root, "reflog", "expire", "--expire=now", "--all")
	runGit(t, f.root, "gc", "--prune=now")
	runGit(t, f.root, "cat-file", "-e", lost+"^{commit}")
	lock.Close()
	if h, err := replacement.observeTrunk(context.Background(), ""); err != nil || h.Lost != lost {
		t.Fatalf("replacement forgot lost history: %+v %v", h, err)
	}
}
