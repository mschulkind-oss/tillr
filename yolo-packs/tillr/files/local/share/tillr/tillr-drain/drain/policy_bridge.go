package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Type aliases to tools/tillr-drain/internal/policy.
type (
	Backend                   = policy.Backend
	Policy                    = policy.Policy
	PolicyState               = policy.PolicyState
	StartOption               = policy.StartOption
	policyChange              = policy.PolicyChange
	seatEscalation            = policy.SeatEscalation
	dispatchDecline           = policy.DispatchDecline
	seatWait                  = policy.SeatWait
	QuotaWindow               = policy.QuotaWindow
	QuotaStatus               = policy.QuotaStatus
	AttemptClass              = policy.AttemptClass
	priorRun                  = policy.PriorRun
	backendOutcome            = policy.BackendOutcome
	backendOutcomesState      = policy.BackendOutcomesState
	BackendMove               = policy.BackendMove
	seatDispatchConflictError = policy.SeatDispatchConflictError
)

// Constants re-exported from tools/tillr-drain/internal/policy.
const (
	invalidPolicySeatName    = policy.InvalidPolicySeatName
	defaultSeatDeclaration   = policy.DefaultSeatDeclaration
	SeatEscalatedKind        = policy.SeatEscalatedKind
	backendQuotaReopenedKind = policy.BackendQuotaReopenedKind
	dispatchDeclinedKind     = policy.DispatchDeclinedKind
	BackendMovedKind         = policy.BackendMovedKind
	QuotaProbeInterval       = policy.QuotaProbeInterval
	backendQuotaDDL          = policy.BackendQuotaDDL
	quotaUpdatedKind         = policy.QuotaUpdatedKind
	AttemptInterrupted       = policy.AttemptInterrupted
	AttemptInfrastructure    = policy.AttemptInfrastructure
	AttemptRefused           = policy.AttemptRefused
	AttemptAnswered          = policy.AttemptAnswered
	AttemptUnjudged          = policy.AttemptUnjudged
	AttemptRepeating         = policy.AttemptRepeating
	adapterRefusedExit       = policy.AdapterRefusedExit
	adapterWallExit          = policy.AdapterWallExit
	ReportFooter             = policy.ReportFooter
	backendOutcomeKind       = policy.BackendOutcomeKind
	backendProbeKind         = policy.BackendProbeKind
)

// Variables re-exported from tools/tillr-drain/internal/policy.
var (
	errSeatWindowClosed       = policy.ErrSeatWindowClosed
	backendOutcomesReads      = &policy.BackendOutcomesReads
	backendOutcomesUnderWrite = &policy.BackendOutcomesUnderWrite
	backendOutcomeUnmarshals  = &policy.BackendOutcomeUnmarshals
)

// Forwarding wrappers for policy.go and policy_reload.go.
func resolveAdapterPath(root, adapter string) string { return policy.ResolveAdapterPath(root, adapter) }
func backendNames(backends map[string]Backend) []string {
	return policy.BackendNames(backends)
}
func LoadPolicy(ref string) (*Policy, error) { return policy.LoadPolicy(ref) }
func decodePolicy(ref string, raw []byte) (*Policy, error) {
	return policy.DecodePolicy(ref, raw)
}
func PolicyStateFile(root string) string               { return policy.PolicyStateFile(root) }
func ReadPolicyState(root string) (PolicyState, error) { return policy.ReadPolicyState(root) }
func contentHash(raw []byte) string                    { return policy.ContentHash(raw) }
func readPolicyContent(ref string) (*Policy, []byte, string, error) {
	return policy.ReadPolicyContent(ref)
}
func EffectivePolicy(root, ref string) (*Policy, error) { return policy.EffectivePolicy(root, ref) }

func (l *Loop) currentPolicy() *Policy {
	l.policyMu.RLock()
	defer l.policyMu.RUnlock()
	return l.cfg.Policy
}

func (l *Loop) allPolicies() ([]*Policy, int) {
	l.policyMu.RLock()
	defer l.policyMu.RUnlock()
	return policy.AllPolicies(l.cfg.Policy, l.profilePolicies, l.policyState.ProfileIndex)
}

func CheckQuotaGroups(policies ...*Policy) error {
	return policy.CheckQuotaGroups(policies...)
}

func (l *Loop) refreshPolicy() error {
	return policy.RefreshPolicy(l.cfg.Root, l.cfg.PolicyRef, l.cfg.Now, &l.policyMu, &l.cfg.Policy, &l.policyState, &l.policyRecorded, l.reportf)
}

// Forwarding wrappers for seat_escalation.go.
func readEscalations(r *store.Reader) (map[string][]seatEscalation, error) {
	return policy.ReadEscalations(r)
}
func readEscalationsForItems(r *store.Reader, itemIDs []string) (map[string][]seatEscalation, error) {
	return policy.ReadEscalationsForItems(r, itemIDs)
}
func escalatedBackend(escalations []seatEscalation) string {
	return policy.EscalatedBackend(escalations)
}
func (l *Loop) escalateSeat(ctx context.Context, itemID, runID, seat, from string) (string, error) {
	return policy.EscalateSeat(ctx, l.cfg.Store, l.cfg.Policy, l.cfg.Now, itemID, runID, seat, from)
}

// Forwarding wrappers for quota_window.go.
func refusedWindow(rec run.Record) (string, float64, time.Time, bool) {
	return policy.RefusedWindow(rec)
}
func (l *Loop) recordBackendWindow(ctx context.Context, rec run.Record) error {
	policies, _ := l.allPolicies()
	return policy.RecordBackendWindow(ctx, l.cfg.Store, l.cfg.Now, rec, policies, l.pauseQuotaPeers)
}
func reopenBackendWindow(tx *store.Tx, backend string, probeSeq int64, at time.Time) error {
	return policy.ReopenBackendWindow(tx, backend, probeSeq, at)
}
func readShutBackends(r *store.Reader, at time.Time) (map[string]bool, error) {
	return policy.ReadShutBackends(r, at)
}
func readBackendWindows(r *store.Reader, at time.Time) (map[string]time.Time, error) {
	return policy.ReadBackendWindows(r, at)
}

// Forwarding wrappers for alarm_report.go.
func alarmPolicy(p *Policy, since time.Time) *alarms.Policy { return policy.AlarmPolicy(p, since) }
func (l *Loop) reportAlarms(ctx context.Context, at time.Time) {
	policy.ReportAlarms(ctx, l.cfg.Store, at, l.currentPolicy(), l.policyState.At, &l.reportedAlarms, l.checkPiBuild, l.reportf)
}
func (l *Loop) reportAlarmChanges(active []alarms.Alarm) {
	policy.ReportAlarmChanges(active, &l.reportedAlarms, l.reportf)
}

// Forwarding wrappers for dispatch_decline.go.
func isDispatch(d Decision) bool { return policy.IsDispatchAction(d.Action) }
func (l *Loop) recordDispatchDecline(ctx context.Context, d Decision, at time.Time, reason, waitingOn string) error {
	runID := ""
	if d.RunRequest != nil {
		runID = d.RunRequest.RunID
	}
	return policy.RecordDispatchDecline(ctx, l.cfg.Store, d.ItemID, d.DecisionID, d.LaneID, runID, at, reason, waitingOn, l.cfg.Now, l.writeTx, l.recordBusyWrite, l.reportf)
}
func dispatchStartedAfter(r *store.Reader, runID string, seq int64) (bool, error) {
	return policy.DispatchStartedAfter(r, runID, seq)
}
func readDispatchDeclines(r *store.Reader) (map[string]dispatchDecline, error) {
	return policy.ReadDispatchDeclines(r)
}
func readDispatchDeclinesForItems(r *store.Reader, itemIDs []string) (map[string]dispatchDecline, error) {
	return policy.ReadDispatchDeclinesForItems(r, itemIDs)
}

// Forwarding wrappers for seat_shares.go.
func keepSeatWait(tx *store.Tx, claim func() error) error { return policy.KeepSeatWait(tx, claim) }
func seatClaim(p *Policy, claim func(context.Context, *store.Tx) error) func(context.Context, *store.Tx) error {
	return policy.SeatClaim(p, claim)
}
func waitingSeatBackend(r *store.Reader, runID, seat string, list []string) (string, error) {
	return policy.WaitingSeatBackend(r, runID, seat, list)
}

// Forwarding wrappers for quota_probe.go.
func decodeQuota(raw []byte, at time.Time) ([]QuotaWindow, error) { return policy.DecodeQuota(raw, at) }
func readQuotaStatuses(r *store.Reader) ([]QuotaStatus, error)    { return policy.ReadQuotaStatuses(r) }
func quotaFresh(q QuotaStatus, command string, at time.Time) bool {
	return policy.QuotaFresh(q, command, at)
}
func (l *Loop) refreshQuotas(ctx context.Context) error {
	policies, activeIdx := l.allPolicies()
	return policy.RefreshQuotas(ctx, l.cfg.Root, l.cfg.Store, &l.quotaMu, policies, activeIdx, l.cfg.Now, l.reportf)
}
func readQuotaOnlyShut(r *store.Reader, at time.Time, p *Policy) (map[string]bool, error) {
	return policy.ReadQuotaOnlyShut(r, at, p)
}

// Forwarding wrappers for quota_continue.go.
func quotaPauseBackend(r *store.Reader, id string) (string, error) {
	return policy.QuotaPauseBackend(r, id)
}
func (l *Loop) quotaInterrupted(ctx context.Context, id string) (bool, error) {
	return policy.QuotaInterrupted(ctx, l.cfg.Store, id)
}
func (l *Loop) holdQuotaRun(ctx context.Context, d Decision, backend string) error {
	return policy.HoldQuotaRun(ctx, l.cfg.Store, d.RunRequest.RunID, d.ItemID, d.LaneID, backend, l.cfg.Now())
}
func (l *Loop) clearQuotaPause(ctx context.Context, id string) error {
	return policy.ClearQuotaPause(ctx, l.cfg.Store, id)
}

// Forwarding wrappers for failover.go.
func ClassifyAttempt(rec run.Record, log string) AttemptClass {
	return policy.ClassifyAttempt(rec, log)
}
func RecordAdapterRefusal(rec run.Record, log string) (string, bool) {
	return policy.RecordAdapterRefusal(rec, log)
}
func AdapterRefusal(log string) (string, bool) { return policy.AdapterRefusal(log) }
func reportFooter(log string) string           { return policy.ReportFooterWord(log) }
func readableFooter(footer string) bool        { return policy.ReadableFooter(footer) }
func unreadFooterReason(footer string) string  { return policy.UnreadFooterReason(footer) }
func readRunJudgmentsAndEndings(r *store.Reader) (map[string]bool, map[string]int64, map[string][]byte, error) {
	return policy.ReadRunJudgmentsAndEndings(r)
}
func logTail(refs []run.ArtifactRef) string    { return policy.LogTail(refs) }
func rawLogTail(refs []run.ArtifactRef) string { return policy.RawLogTail(refs) }
func readFileTail(path string) string          { return policy.ReadFileTail(path) }

// Forwarding wrappers for backend_outcomes.go and backend_availability.go.
func backendAvailabilityChanged(r *store.Reader, after int64) (bool, error) {
	return policy.BackendAvailabilityChanged(r, after)
}
func queryBackendOutcomes(r *store.Reader, afterSeq int64) ([]backendOutcome, int, error) {
	return policy.QueryBackendOutcomes(r, afterSeq)
}
func queryOfferedSeqs(r *store.Reader, afterSeq int64) ([]int64, error) {
	return policy.QueryOfferedSeqs(r, afterSeq)
}
func isUnknownFaceFailure(rec run.Record, log string) bool {
	return policy.IsUnknownFaceFailure(rec, log)
}
func rollPendingAt(r *store.Reader, atSeq int64) (bool, error) {
	return policy.RollPendingAt(r, atSeq)
}
func readInterruptedRuns(r *store.Reader) (map[string]bool, map[string]bool, error) {
	return policy.ReadInterruptedRuns(r)
}
func queryBackendProbes(r *store.Reader, afterSeq int64) (map[string]int64, error) {
	return policy.QueryBackendProbes(r, afterSeq)
}
func resolveInfrastructureClosed(r *store.Reader, outcomes []backendOutcome, probes map[string]int64, prev map[string]backendOutcome) (map[string]backendOutcome, error) {
	return policy.ResolveInfrastructureClosed(r, outcomes, probes, prev)
}
func populateClosedBackendRecords(r *store.Reader, closed, prev map[string]backendOutcome) error {
	return policy.PopulateClosedBackendRecords(r, closed, prev)
}

// Forwarding wrappers for backend_move.go.
func recordBackendMoveTx(tx *store.Tx, move BackendMove, at time.Time) error {
	return policy.RecordBackendMoveTx(tx, move, at)
}
func recordBackendMove(ctx context.Context, storePath string, move BackendMove, at time.Time) error {
	return policy.RecordBackendMove(ctx, storePath, move, at)
}
func seatCurrentBackend(p *Policy, itemID, seat string) (string, bool) {
	return policy.SeatCurrentBackend(p, itemID, seat)
}
func seatAllowsBackend(p *Policy, itemID, seat, backend string) bool {
	return policy.SeatAllowsBackend(p, itemID, seat, backend)
}
func resolveStepBackend(p *Policy, itemID, seat, prevBackend string, shut map[string]bool) (string, *BackendMove, bool) {
	return policy.ResolveStepBackend(p, itemID, seat, prevBackend, shut)
}
