package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// The planner's closed vocabularies this case reads, spelled here rather than imported: a
// test that imported `drain` would link the item and run materializers into this package's
// test binary, and the view's fixtures are deliberately independent of the write faces (a
// module that needs another's writers to test itself is not independently usable). A value
// these disagree with fails the case, which is the agreement it exists to pin.
const (
	planActionDispatch = "dispatch"
	planActionRetry    = "retry"

	planReasonBlocked             = "blocked"
	planReasonNotReady            = "not-ready"
	planReasonBoundaryScopeEscape = "boundary-scope-escape"
	planReasonGateFailed          = "gate-failed"
	planReasonRunUnjudged         = "run-unjudged"
)

// TestProjectItemStateReadsWorkingFromTheLeaseNotTheStore pins the first half of the item:
// a stored `in-progress` row is evidence the item was dispatched, not a claim that anything
// is running. Measured 2026-11-14: the board read "8 working" over zero live lanes because
// the projector mapped the stored state unconditionally.
//
// The projector's answer is the lease's, so the test drives every stored state a row can
// spell and asserts Working appears exactly once -- with a live run, and never without one.
func TestProjectItemStateReadsWorkingFromTheLeaseNotTheStore(t *testing.T) {
	raw := []string{"not-started", "in-progress", "blocked", "in-queue", "landed",
		"resolved-elsewhere", "closed", "completed", "abandoned", "deleted", ""}
	for _, state := range raw {
		item := storeItem{ItemID: "item", State: state}
		if got := ProjectItemState(item, nil, nil, nil, ""); got.State == "working" {
			t.Fatalf("stored state %q projected to working with no live run", state)
		}
	}
	// The other half of the claim: the lease is what makes it Working, so the retired
	// spelling reads as Working while a run holds the item.
	live := &storeRun{RunID: "run-1", ItemID: "item", State: liveRunState}
	legacy := storeItem{ItemID: "item", State: "in-progress"}
	if got := ProjectItemState(legacy, nil, live, nil, ""); got.State != "working" {
		t.Fatalf("an in-progress item with a live run projected to %q, want working", got.State)
	}
}

// dispatchFixture is one non-landing outcome and the store rows that record it.
type dispatchFixture struct {
	itemID string
	runID  string
	// state is the item's stored state after the outcome. A `RESULT: blocked` report moves
	// the item; the other three leave it in the queue.
	state string
	// runState and exitCode are the run's own record.
	runState string
	exitCode *int
	// refusal is the `drain.refusal` step the loop appended, or "" when it appended none.
	refusal string
	// gateResult is the gate's answer, or "" when the gate never judged the run.
	gateResult string
	// acknowledge is the outcome an operator must acknowledge to re-dispatch the run, or ""
	// when a bare retry is enough.
	acknowledge string
}

// nonLandingOutcomes are the four shapes a run can end in without landing, and they are the
// four the drain's own recovery case drives: a fence refusal before the gate, a `blocked`
// report, a gate failure and a wall cut short.
func nonLandingOutcomes() []dispatchFixture {
	zero := 0
	timeout := 124
	return []dispatchFixture{
		{itemID: "item_041", runID: "run_041", state: "in-queue", runState: "exited",
			exitCode: &zero, refusal: "fence"},
		{itemID: "item_042", runID: "run_042", state: "blocked", runState: "exited",
			exitCode: &zero},
		{itemID: "item_043", runID: "run_043", state: "in-queue", runState: "exited",
			exitCode: &zero, gateResult: "fail", acknowledge: "gate-failed"},
		{itemID: "item_044", runID: "run_044", state: "in-queue", runState: "timed-out",
			exitCode: &timeout, acknowledge: "unjudged"},
	}
}

// TestEveryNonLandingOutcomeRecoversAndAgreesWithView is the cross-module round trip the
// item asks for: it drives four fixture items through a fence refusal, a `RESULT: blocked`
// report, a gate failure and a wall cut short, then recovers each through the verb that fits
// it and asserts four things.
//
//   - No outcome holds with the retired `not-ready` dead end: each names its own reason.
//   - Each class re-dispatches as an authorized retry, with no `tillr-item state` surgery.
//   - `ProjectItemState` never emits "working" without a live run.
//   - `boardCountsOf(...).Dispatchable` is the exact count `drain.Plan` dispatches given a
//     lane for every item -- three before recovery (the control item nobody has run, and the
//     fence escape and the gate refusal returned to their worker) and five
//     after it (the four retries plus the control).
//
// The planner is the real one: the case builds the `tillr-drain` binary and runs its `plan`
// face against the same store the view reads, so a disagreement is the code's rather than a
// fixture's. The view is read through its own assembly (storeDomain.boardCounts), never
// through a number this test counted itself.
func TestEveryNonLandingOutcomeRecoversAndAgreesWithView(t *testing.T) {
	outcomes := nonLandingOutcomes()
	path := dispatchStore(t, outcomes)

	// Two of the four held, and the control item nothing has run. The fence escape and the
	// gate refusal go back to their worker with the evidence, uncounted (the owner's ruling
	// of 2026-11-17), so the planner dispatches them with the control, and the view must
	// say three.
	assertAgreesWithPlan(t, path, "after the four outcomes", map[string]string{
		"item_042": planReasonBlocked,
		"item_044": planReasonRunUnjudged,
		// The legacy row is held for its stored state, which is the one hold here that is
		// the retired spelling: it is not one of the four outcomes.
		"item_046": planReasonNotReady,
	}, 3)

	// The recovery: a `blocked` report moves the item back to the queue (its criteria were
	// never frozen), and every one of the four gets an operator's retry authorization naming
	// the outcome it recovers.
	recoverStore(t, path, outcomes)

	assertAgreesWithPlan(t, path, "after recovery", map[string]string{
		"item_046": planReasonNotReady,
	}, len(outcomes)+1)
	for _, outcome := range outcomes {
		next := decisionFor(t, path, outcome.itemID)
		if next.Action != planActionRetry {
			t.Fatalf("%s plans %q/%q after recovery, want a retry", outcome.itemID, next.Action, next.Reason)
		}
	}

	// The third claim, over the store the recovery left: no projection reaches Working
	// without a lease, whatever the stored state says.
	assertNoWorkingWithoutALease(t, path)
}

// plannedDecision is one drain.decision payload, narrowed to what this case asserts.
type plannedDecision struct {
	ItemID string `json:"item_id"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// assertAgreesWithPlan compares the view's own header count with the planner's own decision
// count over one store, and pins the holds it was given: want is item id to hold reason, and
// dispatched is how many items the plan must send.
func assertAgreesWithPlan(t *testing.T, path, when string, want map[string]string, dispatched int) {
	t.Helper()
	plan := planDecisions(t, path)
	viewCounts := viewBoardCounts(t, path)
	got := 0
	for _, decision := range plan {
		if decision.Action == planActionDispatch || decision.Action == planActionRetry {
			got++
			continue
		}
		reason, ok := want[decision.ItemID]
		if !ok {
			t.Fatalf("%s: %s is held %q, which the case does not expect", when, decision.ItemID, decision.Reason)
		}
		if decision.Reason != reason {
			t.Fatalf("%s: %s is held %q, want %q", when, decision.ItemID, decision.Reason, reason)
		}
		// The retired `not-ready` dead end is the answer an outcome must never get: a
		// non-landing run names its own reason. The legacy row is the exception that proves
		// it, because its stored state is the only thing there is to say about it.
		if decision.Reason == planReasonNotReady && reason != planReasonNotReady {
			t.Fatalf("%s: %s is held with the retired %q", when, decision.ItemID, planReasonNotReady)
		}
	}
	if got != dispatched {
		t.Fatalf("%s: the plan dispatched %d item(s), want %d", when, got, dispatched)
	}
	if viewCounts.Dispatchable != got {
		t.Fatalf("%s: the view says %d dispatchable, the planner dispatches %d: %+v",
			when, viewCounts.Dispatchable, got, viewCounts)
	}
}

// viewBoardCounts reads one store through the view's own assembly, which is the path both
// read faces use.
func viewBoardCounts(t *testing.T, path string) boardCounts {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer s.Close()
	var counts boardCounts
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		domain, err := readStoreDomain(r)
		if err != nil {
			return err
		}
		counts = domain.boardCounts()
		return nil
	})
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return counts
}

// planDecisions runs the real `tillr-drain plan` face over one store and answers its
// decisions. It is the planner, not a copy of it: the binary is built from the sibling
// module's own source every run, so the two faces cannot drift apart silently.
func planDecisions(t *testing.T, path string) []plannedDecision {
	t.Helper()
	bin := drainBinary(t)
	dir := plannerFixtureDir(t)
	policyPath := filepath.Join(dir, "policy.json")
	writeJSONFile(t, policyPath, map[string]any{
		"default_backend": "fixture",
		"preference":      []string{"fixture"},
		"max_failovers":   1,
		"backends": map[string]any{
			"fixture": map[string]any{
				"adapter":         "/bin/true",
				"model":           "fixture-model",
				"timeout_seconds": 60,
			},
		},
	})
	lanes := []string{"lane-1", "lane-2", "lane-3", "lane-4", "lane-5", "lane-6"}
	tickPath := filepath.Join(dir, "tick.json")
	writeJSONFile(t, tickPath, map[string]any{
		"record_version": 1,
		"kind":           "drain.pass",
		"id":             "tick-view-agreement",
		"producer":       map[string]string{"system": "tillr", "utility": "drain", "version": "0.1.0"},
		"created_at":     "2026-11-12T00:29:00Z",
		"payload": map[string]any{
			"now":             "2026-11-12T00:30:00Z",
			"available_lanes": lanes,
			"max_dispatches":  len(lanes),
			"policy_ref":      policyPath,
		},
	})
	gitRoot(t, dir)
	out := filepath.Join(dir, "decisions.json")
	cmd := exec.Command(bin, "plan", "--store", path, "--input", tickPath, "--output", out,
		"--root", dir, "--questions-cmd", "/bin/true")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tillr-drain plan failed: %v\n%s", err, raw)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the plan's decisions: %v", err)
	}
	var envelopes []struct {
		Payload plannedDecision `json:"payload"`
	}
	if err := json.Unmarshal(raw, &envelopes); err != nil {
		t.Fatalf("decoding the plan's decisions: %v", err)
	}
	plan := make([]plannedDecision, 0, len(envelopes))
	for _, envelope := range envelopes {
		plan = append(plan, envelope.Payload)
	}
	return plan
}

// drainBinary builds the sibling drain module once per test run and answers its path. The
// module is a sibling rather than a link in this package so the planner stays the planner:
// the view's test binary links no write face's materializer into itself.
func drainBinary(t *testing.T) string {
	t.Helper()
	if sharedDrainBin != "" {
		return sharedDrainBin
	}
	path := filepath.Join(t.TempDir(), "tillr-drain")
	cmd := exec.Command("go", "build", "-o", path, ".")
	cmd.Dir = filepath.Join("..", "tillr-drain")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building tillr-drain: %v\n%s", err, raw)
	}
	sharedDrainBin = path
	return path
}

// sharedDrainBin is the binary the first case built. It lives in one case's temp directory
// and is removed with it, so the variable is only reused while that directory exists.
var sharedDrainBin string

// decisionFor answers the planner's decision for one item.
func decisionFor(t *testing.T, path, itemID string) plannedDecision {
	t.Helper()
	for _, decision := range planDecisions(t, path) {
		if decision.ItemID == itemID {
			return decision
		}
	}
	t.Fatalf("the plan holds no decision for %s", itemID)
	return plannedDecision{}
}

// writeJSONFile writes one document a face reads. It is not writeJSON: that is the HTTP
// helper, and a test that shadowed it would be a second spelling of a response.
func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// assertNoWorkingWithoutALease projects every item the store holds with no active run and
// asserts none of them reads as Working.
func assertNoWorkingWithoutALease(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		domain, err := readStoreDomain(r)
		if err != nil {
			return err
		}
		waiting := map[string][]string{}
		for itemID, edges := range domain.edges {
			for _, edge := range edges {
				waiting[itemID] = append(waiting[itemID], edge.BlockedBy)
			}
		}
		for _, item := range domain.items {
			projected := ProjectItemState(item, nil, nil, waiting[item.ItemID], "")
			if projected.State == "working" {
				t.Fatalf("%s projects to working with no live run (stored state %q)", item.ItemID, item.State)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
}

// dispatchStore writes the fixture: the four outcomes, a control item nothing has run, and a
// legacy `in-progress` row whose execution state was never derived.
func dispatchStore(t *testing.T, outcomes []dispatchFixture) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dispatch.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating the fixture failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture failed: %v", err)
	}
	schema, err := os.ReadFile(filepath.Join("testdata", "serve-fixture.sql"))
	if err != nil {
		t.Fatalf("reading the fixture schema: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening the fixture to seed it: %v", err)
	}
	defer db.Close()
	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("seeding the fixture with %q: %v", stmt, err)
		}
	}
	exec(string(schema))
	// The planner's own reads name the queue's rank and the item's seat, and the store's
	// retry authorizations. The serve fixture predates both, and a plan that could not read
	// them could not decide against this store at all.
	exec(`ALTER TABLE items ADD COLUMN rank TEXT NOT NULL DEFAULT ''`)
	exec(`ALTER TABLE items ADD COLUMN seat TEXT NOT NULL DEFAULT ''`)
	exec(`CREATE TABLE retries (
	        retry_id          TEXT PRIMARY KEY,
	        item_id           TEXT NOT NULL,
	        supersedes_run_id TEXT NOT NULL,
	        requested_by      TEXT NOT NULL,
	        reason            TEXT NOT NULL,
	        requested_at      INTEGER NOT NULL
	      )`)

	// One fence per item, none overlapping: this case is about the planner's attempt rules,
	// not about two lanes contending for a directory.
	fence := func(i int) string {
		return `["tools/` + string(rune('a'+i)) + `/"]`
	}
	// Every item's state is in the log, as a real `tillr item apply` leaves it. An item the
	// log does not name reads to the planner as an unknown history rather than a first
	// attempt, and holds.
	seedItem := func(i int, itemID, state string) {
		exec(`INSERT INTO items (item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
		      VALUES (?, ?, ?, ?, '', ?, ?)`, itemID, "the item "+itemID, "items/"+itemID+".md", state, 1000+i, fence(i))
		exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
		      VALUES (?, 'item.record', 'item', ?, ?)`, 1000+i, itemID, `{"state":"`+state+`"}`)
	}
	for i, outcome := range outcomes {
		seedItem(i, outcome.itemID, outcome.state)
		exec(`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at,
		        artifact_refs, event_seq, usage_tokens, usage_cost_usd)
		      VALUES (?, ?, ?, 'fixture', ?, ?, ?, ?, '[]', 0, NULL, NULL)`,
			outcome.runID, outcome.itemID, "lane-"+outcome.itemID, outcome.runState,
			outcome.exitCode, 1000+i, 2000+i)
		if outcome.refusal != "" {
			exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			      VALUES (?, 'drain.refusal', 'run', ?, ?)`, 3000+i, outcome.runID,
				`{"step":"`+outcome.refusal+`"}`)
		}
		if outcome.gateResult != "" {
			exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			      VALUES (?, 'gate.verdict', 'run', ?, ?)`, 3000+i, outcome.runID,
				`{"result":"`+outcome.gateResult+`"}`)
		}
	}
	// The control item: queued, fenced, never run, so the plan has one dispatch to compare a
	// non-zero view count against.
	seedItem(len(outcomes), "item_045", "in-queue")
	// A legacy row: `in-progress` with nothing running, which is the shape that read as
	// "8 working" over zero live lanes. No run row, so the lease is the only thing that
	// could make it Working, and there is none.
	seedItem(len(outcomes)+1, "item_046", "in-progress")
	return path
}

// recoverStore applies the recovery to a store dispatchStore built: the blocked report's item
// back in the queue -- its criteria were never frozen, so the record of the repair is the
// item's own state rather than a state move -- and an operator's retry authorization for each
// of the four, naming the outcome it recovers.
func recoverStore(t *testing.T, path string, outcomes []dispatchFixture) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening the fixture to recover it: %v", err)
	}
	defer db.Close()
	exec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := db.Exec(stmt, args...); err != nil {
			t.Fatalf("recovering the fixture with %q: %v", stmt, err)
		}
	}
	for i, outcome := range outcomes {
		if outcome.state == "blocked" {
			exec(`UPDATE items SET state = 'in-queue' WHERE item_id = ?`, outcome.itemID)
			exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			      VALUES (?, 'item.record', 'item', ?, '{"state":"in-queue"}')`, 4000+i, outcome.itemID)
		}
		retryID := "retry-" + outcome.itemID + "-" + outcome.runID
		exec(`INSERT INTO retries (retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at)
		      VALUES (?, ?, ?, 'the owner', 'recovering a non-landing outcome', ?)`,
			retryID, outcome.itemID, outcome.runID, 5000+i)
		if outcome.acknowledge != "" {
			exec(`INSERT INTO events (ts, kind, object_type, object_id, payload)
			      VALUES (?, 'drain.retry.ack', 'retry', ?, ?)`, 6000+i, retryID,
				`{"acknowledge":"`+outcome.acknowledge+`"}`)
		}
	}
}

// TestARetryAcknowledgesTheGatesWordOverTheWalls is the header's half of design D-RW2: a
// run can now be both judged and `AttemptUnjudged`, and the gate's verdict governs which
// acknowledgment a retry may carry. Reading the unjudged branch unguarded would refuse both
// values, so the retry would never count as dispatchable and the view would disagree with
// the planner by one for every walled-but-committed candidate.
func TestARetryAcknowledgesTheGatesWordOverTheWalls(t *testing.T) {
	const runID = "run-item_042-20261109T000000Z"
	history := &dispatchHistory{
		ran:      map[string]bool{"item_042": true},
		known:    map[string]bool{"item_042": true},
		latest:   map[string]string{"item_042": runID},
		judged:   map[string]bool{runID: true},
		unjudged: map[string]bool{runID: true},
		retries: map[string]retryAuthorization{
			"item_042": {supersedes: runID, acknowledge: retryAckGateFailed},
		},
	}
	item := storeItem{ItemID: "item_042", State: "in-queue"}
	if !history.dispatchable(item, nil, nil, "") {
		t.Fatalf("a judged-and-walled run acknowledged %q reads not dispatchable", retryAckGateFailed)
	}
	// The wall's own acknowledgment is refused, because the gate's word is the one that
	// governs a judged run.
	history.retries["item_042"] = retryAuthorization{supersedes: runID, acknowledge: retryAckUnjudged}
	if history.dispatchable(item, nil, nil, "") {
		t.Fatalf("a judged-and-walled run accepted %q, want the gate's acknowledgment", retryAckUnjudged)
	}
	// The control: a walled run the gate never saw still takes the wall's acknowledgment,
	// so the guard did not simply stop reading `unjudged`.
	history.judged = map[string]bool{}
	if !history.dispatchable(item, nil, nil, "") {
		t.Fatalf("an unjudged-only walled run refused %q", retryAckUnjudged)
	}
}
