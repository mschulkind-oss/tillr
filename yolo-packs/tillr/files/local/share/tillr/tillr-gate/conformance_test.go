package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

// The conformance suite grows one case per landed face (lanes §4). This is the plan
// face's, and it asserts the two things a second consumer of this module would break by
// linking it rather than by running the binary: that a registry whose digest does not
// match its declarations is refused, and that a plan is a pure function of the registry
// and the change -- the same inputs, the same checks, in the same order.
//
// The negative control is a `Plan` that ranges over a map instead of the registry's
// declaration slice: Go randomises map iteration, so the two plans below carry the same
// checks in different orders and the case is red.
func TestThePlanFaceIsConformant(t *testing.T) {
	refusesARegistryWhoseDigestDoesNotMatch(t)
	plansTheSameChangeTheSameWay(t)
}

// refusesARegistryWhoseDigestDoesNotMatch is the loader's half: a digest is what a verdict
// binds, so a file edited without regenerating it must refuse rather than load.
func refusesARegistryWhoseDigestDoesNotMatch(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("reading the fixture registry failed: %v", err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("decoding the fixture registry failed: %v", err)
	}
	file["digest"] = json.RawMessage(`"0000000000000000000000000000000000000000000000000000000000000000"`)
	edited, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(path); err == nil {
		t.Fatal("a registry whose digest does not match its declarations loaded")
	}
}

// plansTheSameChangeTheSameWay is the planner's half: a plan is a function of the change
// and the registry and nothing else, so two plans of one change are one plan.
func plansTheSameChangeTheSameWay(t *testing.T) {
	t.Helper()
	registry, err := LoadRegistry(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatalf("loading the fixture registry failed: %v", err)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	change := Change{
		BaseSHA:      "c0b4c69197",
		TreeSHA:      "c0b4c69197",
		ChangedPaths: []string{"tools/first/main.go", "AGENTS.md"},
	}
	first, err := Plan(registry, change, root)
	if err != nil {
		t.Fatalf("planning the change failed: %v", err)
	}
	firstBody, err := encodePlan(first)
	if err != nil {
		t.Fatal(err)
	}
	// Twenty calls, not two: a plan that ranged a map would agree with itself whenever the
	// iteration happened to repeat, and with three checks that is one time in six. Twenty
	// agreeing calls is a pure function; two is a coin toss.
	for attempt := 0; attempt < 20; attempt++ {
		again, err := Plan(registry, change, root)
		if err != nil {
			t.Fatalf("planning the change again failed: %v", err)
		}
		body, err := encodePlan(again)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstBody, body) {
			t.Fatalf("two plans of one change differ:\n%s\n%s", firstBody, body)
		}
	}
	// Two calls agreeing is not enough on its own: a plan that ranged a map would agree
	// with itself whenever the iteration happened to repeat. The order is the registry's
	// declaration order, which is what a runner serializing checks by their reads and
	// writes depends on.
	var payload PlanPayload
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatalf("decoding the plan payload failed: %v", err)
	}
	got := make([]string, 0, len(payload.Checks))
	planned := map[string]bool{}
	for _, check := range payload.Checks {
		got = append(got, check.CheckID)
		planned[check.CheckID] = true
	}
	var want []string
	for _, decl := range registry.Declarations {
		if planned[decl.ID] {
			want = append(want, decl.ID)
		}
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("the plan's checks are %v, not the registry's declaration order %v", got, want)
	}
}

// The run face's conformance case, in the same shape as the plan face's: the two things
// a second consumer of this module would break by linking it rather than by running the
// binary. A consumer that read a verdict as "the candidate passed" must be able to trust
// that a plan owing an in-process check never produced one, and that the verdict names
// the plan and the tree it judged.
//
// The negative controls are named on each half.
func TestTheRunFaceIsConformant(t *testing.T) {
	neverPassesAPlanThatOwesAnInProcessCheck(t)
	bindsThePlanAndTheTree(t)
}

// planEnvelope builds a plan record for the run face to consume.
func planEnvelope(t *testing.T, payload PlanPayload) record.Envelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encoding the plan payload failed: %v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          PlanKind,
		ID:            payload.PlanID,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: Version},
		Payload:       raw,
	}
}

// verdictOf runs one plan and decodes the verdict it produced.
func verdictOf(t *testing.T, plan PlanPayload) (VerdictPayload, error) {
	t.Helper()
	worktree := t.TempDir()
	envelope, err := Run(planEnvelope(t, plan), worktree, filepath.Join(t.TempDir(), "logs"))
	if envelope.Kind == "" {
		return VerdictPayload{}, err
	}
	var verdict VerdictPayload
	if decodeErr := json.Unmarshal(envelope.Payload, &verdict); decodeErr != nil {
		t.Fatalf("decoding the verdict payload failed: %v", decodeErr)
	}
	return verdict, err
}

// neverPassesAPlanThatOwesAnInProcessCheck is A-INPROCESS as a property of the package:
// the gate's own-code checks have no command, so a verdict that reported `pass` while
// owing one would assert a transition that did not happen.
//
// Negative control: the same plan with `InProcess` empty is `pass`, so a Run that
// answered `error` for every plan fails the second half.
func neverPassesAPlanThatOwesAnInProcessCheck(t *testing.T) {
	t.Helper()
	plan := PlanPayload{
		PlanID: "p-1", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "digest",
		Checks: []PlanCheck{{CheckID: "ok", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000}},
	}
	owed := plan
	owed.InProcess = []PlanInProcess{{CheckID: "file-size", Kind: "file-size", Label: "no file over the ceiling"}}
	verdict, err := verdictOf(t, owed)
	if err == nil {
		t.Fatal("a plan owing an in-process check produced no error")
	}
	if code := exitCode(err); code != record.ExitExternal {
		t.Fatalf("a plan owing an in-process check exited %d, want %d", code, record.ExitExternal)
	}
	if verdict.Result != ResultError {
		t.Fatalf("a plan owing an in-process check produced verdict %q", verdict.Result)
	}
	clean, err := verdictOf(t, plan)
	if err != nil {
		t.Fatalf("a plan owing no in-process check failed: %v", err)
	}
	if clean.Result != ResultPass {
		t.Fatalf("a plan owing no in-process check produced verdict %q", clean.Result)
	}
}

// bindsThePlanAndTheTree is the binding a consumer refuses a mismatch on: the verdict
// names the plan it ran, the registry that produced it, and the tree it examined, and its
// identity is a function of the plan and the tree.
//
// Negative control: two plans differing only in `TreeSHA` must produce two verdicts with
// different `tree_sha` and different `verdict_id`, so a Run that dropped the tree or
// derived the id from the plan alone fails here.
func bindsThePlanAndTheTree(t *testing.T) {
	t.Helper()
	check := []PlanCheck{{CheckID: "ok", Command: []string{"sh", "-c", "exit 0"}, TimeoutMS: 5000}}
	first, err := verdictOf(t, PlanPayload{
		PlanID: "p-2", BaseSHA: "base", TreeSHA: "tree-a", RegistryDigest: "digest", Checks: check,
	})
	if err != nil {
		t.Fatalf("running the first plan failed: %v", err)
	}
	if first.PlanID != "p-2" || first.RegistryDigest != "digest" || first.BaseSHA != "base" || first.TreeSHA != "tree-a" {
		t.Fatalf("the verdict binds %+v", first)
	}
	second, err := verdictOf(t, PlanPayload{
		PlanID: "p-2", BaseSHA: "base", TreeSHA: "tree-b", RegistryDigest: "digest", Checks: check,
	})
	if err != nil {
		t.Fatalf("running the second plan failed: %v", err)
	}
	if second.TreeSHA != "tree-b" {
		t.Fatalf("the second verdict binds tree %q", second.TreeSHA)
	}
	if first.VerdictID == second.VerdictID {
		t.Fatal("two plans for two trees produced one verdict_id")
	}
}
