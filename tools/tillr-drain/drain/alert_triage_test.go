package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

const triageTestEventKind = "watch-triage-test.fact"
const triageTestClearKind = "watch-triage-test.clear"

func init() {
	for _, kind := range []string{triageTestEventKind, triageTestClearKind} {
		_ = event.Register(kind, "test")
	}
}

var triageTestBaseNow = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

func triageTestRules() []watch.Rule {
	expr, err := watch.ParseExpression(`event.kind == "` + triageTestClearKind + `"`)
	if err != nil {
		panic(err)
	}
	return []watch.Rule{
		{
			ID:       "triage-test-rule",
			Scope:    "core",
			Enabled:  true,
			Severity: "warning",
			Reads:    watch.Reads{Events: []string{triageTestEventKind, triageTestClearKind}},
			When: watch.When{
				Pattern:   watch.PredicateMatch,
				GroupBy:   "subject",
				Threshold: 1,
				Window:    time.Hour,
				ClearWhen: expr,
			},
			Delivery: watch.Delivery{Receiver: "triage"},
		},
	}
}

func setupTriageLoop(t *testing.T, f refusalLoopFixture, steps Steps, rules []watch.Rule) *Loop {
	t.Helper()
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.WatchRules = rules
	l.cfg.TriageTemplate = filepath.Join("..", "..", "tillr-run", "backends", "brief-template-triage.md")
	l.cfg.Policy = &Policy{
		Root:          f.root,
		Backends:      map[string]Backend{"fixture": {Adapter: "fixture"}, "pi-scout": {Adapter: "fixture"}},
		Seats:         map[string]string{item.SeatBuild: "fixture", item.SeatTriage: "pi-scout"},
		ReadOnlySeats: []string{item.SeatTriage},
		SeatPurposes:  map[string]string{item.SeatTriage: "triage alerts"},
		Escalate:      map[string][]string{item.SeatTriage: {}},
	}
	return l
}

func openWritableFixture(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func emitTriageFactAndEvaluate(t *testing.T, f refusalLoopFixture, rules []watch.Rule, kind, subject string, now time.Time) {
	t.Helper()
	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := eval.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		payload, err := json.Marshal(map[string]string{"subject": subject})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind:       kind,
			At:         now,
			ObjectType: "test",
			Payload:    payload,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	res, err := eval.Evaluate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_ = res
}

type triageSteps struct {
	mu      sync.Mutex
	onRun   func(ctx context.Context, req run.Request) (run.Record, string, error)
	reqs    []run.Request
	started chan run.Request
}

func (s *triageSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	if s.started != nil {
		select {
		case s.started <- req:
		default:
		}
	}
	if s.onRun != nil {
		return s.onRun(ctx, req)
	}
	code := 0
	return run.Record{RunID: req.RunID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, EndCause: run.EndNormalExit}, "", nil
}

func (s *triageSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	return GateOutcome{}, nil
}
func (s *triageSteps) Land(context.Context, Candidate) (LandOutcome, error) {
	return LandOutcome{}, nil
}
func (s *triageSteps) Verdict(context.Context, Candidate, string) error { return nil }
func (s *triageSteps) ItemState(context.Context, string, string, string, ...item.WaitingOn) error {
	return nil
}
func (s *triageSteps) Sync(context.Context) (SyncOutcome, error) { return SyncOutcome{}, nil }

func TestAlertTriageConcurrencyCapWhileBuildLanesBusy(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-busy-1", "item-busy-2")
	rules := triageTestRules()
	now := triageTestBaseNow
	for _, subj := range []string{"s1", "s2", "s3", "s4"} {
		now = now.Add(time.Second)
		emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, subj, now)
	}
	{
		sc := openWritableFixture(t, f.storePath)
		var eps []watch.EligibleTriageEpisode
		_ = sc.ReadTx(context.Background(), func(rd *store.Reader) error {
			var err error
			eps, err = watch.ReadEligibleTriageEpisodes(rd, map[string]bool{"triage-test-rule": true})
			return err
		})
		sc.Close()
		if len(eps) != 4 {
			t.Fatalf("before Loop.Run: expected 4 eligible episodes, got %d", len(eps))
		}
	}

	// Mark all build lanes busy (leased) so build lanes are saturated.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, laneID := range []string{"lane-1", "lane-2"} {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES(?,?,?,NULL)
				ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state, run_id=excluded.run_id, leased_until=NULL`,
				laneID, run.LaneStateLeased, "busy-"+laneID); err != nil {
				return err
			}
		}
		return nil
	})

	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var seenOrder []string
	var orderMu sync.Mutex
	releaseFirstTwo := make(chan struct{})
	firstTwoStarted := make(chan struct{}, 2)

	steps := &triageSteps{
		onRun: func(ctx context.Context, req run.Request) (run.Record, string, error) {
			cur := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			orderMu.Lock()
			seenOrder = append(seenOrder, req.ItemID+":"+req.LaneID)
			idx := len(seenOrder)
			orderMu.Unlock()

			if idx <= 2 {
				firstTwoStarted <- struct{}{}
				select {
				case <-releaseFirstTwo:
				case <-ctx.Done():
				}
			}
			if err := eval.RecordTriage(ctx, req.ItemID, watch.RecordTriageOptions{
				RunID:       req.RunID,
				Outcome:     "filed",
				OutcomeKind: "fix-fault",
				ItemID:      "item-busy-1",
				Finding:     "root cause found",
			}); err != nil {
				t.Errorf("RecordTriage(%s): %v", req.ItemID, err)
			}
			inFlight.Add(-1)
			code := 0
			return run.Record{RunID: req.RunID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, EndCause: run.EndNormalExit}, "", nil
		},
	}

	l := setupTriageLoop(t, f, steps, rules)
	var logBuf strings.Builder
	l.cfg.Report = &logBuf
	l.cfg.ExitWhenDrained = true
	l.cfg.MaxConcurrentTriage = 2

	done := make(chan LoopResult, 1)
	go func() {
		res, err := l.Run(context.Background())
		if err != nil {
			t.Errorf("Loop.Run: %v", err)
		}
		done <- res
	}()

	<-firstTwoStarted
	<-firstTwoStarted
	// Give a brief moment to verify no 3rd run starts while 2 are in flight.
	time.Sleep(50 * time.Millisecond)
	if got := inFlight.Load(); got != 2 {
		t.Fatalf("expected 2 in-flight triage runs, got %d", got)
	}
	close(releaseFirstTwo)

	res := <-done
	if maxInFlight.Load() != 2 {
		t.Fatalf("expected max concurrency 2, got %d", maxInFlight.Load())
	}
	if len(seenOrder) != 4 {
		t.Fatalf("expected 4 triage runs total, got %d (%v); loop stop=%s detail=%s\nlog:\n%s", len(seenOrder), seenOrder, res.Stop, res.Detail, logBuf.String())
	}
	for _, entry := range seenOrder {
		if !strings.HasSuffix(entry, ":triage-1") && !strings.HasSuffix(entry, ":triage-2") {
			t.Fatalf("expected dedicated triage-1/triage-2 slot, got %q", entry)
		}
	}
}

func TestAlertTriageNoDispatchWhenNoneAndNoRepeatOnSettled(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-1")
	rules := triageTestRules()
	var runs atomic.Int32
	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return triageTestBaseNow }}

	steps := &triageSteps{
		onRun: func(ctx context.Context, req run.Request) (run.Record, string, error) {
			runs.Add(1)
			if err := eval.RecordTriage(ctx, req.ItemID, watch.RecordTriageOptions{
				RunID:       req.RunID,
				Outcome:     "linked",
				OutcomeKind: "fix-fault",
				ItemID:      "item-1",
				Finding:     "matches item-1",
			}); err != nil {
				t.Errorf("RecordTriage: %v", err)
			}
			code := 0
			return run.Record{RunID: req.RunID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, EndCause: run.EndNormalExit}, "", nil
		},
	}

	l := setupTriageLoop(t, f, steps, rules)
	// Zero triage alerts -> zero runs.
	l.dispatchAlertTriage(context.Background())
	l.triageWG.Wait()
	if runs.Load() != 0 {
		t.Fatalf("expected 0 runs when no triage alerts exist, got %d", runs.Load())
	}

	// Open 1 episode -> dispatches once and settles.
	emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s1", triageTestBaseNow.Add(time.Second))
	l.dispatchAlertTriage(context.Background())
	l.triageWG.Wait()
	if runs.Load() != 1 {
		t.Fatalf("expected 1 run, got %d", runs.Load())
	}

	// Repeated dispatch passes do not re-dispatch settled episode.
	l.dispatchAlertTriage(context.Background())
	l.triageWG.Wait()
	if runs.Load() != 1 {
		t.Fatalf("expected still 1 run after settled episode, got %d", runs.Load())
	}

	// Reopen episode with a newer matching fact -> re-dispatches once.
	emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s1", triageTestBaseNow.Add(2*time.Minute))
	l.dispatchAlertTriage(context.Background())
	l.triageWG.Wait()
	if runs.Load() != 2 {
		t.Fatalf("expected 2 runs after reopen, got %d", runs.Load())
	}
}

func TestAlertTriageAutoResolvedUninvestigated(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-auto")
	rules := triageTestRules()
	emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-auto", triageTestBaseNow.Add(time.Second))
	emitTriageFactAndEvaluate(t, f, rules, triageTestClearKind, "s-auto", triageTestBaseNow.Add(2*time.Second))

	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return triageTestBaseNow }}

	var triaged bool
	steps := &triageSteps{
		onRun: func(ctx context.Context, req run.Request) (run.Record, string, error) {
			triaged = true
			if err := eval.RecordTriage(ctx, req.ItemID, watch.RecordTriageOptions{
				RunID:       req.RunID,
				Outcome:     "filed",
				OutcomeKind: "fix-rule",
				ItemID:      "item-auto",
				Finding:     "auto-resolved transient spike",
			}); err != nil {
				t.Errorf("RecordTriage: %v", err)
			}
			code := 0
			return run.Record{RunID: req.RunID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, EndCause: run.EndNormalExit}, "", nil
		},
	}

	l := setupTriageLoop(t, f, steps, rules)
	l.dispatchAlertTriage(context.Background())
	l.triageWG.Wait()
	if !triaged {
		t.Fatal("expected auto-resolved uninvestigated episode to be triaged")
	}

	var state, settlement, triageState string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, settlement_state, triage_state FROM alert_episodes WHERE subject_key='s-auto'`).
			Scan(&state, &settlement, &triageState)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" || settlement != "settled" || triageState != "settled" {
		t.Fatalf("expected resolved/settled/settled, got %s/%s/%s", state, settlement, triageState)
	}
}

func TestAlertTriageFailureEscalatesImmediatelyWithNoRetry(t *testing.T) {
	rules := triageTestRules()

	t.Run("non-zero exit", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-1")
		emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-nonzero", triageTestBaseNow.Add(time.Second))
		var runs atomic.Int32
		steps := &triageSteps{
			onRun: func(_ context.Context, req run.Request) (run.Record, string, error) {
				runs.Add(1)
				code := 2
				return run.Record{RunID: req.RunID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, EndCause: run.EndNormalExit}, "", nil
			},
		}
		l := setupTriageLoop(t, f, steps, rules)
		l.dispatchAlertTriage(context.Background())
		l.triageWG.Wait()
		// Second pass must not retry.
		l.dispatchAlertTriage(context.Background())
		l.triageWG.Wait()
		if runs.Load() != 1 {
			t.Fatalf("expected 1 run (no retry), got %d", runs.Load())
		}
		assertTriageFailedEscalation(t, f, rules, "s-nonzero")
	})

	t.Run("run error", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-1")
		emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-err", triageTestBaseNow.Add(time.Second))
		steps := &triageSteps{
			onRun: func(_ context.Context, req run.Request) (run.Record, string, error) {
				return run.Record{}, "", errors.New("adapter crashed")
			},
		}
		l := setupTriageLoop(t, f, steps, rules)
		l.dispatchAlertTriage(context.Background())
		l.triageWG.Wait()
		assertTriageFailedEscalation(t, f, rules, "s-err")
	})

	t.Run("exit 0 without record-triage", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-1")
		emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-unrecorded", triageTestBaseNow.Add(time.Second))
		steps := &triageSteps{}
		l := setupTriageLoop(t, f, steps, rules)
		l.dispatchAlertTriage(context.Background())
		l.triageWG.Wait()
		assertTriageFailedEscalation(t, f, rules, "s-unrecorded")
	})

	t.Run("closed quota-shut backend", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-1")
		emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-shut", triageTestBaseNow.Add(time.Second))
		// Mark pi-scout quota-shut in backend_windows.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			until := triageTestBaseNow.Add(time.Hour).UnixMilli()
			_, err := tx.Exec(`INSERT INTO backend_windows(backend, kind, fraction, status, reset_at, run_id) VALUES ('pi-scout', 'window', 1.0, 'shut', ?, 'run-shut')
				ON CONFLICT(backend) DO UPDATE SET status=excluded.status, reset_at=excluded.reset_at`, until)
			return err
		})
		var runs atomic.Int32
		steps := &triageSteps{
			onRun: func(_ context.Context, req run.Request) (run.Record, string, error) {
				runs.Add(1)
				return run.Record{}, "", nil
			},
		}
		l := setupTriageLoop(t, f, steps, rules)
		l.dispatchAlertTriage(context.Background())
		l.triageWG.Wait()
		if runs.Load() != 0 {
			t.Fatalf("expected 0 runs when backend is closed, got %d", runs.Load())
		}
		assertTriageFailedEscalation(t, f, rules, "s-shut")
	})
}

func assertTriageFailedEscalation(t *testing.T, f refusalLoopFixture, rules []watch.Rule, subject string) {
	t.Helper()
	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	var tState, tOutcome, active string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT triage_state, triage_outcome, COALESCE(active_triage_run_id, '') FROM alert_episodes WHERE subject_key=?`, subject).
			Scan(&tState, &tOutcome, &active)
	}); err != nil {
		t.Fatal(err)
	}
	if tState != "escalated" || tOutcome != "triage-failed" || active != "" {
		t.Fatalf("expected escalated/triage-failed/empty, got %s/%s/%q", tState, tOutcome, active)
	}
	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return triageTestBaseNow }}
	notices, err := eval.Pending(context.Background(), "operator-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) == 0 || notices[0].Bundle.Triage == nil || notices[0].Bundle.Triage.Outcome != "triage-failed" {
		t.Fatalf("expected operator-session notice with triage-failed header, got %+v", notices)
	}
}

func TestAlertTriageStaleClaimRecovery(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-1")
	rules := triageTestRules()
	emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-stale", triageTestBaseNow.Add(time.Second))

	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	var alertID string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE subject_key='s-stale'`).Scan(&alertID)
	}); err != nil {
		t.Fatal(err)
	}
	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return triageTestBaseNow }}
	if err := eval.ClaimTriage(context.Background(), alertID, "run-dead-triage"); err != nil {
		t.Fatal(err)
	}

	l := setupTriageLoop(t, f, &triageSteps{}, rules)
	var logBuf strings.Builder
	l.cfg.Report = &logBuf
	l.recoverStaleTriageClaims(context.Background())

	var tState, tOutcome, trk, active string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT triage_state, triage_outcome, COALESCE(tracking_item_id, ''), COALESCE(active_triage_run_id, '') FROM alert_episodes WHERE alert_id=?`, alertID).
			Scan(&tState, &tOutcome, &trk, &active)
	}); err != nil {
		t.Fatal(err)
	}
	if tState != "escalated" || tOutcome != "triage-failed" || trk == "" || active != "" {
		t.Fatalf("expected escalated/triage-failed/item/empty, got %s/%s/%q/%q\nlog:\n%s", tState, tOutcome, trk, active, logBuf.String())
	}
}

func TestAlertTriageRepeatFloodEscalation(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-flood")
	rules := triageTestRules()
	emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-flood", triageTestBaseNow.Add(time.Second))

	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	var alertID string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE subject_key='s-flood'`).Scan(&alertID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET reopen_count_for_item = 3, tracking_item_id = 'item-flood' WHERE alert_id = ?`, alertID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	eval := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return triageTestBaseNow }}
	steps := &triageSteps{
		onRun: func(ctx context.Context, req run.Request) (run.Record, string, error) {
			if err := eval.RecordTriage(ctx, req.ItemID, watch.RecordTriageOptions{
				RunID:       req.RunID,
				Outcome:     "linked",
				OutcomeKind: "fix-fault",
				ItemID:      "item-flood",
				Finding:     "same underlying fault",
			}); err != nil {
				t.Errorf("RecordTriage: %v", err)
			}
			code := 0
			return run.Record{RunID: req.RunID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: &code, EndCause: run.EndNormalExit}, "", nil
		},
	}

	l := setupTriageLoop(t, f, steps, rules)
	l.dispatchAlertTriage(context.Background())
	l.triageWG.Wait()

	var tState, tOutcome, escTo, actionNeeded string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT triage_state, triage_outcome, COALESCE(escalated_to, ''), COALESCE(triage_action_needed, '') FROM alert_episodes WHERE alert_id=?`, alertID).
			Scan(&tState, &tOutcome, &escTo, &actionNeeded)
	}); err != nil {
		t.Fatal(err)
	}
	if tState != "escalated" || tOutcome != "escalated" || escTo != "operator-session" {
		t.Fatalf("expected escalated/escalated/operator-session, got %s/%s/%s", tState, tOutcome, escTo)
	}
	if !strings.Contains(actionNeeded, "tillr watch silence "+alertID) ||
		!strings.Contains(actionNeeded, "--store "+f.storePath) ||
		!strings.Contains(actionNeeded, "--by operator-session") ||
		!strings.Contains(actionNeeded, "--item item-flood") ||
		!strings.Contains(actionNeeded, "--reason ") ||
		!strings.Contains(actionNeeded, "--until-change") ||
		strings.Contains(actionNeeded, "--rule ") ||
		strings.Contains(actionNeeded, "--subject ") {
		t.Fatalf("expected valid tillr watch silence command in action_needed, got %q", actionNeeded)
	}
}

func TestAlertTriageBriefRendersAndPassesPreflight(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-1")
	rules := triageTestRules()
	emitTriageFactAndEvaluate(t, f, rules, triageTestEventKind, "s-brief", triageTestBaseNow.Add(time.Second))

	l := setupTriageLoop(t, f, &triageSteps{}, rules)
	s := openWritableFixture(t, f.storePath)
	defer s.Close()
	var ep watch.EligibleTriageEpisode
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		eps, err := watch.ReadEligibleTriageEpisodes(rd, map[string]bool{"triage-test-rule": true})
		if err != nil {
			return err
		}
		if len(eps) != 1 {
			return fmt.Errorf("expected 1 eligible episode, got %d", len(eps))
		}
		ep = eps[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := l.stageTriageBrief(context.Background(), s, ep, "triage-1", "run-brief-1", "pi-scout", 30000, l.cfg.Policy); err != nil {
		t.Fatalf("stageTriageBrief: %v", err)
	}
	briefPath := filepath.Join(f.laneRoot, "triage-1", "brief.md")
	raw, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if matched := regexp.MustCompile(`\{\{[A-Z0-9_]+\}\}`).FindString(string(raw)); matched != "" {
		t.Fatalf("rendered triage brief still contains unfilled placeholder %s:\n%s", matched, string(raw))
	}

	preflightPath, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(". %q && PROG=triage-test && preflight_brief %q", preflightPath, briefPath)
	cmd := exec.Command("sh", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("preflight.sh failed on rendered triage brief: %v\n%s", err, string(out))
	}
}
