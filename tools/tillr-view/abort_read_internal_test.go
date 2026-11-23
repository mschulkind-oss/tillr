package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A client that went away (the browser cancels a read whenever a newer one replaces it)
// is not a store fault. Measured 2026-11-22 against the owner's server: the perf log
// held 503 lines of 416 B, 2 to 9650 ms, for reads the browser had aborted (the HAR shows
// net::ERR_ABORTED and no 503 at all); serveRead answered the cancelled context as
// store-unreadable.
func TestAbortedReadIsNotAStoreFault(t *testing.T) {
	h := newServeHarness(t)
	for _, path := range []string{"/api/v1/snapshot", "/api/v1/lanes", "/api/v1/passes", "/api/v1/waiting"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		h.srv.routes().ServeHTTP(rec, req)
		if rec.Code != statusClientClosed || rec.Body.Len() != 0 {
			t.Errorf("%s: aborted read answered %d %q, want %d and no body", path, rec.Code, rec.Body.String(), statusClientClosed)
		}
	}
}

// The browser's timing report is not a write: a read-only server must accept it. It
// answered 405 read-only all 21 times in the owner's HAR.
func TestClientTimingIsAcceptedByAReadOnlyServer(t *testing.T) {
	h := newServeHarness(t)
	req := httptest.NewRequest(http.MethodPost, clientTimingPath, nil)
	req.Header.Set("Origin", "http://127.0.0.1:4318")
	req.Host = "127.0.0.1:4318"
	rec := httptest.NewRecorder()
	h.srv.routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("client-timing on a read-only server answered %d %s", rec.Code, rec.Body.String())
	}
}
