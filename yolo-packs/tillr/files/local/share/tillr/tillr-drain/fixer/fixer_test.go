package fixer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(BoundaryGitEnv(),
		"GIT_AUTHOR_NAME=Fixer Test",
		"GIT_AUTHOR_EMAIL=fixer-test@example.com",
		"GIT_COMMITTER_NAME=Fixer Test",
		"GIT_COMMITTER_EMAIL=fixer-test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitTest(t, dir, "init", "-b", TrunkRef)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "README.md")
	runGitTest(t, dir, "commit", "-m", "initial")
	return dir
}

func openTestStore(t *testing.T) (string, *store.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tillr.db")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return path, s
}

func TestFixerChecksAndTrunkFixerWait(t *testing.T) {
	if got := FixerChecks("Fix trunk (red: unit-a, go-test:tools/tillr-drain:./drain:TestFoo)"); !slices.Equal(got, []string{"unit-a", "go-test:tools/tillr-drain:./drain:TestFoo"}) {
		t.Fatalf("FixerChecks = %v", got)
	}
	if got := FixerChecks("no red list here"); len(got) != 0 {
		t.Fatalf("FixerChecks(none) = %v, want empty", got)
	}

	_, s := openTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES('fixer-main-abc123','Fix trunk','queued',?,'build')`, now.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('fixer-main-abc123','trunk-green','Fix trunk (red: chk-1, chk-2)','not-done','verification-only')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at,seat) VALUES('fixer-main-def456','Fix trunk','blocked',?,'build')`, now.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('fixer-main-def456','trunk-green','Fix trunk (red: chk-3)','not-done','verification-only')`); err != nil {
			return err
		}
		return item.RecordConflict(tx, item.Conflict{
			ItemID:     "fixer-main-def456",
			ConflictID: "c1",
			RunID:      "run-1",
			Status:     "owner",
			WaitingOn:  "owner",
			Text:       "need   owner   decision",
		}, now)
	}); err != nil {
		t.Fatal(err)
	}

	var detail string
	var owner bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		detail, owner, err = TrunkFixerWait(r, "fixer-main-abc123")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if owner || detail != "fixer-main-abc123; check: chk-1, chk-2; waiting on repair (not waiting on owner)" {
		t.Fatalf("TrunkFixerWait(abc123) = (%q, %v)", detail, owner)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		detail, owner, err = TrunkFixerWait(r, "fixer-main-def456")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !owner || detail != "fixer-main-def456; check: chk-3; waiting on owner: need owner decision" {
		t.Fatalf("TrunkFixerWait(def456) = (%q, %v)", detail, owner)
	}
}

func TestCheckTrunkWorktree(t *testing.T) {
	if err := CheckTrunkWorktree(context.Background(), ""); err != nil {
		t.Fatalf("empty root err = %v", err)
	}
	repo := initTestRepo(t)
	if err := CheckTrunkWorktree(context.Background(), repo); err != nil {
		t.Fatalf("clean repo err = %v", err)
	}

	runGitTest(t, repo, "config", "--local", "core.worktree", repo)
	err := CheckTrunkWorktree(context.Background(), repo)
	var ce *CodeError
	if !errors.As(err, &ce) || ce.Code != record.ExitUsage {
		t.Fatalf("core.worktree err = %v, want *CodeError(ExitUsage)", err)
	}
}

func TestReadsCoverPath(t *testing.T) {
	if !ReadsCoverPath([]string{"tools/tillr-drain/fixer/"}, "tools/tillr-drain/fixer/fixer.go") {
		t.Fatal("expected prefix match")
	}
	if !ReadsCoverPath([]string{"./tools/fork-check.py"}, "tools/fork-check.py") {
		t.Fatal("expected exact cleaned match")
	}
	if !ReadsCoverPath([]string{"."}, "anything/at/all.go") {
		t.Fatal("expected dot to cover all paths")
	}
	if ReadsCoverPath([]string{"tools/tillr-drain/drain"}, "tools/tillr-drain/fixer/fixer.go") {
		t.Fatal("unexpected coverage across sibling dir")
	}
}

func TestReadGreenProofAndCheckProofs(t *testing.T) {
	repo := initTestRepo(t)
	tip := runGitTest(t, repo, "rev-parse", "HEAD")
	dir, err := heavy.Directory(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "update-ref", heavy.GreenRef, tip)

	provedAt := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	green := heavy.GreenProof{
		Commit:   tip,
		ProvedAt: provedAt,
		Passed:   []string{"chk-green", "go-tool/tools-tillr-drain-s-whole-suite-passes"},
	}
	if err := PublishHeavyJSON(dir, "green-"+tip+".json", green); err != nil {
		t.Fatal(err)
	}

	gotGreen, err := ReadGreenProof(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if gotGreen.Commit != tip || !slices.Equal(gotGreen.Passed, green.Passed) {
		t.Fatalf("ReadGreenProof = %+v", gotGreen)
	}

	progAt := provedAt.Add(time.Minute)
	prog := HeavyProgress{
		TipSHA: tip,
		Checks: map[string]heavy.HeavyCheck{
			"chk-prog": {CheckID: "chk-prog", Result: string(checkresult.Pass)},
			"chk-fail": {CheckID: "chk-fail", Result: string(checkresult.Fail)},
		},
		PassedAt: map[string]time.Time{
			"chk-prog": progAt,
			"chk-fail": progAt,
		},
	}
	if err := PublishHeavyJSON(dir, "heavy-progress.json", prog); err != nil {
		t.Fatal(err)
	}

	proofs, err := ReadCheckProofs(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 2 {
		t.Fatalf("ReadCheckProofs len = %d, want 2", len(proofs))
	}
	if !proofs[0].Passed([]string{"chk-green"}, 0) {
		t.Fatal("expected green proof to pass chk-green")
	}
	if !proofs[0].Passed([]string{"go-test:tools/tillr-drain:./drain:TestFoo"}, 0) {
		t.Fatal("expected whole-suite proof to cover go-test selector")
	}
	if !proofs[1].Passed([]string{"chk-prog"}, 0) {
		t.Fatal("expected progress proof to pass chk-prog")
	}
	if proofs[1].Passed([]string{"chk-fail"}, 0) {
		t.Fatal("expected progress proof not to pass chk-fail")
	}
}

func TestDeferredRepairPrefixesAndRecordTrunkRepair(t *testing.T) {
	detail := "repair deferred to proof of abc1234: suite failed; repair deferred to proof of def5678: trunk was rewritten; repair deferred to proof of 999aaaa: still red"
	got := DeferredRepairPrefixes(detail)
	if !slices.Equal(got, []string{"abc1234", "999aaaa"}) {
		t.Fatalf("DeferredRepairPrefixes = %v", got)
	}

	repo := initTestRepo(t)
	c1 := runGitTest(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "f.txt")
	runGitTest(t, repo, "commit", "-m", "second")
	c2 := runGitTest(t, repo, "rev-parse", "HEAD")

	if err := RecordTrunkRepair(context.Background(), repo, c2, "repaired at c2"); err != nil {
		t.Fatal(err)
	}
	// Recording an older ancestor must not move the recorded repair backward.
	if err := RecordTrunkRepair(context.Background(), repo, c1, "older c1"); err != nil {
		t.Fatal(err)
	}
	recorded, err := RecordedTrunkRepair(context.Background(), repo, c2)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != c2 {
		t.Fatalf("RecordedTrunkRepair = %s, want %s", recorded, c2)
	}

	dir, err := heavy.Directory(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, TrunkRepairFile))
	if err != nil {
		t.Fatal(err)
	}
	var rec TrunkRepairRecord
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Commit != c2 {
		t.Fatalf("trunk-repair.json = %s, err=%v", raw, err)
	}
}
