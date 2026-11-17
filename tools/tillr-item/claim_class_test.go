package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

var criterionClasses = []string{"verification-only", "red-gate-triage", "direction", "owner-only"}

func classMayHarden(from, to string) bool {
	return from == to || from == "verification-only" || to == "owner-only" && (from == "direction" || from == "red-gate-triage")
}

func applyClass(t *testing.T, db, class, text string) (int, []byte) {
	t.Helper()
	p := base("item_042", "claim authority")
	p.Criteria[0].ClaimClass = class
	p.Criteria[0].Text = text
	return apply(t, db, envelope(t, "item.record", "class-item", p))
}

func TestCriterionClaimClassMoves(t *testing.T) {
	for _, from := range criterionClasses {
		for _, to := range criterionClasses {
			t.Run(from+"/"+to, func(t *testing.T) {
				db := filepath.Join(scratch(t, "class-move"), "board.sqlite3")
				if code, out := applyClass(t, db, from, frozenText); code != 0 {
					t.Fatalf("initial: %d %s", code, out)
				}
				code, out := applyClass(t, db, to, frozenText)
				want := from
				if classMayHarden(from, to) {
					if code != 0 {
						t.Fatalf("harden: %d %s", code, out)
					}
					want = to
				} else {
					assertRefused(t, code, out, "claim-class-weakened")
				}
				code, out = run(t, "show", "--store", db, "--item", "item_042")
				if code != 0 || !strings.Contains(string(out), `"claim_class":"`+want+`"`) {
					t.Fatalf("show: %d %s", code, out)
				}
			})
		}
	}
}

func TestCriterionClaimActorTable(t *testing.T) {
	for _, class := range criterionClasses {
		for _, actor := range []string{"mechanical", "agent", "human"} {
			t.Run(class+"/"+actor, func(t *testing.T) {
				db := filepath.Join(scratch(t, "claim-actor"), "board.sqlite3")
				if code, out := applyClass(t, db, class, frozenText); code != 0 {
					t.Fatalf("apply: %d %s", code, out)
				}
				if class != "owner-only" {
					grant := defaultGrant()
					grant.ClaimClasses = []string{class}
					if code, out := issueGrant(t, db, grant); code != 0 {
						t.Fatalf("grant: %d %s", code, out)
					}
				}
				v := agentVerdict()
				v.ActorKind = actor
				v.ClaimClass = class
				code, out := recordVerdict(t, db, v)
				allowed := actor == "human" || actor == "mechanical" && class == "verification-only" || actor == "agent" && class != "owner-only"
				if allowed {
					if code != 0 {
						t.Fatalf("allowed: %d %s", code, out)
					}
				} else {
					assertRefused(t, code, out, "claim-class-actor")
				}
			})
		}
	}
}

func TestCriterionClaimCannotDropAuthority(t *testing.T) {
	for _, class := range criterionClasses[1:] {
		t.Run(class, func(t *testing.T) {
			db := filepath.Join(scratch(t, "class-drop"), "board.sqlite3")
			p := base("item_042", "unjudged authority")
			p.Criteria[0].State = ""
			p.Criteria[0].ClaimClass = class
			if code, out := apply(t, db, envelope(t, "item.record", "class-item", p)); code != 0 {
				t.Fatalf("initial: %d %s", code, out)
			}
			p.Criteria = []criterion{{CriterionID: "c2", Text: "replacement criterion"}}
			code, out := apply(t, db, envelope(t, "item.record", "class-drop", p))
			assertRefused(t, code, out, "claim-class-weakened")
		})
	}
}

func TestCriterionClaimMismatchAndOwnerGrant(t *testing.T) {
	db := storedWithCriterion(t, "class-mismatch", "open")
	v := mechanicalVerdict()
	v.ClaimClass = "owner-only"
	code, out := recordVerdict(t, db, v)
	assertRefused(t, code, out, "claim-class-mismatch")
	grant := defaultGrant()
	grant.ClaimClasses = []string{"verification-only", "owner-only"}
	code, out = issueGrant(t, db, grant)
	assertRefused(t, code, out, "grant-owner-only")
}

func TestCriterionClaimOwnerProse(t *testing.T) {
	for _, text := range []string{"Owner decision recorded: yes", "OWNER DECISION RECORDED"} {
		db := filepath.Join(scratch(t, "owner-prose"), "board.sqlite3")
		code, out := applyClass(t, db, "", text)
		assertRefused(t, code, out, "owner-criterion-untyped")
		if code, out = applyClass(t, db, "owner-only", text); code != 0 {
			t.Fatalf("typed: %d %s", code, out)
		}
	}
	for _, text := range []string{"Owner decision recordedness", "The owner decision recorded"} {
		db := filepath.Join(scratch(t, "not-owner-prose"), "board.sqlite3")
		if code, out := applyClass(t, db, "", text); code != 0 {
			t.Fatalf("default: %d %s", code, out)
		}
	}
}
