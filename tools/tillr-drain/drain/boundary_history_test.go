package drain

import "testing"

func TestOnlyLoopHistoryPinsAreExcused(t *testing.T) {
	main := Worktree{Path: "/repo", Head: "base", Branch: "refs/heads/main"}
	lane := Worktree{Path: "/lane", Head: "base", Branch: "refs/heads/tillr/lane-1"}
	before := &Boundary{Repo: "/repo", Targets: []string{lane.Path}, Refs: map[string]string{}, Worktrees: []Worktree{main, lane}}
	after := &Boundary{Repo: before.Repo, Targets: before.Targets, Refs: map[string]string{observedTrunkRef: "new", lostTrunkRef: "old"}, Worktrees: before.Worktrees}
	for _, compare := range []func(*Boundary, *Boundary) ([]Violation, error){Compare, CompareRun} {
		if violations, err := compare(before, after); err != nil || len(violations) != 0 {
			t.Fatalf("loop history pins convicted a run: %v / %v", violations, err)
		}
	}
	after.Refs["refs/tillr/trunk-history/unauthorized"] = "rogue"
	for _, compare := range []func(*Boundary, *Boundary) ([]Violation, error){Compare, CompareRun} {
		if violations, err := compare(before, after); err != nil || len(violations) != 1 {
			t.Fatalf("unassigned history ref excused: %v / %v", violations, err)
		}
	}
}
