package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// sharedRoutes are the routes whose answers are built once per store change.
var sharedRoutes = []struct {
	path   string
	builds func(*server) *sharedAnswers
}{
	{"/api/v1/snapshot", func(s *server) *sharedAnswers { return &s.snapshotShare }},
	{"/api/v1/lanes", func(s *server) *sharedAnswers { return &s.lanesShare }},
	{"/api/v1/lanes?history=lane-a", func(s *server) *sharedAnswers { return &s.lanesShare }},
	{"/api/v1/passes", func(s *server) *sharedAnswers { return &s.passesShare }},
}

func appendTestEvent(t *testing.T, h *serveHarness) {
	t.Helper()
	db, err := store.Open(context.Background(), h.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := event.Register("test.shared", "view-test"); err != nil {
		t.Fatal(err)
	}
	id := "item-seed-02"
	if err := db.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: "test.shared", ObjectType: "item", ObjectID: &id, Payload: json.RawMessage(`{}`), At: time.Now()})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func dataOf(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unreadable response: %v\n%s", err, raw)
	}
	return string(body.Data)
}

func TestSharedAnswersBuildOncePerStoreChange(t *testing.T) {
	for _, route := range sharedRoutes {
		t.Run(route.path, func(t *testing.T) {
			h := newServeHarness(t)
			share := route.builds(h.srv)
			const clients = 12
			answers := make([]string, clients)
			var wg sync.WaitGroup
			for i := 0; i < clients; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					status, raw := h.getRaw(route.path)
					if status != http.StatusOK {
						t.Errorf("%s answered %d: %s", route.path, status, raw)
					}
					answers[i] = dataOf(t, raw)
				}()
			}
			wg.Wait()
			if n := share.builds.Load(); n != 1 {
				t.Fatalf("%d concurrent requests at one store state ran the builder %d times, want 1", clients, n)
			}
			for i := range answers {
				if answers[i] != answers[0] {
					t.Fatalf("request %d got a different answer from request 0", i)
				}
			}

			// Byte-identical to the unshared path: a second server on the same store has
			// shared nothing, so its first answer is a plain build.
			other := newServer(context.Background(), h.store, h.loopDir, h.laneRoot, h.traceBin, h.dir, "", []string{"false"})
			defer other.close()
			ts := httptest.NewServer(other.routes())
			defer ts.Close()
			resp, err := http.Get(ts.URL + route.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var buf [1 << 20]byte
			var fresh []byte
			for {
				n, err := resp.Body.Read(buf[:])
				fresh = append(fresh, buf[:n]...)
				if err != nil {
					break
				}
			}
			if got := dataOf(t, fresh); got != answers[0] {
				t.Fatalf("the shared answer differs from the unshared build:\nshared: %.400s\nfresh:  %.400s", answers[0], got)
			}

			// A new event makes the next request rebuild, and only the next one.
			appendTestEvent(t, h)
			before := share.builds.Load()
			h.getRaw(route.path)
			h.getRaw(route.path)
			if n := share.builds.Load() - before; n != 1 {
				t.Fatalf("after a new event two requests ran the builder %d times, want 1", n)
			}
		})
	}
}

// A lane log that grows with no store write is an input of the lanes answer: it is
// rebuilt, and an unchanged log is not.
func TestSharedLanesAnswerFollowsItsLogFiles(t *testing.T) {
	h := newServeHarness(t)
	share := &h.srv.lanesShare
	log := filepath.Join(h.laneRoot, "lane-a")
	if err := os.MkdirAll(log, 0o755); err != nil {
		t.Fatal(err)
	}
	path := runLogPath(h.laneRoot, "lane-a", "run-2")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.getRaw("/api/v1/lanes")
	h.getRaw("/api/v1/lanes")
	if n := share.builds.Load(); n != 1 {
		t.Fatalf("an unchanged log rebuilt the answer: %d builds", n)
	}
	if err := os.WriteFile(path, []byte("{}\n{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.getRaw("/api/v1/lanes")
	if n := share.builds.Load(); n != 2 {
		t.Fatalf("a changed log did not rebuild the answer: %d builds", n)
	}
}

// A request that arrived before a build began is owed that build even if the store moved
// while it ran; a request that arrived after the build began and finds a moved store is not.
func TestSharedAnswerIsOwedToRequestsThatArrivedBeforeTheBuild(t *testing.T) {
	h := newServeHarness(t)
	var shared sharedAnswers
	call := func(arrived int64) {
		t.Helper()
		ctx := context.WithValue(context.Background(), arrivalKey{}, arrived)
		err := h.srv.store.ReadTx(ctx, func(r *store.Reader) error {
			shared.get(ctx, r, "k", nil, func() shareBuild { return shareBuild{value: 1} })
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	const late = int64(1) << 40
	call(late)
	appendTestEvent(t, h) // the store moves while later requests queue
	call(0)               // arrived before the build began
	if n := shared.builds.Load(); n != 1 {
		t.Fatalf("a request that arrived before the build rebuilt: %d builds", n)
	}
	call(late) // arrived after the build began, and the store has moved
	if n := shared.builds.Load(); n != 2 {
		t.Fatalf("a later request on a moved store reused the old answer: %d builds", n)
	}
}
