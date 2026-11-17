package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// dispatchRow is one `dispatches` row as a case reads it: the item→run link drain's
// `once` face writes.
type dispatchRow struct {
	DecisionID string
	ItemID     string
	LaneID     string
	RunID      string
	Action     string
	Reason     string
	DeferredBy string
	Seat       string
}

// dispatchRows answers the store's `dispatches` rows in item_id order, through the
// substrate's reader rather than by opening the database.
//
// Every item these cases seed is unranked, and unranked items are the ones plan decides in
// item_id order (its own case pins that), so the rows and plan's records are comparable in
// the order each source states.
func dispatchRows(t *testing.T, storePath string) []dispatchRow {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	rows := []dispatchRow{}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		got, err := r.Query(
			`SELECT decision_id, item_id, COALESCE(lane_id, ''), COALESCE(run_id, ''), action, reason,
			        COALESCE(deferred_by, ''), COALESCE(seat, '')
			   FROM dispatches ORDER BY item_id`)
		if err != nil {
			return err
		}
		defer got.Close()
		for got.Next() {
			var row dispatchRow
			if err := got.Scan(&row.DecisionID, &row.ItemID, &row.LaneID, &row.RunID, &row.Action, &row.Reason, &row.DeferredBy, &row.Seat); err != nil {
				return err
			}
			rows = append(rows, row)
		}
		return got.Err()
	}); err != nil {
		t.Fatalf("reading the dispatches rows failed: %v", err)
	}
	return rows
}

// requestOut is one element of the record sink's file: a `run.request` record.
type requestOut struct {
	Kind          string      `json:"kind"`
	ID            string      `json:"id"`
	CorrelationID string      `json:"correlation_id"`
	Payload       run.Request `json:"payload"`
}

// readRequests reads the record sink's file.
func readRequests(t *testing.T, path string) []requestOut {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the sink's file %s failed: %v", path, err)
	}
	var records []requestOut
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("decoding the sink's file %s failed: %v\n%s", path, err, raw)
	}
	for _, record := range records {
		if record.Kind != run.RequestKind {
			t.Fatalf("the sink holds a %q record, want %s: a runner is handed run requests", record.Kind, run.RequestKind)
		}
	}
	return records
}

// once drives the once face and answers its exit code.
func once(t *testing.T, storePath, tickPath, runner, sinkPath string) (int, []byte) {
	t.Helper()
	return invoke(t, "once", "--store", storePath, "--input", tickPath,
		"--runner", runner, "--runner-output", sinkPath)
}

// onceOK drives the once face and fails the case if it did not complete.
func onceOK(t *testing.T, storePath, tickPath, runner, sinkPath string) {
	t.Helper()
	if code, output := once(t, storePath, tickPath, runner, sinkPath); code != 0 {
		t.Fatalf("once exited %d: %s", code, output)
	}
}

// The face's first claim: the rows in `dispatches` are the decisions plan made, field for
// field and in the same order. A row that disagreed with the decision it records would
// make the store a second, silently different account of the tick.
func TestOnceRecordsTheDecisionsPlanMakes(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "blocked"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	planned := plan(t, storePath, tickPath)
	onceOK(t, storePath, tickPath, "record-sink", filepath.Join(scratch(t, "sink"), "requests.json"))
	rows := dispatchRows(t, storePath)

	if len(rows) != len(planned) {
		t.Fatalf("the store holds %d dispatches rows for %d decisions", len(rows), len(planned))
	}
	for i, record := range planned {
		want := record.Payload
		got := rows[i]
		if got.DecisionID != want.DecisionID || got.ItemID != want.ItemID ||
			got.Action != want.Action || got.Reason != want.Reason || got.LaneID != want.LaneID ||
			got.DeferredBy != want.DeferredBy {
			t.Fatalf("row %d is %+v, want the decision %+v", i, got, want)
		}
		runID := ""
		if want.RunRequest != nil {
			runID = want.RunRequest.RunID
		}
		if got.RunID != runID {
			t.Fatalf("row %d names run %q, want %q: the row is the item→run link", i, got.RunID, runID)
		}
	}

	// The tick is not replayable. Running the same tick again would dispatch an item
	// that a run is already on its way to, so the face refuses it and writes nothing.
	if code, output := once(t, storePath, tickPath, "record-sink", filepath.Join(scratch(t, "again"), "requests.json")); code != 2 {
		t.Fatalf("a replayed tick exited %d, want 2: %s", code, output)
	}
	if again := dispatchRows(t, storePath); len(again) != len(planned) {
		t.Fatalf("a replayed tick left %d rows, want %d", len(again), len(planned))
	}
}

// A fork deferral is recorded as the answer it was: the row names the item whose patch
// branch holds the delivery, so the loop's durable record answers "why wasn't this one run"
// without the terminal that printed it.
func TestOnceRecordsTheItemAForkDeferralWaitsBehind(t *testing.T) {
	storePath := fixture(t,
		itemRecordFenced("item_041", "in-queue", "", []string{".forks/lanes/lane-1/pi/"}),
		itemRecordFenced("item_042", "in-queue", "", []string{".forks/lanes/lane-2/pi/"}),
	)
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	onceOK(t, storePath, tickPath, "record-sink", filepath.Join(scratch(t, "sink"), "requests.json"))
	rows := dispatchRows(t, storePath)
	if len(rows) != 2 {
		t.Fatalf("the store holds %d rows, want 2: %+v", len(rows), rows)
	}
	// dispatchRows orders by item_id, so the deferred row is second.
	if rows[0].Action != drain.ActionDispatch || rows[0].DeferredBy != "" {
		t.Fatalf("the first row is %+v, want a dispatch with no deferred_by", rows[0])
	}
	if rows[1].Action != drain.ActionHold || rows[1].Reason != drain.ReasonForkHeld || rows[1].DeferredBy != "item_041" {
		t.Fatalf("the second row is %+v, want a %s hold behind item_041", rows[1], drain.ReasonForkHeld)
	}
}

// The runner is handed exactly the dispatch decisions' requests: one per dispatch, none
// for a hold, in decision order.
func TestOnceHandsOverExactlyTheDispatchRequests(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "blocked"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	planned := plan(t, storePath, tickPath)
	sink := filepath.Join(scratch(t, "sink"), "requests.json")
	onceOK(t, storePath, tickPath, "record-sink", sink)

	requests := readRequests(t, sink)
	dispatched := 0
	for _, record := range planned {
		if record.Payload.Action != drain.ActionDispatch {
			continue
		}
		if dispatched >= len(requests) {
			t.Fatalf("the sink holds %d requests for %d dispatch decisions", len(requests), dispatched+1)
		}
		if got, want := requests[dispatched].Payload, *record.Payload.RunRequest; got.RunID != want.RunID ||
			got.ItemID != want.ItemID || got.LaneID != want.LaneID || len(got.Command) != len(want.Command) {
			t.Fatalf("request %d is %+v, want %+v", dispatched, got, want)
		}
		dispatched++
	}
	if len(requests) != dispatched {
		t.Fatalf("the sink holds %d requests for %d dispatch decisions: a hold is not a dispatch", len(requests), dispatched)
	}
	if dispatched != 1 {
		t.Fatalf("the tick dispatched %d items, want 1", dispatched)
	}
}

// The invariant that keeps the decision face usable as a simulator: plan writes nothing.
// The store's content is unchanged and no dispatches row exists. Content, not the file's
// bytes: a commit stays in the write-ahead log, so the database file does not move when a
// face writes (store.Fingerprint).
func TestPlanWritesNothingNotEvenADispatchRow(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	before, err := store.Fingerprint(context.Background(), storePath)
	if err != nil {
		t.Fatalf("reading the store failed: %v", err)
	}
	plan(t, storePath, tickPath)
	after, err := store.Fingerprint(context.Background(), storePath)
	if err != nil {
		t.Fatalf("reading the store failed: %v", err)
	}
	if before != after {
		t.Fatalf("plan changed the store's content: fingerprint %s then %s", before, after)
	}
	if rows := dispatchRows(t, storePath); len(rows) != 0 {
		t.Fatalf("plan wrote %d dispatches rows", len(rows))
	}
}

// A runner that fails rolls the whole transaction back: a dispatches row for a request
// that was never handed over is a record asserting a dispatch that did not happen.
func TestAFailingRunnerRollsTheTransactionBack(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	// The sink's directory does not exist, so the record sink cannot write its file.
	unwritable := filepath.Join(scratch(t, "unwritable"), "absent", "requests.json")
	code, output := once(t, storePath, tickPath, "record-sink", unwritable)
	if code != 4 {
		t.Fatalf("once exited %d, want 4 for a runner that failed: %s", code, output)
	}
	if rows := dispatchRows(t, storePath); len(rows) != 0 {
		t.Fatalf("a failed runner left %d dispatches rows", len(rows))
	}
}

// An unknown runner is a usage error, and the message names the one runner that exists.
func TestOnceRefusesAnUnknownRunner(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	code, output := once(t, storePath, tickPath, "subprocess", filepath.Join(scratch(t, "sink"), "requests.json"))
	if code != 2 {
		t.Fatalf("once exited %d, want 2 for an unknown runner: %s", code, output)
	}
	if !strings.Contains(string(output), "record-sink") {
		t.Fatalf("the refusal does not name record-sink: %s", output)
	}
	if rows := dispatchRows(t, storePath); len(rows) != 0 {
		t.Fatalf("a refused runner left %d dispatches rows", len(rows))
	}
}

// laneRow is one lane as `run lanes` answers it, and the shape a case reads the lease in.
type laneRow struct {
	LaneID      string  `json:"lane_id"`
	State       string  `json:"state"`
	RunID       *string `json:"run_id"`
	LeasedUntil *string `json:"leased_until"`
}

// lanesIn answers the store's lanes through run's read face, inside one read
// transaction: a case reads lane state through the utility that owns it rather than by
// opening the database.
func lanesIn(t *testing.T, storePath string) []laneRow {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	var lanes []laneRow
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		env, err := run.LanesRecord(r)
		if err != nil {
			return err
		}
		var body struct {
			Lanes []laneRow `json:"lanes"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		lanes = body.Lanes
		return nil
	}); err != nil {
		t.Fatalf("reading the lanes failed: %v", err)
	}
	return lanes
}

// The reap is the tick's first move: a lane whose lease ran out is a lane the loop may
// reuse (design §5.5, D-UT17), so `once` frees it and dispatches onto it in the same
// call. Without the reap the planner would see a lane held by a run that is gone and
// hold the item with no-lane.
func TestOnceReapsAnExpiredLeaseBeforeItPlans(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	expired := time.Date(2026, 11, 9, 23, 0, 0, 0, time.UTC).UnixMilli()
	seedLease(t, storePath, "lane-1", "run-1", expired)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	sink := filepath.Join(scratch(t, "sink"), "requests.json")

	onceOK(t, storePath, tickPath, "record-sink", sink)
	requests := readRequests(t, sink)
	if len(requests) != 1 || requests[0].Payload.LaneID != "lane-1" {
		t.Fatalf("the tick handed over %d requests on the reaped lane, want one: %+v", len(requests), requests)
	}
	// The dispatch onto the lane it just freed is also the record that the freed lane is
	// the one the decision named: the two halves of the tick describe one moment.
	if rows := dispatchRows(t, storePath); len(rows) != 1 || rows[0].LaneID != "lane-1" {
		t.Fatalf("the dispatch row reads %+v, want the reaped lane-1", rows)
	}
	// The reaped lane holds no lease. It is offered again, so it is either free or claimed
	// by the request this tick handed over — never still held by the run that died.
	for _, lane := range lanesIn(t, storePath) {
		if lane.LaneID != "lane-1" {
			continue
		}
		if lane.State == run.LaneStateLeased && lane.RunID != nil && *lane.RunID == "run-1" {
			t.Fatalf("the lane is still held by the run whose lease expired: %+v", lane)
		}
	}
}

// The negative control: `once` does not take a live lane away from the run that holds it.
// Without this case a reap that freed every lane it was shown would pass the case above.
func TestOnceLeavesALiveLeaseHeldAndTheItemWithoutALane(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	live := time.Date(2026, 11, 10, 1, 0, 0, 0, time.UTC).UnixMilli()
	seedLease(t, storePath, "lane-1", "run-1", live)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	sink := filepath.Join(scratch(t, "sink"), "requests.json")

	onceOK(t, storePath, tickPath, "record-sink", sink)
	if requests := readRequests(t, sink); len(requests) != 0 {
		t.Fatalf("a tick dispatched %d requests into a live lane", len(requests))
	}
	rows := dispatchRows(t, storePath)
	if len(rows) != 1 || rows[0].Action != drain.ActionHold || rows[0].Reason != drain.ReasonNoLane {
		t.Fatalf("the decision reads %+v, want hold / no-lane", rows)
	}
	for _, lane := range lanesIn(t, storePath) {
		if lane.LaneID != "lane-1" {
			continue
		}
		if lane.State != run.LaneStateLeased || lane.RunID == nil || *lane.RunID != "run-1" || lane.LeasedUntil == nil {
			t.Fatalf("the live lane reads %+v, want it leased to run-1 with its expiry", lane)
		}
	}
}

// A retry is a dispatch: the authorization's request reaches the runner, and the row the
// tick records says `retry` with the reason the operator's claim was honoured. The face
// hands over exactly one request, and it is a new run rather than the one it supersedes.
func TestOnceHandsOverARetryRequest(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	sink := filepath.Join(scratch(t, "sink"), "requests.json")

	onceOK(t, storePath, tickPath, "record-sink", sink)

	requests := readRequests(t, sink)
	if len(requests) != 1 {
		t.Fatalf("the sink holds %d request(s), want the retry's one", len(requests))
	}
	if requests[0].Payload.ItemID != "item_042" || requests[0].Payload.RunID == failedRun {
		t.Fatalf("the retry request names item %q / run %q", requests[0].Payload.ItemID, requests[0].Payload.RunID)
	}
	rows := dispatchRows(t, storePath)
	if len(rows) != 1 {
		t.Fatalf("the store holds %d dispatch row(s), want 1", len(rows))
	}
	if rows[0].Action != drain.ActionRetry || rows[0].Reason != drain.ReasonRetry {
		t.Fatalf("the row is %q / %q, want retry / %s", rows[0].Action, rows[0].Reason, drain.ReasonRetry)
	}
	if rows[0].RunID != requests[0].Payload.RunID {
		t.Fatalf("the row names run %q, the sink was handed %q", rows[0].RunID, requests[0].Payload.RunID)
	}
}
