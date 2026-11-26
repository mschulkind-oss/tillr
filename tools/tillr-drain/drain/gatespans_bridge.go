package drain

import (
	"context"
	"os/exec"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gatespans"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	GateSpansFile       = gatespans.GateSpansFileName
	GateFastVerdictFile = gatespans.GateFastVerdictFile
	GateChecksFile      = gatespans.GateChecksFile
	GateSpansRead       = gatespans.GateSpansRead
	GateSpansAbsent     = gatespans.GateSpansAbsent
	GateSpansUnreadable = gatespans.GateSpansUnreadable

	gateTimingFile      = gatespans.GateTimingFile
	gateExecutionRan    = gatespans.GateExecutionRan
	gateExecutionReused = gatespans.GateExecutionReused
	gateExecutionNotRun = gatespans.GateExecutionNotRun

	gateScriptDir = gatespans.GateScriptDir
	gateBinaryDir = gatespans.GateBinaryDir

	GreenRef              = gatespans.GreenRef
	HeavyProgressKind     = gatespans.HeavyProgressKind
	goTestPrefix          = gatespans.GoTestPrefix
	checkGroupGrace       = gatespans.CheckGroupGrace
	heavyCleanupGuard     = gatespans.HeavyCleanupGuard
	heavyReplayGuard      = gatespans.HeavyReplayGuard
	heavyAttributionsFile = gatespans.HeavyAttributionsFile
	heavyReplaysFile      = gatespans.HeavyReplaysFile

	goTestAbsent  = gatespans.GoTestAbsent
	goTestSkipped = gatespans.GoTestSkipped

	goAffectedCheckID = gatespans.GoAffectedCheckID

	GateStalled          = gatespans.GateStalled
	GateStallExit        = gatespans.GateStallExit
	ReasonGateStallRerun = gatespans.ReasonGateStallRerun
	ReasonGateStalled    = gatespans.ReasonGateStalled
	gateStallHeldKind    = gatespans.GateStallHeldKind
	ReasonGateNoVerdict  = gatespans.ReasonGateNoVerdict
)

type (
	gateSpan              = gatespans.GateSpan
	gateSpansFile         = gatespans.GateSpansFile
	gateSkip              = gatespans.GateSkip
	gateSummary           = gatespans.GateSummary
	gateSpanRow           = gatespans.GateSpanRow
	RollingCoverage       = gatespans.RollingCoverage
	gateCheckMeasurement  = gatespans.GateCheckMeasurement
	fastGateTimingPayload = gatespans.FastGateTimingPayload
	HeavyStep             = gatespans.HeavyStep
	heavyDeclaration      = gatespans.HeavyDeclaration
	legacyWatch           = gatespans.LegacyWatch
	legacyDeclaration     = gatespans.LegacyDeclaration
	goModuleInfo          = gatespans.GoModuleInfo
	StallObservation      = gatespans.StallObservation

	HeavyCheck             = gatespans.HeavyCheck
	HeavyRun               = gatespans.HeavyRun
	HeavyOutcome           = gatespans.HeavyOutcome
	HeavyStatus            = gatespans.HeavyStatus
	HeavyRunner            = gatespans.HeavyRunner
	HeavyAttempt           = gatespans.HeavyAttempt
	HeavyReplay            = gatespans.HeavyReplay
	RevertOutcome          = gatespans.RevertOutcome
	LoopLock               = gatespans.LoopLock
	HeavyProgressFact      = gatespans.HeavyProgressFact
	heavyUnownedKey        = gatespans.HeavyUnownedKey
	heavyInverseKey        = gatespans.HeavyInverseKey
	heavyProgressReportKey = gatespans.HeavyProgressReportKey
	heavyCheckEndedKey     = gatespans.HeavyCheckEndedKey
	heavyOwnershipKey      = gatespans.HeavyOwnershipKey
	heavyOwnership         = gatespans.HeavyOwnership
	heavyCommandNotStarted = gatespans.HeavyCommandNotStarted
	heavyCleanupMarker     = gatespans.HeavyCleanupMarker
	heavyProgress          = gatespans.HeavyProgress
	heavyProgressKey       = gatespans.HeavyProgressKey
	heavyAttribution       = gatespans.HeavyAttribution
	legacyGoCase           = gatespans.LegacyGoCase
	legacyFailure          = gatespans.LegacyFailure
)

var (
	errGateStallHeld = gatespans.ErrGateStallHeld
	errGateNoVerdict = gatespans.ErrGateNoVerdict

	ReadHeavyStatus                   = gatespans.ReadHeavyStatus
	ReadHeavyStatusForBranch          = gatespans.ReadHeavyStatusForBranch
	acquireLoopLock                   = gatespans.AcquireLoopLock
	cleanupLost                       = gatespans.CleanupLost
	heavyProgressReport               = gatespans.HeavyProgressReport
	heavyCheckEnded                   = gatespans.HeavyCheckEnded
	reuseHeavyCheck                   = gatespans.ReuseHeavyCheck
	saveHeavyCheck                    = gatespans.SaveHeavyCheck
	startHeavyReplayProgressWatcher   = gatespans.StartHeavyReplayProgressWatcher
	startHeavyCheckLogProgressWatcher = gatespans.StartHeavyCheckLogProgressWatcher
	startHeavyLogFileProgressWatcher  = gatespans.StartHeavyLogFileProgressWatcher
	readLegacyEvidence                = gatespans.ReadLegacyEvidence
	nextHeavyLegacyNonce              = gatespans.NextHeavyLegacyNonce
	runOwnedHeavy                     = gatespans.RunOwnedHeavy
	runCheckGroupStarted              = gatespans.RunCheckGroupStarted
	runHeavyDeclarationGroupStarted   = gatespans.RunHeavyDeclarationGroupStarted
	startAndWait                      = gatespans.StartAndWait
	endProcessGroup                   = gatespans.EndProcessGroup
	revertedBy                        = gatespans.RevertedBy
	heavyReadsChanged                 = gatespans.HeavyReadsChanged
	rebasedLanding                    = gatespans.RebasedLanding
	rebaseNote                        = gatespans.RebaseNote
	joinDetail                        = gatespans.JoinDetail
	heavyBrief                        = gatespans.HeavyBrief
	heavyStoreWholeStepProgress       = gatespans.HeavyStoreWholeStepProgress
	heavyStoreStepProgress            = gatespans.HeavyStoreStepProgress
	hasHeavyStoreProgress             = gatespans.HasHeavyStoreProgress
	heavyDirectory                    = gatespans.HeavyDirectory
	loadHeavyProgress                 = gatespans.LoadHeavyProgress
	readHeavyReplay                   = gatespans.ReadHeavyReplay
	publishHeavy                      = gatespans.PublishHeavy
	prepareHeavyTree                  = gatespans.PrepareHeavyTree
	finishHeavyTree                   = gatespans.FinishHeavyTree
	heavySupervisorIdentity           = gatespans.HeavySupervisorIdentity
	heavySupervisorRunning            = gatespans.HeavySupervisorRunning
	reapWhileSupervising              = gatespans.ReapWhileSupervising
	runSupervisedTree                 = gatespans.RunSupervisedTree
	publishHeavyJSON                  = gatespans.PublishHeavyJSON
	readHeavyAttributions             = gatespans.ReadHeavyAttributions
	saveHeavyReplay                   = gatespans.SaveHeavyReplay
	completedHeavyRed                 = gatespans.CompletedHeavyRed
	heavyFailureFingerprint           = gatespans.HeavyFailureFingerprint
	heavyLandings                     = gatespans.HeavyLandings
	heavyGitOutput                    = gatespans.HeavyGitOutput
	heavyGreen                        = gatespans.HeavyGreen
	recoverHeavyCleanupGuards         = gatespans.RecoverHeavyCleanupGuards
)

func gateSpanID(verdictID, checkID string) string { return gatespans.GateSpanID(verdictID, checkID) }
func gateTraceID(passID, runID string) string     { return gatespans.GateTraceID(passID, runID) }
func gateParentSpanID(verdictID string) string    { return gatespans.GateParentSpanID(verdictID) }
func hexDigest(text string) string                { return gatespans.HexDigest(text) }
func readFailingSpans(path string) ([]string, map[string][]string) {
	return gatespans.ReadFailingSpans(path)
}
func readSpansWithStatus(path, status string) ([]string, map[string][]string) {
	return gatespans.ReadSpansWithStatus(path, status)
}
func readSpansWithWatches(path, status string, declared map[string][]string) ([]string, map[string][]string, error) {
	return gatespans.ReadSpansWithWatches(path, status, declared)
}
func readMatchingSpans(path string, wantOutcome checkresult.Outcome, wantDetail string, declared map[string][]string) ([]string, map[string][]string, error) {
	return gatespans.ReadMatchingSpans(path, wantOutcome, wantDetail, declared)
}
func readRollingCoverage(path string) RollingCoverage { return gatespans.ReadRollingCoverage(path) }
func readGateSpans(root, runID, verdictID, passID string) ([]gateSpanRow, string) {
	return gatespans.ReadGateSpans(root, runID, verdictID, passID)
}
func readRemainingGateSpans(path string) (gateSpansFile, string) {
	return gatespans.ReadRemainingGateSpans(path)
}
func readFastGateSpans(path string) ([]gateSpan, string) { return gatespans.ReadFastGateSpans(path) }

func (s *ProcessSteps) recordGateTiming(ctx context.Context, c Candidate, span StepSpan) {
	gatespans.RecordGateTiming(ctx, s.Root, s.Store, c.ItemID, c.RunID, span)
}
func appendFastGateTiming(ctx context.Context, path string, p fastGateTimingPayload, at time.Time) error {
	return gatespans.AppendFastGateTiming(ctx, path, p, at)
}
func medianFastGateTiming(tx *store.Tx, wallMS int64) (*int64, error) {
	return gatespans.MedianFastGateTiming(tx, wallMS)
}
func readGateTimingChecks(dir string, spans ...StepSpan) map[string]gateCheckMeasurement {
	return gatespans.ReadGateTimingChecks(dir, spans...)
}

func gateUsageError(stderr string) bool                { return gatespans.GateUsageError(stderr) }
func gateErrorLines(stderr string, max int) string     { return gatespans.GateErrorLines(stderr, max) }
func gateBinaryDisagreement(paths []string) string     { return gatespans.GateBinaryDisagreement(paths) }
func gateErrorDetail(stderr string, p []string) string { return gatespans.GateErrorDetail(stderr, p) }
func candidateChangedPaths(ctx context.Context, c Candidate) []string {
	return gatespans.CandidateChangedPaths(ctx, c.Worktree, c.BaseSHA, c.HeadSHA)
}

func (s *ProcessSteps) heavyJudgeConfig() gatespans.HeavyJudgeConfig {
	return gatespans.HeavyJudgeConfig{
		LaneRoot: s.LaneRoot, SelfExe: s.selfExe, Priority: s.priority,
		CheckProcessEnv: s.checkProcessEnv, CommandArgv: s.commandArgv,
		GateProgram: s.HeavyGateBin, HeavyLegacyProgram: s.RollingProgram,
		ReplayProgram: s.ReplayProgram, HeavyReplaySelectors: &s.heavyReplaySelectors,
		LogSampleInterval: s.heavyLogSampleInterval,
	}
}
func heavyCheckDeclarations(dir string) (map[string]heavyDeclaration, error) {
	return gatespans.HeavyCheckDeclarations(dir)
}
func (s *ProcessSteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return gatespans.JudgeHeavy(ctx, s.heavyJudgeConfig(), dir, only)
}
func heavyChangedPaths(ctx context.Context, dir string) ([]string, error) {
	return gatespans.HeavyChangedPaths(ctx, dir)
}
func (s *ProcessSteps) runHeavyLegacy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return gatespans.RunHeavyLegacy(ctx, s.heavyJudgeConfig(), dir, only)
}
func legacyLogPath(ctx context.Context, laneRoot, dir, only string) string {
	return gatespans.LegacyLogPath(ctx, laneRoot, dir, only)
}
func legacyKeptLog(path, verdict string) string { return gatespans.LegacyKeptLog(path, verdict) }
func runHeavyDeclarationGroup(cmd *exec.Cmd) error {
	return gatespans.RunHeavyDeclarationGroup(cmd)
}
func decodeHeavyVerdict(raw []byte) ([]HeavyCheck, error) { return gatespans.DecodeHeavyVerdict(raw) }

func goSuiteProofID(id string) string { return gatespans.GoSuiteProofID(id) }
func goSuiteFailures(label string, evidence legacyFailure, suite HeavyCheck) []HeavyCheck {
	return gatespans.GoSuiteFailures(label, evidence, suite)
}
func (s *ProcessSteps) judgeHeavyGo(ctx context.Context, dir, id string) ([]HeavyCheck, error) {
	return gatespans.JudgeHeavyGo(ctx, s.heavyJudgeConfig(), dir, id)
}
func privateGoReplayGitConfig(ctx context.Context, dir, dest string) error {
	return gatespans.PrivateGoReplayGitConfig(ctx, dir, dest)
}

func legacyCheckDeclarations(ctx context.Context, dir string) ([]legacyDeclaration, error) {
	return gatespans.LegacyCheckDeclarations(ctx, dir)
}
func legacyCheckWatches(ctx context.Context, dir string) (map[string][]string, error) {
	return gatespans.LegacyCheckWatches(ctx, dir)
}
func legacyWatchFiles(root string, files []string, watch legacyWatch) ([]string, error) {
	return gatespans.LegacyWatchFiles(root, files, watch)
}

func (s *ProcessSteps) replayConfig(c Candidate) gatespans.ReplayConfig {
	return gatespans.ReplayConfig{
		Root: s.Root, LaneRoot: s.LaneRoot, SelfExe: s.selfExe, Priority: s.priority,
		CheckProcessEnv: s.checkProcessEnv, RunID: c.RunID, ItemID: c.ItemID,
		BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA,
		CandidateChangedPaths: c.CandidateChangedPaths, ReplayCheckLogs: c.replayCheckLogs,
	}
}
func (s *ProcessSteps) replayChildEnv(c Candidate, extra ...string) []string {
	return gatespans.ReplayChildEnv(s.replayConfig(c), extra...)
}
func appendGitConfigEnv(env []string, pairs ...[2]string) []string {
	return gatespans.AppendGitConfigEnv(env, pairs...)
}
func isReplayGoCheck(check string) bool { return gatespans.IsReplayGoCheck(check) }
func readReplayGoFailingTests(root string, c Candidate, check string) (map[string][]string, bool) {
	return gatespans.ReadReplayGoFailingTests(root, c.RunID, c.replayCheckLogs, check)
}
func parseTriageGoTests(content, check string) (map[string][]string, bool, bool) {
	return gatespans.ParseTriageGoTests(content, check)
}
func parseGoAffectedSummary(content string) (map[string][]string, bool, bool) {
	return gatespans.ParseGoAffectedSummary(content)
}
func indexReplayGoModules(dir string) []goModuleInfo { return gatespans.IndexReplayGoModules(dir) }
func resolveReplayGoPackage(modules []goModuleInfo, pkg string) (string, string, bool) {
	return gatespans.ResolveReplayGoPackage(modules, pkg)
}
func (s *ProcessSteps) replayGoCheck(ctx context.Context, dir string, c Candidate, rev, check string) (bool, bool, bool, string, error) {
	return gatespans.ReplayGoCheck(ctx, s.replayConfig(c), dir, rev, check)
}
func (s *ProcessSteps) diffChangedPaths(ctx context.Context, baseSHA, headSHA string) []string {
	return gatespans.DiffChangedPaths(ctx, s.Root, baseSHA, headSHA)
}
func leafGoTests(names []string) []string { return gatespans.LeafGoTests(names) }
func (s *ProcessSteps) runNamedGoTestsInReplay(ctx context.Context, dir string, c Candidate, rev, check string, pkgs map[string][]string) (bool, string, error) {
	return gatespans.RunNamedGoTestsInReplay(ctx, s.replayConfig(c), dir, rev, check, pkgs)
}

func (s *ProcessSteps) gateStallContext(c Candidate) ([]StallObservation, error) {
	return gatespans.GateStallContext(s.Root, c.RunID, c.HeadSHA, c.Worktree)
}
