package drain

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// A run that faults has given no verdict, so its tip's trigger is not spent: the next
// pass proves the tip instead of leaving the green pointer where it was.
func TestHeavyRunnerFaultDoesNotConsumeTheTipsTrigger(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "one")
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var calls atomic.Int32
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("judge fault")
		}
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	var log strings.Builder
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &log}}
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != base || !strings.Contains(log.String(), "heavy runner fault") {
		t.Fatalf("green=%s log=%s", got, log.String())
	}
	if l.heavyAttempted == tip {
		t.Fatal("a faulted run consumed the tip's trigger")
	}
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != tip || calls.Load() != 2 {
		t.Fatalf("green=%s calls=%d log=%s", got, calls.Load(), log.String())
	}
}

func TestHeavyStatusLineNamesFailingChecks(t *testing.T) {
	line := heavyStatusLine(HeavyStatus{Result: "red", FailingChecks: []HeavyCheck{{CheckID: "a"}, {CheckID: "b"}}})
	if !strings.Contains(line, "failing=a,b") {
		t.Fatal(line)
	}
}

type syncFaultSteps struct {
	heavyTestSteps
}

func (syncFaultSteps) Sync(context.Context) (SyncOutcome, error) {
	return SyncOutcome{}, errors.New("sync fault")
}

// Document sync faults on every start must not starve after-merge proof: the pass starts
// it before syncing.
func TestPassStartsAfterMergeProofBeforeASyncFault(t *testing.T) {
	f, l := passFixture(t, "item-a")
	var calls atomic.Int32
	l.cfg.Steps = syncFaultSteps{heavyTestSteps: heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		calls.Add(1)
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}}
	l.gatePolicy.LandingStrategy = "after-merge"
	l.cfg.Report = io.Discard
	_, err := l.pass(context.Background(), &LoopResult{})
	l.heavyWG.Wait()
	if err == nil || !strings.Contains(err.Error(), "sync fault") {
		t.Fatalf("pass err=%v", err)
	}
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	if calls.Load() != 1 || strings.TrimSpace(runGit(t, f.root, "rev-parse", GreenRef)) != strings.TrimSpace(tip) {
		t.Fatalf("calls=%d green not at tip %s", calls.Load(), tip)
	}
}

func TestPostLandingRollFailureIsInTheLoopReport(t *testing.T) {
	l, _, commit := actionFixture(t)
	var log strings.Builder
	l.cfg.Report = &log
	l.cfg.PostLanding.Roll = settings.Roll{Paths: []string{"src"}, Command: `echo "tillr-roll: refusing a dirty checkout"; exit 3`}
	tip := commit("src/change")
	l.afterLanding(context.Background(), tip)
	waitAction(t, func() bool { return !l.rollAction.busy() })
	if got := log.String(); !strings.Contains(got, "post-landing roll at "+shortSHA(tip)+" failed") || !strings.Contains(got, "refusing a dirty checkout") {
		t.Fatalf("report=%q", got)
	}
}
