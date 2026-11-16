package main

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/mschulkind-oss/tillr/store"
)

func TestHotJournalDiagnosisAndRetryAfterRecoveryOnSameServer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	(&serveHarness{t: t, store: path}).seedStore()
	cmd := exec.Command("python3", "../tillr/store/testdata/crash_writer.py", path, "delete")
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
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(path + "-journal")
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(ctx, path, "", "", filepath.Join(dir, "no-index"), dir, "", nil)
	defer srv.close()
	handler := srv.routes()
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", nil))
		return w
	}
	w := request()
	if w.Code != 503 {
		t.Fatalf("hot store: %d %s", w.Code, w.Body)
	}
	for _, note := range []string{"uncommitted writer", "SQLITE_READONLY_ROLLBACK", "tillr-store recover --store"} {
		if !bytes.Contains(w.Body.Bytes(), []byte(note)) {
			t.Fatalf("missing diagnosis %q: %s", note, w.Body)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nextJournal, err := os.ReadFile(path + "-journal")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !bytes.Equal(journal, nextJournal) {
		t.Fatal("read-only face changed store or journal")
	}
	repaired, _, err := store.Recover(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repaired.Close()
	// Simultaneous retries must publish only one successful handle without races.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := request()
			if w.Code != 200 {
				t.Errorf("same server after repair: %d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	r, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if value, err := r.Meta("killed-writer"); err != nil || value != nil {
		t.Fatalf("uncommitted row: %v %v", value, err)
	}
}

func TestLiveSocketDoesNotHoldStoreOpenLock(t *testing.T) {
	h := newServeHarness(t)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.http.URL, "http")+"/api/v1/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if !h.srv.openMu.TryLock() {
		t.Fatal("live socket holds the store-open lock; a simultaneous startup retry could wait forever")
	}
	h.srv.openMu.Unlock()
}

func TestAbsentStoreRetryFindsNewStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.db")
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "no-index"), dir, "", nil)
	defer srv.close()
	handler := srv.routes()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/snapshot", nil))
		if w.Code != 503 {
			t.Fatalf("missing store %s status %d", method, w.Code)
		}
	}
	(&serveHarness{t: t, store: path}).seedStore()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/snapshot", nil))
	if w.Code != 200 {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
}
