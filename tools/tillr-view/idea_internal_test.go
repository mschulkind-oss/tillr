package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func ideaGet(t *testing.T, url string) map[string]any {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("%d %v", response.StatusCode, body)
	}
	return body
}

func TestIdeasHTTPValidation(t *testing.T) {
	_, readonly := placeServer(t, false, "")
	status, body := postPlace(t, readonly, "/api/v1/ideas", `{}`, "text/plain", "https://foreign.example")
	if status != 405 || body["error"].(map[string]any)["code"] != "read-only" {
		t.Fatalf("%d %v", status, body)
	}
	_, srv := placeServer(t, true, "")
	for _, tc := range []struct {
		body, typ, origin, code string
		status                  int
	}{
		{`{"words":"hello","place":{"to":"first"}}`, "application/json", "https://foreign.example", "origin-refused", 403},
		{`{}`, "text/plain", "", "content-type", 415},
		{strings.Repeat("x", 1<<20+1), "application/json", "", "too-large", 413},
		{`{"words":"hello"}`, "application/json", "", "bad-request", 400},
		{`{"words":null,"place":{"to":"first"}}`, "application/json", "", "bad-request", 400},
		{`{"words":"hello","words":"again","place":{"to":"first"}}`, "application/json", "", "bad-request", 400},
		{`{"words":"hello","place":{"to":"first"},"extra":true}`, "application/json", "", "bad-request", 400},
		{`{"words":"hello","place":{"to":"first"}} {}`, "application/json", "", "bad-request", 400},
		{`{"words":"hello","place":{"to":"first","after":"x"}}`, "application/json", "", "bad-request", 400},
	} {
		status, body := postPlace(t, srv, "/api/v1/ideas", tc.body, tc.typ, tc.origin)
		if status != tc.status || body["error"].(map[string]any)["code"] != tc.code {
			t.Fatalf("%s: %d %v", tc.code, status, body)
		}
	}
}
func TestIdeasHTTPSavesWordsAndShowsIntake(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "tillr-item")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../tillr-item"
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	h, srv := placeServer(t, true, bin)
	if err := os.WriteFile(filepath.Join(h.dir, ".tillr.toml"), []byte("[intake]\ndir='docs/ideas'\npaths=['docs/design/']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	words := "Keep these words\n  exactly. 雪\n"
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(words)))
	id := "item-idea-" + hash[:10]
	for i := 0; i < 2; i++ {
		status, body := postPlace(t, srv, "/api/v1/ideas", `{"words":"Keep these words\n  exactly. 雪\n","place":{"to":"first"}}`, "application/json", "")
		if status != 200 || body["data"].(map[string]any)["item_id"] != id || body["data"].(map[string]any)["place"] != float64(1) {
			t.Fatalf("%d %v", status, body)
		}
	}
	snapshot := ideaGet(t, srv.URL+"/api/v1/snapshot")
	found := false
	for _, row := range snapshot["data"].(map[string]any)["items"].([]any) {
		item := row.(map[string]any)
		if item["item_id"] == id {
			found = true
			if item["kind"] != "idea" {
				t.Fatal(item)
			}
		}
	}
	if !found {
		t.Fatal("idea absent")
	}
	page := ideaGet(t, srv.URL+"/api/v1/items/"+id)
	idea := page["data"].(map[string]any)["idea"].(map[string]any)
	if idea["words"] != words || idea["in_tree"] != false {
		t.Fatal(idea)
	}
	wordsPath := filepath.Join(h.dir, "docs", "ideas", id+".txt")
	if err := os.MkdirAll(filepath.Dir(wordsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wordsPath, []byte(words), 0600); err != nil {
		t.Fatal(err)
	}
	loose := ideaGet(t, srv.URL+"/api/v1/items/"+id)["data"].(map[string]any)["idea"].(map[string]any)
	if loose["in_tree"] != false {
		t.Fatal("uncommitted file was called in the tree", loose)
	}
	for _, args := range [][]string{{"init"}, {"add", "docs/ideas/" + id + ".txt"}, {"commit", "-m", "test: commit idea words fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", h.dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.WriteFile(wordsPath, []byte("uncommitted edits"), 0600); err != nil {
		t.Fatal(err)
	}
	committed := ideaGet(t, srv.URL+"/api/v1/items/"+id)["data"].(map[string]any)["idea"].(map[string]any)
	if committed["in_tree"] != true || committed["words"] != words {
		t.Fatal("committed words were not read verbatim", committed)
	}
	for _, tc := range []struct {
		words, code string
		status      int
	}{{" ", "words-empty", 422}, {strings.Repeat("x", 256*1024+1), "words-too-large", 422}} {
		status, body := postPlace(t, srv, "/api/v1/ideas", fmt.Sprintf(`{"words":%q,"place":{"to":"first"}}`, tc.words), "application/json", "")
		if status != tc.status || body["error"].(map[string]any)["code"] != tc.code {
			t.Fatalf("%d %v", status, body)
		}
	}
	os.WriteFile(filepath.Join(h.dir, ".tillr.toml"), nil, 0600)
	status, body := postPlace(t, srv, "/api/v1/ideas", `{"words":"another","place":{"to":"first"}}`, "application/json", "")
	if status != 409 || body["error"].(map[string]any)["code"] != "intake-not-configured" {
		t.Fatalf("%d %v", status, body)
	}

}
