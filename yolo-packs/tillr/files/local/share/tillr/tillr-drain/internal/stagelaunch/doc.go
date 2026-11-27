// tillr-arch: layer=3 imports=gateland

// Package stagelaunch holds the standalone outside-pass staging (Step 1) and
// worker launch (Step 2) coordinator state, eligibility predicates, worktree
// stage-lock acquisition, transient Git config.lock retry, and store-reader
// helpers for Scheduling Slice 5.
package stagelaunch
