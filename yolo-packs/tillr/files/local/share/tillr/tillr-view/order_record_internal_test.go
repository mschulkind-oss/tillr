package main

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestQueueReadsOmitOrderRecordStatus(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "order.txt"), []byte("a\nb\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "order.txt")
	git("commit", "-qm", "legacy queue-order record")
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "no-tillr-trace"), dir, "", nil)
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
		if _, ok := body["snapshot"].(map[string]any)["order_recording"]; ok {
			t.Fatalf("%s still reports order record status: %s", route, raw)
		}
	}
	if got := storeFingerprint(t, path); got != before {
		t.Fatal("status read changed store")
	}
}
