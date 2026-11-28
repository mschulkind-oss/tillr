package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Constants re-exported from internal/storelog.
const (
	SchemaRollCheckLine        = storelog.SchemaRollCheckLine
	StoreBusyKind              = storelog.StoreBusyKind
	CheckpointNotRestartedKind = storelog.CheckpointNotRestartedKind
	CheckpointInterval         = storelog.CheckpointInterval
	CheckpointAllowance        = storelog.CheckpointAllowance
	LogReportFrames            = storelog.LogReportFrames
	loopEndedKind              = storelog.LoopEndedKind
	LoopPassKind               = storelog.LoopPassKind
	LoopPassRemainderPhase     = storelog.LoopPassRemainderPhase
	loopPassInterval           = storelog.LoopPassInterval
	passSpendDDL               = storelog.PassSpendDDL
	DispatchDDL                = storelog.DispatchDDL
	graphRevisionDDL           = storelog.GraphRevisionDDL
	SessionDDL                 = storelog.SessionDDL
	RetryDDL                   = storelog.RetryDDL
	GateSpanDDL                = storelog.GateSpanDDL
	rollingDDL                 = storelog.RollingDDL
	backendWindowDDL           = storelog.BackendWindowDDL
	backendOutcomesDDL         = storelog.BackendOutcomesDDL
	runtimeConfigSchema        = storelog.RuntimeConfigSchema

	BoundaryCheckKind      = storelog.BoundaryCheckKind
	GateStartedKind        = storelog.GateStartedKind
	GateStoppedKind        = storelog.GateStoppedKind
	GatePlanKind           = storelog.GatePlanKind
	GateVerdictKind        = storelog.GateVerdictKind
	RefusalKind            = storelog.RefusalKind
	FixerAttachedKind      = storelog.FixerAttachedKind
	TrunkFailureLinkedKind = storelog.TrunkFailureLinkedKind
	HeavyFaultKind         = storelog.HeavyFaultKind
	FastGateTimingKind     = storelog.FastGateTimingKind

	BoundaryScopeRun        = storelog.BoundaryScopeRun
	BoundaryScopeResume     = storelog.BoundaryScopeResume
	GatePlanRecord          = storelog.GatePlanRecord
	GateVerdictRecord       = storelog.GateVerdictRecord
	GateRecordRead          = storelog.GateRecordRead
	GateRecordAbsent        = storelog.GateRecordAbsent
	maxBoundaryObservations = storelog.MaxBoundaryObservations

	BaseReplayStartedKind = storelog.BaseReplayStartedKind
	BaseReplayEndedKind   = storelog.BaseReplayEndedKind
	handoffTimingFile     = storelog.HandoffTimingFile
)

// Type aliases re-exported from internal/storelog.
type (
	schemaRoll                 = storelog.SchemaRoll
	StoreBusyFact              = storelog.StoreBusyFact
	CheckpointNotRestartedFact = storelog.CheckpointNotRestartedFact
	loopEnded                  = storelog.LoopEnded
	LoopPassFact               = storelog.LoopPassFact
	loopPassTimer              = storelog.LoopPassTimer
	pipelineObservation        = storelog.PipelineObservation
	runRef                     = storelog.RunRef
	boundaryViolation          = storelog.BoundaryViolation
	boundaryCheckPayload       = storelog.BoundaryCheckPayload
	RefusalCause               = storelog.RefusalCause
	gateJudgmentRecordError    = storelog.GateJudgmentRecordError
	gateStartedPayload         = storelog.GateStartedPayload
	gateStoppedPayload         = storelog.GateStoppedPayload
	decisionCollisionError     = storelog.DecisionCollisionError
	BaseReplayStartedPayload   = storelog.BaseReplayStartedPayload
	BaseReplayEndedPayload     = storelog.BaseReplayEndedPayload
	handoffSpan                = storelog.HandoffSpan
	handoffTiming              = storelog.HandoffTiming
	examinationTimer           = storelog.ExaminationTimer
)

// Forwarding wrappers for schema_roll.go.
func schemaRollDir(executable, digest string) string {
	return storelog.SchemaRollDir(executable, digest)
}
func readSchemaRoll(dir string) (schemaRoll, error) { return storelog.ReadSchemaRoll(dir) }

func (l *Loop) StagedSchemaPrograms() string {
	if l.cfg.Restart == nil || l.build.Digest == "" {
		return ""
	}
	return storelog.SchemaRollDir(l.build.Executable, l.build.Digest)
}

func (l *Loop) activateSchemaRoll(ctx context.Context) error {
	return storelog.ActivateSchemaRoll(ctx, storelog.SchemaRollActivation{
		Dir: l.StagedSchemaPrograms(), StorePath: l.cfg.Store, Executable: l.build.Executable,
		BuildString: l.build.String(), Now: l.cfg.Now, PassInterval: PassInterval,
		RefreshSession: l.refreshSession, Restarter: l.restarter, Reportf: l.reportf,
	})
}

func (l *Loop) alarmStoreRoll(ctx context.Context, commit, step string, cause error) {
	storelog.AlarmStoreRoll(ctx, l.cfg.Store, commit, step, cause, l.cfg.Now(), l.reportf)
}

// Forwarding wrappers for store_busy.go.
func (l *Loop) takeBusyWrites() []StoreBusyFact {
	l.busyWritesMu.Lock()
	pending := l.pendingBusyWrites
	l.pendingBusyWrites = nil
	l.busyWritesMu.Unlock()
	return pending
}

func (l *Loop) restoreBusyWrites(pending []StoreBusyFact) {
	l.busyWritesMu.Lock()
	l.pendingBusyWrites = append(pending, l.pendingBusyWrites...)
	l.busyWritesMu.Unlock()
}

func (l *Loop) recordBusyWrite(operation string, waited time.Duration, at time.Time) {
	fact := StoreBusyFact{Operation: operation, WaitedMS: waited.Milliseconds(), SessionID: l.currentSessionID(), At: at.UTC().Format(time.RFC3339Nano)}
	l.busyWritesMu.Lock()
	l.pendingBusyWrites = append(l.pendingBusyWrites, fact)
	l.busyWritesMu.Unlock()
}

func (l *Loop) writeTx(ctx context.Context, s *store.Store, fn func(*store.Tx) error) error {
	return storelog.WriteTxWithBusyFacts(ctx, s, l.takeBusyWrites, l.restoreBusyWrites, l.currentSessionID, l.cfg.Now, fn)
}

func (l *Loop) appendBusyWrites(tx *store.Tx, pending []StoreBusyFact) error {
	return storelog.AppendBusyFacts(tx, l.currentSessionID(), pending, l.cfg.Now())
}

// Forwarding wrappers for wal_keeper.go.
func openWALKeeper(ctx context.Context, path string) (*store.CheckpointOwner, error) {
	return storelog.OpenWALKeeper(ctx, path)
}

func (l *Loop) keepWALWhileRunning() {
	l.sessionMu.Lock()
	defer l.sessionMu.Unlock()
	l.keepWAL = true
}

func checkpointRetryDelay(notRestarted int) time.Duration {
	return storelog.CheckpointRetryDelay(notRestarted, PassInterval)
}

func (l *Loop) checkpointDueLocked(now time.Time) bool {
	return storelog.CheckpointDue(l.walKeeper != nil, l.restartPending, l.lastCheckpoint, l.retryWait, now)
}

func (l *Loop) recordCheckpointLocked(now time.Time, c store.Checkpointed) int {
	var consecutive int
	l.lastCheckpoint, l.restartPending, l.notRestarted, l.retryWait, consecutive = storelog.RecordCheckpoint(now, c, l.notRestarted, PassInterval)
	return consecutive
}

func (l *Loop) recordCheckpointState(now time.Time, c store.Checkpointed) int {
	l.sessionMu.Lock()
	defer l.sessionMu.Unlock()
	return l.recordCheckpointLocked(now, c)
}

func (l *Loop) checkpointTarget() (*store.CheckpointOwner, time.Time, bool) {
	l.sessionMu.Lock()
	owner, now, due := l.walKeeper, l.cfg.Now(), l.checkpointDueLocked(l.cfg.Now())
	l.sessionMu.Unlock()
	if owner != nil && !l.reportedLockError && owner.LockError() != nil {
		l.reportedLockError = true
		l.reportf("loop: other programs cannot see this loop's checkpoint ownership, so they keep checkpointing themselves: %v\n", owner.LockError())
	}
	return owner, now, due
}

func (l *Loop) checkpointIfDue(ctx context.Context) error {
	owner, now, due := l.checkpointTarget()
	return storelog.CheckpointIfDue(ctx, storelog.CheckpointParams{
		Owner: owner, Due: due, Now: now, WaitClaimGate: claimGateFor(l.cfg.Store).wait,
		RefreshSession: l.refreshSession, BeforeCheckpoint: l.beforeCheckpoint, AfterCheckpoint: l.checkpointed,
		RecordState: l.recordCheckpointState, RecordMissed: l.recordCheckpointNotRestarted, Report: l.reportCheckpoint,
	})
}

func (l *Loop) recordCheckpointNotRestarted(ctx context.Context, c store.Checkpointed, consecutive int, at time.Time) {
	l.mu.Lock()
	id, initialized := l.sessionID, l.sessionInitialized
	l.mu.Unlock()
	storelog.RecordCheckpointNotRestarted(ctx, l.cfg.Store, initialized, l.sessionOptions, id, c, consecutive, at, l.reportf)
}

func (l *Loop) reportCheckpoint(c store.Checkpointed) {
	l.missedRestarts, l.reportedLogFrames = storelog.ReportCheckpoint(c, l.missedRestarts, l.reportedLogFrames, l.reportf)
}

func (l *Loop) closeWALKeeper() {
	l.sessionMu.Lock()
	owner := l.walKeeper
	l.walKeeper, l.keepWAL = nil, false
	l.restartPending, l.notRestarted, l.retryWait = false, 0, 0
	l.sessionMu.Unlock()
	storelog.CloseWALKeeper(owner, l.reportf)
}

// Forwarding wrappers for loop_ended.go.
func (l *Loop) recordLoopEnded(result LoopResult) {
	if result.Stop != StopFault {
		return
	}
	storelog.RecordLoopEnded(l.cfg.Store, l.sessionInitialized, l.sessionID, result.Stop, result.Detail, result.ExitCode, result.StopItemID, result.StopStep, l.cfg.Now(), l.reportf)
}

// Forwarding wrappers for loop_pass.go.
func newLoopPassTimer(now func() time.Time) *loopPassTimer { return storelog.NewLoopPassTimer(now) }
func (l *Loop) startPassPhase(name string) func()          { return l.passTimer.StartPhase(name) }
func withPassStepTimer(ctx context.Context, pt *loopPassTimer) context.Context {
	return storelog.WithPassStepTimer(ctx, pt)
}
func startPassStep(ctx context.Context, name string) func() {
	return storelog.StartPassStep(ctx, name)
}

func (l *Loop) timePassPhase(name string, fn func() error) error {
	end := l.startPassPhase(name)
	err := fn()
	end()
	return err
}

func (l *Loop) recordLoopPass(ctx context.Context, pt *loopPassTimer) {
	l.mu.Lock()
	id, initialized := l.sessionID, l.sessionInitialized
	l.mu.Unlock()
	p := storelog.LoopPassParams{StorePath: l.cfg.Store, SessionID: id, Initialized: initialized, SessionOptions: l.sessionOptions, LastLoopPassAt: l.lastLoopPassAt, Now: l.cfg.Now, Timer: pt, BeforeWrite: l.beforeRecordLoopPass, Reportf: l.reportf}
	if at, ok := storelog.RecordLoopPass(ctx, p); ok {
		l.lastLoopPassAt = at
	}
}

// Forwarding wrappers for pass_spend.go.
func (l *Loop) recordSettlementSpend(ctx context.Context, passID, origin string, rec run.Record) error {
	return storelog.RecordSettlementSpend(ctx, l.cfg.Store, l.sessionID, passID, origin, rec)
}

// Forwarding wrappers for pipeline_observation.go.
func pipelineStart(root, runID, step string) (pipelineObservation, error) {
	return storelog.PipelineStart(root, runID, step)
}
func pipelineFinish(root, runID string, observation pipelineObservation) error {
	return storelog.PipelineFinish(root, runID, observation)
}
func pipelineCloseGraph(root, runID string) error { return storelog.PipelineCloseGraph(root, runID) }

// Forwarding wrappers for events.go, dispatches.go, and handoff_timing.go.
func (l *Loop) recordItemRefusal(ctx context.Context, itemID, reason, detail string) error {
	return storelog.RecordItemRefusal(ctx, l.cfg.Store, itemID, reason, detail, l.cfg.Now().UTC())
}
func (l *Loop) recordHeavyFault(ctx context.Context, tip string, fault error) error {
	return storelog.RecordHeavyFault(ctx, l.cfg.Store, tip, fault, l.cfg.Now())
}
func payloadString(payload json.RawMessage, key string) string {
	return storelog.PayloadString(payload, key)
}
func withPayloadField(payload json.RawMessage, key, value string) (json.RawMessage, error) {
	return storelog.WithPayloadField(payload, key, value)
}
func recordPayload(body []byte, kind string) (map[string]json.RawMessage, error) {
	return storelog.RecordPayload(body, kind)
}
func appendJudgment(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time) error {
	return storelog.AppendJudgment(ctx, storePath, kind, objectType, objectID, payload, at)
}
func appendJudgmentThen(ctx context.Context, storePath, kind, objectType, objectID string, payload any, at time.Time, then func(*store.Tx) error) error {
	return storelog.AppendJudgmentThen(ctx, storePath, kind, objectType, objectID, payload, at, then)
}
func judgmentSeen(tx *store.Tx, kind, objectType, id string) (int, error) {
	return storelog.JudgmentSeen(tx, kind, objectType, id)
}
func gateJudgmentEpoch(r *store.Reader, runID string) (int64, error) {
	return storelog.GateJudgmentEpoch(r, runID)
}
func readFailure(err error) error  { return storelog.ReadFailure(err) }
func nullablePassID(id string) any { return storelog.NullablePassID(id) }
func newHandoffSpan(phase string, start, end time.Time) handoffSpan {
	return storelog.NewHandoffSpan(phase, start, end)
}
func withPreHandoffSpans(ctx context.Context, start time.Time, phases []handoffSpan) context.Context {
	return storelog.WithPreHandoffSpans(ctx, start, phases)
}
func timeHandoffPhase(ctx context.Context, phase string) func() {
	return storelog.TimeHandoffPhase(ctx, phase)
}
