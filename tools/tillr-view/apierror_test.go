package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// allErrorCodes is every code apierror.go declares. It exists so a code added to the list
// and to no status table is caught here rather than by a client that saw a 500 for a
// condition the API could have named.
//
// Negative control: dropping one entry from apiCodes fails
// TestEveryErrorCodeHasAStatus.
var allErrorCodes = []string{
	codeWordsEmpty, codeWordsNotText, codeWordsTooLarge, codeIntakeNotConfigured, codeIntakeStoreFailed,
	codeReadOnly, codeOriginRefused, codeContentType, codeTooLarge, codeBadRequest,
	codeUnknownItem, codeNotInOrder, codeStoreBusy, codeWriterUnavailable,
	codeStoreAbsent,
	codeStoreUnreadable,
	codeSchemaTooNew,
	codeSchemaUninitialized,
	codeTableAbsent,
	codeItemNotFound,
	codeRunNotFound,
	codeLandingNotFound,
	codeCheckNotFound,
	codeEntryNotFound,
	codeBadQuery,
	codeNoDesignDoc,
	codeDesignDocMissing,
	codeDesignDocOutsideRoot,
	codeDesignDocUnreadable,
	codeUnknownKind,
	codeUnknownRoute,
	codeUnknownTable,
	codeRowNotFound,
	codePlannerUnavailable,
	codePlannerMismatch,
}

// TestEveryErrorCodeHasAStatus pins the vocabulary to its statuses: one entry per code, no
// entry for a code that does not exist, and no code spelled twice.
func TestEveryErrorCodeHasAStatus(t *testing.T) {
	seen := map[string]int{}
	for _, c := range apiCodes {
		seen[c.Code]++
		if c.Status < 400 || c.Status > 599 {
			t.Fatalf("code %q pairs with status %d, which is not an error", c.Code, c.Status)
		}
	}
	for _, code := range allErrorCodes {
		if seen[code] != 1 {
			t.Fatalf("code %q appears %d times in apiCodes, want exactly 1", code, seen[code])
		}
		delete(seen, code)
	}
	for code := range seen {
		t.Fatalf("apiCodes carries %q, which allErrorCodes does not declare", code)
	}
}

// TestTheStoreConditionsAreDistinctCodes is the brief's complaint made a test: a store that
// is absent, one that cannot be read, one whose schema is newer than this binary and one
// that has applied no migration are four different answers, and each answers its own code
// on the wire rather than as a start-up refusal.
//
// Negative control: reverting newServer to an early exit for an unreadable store fails
// every case here with a nil server and no HTTP answer to read.
func TestTheStoreConditionsAreDistinctCodes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		store    func(t *testing.T) string
		wantCode string
		wantStat string
	}{
		{"absent", func(t *testing.T) string {
			return filepath.Join(dir, "not-a-store.db")
		}, codeStoreAbsent, "absent"},
		{"unreadable", func(t *testing.T) string {
			// A file that is not a database: it is there, so it is not absent, and reading it
			// fails, so it is not present either.
			path := filepath.Join(dir, "garbage.db")
			if err := os.WriteFile(path, []byte("this is not a sqlite database"), 0o644); err != nil {
				t.Fatalf("writing the garbage store: %v", err)
			}
			return path
		}, codeStoreUnreadable, stateUnreadable},
		{"schema_newer", func(t *testing.T) string {
			path := filepath.Join(dir, "newer.db")
			writeStore(t, path, func(db *sql.DB) {
				mustExec(t, db, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1)`,
					store.SchemaVersion+1)
			})
			return path
		}, codeSchemaTooNew, "schema_too_new"},
		{"schema_uninitialized", func(t *testing.T) string {
			// No migration row at all: whatever file this is, it is not a store these
			// utilities created.
			path := filepath.Join(dir, "empty.db")
			writeStore(t, path, func(db *sql.DB) {
				mustExec(t, db, `DELETE FROM schema_migrations`)
			})
			return path
		}, codeSchemaUninitialized, "schema_uninitialized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storePath := tc.store(t)
			srv := newServer(context.Background(), storePath, filepath.Join(dir, "loop"),
				filepath.Join(dir, "lanes"), filepath.Join(dir, "no-tillr-trace"), dir, "", nil)
			t.Cleanup(srv.close)
			if srv.problem == nil {
				t.Fatalf("a %s store answered no problem: the face thinks it can read it", tc.name)
			}
			if srv.problem.code != tc.wantCode {
				t.Fatalf("a %s store answered code %q, want %q (note: %s)",
					tc.name, srv.problem.code, tc.wantCode, srv.problem.note)
			}
			if got := srv.sources().Store.State; got != tc.wantStat {
				t.Fatalf("a %s store reports sources.store.state %q, want %q", tc.name, got, tc.wantStat)
			}
			// Every route answers it, not just the snapshot: the socket and the item routes
			// would otherwise answer an empty board for a store that is not there.
			for _, route := range []string{"/api/v1/snapshot", "/api/v1/items/item-1", "/api/v1/ws"} {
				status, code, _ := readFailure(t, srv, route)
				if status != statusFor(tc.wantCode) {
					t.Fatalf("%s on a %s store answered %d, want %d", route, tc.name, status, statusFor(tc.wantCode))
				}
				if code != tc.wantCode {
					t.Fatalf("%s on a %s store answered code %q, want %q", route, tc.name, code, tc.wantCode)
				}
			}
			// An unknown path is still the caller's mistake, not the store's: the code names
			// the condition the client can act on.
			if _, code, _ := readFailure(t, srv, "/api/v1/nonsense"); code != tc.wantCode {
				t.Fatalf("an unknown path on a %s store answered %q, want the store's %q", tc.name, code, tc.wantCode)
			}
		})
	}
}

// readFailure runs one route against a served face and answers its status and error code.
func readFailure(t *testing.T, srv *server, path string) (int, string, []byte) {
	t.Helper()
	status, body, raw := getJSON(t, srv, path)
	failure, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("%s answered no error body: %s", path, raw)
	}
	code, _ := failure["code"].(string)
	if message, _ := failure["message"].(string); message == "" {
		t.Fatalf("%s answered no message: %s", path, raw)
	}
	return status, code, raw
}

// getJSON runs one route against a served face and answers its status and decoded body.
func getJSON(t *testing.T, srv *server, path string) (int, map[string]any, []byte) {
	t.Helper()
	httpServer := httptest.NewServer(srv.routes())
	defer httpServer.Close()
	resp, err := http.Get(httpServer.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("GET %s answered unreadable JSON (%d): %v\n%s", path, resp.StatusCode, err, raw)
	}
	return resp.StatusCode, body, raw
}

// writeStore creates a store at path and runs fn against it.
func writeStore(t *testing.T, path string, fn func(db *sql.DB)) {
	t.Helper()
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing %s: %v", path, err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer db.Close()
	fn(db)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("executing %q: %v", query, err)
	}
}

// A page newer than its server needs a restart, not an inventory of server routes.
func TestAnUnknownRouteNamesTheRestartNotTheRouteSet(t *testing.T) {
	h := newServeHarness(t)
	status, body, raw := h.get("/api/v1/nope")
	if status != http.StatusNotFound {
		t.Fatalf("an unknown route answered %d: %s", status, raw)
	}
	failure := body["error"].(map[string]any)
	if failure["code"] != codeUnknownRoute {
		t.Fatalf("an unknown route answered %v, want %s", failure, codeUnknownRoute)
	}
	message, _ := failure["message"].(string)
	if !strings.Contains(message, "This page needs a newer server") || !strings.Contains(message, "just observatory") {
		t.Fatalf("unknown route lacks restart advice: %q", message)
	}
	if strings.Contains(message, "face") || strings.Contains(message, "/api/") || strings.Contains(message, "routes") {
		t.Fatalf("unknown route leaks a route inventory: %q", message)
	}
	if body["snapshot"].(map[string]any)["build"] == nil {
		t.Fatal("unknown-route response names no server build")
	}
}
