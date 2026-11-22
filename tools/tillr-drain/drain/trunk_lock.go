package drain

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
			file, acquired, err := acquireHistoryLock(path)
			guard.Close()
			if err != nil {
				return nil, Externalf("locking trunk history: %v", err)
			}
			if acquired {
				return func() { file.Close() }, nil
			}
		} else {
			guard.Close()
			if !lockContended(guardErr) {
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
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// acquireHistoryLock runs only under the takeover guard, with no SQLite writer.
func acquireHistoryLock(path string) (*os.File, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if !lockContended(err) {
			return nil, false, err
		}
		if !historyHolderDead(path) {
			return nil, false, nil
		}
		aside := fmt.Sprintf("%s.stale.%d.%d", path, os.Getpid(), time.Now().UnixNano())
		if err := os.Rename(path, aside); err != nil {
			return nil, false, err
		}
		file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0644)
		if err != nil {
			return nil, false, err
		}
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			file.Close()
			return nil, false, err
		}
		if err := os.Remove(aside); err != nil {
			file.Close()
			return nil, false, err
		}
	}
	mine, mineErr := file.Stat()
	current, currentErr := os.Stat(path)
	if mineErr != nil || currentErr != nil || !os.SameFile(mine, current) {
		file.Close()
		return nil, false, fmt.Errorf("history lock file changed during acquisition")
	}
	host, err := os.Hostname()
	if err != nil {
		file.Close()
		return nil, false, err
	}
	record, err := json.Marshal(map[string]any{"kind": "tillr-drain-history", "pid": os.Getpid(), "host": host, "at": time.Now().UnixMilli()})
	if err == nil {
		err = file.Truncate(0)
	}
	if err == nil {
		_, err = file.WriteAt(append(record, '\n'), 0)
	}
	if err != nil {
		file.Close()
		return nil, false, err
	}
	return file, true, nil
}

// Only a provably dead local holder can be replaced. Match the lander's JSON
// and legacy plain-PID records, including its treatment of zombies as dead.
func historyHolderDead(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var record struct {
		PID  int    `json:"pid"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		record.PID, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	host, err := os.Hostname()
	if err != nil || record.PID <= 0 || (record.Host != "" && record.Host != host) {
		return false
	}
	err = syscall.Kill(record.PID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil {
		return false
	}
	if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", record.PID)); err == nil {
		text := string(raw)
		if end := strings.LastIndexByte(text, ')'); end >= 0 {
			return strings.HasPrefix(strings.TrimSpace(text[end+1:]), "Z")
		}
	}
	return false
}
