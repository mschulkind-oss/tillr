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

func pushTestHook(t *testing.T, dir, name, message string) string {
	t.Helper()
	hook := filepath.Join(dir, "hooks", name)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho '"+message+"' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return hook
}

func TestAfterLandingRemoteDeclinedDoesNotFetchOrRebase(t *testing.T) {
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
	if _, err := os.Stat(filepath.Join(root, ".git", "FETCH_HEAD")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no fetch, stat FETCH_HEAD: %v", err)
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
