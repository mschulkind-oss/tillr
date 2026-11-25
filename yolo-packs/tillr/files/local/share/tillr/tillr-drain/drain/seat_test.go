package drain_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
)

// seatPolicy writes a policy whose seat map is the caller's, with every backend running
// /bin/true and naming the model the caller gives it (or `model-<backend>`). The default
// backend is `fixture` and is deliberately not one of the seats, so a case can tell "the
// backend the seat points at answered" from "the tick's default answered".
//
// `models` may be nil; it exists so a case can change one backend's model and prove the
// change reached the request, which is the one-line model swap a seat is for.
func seatPolicy(t *testing.T, seats map[string]string, models map[string]string) string {
	t.Helper()
	backends := map[string]any{
		"fixture": map[string]any{"adapter": "/bin/true", "model": "model-fixture", "timeout_seconds": 60},
	}
	for _, backend := range seats {
		model, ok := models[backend]
		if !ok {
			model = "model-" + backend
		}
		backends[backend] = map[string]any{"adapter": "/bin/true", "model": model, "timeout_seconds": 60}
	}
	return policyDoc(t, map[string]any{
		"default_backend": "fixture",
		"preference":      []string{"fixture"},
		"max_failovers":   1,
		"seats":           seats,
		"backends":        backends,
	})
}

// Criterion 1: an item names a seat and never a model, and the policy's seat map reaches
// the backend that answers. The design seat reaches the designer backend, whose model the
// request's argv carries, and no item id appears in `overrides`.
func TestPlanDispatchesAnItemToTheBackendItsSeatMapsTo(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "in-queue", "design"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1,
		seatPolicy(t, map[string]string{"design": "pi-designer"}, nil))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionDispatch {
		t.Fatalf("the item was %q / %q, want a dispatch", got.Action, got.Reason)
	}
	if got.Seat != "design" {
		t.Fatalf("the decision records seat %q, want design", got.Seat)
	}
	if got.RunRequest == nil || got.RunRequest.Backend != "pi-designer" {
		t.Fatalf("the dispatch's backend is %v, want pi-designer from the design seat with no override", got.RunRequest)
	}
	if model := modelOf(t, got.RunRequest); model != "model-pi-designer" {
		t.Fatalf("the dispatch's model is %q, want the designer backend's model", model)
	}
}

// Criterion 2: swapping which model designs, or moving the design seat to another backend,
// is a one-line policy change that touches no item — proven on the same fixture store, where
// the item's record is never rewritten and each policy dispatches it to the model it names.
func TestSwappingTheDesignSeatIsOnePolicyLine(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "in-queue", "design"))
	seat := map[string]string{"design": "designer"}

	dispatchWith := func(policyPath string) decision {
		tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)
		return decisionFor(t, plan(t, storePath, tickPath), "item_042")
	}

	first := dispatchWith(seatPolicy(t, seat, map[string]string{"designer": "model-a"}))
	if got := modelOf(t, first.RunRequest); got != "model-a" {
		t.Fatalf("the first policy dispatched model %q, want model-a", got)
	}
	if first.RunRequest.Backend != "designer" {
		t.Fatalf("the first policy dispatched backend %q, want designer", first.RunRequest.Backend)
	}

	// The same seat, the same backend, one different model id: what an operator edits to
	// change which model designs.
	second := dispatchWith(seatPolicy(t, seat, map[string]string{"designer": "model-b"}))
	if got := modelOf(t, second.RunRequest); got != "model-b" {
		t.Fatalf("swapping one model line dispatched %q, want model-b", got)
	}
	if second.RunRequest.Backend != first.RunRequest.Backend {
		t.Fatalf("the model swap moved the backend from %q to %q", first.RunRequest.Backend, second.RunRequest.Backend)
	}

	// Moving the seat itself: one value in the map points the design seat at another
	// backend, and the same item reaches it with that backend's model.
	moved := dispatchWith(seatPolicy(t, map[string]string{"design": "other"}, map[string]string{"other": "model-c"}))
	if moved.RunRequest.Backend != "other" {
		t.Fatalf("the moved design seat dispatched backend %q, want other", moved.RunRequest.Backend)
	}
	if got := modelOf(t, moved.RunRequest); got != "model-c" {
		t.Fatalf("the moved design seat dispatched model %q, want model-c", got)
	}
	if moved.Seat != "design" {
		t.Fatalf("the moved design seat recorded seat %q, want design", moved.Seat)
	}
}

// An unmapped explicit seat holds only its own item: mapped work still settles, and
// editing the policy lets the same queued item dispatch without rewriting its seat.
func TestLoopHoldsAnUnmappedSeatWhileMappedWorkLandsAndPolicyRepairDispatches(t *testing.T) {
	builder := stub(t, "builder-adapter", committingAdapter)
	designer := stub(t, "designer-adapter", committingAdapter)
	floor := stub(t, "floor-adapter", failingAdapter)
	f := newLoopFixture(t, floor)
	policy := &drain.Policy{
		DefaultBackend: "fixture",
		Seats:          map[string]string{"build": "pi-builder"},
		Backends: map[string]drain.Backend{
			"fixture":     {Adapter: floor, Model: "floor-model", TimeoutSeconds: 60},
			"pi-builder":  {Adapter: builder, Model: "builder-model", TimeoutSeconds: 60},
			"pi-designer": {Adapter: designer, Model: "designer-model", TimeoutSeconds: 60},
		},
	}
	policyPath := filepath.Join(scratch(t, "seat-policy"), "policy.json")
	writePolicy := func() {
		t.Helper()
		raw, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("encoding the policy failed: %v", err)
		}
		writeFile(t, policyPath, string(raw))
	}
	writePolicy()
	var err error
	f.policy, err = drain.LoadPolicy(policyPath)
	if err != nil {
		t.Fatalf("loading the policy failed: %v", err)
	}
	// Put the unmapped item first so its hold must leave the lane for mapped work.
	fixtureAt(t, f.store,
		seatedFencedItemRecord("item_041", "in-queue", "design", "candidate.txt"),
		seatedFencedItemRecord("item_042", "in-queue", "build", "candidate.txt"),
	)

	var report bytes.Buffer
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)),
		passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("an unmapped seat must not stop the mixed queue: %v", err)
	}
	if result.Dispatched != 1 || result.Landed != 1 {
		t.Fatalf("want one mapped dispatch and landing, got %+v", result)
	}
	if got := itemStateOf(t, f.store, "item_042"); got != "landed" {
		t.Fatalf("the mapped item is %q, want landed", got)
	}
	mappedRuns := runsForItem(t, f.store, "item_042")
	if len(mappedRuns) != 1 || mappedRuns[0].Backend != "pi-builder" {
		t.Fatalf("want one mapped run on pi-builder, got %+v", mappedRuns)
	}
	if runs := runsForItem(t, f.store, "item_041"); len(runs) != 0 {
		t.Fatalf("the unmapped seat must never run on the default backend: %+v", runs)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the unmapped item is %q, want it still in-queue", got)
	}
	for _, want := range []string{"seat-unmapped: ", "item_041", "design", policyPath, "mover: operator"} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("the hold report does not name %q:\n%s", want, report.String())
		}
	}

	tickPath := tick(t, "2026-11-12T00:00:00Z", []string{"lane-1"}, 1, policyPath)
	held := decisionFor(t, plan(t, f.store, tickPath), "item_041")
	if held.Action != drain.ActionHold || held.RunRequest != nil || held.Seat != "design" || !strings.HasPrefix(held.Reason, "seat-unmapped: ") {
		t.Fatalf("want a seat-unmapped hold with no request, got %+v", held)
	}
	for _, want := range []string{"design", policyPath} {
		if !strings.Contains(held.Reason, want) {
			t.Fatalf("the hold reason %q does not name %q", held.Reason, want)
		}
	}

	// Repair only the same policy file. The item and store are not recreated.
	policy.Seats["design"] = "pi-designer"
	writePolicy()
	recovered := decisionFor(t, plan(t, f.store, tickPath), "item_041")
	if recovered.Action != drain.ActionDispatch || recovered.Seat != "design" || recovered.RunRequest == nil || recovered.RunRequest.Backend != "pi-designer" {
		t.Fatalf("want the repaired seat to dispatch on pi-designer, never fixture: %+v", recovered)
	}
	if model := modelOf(t, recovered.RunRequest); model != "designer-model" {
		t.Fatalf("the repaired seat dispatches model %q, want designer-model", model)
	}
}

func TestPlanHoldsAnUnmappedSeatOnABlockedItem(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "blocked", "tricky"))
	policyPath := seatPolicy(t, map[string]string{"build": "builder"}, nil)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)
	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionHold || got.RunRequest != nil || !strings.HasPrefix(got.Reason, "seat-unmapped: ") || got.Seat != "tricky" {
		t.Fatalf("want a seat-unmapped hold with no request, got %+v", got)
	}
}

// Criterion 4: the dispatch records which seat it was and which backend answered, in both
// places a reader can find it — the decision payload the plan emits, and the store's durable
// dispatch row — so a run's provenance explains the model without the policy that was live.
func TestTheDispatchRecordCarriesTheSeatAndTheBackendThatAnswered(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "in-queue", "design"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1,
		seatPolicy(t, map[string]string{"design": "pi-designer"}, nil))

	planned := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if planned.Seat != "design" || planned.RunRequest == nil || planned.RunRequest.Backend != "pi-designer" {
		t.Fatalf("the decision records seat %q and backend %v, want design and pi-designer",
			planned.Seat, planned.RunRequest)
	}

	sink := filepath.Join(scratch(t, "sink"), "requests.json")
	onceOK(t, storePath, tickPath, "record-sink", sink)

	rows := dispatchRows(t, storePath)
	if len(rows) != 1 {
		t.Fatalf("the store holds %d dispatch row(s), want 1: %v", len(rows), rows)
	}
	if rows[0].Seat != "design" {
		t.Fatalf("the dispatch row records seat %q, want design", rows[0].Seat)
	}
	if rows[0].RunID != planned.RunRequest.RunID {
		t.Fatalf("the dispatch row names run %q, want the request's %q", rows[0].RunID, planned.RunRequest.RunID)
	}

	// The request the runner was handed names the backend that answered; together with the
	// row's seat, that is the whole explanation of the model the run used.
	handed := readRequests(t, sink)
	if len(handed) != 1 {
		t.Fatalf("the runner was handed %d request(s), want 1", len(handed))
	}
	if handed[0].Payload.Backend != "pi-designer" {
		t.Fatalf("the handed request names backend %q, want pi-designer", handed[0].Payload.Backend)
	}
}

// The shipped policy is the one this machine actually dispatches on, so it must pass the
// same Check the face runs and map every seat the item model defines: a seat the policy
// leaves out is an item refused for the policy's gap rather than for a bad request, which
// is the failure the seat layer exists to make loud.
func TestTheShippedPolicyMapsEverySeat(t *testing.T) {
	raw, err := os.ReadFile("../../../config/tillr/backend-policy.json")
	if err != nil {
		t.Fatalf("reading the shipped policy failed: %v", err)
	}
	var policy drain.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatalf("decoding the shipped policy failed: %v", err)
	}
	if err := policy.Check(); err != nil {
		t.Fatalf("the shipped policy fails its own check: %v", err)
	}
	for _, seat := range []string{item.SeatDesign, item.SeatBuild, item.SeatReview, item.SeatScout} {
		if _, ok := policy.Seats[seat]; !ok {
			t.Fatalf("the shipped policy does not map seat %q: it maps %v", seat, policy.Seats)
		}
	}
}

func TestPlanHoldsASingleBackendSeatAfterFailure(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecordSeated("item_042", "in-queue", "design"),
		runRecord(failedRun, "item_042", "lane-9", "pi-designer", run.StateFailedToStart, nil, ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1,
		seatPolicy(t, map[string]string{"design": "pi-designer"}, nil))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.RunRequest != nil || got.DeferredBy != "pi-designer" {
		t.Fatalf("want hold on pi-designer with no request, got %+v", got)
	}
}

// seatedFencedItemRecord is fencedItemRecord with the seat the item asks for: the loop's
// cases need one criterion and a fence to stage a run at all, and a seat for the policy to
// route.
func seatedFencedItemRecord(itemID, state, seat string, paths ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"body_ref":      "briefs/" + itemID + ".md",
		"state":         state,
		"blocked_by":    []string{},
		"allowed_paths": paths,
		"seat":          seat,
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion of " + itemID, "state": ""},
		},
	})
}

func TestLoopHoldsASingleBackendSeatWithoutFailover(t *testing.T) {
	designer := stub(t, "designer-adapter", counting+`exit 2
`)
	floor := stub(t, "floor-adapter", counting+committingAdapterBody+`printf 'RESULT: done\n'`)

	f := newLoopFixture(t, floor)
	f.policy = &drain.Policy{
		DefaultBackend: "pi",
		Seats:          map[string]string{"design": "pi-designer"},
		Backends: map[string]drain.Backend{
			"pi":          {Adapter: floor, Model: "floor-model", TimeoutSeconds: 60},
			"pi-designer": {Adapter: designer, Model: "designer-model", TimeoutSeconds: 60},
		},
	}
	fixtureAt(t, f.store, seatedFencedItemRecord("item_042", "in-queue", "design", "candidate.txt"))

	var report bytes.Buffer
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)),
		passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 0 || result.Dispatched != 1 {
		t.Fatalf("want one dispatch and no landing: %+v", result)
	}
	runs := runsForItem(t, f.store, "item_042")
	if len(runs) != 1 || runs[0].Backend != "pi-designer" {
		t.Fatalf("want one run on pi-designer, got %+v", runs)
	}
	for _, want := range []string{drain.ReasonBackendClosed, "pi-designer", "infrastructure", "operator", runs[0].RunID} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("the loop report does not name %q:\n%s", want, report.String())
		}
	}
	if strings.Contains(report.String(), "seat-degraded") {
		t.Fatalf("unexpected degradation:\n%s", report.String())
	}
}

// listedSeatPolicy assigns shares to the design and build seats. The preview
// uses the first design backend's harness arguments.
func listedSeatPolicy(t *testing.T) string {
	t.Helper()
	return policyDoc(t, map[string]any{
		"default_backend": "builder-a",
		"seats": map[string]any{
			"design": []map[string]any{{"backend": "designer-a", "share": 1}, {"backend": "designer-b", "share": 1}},
			"build":  []map[string]any{{"backend": "builder-a", "share": 1}, {"backend": "builder-b", "share": 1}},
		},
		"backends": map[string]any{
			"builder-a": map[string]any{"adapter": "/bin/true", "model": "model-x"},
			"builder-b": map[string]any{"adapter": "/bin/true", "model": "model-y"},
			"designer-a": map[string]any{"adapter": "/bin/true", "model": "model-z",
				"harness_args": []string{"--tools", "Read,Edit,Bash", "--append-system-prompt", "two words"}},
			"designer-b": map[string]any{"adapter": "/bin/true", "model": "model-w"},
		},
	})
}

// A design item's first dispatch goes to the head of the design seat's list, and the
// backend row's harness arguments are the tail of the recorded run.request's argv, after a
// `--`, in their own order.
func TestPlanStagesTheSeatListsHeadWithItsHarnessArgs(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "in-queue", "design"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, listedSeatPolicy(t))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionDispatch || got.RunRequest == nil {
		t.Fatalf("the item was %q / %q, want a dispatch", got.Action, got.Reason)
	}
	if got.RunRequest.Backend != "designer-a" {
		t.Fatalf("the design item dispatched to %q, want the head of its seat's list", got.RunRequest.Backend)
	}
	command := got.RunRequest.Command
	tail := []string{"--", "--tools", "Read,Edit,Bash", "--append-system-prompt", "two words"}
	if len(command) < len(tail) || strings.Join(command[len(command)-len(tail):], "\x00") != strings.Join(tail, "\x00") {
		t.Fatalf("the recorded argv %q does not end with the row's harness arguments %q", command, tail)
	}
}

// A listed seat holds on the backend that actually failed, even if the list's
// other backend is open.
func TestPlanHoldsAListedSeatOnItsFailedBackend(t *testing.T) {
	storePath := fixture(t,
		itemRecordSeated("item_042", "in-queue", "design"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "designer-a", run.StateFailedToStart, nil, ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, listedSeatPolicy(t))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.DeferredBy != "designer-a" || got.RunRequest != nil {
		t.Fatalf("want hold on designer-a, got %+v", got)
	}

	// If the last recorded failure was on another backend, hold on that one.
	// The current seat mapping does not change where the earlier run holds.
	spent := fixture(t,
		itemRecordSeated("item_042", "in-queue", "design"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "designer-a", run.StateFailedToStart, nil, ""),
		runRecord("run-item_042-20261109T010000Z", "item_042", "lane-9", "designer-b", run.StateFailedToStart, nil, ""),
	)
	policyPath := policyDoc(t, map[string]any{
		"default_backend": "builder-a",
		"seats":           map[string]string{"design": "designer-a"},
		"backends": map[string]any{
			"builder-a":  map[string]any{"adapter": "/bin/true", "model": "model-x"},
			"builder-b":  map[string]any{"adapter": "/bin/true", "model": "model-y"},
			"designer-a": map[string]any{"adapter": "/bin/true", "model": "model-z"},
			"designer-b": map[string]any{"adapter": "/bin/true", "model": "model-w"},
		},
	})
	got = decisionFor(t, plan(t, spent, tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.DeferredBy != "designer-b" || got.RunRequest != nil {
		t.Fatalf("want hold on designer-b, got %+v", got)
	}
}
