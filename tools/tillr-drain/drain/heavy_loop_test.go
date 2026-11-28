package drain

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/store"
)

type heavyTestSteps struct {
	Steps
	judge func(context.Context, string, string) ([]HeavyCheck, error)
}

func (s heavyTestSteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return s.judge(ctx, dir, only)
}
func TestHeavyLandingTriggersDoNotReadOrWriteRollingWatermark(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "one")
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO rolling_state (id,watermark_sha,landings_since) VALUES(1,'not-a-commit',99)`); err != nil {
			return err
		}
		return recordFixtureLanding(tx, "item-one", "run-one", tip, 0)
	}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard}}
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	<-entered
	for _, value := range []string{"two", "three", "four"} {
		tip = heavyCommit(t, root, value)
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			return recordFixtureLanding(tx, "item-"+value, "run-"+value, tip, 0)
		}); err != nil {
			t.Fatal(err)
		}
		if err := l.bumpLandings(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	l.heavyWG.Wait()
	if calls.Load() != 2 {
		t.Fatalf("runs=%d", calls.Load())
	}
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != tip {
		t.Fatalf("green=%s", got)
	}
	var watermark string
	var count int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT watermark_sha,landings_since FROM rolling_state WHERE id=1`).Scan(&watermark, &count)
	}); err != nil {
		t.Fatal(err)
	}
	if watermark != "not-a-commit" || count != 99 {
		t.Fatalf("retired row changed: %s %d", watermark, count)
	}
}
func TestHeavyFilesOneFixerAgainstTheFirstRedLanding(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	red := heavyCommit(t, root, "bad")
	tip := heavyCommit(t, root, "worse")
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, sha := range []string{red, tip} {
			err := recordFixtureLanding(tx, "fixture", "fixture-run", sha, 0)
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	steps := heavyTestSteps{judge: func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		value, err := os.ReadFile(filepath.Join(dir, "value"))
		result := "pass"
		if string(value) != "good" {
			result = "fail"
		}
		return []HeavyCheck{{CheckID: "unit", Result: result, Reads: []string{"value"}}}, err
	}}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard}}
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	// Restart retries the red tip, but the landing's fixer remains deduplicated.
	l.heavyAttempted = ""
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	var id, text string
	var count int
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
			return err
		}
		return r.QueryRow(`SELECT items.item_id,item_criteria.text FROM items JOIN item_criteria USING(item_id)`).Scan(&id, &text)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 || id != TrunkFixerID(red) || !strings.Contains(text, "refs/tillr/green") {
		t.Fatalf("fixer=%s count=%d criteria=%s", id, count, text)
	}
	status, err := ReadHeavyStatus(context.Background(), root, s.Path())
	if err != nil || status.LandingsSince != 2 || status.FailingChecks[0].FirstRedLanding != red {
		t.Fatalf("status=%+v %v", status, err)
	}
}

// The remote receives a commit only through a green proof: a red run pushes nothing, and a
// green run pushes the commit it proved.
func TestHeavyGreenAdvancePushesTheProvedCommitOnly(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	remote := filepath.Join(scratchUnderDurable(t), "remote.git")
	heavyGit(t, root, "init", "--bare", remote)
	heavyGit(t, root, "remote", "add", "origin", remote)
	heavyGit(t, root, "push", "-u", "origin", TrunkRef)
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result := "fail"
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: result}}, nil
	}}
	log := &strings.Builder{}
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: log}}
	l.cfg.PostLanding.Push.What = "green"
	remoteTip := func() string { return heavyGit(t, root, "--git-dir="+remote, "rev-parse", "refs/heads/"+TrunkRef) }
	redTip := heavyCommit(t, root, "red")
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "item-red", "run-red", redTip, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.bumpLandings(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)
	if remoteTip() != base {
		t.Fatalf("a red run pushed %s", remoteTip())
	}
	result = "pass"
	greenTip := heavyCommit(t, root, "green")
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "item-green", "run-green", greenTip, 0)
	}); err != nil {
		t.Fatal(err)
	}
	if err := l.bumpLandings(context.Background()); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)
	if got := remoteTip(); got != greenTip || got == redTip {
		t.Fatalf("remote = %s, want proved %s", got, greenTip)
	}
}

func TestHandMadeTrunkCommitStillProvesGreen(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	remote := filepath.Join(scratchUnderDurable(t), "remote.git")
	heavyGit(t, root, "init", "--bare", remote)
	heavyGit(t, root, "remote", "add", "origin", remote)
	heavyGit(t, root, "push", "-u", "origin", TrunkRef)
	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	log := &strings.Builder{}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: log},
	}
	l.cfg.PostLanding.Push.What = "green"
	remoteTip := func() string { return heavyGit(t, root, "--git-dir="+remote, "rev-parse", "refs/heads/"+TrunkRef) }
	unrecordedEvents := func() []string {
		t.Helper()
		var commits []string
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT object_type, coalesce(object_id, ''), coalesce(json_extract(payload, '$.commit'), '')
FROM events WHERE kind = ? ORDER BY seq ASC`, alarms.UnrecordedTrunkCommitKind)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var objType, objID, commit string
				if err := rows.Scan(&objType, &objID, &commit); err != nil {
					return err
				}
				if objType != "trunk" || objID != "trunk" {
					t.Fatalf("event object = (%q, %q), want (trunk, trunk)", objType, objID)
				}
				commits = append(commits, commit)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return commits
	}
	assertNoAlarms := func() {
		t.Helper()
		var active []alarms.Alarm
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			active, err = alarms.Read(r, time.Now(), nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(active) != 0 {
			t.Fatalf("active alarms = %+v, want none", active)
		}
	}

	// Owner commits one file on trunk by hand, then an item lands on top.
	hand := heavyCommit(t, root, "hand-made")
	landed := heavyCommit(t, root, "landed-on-top")
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "item-landed", "run-landed", landed, 0)
	}); err != nil {
		t.Fatal(err)
	}

	if err := l.startHeavy(ctx, landed); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)

	if got := heavyGit(t, root, "rev-parse", GreenRef); got != landed {
		t.Fatalf("green ref = %s, want %s", got, landed)
	}
	if got := remoteTip(); got != landed {
		t.Fatalf("remote tip = %s, want %s", got, landed)
	}
	if got := unrecordedEvents(); len(got) != 1 || got[0] != hand {
		t.Fatalf("unrecorded-trunk-commit events = %v, want [%s]", got, hand)
	}
	assertNoAlarms()
	for _, forbidden := range []string{"reset", "accept-rewrite", "ALARM"} {
		if strings.Contains(log.String(), forbidden) {
			t.Fatalf("loop log must not contain %q:\n%s", forbidden, log.String())
		}
	}

	// A second proof seeing the same hand-made commit writes no second event.
	heavyGit(t, root, "update-ref", GreenRef, base)
	l.heavyAttempted = ""
	if err := l.startHeavy(ctx, landed); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != landed {
		t.Fatalf("green ref on second proof = %s, want %s", got, landed)
	}
	if got := unrecordedEvents(); len(got) != 1 || got[0] != hand {
		t.Fatalf("unrecorded-trunk-commit events after second proof = %v, want [%s]", got, hand)
	}
	assertNoAlarms()

	// Tip publication also pushes without withholding a hand-made commit and records it once.
	hand2 := heavyCommit(t, root, "hand-made-tip")
	l.cfg.PostLanding.Push.What = "tip"
	if _, err := l.pushTipOnce(ctx, l.cfg.PostLanding.Push); err != nil {
		t.Fatalf("pushTipOnce with hand-made commit failed: %v", err)
	}
	if got := remoteTip(); got != hand2 {
		t.Fatalf("remote tip after pushTipOnce = %s, want %s", got, hand2)
	}
	if got := unrecordedEvents(); len(got) != 2 || got[0] != hand || got[1] != hand2 {
		t.Fatalf("unrecorded-trunk-commit events after pushTipOnce = %v, want [%s %s]", got, hand, hand2)
	}
	l.heavyAttempted = ""
	if err := l.startHeavy(ctx, hand2); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != hand2 {
		t.Fatalf("green ref after hand2 proof = %s, want %s", got, hand2)
	}
	if got := unrecordedEvents(); len(got) != 2 || got[0] != hand || got[1] != hand2 {
		t.Fatalf("unrecorded-trunk-commit events after hand2 proof = %v, want [%s %s]", got, hand, hand2)
	}
	assertNoAlarms()
}

func TestForkPinCommitNotRecordedAsHandMade(t *testing.T) {
	ctx := context.Background()
	root, base := heavyRepo(t)
	remote := filepath.Join(scratchUnderDurable(t), "remote.git")
	heavyGit(t, root, "init", "--bare", remote)
	heavyGit(t, root, "remote", "add", "origin", remote)

	// Seed a tracked fork pin and pack manifest on trunk pointing at base.
	const forkRemote = "git@github.com:example/tillr"
	pinRel := "yolo-packs/fork-pins/pi"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(pinRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pinRel), []byte(fmt.Sprintf("%s\n%s\n%s\n", base, forkRemote, base)), 0o644); err != nil {
		t.Fatal(err)
	}
	packRel := "yolo-packs/pi-fork/pack.json"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(packRel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, packRel), []byte(`{"contributes":[{"source":"git+https://github.com/example/pi?ref=main","patch_branch":"git+ssh://git@github.com/example/tillr?ref=patch/pi"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".forks/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", ".gitignore", pinRel, packRel)
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "track pi fork pin")
	trunkBefore := heavyGit(t, root, "rev-parse", "HEAD")
	heavyGit(t, root, "update-ref", GreenRef, trunkBefore)
	heavyGit(t, root, "push", "-u", "origin", TrunkRef)

	// Set up patch/pi at base and .forks/pi with upstream/main at base.
	heavyGit(t, root, "branch", "patch/pi", base)
	cache := filepath.Join(root, ".forks/pi")
	heavyGit(t, root, "clone", "--quiet", "--no-checkout", root, cache)
	heavyGit(t, cache, "checkout", "-B", "main", base)
	heavyGit(t, cache, "update-ref", "refs/remotes/upstream/main", base)

	// Create a fork candidate commit descending from base.
	heavyGit(t, root, "checkout", "-q", "-b", "fork-candidate", base)
	if err := os.WriteFile(filepath.Join(root, "fork-change.txt"), []byte("new fork work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", "fork-change.txt")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "feat: fork change")
	newTip := heavyGit(t, root, "rev-parse", "HEAD")
	treeSHA := heavyGit(t, root, "rev-parse", "HEAD^{tree}")
	heavyGit(t, root, "checkout", "-q", TrunkRef)

	s, err := store.InitFixture(ctx, filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	trunk, err := land.OpenTrunk(root)
	if err != nil {
		t.Fatal(err)
	}
	landed, err := land.Land(ctx, s, trunk, land.LandingRequest{
		ItemID:        "item-fork-pin",
		RunID:         "run-fork-pin",
		BaseRef:       "patch/pi",
		BaseSHA:       base,
		HeadSHA:       newTip,
		ObservedTrunk: trunkBefore,
		At:            time.Now().UTC(),
		Verdict: land.Verdict{
			VerdictID: "verdict-fork-pin",
			BaseSHA:   base,
			TreeSHA:   treeSHA,
			Result:    land.VerdictPass,
			Checks:    []land.VerdictCheck{{CheckID: "fork-build", Result: land.VerdictPass}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !landed.Landed() {
		t.Fatalf("fork landing = %s/%s, want landed", landed.Result, landed.Reason)
	}
	pinCommit := heavyGit(t, root, "rev-parse", "HEAD")
	if pinCommit == trunkBefore {
		t.Fatal("trunk did not advance with fork pin commit")
	}

	// Commit one hand-made commit on top of the fork pin commit.
	hand := heavyCommit(t, root, "hand-made-after-pin")

	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	log := &strings.Builder{}
	l := &Loop{
		gatePolicy: GatePolicy{LandingStrategy: "after-merge"},
		cfg:        LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: log},
	}
	l.cfg.PostLanding.Push.What = "green"
	unrecordedEvents := func() []string {
		t.Helper()
		var commits []string
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT coalesce(json_extract(payload, '$.commit'), '')
FROM events WHERE kind = ? ORDER BY seq ASC`, alarms.UnrecordedTrunkCommitKind)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var commit string
				if err := rows.Scan(&commit); err != nil {
					return err
				}
				commits = append(commits, commit)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return commits
	}

	if err := l.startHeavy(ctx, hand); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)

	if got := heavyGit(t, root, "rev-parse", GreenRef); got != hand {
		t.Fatalf("green ref = %s, want %s", got, hand)
	}
	if got := unrecordedEvents(); len(got) != 1 || got[0] != hand {
		t.Fatalf("unrecorded-trunk-commit events = %v, want only hand-made [%s] (pin commit %s must not be recorded)", got, hand, pinCommit)
	}
	if strings.Contains(log.String(), pinCommit) {
		t.Fatalf("loop log recorded fork pin commit %s as hand-made:\n%s", pinCommit, log.String())
	}

	// Tip publication also recognizes the pin commit and does not re-record hand.
	heavyGit(t, root, "update-ref", GreenRef, trunkBefore)
	l.cfg.PostLanding.Push.What = "tip"
	if _, err := l.pushTipOnce(ctx, l.cfg.PostLanding.Push); err != nil {
		t.Fatalf("pushTipOnce failed: %v", err)
	}
	if got := unrecordedEvents(); len(got) != 1 || got[0] != hand {
		t.Fatalf("unrecorded-trunk-commit events after pushTipOnce = %v, want [%s]", got, hand)
	}
}
