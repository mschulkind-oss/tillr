package main

import (
	"os/exec"

	"context"
	"github.com/mschulkind-oss/tillr/store"
	"strings"
	"testing"
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
		{"proving", `{"action":"push","what":"green","kind":"remote-sync-proving","detail":"rebased 2 local commit(s)\nmore","remote":"origin","ref":"main","remote_tip":"` + R + `","since":"2026-11-20T10:00:00Z","unpublished_green_count":1}`,
			"ttttttttttttttttttttt", "waits on work", "the heavy runner; prove rebased tip tttttttttttt before pushing", "rebased 2 local commit(s); 1 proven commit(s) unpublished", "2026-11-20T10:00:00Z"},
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
		{"dirty", `{"action":"push","kind":"remote-sync-worktree-dirty","detail":"d"}`,
			"t", "needs you", "operator; check out main or commit, stash, or restore clashing paths in the main checkout", "", ""},
		{"rewritten", `{"action":"push","kind":"remote-rewritten","detail":"d","remote":"origin","ref":"main","remote_tip":"` + R + `","previous_remote_tip":"pppp"}`,
			"t", "needs you", "operator; reconcile force-pushed origin/main (pppp -> rrrrrrrrrrrr)", "", ""},
		{"patch held", `{"action":"push","kind":"push-patch-move-held","detail":"d"}`,
			"t", "needs you", "operator; run tillr drain acknowledge-patch-move after checking the fork rewrite", "", ""},
		{"declined", `{"action":"push","kind":"push-remote-declined","detail":"hook said no\nmore","remote":"origin","ref":"main"}`,
			"ppppppppppppppp", "needs you", "operator; inspect remote rejection on origin main and run tillr drain push", "remote server declined pppppppppppp: hook said no", ""},
		{"failed", `{"action":"push","kind":"push-failed","detail":"no broker"}`,
			"ppp", "needs you", "operator; check git-broker approval or remote access, or run tillr drain push", "push of ppp did not reach a ref update (no broker)", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			row := pushWaitingRow(t, h, c.payload, c.tip)
			if row.Class != c.class || row.Mover != c.mover || !strings.Contains(row.Why, c.why) {
				t.Fatalf("row = %+v", row)
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

func TestWaitingPushOKClearsStructuredState(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1000,'drain.post-landing-action','landing','a','{"action":"push","kind":"push-failed","detail":"x"}'),
 (2000,'drain.post-landing-action','landing','b','{"action":"push","kind":"push-ok","detail":""}')`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Observation == "post-landing:push" {
			t.Fatalf("push-ok must clear: %+v", row)
		}
	}
}

func TestPushSummaryText(t *testing.T) {
	zero, three := 0, 3
	if (*pushSummary)(nil).text() != "" || (&pushSummary{What: "none"}).text() != "" {
		t.Fatal("none must print nothing")
	}
	if (&pushSummary{What: "green", UnpublishedGreenCount: &zero}).text() != "" {
		t.Fatal("idle with nothing ahead must print nothing")
	}
	got := (&pushSummary{What: "green", Remote: "origin", Ref: "main", UnpublishedGreenCount: &three, StateKind: "push-failed", Mover: "operator; x", Since: "T"}).text()
	if got != "push: 3 proven commit(s) unpublished to origin/main — push-failed; waits on operator; x since T" {
		t.Fatal(got)
	}
	if (&pushSummary{What: "tip", UnpublishedGreenCount: &zero, UnpublishedMainCount: &three}).text() == "" {
		t.Fatal("tip mode counts main")
	}
}

func TestReadPushSummaryCountsFromRefs(t *testing.T) {
	h := newServeHarness(t)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.test", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	for _, m := range []string{"a", "b", "c"} {
		git("commit", "-q", "--allow-empty", "-m", m)
	}
	git("update-ref", "refs/tillr/published/origin/main", "HEAD~2")
	git("update-ref", "refs/tillr/green", "HEAD~1")
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1700000000000,'drain.post-landing-action','landing','x','{"action":"push","what":"green","kind":"push-failed","detail":"no broker","remote":"origin","ref":"main"}')`)
	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got *pushSummary
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		got = readPushSummary(context.Background(), r, repo)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got.What != "green" || got.StateKind != "push-failed" || got.UnpublishedGreenCount == nil || *got.UnpublishedGreenCount != 1 ||
		got.UnpublishedMainCount == nil || *got.UnpublishedMainCount != 2 || got.PublishedSHA == "" || !strings.HasPrefix(got.Mover, "operator;") || got.Since == "" {
		t.Fatalf("summary = %+v", got)
	}
}

func TestSnapshotEndpointIncludesPushSummary(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1700000000000,'drain.post-landing-action','landing','x','{"action":"push","what":"green","kind":"push-remote-declined","detail":"policy","remote":"origin","ref":"main"}')`)
	code, env, raw := h.get("/api/v1/snapshot")
	if code != 200 {
		t.Fatalf("status = %d: %s", code, raw)
	}
	data, _ := env["data"].(map[string]any)
	push, _ := data["push"].(map[string]any)
	if push == nil || push["what"] != "green" || push["state_kind"] != "push-remote-declined" {
		t.Fatalf("snapshot push = %+v (%s)", push, raw)
	}
}
