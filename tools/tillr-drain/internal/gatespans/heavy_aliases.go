package gatespans

import (
	"context"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/boundary"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
)

const (
	GreenRef              = heavyrun.GreenRef
	HeavyProgressKind     = heavyrun.HeavyProgressKind
	GoTestPrefix          = heavyrun.GoTestPrefix
	CheckGroupGrace       = heavyrun.CheckGroupGrace
	HeavyCleanupGuard     = heavyrun.HeavyCleanupGuard
	HeavyReplayGuard      = heavyrun.HeavyReplayGuard
	HeavyAttributionsFile = heavyrun.HeavyAttributionsFile
	HeavyReplaysFile      = heavyrun.HeavyReplaysFile
)

type (
	HeavyCheck             = heavyrun.HeavyCheck
	HeavyRun               = heavyrun.HeavyRun
	HeavyOutcome           = heavyrun.HeavyOutcome
	HeavyStatus            = heavyrun.HeavyStatus
	HeavyRunner            = heavyrun.HeavyRunner
	HeavyAttempt           = heavyrun.HeavyAttempt
	HeavyReplay            = heavyrun.HeavyReplay
	RevertOutcome          = heavyrun.RevertOutcome
	LoopLock               = heavyrun.LoopLock
	HeavyProgressFact      = heavyrun.HeavyProgressFact
	HeavyUnownedKey        = heavyrun.HeavyUnownedKey
	HeavyInverseKey        = heavyrun.HeavyInverseKey
	HeavyProgressReportKey = heavyrun.HeavyProgressReportKey
	HeavyCheckEndedKey     = heavyrun.HeavyCheckEndedKey
	HeavyOwnershipKey      = heavyrun.HeavyOwnershipKey
	HeavyOwnership         = heavyrun.HeavyOwnership
	HeavyCommandNotStarted = heavyrun.HeavyCommandNotStarted
	HeavyCleanupMarker     = heavyrun.HeavyCleanupMarker
	HeavyProgress          = heavyrun.HeavyProgress
	HeavyProgressKey       = heavyrun.HeavyProgressKey
	HeavyAttribution       = heavyrun.HeavyAttribution
	LegacyGoCase           = heavyrun.LegacyGoCase
	LegacyFailure          = heavyrun.LegacyFailure
)

var (
	ReadHeavyStatus                   = heavyrun.ReadHeavyStatus
	ReadHeavyStatusForBranch          = heavyrun.ReadHeavyStatusForBranch
	AcquireLoopLock                   = heavyrun.AcquireLoopLock
	CleanupLost                       = heavyrun.CleanupLost
	HeavyProgressReport               = heavyrun.HeavyProgressReport
	HeavyCheckEnded                   = heavyrun.HeavyCheckEnded
	ReuseHeavyCheck                   = heavyrun.ReuseHeavyCheck
	SaveHeavyCheck                    = heavyrun.SaveHeavyCheck
	LoadHeavyFailingTests             = heavyrun.LoadHeavyFailingTests
	SaveHeavyFailingTests             = heavyrun.SaveHeavyFailingTests
	ClearHeavyFailingTests            = heavyrun.ClearHeavyFailingTests
	StartHeavyReplayProgressWatcher   = heavyrun.StartHeavyReplayProgressWatcher
	StartHeavyCheckLogProgressWatcher = heavyrun.StartHeavyCheckLogProgressWatcher
	StartHeavyLogFileProgressWatcher  = heavyrun.StartHeavyLogFileProgressWatcher
	ReadLegacyEvidence                = heavyrun.ReadLegacyEvidence
	NextHeavyLegacyNonce              = heavyrun.NextHeavyLegacyNonce
	RunOwnedHeavy                     = heavyrun.RunOwnedHeavy
	RunCheckGroupStarted              = heavyrun.RunCheckGroupStarted
	RunHeavyDeclarationGroupStarted   = heavyrun.RunHeavyDeclarationGroupStarted
	StartAndWait                      = heavyrun.StartAndWait
	EndProcessGroup                   = heavyrun.EndProcessGroup
	RevertedBy                        = heavyrun.RevertedBy
	HeavyReadsChanged                 = heavyrun.HeavyReadsChanged
	RebasedLanding                    = heavyrun.RebasedLanding
	RebaseNote                        = heavyrun.RebaseNote
	JoinDetail                        = heavyrun.JoinDetail
	HeavyBrief                        = heavyrun.HeavyBrief
	HeavyStoreWholeStepProgress       = heavyrun.HeavyStoreWholeStepProgress
	HeavyStoreStepProgress            = heavyrun.HeavyStoreStepProgress
	HasHeavyStoreProgress             = heavyrun.HasHeavyStoreProgress
	HeavyDirectory                    = heavyrun.HeavyDirectory
	LoadHeavyProgress                 = heavyrun.LoadHeavyProgress
	ReadHeavyReplay                   = heavyrun.ReadHeavyReplay
	PublishHeavy                      = heavyrun.PublishHeavy
	PrepareHeavyTree                  = heavyrun.PrepareHeavyTree
	FinishHeavyTree                   = heavyrun.FinishHeavyTree
	HeavySupervisorIdentity           = heavyrun.HeavySupervisorIdentity
	HeavySupervisorRunning            = heavyrun.HeavySupervisorRunning
	ReapWhileSupervising              = heavyrun.ReapWhileSupervising
	RunSupervisedTree                 = heavyrun.RunSupervisedTree
	PublishHeavyJSON                  = heavyrun.PublishHeavyJSON
	ReadHeavyAttributions             = heavyrun.ReadHeavyAttributions
	SaveHeavyReplay                   = heavyrun.SaveHeavyReplay
	CompletedHeavyRed                 = heavyrun.CompletedHeavyRed
	HeavyFailureFingerprint           = heavyrun.HeavyFailureFingerprint
	HeavyLandings                     = heavy.Landings
)

// GitKey names one git command in one checkout, the key gathered I/O is answered by.
func GitKey(form, root string, args []string) string {
	return form + ": git -C " + root + " " + strings.Join(args, " ")
}

func HeavyGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	return runhistory.Gather(ctx, GitKey("trimmed output", root, args), func(ctx context.Context) (string, error) {
		return heavy.GitOutput(ctx, root, args...)
	})
}

func HeavyGreen(ctx context.Context, root string) (string, error) {
	return runhistory.Gather(ctx, "green pointer in "+root, func(ctx context.Context) (string, error) {
		return heavy.Green(ctx, root)
	})
}

func RecoverHeavyCleanupGuards(ctx context.Context, root, dir string) (string, error) {
	return heavyrun.RecoverHeavyCleanupGuardsWithLock(ctx, root, dir, boundary.WithWorktreeRegistryLock)
}
