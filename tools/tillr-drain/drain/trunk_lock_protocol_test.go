package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestHistoryLockPublishesHolderAndReleasesGuard(t *testing.T) {
	f, l := passFixture(t)
	release, err := l.lockTrunkHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	common := runGit(t, f.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	path := filepath.Join(common, "land.lock")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		PID  int    `json:"pid"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	if record.PID != os.Getpid() || record.Host != host || historyHolderDead(path) {
		t.Fatalf("not a live local holder: %s", raw)
	}
	guard, err := os.OpenFile(path+".guard", os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	// The lander takes this guard before consulting context or timeout. It must
	// be available while the history observer still holds the descriptor lock.
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("observer blocks lander cancellation: %v", err)
	}
	other, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !lockContended(err) {
		t.Fatalf("descriptor not mutually exclusive: %v", err)
	}
}

func TestHistoryLockRecoversDeadHolderButNotUnknownOrForeign(t *testing.T) {
	dead := exec.Command("true")
	if err := dead.Start(); err != nil {
		t.Fatal(err)
	}
	pid := dead.Process.Pid
	if err := dead.Wait(); err != nil {
		t.Fatal(err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"dead-json", "dead-plain", "foreign", "live", "unknown"} {
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t)
			common := runGit(t, f.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
			path := filepath.Join(common, "land.lock")
			leftover, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
			if err != nil {
				t.Fatal(err)
			}
			defer leftover.Close()
			if err := syscall.Flock(int(leftover.Fd()), syscall.LOCK_EX); err != nil {
				t.Fatal(err)
			}
			record := map[string]any{"pid": pid, "host": host}
			if name == "foreign" {
				record["host"] = host + "-elsewhere"
			}
			if name == "live" {
				record["pid"] = os.Getpid()
			}
			raw, _ := json.Marshal(record)
			if name == "dead-plain" {
				raw = []byte(strconv.Itoa(pid))
			}
			if name == "unknown" {
				raw = []byte("not a holder record")
			}
			if _, err := leftover.WriteAt(raw, 0); err != nil {
				t.Fatal(err)
			}
			before, _ := leftover.Stat()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			release, err := l.lockTrunkHistory(ctx)
			if name == "dead-json" || name == "dead-plain" {
				if err != nil {
					t.Fatalf("dead holder not recovered: %v", err)
				}
				defer release()
				after, err := os.Stat(path)
				if err != nil || os.SameFile(before, after) {
					t.Fatalf("dead descriptor still owns current file: %v", err)
				}
			} else {
				if err == nil {
					release()
					t.Fatal("unproven-dead holder taken over")
				}
				after, err := os.Stat(path)
				if err != nil || !os.SameFile(before, after) {
					t.Fatalf("live/foreign/unknown descriptor replaced: %v", err)
				}
			}
		})
	}
}
