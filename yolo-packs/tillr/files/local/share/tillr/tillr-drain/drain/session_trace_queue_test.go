package drain

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A full trace queue must never hold the drain's real sessionMu. The sink is stalled
// first, then the session is refreshed -- store.Open plus a WriteTx, all under
// sessionMu -- until the store reports that it dropped records, and a few times more,
// so several refreshes enqueue into a queue that is already full. Afterwards the
// stall is released and the trace must say, in its own output, that it lost records.
func TestFullTraceQueueDoesNotRetainRealSessionMutexAndReportsTheLoss(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	_, l := passFixture(t, "item-a")
	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	var mu sync.Mutex
	var captured bytes.Buffer
	var releaseOnce sync.Once
	copied := make(chan struct{})
	release := func() {
		releaseOnce.Do(func() {
			go func() {
				defer close(copied)
				buf := make([]byte, 64<<10)
				for {
					n, err := reader.Read(buf)
					mu.Lock()
					captured.Write(buf[:n])
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
		})
	}
	defer release()
	t.Cleanup(func() { release(); os.Stderr = original; writer.Close(); <-copied; reader.Close() })
	fd := int(writer.Fd())
	if err := syscall.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := syscall.Write(fd, make([]byte, 4096))
		if err == syscall.EAGAIN {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	syscall.SetNonblock(fd, false)
	baseline := store.SQLiteTraceDeliveryStatus().DroppedRecords
	t.Setenv("TILLR_SQLITE_TRACE", "1")

	const limit, afterFull = 2000, 3
	done := make(chan error, 1)
	go func() {
		extra := -1
		for i := 0; i < limit && extra < afterFull; i++ {
			if err := l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)); err != nil {
				done <- err
				return
			}
			if extra >= 0 {
				extra++
			} else if store.SQLiteTraceDeliveryStatus().DroppedRecords > baseline {
				extra = 0
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		held := !l.sessionMu.TryLock()
		if !held {
			l.sessionMu.Unlock()
		}
		release()
		<-done
		t.Fatalf("a full trace queue blocked refreshSession (sessionMu held: %v)", held)
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	stalled := store.SQLiteTraceDeliveryStatus()
	if stalled.DroppedRecords == baseline {
		t.Fatalf("%d refreshes never filled the trace queue: %+v", limit, stalled)
	}
	free := make(chan struct{})
	go func() { l.sessionMu.Lock(); l.sessionMu.Unlock(); close(free) }()
	select {
	case <-free:
	case <-time.After(10 * time.Second):
		release()
		<-free
		t.Fatal("a full trace queue retained the actual sessionMu")
	}

	release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	final, _ := store.FlushSQLiteTrace(ctx)
	cancel()
	if !final.LossReported {
		t.Fatalf("the flush returned before the loss was reported: %+v", final)
	}
	mu.Lock()
	out := captured.String()
	mu.Unlock()
	reported := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "tillr-sqlite ") && strings.Contains(line, " dropped ") && !strings.Contains(line, " records=0 ") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("the trace never reported its dropped records: %+v", final)
	}
}
