package drain

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/procguard"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Type aliases to internal/procguard.
type (
	sqliteTraceForwarder    = procguard.SQLiteTraceForwarder
	streamState             = procguard.StreamState
	ProcessClass            = procguard.ProcessClass
	ProcessPrioritySummary  = procguard.ProcessPrioritySummary
	ProcessPriority         = procguard.ProcessPriority
	heavyRepairKey          = procguard.HeavyRepairKey
	ProcessCPUEntry         = procguard.ProcessCPUEntry
	ProcessCPUFact          = procguard.ProcessCPUFact
	processCPUSnapshot      = procguard.ProcessCPUSnapshot
	survivingProcess        = procguard.SurvivingProcess
	survivingProcessesError = procguard.SurvivingProcessesError
	Worker                  = procguard.Worker
	LoopOwner               = procguard.LoopOwner
	runFaceOwner            = procguard.RunFaceOwner
	reapEvidence            = procguard.ReapEvidence[*pendingWork]
)

// Constants re-exported from internal/procguard.
const (
	sqliteTraceLineLimit   = procguard.SQLiteTraceLineLimit
	streamAbsent           = procguard.StreamAbsent
	streamUnwritten        = procguard.StreamUnwritten
	streamWritten          = procguard.StreamWritten
	ProcessClassRepair     = procguard.ProcessClassRepair
	ProcessClassProof      = procguard.ProcessClassProof
	ProcessClassGate       = procguard.ProcessClassGate
	ProcessClassWorker     = procguard.ProcessClassWorker
	ProcessCPUKind         = procguard.ProcessCPUKind
	ProcessRoleDrainLoop   = procguard.ProcessRoleDrainLoop
	ProcessRoleWatchWait   = procguard.ProcessRoleWatchWait
	ProcessRoleViewServe   = procguard.ProcessRoleViewServe
	ReasonRunProcessesLive = procguard.ReasonRunProcessesLive
)

// Functions and variables re-exported from internal/procguard.
var (
	openForWriting             = procguard.OpenForWriting
	loopPresencePath           = procguard.LoopPresencePath
	openLoopPresence           = procguard.OpenLoopPresence
	holdLoopPresence           = procguard.HoldLoopPresence
	fromOtherDomain            = procguard.FromOtherDomain
	endedSessionOwners         = procguard.EndedSessionOwners
	foreignOwnerGone           = procguard.ForeignOwnerGone
	processClassOrder          = procguard.ProcessClassOrder
	processClassNiceOffsets    = procguard.ProcessClassNiceOffsets
	InitProcessPriority        = procguard.InitProcessPriority
	newNicePriority            = procguard.NewNicePriority
	newNicePriorityWithBase    = procguard.NewNicePriorityWithBase
	storePriorityHash          = procguard.StorePriorityHash
	formatSurvivingProcesses   = procguard.FormatSurvivingProcesses
	survivingRunProcesses      = procguard.SurvivingRunProcesses
	waitProcessExit            = procguard.WaitProcessExit
	awaitSurvivingRunProcesses = procguard.AwaitSurvivingRunProcesses
	processInCheckout          = procguard.ProcessInCheckout
	liveRunWorkerPIDs          = procguard.LiveRunWorkerPIDs
	Workers                    = procguard.Workers
	processStart               = procguard.ProcessStart
	currentLoopOwner           = procguard.CurrentLoopOwner
	ownerReads                 = procguard.OwnerReads
	processGone                = procguard.ProcessGone
	ownerDead                  = procguard.OwnerDead
	processStoreIdentity       = procguard.ProcessStoreIdentity
	runProcessEnvironment      = procguard.RunProcessEnvironment
	withoutProcessEnv          = procguard.WithoutProcessEnv
	procScans                  = &procguard.ProcScans
	liveRunIDs                 = procguard.LiveRunIDs
	liveRunProcessIDs          = procguard.LiveRunProcessIDs
	liveRunFaces               = procguard.LiveRunFaces
	liveRunWorkers             = procguard.LiveRunWorkers
	isRunFaceArgv              = procguard.IsRunFaceArgv
	dispatchSessionEnded       = procguard.DispatchSessionEnded
	resumeUnfinished           = procguard.ResumeUnfinished
	leasesWithNoExpiry         = procguard.LeasesWithNoExpiry
	runHasActiveResume         = procguard.RunHasActiveResume
)

func locatorFunc(locator runStreamLocator) func(string) []string {
	if locator == nil {
		return nil
	}
	return locator.runOutputFiles
}

func foreignRunLive(locator runStreamLocator, runID, ownerDomain, hereDomain string) bool {
	return procguard.ForeignRunLive(locatorFunc(locator), runID, ownerDomain, hereDomain)
}

func missingRunStreams(locator runStreamLocator, id string) bool {
	return procguard.MissingRunStreams(locatorFunc(locator), id)
}

func (s *ProcessSteps) applyHeavyPriority(ctx context.Context, cmd *exec.Cmd) error {
	if s == nil {
		return nil
	}
	return s.priority.ApplyHeavyToCmd(ctx, cmd)
}

func (l *Loop) primeProcessCPU() {
	procguard.PrimeProcessCPU(&l.processCPUMu, &l.lastProcessCPU, l.procRoot, l.selfPID, l.cfg.Now())
}

func (l *Loop) processCPUSessionInfo() (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sessionID, l.sessionInitialized
}

func (l *Loop) recordProcessCPUIfDue(ctx context.Context) {
	procguard.RecordProcessCPUIfDue(ctx, procguard.CPUTrackerParams{
		Mu: &l.processCPUMu, Last: &l.lastProcessCPU, ProcRoot: l.procRoot, SelfPID: l.selfPID,
		Now: l.cfg.Now, StorePath: l.cfg.Store, SessionInfo: l.processCPUSessionInfo,
		SessionOptions: l.sessionOptions, Reportf: l.reportf,
	})
}

func examinationRunIDs(work *pendingWork) []string {
	ids := parallelRunIDs(work.Decision)
	if work.Candidate != nil && work.Candidate.RunID != "" {
		ids = append(ids, work.Candidate.RunID)
	}
	return ids
}

func parallelRunIDs(d Decision) []string {
	var ids []string
	for _, node := range parallelNodes(d) {
		if node.RunRequest != nil && node.RunRequest.RunID != "" {
			ids = append(ids, node.RunRequest.RunID)
		}
	}
	return ids
}

func (l *Loop) checkSurvivingRunProcesses(ctx context.Context, itemID, lane string, runIDs ...string) (laneOutcome, bool) {
	runID, procs, err := procguard.FindSurvivingRunProcesses(ctx, l.cfg.Store, runIDs...)
	if err != nil {
		return laneOutcome{err: err}, true
	}
	if len(procs) > 0 {
		return l.retainSurvivingProcesses(ctx, itemID, lane, runID, procs), true
	}
	return laneOutcome{}, false
}

func (l *Loop) retainSurvivingProcesses(ctx context.Context, itemID, lane, runID string, procs []survivingProcess) laneOutcome {
	hold := func(d string) error {
		return l.holdItemFault(ctx, Candidate{ItemID: itemID, RunID: runID, LaneID: lane}, ReasonRunProcessesLive, d)
	}
	res := procguard.RetainSurvivingProcesses(itemID, lane, runID, procs, l.reportf, hold, func(e error) bool { return asItemTerminal(e) != nil })
	return survivingOutcome(lane, runID, itemID, res)
}

func survivingOutcome(lane, runID, itemID string, res procguard.RetainSurvivingOutcome) laneOutcome {
	if len(res.TerminalPIDs) > 0 {
		return laneOutcome{stop: stopLaneRetained, laneRefusal: holdLaneChildrenRefusal(lane, res.TerminalPIDs, formatLeftBy(runID, itemID))}
	}
	if res.Fault {
		return laneOutcome{stop: StopFault}
	}
	return laneOutcome{stop: stopLaneRetained}
}

func (l *Loop) recheckLaneChildren(ctx context.Context) error {
	return procguard.RecheckLaneChildren(ctx, l.cfg.Store, l.cfg.LaneRoot, procguard.LaneChildrenHooks{
		LoadLaneConditions: l.loadLaneConditions, Lanes: l.lanes,
		HeldChildrenLanes: l.heldChildrenLanes,
		ResolveRunLeftBy:  l.resolveRunLeftBy,
		HoldLaneChildren:  l.holdLaneChildren, RestoreLane: l.restoreLane,
		HasLaneCondition: func(lane string) bool { return l.hasLaneCondition(lane, ReasonLaneChildren) },
	})
}

func (l *Loop) heldChildrenLanes() (out []string) {
	l.laneMu.Lock()
	defer l.laneMu.Unlock()
	for lane, c := range l.laneConditions {
		if c != nil && c.reason == ReasonLaneChildren {
			out = append(out, lane)
		}
	}
	return out
}

func (l *Loop) processInLane(p *runFaceOwner, lane string) (bool, error) {
	return procguard.ProcessInCheckout(p, filepath.Join(l.cfg.LaneRoot, lane))
}

func holdLaneChildrenRefusal(lane string, pids []int, leftBy string) *LaneRefusalError {
	remediation, message := procguard.FormatLaneChildrenRefusal(lane, pids)
	refusal := asLaneRefusal(laneRefusalf(lane, ReasonLaneChildren, remediation, "%s", message))
	refusal.LeftBy = leftBy
	return refusal
}

func (l *Loop) holdLaneChildren(ctx context.Context, lane string, pids []int, leftBy string) error {
	return l.takeLaneOutOfRotation(ctx, holdLaneChildrenRefusal(lane, pids, leftBy))
}

func (l *Loop) isRotatedLane(lane string) bool {
	return slices.Contains(l.lanes(), lane)
}

func (l *Loop) hasInterruptedWork(ctx context.Context, w *pendingWork) (bool, error) {
	return l.hasInterruptedWorkflowSession(ctx, w.Decision)
}

func (l *Loop) gatherReapEvidence(ctx context.Context, s *store.Store) (reapEvidence, error) {
	locator, _ := l.cfg.Steps.(runStreamLocator)
	return procguard.GatherReapEvidence(ctx, s, procguard.GatherHost[*pendingWork]{
		StorePath: l.cfg.Store, CurrentSessionID: l.currentSessionID, IsActive: l.isActive,
		IsRotatedLane: func(lane string) bool { return l.isRotatedLane(lane) || !isPoolLaneName(lane) }, IsDocumentDesk: documentDesk, ReadPending: readPendingViews,
		TerminalRecord: terminalRecord, HasInterruptedWorkflowSession: l.hasInterruptedWork,
		RunOutputFiles: locatorFunc(locator),
	})
}

func (l *Loop) reapDeadLoopRuns(ctx context.Context, tx *store.Tx, at time.Time, ev reapEvidence) error {
	return procguard.ReapDeadLoopRuns(ctx, tx, at, ev, procguard.ReapTxHost[*pendingWork]{
		ReadPending: readPendingViews, TerminalRecord: terminalRecord, AppendEffect: appendEffect,
		ReapParallel: l.reapParallel, CloneRaw: clonePendingWork, Reportf: l.reportf,
		RetractClaim: func(tx *store.Tx, w *pendingWork, at time.Time) error { return retractClaim(tx, w.Decision, at, true) },
	})
}

func (l *Loop) hasInterruptedWorkflowSession(ctx context.Context, d Decision) (bool, error) {
	if ok, err := l.canCheckInterruptedSession(ctx, d); !ok || err != nil {
		return false, err
	}
	return procguard.HasInterruptedWorkflowSession(l.cfg.Root, d.RunRequest.RunID, l.laneDirPath(d.LaneID), l.laneTarget(d.LaneID), BriefFile)
}

func (l *Loop) canCheckInterruptedSession(ctx context.Context, d Decision) (bool, error) {
	if d.Workflow == nil || len(d.Parallel) != 0 || commandStage(d) {
		return false, nil
	}
	if kind, err := adapterPauseKind(ctx, l.cfg.Root, d.RunRequest); err != nil || kind != run.PauseAtGap {
		return false, err
	}
	staged, err := l.stagedNodeRun(d)
	return err == nil && staged == d.RunRequest.RunID, err
}

func readPendingViews(r *store.Reader) ([]procguard.PendingWorkView[*pendingWork], error) {
	works, err := readPending(r)
	out := make([]procguard.PendingWorkView[*pendingWork], 0, len(works))
	for _, w := range works {
		out = append(out, toPendingWorkView(w))
	}
	return out, err
}

func toPendingWorkView(w *pendingWork) procguard.PendingWorkView[*pendingWork] {
	v := procguard.PendingWorkView[*pendingWork]{
		Raw: w, ItemID: w.Decision.ItemID, LaneID: w.Decision.LaneID, ParentRunID: w.ParentRunID,
		LoopID: w.LoopID, Owner: w.Owner, Examiner: w.Examiner, Done: w.Done, Ready: w.Ready,
		Examining: w.Examining, Orphaned: w.Orphaned, LaneReleased: w.LaneReleased,
		HasWorkflow: w.Decision.Workflow != nil, HasParallel: len(w.Decision.Parallel) > 0,
		ParallelRunIDs: parallelRunIDs(w.Decision), SingleRecoverable: isSingleWorkerRecoverable(w),
	}
	return fillPendingViewRequest(v, w.Decision)
}

func fillPendingViewRequest(v procguard.PendingWorkView[*pendingWork], d Decision) procguard.PendingWorkView[*pendingWork] {
	if d.RunRequest == nil {
		return v
	}
	v.RunID, v.ReadOnlyGroup = d.RunRequest.RunID, d.RunRequest.ReadOnlyGroup
	stamped := *d.RunRequest
	stampDecisionRequest(d, &stamped)
	v.StampedBackend, v.StampedSeat, v.StampedWorkflow, v.StampedNode = stamped.Backend, stamped.Seat, stamped.Workflow, stamped.Node
	return v
}

func isSingleWorkerRecoverable(work *pendingWork) bool {
	d := work.Decision
	return d.Workflow == nil && !work.Done && work.Candidate == nil && work.DirtyHold == nil && work.RepairHold == nil && work.GateStall == nil && !work.BoundaryFailed
}

func clonePendingWork(w *pendingWork) *pendingWork {
	cp := *w
	return &cp
}
