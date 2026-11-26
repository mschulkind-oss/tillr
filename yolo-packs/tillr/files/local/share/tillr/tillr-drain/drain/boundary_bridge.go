package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/boundary"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	boundary.TimeHandoffPhase = timeHandoffPhase
}

type (
	BoundarySpec       = boundary.BoundarySpec
	StoreState         = boundary.StoreState
	Worktree           = boundary.Worktree
	StatusEntry        = boundary.StatusEntry
	Boundary           = boundary.Boundary
	Violation          = boundary.Violation
	candidateSize      = boundary.CandidateSize
	deliveryDecision   = boundary.DeliveryDecision
	deliveryParseError = boundary.DeliveryParseError
)

const (
	denyListPath         = boundary.DenyListPath
	deliveryDecisionKind = boundary.DeliveryDecisionKind
	deliveryPartialKind  = boundary.DeliveryPartialKind
	deliveryOutput       = boundary.DeliveryOutput
	deliveryShape        = boundary.DeliveryShape
	deliveryPrompt       = boundary.DeliveryPrompt
	staleIndexLockKind   = boundary.StaleIndexLockKind
	tillrMirrorRoot      = boundary.TillrMirrorRoot
)

var (
	TakeBoundary          = boundary.TakeBoundary
	Compare               = boundary.Compare
	CompareRun            = boundary.CompareRun
	CompareRunObserved    = boundary.CompareRunObserved
	AuthorizeCheckoutRoot = boundary.AuthorizeCheckoutRoot
)

func (l *Loop) env() drainmodel.Env {
	return drainmodel.Env{
		Root: l.cfg.Root, StorePath: l.cfg.Store, LaneRoot: l.cfg.LaneRoot,
		Now: l.cfg.Now, Reportf: l.reportf,
	}
}

func (l *Loop) refuseParallelBoundary(ctx context.Context, d Decision, base, head string, result *LoopResult, unstarted bool) error {
	nodes := parallelNodes(d)
	return boundary.RefuseParallelBoundary(ctx, l.env(), d.LaneID, unstarted, len(nodes), func(ctx context.Context, i int) (string, error) {
		return nodes[i].Workflow.Stages[nodes[i].NodeIndex].ID, l.nodeOutcome(ctx, nodes[i], "refused")
	}, func(ctx context.Context, detail string) error {
		return l.settleParallelBoundaryRefusal(ctx, d, base, head, detail, result)
	})
}

func (l *Loop) settleParallelBoundaryRefusal(ctx context.Context, d Decision, base, head, detail string, result *LoopResult) error {
	c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: l.laneTarget(d.LaneID), BaseSHA: base, HeadSHA: head, RefusalDetail: detail}
	if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
		return err
	}
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, head, detail); stop != "" {
		return retainedAfter(d.LaneID, stop)
	}
	return l.markExamined(ctx, d)
}

func withWorktreeRegistryLock(ctx context.Context, repo string, fn func() error) error {
	return boundary.WithWorktreeRegistryLock(ctx, repo, fn)
}

func acquireRegistryLock(ctx context.Context, path string) (func(), error) {
	return boundary.AcquireRegistryLock(ctx, path)
}

func unlockRegisteredWorktree(ctx context.Context, repo, common, work string) (string, time.Time, bool, error) {
	return boundary.UnlockRegisteredWorktree(ctx, repo, common, work)
}

func authorizeWorktreeRoot(root string) error         { return boundary.AuthorizeWorktreeRoot(root) }
func realCommonDirs(starts ...string) map[string]bool { return boundary.RealCommonDirs(starts...) }
func sameDirKey(p string) string                      { return boundary.SameDirKey(p) }

func (l *Loop) denyListViolation(ctx context.Context, c Candidate) (string, error) {
	return boundary.DenyListViolation(ctx, boundary.DenyListRequest{
		Env: l.env(), TrunkRef: l.trunkRef(), ItemID: c.ItemID, Worktree: c.Worktree, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA,
		TriageCaptured: c.BlockConflict != nil && c.BlockConflict.TriageVersion != "",
		Git:            l.git, RevParse: l.revParse,
		BlobAt: func(ctx context.Context, worktree, rev, path string) ([]byte, error) {
			return l.blobAt(ctx, Candidate{Worktree: worktree}, rev, path)
		},
	})
}

func (l *Loop) measureCandidateSize(ctx context.Context, c Candidate) (candidateSize, error) {
	return boundary.MeasureCandidateSize(ctx, c.Worktree, c.BaseSHA, c.HeadSHA, c.BaseRef, l.candidateSizeLimitsSettings(), l.git)
}

func unformattedRepoFiles(ctx context.Context, worktree string) ([]string, error) {
	return boundary.UnformattedRepoFiles(ctx, worktree)
}

func (l *Loop) unformattedRepoFiles(ctx context.Context, worktree string) ([]string, error) {
	if l.settingsUnreadable() {
		return boundary.UnformattedRepoFilesInChecksDir(ctx, worktree, l.postLandingSettings().ChecksDir())
	}
	return boundary.UnformattedRepoFiles(ctx, worktree)
}

func eligibleFormatContinuation(rec run.Record) bool {
	return boundary.EligibleFormatContinuation(rec)
}

func unformattedContinuationPrompt(files []string) string {
	return boundary.UnformattedContinuationPrompt(files)
}

func unformattedRefusalDetail(files []string) string {
	return boundary.UnformattedRefusalDetail(files)
}

func classificationNode(stage WorkflowStage) bool { return boundary.ClassificationNode(stage) }

func classifyDelivery(root string, artifacts []nodeArtifact, quickWay bool) (*deliveryDecision, error) {
	out := make([]boundary.DeliveryArtifact, len(artifacts))
	for i, a := range artifacts {
		out[i] = boundary.DeliveryArtifact{Name: a.Name, Type: a.Type, Location: a.Location}
	}
	return boundary.ClassifyDelivery(root, out, quickWay)
}

func planDelivery(w *Workflow, selected string, row itemRow, known map[string]bool) *Workflow {
	return boundary.PlanDelivery(w, selected, row.ItemID, row.Seat, row.Delivery, known, IsGeneratedRepair, singleWorkerWorkflow, evaluatorSlot)
}

func workflowForDelivery(w *Workflow, d deliveryDecision) *Workflow {
	return boundary.WorkflowForDelivery(w, d, evaluatorSlot)
}

func deliveryCatalog(root string) (map[string]bool, error) { return boundary.DeliveryCatalog(root) }

func (l *Loop) recordedDelivery(ctx context.Context, id string) (deliveryDecision, error) {
	return boundary.RecordedDelivery(ctx, l.cfg.Store, id)
}

func readDelivery(r *store.Reader, id string) (deliveryDecision, error) {
	return boundary.ReadDelivery(r, id)
}

func readDeliveries(r *store.Reader) (map[string]deliveryDecision, error) {
	return boundary.ReadDeliveries(r)
}

func (l *Loop) refuseOutOfOrderDelivery(ctx context.Context, c Candidate, result *LoopResult) (bool, error) {
	return boundary.RefuseOutOfOrderDelivery(ctx, l.cfg.Store, c.ItemID, c.RunID, c.BaseRef, func(ctx context.Context, detail string) error {
		c.RefusalDetail = detail
		return l.refuse(ctx, c, result, RefusalDeliveryOrder)
	})
}

func (l *Loop) checkDeliveryOrder(ctx context.Context, c Candidate) error {
	return boundary.CheckDeliveryOrder(ctx, l.cfg.Store, c.ItemID, c.RunID, c.BaseRef)
}

func (l *Loop) settlePartialDelivery(ctx context.Context, c Candidate, sha string) (bool, error) {
	return boundary.SettlePartialDelivery(ctx, l.env(), c.ItemID, c.RunID, c.BaseRef, sha, appendEffectOn)
}

func checkoutForDelivery(root, lane string, d deliveryDecision) ForkTarget {
	return boundary.CheckoutForDelivery(root, lane, d)
}

func forkFence(fork ForkTarget, paths []string) []string { return boundary.ForkFence(fork, paths) }

func itemDeliveryFence(fork ForkTarget, paths []string) []string {
	return boundary.ItemDeliveryFence(fork, paths)
}

func (l *Loop) clearStaleIndexLock(ctx context.Context, lane, runID string) error {
	return boundary.ClearStaleIndexLock(ctx, l.env(), l.laneTarget(lane), lane, runID, l.git)
}

func packMirrorModule(path string) (string, bool) { return boundary.PackMirrorModule(path) }

func filterMirrorConflictReports(reports []string) ([]string, []string) {
	return boundary.FilterMirrorConflictReports(reports, conflictPath)
}

func (l *Loop) hasPackManifest(ctx context.Context, worktree, rev string) (bool, error) {
	return boundary.HasPackManifest(ctx, worktree, rev, l.git)
}

func (l *Loop) stagePackMirror(ctx context.Context, c Candidate) (bool, error) {
	return boundary.StagePackMirror(ctx, c.Worktree, c.BaseSHA, c.HeadSHA, c.ItemID, l.git)
}

func (l *Loop) syncAndStagePackMirrorModules(ctx context.Context, c Candidate, selectedModules []string) (bool, error) {
	return boundary.SyncAndStagePackMirrorModules(ctx, c.Worktree, c.ItemID, selectedModules, l.git)
}

func listWorktrees(ctx context.Context, repo string) ([]Worktree, error) {
	return boundary.ListWorktrees(ctx, repo)
}

func listWorktreesLocked(ctx context.Context, repo string) ([]Worktree, error) {
	return boundary.ListWorktreesLocked(ctx, repo)
}

func boundaryGit(ctx context.Context, dir string, args ...string) (string, error) {
	return boundary.BoundaryGit(ctx, dir, args...)
}

func boundaryGitEnv() []string                     { return boundary.BoundaryGitEnv() }
func canonicalDir(path string) (string, error)     { return boundary.CanonicalDir(path) }
func worktreeFor(w []Worktree, p string) *Worktree { return boundary.WorktreeFor(w, p) }
func shortSHA(sha string) string                   { return boundary.ShortSHA(sha) }
func isTarget(targets []string, path string) bool  { return boundary.IsTarget(targets, path) }
func landerRef(ref string) bool                    { return boundary.LanderRef(ref) }
func runRefExcused(ref string, b *Boundary) bool   { return boundary.RunRefExcused(ref, b) }
func compareRunWorktrees(b, a *Boundary) ([]Violation, []Violation) {
	return boundary.CompareRunWorktrees(b, a)
}
