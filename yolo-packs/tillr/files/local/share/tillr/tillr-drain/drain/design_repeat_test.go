package drain

import (
	"context"
	"strings"
	"testing"
)

// A design run's ending is keyed by its cause and its evidence, and only by them.
func TestDesignEndingKey(t *testing.T) {
	gate := []EndingCheck{{CheckID: "go-test", Digest: "d1"}}
	for _, tc := range []struct {
		name      string
		a, b      designEndingFacts
		cause     string
		same, nil bool
	}{
		{name: "a missing actor, same conflict and block", a: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11", RunID: "r1"}, b: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11", RunID: "r2"}, cause: designCauseMissingActor, same: true},
		{name: "a no-op refusal is a missing actor", a: designEndingFacts{Step: RefusalNoOp, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11", RunID: "r1"}, b: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11", RunID: "r2"}, cause: designCauseMissingActor, same: true},
		{name: "a missing actor on a changed block", a: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, b: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c", Block: "996327b22b153ec430d45266d54b6350d037e74028d6fe0d39291b87239cf87b"}, cause: designCauseMissingActor},
		{name: "a missing actor on another conflict", a: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c1", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, b: designEndingFacts{Step: designCauseMissingActor, ConflictID: "c2", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, cause: designCauseMissingActor},
		{name: "a removed block, same conflict and block", a: designEndingFacts{Step: designCauseBlockRemoved, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, b: designEndingFacts{Step: designCauseBlockRemoved, ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, cause: designCauseBlockRemoved, same: true},
		{name: "a gate refusal's recorded evidence", a: designEndingFacts{Step: RefusalGate, Evidence: "gate:x"}, b: designEndingFacts{Step: RefusalGate, Evidence: "gate:x"}, cause: RefusalGate, same: true},
		{name: "a gate refusal with other evidence", a: designEndingFacts{Step: RefusalGate, Evidence: "gate:x"}, b: designEndingFacts{Step: RefusalGate, Evidence: "gate:y"}, cause: RefusalGate},
		{name: "a gate refusal read from its checks", a: designEndingFacts{Step: RefusalGate, Checks: gate, RunID: "r1"}, b: designEndingFacts{Step: RefusalGate, Checks: gate, RunID: "r2"}, cause: RefusalGate, same: true},
		{name: "a gate refusal with no evidence", a: designEndingFacts{Step: RefusalGate}, b: designEndingFacts{Step: RefusalGate}, cause: RefusalGate, nil: true},
		{name: "a fence refusal's recorded evidence", a: designEndingFacts{Step: RefusalFence, Evidence: "fence:x"}, b: designEndingFacts{Step: RefusalFence, Evidence: "fence:x"}, cause: RefusalFence, same: true},
		{name: "an interrupted or killed run", a: designEndingFacts{Step: "", ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, b: designEndingFacts{Step: "", ConflictID: "c", Block: "106fe51f2819accdc8a1a5b68c12482d05be5b19cc59e911a338d92f017dfa11"}, nil: true},
		{name: "a squash conflict keeps its own route", a: designEndingFacts{Step: RefusalConflict, Evidence: "x"}, b: designEndingFacts{Step: RefusalConflict, Evidence: "x"}, nil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			causeA, keyA := designEndingKey(tc.a)
			causeB, keyB := designEndingKey(tc.b)
			if tc.nil {
				if keyA != "" || keyB != "" {
					t.Fatalf("an ending with no evidence has a key: %q %q", keyA, keyB)
				}
				return
			}
			if causeA != tc.cause || causeB != tc.cause || keyA == "" {
				t.Fatalf("causes %q %q keys %q %q, want cause %q", causeA, causeB, keyA, keyB, tc.cause)
			}
			if (keyA == keyB) != tc.same {
				t.Fatalf("keys %q and %q: same = %v, want %v", keyA, keyB, keyA == keyB, tc.same)
			}
		})
	}
}

// The design seat's repeat is the worker path's comparison, over the item's design records.
func TestDesignRepeatOf(t *testing.T) {
	m := func(run, cause, key string) designUnanswered {
		return designUnanswered{RunID: run, Cause: cause, Key: key}
	}
	for _, tc := range []struct {
		name    string
		marks   []designUnanswered
		latest  designUnanswered
		earlier string
	}{
		{name: "a first ending", latest: m("r1", RefusalGate, "k")},
		{name: "the same ending again", marks: []designUnanswered{m("r1", RefusalGate, "k")}, latest: m("r2", RefusalGate, "k"), earlier: "r1"},
		{name: "a different ending", marks: []designUnanswered{m("r1", RefusalGate, "k")}, latest: m("r2", RefusalGate, "j")},
		{name: "five different endings", marks: []designUnanswered{m("r1", RefusalGate, "a"), m("r2", RefusalGate, "b"), m("r3", RefusalGate, "c"), m("r4", RefusalGate, "d")}, latest: m("r5", RefusalGate, "e")},
		{name: "an alternation is caught on the third", marks: []designUnanswered{m("r1", RefusalGate, "a"), m("r2", RefusalGate, "b")}, latest: m("r3", RefusalGate, "a"), earlier: "r1"},
		{name: "a record written before keys existed", marks: []designUnanswered{m("r1", "", "")}, latest: m("r2", "", "")},
		{name: "the same key under another cause", marks: []designUnanswered{m("r1", RefusalFence, "k")}, latest: m("r2", RefusalGate, "k")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			earlier, repeat := designRepeatOf(tc.marks, tc.latest)
			if repeat != (tc.earlier != "") || earlier != tc.earlier {
				t.Fatalf("designRepeatOf = %q, %v; want %q", earlier, repeat, tc.earlier)
			}
		})
	}
}

// heldForOwnerOnRepeat fails unless item-a's conflict is with the owner on an identical
// repeat of design run earlier by latest: the conflict names latest, its text carries the
// loop's question naming both runs and the command that moves it, the plan holds it for
// the owner, and a settle pass writes nothing more.
func heldForOwnerOnRepeat(t *testing.T, f refusalLoopFixture, l *Loop, earlier, latest string) {
	t.Helper()
	c, _ := conflictOf(t, f, "item-a")
	if c.Status != "owner" || c.WaitingOn != "owner" || c.DesignRunID != latest {
		t.Fatalf("the repeat did not go to the owner naming %s: %+v", latest, c)
	}
	for _, want := range []string{"Question for the owner", earlier, latest, "tillr drain retry --item item-a --run " + latest, DesignNextActorField + ": owner"} {
		if !strings.Contains(c.Text, want) {
			t.Fatalf("the owner's question lacks %q:\n%s", want, c.Text)
		}
	}
	if d := decisionFor(t, l, "item-a").decision; d.Action == ActionDispatch || d.Reason != ReasonConflictOwner {
		t.Fatalf("the plan does not hold the repeat for the owner: %+v", d)
	}
	if m := lastDesignMark(t, f, c.ConflictID); !m.Repeat || m.RepeatOf != earlier || m.Reraised || m.RunID != latest {
		t.Fatalf("the repeat's record: %+v", m)
	}
	before := eventCount(t, f.storePath)
	settleDesign(t, l)
	if got := eventCount(t, f.storePath); got != before {
		t.Fatalf("a settle pass after the repeat wrote %d event(s)", got-before)
	}
}

// The design seat was re-dispatched without end on a design run that named no next actor.
// A second such ending, on the same conflict and the same block, is an identical repeat:
// it goes to the owner with a written question, and the owner's retry accepts it.
func TestADesignRunThatNamesNoActorTwiceOnTheSameBlockGoesToTheOwner(t *testing.T) {
	f, l := designConflictFixture(t)
	first := endDesignRun(t, f, l, RefusalNoOp)
	settleDesign(t, l)
	raisedForDesign(t, f, l, first, DesignNextActorField)
	second := endDesignRun(t, f, l, RefusalNoOp)
	settleDesign(t, l)
	heldForOwnerOnRepeat(t, f, l, first, second)
	if _, err := retryDesignRun(t, f, l, second); err != nil {
		t.Fatalf("the owner's retry named in the question is refused: %v", err)
	}
	raisedForDesign(t, f, l, second, "raised the conflict again")
	// The retried design run repeats the same ending again: the owner is asked again, about
	// the new pair, so the repeat never goes back to the design seat unasked.
	third := endDesignRun(t, f, l, RefusalNoOp)
	settleDesign(t, l)
	heldForOwnerOnRepeat(t, f, l, second, third)
}

// A changed block is a different ending: the design seat is given something new, so the
// missing actor goes back to it.
func TestAMissingActorOnAChangedBlockGoesBackToTheDesignSeat(t *testing.T) {
	f, l := designConflictFixture(t)
	first := endDesignRun(t, f, l, RefusalNoOp)
	settleDesign(t, l)
	raisedForDesign(t, f, l, first, DesignNextActorField)
	commitBlock(t, f.root, "item-a", "- **c1:** the first criterion of item-a, stated again")
	second := endDesignRun(t, f, l, RefusalNoOp)
	settleDesign(t, l)
	raisedForDesign(t, f, l, second, DesignNextActorField)
}

// Gate refusals go back even when the same evidence repeats.
func TestADesignGateRefusalReturnsEvenWhenEvidenceRepeats(t *testing.T) {
	f, l := designConflictFixture(t)
	for _, evidence := range []string{"gate:go-test=a", "gate:go-test=b", "gate:lint=c"} {
		design := endDesignRunWith(t, f, l, RefusalGate, "", evidence)
		settleDesign(t, l)
		raisedForDesign(t, f, l, design, "was refused at the gate")
	}
	again := endDesignRunWith(t, f, l, RefusalGate, "", "gate:go-test=b")
	settleDesign(t, l)
	raisedForDesign(t, f, l, again, "was refused at the gate")
}

// A blocked report that names no actor, settled by reportedBlock, is the same missing
// actor: the second one on the same conflict and block goes to the owner.
func TestABlockedReportNamingNoActorTwiceGoesToTheOwner(t *testing.T) {
	f, l, d, base := designAnswerFixture(t)
	c := settleDesignReport(t, f, l, d, base, "Needs a ruling.\nRESULT: blocked\n")
	if c.Status != "raised" || c.WaitingOn != "design" {
		t.Fatalf("the first missing actor did not go back to the design seat: %+v", c)
	}
	again := d
	request := *d.RunRequest
	request.RunID = d.RunRequest.RunID + "-again"
	again.RunRequest = &request
	c.Status, c.DesignRunID = "awaiting", request.RunID
	again.BlockConflict = &c
	if err := l.reportedBlock(context.Background(), again, "Still needs a ruling.\nRESULT: blocked\n"); err != nil {
		t.Fatal(err)
	}
	held, _ := conflictOf(t, f, "item-a")
	if held.Status != "owner" || held.WaitingOn != "owner" || held.DesignRunID != request.RunID || !strings.Contains(held.Text, "Question for the owner") || !strings.Contains(held.Text, d.RunRequest.RunID) {
		t.Fatalf("the repeated missing actor did not go to the owner with the question: %+v", held)
	}
}
