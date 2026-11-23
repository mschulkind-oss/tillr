package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
)

const (
	GreenRef              = heavyrun.GreenRef
	HeavyProgressKind     = heavyrun.HeavyProgressKind
	goTestPrefix          = heavyrun.GoTestPrefix
	checkGroupGrace       = heavyrun.CheckGroupGrace
	heavyCleanupGuard     = heavyrun.HeavyCleanupGuard
	heavyReplayGuard      = heavyrun.HeavyReplayGuard
	heavyAttributionsFile = heavyrun.HeavyAttributionsFile
	heavyReplaysFile      = heavyrun.HeavyReplaysFile
)

type (
	HeavyCheck        = heavyrun.HeavyCheck
	HeavyRun          = heavyrun.HeavyRun
	HeavyOutcome      = heavyrun.HeavyOutcome
	HeavyStatus       = heavyrun.HeavyStatus
	HeavyRunner       = heavyrun.HeavyRunner
	HeavyAttempt      = heavyrun.HeavyAttempt
	HeavyReplay       = heavyrun.HeavyReplay
	RevertOutcome     = heavyrun.RevertOutcome
	LoopLock          = heavyrun.LoopLock
	HeavyProgressFact = heavyrun.HeavyProgressFact

	heavyUnownedKey        = heavyrun.HeavyUnownedKey
	heavyInverseKey        = heavyrun.HeavyInverseKey
	heavyProgressReportKey = heavyrun.HeavyProgressReportKey
	heavyCheckEndedKey     = heavyrun.HeavyCheckEndedKey
	heavyOwnershipKey      = heavyrun.HeavyOwnershipKey
	heavyOwnership         = heavyrun.HeavyOwnership
	heavyCommandNotStarted = heavyrun.HeavyCommandNotStarted
	heavyCleanupMarker     = heavyrun.HeavyCleanupMarker
	heavyProgress          = heavyrun.HeavyProgress
	heavyProgressKey       = heavyrun.HeavyProgressKey
	heavyAttribution       = heavyrun.HeavyAttribution
	legacyGoCase           = heavyrun.LegacyGoCase
	legacyFailure          = heavyrun.LegacyFailure
)

var (
	ReadHeavyStatus                 = heavyrun.ReadHeavyStatus
	acquireLoopLock                 = heavyrun.AcquireLoopLock
	cleanupLost                     = heavyrun.CleanupLost
	heavyProgressReport             = heavyrun.HeavyProgressReport
	heavyCheckEnded                 = heavyrun.HeavyCheckEnded
	reuseHeavyCheck                 = heavyrun.ReuseHeavyCheck
	saveHeavyCheck                  = heavyrun.SaveHeavyCheck
	heavyLegacyArgv                 = heavyrun.HeavyLegacyArgv
	nextHeavyLegacyNonce            = heavyrun.NextHeavyLegacyNonce
	startHeavyLegacyProgressWatcher = heavyrun.StartHeavyLegacyProgressWatcher
	startHeavyReplayProgressWatcher = heavyrun.StartHeavyReplayProgressWatcher
	readLegacyEvidence              = heavyrun.ReadLegacyEvidence
	runOwnedHeavy                   = heavyrun.RunOwnedHeavy
	runCheckGroupStarted            = heavyrun.RunCheckGroupStarted
	runHeavyDeclarationGroupStarted = heavyrun.RunHeavyDeclarationGroupStarted
	startAndWait                    = heavyrun.StartAndWait
	endProcessGroup                 = heavyrun.EndProcessGroup
	revertedBy                      = heavyrun.RevertedBy
	heavyReadsChanged               = heavyrun.HeavyReadsChanged
	rebasedLanding                  = heavyrun.RebasedLanding
	rebaseNote                      = heavyrun.RebaseNote
	joinDetail                      = heavyrun.JoinDetail
	heavyBrief                      = heavyrun.HeavyBrief
	heavyStoreWholeStepProgress     = heavyrun.HeavyStoreWholeStepProgress
	heavyStoreStepProgress          = heavyrun.HeavyStoreStepProgress
	hasHeavyStoreProgress           = heavyrun.HasHeavyStoreProgress
	heavyDirectory                  = heavyrun.HeavyDirectory
	loadHeavyProgress               = heavyrun.LoadHeavyProgress
	readHeavyReplay                 = heavyrun.ReadHeavyReplay
	publishHeavy                    = heavyrun.PublishHeavy
	prepareHeavyTree                = heavyrun.PrepareHeavyTree
	finishHeavyTree                 = heavyrun.FinishHeavyTree
	heavySupervisorIdentity         = heavyrun.HeavySupervisorIdentity
	heavySupervisorRunning          = heavyrun.HeavySupervisorRunning
	reapWhileSupervising            = heavyrun.ReapWhileSupervising
	runSupervisedTree               = heavyrun.RunSupervisedTree
	publishHeavyJSON                = heavyrun.PublishHeavyJSON
	readHeavyAttributions           = heavyrun.ReadHeavyAttributions
	saveHeavyReplay                 = heavyrun.SaveHeavyReplay
	completedHeavyRed               = heavyrun.CompletedHeavyRed
	heavyFailureFingerprint         = heavyrun.HeavyFailureFingerprint
	heavyLandings                   = heavy.Landings
)

func heavyGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	return gather(ctx, gitKey("trimmed output", root, args), func(ctx context.Context) (string, error) {
		return heavy.GitOutput(ctx, root, args...)
	})
}

func heavyGreen(ctx context.Context, root string) (string, error) {
	return gather(ctx, "green pointer in "+root, func(ctx context.Context) (string, error) {
		return heavy.Green(ctx, root)
	})
}

func recoverHeavyCleanupGuards(ctx context.Context, root, dir string) (string, error) {
	return heavyrun.RecoverHeavyCleanupGuardsWithLock(ctx, root, dir, withWorktreeRegistryLock)
}
