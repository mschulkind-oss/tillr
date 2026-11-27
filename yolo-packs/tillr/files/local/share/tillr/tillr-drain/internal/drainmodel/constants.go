package drainmodel

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr/settings"
)

// Env carries immutable workspace, store, clock, build, and logging capabilities
// for loop-free subsystems.
type Env struct {
	Root             string
	StorePath        string
	LaneRoot         string
	GateResultsStore string
	Now              func() time.Time
	Build            BuildStamp
	Reportf          func(format string, args ...any)
}

// TrunkRef is the repository's default trunk branch.
const TrunkRef = settings.DefaultTrunkBranch

// Record kinds and producer metadata spoken by drain.
const (
	PassKind       = "drain.pass"
	DecisionKind   = "drain.decision"
	LoopResultKind = "drain.loop"
	Utility        = "drain"
	Version        = "0.1.0"
)

// Decision actions.
const (
	ActionDispatch = "dispatch"
	ActionRetry    = "retry"
	ActionHold     = "hold"
)

// Planner hold/dispatch reasons defined in schema.go.
const (
	ReasonBackendWindow       = "backend-window"
	ReasonBackendClosed       = "backend-closed"
	ReasonBackendMissing      = "backend-missing"
	ReasonSeatUnmapped        = "seat-unmapped"
	ReasonCommandFault        = "command-fault"
	ReasonReady               = "ready"
	ReasonBlocked             = "blocked"
	ReasonOwnerOnlyOwed       = "owner-only-owed"
	ReasonNotReady            = "not-ready"
	ReasonRunLinkMissing      = "run-link-missing"
	ReasonBoundaryScopeEscape = "boundary-scope-escape"
	ReasonCandidateNoOp       = "candidate-no-op"
	ReasonRunUnjudged         = "run-unjudged"
	ReasonGateFailed          = "gate-failed"
	ReasonGateReplay          = "gate-replay"
	ReasonLandRefused         = "land-refused"
	ReasonNoPolicy            = "no-policy"
	ReasonNoLane              = "no-lane"
	ReasonAgentLimit          = "agent-limit"
	ReasonRunLive             = "run-live"
	ReasonNoFence             = "no-fence"
	ReasonForkHeld            = "fork-held"
	ReasonInfraExhausted      = "run-infra-exhausted"
	ReasonRetry               = "operator-retry"
	ReasonReturned            = "returned-with-evidence"
	ReasonRetryJudged         = "retry-judged"
	ReasonRetryUnjudged       = "retry-unjudged"
	ReasonQuestionOpen        = "question-open"
	ReasonQuestionsUnknown    = "questions-unknown"
	ReasonDesignDocMissing    = "design-doc-missing"
	ReasonTrunkBroken         = "trunk-broken"
	ReasonConflictFix         = "conflict-fix"
	ReasonConflictExhausted   = "conflict-exhausted"
	ReasonItemUnstagleable    = "item-unstagleable"
	ReasonStagingFailed       = "staging-failed"
)

// Item and lane states read by drain.
const (
	StateInQueue    = "in-queue"
	StateBlocked    = "blocked"
	StateInProgress = "in-progress"
	StateLanded     = "landed"
	LaneStateLeased = "leased"
)

// RefusalDeliveryOrder is the refusal step for delivery-order violations.
const RefusalDeliveryOrder = "delivery-order"

// Generated repair item prefixes.
const (
	TrunkFixerPrefix  = "fixer-main-"
	FlakyRepairPrefix = "flaky-"
	RevertItemPrefix  = "revert-main-"
	DefectItemPrefix  = "defect-"
)

// IsTrunkFixer answers whether an item is a trunk fixer.
func IsTrunkFixer(itemID string) bool {
	return strings.HasPrefix(itemID, TrunkFixerPrefix)
}

// IsFlakyRepairItem answers whether itemID is a generated repair item for a disabled check.
func IsFlakyRepairItem(itemID string) bool {
	return strings.HasPrefix(itemID, FlakyRepairPrefix)
}

// IsRemoteSyncItem reports whether an item is a generated remote-sync repair.
func IsRemoteSyncItem(itemID string) bool {
	return remotesync.IsRemoteSyncItem(itemID)
}

// IsConflictFixRun reports whether this run is itself a conflict-fix run.
func IsConflictFixRun(runID string) bool {
	return strings.HasSuffix(runID, ConflictFixSuffix)
}

// IsExpressLane reports whether a lane id is an express lane.
func IsExpressLane(id string) bool {
	return strings.HasPrefix(id, "express-")
}

// IsPoolLaneName reports whether id has the shape of a pool or desk lane name
// (lane-*, express-*, or desk-*).
func IsPoolLaneName(id string) bool {
	return strings.HasPrefix(id, "lane-") || IsExpressLane(id) || strings.HasPrefix(id, "desk-")
}

// IsRevertItem answers whether a landing's item id is an automatic revert.
func IsRevertItem(itemID string) bool {
	return strings.HasPrefix(itemID, RevertItemPrefix)
}

// IsDefectItem answers whether an item is a generated tooling-defect item.
func IsDefectItem(itemID string) bool {
	return strings.HasPrefix(itemID, DefectItemPrefix)
}
