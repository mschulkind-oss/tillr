package drain

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	"modernc.org/sqlite"
)

func TestBlockedTraceSinkDoesNotRetainRealSessionMutex(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	restore, err := store.SetSQLiteTraceSlowSpan(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	f, l := passFixture(t, "item-a")
	if err := l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	entered, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(proceed) }) }
	defer unblock()
	name := fmt.Sprintf("blocked_session_trace_%d", time.Now().UnixNano())
	if err := sqlite.RegisterScalarFunction(name, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		close(entered)
		<-proceed
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("CREATE TRIGGER pause_session BEFORE UPDATE ON drain_sessions BEGIN SELECT " + name + "(); END;")
		return err
	})
	other, err := store.Open(context.Background(), f.storePath, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { go io.Copy(io.Discard, reader) }) }
	defer release()
	t.Cleanup(func() { release(); os.Stderr = original; writer.Close(); reader.Close() })
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	operation := runtime.FuncForPC(reflect.ValueOf((*Loop).refreshSession).Pointer()).Name()
	result := make(chan error, 1)
	go func() { result <- l.refreshSession(context.Background(), l.cfg.Now().Add(PassInterval)) }()
	<-entered
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
	// Wait for an actual snapshot handoff, not an estimate of how long sampling takes.
	// The full pipe prevents delivery while the real session write remains blocked.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !store.SQLiteTraceSampleQueued(f.storePath, operation) {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("session trace never queued a stack sample")
		}
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	unblock()
	competitor := make(chan error, 1)
	go func() {
		competitor <- other.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec("INSERT INTO meta VALUES('competitor','committed')")
			return err
		})
	}()
	select {
	case err := <-competitor:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		release()
		<-competitor
		<-result
		t.Fatal("diagnostics retained SQLite writer")
	}
	free := make(chan struct{})
	go func() { l.sessionMu.Lock(); l.sessionMu.Unlock(); close(free) }()
	select {
	case <-free:
	case <-time.After(5 * time.Second):
		release()
		<-free
		<-result
		t.Fatal("diagnostic delivery/join retained actual sessionMu")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
