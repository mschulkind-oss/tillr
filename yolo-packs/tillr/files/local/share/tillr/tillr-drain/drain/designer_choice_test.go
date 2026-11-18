package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDesignerDispatchChoices(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, reason := range []string{"no-designer", "cannot-resume", "seat-changed", "transcript-missing", "resumed-warm", "resumed-expired", "resumed-compacted"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			transcript := filepath.Join(dir, "session.jsonl")
			writeForkFile(t, transcript, "transcript")
			writeForkFile(t, filepath.Join(dir, "compact"), "#!/bin/sh\n")
			if err := os.Chmod(filepath.Join(dir, "compact"), 0755); err != nil {
				t.Fatal(err)
			}
			lifetime, tokens := 3600, 50000
			d := &lastDesigner{SessionID: "old-session", Backend: "pi", Model: "model", Transcript: transcript, LastRequest: at.Add(-2 * time.Hour).Format(time.RFC3339Nano), CacheLifetimeSeconds: &lifetime, ContextTokens: &tokens}
			req := run.Request{Backend: "pi", Command: []string{filepath.Join(dir, "run-pi"), "--model", "model"}}
			switch reason {
			case "no-designer":
				d = nil
			case "cannot-resume":
				writeForkFile(t, filepath.Join(dir, "pause-kind"), "cannot-resume\n")
				d.Backend = "old-pi"
				d.Model = "other"
				d.Transcript = "missing"
			case "seat-changed":
				d.Model = "other"
				d.Transcript = "missing"
			case "transcript-missing":
				d.Transcript = "missing"
			case "resumed-warm":
				d.LastRequest = at.Add(-time.Minute).Format(time.RFC3339Nano)
			case "resumed-expired":
				tokens = 40000
			}
			session, got := chooseDesigner(d, &req, at, req.Command[0])
			if got != reason || (session == "old-session") != (reason == "resumed-warm" || reason == "resumed-expired" || reason == "resumed-compacted") {
				t.Fatalf("%s %s", session, got)
			}
		})
	}
}

func TestDesignerLifetimeAndNoCompactAdapter(t *testing.T) {
	at := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, lifetime := range []int{300, 3600} {
		for _, delta := range []int{lifetime - 1, lifetime + 1} {
			tokens := 50000
			file := filepath.Join(t.TempDir(), "session")
			writeForkFile(t, file, "old")
			d := &lastDesigner{SessionID: "old", Backend: "fixture", Model: "model", Transcript: file, LastRequest: at.Format(time.RFC3339Nano), CacheLifetimeSeconds: &lifetime, ContextTokens: &tokens}
			req := run.Request{Backend: "fixture", Command: []string{filepath.Join(t.TempDir(), "adapter"), "--model", "model"}}
			_, reason := chooseDesigner(d, &req, at.Add(time.Duration(delta)*time.Second), req.Command[0])
			want := "resumed-expired"
			if delta < lifetime {
				want = "resumed-warm"
			}
			if reason != want {
				t.Fatalf("lifetime %d delta %d: %s", lifetime, delta, reason)
			}
		}
	}
}

func putDesigner(t *testing.T, f refusalLoopFixture, d lastDesigner) {
	t.Helper()
	if d.RunID == "" {
		d.RunID = "previous-run"
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec("INSERT INTO last_designers(document,record) VALUES(?,?) ON CONFLICT(document) DO UPDATE SET record=excluded.record", d.Document, string(raw))
		return err
	})
}

func TestDesignerExpiryDoesNothingWithoutDispatch(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	dir := t.TempDir()
	marker := filepath.Join(dir, "compacted")
	writeForkFile(t, filepath.Join(dir, "compact"), "#!/bin/sh\ntouch '"+marker+"'\n")
	os.Chmod(filepath.Join(dir, "compact"), 0755)
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: filepath.Join(dir, "run"), Model: "fixture-model"}
	lifetime, tokens := 300, 50000
	putDesigner(t, f, lastDesigner{Document: "docs/design/example.md", SessionID: "old", Backend: "fixture", Model: "fixture-model", LastRequest: l.cfg.Now().Add(-299 * time.Second).Format(time.RFC3339Nano), CacheLifetimeSeconds: &lifetime, ContextTokens: &tokens})
	mustPlan(t, l)
	now := l.cfg.Now().Add(2 * time.Second)
	l.cfg.Now = func() time.Time { return now }
	mustPlan(t, l)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expiry compacted: %v", err)
	}
}

func TestFailedDesignerResumeStartsNewSessionOnce(t *testing.T) {
	f, l, steps := commentFixture(t, "")
	number := 1
	server := stubReview(t, &number)
	defer server.Close()
	takeFixtureComment(t, f, server.URL)
	// Both loops exit when drained. A shared elapsed deadline can cancel the second
	// loop between its replacement's terminal record and gate, before this test's
	// settlement assertions; wait for the protocol's completion instead.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := l.Run(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.root, ".vantage/inbox/*.jsonl"))
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	steps.resumeFailureSession = steps.sessions[0]
	oldTranscript := ""
	st, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	err = st.ReadTx(ctx, func(r *store.Reader) error {
		d, e := readDesigner(r, "docs/design/example.md")
		if e == nil {
			oldTranscript = d.Transcript
		}
		return e
	})
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	number = 2
	id := takeFixtureComment(t, f, server.URL)
	stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	l = f.loop(t, steps, stage)
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "fixture", Model: "fixture-model"}
	steps.replacementTerminal = make(chan struct{})
	release := make(chan struct{})
	steps.replacementReturn = release
	type answer struct {
		result LoopResult
		err    error
	}
	finished := make(chan answer, 1)
	go func() { r, e := l.Run(ctx); finished <- answer{r, e} }()
	select {
	case <-steps.replacementTerminal:
	case <-ctx.Done():
		t.Fatal("replacement never terminated")
	}
	parent := ""
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == id {
			parent = p.parentRunID
		}
	}
	close(release)
	out := <-finished
	result, err := out.result, out.err
	if parent != steps.requests[1].RunID {
		t.Fatalf("replacement can settle while parent is active: parent %q want %q", parent, steps.requests[1].RunID)
	}
	if err != nil {
		t.Fatalf("%+v %v", result, err)
	}
	if len(steps.sessions) != 3 || steps.sessions[1] != steps.sessions[0] || steps.sessions[2] == steps.sessions[0] {
		t.Fatalf("sessions %v", steps.sessions)
	}
	if result.Stop != StopQueueDrained || result.Dispatched != 2 || steps.gates != 2 {
		t.Fatalf("dispatch count %+v gates %d", result, steps.gates)
	}
	if !strings.Contains(steps.briefs[2], oldTranscript) {
		t.Fatal("replacement lacks old transcript")
	}
	st, err = store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	err = st.ReadTx(ctx, func(r *store.Reader) error {
		var failed int
		if err := r.QueryRow("SELECT count(*) FROM comment_runs WHERE item_id=? AND outcome='resume-failed'", id).Scan(&failed); err != nil {
			return err
		}
		if failed != 1 {
			t.Fatalf("resume failures %d", failed)
		}
		d, e := readDesigner(r, "docs/design/example.md")
		if e == nil && d.SessionID != steps.sessions[2] {
			t.Fatalf("replacement row %+v", d)
		}
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDesignerCompactRunsOnlyAtColdDispatch(t *testing.T) {
	for _, warm := range []bool{true, false} {
		t.Run(fmt.Sprint(warm), func(t *testing.T) {
			f, l, steps := commentFixture(t, "")
			dir := t.TempDir()
			marker := filepath.Join(dir, "compacted")
			compact := filepath.Join(dir, "compact")
			writeForkFile(t, compact, "#!/bin/sh\necho compact >> '"+marker+"'\necho '{\"tokens_before\":50000,\"tokens_after\":20000,\"tokens\":1000,\"cost_usd\":0.12}'\n")
			os.Chmod(compact, 0755)
			l.cfg.Policy.Backends["fixture"] = Backend{Adapter: filepath.Join(dir, "run"), Model: "fixture-model"}
			lifetime, tokens := 300, 50000
			age := 301
			if warm {
				age = 299
			}
			transcript := filepath.Join(dir, "old.jsonl")
			writeForkFile(t, transcript, "old")
			putDesigner(t, f, lastDesigner{Compacted: warm, Document: "docs/design/example.md", SessionID: "11111111-1111-4111-8111-111111111111", Backend: "fixture", Model: "fixture-model", Transcript: transcript, BlobID: runGit(t, f.root, "rev-parse", "HEAD:docs/design/example.md"), LastRequest: l.cfg.Now().Add(-time.Duration(age) * time.Second).Format(time.RFC3339Nano), CacheLifetimeSeconds: &lifetime, ContextTokens: &tokens})
			number := 1
			server := stubReview(t, &number)
			defer server.Close()
			id := takeFixtureComment(t, f, server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			if _, err := l.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if len(steps.sessions) != 1 || steps.sessions[0] != "11111111-1111-4111-8111-111111111111" {
				t.Fatalf("sessions %v", steps.sessions)
			}
			stCheck, e := store.OpenRead(ctx, f.storePath)
			if e != nil {
				t.Fatal(e)
			}
			e = stCheck.ReadTx(ctx, func(r *store.Reader) error {
				d, e := readDesigner(r, "docs/design/example.md")
				if e == nil && !d.Compacted {
					t.Fatalf("lost compaction history: %+v", d)
				}
				return e
			})
			stCheck.Close()
			if e != nil {
				t.Fatal(e)
			}
			raw, err := os.ReadFile(marker)
			if warm {
				if !os.IsNotExist(err) {
					t.Fatalf("warm compacted: %v", err)
				}
			} else {
				if err != nil || string(raw) != "compact\n" {
					t.Fatalf("compact %s %v", raw, err)
				}
				st, e := store.OpenRead(ctx, f.storePath)
				if e != nil {
					t.Fatal(e)
				}
				defer st.Close()
				e = st.ReadTx(ctx, func(r *store.Reader) error {
					events, e := r.Events(0)
					if e != nil {
						return e
					}
					found := false
					for _, v := range events {
						if v.Kind == designerCompactedKind {
							found = true
							if !strings.Contains(string(v.Payload), id) || !strings.Contains(string(v.Payload), "0.12") {
								t.Fatalf("receipt %s", v.Payload)
							}
						}
					}
					if !found {
						t.Fatal("no item cost receipt")
					}
					return nil
				})
				if e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
