package boundary

import "testing"

// A worktree a sibling lane's run registers inside the sibling's own lane directory is the
// sibling's doing. On 2026-11-15, with nine runs in flight, one run added a scratch
// worktree under its lane directory and five other runs that ended while it existed were
// each convicted of it, although the path named the lane and the run that wrote it.
func TestARunIsNotConvictedOfAWorktreeInsideASiblingLanesDirectory(t *testing.T) {
	mainWT := Worktree{Path: "/repo", Head: "aaa", Branch: "refs/heads/main"}
	own := Worktree{Path: "/repo/.tillr/lanes/lane-1/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	sibling := Worktree{Path: "/repo/.tillr/lanes/lane-5/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-5"}
	scratch := Worktree{Path: "/repo/.tillr/lanes/lane-5/durable/run-a3/old-recipe", Head: "aaa"}
	snap := func(worktrees ...Worktree) *Boundary {
		return &Boundary{
			Repo:        "/repo",
			Targets:     []string{own.Path},
			SiblingDirs: []string{"/repo/.tillr/lanes/lane-5"},
			Refs:        map[string]string{},
			Worktrees:   worktrees,
		}
	}

	appeared, err := compareAll(snap(mainWT, own, sibling), snap(mainWT, own, sibling, scratch))
	if err != nil {
		t.Fatal(err)
	}
	if len(appeared) != 0 {
		t.Fatalf("a worktree that appeared inside a sibling lane's directory convicted this run: %v", appeared)
	}
	gone, err := compareAll(snap(mainWT, own, sibling, scratch), snap(mainWT, own, sibling))
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 0 {
		t.Fatalf("a worktree that went away inside a sibling lane's directory convicted this run: %v", gone)
	}
}

// The excuse is the sibling's directory and nothing wider: a worktree that appears in the
// run's own lane directory, or anywhere else, is still observed on this run's bracket — as
// information, since a worktree registration cannot be attributed to the run
// (CompareRunObserved).
func TestAWorktreeOutsideASiblingLanesDirectoryIsObserved(t *testing.T) {
	mainWT := Worktree{Path: "/repo", Head: "aaa", Branch: "refs/heads/main"}
	own := Worktree{Path: "/repo/.tillr/lanes/lane-1/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	for _, path := range []string{
		"/repo/.tillr/lanes/lane-1/durable/run-a1/scratch",
		"/repo/elsewhere",
		"/repo/.tillr/lanes/lane-50/worktree",
	} {
		before := &Boundary{Repo: "/repo", Targets: []string{own.Path}, SiblingDirs: []string{"/repo/.tillr/lanes/lane-5"},
			Refs: map[string]string{}, Worktrees: []Worktree{mainWT, own}}
		after := &Boundary{Repo: "/repo", Targets: []string{own.Path}, SiblingDirs: []string{"/repo/.tillr/lanes/lane-5"},
			Refs: map[string]string{}, Worktrees: []Worktree{mainWT, own, {Path: path, Head: "aaa"}}}
		violations, observed, err := CompareRunObserved(before, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(violations) != 0 || len(observed) != 1 || observed[0].Kind != "worktree" || observed[0].Where != path {
			t.Fatalf("a worktree appearing at %s answered violations %v, observed %v, want the one observation naming it", path, violations, observed)
		}
	}
}
