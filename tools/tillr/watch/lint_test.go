package watch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
)

func registerLintTestKinds(t *testing.T) {
	t.Helper()
	register(t, "drain.refusal", "run.allocate", "land.landing", "drain.revert")
}

func writeLintFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func assertHasLintCode(t *testing.T, findings []LintFinding, code string, substr string) {
	t.Helper()
	for _, f := range findings {
		if f.Code == code && (substr == "" || strings.Contains(f.Detail, substr) || strings.Contains(f.String(), substr)) {
			return
		}
	}
	t.Fatalf("expected finding with code %q (substr %q), got: %v", code, substr, findings)
}

func assertNoLintCode(t *testing.T, findings []LintFinding, code string) {
	t.Helper()
	for _, f := range findings {
		if f.Code == code {
			t.Fatalf("unexpected finding with code %q: %s", code, f.String())
		}
	}
}

const minimalPolicyJSON = `{
  "about": "test policy",
  "default_backend": "pi",
  "seats": {
    "plan": "pi",
    "build": "pi",
    "verify": "pi",
    "scout": "pi"
  },
  "read_only_seats": ["scout"],
  "backends": {
    "pi": {
      "adapter": "tools/tillr-run/backends/pi/run-pi",
      "model": "test-model"
    }
  }
}`

const minimalLandingWorkflowJSON = `{
  "name": "simple-build",
  "version": 1,
  "stages": [
    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
  ],
  "edges": [],
  "exit": { "requires_nodes": ["work"], "requires_candidate": "commit_required" }
}`

const minimalRuleTOML = `[[rule]]
id = "test-refusal-bound"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
threshold = 3
window = "1h"

[rule.guidance]
summary = "Repeated refusals on {{subject}} ({{count}})."
check = ["Inspect {{subject}}."]
do = ["Split the item if needed."]

[rule.delivery]
receiver = "foreground"
`

func TestLintCleanTupleAndStoreFreeMutationFree(t *testing.T) {
	registerLintTestKinds(t)
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/backend-policy.json", minimalPolicyJSON)
	wfPath := writeLintFile(t, root, "config/tillr/workflows/simple-build@v1.json", minimalLandingWorkflowJSON)
	rulePath := writeLintFile(t, root, "config/tillr/watch/refusals.toml", minimalRuleTOML)

	wfBefore, _ := os.ReadFile(wfPath)
	ruleBefore, _ := os.ReadFile(rulePath)

	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings, got: %v", findings)
	}

	if _, err := os.Stat(filepath.Join(root, ".tillr", "state.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("expected .tillr/state.sqlite not to exist, got err=%v", err)
	}
	if wfAfter, _ := os.ReadFile(wfPath); string(wfAfter) != string(wfBefore) {
		t.Fatal("workflow file mutated by Lint")
	}
	if ruleAfter, _ := os.ReadFile(rulePath); string(ruleAfter) != string(ruleBefore) {
		t.Fatal("rule file mutated by Lint")
	}
}

func TestLintInput(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "does-not-exist")
	findings, err := Lint(context.Background(), LintOptions{Root: missingRoot})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintInput, "")

	root := t.TempDir()
	findings, err = Lint(context.Background(), LintOptions{
		Root:          root,
		CharterPath:   filepath.Join(root, "missing-charter.json"),
		WorkflowsPath: filepath.Join(root, "missing-workflows"),
		PolicyPaths:   []string{filepath.Join(root, "missing-policy.json")},
		RulesPath:     filepath.Join(root, "missing-rules"),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintInput, "missing-charter.json")
	assertHasLintCode(t, findings, CodeWatchLintInput, "missing-workflows")
	assertHasLintCode(t, findings, CodeWatchLintInput, "missing-policy.json")
	assertHasLintCode(t, findings, CodeWatchLintInput, "missing-rules")

	// Default directory path existing as a regular file.
	root2 := t.TempDir()
	writeLintFile(t, root2, "config/tillr/workflows", "not a dir")
	writeLintFile(t, root2, "config/tillr/watch", "not a dir")
	findings, err = Lint(context.Background(), LintOptions{Root: root2})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintInput, "workflows")
	assertHasLintCode(t, findings, CodeWatchLintInput, "watch")
}

func TestLintCharterSchema(t *testing.T) {
	registerLintTestKinds(t)
	root := t.TempDir()
	writeLintFile(t, root, "definition.json", `{"definition":"bad","version":1,"unknown_key":true}`)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintCharterSchema, "unknown_key")

	// Missing observability.rule_pack when rules dir exists.
	root2 := t.TempDir()
	writeLintFile(t, root2, "definition.json", `{
	  "definition": "obs",
	  "version": 1,
	  "states": ["ready"],
	  "ready": "ready",
	  "blocked": "ready",
	  "closed": "ready",
	  "completes": "landing",
	  "observability": { "progress_event": "land.landing", "rule_pack": "charters/missing.toml" }
	}`)
	writeLintFile(t, root2, "config/tillr/watch/ok.toml", minimalRuleTOML)
	findings, err = Lint(context.Background(), LintOptions{Root: root2})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintCharterSchema, "charters/missing.toml")
}

func TestLintHookContract(t *testing.T) {
	root := t.TempDir()
	// Non-executable hook script + completes=hook without complete-item or ready->finished.
	writeLintFile(t, root, "hook.sh", "#!/bin/sh\n")
	writeLintFile(t, root, "definition.json", `{
	  "definition": "hooked",
	  "version": 1,
	  "states": ["ready", "done"],
	  "ready": "ready",
	  "blocked": "ready",
	  "closed": "ready",
	  "finished": ["done"],
	  "transitions": [],
	  "completes": "hook",
	  "hooks": { "argv": ["./hook.sh"], "names": ["complete-item"] }
	}`)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintHookContract, "ready")
	assertHasLintCode(t, findings, CodeWatchLintHookContract, "not executable")

	// Executable hook returning missing flag in describe JSON.
	root2 := t.TempDir()
	hookPath := writeLintFile(t, root2, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\"]}}'\n")
	if err := os.Chmod(hookPath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLintFile(t, root2, "definition.json", `{
	  "definition": "hooked",
	  "version": 1,
	  "states": ["ready", "done"],
	  "ready": "ready",
	  "blocked": "ready",
	  "closed": "ready",
	  "finished": ["done"],
	  "transitions": [{"from":"ready","to":"done"}],
	  "completes": "hook",
	  "hooks": { "argv": ["./hook.sh"], "names": ["complete-item"] }
	}`)
	findings, err = Lint(context.Background(), LintOptions{Root: root2})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintHookContract, "--run")

	// Valid hook describe passes cleanly.
	root3 := t.TempDir()
	hookPath3 := writeLintFile(t, root3, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\",\"--run\",\"--store\",\"--root\"]}}'\n")
	if err := os.Chmod(hookPath3, 0o755); err != nil {
		t.Fatal(err)
	}
	writeLintFile(t, root3, "definition.json", `{
	  "definition": "hooked",
	  "version": 1,
	  "states": ["ready", "done"],
	  "ready": "ready",
	  "blocked": "ready",
	  "closed": "ready",
	  "finished": ["done"],
	  "transitions": [{"from":"ready","to":"done"}],
	  "completes": "hook",
	  "hooks": { "argv": ["./hook.sh"], "names": ["complete-item"] }
	}`)
	findings, err = Lint(context.Background(), LintOptions{Root: root3})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for valid hook contract, got: %v", findings)
	}
}

func TestLintWorkflowSchemaAndCycleRouting(t *testing.T) {
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root, "config/tillr/commands.json", `{"ok":{"argv":["true"],"flags":{}}}`)

	// Unknown field + bad command_ref + bad watch block.
	writeLintFile(t, root, "config/tillr/workflows/bad@v1.json", `{
	  "name": "bad",
	  "version": 1,
	  "extra": true,
	  "stages": [
	    { "id": "cmd", "kind": "command", "command_ref": "missing-cmd", "access": "read_only" },
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": [
	    { "from_node": "cmd", "to_node": "work" }
	  ],
	  "watch": {
	    "cycle_anchor": { "from": "work", "to": "work", "max_cycles": 0 }
	  }
	}`)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintWorkflowSchema, "extra")
	assertHasLintCode(t, findings, CodeWatchLintWorkflowSchema, "missing-cmd")
	assertHasLintCode(t, findings, CodeWatchLintWorkflowSchema, "cycle_anchor")

	// Directed cycle in edges must always emit watch-lint-workflow-schema, both without and with watch.cycle_anchor.
	root2 := t.TempDir()
	writeLintFile(t, root2, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root2, "config/tillr/workflows/cyclic@v1.json", `{
	  "name": "cyclic",
	  "version": 1,
	  "stages": [
	    { "id": "plan", "kind": "agent", "seat": "plan", "access": "read_only" },
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": [
	    { "from_node": "plan", "to_node": "work" },
	    { "from_node": "work", "to_node": "plan" }
	  ]
	}`)
	findings, err = Lint(context.Background(), LintOptions{Root: root2})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintWorkflowSchema, "workflow-cycle")

	root3 := t.TempDir()
	writeLintFile(t, root3, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root3, "config/tillr/workflows/cyclic-anchored@v1.json", `{
	  "name": "cyclic-anchored",
	  "version": 1,
	  "stages": [
	    { "id": "plan", "kind": "agent", "seat": "plan", "access": "read_only" },
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": [
	    { "from_node": "plan", "to_node": "work" },
	    { "from_node": "work", "to_node": "plan" }
	  ],
	  "watch": {
	    "cycle_anchor": { "from": "work", "to": "plan", "max_cycles": 2 }
	  }
	}`)
	findings, err = Lint(context.Background(), LintOptions{Root: root3})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintWorkflowSchema, "workflow-cycle")
}

func TestLintCharterCandidate(t *testing.T) {
	// Landing charter + read-only workflow with default commit_required => refused.
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root, "config/tillr/workflows/ro@v1.json", `{
	  "name": "ro",
	  "version": 1,
	  "stages": [
	    { "id": "scan", "kind": "agent", "seat": "scout", "access": "read_only" }
	  ],
	  "edges": []
	}`)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintCharterCandidate, "no write-capable stage")

	// Landing charter + pieces repeat => hasWriteStage is true => clean!
	root2 := t.TempDir()
	writeLintFile(t, root2, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root2, "config/tillr/workflows/pieces@v1.json", `{
	  "name": "pieces",
	  "version": 1,
	  "args": { "pieces": { "type": "pieces", "most": 4 } },
	  "stages": [
	    { "id": "piece", "kind": "agent", "seat": "build", "access": "read_only", "repeat": "pieces" }
	  ],
	  "edges": [],
	  "exit": { "requires_nodes": ["piece"], "requires_candidate": "commit_required" }
	}`)
	findings, err = Lint(context.Background(), LintOptions{Root: root2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for pieces workflow, got: %v", findings)
	}

	// Hook charter + commit_required => refused.
	root3 := t.TempDir()
	hookPath := writeLintFile(t, root3, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\",\"--run\",\"--store\",\"--root\"]}}'\n")
	_ = os.Chmod(hookPath, 0o755)
	writeLintFile(t, root3, "definition.json", `{
	  "definition": "hooked",
	  "version": 1,
	  "states": ["ready", "done"],
	  "ready": "ready",
	  "blocked": "ready",
	  "closed": "ready",
	  "finished": ["done"],
	  "transitions": [{"from":"ready","to":"done"}],
	  "completes": "hook",
	  "hooks": { "argv": ["./hook.sh"], "names": ["complete-item"] }
	}`)
	writeLintFile(t, root3, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root3, "config/tillr/workflows/hook-bad@v1.json", minimalLandingWorkflowJSON)
	findings, err = Lint(context.Background(), LintOptions{Root: root3})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintCharterCandidate, "commit_required")
}

func TestLintSeatUnmapped(t *testing.T) {
	// Missing default policy when workflow has agent stages.
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/workflows/simple-build@v1.json", minimalLandingWorkflowJSON)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "no backend policy loaded")

	// Policy with unmapped seat & read_only_seats write violation & bad policy schema.
	root2 := t.TempDir()
	p1 := writeLintFile(t, root2, "config/tillr/backend-policy.json", minimalPolicyJSON)
	p2 := writeLintFile(t, root2, "config/tillr/backend-policy.extra.json", `{
	  "default_backend": "pi",
	  "seats": { "build": "pi" },
	  "read_only_seats": ["build"],
	  "backends": { "pi": { "adapter": "a", "model": "m" } }
	}`)
	writeLintFile(t, root2, "config/tillr/workflows/scout-write@v1.json", `{
	  "name": "scout-write",
	  "version": 1,
	  "stages": [
	    { "id": "s1", "kind": "agent", "seat": "scout", "access": "write" },
	    { "id": "s2", "kind": "agent", "seat": "design", "access": "write" }
	  ],
	  "edges": []
	}`)
	findings, err = Lint(context.Background(), LintOptions{
		Root:        root2,
		PolicyPaths: []string{p1, p2},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "read_only_seat")
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "design")
}

func TestLintUnboundedReturn(t *testing.T) {
	registerLintTestKinds(t)
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root, "config/tillr/workflows/pb@v1.json", `{
	  "name": "pb",
	  "version": 1,
	  "stages": [
	    { "id": "plan", "kind": "agent", "seat": "plan", "access": "read_only" },
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" },
	    { "id": "verify", "kind": "agent", "seat": "verify", "access": "read_only" }
	  ],
	  "edges": [
	    { "from_node": "plan", "to_node": "work" },
	    { "from_node": "work", "to_node": "verify" }
	  ]
	}`)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintUnboundedReturn, "verify -> plan")
	assertHasLintCode(t, findings, CodeWatchLintUnboundedReturn, "verify -> work")

	// Adding a standing count_in_window per-item rule on drain.refusal bounds both.
	writeLintFile(t, root, "config/tillr/watch/refusals.toml", minimalRuleTOML)
	findings, err = Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings once bounded by rule, got: %v", findings)
	}
}

func TestLintUnknownSignalAndUnknownStage(t *testing.T) {
	registerLintTestKinds(t)
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, root, "config/tillr/workflows/simple-build@v1.json", `{
	  "name": "simple-build",
	  "version": 1,
	  "stages": [
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": [],
	  "watch": {
	    "oscillation_pairs": [{ "a": "work", "b": "nonexistent-stage" }]
	  }
	}`)
	writeLintFile(t, root, "config/tillr/watch/signals.toml", `[[rule]]
id = "bad-signal"
scope = "workflow:simple-build"
severity = "warning"

[rule.reads]
events = ["drain.typo"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal" and event.seat == "missing-seat" and event.stage_id == "missing-stage"'
threshold = 2
window = "5m"

[rule.guidance]
summary = "Bad signal {{count}}."
check = ["check"]
do = ["do"]

[rule.delivery]
receiver = "foreground"
`)
	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, "drain.typo")
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, "reads.events")
	assertHasLintCode(t, findings, CodeWatchLintUnknownStage, "nonexistent-stage")
	assertHasLintCode(t, findings, CodeWatchLintUnknownStage, "missing-seat")
	assertHasLintCode(t, findings, CodeWatchLintUnknownStage, "missing-stage")

	// git_refs: on scope=core with hook charter => watch-lint-unknown-signal.
	root2 := t.TempDir()
	hookPath := writeLintFile(t, root2, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\",\"--run\",\"--store\",\"--root\"]}}'\n")
	_ = os.Chmod(hookPath, 0o755)
	writeLintFile(t, root2, "definition.json", `{
	  "definition": "hooked",
	  "version": 1,
	  "states": ["ready", "done"],
	  "ready": "ready",
	  "blocked": "ready",
	  "closed": "ready",
	  "finished": ["done"],
	  "transitions": [{"from":"ready","to":"done"}],
	  "completes": "hook",
	  "hooks": { "argv": ["./hook.sh"], "names": ["complete-item"] }
	}`)
	writeLintFile(t, root2, "config/tillr/watch/lag.toml", `[[rule]]
id = "lag"
scope = "core"
severity = "warning"

[rule.reads]
events = ["land.landing"]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]

[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_duration = "10m"

[rule.guidance]
summary = "Lag {{elapsed}}"
check = ["check"]
do = ["do"]

[rule.delivery]
receiver = "foreground"
`)
	findings, err = Lint(context.Background(), LintOptions{Root: root2})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, "git_refs")
}

func TestLintEmptyGuidanceForbiddenRemedyAndRuleSchema(t *testing.T) {
	registerLintTestKinds(t)
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/watch/bad.toml", `[[rule]]
id = "r1"
scope = "core"
severity = "warning"
extra_rule_key = 1
remedy = "auto-fix"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "seq"
pattern = "count_in_window"
threshold = 0
window = "0s"

[rule.guidance]
summary = "{{unknown_tok}}"
check = ["unclosed {{"]
do = [""]

[rule.delivery]
receiver = "drain:build"
`)
	// Duplicate ID across files + symlink .toml.
	writeLintFile(t, root, "config/tillr/watch/dup.toml", strings.Replace(minimalRuleTOML, "test-refusal-bound", "r1", 1))
	symTarget := writeLintFile(t, root, "other.toml", minimalRuleTOML)
	if err := os.Symlink(symTarget, filepath.Join(root, "config/tillr/watch/link.toml")); err != nil {
		t.Fatal(err)
	}

	findings, err := Lint(context.Background(), LintOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, "extra_rule_key")
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, "duplicate rule ID")
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, "symlink")
	assertHasLintCode(t, findings, CodeWatchLintForbiddenRemedy, "remedies are not allowed")
	assertHasLintCode(t, findings, CodeWatchLintForbiddenRemedy, "seq")
	assertHasLintCode(t, findings, CodeWatchLintForbiddenRemedy, "threshold")
	assertHasLintCode(t, findings, CodeWatchLintForbiddenRemedy, "window")
	assertHasLintCode(t, findings, CodeWatchLintForbiddenRemedy, "drain:build")
	assertHasLintCode(t, findings, CodeWatchLintEmptyGuidance, "unknown_tok")
	assertHasLintCode(t, findings, CodeWatchLintEmptyGuidance, "unclosed")
	assertHasLintCode(t, findings, CodeWatchLintEmptyGuidance, "non-blank")

	// Verify placeholder-only strings and diagnostic command prose pass cleanly (D-TW42, D-TW60).
	cleanRoot := t.TempDir()
	writeLintFile(t, cleanRoot, "config/tillr/watch/diagnostic.toml", `[[rule]]
id = "diag-ok"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
threshold = 2
window = "10m"

[rule.guidance]
summary = "{{subject}}"
check = [
  "Query sqlite3 .tillr/state.db for TODO items",
  "Check git config core.hooksPath and git restore status"
]
do = ["Never run git reset --hard; inspect the refusal first"]

[rule.delivery]
receiver = "foreground"
`)
	cleanFindings, err := Lint(context.Background(), LintOptions{Root: cleanRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleanFindings) != 0 {
		t.Fatalf("expected 0 findings for diagnostic guidance prose, got %v", cleanFindings)
	}

	// Verify deterministic sorting by (Source, Code, Detail) and String() format.
	sorted := slices.IsSortedFunc(findings, func(a, b LintFinding) int {
		if a.Source != b.Source {
			return strings.Compare(a.Source, b.Source)
		}
		if a.Code != b.Code {
			return strings.Compare(a.Code, b.Code)
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	if !sorted {
		t.Fatalf("findings not sorted deterministically: %v", findings)
	}
	if got, want := findings[0].String(), findings[0].Source+": "+findings[0].Code+": "+findings[0].Detail; got != want {
		t.Fatalf("LintFinding.String() = %q, want %q", got, want)
	}

	// Built-in charter source label uses definition.GitLandingName without literal.
	c := loadAndLintCharter(t.TempDir(), "", newFindingCollector(t.TempDir()))
	if c.source != "builtin:"+definition.GitLandingName {
		t.Fatalf("unexpected builtin charter source: %q", c.source)
	}
}

func TestLintPolicyAndExprOperatorEdgeCases(t *testing.T) {
	registerLintTestKinds(t)

	// 1. Multiple --policy flags: first missing, second valid (missing "build" seat).
	root := t.TempDir()
	writeLintFile(t, root, "config/tillr/workflows/wf@v1.json", `{
	  "name": "wf",
	  "version": 1,
	  "stages": [
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": []
	}`)
	validPolicy := writeLintFile(t, root, "p2.json", `{
	  "default_backend": "pi",
	  "backends": { "pi": { "adapter": "pi", "model": "m" } },
	  "seats": { "scout": "pi" }
	}`)
	findings, err := Lint(context.Background(), LintOptions{
		Root:        root,
		PolicyPaths: []string{filepath.Join(root, "missing1.json"), filepath.Join(root, "missing2.json"), validPolicy},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintInput, "missing1.json")
	assertHasLintCode(t, findings, CodeWatchLintInput, "missing2.json")
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "targets seat \"build\"")
	for _, f := range findings {
		if strings.Contains(f.Detail, "no backend policy loaded") {
			t.Fatalf("unexpected no-backend-policy-loaded finding: %v", f)
		}
	}

	// 2. Schema-invalid default backend-policy.json must not emit spurious no-backend-policy-loaded.
	rootBadDefault := t.TempDir()
	writeLintFile(t, rootBadDefault, "config/tillr/workflows/wf@v1.json", `{
	  "name": "wf",
	  "version": 1,
	  "stages": [
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": []
	}`)
	writeLintFile(t, rootBadDefault, "config/tillr/backend-policy.json", `{ "bad_key": 1 }`)
	findings, err = Lint(context.Background(), LintOptions{Root: rootBadDefault})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "policy schema")
	for _, f := range findings {
		if strings.Contains(f.Detail, "no backend policy loaded") {
			t.Fatalf("unexpected no-backend-policy-loaded finding after schema error: %v", f)
		}
	}

	// 3. event.kind != / not in (D-TW59) and event.seat / event.stage* not in (D-TW60) are allowed.
	rootExpr := t.TempDir()
	writeLintFile(t, rootExpr, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, rootExpr, "config/tillr/workflows/wf@v1.json", `{
	  "name": "wf",
	  "version": 1,
	  "stages": [
	    { "id": "work", "kind": "agent", "seat": "build", "access": "write" }
	  ],
	  "edges": []
	}`)
	writeLintFile(t, rootExpr, "config/tillr/watch/ops.toml", `[[rule]]
id = "neg-ops-ok"
scope = "workflow:wf@v1"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.kind != "nonexistent.kind" and event.kind not in ["other.kind"] and event.seat not in ["missing-seat"] and event.stage not in ["missing-stage"]'
threshold = 2
window = "10m"

[rule.guidance]
summary = "Negation operators ok for {{subject}}"
check = ["Check {{rule_id}}"]
do = ["Inspect {{subject}}"]

[rule.delivery]
receiver = "foreground"
`)
	findings, err = Lint(context.Background(), LintOptions{Root: rootExpr})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for != and not in operators, got %v", findings)
	}
}
