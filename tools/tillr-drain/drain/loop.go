// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-drain/spend"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// The trunk the loop stages lanes from and lands onto. It is `main` because design §4.1's
// landing step is the only thing that moves it (D-UT18). Fresh lanes start there;
// returned worker sessions retain the original lane and base.
const TrunkRef = "main"

// Loop stop and historical fault reason spellings. Boundary and gate faults now
// hold only their attributed item; the Stop-prefixed constants remain readable
// in historical reports and standalone exit-code mappings.
//
// The set has no count in it, by ruling (docs/design/momentum-principle.md): the pile is the
// only throttle, so nothing here is reached by a tick budget, a spend total or a token total.
// A wall of that kind stops the loop with work in the pile, which is the one outcome the loop
// exists to prevent.
const (
	// StopQueueDrained is a clean stop: a full tick planned nothing dispatchable, so the
	// pile has nothing the loop may run right now. It is the service waiting, not the
	// service finishing — a supervisor that reads it as "done" stops the loop exactly when
	// it should be idle but alive.
	StopQueueDrained = "queue-drained"
	// StopOperatorStop is a clean stop: the caller asked the loop to stop. That is the only
	// authority the ruling recognizes for ending the loop while work remains.
	StopOperatorStop = "operator-stop"
	// StopHostBoundaryViolation holds the item whose run wrote outside its boundary.
	// Only explicit fault clearance permits examination again.
	StopHostBoundaryViolation = "host-boundary-violation"
	// StopGateHarnessError holds an item whose gate could not judge; it is not
	// a verdict against the candidate's code.
	StopGateHarnessError = "gate-harness-error"
	// StopLanesLeased is a hard stop: a full tick planned nothing dispatchable because a
	// lane it may dispatch onto is held with no expiry. No clock will free that lane, so
	// the operator's `tillr run release` is the missing act; the reason says so rather
	// than reporting a drained queue that would stop the service while work remains.
	StopLanesLeased = "lanes-leased"
	// StopFault is a hard stop that is not a decision about the pile: the loop could not
	// complete a step. It is a policy that will not load or a program it cannot be given
	// (the loop never started), or a tick that could not read its store or its trunk. The
	// record's Detail names the fault and ExitCode is the status the caller answered with,
	// which is the one place such a death survives the terminal that printed it.
	StopFault = "fault"
)

// DefaultBackoffBase is the wait before the next attempt after the first consecutive
// infrastructure failure, and BackoffCap is the longest that wait grows to. The ruling's
// rule for a repeated fault is a backoff and not a count: the machinery is broken and the
// work is fine, so the loop reports the failure, waits longer, and tries again.
const (
	DefaultBackoffBase = time.Second
	BackoffCap         = 5 * time.Minute
)

// BackoffDelay answers how long to wait after streak consecutive infrastructure failures:
// base, doubled once per consecutive failure, capped at BackoffCap. A streak of zero is no
// wait at all.
//
// A backoff is not the artificial pause the ruling forbids, and the difference is what the
// wait is for. A pause withholds work that could be done — a night ending, a cool-down, a
// comfortable concurrency number. This wait is called only when the loop dispatched nothing
// because its own machinery failed (Run checks that), so there is no work being withheld:
// the wait is the absence of work made visible, and it grows because a broken harness does
// not heal faster when asked more often.
func BackoffDelay(streak int, base time.Duration) time.Duration {
	if streak <= 0 {
		return 0
	}
	delay := base
	for n := 1; n < streak && delay < BackoffCap; n++ {
		delay *= 2
	}
	if delay > BackoffCap {
		return BackoffCap
	}
	return delay
}

// waitFor waits d, and answers whether the wait finished: a false answer is the caller's
// context being done, which is how an operator stop interrupts a backoff instead of waiting
// it out.
func waitFor(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// StopExitCode is the exit status a stop reason answers with: 0 for the clean stops, and
// the record contract's codes for the hard ones, so a caller's `if tillr drain loop; then`
// is true exactly when nothing needs looking at.
func StopExitCode(reason string) int {
	switch reason {
	case StopQueueDrained, StopOperatorStop:
		return 0
	case StopHostBoundaryViolation, StopLanesLeased:
		// A completed negative decision: the loop stopped itself rather than failing,
		// and the reason names the act a person has to take.
		return 1
	case StopGateHarnessError:
		// The gate could not judge: an external dependency, as `tillr-gate` answers.
		return 4
	}
	return 5
}

// The gate's candidate verdicts and harness error. A recorded stall is the
// distinct exit 6 outcome declared with its retry handling in gate_stall.go.
// GatePassedInherited is the second passing verdict (design §8.2, OQ-GP7): the
// candidate's only failing checks also fail on the trunk tip, each recorded as
// inherited with its output.
const (
	GatePassed          = "pass"
	GatePassedInherited = "pass-inherited"
	GateFailed          = "fail"
	GateErrored         = "error"
)

// InheritedFailure records one check that failed on both the candidate and the trunk tip,
// with the tip commit and output that acquitted the candidate.
type InheritedFailure struct {
	CheckID string `json:"check_id"`
	TipSHA  string `json:"tip_sha,omitempty"`
	LogRef  string `json:"log_ref,omitempty"`
	Output  string `json:"output,omitempty"`
}

// The land outcomes the loop distinguishes. `landed` is the positive decision; `refused`
// is the land step declining to move the trunk, which the loop records as the item's
// verdict; `squash-conflict` is the moved trunk's own outcome and becomes one bounded
// conflict-fix request rather than a failed candidate (design §6: no self-healing in the
// loop, but a conflict is a fact about the moved trunk the next run can resolve).
const (
	LandLanded         = "landed"
	LandRefused        = "refused"
	LandSquashConflict = "squash-conflict"

	// ConflictFixSuffix is the marker a conflict-fix run's id carries. It is what makes
	// "at most one conflict-fix run per candidate" structural: the fix run's own id ends
	// in the suffix, so a conflict on it is the second one and holds instead of staging
	// another. The original run id never ends in it (the planner's ids end in a stamp).
	ConflictFixSuffix = "-cfix"
)

// VerdictPassed retires the item's criteria only after the candidate lands.
// An attempt that did not land records a run-level refusal, never an item verdict.
const VerdictPassed = "pass"

// ScopeEscape names the refusal the loop makes when a candidate's change is outside its
// lane's fence, or its worktree carries work the commit does not. It is a verdict about the
// work — the change is not the change the item asked for — and never an infrastructure
// failure: the harness ran fine, and re-dispatching the same item would produce the same
// escape.
const ScopeEscape = "boundary-scope-escape"

// Candidate is one item's work on one lane, as the gate, land and verdict steps see it:
// plain data, so a step that is a subprocess can be handed it on argv and a step that is a
// stub in a test can read it as a value.
//
// TickID is not a property of the candidate: it is the tick the candidate was dispatched in,
// carried here because the judgment events a step appends are correlated by run *and* tick,
// and the step is handed nothing else that knows which tick it is serving.
type Candidate struct {
	Judge           *JudgeReceipt     `json:"judge,omitempty"`
	Evaluator       *run.Request      `json:"-"`
	Writers         []JudgeWriter     `json:"-"`
	Producers       []JudgeWriter     `json:"-"`
	EvaluatorInputs map[string]string `json:"-"`
	EvaluatorTask   string            `json:"-"`
	// VerifyReplan is set on a verify-not-done refusal whose findings say the plan was
	// wrong (a reason beginning plan-wrong:); the item then returns to the plan step.
	VerifyReplan bool `json:"-"`
	// Runtime-only observation, sampled when the gate records a stall.
	gateCount     func() int
	RefusalDetail string `json:"refusal_detail,omitempty"`
	// Required replays can fail even when the recorded fast gate passed.
	requiredFailures []EndingCheck
	// EvidenceKey, when set, is the returned ending's evidence key in place of the one its
	// step derives: a seat obstacle keys its return by the backend it leaves, so climbing
	// to the next backend is never read as an identical repeat (seat_escalation.go).
	EvidenceKey   string         `json:"-"`
	BlockConflict *item.Conflict `json:"block_conflict,omitempty"`
	ItemID        string         `json:"item_id"`
	LaneID        string         `json:"lane_id"`
	RunID         string         `json:"run_id"`
	TickID        string         `json:"tick_id,omitempty"`
	// Worktree is the checkout the candidate's change lives in. For an ordinary lane it
	// is the lane worktree; for a fork-series item it is the fork checkout the item's
	// fence names, because that is where the run committed and where the gate's own
	// change lives.
	Worktree string `json:"worktree"`
	// BaseRef is the ref a fork-series candidate's delivery moves, refs/heads/patch/<fork>.
	// It is empty for an ordinary item, whose landing moves the trunk the land step reads.
	BaseRef string `json:"base_ref,omitempty"`
	// ObservedTrunk is the last observed trunk tip, checked under the land lock.
	ObservedTrunk string `json:"observed_trunk,omitempty"`
	BaseSHA       string `json:"base_sha"`
	HeadSHA       string `json:"head_sha"`
	// WorkerHeadSHA and RetirementTreeSHA bind loop-owned document maintenance to the
	// worker commit and the exact staged tree. The intent is persisted before committing,
	// so a restarted loop can recover HEAD without exempting any worker change from its fence.
	WorkerHeadSHA     string         `json:"worker_head_sha,omitempty"`
	RetirementTreeSHA string         `json:"retirement_tree_sha,omitempty"`
	WorkerSession     *WorkerSession `json:"worker_session,omitempty"`
	// FixerBaseSHA is the rolling watermark a fixer's full gate plans from: the last
	// trunk commit a rolling run proved green. It is set only for a fixer, so the fixer's
	// gate is the untiered range since the last green while an ordinary candidate's gate
	// stays the diff-scoped landing tier.
	FixerBaseSHA string `json:"fixer_base_sha,omitempty"`
	// Request is the run.request that produced this candidate. It is held so a
	// conflict-fix run can be built from the same backend, command and timeout instead of
	// inventing a second request. It is not persisted: the candidate is a value a step
	// reads, and the run record is the persistent copy.
	Request *run.Request `json:"-"`
	// Seat is the seat the item asked for, carried so the conflict-fix run's decision
	// names the same seat the original dispatch did.
	Seat     string `json:"-"`
	Workflow string `json:"workflow,omitempty"`
}

// GateOutcome is what the gate step answers: the verdict the gate reached and the gate
// program's own wall time.
//
// The duration is the gate's and nothing else's: the step measures around the program it
// starts, so a slow lane or a slow model cannot enter the number. It is carried out of the
// step rather than timed by the loop because the loop's wall clock around the call would also
// include the step's own store write (the gate judgment), which is the loop's bookkeeping
// and not the gate.
type GateOutcome struct {
	StallHeld     bool // first-stall transition committed atomically with the judgment
	StalledChecks []StallObservation

	// Outcome is GatePassed, GatePassedInherited, GateFailed, GateErrored or GateStalled.
	Outcome string
	// InheritedFailures records the trunk-red checks that acquitted a GatePassedInherited
	// candidate, each with its tip sha and output.
	InheritedFailures []InheritedFailure
	// DurationMS is the gate program's wall time in milliseconds.
	DurationMS int64
	// ErrorDetail is set on a GateErrored answer that no later pass can change: the gate
	// refused its own arguments (gate_usage_error.go). It is the hold's reason.
	ErrorDetail string
	// FailingChecks is the check ids the gate recorded as failed. It is the set the
	// trunk-already-red replay re-runs on the base; a gate failure that names none cannot
	// be replayed and takes the ordinary refusal.
	FailingChecks []string
	// SuspectPaths is the candidate's changed paths, the first half of a fixer's fence.
	SuspectPaths []string
	// CheckWatches maps a check id to the read and write paths its plan declared, the
	// second half of a fixer's fence.
	CheckWatches map[string][]string
	// RepairDetail carries an unattributable heavy batch's endpoints into its fixer.
	RepairDetail string
	// CandidateChangedPaths records the candidate's changed paths (base..head) for
	// OQ-GP7 path intersection against failing red checks' declared reads.
	CandidateChangedPaths []string
	// CheckReads maps a check id to its declared `reads` (defaulting to ["."] when
	// undeclared, matching checkfiles.go) for OQ-GP7 path intersection.
	CheckReads map[string][]string
	// CheckLogs maps a failing check id to its log or triage file path so a trunk
	// fixer's brief can keep a copy with its run and show the failing cases.
	CheckLogs map[string]string
}

// Steps is what the loop drives and does not own: one run on a lane, then the gate, land
// and verdict that turn a candidate into landed work.
//
// The seam is deliberately four calls and not a store handle, because these four are
// processes: `tillr run exec`, and the caller-named gate, land and verdict programs. The
// loop owns the order, the boundary between them and the stopping conditions; each step
// owns its own judgment, so a step's exit code is never re-derived here.
type Steps interface {
	// Run executes one run.request and answers the terminal record and the run's log
	// text. The log is what the attempt is classified from, so a step that cannot read it
	// answers no log rather than failing.
	Run(ctx context.Context, req run.Request) (run.Record, string, error)
	// Gate judges one candidate: passed, failed, or errored (could not judge), with the gate
	// program's own wall time.
	Gate(ctx context.Context, candidate Candidate) (GateOutcome, error)
	// Land moves the trunk to one gated candidate, or answers what the lander recorded.
	// The outcome is the lander's own record, so a squash conflict and a refusal are told
	// apart by the record rather than by a shared exit code.
	Land(ctx context.Context, candidate Candidate) (LandOutcome, error)
	// Verdict records VerdictPassed only after the candidate lands. A gate failure,
	// refused landing, fence refusal or run with no commit records a `drain.refusal`
	// event instead and leaves the item's criteria unchanged.
	Verdict(ctx context.Context, candidate Candidate, verdict string) error
	// ItemState moves the item's own state — not a criterion's — through item's state
	// face. It is how a candidate's terminal outcome reaches the board: a landed
	// candidate's item is `landed`, and a run that answered `blocked` leaves it `blocked`
	// for a person to decide. A run that produced no landing leaves the item where it was,
	// in the queue; the planner's named hold is what keeps it from being dispatched again.
	// It is called after the verdict when the gate judged the candidate, because a verdict
	// retires criteria and this says where the item's work stands; a `blocked` report is the
	// one call made on its own, because the model's answer is about the item rather than
	// about a candidate. The run is named because the move is recorded against it: a step
	// span belongs to one run's record directory, and this call is the one step the loop
	// makes that has no candidate to read a run id from.
	ItemState(ctx context.Context, itemID, runID, state string, waiting ...item.WaitingOn) error
	// Sync materializes the item blocks of the repository's tracked documents into the
	// store, through item's sync face. The loop calls it before planning at each new trunk
	// commit, so the queue it drains is the one the documents declare (docs/design/tillr-convergence.md
	// Section 3.1). A block with a finding is the documents' defect, not the loop's: it comes
	// back in the outcome and the loop drains what was written. An error is a sync that could
	// not run at all, which is a fault.
	Sync(ctx context.Context) (SyncOutcome, error)
}

// LoopConfig is one loop's parameters: where it works, what it may dispatch, and how it
// spends.
type LoopConfig struct {
	PostLanding settings.Settings
	// MigrateOwnerQuestions converts only existing owner decisions, through ordinary
	// command runs and the design gate, then exits without dispatching other work.
	MigrateOwnerQuestions bool
	// Root is the repository the loop stages lanes from and lands onto. It is the main
	// checkout, and its refs and worktrees are the boundary every run is bracketed by.
	Root string
	// Store is the store the loop reaps and plans from. It must not live inside a run's
	// bracket: `run exec` writes it by design, so the loop never names it to the boundary
	// check (see BoundarySpec.Store).
	Store string
	// LaneRoot is the directory the lanes' worktrees live under: lane `lane-1` is
	// <LaneRoot>/lane-1, which is also the lane directory `run exec` writes its log into.
	LaneRoot string
	// Lanes is the lane ids the loop may dispatch onto, in the order a tick prefers them.
	Lanes []string
	// BackoffBase is the wait before the next attempt after the first consecutive
	// infrastructure failure; each further one doubles it up to BackoffCap. 0 is
	// DefaultBackoffBase. It is a wait, never a stop: the ruling's answer to a repeated
	// fault is to report it and try again, because the pile still holds work.
	BackoffBase time.Duration
	// StorePollInterval bounds the wait before noticing a new store fact. Zero
	// selects DefaultStorePollInterval; it never bounds the amount of work.
	StorePollInterval time.Duration
	// GateBudget is the bound on the gate's own wall time for one tick. 0 is
	// DefaultGateBudget, which is the one place the budget is stated; this field exists so a
	// test can measure the overrun path with a bound it can cross, and a face that never sets
	// it is reading the statement rather than a second copy of the number.
	GateBudget time.Duration
	// Policy is the backend policy a dispatch's run.request is built from.
	Policy *Policy
	// PolicyRef enables content-checked reloads at the start of each pass.
	PolicyRef string
	// QuestionsCmd is the program that answers Vantage's index, cached lazily for each
	// trunk commit and discarded when the trunk moves. Empty is
	// DefaultIndexCommand, and a program that is not there holds the affected items with
	// `questions-unknown` rather than dispatching them.
	QuestionsCmd []string
	// QuestionsIndex overrides the shadow watcher's saved Vantage index path.
	// It does not replace the pass's trunk-bound question cache.
	QuestionsIndex string
	// VantageURL and VantageRepo are handed over at startup for reply confirmation.
	// Empty values disable confirmation; this check never reads settings.
	VantageURL       string
	VantageRepo      string
	WorkflowDefaults map[string]string
	Comments         *settings.Comments
	// Restart enables explicit requests to replace this loop's process in place.
	Restart *RestartConfig
	// ExitWhenDrained opts out of the default wait when nothing is dispatchable.
	ExitWhenDrained bool
	// Steps is what the loop drives.
	Steps Steps
	// Stage prepares each dispatch's lane directory before it runs: the worktree, the
	// brief, the fence and the base sha. It is required, because a tick that dispatches
	// without it launches an adapter with no brief — which is what the live canary did, and
	// every adapter refused it in a tenth of a second.
	Stage Stager
	// Now is the loop's clock, injected so a test's store and boundary agree.
	Now func() time.Time
	// Report is where the loop writes its one line per event. A nil report discards.
	Report io.Writer
}

// LoopResult is what the loop did and why it stopped.
//
// Its field names are the file's field names too: the loop writes it as the payload of the
// `drain.loop` record at <root>/.tillr/loop/drain-<stamp>.json (LoopResultFile), so an
// operator asking "why did it stop" reads the same words the caller's exit status was
// derived from.
type LoopResult struct {
	FirstGeneration string `json:"first_generation,omitempty"`
	LastGeneration  string `json:"last_generation,omitempty"`
	// Stop is the stop reason, one of the Stop* constants.
	Stop string `json:"stop"`
	// Ticks is the number of complete ticks.
	Ticks int `json:"ticks"`
	// Dispatched is the number of runs the loop started.
	Dispatched int `json:"dispatched"`
	// Landed is the number of candidates the loop landed.
	Landed int `json:"landed"`
	// TrunkAcquitted is the number of gate failures replayed onto the base and found to be
	// the trunk's, not the candidate's. Each one filed or reused one fixer and wrote no
	// failing verdict.
	TrunkAcquitted int `json:"trunk_acquitted,omitempty"`
	// RollingGreen and RollingRed count the rolling-tier runs this loop made: a green one
	// advanced the watermark, a red one filed the fixer for the tip and advanced nothing.
	RollingGreen int `json:"rolling_green,omitempty"`
	RollingRed   int `json:"rolling_red,omitempty"`
	// ConflictFixes is the number of squash conflicts that staged a conflict-fix run.
	ConflictFixes int `json:"conflict_fixes,omitempty"`
	// Rejected is the number of candidates the loop refused. Refusals record a
	// `drain.refusal` event, never an item verdict.
	Rejected int `json:"rejected"`
	// Candidates is the number of runs that committed a candidate. Every one of them is
	// a run the fence, the gate or the land judged; a run that produced no commit is not
	// a candidate. It is the denominator that makes a repeated refusal measurable: a run
	// that has landed nothing has refused every candidate it produced, and Candidates
	// says how many that is.
	Candidates int `json:"candidates"`
	// RefusedSteps counts the refusals by the step that made them (`fence`, `gate`,
	// `land`, `no-op`), so a reader can tell a fence that admits nothing from a gate that
	// fails everything. It is the evidence a repeated refusal is built from.
	RefusedSteps map[string]int `json:"refused_steps,omitempty"`
	// RefusedItems is the items the loop's refusals left in the queue, in the order they
	// were refused and without repeats. It is the blast radius a person has to look at.
	RefusedItems []string `json:"refused_items,omitempty"`
	// RepeatedRefusal is set when the loop landed nothing and refused every run it
	// produced. It is a report and not a stop (see refusal.go), and it is the field that
	// keeps a drain that has never landed from reading like a drain that is merely
	// unlucky.
	RepeatedRefusal *RepeatedRefusal `json:"repeated_refusal,omitempty"`
	// InfraFailures is the number of infrastructure-class attempts the loop saw, across
	// all ticks, reset by a candidate. It is reported here and drives the backoff between
	// attempts; it never stops the loop.
	InfraFailures int `json:"infra_failures"`
	// SpendUSD sums work started in this loop and earlier work settled here.
	// Only reported prices contribute; CostUnknown counts the missing prices.
	// RecordedSpend separately sums every terminal run in the store.
	SpendUSD      float64     `json:"spend_usd"`
	StartedSpend  SpendTotals `json:"spend_started_in_loop"`
	CarriedSpend  SpendTotals `json:"spend_carried_over"`
	RecordedSpend SpendTotals `json:"spend_all_recorded_runs"`
	spendRecords  map[string]run.Record
	spendCarried  map[string]bool
	// Tokens sums reported tokens in the started and carried groups.
	Tokens int64 `json:"tokens"`
	// SpendUnknown counts missing token measurements, and CostUnknown missing
	// prices, including wholly unreported runs. Each example names one missing
	// measurement instead of treating an absent measurement as zero.
	SpendUnknown        int    `json:"spend_unknown"`
	SpendUnknownExample string `json:"spend_unknown_example,omitempty"`
	CostUnknown         int    `json:"cost_unknown"`
	CostUnknownExample  string `json:"cost_unknown_example,omitempty"`
	// Detail names what stopped the loop when the reason alone does not: the boundary
	// paths of a violation, or the item and class of a broken harness.
	Detail          string           `json:"detail"`
	StopItemID      string           `json:"stop_item_id,omitempty"`
	StopStep        string           `json:"stop_step,omitempty"`
	StopExplanation *StopExplanation `json:"stop_explanation,omitempty"`
	// ExitCode is the status the process answered with, written for StopFault, where the
	// fault's own code (a policy that will not load is usage, a store that will not open is
	// incompatible) is the fact a reader cannot derive from the reason. A clean stop omits
	// it: there the reason is the answer, and 0 is not a status anyone needs to read.
	ExitCode int `json:"exit_code,omitempty"`
	// Sync is the latest document sync outcome. SyncedSHA names the trunk commit
	// whose documents were synced before planning. Both are absent before a sync.
	Sync      *SyncOutcome `json:"sync,omitempty"`
	SyncedSHA string       `json:"synced_sha,omitempty"`
	// Build is the binary that wrote this result. It is a pointer so a record written by a
	// loop that could not identify itself omits the field, which a reader draws as a build
	// that cannot be named rather than as a build with nothing to say (build.go).
	Build *BuildStamp `json:"build,omitempty"`
}

// Loop supervises per-item passes (docs/reference/tillr-drain-successor.md#one-pass-and-compare-and-swap-application).
// Dispatch, settlement and judgment each claim their resource before launching an
// independent worker. Terminal outcomes are read from the store; completion channels
// are only this process's wake triggers. Each run retains its own boundary bracket.
type Loop struct {
	reportedAlarms                       []string // Previous pass's display, not an availability input.
	startView                            atomic.Pointer[startView]
	endingCapabilities                   map[string]string
	rollAction, piRollAction, pushAction landingAction
	greenPushAtStart                     sync.Once
	ownerMigrationItem                   string
	laneBootstrapReported                bool
	staleRunsRepaired                    bool
	orderWorkRetired                     bool // pass-owned check for interrupted pre-upgrade recorder work
	// staleBaseChecked holds the owner conflicts already read for a stale base in this
	// process: a conflict's run base and dispatch record never change, so one read answers.
	staleBaseChecked            map[string]bool
	gatePolicy                  GatePolicy
	extRollAction               landingAction
	lastHeavyStatus             string // pass-owned status; cap zero still reports distance from green
	restartMu                   sync.Mutex
	restartReplaced             bool
	restartMechanical           sync.RWMutex
	heavyMu                     sync.Mutex
	heavyWG                     sync.WaitGroup
	heavyRunning                bool
	heavyProofActive            bool // heavyMu; entry is serialized with exec by restartMechanical
	heavyReuseRed               bool // set at startup after adopting an in-place replacement
	heavyLandingCounts          []int
	landLockHolds               []int64
	landLockUnmeasured          int
	heavyWanted, heavyAttempted string
	cfg                         LoopConfig
	// mu guards worker-side infrastructure bookkeeping and report measurements.
	// Each examination has its own result, transferred to the pass at completion.
	mu sync.Mutex
	// historyPinned records that this process has initialized object retention.
	// mu guards it; unchanged observations then need only a read transaction.
	historyPinned bool
	// reportMu serializes the loop's report writes, so a caller's io.Writer need not be
	// safe for concurrent use.
	reportMu sync.Mutex
	// infraStreak is the run of consecutive infrastructure-class attempts. A candidate
	// attempt — pass or fail — resets it. It is the exponent of the backoff the loop waits
	// between attempts when it cannot dispatch anything, and it is not a stop condition:
	// the pile is the only throttle.
	infraStreak int
	stageFault  bool
	// laneConditions is each lane a lane-owned staging refusal took out of rotation this
	// session (lane_rotation.go), and laneRefusedNow says the current staging refused its
	// lane, so the pass offers the item another. laneMu guards rotation state;
	// mu guards the staging flags and active-run bookkeeping.
	laneMu               sync.Mutex
	laneConditions       map[string]*laneCondition
	laneConditionsLoaded bool
	laneRefusedNow       bool
	// replanning is true while a pass re-plans after a refused claim: lanes that pass
	// took out of rotation are not re-checked until the next pass.
	replanning bool
	// infraLines is the last infrastructure-class staging line printed per item and lane,
	// so a refusal that repeats unchanged on every pass is printed once.
	infraLines map[string]string
	// gateBudget is the resolved bound on a tick's gate wall time: cfg.GateBudget when it
	// named one, DefaultGateBudget otherwise.
	gateBudget    time.Duration
	gatesInFlight int
	// build is the binary this loop is running, read once when the loop is built. It is
	// printed before the first tick and written into every result record, so a record names
	// the build that made it (build.go).
	build BuildStamp
	// held is the items this run holds because their own record refused staging, keyed by
	// item id. It is the loop's memory and not the store's: the durable record is the
	// `drain.refusal` event, and a fresh process that finds the record unchanged fails the
	// same way once more and holds it again — which costs a staging attempt and no tokens.
	// The planner never sees it, so the plan stays a pure function of the store; passPlans
	// applies it to the decisions it read back.
	//
	// It is written by staging and read by passPlans on the pass goroutine, so it
	// needs no lock.
	held           map[string]heldItem
	quotaMu        sync.Mutex
	policyMu       sync.RWMutex
	policyState    PolicyState
	policyRecorded bool
	// active owns only completion triggers, never terminal outcomes. Outcomes are
	// reconstructed from the store by the next pass or a replacement Loop.
	examinations  sync.WaitGroup
	active        map[string]bool
	activeItems   map[string]string // item -> local run or examination
	activeCancels map[string]context.CancelFunc
	stoppedRuns   map[string]bool
	completions   chan runCompletion
	laneChanges   map[string]int
	stagingLanes  map[string]int
	// segmentEvidence is each settling run's uncommitted-work evidence as it stood before
	// its session was last continued (segmentRepeated).
	segmentEvidence map[string]string
	runDurations    map[string]int64
	sessionID       string
	sessionMu       sync.Mutex
	sessionStarted  int64
	sessionNextPass int64
	passSequence    uint64
	previousAnswers string

	sessionInitialized bool
	// keepWAL and walKeeper are the store's checkpoint owner, lastCheckpoint is the clock
	// reading of its last checkpoint, and restartPending is whether that cycle left the log
	// unrestarted, so the next heartbeat tries again (see openWALKeeper); sessionMu guards
	// all four. missedRestarts, reportedLogBytes and reportedLockError are the heartbeat
	// goroutine's own.
	// beforeCheckpoint and checkpointed are test seams, run just before and just after
	// each heartbeat checkpoint.
	keepWAL           bool
	walKeeper         *store.CheckpointOwner
	lastCheckpoint    time.Time
	restartPending    bool
	missedRestarts    int
	reportedLogBytes  int64
	reportedLockError bool
	beforeCheckpoint  func()
	checkpointed      func(store.Checkpointed)
	// sessionStop cancels Run's context as an operator stop when drain_sessions
	// records stop_requested > 0.
	sessionStop func()
	// beforeSessionOpen is a test seam, run just before a session write opens the store,
	// with whether that open creates and migrates it.
	beforeSessionOpen func(create bool)
	// waitingDetail retains all holds, not just the five printed examples, so a
	// changed reason outside the summary still reports a changed queue.
	waitingDetail   string
	nextCommentPoll time.Time
	hasAnswers      bool
	pollOnly        bool // test seam: suppress accelerators, retain store polling
	// heartbeatClock is a test seam: when set, the heartbeat waits on the ticker it
	// returns instead of a wall-clock one, so a test decides when each tick happens.
	heartbeatClock func() heartbeatTicker
	// sessionOptions are the open options the session refresh uses; a test sets a zero
	// busy timeout to meet a held write lock without waiting out the default one.
	sessionOptions []store.OpenOption
	// keeperOpen lets tests shorten the checkpoint writer's SQLite lock wait.
	keeperOpen func(context.Context, string) (*store.CheckpointOwner, error)
	// Document inputs belong to the pass goroutine, never its asynchronous workers.
	historyMu        sync.Mutex
	nonLanding       bool
	syncedSHA        string
	syncClearSeq     int64
	questions        QuestionSource
	ownerAnswerMu    sync.Mutex
	ownerAnswerBlobs map[string]string
}

func (l *Loop) isActive(runID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active[runID]
}

func (l *Loop) activeCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.active)
}

func (l *Loop) dispatched(result *LoopResult) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return result.Dispatched
}

func (l *Loop) addDispatched(result *LoopResult) {
	l.mu.Lock()
	result.Dispatched++
	l.mu.Unlock()
}

// heldItem binds a staging refusal to the content the loop held. The clock
// still orders explicit retries and lane restorations, and is the fallback
// when the content version could not be read at the time of the refusal.
type heldItem struct {
	reason         string
	at             time.Time
	contentVersion string
}

// NewLoop validates a configuration and answers the loop it describes. Everything it
// refuses is a configuration the loop cannot honour, refused before a tick runs rather
// than discovered mid-run.
func NewLoop(cfg LoopConfig) (*Loop, error) {
	if cfg.Root == "" {
		return nil, Usagef("the loop needs --root <repository>: the checkout it stages lanes from and lands onto")
	}
	if cfg.Store == "" {
		return nil, Usagef("the loop needs --store <path>: the store it reaps and plans from")
	}
	if cfg.LaneRoot == "" {
		return nil, Usagef("the loop needs --lane-root <dir>: where the lanes' worktrees live")
	}
	if len(cfg.Lanes) == 0 {
		return nil, Usagef("the loop needs at least one lane: a tick with no lane dispatches nothing")
	}
	if cfg.BackoffBase < 0 {
		return nil, Usagef("the loop needs a non-negative backoff base: it is the wait after an infrastructure failure, not a bound on the run")
	}
	if cfg.BackoffBase == 0 {
		cfg.BackoffBase = DefaultBackoffBase
	}
	if cfg.StorePollInterval < 0 {
		return nil, Usagef("the loop needs a non-negative store poll interval")
	}
	if cfg.StorePollInterval == 0 {
		cfg.StorePollInterval = DefaultStorePollInterval
	}
	if cfg.GateBudget < 0 {
		return nil, Usagef("the loop needs a non-negative gate budget: it bounds the gate's own wall time, not a run's")
	}
	budget := cfg.GateBudget
	if budget == 0 {
		budget = DefaultGateBudget
	}
	if cfg.Policy == nil {
		return nil, Usagef("the loop needs --policy <file>: no tick can build a run.request without one")
	}
	if cfg.Steps == nil {
		return nil, Usagef("the loop needs its steps: the run, gate, land and verdict programs it drives")
	}
	if cfg.Stage == nil {
		return nil, Usagef("the loop needs a stager: nothing else writes a lane's brief, fence and base sha, and an adapter launched without them refuses in its preflight")
	}
	if c := cfg.Comments; c != nil {
		cfg.VantageURL, cfg.VantageRepo = c.VantageURL, c.Repo
		if cfg.VantageRepo == "" {
			root, err := filepath.Abs(cfg.Root)
			if err != nil {
				return nil, Usagef("--root: %v", err)
			}
			cfg.VantageRepo = filepath.Base(root)
		}
	}
	gatePolicy, _, err := ReadGatePolicy(cfg.Root)
	if err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	var clockMu sync.Mutex
	now := cfg.Now
	cfg.Now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now() }
	if cfg.Report == nil {
		cfg.Report = io.Discard
	}
	// Read the process identity now, outside every store transaction; each later
	// claim and reap answers it from memory.
	if _, err := currentLoopOwner(); err != nil {
		return nil, err
	}
	l := &Loop{gatePolicy: gatePolicy, cfg: cfg, gateBudget: budget, build: ReadBuildStamp(), held: map[string]heldItem{}, active: map[string]bool{}, activeCancels: map[string]context.CancelFunc{}, stoppedRuns: map[string]bool{}, laneConditions: map[string]*laneCondition{}, infraLines: map[string]string{}, completions: make(chan runCompletion, len(cfg.Lanes)), laneChanges: map[string]int{}, staleBaseChecked: map[string]bool{}}
	if cfg.PolicyRef != "" {
		p, raw, hash, err := readPolicyContent(cfg.PolicyRef)
		if err != nil {
			return nil, err
		}
		l.cfg.Policy = p
		l.policyState = PolicyState{Ref: cfg.PolicyRef, Hash: hash, DiskHash: hash, Content: string(raw)}
	}
	if l.cfg.Policy != nil && l.cfg.Policy.Root == "" && cfg.Root != "" {
		policyCopy := *l.cfg.Policy
		policyCopy.Root = cfg.Root
		l.cfg.Policy = &policyCopy
	}
	for _, diagnostic := range WorkflowDefaultDiagnostics(cfg.Root, cfg.WorkflowDefaults, l.cfg.Policy) {
		l.reportf("workflow-invalid: %s\n", diagnostic)
	}
	return l, nil
}

// Run drives ticks until a stopping condition, and answers what happened.
//
// There is no budget in this loop: it runs while the pile has work it may dispatch, and it
// waits by default when the pile has nothing dispatchable. With ExitWhenDrained it stops
// when nothing is dispatchable and no lane is held (queue-drained),
// when an operator stops it (the context is canceled, which main wires to SIGINT/SIGTERM),
// when a lane it may dispatch onto is held with no expiry (lanes-leased), or on a fault it
// cannot route around. A lane on a finite lease is waited on, not stopped for. See
// docs/design/momentum-principle.md — the pile is the only throttle, and a limit on how much
// work the service may do is a limit on the pile, not on the service.
//
// A run error is not a stop: it is a step that could not be completed (a store that would
// not open, a git that would not answer), and the caller reads it as exit 5 unless it was
// classified. Every honest stop is a LoopResult with a reason, and the reason is written to
// disk before the caller is answered (recordResult), so a stop is readable after the process
// that made it is gone.
func (l *Loop) Run(ctx context.Context) (result LoopResult, err error) {
	// Adopt before starting any new child, so inherited-child collection cannot
	// steal a status from this image's subprocess waiters.
	fmt.Fprintf(l.cfg.Report, "loop: build %s\n", l.build)
	if err := l.adoptRestart(ctx); err != nil {
		return faultResult(result, err)
	}
	defer l.recordResult(&result)
	defer func() {
		if spendErr := l.snapshotSpend(&result); spendErr != nil {
			l.reportf("loop: spend snapshot unavailable: %v\n", spendErr)
		}
	}()
	defer l.closeWALKeeper()
	defer l.endSession()
	l.keepWALWhileRunning()
	if l.cfg.Policy == nil || len(l.cfg.Policy.Backends) == 0 {
		return faultResult(result, Usagef("the loop has no usable policy"))
	}
	if err := l.recordEndingCapabilities(ctx); err != nil {
		if ctx.Err() != nil {
			result.Stop, result.Detail = StopOperatorStop, "the loop was asked to stop"
			return result, nil
		}
		return faultResult(result, err)
	}
	if err := l.refreshSession(ctx, l.cfg.Now().Add(PassInterval)); err != nil {
		if ctx.Err() != nil {
			result.Stop, result.Detail = StopOperatorStop, "the loop was asked to stop"
			return result, nil
		}
		return faultResult(result, err)
	}
	ctx, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(nil) }
	l.setSessionStop(func() { cancelCause(errStopRequested) })
	defer l.setSessionStop(nil)
	if stopped, err := l.stopRequested(ctx); err != nil {
		if stoppedByOperator(ctx, err) {
			result.Stop, result.Detail = StopOperatorStop, "the loop was asked to stop"
			return result, nil
		}
		return faultResult(result, err)
	} else if stopped {
		l.signalSessionStop()
		result.Stop, result.Detail = StopOperatorStop, "the loop was asked to stop"
		return result, nil
	}
	stopHeartbeat := l.keepSessionAlive(ctx, func() { cancelCause(errHeartbeatFailed) })
	defer func() {
		// The heartbeat's failure is reported beside the error Run already has, labelled
		// as the heartbeat's, never in its place: replacing it hid what startup was doing.
		if heartbeatErr := stopHeartbeat(); heartbeatErr != nil {
			heartbeatErr = fmt.Errorf("session heartbeat: %w", heartbeatErr)
			if err != nil {
				heartbeatErr = errors.Join(err, heartbeatErr)
			}
			result, err = faultResult(result, heartbeatErr)
		}
	}()
	// Deferred calls run last-first: the end-runs sweep below runs after the
	// examinations are cancelled and drained, so a wall continuation an
	// examination was about to start cannot start after the sweep.
	defer func() {
		if stopErr := l.endRequestedRuns(); stopErr != nil {
			result, err = faultResult(result, stopErr)
		}
	}()
	defer func() {
		cancel()
		// A worker-end handoff registers under mu only while its context is
		// live. Cross that admission point before joining the examinations.
		l.mu.Lock()
		l.mu.Unlock()
		// Judging workers own short-lived leases, not model lifetimes. Finish
		// their cancellation cleanup before this process exits.
		l.examinations.Wait()
		l.heavyWG.Wait()
	}()
	// The result is written on every way out, including the failing one: a loop that stopped
	// on a store it could not open is a stop an operator needs the record of, and a file
	// written only on the clean path would answer "why did it stop" for everything except
	// the stops that matter most.
	// Cost is observation only. An unavailable measurement is explicit, but
	// cannot withhold work that the planner would otherwise dispatch.
	if window, err := spend.Read(ctx, l.cfg.Store, l.cfg.Now()); err != nil {
		l.reportf("loop: spend, last 24h: unavailable: %v\n", err)
	} else {
		l.reportf("loop: %s\n", window)
	}
	if err := l.startDefinition(ctx); err != nil {
		return faultResult(result, err)
	}
	// Runs that ended while no loop was running have no saved class yet.
	if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
		return faultResult(result, err)
	}
	// The documents are synced before anything is planned: an item block committed since
	// the last drain is work this drain should see, and the sync is the only way an item
	// enters the store. A finding does not stop the loop — the documents without one were
	// written, and the rest of the queue is still work — but a sync that cannot run at all
	// is a fault, because a drain over a queue it could not read is a drain over a guess.
	if _, err := l.refreshDocuments(ctx, &result); err != nil {
		if stoppedByOperator(ctx, err) {
			result.Stop = StopOperatorStop
			result.Detail = "the loop was asked to stop"
			return result, nil
		}
		return faultResult(result, err)
	}
	// The lanes are staged once, before the first tick: a bracket names its sibling lanes,
	// and a lane that does not exist yet cannot be watched. A stop that arrives during
	// staging is the operator's stop, not a staging failure: the signal is why the git
	// worktree add was abandoned.
	if err := l.stageLandingLanes(ctx); err != nil {
		if stoppedByOperator(ctx, err) {
			result.Stop = StopOperatorStop
			result.Detail = "the loop was asked to stop"
			return result, nil
		}
		return faultResult(result, err)
	}
	if l.cfg.MigrateOwnerQuestions {
		if err := l.migrateOwnerQuestions(ctx, &result); err != nil {
			return faultResult(result, err)
		}
		result.Stop, result.Detail = StopQueueDrained, "owner question migration finished"
		return result, nil
	}
	view := newStartView()
	if !l.pollOnly {
		view.storePoll = l.cfg.StorePollInterval
	}
	l.startView.Store(view)
	defer l.startView.Store(nil)
	defer l.watchStartView(ctx, view, startViewRefreshInterval)()
	stopStarts := func() {}
	if !l.pollOnly {
		stopStarts = l.startBackgroundCoordinator(ctx, view, &result)
	}
	err = l.runPasses(ctx, &result, !l.pollOnly, view)
	// Join staging before copying the result or ending operator-requested runs.
	stopStarts()
	if err != nil {
		return faultResult(result, err)
	}
	return result, nil
}

func (l *Loop) runPasses(ctx context.Context, result *LoopResult, pendingOnly bool, view *startView) error {
	for {
		if err := ctx.Err(); err != nil {
			result.Stop = StopOperatorStop
			result.Detail = "the loop was asked to stop"
			return nil
		}
		l.mu.Lock()
		dispatched := result.Dispatched
		if !pendingOnly {
			l.stageFault = false
		}
		l.mu.Unlock()
		done, err := l.passMode(ctx, result, pendingOnly)
		if err != nil {
			// A stop that arrived while the tick was reading the store is still the
			// operator's stop, not a store failure: the signal is why the write was
			// abandoned, and the record says so.
			if stoppedByOperator(ctx, err) {
				result.Stop = StopOperatorStop
				result.Detail = "the loop was asked to stop"
				return nil
			}
			return err
		}
		if done != "" {
			result.Stop = done
			return nil
		}
		// The backoff applies when a tick dispatched nothing and the reason is the loop's
		// own machinery: the same plan will be attempted again, and asking a broken harness
		// faster does not make it work. It is deliberately conditional on the dispatch
		// count, because it must never ration work: when a tick dispatched anything, or when
		// the pile answered "nothing dispatchable", the loop has a real answer and does not
		// wait at all.
		l.mu.Lock()
		backOff := result.Dispatched == dispatched && l.stageFault && l.infraStreak > 0 && len(l.active) == 0
		if backOff && !pendingOnly {
			l.stageFault = false
		}
		l.mu.Unlock()
		if backOff {
			waited, err := l.backOff(ctx)
			if err != nil && ctx.Err() == nil {
				return err
			}
			if !waited {
				result.Stop = StopOperatorStop
				result.Detail = "the loop was asked to stop while waiting out an infrastructure failure"
				return nil
			}
			if pendingOnly {
				l.mu.Lock()
				l.stageFault = false
				l.mu.Unlock()
				view.notifyUpdate()
			}
		}
	}
}

// startDefinition reads the store before any Git work. Old stores resolve to
// the embedded Git-landing definition through ForReader.
func (l *Loop) startDefinition(ctx context.Context) error {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var d definition.Definition
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		declared, err := definition.ForReader(r)
		if err == nil {
			d = declared.Parsed
		}
		return err
	})
	s.Close()
	if err != nil {
		return err
	}
	l.nonLanding = d.Completes != "landing"
	if l.nonLanding {
		if steps, ok := l.cfg.Steps.(interface{ ConfigureNonLanding() }); ok {
			steps.ConfigureNonLanding()
		}
		if _, err := settings.ReadWithoutLanding(l.cfg.Root, d.Name); err != nil {
			return Usagef("%v", err)
		}
		l.reportf("loop: definition: %s; skipped Git, item sync, trunk history, Git lane staging, rolling tier, roll and push\n", d.Name)
	}
	return nil
}

func (l *Loop) stageLandingLanes(ctx context.Context) error {
	if l.nonLanding {
		return nil
	}
	if err := l.stageLanes(ctx); err != nil {
		return err
	}
	return l.registerLanePools(ctx)
}

// refreshDocuments syncs before planning at a new trunk commit and retains the lazy
// question reader for all passes at that commit. A concurrent landing during the sync
// invalidates that read: retry at the new tip rather than label mixed inputs as synced.
func (l *Loop) refreshDocuments(ctx context.Context, result *LoopResult) (string, error) {
	if l.nonLanding {
		return "", nil
	}
	for {
		sha, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return "", err
		}
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return "", err
		}
		var clearSeq int64
		err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; clearSeq, err = syncClearVersion(r); return err })
		s.Close()
		if err != nil {
			return "", err
		}
		if sha == l.syncedSHA && clearSeq == l.syncClearSeq {
			if err := l.settleOwnerAnswers(ctx, sha); err != nil {
				return "", err
			}
			return sha, nil
		}
		if err := l.syncDocuments(ctx, result); err != nil {
			return "", err
		}
		after, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return "", err
		}
		if after != sha {
			continue
		}
		l.syncedSHA = sha
		l.syncClearSeq = clearSeq
		l.waitingDetail = ""
		l.resetPassQuestions(ctx)
		if err := l.settleOwnerAnswers(ctx, sha); err != nil {
			return "", err
		}
		result.SyncedSHA = sha
		l.reportf("loop: synced documents at %s\n", sha)
		return sha, nil
	}
}

// The pass's question source is loop-owned and pinned to its synced SHA.
func (l *Loop) resetPassQuestions(ctx context.Context) {
	l.questions = newSavedQuestions(l.cfg.Root, l.syncedSHA, l.cfg.QuestionsCmd, ctx)
}

func (l *Loop) designerQuestions(ctx context.Context) QuestionSource {
	if l.questions == nil {
		l.resetPassQuestions(ctx)
	}
	return l.questions
}

// syncDocuments reports one summary line and each finding and frozen definition
// before any decision is made from the refreshed queue.
func (l *Loop) syncDocuments(ctx context.Context, result *LoopResult) error {
	// A reverted landing's verdicts would freeze the definition this sync applies.
	if err := l.withdrawRevertedVerdicts(ctx); err != nil {
		return err
	}
	outcome, err := l.cfg.Steps.Sync(ctx)
	if err != nil {
		return err
	}
	result.Sync = &outcome
	l.reportf("loop: %s\n", outcome.Line())
	for _, n := range outcome.Frozen {
		l.reportf("loop: sync frozen: %s\n", n)
	}
	for _, n := range outcome.Findings {
		l.reportf("loop: sync finding: %s\n", n)
	}
	for itemID, detail := range outcome.ItemFaults {
		if err := l.holdItemFault(ctx, Candidate{ItemID: itemID}, ReasonDefinitionSyncFailed, detail); err != nil {
			return err
		}
	}
	return l.settleDefinitionHolds(ctx, outcome)
}

// errHeartbeatFailed is the cause the session heartbeat cancels Run's work with. A step
// abandoned for it was stopped by the heartbeat's failure, not by the operator, so its
// error is kept and reported beside the heartbeat's.
var errHeartbeatFailed = errors.New("the session heartbeat failed")

// errStopRequested is the cause Run's context is canceled with when drain_sessions
// records stop_requested > 0.
var errStopRequested = errors.New("the loop session was asked to stop")

// operatorStopped answers whether ctx was canceled by the operator (a signal, or the
// caller's own cancel) rather than by a failed heartbeat.
func operatorStopped(ctx context.Context) bool {
	return ctx.Err() != nil && !errors.Is(context.Cause(ctx), errHeartbeatFailed)
}

// signalGrace is how long an error that reads as a child killed by SIGINT or SIGTERM waits
// for the loop's own context to be canceled before it is reported as a fault.
var signalGrace = 5 * time.Second

// stoppedByOperator answers whether err is the operator's stop. A terminal or supervisor
// signal goes to the loop's whole process group, so a child the loop is running (a `git
// ls-files`, a `git worktree add`) can die of it before the loop's own signal handler has
// canceled ctx. That error is the stop, not a fault: the loop's own copy of the signal is
// already on its way, so an error that reads as a child killed by SIGINT or SIGTERM waits for
// ctx to be canceled. If it never is, the signal was aimed at the child alone and the error
// is a fault after all.
func stoppedByOperator(ctx context.Context, err error) bool {
	if operatorStopped(ctx) || errors.Is(err, errStopRequested) {
		return true
	}
	if err == nil || !diedOfStopSignal(err) {
		return false
	}
	timer := time.NewTimer(signalGrace)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return operatorStopped(ctx)
	case <-timer.C:
		return false
	}
}

// diedOfStopSignal answers whether err reports a child process ended by SIGINT or SIGTERM.
// Errors cross the loop's layers as text, so the exit status is read from the chain when it
// survives and from the standard `signal: terminated` wording when it does not.
func diedOfStopSignal(err error) bool {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal() == syscall.SIGTERM || status.Signal() == syscall.SIGINT
		}
	}
	text := err.Error()
	return strings.Contains(text, "signal: terminated") || strings.Contains(text, "signal: interrupt")
}

// faultResult names a run error in the result the deferred record writes, and answers the
// pair Run returns. Without it a loop that could not open its store wrote a record whose
// stop, detail and status were empty — a file that exists and says nothing, which reads as a
// loop that stopped for no reason rather than as one that never started.
func faultResult(result LoopResult, err error) (LoopResult, error) {
	result.Stop = StopFault
	result.Detail = err.Error()
	result.ExitCode = ExitCode(err)
	return result, err
}

// backOff waits before the next attempt after consecutive infrastructure failures, and
// answers whether the wait finished: false is the caller's context being done, and the loop
// stops then rather than sleeping out a wait an operator no longer wants. Every failure was
// already reported when it happened; this line says how long the loop is waiting, so a
// quiet log reads as a waiting loop rather than a hung one.
func (l *Loop) infrastructureStreak() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.infraStreak
}

func (l *Loop) backOff(ctx context.Context) (bool, error) {
	streak := l.infrastructureStreak()
	delay := BackoffDelay(streak, l.cfg.BackoffBase)
	l.reportf("loop: %d consecutive infrastructure failure(s) and nothing dispatched; waiting %s before the next attempt\n",
		streak, delay)
	if err := l.beat(ctx, l.cfg.Now().Add(delay)); err != nil {
		return false, err
	}
	if l.cfg.Comments == nil {
		return waitFor(ctx, delay), nil
	}
	// Keep infrastructure retry backoff intact while review intake remains awake.
	deadline := time.Now().Add(delay)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			return true, nil
		}
		interval := time.Duration(l.cfg.Comments.PollSeconds) * time.Second
		if left < interval {
			interval = left
		}
		if !waitFor(ctx, interval) {
			return false, nil
		}
		if err := l.intakeComments(ctx); err != nil {
			return false, err
		}
	}
}

// planNothing is the tick's stop check: a full tick that dispatched nothing is either the
// queue's answer or a lane's, and the two are different facts.
//
// A lane held by a finite lease is a wait, not a stop: the loop's own work is waiting on
// the clock, so the honest answer is to wait until the earliest expiry and re-plan. A lane
// held with no expiry is a different fact — no clock will free it, so the operator's
// `tillr run release` is the missing act, and the loop stops with a reason that says so.
//
// `queue-drained` is reserved for the row with no live lease on any lane the loop offers,
// which is design §4.3's "0 ready and 0 active leases": the first night's drain reported it
// while every held item was `no-lane` behind a lane a killed run still held, and the word
// then told the operator the opposite of the truth.
func (l *Loop) planNothing(ctx context.Context, holds []Decision, leases []LaneLease, at time.Time, result *LoopResult) (string, error) {
	held := heldDetail(holds)
	for _, d := range holds {
		if strings.HasPrefix(d.Reason, ReasonTrunkPathDirty+": ") || strings.HasPrefix(d.Reason, ReasonTrunkRewritten+": ") || strings.HasPrefix(d.Reason, ReasonForeignIdentity+": ") || strings.HasPrefix(d.Reason, ReasonRedCap+": ") {
			result.Detail = held
			return l.waitForTrigger(ctx, leases, at, result)
		}
	}
	if len(leases) == 0 {
		detail := fmt.Sprintf("a full tick planned nothing dispatchable: %s", held)
		if !l.cfg.ExitWhenDrained {
			if l.waitingDetail != detail {
				l.reportf("loop: a full tick planned nothing dispatchable: %s; waiting for a trigger\n", waitingHolds(holds))
			}
			l.waitingDetail = detail
			result.Detail = detail
			return l.waitForTrigger(ctx, leases, at, result)
		}
		l.reportf("loop: %s\n", detail)
		result.Detail = detail
		return StopQueueDrained, nil
	}
	for _, lease := range leases {
		if lease.LeasedUntil == nil {
			detail := fmt.Sprintf("a full tick planned nothing dispatchable and %s is held with no expiry: %s; the missing act is `tillr run release %s`, which is the one thing no clock can do",
				lease.LaneID, held, lease.LaneID)
			l.reportf("loop: %s\n", detail)
			result.Detail = detail
			return StopLanesLeased, nil
		}
	}
	l.reportf("loop: %s; waiting for a lease expiry or new store fact before re-planning\n", held)
	return l.waitForTrigger(ctx, leases, at, result)
}

func holdText(hold Decision) string {
	if hold.BackendHoldCause != "" {
		return fmt.Sprintf("%s (%s: %s: %s; mover: %s)", hold.ItemID, hold.Reason, hold.DeferredBy, hold.BackendHoldCause, hold.BackendHoldMover)
	}
	if hold.DeferredBy != "" {
		return fmt.Sprintf("%s (%s behind %s)", hold.ItemID, hold.Reason, hold.DeferredBy)
	}
	return fmt.Sprintf("%s (%s)", hold.ItemID, hold.Reason)
}

// waitingHolds bounds the idle line to five examples while retaining the total.
func waitingHolds(holds []Decision) string {
	var examples []string
	for i, hold := range holds {
		if i == 5 {
			break
		}
		text := holdText(hold)
		examples = append(examples, text)
	}
	return fmt.Sprintf("%d held item(s): %s", len(holds), strings.Join(examples, ", "))
}

// heldDetail is one tick's holds as the operator reads them: each item, and the plan's own
// reason for holding it. The reasons are the closed vocabulary plan emits, so the detail
// says which rule held which item rather than repeating the rule's prose eight ways.
func heldDetail(holds []Decision) string {
	if len(holds) == 0 {
		return "the store holds no item, so there was nothing to plan"
	}
	var held, deferred []string
	for _, hold := range holds {
		text := holdText(hold)
		if hold.Reason == ReasonNoLane || hold.Reason == ReasonForkHeld {
			deferred = append(deferred, text)
		} else {
			held = append(held, text)
		}
	}
	return fmt.Sprintf("%d held item(s): %s; %d deferred item(s): %s", len(held), strings.Join(held, ", "), len(deferred), strings.Join(deferred, ", "))
}

// laneOutcome is one lane's pipeline as its goroutine reports it: the stop the lane
// reached, or the error that ended it. A lane that neither stopped nor failed answers the
// zero value.
type laneOutcome struct {
	stop        string
	err         error
	laneRefusal *LaneRefusalError
}

// holdLocal captures the item content on a staging refusal, not its place in line.
func (l *Loop) holdLocal(ctx context.Context, itemID, reason string) {
	hold := heldItem{reason: reason, at: l.cfg.Now().UTC()}
	if s, err := store.OpenRead(ctx, l.cfg.Store); err == nil {
		defer s.Close()
		_ = s.ReadTx(ctx, func(r *store.Reader) error {
			version, err := localHoldVersion(r, itemID)
			hold.contentVersion = version
			return err
		})
	}
	l.mu.Lock()
	l.held[itemID] = hold
	l.mu.Unlock()
	if v := l.startView.Load(); v != nil {
		v.noteItemHold(itemID, hold)
	}
}

func localHoldVersion(r *store.Reader, itemID string) (string, error) {
	schema, err := readVersionSchema(r)
	if err != nil {
		return "", err
	}
	return readItemContentVersion(r, schema, itemID)
}

// heldFor drops the hold on a content edit, not a rerank. A record that cannot
// be read keeps the hold, which is the safe direction.
func (l *Loop) heldFor(r *store.Reader, itemID string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hold, held := l.held[itemID]
	if !held {
		return "", false
	}
	drop := func() (string, bool) {
		if lane, ok := strings.CutPrefix(hold.reason, ReasonStagingFailed+": "); ok {
			delete(l.infraLines, itemID+"\x00"+lane)
		} else if hold.reason == ReasonStagingFailed {
			delete(l.infraLines, itemID+"\x00")
		}
		delete(l.held, itemID)
		return "", false
	}
	if hold.contentVersion != "" {
		version, err := localHoldVersion(r, itemID)
		if err != nil {
			return hold.reason, true
		}
		if version != hold.contentVersion {
			return drop()
		}
	} else {
		var updatedAt int64
		if err := r.QueryRow(`SELECT updated_at FROM items WHERE item_id = ?`, itemID).Scan(&updatedAt); err != nil {
			return hold.reason, true
		}
		if updatedAt > hold.at.UnixMilli() {
			return drop()
		}
	}
	if strings.HasPrefix(hold.reason, ReasonStagingFailed) {
		var hasRetries bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='retries')`).Scan(&hasRetries); err == nil && hasRetries {
			var retryAt int64
			if err := r.QueryRow(`SELECT COALESCE(MAX(requested_at), 0) FROM retries WHERE item_id = ?`, itemID).Scan(&retryAt); err == nil && retryAt > hold.at.UnixMilli() {
				return drop()
			}
		}
		if lane, ok := strings.CutPrefix(hold.reason, ReasonStagingFailed+": "); ok && lane != "" {
			var restoredAt int64
			if err := r.QueryRow(`SELECT COALESCE(MAX(ts), 0) FROM events WHERE kind = ? AND object_type = 'lane' AND object_id = ?`, laneRestoredKind, lane).Scan(&restoredAt); err == nil && restoredAt > hold.at.UnixMilli() {
				return drop()
			}
		}
	}
	return hold.reason, true
}

func (l *Loop) prepareContinuedLane(ctx context.Context, decision Decision, itemID, expectedHead string) (bool, error) {
	target := l.laneTarget(decision.LaneID)
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return false, err
	}
	if head == expectedHead {
		return false, nil
	}
	status, err := l.git(ctx, target, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(status) != "" {
		first := strings.TrimSpace(strings.Split(strings.TrimSpace(status), "\n")[0])
		return false, laneRefusalf(decision.LaneID, ReasonLaneUncommittedWork,
			"commit or remove the files, or reset the lane deliberately",
			"lane %s holds uncommitted work (%s)", decision.LaneID, first)
	}
	if target != l.lanePath(decision.LaneID) {
		stager := &LaneStager{Root: l.cfg.Root, Store: l.cfg.Store, LaneRoot: l.cfg.LaneRoot, Now: l.cfg.Now}
		err := stager.restageForkAtKeptHead(ctx, StageSpec{LaneID: decision.LaneID, ItemID: itemID, KeptHeadSHA: expectedHead}, ForkTarget{Checkout: target, Ref: l.lanePatchRef(decision.LaneID)})
		return err == nil, err
	}
	contained, err := containsCommit(ctx, l.cfg.Root, head, expectedHead)
	if err != nil {
		return false, err
	}
	if !contained {
		contained, err = containsCommit(ctx, l.cfg.Root, head, TrunkRef)
		if err != nil {
			return false, err
		}
	}
	if !contained {
		stager := &LaneStager{Root: l.cfg.Root, Store: l.cfg.Store, LaneRoot: l.cfg.LaneRoot, Now: l.cfg.Now}
		clearable, err := stager.clearableLaneCopy(ctx, decision.LaneID, itemID, head, "")
		if err != nil {
			return false, err
		}
		if !clearable {
			return false, unlandedLaneRefusal(decision.LaneID, head, expectedHead)
		}
	}
	if _, err := l.git(ctx, target, "reset", "--hard", expectedHead); err != nil {
		return false, err
	}
	if _, err := l.git(ctx, target, "clean", "-fd"); err != nil {
		return false, err
	}
	return true, nil
}

// stageDispatches stages the pass's claimed dispatches and returns those that may run.
// Each lane is staged before its own run starts; siblings need not wait for it.
//
// The two kinds of failure are held apart, because they are different facts and they earn
// different answers:
//
//   - The item's own record is what refused (`IsItemContent`: an empty title, an absent
//     criterion, an absent fence, or a brace pair in one of those fields). Staging that
//     record again fails the same way, so retrying is not a repair and waiting cannot fix
//     it. The loop records a per-item refusal and holds that one item; the rest of the
//     pass still runs, so one bad record does not stall the queue.
//   - Anything else (a template that will not render, a git that will not answer) is the
//     machinery's fault. Nothing about the item was learned, so it is infrastructure-class:
//     it feeds the backoff and leaves the item in the plan for the next tick.
func isPerItemStageFailure(spec StageSpec, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENAMETOOLONG) {
		return true
	}
	msg := err.Error()
	if strings.Contains(strings.ToLower(msg), "file name too long") {
		return true
	}
	if spec.RunID != "" && strings.Contains(msg, spec.RunID) {
		return true
	}
	if spec.ItemID != "" && strings.Contains(msg, spec.ItemID) {
		return true
	}
	return false
}

// beginLaneStaging excludes a writer from sibling snapshots and invalidates
// observations taken before or during staging, even when it fails.
func (l *Loop) beginLaneStaging(lane string) func() {
	l.mu.Lock()
	if l.stagingLanes == nil {
		l.stagingLanes = map[string]int{}
	}
	l.stagingLanes[lane]++
	l.bumpLaneEpoch(lane)
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		l.stagingLanes[lane]--
		l.bumpLaneEpoch(lane)
		l.mu.Unlock()
	}
}

func (l *Loop) stageLane(ctx context.Context, spec StageSpec) error {
	end := l.beginLaneStaging(spec.LaneID)
	defer end()
	return l.cfg.Stage.Stage(ctx, spec)
}

func (l *Loop) stageDispatches(ctx context.Context, decisions []Decision, mainSHA string, result *LoopResult) ([]Decision, error) {
	staged := make([]Decision, 0, len(decisions))
	for _, decision := range decisions {
		request := decision.RunRequest
		if evaluatorNode(decision) && !verifyNoVerdictReturn(decision.Returned) {
			// The judge owns this node's staging, including the verdict location.
			// A returned evaluator first needs its pinned candidate staged, possibly
			// on a cold lane, before the judge can prepare its own inputs.
			staged = append(staged, decision)
			continue
		}
		failStage := func(step string, stepErr error) error {
			if refusal := asLaneRefusal(stepErr); refusal != nil {
				if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
					return err
				}
				l.mu.Lock()
				l.laneRefusedNow = true
				l.mu.Unlock()
				return nil
			}
			return l.holdStageFailed(ctx, decision, step, stepErr)
		}
		var endingCapability string
		if request.Backend != "command" && len(request.Command) > 0 {
			endingCapability = l.endingCapabilities[request.Command[0]]
		}
		spec := StageSpec{
			EndingCapability: endingCapability,
			Policy:           l.currentPolicy(),
			LaneID:           decision.LaneID, ItemID: request.ItemID, RunID: request.RunID,
			Backend: request.Backend, BaseSHA: mainSHA, WorkingDir: request.WorkingDir,
			TimeoutMS: request.TimeoutMS, BlockConflict: decision.BlockConflict,
			Returned: decision.Returned, RepeatedCall: decision.RepeatedCall,
			RetryCandidate: decision.RetryCandidate,
		}
		if decision.DesignContinuationFrom != "" {
			spec.SessionID = decision.DesignerSessionID
			spec.DesignContinuationHead = decision.DesignContinuationHead
		}
		if err := l.stageWorkerSession(&decision, &spec); err != nil {
			if holdErr := failStage("stage worker session", err); holdErr != nil {
				return nil, holdErr
			}
			continue
		}
		if err := l.stageLandingAnswer(ctx, decision, &spec); err != nil {
			if holdErr := failStage("stage landing answer", err); holdErr != nil {
				return nil, holdErr
			}
			continue
		}
		freshCandidateRetry := decision.RetryCandidate != nil && decision.RetryCandidate.Available
		if freshCandidateRetry {
			spec.KeptHeadSHA = decision.RetryCandidate.HeadSHA
			spec.KeptItemBaseSHA = decision.RetryCandidate.BaseSHA
		}
		freshVerifyReturn := (decision.Reason == ReasonReturned || decision.Action == ActionRetry) && (verifyBuildReturn(decision.Returned) || verifyNoVerdictReturn(decision.Returned)) && !request.SessionTurn
		if freshVerifyReturn {
			// A fresh build run keeps the candidate the verifier read, even on a cold lane.
			s := decision.Returned.WorkerSession
			spec.KeptHeadSHA = s.HeadSHA
			spec.KeptItemBaseSHA = s.BaseSHA
		} else if decision.Reason == ReasonReturned && decision.Returned != nil && !request.SessionTurn &&
			((decision.Returned.WorkerSession != nil && decision.LaneID == decision.Returned.WorkerSession.LaneID) ||
				(decision.Workflow != nil && decision.NodeIndex == 0)) {
			// A returned run that continues no session starts from a clean lane. A workflow
			// restarted at its first stage is cut from the node base, so a commit the earlier
			// candidate left on the lane (it stays pinned) cannot turn a read-only first stage
			// into a boundary violation.
			revision := TrunkRef
			if decision.Workflow != nil && decision.NodeBaseSHA != "" {
				revision = decision.NodeBaseSHA
			}
			// A cold lane has no worktree to reset; Stage creates it.
			if _, statErr := os.Stat(l.lanePath(decision.LaneID)); statErr != nil {
				revision = ""
			}
			if revision != "" {
				if err := l.resetLaneTo(ctx, decision.LaneID, revision); err != nil {
					if holdErr := failStage("reset lane", err); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
			}
		}
		if err := l.stageComment(ctx, &decision, &spec); err != nil {
			if holdErr := failStage("stage comment", err); holdErr != nil {
				return nil, holdErr
			}
			continue
		}
		revertedSession := decision.Returned != nil && decision.Returned.Cause == RefusalReverted && request.SessionTurn
		// A retry that keeps the readers its earlier attempt passed starts a new attempt at
		// a later node (retainedReaders), on the current trunk and on whatever lane the plan
		// offered. Its first node is staged fresh, like node 0: it continues no lane state,
		// so the lane is not required to sit at this attempt's node start.
		freshRetry := len(decision.ReusedNodes) > 0 && !decision.WorkflowRestart
		resumeCommitted := false
		if decision.Workflow != nil {
			node := decision.effectiveNode()
			spec.Node = &node
			spec.nodeFenceWidened = len(decision.NodeFencePaths) > 0
			paths, err := l.nodeInputPaths(ctx, decision)
			if err != nil {
				if holdErr := failStage("stage node inputs", err); holdErr != nil {
					return nil, holdErr
				}
				continue
			}
			spec.InputPaths = paths
			if !revertedSession {
				spec.Continue = spec.Continue || (decision.NodeIndex > 0 && !freshRetry && !freshVerifyReturn && decision.RetryCandidate == nil) || decision.WorkflowRestart
			}
			if decision.NodeIndex == 1 && classificationNode(decision.Workflow.Stages[0]) && l.laneForkTarget(decision.LaneID) == "" && spec.KeptHeadSHA == "" {
				// The classifier had only a repository checkout. Start the fork
				// worker (if selected) from its own independently pinned base.
				spec.Continue = false
			}
			if spec.Continue {
				spec.PreviousRunID = decision.NodeStartRunID
				if decision.RepeatedCall != nil && decision.RepeatedCall.RunID != "" {
					spec.PreviousRunID = decision.RepeatedCall.RunID
				} else if decision.Returned != nil && decision.Returned.RunID != "" {
					spec.PreviousRunID = decision.Returned.RunID
				} else if spec.PreviousRunID == "" {
					if prev, prevErr := l.stagedNodeRun(decision); prevErr == nil && prev != "" {
						spec.PreviousRunID = prev
					} else if pred, predErr := l.workflowPredecessorRun(ctx, decision); predErr == nil && pred != "" {
						spec.PreviousRunID = pred
					}
				}
			}
			if decision.WorkflowRestart && len(decision.Parallel) > 0 && decision.NodeIndex == 0 {
				previous, err := l.stagedNodeRun(decision)
				if err != nil {
					if holdErr := failStage("read staged node run", err); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
				if previous == "" {
					spec.Continue = false
				}
			}
			if decision.WorkflowRestart && len(decision.Parallel) == 0 && !l.nonLanding {
				previous, stagedErr := l.stagedNodeRun(decision)
				if stagedErr != nil {
					if holdErr := failStage("read staged node run", stagedErr); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
				// A crash before first staging owns no existing lane contents.
				// Keep the ordinary unlanded-commit refusal in that case.
				if previous == "" && decision.NodeIndex == 0 {
					spec.Continue = false
				} else {
					target := l.laneTarget(decision.LaneID)
					nodeBase := decision.NodeBaseSHA
					if decision.NodeIndex == 0 && decision.NodeStartRunID == "" && previous != "" {
						raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, previous), BaseSHAFile))
						if err != nil {
							if holdErr := failStage("read staged base", err); holdErr != nil {
								return nil, holdErr
							}
							continue
						}
						nodeBase = strings.TrimSpace(string(raw))
						spec.BaseSHA = nodeBase
					}
					if decision.NodeIndex == 0 {
						spec.BaseSHA = nodeBase
					}
					if nodeBase == "" {
						nodeBase = l.laneBase(decision.LaneID)
					}
					if previous != "" && restartWriter(decision) {
						itemBase, scopeBase, err := l.committedRestartBases(ctx, decision.LaneID, nodeBase)
						if err != nil {
							if holdErr := failStage("inspect restart lane", err); holdErr != nil {
								return nil, holdErr
							}
							continue
						}
						if itemBase != "" {
							resumeCommitted = true
							spec.KeptItemBaseSHA = itemBase
							nodeBase, decision.NodeBaseSHA = scopeBase, scopeBase
						}
					}
					if err := l.preserveWorkFromBase(ctx, decision.LaneID, previous, nodeBase, "orphaned workflow restarted"); err != nil {
						if holdErr := failStage("preserve workflow lane", err); holdErr != nil {
							return nil, holdErr
						}
						continue
					}
					resetTo := nodeBase
					if resumeCommitted {
						resetTo = "HEAD"
					}
					if _, err := l.git(ctx, target, "reset", "--hard", resetTo); err != nil {
						if holdErr := failStage("reset workflow lane", err); holdErr != nil {
							return nil, holdErr
						}
						continue
					}
					if _, err := l.git(ctx, target, "clean", "-fd"); err != nil {
						if holdErr := failStage("clean workflow lane", err); holdErr != nil {
							return nil, holdErr
						}
						continue
					}
				}
			}
			// Report-only workflows retain their original handoff. Once a graph
			// declares named files, only explicit producer edges carry inputs,
			// except when a proposal-only predecessor proposed no graph replacement.
			if !decision.Workflow.hasNamedFiles() {
				spec.PreviousReport = decision.PreviousReport
			} else if decision.Workflow.proposalOnlyPredecessors(decision.NodeIndex) {
				reportPath, reportText, reportErr := l.proposalPredecessorReport(ctx, decision)
				if reportErr != nil {
					if holdErr := failStage("read predecessor report", reportErr); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
				spec.PreviousReport = reportPath
				spec.PreviousReportText = reportText
			}
		}
		if !l.nonLanding && spec.Continue && !decision.WorkflowRestart && !revertedSession && !freshRetry && !freshCandidateRetry {
			expectedHead := decision.NodeBaseSHA
			if request.SessionTurn && decision.Returned != nil && decision.Returned.WorkerSession != nil && decision.Returned.WorkerSession.HeadSHA != "" {
				expectedHead = decision.Returned.WorkerSession.HeadSHA
			}
			if expectedHead != "" {
				restaged, prepErr := l.prepareContinuedLane(ctx, decision, request.ItemID, expectedHead)
				if prepErr != nil {
					if holdErr := failStage("prepare continued lane", prepErr); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
				if restaged && l.cfg.Store != "" && spec.ItemBaseSHA == "" {
					if itemBase, baseErr := l.pendingItemBase(ctx, request.ItemID); baseErr == nil && itemBase != "" {
						spec.ItemBaseSHA = itemBase
					}
				}
			}
		}
		if !l.nonLanding && spec.Continue {
			prevRun := spec.PreviousRunID
			if prevRun == "" {
				if decision.RepeatedCall != nil && decision.RepeatedCall.RunID != "" {
					prevRun = decision.RepeatedCall.RunID
				} else if decision.Returned != nil && decision.Returned.RunID != "" {
					prevRun = decision.Returned.RunID
				}
			}
			if p, pErr := l.preservedWorkForDecision(ctx, decision, prevRun); pErr != nil {
				if holdErr := failStage("read preserved work", pErr); holdErr != nil {
					return nil, holdErr
				}
				continue
			} else if p != nil && p.LaneID != "" && p.LaneID != decision.LaneID {
				if applyErr := l.applyPreservedWork(ctx, l.laneTarget(decision.LaneID), *p); applyErr != nil {
					if holdErr := failStage("apply preserved work", applyErr); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
			}
		}
		observation, observeErr := pipelineStart(l.cfg.Root, spec.RunID, "stage")
		if observeErr != nil {
			if holdErr := failStage("start stage pipeline", observeErr); holdErr != nil {
				return nil, holdErr
			}
			continue
		}
		err := l.stageLane(ctx, spec)
		if finishErr := pipelineFinish(l.cfg.Root, spec.RunID, observation); finishErr != nil && err == nil {
			if holdErr := failStage("finish stage pipeline", finishErr); holdErr != nil {
				return nil, holdErr
			}
			continue
		}
		if err == nil {
			if compactErr := l.compactDesigner(ctx, decision); compactErr != nil {
				if holdErr := failStage("compact designer", compactErr); holdErr != nil {
					return nil, holdErr
				}
				continue
			}
			if restoreErr := l.restoreLane(ctx, decision.LaneID); restoreErr != nil {
				if holdErr := failStage("restore lane", restoreErr); holdErr != nil {
					return nil, holdErr
				}
				continue
			}
			l.clearStageInfraLine(request.ItemID, decision.LaneID)
			if decision.Workflow != nil {
				if decision.NodeBaseSHA == "" || (decision.NodeIndex == 0 && !request.SessionTurn && !resumeCommitted) || revertedSession {
					decision.NodeBaseSHA = l.laneBase(decision.LaneID)
				}
				decision.NodeBaseSHA = l.baseInCheckout(ctx, decision.LaneID, decision.NodeBaseSHA)
				decision.NodeStartRunID = decision.RunRequest.RunID
				if recErr := l.recordStagedNode(ctx, decision); recErr != nil {
					if holdErr := failStage("record staged node", recErr); holdErr != nil {
						return nil, holdErr
					}
					continue
				}
			}
			staged = append(staged, decision)
			continue
		}
		if stoppedByOperator(ctx, err) || ctx.Err() != nil {
			return nil, err
		}
		if refusal := asLaneRefusal(err); refusal != nil {
			// The lane's own state refused, not the item and not the machinery: the lane
			// leaves rotation and the item is offered another lane. No infrastructure
			// streak is counted, since nothing will change by waiting.
			if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
				return nil, err
			}
			l.mu.Lock()
			l.laneRefusedNow = true
			l.mu.Unlock()
			continue
		}
		var pinErr *runPinError
		if errors.As(err, &pinErr) || (!IsItemContent(err) && isPerItemStageFailure(spec, err)) {
			if holdErr := failStage("stage", err); holdErr != nil {
				return nil, holdErr
			}
			continue
		}
		if !IsItemContent(err) {
			l.mu.Lock()
			l.stageFault = true
			result.InfraFailures++
			l.infraStreak++
			streak := l.infraStreak
			key := request.ItemID + "\x00" + decision.LaneID
			repeated := l.infraLines[key] == err.Error()
			l.infraLines[key] = err.Error()
			l.mu.Unlock()
			if !repeated {
				l.reportf("loop: staging %s on %s failed in the infrastructure class (%d consecutive): %v\n",
					request.ItemID, decision.LaneID, streak, err)
				continue
			}
			reason := ReasonStagingFailed
			if decision.LaneID != "" {
				reason = ReasonStagingFailed + ": " + decision.LaneID
			}
			_, _, mover := whyPresentation(Decision{Action: ActionHold, Reason: reason})
			if mover == "" {
				mover = holdrules.For(reason).Mover
			}
			l.holdLocal(ctx, request.ItemID, reason)
			l.reportf("loop: holding %s on %s (%s): staging failure repeated unchanged; mover: %s: %v\n",
				request.ItemID, decision.LaneID, reason, mover, err)
			if err := l.recordItemRefusal(ctx, request.ItemID, reason, err.Error()); err != nil {
				return nil, err
			}
			continue
		}
		l.clearStageInfraLine(request.ItemID, decision.LaneID)
		// The record itself refused: hold this item and keep the rest of the pass. The
		// refusal is recorded before the hold, so a crash between the two still leaves the
		// event a reader needs.
		l.holdLocal(ctx, request.ItemID, ReasonItemUnstagleable)
		l.reportf("loop: holding %s on %s: its own record cannot be staged (%s): %v\n",
			request.ItemID, decision.LaneID, ReasonItemUnstagleable, err)
		if err := l.recordItemRefusal(ctx, request.ItemID, ReasonItemUnstagleable, err.Error()); err != nil {
			return nil, err
		}
	}
	return staged, nil
}

// runLane executes the already durably handed-off conflict repair inside its
// own bracket. A later pass owns settlement and judgment just as it does for
// ordinary dispatches.
func (l *Loop) runLane(ctx context.Context, decision Decision, mainSHA, tick string, at time.Time, result *LoopResult, timing *PassTiming, watched []string) laneOutcome {
	lane := decision.LaneID
	worktree := l.lanePath(lane)
	request := *decision.RunRequest
	stampDecisionRequest(decision, &request)
	before, err := l.snapshot(ctx, []string{worktree}, watched)
	if err != nil {
		return laneOutcome{err: err}
	}
	l.mu.Lock()
	result.Dispatched++
	tickNumber := result.Ticks
	l.mu.Unlock()
	l.reportf("loop: tick %d dispatch %s -> %s on %s\n",
		tickNumber, request.ItemID, request.Backend, lane)
	_, _, err = l.runRestartable(ctx, request)
	if err != nil {
		return laneOutcome{err: err}
	}
	stop, err := l.closeRunBracket(context.WithoutCancel(ctx), worktree, request, tick, at, before, result, watched)
	if err != nil || stop != "" {
		return laneOutcome{stop: stop, err: err}
	}
	return laneOutcome{}
}

// spendWords is one run's spend as a line reads it: what the backend reported, and
// `unreported` for each number it did not report. It never prints a zero the backend did not
// report, for the reason Usage's comment gives.
func spendWords(usage *run.Usage) string {
	tokens, cost := "unreported tokens", "unreported cost"
	if usage != nil && usage.Tokens != nil {
		tokens = fmt.Sprintf("%d tokens", *usage.Tokens)
	}
	if usage != nil && usage.CostUSD != nil {
		cost = fmt.Sprintf("$%f", *usage.CostUSD)
	}
	return tokens + ", " + cost
}

// closeRunBracket closes one run's bracket: the after snapshot, the narrow comparison that
// convicts only what a run may not do, and the event that records either answer. It answers
// the stop a violation is.
func (l *Loop) closeRunBracket(ctx context.Context, worktree string, request run.Request, tick string, at time.Time, before *Boundary, result *LoopResult, watched []string) (string, error) {
	after, err := l.snapshot(ctx, []string{worktree}, watched)
	if err != nil {
		return "", err
	}
	return l.finishRunBracket(ctx, worktree, request, tick, at, before, after, result)
}

func (l *Loop) finishRunBracket(ctx context.Context, worktree string, request run.Request, tick string, at time.Time, before, after *Boundary, result *LoopResult) (string, error) {
	if l.nonLanding {
		return "", nil
	}
	violations, observed, err := CompareRunObserved(before, after)
	if err != nil {
		return "", err
	}
	scope := BoundaryScopeRun
	if request.Resume || request.SessionTurn {
		scope = BoundaryScopeResume
	}
	if err := l.recordBoundaryCheck(ctx, scope, tick,
		[]runRef{{RunID: request.RunID, ItemID: request.ItemID, LaneID: laneOfWorktree(worktree)}}, []string{worktree}, violations, at, observed...); err != nil {
		return "", err
	}
	if len(observed) > 0 {
		more := ""
		if len(observed) > 1 {
			more = fmt.Sprintf(" (and %d more, on its boundary.check)", len(observed)-1)
		}
		l.reportf("loop: the repository changed outside lane %s during run %s: %s%s; recorded, not attributed to the run\n", laneOfWorktree(worktree), request.RunID, observed[0], more)
	}
	if len(violations) > 0 {
		l.stopForViolations(violations, []string{request.ItemID}, []string{laneOfWorktree(worktree)}, result)
		// The stored check holds this run's item on the next plan. It is not
		// a verdict about sibling runs and must not cancel them.
	}
	return "", nil
}

// laneOfWorktree answers the lane id a worktree path ends in, for a report line only: the
// worktree is <lane-root>/<lane>/worktree.
func laneOfWorktree(worktree string) string {
	return filepath.Base(filepath.Dir(worktree))
}

// countSpend adds one finished run's report to the loop's total, and prints that run's own
// line: the total is what the loop ends with, and the per-run line is how an operator sees
// which run spent what. Measurement is introspection and never a wall — the loop has no
// spend budget to compare against (docs/design/momentum-principle.md) — so every run's spend
// is counted and printed, and no total in this loop stops anything.
//
// A run the step could not start at all has no record and is not counted: the attempt is
// already reported as failed, and inventing a zero for it would be the same defect as
// reading a missing cost as free.
func (l *Loop) countSpend(result *LoopResult, rec run.Record) {
	if rec.RunID == "" {
		return
	}
	who := rec.RunID + " on " + rec.Backend
	l.mu.Lock()
	if result.spendRecords == nil {
		result.spendRecords = map[string]run.Record{}
	}
	result.spendRecords[rec.RunID] = rec
	result.recountSpend()
	l.mu.Unlock()
	l.reportf("loop: %s spent %s\n", who, spendWords(rec.Usage))
}

// settle takes one finished run the rest of the way: the attempt's class, the one permitted
// failover, and the gate/land/verdict section. It runs in the lane's own goroutine, so a
// lane's candidate is judged and landed as soon as its own run ends.
func (l *Loop) settle(ctx context.Context, decision Decision, record run.Record, log, mainSHA, tick string, at time.Time, result *LoopResult, timing *PassTiming, watched []string, deferJudge bool) (outcome laneOutcome) {
	prepared := false
	defer func() {
		if deferJudge && !prepared && outcome.err == nil && outcome.stop == "" {
			outcome.err = l.markExamined(ctx, decision)
		}
	}()
	lane := decision.LaneID
	if out, held := l.checkSurvivingRunProcesses(ctx, decision.ItemID, lane, record.RunID); held {
		return out
	}
	worktree := l.lanePath(lane)
	request := *decision.RunRequest
	failedResume, err := failedDesignerResume(decision, record)
	if err != nil {
		return laneOutcome{err: err}
	}
	if !failedResume {
		if err := l.reconcileLandingDelivery(ctx, decision); err != nil {
			var surviving *survivingProcessesError
			if errors.As(err, &surviving) {
				return l.retainSurvivingProcesses(ctx, decision.ItemID, lane, surviving.RunID, surviving.Processes)
			}
			return laneOutcome{err: err}
		}
	}
	intake, err := l.intakeLandingOffer(ctx, decision, record, mainSHA)
	if err != nil {
		var surviving *survivingProcessesError
		if errors.As(err, &surviving) {
			return l.retainSurvivingProcesses(ctx, decision.ItemID, lane, surviving.RunID, surviving.Processes)
		}
		return laneOutcome{err: err}
	}
	if intake == landingOfferSettled {
		return laneOutcome{}
	}
	if intake == landingOfferDuplicate {
		l.reportf("loop: %s repeated a landing call already recorded; not judging it again\n", record.RunID)
		return laneOutcome{stop: l.abandon(ctx, lane, record.RunID, l.headOf(ctx, lane), "duplicate landing offer")}
	}
	offered := intake == landingOfferSubmitted
	if offered {
		code := 0
		record.State, record.ExitCode = run.StateExited, &code
		// A commit offer can go directly to a judge without advancing a workflow
		// node. Retain its extracted report at the same path as other node reports.
		for _, ref := range record.ArtifactRefs {
			if ref.Kind != "report" {
				continue
			}
			body, err := os.ReadFile(ref.Ref)
			if err != nil {
				return laneOutcome{err: err}
			}
			dir := RunDir(l.cfg.Root, record.RunID)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return laneOutcome{err: err}
			}
			if err := os.WriteFile(filepath.Join(dir, "node-report.md"), body, 0644); err != nil {
				return laneOutcome{err: err}
			}
			break
		}
	}
	if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
		return laneOutcome{err: err}
	}
	if !offered && quotaSingleRun(decision) {
		interrupted, err := l.quotaInterrupted(ctx, record.RunID)
		if err != nil {
			return laneOutcome{err: err}
		}
		_, _, _, refused := refusedWindow(record)
		if refused {
			if err := l.recordBackendWindow(ctx, record); err != nil {
				return laneOutcome{err: err}
			}
			paths, err := l.uncommittedPaths(ctx, l.laneTarget(lane))
			if err != nil {
				return laneOutcome{err: err}
			}
			head, err := l.revParse(ctx, l.laneTarget(lane), "HEAD")
			if err != nil {
				return laneOutcome{err: err}
			}
			interrupted = interrupted || len(paths) > 0 || head != l.settleBase(lane, mainSHA)
		}
		if interrupted && (refused || record.State == run.StatePaused) {
			prepared = true
			resumed, resumedLog, stop, err := l.resumeQuotaRun(ctx, decision, record, tick, at, result, watched)
			if err != nil || stop != "" || resumed.RunID == "" {
				return laneOutcome{stop: stop, err: err}
			}
			if resumed.Backend != request.Backend {
				next := requestForQuotaBackend(l.currentPolicy(), request, resumed.Backend)
				if next == nil {
					return laneOutcome{err: Usagef("quota continuation lost backend %s from policy", resumed.Backend)}
				}
				decision.RunRequest = next
			}
			return l.settle(ctx, decision, resumed, resumedLog, mainSHA, tick, at, result, timing, watched, deferJudge)
		}
		// A peer that finished before the pause reached it is settled normally.
		if interrupted && record.State != run.StatePaused {
			if err := l.clearQuotaPause(ctx, record.RunID); err != nil {
				return laneOutcome{err: err}
			}
		}
	}
	if record.State == run.StatePaused {
		// Pausing is not settlement: even an already committed tree belongs to
		// the continuing session until it answers. Keep pending work running.
		prepared = true
		return laneOutcome{}
	}
	if l.nonLanding {
		if decision.Workflow != nil {
			_, err := l.settleNode(ctx, decision, record, log, mainSHA, result, offered)
			return laneOutcome{err: err}
		}
		return l.settleHook(ctx, decision, result)
	}
	if err := pipelineCloseGraph(l.cfg.Root, record.RunID); err != nil {
		return laneOutcome{err: err}
	}
	if failedResume && !offered {
		prepared = true
		return l.replaceFailedDesigner(ctx, decision, record, mainSHA, tick, at, result, timing, watched, deferJudge)
	}
	if err := l.recordDesigner(ctx, decision, record); err != nil {
		return laneOutcome{err: err}
	}
	diagnostic, outputErr := runOutputTail(RunDir(l.cfg.Root, record.RunID))
	if outputErr != nil {
		return laneOutcome{err: outputErr}
	}
	if diagnostic != "" {
		l.reportf("loop: %s output: %s\n", record.RunID, diagnostic)
	}
	for _, ref := range record.ArtifactRefs {
		if ref.Kind == "report" {
			log = logTail(record.ArtifactRefs)
			break
		}
	}
	// A hand landing may have committed while this run was being taken up or continued.
	if err := l.checkItemTerminal(ctx, request.ItemID, "settle"); err != nil {
		return laneOutcome{err: err}
	}
	if decision.Workflow != nil {
		if ClassifyAttempt(record, log) == AttemptUnjudged && !commandStage(decision) && !evaluatorNode(decision) {
			// A prior node's commit is not this node's answer. Continue the
			// interrupted node in place, or retain its unjudged hold; neither
			// route may enter the single-run candidate admission path.
			resumed, resumedLog, stop, err := l.resumeAfterWall(ctx, lane, request, worktree, record, tick, at, result, watched)
			if err != nil || stop != "" {
				return laneOutcome{err: err, stop: stop}
			}
			if resumed.RunID != "" {
				prepared = deferJudge
				return l.settle(ctx, decision, resumed, resumedLog, mainSHA, tick, at, result, timing, watched, deferJudge)
			}
			if err := l.nodeOutcome(ctx, decision, "unjudged"); err != nil {
				return laneOutcome{err: err}
			}
			return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "workflow node was cut short and remains unjudged")}
		}
		handled, nodeErr := l.settleNode(ctx, decision, record, log, mainSHA, result, offered)
		if handled {
			prepared = nodeErr == nil
			return laneOutcome{err: nodeErr}
		}
	}
	// An interrupted run is evidence about the supervisor, not the item. It is
	// neither judged nor charged to any attempt budget, even if it left a commit.
	orphaned, err := l.orphanedRun(ctx, record)
	if err != nil {
		return laneOutcome{err: err}
	}
	if orphaned && !offered {
		runBase := mainSHA
		if decision.Workflow != nil && decision.NodeBaseSHA != "" {
			runBase = decision.NodeBaseSHA
		}
		if err := l.preserveWorkFromBase(ctx, lane, request.RunID, runBase, "orphaned by a loop stop"); err != nil {
			return laneOutcome{stop: l.retainUnpreserved(ctx, lane, request.RunID, l.headOf(ctx, lane), err)}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "orphaned by a loop stop")}
	}
	// pi ended the session because the model kept repeating one identical call. That
	// conversation is not continued and the ending is not judged: the item starts again
	// fresh, fails over, or is held for the operator (repeating_call.go).
	if record.EndCause == run.EndRepeatingCall && !offered && decision.BlockConflict == nil && decision.DeskDocument == "" {
		return l.settleRepeatingCall(ctx, decision, record, lane)
	}
	// A usage refusal says nothing about the conflict. Release it for the next
	// planner pass instead of leaving the claimed design run awaiting forever.
	if decision.BlockConflict != nil {
		if _, _, _, refused := refusedWindow(record); refused {
			if err := l.recordBackendWindow(ctx, record); err != nil {
				return laneOutcome{err: err}
			}
			c := *decision.BlockConflict
			c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
			if c.TriageVersion != "" {
				c.Status, c.WaitingOn, c.DesignRunID = "owner", "owner", record.RunID
			}
			c.Text += "\n\nDesign run " + record.RunID + " refused by backend quota on " + record.Backend
			if err := l.saveBlockConflict(ctx, c); err != nil {
				return laneOutcome{err: err}
			}
			return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane))}
		}
	}
	class := ClassifyAttempt(record, log)
	if evaluatorNode(decision) {
		class = AttemptAnswered
	}
	if class == AttemptInterrupted {
		runBase := mainSHA
		if decision.Workflow != nil && decision.NodeBaseSHA != "" {
			runBase = decision.NodeBaseSHA
		}
		if err := l.preserveWorkFromBase(ctx, lane, request.RunID, runBase, "run interrupted by signal"); err != nil {
			return laneOutcome{stop: l.retainUnpreserved(ctx, lane, request.RunID, l.headOf(ctx, lane), err)}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "run interrupted by signal")}
	}
	if class == AttemptRefused {
		// An adapter refused the brief before any model ran. The refusal is not a backend
		// failure: no streak, no closed backend, no failover. The item is held for the
		// operator with the adapter's words.
		stop, err := l.holdAdapterRefusal(ctx, lane, request, log)
		return laneOutcome{stop: stop, err: err}
	}
	for class == AttemptInfrastructure {
		if err := l.recordBackendWindow(ctx, record); err != nil {
			return laneOutcome{err: err}
		}
		l.mu.Lock()
		result.InfraFailures++
		l.infraStreak++
		streak := l.infraStreak
		l.mu.Unlock()
		l.reportf("loop: %s failed in the infrastructure class (%d consecutive)\n", request.ItemID, streak)
		// Machinery failed: close that backend and walk to the next open one after reset.
		// A fully closed route ends this walk until recorded evidence reopens it. It
		// runs in this lane's own goroutine, so its bracket has one target and a violation
		// there names this run.
		if deferJudge {
			// The next pass's planner owns a failover dispatch. Settlement is one
			// transition, not a second run smuggled into the same item application.
			return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane))}
		}
		failover, ok, err := l.nextAvailableRequest(ctx, request.ItemID, lane, attemptRunID(request.ItemID, decision.Attempt+1), request.Backend, decision.Seat)
		if err != nil {
			return laneOutcome{err: err}
		}
		if !ok {
			break
		}
		if ok {
			decision.Attempt++
			failover.CorrelationID = tick
			if err := l.preserveWork(ctx, lane, request.RunID, "infrastructure failure before failover"); err != nil {
				return laneOutcome{err: err}
			}
			if err := l.resetLane(ctx, lane); err != nil {
				return laneOutcome{err: err}
			}
			before, err := l.snapshot(ctx, []string{worktree}, watched)
			if err != nil {
				return laneOutcome{err: err}
			}
			l.reportf("loop: failover %s -> %s on %s\n", request.ItemID, failover.Backend, lane)
			l.mu.Lock()
			result.Dispatched++
			l.mu.Unlock()
			// After the reset this work belongs to the new run, not the run whose
			// snapshot was just preserved. Reusing its ref would lose failover edits.
			stampDecisionRequest(decision, failover)
			request = *failover
			decision.RunRequest = failover
			record, log, err = l.runRestartable(ctx, request)
			if err != nil {
				return laneOutcome{err: err}
			}
			l.countSpend(result, record)
			stop, err := l.closeRunBracket(ctx, worktree, request, tick, at, before, result, watched)
			if err != nil || stop != "" {
				return laneOutcome{stop: stop, err: err}
			}
			if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
				return laneOutcome{err: err}
			}
			class = ClassifyAttempt(record, log)
			if class == AttemptRefused {
				stop, err := l.holdAdapterRefusal(ctx, lane, request, log)
				return laneOutcome{stop: stop, err: err}
			}
			if class == AttemptInfrastructure {
				if err := l.recordBackendWindow(ctx, record); err != nil {
					return laneOutcome{err: err}
				}
			}
		}
	}
	if class == AttemptInfrastructure {
		// No backend is open: the next pass waits on recorded backend recovery.
		// The criteria are untouched, the
		// head is pinned, and the loop moves on.
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane))}
	}
	l.mu.Lock()
	l.infraStreak = 0
	l.mu.Unlock()
	// A run that answers `blocked` is the model's own statement that the item cannot be
	// done as written: a spec conflict, not a failing candidate. The item is blocked for a
	// person to decide, its criteria are left as they were, and nothing is judged.
	// A design run that names its next actor is an objection whatever its footer, when it
	// committed nothing: a commit is the answer, and goes to the gate (designObjection).
	// A worker's run has two endings: the commit, or a complete obstacle (obstacle.go). A
	// complete trunk, tooling or seat obstacle is acted on here; definition, fence and owner
	// take the blocked route below. Any other ending is a missing output: with no commit it
	// is returned to the same session saying what is missing, like a run that committed
	// nothing, and a commit is a candidate like any other.
	footer := "done"
	recoveredOwnerQuestion := false
	if offered {
		if obs, isObstacle, obsErr := l.offeredObstacle(ctx, request.RunID); obsErr != nil {
			return laneOutcome{err: obsErr}
		} else if isObstacle {
			offered = false
			footer = "blocked"
			log = formatObstacleReport(obs)
		}
	} else {
		footer = reportFooter(log)
		recoveredOwnerQuestion, err = l.recoverOwnerQuestion(ctx, &decision, log)
		if err != nil {
			return laneOutcome{err: err}
		}
	}
	contract := !offered && workerContract(decision, request) && !recoveredOwnerQuestion
	endingNote := ""
	if contract {
		if problem := workerEndingProblem(log); problem != "" {
			endingNote = missingOutputDetail(problem)
		} else if parseObstacle(log).Kind == ObstacleSeat && !l.laneAttempted(ctx, lane, mainSHA) {
			endingNote = missingOutputDetail(SeatNeedsAttempt)
		} else if footer == "blocked" || footer == "trunk-broken" {
			if stale, err := l.staleBlockedReport(ctx, decision); err != nil {
				return laneOutcome{err: err}
			} else if stale {
				return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "reported RESULT: blocked on a stale base")}
			}
			if outcome, handled := l.routeObstacle(ctx, decision, request, lane, tick, mainSHA, parseObstacle(log), result); handled {
				return outcome
			}
		}
	}
	if (request.Backend != "command" && (footer == "blocked" || (contract && footer == "trunk-broken")) && (!contract || endingNote == "")) || (decision.BlockConflict != nil && decision.BlockConflict.TriageVersion != "" && l.headOf(ctx, lane) == l.laneBase(lane)) || (decision.BlockConflict != nil && decision.BlockConflict.TriageVersion == "" && designNextActor(log) != "" && (l.headOf(ctx, lane) == l.settleBase(lane, mainSHA) || l.headOf(ctx, lane) == decision.DesignContinuationHead)) {
		// A report from a run staged on a base that lacked what made the item ready is
		// about the staging, which neither the design seat nor the owner can change.
		if stale, err := l.staleBlockedReport(ctx, decision); err != nil {
			return laneOutcome{err: err}
		} else if stale {
			return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "reported RESULT: blocked on a stale base")}
		}
		written := recoveredOwnerQuestion
		if !written {
			written, err = l.prepareOwnerQuestion(ctx, &decision, log)
			if err != nil {
				return laneOutcome{err: err}
			}
		}
		if !written {
			if IsTrunkFixer(request.ItemID) && l.headOf(ctx, lane) == l.settleBase(lane, mainSHA) {
				if resolved, err := l.resolveKeptFixerWithoutCommit(ctx, request.ItemID); err != nil {
					return laneOutcome{err: err}
				} else if resolved {
					return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "resolved elsewhere: rerun passed")}
				}
			}
			l.reportf("loop: %s reported blocked: leaving it on the board to be decided\n", request.ItemID)
			if err := l.reportedBlock(ctx, decision, log); err != nil {
				return laneOutcome{err: err}
			}
			return laneOutcome{stop: l.abandonFromBase(ctx, lane, request.RunID, l.headOf(ctx, lane), l.settleBase(lane, mainSHA), "reported RESULT: blocked")}
		}
		// The report remains pinned evidence, but the loop-authored document is now
		// a design candidate, not the no-commit objection the report described.
		class = AttemptAnswered
	}
	if request.Backend != "command" && item.IsCommentItem(request.ItemID) && footer != "done" {
		candidate := Candidate{ItemID: request.ItemID, LaneID: lane, RunID: request.RunID, Worktree: l.laneTarget(lane), HeadSHA: l.headOf(ctx, lane)}
		candidate.RefusalDetail = "comment run did not report RESULT: done"
		if err := l.refuse(ctx, candidate, result, RefusalGate); err != nil {
			return laneOutcome{err: err}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, candidate.HeadSHA)}
	}
	target := l.laneTarget(lane)
	baseSHA := l.settleBase(lane, mainSHA)
	baseRef := l.lanePatchRef(lane)
	headSHA, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return laneOutcome{err: err}
	}
	noNewCommit := headSHA == baseSHA || headSHA == decision.DesignContinuationHead
	if decision.Returned != nil && decision.Returned.WorkerSession != nil && !verifyNoVerdictReturn(decision.Returned) {
		previous := decision.Returned.WorkerSession.HeadSHA
		// An evaluator can ask for evidence without asking for a code change.
		// Keep the real base guard above, but let that retained commit be judged again.
		noNewCommit = noNewCommit || (headSHA == previous && judgedCandidateReturn(decision) == nil)
	}
	if noNewCommit {
		paths, err := l.uncommittedPaths(ctx, target)
		if err != nil {
			return laneOutcome{err: err}
		}
		if len(paths) > 0 && !record.Resumed {
			record.Resumed, err = l.continuationStarted(ctx, request)
			if err != nil {
				return laneOutcome{err: err}
			}
		}
		// Uncommitted fenced work is continued in its own session for as long as each
		// segment changes it. The owner ruled on 2026-11-17 that this is not counted (audit
		// finding F-09): what ends it is a segment that left the work exactly as the one
		// before it did, an identical repeat, which is refused as `candidate-no-op` and goes
		// back to the item's worker with that evidence (returned.go).
		repeated := false
		if len(paths) > 0 && !item.IsCommentItem(request.ItemID) && (decision.Seat == "" || decision.Seat == "build") && class == AttemptAnswered && record.State == run.StateExited && record.EndCause == run.EndNormalExit {
			fenced, err := l.fencedUncommittedPaths(ctx, lane, target, paths)
			if err != nil {
				return laneOutcome{err: err}
			}
			if len(fenced) > 0 {
				repeated = l.segmentRepeated(request.RunID, l.uncommittedEvidence(ctx, target))
			}
			if len(fenced) > 0 && !repeated {
				request.ResumePrompt = "Your previous turn ended without the requested commit. Uncommitted fenced paths: " + strings.Join(fenced, ", ") + ". Preserve this work, finish the applicable verification (wait for any checks still running), and make the one requested commit before ending. Do not bypass failed checks or commit outside the fence. " + ObstacleContract
				resumed, resumedLog, stop, err := l.resumeAfterWall(ctx, lane, request, worktree, record, tick, at, result, watched)
				if err != nil || stop != "" {
					return laneOutcome{err: err, stop: stop}
				}
				if resumed.RunID != "" {
					prepared = deferJudge
					outcome := l.settle(ctx, decision, resumed, resumedLog, mainSHA, tick, at, result, timing, watched, deferJudge)
					l.forgetSegment(request.RunID)
					return outcome
				}
			}
		}
		if item.IsCommentItem(request.ItemID) && footer == "done" && record.ExitCode != nil && *record.ExitCode == 0 {
			candidate := Candidate{ItemID: request.ItemID, LaneID: lane, RunID: request.RunID, Worktree: target, BaseSHA: baseSHA, HeadSHA: headSHA}
			if err := l.answerCommentWithoutCommit(ctx, candidate, result); err != nil {
				return laneOutcome{err: err}
			}
			return laneOutcome{stop: l.abandon(ctx, lane, "", "")}
		}
		if class == AttemptUnjudged {
			// A wall cut the run short with nothing committed. The lane still holds this
			// run's worktree, brief and session id, so the attempt gets one continuation
			// segment in place before anything is reset — a run that committed there is a
			// candidate like any other (design D-RW3). A second wall on the same run_id
			// holds the item as unjudged rather than resuming again.
			if resumed, resumedLog, stop, err := l.resumeAfterWall(ctx, lane, request, worktree, record, tick, at, result, watched); err != nil {
				return laneOutcome{err: err}
			} else if stop != "" {
				return laneOutcome{stop: stop}
			} else if resumed.RunID != "" {
				// The continued segment is a finished run like any other, and this is the one
				// function that takes a finished run the rest of the way: `Record.Resumed` on
				// it is what makes the resume at most once per attempt.
				prepared = deferJudge
				return l.settle(ctx, decision, resumed, resumedLog, mainSHA, tick, at, result, timing, watched, deferJudge)
			}
			// No session to continue: an inspection is a person's, and re-dispatching is
			// not the loop's (design §4.2: not infrastructure, no failover).
			if IsTrunkFixer(request.ItemID) && headSHA == baseSHA {
				if resolved, err := l.resolveKeptFixerWithoutCommit(ctx, request.ItemID); err != nil {
					return laneOutcome{err: err}
				} else if resolved {
					return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, headSHA, "resolved elsewhere: rerun passed")}
				}
			}
			l.reportf("loop: %s was cut short and is left unjudged on %s\n", request.ItemID, lane)
			return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, headSHA)}
		}
		if IsTrunkFixer(request.ItemID) && headSHA == baseSHA {
			if resolved, err := l.resolveKeptFixerWithoutCommit(ctx, request.ItemID); err != nil {
				return laneOutcome{err: err}
			} else if resolved {
				l.forgetSegment(request.RunID)
				return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, headSHA, "resolved elsewhere: rerun passed")}
			}
		}
		// The model answered with no commit: `candidate-no-op`, a refusal rather than an
		// infrastructure failure, and not a failover. Nobody judged the work, so the item
		// keeps its criteria and stays in the queue.
		l.reportf("loop: %s produced no commit: recording a refusal\n", request.ItemID)
		candidate := Candidate{ItemID: request.ItemID, LaneID: lane, RunID: request.RunID, TickID: tick, Worktree: target, BaseRef: baseRef, BaseSHA: baseSHA, HeadSHA: headSHA}
		candidate.RefusalDetail = endingNote
		if len(paths) > 0 {
			if candidate.RefusalDetail != "" {
				candidate.RefusalDetail += " "
			}
			candidate.RefusalDetail += "No commit; uncommitted paths: " + strings.Join(paths, ", ")
			if repeated {
				candidate.RefusalDetail += "; the last continuation of its session left this work exactly as the one before it did, and no commit was produced"
			} else if record.Resumed {
				candidate.RefusalDetail += "; a continuation was started, but no commit was produced"
			} else {
				candidate.RefusalDetail += "; no commit continuation was available (requires an eligible build exit, brief and session, and no operator stop)"
			}
		}
		err = l.refuse(ctx, candidate, result, RefusalNoOp)
		l.forgetSegment(request.RunID)
		if err != nil {
			return laneOutcome{err: err}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, headSHA, "ended without a candidate commit")}
	}
	if decision.BlockConflict != nil && decision.BlockConflict.TriageVersion != "" {
		c := *decision.BlockConflict
		c.DesignRunID = request.RunID
		c.Text += "\n\nTriage run " + request.RunID + ":\n" + strings.TrimSpace(log)
		decision.BlockConflict = &c
	}
	candidate := Candidate{
		ItemID: request.ItemID, LaneID: lane, RunID: request.RunID, TickID: tick,
		Worktree: target, BaseRef: baseRef, BaseSHA: baseSHA, HeadSHA: headSHA,
		Request: &request, Seat: decision.Seat, Workflow: decision.workflowRef(), BlockConflict: decision.BlockConflict,
	}
	if err := l.attachWorkflowJudge(ctx, decision, &candidate); err != nil {
		return laneOutcome{err: err}
	}
	if deferJudge {
		if candidate.HeadSHA != "" && candidate.HeadSHA != candidate.BaseSHA {
			if err := l.pinLane(ctx, lane, request.RunID, candidate.HeadSHA); err != nil {
				return laneOutcome{err: err}
			}
		}
		if err := appendJudgment(ctx, l.cfg.Store, candidateReadyKind, "run", request.RunID, candidate, at); err != nil {
			return laneOutcome{err: err}
		}
		prepared = true
		return laneOutcome{}
	}
	stop, landed, err := l.verify(ctx, &candidate, result, timing)
	if err != nil {
		return laneOutcome{err: err}
	}
	if stop != "" {
		// A stop is still a non-landing outcome: the head is pinned before this lane lets
		// go, so an operator can inspect what the gate or the harness saw.
		if err := l.pinLane(ctx, lane, request.RunID, candidate.HeadSHA); err != nil {
			return laneOutcome{err: err}
		}
		return laneOutcome{stop: stop}
	}
	if landed {
		if err := l.prunePreservedWork(ctx); err != nil {
			return laneOutcome{err: err}
		}
		return laneOutcome{stop: l.abandon(ctx, lane, "", "")}
	}
	// Every non-landing outcome pins the head before the lane is reset, so a refused head
	// survives garbage collection for the person or the conflict-fixer who reads it.
	return laneOutcome{stop: l.abandon(ctx, lane, request.RunID, candidate.HeadSHA)}
}

// segmentRepeated records the evidence of a run's uncommitted work before its session is
// continued, and answers whether it equals the evidence recorded before the previous
// continuation: the segment in between changed nothing. A replacement loop has no earlier
// evidence, so a continuation a stop killed is continued again, uncounted.
func (l *Loop) segmentRepeated(runID, evidence string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.segmentEvidence == nil {
		l.segmentEvidence = map[string]string{}
	}
	previous, seen := l.segmentEvidence[runID]
	l.segmentEvidence[runID] = evidence
	return seen && previous == evidence
}

// forgetSegment drops a run's continuation evidence once its settlement is done.
func (l *Loop) forgetSegment(runID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.segmentEvidence, runID)
}

// resumeAfterWall continues a time-limited, paused, or uncommitted build run in place,
// when the lane still holds everything the continuation needs: the brief, the harness
// session staging pinned, and the worktree the previous segment was working in. It is one attempt's second segment, not a
// second attempt — the same run id, no retry authorization, and no failover hop — so it
// spends nothing the retry budget tracks (design D-RW3).
//
// An empty Record means nothing was resumed, and the caller holds the item as unjudged:
// a wall-cut run already had its continuation (`Record.Resumed`, which bounds that at one
// per run_id; a continuation of uncommitted fenced work has no such bound), the lane carries no brief or no session id to continue, or the operator asked
// the loop to stop. Never resuming in `expire` is deliberate: that is a runner that
// vanished unobserved, not one a wall cut short.
//
// The bracket is taken at the resume and not reused from the dispatch, because the killed
// segment committed nothing but may still have written to the worktree, and a resumed
// segment has to answer for what it writes from where it actually starts.
func (l *Loop) resumeAfterWall(ctx context.Context, lane string, request run.Request, worktree string, record run.Record, tick string, at time.Time, result *LoopResult, watched []string) (run.Record, string, string, error) {
	// A continuation of uncommitted fenced work (ResumePrompt) is bounded by its own
	// identical-repeat rule (segmentRepeated), not by Record.Resumed.
	if record.Resumed && record.State != run.StatePaused && request.ResumePrompt == "" {
		return run.Record{}, "", "", nil
	}
	return l.runContinuation(ctx, lane, request, worktree, record, tick, at, result, watched, "")
}

// Quota and restarts can interrupt multiple segments of the same attempt.
// They use the same executor without the ordinary wall's one-continuation bound.
func (l *Loop) runContinuation(ctx context.Context, lane string, request run.Request, worktree string, record run.Record, tick string, at time.Time, result *LoopResult, watched []string, restartReason string) (run.Record, string, string, error) {
	if ctx.Err() != nil {
		return run.Record{}, "", "", nil
	}
	laneDir := l.laneDirPath(lane)
	if request.ReadOnlyGroup != "" {
		laneDir = run.NodeDir(laneDir, request.RunID)
	}
	if _, err := os.Stat(filepath.Join(laneDir, BriefFile)); err != nil {
		return run.Record{}, "", "", nil
	}
	sessionID, err := ReadSessionID(laneDir)
	if err != nil {
		// A malformed session-id is a lane defect to report, not a value to hand on.
		return run.Record{}, "", "", err
	}
	if sessionID == "" {
		return run.Record{}, "", "", nil
	}
	resumed := request
	resumed.Resume = true
	resumed.CorrelationID = tick
	l.noteLaneWrite(lane)
	epochs := l.idleLaneEpochs()
	before, err := l.snapshotIdleSiblings(ctx, worktree, watched, epochs)
	if err != nil {
		return run.Record{}, "", "", err
	}
	l.mu.Lock()
	result.Dispatched++
	l.mu.Unlock()
	if restartReason != "" {
		l.reportf("loop: continuing interrupted run %s on %s as the same attempt: %s\n", request.RunID, lane, restartReason)
	} else if record.State == run.StatePaused {
		l.reportf("loop: continuing paused run %s on %s as the same attempt: the runner recorded a pause and the staged session is available\n", request.RunID, lane)
	} else if request.ResumePrompt != "" {
		l.reportf("loop: %s exited with uncommitted fenced work; continuing its session on %s to finish and commit\n", request.ItemID, lane)
	} else {
		l.reportf("loop: %s hit the wall before committing; continuing its session once on %s\n", request.ItemID, lane)
	}
	record, log, err := l.runRestartable(ctx, resumed)
	if err != nil {
		return run.Record{}, "", "", err
	}
	// The record is the resume's bound, so it is stated here and not only relied on from the
	// step: `run exec` sets it, and a step that lost it would let this loop resume a stuck
	// prompt forever. The loop knows it just resumed; it says so.
	record.Resumed = true
	l.countSpend(result, record)
	origin := l.sessionID
	if result.spendCarried[record.RunID] {
		origin = ""
	}
	if err := l.recordSettlementSpend(context.WithoutCancel(ctx), tick, origin, record); err != nil {
		return record, log, "", err
	}
	stop, err := l.closeAsyncBracket(ctx, Decision{LaneID: lane, RunRequest: &resumed}, at, before, result, watched, epochs)
	if err != nil || stop != "" {
		return record, log, stop, err
	}
	return record, log, "", nil
}

// Verify takes one candidate through the gate and — only when the gate passed — the land and
// the verdict. It is no longer a serial section: two lanes may gate and land at once, and
// the only serialization left is the land step's own ref swap, which the lander takes
// through the repository's land lock.
//
// timing accumulates the gate and land wall times this candidate spent, and a nil one
// measures nothing. The gate's number is the gate program's own (GateOutcome.DurationMS),
// never this call's elapsed time.
func (l *Loop) Verify(ctx context.Context, candidate Candidate, result *LoopResult, timing *PassTiming) (string, bool, error) {
	return l.verify(ctx, &candidate, result, timing)
}

// verify returns the prepared head to internal callers so refusal pins retain the same
// tree the gate saw, including loop-owned document retirement.
func (l *Loop) verify(ctx context.Context, prepared *Candidate, result *LoopResult, timing *PassTiming) (string, bool, error) {
	candidate := *prepared
	defer func() { *prepared = candidate }()
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "gate"); err != nil {
		return "", false, err
	}
	if concluded, err := l.concludedByRefusal(ctx, candidate.RunID); err != nil || concluded {
		// The run's examination already ended in a refusal, which released and recut its
		// lane, and only the examination's done record is missing. Replay that ending: no
		// second gate, and no comparison of the recut lane with the candidate it no longer
		// holds. The caller records the examination done.
		if concluded && candidate.BlockConflict != nil {
			if err := l.readStore(ctx, func(r *store.Reader) error {
				_, refusal, err := designOutcome(r, candidate.RunID, "", "")
				if refusal.DesignHeadSHA != "" {
					candidate.BaseSHA, candidate.HeadSHA = refusal.DesignBaseSHA, refusal.DesignHeadSHA
				}
				return err
			}); err != nil {
				return "", false, err
			}
		}
		if concluded {
			l.reportf("loop: %s run %s already ended in a refusal; its examination concludes without a second gate\n", candidate.ItemID, candidate.RunID)
		}
		return "", false, err
	}
	if published, err := l.recoverJudgePublication(ctx, candidate, result); published || err != nil {
		return "", published && err == nil, err
	}
	var accepted bool
	var recoveryErr error
	candidate, accepted, recoveryErr = l.recoverJudgeHandoff(ctx, candidate, result)
	if recoveryErr != nil || !accepted {
		return "", false, recoveryErr
	}
	if err := l.recoverSecondGateStall(ctx, candidate); err != nil {
		return "", false, err
	}
	if item.IsCommentItem(candidate.ItemID) {
		if _, _, err := l.readCommentReplies(ctx, candidate); err != nil {
			candidate.RefusalDetail = err.Error()
			if err := l.rejectCommentReplies(ctx, candidate, result); err != nil {
				return "", false, err
			}
			return "", false, nil
		}
	}
	// A candidate reaching this point is a run that committed, and the count is taken here
	// rather than at the dispatch so a run that produced no commit is never read as a
	// candidate the loop refused.
	l.mu.Lock()
	result.Candidates++
	l.mu.Unlock()
	// A fixer is rebased onto the current trunk before anything judges it, so the tree the
	// gate sees is the tree the land would ship. The exemption's third rule depends on this
	// running before the fence: a rebase moves the candidate's head, and the fence must be
	// read against the tree that is actually judged.
	var err error
	candidate, err = l.recoverRetirement(ctx, candidate)
	if err != nil {
		if ExitCode(err) == record.ExitExternal {
			return "", false, l.candidateFault(ctx, candidate, err.Error())
		}
		return "", false, err
	}
	if err := l.candidateInItsLane(ctx, candidate); err != nil {
		return "", false, err
	}
	head, err := l.revParse(ctx, candidate.Worktree, "HEAD")
	if err != nil {
		return "", false, l.candidateFault(ctx, candidate, err.Error())
	}
	if head != candidate.HeadSHA {
		return "", false, l.candidateFault(ctx, candidate, fmt.Sprintf("lane HEAD %s differs from recorded candidate %s", head, candidate.HeadSHA))
	}
	if _, err := l.git(ctx, candidate.Worktree, "cat-file", "-e", candidate.BaseSHA+"^{commit}"); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		return "", false, l.candidateFault(ctx, candidate, fmt.Sprintf("cannot resolve candidate base %s for head %s in %s: %v", candidate.BaseSHA, candidate.HeadSHA, candidate.Worktree, err))
	}
	// A prepared retirement tree is already bound; do not rebase it on rejudgment.
	if candidate.WorkerHeadSHA == "" {
		if rebased, err := l.preRebaseFixer(ctx, candidate); err != nil {
			if conflict := asFixerRebaseConflict(err); conflict != nil {
				return l.fixerRebaseConflicted(ctx, candidate, conflict, result, timing)
			}
			return "", false, err
		} else {
			candidate = rebased
		}
	}
	if retitled, err := l.retitleIntermittentFixer(ctx, candidate); err != nil {
		return "", false, err
	} else {
		candidate = retitled
	}
	// The fence is checked before the gate, because the gate answers a different question.
	// A gate that passes says the tests pass; it never says the change is the one the item
	// asked for, and a candidate that edits out of scope can pass every check in the
	// repository while landing behaviour nobody wanted.
	worker := candidate
	if candidate.WorkerHeadSHA != "" {
		worker.HeadSHA = candidate.WorkerHeadSHA
	}
	escape, err := l.fenceViolation(ctx, worker)
	if err != nil {
		return "", false, err
	}
	if escape != "" {
		l.reportf("loop: refusing %s before the gate: %s\n", candidate.ItemID, escape)
		if candidate.RefusalDetail == "" {
			candidate.RefusalDetail = escape
		}
		if err := l.refuse(ctx, candidate, result, RefusalFence); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	if detail, err := l.validateIdea(ctx, worker); err != nil {
		return "", false, err
	} else if detail != "" {
		candidate.RefusalDetail = detail
		step, _, _ := strings.Cut(detail, ":")
		l.reportf("loop: refusing %s before the gate: %s\n", candidate.ItemID, detail)
		if err := l.refuse(ctx, candidate, result, step); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	if detail, err := l.validateRequest(ctx, worker); err != nil {
		return "", false, err
	} else if detail != "" {
		candidate.RefusalDetail = detail
		l.reportf("loop: refusing %s before the gate: %s\n", candidate.ItemID, detail)
		if err := l.refuse(ctx, candidate, result, RefusalFence); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	ref, err := l.blockReference(ctx, candidate.ItemID)
	if err != nil {
		return "", false, err
	}
	if item.ProseRewriteOriginal(candidate.ItemID) != "" {
		detail, err := l.validateProseRewrite(ctx, worker, ref)
		if err != nil {
			return "", false, err
		}
		if detail != "" {
			candidate.RefusalDetail = detail
			l.reportf("loop: refusing %s before the gate: %s\n", candidate.ItemID, detail)
			if err := l.refuse(ctx, candidate, result, RefusalFence); err != nil {
				return "", false, err
			}
			return "", false, nil
		}
	}
	changed, err := l.definitionChanged(ctx, worker, ref)
	if err != nil {
		return "", false, err
	}
	if candidate.BlockConflict == nil && item.ProseRewriteOriginal(candidate.ItemID) == "" && changed {
		l.reportf("loop: refusing %s before the gate: worker edited its own item block %s\n", candidate.ItemID, ref)
		candidate.RefusalDetail = "worker edited its own item block " + ref
		if err := l.refuse(ctx, candidate, result, RefusalFence); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	if candidate.BlockConflict != nil && candidate.BlockConflict.TriageVersion != "" {
		detail, err := l.validateTriage(ctx, candidate)
		if err != nil {
			return "", false, err
		}
		if detail != "" {
			l.reportf("loop: refusing %s before the gate: %s\n", candidate.ItemID, detail)
			c := *candidate.BlockConflict
			c.Status, c.WaitingOn = "owner", "owner"
			c.Text += "\nTriage run " + candidate.RunID + ": " + detail
			return "", false, l.saveBlockConflict(ctx, c)
		}
	}
	if candidate.BlockConflict != nil {
		triage := candidate.BlockConflict.TriageVersion != ""
		exists, answered := true, false
		if !triage && ref == "" {
			// A filed request has no item block to compare: a commit is the answer.
			answered = candidate.HeadSHA != candidate.BaseSHA
		} else {
			var err error
			if exists, answered, err = l.designBlockAnswer(ctx, candidate, ref); err != nil {
				return "", false, err
			}
		}
		if !exists && triage {
			c := *candidate.BlockConflict
			c.Status, c.WaitingOn = "owner", "owner"
			c.Text += "\nDesign run " + candidate.RunID + " removed or renamed its item block " + ref
			return "", false, l.saveBlockConflict(ctx, c)
		}
		if !triage && designReadingOf(true, exists, "") == designBlockRemoved {
			// A commit that removes the block leaves no item for the worker to build, so it
			// cannot be the answer: it goes back to the design seat, naming the commit, and
			// never to the owner (the owner's ruling of 2026-11-17).
			// An identical repeat (the same conflict, given the same block) goes to the owner
			// instead (design_repeat.go).
			if err := l.pinLane(ctx, candidate.LaneID, candidate.RunID, candidate.HeadSHA); err != nil {
				return "", false, err
			}
			return "", false, l.settleDesignStatement(ctx, *candidate.BlockConflict, candidate.RunID, designCauseBlockRemoved, "removed its item block", func(c *item.Conflict) {
				c.Status, c.WaitingOn, c.DesignRunID = "raised", "design", ""
				c.Text += "\n\nDesign run " + candidate.RunID + " removed or renamed its item block " + ref + " in commit " + shortSHA(candidate.HeadSHA) + " (pinned at refs/tillr/runs/" + candidate.RunID + "). An answer keeps the block's directive and item id; the design seat is dispatched again."
				l.reportf("loop: %s conflict %s is raised again for the design seat: design run %s removed its item block\n", c.ItemID, c.ConflictID, candidate.RunID)
			})
		}
		changed = answered
		if !triage && designReadingOf(true, exists, "") == designAmended {
			// A design run's gated commit is its answer whatever it changed: the owner
			// ruled on 2026-11-17 that a commit outside the block (the design document, a
			// sibling) is the design seat's judgment and not "no answer".
			changed = true
		}
	}
	if candidate.BlockConflict != nil && !changed {
		c := *candidate.BlockConflict
		c.Status, c.WaitingOn = "owner", "owner"
		c.Text += "\nDesign run " + candidate.RunID + " did not amend the blocked item block " + ref
		return "", false, l.saveBlockConflict(ctx, c)
	}
	if candidate.BlockConflict == nil {
		var accepted bool
		candidate, accepted, err = l.judgeCandidate(ctx, candidate, result)
		if err != nil || !accepted {
			return "", false, err
		}
	}
	// A design answer amends the block for the next worker; only a completed
	// worker candidate retires it. Recovery checks the original worker above,
	// not the loop-owned retirement commit.
	if candidate.BlockConflict == nil && item.ProseRewriteOriginal(candidate.ItemID) == "" {
		candidate, err = l.retireDocumentBlock(ctx, candidate)
		if err != nil {
			return "", false, err
		}
	}
	runnerChange, err := l.runnerProofChange(ctx, candidate)
	if err != nil {
		return "", false, err
	}
	l.mu.Lock()
	l.gatesInFlight++
	l.mu.Unlock()
	candidate.gateCount = l.gateCount
	ctx, unlock, err := l.lockRestartMechanical(ctx)
	if err != nil {
		return "", false, err
	}
	defer unlock()
	gate, err := l.cfg.Steps.Gate(ctx, candidate)
	l.mu.Lock()
	stalledGateCount := l.gatesInFlight
	l.gatesInFlight--
	l.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			return "", false, err
		}
		if ExitCode(err) == record.ExitExternal {
			return "", false, l.gateFault(ctx, candidate, err.Error())
		}
		return "", false, err
	}
	if timing != nil {
		l.mu.Lock()
		timing.addGateTime(gate.DurationMS, runnerChange)
		l.mu.Unlock()
	}
	// The gate is the long step: an item landed by hand while it ran gets no verdict,
	// refusal or fault from its answer.
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "gate"); err != nil {
		return "", false, err
	}
	switch gate.Outcome {
	case GateStalled:
		return "", false, l.holdGateStall(ctx, candidate, gate, stalledGateCount)
	case GatePassed, GatePassedInherited:
		// An item whose landing was reverted must pass the check that proved it broke
		// the trunk: the fast gate has shown it cannot see that failure.
		detail, err := l.judgeRequired(ctx, &candidate)
		if err != nil {
			return "", false, err
		}
		if detail != "" {
			l.reportf("loop: gate passed %s, but a required full-suite check failed: recording a refusal\n", candidate.ItemID)
			candidate.RefusalDetail = detail
			if err := l.refuse(ctx, candidate, result, RefusalGate); err != nil {
				return "", false, err
			}
			return "", false, nil
		}
	case GateFailed:
		// A red gate is a suspect, not a conviction. Judge the failing checks on the base
		// and trunk tip, reusing exact recorded passes before replaying:
		// - If repaired on tip, send back to worker on tip.
		// - If red on base and tip (OQ-GP7):
		//   * Compare the candidate's changed paths against each red check's declared
		//     `reads` (defaulting to ["."] when undeclared, matching checkfiles.go).
		//   * If disjoint from every red check's declared reads, promote the gate outcome
		//     to GatePassedInherited (and update gate.verdict.json if present) so the
		//     candidate lands with inherited failures recorded.
		//   * Otherwise, acquit and hold behind the fixer naming the check, fixer, and
		//     intersecting paths.
		outcome, acquitted, err := l.settleTrunkAlreadyRed(ctx, &candidate, gate, result)
		if err != nil {
			return "", false, err
		}
		if outcome == GatePassedInherited {
			gate.Outcome = GatePassedInherited
			break
		}
		if acquitted {
			return "", false, nil
		}
		// A red gate is a refusal about the code, never a verdict: no judge retired the
		// item's criteria, so the item stays in the queue with no verdict frozen against
		// it (MQ1). The refusal event is what holds it.
		if err := l.refuse(ctx, candidate, result, RefusalGate); err != nil {
			return "", false, err
		}
		return "", false, nil
	case GateErrored:
		// The gate ran and reached no verdict: nothing was judged, so the same candidate is
		// gated again on the next pass (gate_no_verdict.go), never held for an operator.
		// A deterministic answer is the exception: a gate that refused its own arguments
		// answers the same words on every pass, so the item is held for the operator with
		// the gate's error as the reason, and its lane is released like any held one.
		if gate.ErrorDetail != "" {
			return "", false, l.gateFault(ctx, candidate, gate.ErrorDetail)
		}
		return "", false, l.holdGateNoVerdict(ctx, candidate)
	default:
		// An outcome the drain does not know is a contract defect between the drain and its
		// gate step; gating again would answer the same unknown word.
		return "", false, l.gateFault(ctx, candidate, fmt.Sprintf("the gate answered %q", gate.Outcome))
	}
	// The lane is released after landing, possibly long before a rolling check
	// reverts it. Capture its worker now, never from the later lane occupant.
	candidate.WorkerSession, err = l.captureLandedWorkerSession(ctx, candidate)
	if err != nil {
		return "", false, err
	}
	if err := appendJudgment(ctx, l.cfg.Store, candidateGatedKind, "run", candidate.RunID, candidate, l.cfg.Now()); err != nil {
		return "", false, err
	}
	return l.landCandidate(ctx, candidate, result, timing)
}

// settleBase is the commit a finished run's lane was cut from: the lane's recorded base,
// or the trunk the settlement read when the lane records none.
func (l *Loop) settleBase(lane, mainSHA string) string {
	if base := l.laneBase(lane); base != "" {
		return base
	}
	return mainSHA
}

// candidateInItsLane holds a recorded candidate whose checkout is not its lane's target.
// A recorded candidate outlives the loop that recorded it, and one recorded while a reused
// lane still read a fork item's leftover target (lane-9, 2026-11-17) names a trunk range
// in a fork checkout that no git there can diff: a mismatch to hold, not a loop fault.
func (l *Loop) candidateInItsLane(ctx context.Context, candidate Candidate) error {
	if target := l.laneTarget(candidate.LaneID); candidate.Worktree != target {
		return l.candidateFault(ctx, candidate, fmt.Sprintf("recorded candidate worktree %s is not lane %s's target %s", candidate.Worktree, candidate.LaneID, target))
	}
	// The same mismatch reached from the other side: a base recorded from another
	// repository (a fork item's later node given the trunk commit, 2026-11-20) names a
	// range no git in this checkout can diff.
	if candidate.BaseSHA != "" {
		if _, err := l.git(ctx, candidate.Worktree, "cat-file", "-e", candidate.BaseSHA+"^{commit}"); err != nil {
			return l.candidateFault(ctx, candidate, fmt.Sprintf("recorded candidate base %s is not a commit in %s, whose head is %s", candidate.BaseSHA, candidate.Worktree, candidate.HeadSHA))
		}
	}
	return nil
}

// landCandidate retries a retained, already-gated tree without another run or gate.
// Land still validates the verdict's tree binding and recomputes the squash.
func (l *Loop) landCandidate(ctx context.Context, candidate Candidate, result *LoopResult, timing *PassTiming) (string, bool, error) {
	ctx, unlock, lockErr := l.lockRestartMechanical(ctx)
	if lockErr != nil {
		return "", false, lockErr
	}
	defer unlock()
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "land"); err != nil {
		return "", false, err
	}
	if published, err := l.recoverJudgePublication(ctx, candidate, result); published || err != nil {
		return "", published && err == nil, err
	}
	if err := l.checkLandingJudgeGrant(ctx, candidate); err != nil {
		return "", false, err
	}
	if err := l.checkDeliveryOrder(ctx, candidate); err != nil {
		return "", false, err
	}
	// Retrying a durable verdict skips the gate, not the recorded-head guard.
	if err := l.candidateInItsLane(ctx, candidate); err != nil {
		return "", false, err
	}
	head, err := l.revParse(ctx, candidate.Worktree, "HEAD")
	if err != nil {
		return "", false, l.candidateFault(ctx, candidate, err.Error())
	}
	if head != candidate.HeadSHA {
		return "", false, l.candidateFault(ctx, candidate, fmt.Sprintf("lane HEAD %s differs from recorded candidate %s", head, candidate.HeadSHA))
	}
	h, err := l.observeTrunk(ctx, "")
	if err != nil {
		return "", false, err
	}
	if held, err := l.identityHeld(ctx); err != nil {
		return "", false, err
	} else if held {
		return "", false, l.holdIdentity(ctx, candidate)
	}
	if h.Lost != "" {
		return "", false, l.holdRewrite(ctx, candidate, h)
	}
	candidate.ObservedTrunk = h.Observed
	runnerChange, err := l.runnerProofChange(ctx, candidate)
	if err != nil {
		return "", false, err
	}
	landStart := time.Now()
	landed, err := l.landWithActions(ctx, candidate)
	if err == nil {
		l.mu.Lock()
		if hold, ok := landed.LockObservation.holdNS(); ok {
			l.landLockHolds = append(l.landLockHolds, hold)
		} else {
			l.landLockUnmeasured++
		}
		l.mu.Unlock()
	}
	if timing != nil {
		l.mu.Lock()
		timing.addLandTime(time.Since(landStart).Milliseconds(), runnerChange)
		l.mu.Unlock()
	}
	if err != nil {
		if ctx.Err() != nil {
			return "", false, err
		}
		if ExitCode(err) == record.ExitExternal {
			return "", false, l.landFault(ctx, candidate, err.Error())
		}
		return "", false, err
	}
	if landed.Result == LandRefused && landed.Reason == ReasonRedCap {
		return "", false, l.holdRedCap(ctx, candidate, landed.RedCap)
	}
	if landed.Result == LandRefused && landed.Reason == ReasonForeignIdentity {
		if _, err := l.observeTrunk(ctx, candidate.ObservedTrunk); err != nil {
			return "", false, err
		}
		return "", false, l.holdIdentity(ctx, candidate)
	}
	if landed.Result == LandRefused && landed.Reason == ReasonTrunkRewritten {
		h, err := l.observeTrunk(ctx, candidate.ObservedTrunk)
		if err != nil {
			return "", false, err
		}
		if h.Lost == "" {
			// History was restored after the lock-time refusal. Retain the gated
			// candidate for the next pass, not a refusal or a second gate.
			return "", false, errTrunkRewritten
		}
		return "", false, l.holdRewrite(ctx, candidate, h)
	}
	if landed.Result == LandRefused && landed.Reason == "trunk-worktree-uncommitted" {
		return "", false, l.holdDirty(ctx, candidate, landed.ConflictPaths)
	}
	if landed.Result == LandRefused && landed.Reason == LandAlreadyOnTrunk {
		return "", false, l.settleAlreadyOnTrunk(ctx, candidate)
	}
	if landed.Result == LandSquashConflict {
		// A conflict is not a refusal: the trunk moved and the candidate no longer squashes
		// onto it. It is the one negative outcome the next run can resolve, so it becomes
		// one bounded fix request instead of a failing verdict.
		return l.conflictFix(ctx, candidate, landed, result, timing)
	}
	if landed.Result != LandLanded {
		l.reportf("loop: land did not take %s (%s): recording a refusal\n", candidate.ItemID, landed.Result)
		if candidate.RefusalDetail == "" {
			candidate.RefusalDetail = strings.TrimSpace("the lander answered " + landed.Result + " " + landed.Reason)
		}
		if err := l.refuse(ctx, candidate, result, RefusalLand); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	if partial, err := l.settlePartialDelivery(ctx, candidate, landed.TrunkAfter); err != nil || partial {
		return "", partial, err
	}
	if err := l.recordLandingCallLanded(ctx, judgeWriterRun(candidate), landed.TrunkAfter); err != nil {
		return "", false, err
	}
	if err := l.deliverCommentReplies(ctx, candidate); err != nil {
		return "", false, err
	}
	if err := l.checkItemTerminal(ctx, candidate.ItemID, "landed"); err != nil {
		terminal := asItemTerminal(err)
		if terminal == nil {
			return "", false, err
		}
		// The trunk carries this run's change, but the item reached a terminal state by
		// another route while the lander ran: its state and verdicts stay as they are.
		l.mu.Lock()
		result.Landed++
		l.mu.Unlock()
		if err := l.bumpLandings(ctx); err != nil {
			l.reportf("loop: landed %s but could not record the landing count: %v\n", candidate.ItemID, err)
		}
		terminal.Landed = true
		return "", false, terminal
	}
	if candidate.Judge != nil {
		receipt := *candidate.Judge
		receipt.Options.Landed = landed.TrunkAfter
		if receipt.Options.Landed == "" {
			ref := TrunkRef
			if candidate.BaseRef != "" {
				ref = candidate.BaseRef
			}
			receipt.Options.Landed, err = l.revParse(ctx, l.cfg.Root, ref)
			if err != nil {
				return "", false, err
			}
		}
		candidate.Judge = &receipt
		if err := appendJudgment(ctx, l.cfg.Store, judgeLandedKind, "run", candidate.RunID, candidate, l.cfg.Now()); err != nil {
			return "", false, err
		}
	}
	if err := l.completeItem(ctx, candidate); err != nil {
		return "", false, err
	}
	l.mu.Lock()
	result.Landed++
	l.mu.Unlock()
	// Every landing moves the trunk the rolling tier has not proved yet. The count is what
	// makes "every 8 landings" a fact rather than a hope; a write that fails here is a
	// bookkeeping failure, reported rather than read as a refused landing.
	if err := l.bumpLandings(ctx); err != nil {
		l.reportf("loop: landed %s but could not record the landing count: %v\n", candidate.ItemID, err)
	}
	l.reportf("loop: landed %s (%s)\n", candidate.ItemID, candidate.HeadSHA)
	return "", true, nil
}

// headOf reads the worker checkout, not the boundary worktree of a fork lane.
// A pin that cannot name a head is skipped rather than failing bookkeeping.
func (l *Loop) headOf(ctx context.Context, lane string) string {
	head, err := l.revParse(ctx, l.laneTarget(lane), "HEAD")
	if err != nil {
		return ""
	}
	return head
}

// pin keeps a root commit reachable after lane reset and garbage collection.
func (l *Loop) pin(ctx context.Context, runID, sha string) error {
	return l.pinFrom(ctx, "", runID, sha)
}

func (l *Loop) pinFrom(ctx context.Context, checkout, runID, sha string) error {
	return writeRunPin(ctx, l.git, l.cfg.Root, checkout, runID, sha, false)
}

// pinLane also handles fork lanes, whose candidates live in a separate repository.
func (l *Loop) pinLane(ctx context.Context, lane, runID, sha string) error {
	return l.pinFrom(ctx, l.laneForkTarget(lane), runID, sha)
}

// maxRefComponent is the longest one path component of a ref name the file system stores:
// git keeps a loose ref as a file, and a file name holds 255 bytes.
const maxRefComponent = 255

// pinRef names the ref that keeps a run's commit reachable. A run id that fits a file name
// is pinned under its own name, which is where every reader looks. One that does not fit is
// pinned under a digest of the name, in a namespace of its own: the commit stays reachable,
// which is what a pin is for, and no reader that looks a pin up by run id finds it. A run id
// grown by repeated failover and restart suffixes made `git update-ref` fail on every
// restart on 2026-11-19, and each failure ended the loop.
func pinRef(runID string) string {
	if len(runID) <= maxRefComponent {
		return "refs/tillr/runs/" + runID
	}
	return fmt.Sprintf("refs/tillr/runs-long/%x", sha256.Sum256([]byte(runID)))
}

// pinTimeout bounds the fetch and ref write that survive the loop's stop.
const pinTimeout = 30 * time.Second

func (l *Loop) pendingItemBase(ctx context.Context, itemID string) (string, error) {
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var base string
	err = r.ReadTx(ctx, func(reader *store.Reader) error {
		works, err := readPending(reader)
		if err != nil {
			return err
		}
		if w := works[itemID]; w != nil {
			base = w.BaseSHA
		}
		return nil
	})
	return base, err
}

// fenceViolation answers the reason a candidate is not inside its lane's fence, or "" when
// it is. Two things make a candidate out of scope, and the first is the one a reader does
// not expect: a worktree whose status is not clean. The rest of the loop reads a candidate
// as a commit, and a working tree holding more than that commit is either work the commit
// dropped or a tree the gate would have judged in place of the one that lands.
//
// The fence itself is the file staging wrote: one repo-relative path per line. A path is
// inside the fence when it is the entry, or lies under it. An entry with no line for the
// path is a fence the path is outside of, and a lane whose fence is empty admits nothing —
// staging refuses such an item, so an empty fence here is a lane someone edited by hand.
func (l *Loop) fenceViolation(ctx context.Context, candidate Candidate) (string, error) {
	fencePath, err := l.itemFencePath(candidate.LaneID)
	if err != nil {
		return "", err
	}
	return l.fenceViolationAt(ctx, candidate, fencePath)
}

func (l *Loop) itemFencePath(lane string) (string, error) {
	fencePath := filepath.Join(l.laneDirPath(lane), ItemFenceFile)
	if _, err := os.Stat(fencePath); errors.Is(err, os.ErrNotExist) {
		// Legacy stages wrote only fence.txt, which was the full item fence.
		fencePath = filepath.Join(l.laneDirPath(lane), FenceFile)
	} else if err != nil {
		return "", err
	}
	return fencePath, nil
}

func (l *Loop) fenceViolationAt(ctx context.Context, candidate Candidate, fencePath string) (string, error) {
	status, err := l.git(ctx, candidate.Worktree, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if dirty := strings.TrimSpace(status); dirty != "" {
		return fmt.Sprintf("%s: the candidate worktree is not clean, so the commit is not the change that was judged: %s",
			ScopeEscape, strings.Join(strings.Split(dirty, "\n"), "; ")), nil
	}
	// Disable rename detection so both the removed source and added destination
	// are checked; a rename into scope must not hide a deletion outside it.
	changed, err := l.git(ctx, candidate.Worktree, "diff", "--no-renames", "--name-only", "-z", candidate.BaseSHA+".."+candidate.HeadSHA)
	if err != nil {
		return "", err
	}
	body, err := os.ReadFile(fencePath)
	if errors.Is(err, os.ErrNotExist) {
		// A lane with no fence is a lane whose candidate cannot be shown to be in scope.
		// That is a refusal, not a halt: the item is recorded as failed and the loop moves
		// on, rather than an unattended run ending on one lane's missing file.
		return fmt.Sprintf("%s: the lane has no fence at %s, so nothing shows the change is in scope", ScopeEscape, fencePath), nil
	}
	if err != nil {
		return "", Externalf("reading the lane's fence %s: %v", fencePath, err)
	}
	fence := fenceEntries(string(body))
	prefix := l.fencePrefix(ctx, candidate.Worktree)
	for _, path := range strings.Split(changed, "\x00") {
		if path == "" {
			continue
		}
		// A fork checkout is a repository of its own, so its diff names paths relative
		// to that checkout. The fence is repo-relative, so the path is moved into the
		// fence's spelling before it is compared.
		scoped := path
		if prefix != "" {
			scoped = prefix + path
		}
		if insideFence(scoped, fence) {
			continue
		}
		return fmt.Sprintf("%s: %s is outside allowed_paths (the fence is %s)",
			ScopeEscape, scoped, fencePath), nil
	}
	return "", nil
}

// fencePrefix is the repo-relative prefix a candidate's checkout sits under, when that
// checkout is a REPOSITORY OF ITS OWN. A fork checkout is: its diff names paths relative
// to the checkout while the fence is repo-relative, so the prefix moves one spelling into
// the other. A worktree of the main repository is not — its diff is already repo-relative —
// so it needs no prefix, and prefixing it puts every path outside a fence that names it.
//
// This function used to answer the prefix for any checkout under the repository root, on
// the stated premise that "the lane worktree is outside the repository". It is not: the
// lane worktree is `<root>/.tillr/lanes/<lane>/worktree`. On 2026-11-14 that premise
// refused every candidate in take 7 with `boundary-scope-escape` naming a path that was
// inside its fence, so the premise is now read from the repository rather than assumed
// from the path shape.
//
// A checkout whose repository cannot be read gets no prefix, which is the refusing
// direction: a fork checkout's relative path then fails the fence rather than silently
// matching it.
func (l *Loop) fencePrefix(ctx context.Context, worktree string) string {
	rel, err := filepath.Rel(l.cfg.Root, worktree)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	if same, ok := l.sharesRepository(ctx, worktree); !ok || same {
		return ""
	}
	return filepath.ToSlash(rel) + "/"
}

// sharesRepository answers whether worktree and the loop's root are checkouts of one
// repository, and whether that could be read at all. A git worktree shares its common
// directory with the repository it was added to; a clone or a fork checkout has its own.
func (l *Loop) sharesRepository(ctx context.Context, worktree string) (bool, bool) {
	mine, ok := l.commonDir(ctx, worktree)
	if !ok {
		return false, false
	}
	theirs, ok := l.commonDir(ctx, l.cfg.Root)
	if !ok {
		return false, false
	}
	return mine == theirs, true
}

// commonDir answers the absolute, symlink-resolved common directory of the repository the
// checkout at dir belongs to.
func (l *Loop) commonDir(ctx context.Context, dir string) (string, bool) {
	out, err := l.git(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", false
	}
	common := strings.TrimSpace(out)
	if common == "" {
		return "", false
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	if resolved, err := filepath.EvalSymlinks(common); err == nil {
		common = resolved
	}
	return filepath.Clean(common), true
}

// fenceEntries answers the fence as the paths it names: the file's non-empty lines, in the
// order staging wrote them.
func fenceEntries(body string) []string {
	entries := []string{}
	for _, line := range strings.Split(body, "\n") {
		if entry := strings.TrimSpace(line); entry != "" {
			entries = append(entries, entry)
		}
	}
	return entries
}

// insideFence answers whether one changed path is inside the fence. An entry admits itself
// and everything under it, so a directory may be spelled with or without its trailing slash
// (the item face calls the field a list of repo-relative path prefixes, and its own fixtures
// spell directories both ways). Nothing else is admitted: a path no entry covers is a path
// the fence does not cover.
func insideFence(path string, fence []string) bool {
	for _, entry := range fence {
		if path == entry || strings.HasPrefix(path, strings.TrimSuffix(entry, "/")+"/") {
			return true
		}
	}
	return false
}

// abandon resets a lane after a dispatch that did not land. The lane goes back to the trunk
// so the next tick's run starts from the same place, and a non-empty head is pinned first:
// a refused, conflicted or unjudged head is evidence, and a lane reset is what would make it
// unreachable.
func (l *Loop) abandon(ctx context.Context, lane, runID, headSHA string, reasons ...string) string {
	return l.abandonFromBase(ctx, lane, runID, headSHA, "", reasons...)
}

// A hold supplies the whole-item base so clean committed work is recorded against
// the item under a preserved ref before the lane is returned to rotation.
func (l *Loop) abandonFromBase(ctx context.Context, lane, runID, headSHA, base string, reasons ...string) string {
	if l.nonLanding {
		// Plain lane files stay available for inspection; there is no Git tree to preserve or reset.
		return ""
	}
	reason := "settled without landing"
	if len(reasons) > 0 {
		reason = reasons[0]
	}
	if err := l.preserveWorkFromBase(ctx, lane, runID, base, reason); err != nil {
		return l.retainUnpreserved(ctx, lane, runID, headSHA, err)
	}
	if err := l.pinLane(ctx, lane, runID, headSHA); err != nil {
		return l.retainUnpreserved(ctx, lane, runID, headSHA, err)
	}
	if runID != "" {
		retained, err := l.retainedWorkerSession(ctx, runID)
		if err != nil {
			return l.retainUnpreserved(ctx, lane, runID, headSHA, err)
		}
		if retained != nil {
			// Item-block retirement is loop-owned preparation, not work the
			// returning worker may inherit as its own change.
			if retained.HeadSHA != "" && headSHA != "" && retained.HeadSHA != headSHA {
				if _, err := l.git(ctx, l.laneTarget(lane), "reset", "--hard", retained.HeadSHA); err != nil {
					return l.retainUnpreserved(ctx, lane, runID, headSHA, err)
				}
			}
			return ""
		}
	}
	if err := l.resetLane(ctx, lane); err != nil {
		// The reset is not the tick's outcome: the next tick's own stage step resets the
		// lane again, and failing the loop here would end an unattended run over a lane
		// that is about to be reset anyway.
		l.reportf("loop: lane %s could not be reset: %v\n", lane, err)
	}
	return ""
}

// reportf writes one line to the loop's report under the report lock, so two lanes'
// goroutines never interleave a line and a caller need not hand in a safe writer.
func (l *Loop) reportf(format string, args ...any) {
	l.reportMu.Lock()
	defer l.reportMu.Unlock()
	fmt.Fprintf(l.cfg.Report, format, args...)
}

// snapshot takes one side of a boundary bracket whose targets are the given lane worktrees.
//
// The store is deliberately not named: `run exec` writes it inside the bracket, so a store
// delta there has a legitimate cause and cannot be a stop signal (BoundarySpec.Store). The
// sibling lanes are named: the loop holds them, and one run's write into another's worktree
// is an escape, not a neighbour's work.
func (l *Loop) snapshot(ctx context.Context, targets, watched []string) (*Boundary, error) {
	if l.nonLanding {
		return nil, nil
	}
	spec := BoundarySpec{Repo: l.cfg.Root, Targets: targets, LaneRoot: l.cfg.LaneRoot}
	// Only the lanes this tick is not dispatching are watched. A lane whose own run is in
	// flight writes in its own worktree, and a sibling bracket that watched it would convict
	// the sibling's own work as a write into it.
	//
	// A pass computes its watched set from the lanes idle when it began, and a lane refusal
	// can re-plan an item onto one of them in the same pass (stageDispatches). That lane is
	// then this bracket's own target, and a run's own worktree is never a lane it watches:
	// naming it as both is a usage error TakeBoundary refuses, which stopped the loop on
	// 2026-11-17. A later sibling start on a watched lane is the epoch check's to excuse.
	for _, lane := range watched {
		if path := l.lanePath(lane); !isTarget(targets, path) {
			spec.Lanes = append(spec.Lanes, path)
		}
	}
	for _, lane := range l.cfg.Lanes {
		if !isTarget(targets, l.lanePath(lane)) {
			// Every lane this bracket does not own, dispatching or not: what a sibling's
			// run registers inside its own lane directory is the sibling's to answer for.
			spec.SiblingDirs = append(spec.SiblingDirs, l.laneDirPath(lane))
			continue
		}
		// A fork-series lane delivers through its patch branch, so the branch this
		// bracket's run may move is named beside the worktree it owns.
		if ref := l.lanePatchRef(lane); ref != "" {
			spec.PatchRefs = append(spec.PatchRefs, ref)
		}
	}
	return TakeBoundary(ctx, spec)
}

// stopForViolations reports one run's violations — the paths in the report, the same paths
// in the loop's stop detail, which is what an operator reads at exit — and answers the stop.
func (l *Loop) stopForViolations(violations []Violation, items, lanes []string, result *LoopResult) string {
	lines := make([]string, 0, len(violations))
	for _, violation := range violations {
		lines = append(lines, violation.String())
	}
	detail := fmt.Sprintf("%d change(s) outside %s: %s", len(violations), strings.Join(lanes, ", "), strings.Join(lines, "; "))
	l.mu.Lock()
	result.Detail = detail
	result.StopItemID = strings.Join(items, ", ")
	result.StopStep = "boundary check"
	l.mu.Unlock()
	l.reportf("loop: boundary violation during %s:\n  %s\n",
		strings.Join(items, ", "), strings.Join(lines, "\n  "))
	return StopHostBoundaryViolation
}

// laneDirPath is where one lane's directory lives: <lane-root>/<lane-id>. It is the lane
// directory `run exec` writes its log into, and the directory the brief, the fence and the
// base sha sit in — the parent of the worktree, so no staged artifact can land in the diff.
func (l *Loop) laneDirPath(lane string) string { return filepath.Join(l.cfg.LaneRoot, lane) }

// lanePath is the worktree a lane's run is confined to: <lane-dir>/worktree. It is the
// boundary target, the gate's directory and the candidate's tree, and it is what `run exec`
// makes the adapter's working directory.
func (l *Loop) lanePath(lane string) string {
	return filepath.Join(l.laneDirPath(lane), WorktreeDir)
}

// laneTarget is the checkout a lane's candidate lives in: the target staging wrote when the
// item is a fork-series item, and the lane worktree otherwise. A target that is missing or
// empty is the ordinary lane, which is also what a lane staged before this file existed
// reads as.
func (l *Loop) laneTarget(lane string) string {
	if target := l.laneForkTarget(lane); target != "" {
		return target
	}
	return l.lanePath(lane)
}

// laneForkTarget is the fork checkout staging recorded for the lane's item, or "" for an
// ordinary item. A recorded target counts only when the lane's own item fence names a path
// inside it — staging rewrites every fork-series fence to `.forks/lanes/<lane>/<fork>` —
// because a lane is reused, and a lane staged before staging cleared a fork item's files
// still holds a target its current item never asked for (lane-9, 2026-11-17).
func (l *Loop) laneForkTarget(lane string) string {
	target := l.laneFile(lane, TargetFile)
	if target == "" {
		return ""
	}
	fencePath, err := l.itemFencePath(lane)
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(fencePath)
	if err != nil {
		return ""
	}
	root, err := filepath.Abs(l.cfg.Root)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	for _, entry := range fenceEntries(string(body)) {
		entry = strings.TrimSuffix(entry, "/")
		if entry == rel || strings.HasPrefix(entry, rel+"/") {
			return target
		}
	}
	return ""
}

// laneBase is the sha staging pinned for the lane's candidate: the trunk for an ordinary
// item, and the fork checkout's own HEAD for a fork-series item.
func (l *Loop) laneBase(lane string) string {
	if base := l.laneFile(lane, ItemBaseSHAFile); base != "" {
		return base
	}
	return l.laneFile(lane, BaseSHAFile)
}

// lanePatchRef is the patch branch a fork-series item's delivery moves, and empty for an
// ordinary item whose landing moves the trunk.
func (l *Loop) lanePatchRef(lane string) string {
	if l.laneForkTarget(lane) == "" {
		return ""
	}
	return l.laneFile(lane, PatchRefFile)
}

// laneFile answers one line staging wrote in the lane directory, or "" when the file is not
// there. A lane is reused, so a file left by a previous run is overwritten by the next
// stage, and staging an ordinary item removes a fork item's target and patch ref.
func (l *Loop) laneFile(lane, name string) string {
	body, err := os.ReadFile(filepath.Join(l.laneDirPath(lane), name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// stageLanes creates the available lane worktrees before the first tick. A branch held
// elsewhere leaves its lane out of rotation, not the whole loop; no available lane is
// still a startup refusal.
func (l *Loop) stageLanes(ctx context.Context) error {
	if err := l.recheckLanes(ctx); err != nil {
		return err
	}
	var held []string
	for _, lane := range l.cfg.Lanes {
		path := l.lanePath(lane)
		if err := os.MkdirAll(l.laneDirPath(lane), 0o755); err != nil {
			return Externalf("creating the lane directory %s: %v", l.laneDirPath(lane), err)
		}
		// A lane directory that was deleted leaves its registration behind, and that
		// registration is what makes `worktree add` refuse the branch the lane is on — the
		// canary's second attempt died on exactly that message before a tick could run.
		// Pruning first removes only registrations whose directory is already gone.
		if err := withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
			_, err := l.git(ctx, l.cfg.Root, "worktree", "prune")
			return err
		}); err != nil {
			return err
		}
		// The branch is scoped by the lane root, so a holder is this lane's own directory
		// moved aside; the check names it instead of leaving the fallback to git's `fatal:`
		// line, exactly as the stager's does. It runs after the prune so a registration whose
		// directory is gone is cleared rather than refused.
		branch := LaneBranch(l.cfg.LaneRoot, lane)
		holder, err := branchHolder(ctx, l.cfg.Root, branch, path)
		if err != nil {
			return err
		}
		if holder != "" {
			refusal := asLaneRefusal(refuseBranchHeld(lane, l.cfg.LaneRoot, branch, holder))
			if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
				return err
			}
			held = append(held, refusal.Msg)
			continue
		}
		if _, err := os.Stat(path); err == nil {
			// An existing path can be detached or on a different branch, so check
			// for holders before leaving it to the stager to reuse or refuse.
			continue
		}
		// Removing a holder frees its branch, not the work committed on it.
		// Startup must protect that work just as dispatch staging does, before
		// recreating a missing worktree resets the branch to trunk.
		base, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return err
		}
		stager := &LaneStager{Root: l.cfg.Root, LaneRoot: l.cfg.LaneRoot, Store: l.cfg.Store, Now: l.cfg.Now}
		if err := stager.refuseUnlandedCommit(ctx, StageSpec{LaneID: lane, BaseSHA: base}, path); err != nil {
			refusal := asLaneRefusal(err)
			if refusal == nil {
				return err
			}
			if err := l.takeLaneOutOfRotation(ctx, refusal); err != nil {
				return err
			}
			held = append(held, refusal.Msg)
			continue
		}
		if err := withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
			_, err := l.git(ctx, l.cfg.Root, "worktree", "add", "--force", "-B", branch, path, base)
			return err
		}); err != nil {
			return err
		}
	}
	if len(held) == len(l.cfg.Lanes) {
		return Externalf("no lane is available at loop start: %s", strings.Join(held, "\n"))
	}
	return nil
}

// registerLanePools records startup capacity separately from Git worktree staging.
// Register unused lanes too, so views can show the configured pools before
// their first dispatch. Never replace an existing lease or its holder.
func (l *Loop) registerLanePools(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return Externalf("registering configured lanes in %s: %v", l.cfg.Store, err)
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, lane := range l.cfg.Lanes {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state) VALUES(?,?) ON CONFLICT(lane_id) DO NOTHING`, lane, run.LaneStateIdle); err != nil {
				return err
			}
		}
		return nil
	})
}

// resetLane puts one lane's worktree back on the trunk: the branch is re-created at the
// trunk and the working tree reset to it. Untracked files are left alone — the run's own
// log lives in the lane directory, and deleting a run's evidence to tidy a lane is not a
// reset's job.
func (l *Loop) resetLane(ctx context.Context, lane string) error {
	return l.resetLaneTo(ctx, lane, TrunkRef)
}

func (l *Loop) resetLaneTo(ctx context.Context, lane, revision string) error {
	if strings.HasPrefix(lane, "desk-") {
		return withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
			return l.resetLaneToRegistryLocked(ctx, lane, revision)
		})
	}
	return l.resetLaneToRegistryLocked(ctx, lane, revision)
}

// Desk removal requires the registry lock. Other lane resets do not change the
// registry. Release callers acquire it before their SQLite writer, not within it.
func (l *Loop) resetLaneToRegistryLocked(ctx context.Context, lane, revision string) error {
	path := l.lanePath(lane)
	if strings.HasPrefix(lane, "desk-") {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		if _, err := l.git(ctx, path, "reset", "--hard", revision); err != nil {
			return err
		}
		_, err := l.git(ctx, l.cfg.Root, "worktree", "remove", "--force", path)
		return err
	}
	// A lock a killed git left refuses every write below (index_lock.go).
	if err := l.clearStaleIndexLock(ctx, lane, ""); err != nil {
		return err
	}
	// A lane left mid-rebase, mid-merge or mid-cherry-pick keeps that state through a
	// checkout, so the next rebase there refuses; the operation is aborted first.
	l.abortInProgress(ctx, path)
	// A hard reset keeps untracked files. Every caller preserves the lane's dirty work
	// (preserveWork) before it resets, so those files already have a durable copy; left
	// behind, they released a lane with a settled run's files in it (2026-11-17, lane-12).
	// The clean runs before the reset, under the ignore rules the snapshot read: a file
	// the run's own ignore edit hid from the snapshot stays ignored here, and is not
	// deleted once the reset restores the trunk's rules. Ignored files stay.
	if _, err := l.git(ctx, path, "clean", "-fd"); err != nil {
		return err
	}
	if _, err := l.git(ctx, path, "checkout", "--force", "-B", LaneBranch(l.cfg.LaneRoot, lane), revision); err != nil {
		return err
	}
	if _, err := l.git(ctx, path, "reset", "--hard", revision); err != nil {
		return err
	}
	return nil
}

// revParse answers one revision in one checkout, trimmed.
func (l *Loop) revParse(ctx context.Context, dir, rev string) (string, error) {
	out, err := l.git(ctx, dir, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// git runs one git command in a directory and answers its stdout. A failure is reported
// with git's own stderr, because "a git command failed" is not a diagnostic an operator can
// act on at 3am.
//
// Inside a gathered write (writeGathered) the answer is the one gathered before the write;
// a command that changes the repository must never be issued there, because a gathered
// answer is a read.
func (l *Loop) git(ctx context.Context, dir string, args ...string) (string, error) {
	return gather(ctx, gitKey("stdout", dir, args), func(ctx context.Context) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", Externalf("git %s in %s: %v: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
		}
		return string(out), nil
	})
}

// countPtr answers a pointer to a copy of n, for Pass's pointer fields.
func countPtr(n int) *int { return &n }
