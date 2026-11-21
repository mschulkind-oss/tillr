package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPushSummaryText(t *testing.T) {
	zero, three, nine := 0, 3, 9
	cases := []struct {
		name string
		push *pushSummary
		want string
	}{
		{"nil", nil, ""},
		{"no events", &pushSummary{What: "none"}, ""},
		{"idle zero", &pushSummary{What: "green", UnpublishedGreenCount: &zero, UnpublishedMainCount: &zero}, ""},
		{"push-ok zero", &pushSummary{What: "tip", StateKind: "push-ok", UnpublishedGreenCount: &zero, UnpublishedMainCount: &zero}, ""},
		{"green held", &pushSummary{What: "green", Remote: "origin", Ref: "main", UnpublishedGreenCount: &three, UnpublishedMainCount: &nine, StateKind: "push-failed", Mover: "operator; x", Since: "T"},
			"push: 3 proven of 9 total commit(s) unpublished to origin/main — push-failed; waits on operator; x since T"},
		{"tip held", &pushSummary{What: "tip", Remote: "origin", Ref: "main", UnpublishedGreenCount: &three, UnpublishedMainCount: &nine, StateKind: "push-failed", Mover: "operator; x", Since: "T"},
			"push: 3 proven of 9 total commit(s) unpublished to origin/main — push-failed; waits on operator; x since T"},
		{"tip unproven", &pushSummary{What: "tip", UnpublishedGreenCount: &zero, UnpublishedMainCount: &three},
			"push: 0 proven of 3 total commit(s) unpublished"},
		{"held null counts", &pushSummary{What: "green", StateKind: "push-failed"},
			"push: unknown proven of unknown total commit(s) unpublished — push-failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.push.text(); got != tc.want {
				t.Fatalf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadPushSummaryCountsFromRefs(t *testing.T) {
	for _, what := range []string{"green", "tip"} {
		t.Run(what, func(t *testing.T) {
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
			git("commit", "-q", "--allow-empty", "-m", "published")
			published := git("rev-parse", "HEAD")
			git("update-ref", "refs/tillr/published/origin/main", published)
			for i := 0; i < 9; i++ {
				git("commit", "-q", "--allow-empty", "-m", "unpublished")
				if i == 2 {
					git("update-ref", "refs/tillr/green", "HEAD")
				}
			}
			h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1700000000000,'drain.post-landing-action','landing','x','{"action":"push","what":"` + what + `","kind":"push-failed","detail":"no broker","remote":"origin","ref":"main","unpublished_green_count":99,"unpublished_main_count":99}')`)
			ctx := context.Background()
			s, err := store.OpenRead(ctx, h.store)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var got *pushSummary
			if err := s.ReadTx(ctx, func(r *store.Reader) error {
				got = readPushSummary(ctx, r, repo)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if got == nil || got.What != what || got.StateKind != "push-failed" || got.UnpublishedGreenCount == nil || *got.UnpublishedGreenCount != 3 ||
				got.UnpublishedMainCount == nil || *got.UnpublishedMainCount != 9 || got.PublishedSHA != published || !strings.HasPrefix(got.Mover, "operator;") || got.Since == "" {
				t.Fatalf("summary = %+v", got)
			}

			env, code := statusFace([]string{"--store", h.store, "--root", repo})
			if code != record.ExitOK {
				t.Fatalf("status exit %d", code)
			}
			var payload statusPayload
			if err := json.Unmarshal(env.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if p := payload.Push; p == nil || p.What != what || p.UnpublishedGreenCount == nil || *p.UnpublishedGreenCount != 3 ||
				p.UnpublishedMainCount == nil || *p.UnpublishedMainCount != 9 {
				t.Fatalf("status push = %+v (%s)", p, env.Payload)
			}
			text := statusText(payload)
			if !strings.Contains(text, "push: 3 proven of 9 total commit(s) unpublished to origin/main — push-failed;") || strings.Contains(text, "9 proven") {
				t.Fatalf("status text = %q", text)
			}
		})
	}
}

func TestSnapshotEndpointIncludesPushSummary(t *testing.T) {
	t.Run("no events", func(t *testing.T) {
		h := newServeHarness(t)
		code, env, raw := h.get("/api/v1/snapshot")
		if code != 200 {
			t.Fatalf("status = %d: %s", code, raw)
		}
		data, _ := env["data"].(map[string]any)
		push, _ := data["push"].(map[string]any)
		if push != nil && (push["what"] != "none" || push["state_kind"] != nil || push["unpublished_green_count"] != nil || push["unpublished_main_count"] != nil) {
			t.Fatalf("no events must show no push summary: %+v (%s)", push, raw)
		}
	})
	t.Run("newest hold", func(t *testing.T) {
		h := newServeHarness(t)
		h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1700000001000,'drain.post-landing-action','landing','old','{"action":"push","what":"tip","kind":"push-failed","detail":"old failure","remote":"origin","ref":"main"}'),
 (1700000000000,'drain.post-landing-action','landing','x','{"action":"push","what":"green","kind":"push-remote-declined","detail":"policy","remote":"fixture-no-published-ref","ref":"refs/heads/main"}')`)
		code, env, raw := h.get("/api/v1/snapshot")
		if code != 200 {
			t.Fatalf("status = %d: %s", code, raw)
		}
		data, _ := env["data"].(map[string]any)
		push, _ := data["push"].(map[string]any)
		if push == nil || push["what"] != "green" || push["state_kind"] != "push-remote-declined" || push["detail"] != "policy" ||
			push["remote"] != "fixture-no-published-ref" || push["ref"] != "main" || push["since"] != "2023-11-14T22:13:20Z" {
			t.Fatalf("snapshot push = %+v (%s)", push, raw)
		}
		mover, _ := push["mover"].(string)
		if !strings.HasPrefix(mover, "operator;") {
			t.Fatalf("snapshot mover = %q (%s)", mover, raw)
		}
		green, greenPresent := push["unpublished_green_count"]
		main, mainPresent := push["unpublished_main_count"]
		if !greenPresent || !mainPresent || green != nil || main != nil {
			t.Fatalf("unreadable refs must have null counts: %+v (%s)", push, raw)
		}
	})
}
