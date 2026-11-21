package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// hangGuard is how long a test waits for an event that is certain to happen before it
// concludes the code deadlocked. It is a deadlock detector and never an expectation: no
// passing run reaches it however loaded the machine is, and no test may assert that
// something happened "quickly". It is kept below the suite's own timeout so that a hang
// fails the one test with a message instead of killing the whole package.
const hangGuard = 5 * time.Minute

func passFixture(t *testing.T, ids ...string) (refusalLoopFixture, *Loop) {
	t.Helper()
	f := newRefusalLoopFixture(t, ids...)
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	holdWALKeeper(t, f.storePath)
	return f, l
}

func mustPlan(t *testing.T, l *Loop) []plannedApplication {
	t.Helper()
	plans, _, err := l.passPlans(context.Background(), l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	return plans
}

func fixtureWrite(t *testing.T, f refusalLoopFixture, change func(*store.Tx) error) {
	t.Helper()
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(context.Background(), change); err != nil {
		t.Fatal(err)
	}
}

func TestAPassDeclinesDispatchWhenItemRunOrLeaseChanges(t *testing.T) {
	for _, change := range []string{"item", "run", "lease"} {
		t.Run(change, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			plan := mustPlan(t, l)[0]
			fixtureWrite(t, f, func(tx *store.Tx) error {
				switch change {
				case "item":
					_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, StateBlocked, plan.decision.ItemID)
					return err
				case "run":
					return applyTerminal(tx, run.Record{RunID: "foreign", ItemID: plan.decision.ItemID, LaneID: plan.decision.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
				case "lease":
					_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "foreign", ItemID: plan.decision.ItemID, Backend: "fixture"}, plan.decision.LaneID, l.cfg.Now())
					return err
				}
				return nil
			})
			before := eventCount(t, f.storePath)
			ok, _, err := l.applyPlan(context.Background(), plan, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
			if err != nil || ok {
				t.Fatalf("stale %s applied: %v / %v", change, ok, err)
			}
			assertOnlyDispatchDecline(t, f.storePath, before, plan.decision, "item or lane state changed since planning")
			s := openFixture(t, f.storePath)
			defer s.Close()
			s.ReadTx(context.Background(), func(r *store.Reader) error {
				var n int
				if err := r.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Errorf("stale application wrote %d decisions", n)
				}
				return nil
			})
			next := mustPlan(t, l)[0]
			if next.version == plan.version && next.decision.LaneID == plan.decision.LaneID && next.decision.Action == plan.decision.Action {
				t.Fatal("next pass did not re-read changed inputs")
			}
			if change != "lease" && next.decision.Action == ActionDispatch {
				t.Fatalf("next pass dispatched changed %s: %+v", change, next.decision)
			}
		})
	}
}

// A declined dispatch records why the drain refused the plan for `tillr why`.
// This observation must be the only event: it is not a dispatch or a lane claim.
func assertOnlyDispatchDecline(t *testing.T, path string, before int, d Decision, reason string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	events, err := s.Events(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != before+1 {
		t.Fatalf("cancelled dispatch appended %d events, want exactly one %s", len(events)-before, dispatchDeclinedKind)
	}
	e := events[before]
	if e.Kind != dispatchDeclinedKind || e.ObjectType != "item" || e.ObjectID == nil || *e.ObjectID != d.ItemID {
		t.Fatalf("cancelled dispatch appended %+v, want %s for %s", e, dispatchDeclinedKind, d.ItemID)
	}
	var decline dispatchDecline
	if err := json.Unmarshal(e.Payload, &decline); err != nil {
		t.Fatal(err)
	}
	if decline.DecisionID != d.DecisionID || decline.LaneID != d.LaneID || decline.RunID != d.RunRequest.RunID || decline.Reason != reason {
		t.Fatalf("decline payload %s, want decision %s, lane %s, run %s, reason %q", e.Payload, d.DecisionID, d.LaneID, d.RunRequest.RunID, reason)
	}
}

func eventCount(t *testing.T, path string) int {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	count := 0
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error { events, err := r.Events(0); count = len(events); return err }); err != nil {
		t.Fatal(err)
	}
	return count
}

func applyTerminal(tx *store.Tx, rec run.Record) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = run.Apply(context.Background(), tx, record.Envelope{RecordVersion: 1, Kind: run.RecordKind, ID: rec.RunID, CreatedAt: time.Now().UTC(), Payload: raw})
	return err
}

func TestALiveLeaseFromAnEarlierPassDefersTheSameFork(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return recordFixtureFork(tx, "item-a", "item-b")
	})
	plans := mustPlan(t, l)
	ok, _, err := l.applyPlan(context.Background(), plans[0], runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("first dispatch %v / %v", ok, err)
	}
	next := mustPlan(t, l)
	if got := next[1].decision; got.Reason != ReasonForkHeld || got.DeferredBy != "item-a" {
		t.Fatalf("second pass lost live fork owner: %+v", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: plans[0].decision.RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
	})
	next = mustPlan(t, l)
	if got := next[1].decision; got.Reason != ReasonForkHeld || got.DeferredBy != "item-a" {
		t.Fatalf("terminal release lost fork ownership before examination: %+v", got)
	}
}

type persistentSteps struct {
	recordingSteps
	store       string
	started     chan struct{}
	release     chan struct{}
	gated       chan struct{}
	gateRelease chan struct{}
}

func (s *persistentSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	close(s.started)
	select {
	case <-s.release:
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	}
	dir := req.WorkingDir
	// The fixture request uses a relative working_dir. The runner, like run exec,
	// is given the lane's actual worktree through the test's explicit path.
	if err := os.WriteFile(filepath.Join(dir, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		return run.Record{}, "", err
	}
	// Git's fixture helper is used by the test to commit before releasing the runner.
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: time.Now().UTC().Format(time.RFC3339), FinishedAt: time.Now().UTC().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}
	st, err := store.Open(ctx, s.store, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}

func (s *persistentSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if s.gated != nil {
		close(s.gated)
		select {
		case <-s.gateRelease:
		case <-ctx.Done():
			return GateOutcome{}, ctx.Err()
		}
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func drainOwned(t *testing.T, l *Loop, result *LoopResult) {
	t.Helper()
	for len(l.active) > 0 {
		select {
		case c := <-l.completions:
			if stop, err := l.takeCompletion(c, result); stop != "" || err != nil {
				t.Fatalf("completion %s / %v", stop, err)
			}
		case <-time.After(hangGuard):
			t.Fatalf("owned transition did not finish%s", ownedTransitionTimeoutDiagnostic())
		}
	}
}

func TestTheGateReclaimsItsLaneUntilItAnswers(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &persistentSteps{store: f.storePath, gated: make(chan struct{}), gateRelease: make(chan struct{})}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]'`)
		return err
	})
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %v / %v", ok, err)
	}
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: plan.decision.RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
	})
	var result LoopResult
	finished := make(chan laneOutcome, 1)
	go func() {
		stop, err := l.pass(context.Background(), &result)
		finished <- laneOutcome{stop: stop, err: err}
	}()
	select {
	case <-steps.gated:
	case <-time.After(hangGuard):
		close(steps.gateRelease)
		t.Fatal("gate did not start")
	}
	other := f.loop(t, &recordingSteps{}, &itemContentStager{})
	other.cfg.Policy.Backends = l.cfg.Policy.Backends
	other.cfg.Lanes = l.cfg.Lanes
	plans, leases, err := other.passPlans(context.Background(), other.cfg.Now())
	if err != nil {
		close(steps.gateRelease)
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].LaneID != "lane-1" || leases[0].LeasedUntil != nil {
		t.Errorf("gate lost its lease: %+v", leases)
	}
	for _, p := range plans {
		if p.decision.Action != ActionHold {
			t.Errorf("another pass offered gate's lane: %+v", p.decision)
		}
	}
	close(steps.gateRelease)
	outcome := <-finished
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("judge %s / %v", outcome.stop, outcome.err)
	}
	drainOwned(t, l, &result)
	plans = mustPlan(t, other)
	if plans[1].decision.Action != ActionDispatch || plans[1].decision.LaneID != "lane-1" {
		t.Fatalf("lane not offered after gate: %+v", plans[1].decision)
	}
}

func TestAReplacementLoopHonorsTheStoredBoundaryRefusal(t *testing.T) {
	f, l := passFixture(t, "item-a")
	plan := mustPlan(t, l)[0]
	ok, _, err := l.applyPlan(context.Background(), plan, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %v / %v", ok, err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: plan.decision.RunRequest.RunID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeRun, "tick", runRefs([]Decision{plan.decision}), []string{l.lanePath("lane-1")}, []Violation{{Kind: "ref", Where: "refs/heads/rogue", Detail: "appeared"}}, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	replacement := f.loop(t, &recordingSteps{}, &itemContentStager{})
	var result LoopResult
	stop, err := replacement.pass(context.Background(), &result)
	if err != nil || stop != StopQueueDrained || result.Candidates != 0 || mustPlan(t, replacement)[0].decision.Reason != StopHostBoundaryViolation {
		t.Fatalf("recovery ignored boundary refusal: %+v / %s / %v", result, stop, err)
	}
}

func TestAReplacementLoopSettlesTheStoredOutcomeWithoutRedispatch(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.pollOnly = true // Model losing the live handoff but retaining the worker's finish.
	steps := &persistentSteps{store: f.storePath, started: make(chan struct{}), release: make(chan struct{})}
	l.cfg.Steps = steps
	l.cfg.Lanes = []string{"lane-1"}
	// Keep the candidate inside the fixture's fence and supply the base artifact
	// a production stager pins. Its candidate must survive the first Loop's disposal.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]'`)
		return err
	})
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), runGit(t, f.root, "rev-parse", TrunkRef)+"\n")
	plan := mustPlan(t, l)[0]
	plan.decision.RunRequest.WorkingDir = lane
	ok, _, err := l.applyPlan(context.Background(), plan, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %v / %v", ok, err)
	}
	var initial LoopResult
	l.launchRun(context.Background(), plan.decision, l.cfg.Now(), nil, &initial)
	<-steps.started
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate")
	close(steps.release)
	<-l.completions
	// No settlement by the original Loop: a second instance has only store facts.
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = []string{"lane-1"}
	replacement.cfg.Policy.Backends = l.cfg.Policy.Backends
	var result LoopResult
	stop, err := replacement.pass(context.Background(), &result)
	if err != nil || stop != "" {
		t.Fatalf("replacement pass: %s / %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	if result.Dispatched != 0 || result.Candidates != 1 || result.Landed != 1 {
		t.Fatalf("replacement did not settle and judge exactly once: %+v", result)
	}
	plans := mustPlan(t, replacement)
	if plans[0].decision.Action == ActionDispatch {
		t.Fatal("replacement dispatched the item twice")
	}
}

// Called only at the existing timeout, before its assertion. This is diagnostic
// evidence of the actual blocked stage, not a longer completion deadline.
func ownedTransitionTimeoutDiagnostic() string {
	if os.Getenv("TILLR_SQLITE_TRACE") != "1" {
		return ""
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	diagnostic := "\nblocked-stage goroutine snapshot:\n" + string(buf[:n])
	if n == len(buf) {
		diagnostic += "\nstack snapshot truncated"
	}
	return diagnostic
}
