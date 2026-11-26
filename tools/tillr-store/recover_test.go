package main_test

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
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
	if _, err := db.Exec("PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
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

func TestRecoverRealHotJournalAndSecondRunWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	if code, out := run(t, "init", "--fixture", "--store", path); code != 0 {
		t.Fatalf("init: %d %s", code, out)
	}
	bundle := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(bundle, []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if code, out := run(t, "seed", "--store", path, "--input", bundle); code != 0 {
		t.Fatalf("seed: %d %s", code, out)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestCrashWriterHelper$")
	cmd.Env = append(os.Environ(), "TILLR_CRASH_WRITER_DB="+path)
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
		t.Fatal("writer not ready")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if cmd.Wait() == nil {
		t.Fatal("writer not killed")
	}
	if info, err := os.Stat(path + "-journal"); err != nil || info.Size() <= 512 {
		t.Fatalf("journal: %v %v", info, err)
	}
	if code, _ := run(t, "check", "--store", path); code != 3 {
		t.Fatalf("hot journal check exited %d", code)
	}
	code, raw := run(t, "recover", "--store", path)
	if code != 0 {
		t.Fatalf("recover: %d %s", code, raw)
	}
	var record struct {
		Payload struct {
			Action  string         `json:"action"`
			Journal string         `json:"journal_found"`
			Head    int64          `json:"event_head_seq"`
			Counts  map[string]int `json:"table_counts"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if record.Payload.Action != "recover" || record.Payload.Journal != path+"-journal" || record.Payload.Head != 1 || record.Payload.Counts["meta"] != 2 {
		t.Fatalf("recovery record: %s", raw)
	}
	if code, raw := run(t, "check", "--store", path); code != 0 {
		t.Fatalf("check after recover: %d %s", code, raw)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, raw := run(t, "recover", "--store", path); code != 0 {
		t.Fatalf("second recovery: %d %s", code, raw)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	next, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !info.ModTime().Equal(next.ModTime()) {
		t.Fatal("second recovery changed store")
	}
	for _, suffix := range []string{"-journal", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("second recovery created sidecar %s: %v", suffix, err)
		}
	}
}
