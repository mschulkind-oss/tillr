package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// capturedStalledSQLiteStderr is stalledSQLiteStderr whose release keeps what the
// delivery goroutine wrote after the stall, so a test can read the loss report.
func capturedStalledSQLiteStderr(t *testing.T) (block, release func(), read func() string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	var mu sync.Mutex
	var captured bytes.Buffer
	var once sync.Once
	copied := make(chan struct{})
	release = func() {
		once.Do(func() {
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
	read = func() string { mu.Lock(); defer mu.Unlock(); return captured.String() }
	block = func() {
		fd := int(writer.Fd())
		if err := syscall.SetNonblock(fd, true); err != nil {
			t.Fatal(err)
		}
		defer syscall.SetNonblock(fd, false)
		data := make([]byte, 4096)
		for {
			_, err := syscall.Write(fd, data)
			if err == syscall.EAGAIN {
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { release(); os.Stderr = original; writer.Close(); <-copied; reader.Close() })
	return block, release, read
}

// privateSQLiteTraceObserver gives one test its own observer with a small queue, so
// its deliberate loss neither leaks into another test's completeness check nor needs
// a thousand records to fill the production-sized queue.
func privateSQLiteTraceObserver(t *testing.T, queue int) *sqliteTraceObserver {
	t.Helper()
	previous := sqliteTrace
	sqliteTrace = newSQLiteTraceObserver(queue)
	t.Cleanup(func() { sqliteTrace = previous })
	return sqliteTrace
}

// A full trace queue must cost a span's caller nothing: every enqueue made while the
// SQLite writer or an application mutex is held returns at once, the overflow is
// counted, and the delivery goroutine reports the count in the trace itself once the
// sink drains. The sink is stalled before the first record, so the delivery goroutine
// holds one record, the queue fills, and everything after it is dropped.
func TestFullTraceQueueNeverBlocksAWriterAndReportsTheLoss(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	path := filepath.Join(t.TempDir(), "queue.db")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	const queue, writes = 4, 8 // Each WriteTx queues three records: far past 1+queue.
	observer := privateSQLiteTraceObserver(t, queue)
	block, release, read := capturedStalledSQLiteStderr(t)
	defer release()
	block()
	t.Setenv("TILLR_SQLITE_TRACE", "1")

	// appMu stands in for an application mutex held around each write, the shape of
	// the drain's sessionMu: a blocking enqueue would leave both it and, for the
	// acquired record, the SQLite writer held.
	var appMu sync.Mutex
	done := make(chan error, 1)
	go func() {
		for i := 0; i < writes; i++ {
			appMu.Lock()
			err := s.WriteTx(context.Background(), func(tx *Tx) error {
				_, err := tx.Exec("INSERT OR REPLACE INTO meta VALUES('queue-probe', ?)", i)
				return err
			})
			appMu.Unlock()
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		release()
		<-done
		t.Fatal("a full trace queue blocked a span's caller while it held the writer or its mutex")
	}
	t.Setenv("TILLR_SQLITE_TRACE", "")
	competitor := make(chan error, 1)
	go func() {
		competitor <- other.WriteTx(context.Background(), func(tx *Tx) error {
			_, err := tx.Exec("INSERT INTO meta VALUES('competitor','committed')")
			return err
		})
	}()
	select {
	case err := <-competitor:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		release()
		<-competitor
		t.Fatal("a full trace queue retained the SQLite writer")
	}
	stalled := observer.status()
	if stalled.DroppedRecords == 0 {
		t.Fatalf("the full queue dropped nothing, so the overflow path did not run: %+v", stalled)
	}
	if read() != "" {
		t.Fatalf("the stalled sink accepted output before release: %q", read())
	}

	release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	final, err := FlushSQLiteTrace(ctx)
	cancel()
	if !errors.Is(err, ErrIncompleteSQLiteTrace) {
		t.Fatalf("a lossy capture flushed as %v, want ErrIncompleteSQLiteTrace: %+v", err, final)
	}
	if final.DroppedRecords != stalled.DroppedRecords || !final.LossReported {
		t.Fatalf("loss changed or went unreported after the stall: stalled %+v, final %+v", stalled, final)
	}
	want := "dropped spans=0 records=" + strconv.FormatUint(final.DroppedRecords, 10) + " sink_errors=0"
	var reports []string
	for _, line := range strings.Split(read(), "\n") {
		if strings.HasPrefix(line, "tillr-sqlite ") && strings.Contains(line, " dropped ") {
			reports = append(reports, line)
		}
	}
	if len(reports) == 0 || !strings.HasSuffix(reports[len(reports)-1], want) {
		t.Fatalf("the trace never reported its loss (want a line ending %q): %q", want, reports)
	}
}

// Every return path of WriteTx -- callback error, BEGIN error, cancellation inside
// the callback -- must release the writer even while the sink is stalled, because
// they all leave through the same deferred finish.
func TestBlockedTraceSinkReleasesTheWriterOnEveryReturnPath(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	path := filepath.Join(t.TempDir(), "paths.db")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	privateSQLiteTraceObserver(t, sqliteTraceQueueLimit)
	block, release := stalledSQLiteStderr(t)
	defer release()
	block()
	failure := errors.New("callback failure fixture")
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	for _, c := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		fn   func(context.CancelFunc) func(*Tx) error
		want func(error) bool
	}{
		{"callback error", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			func(context.CancelFunc) func(*Tx) error { return func(*Tx) error { return failure } },
			func(err error) bool { return errors.Is(err, failure) }},
		{"BEGIN error", func() (context.Context, context.CancelFunc) { return cancelled, func() {} },
			func(context.CancelFunc) func(*Tx) error { return func(*Tx) error { return nil } },
			func(err error) bool { return errors.Is(err, context.Canceled) }},
		{"cancellation inside the callback", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) },
			func(cancel context.CancelFunc) func(*Tx) error {
				return func(tx *Tx) error {
					cancel()
					_, err := tx.Exec("INSERT INTO meta VALUES('cancelled','x')")
					return err
				}
			},
			func(err error) bool { return err != nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TILLR_SQLITE_TRACE", "1")
			ctx, cancel := c.ctx()
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- s.WriteTx(ctx, c.fn(cancel)) }()
			select {
			case err := <-result:
				if !c.want(err) {
					t.Fatalf("unexpected outcome %v", err)
				}
			case <-time.After(10 * time.Second):
				release()
				<-result
				t.Fatal("a stalled trace sink held WriteTx on this return path")
			}
			t.Setenv("TILLR_SQLITE_TRACE", "")
			competitor := make(chan error, 1)
			go func() {
				competitor <- other.WriteTx(context.Background(), func(tx *Tx) error {
					_, err := tx.Exec("INSERT OR REPLACE INTO meta VALUES('competitor', ?)", c.name)
					return err
				})
			}()
			select {
			case err := <-competitor:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				release()
				<-competitor
				t.Fatal("a stalled trace sink retained the SQLite writer on this return path")
			}
		})
	}
}
