# QA: landing across the owner's uncommitted edits

Stage 5. Each row is a test in `tools/tillr-land` (`owner_test.go`, `owner_race_test.go`,
`owner_cases_test.go`) or `tools/tillr-drain/drain` unless noted. Races are forced by hooks, never by time.

| Behavior | Test |
| :--- | :--- |
| untouched paths, preparation changing nothing, recovery holding only touched content, Git call order | `TestALandingLeavesUntouchedPathsAloneAndKeepsTheStagedSplitOnTouchedOnes` |
| staged and unstaged split, overlapping | same, `TestOverlappingStagedAndUnstagedEditsKeepTheirSplit` |
| conflict changes nothing | `TestAPredictedConflictChangesNothingAndNamesThePath` |
| stale prediction, descriptor saves before and after the move | `TestASave…` tests |
| atomic editor save, collision names, parent symlink, branch switch | `TestAnAtomicEditorSave…`, `TestTemporaryNameCollisions…`, `TestAParentReplacedBySymlink…`, `TestABranchSwitchDuring…` |
| receipt and commit failure after publication | `TestAReceiptWriteFailure…`, `TestACommitFailure…` |
| failed install, symlink rollback, durable fault | `TestAFailedInstall…`, `TestARollbackRestoresAnOriginalSymlink` |
| rollback: two atomic saves, failed displaced read, failed rollback never reported exact, failed index restoration | `TestACorrectiveRollback…`, `TestAFailedDisplacedRead…`, `TestAFailedRollback…`, `TestAFailedIndexRestoration…` |
| stored receipt reconciles a surviving copy; a lost fault event is recorded by the next answer | `TestAReceiptThatIsDurable…`, `TestAFaultEventThat…` |
| renamed parent, index lock versus checkout, landed binary attribute, directory at the exchange, canceled request | `TestARenamedParent…`, `TestASwitchBeforeTheIndexLock…`, `TestACheckoutDuring…`, `TestACandidateThatAddsABinary…`, `TestADirectoryAppearing…`, `TestACanceledRequest…` |
| owner Git add/checkout refused through publication and rollback; dead lander lock reclaimed; foreign or live lock retried, never a hold | `TestOwnerGitCommandsAreRefusedByTheLock…`, `TestADeadLandersIndexLock…`, `TestALiveOrForeignIndexLock…` |
| failed read with a save during inspection, failed self-restoration, unreadable undo path, canceled rollback, symlink saves, relocation at the swap | `TestAReadFailureWithASave…`, `TestAFailedSelfRestoration…`, `TestAnUndoInspectionError…`, `TestACanceledRequestStillPreservesEveryRollbackSave`, `TestSupersededSymlinkSaves…`, `TestARelocationAtTheSwapBoundary…` |
| reconciliation errors returned; surviving copy faults recorded once | `TestReconciliationErrors…`, `TestSurvivingCopyFaults…` |
| owner renames, clean open untouched file | `TestOwnerRenames…`, `TestACleanOpenUntouchedFile…` |
| real process kill, same-run retry keeps the stale copy | `TestASurvivingCopyOfAKilledLanding…` |
| deletions, renames, modes, file and directory exchanges, binaries | `TestDeletionsAndRenames…`, `TestModeChanges…`, `TestFileAndDirectoryExchanges…`, `TestBinaryFiles…` |
| real clean/smudge filter changed by the candidate | `TestACandidateThatChangesTheSmudgeFilter…` |
| second change lands while one is held | `TestASecondChangeLandsWhileAnotherIsHeldByAConflict`; with distinct items through the drain, `TestDirtyWaitReopens…` |
| retained conflict reopens while still dirty | `TestDirtyWaitReopensWhenTheEditStopsConflicting…` (drain) |
| real landing with an owner stash and a recovery copy | `TestARealLandingWithTheOwnersStashPresent…` (drain) |

Known limits: bytes written through an old descriptor after the last read are outside the promise;
renames are a delete and an add (rename detection is off); a failed install is reported as a fault
event and an exit-4 error rather than a refusal.
