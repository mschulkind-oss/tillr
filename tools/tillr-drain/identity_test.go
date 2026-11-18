package main_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// The same fixed clock drives both loop invocations and every run record. A no-op
// is not judged, so an operator can authorize another attempt without moving time.
func TestSameSecondRetryPreservesEachDispatch(t *testing.T) {
	for _, failover := range []bool{false, true} {
		t.Run(fmt.Sprintf("failover=%t", failover), func(t *testing.T) {
			f := newLoopFixture(t, stub(t, "adapter", noOpAdapter))
			if failover {
				f.policy = &drain.Policy{
					DefaultBackend: "first", Preference: []string{"first", "second"},
					Backends: map[string]drain.Backend{
						"first": {Adapter: stub(t, "first", `if [ ! -e "$ATTEMPT_MARKER" ]; then touch "$ATTEMPT_MARKER"; exit 2; fi
printf 'RESULT: done\n'
`), Model: "first-model"},
						"second": {Adapter: stub(t, "second", noOpAdapter), Model: "second-model"},
					},
				}
				t.Setenv("ATTEMPT_MARKER", filepath.Join(scratch(t, "marker"), "started"))
			}
			fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
			steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
			planned, _ := transitionPlan(t, f.store, steps.Now().Format("2006-01-02T15:04:05Z"), []string{"lane-1"}, 1, f.policy)
			d := planned[0]
			original := dispatchRow{DecisionID: d.DecisionID, ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Action: d.Action, Reason: d.Reason}
			first, err := f.newLoop(t, 1, steps).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !failover && !strings.Contains(first.Detail, drain.ReasonCandidateNoOp) {
				t.Fatalf("first stop: %+v", first)
			}
			before := runsForItem(t, f.store, "item_041")
			wantFirst := 1
			if failover {
				wantFirst = 2
			}
			if len(before) != wantFirst {
				t.Fatalf("first runs: %+v", before)
			}
			if code, out := authorizeRetry(t, f.store, "item_041", before[len(before)-1].RunID, "the owner", "retry the no-op", steps.Now().Format("2006-01-02T15:04:05Z")); code != 0 {
				t.Fatalf("retry exited %d: %s", code, out)
			}
			retryCtx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()
			_, retryErr := f.newLoop(t, 1, steps).Run(retryCtx)
			runs := runsForItem(t, f.store, "item_041")
			if len(runs) != wantFirst+1 {
				t.Fatalf("runs = %+v, want %d distinct attempts", runs, wantFirst+1)
			}
			seen := map[string]bool{}
			for i, rec := range runs {
				want := fmt.Sprintf("run-item_041-a%d", i+1)
				if rec.RunID != want || seen[rec.RunID] {
					t.Errorf("run %d = %q, want %q uniquely", i, rec.RunID, want)
				}
				seen[rec.RunID] = true
			}
			dispatches := 0
			preserved := false
			for _, row := range dispatchRows(t, f.store) {
				if row.Action == drain.ActionDispatch || row.Action == drain.ActionRetry {
					dispatches++
				}
				if row == original {
					preserved = true
				}
			}
			if dispatches != wantFirst+1 || !preserved {
				t.Fatalf("dispatches=%d original preserved=%t; rows=%+v", dispatches, preserved, dispatchRows(t, f.store))
			}
			if retryErr != nil {
				t.Fatal(retryErr)
			}
		})
	}
}

func TestHoldIdentityChangesWithReason(t *testing.T) {
	path := fixture(t, itemRecord("item_041", "in-queue"))
	first, _ := transitionPlan(t, path, "2026-11-10T00:00:00Z", nil, 1, transitionPolicy())
	second, _ := transitionPlan(t, path, "2026-11-10T00:00:00Z", nil, 0, nil)
	if first[0].Reason == second[0].Reason || first[0].DecisionID == second[0].DecisionID {
		t.Fatalf("different holds share identity: %+v / %+v", first[0], second[0])
	}
}

func TestOnceReusesAnUnchangedHoldAcrossClocks(t *testing.T) {
	path := fixture(t, itemRecord("item_041", "blocked"))
	sink := filepath.Join(scratch(t, "hold-sink"), "requests.json")
	for _, now := range []string{"2026-11-10T00:00:00Z", "2026-11-10T01:00:00Z"} {
		onceOK(t, path, tick(t, now, []string{"lane-1"}, 1, policy(t, "/bin/true")), "record-sink", sink)
	}
	if rows := dispatchRows(t, path); len(rows) != 1 {
		t.Fatalf("repeated hold wrote %d rows: %+v", len(rows), rows)
	}
	if requests := readRequests(t, sink); len(requests) != 0 {
		t.Fatalf("hold dispatched: %+v", requests)
	}
}

func TestOnceRefusesRecordedDispatchAndRetryAcrossClocks(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry=%t", retry), func(t *testing.T) {
			path := fixture(t, itemRecord("item_041", "in-queue"))
			if retry {
				fixtureAt(t, path, runRecord("old-run", "item_041", "lane-9", "fixture", "failed-to-start", nil, ""))
				seedRetry(t, path, "item_041", "old-run", "the owner", "retry", "2026-11-09T00:00:00Z")
			}
			sink := filepath.Join(scratch(t, "dispatch-sink"), "requests.json")
			onceOK(t, path, tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true")), "record-sink", sink)
			code, out := once(t, path, tick(t, "2026-11-10T01:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true")), "record-sink", sink)
			if code != 2 || !strings.Contains(string(out), "already recorded") {
				t.Fatalf("repeat exited %d: %s", code, out)
			}
			if rows := dispatchRows(t, path); len(rows) != 1 {
				t.Fatalf("repeat wrote rows: %+v", rows)
			}
		})
	}
}
