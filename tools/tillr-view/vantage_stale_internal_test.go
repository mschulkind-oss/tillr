package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const staleTestIndexOld = `{"index":{"refused":false,"config":{"stages":{}},"documents":[{"path":"docs/old.md","questions":[]}]}}`
const staleTestIndexNew = `{"index":{"refused":false,"config":{"stages":{}},"documents":[{"path":"docs/new.md","questions":[]}]}}`

type staleRig struct {
	c       *vantageCache
	clock   *fakeClock
	runs    atomic.Int32
	mu      sync.Mutex
	fp      string
	answer  string
	failure error
	release chan struct{}
}

func newStaleRig() *staleRig {
	r := &staleRig{clock: &fakeClock{t: time.Date(2026, 11, 22, 12, 0, 0, 0, time.Local)}, fp: "tree-1", answer: staleTestIndexOld, release: make(chan struct{})}
	r.c = &vantageCache{root: "/r", now: r.clock.now, maxAge: 10 * time.Minute,
		fingerprint: func(string) (string, error) { r.mu.Lock(); defer r.mu.Unlock(); return r.fp, nil },
		run: func(context.Context, string, []string) ([]byte, error) {
			r.runs.Add(1)
			<-r.release
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.failure != nil {
				return nil, r.failure
			}
			return []byte(r.answer), nil
		}}
	return r
}

func (r *staleRig) edit(fp, answer string) {
	r.mu.Lock()
	r.fp, r.answer = fp, answer
	r.mu.Unlock()
}

// A document change used to block the next request for the whole walk (27 s on this
// repository), and the drain edits documents constantly. Now the previous index is served at
// once and marked stale, one rebuild runs, and the next request after it finishes uses it.
func TestVantageCacheServesTheOldIndexWhileOneRebuildRuns(t *testing.T) {
	r := newStaleRig()
	// The first request, with no index yet, waits for the walk.
	first := make(chan vantageLookup, 1)
	go func() {
		_, look, err := r.c.get(context.Background())
		if err != nil {
			t.Error(err)
		}
		first <- look
	}()
	waitFor(t, func() bool { return r.runs.Load() == 1 })
	select {
	case <-first:
		t.Fatal("the first request answered before any index existed")
	default:
	}
	r.release <- struct{}{}
	if look := <-first; look.Cache != "miss" || look.Stale {
		t.Fatalf("first lookup %+v", look)
	}
	// A document changes. Requests answer at once with the old index, marked stale.
	r.edit("tree-2", staleTestIndexNew)
	r.clock.advance(30 * time.Second)
	for i := 0; i < 3; i++ {
		raw, look, err := r.c.get(context.Background())
		if err != nil || string(raw) != staleTestIndexOld {
			t.Fatalf("request %d: %v %s", i, err, raw)
		}
		if look.Cache != "stale" || !look.Stale || !look.Refreshing || look.AgeMS != 30000 || look.RefreshError != "" {
			t.Fatalf("request %d: %+v", i, look)
		}
	}
	waitFor(t, func() bool { return r.runs.Load() == 2 })
	if r.runs.Load() != 2 {
		t.Fatalf("%d walks for three stale requests, want 1 rebuild (2 in all)", r.runs.Load())
	}
	// The rebuild finishes: the next request uses it.
	r.release <- struct{}{}
	waitFor(t, func() bool {
		raw, look, _ := r.c.get(context.Background())
		return string(raw) == staleTestIndexNew && look.Cache == "hit" && !look.Stale
	})
	if r.runs.Load() != 2 {
		t.Fatalf("%d walks after the rebuild, want 2", r.runs.Load())
	}
}

// A rebuild that fails keeps the previous index and says so in every answer.
func TestVantageCacheKeepsTheOldIndexAndReportsAFailedRebuild(t *testing.T) {
	r := newStaleRig()
	go func() { r.release <- struct{}{} }()
	if _, _, err := r.c.get(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.edit("tree-2", staleTestIndexNew)
	r.mu.Lock()
	r.failure = errors.New("uvx: not found")
	r.mu.Unlock()
	if _, look, _ := r.c.get(context.Background()); !look.Stale || look.RefreshError != "" {
		t.Fatalf("before the failure is known: %+v", look)
	}
	waitFor(t, func() bool { return r.runs.Load() == 2 })
	r.release <- struct{}{}
	waitFor(t, func() bool {
		raw, look, _ := r.c.get(context.Background())
		return string(raw) == staleTestIndexOld && look.Stale && look.RefreshError != ""
	})
	_, look, err := r.c.get(context.Background())
	if err != nil || look.RefreshError != "running the index: uvx: not found" && look.RefreshError != "uvx: not found" {
		t.Fatalf("failed rebuild report: %v %+v", err, look)
	}
	// A later rebuild that works clears it.
	r.mu.Lock()
	r.failure = nil
	r.mu.Unlock()
	waitFor(t, func() bool { return r.runs.Load() >= 3 })
	r.release <- struct{}{}
	waitFor(t, func() bool {
		raw, look, _ := r.c.get(context.Background())
		return string(raw) == staleTestIndexNew && !look.Stale && look.RefreshError == ""
	})
}

// /why says plainly that the document index is being refreshed and how old the one it used
// is, so the page can show it.
func TestWhyAnswerSaysWhenTheDocumentIndexIsStale(t *testing.T) {
	h := newServeHarness(t)
	r := newStaleRig()
	h.srv.vantage = r.c
	planner := filepath.Join(t.TempDir(), "tillr-drain")
	writeScript(t, planner, "cat >/dev/null\nprintf '%s' '{\"event_head_seq\":1,\"rows\":[],\"summary\":{\"held\":{}}}'\n")
	h.srv.plannerFlag = planner
	go func() { r.release <- struct{}{} }()
	status, body, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("%d %s", status, raw)
	}
	index := body["data"].(map[string]any)["document_index"].(map[string]any)
	if index["state"] != "current" {
		t.Fatalf("a fresh index: %v", index)
	}
	r.edit("tree-2", staleTestIndexNew)
	r.clock.advance(45 * time.Second)
	status, body, raw = h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("%d %s", status, raw)
	}
	index = body["data"].(map[string]any)["document_index"].(map[string]any)
	if index["state"] != "refreshing" || index["age_ms"] != float64(45000) || index["message"] == "" {
		t.Fatalf("a stale index: %v", index)
	}
	r.release <- struct{}{}
	_ = os.Stderr
}
