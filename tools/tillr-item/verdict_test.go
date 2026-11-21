package main_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Agent intake is bound to the persisted evaluator; human and mechanical paths
// retain their existing authority. Historical grants do not license new verdicts.
// issuedAt is the injected record clock, not an authority lifetime.
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
	EvaluatedTree string   `json:"evaluated_tree,omitempty"`
	Workflow      string   `json:"workflow,omitempty"`
	EvaluatorNode string   `json:"evaluator_node,omitempty"`
	GraphSHA256   string   `json:"graph_sha256,omitempty"`
}

// agentVerdict is the delegated record every agent case starts from: lane-7 ruling on
// lane-3's work at the selected evaluator, against the frozen criterion's hash.
func agentVerdict(t *testing.T) verdictInput {
	return verdictInput{
		ItemID: "item_042", CriterionID: "c1", CriterionHash: hashOf(frozenText),
		Verdict: "done", ClaimClass: "verification-only", Reason: "the committed path exists and the check is green",
		Evidence: []string{"commit:0f1e2d", "gate-verdict-7"}, EvaluatedHead: judgeGit(t, "HEAD"), EvaluatedTree: judgeGit(t, "HEAD^{tree}"),
		Workflow: "review@v1", EvaluatorNode: "verify", GraphSHA256: hashOf(selectedGraph),
		ActorKind: "agent", Actor: "lane-7", JudgedActor: "lane-3",
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

// recordVerdict records one verdict at the case clock.
func recordVerdict(t *testing.T, db string, v verdictInput) (int, []byte) {
	t.Helper()
	return recordVerdictAt(t, db, v, issuedAt)
}

// recordVerdictAt records a verdict at an injected clock. Workflow authority has no expiry.
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

const selectedGraph = `{"name":"review","version":1,"stages":[{"id":"verify","kind":"agent","access":"read_only","judge_classes":["verification-only"]}],"exit":{"evaluator_slot":"verify","requires_nodes":["verify"]}}`

func selectEvaluator(t *testing.T, db, graph string) {
	t.Helper()
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err = raw.Exec(`CREATE TABLE IF NOT EXISTS workflow_selections(item_id TEXT PRIMARY KEY,workflow TEXT,source TEXT,graph TEXT); INSERT OR REPLACE INTO workflow_selections VALUES('item_042','review@v1','block',?)`, graph); err != nil {
		t.Fatal(err)
	}
}
func TestAgentVerdictPersistedEvaluator(t *testing.T) {
	for _, tc := range []struct {
		name, rule string
		change     func(*verdictInput)
	}{
		{"valid", "", nil},
		{"workflow", "evaluator-binding", func(v *verdictInput) { v.Workflow = "forged@v1" }},
		{"node", "evaluator-binding", func(v *verdictInput) { v.EvaluatorNode = "writer" }},
		{"digest", "evaluator-binding", func(v *verdictInput) { v.GraphSHA256 = hashOf(selectedGraph + " ") }},
		{"head", "evaluated-head", func(v *verdictInput) { v.EvaluatedHead = "short" }},
		{"tree", "evaluated-tree", func(v *verdictInput) { v.EvaluatedTree = "" }},
		{"self", "self-judging", func(v *verdictInput) { v.JudgedActor = v.Actor }},
		{"grant", "grant-retired", func(v *verdictInput) { v.GrantID = "old-grant" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := storedWithCriterion(t, "persisted-evaluator", "open")
			selectEvaluator(t, db, selectedGraph)
			v := agentVerdict(t)
			if tc.change != nil {
				tc.change(&v)
			}
			code, out := recordVerdict(t, db, v)
			if tc.rule == "" {
				if code != 0 {
					t.Fatalf("valid: %d %s", code, out)
				}
				return
			}
			assertRefused(t, code, out, tc.rule)
			if eventsOfKind(t, db, "item.verdict") != 0 || criterionOf(t, showItem(t, db, "item_042"), "c1").State != "open" {
				t.Fatal("refusal mutated item")
			}
		})
	}
}
func TestVerdictRecordRejectsNullPayloadWithEvaluatorFlags(t *testing.T) {
	db := storedWithCriterion(t, "verdict-null", "open")
	cmd := exec.Command(bin, "verdict", "record", "--store", db, "--input",
		input(t, envelope(t, "item.verdict", "verdict-rec", nil)), "--now", issuedAt, "--workflow", "review@v1")
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "verdict payload must be a JSON object") {
		t.Fatalf("null verdict payload: want usage error, got %v: %s", err, out)
	}
}

func TestAgentVerdictNeedsSelectedEvaluator(t *testing.T) {
	db := storedWithCriterion(t, "no-evaluator", "open")
	code, out := recordVerdict(t, db, agentVerdict(t))
	assertRefused(t, code, out, "evaluator-selection")
}
func TestAgentVerdictStoredStepClasses(t *testing.T) {
	db := storedWithCriterion(t, "step-classes", "open")
	if c, o := applyClass(t, db, "direction", frozenText); c != 0 {
		t.Fatalf("class: %d %s", c, o)
	}
	selectEvaluator(t, db, selectedGraph)
	v := agentVerdict(t)
	v.ClaimClass = "direction"
	c, o := recordVerdict(t, db, v)
	assertRefused(t, c, o, "evaluator-class")
	graph := strings.Replace(selectedGraph, `["verification-only"]`, `["verification-only","direction"]`, 1)
	selectEvaluator(t, db, graph)
	v.GraphSHA256 = hashOf(graph)
	if c, o = recordVerdict(t, db, v); c != 0 {
		t.Fatalf("reviewed widening: %d %s", c, o)
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

func TestVerdictRecordFlagsUsePersistedBinding(t *testing.T) {
	db := storedWithCriterion(t, "verdict-flags", "open")
	selectEvaluator(t, db, selectedGraph)
	v := agentVerdict(t)
	v.Workflow = ""
	v.EvaluatorNode = ""
	v.GraphSHA256 = ""
	v.EvaluatedHead = ""
	v.EvaluatedTree = ""
	c, o := run(t, "verdict", "record", "--store", db, "--input", input(t, envelope(t, "item.verdict", "flags", v)), "--now", issuedAt, "--workflow", "review@v1", "--evaluator-node", "verify", "--graph-sha256", hashOf(selectedGraph), "--head", judgeGit(t, "HEAD"))
	if c != 0 {
		t.Fatalf("flags: %d %s", c, o)
	}
}
func TestDirectVerdictCandidateTreeIsConfirmedAndAtomic(t *testing.T) {
	db := storedWithCriterion(t, "verdict-tree", "open")
	selectEvaluator(t, db, selectedGraph)
	v := agentVerdict(t)
	v.EvaluatedTree = strings.Repeat("c", 40)
	c, o := recordVerdict(t, db, v)
	assertRefused(t, c, o, "evaluated-tree")
	if eventsOfKind(t, db, "item.verdict") != 0 || criterionOf(t, showItem(t, db, "item_042"), "c1").State != "open" {
		t.Fatal("Git refusal mutated item")
	}
}
func TestGrantIssueCommandAndJudgeFlagAreRetired(t *testing.T) {
	for _, args := range [][]string{{"grant", "issue"}, {"judge", "--grant", "old"}} {
		c, _ := run(t, args...)
		if c != 2 {
			t.Fatalf("%v: exit %d want usage refusal", args, c)
		}
	}
}

func TestPersistedEvaluatorDefinitionFaults(t *testing.T) {
	for _, graph := range []string{
		`{}`,
		strings.Replace(selectedGraph, `"read_only"`, `"write"`, 1),
		strings.Replace(selectedGraph, `"requires_nodes":["verify"]`, `"requires_nodes":[]`, 1),
		strings.Replace(selectedGraph, `["verification-only"]`, `[]`, 1),
		strings.Replace(selectedGraph, `["verification-only"]`, `["owner-only"]`, 1),
		strings.Replace(selectedGraph, `["verification-only"]`, `["unknown"]`, 1),
		strings.Replace(selectedGraph, `["verification-only"]`, `["verification-only","verification-only"]`, 1),
	} {
		db := storedWithCriterion(t, "evaluator-definition", "open")
		selectEvaluator(t, db, graph)
		v := agentVerdict(t)
		v.GraphSHA256 = hashOf(graph)
		c, o := recordVerdict(t, db, v)
		assertRefused(t, c, o, "evaluator-selection")
		if eventsOfKind(t, db, "item.verdict") != 0 {
			t.Fatal("definition fault wrote verdict")
		}
	}
}

func TestEvaluatorAuthorityDoesNotExpire(t *testing.T) {
	db := storedWithCriterion(t, "no-expiry", "open")
	selectEvaluator(t, db, selectedGraph)
	for _, at := range []string{issuedAt, "2036-11-11T00:00:00Z"} {
		c, o := recordVerdictAt(t, db, agentVerdict(t), at)
		if c != 0 {
			t.Fatalf("at %s: %d %s", at, c, o)
		}
		payload := lifecyclePayload(t, o)
		if _, exists := payload["grant_id"]; exists {
			t.Fatal("new verdict retained a grant")
		}
		if payload["workflow"] != "review@v1" || payload["evaluator_node"] != "verify" || payload["graph_sha256"] != hashOf(selectedGraph) || payload["evaluated_tree"] != judgeGit(t, "HEAD^{tree}") {
			t.Fatalf("missing provenance: %v", payload)
		}
	}
}
