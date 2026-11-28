package policy

import "testing"

func TestEscalationAppliesOnlyToTheProfileThatRecordedIt(t *testing.T) {
	pref := &Policy{Profile: "pref", Backends: map[string]Backend{"fast": {}, "strong": {}}}
	fall := &Policy{Profile: "fall", Backends: map[string]Backend{"cheap": {}, "cheap-strong": {}}}
	list := []SeatEscalation{
		{ItemID: "i", From: "fast", To: "strong", Profile: "pref"},
		{ItemID: "i", From: "cheap", To: "cheap-strong", Profile: "fall"},
	}
	// The latest row belongs to the fallback; the preferred profile still sees its own.
	if got := EscalatedBackend(list, fall); got != "cheap-strong" {
		t.Fatalf("fallback sees %q, want cheap-strong", got)
	}
	if got := EscalatedBackend(list, pref); got != "strong" {
		t.Fatalf("preferred sees %q after switch-back, want strong", got)
	}
	if got := EscalatedBackend(list[:1], fall); got != "" {
		t.Fatalf("fallback applied the preferred profile's escalation %q; want the seat's own primary backend", got)
	}
}

func TestLegacyEscalationAppliesOnlyWhenTheActiveProfileDefinesItsTarget(t *testing.T) {
	pref := &Policy{Profile: "pref", Backends: map[string]Backend{"strong": {}}}
	fall := &Policy{Profile: "fall", Backends: map[string]Backend{"cheap": {}}}
	legacy := []SeatEscalation{{ItemID: "i", From: "fast", To: "strong"}}
	if got := EscalatedBackend(legacy, pref); got != "strong" {
		t.Fatalf("preferred = %q, want strong", got)
	}
	if got := EscalatedBackend(legacy, fall); got != "" {
		t.Fatalf("fallback = %q, want none for a backend it does not define", got)
	}
}
