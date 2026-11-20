package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The green pointer and the common directory's tillr-gate/ are one shared place: every
// linked worktree of a repository reads the same refs/tillr/green and the same proof
// files. A proof that moves the pointer therefore speaks for the whole repository, and
// only one writer may: the after-merge proof of the loop that holds the repository's
// loop lock. A proof started from a lane worktree, or by anything that holds no loop
// lock, refuses before it judges, so a stub gate program run from a lane cannot prove
// the real trunk.

// LoopLock is the proof of holding the repository's loop lock: an exclusive flock on
// tillr-loop.lock in the git common directory, which sits beside tillr-gate/ and not in
// it. The descriptor is close-on-exec, so a loop that replaces itself releases it.
type LoopLock struct {
	file   *os.File
	common string
}

// Close releases the lock. A nil lock is fine to close.
func (l *LoopLock) Close() {
	if l != nil && l.file != nil {
		l.file.Close()
	}
}

// acquireLoopLock takes the root's loop lock. busy is true, with a nil error, when
// another process holds it. A linked worktree can never hold it.
func acquireLoopLock(ctx context.Context, root string) (lock *LoopLock, busy bool, err error) {
	common, err := proofCommonDir(ctx, root)
	if err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(filepath.Join(common, "tillr-loop.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return nil, true, nil
		}
		return nil, false, err
	}
	return &LoopLock{file: file, common: common}, false, nil
}

// proofCommonDir answers root's git common directory, refusing a linked worktree: the
// refusal names the worktree, because the pointer it would move is not the worktree's.
func proofCommonDir(ctx context.Context, root string) (string, error) {
	gitDir, err := heavyGitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return "", err
	}
	common, err := heavyGitOutput(ctx, root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !sameDir(gitDir, common) {
		return "", Usagef("heavy proof refused: %s is a linked worktree of %s, and %s and its proof files are shared with every worktree; only the loop run from the repository itself may prove the trunk", root, strings.TrimSuffix(filepath.Clean(common), "/.git"), GreenRef)
	}
	if guard := heavyCommonGuard; guard != nil {
		if err := guard(common); err != nil {
			return "", err
		}
	}
	return common, nil
}

func sameDir(a, b string) bool {
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// heavyCommonGuard is a seam for the suite: it is nil in the program, and the tests set
// it to fail any proof or lock that would reach the real repository's common directory.
var heavyCommonGuard func(common string) error

// authorize refuses a runner that may not prove this root, before it judges anything.
func (r HeavyRunner) authorize(ctx context.Context) error {
	common, err := proofCommonDir(ctx, r.Root)
	if err != nil {
		return err
	}
	if r.Lock == nil || r.Lock.file == nil || !sameDir(r.Lock.common, common) {
		return Usagef("heavy proof refused: the process does not hold the loop lock of %s; only the after-merge proof of the loop that holds it may move %s or write its proof files", r.Root, GreenRef)
	}
	return nil
}
