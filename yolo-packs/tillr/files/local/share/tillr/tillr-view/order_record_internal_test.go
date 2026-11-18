package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/orderrecord"
	"github.com/mschulkind-oss/tillr/store"
)

func TestOrderRecordingReceiptOnWaitingAndWhy(t *testing.T) {
	_, drainBin := buildPair(t)
	dir, path := whyFixture(t, drainBin)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("symbolic-ref", "HEAD", "refs/heads/main")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec("UPDATE items SET rank=CASE item_id WHEN 'b' THEN '0' ELSE rank END"); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (?,'item.update','item','b','{"placed_via":"cli"}')`, at.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[order]\nrecord='order.txt'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".tillr.toml")
	git("commit", "-qm", "settings")
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "absent-index"), dir, "", nil)
	srv.plannerFlag = drainBin
	defer srv.close()
	httpServer := httptest.NewServer(srv.routes())
	defer httpServer.Close()
	h := &serveHarness{t: t, http: httpServer}
	before := storeFingerprint(t, path)
	for _, route := range []string{"/api/v1/waiting", "/api/v1/why"} {
		status, body, raw := h.get(route)
		if status != 200 {
			t.Fatalf("%s: %d %s", route, status, raw)
		}
		receipt, ok := body["snapshot"].(map[string]any)["order_recording"].(map[string]any)
		if !ok || receipt["recorded"] != false || receipt["changed_at_ms"] != float64(at.UnixMilli()) {
			t.Fatalf("pending %s: %s", route, raw)
		}
	}
	// A hand-edited trunk record is stale, not a route failure, including a
	// directory at the configured path. Without a hand move there is no work.
	for _, content := range []string{"b\na\n", "# last_hand_move_event_seq: invalid\nb\n", "directory"} {
		path := filepath.Join(dir, "order.txt")
		git("rm", "-rf", "--ignore-unmatch", "order.txt")
		if content == "directory" {
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			path = filepath.Join(path, "child")
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", "order.txt")
		git("commit", "-qm", "hand edited record")
		status, body, raw := h.get("/api/v1/waiting")
		if status != 200 || body["snapshot"].(map[string]any)["order_recording"].(map[string]any)["recorded"] != false {
			t.Fatalf("hand edited record: %d %s", status, raw)
		}
	}
	git("rm", "-rf", "order.txt")
	git("commit", "-qm", "remove hand edited record")
	// An uncommitted checkout edit is not proof that the order was recorded.
	var record string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		_, body, err := orderrecord.Read(context.Background(), r, dir)
		record = body + "b\na\n"
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "order.txt"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	_, body, _ := h.get("/api/v1/waiting")
	if body["snapshot"].(map[string]any)["order_recording"].(map[string]any)["recorded"] != false {
		t.Fatal("dirty checkout counted as recorded")
	}
	git("add", "order.txt")
	git("commit", "-qm", "record")
	_, body, raw := h.get("/api/v1/waiting")
	if body["snapshot"].(map[string]any)["order_recording"].(map[string]any)["recorded"] != true {
		t.Fatalf("recorded: %s", raw)
	}
	if got := storeFingerprint(t, path); got != before {
		t.Fatal("status read changed store")
	}
}
