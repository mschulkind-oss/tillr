package drain

import (
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
)

func verifyFindings(reason string) []item.JudgeEntry {
	return []item.JudgeEntry{{CriterionID: "c1", Verdict: item.VerdictNotDone, Reason: reason}}
}

func verifyChainEnding(run, head, reason string) chainEnding {
	_, key := verifyNotDoneEvidence(head, run, verifyFindings(reason))
	return chainEnding{RunID: run, Producer: "build", Cause: RefusalVerifyNotDone, Key: key}
}

const (
	headOne = "bf8036f67aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headTwo = "611055d1daaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// Two verify refusals that share the step and criterion id but differ in candidate and
// finding are not a repeat: the worker gets the new findings.
func TestVerifyNotDoneRefusalsWithDifferentEvidenceAreNotARepeat(t *testing.T) {
	first := verifyChainEnding("run-a1", headOne, "sibling-boundary race; start latency")
	second := verifyChainEnding("run-a2", headTwo, "three new defects in start_view.go")
	if run, repeated := repeatedEnding([]chainEnding{first}, second); repeated {
		t.Fatalf("different candidate and detail read as a repeat of %s", run)
	}
	// Same finding text on a different candidate is still new evidence.
	sameText := verifyChainEnding("run-a3", headTwo, "sibling-boundary race; start latency")
	if run, repeated := repeatedEnding([]chainEnding{first}, sameText); repeated {
		t.Fatalf("different candidate read as a repeat of %s", run)
	}
	// Same candidate with a different finding is also not a repeat.
	otherText := verifyChainEnding("run-a4", headOne, "something else")
	if run, repeated := repeatedEnding([]chainEnding{first}, otherText); repeated {
		t.Fatalf("different detail read as a repeat of %s", run)
	}
}

// The same candidate refused for the same finding stays a held repeat.
func TestVerifyNotDoneRefusalOfTheSameEvidenceIsStillARepeat(t *testing.T) {
	first := verifyChainEnding("run-a1", headOne, "sibling-boundary race")
	again := verifyChainEnding("run-a2", headOne, "sibling-boundary race")
	run, repeated := repeatedEnding([]chainEnding{first}, again)
	if !repeated || run != "run-a1" {
		t.Fatalf("true repeat not held: %q %v", run, repeated)
	}
}
