package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func lastPushEvent(t *testing.T, f refusalLoopFixture) map[string]any {
	t.Helper()
	var raw string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload FROM events WHERE kind=? AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`, postLandingKind).Scan(&raw)
	})
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBoundaryExcusesPublishedRefs(t *testing.T) {
	ref := land.PublishedRefPrefix + "origin/main"
	before := &Boundary{Refs: map[string]string{ref: strings.Repeat("a", 40)}}
	after := &Boundary{Refs: map[string]string{ref: strings.Repeat("b", 40)}}
	violations, observed, err := CompareRunObserved(before, after)
	if err != nil || len(violations) != 0 || len(observed) != 0 {
		t.Fatalf("CompareRunObserved = (%v, %v, %v), want no boundary changes", violations, observed, err)
	}
	if violations, err := Compare(before, after); err != nil || len(violations) != 0 {
		t.Fatalf("Compare = (%v, %v), want no boundary changes", violations, err)
	}
}

func TestPushAdvancesPublishedRefAndConfirmsOnAlreadyPublished(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	root := p.f.root
	dest := "refs/heads/" + TrunkRef
	pubRef := land.PublishedRefPrefix + "origin/" + TrunkRef
	checkPublished := func(want string) {
		t.Helper()
		if got := runGit(t, p.remote, "rev-parse", dest); got != want {
			t.Errorf("remote tip = %s, want %s", got, want)
		}
		out, err := rawGit(ctx, root, nil, "rev-parse", "--verify", "--quiet", pubRef)
		if got := strings.TrimSpace(out); err != nil || got != want {
			t.Errorf("published pointer = (%q, %v), want %s", got, err, want)
		}
	}

	// The remote moved beyond the last confirmation and is ahead of green.
	if err := advancePublished(ctx, root, "origin", dest, p.local[0]); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "push", "origin", p.local[2]+":"+dest)
	// Confirm the fetched tip, not the older green.
	if green, err := pushGreen(ctx, root, settings.Push{}); !errors.Is(err, errAlreadyPublished) || green != p.local[1] {
		t.Fatalf("pushGreen = (%s, %v), want (%s, errAlreadyPublished)", green, err, p.local[1])
	}
	checkPublished(p.local[2])

	next := ownerCommit(t, root, "proved.txt")
	runGit(t, root, "update-ref", GreenRef, next)
	unproved := ownerCommit(t, root, "unproved.txt")
	if green, err := pushGreen(ctx, root, settings.Push{}); err != nil || green != next {
		t.Fatalf("pushGreen = (%s, %v), want (%s, nil)", green, err, next)
	}
	checkPublished(next)
	if got := runGit(t, root, "rev-parse", "FETCH_HEAD"); got != p.local[2] {
		t.Errorf("preflight fetched %s, want %s", got, p.local[2])
	}
	if err := pushTrunk(ctx, root, settings.Push{}); err != nil {
		t.Fatalf("pushTrunk: %v", err)
	}
	checkPublished(unproved)
}

func TestPushStopsOnStraightBackwardRemoteRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, what string
		pointer    bool
	}{
		{"published/tip", settings.PushTip, true},
		{"tracking-only/tip", settings.PushTip, false},
		{"tracking-only/green-at-old-tip", settings.PushGreen, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPushFixture(t)
			ctx := context.Background()
			root := p.f.root
			ref := "refs/heads/" + TrunkRef
			if tc.pointer {
				if err := pushTrunk(ctx, root, settings.Push{}); err != nil {
					t.Fatal(err)
				}
			} else {
				runGit(t, root, "push", "origin", p.local[2]+":"+ref)
			}
			published, rewound := p.local[2], p.local[1]
			runGit(t, p.remote, "update-ref", ref, rewound, published)
			tip := ownerCommit(t, root, "unpublished.txt")
			runGit(t, root, "update-ref", GreenRef, published)
			p.l.cfg.PostLanding.Push.What = tc.what
			marker := filepath.Join(root, ".git", "push-attempted")
			hook := filepath.Join(root, ".git", "hooks", "pre-push")
			if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch .git/push-attempted\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			before := runGit(t, root, "show-ref")
			remoteBefore := runGit(t, p.remote, "show-ref")
			// The next attempt must still use the same baseline, even without a pointer.
			for attempt := 0; attempt < 2; attempt++ {
				if tc.what == settings.PushGreen {
					green, err := pushGreen(ctx, root, settings.Push{})
					p.l.recordPush(ctx, tc.what, green, err)
				} else {
					p.l.recordPush(ctx, tc.what, tip, pushTrunk(ctx, root, settings.Push{}))
				}
				ev := lastPushEvent(t, p.f)
				if ev["kind"] != "remote-rewritten" || ev["escalated_mover"] != "operator" || ev["remote_tip"] != rewound || ev["previous_remote_tip"] != published {
					t.Errorf("push event = %+v; want remote-rewritten from %s to %s naming the operator", ev, published, rewound)
				}
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("push attempted (marker stat: %v)", err)
			}
			if runGit(t, root, "show-ref") != before || runGit(t, p.remote, "show-ref") != remoteBefore {
				t.Error("a ref moved despite the remote rewrite")
			}
		})
	}
}

func TestRefusedPushStopsOnRemoteForcePush(t *testing.T) {
	for _, baseline := range []string{"published", "tracking-only"} {
		t.Run(baseline, func(t *testing.T) {
			p := newPushFixture(t)
			ctx := context.Background()
			root := p.f.root
			ref := "refs/heads/" + TrunkRef
			if baseline == "tracking-only" {
				runGit(t, root, "push", "origin", p.local[2]+":"+ref)
			} else if err := pushTrunk(ctx, root, settings.Push{}); err != nil {
				t.Fatal(err)
			}
			published := p.local[2]
			runGit(t, p.remote, "update-ref", ref, published+"^", published)
			runGit(t, p.other, "pull", "--ff-only", "-q")
			rewritten := p.remoteCommit(t, "replacement.txt", "replacement\n")
			tip := ownerCommit(t, root, "unpublished.txt")
			// Bypass preflight to simulate a rewrite between the guard fetch and push.
			refusal := pushCommand(ctx, root, "--", "origin", tip+":"+ref)
			var refused *refusedPushError
			if !errors.As(refusal, &refused) {
				t.Fatalf("push = %v, want a refusal", refusal)
			}
			before := runGit(t, root, "show-ref")
			_, err := p.l.healRefusedPush(ctx, settings.Push{}, refusal)
			p.l.recordPush(ctx, settings.PushTip, tip, err)
			ev := lastPushEvent(t, p.f)
			if ev["kind"] != "remote-rewritten" || ev["escalated_mover"] != "operator" || ev["remote_tip"] != rewritten || ev["previous_remote_tip"] != published {
				t.Errorf("push event = %+v; want remote-rewritten from %s to %s naming the operator", ev, published, rewritten)
			}
			if runGit(t, root, "show-ref") != before || runGit(t, p.remote, "rev-parse", ref) != rewritten {
				t.Error("a ref moved during refused-push healing")
			}
		})
	}
}

func pushTestHook(t *testing.T, dir, name, message string) string {
	t.Helper()
	hook := filepath.Join(dir, "hooks", name)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho '"+message+"' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return hook
}

func TestAfterLandingRemoteDeclinedDoesNotReplay(t *testing.T) {
	p := newPushFixture(t)
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	root := p.f.root
	remoteBefore := runGit(t, p.remote, "rev-parse", "refs/heads/"+TrunkRef)
	pushTestHook(t, p.remote, "pre-receive", "declined by server policy")
	p.l.afterLanding(context.Background(), p.local[2])
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	ev := lastPushEvent(t, p.f)
	if ev["kind"] != "push-remote-declined" || ev["escalated_mover"] != "operator" {
		t.Errorf("unexpected decline event: %+v", ev)
	}
	if detail, _ := ev["detail"].(string); !strings.Contains(detail, "[remote rejected]") || !strings.Contains(detail, "declined by server policy") {
		t.Errorf("event lost original detail: %q", detail)
	}
	if got := runGit(t, root, "rev-parse", "HEAD"); got != p.local[2] {
		t.Errorf("local tip moved to %s", got)
	}
	if got := runGit(t, p.remote, "rev-parse", "refs/heads/"+TrunkRef); got != remoteBefore {
		t.Errorf("remote tip moved to %s", got)
	}
	if got := runGit(t, root, "rev-parse", "FETCH_HEAD"); got != remoteBefore {
		t.Errorf("preflight fetched %s, want %s", got, remoteBefore)
	}
}

func TestPushHookFailureWithoutPatchMoveRecordsPushFailed(t *testing.T) {
	p := newPushFixture(t)
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	pushTestHook(t, filepath.Join(p.f.root, ".git"), "pre-push", "local pre-push denied")
	p.l.afterLanding(context.Background(), p.local[2])
	waitAction(t, func() bool { return !p.l.pushAction.busy() })
	if ev := lastPushEvent(t, p.f); ev["kind"] != "push-failed" {
		t.Fatalf("unexpected hook failure: %+v", ev)
	}
}

func TestPushClassificationAndStructuredState(t *testing.T) {
	p := newPushFixture(t)
	ctx := context.Background()
	p.l.cfg.PostLanding.Push.What = settings.PushTip
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	p.l.cfg.Now = func() time.Time { return now }
	before := &Boundary{Repo: p.f.root, Refs: map[string]string{"refs/heads/patch/pi": "old"}}
	after := &Boundary{Repo: p.f.root, Refs: map[string]string{"refs/heads/patch/pi": "new"}}
	_, observed, err := CompareRunObserved(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.l.recordBoundaryCheck(ctx, BoundaryScopeRun, "tick", []runRef{{RunID: "run-a", ItemID: "item-a", LaneID: "lane-1"}}, nil, nil, now, observed...); err != nil {
		t.Fatal(err)
	}
	attempt := func() map[string]any {
		p.l.afterLanding(ctx, p.local[2])
		waitAction(t, func() bool { return !p.l.pushAction.busy() })
		return lastPushEvent(t, p.f)
	}
	remoteTip := runGit(t, p.remote, "rev-parse", "refs/heads/"+TrunkRef)
	gitDir := filepath.Join(p.f.root, ".git")
	hook := pushTestHook(t, gitDir, "pre-push", "patch move is not acknowledged")
	since := now.Format(time.RFC3339)
	first := attempt()
	if first["kind"] != "push-patch-move-held" || first["since_ts"] != since || first["remote_tip"] != remoteTip ||
		first["unpublished_green_count"] != float64(2) || first["unpublished_main_count"] != float64(3) {
		t.Errorf("unexpected held event: %+v", first)
	}
	now = now.Add(time.Minute)
	if repeated := attempt(); repeated["kind"] != "push-patch-move-held" || repeated["since_ts"] != since {
		t.Errorf("hold did not keep its timestamp: %+v", repeated)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	ok := attempt()
	if _, hasSince := ok["since_ts"]; ok["kind"] != "push-ok" || ok["detail"] != "" || hasSince {
		t.Errorf("successful push did not clear its failure: %+v", ok)
	}
	if ok["remote_tip"] != p.local[2] || ok["unpublished_green_count"] != float64(0) || ok["unpublished_main_count"] != float64(0) {
		t.Errorf("successful push did not record the updated remote state: %+v", ok)
	}
	now = now.Add(time.Minute)
	pushTestHook(t, gitDir, "pre-push", "patch move is not acknowledged")
	if later := attempt(); later["kind"] != "push-patch-move-held" || later["since_ts"] != now.Format(time.RFC3339) {
		t.Errorf("a new failure inherited the old timestamp: %+v", later)
	}
}
