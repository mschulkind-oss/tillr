package drain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// transitionPlan calls Plan directly and decodes its emitted payload, so the assertions
// cover both the derived value and the JSON a decision reader receives.
func transitionPlan(t *testing.T, storePath, now string, lanes []string, capacity int, policy *drain.Policy) ([]drain.Decision, []map[string]json.RawMessage) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var decisions []drain.Decision
	var payloads []map[string]json.RawMessage
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		records, err := drain.Plan(r, drain.Pass{
			Now: now, AvailableLanes: &lanes, MaxDispatches: &capacity,
		}, policy, nil)
		if err != nil {
			return err
		}
		for _, record := range records {
			var decision drain.Decision
			if err := json.Unmarshal(record.Payload, &decision); err != nil {
				return err
			}
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(record.Payload, &payload); err != nil {
				return err
			}
			decisions = append(decisions, decision)
			payloads = append(payloads, payload)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return decisions, payloads
}

func transitionPolicy() *drain.Policy {
	return &drain.Policy{
		DefaultBackend: "first",
		Backends: map[string]drain.Backend{
			"first":  {Adapter: "/bin/true", Model: "first-model"},
			"second": {Adapter: "/bin/true", Model: "second-model"},
		},
	}
}

// An unchanged store answers the same transition at two different instants. Dispatch,
// retry, failover, exhausted history, resource holds and terminal items all take part.
// Decision and run identities, as well as the transition, ignore the clock.
func TestPlanTransitionIdentityDoesNotReadTheClock(t *testing.T) {
	const retryRun = "run-retry-first"
	storePath := fixture(t,
		itemRecord("item_dispatch", "in-queue"),
		itemRecord("item_exhausted", "in-queue"),
		itemRecord("item_failover", "in-queue"),
		itemRecord("item_landed", "landed"),
		itemRecord("item_legacy", "in-queue"),
		itemRecord("item_not_started", "not-started"),
		itemRecord("item_resource", "in-queue"),
		itemRecord("item_retry", "in-queue"),
		runRecord("run-exhausted-first", "item_exhausted", "lane-8", "first", run.StateFailedToStart, nil, ""),
		runRecord("run-exhausted-first-f1", "item_exhausted", "lane-9", "second", run.StateExited, exitCode(2), ""),
		runRecord("run-failover-first", "item_failover", "lane-8", "first", run.StateFailedToStart, nil, ""),
		runRecord(retryRun, "item_retry", "lane-9", "first", run.StateFailedToStart, nil, ""),
	)
	legacyItemState(t, storePath, "item_legacy", "in-progress")
	seedRetry(t, storePath, "item_retry", retryRun, "the owner", "the harness could not start", "2026-11-09T01:00:00Z")

	lanes := []string{"lane-1", "lane-2", "lane-3"}
	at := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	first, _ := transitionPlan(t, storePath, at.Format(time.RFC3339), lanes, 2, transitionPolicy())
	second, _ := transitionPlan(t, storePath, at.Add(time.Hour).Format(time.RFC3339), lanes, 2, transitionPolicy())
	if len(first) != 8 || len(second) != len(first) {
		t.Fatalf("planned %d and %d decisions, want eight each", len(first), len(second))
	}
	for i, a := range first {
		b := second[i]
		if a.ItemID != b.ItemID || a.TransitionID == "" || a.TransitionID != b.TransitionID {
			t.Errorf("item %s changed transition: %q -> %q", a.ItemID, a.TransitionID, b.TransitionID)
		}
		if a.DecisionID != b.DecisionID {
			t.Errorf("item %s changed decision id %q -> %q", a.ItemID, a.DecisionID, b.DecisionID)
		}
		if a.RunRequest != nil {
			want := fmt.Sprintf("run-%s-a%d", a.ItemID, a.Attempt)
			if a.RunRequest.RunID != want || b.RunRequest == nil || b.RunRequest.RunID != want {
				t.Errorf("run requests = %+v / %+v, want %q", a.RunRequest, b.RunRequest, want)
			}
			wantDecision := fmt.Sprintf("decision-%s-a%d-queued-running", a.ItemID, a.Attempt)
			if a.DecisionID != wantDecision {
				t.Errorf("decision id = %q, want %q", a.DecisionID, wantDecision)
			}
		}
	}
}

// Every decision carries its attempt and source, while only dispatches and retries
// carry a destination. Each terminal run counts, even when the recorded backend
// is still closed and the next attempt is held rather than dispatched.
func TestPlanTransitionPayloadCountsTerminalRuns(t *testing.T) {
	const retryRun = "run-retry-first"
	storePath := fixture(t,
		itemRecord("item_dispatch", "in-queue"),
		itemRecord("item_exhausted", "in-queue"),
		itemRecord("item_failover", "in-queue"),
		itemRecord("item_hold", "blocked"),
		itemRecord("item_landed", "landed"),
		itemRecord("item_retry", "in-queue"),
		runRecord("run-exhausted-first", "item_exhausted", "lane-8", "first", run.StateFailedToStart, nil, ""),
		runRecord("run-exhausted-first-f1", "item_exhausted", "lane-9", "second", run.StateExited, exitCode(2), ""),
		runRecord("run-failover-first", "item_failover", "lane-8", "first", run.StateFailedToStart, nil, ""),
		runRecord(retryRun, "item_retry", "lane-9", "first", run.StateFailedToStart, nil, ""),
		// A run that ended normally on the second backend leaves it open.
		runRecord("run-landed-second", "item_landed", "lane-7", "second", run.StateExited, exitCode(0), ""),
	)
	seedRetry(t, storePath, "item_retry", retryRun, "the owner", "the harness could not start", "2026-11-09T01:00:00Z")
	policy := transitionPolicy()
	// Fresh work can dispatch while the default backend is closed only if the
	// policy explicitly assigns it to another backend.
	policy.Overrides = map[string]string{"item_dispatch": "second"}
	decisions, payloads := transitionPlan(t, storePath, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2", "lane-3", "lane-4"}, 4, policy)
	want := map[string]struct {
		action, from, to string
		attempt          int
	}{
		"item_dispatch":  {drain.ActionDispatch, "queued", "running", 1},
		"item_exhausted": {drain.ActionDispatch, "queued", "running", 3},
		"item_failover":  {drain.ActionHold, "held", "", 2},
		"item_hold":      {drain.ActionHold, "held", "", 1},
		"item_landed":    {drain.ActionHold, "landed", "", 2},
		"item_retry":     {drain.ActionRetry, "queued", "running", 2},
	}
	if len(decisions) != len(want) {
		t.Fatalf("planned %d decisions, want %d", len(decisions), len(want))
	}
	identities := map[string]bool{}
	for i, got := range decisions {
		w, ok := want[got.ItemID]
		if !ok {
			t.Fatalf("unexpected item %q", got.ItemID)
		}
		if got.Action != w.action || got.From != w.from || got.To != w.to || got.Attempt != w.attempt {
			t.Errorf("item %s: action/from/to/attempt = %s/%s/%s/%d, want %s/%s/%s/%d",
				got.ItemID, got.Action, got.From, got.To, got.Attempt, w.action, w.from, w.to, w.attempt)
		}
		if got.ItemID == "item_failover" && (got.Reason != drain.ReasonBackendClosed || got.DeferredBy != "first" || got.RunRequest != nil) {
			t.Errorf("failed run did not hold on its recorded backend: %+v", got)
		}
		if got.TransitionID == "" || identities[got.TransitionID] {
			t.Errorf("item %s has an empty or duplicate transition id %q", got.ItemID, got.TransitionID)
		}
		identities[got.TransitionID] = true
		for _, field := range []string{"transition_id", "attempt", "from"} {
			if _, ok := payloads[i][field]; !ok {
				t.Errorf("item %s payload omits %s", got.ItemID, field)
			}
		}
		_, hasTo := payloads[i]["to"]
		if hasTo != (w.to != "") {
			t.Errorf("item %s payload destination present = %t, want %t", got.ItemID, hasTo, w.to != "")
		}
	}
}
