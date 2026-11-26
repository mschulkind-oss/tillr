package boundary

import "testing"

func TestTrunkLandingWhileOwnerContentRemainsDoesNotConvictSibling(t *testing.T) {
	main := Worktree{Path: "/repo", Head: "before", Branch: "refs/heads/main", Status: []StatusEntry{{Path: "owner.txt", Code: " M"}}}
	target := Worktree{Path: "/repo/lanes/lane-1/worktree", Head: "before", Branch: "refs/heads/tillr/lane-1"}
	before := &Boundary{Repo: "/repo", Targets: []string{target.Path}, Refs: map[string]string{"refs/heads/main": "before"}, Worktrees: []Worktree{main, target}}
	main.Head = "landed"
	after := &Boundary{Repo: "/repo", Targets: before.Targets, Refs: map[string]string{"refs/heads/main": "landed"}, Worktrees: []Worktree{main, target}}
	violations, err := compareAll(before, after)
	if err != nil || len(violations) != 0 {
		t.Fatalf("owner dirt made a legitimate landing convict a sibling: %v / %v", violations, err)
	}
}
