package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func stalledSQLiteStderr(t *testing.T) (block, release func()) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	var once sync.Once
	release = func() { once.Do(func() { go io.Copy(io.Discard, reader) }) }
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
	t.Cleanup(func() { release(); os.Stderr = original; writer.Close(); reader.Close() })
	return block, release
}

func TestBlockedTraceSinkDoesNotRetainMigrationWriter(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	path := filepath.Join(t.TempDir(), "migration.db")
	other, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	name := fmt.Sprintf("blocked_trace_migration_%d", traceFixtureSequence.Add(1))
	entered, proceed := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(proceed) }) }
	defer unblock()
	if err := sqlite.RegisterScalarFunction(name, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		close(entered)
		<-proceed
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	registryMu.Lock()
	previous := append([]ownerDDL(nil), registeredDDL...)
	registeredDDL = append(registeredDDL, ownerDDL{owner: name, ddl: "SELECT " + name + "();"})
	registryMu.Unlock()
	defer func() { registryMu.Lock(); registeredDDL = previous; registryMu.Unlock() }()
	block, release := stalledSQLiteStderr(t)
	defer release()
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	migration := make(chan error, 1)
	go func() {
		s, err := Open(context.Background(), path, true)
		if s != nil {
			s.Close()
		}
		migration <- err
	}()
	<-entered
	block()
	t.Setenv("TILLR_SQLITE_TRACE", "")
	unblock()
	competitor := make(chan error, 1)
	go func() {
		competitor <- other.WriteTx(context.Background(), func(tx *Tx) error { _, err := tx.Exec("INSERT INTO meta VALUES('competitor','committed')"); return err })
	}()
	select {
	case err := <-competitor:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		release()
		<-competitor
		<-migration
		t.Fatal("blocked diagnostic sink retained migration writer")
	}
	select {
	case err := <-migration:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		release()
		<-migration
		t.Fatal("migration return waited for diagnostic sink")
	}
}
