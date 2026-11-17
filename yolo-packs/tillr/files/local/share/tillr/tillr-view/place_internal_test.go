package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func placeServer(t *testing.T, write bool, writer string) (*serveHarness, *httptest.Server) {
	t.Helper()
	h := newServeHarness(t)
	s := newServer(context.Background(), h.store, h.loopDir, h.laneRoot, h.index, h.dir, "", []string{"false"})
	s.writeEnabled = write
	s.itemFlag = writer
	srv := httptest.NewServer(s.routes())
	t.Cleanup(func() { srv.Close(); s.close() })
	if err := s.store.WriteMeta("write-probe", "x"); err == nil {
		t.Fatal("server handle is writable")
	}
	return h, srv
}
func postPlace(t *testing.T, srv *httptest.Server, path, body, contentType, origin string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}
func TestPlaceHTTPReadOnlyAndValidation(t *testing.T) {
	_, readonly := placeServer(t, false, "")
	for _, path := range []string{"/api/v1/items/item-seed-06/place", "/api/v1/missing", "/api/v1/client-timing"} {
		status, body := postPlace(t, readonly, path, `{}`, "text/plain", "https://foreign.example")
		if status != 405 || body["error"].(map[string]any)["code"] != "read-only" {
			t.Fatalf("%d %v", status, body)
		}
	}
	resp, err := http.Get(readonly.URL + "/api/v1/snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snapshot map[string]any
	json.NewDecoder(resp.Body).Decode(&snapshot)
	if snapshot["snapshot"].(map[string]any)["writes_enabled"] != false {
		t.Fatal(snapshot)
	}
	_, srv := placeServer(t, true, "")
	for _, path := range []string{"/api/v1/snapshot", "/api/v1/missing"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if body["snapshot"].(map[string]any)["writes_enabled"] != true {
			t.Fatal(body)
		}
	}
	for _, tc := range []struct {
		body, typ, origin, code string
		status                  int
	}{
		{`{"to":"first"}`, "application/json", "https://foreign.example", "origin-refused", 403},
		{`{"to":"first"}`, "text/plain", "", "content-type", 415},
		{`oops`, "application/json", "", "bad-request", 400},
		{`{"to":"middle"}`, "application/json", "", "bad-request", 400},
		{`{"to":"first","extra":1}`, "application/json", "", "bad-request", 400},
		{`{"to":"first","after":"x"}`, "application/json", "", "bad-request", 400},
		{`{"before":""}`, "application/json", "", "bad-request", 400},
		{`null`, "application/json", "", "bad-request", 400},
		{`{"to":"first"} {}`, "application/json", "", "bad-request", 400},
		{`{"to":"last","to":"first"}`, "application/json", "", "bad-request", 400},
		{strings.Repeat(" ", 1<<20) + `{}`, "application/json", "", "too-large", 413},
	} {
		status, body := postPlace(t, srv, "/api/v1/items/item-seed-06/place", tc.body, tc.typ, tc.origin)
		if status != tc.status || body["error"].(map[string]any)["code"] != tc.code {
			t.Fatalf("%s: %d %v", tc.code, status, body)
		}
	}
}
func TestPlaceHTTPRunsTheItemCommand(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tillr-item")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../tillr-item"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	_, srv := placeServer(t, true, bin)
	for _, body := range []string{`{"to":"first"}`, `{"to":"last"}`, `{"before":"item-seed-02"}`, `{"after":"item-seed-02"}`} {
		status, data := postPlace(t, srv, "/api/v1/items/item-seed-06/place", body, "application/json; charset=utf-8", "http://localhost:4318")
		want := float64(1)
		if strings.Contains(body, "last") || strings.Contains(body, "after") {
			want = 2
		}
		if status != 200 || data["data"].(map[string]any)["place"] != want || data["snapshot"].(map[string]any)["writes_enabled"] != true {
			t.Fatalf("%d %v", status, data)
		}
	}
	for _, tc := range []struct {
		id, body, code string
		status         int
	}{{"missing", `{"to":"first"}`, "unknown-item", 404}, {"item-seed-04", `{"to":"first"}`, "not-in-order", 409}, {"item-seed-06", `{"before":"missing"}`, "unknown-item", 404}, {"item-seed-06", `{"after":"item-seed-04"}`, "not-in-order", 409}} {
		status, data := postPlace(t, srv, "/api/v1/items/"+tc.id+"/place", tc.body, "application/json", "")
		if status != tc.status || data["error"].(map[string]any)["code"] != tc.code {
			t.Fatalf("%d %v", status, data)
		}
	}
	_, unavailable := placeServer(t, true, filepath.Join(t.TempDir(), "absent"))
	status, data := postPlace(t, unavailable, "/api/v1/items/item-seed-06/place", `{"to":"first"}`, "application/json", "")
	if status != 503 || data["error"].(map[string]any)["code"] != "writer-unavailable" {
		t.Fatalf("%d %v", status, data)
	}
}
