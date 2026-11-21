package drain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func fixtureRead(t *testing.T, f refusalLoopFixture, read func(*store.Reader) error) {
	t.Helper()
	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.ReadTx(context.Background(), read); err != nil {
		t.Fatal(err)
	}
}

func TestCommentsInferRepositoryWithRelativeRoot(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	t.Chdir(f.root)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/r/"+filepath.Base(f.root)+"/review" {
			t.Errorf("repository inferred incorrectly: %s", r.URL)
		}
		w.Write([]byte(`{"comments":[]}`))
	}))
	defer server.Close()
	cfg := l.cfg
	cfg.Root = "."
	cfg.Comments = &settings.Comments{VantageURL: server.URL, Paths: []string{"docs/design/"}, PollSeconds: 15, QuietSeconds: 60}
	loop, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if loop.cfg.VantageRepo != filepath.Base(f.root) {
		t.Fatalf("confirmation repository: %q", loop.cfg.VantageRepo)
	}
	if err := loop.intakeComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("intake calls %d", calls)
	}
}

func TestAutomaticCommentsOff(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{"comments":[]}`)) }))
	defer server.Close()
	l.cfg.VantageURL = server.URL
	for i := 0; i < 100; i++ {
		result := LoopResult{}
		if _, err := l.pass(context.Background(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Dispatched != 0 {
			t.Fatalf("off dispatched: %+v", result)
		}
	}
	if calls != 0 {
		t.Fatalf("off made %d requests", calls)
	}
	if _, err := os.Stat(filepath.Join(f.laneRoot, "desk-example")); !os.IsNotExist(err) {
		t.Fatalf("off created desk: %v", err)
	}
}

func TestQuietCommentIntake(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	ctx := context.Background()
	now := time.Unix(1000, 0).UTC()
	l.cfg.Now = func() time.Time { return now }
	comments := []item.ReviewComment{}
	open := false
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		sessions := []map[string]string{}
		if open {
			sessions = append(sessions, map[string]string{"comment_id": "a", "state": "open"})
		}
		json.NewEncoder(w).Encode(map[string]any{"comments": comments, "comment_sessions": sessions})
	}))
	l.cfg.Comments = &settings.Comments{VantageURL: server.URL, Repo: "fixture", Paths: []string{"docs/design/"}, QuietSeconds: 60, PollSeconds: 15}
	defer server.Close()
	poll := func() {
		t.Helper()
		if err := l.intakeComments(ctx); err != nil {
			t.Fatal(err)
		}
	}
	count := func(want int) {
		t.Helper()
		fixtureRead(t, f, func(r *store.Reader) error {
			var n int
			err := r.QueryRow("SELECT count(*) FROM comment_batches").Scan(&n)
			if err == nil && n != want {
				t.Fatalf("batches %d want %d", n, want)
			}
			return err
		})
	}
	poll() // first-intake watermark
	comments = []item.ReviewComment{{ID: "a", Comment: "Why?", CreatedAt: 1001}, {ID: "older", Comment: "Old question", CreatedAt: 900}}
	now = time.Unix(1031, 0)
	poll()
	count(0) // 30 seconds
	now = time.Unix(1062, 0)
	open = true
	poll()
	count(0) // 61 seconds but open
	now = time.Unix(1077, 0)
	open = false
	poll()
	count(1)
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		err := r.QueryRow("SELECT older FROM comment_intake_documents WHERE document='docs/design/example.md'").Scan(&n)
		if n != 1 {
			t.Fatalf("older count %d", n)
		}
		return err
	})
	// New comment must wait while the first item is open.
	comments[0].EditedAt = 1078
	now = time.Unix(1140, 0)
	poll()
	count(1)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='closed' WHERE item_id LIKE 'item-comments-%'")
		return err
	})
	now = now.Add(15 * time.Second)
	poll()
	count(2)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("UPDATE items SET state='closed' WHERE item_id LIKE 'item-comments-%'")
		return err
	})
	now = now.Add(15 * time.Second)
	poll()
	count(2) // same identity is not retaken
	comments[0].Reactions = []json.RawMessage{json.RawMessage(`{"actor":"reviewer","kind":"needs_clarification","timestamp":1171}`)}
	now = time.Unix(1232, 0)
	poll()
	count(3)
	before := calls
	poll()
	if calls != before {
		t.Fatal("polled early")
	}
	server.Close()
	for i := 0; i < 3; i++ {
		now = now.Add(15 * time.Second)
		poll()
	}
	count(3)
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		var answer string
		err := r.QueryRow("SELECT count(*),last_answer FROM comment_intake WHERE unavailable=1").Scan(&n, &answer)
		if n != 1 || answer == "" {
			t.Fatalf("outage status %d %q", n, answer)
		}
		return err
	})
}

func TestCommentOutageCreatesNoBatch(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	now := time.Unix(1000, 0)
	l.cfg.Now = func() time.Time { return now }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"comments":[{"id":"a","comment":"Still typing","created_at":1001,"reactions":[]}]}`))
	}))
	defer server.Close()
	l.cfg.Comments = &settings.Comments{VantageURL: server.URL, Repo: "fixture", Paths: []string{"docs/design/"}, QuietSeconds: 60, PollSeconds: 15}
	if err := l.intakeComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()
	for i := 0; i < 3; i++ {
		now = now.Add(15 * time.Second)
		if err := l.intakeComments(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow("SELECT count(*) FROM comment_batches").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("outage created %d batches", count)
		}
		var last string
		if err := r.QueryRow("SELECT count(*),last_answer FROM comment_intake WHERE unavailable=1").Scan(&count, &last); err != nil {
			return err
		}
		if count != 1 || last != "1970-01-01T00:16:40Z" {
			t.Fatalf("outage status %d %q", count, last)
		}
		return nil
	})
}

func TestCommentsClosedBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name              string
		blocked, resolved bool
	}{
		{name: "deleted"},
		{name: "resolved", blocked: true, resolved: true},
		{name: "deleted-while-blocked", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, _ := commentFixture(t, "")
			comments := []item.ReviewComment{{ID: "a", Comment: "Why?", CreatedAt: 1}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"comments": comments})
			}))
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			if tc.resolved {
				comments[0].Resolved = true
			} else {
				comments = nil
			}
			if tc.blocked {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec("UPDATE items SET state='blocked' WHERE item_id=?", id)
					return err
				})
			}
			l.cfg.VantageURL = server.URL
			l.cfg.VantageRepo = "fixture"
			result, err := l.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Dispatched != 0 {
				t.Fatalf("dispatched dead batch: %+v", result)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var state, reason string
				err := r.QueryRow("SELECT state,close_reason FROM items WHERE item_id=?", id).Scan(&state, &reason)
				if reason != "review comments all resolved or deleted before dispatch" {
					t.Fatalf("closure reason %q", reason)
				}
				if state != "closed" {
					t.Fatalf("state %s", state)
				}
				return err
			})
		})
	}
}
