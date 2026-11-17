package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// This connection test deliberately accepts either landing order: settings must
// refuse comments by name until the intake change wires its server into the loop.
func TestLoopCommentsServerConnection(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/review") {
			calls.Add(1)
		}
		fmt.Fprint(w, `{"comments":[]}`)
	}))
	defer server.Close()
	f := newLoopFixture(t, stub(t, "adapter", "exit 0"))
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), fmt.Sprintf("[comments]\nvantage_url = %q\nrepo = 'fixture'\n", server.URL))
	// The existing fixture already has its store and policy. If comments becomes
	// accepted, a consumed reply must be observed rather than merely starting a loop.
	st, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	policy := filepath.Join(scratch(t, "reply-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	db, err := sql.Open("sqlite", f.store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS comment_deliveries(run_id TEXT PRIMARY KEY,item_id TEXT NOT NULL,document TEXT NOT NULL,blob_id TEXT NOT NULL,body TEXT NOT NULL,written INTEGER NOT NULL DEFAULT 0);
 INSERT INTO comment_deliveries VALUES('reply-run','item-comments-example-1','docs/design/example.md','blob','{"id":"question","round":0,"summary":"answer"}',1);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	code, out := invoke(t, "loop", "--root", f.root, "--store", f.store, "--policy", policy, "--lane-root", f.laneRoot, "--gate", "/bin/true", "--land", "/bin/true", "--verdict", "/bin/true", "--trace-bin=", "--rolling=", "--exit-when-drained")
	if code == 2 && strings.Contains(string(out), "comments: expected a known table") {
		return
	}
	if calls.Load() == 0 {
		t.Fatalf("comments accepted without asking the reply server (exit %d): %s", code, out)
	}
}
