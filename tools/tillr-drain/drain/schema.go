// Package drain is the drain utility's contract: the two record kinds it speaks, the
// tick it consumes and the decisions it answers with. It is a package rather than part
// of main because a main package cannot be imported, and the conformance case that pins
// what drain registers has to run in a test process.
package drain

import (
	"fmt"

	// The blank imports are the ITEM-PKG precondition made structural. plan writes
	// nothing, but it reads items, lanes and runs, and a store drain opens must carry
	// those tables: the substrate composes its migration list from what was registered
	// at package load, so a store created in drain's process has item's and run's tables
	// only because their packages are linked here. The same imports register their
	// record→row mappings, which is what lets a fixture be seeded in drain's test
	// process. A face that remembered to import them would be a face that could forget.
	_ "github.com/mschulkind-oss/tillr-item/item"
	_ "github.com/mschulkind-oss/tillr-run/run"
)

// The record kinds drain speaks. None is registered as an event kind: plan asserts no state
// transition, and registering one as appendable would claim one; the loop's own result is a
// file beside the runs' records rather than an entry in the log. drain registers one DDL
// statement, `dispatches` (design §5.1, the item→run link), in dispatches.go: it is written
// by `once` and — one tick's decisions per transaction, from the loop's own plan — by `loop`.
const (
	// PassKind is the input record plan consumes.
	PassKind = "drain.pass"
	// DecisionKind is the record plan emits, one per considered item.
	DecisionKind = "drain.decision"
	// LoopResultKind is the record a loop's outcome is written as, at
	// <root>/.tillr/loop/drain-<stamp>.json (LoopResultFile). It is a file record: nothing
	// materializes it into the store, and it answers what the loop did and why it stopped.
	LoopResultKind = "drain.loop"
)

// Utility is the producer name every record this utility emits carries.
const Utility = "drain"

// Version is this utility's producer version. The version set is released together
// (design §11), so this moves with the set rather than on its own.
const Version = "0.1.0"

// The three actions this face emits, which is design §5.5's field list whole.
//
// `retry` was deferred under A-NORETRY until the item→run link existed. It does: D-UT19's
// `runs.item_id` names the item, drain's `dispatches` table names the run a decision
// requested, and drain's `retries` table carries an operator's claim that a run failed in
// the harness. A-NORETRY's closing condition was `retry` landing with a test that fails in
// its absence (TestPlanRetriesARunTheHarnessRefused), and this is that landing.
//
// A retry is never a guess. `plan` still writes nothing; it reads the authorization, checks
// that the run it names is the item's latest terminal run, and checks that the gate never
// judged it. An item that has run before without an authorization still holds with
// ReasonRunLinkMissing, which is the conservative direction: the link exists, but a person
// has not asked for the second attempt.
const (
	// ActionDispatch is the positive decision: the item is dispatched onto a lane for the
	// first time in this attempt.
	ActionDispatch = "dispatch"
	// ActionRetry is the positive decision that re-dispatches an item an operator
	// authorized: the decision names the run it supersedes, and the run it requests is a
	// new attempt rather than the run that failed.
	ActionRetry = "retry"
	// ActionHold is the negative decision: the item is not dispatched this tick.
	ActionHold = "hold"
)

// The reasons this slice emits, as a closed set. design §5.5 names the vocabulary as
// open ("e.g. blocked, no-lane, capacity, question-open"); these are the reasons this
// face can derive from the store, the tick and the tree.
//
// A-NOQ: `question-open` is here, and the questions come from Vantage's own index rather
// than from a parser in this repository (questions.go). Three answers that must stay
// distinct: a document with an open question, a document the tree does not hold, and an
// index that could not be read. Only the first two are facts about the item.
const (
	// ReasonBackendWindow holds an item when every backend on its route has a
	// reported spent window; the reset time releases it without an operator retry.
	ReasonBackendWindow = "backend-window"
	// ReasonBackendClosed waits for recorded evidence that the backend works again.
	ReasonBackendClosed = "backend-closed"
	// ReasonCommandFault is a command process failure, not an agent backend route.
	ReasonCommandFault = "command-fault"
	// ReasonReady is a dispatch's reason: the item is queued, unblocked, and a lane and
	// a policy were available.
	ReasonReady = "ready"
	// ReasonBlocked is a hold's reason: the item is blocked, or something it is blocked
	// by has not reached a terminal state.
	ReasonBlocked = "blocked"
	// ReasonNotReady is a hold's reason: the item's state is not `in-queue`.
	ReasonNotReady = "not-ready"
	// ReasonRunLinkMissing is a hold's reason: the item may be a retry — its history is
	// unknown, or it has run before — and no operator has authorized the second attempt.
	// The link exists; the person's decision is what is missing, so the face holds rather
	// than planning a retry nobody asked for. It is also the fallback when the item's
	// latest run cannot be classified into one of the four named outcomes below.
	ReasonRunLinkMissing = "run-link-missing"
	// ReasonBoundaryScopeEscape is a hold's reason: the item's latest run produced a
	// candidate the fence refused before the gate judged it, so the change was out of scope
	// and the gate never answered. The named step is `drain.refusal`'s `fence`.
	ReasonBoundaryScopeEscape = "boundary-scope-escape"
	// ReasonCandidateNoOp is a hold's reason: the item's latest run answered but committed
	// nothing, so there was no candidate to judge. The named step is `drain.refusal`'s
	// `no-op`.
	ReasonCandidateNoOp = "candidate-no-op"
	// ReasonRunUnjudged is a hold's reason: the item's latest run was cut short by a wall, so
	// its tree may hold half-finished work and a person inspects it before anything spends
	// a second attempt.
	ReasonRunUnjudged = "run-unjudged"
	// ReasonGateFailed is a hold's reason: the item's latest run was judged by the gate and
	// failed, so the code is wrong and a person picks it up with a task. The verdict was
	// recorded; the item stays in the queue.
	ReasonGateFailed = "gate-failed"
	// ReasonLandRefused holds an item whose latest candidate passed the gate but
	// was refused by the landing step. Its criteria remain unjudged.
	ReasonLandRefused = "land-refused"
	// ReasonNoPolicy is a hold's reason: the tick names no policy, so no complete
	// run.request can be built. A policy that *is* named but cannot build one is refused
	// before planning rather than held per item (Policy.Check).
	ReasonNoPolicy = "no-policy"
	// ReasonNoLane is a hold's reason: no lane the tick offered is free.
	ReasonNoLane = "no-lane"
	// ReasonRunLive is a hold's reason: the item's claimed run is still in flight.
	ReasonRunLive = "run-live"
	// ReasonNoFence is a hold's reason: the item names no fence, or names the repository
	// root, so it would fence nothing. It is refused rather than deferred behind every other
	// item, which is the rule that would silently serialise the whole queue behind one
	// blanket: the fence is the item's own scope, and an item without one is an item whose
	// change cannot be shown to be in scope.
	ReasonNoFence = "no-fence"
	// ReasonForkHeld is a hold's reason: the item delivers a fork whose patch branch an item
	// this tick already dispatched is delivering, so dispatching both would put two writers
	// on one branch. The decision names the item it was deferred behind in DeferredBy. It is
	// the only planner-level exclusion: two ordinary items with disjoint fences run together,
	// because each has its own worktree and its own land lock.
	ReasonForkHeld = "fork-held"
	// ReasonFailover is a dispatch's reason: the item's last run failed in the
	// infrastructure class, so this dispatch re-stages the same work on the next backend
	// in the policy's preference (design §2, D-MM11). It is a dispatch reason rather than
	// a hold's because the item *is* dispatched — the spend is a re-dispatch, and the
	// record says so.
	ReasonFailover = "failover"
	// ReasonSeatDegraded is a dispatch's reason: the backend the item's seat names failed in
	// the infrastructure class, and the failover walked to a backend the seat does not name.
	// A seat is a statement about the work — the design seat's work runs on the design
	// backend — so leaving it is a model switch, and the reason names both what fell back and
	// what answered (seatDegradedReason). It is a dispatch rather than a hold because the
	// ruling's degraded mode is an allowed, stamped run: the floor may produce the document,
	// and this is the stamp that keeps it from being read as a full seat's work.
	ReasonSeatDegraded = "seat-degraded"
	// ReasonInfraExhausted is retained for historical records only. New infrastructure
	// holds use ReasonBackendClosed and wait for evidence, not a budget.
	// Previously the preference had no backend
	// after the one that failed, and a chain is refused rather than walked on past what
	// the policy allows.
	ReasonInfraExhausted = "run-infra-exhausted"
	// ReasonRetry is a retry's reason: the item's latest run was refused by the harness —
	// it produced no judgable candidate — and an operator authorized the second attempt.
	ReasonRetry = "harness-refused"
	// ReasonReturned is a dispatch's reason: the item's latest run ended in a gate refusal,
	// with no commit, in a fence escape or in a refused landing, and the ending goes back
	// to the item's worker with its evidence (Decision.Returned). It is uncounted: the
	// owner ruled on 2026-11-17 that a failure returns to its worker every time, and only
	// an identical repeat goes to the design seat instead (returned.go).
	ReasonReturned = "returned-with-evidence"
	// ReasonRetryJudged is a hold's reason: an authorization names a run the gate judged,
	// so re-dispatching it would spend a second attempt on work a verdict already covers.
	// The gate's `gate.verdict` event is the evidence, not the run's exit status.
	ReasonRetryJudged = "retry-judged"
	// ReasonRetryUnjudged is a hold's reason: an authorization names a run a wall cut
	// short, which is inspected before anything re-dispatches it.
	ReasonRetryUnjudged = "retry-unjudged"
	// ReasonQuestionOpen is a hold's reason: the item's design document carries a question
	// Vantage's index reports as unanswered. The decision names it in QuestionID. It is a
	// hold rather than a refusal because the remedy is a person's ruling, and the item is
	// dispatchable the moment the document is.
	ReasonQuestionOpen = "question-open"
	// ReasonQuestionsUnknown is a hold's reason: the question index could not be read — the
	// tool is absent, it failed, it timed out, or the planner was given no source at all —
	// so whether the item's design document is open is unknown. Unknown is never "no
	// questions": a dispatch on an unread index spends a run on work the design may not
	// have decided.
	ReasonQuestionsUnknown = "questions-unknown"
	// ReasonDesignDocMissing is a hold's reason: the item names a design document the tree
	// does not hold. The reference is what is broken, so the remedy is the item's own
	// reference rather than a lane or a policy.
	ReasonDesignDocMissing = "design-doc-missing"
	// ReasonTrunkBroken is a hold's reason: a fixer owns a red trunk and every ordinary
	// item waits for it. The decision names the fixer in DeferredBy, so an operator
	// reading one decision can follow the hold to the item doing the repair. The fixer
	// itself is never held for this reason.
	ReasonTrunkBroken = "trunk-broken"
	// ReasonConflictFix is a dispatch's reason: the candidate's change did not squash onto
	// a moved trunk, so this run re-applies it with the conflict in its brief. It is a
	// dispatch rather than a hold, staged immediately rather than left for a later tick;
	// what ends a chain of them is the identical-repeat rule, not a count.
	ReasonConflictFix = "conflict-fix"
	// ReasonConflictExhausted was a hold's reason: the candidate had spent its one
	// conflict-fix run and the fix run conflicted too. The owner's ruling of 2026-11-17
	// removed that count. A pass no longer writes it as a hold: an item whose latest run
	// ended in a conflict no fix run answered is returned (returnedReasons), and the name
	// stays so recorded decisions read as history.
	ReasonConflictExhausted = "conflict-exhausted"
	// ReasonItemUnstagleable is a hold's reason: the item's own record refused staging, so
	// no brief could be written for it. It is a hold and not an infrastructure fault: an
	// empty title, an absent criterion, an absent fence or a brace pair in one of those
	// fields will refuse the same way on every retry, so the loop holds this one item and
	// lets the rest of the queue run instead of backing the whole drain off for it. The
	// named step is `drain.refusal`'s `staging`, and the event carries what must be edited.
	ReasonItemUnstagleable = "item-unstagleable"
)

// seatDegradedReason spells a seat degradation as the one line a reader of the decision or
// the loop's report has: the seat that fell back and the backend that actually answered.
//
// The reason a reader needs it in one string is that the seat lives on the decision and the
// backend lives on its run request, and a seal that took both to explain would be invisible
// in the one place an operator looks first. The stored row carries the same two values in
// its own columns, so this text is a restatement and not the only record.
func seatDegradedReason(seat, backend string) string {
	return ReasonSeatDegraded + fmt.Sprintf(`: seat %q fell back to backend %q`, seat, backend)
}

// The item states this face reads. They are item's vocabulary, spelled here because
// drain reads item's rows and must not invent a second spelling for them.
const (
	// StateInQueue is the one dispatchable item state: the item is waiting for a lane.
	StateInQueue = "in-queue"
	// StateBlocked is an item state that holds the item whatever its edges say.
	StateBlocked = "blocked"
	// StateInProgress is the frozen legacy item state that is evidence the item has been
	// dispatched and run before: an item that has been in progress and is queued again may
	// be a retry. No transition writes it now; the drain leaves a non-landing run's item in
	// `in-queue`, and a legacy row stays readable.
	StateInProgress = "in-progress"
	// StateLanded is the item state a landed candidate leaves its item in. It is the one
	// terminal done outcome the loop writes, and it is terminal for an edge: an item that
	// landed no longer blocks the items waiting on it.
	StateLanded = "landed"
)

// LaneStateLeased is the lane state a run holds: run's claim writes it with the lease,
// and its terminal record or a release clears it (design §5.6). A lane in it is
// unavailable until its lease is at or before the tick's now, at which point it is
// expired and the planner offers it again (§5.5).
const LaneStateLeased = "leased"
