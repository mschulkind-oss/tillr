package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Every test here uses fixture backends and a fixed clock: no provider is called.

// The loop stamps each loaded policy with its configured profile name, and a reload
// of the policy in force keeps the name, since the document never carries it.
func TestPolicyProfileIsStampedAtStartupAndKeptAcrossReload(t *testing.T) {
	f, base := passFixture(t, "item-a")
	write := func(name string, p Policy) string {
		t.Helper()
		path := filepath.Join(f.root, "backend-policy."+name+".json")
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, path, string(raw))
		return path
	}
	pref := Policy{DefaultBackend: "pref", Backends: map[string]Backend{"pref": {Adapter: "/bin/true", Model: "m-pref"}}}
	fall := Policy{DefaultBackend: "fall", Backends: map[string]Backend{"fall": {Adapter: "/bin/true", Model: "m-fall"}}}
	prefRef, fallRef := write("pref", pref), write("fall", fall)
	cfg := base.cfg
	cfg.Root = f.root
	cfg.PolicyRef, cfg.PolicyProfile = prefRef, "pref"
	cfg.PolicyProfiles, cfg.PolicyProfileRefs = []string{"pref", "fall"}, []string{prefRef, fallRef}
	l, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if l.cfg.Policy.Profile != "pref" || l.profilePolicies[0].Profile != "pref" || l.profilePolicies[1].Profile != "fall" {
		t.Fatalf("profiles = %q / %q, %q; want pref / pref, fall", l.cfg.Policy.Profile, l.profilePolicies[0].Profile, l.profilePolicies[1].Profile)
	}
	pref.Backends["pref"] = Backend{Adapter: "/bin/true", Model: "m-pref-2"}
	write("pref", pref)
	if err := l.refreshPolicy(); err != nil {
		t.Fatal(err)
	}
	if got := l.cfg.Policy.Backends["pref"].Model; got != "m-pref-2" {
		t.Fatalf("reload not in force: model %q", got)
	}
	if l.cfg.Policy.Profile != "pref" {
		t.Fatalf("profile after reload = %q, want pref", l.cfg.Policy.Profile)
	}
}

func TestSeatEscalationIsStampedWithTheProfileInForce(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Policy = &Policy{Profile: "pref", DefaultBackend: "fast", Seats: map[string]string{"build": "fast"},
		Escalate: map[string][]string{"build": {"strong"}},
		Backends: map[string]Backend{"fast": {Adapter: "fixture"}, "strong": {Adapter: "fixture"}}}
	next, err := l.escalateSeat(ctx, "item-a", "run-x", "build", "fast")
	if err != nil || next != "strong" {
		t.Fatalf("escalateSeat = %q, %v", next, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		all, err := readEscalations(r)
		if err != nil {
			return err
		}
		list := all["item-a"]
		if len(list) != 1 || list[0].Profile != "pref" {
			t.Fatalf("recorded escalations = %+v, want one stamped with profile pref", list)
		}
		fallback := &Policy{Profile: "fall", Backends: map[string]Backend{"cheap": {}}}
		if got := escalatedBackend(list, fallback); got != "" {
			t.Fatalf("fallback profile applied the preferred profile's escalation to %q", got)
		}
		if got := escalatedBackend(list, l.cfg.Policy); got != "strong" {
			t.Fatalf("preferred profile after switch-back = %q, want strong", got)
		}
		return nil
	})
}

// A fallback profile's designer turn must not overwrite the preferred designer's
// saved session: both stay saved under their own (document, backend, route) key,
// the latest-row reader still sees the newest, and retirement removes every row.
func TestFallbackDesignerTurnKeepsThePreferredDesignerSession(t *testing.T) {
	f, l, _ := commentFixture(t, "")
	doc := "docs/design/example.md"
	save := func(session, backend, model string) {
		t.Helper()
		d := lastDesigner{Document: doc, SessionID: session, Backend: backend, Model: model, RunID: "run-" + session}
		raw, _ := json.Marshal(d)
		fixtureWrite(t, f, func(tx *store.Tx) error {
			// The same two upserts recordDesigner makes.
			for _, key := range []string{doc, designerKey(doc, backend, model)} {
				if _, err := tx.Exec("INSERT INTO last_designers(document,record) VALUES(?,?) ON CONFLICT(document) DO UPDATE SET record=excluded.record", key, string(raw)); err != nil {
					return err
				}
			}
			return nil
		})
	}
	save("session-pref", "pref", "m-pref")
	save("session-fall", "fall", "m-fall")
	fixtureRead(t, f, func(r *store.Reader) error {
		latest, err := readDesigner(r, doc)
		if err != nil || latest == nil || latest.SessionID != "session-fall" {
			t.Fatalf("latest designer = %+v, %v; want session-fall", latest, err)
		}
		pref, err := readDesignerFor(r, doc, "pref", "m-pref")
		if err != nil || pref == nil || pref.SessionID != "session-pref" {
			t.Fatalf("preferred designer = %+v, %v; want session-pref still saved", pref, err)
		}
		if all, err := readDesignersFor(r, doc); err != nil || len(all) != 2 {
			t.Fatalf("designers for the document = %d, %v; want 2", len(all), err)
		}
		return nil
	})

	// The document is finished: retirement removes the plain row and the keyed rows.
	index := filepath.Join(t.TempDir(), "index")
	writeForkFile(t, index, "#!/bin/sh\necho '{\"index\":{\"config\":{\"stages\":{\"FINISHED\":\"done\"}},\"documents\":[{\"path\":\"docs/design/example.md\",\"stage\":\"FINISHED\",\"questions\":[]}]}}'\n")
	if err := os.Chmod(index, 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.QuestionsCmd = []string{index}
	if err := l.retireDesigners(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		latest, err := readDesigner(r, doc)
		if err != nil || latest != nil {
			t.Fatalf("latest after retirement = %+v, %v", latest, err)
		}
		if all, err := readDesignersFor(r, doc); err != nil || len(all) != 0 {
			t.Fatalf("keyed designers after retirement = %d, %v", len(all), err)
		}
		return nil
	})
}

func TestPlanResumesThePreferredDesignerAfterSwitchBack(t *testing.T) {
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	transcript := filepath.Join(dir, "pref.jsonl")
	writeForkFile(t, transcript, "kept")
	pref := &lastDesigner{Document: "d.md", SessionID: "session-pref", Backend: "pref", Model: "m-pref", Transcript: transcript}
	fall := &lastDesigner{Document: "d.md", SessionID: "session-fall", Backend: "fall", Model: "m-fall", Transcript: transcript}
	saved := []*lastDesigner{fall, pref}
	req := func(backend, model string) *run.Request {
		return &run.Request{Backend: backend, Command: []string{"adapter", "--model", model}}
	}
	atPref := req("pref", "m-pref")
	got := pickDesigner(fall, saved, atPref)
	if got != pref {
		t.Fatalf("picked %+v, want the preferred designer", got)
	}
	if session, reason := chooseDesigner(got, atPref, at, ""); session != "session-pref" || !strings.HasPrefix(reason, "resumed-") {
		t.Fatalf("after switch-back: %q %q, want the preferred session resumed", session, reason)
	}
	// A backend with no saved session of its own gets a fresh one, never the preferred one.
	atNew := req("other", "m-other")
	if got := pickDesigner(fall, saved, atNew); got != fall {
		t.Fatalf("picked %+v", got)
	}
	if session, reason := chooseDesigner(fall, atNew, at, ""); session != "" || reason != "seat-changed" {
		t.Fatalf("different backend: %q %q, want a fresh seat-changed session", session, reason)
	}
}
