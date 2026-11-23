package fixer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAcquireHistoryLockAndHistoryHolderDead(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "land.lock")

	// Live process (our own pid) is not dead.
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	livePayload, err := json.Marshal(map[string]any{"kind": "tillr-drain-history", "pid": os.Getpid(), "host": host})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, livePayload, 0o644); err != nil {
		t.Fatal(err)
	}
	if HistoryHolderDead(lockPath) {
		t.Fatal("HistoryHolderDead returned true for current live pid")
	}

	// Non-existent pid is dead.
	deadPayload, err := json.Marshal(map[string]any{"kind": "tillr-drain-history", "pid": 99999999, "host": host})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, deadPayload, 0o644); err != nil {
		t.Fatal(err)
	}
	if !HistoryHolderDead(lockPath) {
		t.Fatal("HistoryHolderDead returned false for non-existent pid")
	}

	// AcquireHistoryLock succeeds and writes our PID/host.
	file, acquired, err := AcquireHistoryLock(lockPath)
	if err != nil || !acquired {
		t.Fatalf("AcquireHistoryLock = (%v, %v, %v)", file, acquired, err)
	}
	defer file.Close()

	// Contended flock returns EWOULDBLOCK/EAGAIN.
	other, err := os.OpenFile(lockPath, os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !LockContended(err) {
		t.Fatalf("expected LockContended(err) = true, got err=%v", err)
	}
}

func TestReadTrunkHistoryAndTrunkAncestor(t *testing.T) {
	_, s := openTestStore(t)
	ctx := context.Background()

	// Missing table returns zero TrunkHistory without error.
	var h TrunkHistory
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		h, err = ReadTrunkHistory(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if h != (TrunkHistory{}) {
		t.Fatalf("ReadTrunkHistory(missing table) = %+v", h)
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(TrunkHistoryDDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_history(id, observed_tip, lost_tip) VALUES(1, 'aaa', 'bbb')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		h, err = ReadTrunkHistory(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if h.Observed != "aaa" || h.Lost != "bbb" || h.Reason() != fmt.Sprintf("%s: bbb -> aaa", ReasonTrunkRewritten) {
		t.Fatalf("ReadTrunkHistory = %+v, reason=%q", h, h.Reason())
	}

	repo := initTestRepo(t)
	c1 := runGitTest(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "a.txt")
	runGitTest(t, repo, "commit", "-m", "a")
	c2 := runGitTest(t, repo, "rev-parse", "HEAD")

	ok, err := TrunkAncestor(ctx, repo, c1, c2)
	if err != nil || !ok {
		t.Fatalf("TrunkAncestor(c1, c2) = (%v, %v)", ok, err)
	}
	ok, err = TrunkAncestor(ctx, repo, c2, c1)
	if err != nil || ok {
		t.Fatalf("TrunkAncestor(c2, c1) = (%v, %v)", ok, err)
	}
}
