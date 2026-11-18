package main_test

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
	"github.com/mschulkind-oss/tillr/store"
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

// setSeat updates a fixture row directly. The item writer checks only the shape;
// the planner checks whether the seat is mapped by the active policy.
func setSeat(t *testing.T, storePath, itemID, seat string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat = ? WHERE item_id = ?`, seat, itemID)
		return err
	}); err != nil {
		t.Fatalf("writing seat %q onto %s failed: %v", seat, itemID, err)
	}
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

// runPlan plans a fixture tick without asserting success, so a case can read a refusal.
func runPlan(t *testing.T, storePath, tickPath string) (int, string) {
	t.Helper()
	out := filepath.Join(scratch(t, "out"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath, "--input", tickPath, "--output", out)
	return code, string(output)
}

// Criterion 3, first half: a seat the policy does not map refuses the whole plan, naming the
// seat and the seats the policy does map. It is never defaulted to the tick's backend, which
// would dispatch design work to the build model and record it as intended.
func TestPlanRefusesASeatThePolicyDoesNotMap(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "in-queue", "design"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1,
		seatPolicy(t, map[string]string{"build": "fixture", "review": "fixture"}, nil))

	code, output := runPlan(t, storePath, tickPath)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, output)
	}
	for _, want := range []string{`item "item_042"`, `seat "design"`, "does not map", "build, review", "never defaulted"} {
		if !strings.Contains(output, want) {
			t.Fatalf("the refusal %q does not name %q", output, want)
		}
	}
}

// An unmapped seat is refused at plan time, with the policy and its mapped seats named.
func TestPlanRefusesAnUnmappedSeat(t *testing.T) {
	storePath := fixture(t, itemRecordSeated("item_042", "in-queue", "build"))
	setSeat(t, storePath, "item_042", "qa")
	policyPath := seatPolicy(t, map[string]string{"design": "fixture", "build": "fixture", "review": "fixture", "scout": "fixture"}, nil)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)

	code, output := runPlan(t, storePath, tickPath)
	if code != 2 {
		t.Fatalf("plan exited %d, want 2: %s", code, output)
	}
	for _, want := range []string{`item "item_042"`, `seat "qa"`, "which the policy does not map", "build, design, review, scout", "seats are declared in " + policyPath} {
		if !strings.Contains(output, want) {
			t.Fatalf("the refusal %q does not name %q", output, want)
		}
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
	raw, err := os.ReadFile("../../config/tillr/backend-policy.json")
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

// Criterion 2: an unavailable seat backend is not silently degraded onto the policy's first
// preference. The design item's one attempt ran on pi-designer and failed to start, the
// failover walks the preference, and the decision names the seat that fell back and the
// backend that answered — so the model switch is on the record instead of being read as an
// ordinary failover.
func TestPlanRecordsASeatDegradedFailover(t *testing.T) {
	storePath := fixture(t,
		itemRecordSeated("item_042", "in-queue", "design"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "pi-designer", run.StateFailedToStart, nil, ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1,
		seatPolicy(t, map[string]string{"design": "pi-designer"}, nil))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionDispatch {
		t.Fatalf("the item was %q / %q, want a dispatch", got.Action, got.Reason)
	}
	if got.RunRequest == nil || got.RunRequest.Backend != "fixture" {
		t.Fatalf("the failover names %+v, want the policy's preference head fixture", got.RunRequest)
	}
	if !strings.Contains(got.Reason, drain.ReasonSeatDegraded) {
		t.Fatalf("the decision's reason %q does not name the degradation", got.Reason)
	}
	// The reason names both halves the criterion asks for: the seat that fell back and the
	// backend that actually ran it.
	for _, want := range []string{`"design"`, `"fixture"`} {
		if !strings.Contains(got.Reason, want) {
			t.Fatalf("the decision's reason %q does not name %s", got.Reason, want)
		}
	}
	if got.Seat != "design" {
		t.Fatalf("the decision records seat %q, want design", got.Seat)
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

// Criterion 3: the 02h incident, reproduced through the loop. A design item's seat backend
// refuses before it runs anything — exit 2 in 0s — and the one permitted failover must not
// reach the floor model unremarked. The report names the seat that fell back and the backend
// that answered, and the store shows both runs; without the naming this is exactly the
// unremarked floor run the incident left behind.
func TestASeatDegradedFailoverIsNamedInTheLoopReport(t *testing.T) {
	designer := stub(t, "designer-adapter", counting+`exit 2
`)
	floor := stub(t, "floor-adapter", counting+committingAdapterBody+`printf 'RESULT: done\n'`)

	f := newLoopFixture(t, floor)
	f.policy = &drain.Policy{
		DefaultBackend: "pi",
		Preference:     []string{"pi"},
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
	if result.Landed != 1 {
		t.Fatalf("the loop landed %d candidate(s), want 1: %s", result.Landed, result.Detail)
	}

	runs := runsForItem(t, f.store, "item_042")
	if len(runs) != 2 {
		t.Fatalf("the store holds %d run(s) for item_042, want 2: %+v", len(runs), runs)
	}
	if runs[0].Backend != "pi-designer" || runs[1].Backend != "pi" {
		t.Fatalf("the runs are %q then %q, want pi-designer then pi", runs[0].Backend, runs[1].Backend)
	}
	for _, want := range []string{drain.ReasonSeatDegraded, `"design"`, `"pi"`} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("the loop report does not name %q:\n%s", want, report.String())
		}
	}
}

// listedSeatPolicy writes the shape docs/design/tillr-claude-backend.md §4 ships: the
// design seat falls back through its own list and the build seat through another, while
// the preference names only the build backends. The first design backend carries harness
// arguments, so the staged argv can be read for them.
func listedSeatPolicy(t *testing.T) string {
	t.Helper()
	return policyDoc(t, map[string]any{
		"default_backend": "builder-a",
		"preference":      []string{"builder-a", "builder-b"},
		"max_failovers":   1,
		"seats": map[string]any{
			"design": []string{"designer-a", "designer-b"},
			"build":  []string{"builder-a", "builder-b"},
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

// A design item whose design backend failed in the infrastructure class fails over to the
// next backend on the design seat's own list, not to the preference's build backend, and
// the decision is an ordinary failover: a step along the seat's own list is not a seat
// degradation. Once the list is spent the item holds as exhausted rather than walking onto
// a build backend.
func TestPlanFailsAListedSeatOverAlongItsOwnList(t *testing.T) {
	storePath := fixture(t,
		itemRecordSeated("item_042", "in-queue", "design"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "designer-a", run.StateFailedToStart, nil, ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, listedSeatPolicy(t))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_042")
	if got.Action != drain.ActionDispatch || got.RunRequest == nil {
		t.Fatalf("the item was %q / %q, want a failover dispatch", got.Action, got.Reason)
	}
	if got.RunRequest.Backend != "designer-b" {
		t.Fatalf("the failover dispatched to %q, want designer-b from the design seat's list", got.RunRequest.Backend)
	}
	if got.Reason != drain.ReasonFailover {
		t.Fatalf("the failover's reason is %q, want %q and no seat degradation", got.Reason, drain.ReasonFailover)
	}
	for _, element := range got.RunRequest.Command {
		if element == "--" {
			t.Fatalf("designer-b names no harness arguments but its argv %q carries a separator", got.RunRequest.Command)
		}
	}

	// The list's last backend failed too: with a larger budget there is still no hop, so
	// the item holds rather than reaching the build seat's backends.
	spent := fixture(t,
		itemRecordSeated("item_042", "in-queue", "design"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "designer-a", run.StateFailedToStart, nil, ""),
		runRecord("run-item_042-20261109T010000Z", "item_042", "lane-9", "designer-b", run.StateFailedToStart, nil, ""),
	)
	policyPath := policyDoc(t, map[string]any{
		"default_backend": "builder-a",
		"preference":      []string{"builder-a", "builder-b"},
		"max_failovers":   5,
		"seats":           map[string]any{"design": []string{"designer-a", "designer-b"}},
		"backends": map[string]any{
			"builder-a":  map[string]any{"adapter": "/bin/true", "model": "model-x"},
			"builder-b":  map[string]any{"adapter": "/bin/true", "model": "model-y"},
			"designer-a": map[string]any{"adapter": "/bin/true", "model": "model-z"},
			"designer-b": map[string]any{"adapter": "/bin/true", "model": "model-w"},
		},
	})
	got = decisionFor(t, plan(t, spent, tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.DeferredBy != "designer-a, designer-b" {
		t.Fatalf("the item past its list's end was %q / %q waiting on %q, want a hold with %q on both designers", got.Action, got.Reason, got.DeferredBy, drain.ReasonBackendClosed)
	}
}

// The Claude-first policy is the one a drain on the work machine names with --policy to
// spend the subscription, so it must decode with its seat lists, pass the same Check the
// face runs, map every seat, and put each seat's first dispatch on the claude adapter with
// its pi counterpart next. The Bedrock-only policy beside it, whose seats are plain strings,
// must still decode and pass Check under the two-shape seat decoder.
func TestTheClaudePolicyLoadsWithItsSeatLists(t *testing.T) {
	var bedrock drain.Policy
	if raw, err := os.ReadFile("../../config/tillr/backend-policy.bedrock.json"); err != nil {
		t.Fatalf("reading the Bedrock policy failed: %v", err)
	} else if err := json.Unmarshal(raw, &bedrock); err != nil {
		t.Fatalf("decoding the Bedrock policy failed: %v", err)
	}
	if err := bedrock.Check(); err != nil {
		t.Fatalf("the Bedrock policy fails its own check: %v", err)
	}

	raw, err := os.ReadFile("../../config/tillr/backend-policy.claude.json")
	if err != nil {
		t.Fatalf("reading the Claude policy failed: %v", err)
	}
	var policy drain.Policy
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatalf("decoding the Claude policy failed: %v", err)
	}
	if err := policy.Check(); err != nil {
		t.Fatalf("the Claude policy fails its own check: %v", err)
	}
	want := map[string][]string{
		item.SeatDesign: {"claude-designer", "pi-designer"},
		item.SeatBuild:  {"claude", "pi"},
		item.SeatReview: {"claude", "pi"},
		item.SeatScout:  {"claude", "pi"},
	}
	for seat, list := range want {
		if got := strings.Join(policy.SeatLists[seat], ","); got != strings.Join(list, ",") {
			t.Fatalf("the Claude policy's %s seat is %q, want %q", seat, got, strings.Join(list, ","))
		}
		if policy.Seats[seat] != list[0] {
			t.Fatalf("the Claude policy's %s seat dispatches to %q, want %q", seat, policy.Seats[seat], list[0])
		}
	}
	for _, name := range []string{"claude", "claude-designer"} {
		if got := policy.Backends[name].Adapter; got != "tools/tillr-run/backends/claude/run-claude" {
			t.Fatalf("the Claude policy's %s row names adapter %q", name, got)
		}
	}
	if adapter := filepath.Join("..", "..", policy.Backends["claude"].Adapter); !isExecutable(t, adapter) {
		t.Fatalf("the claude adapter %s is not an executable file", adapter)
	}
}

// isExecutable answers whether path is a regular file with an execute bit.
func isExecutable(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
