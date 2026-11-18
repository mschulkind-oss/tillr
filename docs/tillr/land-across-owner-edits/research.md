# Research: landing across the owner's uncommitted edits

Stage 1 of [the build](../roadmap.md#route-a-landing-around-the-owners-uncommitted-edits).

- The lander moved the trunk inside the store's write transaction with `git merge --ff-only`.
  Git stops on any dirty touched path, so `ClashingPaths` and the drain's wait check refused on
  any overlap. A merge cannot carry the owner's content across, so the move was replaced.
- Git answers each need without a checkout write: `diff-tree --raw` for touched paths, modes and
  blobs; `hash-object --path` for the stored form of a file; `merge-file -p` for a three-way merge;
  `cat-file --filters --path` with `--attr-source` for the checked-out form under the landed
  attributes; a scratch `GIT_INDEX_FILE` for the recovery tree.
- A rename of a temporary over the path loses the displaced inode unless the lander holds it. A
  `renameat2` exchange leaves that inode under the temporary's random name, which only the lander
  knows, so reading it there reads exactly what was displaced.
- The boundary check's ignored list is `landerRef` in `tools/tillr-drain/drain/boundary.go`.
