package drain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func pushGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, dir, args)
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

type pushFixture struct{ repo, remote, path, base, first, tip string }

func newPushFixture(t *testing.T, mode string) pushFixture {
	t.Helper()
	t.Setenv("TILLR_SQLITE_TRACE", "") // CombinedOutput must contain only the face's report.
	dir := scratch(t, "push-face")
	f := pushFixture{repo: filepath.Join(dir, "repo"), remote: filepath.Join(dir, "remote.git")}
	pushGit(t, dir, "init", "--bare", "-q", f.remote)
	pushGit(t, dir, "init", "-q", "-b", drain.TrunkRef, f.repo)
	if err := os.WriteFile(filepath.Join(f.repo, ".tillr.toml"), []byte(fmt.Sprintf("[push]\nwhat = %q\n", mode)), 0o644); err != nil {
		t.Fatal(err)
	}
	pushGit(t, f.repo, "add", ".tillr.toml")
	pushGit(t, f.repo, "commit", "-qm", "base")
	f.base = pushGit(t, f.repo, "rev-parse", "HEAD")
	pushGit(t, f.repo, "remote", "add", "origin", f.remote)
	pushGit(t, f.repo, "push", "-qu", "origin", drain.TrunkRef)
	pushGit(t, f.repo, "commit", "--allow-empty", "-qm", "first")
	f.first = pushGit(t, f.repo, "rev-parse", "HEAD")
	pushGit(t, f.repo, "commit", "--allow-empty", "-qm", "tip")
	f.tip = pushGit(t, f.repo, "rev-parse", "HEAD")
	f.path = filepath.Join(f.repo, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureAt(t, f.path, itemRecord("item_041", "in-queue"))
	return f
}

func (f pushFixture) append(t *testing.T, kind, typ, id, payload string) int64 {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, f.path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var seq int64
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		seq, err = tx.Append(event.Event{Kind: kind, ObjectType: typ, ObjectID: &id, At: time.Now().UTC(), Payload: []byte(payload)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return seq
}

type pushResult struct {
	Kind, What, Tip, Detail string
	Mover                   string `json:"escalated_mover"`
	OK                      bool
}

func (f pushFixture) checkJSON(t *testing.T, wantKind, wantWhat, wantTip, wantMover string, count int) {
	t.Helper()
	code, out := invoke(t, "push", "--root", f.repo, "--json")
	var got pushResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("push exited %d, invalid JSON: %v\n%s", code, err, out)
	}
	if wantTip == "*" {
		wantTip = pushGit(t, f.remote, "rev-parse", "refs/heads/"+drain.TrunkRef)
	}
	ok := wantKind == "push-ok"
	if (code == 0) != ok || got.OK != ok || got.Kind != wantKind || got.What != wantWhat || got.Tip != wantTip || !strings.Contains(got.Mover, wantMover) {
		t.Fatalf("push exited %d: %+v; want %s %s %s mover %q", code, got, wantKind, wantWhat, wantTip, wantMover)
	}
	ctx := context.Background()
	s, err := store.OpenRead(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var payload []byte
	var n int
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT payload, count(*) OVER () FROM events WHERE kind='drain.post-landing-action' AND json_extract(payload,'$.action')='push' ORDER BY seq DESC LIMIT 1`).Scan(&payload, &n)
	}); err != nil {
		t.Fatal(err)
	}
	var stored pushResult
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	if n != count || got.Kind != stored.Kind || got.Mover != stored.Mover || got.Detail != stored.Detail {
		t.Fatalf("reported %+v, stored %s, event count %d want %d", got, payload, n, count)
	}
}

func TestPushFaceJSONAndRecordsEvent(t *testing.T) {
	f := newPushFixture(t, "tip")
	f.checkJSON(t, "push-ok", "tip", f.tip, "", 1)
	if got := pushGit(t, f.remote, "rev-parse", "refs/heads/"+drain.TrunkRef); got != f.tip {
		t.Fatalf("remote tip %s, want %s", got, f.tip)
	}
}

func TestPushFaceNoPublicationStillRecordsOutcome(t *testing.T) {
	for _, mode := range []string{"none", "green"} {
		t.Run(mode, func(t *testing.T) {
			f := newPushFixture(t, mode)
			f.checkJSON(t, "push-ok", mode, "", "", 1)
			if got := pushGit(t, f.remote, "rev-parse", "refs/heads/"+drain.TrunkRef); got != f.base {
				t.Fatalf("remote tip %s, want unchanged %s", got, f.base)
			}
		})
	}
}

func TestPushFaceGreenModePublishesParkedGreenAndAdvancesPublishedRef(t *testing.T) {
	f := newPushFixture(t, "green")
	pushGit(t, f.repo, "update-ref", drain.GreenRef, f.first)
	f.checkJSON(t, "push-ok", "green", f.first, "", 1)
	f.checkJSON(t, "push-ok", "green", f.first, "", 2) // already published
	for repo, ref := range map[string]string{f.remote: "refs/heads/" + drain.TrunkRef, f.repo: "refs/tillr/published/origin/" + drain.TrunkRef} {
		if got := pushGit(t, repo, "rev-parse", ref); got != f.first {
			t.Fatalf("%s = %s, want green %s", ref, got, f.first)
		}
	}
}

func TestPushFaceProofHoldWithOldGreenAlreadyPublished(t *testing.T) {
	f := newPushFixture(t, "green")
	pushGit(t, f.repo, "update-ref", drain.GreenRef, f.base)
	f.append(t, "drain.post-landing-action", "landing", f.base, fmt.Sprintf(`{"action":"push","kind":"remote-sync-proving","what":"green","remote":"origin","ref":"refs/heads/%s","rebased_tip":%q}`, drain.TrunkRef, f.tip))
	// Any push would execute the local hook, even if no ref update reached the remote.
	hook := filepath.Join(f.repo, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch attempted-push\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := invoke(t, "push", "--root", f.repo)
	if code == 0 || !strings.Contains(string(out), "remote-sync-proving") || !strings.Contains(string(out), "the heavy runner") {
		t.Fatalf("push exited %d: %s", code, out)
	}
	f.checkJSON(t, "remote-sync-proving", "green", f.base, "the heavy runner", 3)
	if _, err := os.Stat(filepath.Join(f.repo, "attempted-push")); !os.IsNotExist(err) {
		t.Fatalf("push ran while proof held: %v", err)
	}
	if got := pushGit(t, f.remote, "rev-parse", "refs/heads/"+drain.TrunkRef); got != f.base {
		t.Fatalf("remote tip %s, want untouched %s", got, f.base)
	}
}

func TestPushFaceRecordsSuccessfulRetryAfterRefusedTip(t *testing.T) {
	f := newPushFixture(t, "tip")
	if err := os.WriteFile(filepath.Join(f.repo, "local"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushGit(t, f.repo, "add", "local")
	pushGit(t, f.repo, "commit", "-qm", "local payload")
	other := filepath.Join(filepath.Dir(f.repo), "other")
	pushGit(t, f.repo, "clone", "-qb", drain.TrunkRef, f.remote, other)
	if err := os.WriteFile(filepath.Join(other, "remote"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pushGit(t, other, "add", "remote")
	pushGit(t, other, "commit", "-qm", "remote payload")
	pushGit(t, other, "push", "-q", "origin", drain.TrunkRef)

	// The final tip is unknown until replay; predict it by checking the reported ref separately.
	f.checkJSON(t, "push-ok", "tip", "*", "", 1)
	if _, err := os.Stat(filepath.Join(f.repo, ".git", "land.lock")); err != nil {
		t.Fatalf("push replay did not use land.lock: %v", err)
	}
	for _, file := range []string{"local", "remote"} {
		if got := pushGit(t, f.remote, "show", drain.TrunkRef+":"+file); got != file {
			t.Fatalf("remote %s contains %q", file, got)
		}
	}
}

func TestPushFaceReportsStoredOperatorOnRemoteRewrite(t *testing.T) {
	f := newPushFixture(t, "tip")
	pushGit(t, f.repo, "push", "origin", f.first+":refs/heads/"+drain.TrunkRef)
	pushGit(t, f.remote, "update-ref", "refs/heads/"+drain.TrunkRef, f.base)
	f.checkJSON(t, "remote-rewritten", "tip", f.tip, "operator", 1)
}

func TestPushFaceRetriesAfterAcknowledgingPatchMove(t *testing.T) {
	f := newPushFixture(t, "tip")
	seq := f.append(t, drain.BoundaryCheckKind, "run", "observing-run", `{"clean":true,"runs":[{"run_id":"observing-run"}],"patch_moves":[{"ref":"refs/heads/patch/pi","old":"old","new":"new"}]}`)
	code, out := invoke(t, "acknowledge-patch-move", "--store", f.path, "--observation", fmt.Sprint(seq), "--fork", "pi", "--by", "owner")
	if code != 0 || !strings.Contains(string(out), "push-ok") {
		t.Fatalf("acknowledge exited %d: %s", code, out)
	}
	if got := pushGit(t, f.remote, "rev-parse", "refs/heads/"+drain.TrunkRef); got != f.tip {
		t.Fatalf("remote tip %s, want retried %s", got, f.tip)
	}
}
