package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
)

// lockTrunkHistory follows the lander's land.lock protocol: the guard serializes
// acquisition and replacement of a dead holder's file, while a descriptor lock
// provides mutual exclusion. Publish a live PID before releasing the guard so
// the lander can wait with cancellation rather than taking over our descriptor.
// Importing the landing package would also register its schema in the drain.
func (l *Loop) lockTrunkHistory(ctx context.Context) (func(), error) {
	return l.acquireTrunkHistory(ctx, true)
}

// Without wait, a nil release and nil error means another holder won. The lock
// protocol and mutual exclusion are identical when acquisition succeeds.
func (l *Loop) acquireTrunkHistory(ctx context.Context, wait bool) (func(), error) {
	raw, err := l.git(ctx, l.cfg.Root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(strings.TrimSpace(raw), "land.lock")
	for {
		guard, err := os.OpenFile(path+".guard", os.O_CREATE|os.O_RDWR, 0644)
		if err != nil {
			return nil, Externalf("opening history lock guard: %v", err)
		}
		guardErr := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if guardErr == nil {
			file, acquired, err := fixer.AcquireHistoryLock(path)
			guard.Close()
			if err != nil {
				return nil, Externalf("locking trunk history: %v", err)
			}
			if acquired {
				return func() { file.Close() }, nil
			}
		} else {
			guard.Close()
			if !fixer.LockContended(guardErr) {
				return nil, Externalf("locking trunk history guard: %v", guardErr)
			}
		}
		if !wait {
			return nil, nil
		}
		if !waitFor(ctx, 20*time.Millisecond) {
			return nil, Externalf("waiting for trunk history lock: %v", ctx.Err())
		}
	}
}

func lockContended(err error) bool {
	return fixer.LockContended(err)
}

func acquireHistoryLock(path string) (*os.File, bool, error) {
	return fixer.AcquireHistoryLock(path)
}

func historyHolderDead(path string) bool {
	return fixer.HistoryHolderDead(path)
}
