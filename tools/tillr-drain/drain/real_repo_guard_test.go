package drain

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
)

// The suite's scratch lives under the real repository's working tree, so a fixture that
// forgets `git init` resolves to the real repository: its proof then judges the real
// trunk and moves the real refs/tillr/green (measured 2026-11-20). This guard fails any
// proof, loop lock or fixer path that resolves to the real common directory.
func init() {
	installRealRepoGuard()
}

func installRealRepoGuard() {
	wd, _ := os.Getwd()
	guarded := realCommonDirs(wd, os.Getenv("YOLO_DURABLE_DIR"), os.Getenv("TMPDIR"))
	if len(guarded) == 0 {
		heavyrun.CommonGuard = nil
		return
	}
	heavyrun.CommonGuard = func(common string) error {
		if guarded[sameDirKey(common)] {
			return fmt.Errorf("test reached the real repository's common directory %s: give the fixture its own repository", common)
		}
		return nil
	}
}

// loopHeld takes root's loop lock for the test, as the loop does around a proof.
func loopHeld(t testing.TB, root string) *LoopLock {
	t.Helper()
	key := t.Name() + "\x00" + root
	if held, ok := heldLoopLocks.Load(key); ok {
		return held.(*LoopLock)
	}
	lock, busy, err := acquireLoopLock(context.Background(), root)
	if err != nil || busy {
		t.Fatalf("loop lock for %s: busy=%v err=%v", root, busy, err)
	}
	heldLoopLocks.Store(key, lock)
	t.Cleanup(func() { heldLoopLocks.Delete(key); lock.Close() })
	return lock
}

var heldLoopLocks sync.Map
