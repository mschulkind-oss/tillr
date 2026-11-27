package drain

import (
	"context"
	"os/exec"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/store"
)

// Type aliases to internal/drainmodel.
type (
	ExitError            = drainmodel.ExitError
	ItemContentError     = drainmodel.ItemContentError
	SpendTotals          = drainmodel.SpendTotals
	PassTiming           = drainmodel.PassTiming
	DrainingLane         = drainmodel.DrainingLane
	StopExplanation      = drainmodel.StopExplanation
	CandidateLaneState   = drainmodel.CandidateLaneState
	GatePolicy           = drainmodel.GatePolicy
	AcceptRewriteRequest = drainmodel.AcceptRewriteRequest
	RewriteAcceptance    = drainmodel.RewriteAcceptance
	StepSpan             = drainmodel.StepSpan
	stderrTail           = drainmodel.StderrTail
	runPinError          = drainmodel.RunPinError
	landingCallChoice    = drainmodel.LandingCallChoice
)

// Constants re-exported from internal/drainmodel.
const (
	TrunkRef                      = drainmodel.TrunkRef
	PassKind                      = drainmodel.PassKind
	DecisionKind                  = drainmodel.DecisionKind
	LoopResultKind                = drainmodel.LoopResultKind
	Utility                       = drainmodel.Utility
	Version                       = drainmodel.Version
	ActionDispatch                = drainmodel.ActionDispatch
	ActionRetry                   = drainmodel.ActionRetry
	ActionHold                    = drainmodel.ActionHold
	ReasonBackendWindow           = drainmodel.ReasonBackendWindow
	ReasonBackendClosed           = drainmodel.ReasonBackendClosed
	ReasonBackendMissing          = drainmodel.ReasonBackendMissing
	ReasonSeatUnmapped            = drainmodel.ReasonSeatUnmapped
	ReasonCommandFault            = drainmodel.ReasonCommandFault
	ReasonReady                   = drainmodel.ReasonReady
	ReasonBlocked                 = drainmodel.ReasonBlocked
	ReasonOwnerOnlyOwed           = drainmodel.ReasonOwnerOnlyOwed
	ReasonNotReady                = drainmodel.ReasonNotReady
	ReasonRunLinkMissing          = drainmodel.ReasonRunLinkMissing
	ReasonBoundaryScopeEscape     = drainmodel.ReasonBoundaryScopeEscape
	ReasonCandidateNoOp           = drainmodel.ReasonCandidateNoOp
	ReasonRunUnjudged             = drainmodel.ReasonRunUnjudged
	ReasonGateFailed              = drainmodel.ReasonGateFailed
	ReasonGateReplay              = drainmodel.ReasonGateReplay
	ReasonLandRefused             = drainmodel.ReasonLandRefused
	ReasonNoPolicy                = drainmodel.ReasonNoPolicy
	ReasonNoLane                  = drainmodel.ReasonNoLane
	ReasonAgentLimit              = drainmodel.ReasonAgentLimit
	ReasonWorktreeBound           = drainmodel.ReasonWorktreeBound
	ReasonRunLive                 = drainmodel.ReasonRunLive
	ReasonNoFence                 = drainmodel.ReasonNoFence
	ReasonForkHeld                = drainmodel.ReasonForkHeld
	ReasonInfraExhausted          = drainmodel.ReasonInfraExhausted
	ReasonRetry                   = drainmodel.ReasonRetry
	ReasonReturned                = drainmodel.ReasonReturned
	ReasonRetryJudged             = drainmodel.ReasonRetryJudged
	ReasonRetryUnjudged           = drainmodel.ReasonRetryUnjudged
	ReasonQuestionOpen            = drainmodel.ReasonQuestionOpen
	ReasonQuestionsUnknown        = drainmodel.ReasonQuestionsUnknown
	ReasonNeedsCompanionPlanSlice = drainmodel.ReasonNeedsCompanionPlanSlice
	ReasonDesignDocMissing        = drainmodel.ReasonDesignDocMissing
	ReasonTrunkBroken             = drainmodel.ReasonTrunkBroken
	ReasonConflictFix             = drainmodel.ReasonConflictFix
	ReasonConflictExhausted       = drainmodel.ReasonConflictExhausted
	ReasonItemUnstagleable        = drainmodel.ReasonItemUnstagleable
	ReasonStagingFailed           = drainmodel.ReasonStagingFailed
	StateInQueue                  = drainmodel.StateInQueue
	StateBlocked                  = drainmodel.StateBlocked
	StateInProgress               = drainmodel.StateInProgress
	StateLanded                   = drainmodel.StateLanded
	LaneStateLeased               = drainmodel.LaneStateLeased
	RefusalDeliveryOrder          = drainmodel.RefusalDeliveryOrder
	TrunkFixerPrefix              = drainmodel.TrunkFixerPrefix
	FlakyRepairPrefix             = drainmodel.FlakyRepairPrefix
	RevertItemPrefix              = drainmodel.RevertItemPrefix
	DefectItemPrefix              = drainmodel.DefectItemPrefix
	TillrDir                      = drainmodel.TillrDir
	LoopDirName                   = drainmodel.LoopDirName
	StepsFile                     = drainmodel.StepsFile
	StderrTailBytes               = drainmodel.StderrTailBytes
	DefaultGateBudget             = drainmodel.DefaultGateBudget
	PassDDL                       = drainmodel.PassDDL
	GateBudgetItemPrefix          = drainmodel.GateBudgetItemPrefix
	SessionIDFile                 = drainmodel.SessionIDFile
	ConflictFixSuffix             = drainmodel.ConflictFixSuffix
	maxRunItemSlugLen             = drainmodel.MaxRunItemSlugLen
	maxRunIDBaseLen               = drainmodel.MaxRunIDBaseLen
	maxRefComponent               = drainmodel.MaxRefComponent
	itemPinPrefix                 = drainmodel.ItemPinPrefix
	itemPinLongPrefix             = drainmodel.ItemPinLongPrefix
	pinTimeout                    = drainmodel.PinTimeout
	generationDDL                 = drainmodel.GenerationDDL
	rewriteAcceptedKind           = drainmodel.RewriteAcceptedKind
	acceptedTrunkPrefix           = drainmodel.AcceptedTrunkPrefix
)

// Variables re-exported from internal/drainmodel.
var (
	classifyCheck          = checkresult.Classify
	runIDMarker            = drainmodel.RunIDMarker
	runIDContinuationKinds = drainmodel.RunIDContinuationKinds
	traceLinePrefixes      = drainmodel.TraceLinePrefixes
	traceLinePrefixLen     = drainmodel.TraceLinePrefixLen
	legacyPassMigrationDDL = drainmodel.LegacyPassMigrationDDL
)

func wrapFixerError(err error) error { return drainmodel.WrapFixerError(err) }

func Usagef(format string, args ...any) error { return drainmodel.Usagef(format, args...) }

func Incompatiblef(format string, args ...any) error {
	return drainmodel.Incompatiblef(format, args...)
}

func Externalf(format string, args ...any) error { return drainmodel.Externalf(format, args...) }

func Internalf(format string, args ...any) error { return drainmodel.Internalf(format, args...) }

func ItemContentf(itemID, field, format string, args ...any) error {
	return drainmodel.ItemContentf(itemID, field, format, args...)
}

func IsItemContent(err error) bool { return drainmodel.IsItemContent(err) }

func ExitCode(err error) int { return drainmodel.ExitCode(err) }

func runItemSlug(itemID string) string { return drainmodel.RunItemSlug(itemID) }

func boundRunIDBase(base string) string { return drainmodel.BoundRunIDBase(base) }

func judgeRunID(runID string) string { return drainmodel.JudgeRunID(runID) }

func conflictFixRunID(runID string) string { return drainmodel.ConflictFixRunID(runID) }

func attemptRunID(itemID string, attempt int) string {
	return drainmodel.AttemptRunID(itemID, attempt)
}

func attemptFromRunID(itemID, runID string) int {
	return drainmodel.AttemptFromRunID(itemID, runID)
}

func splitRunID(id string) (string, int, string) { return drainmodel.SplitRunID(id) }

func markRunID(id, kind string) string { return drainmodel.MarkRunID(id, kind) }

func runIDAncestors(id string) []string { return drainmodel.RunIDAncestors(id) }

func markRunIDTurns(base, kind string, turns int) string {
	return drainmodel.MarkRunIDTurns(base, kind, turns)
}

func pinRef(runID string) string { return drainmodel.PinRef(runID) }

func itemPinRef(itemID string) string { return drainmodel.ItemPinRef(itemID) }

func writeRunPin(ctx context.Context, git func(context.Context, string, ...string) (string, error), repo, runID, sha string, createOnly bool) error {
	return drainmodel.WriteRunPin(ctx, git, repo, runID, sha, createOnly)
}

func writeItemPin(ctx context.Context, git func(context.Context, string, ...string) (string, error), repo, itemID, sha string) error {
	return drainmodel.WriteItemPin(ctx, git, repo, itemID, sha)
}

func NewSessionID() (string, error) { return drainmodel.NewSessionID() }

func ReadSessionID(laneDir string) (string, error) { return drainmodel.ReadSessionID(laneDir) }

func sessionIDFor(pinned string) (string, error) { return drainmodel.SessionIDFor(pinned) }

func LoopDir(root string) string { return drainmodel.LoopDir(root) }

func RunDir(root, runID string) string { return drainmodel.RunDir(root, runID) }

func LoopResultFile(root, first, last string) string {
	return drainmodel.LoopResultFile(root, first, last)
}

func isTraceLine(line []byte) bool { return drainmodel.IsTraceLine(line) }

func appendStepSpan(root, runID string, span StepSpan) error {
	return drainmodel.AppendStepSpan(root, runID, span)
}

func appendRunLine(root, runID, name string, value any) error {
	return drainmodel.AppendRunLine(root, runID, name, value)
}

func cwdOf(cmd *exec.Cmd) string { return drainmodel.CwdOf(cmd) }

func exitCodeOf(cmd *exec.Cmd) *int { return drainmodel.ExitCodeOf(cmd) }

func runnerProofPaths(paths string) bool { return drainmodel.RunnerProofPaths(paths) }

func (l *Loop) runnerProofChange(ctx context.Context, c Candidate) (bool, error) {
	paths, err := l.git(ctx, c.Worktree, "diff", "--name-only", "--no-renames", "-z", c.BaseSHA, c.HeadSHA)
	return drainmodel.RunnerProofPaths(paths), err
}

func recordPassTiming(ctx context.Context, storePath string, timing PassTiming) error {
	return drainmodel.RecordPassTiming(ctx, storePath, timing)
}

func recordAccumulatedPassTiming(ctx context.Context, storePath string, timing *PassTiming) error {
	return drainmodel.RecordAccumulatedPassTiming(ctx, storePath, timing)
}

func aggregatePassTiming(ctx context.Context, storePath, id string) (PassTiming, error) {
	return drainmodel.AggregatePassTiming(ctx, storePath, id)
}

func (l *Loop) recordPass(timing *PassTiming) error {
	return drainmodel.RecordAndReportPassTiming(l.cfg.Store, l.reportf, timing)
}

func IsGateBudgetItem(itemID string) bool { return drainmodel.IsGateBudgetItem(itemID) }

func boolInt(value bool) int { return drainmodel.BoolInt(value) }

func seconds(d time.Duration) string { return drainmodel.Seconds(d) }

func ExplainStop(result LoopResult, at time.Time) *StopExplanation {
	return drainmodel.ExplainStopFields(result.Stop, result.Detail, result.StopItemID, result.StopStep, at)
}

func PendingLaneStates(r *store.Reader) (map[string]CandidateLaneState, error) {
	return drainmodel.PendingLaneStates(r)
}

func allocateGeneration(tx *store.Tx) (string, error) { return drainmodel.AllocateGeneration(tx) }

func ReadGatePolicy(root string) (GatePolicy, bool, error) {
	return drainmodel.ReadGatePolicy(root)
}

func CheckTrunkWorktree(ctx context.Context, root string) error {
	return drainmodel.CheckTrunkWorktree(ctx, root)
}

func AcceptRewrite(ctx context.Context, req AcceptRewriteRequest) (RewriteAcceptance, error) {
	return drainmodel.AcceptRewrite(ctx, req)
}

func chooseLandingCallAnswer(outcome string) landingCallChoice {
	return drainmodel.ChooseLandingCallAnswer(outcome)
}

func IsTrunkFixer(itemID string) bool { return drainmodel.IsTrunkFixer(itemID) }

func IsFlakyRepairItem(itemID string) bool { return drainmodel.IsFlakyRepairItem(itemID) }

func IsRemoteSyncItem(itemID string) bool { return drainmodel.IsRemoteSyncItem(itemID) }

func isConflictFixRun(runID string) bool { return drainmodel.IsConflictFixRun(runID) }

func isExpressLane(id string) bool { return drainmodel.IsExpressLane(id) }

func isPoolLaneName(id string) bool { return drainmodel.IsPoolLaneName(id) }

func IsRevertItem(itemID string) bool { return drainmodel.IsRevertItem(itemID) }

func IsDefectItem(itemID string) bool { return drainmodel.IsDefectItem(itemID) }

func inputHash(v any) (string, error) { return drainmodel.InputHash(v) }
