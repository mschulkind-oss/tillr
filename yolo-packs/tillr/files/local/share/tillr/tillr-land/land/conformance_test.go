package land_test

import (
	"sort"
	"testing"

	"github.com/mschulkind-oss/tillr-land/land"
)

// TestTheRefusalReasonsAreTheRegisteredSet pins every reason the utility can emit. A new
// reason cannot be added without naming it here, which is what keeps the reasons a reader
// queries against a closed set rather than a scattering of string literals.
func TestTheRefusalReasonsAreTheRegisteredSet(t *testing.T) {
	want := []string{
		"patch-at-head",
		"patch-rewind",
		"trunk-worktree-uncommitted",
		"verdict-error",
		"verdict-failed",
		"verdict-mismatch",
	}
	got := append([]string{}, land.Reasons()...)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("reasons are %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reasons are %v, want %v", got, want)
		}
	}
}

// TestTheOutcomesAreTheRecordedSet pins the three results a landing row can carry. A
// landing is landed, a squash conflict, or refused, and no fourth spelling.
func TestTheOutcomesAreTheRecordedSet(t *testing.T) {
	for _, result := range []string{land.ResultLanded, land.ResultSquashConflict, land.ResultRefused} {
		if result == "" {
			t.Fatal("an outcome is the empty string")
		}
	}
	if land.ResultLanded == land.ResultRefused || land.ResultLanded == land.ResultSquashConflict ||
		land.ResultRefused == land.ResultSquashConflict {
		t.Fatal("two outcomes share one spelling")
	}
}

// TestPatchRefNamesOnlyPatchBranches: a patch branch is a delivery a landing advances rather
// than a trunk it squashes onto, and only a branch under refs/heads/patch/ is one.
func TestPatchRefNamesOnlyPatchBranches(t *testing.T) {
	for _, tc := range []struct{ ref, want string }{
		{"patch/pi", "refs/heads/patch/pi"},
		{"refs/heads/patch/pi", "refs/heads/patch/pi"},
		{"main", ""},
		{"refs/heads/main", ""},
		{"patch", ""},
		{"refs/heads/patch/", ""},
		{"refs/tags/patch/pi", ""},
	} {
		if got := land.PatchRef(tc.ref); got != tc.want {
			t.Fatalf("PatchRef(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}
}
