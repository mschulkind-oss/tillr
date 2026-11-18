package main_test

import (
	"github.com/mschulkind-oss/tillr-land/land"
	"testing"
)

func TestIdentityRewriteComparesEveryTreeMessageAndOrderedParent(t *testing.T) {
	f := newFixture(t)
	tree := git(t, f.dir, "rev-parse", f.baseSHA+"^{tree}")
	commit := func(name, msg, tr string, parents ...string) string {
		args := []string{"commit-tree", tr, "-m", msg}
		for _, p := range parents {
			args = append(args, "-p", p)
		}
		return gitAs(t, f.dir, name, name+"@example.invalid", args...)
	}
	oldLeft := commit("Old", "left", tree, f.baseSHA)
	oldRight := commit("Old", "right", tree, f.baseSHA)
	oldTip := commit("Old", "merge", tree, oldLeft, oldRight)
	newLeft := commit("New", "left", tree, f.baseSHA)
	newRight := commit("New", "right", tree, f.baseSHA)
	tr, err := land.OpenTrunk(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, tip string
		want      bool
	}{
		{"identity-only merge", commit("New", "merge", tree, newLeft, newRight), true},
		{"parent order", commit("New", "merge", tree, newRight, newLeft), false},
		{"parent count", commit("New", "merge", tree, newLeft), false},
		{"message", commit("New", "changed", tree, newLeft, newRight), false},
		{"intermediate message", commit("New", "merge", tree, commit("New", "edited", tree, f.baseSHA), newRight), false},
		{"intermediate tree restored at tip", commit("New", "merge", tree, commit("New", "left", f.treeSHA, f.baseSHA), newRight), false},
	}
	// Identical content does not make distinct parent ids interchangeable.
	x := commit("X", "same", tree, f.baseSHA)
	y := commit("Y", "same", tree, f.baseSHA)
	a := commit("Old", "twins", tree, x, y)
	b := commit("New", "twins", tree, y, x)
	if _, same, err := tr.IdentityRewrite(a, b); err != nil || same {
		t.Fatalf("reordered identity twins accepted: %v %v", same, err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped, ok, err := tr.IdentityRewrite(oldTip, tc.tip)
			if err != nil || ok != tc.want {
				t.Fatalf("equivalence %v %v mapping %v", ok, err, mapped)
			}
			if ok && (mapped[oldLeft] != newLeft || mapped[oldRight] != newRight) {
				t.Fatalf("lost parent mappings: %v", mapped)
			}
		})
	}
}
