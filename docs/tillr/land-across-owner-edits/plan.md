# Plan: landing across the owner's uncommitted edits

Stage 2. Details and cases live in the [lander README](../../../tools/tillr-land/README.md#the-owners-working-tree).

1. `PlanOwner` predicts and writes the recovery copy before the write transaction; `MoveOwner` does
   the comparison, the install through no-follow directory handles, the index write and a guarded
   `update-ref`; `Finish` makes the last read.
2. A change under the transaction (`ErrOwnerChanged`) rolls it back and the landing predicts again.
3. Everything after the transaction (the last read, the fault event, the cleanup) runs after it
   ends, whether it committed or failed; a published checkout is never undone.
4. The drain's wait check asks the same prediction; the boundary check ignores the recovery prefix.
