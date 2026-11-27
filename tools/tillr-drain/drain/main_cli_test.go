package drain_test

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

// The positive decision: a queued item, a free lane and a policy that names a command
// produce a dispatch carrying a complete run.request — every field run exec reads.
func TestPlanDispatchesAQueuedItemOntoAnAvailableLane(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	if len(records) != 1 {
		t.Fatalf("the tick produced %d decisions, want 1", len(records))
	}
	got := records[0].Payload
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonReady {
		t.Fatalf("the decision is %q / %q, want dispatch / ready", got.Action, got.Reason)
	}
	if got.ItemID != "item_042" || got.LaneID != "item_042" {
		t.Fatalf("the decision names item %q on lane %q", got.ItemID, got.LaneID)
	}
	if got.DecisionID != "decision-item_042-a1-queued-running" {
		t.Fatalf("the decision id is %q", got.DecisionID)
	}
	request := got.RunRequest
	if request == nil {
		t.Fatalf("a dispatch carries no run_request")
	}
	if request.RunID != "run-item_042-a1" || request.ItemID != "item_042" || request.LaneID != "item_042" {
		t.Fatalf("the request names run %q / item %q / lane %q", request.RunID, request.ItemID, request.LaneID)
	}
	// The command is the policy's backend composed for the adapter: the program, the
	// backend it must be told to be, the model id staging supplies, the effort, and the
	// wall the policy names.
	wantCommand := []string{"/bin/true", "--backend", "fixture", "--model", "fixture-model", "--effort", "low", "--timeout", "60"}
	if strings.Join(request.Command, " ") != strings.Join(wantCommand, " ") {
		t.Fatalf("the request's command is %v, want %v", request.Command, wantCommand)
	}
	if request.WorkingDir != drain.WorktreeDir || request.TimeoutMS != 60000 || request.EnvRefs == nil {
		t.Fatalf("the request's working_dir %q / timeout_ms %d / env_refs %v are not the defaults",
			request.WorkingDir, request.TimeoutMS, request.EnvRefs)
	}
	if request.Backend != "fixture" {
		t.Fatalf("the request names backend %q, want the policy's", request.Backend)
	}
}

// An edge is read, not assumed: the same item holds while its blocker is unfinished and
// dispatches once the blocker lands.
func TestPlanHoldsAnItemBlockedByAnUnlandedItem(t *testing.T) {
	// This tests item edges, not the invoking checkout's proved-trunk pointer.
	// A private repository with no pointer exercises the bootstrap case equally
	// in a lane and a fresh clone; lane-base tests cover proof requirements.
	root := fixtureRepo(t)
	git(t, root, "init", "-b", "main")
	git(t, root, "commit", "--allow-empty", "-qm", "base")
	policyPath := policy(t, "/bin/true")
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)

	t.Run("blocker not started", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_007", "not-started"),
			itemRecord("item_042", "in-queue", "item_007"),
		)
		got := decisionFor(t, planFrom(t, storePath, tickPath, root, indexStub(t)), "item_042")
		if got.Action != drain.ActionHold || got.Reason != drain.ReasonBlocked {
			t.Fatalf("the decision is %q / %q, want hold / blocked", got.Action, got.Reason)
		}
		if got.RunRequest != nil {
			t.Fatalf("a hold carries a run_request")
		}
	})

	t.Run("blocker landed", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_007", "landed"),
			itemRecord("item_042", "in-queue", "item_007"),
		)
		// A landed state is not proof that a shared repository's green pointer
		// contains this fixture's blocker. Supply its landing and proof in an
		// isolated repository instead of borrowing the live checkout's refs.
		root := fixtureRepo(t)
		git(t, root, "init", "-q", "-b", "main")
		git(t, root, "commit", "--allow-empty", "-qm", "proved blocker landing")
		landed := git(t, root, "rev-parse", "HEAD")
		git(t, root, "update-ref", drain.GreenRef, landed)
		s, err := store.Open(context.Background(), storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES('fixture-landing','item_007','fixture-run','fixture-verdict','landed','fixture-base',?,'fixture-before',?,1)`, landed, landed)
			return err
		})
		closeErr := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		got := decisionFor(t, planFrom(t, storePath, tickPath, root, indexStub(t)), "item_042")
		if got.Action != drain.ActionDispatch {
			t.Fatalf("the decision is %q / %q, want dispatch", got.Action, got.Reason)
		}
	})
}

// An item whose own state says blocked holds even with no edge, and an item that is not
// queued is not dispatched: the drain does not choose work the board has not queued.
func TestPlanHoldsAnItemWhoseStateIsNotQueued(t *testing.T) {
	policyPath := policy(t, "/bin/true")
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath)

	for _, state := range []string{"not-started", "landed"} {
		t.Run(state, func(t *testing.T) {
			storePath := fixture(t, itemRecord("item_042", state))
			got := plan(t, storePath, tickPath)[0].Payload
			if got.Action != drain.ActionHold || got.Reason != drain.ReasonNotReady {
				t.Fatalf("the decision is %q / %q, want hold / not-ready", got.Action, got.Reason)
			}
		})
	}

	t.Run("blocked state", func(t *testing.T) {
		storePath := fixture(t, itemRecord("item_042", "blocked"))
		got := plan(t, storePath, tickPath)[0].Payload
		if got.Action != drain.ActionHold || got.Reason != drain.ReasonBlocked {
			t.Fatalf("the decision is %q / %q, want hold / blocked", got.Action, got.Reason)
		}
	})
}

// An item that has run before may be a retry, and nobody authorized the second attempt, so
// it holds with `run-link-missing` rather than being planned as a first dispatch — the
// conservative half of the retry rule. The authorization is what turns the hold into the
// retry TestPlanRetriesARunTheHarnessRefused pins; without one, the hold stands.
func TestPlanHoldsAnItemThatMayBeARetry(t *testing.T) {
	// The item has a terminal run: it was dispatched and run before. The run is the
	// evidence `runs.item_id` carries (D-UT19), not a state the item had to be left in.
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-1", "fixture", run.StateExited, exitCode(0), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || !reasonIs(got.Reason, drain.ReasonRunLinkMissing) {
		t.Fatalf("the decision is %q / %q, want hold / run-link-missing", got.Action, got.Reason)
	}
	if got.RunRequest != nil {
		t.Fatalf("a hold carries a run_request")
	}
}

// A previously run item is planned by the outcome of its latest run, not with a generic
// `not-ready`. A fence refusal, a run that produced no commit and a red gate go back to the
// worker with their evidence (`returned-with-evidence`, the owner's ruling of 2026-11-17);
// a wall that cut a run short is held with its own reason. A run the store cannot classify
// falls back to `run-link-missing`, which is the conservative answer.
func TestAPreviouslyRunItemHoldsWithItsOwnOutcomeReason(t *testing.T) {
	const runID = "run-item_042-20261109T000000Z"
	cases := []struct {
		name  string
		state string
		exit  *int
		seed  func(t *testing.T, storePath string)
		want  string
	}{
		{
			name: "fence refused", state: run.StateExited, exit: exitCode(0),
			seed: func(t *testing.T, storePath string) { seedRefusal(t, storePath, runID, drain.RefusalFence) },
			want: drain.ReasonReturned,
		},
		{
			name: "no commit", state: run.StateExited, exit: exitCode(0),
			seed: func(t *testing.T, storePath string) { seedRefusal(t, storePath, runID, drain.RefusalNoOp) },
			want: drain.ReasonReturned,
		},
		{
			name: "timed out", state: run.StateTimedOut, exit: nil,
			want: drain.ReasonRunUnjudged,
		},
		{
			name: "gate failed", state: run.StateExited, exit: exitCode(0),
			seed: func(t *testing.T, storePath string) { seedGateVerdict(t, storePath, runID) },
			want: drain.ReasonReturned,
		},
		{
			name: "unclassifiable answer", state: run.StateExited, exit: exitCode(0),
			want: drain.ReasonRunLinkMissing,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storePath := fixture(t,
				itemRecord("item_042", "in-queue"),
				runRecord(runID, "item_042", "lane-1", "fixture", c.state, c.exit, ""),
			)
			if c.seed != nil {
				c.seed(t, storePath)
			}
			tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
			got := plan(t, storePath, tickPath)[0].Payload
			action := drain.ActionHold
			if c.want == drain.ReasonReturned {
				action = drain.ActionDispatch
			}
			if got.Action != action || !reasonIs(got.Reason, c.want) {
				t.Fatalf("the decision is %q / %q, want %s / %q", got.Action, got.Reason, action, c.want)
			}
			if got.Reason == drain.ReasonNotReady {
				t.Fatalf("a previously run item was held as %s", drain.ReasonNotReady)
			}
		})
	}
}

// The vocabulary pin: `retry` is never emitted without an operator's authorization. The
// fixture holds one item that ran before and one that never did, so a face that guessed
// would have something to guess at — and the guess would be a retry planned as a first
// dispatch, which is the one answer the conservative rule must not produce.
func TestPlanNeverEmitsAnUnauthorizedRetry(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-1", "fixture", run.StateExited, exitCode(0), ""),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	for _, record := range records {
		if record.Payload.Action == drain.ActionRetry {
			t.Fatalf("the tick emitted an unauthorized retry for %q", record.Payload.ItemID)
		}
	}
	if got := decisionFor(t, records, "item_042"); !reasonIs(got.Reason, drain.ReasonRunLinkMissing) {
		t.Fatalf("the item that ran before is %q / %q, want hold / run-link-missing", got.Action, got.Reason)
	}
	if got := decisionFor(t, records, "item_043"); got.Action != drain.ActionDispatch {
		t.Fatalf("the item that never ran is %q / %q, want dispatch", got.Action, got.Reason)
	}
}

// An item whose history is not in the log at all is unknown, not never-run: the absence of
// evidence is not evidence that the item never ran, so it holds. The seed path always
// appends an item event, so the row is written through the substrate's write transaction —
// this is the one way to construct an item the log says nothing about.
func TestPlanHoldsAnItemWhoseHistoryIsUnknown(t *testing.T) {
	storePath := fixture(t)
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)`,
			"item_042", "an item the log says nothing about", "in-queue", 0,
		)
		return err
	}); err != nil {
		s.Close()
		t.Fatalf("writing the item row failed: %v", err)
	}
	s.Close()

	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || !reasonIs(got.Reason, drain.ReasonRunLinkMissing) {
		t.Fatalf("the decision is %q / %q, want hold / run-link-missing", got.Action, got.Reason)
	}
}

// A tick that names no policy can dispatch nothing, and it says so rather than emitting a
// run.request no runner would accept.
func TestPlanHoldsWhenTheTickNamesNoPolicy(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, "")

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonNoPolicy {
		t.Fatalf("the decision is %q / %q, want hold / no-policy", got.Action, got.Reason)
	}
}

// Capacity is the tick's own bound: the first item dispatches and the second holds, in the
// queue's order, so the same tick always spends the budget the same way.
func TestPlanHoldsWhenCapacityIsExhausted(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 1, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	if len(records) != 2 {
		t.Fatalf("the tick produced %d decisions, want 2", len(records))
	}
	if got := records[0].Payload; got.Action != drain.ActionDispatch || got.LaneID != "item_042" {
		t.Fatalf("the first decision is %q on lane %q, want dispatch on item_042", got.Action, got.LaneID)
	}
	if got := records[1].Payload; got.Action != drain.ActionHold || got.Reason != drain.ReasonNoLane {
		t.Fatalf("the second decision is %q / %q, want hold / capacity", got.Action, got.Reason)
	}
}

// A tick that offers no lane dispatches nothing, and a lane it did not offer is never
// assigned.
func TestPlanHoldsWhenNoLaneIsAvailable(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonNoLane {
		t.Fatalf("the decision is %q / %q, want hold / no-lane", got.Action, got.Reason)
	}
	if got.LaneID != "" {
		t.Fatalf("a hold names lane %q", got.LaneID)
	}
}

// Two items classified to deliver one fork's patch branch cannot run at once,
// even with disjoint fences. The recorded delivery names the branch they share.
func TestPlanHoldsASecondItemDeliveringTheSameFork(t *testing.T) {
	storePath := fixture(t, itemRecord("item_041", "in-queue"), itemRecord("item_042", "in-queue"))
	recordForkDelivery(t, storePath, "item_041", "item_042")
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	if got := decisionFor(t, records, "item_041"); got.Action != drain.ActionDispatch {
		t.Fatalf("item_041 is %q / %q, want dispatch", got.Action, got.Reason)
	}
	got := decisionFor(t, records, "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonForkHeld || got.DeferredBy != "item_041" {
		t.Fatalf("item_042 is %q / %q behind %q, want hold / %s behind item_041",
			got.Action, got.Reason, got.DeferredBy, drain.ReasonForkHeld)
	}
	if got.RunRequest != nil {
		t.Fatalf("a fork-held item carries a run_request")
	}
}

// Two ordinary items with disjoint fences run together, because each has its own worktree and
// its own land lock. A tick that serialized them would be the batch barrier back as a plan.
func TestPlanDispatchesTwoItemsWithDisjointFences(t *testing.T) {
	storePath := fixture(t,
		itemRecordFenced("item_041", "in-queue", "", []string{"tools/tillr-view/"}),
		itemRecordFenced("item_042", "in-queue", "", []string{"tools/tillr-drain/"}),
	)
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	for _, id := range []string{"item_041", "item_042"} {
		if got := decisionFor(t, records, id); got.Action != drain.ActionDispatch {
			t.Fatalf("%s is %q / %q, want dispatch", id, got.Action, got.Reason)
		}
	}
}

// An item that names no fence is dispatched: items no longer carry allow-lists (owner ruling 2026-11-19).
func TestPlanDispatchesAnItemThatNamesNoFence(t *testing.T) {
	storePath := fixture(t, itemRecordFenced("item_041", "in-queue", "", nil))
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_041")
	if got.Action != drain.ActionDispatch {
		t.Fatalf("the decision is %q / %q, want dispatch", got.Action, got.Reason)
	}
	if got.RunRequest == nil {
		t.Fatalf("a dispatched item lacks a run_request")
	}
}

// An item whose stored allowed_paths contains "." is still dispatched: existing items' lists are ignored.
func TestPlanDispatchesAnItemWithRootAllowedPaths(t *testing.T) {
	storePath := fixture(t, itemRecord("item_041", "in-queue"))
	setFence(t, storePath, "item_041", ".")
	tickPath := tick(t, "2026-11-14T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := decisionFor(t, plan(t, storePath, tickPath), "item_041")
	if got.Action != drain.ActionDispatch {
		t.Fatalf("the decision is %q / %q, want dispatch", got.Action, got.Reason)
	}
}

// The store's lane state is read: a lane a live run holds is skipped, and the next
// offered lane is taken. The lease expires after the tick's now, so this is the
// unexpired case: `plan` writes nothing, so nothing reaps it here.
func TestPlanDoesNotOfferALaneTheStoreHoldsLeased(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	seedLease(t, storePath, "lane-1", "run-1", time.Date(2026, 11, 10, 0, 10, 0, 0, time.UTC).UnixMilli())
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.LaneID != "item_042" {
		t.Fatalf("the decision is %q on lane %q, want dispatch on item_042", got.Action, got.LaneID)
	}
}

// A lease at or before the tick's now has expired: the lane is a lane the loop may
// reuse, and `plan` offers it. `plan` writes nothing, so this is the pure half of the
// same rule `once` reaps by, and the boundary is the lease's own instant — "at or
// before", not "before".
func TestPlanTreatsAnExpiredLeaseAsAvailable(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	seedLease(t, storePath, "lane-1", "run-1", now.UnixMilli())
	tickPath := tick(t, now.Format(time.RFC3339), []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.LaneID != "item_042" {
		t.Fatalf("the decision is %q on lane %q, want dispatch on item_042 after expired lease", got.Action, got.LaneID)
	}
}

// The face's whole claim: it writes nothing. The store's event head and table counts are
// the same before and after, and the store is opened read-only so a write is impossible
// rather than merely absent.
func TestPlanWritesNothingToTheStore(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	before := describe(t, storePath)
	plan(t, storePath, tickPath)
	after := describe(t, storePath)

	if before.EventHeadSeq != after.EventHeadSeq {
		t.Fatalf("the event head moved from %d to %d", before.EventHeadSeq, after.EventHeadSeq)
	}
	if len(before.TableCounts) != len(after.TableCounts) {
		t.Fatalf("the table set changed: %v then %v", before.TableCounts, after.TableCounts)
	}
	for table, count := range before.TableCounts {
		if after.TableCounts[table] != count {
			t.Fatalf("table %q went from %d rows to %d", table, count, after.TableCounts[table])
		}
	}
}

// The decision order is the queue's order, which is the rank's: an item whose rank sorts
// before another's is decided first however the two ids compare. The fixture states ranks
// that reverse the id order, so a planner still answering in item_id order fails here —
// that is this case's negative control, and it is the behaviour the face had before rank.
//
// Ranks are strings over the capacity queue's sparse alphabet (`a`, `am`, `b`), compared as
// plain strings, so `item_042`'s `a` is ahead of `item_043`'s `am` and both are ahead of
// `item_041`'s `b`.
func TestPlanAnswersItemsInRankOrder(t *testing.T) {
	storePath := fixture(t,
		itemRecordRanked("item_041", "in-queue", "b"),
		itemRecordRanked("item_042", "in-queue", "a"),
		itemRecordRanked("item_043", "in-queue", "am"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2", "lane-3"}, 3, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	want := []string{"item_042", "item_043", "item_041"}
	if len(records) != len(want) {
		t.Fatalf("the tick produced %d decisions, want %d", len(records), len(want))
	}
	for i, id := range want {
		if records[i].Payload.ItemID != id {
			t.Fatalf("decision %d is about %q, want %q", i, records[i].Payload.ItemID, id)
		}
	}
}

// An unranked item sorts after every ranked one and keeps item_id order among the unranked,
// which is the half of the rule a store that has never been ranked relies on: two ticks over
// one store still produce comparable files, and the queue is the id order it always was.
//
// The negative control is the two ranked items' ids: they are the *highest* ids in the
// fixture, so a planner that read the column and then ignored it, or that sorted the whole
// store by id, would put them last instead of first.
func TestPlanAnswersUnrankedItemsAfterEveryRankedOneInItemIDOrder(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_040", "in-queue"),
		itemRecordRanked("item_042", "in-queue", "z"),
		itemRecord("item_041", "in-queue"),
		itemRecordRanked("item_043", "in-queue", "a"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z",
		[]string{"lane-1", "lane-2", "lane-3", "lane-4"}, 4, policy(t, "/bin/true"))

	records := plan(t, storePath, tickPath)
	want := []string{"item_043", "item_042", "item_040", "item_041"}
	if len(records) != len(want) {
		t.Fatalf("the tick produced %d decisions, want %d", len(records), len(want))
	}
	for i, id := range want {
		if records[i].Payload.ItemID != id {
			t.Fatalf("decision %d is about %q, want %q: the order is %v",
				i, records[i].Payload.ItemID, id, decisionIDs(records))
		}
	}
}

// The same store and the same tick produce the same bytes, which is what makes the face a
// capacity simulator rather than a report on a moment.
func TestPlanIsDeterministic(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 2, policy(t, "/bin/true"))

	first := filepath.Join(scratch(t, "first"), "decisions.json")
	second := filepath.Join(scratch(t, "second"), "decisions.json")
	for _, out := range []string{first, second} {
		if code, output := invoke(t, "plan", "--store", storePath, "--input", tickPath, "--output", out); code != 0 {
			t.Fatalf("plan exited %d: %s", code, output)
		}
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatalf("reading %s failed: %v", first, err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatalf("reading %s failed: %v", second, err)
	}
	if string(a) != string(b) {
		t.Fatalf("two runs of one tick differ:\n%s\n%s", a, b)
	}
	// The record time is injected, and identities name the attempt. Re-running the
	// same plan therefore answers the same bytes without reading the process clock.
	for _, record := range readDecisions(t, first) {
		if want := "decision-" + record.Payload.ItemID + "-a1-queued-running"; record.Payload.DecisionID != want {
			t.Fatalf("the decision id is %q, want %q", record.Payload.DecisionID, want)
		}
		if want := "run-" + record.Payload.ItemID + "-a1"; record.Payload.RunRequest.RunID != want {
			t.Fatalf("the run id is %q, want %q", record.Payload.RunRequest.RunID, want)
		}
	}
}

// Per-item selection: the override chooses the item's first backend and the default serves
// the rest, and each request carries the chosen name and a route drawn from that backend's
// own rotation — the record, the report header and the retry policy then read one datum.
func TestPlanSendsEachItemToTheBackendThePolicyNamesForIt(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1", "lane-2"}, 2,
		twoBackendPolicy(t, 1, map[string]string{"item_043": "second"}))

	records := plan(t, storePath, tickPath)
	defaulted := decisionFor(t, records, "item_042")
	overridden := decisionFor(t, records, "item_043")

	if defaulted.RunRequest == nil || defaulted.RunRequest.Backend != "first" {
		t.Fatalf("the item with no override was dispatched to %+v, want backend first", defaulted.RunRequest)
	}
	if overridden.RunRequest == nil || overridden.RunRequest.Backend != "second" {
		t.Fatalf("the overridden item was dispatched to %+v, want backend second", overridden.RunRequest)
	}
	if defaulted.Reason != drain.ReasonReady || overridden.Reason != drain.ReasonReady {
		t.Fatalf("the decisions are %q / %q, want ready / ready", defaulted.Reason, overridden.Reason)
	}
	// The route is one of the backend's own models, and it is in the request before the run
	// starts: the wire id collapses the rotation to one id, so this is the attribution.
	route := modelOf(t, defaulted.RunRequest)
	if route != "route-a" && route != "route-b" {
		t.Fatalf("the drawn route %q is not one of the backend's models", route)
	}
	if got := modelOf(t, overridden.RunRequest); got != "route-z" {
		t.Fatalf("the single-model backend drew %q, want its model", got)
	}
}

func TestPlanHoldsAnInfrastructureFailureOnTheFailedBackend(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "first", run.StateExited, exitCode(2), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.RunRequest != nil || got.DeferredBy != "first" || !strings.Contains(got.BackendHoldCause, failedRun) {
		t.Fatalf("want a measured hold on first, got %+v", got)
	}
}

// DRAIN-2B's negative direction, both halves. A run that answered — cleanly, or with a
// report footer that says the model failed — is judged work, and judged work never fails
// over: spending a second backend on it would pay twice to learn what the gate already
// said once.
func TestPlanDoesNotFailOverACandidateFailure(t *testing.T) {
	cases := map[string]struct {
		state string
		exit  *int
		log   string
	}{
		"the harness answered cleanly": {state: run.StateExited, exit: exitCode(0)},
		"the model reported a failed answer": {
			state: run.StateExited, exit: exitCode(1), log: "verification: go test ./... -> FAIL\n\nRESULT: failed\n",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			logPath := ""
			if c.log != "" {
				logPath = filepath.Join(scratch(t, "log"), "run.log")
				if err := os.WriteFile(logPath, []byte(c.log), 0o644); err != nil {
					t.Fatalf("writing the run log failed: %v", err)
				}
			}
			storePath := fixture(t,
				itemRecord("item_042", "in-queue"),
				runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "first", c.state, c.exit, logPath),
			)
			tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

			got := plan(t, storePath, tickPath)[0].Payload
			if got.Action != drain.ActionHold {
				t.Fatalf("the decision is %q on %+v, want a hold: judged work never fails over", got.Action, got.RunRequest)
			}
			if got.RunRequest != nil {
				t.Fatalf("a hold carries a run_request to %q", got.RunRequest.Backend)
			}
		})
	}
}

// Both backends have failed: no open backend exists until a recorded reopening.
func TestPlanHoldsWhenEveryBackendIsClosed(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-8", "first", run.StateFailedToStart, nil, ""),
		runRecord("run-item_042-20261109T010000Z", "item_042", "lane-9", "second", run.StateExited, exitCode(2), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 1, nil))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonBackendClosed)
	}
	if got.RunRequest != nil {
		t.Fatalf("closed backends still dispatched to %q", got.RunRequest.Backend)
	}
}

// A legacy zero budget does not change the recorded backend hold.
func TestPlanIgnoresLegacyZeroFailoverBudget(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-20261109T000000Z", "item_042", "lane-9", "first", run.StateFailedToStart, nil, ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, twoBackendPolicy(t, 0, nil))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonBackendClosed || got.DeferredBy != "first" || got.RunRequest != nil {
		t.Fatalf("legacy zero budget changed the hold: %+v", got)
	}
}

// A backend with no wall plans an unbounded request: timeout_ms 0 and no `--timeout` on the
// adapter's argv.
func TestPlanDispatchesAnUnboundedRequestWhenThePolicyNamesNoWall(t *testing.T) {
	storePath := fixture(t, itemRecord("item_042", "in-queue"))
	policyPath := policyDoc(t, map[string]any{
		"default_backend": "first",
		"preference":      []string{"first"},
		"backends": map[string]any{
			"first": map[string]any{"adapter": "/bin/true", "model": "route-a"},
		},
	})
	out := filepath.Join(scratch(t, "unbounded"), "decisions.json")
	code, output := invoke(t, "plan", "--store", storePath,
		"--input", tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policyPath), "--output", out)
	if code != 0 {
		t.Fatalf("plan exited %d: %s", code, output)
	}
	request := readDecisions(t, out)[0].Payload.RunRequest
	if request == nil {
		t.Fatalf("the decision carries no run_request")
	}
	if request.TimeoutMS != 0 {
		t.Fatalf("the unbounded request carries timeout_ms %d, want 0", request.TimeoutMS)
	}
	for _, element := range request.Command {
		if element == "--timeout" {
			t.Fatalf("the unbounded request passes a wall to the adapter: %v", request.Command)
		}
	}
}

// The item-authorized retry: a run the harness refused — the adapter's own preflight exit 2,
// the class a mandatory fence or a stale binary also leaves — is re-dispatched because a
// person authorized it, and the decision names the run it supersedes.
//
// This is A-NORETRY's closing test: the same fixture without the authorization holds with
// `run-link-missing`, and the only difference the retry turns on is the recorded claim.
func TestPlanRetriesARunTheHarnessRefused(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused before the work started", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionRetry || got.Reason != drain.ReasonRetry {
		t.Fatalf("the decision is %q / %q, want retry / %s", got.Action, got.Reason, drain.ReasonRetry)
	}
	if got.SupersedesRunID != failedRun {
		t.Fatalf("the decision supersedes %q, want %q", got.SupersedesRunID, failedRun)
	}
	if got.RetryID != "retry-item_042-"+failedRun {
		t.Fatalf("the decision names authorization %q", got.RetryID)
	}
	if got.RunRequest == nil {
		t.Fatalf("a retry carries no run_request")
	}
	if got.RunRequest.RunID == failedRun {
		t.Fatalf("the retry reused the failed run's id %q: a re-dispatch is a new run", failedRun)
	}
	if got.RunRequest.ItemID != "item_042" || got.RunRequest.LaneID != "item_042" {
		t.Fatalf("the retry request names item %q on lane %q", got.RunRequest.ItemID, got.RunRequest.LaneID)
	}
}

// The same fixture without an authorization is still a parking space by design: the link
// exists, but nobody asked for the second attempt, so the item holds. This is the control
// the retry test above is read against.
func TestPlanHoldsARefusedRunWithoutAnAuthorization(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold {
		t.Fatalf("the decision is %q, want a hold without an authorization", got.Action)
	}
	if got.SupersedesRunID != "" || got.RetryID != "" {
		t.Fatalf("a hold names the authorization it does not have: retry %q supersedes %q", got.RetryID, got.SupersedesRunID)
	}
}

// The distinction criterion 3 is about: the gate judged this candidate, so the claim is
// refused rather than guessed at. The run exited 0 — a fence refusal looks exactly like an
// answered run by exit status alone — and only the gate's own `gate.verdict` event tells
// the two apart.
func TestPlanHoldsARetryWhoseRunTheGateJudged(t *testing.T) {
	const judgedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(judgedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(0), ""),
	)
	seedGateVerdict(t, storePath, judgedRun)
	// The face refuses a judged run, so the row is seeded directly: this is the race the
	// plan's own check exists for, not a claim the CLI would have recorded.
	seedRetry(t, storePath, "item_042", judgedRun, "the owner", "a mistaken claim", "2026-11-10T00:00:00Z")
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonRetryJudged {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonRetryJudged)
	}
	if got.SupersedesRunID != judgedRun {
		t.Fatalf("the hold supersedes %q, want the run the claim names", got.SupersedesRunID)
	}
	if got.RunRequest != nil {
		t.Fatalf("a refused retry still carried a run_request to %q", got.RunRequest.RunID)
	}
}

// A run a wall cut short is not the harness refusing: it is unjudged work a person inspects,
// so the claim is refused with its own reason rather than re-dispatched.
func TestPlanHoldsARetryWhoseRunAWallCutShort(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedRetry(t, storePath, "item_042", cutShort, "the owner", "a mistaken claim", "2026-11-10T00:00:00Z")
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonRetryUnjudged {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonRetryUnjudged)
	}
}

// A run a wall cut short can also have committed, and a committed candidate is judged
// before its wall status (design D-RW2). Once the gate has spoken, the item goes back to its
// worker with the gate's evidence (`returned-with-evidence`), even though the run is also
// `AttemptUnjudged`: a judged candidate's next act is the gate's.
func TestPlanHoldsAWalledRunByTheGatesVerdict(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedGateVerdict(t, storePath, cutShort)
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonReturned {
		t.Fatalf("the decision is %q / %q, want dispatch / %s: a judged candidate's next act is the gate's",
			got.Action, got.Reason, drain.ReasonReturned)
	}
}

// ...and the acknowledgment such a run accepts is the gate's. Demanding `unjudged` as well
// would make every acknowledgment unsatisfiable, which is the deadlock the guard exists to
// prevent.
func TestPlanRetriesAWalledRunWithTheGatesAcknowledgment(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedGateVerdict(t, storePath, cutShort)
	if code, output := authorizeRetryWith(t, storePath, "item_042", cutShort,
		"the owner", "the gate failed", "2026-11-10T00:00:00Z", drain.RetryAckGateFailed); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action != drain.ActionRetry {
		t.Fatalf("the decision is %q / %q, want a retry naming the walled run", got.Action, got.Reason)
	}
	if got.SupersedesRunID != cutShort {
		t.Fatalf("the retry supersedes %q, want %q", got.SupersedesRunID, cutShort)
	}
}

// The negative control: the same judged-and-walled run does not accept the wall's own
// acknowledgment, because the gate's word is the one that governs it.
func TestRetryFaceRefusesTheWallsAcknowledgmentOnAJudgedRun(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)
	seedGateVerdict(t, storePath, cutShort)
	code, output := authorizeRetryWith(t, storePath, "item_042", cutShort,
		"the owner", "the gate failed", "2026-11-10T00:00:00Z", drain.RetryAckUnjudged)
	if code != 2 {
		t.Fatalf("retry exited %d for a judged run acknowledged as unjudged, want 2: %s", code, output)
	}
}

// An authorization naming a run that is no longer the item's latest terminal run cannot be
// the retry's link, and it no longer speaks: the item routes by its latest run, as with no
// authorization. It is not held as `retry-stale` (the owner's ruling of 2026-11-17).
func TestPlanIgnoresAStaleRetryAuthorization(t *testing.T) {
	const (
		older = "run-item_042-20261109T000000Z"
		newer = "run-item_042-20261109T010000Z"
	)
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(older, "item_042", "lane-8", "fixture", run.StateExited, exitCode(2), ""),
		runRecord(newer, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	seedRetry(t, storePath, "item_042", older, "the owner", "the older run", "2026-11-10T00:00:00Z")
	tickPath := tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := plan(t, storePath, tickPath)[0].Payload
	if got.Action == drain.ActionRetry || got.Reason == "retry-stale" || got.RetryID != "" || got.SupersedesRunID != "" {
		t.Fatalf("a stale authorization still spoke: %+v", got)
	}
}

// One authorization is one retry. Once the retry's own run is the item's latest terminal
// run, the authorization that named the previous run no longer applies — the retry run's row
// is what retires it — and the item holds until a person authorizes the new run.
func TestPlanRetriesOncePerAuthorization(t *testing.T) {
	const (
		failedRun = "run-item_042-20261109T000000Z"
		retryRun  = "run-item_042-20261109T010000Z"
	)
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	first := plan(t, storePath, tick(t, "2026-11-10T00:01:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true")))[0].Payload
	if first.Action != drain.ActionRetry {
		t.Fatalf("the first plan is %q / %q, want a retry", first.Action, first.Reason)
	}

	seedRunRecord(t, storePath, runRecord(retryRun, "item_042", "lane-1", "fixture", run.StateExited, exitCode(2), ""))
	second := plan(t, storePath, tick(t, "2026-11-10T00:02:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true")))[0].Payload
	if second.Action != drain.ActionHold {
		t.Fatalf("the second plan is %q / %q, want a hold: one authorization is one retry", second.Action, second.Reason)
	}
}

// The face's own write: the authorization is recorded with who asked, why and which run it
// supersedes, a `drain.retry` record is echoed, and a legacy `in-progress` item — a row
// written before the state was frozen — goes back in the queue.
func TestRetryFaceRecordsTheAuthorizationAndRequeuesTheItem(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	legacyItemState(t, storePath, "item_042", "in-progress")
	code, output := driveRecord(t, drainBin(t), "retry", "--store", storePath, "--item", "item_042", "--run", failedRun,
		"--by", "the owner", "--why", "the harness refused before the work started", "--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	var echoed struct {
		Kind    string      `json:"kind"`
		ID      string      `json:"id"`
		Payload drain.Retry `json:"payload"`
	}
	if err := json.Unmarshal(output, &echoed); err != nil {
		t.Fatalf("the face's output is not a record: %v\n%s", err, output)
	}
	if echoed.Kind != drain.RetryKind || echoed.ID != "retry-item_042-"+failedRun {
		t.Fatalf("the output is %q / %q, want %s / retry-item_042-%s", echoed.Kind, echoed.ID, drain.RetryKind, failedRun)
	}
	if echoed.Payload.RequestedBy != "the owner" || echoed.Payload.Reason != "the harness refused before the work started" {
		t.Fatalf("the authorization names %q / %q", echoed.Payload.RequestedBy, echoed.Payload.Reason)
	}

	rows := retryRows(t, storePath)
	if len(rows) != 1 {
		t.Fatalf("the store holds %d authorizations, want 1", len(rows))
	}
	if rows[0].ItemID != "item_042" || rows[0].SupersedesRunID != failedRun {
		t.Fatalf("the row names item %q / run %q", rows[0].ItemID, rows[0].SupersedesRunID)
	}
	if got := itemStateOf(t, storePath, "item_042"); got != "in-queue" {
		t.Fatalf("the failed item is %q after the retry, want in-queue", got)
	}
}

// The negative direction the face owns: a run the gate judged is refused at the face, so an
// operator learns immediately instead of leaving a claim the plan can never honour.
func TestRetryFaceRefusesAJudgedRun(t *testing.T) {
	const judgedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(judgedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(0), ""),
	)
	seedGateVerdict(t, storePath, judgedRun)

	code, output := authorizeRetry(t, storePath, "item_042", judgedRun,
		"the owner", "a mistaken claim", "2026-11-10T00:00:00Z")
	if code != 2 {
		t.Fatalf("retry exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "judged by the gate") {
		t.Fatalf("the refusal %q does not name the gate's judgment", output)
	}
	if rows := retryRows(t, storePath); len(rows) != 0 {
		t.Fatalf("a refused retry left %d authorization(s) in the store", len(rows))
	}
}

// A wall cut a run short, and that is not the harness refusing: the face refuses it by name
// so the operator inspects the tree instead of buying the same wall twice.
func TestRetryFaceRefusesAnUnjudgedRun(t *testing.T) {
	const cutShort = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(cutShort, "item_042", "lane-9", "fixture", run.StateTimedOut, nil, ""),
	)

	code, output := authorizeRetry(t, storePath, "item_042", cutShort,
		"the owner", "a mistake", "2026-11-10T00:00:00Z")
	if code != 2 {
		t.Fatalf("retry exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "cut short by a wall") {
		t.Fatalf("the refusal %q does not name the wall", output)
	}
	if rows := retryRows(t, storePath); len(rows) != 0 {
		t.Fatalf("a refused retry left %d authorization(s) in the store", len(rows))
	}
}

// The authorization survives: a second retry of the same run is refused rather than
// overwriting the first record, which is the defect the criterion names.
func TestRetryFaceRefusesADuplicateAuthorization(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"the owner", "the harness refused", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("the first retry exited %d: %s", code, output)
	}
	code, output := authorizeRetry(t, storePath, "item_042", failedRun,
		"someone else", "a second claim", "2026-11-10T00:05:00Z")
	if code != 2 {
		t.Fatalf("the duplicate retry exited %d, want 2: %s", code, output)
	}
	if !strings.Contains(string(output), "already authorized") {
		t.Fatalf("the refusal %q does not say the run is already authorized", output)
	}
	rows := retryRows(t, storePath)
	if len(rows) != 1 || rows[0].RequestedBy != "the owner" {
		t.Fatalf("the duplicate overwrote the first record: %+v", rows)
	}
}

// The structural refusals: an item the store does not hold, a run it does not hold, a run
// that belongs to another item, and a terminal item a retry must not re-open.
func TestRetryFaceRefusesWhatItCannotLink(t *testing.T) {
	const failedRun = "run-item_042-20261109T000000Z"
	cases := map[string]struct {
		item string
		run  string
		want string
	}{
		"the item is not in the store": {item: "item_999", run: failedRun, want: "no item"},
		"the run is not in the store":  {item: "item_042", run: "run-item_042-20260101T000000Z", want: "no run"},
		"the run is another item's":    {item: "item_043", run: failedRun, want: "belongs to item"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			storePath := fixture(t,
				itemRecord("item_042", "in-queue"),
				itemRecord("item_043", "in-queue"),
				runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
			)
			code, output := authorizeRetry(t, storePath, c.item, c.run,
				"the owner", "a reason", "2026-11-10T00:00:00Z")
			if code != 2 {
				t.Fatalf("retry exited %d, want 2: %s", code, output)
			}
			if !strings.Contains(string(output), c.want) {
				t.Fatalf("the refusal %q does not name %q", output, c.want)
			}
		})
	}

	t.Run("the item is terminal", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_042", "landed"),
			runRecord(failedRun, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
		)
		code, output := authorizeRetry(t, storePath, "item_042", failedRun,
			"the owner", "a reason", "2026-11-10T00:00:00Z")
		if code != 2 {
			t.Fatalf("retry exited %d, want 2: %s", code, output)
		}
		if !strings.Contains(string(output), "terminal item") {
			t.Fatalf("the refusal %q does not name the terminal item", output)
		}
	})
}

// On 2026-11-17 an operator facing eight owner holds could not learn from any retry
// refusal which spelling the face would accept. Every refusal names the exact command it
// would accept, or says that none exists and who moves (docs/design/landing-principles.md,
// section 13).
func TestEveryRetryRefusalNamesTheAcceptedCommandOrTheMover(t *testing.T) {
	const r = "run-item_042-20261109T000000Z"
	accepted := func(item, ack string) string {
		cmd := "tillr drain retry --item " + item + " --run " + r + " --by <who> --why <reason>"
		if ack != "" {
			cmd += " --acknowledge " + ack
		}
		return cmd
	}
	cases := map[string]struct {
		item, state, runState, ack string
		code                       *int
		judged                     bool
		want                       string
	}{
		"a judged run":                     {item: "item_042", state: "in-queue", runState: run.StateExited, code: exitCode(0), judged: true, want: accepted("item_042", drain.RetryAckGateFailed)},
		"a run a wall cut short":           {item: "item_042", state: "in-queue", runState: run.StateTimedOut, want: accepted("item_042", drain.RetryAckUnjudged)},
		"the wall's ack on a judged run":   {item: "item_042", state: "in-queue", runState: run.StateTimedOut, judged: true, ack: drain.RetryAckUnjudged, want: accepted("item_042", drain.RetryAckGateFailed)},
		"the gate's ack on a harness fail": {item: "item_042", state: "in-queue", runState: run.StateExited, code: exitCode(2), ack: drain.RetryAckGateFailed, want: accepted("item_042", "") + "\n"},
		"an unknown acknowledgment":        {item: "item_042", state: "in-queue", runState: run.StateTimedOut, ack: "maybe", want: accepted("item_042", drain.RetryAckUnjudged)},
		"another item's run":               {item: "item_043", state: "in-queue", runState: run.StateExited, code: exitCode(2), want: accepted("item_042", "")},
		"a paused run":                     {item: "item_042", state: "in-queue", runState: run.StatePaused, want: "No retry command is accepted: the loop moves"},
		"a terminal item":                  {item: "item_042", state: "landed", runState: run.StateExited, code: exitCode(2), want: "No retry command is accepted: the owner moves: tillr item reopen"},
		"an item the store does not hold":  {item: "item_999", state: "in-queue", runState: run.StateExited, code: exitCode(2), want: "No retry command is accepted: the owner moves: tillr item apply"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			state42 := "in-queue"
			if c.item == "item_042" {
				state42 = c.state
			}
			storePath := fixture(t,
				itemRecord("item_042", state42),
				itemRecord("item_043", "in-queue"),
				runRecord(r, "item_042", "lane-9", "fixture", c.runState, c.code, ""),
			)
			if c.judged {
				seedGateVerdict(t, storePath, r)
			}
			code, output := authorizeRetryWith(t, storePath, c.item, r, "the owner", "a reason", "2026-11-10T00:00:00Z", c.ack)
			if code != 2 {
				t.Fatalf("retry exited %d, want 2: %s", code, output)
			}
			if !strings.Contains(string(output), c.want) {
				t.Fatalf("the refusal does not name %q:\n%s", c.want, output)
			}
		})
	}

	t.Run("a run the store does not hold", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_042", "in-queue"),
			runRecord(r, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
		)
		code, output := authorizeRetry(t, storePath, "item_042", "run-item_042-20260101T000000Z", "the owner", "a reason", "2026-11-10T00:00:00Z")
		if code != 2 || !strings.Contains(string(output), "latest finished run is "+r) || !strings.Contains(string(output), accepted("item_042", "")) {
			t.Fatalf("the refusal does not name the item's latest finished run (exit %d):\n%s", code, output)
		}
	})

	t.Run("a duplicate authorization", func(t *testing.T) {
		storePath := fixture(t,
			itemRecord("item_042", "in-queue"),
			runRecord(r, "item_042", "lane-9", "fixture", run.StateExited, exitCode(2), ""),
		)
		if code, output := authorizeRetry(t, storePath, "item_042", r, "the owner", "a reason", "2026-11-10T00:00:00Z"); code != 0 {
			t.Fatalf("the first retry exited %d: %s", code, output)
		}
		code, output := authorizeRetry(t, storePath, "item_042", r, "the owner", "again", "2026-11-10T00:01:00Z")
		if code != 2 || !strings.Contains(string(output), "No retry command is accepted: the loop moves") {
			t.Fatalf("the duplicate refusal does not name the loop (exit %d):\n%s", code, output)
		}
	})
}

// The question hold, from the index: an item whose design document carries a question
// Vantage's index reports as unanswered holds with reason `question-open` and names the
// question, and the same item dispatches the moment the index reports the question settled.
// Nothing about the item changed between the two plans — the answer did — which is the whole
// point of reading the index rather than a copy of its state.
func TestPlanHoldsAnItemWhoseDesignDocumentCarriesAnOpenQuestion(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	storePath := fixture(t, itemRecordWithDoc("item_042", "in-queue", "docs/design/open.md"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	open := indexStub(t, map[string]any{
		"path": "docs/design/open.md",
		"questions": []map[string]any{
			{"id": "OQ-1", "state": "open", "title": "OQ-1: which way?", "line": 12},
		},
	})
	got := decisionFor(t, planFrom(t, storePath, tickPath, root, open), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonQuestionOpen {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonQuestionOpen)
	}
	if got.QuestionID != "OQ-1" {
		t.Fatalf("the decision names question %q, want OQ-1", got.QuestionID)
	}
	if got.RunRequest != nil {
		t.Fatalf("a question-open hold carries a run_request: %+v", got.RunRequest)
	}

	settled := indexStub(t, map[string]any{
		"path": "docs/design/open.md",
		"questions": []map[string]any{
			{"id": "OQ-1", "state": drain.QuestionAnswered, "title": "OQ-1: which way?", "line": 12},
		},
	})
	got = decisionFor(t, planFrom(t, storePath, tickPath, root, settled), "item_042")
	if got.Action != drain.ActionDispatch || got.Reason != drain.ReasonReady {
		t.Fatalf("the settled decision is %q / %q, want dispatch / %s", got.Action, got.Reason, drain.ReasonReady)
	}
	if got.QuestionID != "" {
		t.Fatalf("a dispatch names question %q: only a question-open hold names one", got.QuestionID)
	}
}

// An item whose design document is not in the tree holds as `design-doc-missing` — and it
// stays that answer even when the index answers, because the tree is what says the file is
// not there.
func TestPlanHoldsAnItemWhoseDesignDocumentIsGone(t *testing.T) {
	root := fixtureRepo(t)
	storePath := fixture(t, itemRecordWithDoc("item_042", "in-queue", "docs/design/gone.md"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	got := decisionFor(t, planFrom(t, storePath, tickPath, root, indexStub(t)), "item_042")
	if got.Action != drain.ActionHold || got.Reason != drain.ReasonDesignDocMissing {
		t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonDesignDocMissing)
	}
	if got.QuestionID != "" {
		t.Fatalf("a design-doc-missing hold names question %q: there is no document to read one from", got.QuestionID)
	}
}

// An index that cannot be read is `questions-unknown`, never "no questions": a tool that is
// absent, that fails, or that answers something which is not an index leaves the item held
// rather than dispatching it on a guess.
func TestPlanHoldsAnItemWhenTheQuestionIndexCannotBeRead(t *testing.T) {
	root := fixtureRepo(t, "docs/design/open.md")
	storePath := fixture(t, itemRecordWithDoc("item_042", "in-queue", "docs/design/open.md"))
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))

	cases := map[string]string{
		"the tool is not there":     "definitely-not-a-vantage-tool-7f3a index --format json",
		"the tool fails":            "false",
		"the tool answers no index": `echo {"index":{}}`,
		"the tool refuses the tree": `echo {"index":{"refused":true,"documents":[]}}`,
	}
	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			got := decisionFor(t, planFrom(t, storePath, tickPath, root, command), "item_042")
			if got.Action != drain.ActionHold || got.Reason != drain.ReasonQuestionsUnknown {
				t.Fatalf("the decision is %q / %q, want hold / %s", got.Action, got.Reason, drain.ReasonQuestionsUnknown)
			}
			if got.RunRequest != nil {
				t.Fatalf("a questions-unknown hold carries a run_request: %+v", got.RunRequest)
			}
		})
	}
}

// The unclassifiable fallthrough names the run it found and what that run's record says.
func TestRunLinkMissingNamesWhatWasFound(t *testing.T) {
	storePath := fixture(t,
		itemRecord("item_042", "in-queue"),
		runRecord("run-item_042-a1", "item_042", "lane-1", "fixture", run.StateExited, exitCode(0), ""),
	)
	tickPath := tick(t, "2026-11-10T00:00:00Z", []string{"lane-1"}, 1, policy(t, "/bin/true"))
	got := plan(t, storePath, tickPath)[0].Payload
	if !strings.HasPrefix(got.Reason, drain.ReasonRunLinkMissing+": ") || !strings.Contains(got.Reason, "run-item_042-a1") {
		t.Fatalf("the reason is %q, want run-link-missing naming run-item_042-a1", got.Reason)
	}
}
