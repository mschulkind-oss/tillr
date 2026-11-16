package land

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// The land lock: the writer's mutual exclusion across processes, and the one thing that
// makes design D-UT3's "one writer for candidate state" true. The protocol is a JSON
// holder record over an exclusive flock, with a stale-pid takeover, and the file lives
// beside the repository's common git directory so every landing path in a checkout —
// a hand-run `tillr-land one`, a drain, a loop — serializes on one file instead of racing
// on one trunk.
//
// The descriptor IS the lock. The kernel drops the flock when the descriptor closes,
// so a lander killed at any instant leaves nothing to reclaim: that is why there is no
// lock directory with a pid file, which a process killed between mkdir and write would
// leave for the next caller to guess about.
//
// The record inside the file does not decide ownership; it lets a blocked caller tell a
// live holder from a provably dead one. A killed lander's flock normally goes with it,
// but a child that inherited the descriptor can outlive it — the 2026-10-28 stall that
// mq's lock documents — so a recorded pid that no longer exists is taken over, while a
// live recorded pid is never preempted.
type Lock struct {
	path   string
	file   *os.File
	held   bool
	pollBy time.Duration
	now    func() time.Time
}

// OpenLock answers the lock at path. The path is configuration, never derived here: a
// caller whose landings also pass through another lander names that lander's lock file
// and the two then serialize.
func OpenLock(path string) (*Lock, error) {
	if path == "" {
		return nil, Usagef("a lock path is required")
	}
	return &Lock{path: path, pollBy: 200 * time.Millisecond, now: time.Now}, nil
}

// Path answers the lock file this lock takes.
func (l *Lock) Path() string { return l.path }

// Held reports whether this process owns the lock. Release is a no-op otherwise, so a
// deferred Release after a failed Acquire is safe.
func (l *Lock) Held() bool { return l.held }

func lockGuardPath(path string) string { return path + ".guard" }

// lockRecord reads a holder record written by either lander. The queue's record and
// this one differ only in `kind`, so one reader serves both: that is the whole of the
// protocol the two implementations share.
func lockRecord(data []byte) map[string]any {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(trimmed), &record); err == nil && record != nil {
		return record
	}
	if pid, err := strconv.Atoi(trimmed); err == nil && pid > 0 {
		return map[string]any{"kind": "plain-pid", "pid": float64(pid)}
	}
	return nil
}

func lockPid(record map[string]any) (int, bool) {
	switch value := record["pid"].(type) {
	case float64:
		return int(value), value > 0
	case int:
		return value, value > 0
	case json.Number:
		pid, err := strconv.Atoi(string(value))
		return pid, err == nil && pid > 0
	}
	return 0, false
}

func lockProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return err != syscall.ESRCH
	}
	// A zombie has closed every descriptor already, so it no longer holds the lock.
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)); err == nil {
		text := string(data)
		if end := strings.LastIndexByte(text, ')'); end >= 0 {
			if strings.HasPrefix(strings.TrimSpace(text[end+1:]), "Z") {
				return false
			}
		}
	}
	return true
}

func lockHostname() string {
	host, err := os.Hostname()
	if err != nil {
		return ""
	}
	return host
}

// staleLock answers the record of a holder that is provably gone: a dead pid, or one
// recorded on another host (whose liveness this process cannot judge, so it is never
// declared dead).
func staleLock(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	record := lockRecord(data)
	if record == nil {
		return nil, false
	}
	pid, ok := lockPid(record)
	if !ok || lockProcessAlive(pid) {
		return nil, false
	}
	if host, ok := record["host"].(string); ok && host != "" && host != lockHostname() {
		return nil, false
	}
	return record, true
}

// takeOverLock renames a stale lock aside and takes a fresh one under the same path. The
// rename is what lets the fresh inode be locked without the dead holder's descriptor
// mattering, and it is removed again once the new lock is held.
func takeOverLock(path string) (*os.File, map[string]any, bool) {
	stale, ok := staleLock(path)
	if !ok {
		return nil, nil, false
	}
	aside := fmt.Sprintf("%s.stale.%d.%d", path, os.Getpid(), time.Now().UnixNano())
	if err := os.Rename(path, aside); err != nil {
		return nil, nil, false
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o644)
	if err != nil {
		_ = os.Remove(aside)
		return nil, nil, false
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		_ = os.Remove(aside)
		return nil, nil, false
	}
	_ = os.Remove(aside)
	return file, stale, true
}

// writeLockRecord names this lander in the locked file. The record is a note for a
// reader — who holds the lock, and since when — and never what decides ownership.
func (l *Lock) writeLockRecord(stale map[string]any) {
	record := map[string]any{
		"kind": "tillr-land",
		"pid":  os.Getpid(),
		"host": lockHostname(),
		"at":   l.now().UnixMilli(),
	}
	if stale != nil {
		pid, _ := lockPid(stale)
		record["took_over"] = map[string]any{
			"kind": stale["kind"],
			"pid":  stale["pid"],
			"at":   stale["at"],
			"why":  fmt.Sprintf("pid %d no longer exists", pid),
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	if l.file.Truncate(0) == nil {
		_, _ = l.file.WriteAt(append(data, '\n'), 0)
	}
}

// Acquire waits until this process owns the lock and answers how long it waited. A
// timeout of zero waits until the context ends: a caller that names no bound wants the
// landing to happen, not to give up.
func (l *Lock) Acquire(ctx context.Context, timeout time.Duration) (time.Duration, error) {
	start := l.now()
	if dir := filepath.Dir(l.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 0, Externalf("creating the lock directory: %v", err)
		}
	}
	for {
		// The guard serializes the takeover itself: two callers that both see the same
		// stale record must not both rename it aside and both believe they won.
		guard, err := os.OpenFile(lockGuardPath(l.path), os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return l.now().Sub(start), Externalf("opening the lock guard: %v", err)
		}
		if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX); err != nil {
			_ = guard.Close()
			return l.now().Sub(start), Externalf("locking the lock guard: %v", err)
		}
		file, stale, acquired, err := l.tryAcquire()
		if err == nil && acquired {
			l.file = file
			l.held = true
			l.writeLockRecord(stale)
		}
		_ = syscall.Flock(int(guard.Fd()), syscall.LOCK_UN)
		_ = guard.Close()
		if err != nil {
			if file != nil {
				_ = file.Close()
			}
			return l.now().Sub(start), err
		}
		if acquired {
			return l.now().Sub(start), nil
		}
		if file != nil {
			_ = file.Close()
		}
		waited := l.now().Sub(start)
		if timeout > 0 && waited > timeout {
			return waited, Externalf("the land lock at %s is held by %s; waited %s",
				l.path, l.holder(), waited.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return waited, Externalf("waiting for the land lock at %s: %v", l.path, ctx.Err())
		case <-time.After(l.pollBy):
		}
	}
}

// tryAcquire is one attempt under the guard: a held descriptor, the stale record it
// displaced, and whether this process owns the lock. A live blocker answers
// acquired=false with no error, which is the wait.
func (l *Lock) tryAcquire() (*os.File, map[string]any, bool, error) {
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, nil, false, Externalf("opening the land lock: %v", err)
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	var stale map[string]any
	if err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, nil, false, Externalf("locking %s: %v", l.path, err)
		}
		fresh, displaced, ok := takeOverLock(l.path)
		if !ok {
			return file, nil, false, nil
		}
		_ = file.Close()
		file = fresh
		stale = displaced
	}
	// The descriptor this process holds must be the file at the path: a lock renamed
	// aside under us is a lock someone else now holds.
	mine, mineErr := file.Stat()
	current, currentErr := os.Stat(l.path)
	if mineErr != nil || currentErr != nil || !os.SameFile(mine, current) {
		_ = file.Close()
		return nil, nil, false, nil
	}
	return file, stale, true, nil
}

// holder names whoever wrote itself into the lock file, for a message only.
func (l *Lock) holder() string {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return "another landing"
	}
	if record := lockRecord(data); record != nil {
		if pid, ok := lockPid(record); ok {
			return fmt.Sprintf("pid %d", pid)
		}
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
		return fmt.Sprintf("pid %d", pid)
	}
	return "another landing"
}

// Release answers the lock. Closing the descriptor is the release: the lock goes with
// it, whether this process asked for that or died.
func (l *Lock) Release() {
	if !l.held || l.file == nil {
		return
	}
	_ = l.file.Close()
	l.file = nil
	l.held = false
}
