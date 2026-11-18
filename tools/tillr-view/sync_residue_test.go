package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncResidueStatusFromBuiltSync(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	command := func(dir string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TILLR_SQLITE_TRACE=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	command(root, "git", "init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "done.md"), []byte("### Done\n<!-- tillr: item id=\"item-done\" paths=\"docs/\" -->\n- **c1:** done\n"), 0644); err != nil {
		t.Fatal(err)
	}
	command(root, "git", "add", "docs")
	path := filepath.Join(root, "state.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE items(item_id TEXT PRIMARY KEY,title TEXT NOT NULL,body_ref TEXT,state TEXT NOT NULL,design_doc TEXT,updated_at INTEGER,event_seq INTEGER NOT NULL DEFAULT 0)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('item-private','Private','in-queue',?),('item-done','Done','landed',?)`, at.Add(-10*time.Minute).UnixMilli(), at.UnixMilli())
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	syncBin := filepath.Join(root, "tillr-item")
	command("../tillr-item", "go", "build", "-o", syncBin, ".")
	command(root, syncBin, "sync", "--store", path, "--root", root, "--now", at.Format(time.RFC3339), "--json")
	// The persisted observation survives deleting every document and needs no git tree.
	if err := os.RemoveAll(filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	viewBin := filepath.Join(root, "tillr-view")
	command(".", "go", "build", "-o", viewBin, ".")
	text := string(command(root, viewBin, "status", "--store", path, "--text", "--root", filepath.Join(root, "absent")))
	if !regexp.MustCompile(`unshared_open: 1 \(oldest [0-9]+m\)`).MatchString(text) {
		t.Fatalf("missing oldest age: %s", text)
	}
	for _, want := range []string{"stale_doc_blocks: 1", at.Format(time.RFC3339)} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	raw := command(root, viewBin, "status", "--store", path, "--json")
	var env struct {
		Payload struct {
			UnsharedOpen       *int     `json:"unshared_open"`
			UnsharedOpenIDs    []string `json:"unshared_open_ids"`
			UnsharedOpenOldest string   `json:"unshared_open_oldest"`
			StaleDocBlocks     *int     `json:"stale_doc_blocks"`
			StaleDocBlockIDs   []string `json:"stale_doc_block_ids"`
			SyncedAt           string   `json:"synced_at"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	p := env.Payload
	if p.UnsharedOpen == nil || *p.UnsharedOpen != 1 || p.StaleDocBlocks == nil || *p.StaleDocBlocks != 1 || strings.Join(p.UnsharedOpenIDs, ",") != "item-private" || strings.Join(p.StaleDocBlockIDs, ",") != "item-done" || p.SyncedAt != at.Format(time.RFC3339) || p.UnsharedOpenOldest != at.Add(-10*time.Minute).Format(time.RFC3339) {
		t.Fatalf("payload: %s", raw)
	}
}

func TestSyncResidueStatusAbsent(t *testing.T) {
	for _, emptyTable := range []bool{false, true} {
		s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		if emptyTable {
			err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
				_, err := tx.Exec(`CREATE TABLE item_sync_residue(singleton INTEGER PRIMARY KEY,synced_at TEXT,unshared_open_ids TEXT,unshared_open_oldest TEXT,stale_doc_block_ids TEXT)`)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		var v view
		err = s.ReadTx(context.Background(), func(r *store.Reader) error { var err error; v.domain, err = readStoreDomain(r); return err })
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		p, code := assemble(v, nil, nil)
		if code != record.ExitOK {
			t.Fatalf("exit %d", code)
		}
		text := statusText(p)
		if !strings.Contains(text, "no sync has been recorded") || strings.Contains(text, "unshared_open: 0") || strings.Contains(text, "stale_doc_blocks: 0") {
			t.Fatalf("absence: %s", text)
		}
		if p.UnsharedOpen != nil || p.StaleDocBlocks != nil {
			t.Fatalf("absence became zero: %+v", p)
		}
	}
}

func TestSyncResidueStatusAgeUsesReadTime(t *testing.T) {
	count := 1
	p := statusPayload{GeneratedAt: time.Date(2026, 11, 17, 12, 10, 59, 0, time.UTC),
		SyncedAt: "2026-11-17T12:00:00Z", UnsharedOpenOldest: "2026-11-17T12:00:00Z",
		UnsharedOpen: &count, StaleDocBlocks: &count}
	if text := statusText(p); !strings.Contains(text, "unshared_open: 1 (oldest 10m)") {
		t.Fatalf("minute age: %s", text)
	}
}
