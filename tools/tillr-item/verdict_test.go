package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// The verdict and grant cases: a criterion's state moves only on an item.verdict, a
// mechanical verdict needs no grant because its evidence carries it, an agent's verdict
// counts only under a grant that is present, unexpired and in scope, and no verdict from
// the actor whose work is judged is recorded at all. The rules are board-utilities.md
// §5.3's writer contract, D-UT16's retirement split and agent-runtime.md §12's bounds on
// a delegated sign-off; these cases pin the store side of them.

// issuedAt is the clock every case issues and records at, so an expiry is a fact of the
// record rather than of when the suite happened to run.
const issuedAt = "2026-11-10T00:00:00Z"

// frozenText is the criterion text the helper item freezes, which is what a verdict
// naming a criterion hash is bound to.
const frozenText = "the first criterion"

// verdictInput is board-utilities.md §5.3's item.verdict payload. A case varies one
// field and leaves the rest, so a refusal is attributable to the field the case names.
type verdictInput struct {
	ItemID        string   `json:"item_id"`
	CriterionID   string   `json:"criterion_id"`
	CriterionHash string   `json:"criterion_hash"`
	Verdict       string   `json:"verdict"`
	ClaimClass    string   `json:"claim_class"`
	Reason        string   `json:"reason"`
	Landed        []string `json:"landed,omitempty"`
	Evidence      []string `json:"evidence"`
	EvaluatedHead string   `json:"evaluated_head,omitempty"`
	ActorKind     string   `json:"actor_kind"`
	Actor         string   `json:"actor,omitempty"`
	JudgedActor   string   `json:"judged_actor,omitempty"`
	GrantID       string   `json:"grant_id,omitempty"`
}

// agentVerdict is the delegated record every agent case starts from: lane-7 ruling on
// lane-3's work, under grant_1, against the frozen criterion's hash.
func agentVerdict() verdictInput {
	return verdictInput{
		ItemID: "item_042", CriterionID: "c1", CriterionHash: hashOf(frozenText),
		Verdict: "done", ClaimClass: "verification-only", Reason: "the committed path exists and the check is green",
		Evidence: []string{"commit:0f1e2d", "gate-verdict-7"}, EvaluatedHead: "0f1e2d",
		ActorKind: "agent", Actor: "lane-7", JudgedActor: "lane-3", GrantID: "grant_1",
	}
}

// mechanicalVerdict is the composed pipeline's record: the landed attempt and the gate
// verdict that admitted it are the evidence, so no judge and no grant appears in it.
func mechanicalVerdict() verdictInput {
	return verdictInput{
		ItemID: "item_042", CriterionID: "c1", CriterionHash: hashOf(frozenText),
		Verdict: "done", ClaimClass: "verification-only", Reason: "the candidate landed on its gate verdict",
		Landed: []string{"commit:0f1e2d"}, Evidence: []string{"land-attempt-3", "gate-verdict-7"},
		EvaluatedHead: "0f1e2d", ActorKind: "mechanical",
	}
}

// grantInput is the item.grant payload: the scope as claim classes, the grantor and the
// expiry agent-runtime.md §12 settles.
type grantInput struct {
	GrantID      string   `json:"grant_id"`
	ClaimClasses []string `json:"claim_classes"`
	Grantor      string   `json:"grantor"`
	ExpiresAt    string   `json:"expires_at"`
}

// defaultGrant is the grant §12 keeps in force until the owner rules: verification-only
// claims, 24 hours from issue.
func defaultGrant() grantInput {
	return grantInput{
		GrantID: "grant_1", ClaimClasses: []string{"verification-only"},
		Grantor: "owner", ExpiresAt: "2026-11-11T00:00:00Z",
	}
}

// hashOf computes the binding a verdict names: the SHA-256 of the frozen criterion text
// in lowercase hex. The case computes it here rather than calling the package, so the
// spelling is pinned by the test and not only by the code it tests.
func hashOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// issuedVerdict is the record the verdict and grant faces emit, decoded. The fields a
// case asserts on live under `payload`, as §5.1's envelope makes them.
type issuedVerdict struct {
	Kind    string `json:"kind"`
	Payload struct {
		ItemID       string   `json:"item_id"`
		CriterionID  string   `json:"criterion_id"`
		GrantID      string   `json:"grant_id"`
		Grantor      string   `json:"grantor"`
		ClaimClasses []string `json:"claim_classes"`
		Verdict      string   `json:"verdict"`
		ActorKind    string   `json:"actor_kind"`
		RecordedAt   string   `json:"recorded_at"`
		Rule         string   `json:"rule"`
		Reason       string   `json:"reason"`
	} `json:"payload"`
}

func decodeIssued(t *testing.T, out []byte) issuedVerdict {
	t.Helper()
	var got issuedVerdict
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("the face did not emit a record: %v\n%s", err, out)
	}
	return got
}

// issueGrant records one grant at the case clock.
func issueGrant(t *testing.T, db string, grant grantInput) (int, []byte) {
	t.Helper()
	return run(t, "grant", "issue", "--store", db, "--input",
		input(t, envelope(t, "item.grant", "grant-rec", grant)), "--now", issuedAt)
}

// recordVerdict records one verdict at the case clock.
func recordVerdict(t *testing.T, db string, v verdictInput) (int, []byte) {
	t.Helper()
	return recordVerdictAt(t, db, v, issuedAt)
}

// recordVerdictAt records one verdict at the case's own clock, which is how an expiry is
// exercised: the grant does not change, the clock does.
func recordVerdictAt(t *testing.T, db string, v verdictInput, now string) (int, []byte) {
	t.Helper()
	return run(t, "verdict", "record", "--store", db, "--input",
		input(t, envelope(t, "item.verdict", "verdict-rec", v)), "--now", now)
}

// assertRefused asserts one face's answer is a negative decision under the named rule,
// with a reason a caller can read. The rule is the name the criterion's "refuses by name"
// means: a caller branches on it, so a bare nonzero exit is not the answer.
func assertRefused(t *testing.T, code int, out []byte, rule string) issuedVerdict {
	t.Helper()
	if code != 1 {
		t.Fatalf("the face exited %d, want 1: %s", code, out)
	}
	got := decodeIssued(t, out)
	if got.Kind != "item.rejection" {
		t.Fatalf("the face emitted kind %q, want the rejection record: %s", got.Kind, out)
	}
	if got.Payload.Rule != rule {
		t.Fatalf("the face refused with rule %q, want %q: %s", got.Payload.Rule, rule, got.Payload.Reason)
	}
	if got.Payload.Reason == "" {
		t.Fatal("the refusal carries no reason: a caller can read the rule but not what it means")
	}
	return got
}

// A mechanical verdict lands with no grant at all, and it is the only writer that moves
// the criterion's state: the row becomes the verdict and the event says who judged it on
// what evidence.
func TestAMechanicalVerdictLandsWithNoGrant(t *testing.T) {
	db := storedWithCriterion(t, "verdict-mechanical", "open")
	code, out := recordVerdict(t, db, mechanicalVerdict())
	if code != 0 {
		t.Fatalf("the mechanical verdict exited %d: %s", code, out)
	}
	got := decodeIssued(t, out)
	if got.Kind != "item.verdict" {
		t.Fatalf("the verdict face emitted kind %q", got.Kind)
	}
	if got.Payload.Verdict != "done" || got.Payload.ActorKind != "mechanical" {
		t.Fatalf("the verdict echoed %q / %q", got.Payload.Verdict, got.Payload.ActorKind)
	}
	if got.Payload.RecordedAt != issuedAt {
		t.Fatalf("the verdict recorded_at is %q, want the injected clock", got.Payload.RecordedAt)
	}
	stored := criterionOf(t, showItem(t, db, "item_042"), "c1")
	if stored.State != "done" {
		t.Fatalf("the store holds the criterion as %+v, want the verdict's state", stored)
	}
	if n := eventsOfKind(t, db, "item.verdict"); n != 1 {
		t.Fatalf("a landed mechanical verdict appended %d item.verdict events, want 1", n)
	}
}

// A delegated verdict under the default grant lands: the positive control every refusal
// below is a narrow exception to. Without it a blanket refusal would pass every other
// case in this file.
func TestAnAgentVerdictUnderAGrantLands(t *testing.T) {
	db := storedWithCriterion(t, "verdict-agent", "open")
	if code, out := issueGrant(t, db, defaultGrant()); code != 0 {
		t.Fatalf("issuing the grant exited %d: %s", code, out)
	}
	if n := eventsOfKind(t, db, "item.grant"); n != 1 {
		t.Fatalf("issuing a grant appended %d item.grant events, want 1", n)
	}
	code, out := recordVerdict(t, db, agentVerdict())
	if code != 0 {
		t.Fatalf("the agent verdict exited %d: %s", code, out)
	}
	got := decodeIssued(t, out)
	if got.Payload.GrantID != "grant_1" {
		t.Fatalf("the verdict echoed grant %q, want the one it was recorded under", got.Payload.GrantID)
	}
	if stored := criterionOf(t, showItem(t, db, "item_042"), "c1"); stored.State != "done" {
		t.Fatalf("the store holds the criterion as %+v, want the verdict's state", stored)
	}
}

// An agent verdict citing no grant is refused by name, and the refusal is a genuine
// refusal: the criterion keeps the state it had and nothing reaches the log.
func TestAnAgentVerdictWithNoGrantIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-no-grant", "open")
	v := agentVerdict()
	v.GrantID = ""
	code, out := recordVerdict(t, db, v)
	got := assertRefused(t, code, out, "grant-absent")
	if !strings.Contains(got.Payload.Reason, "grant") {
		t.Fatalf("the refusal does not name the grant it needs: %q", got.Payload.Reason)
	}
	if stored := criterionOf(t, showItem(t, db, "item_042"), "c1"); stored.State != "open" {
		t.Fatalf("a refused verdict left the criterion as %+v, want it unchanged", stored)
	}
}

// A grant id the store does not hold is the same absence from the verdict's side: naming
// a grant is only a grant if a reader can find it.
func TestAnAgentVerdictCitingAGrantTheStoreDoesNotHoldIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-unknown-grant", "open")
	code, out := recordVerdict(t, db, agentVerdict())
	got := assertRefused(t, code, out, "grant-absent")
	if !strings.Contains(got.Payload.Reason, "grant_1") {
		t.Fatalf("the refusal does not name the grant it could not find: %q", got.Payload.Reason)
	}
}

// An expired grant stops being a delegation: the same verdict that lands at the issue
// clock is refused at the expiry, because the evidence it cites has a shelf life the
// grant has outlived.
func TestAnAgentVerdictUnderAnExpiredGrantIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-expired", "open")
	if code, out := issueGrant(t, db, defaultGrant()); code != 0 {
		t.Fatalf("issuing the grant exited %d: %s", code, out)
	}
	code, out := recordVerdictAt(t, db, agentVerdict(), "2026-11-11T00:00:00Z")
	got := assertRefused(t, code, out, "grant-expired")
	if !strings.Contains(got.Payload.Reason, "2026-11-11T00:00:00Z") {
		t.Fatalf("the refusal does not name the expiry it crossed: %q", got.Payload.Reason)
	}
	if stored := criterionOf(t, showItem(t, db, "item_042"), "c1"); stored.State != "open" {
		t.Fatalf("a refused verdict left the criterion as %+v, want it unchanged", stored)
	}
}

// A grant's scope is the claim classes it names. A verdict claiming a class the grant
// does not cover is out of scope, which is an escalation and not a sign-off.
func TestAnAgentVerdictOutOfScopeIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-scope", "open")
	if code, out := applyClass(t, db, "red-gate-triage", frozenText); code != 0 {
		t.Fatalf("hardening the stored criterion exited %d: %s", code, out)
	}
	if code, out := issueGrant(t, db, defaultGrant()); code != 0 {
		t.Fatalf("issuing the grant exited %d: %s", code, out)
	}
	v := agentVerdict()
	v.ClaimClass = "red-gate-triage"
	code, out := recordVerdict(t, db, v)
	got := assertRefused(t, code, out, "grant-scope")
	if !strings.Contains(got.Payload.Reason, "verification-only") || !strings.Contains(got.Payload.Reason, "red-gate-triage") {
		t.Fatalf("the refusal does not name both scopes: %q", got.Payload.Reason)
	}
}

// The independence rule: the actor whose work the criterion judges cannot be the actor
// issuing the verdict, however good the grant under it is.
func TestASelfJudgingVerdictIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-self", "open")
	if code, out := issueGrant(t, db, defaultGrant()); code != 0 {
		t.Fatalf("issuing the grant exited %d: %s", code, out)
	}
	v := agentVerdict()
	v.JudgedActor = v.Actor
	code, out := recordVerdict(t, db, v)
	got := assertRefused(t, code, out, "self-judging")
	if !strings.Contains(got.Payload.Reason, "lane-7") {
		t.Fatalf("the refusal does not name the actor that judged itself: %q", got.Payload.Reason)
	}
}

// The binding: a verdict names the hash of the criterion text the store froze. A verdict
// naming any other hash is about a criterion this store does not hold, so it is refused
// rather than recorded against whatever the row says today.
func TestAVerdictNamingAnotherCriterionHashIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-hash", "open")
	v := mechanicalVerdict()
	v.CriterionHash = hashOf("the first criterion, eased")
	code, out := recordVerdict(t, db, v)
	got := assertRefused(t, code, out, "criterion-hash")
	if !strings.Contains(got.Payload.Reason, hashOf(frozenText)) {
		t.Fatalf("the refusal does not name the hash the store holds: %q", got.Payload.Reason)
	}
	if stored := criterionOf(t, showItem(t, db, "item_042"), "c1"); stored.State != "open" {
		t.Fatalf("a refused verdict left the criterion as %+v, want it unchanged", stored)
	}
}

// A verdict records the evidence it read as pointers to records (§12's three-part
// recording rule): one that names none is not auditable, so it is refused.
func TestAVerdictNamingNoEvidenceIsRefused(t *testing.T) {
	db := storedWithCriterion(t, "verdict-evidence", "open")
	v := mechanicalVerdict()
	v.Evidence = nil
	code, out := recordVerdict(t, db, v)
	got := assertRefused(t, code, out, "verdict-evidence")
	if !strings.Contains(got.Payload.Reason, "evidence") {
		t.Fatalf("the refusal does not name the evidence it wants: %q", got.Payload.Reason)
	}
}

// The unbounded grant is refused where it would be written: a grant that names every
// claim rather than a class delegates everything, which §12 rules is not a decision.
func TestAGrantNamingEveryClaimIsRefusedAsUnbounded(t *testing.T) {
	db := filepath.Join(scratch(t, "grant-unbounded"), "board.sqlite3")
	grant := defaultGrant()
	grant.ClaimClasses = []string{"all"}
	code, out := issueGrant(t, db, grant)
	got := assertRefused(t, code, out, "grant-unbounded")
	if !strings.Contains(got.Payload.Reason, "\"all\"") {
		t.Fatalf("the refusal does not name the unbounded scope it read: %q", got.Payload.Reason)
	}
	if n := eventsOfKind(t, db, "item.grant"); n != 0 {
		t.Fatalf("a refused grant appended %d item.grant events", n)
	}
}

// A person's verdict stands without a grant: the grant bounds a delegation, and the
// owner signing off is the authority the delegation was borrowed from. The record still
// carries its actor kind and its evidence, which is what makes it auditable.
func TestAHumanVerdictLandsWithNoGrant(t *testing.T) {
	db := storedWithCriterion(t, "verdict-human", "open")
	v := mechanicalVerdict()
	v.ActorKind = "human"
	v.Actor = "owner"
	v.JudgedActor = "lane-3"
	code, out := recordVerdict(t, db, v)
	if code != 0 {
		t.Fatalf("the human verdict exited %d: %s", code, out)
	}
	if got := decodeIssued(t, out); got.Payload.ActorKind != "human" || got.Payload.GrantID != "" {
		t.Fatalf("the verdict echoed %q under grant %q", got.Payload.ActorKind, got.Payload.GrantID)
	}
	if stored := criterionOf(t, showItem(t, db, "item_042"), "c1"); stored.State != "done" {
		t.Fatalf("the store holds the criterion as %+v, want the verdict's state", stored)
	}
}
