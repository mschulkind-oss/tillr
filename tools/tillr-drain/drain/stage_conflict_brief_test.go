package drain

import (
	"strings"
	"testing"
)

// A conflict-fix brief for a candidate the loop retired names the pinned prepared head as
// the loop's commit and tells the worker to re-apply only its own head.
func TestAConflictBriefForARetiredCandidateSaysNotToReapplyTheRetirement(t *testing.T) {
	retired := conflictTask(stagedItem{Title: "t"}, ConflictBrief{HeadSHA: "worker1", TrunkSHA: "trunk1", OriginalRunID: "run-a", PreparedHead: "prepared1"})
	if !strings.Contains(retired, "Re-apply worker1 only, not the pinned ref") || !strings.Contains(retired, "prepared1") {
		t.Fatalf("the retired brief does not separate the loop's commit: %s", retired)
	}
	plain := conflictTask(stagedItem{Title: "t"}, ConflictBrief{HeadSHA: "worker1", TrunkSHA: "trunk1", OriginalRunID: "run-a"})
	if strings.Contains(plain, "retiring") {
		t.Fatalf("an unretired brief mentions a retirement: %s", plain)
	}
}
