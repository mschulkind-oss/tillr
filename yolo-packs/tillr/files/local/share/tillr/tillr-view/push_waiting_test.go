package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func pushWaitingRow(t *testing.T, h *serveHarness, payload, tip string) waitingRow {
	t.Helper()
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (1700000000000,'drain.post-landing-action','landing','` + tip + `','` + payload + `')`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Observation == "post-landing:push" {
			return row
		}
	}
	t.Fatalf("no push row for %s", payload)
	return waitingRow{}
}

func TestWaitingStructuredPushStates(t *testing.T) {
	h := newServeHarness(t)
	const R = "rrrrrrrrrrrrrrrrrrrr"
	cases := []struct{ name, payload, tip, class, mover, why, since string }{
		{"proving", `{"action":"push","what":"green","kind":"remote-sync-proving","detail":"rebased 2 local commit(s) onto remote rrrrrrrrrrrr at tttttttttttt; awaiting heavy proof before pushing\nmore","remote":"origin","ref":"main","remote_tip":"` + R + `","since_ts":"2026-11-20T10:00:00Z","unpublished_green_count":1}`,
			"ttttttttttttttttttttt", "waits on work", "the heavy runner; prove rebased tip tttttttttttt before pushing", "rebased 2 local commit(s) onto remote rrrrrrrrrrrr at tttttttttttt; awaiting heavy proof before pushing; 1 proven commit(s) unpublished", "2026-11-20T10:00:00Z"},
		{"proving names rebased tip", `{"action":"push","what":"green","kind":"remote-sync-proving","detail":"d","remote":"origin","ref":"main","rebased_tip":"bbbbbbbbbbbbbbbbbb"}`,
			"ttt", "waits on work", "the heavy runner; prove rebased tip bbbbbbbbbbbb before pushing", "d", ""},
		{"counts both", `{"action":"push","kind":"remote-sync-conflict","detail":"d","remote":"origin","ref":"main","remote_tip":"` + R + `","unpublished_green_count":2,"unpublished_main_count":5}`,
			"t", "waits on work", "sync-remote-rrrrrrrrrrrr; resolve rebase conflict onto origin/main (rrrrrrrrrrrr)", "d; 2 proven / 5 total local commit(s) unpublished", ""},
		{"escalated sync item", `{"action":"push","kind":"remote-sync-conflict","detail":"x\nmover: owner; resolve sync-remote-rrrr conflict onto origin/main (rrrr)","escalated_mover":"owner"}`,
			"t", "needs you", "owner; resolve sync-remote-rrrr conflict onto origin/main (rrrr)", "x", ""},
		{"conflict active", `{"action":"push","kind":"remote-sync-conflict","detail":"conflict","remote":"origin","ref":"main","remote_tip":"` + R + `"}`,
			"t", "waits on work", "sync-remote-rrrrrrrrrrrr; resolve rebase conflict onto origin/main (rrrrrrrrrrrr)", "conflict", "2023-11-14T22:13:20Z"},
		{"conflict owner", `{"action":"push","kind":"remote-sync-conflict","detail":"c","remote":"origin","ref":"main","remote_tip":"` + R + `","escalated_mover":"owner"}`,
			"t", "needs you", "owner; resolve gate-code conflict onto origin/main (rrrrrrrrrrrr)", "c", ""},
		{"conflict operator", `{"action":"push","kind":"remote-sync-conflict","detail":"c","remote":"origin","ref":"main","remote_tip":"` + R + `","escalated_mover":"operator"}`,
			"t", "needs you", "operator; rebase main onto origin/main (rrrrrrrrrrrr)", "c", ""},
		{"dirty", `{"action":"push","kind":"remote-sync-worktree-dirty","detail":"d","rebased_tip":"bbbbbbbbbbbbbbbbbb"}`,
			"t", "needs you", "operator; commit, stash, or restore clashing paths in the main checkout", "cannot reset main to rebased tip bbbbbbbbbbbb", ""},
		{"rewritten", `{"action":"push","kind":"remote-rewritten","detail":"d","remote":"origin","ref":"main","remote_tip":"` + R + `","previous_remote_tip":"pppp"}`,
			"t", "needs you", "operator; reconcile force-pushed origin/main (pppp -> rrrrrrrrrrrr)", "", ""},
		{"patch held", `{"action":"push","kind":"push-patch-move-held","detail":"d"}`,
			"t", "needs you", "operator; run tillr drain acknowledge-patch-move after checking the fork rewrite", "", ""},
		{"declined", `{"action":"push","kind":"push-remote-declined","detail":"hook said no\nmore","remote":"origin","ref":"main"}`,
			"ppppppppppppppp", "needs you", "operator; inspect remote rejection on origin main and run tillr drain push", "remote server declined pppppppppppp: hook said no", ""},
		{"total only", `{"action":"push","kind":"push-failed","unpublished_main_count":9}`,
			"ppp", "needs you", "operator; check git-broker approval or remote access, or run tillr drain push", "9 total local commit(s) unpublished", ""},
		{"failed", `{"action":"push","kind":"push-failed","detail":"no broker"}`,
			"ppp", "needs you", "operator; check git-broker approval or remote access, or run tillr drain push", "push of ppp did not reach a ref update (no broker)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := pushWaitingRow(t, h, c.payload, c.tip)
			if row.Class != c.class || row.Mover != c.mover || !strings.Contains(row.Why, c.why) {
				t.Fatalf("row = %+v", row)
			}
			normalizeWaitingMover(&row)
			if row.Mover != c.mover {
				t.Fatalf("CLI changed the named mover to %q", row.Mover)
			}
			if c.since != "" && row.Since != c.since {
				t.Fatalf("since = %q, want %q", row.Since, c.since)
			}
			if row.Since == "—" || row.Since == "" {
				t.Fatalf("since must be real: %q", row.Since)
			}
		})
	}
}

func TestWaitingRetriedConflictStillNeedsYou(t *testing.T) {
	for _, tc := range []struct{ who, reason, action string }{
		{"owner", "owner", "resolve sync-remote-rrrrrrrrrrrr conflict"},
		{"operator", "tooling", "rebase main"},
	} {
		t.Run(tc.who, func(t *testing.T) {
			h := newServeHarness(t)
			pushWaitingRow(t, h, `{"action":"push","kind":"remote-sync-conflict","detail":"active repair"}`, "t")
			const destination = `"remote":"origin","ref":"refs/heads/main","remote_tip":"rrrrrrrrrrrrrrrr"`
			mover := tc.who + "; " + tc.action + " onto origin/main (rrrrrrrrrrrr)"
			payload := `{"action":"push","kind":"remote-sync-conflict",` + destination + `,"escalated_mover":"` + tc.who + `","detail":"remote-sync-conflict: sync-remote-rrrrrrrrrrrr escalated (` + tc.reason + `) on café.txt\nmover: ` + mover + `"}`
			pushWaitingRow(t, h, payload, "t")         // worker escalation
			row := pushWaitingRow(t, h, payload, "t2") // retry on the same remote tip
			if row.Class != "needs you" || row.Mover != mover || !strings.Contains(row.Why, "escalated ("+tc.reason+") on café.txt") {
				t.Fatalf("waiting row after retry = %+v", row)
			}
			var text bytes.Buffer
			if err := printWaiting(&text, []waitingRow{row}); err != nil {
				t.Fatal(err)
			}
			want := "owner-waiting 1; next [drain=0, design seat=0, operator=0]"
			if tc.who == "operator" {
				want = "owner-waiting 0; next [drain=0, design seat=0, operator=1]"
			}
			if !strings.Contains(text.String(), want) {
				t.Fatalf("want %q in CLI output: %s", want, text.String())
			}
		})
	}
}

func TestWaitingAndStatusClearReconciledRepair(t *testing.T) {
	h := newServeHarness(t)
	pushWaitingRow(t, h, `{"action":"push","what":"tip","kind":"remote-sync-conflict","detail":"sync-remote-a","escalated_mover":"owner","since_ts":"2026-11-21T00:00:00Z"}`, "a")
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1700000001000,'drain.post-landing-action','landing','a','{"action":"push","what":"tip","kind":"remote-sync-cleared","detail":""}')`)
	ctx := context.Background()
	rows, err := readWaiting(ctx, h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Observation == "post-landing:push" {
			t.Errorf("resolved repair left a waiting row: %+v", row)
		}
	}
	s, err := store.OpenRead(ctx, h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		p := readPushSummary(ctx, r, "")
		if p.What != "tip" || p.StateKind != "" || p.Mover != "" || p.Detail != "" || p.Since != "" || p.text() != "" {
			t.Errorf("resolved repair leaked into status: %+v", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingPushOKClearsStructuredState(t *testing.T) {
	for _, kind := range []string{"push-ok", "remote-sync-cleared"} {
		t.Run(kind, func(t *testing.T) {
			h := newServeHarness(t)
			pushWaitingRow(t, h, `{"action":"push","kind":"push-failed","detail":"x"}`, "a")
			h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1700000001000,'drain.post-landing-action','landing','b','{"action":"push","kind":"` + kind + `","detail":"informational detail"}')`)
			rows, err := readWaiting(context.Background(), h.store, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.Observation == "post-landing:push" {
					t.Fatalf("%s must clear: %+v", kind, row)
				}
			}
		})
	}
}
