package store_test

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCrashWriterHelper(t *testing.T) {
	dbPath := os.Getenv("TILLR_CRASH_WRITER_DB")
	if dbPath == "" {
		return
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if os.Getenv("TILLR_CRASH_WRITER_DELETE") == "1" {
		if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("PRAGMA cache_size=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA synchronous=FULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO meta(key,value) VALUES ('killed-writer', zeroblob(1048576))"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("ready")
	select {}
}

func killWriter(t *testing.T, path string, legacy bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCrashWriterHelper$")
	cmd.Env = append(os.Environ(), "TILLR_CRASH_WRITER_DB="+path)
	if legacy {
		cmd.Env = append(cmd.Env, "TILLR_CRASH_WRITER_DELETE=1")
	}
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	scan := bufio.NewScanner(out)
	if !scan.Scan() || scan.Text() != "ready" {
		t.Fatal("writer never reached its uncommitted write")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil {
		t.Fatal("writer was not killed")
	}
}

func TestKilledWriterLeavesCommittedStoreReadable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	killWriter(t, path, false)
	r, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if version, err := r.SchemaVersion(); err != nil || version != store.SchemaVersion {
		t.Fatalf("schema: %d %v", version, err)
	}
	if counts, err := r.TableCounts(); err != nil || counts["meta"] != 2 {
		t.Fatalf("committed counts: %v %v", counts, err)
	}
	if integrity, err := r.Integrity(); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
	if value, err := r.Meta("killed-writer"); err != nil || value != nil {
		t.Fatalf("uncommitted row visible: %v %v", value, err)
	}
}

func TestWritableOpensSelectWALAndReadOpensPreserveMode(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	for _, create := range []bool{true, false} {
		if !create {
			execSQL(t, path, `PRAGMA journal_mode=DELETE`)
		}
		s, err := store.Open(ctx, path, create)
		if err != nil {
			t.Fatal(err)
		}
		var mode string
		err = s.ReadTx(ctx, func(r *store.Reader) error { return r.QueryRow("PRAGMA journal_mode").Scan(&mode) })
		s.Close()
		if err != nil || mode != "wal" {
			t.Fatalf("writer create=%v mode=%q: %v", create, mode, err)
		}
		reader, err := store.OpenRead(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		err = reader.ReadTx(ctx, func(r *store.Reader) error { return r.QueryRow("PRAGMA journal_mode").Scan(&mode) })
		reader.Close()
		if err != nil || mode != "wal" {
			t.Fatalf("reader create=%v mode=%q: %v", create, mode, err)
		}
	}
	killWriter(t, path, true)
	if info, err := os.Stat(path + "-journal"); err != nil || info.Size() <= 512 {
		t.Fatalf("no real journal: %v %v", info, err)
	}
	r, err := store.OpenRead(ctx, path)
	if err == nil {
		_, err = r.SchemaVersion()
		r.Close()
	}
	if err == nil {
		t.Fatal("legacy hot journal unexpectedly readable")
	}
}
