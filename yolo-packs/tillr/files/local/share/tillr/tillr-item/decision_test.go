package main_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

type scope struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref,omitempty"`
}

type decisionInput struct {
	DecisionID string `json:"decision_id"`
	Text       string `json:"text"`
	Scope      scope  `json:"scope"`
	DecidedBy  string `json:"decided_by"`
	SessionID  string `json:"session_id"`
}

type decisionOut struct {
	Kind    string `json:"kind"`
	Payload struct {
		DecisionID string `json:"decision_id"`
		Text       string `json:"text"`
		Scope      scope  `json:"scope"`
		DecidedBy  string `json:"decided_by"`
		DecidedAt  string `json:"decided_at"`
		Status     string `json:"status"`
		Supersedes string `json:"supersedes"`
		SessionID  string `json:"session_id"`
		Rule       string `json:"rule"`
	} `json:"payload"`
}

// decisionList is the record `decision list` emits: one payload per decision, in
// declaration order, so a caller can diff two lists.
type decisionList struct {
	Kind    string `json:"kind"`
	Payload struct {
		Decisions []struct {
			DecisionID string `json:"decision_id"`
			Status     string `json:"status"`
			DecidedBy  string `json:"decided_by"`
		} `json:"decisions"`
	} `json:"payload"`
}

func decodeAs(t *testing.T, out []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(out, v); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
}

func decision(id, text string) decisionInput {
	return decisionInput{DecisionID: id, Text: text, Scope: scope{Kind: "global"}, DecidedBy: "owner", SessionID: "s-1"}
}

// A recorded decision is a typed store object with a status and a clock, and the echo
// is the stored row: the input carries neither status nor decided_at.
func TestDecisionRecordIsStoredAndEchoed(t *testing.T) {
	db := filepath.Join(scratch(t, "decision"), "board.sqlite3")
	code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", decision("dec-1", "the item face owns decisions"))),
		"--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("decision record exited %d: %s", code, out)
	}
	var got decisionOut
	decodeAs(t, out, &got)
	if got.Kind != "decision.record" {
		t.Fatalf("the face emitted kind %q", got.Kind)
	}
	if got.Payload.Status != "active" {
		t.Fatalf("a new decision is %q, want active", got.Payload.Status)
	}
	if got.Payload.DecidedAt != "2026-11-10T00:00:00Z" {
		t.Fatalf("decided_at is %q, want the injected clock", got.Payload.DecidedAt)
	}
	if got.Payload.Scope.Kind != "global" || got.Payload.DecidedBy != "owner" {
		t.Fatalf("the echo carries scope %q / decided_by %q", got.Payload.Scope.Kind, got.Payload.DecidedBy)
	}
	code, out = run(t, "decision", "list", "--store", db)
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	var list decisionList
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-1" {
		t.Fatalf("the store holds %d decisions", len(list.Payload.Decisions))
	}
}

// An unknown scope kind is malformed input: exit 2, no record, nothing written.
func TestDecisionRecordRefusesAnUnknownScopeKind(t *testing.T) {
	db := filepath.Join(scratch(t, "decision-scope"), "board.sqlite3")
	in := decision("dec-1", "a decision with no reach")
	in.Scope = scope{Kind: "everything"}
	code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", in)), "--now", "2026-11-10T00:00:00Z")
	if code != 2 {
		t.Fatalf("decision record exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
}

// A harness records its own ruling: `autoqa` is a decider this store accepts, so an
// automated QA decision does not have to attribute itself to the whale (design §2.3 of
// autoqa-tillr-seams.md). The read-back through `decision list` is the assertion that
// matters, because a value the store accepted and spelled differently on the way out
// would be the same false attribution a write-only case cannot see.
func TestDecisionRecordAcceptsAutoQA(t *testing.T) {
	db := filepath.Join(scratch(t, "decision-autoqa"), "board.sqlite3")
	in := decision("dec-1", "the harness ruled the check green")
	in.DecidedBy = "autoqa"
	code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", in)), "--now", "2026-11-10T00:00:00Z")
	if code != 0 {
		t.Fatalf("decision record exited %d: %s", code, out)
	}
	var got decisionOut
	decodeAs(t, out, &got)
	if got.Payload.DecidedBy != "autoqa" {
		t.Fatalf("the echo carries decided_by %q, want autoqa", got.Payload.DecidedBy)
	}
	code, out = run(t, "decision", "list", "--store", db)
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	var list decisionList
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecidedBy != "autoqa" {
		t.Fatalf("the stored decisions are %+v, want dec-1 by autoqa", list.Payload.Decisions)
	}
}

// The set is still closed: a decider the vocabulary does not name is malformed input
// and nothing is written. This is what keeps a near miss -- `auto-qa`, `autoQA` -- from
// being stored as a decider no reader has a name for.
func TestDecisionRecordRefusesAnUnknownDecider(t *testing.T) {
	db := filepath.Join(scratch(t, "decision-decider"), "board.sqlite3")
	in := decision("dec-1", "a decision by nobody")
	in.DecidedBy = "auto-qa"
	code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", in)), "--now", "2026-11-10T00:00:00Z")
	if code != 2 {
		t.Fatalf("decision record exited %d, want 2: %s", code, out)
	}
	if len(out) != 0 {
		t.Fatalf("a usage error emitted a record: %s", out)
	}
	code, out = run(t, "decision", "list", "--store", db)
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	var list decisionList
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 0 {
		t.Fatalf("a refused decision was stored anyway: %+v", list.Payload.Decisions)
	}
}

// A scoped decision with no ref governs nothing, so it is refused with a rule rather
// than stored as a decision that silently reaches nowhere.
func TestDecisionRecordRefusesAnEmptyScopeRef(t *testing.T) {
	db := filepath.Join(scratch(t, "decision-ref"), "board.sqlite3")
	in := decision("dec-1", "a subsystem decision with no subsystem")
	in.Scope = scope{Kind: "subsystem"}
	code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", in)), "--now", "2026-11-10T00:00:00Z")
	if code != 1 {
		t.Fatalf("decision record exited %d, want 1: %s", code, out)
	}
	var got decisionOut
	decodeAs(t, out, &got)
	if got.Kind != "item.rejection" || got.Payload.Rule != "scope-ref-required" {
		t.Fatalf("the face emitted %q / rule %q", got.Kind, got.Payload.Rule)
	}
}

// Superseding is two writes in one transaction: the old row is marked and the new row
// links it. A reader that sees one without the other cannot tell which decision binds.
func TestSupersedeMarksTheOldDecisionAndLinksTheNew(t *testing.T) {
	db := filepath.Join(scratch(t, "supersede"), "board.sqlite3")
	if code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", decision("dec-1", "the first ruling"))),
		"--now", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("recording dec-1 exited %d: %s", code, out)
	}
	code, out := run(t, "decision", "supersede", "--store", db, "--decision", "dec-1",
		"--input", input(t, envelope(t, "decision.record", "rec-2", decision("dec-2", "the second ruling"))),
		"--now", "2026-11-10T01:00:00Z")
	if code != 0 {
		t.Fatalf("supersede exited %d: %s", code, out)
	}
	var got decisionOut
	decodeAs(t, out, &got)
	if got.Payload.Supersedes != "dec-1" || got.Payload.Status != "active" {
		t.Fatalf("the successor carries supersedes %q / status %q", got.Payload.Supersedes, got.Payload.Status)
	}
	code, out = run(t, "decision", "list", "--store", db, "--status", "superseded")
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	var list decisionList
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-1" {
		t.Fatalf("the superseded set holds %d decisions", len(list.Payload.Decisions))
	}
	code, out = run(t, "decision", "list", "--store", db)
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-2" {
		t.Fatalf("the active set holds %d decisions", len(list.Payload.Decisions))
	}
}

// Revoking is terminal: a second revoke is a negative decision with a rule, not a
// silent no-op that lets a caller believe it revoked something.
func TestRevokeMarksADecisionRevokedAndRefusesASecondRevoke(t *testing.T) {
	db := filepath.Join(scratch(t, "revoke"), "board.sqlite3")
	if code, out := run(t, "decision", "record", "--store", db,
		"--input", input(t, envelope(t, "decision.record", "rec-1", decision("dec-1", "a ruling to revoke"))),
		"--now", "2026-11-10T00:00:00Z"); code != 0 {
		t.Fatalf("recording dec-1 exited %d: %s", code, out)
	}
	code, out := run(t, "decision", "revoke", "--store", db, "--decision", "dec-1", "--now", "2026-11-10T02:00:00Z")
	if code != 0 {
		t.Fatalf("revoke exited %d: %s", code, out)
	}
	var got decisionOut
	decodeAs(t, out, &got)
	if got.Payload.Status != "revoked" {
		t.Fatalf("the revoked decision is %q", got.Payload.Status)
	}
	code, out = run(t, "decision", "revoke", "--store", db, "--decision", "dec-1", "--now", "2026-11-10T03:00:00Z")
	if code != 1 {
		t.Fatalf("a second revoke exited %d, want 1: %s", code, out)
	}
	decodeAs(t, out, &got)
	if got.Payload.Rule != "decision-not-active" {
		t.Fatalf("the second revoke refused with rule %q", got.Payload.Rule)
	}
}

// The status filter is the face's whole point: the default list is the set that binds,
// and the other two are reachable on request.
func TestDecisionListFiltersByStatus(t *testing.T) {
	db := filepath.Join(scratch(t, "decision-list"), "board.sqlite3")
	for i, id := range []string{"dec-1", "dec-2"} {
		if code, out := run(t, "decision", "record", "--store", db,
			"--input", input(t, envelope(t, "decision.record", "rec-"+id, decision(id, "ruling "+id))),
			"--now", "2026-11-10T00:00:00Z"); code != 0 {
			t.Fatalf("recording %s (case %d) exited %d: %s", id, i, code, out)
		}
	}
	if code, out := run(t, "decision", "revoke", "--store", db, "--decision", "dec-2", "--now", "2026-11-10T01:00:00Z"); code != 0 {
		t.Fatalf("revoking dec-2 exited %d: %s", code, out)
	}
	code, out := run(t, "decision", "list", "--store", db, "--status", "active")
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	var list decisionList
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-1" {
		t.Fatalf("the active set holds %d decisions", len(list.Payload.Decisions))
	}
	// The default list is the set that binds, not every row: a caller that has to
	// filter to find out what governs will not filter. The revoked decision is
	// reachable only by asking for it.
	code, out = run(t, "decision", "list", "--store", db)
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-1" {
		t.Fatalf("the default list holds %d decisions, want the one active decision", len(list.Payload.Decisions))
	}
	code, out = run(t, "decision", "list", "--store", db, "--status", "revoked")
	if code != 0 {
		t.Fatalf("decision list exited %d: %s", code, out)
	}
	decodeAs(t, out, &list)
	if len(list.Payload.Decisions) != 1 || list.Payload.Decisions[0].DecisionID != "dec-2" {
		t.Fatalf("the revoked set holds %d decisions", len(list.Payload.Decisions))
	}
}
