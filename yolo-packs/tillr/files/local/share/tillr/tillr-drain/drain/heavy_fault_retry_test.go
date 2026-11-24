package drain

import (
	"context"
	"encoding/json"
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
	const faultAttempts = 2
	fault := errors.New("judge fault: test runner exited\nwith detail")
	assertFaultEvents := func(want int) {
		t.Helper()
		events, err := s.Events(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, e := range events {
			if e.Kind != "drain.heavy-fault" {
				continue
			}
			if count >= want {
				t.Fatalf("unexpected drain.heavy-fault event: %+v", e)
			}
			if e.ObjectType != "commit" || e.ObjectID == nil || *e.ObjectID != tip {
				t.Fatalf("heavy fault event=%+v, want commit %s", e, tip)
			}
			var payload struct {
				TipSHA string `json:"tip_sha"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.TipSHA != tip || payload.Detail != fault.Error() {
				t.Fatalf("heavy fault payload=%s, want tip %s and full error %q", e.Payload, tip, fault.Error())
			}
			count++
		}
		if count != want {
			t.Fatalf("drain.heavy-fault events=%d, want %d", count, want)
		}
	}
	var calls atomic.Int32
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		if calls.Add(1) <= faultAttempts {
			return nil, fault
		}
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	var log strings.Builder
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &log}}
	for attempt := 1; attempt <= faultAttempts; attempt++ {
		var err error
		if attempt == 1 {
			// The runner resolves trunk, not this stale trigger: the fault must name tip.
			err = l.startHeavy(context.Background(), base)
		} else {
			err = l.maybeRolling(context.Background(), tip, false, &LoopResult{})
		}
		if err != nil {
			t.Fatal(err)
		}
		l.heavyWG.Wait()
		if got := heavyGit(t, root, "rev-parse", GreenRef); got != base || calls.Load() != int32(attempt) || !strings.Contains(log.String(), "heavy runner fault") {
			t.Fatalf("green=%s calls=%d log=%s", got, calls.Load(), log.String())
		}
		if l.heavyAttempted == tip {
			t.Fatal("a faulted run consumed the tip's trigger")
		}
		assertFaultEvents(attempt)
	}
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	if got := heavyGit(t, root, "rev-parse", GreenRef); got != tip || calls.Load() != faultAttempts+1 {
		t.Fatalf("green=%s calls=%d log=%s", got, calls.Load(), log.String())
	}
	assertFaultEvents(faultAttempts)
}

func TestHeavyStatusLineNamesFailingChecks(t *testing.T) {
	line := heavyStatusLine(HeavyStatus{Result: "red", FailingChecks: []HeavyCheck{{CheckID: "a"}, {CheckID: "b"}}})
	if !strings.Contains(line, "failing=a,b") {
		t.Fatal(line)
	}
}

func TestHeavyStatusLineShowsRunningProofInsteadOfPriorFault(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "one")
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const faultDetail = "cleanup not acknowledged; checkout retained; independently recover descendants named by .git/tillr-gate/heavy.cleanup-pending before clearing that guard"
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	cancelledEntered := make(chan struct{})
	steps := heavyTestSteps{judge: func(ctx context.Context, _ string, _ string) ([]HeavyCheck, error) {
		switch calls.Add(1) {
		case 1:
			return []HeavyCheck{{CheckID: "legacy-runner", Result: "error"}}, errors.New(faultDetail)
		case 2:
			close(cancelledEntered)
			<-ctx.Done()
			return nil, ctx.Err()
		default:
			close(entered)
			<-release
			return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
		}
	}}
	var log strings.Builder
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: &log}}

	// Attempt 1 faults and records heavy=unknown with the cleanup fault text.
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()

	// Attempt 2 starts via maybeRolling (which first sees heavy=unknown before starting
	// the retry) and is cancelled mid-proof; while running it reports heavy=running,
	// and cancelling it must not resurrect Attempt 1's cleared fault text.
	cancelCtx, cancel := context.WithCancel(context.Background())
	if err := l.maybeRolling(cancelCtx, tip, false, &LoopResult{}); err != nil {
		cancel()
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "heavy=unknown") {
		cancel()
		l.heavyWG.Wait()
		t.Fatalf("expected heavy=unknown before retry started, got %q", log.String())
	}
	<-cancelledEntered
	log.Reset()
	l.lastHeavyStatus = ""
	if err := l.maybeRolling(cancelCtx, tip, false, &LoopResult{}); err != nil {
		cancel()
		l.heavyWG.Wait()
		t.Fatal(err)
	}
	if got := log.String(); !strings.Contains(got, "heavy=running") || strings.Contains(got, "heavy=unknown") {
		cancel()
		l.heavyWG.Wait()
		t.Fatalf("loop status while proof running = %q, want heavy=running without heavy=unknown", got)
	}
	cancel()
	l.heavyWG.Wait()
	afterCancel, err := ReadHeavyStatus(context.Background(), root, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(afterCancel.Detail, "cleanup not acknowledged") || afterCancel.Result == "unknown" {
		t.Fatalf("cancelled retry resurrected earlier fault: %+v", afterCancel)
	}

	// Attempt 3 runs to completion; while running maybeRolling logs heavy=running, and once
	// finished the trunk is green with no fault detail.
	if err := l.startHeavy(context.Background(), tip); err != nil {
		t.Fatal(err)
	}
	<-entered
	log.Reset()
	l.lastHeavyStatus = ""
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		close(release)
		l.heavyWG.Wait()
		t.Fatal(err)
	}
	if got := log.String(); !strings.Contains(got, "heavy=running") || strings.Contains(got, "heavy=unknown") {
		close(release)
		l.heavyWG.Wait()
		t.Fatalf("loop status while proof running = %q, want heavy=running", got)
	}
	close(release)
	l.heavyWG.Wait()
	finalStatus, err := ReadHeavyStatus(context.Background(), root, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	if finalStatus.Result != "green" || finalStatus.Detail != "" || finalStatus.GreenCommit != tip {
		t.Fatalf("final status = %+v, want green on %s with empty detail", finalStatus, tip)
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
	waitAction(t, &l.rollAction)
	if got := log.String(); !strings.Contains(got, "post-landing roll at "+shortSHA(tip)+" failed") || !strings.Contains(got, "refusing a dirty checkout") {
		t.Fatalf("report=%q", got)
	}
}
