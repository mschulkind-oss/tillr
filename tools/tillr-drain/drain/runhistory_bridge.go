package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Type aliases to tools/tillr-drain/internal/runhistory.
type (
	gathered          = runhistory.Gathered
	gatherMiss        = runhistory.GatherMiss
	FailingCase       = runhistory.FailingCase
	FailingCheck      = runhistory.FailingCheck
	goTestFailure     = runhistory.GoTestFailure
	goTestLogResult   = runhistory.GoTestLogResult
	runSuperseded     = runhistory.RunSuperseded
	orphanedRunEffect = runhistory.OrphanedRunEffect
	Retry             = runhistory.Retry
	RetryRequest      = runhistory.RetryRequest
	retryRow          = runhistory.RetryRow
	retryAckPayload   = runhistory.RetryAckPayload
	RunInputs         = runhistory.RunInputs
	blockFields       = runhistory.BlockFields
	storedBlockFields = runhistory.StoredBlockFields
	RepeatedRefusal   = runhistory.RepeatedRefusal
	EndingCheck       = runhistory.EndingCheck
	chainEnding       = runhistory.ChainEnding
	RepeatedCall      = runhistory.RepeatedCall
	ItemFault         = runhistory.ItemFault
)

// Constants re-exported from tools/tillr-drain/internal/runhistory.
const (
	failureHeading      = runhistory.FailureHeading
	noCaseRecorded      = runhistory.NoCaseRecorded
	failureSummaryMax   = runhistory.FailureSummaryMax
	triageScanMax       = runhistory.TriageScanMax
	fixerFailureHeading = runhistory.FixerFailureHeading

	runSupersededKind = runhistory.RunSupersededKind

	examinationClaimedKind  = runhistory.ExaminationClaimedKind
	examinationReleasedKind = runhistory.ExaminationReleasedKind
	dispatchStartedKind     = runhistory.DispatchStartedKind
	candidateReadyKind      = runhistory.CandidateReadyKind
	candidateRetirementKind = runhistory.CandidateRetirementKind
	candidatePreparedKind   = runhistory.CandidatePreparedKind
	examinationDoneKind     = runhistory.ExaminationDoneKind
	designQuotaRefusedKind  = runhistory.DesignQuotaRefusedKind
	examinationOwnerKind    = runhistory.ExaminationOwnerKind
	loopOrphanedKind        = runhistory.LoopOrphanedKind
	laneReleasedKind        = runhistory.LaneReleasedKind
	ActionSettle            = runhistory.ActionSettle
	ActionJudge             = runhistory.ActionJudge

	RetryKind          = runhistory.RetryKind
	RetryAckKind       = runhistory.RetryAckKind
	DesignRetryKind    = runhistory.DesignRetryKind
	StageRetryKind     = runhistory.StageRetryKind
	RetryAckGateFailed = runhistory.RetryAckGateFailed
	RetryAckUnjudged   = runhistory.RetryAckUnjudged

	inputRecordedKind = runhistory.InputRecordedKind

	RepeatedRefusalThreshold = runhistory.RepeatedRefusalThreshold
	RefusalNoOp              = runhistory.RefusalNoOp
	RefusalFence             = runhistory.RefusalFence
	RefusalGate              = runhistory.RefusalGate
	RefusalVerifyNotDone     = runhistory.RefusalVerifyNotDone
	RefusalVerifyNoVerdict   = runhistory.RefusalVerifyNoVerdict
	RefusalPlanWrong         = runhistory.RefusalPlanWrong
	RefusalLand              = runhistory.RefusalLand
	RefusalConflict          = runhistory.RefusalConflict
	RefusalNodeOutput        = runhistory.RefusalNodeOutput
	RefusalPlanOutput        = runhistory.RefusalPlanOutput
	RefusalStaging           = runhistory.RefusalStaging
	RefusalWordsMissing      = runhistory.RefusalWordsMissing
	RefusalWordsAltered      = runhistory.RefusalWordsAltered
	RefusalIdeaNoOutcome     = runhistory.RefusalIdeaNoOutcome
	RefusalReverted          = runhistory.RefusalReverted
	ReasonLandingReverted    = runhistory.ReasonLandingReverted
	ReasonEvalFailed         = runhistory.ReasonEvalFailed

	IdenticalRepeatPrefix      = runhistory.IdenticalRepeatPrefix
	conflictAfterRestartPrefix = runhistory.ConflictAfterRestartPrefix

	repeatingCallKind        = runhistory.RepeatingCallKind
	ReasonRepeatingCall      = runhistory.ReasonRepeatingCall
	ReasonRepeatingCallFresh = runhistory.ReasonRepeatingCallFresh
	repeatRouteFresh         = runhistory.RepeatActionFreshAttempt
	repeatRouteHold          = runhistory.RepeatActionHold

	itemFaultKind              = runhistory.ItemFaultKind
	itemFaultClearedKind       = runhistory.ItemFaultClearedKind
	ReasonCandidateMismatch    = runhistory.ReasonCandidateMismatch
	ReasonDefinitionSyncFailed = runhistory.ReasonDefinitionSyncFailed
	ReasonLandHarnessError     = runhistory.ReasonLandHarnessError
	ReasonPackSyncFailed       = runhistory.ReasonPackSyncFailed
	ReasonDecisionCollision    = runhistory.ReasonDecisionCollision
	ReasonSettleFailed         = runhistory.ReasonSettleFailed
	ReasonAdapterRefused       = runhistory.ReasonAdapterRefused
	ReasonStageFailed          = runhistory.ReasonStageFailed
)

// Variables re-exported from tools/tillr-drain/internal/runhistory.
var (
	writeGatheredHooks  = &runhistory.WriteGatheredHooks
	pendingEventsFolded = &runhistory.PendingEventsFolded
	terminalRunStates   = runhistory.TerminalRunStates
	concludingRefusals  = runhistory.ConcludingRefusals
	returnedCauses      = runhistory.ReturnedCauses
	returnedReasons     = runhistory.ReturnedReasons
)

func gatheringFor(ctx context.Context) (context.Context, *gathered) {
	return runhistory.GatheringFor(ctx)
}

func isGatherMiss(err error) bool { return runhistory.IsGatherMiss(err) }

func gather(ctx context.Context, key string, run func(context.Context) (string, error)) (string, error) {
	return runhistory.Gather(ctx, key, run)
}

func writeGathered(ctx context.Context, s *store.Store, fn func(context.Context, *store.Tx) error) error {
	return runhistory.WriteGatheredWithGate(ctx, s, claimGateFor(s.Path()).wait, fn)
}

func writeGatheredFrom(ctx context.Context, s *store.Store, g *gathered, fn func(context.Context, *store.Tx) error) error {
	return runhistory.WriteGatheredFromWithGate(ctx, s, g, claimGateFor(s.Path()).wait, fn)
}

func readTriageBlock(logRef string) []FailingCheck     { return runhistory.ReadTriageBlock(logRef) }
func readGoTestFailures(logRef string) goTestLogResult { return runhistory.ReadGoTestFailures(logRef) }
func failureSummary(checks []EndingCheck) string       { return runhistory.FailureSummary(checks) }
func fixerFailureSummary(c []EndingCheck, e map[string]string) string {
	return runhistory.FixerFailureSummary(c, e)
}
func isShardSlotNoticeLine(line string) bool { return runhistory.IsShardSlotNoticeLine(line) }
func parseShardFailureLine(trimmed string) (string, []string) {
	return runhistory.ParseShardFailureLine(trimmed)
}

func tableExists(r *store.Reader, name string) (bool, error) { return runhistory.TableExists(r, name) }
func isTerminalRunState(state string) bool                   { return runhistory.IsTerminalRunState(state) }

func endNonTerminalRunRow(ctx context.Context, tx *store.Tx, runID, reason string, markSuperseded bool, at time.Time) error {
	return runhistory.EndNonTerminalRunRow(ctx, tx, runID, reason, markSuperseded, at)
}

func (l *Loop) awaitBuildReader(ctx context.Context, id string) error {
	return runhistory.AwaitBuildReader(ctx, l.cfg.Store, id, l.cfg.Now, func(storePath, runID string) (bool, error) {
		here, err := currentLoopOwner()
		if err != nil {
			return false, err
		}
		live, err := liveRunWorkers(storePath, runID, here)
		return len(live) > 0, err
	})
}

func appendEffect(tx *store.Tx, kind, id string, value any, at time.Time) error {
	return runhistory.AppendEffect(tx, kind, id, value, at)
}

func appendEffectOn(tx *store.Tx, kind, objectType, id string, value any, at time.Time) error {
	return runhistory.AppendEffectOn(tx, kind, objectType, id, value, at)
}

func terminalRecord(r *store.Reader, id string) (*run.Record, error) {
	return runhistory.TerminalRecord(r, id)
}

func activeWorkerLease(r *store.Reader, req *run.Request, lane string, at time.Time) (bool, error) {
	return runhistory.ActiveWorkerLease(r, req, lane, at)
}

func pendingHistoryHead(r *store.Reader) (int64, error) {
	return runhistory.PendingHistoryHeadForKinds(r, pendingHistoryKinds)
}

func pendingHistoryChanged(r *store.Reader, after int64) (bool, error) {
	return runhistory.PendingHistoryChangedForKinds(r, after, pendingHistoryKinds)
}

func isReadOnlyReader(r *store.Reader) bool           { return runhistory.IsReadOnlyReader(r) }
func readerStorePath(r *store.Reader) (string, error) { return runhistory.ReaderStorePath(r) }
func samePendingAnchor(r *store.Reader, a event.Event) (bool, error) {
	return runhistory.SamePendingAnchor(r, a)
}

func activeOrdinaryAgentRuns(active map[string]string) []string {
	return runhistory.ActiveOrdinaryAgentRuns(active)
}
func activeOrdinaryAgentSummary(active map[string]string) string {
	return runhistory.ActiveOrdinaryAgentSummary(active)
}
func readExpressByID(r *store.Reader) (map[string]bool, error) { return runhistory.ReadExpressByID(r) }

func authorizeStageRetry(tx *store.Tx, req RetryRequest, f ItemFault, at time.Time) (Retry, error) {
	return runhistory.AuthorizeStageRetry(tx, req, f, at)
}
func RetryEnvelope(retry Retry) (record.Envelope, error)       { return runhistory.RetryEnvelope(retry) }
func readRetries(r *store.Reader) (map[string]retryRow, error) { return runhistory.ReadRetries(r) }
func readRetryAcks(r *store.Reader) (map[string]string, error) { return runhistory.ReadRetryAcks(r) }
func runJudged(r *store.Reader, runID string) (bool, error)    { return runhistory.RunJudged(r, runID) }
func runGateResult(r *store.Reader, runID string) (bool, string, error) {
	return runhistory.RunGateResult(r, runID)
}
func gateResultPassed(result string) bool { return runhistory.GateResultPassed(result) }
func runNeedsGateFailedAck(r *store.Reader, runID string) (bool, bool, error) {
	return runhistory.RunNeedsGateFailedAck(r, runID)
}
func retryID(itemID, runID string) string { return runhistory.RetryID(itemID, runID) }

func itemBlockHash(f blockFields, body string) (string, error) {
	return runhistory.ItemBlockHash(f, body)
}
func readStoredBlockFields(r *store.Reader, id string) (storedBlockFields, error) {
	return runhistory.ReadStoredBlockFields(r, id)
}

func refusalStepsWords(steps map[string]int) string { return runhistory.RefusalStepsWords(steps) }
func GateRefusalCauses(checks []EndingCheck) []RefusalCause {
	return runhistory.GateRefusalCauses(checks)
}
func (l *Loop) concludedByRefusal(ctx context.Context, runID string) (bool, error) {
	return runhistory.ConcludedByRefusal(ctx, l.cfg.Store, runID, candidateAssignedKind)
}

func normalizeEvidence(output, runID string) string {
	return runhistory.NormalizeEvidence(output, runID)
}
func evidenceDigest(output, runID string) string { return runhistory.EvidenceDigest(output, runID) }
func endingKey(cause string, checks []EndingCheck, detail, runID string) string {
	return runhistory.EndingKey(cause, checks, detail, runID)
}
func conflictPaths(reports []string) []string { return runhistory.ConflictPaths(reports) }
func conflictPath(report string) string       { return runhistory.ConflictPath(report) }
func conflictEndingKey(reports []string, markers, runID string) string {
	return runhistory.ConflictEndingKey(reports, markers, runID)
}
func repeatedEnding(chain []chainEnding, latest chainEnding) (string, bool) {
	return runhistory.RepeatedEnding(chain, latest)
}

func repeatingRoute(prior *RepeatedCall, backend string, recovered bool) (string, string, []string) {
	return runhistory.RepeatingRoute(prior, backend, recovered)
}
func readRepeatedCalls(r *store.Reader) (map[string]RepeatedCall, error) {
	return runhistory.ReadRepeatedCalls(r)
}
func repeatedCallBrief(rc RepeatedCall) string { return runhistory.RepeatedCallBrief(rc) }

func ReadItemFaults(r *store.Reader) (map[string]ItemFault, error) {
	return runhistory.ReadItemFaults(r)
}
func faultClearDetail(f ItemFault, storePath string) string {
	return runhistory.FaultClearDetail(f, storePath)
}
func decisionCollisionDetail(collision *decisionCollisionError, action string) string {
	return runhistory.DecisionCollisionDetail(collision, action)
}
func execSetupRefused(f ItemFault) bool { return runhistory.ExecSetupRefused(f) }
func clearItemFault(tx *store.Tx, f ItemFault, at time.Time) error {
	return runhistory.ClearItemFaultTx(tx, f, at)
}
func syncClearVersion(r *store.Reader) (int64, error) { return runhistory.SyncClearVersion(r) }
func (l *Loop) clearUnrunNode(ctx context.Context, runID string) error {
	return runhistory.ClearUnrunNode(ctx, l.cfg.Store, runID)
}
