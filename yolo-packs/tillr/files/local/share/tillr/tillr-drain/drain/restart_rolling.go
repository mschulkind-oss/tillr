package drain

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Rolling checks use the same owner-pipe supervision as heavy checks. The
// writer is close-on-exec, so a failed exec keeps ownership and a successful
// replacement ends it without relying on an old image's cancellation goroutine.
func rollingOwnership(ctx context.Context, root string) (context.Context, func(), error) {
	dir, err := heavyDirectory(ctx, root)
	if err != nil {
		return ctx, func() {}, err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return ctx, func() {}, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "heavy.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return ctx, func() {}, err
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			lock.Close()
			return ctx, func() {}, err
		}
		if !waitFor(ctx, 10*time.Millisecond) {
			lock.Close()
			return ctx, func() {}, ctx.Err()
		}
	}
	guard := filepath.Join(dir, heavyCleanupGuard)
	unconfirmed, err := recoverHeavyCleanupGuards(ctx, root, dir)
	if err != nil {
		lock.Close()
		return ctx, func() {}, err
	}
	if unconfirmed != "" {
		lock.Close()
		return ctx, func() {}, Externalf("rolling cleanup not acknowledged; checkout retained: %s", unconfirmed)
	}
	owner, life, err := os.Pipe()
	if err != nil {
		lock.Close()
		return ctx, func() {}, err
	}
	cleanup := func() { owner.Close(); life.Close(); lock.Close() }
	return context.WithValue(ctx, heavyOwnershipKey{}, heavyOwnership{Lock: lock, Owner: owner, Guard: guard}), cleanup, nil
}
