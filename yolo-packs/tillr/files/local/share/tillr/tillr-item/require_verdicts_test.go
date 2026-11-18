package main_test

import "testing"

func TestStateRequiresVerdictsForEveryStoredCriterion(t *testing.T) {
	db := findingsStore(t)
	p := base("item_042", "a finding")
	p.State = "queued"
	p.Criteria = append(p.Criteria, criterion{CriterionID: "c2", Text: "the second criterion", State: "open"})
	if code, out := apply(t, db, envelope(t, "item.record", p.ItemID, p)); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	if code, out := recordVerdict(t, db, mechanicalVerdict()); code != 0 {
		t.Fatalf("verdict: %d %s", code, out)
	}
	// c1 is judged, but c2 is not: criterion state alone is not a verdict receipt.
	code, out := findingState(t, db, p.ItemID, "confirmed", "--require-verdicts", "--reason", "checked", "--by", "definition:findings")
	assertRefused(t, code, out, "criteria-unjudged")
	v := mechanicalVerdict()
	v.CriterionID, v.CriterionHash = "c2", hashOf("the second criterion")
	if code, out := recordVerdict(t, db, v); code != 0 {
		t.Fatalf("verdict c2: %d %s", code, out)
	}
	// An apply can append a criterion after all the old criteria were judged.
	p.Criteria[0].State, p.Criteria[1].State = "done", "done"
	p.Criteria = append(p.Criteria, criterion{CriterionID: "c3", Text: "new criterion", State: "open"})
	if code, out := apply(t, db, envelope(t, "item.record", p.ItemID, p)); code != 0 {
		t.Fatalf("append: %d %s", code, out)
	}
	code, out = findingState(t, db, p.ItemID, "confirmed", "--require-verdicts", "--reason", "checked", "--by", "definition:findings")
	assertRefused(t, code, out, "criteria-unjudged")
	code, out = run(t, "show", "--store", db, "--item", p.ItemID)
	if code != 0 || decode(t, out).Payload.State != "queued" {
		t.Fatalf("item moved: %d %s", code, out)
	}
	v.CriterionID, v.CriterionHash = "c3", hashOf("new criterion")
	if code, out := recordVerdict(t, db, v); code != 0 {
		t.Fatalf("verdict c3: %d %s", code, out)
	}
	if code, out := findingState(t, db, p.ItemID, "confirmed", "--require-verdicts", "--reason", "checked", "--by", "definition:findings"); code != 0 {
		t.Fatalf("judged move: %d %s", code, out)
	}
}
