package land_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
)

// writeHolder writes a holder record under an exclusive flock and answers the open
// descriptor: the state a lander killed with a surviving child leaves behind, and the
// state another lander's lock is in while it holds it. The descriptor is the lock, so a
// test that cannot hold one cannot control who the holder is.
func holdLock(t *testing.T, path string, record map[string]any) *os.File {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("cannot open the lock: %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		t.Fatalf("cannot take the lock: %v", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("cannot encode the holder record: %v", err)
	}
	if _, err := file.WriteAt(append(encoded, '\n'), 0); err != nil {
		_ = file.Close()
		t.Fatalf("cannot write the holder record: %v", err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func readHolder(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read the lock record: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("the lock record is not JSON: %v\n%s", err, data)
	}
	return record
}

// A live holder is waited for and never preempted: the second lander exits with the
// bound's error naming the holder, and the holder's record is left exactly as it was.
// The negative control is the waiter's own error — with the first lock released the same
// acquire succeeds.
func TestALiveLandLockIsWaitedForAndNeverPreempted(t *testing.T) {
	path := filepath.Join(scratch(t), "land.lock")
	ctx := context.Background()

	first, err := land.OpenLock(path)
	if err != nil {
		t.Fatalf("opening the first lock failed: %v", err)
	}
	if _, err := first.Acquire(ctx, 0); err != nil {
		t.Fatalf("the first acquire failed: %v", err)
	}
	if !first.Held() {
		t.Fatal("the first lock reports it is not held")
	}
	if pid, _ := readHolder(t, path)["pid"].(float64); int(pid) != os.Getpid() {
		t.Fatalf("the lock record does not name this process: %v", readHolder(t, path))
	}

	second, err := land.OpenLock(path)
	if err != nil {
		t.Fatalf("opening the second lock failed: %v", err)
	}
	if _, err := second.Acquire(ctx, 100*time.Millisecond); err == nil {
		t.Fatal("a live holder was preempted")
	} else if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("the refusal does not name the live holder: %v", err)
	}
	if second.Held() {
		t.Fatal("the refused lock reports it is held")
	}
	if _, took := readHolder(t, path)["took_over"]; took {
		t.Fatalf("the live holder's record carries a takeover: %v", readHolder(t, path))
	}

	first.Release()
	if _, err := second.Acquire(ctx, time.Second); err != nil {
		t.Fatalf("the lock could not be taken after the holder released it: %v", err)
	}
	second.Release()
}

// A record the retired merge queue wrote is still a holder: the lock file outlived that
// lander, and a lock left held by a process that no longer exists must be waited for (or
// taken over once its pid is provably dead) rather than overwritten. The negative control
// is the holder name: a record this lander could not parse would answer "another
// landing".
func TestTheLandLockReadsTheMergeQueuesRecord(t *testing.T) {
	path := filepath.Join(scratch(t), "land.lock")
	host, _ := os.Hostname()
	holdLock(t, path, map[string]any{
		"kind": "mq-land", "pid": os.Getpid(), "at": time.Now().UnixMilli(), "host": host,
	})

	lock, err := land.OpenLock(path)
	if err != nil {
		t.Fatalf("opening the lock failed: %v", err)
	}
	_, err = lock.Acquire(context.Background(), 100*time.Millisecond)
	if err == nil {
		t.Fatal("the queue's live lock was preempted")
	}
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("the queue's record was not read as a holder: %v", err)
	}
}

// The 2026-10-28 stall: a surviving child keeps the flock while the record names a
// lander that no longer exists. A recorded pid that is provably gone is taken over, and
// the takeover names the stale holder so the next reader can see who it displaced.
func TestTheLandLockTakesOverADeadHolder(t *testing.T) {
	path := filepath.Join(scratch(t), "land.lock")
	dead := exec.Command("sh", "-c", "exit 0")
	if err := dead.Run(); err != nil {
		t.Fatalf("cannot create a dead pid: %v", err)
	}
	deadPID := dead.Process.Pid
	holdLock(t, path, map[string]any{
		"kind": "board-land", "pid": deadPID, "at": time.Now().UnixMilli(),
	})

	lock, err := land.OpenLock(path)
	if err != nil {
		t.Fatalf("opening the lock failed: %v", err)
	}
	if _, err := lock.Acquire(context.Background(), time.Second); err != nil {
		t.Fatalf("a dead holder wedged the landing: %v", err)
	}
	defer lock.Release()
	record := readHolder(t, path)
	if pid, _ := record["pid"].(float64); int(pid) != os.Getpid() {
		t.Fatalf("the lock still names the dead holder: %v", record)
	}
	took, ok := record["took_over"].(map[string]any)
	if !ok {
		t.Fatalf("the takeover was not recorded: %v", record)
	}
	if pid, _ := took["pid"].(float64); int(pid) != deadPID {
		t.Fatalf("the takeover names pid %v, want %d: %v", took["pid"], deadPID, record)
	}
	if why, _ := took["why"].(string); !strings.Contains(why, "no longer exists") {
		t.Fatalf("the takeover does not say why: %v", record)
	}
}
