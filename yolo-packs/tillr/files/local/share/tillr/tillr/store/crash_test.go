package store_test

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func killWriter(t *testing.T, path string, legacy bool) {
	t.Helper()
	args := []string{"testdata/crash_writer.py", path}
	if legacy {
		args = append(args, "delete")
	}
	cmd := exec.Command("python3", args...)
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
