package drain

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
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

func TestLoopRefusesUnrecordedTrunkCommitAndRaisesAlarm(t *testing.T) {
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

	// A commit appears on trunk with no landing record in the store.
	unrecorded := heavyCommit(t, root, "unrecorded")
	if err := l.startHeavy(ctx, unrecorded); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)

	if got := heavyGit(t, root, "rev-parse", GreenRef); got != base {
		t.Fatalf("green ref moved to %s, want %s (unchanged)", got, base)
	}
	if got := remoteTip(); got != base {
		t.Fatalf("remote pushed unrecorded commit %s, want %s", got, base)
	}
	var active []alarms.Alarm
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		active, err = alarms.Read(r, time.Now(), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Kind != alarms.UnrecordedTrunkCommit || !strings.Contains(active[0].Cause, unrecorded) {
		t.Fatalf("alarms = %+v, want one %s naming %s", active, alarms.UnrecordedTrunkCommit, unrecorded)
	}
	if !strings.Contains(log.String(), unrecorded) {
		t.Fatalf("loop log does not name unrecorded commit %s:\n%s", unrecorded, log.String())
	}

	// Recording the landing and re-running clears the alarm and allows green to advance and push.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		return recordFixtureLanding(tx, "item-recorded", "run-recorded", unrecorded, 0)
	}); err != nil {
		t.Fatal(err)
	}
	l.heavyAttempted = ""
	if err := l.startHeavy(ctx, unrecorded); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	waitAction(t, &l.pushAction)
	if got := remoteTip(); got != unrecorded {
		t.Fatalf("remote = %s, want %s after landing recorded", got, unrecorded)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		active, err = alarms.Read(r, time.Now(), nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("expected alarms to clear after landing recorded, got %+v", active)
	}
}
