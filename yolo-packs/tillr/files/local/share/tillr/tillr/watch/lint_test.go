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
	hookPath3 := writeLintFile(t, root3, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\",\"--run\",\"--store\",\"--root\",\"--artifact-dir\"]}}'\n")
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
	hookPath := writeLintFile(t, root3, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\",\"--run\",\"--store\",\"--root\",\"--artifact-dir\"]}}'\n")
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
	  "backends": { "pi": { "adapter": "true", "model": "m" } }
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
	hookPath := writeLintFile(t, root2, "hook.sh", "#!/bin/sh\necho '{\"hooks\":{\"complete-item\":[\"--item\",\"--run\",\"--store\",\"--root\",\"--artifact-dir\"]}}'\n")
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
	  "backends": { "pi": { "adapter": "true", "model": "m" } },
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

func TestLintProjectionOnlyPredicateAndFixerProof(t *testing.T) {
	registerLintTestKinds(t)

	// 1. Valid projection-only predicate_match with item.id and item.fixer_proved.
	cleanRoot := t.TempDir()
	writeLintFile(t, cleanRoot, "config/tillr/watch/proved.toml", `[[rule]]
id = "drain-trunk-fixer-proved-queued"
scope = "core"
severity = "warning"

[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.id matches "^fixer-main-" and item.state == "in-queue" and item.fixer_proved("refs/tillr/green")'

[rule.guidance]
summary = "Fixer {{subject}} is still queued"
check = ["Inspect {{subject}}"]
do = ["Resolve {{subject}}"]

[rule.delivery]
receiver = "foreground"
`)
	findings, err := Lint(context.Background(), LintOptions{Root: cleanRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for valid projection-only rule, got %v", findings)
	}

	// 2. Non-landing charter refuses fixer_checks and green_proof: on core and charter:<name> scopes.
	hookRoot := t.TempDir()
	writeLintFile(t, hookRoot, "definition.json", `{
  "definition": "hook-flow",
  "version": 1,
  "states": ["ready", "blocked", "closed"],
  "ready": "ready",
  "blocked": "blocked",
  "closed": "closed",
  "finished": ["closed"],
  "transitions": [
    { "from": "ready", "to": "closed" },
    { "from": "ready", "to": "blocked" },
    { "from": "blocked", "to": "ready" }
  ],
  "prerequisites": [],
  "completes": "hook",
  "retention": "keep",
  "brief_template": "brief.md",
  "hooks": { "argv": ["true"], "names": ["pre-commit"] }
}`)
	writeLintFile(t, hookRoot, "config/tillr/watch/proved.toml", `[[rule]]
id = "core-fixer"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "{{subject}}"
check = ["inspect"]
do = ["resolve"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "charter-fixer"
scope = "charter:hook-flow"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "{{subject}}"
check = ["inspect"]
do = ["resolve"]
[rule.delivery]
receiver = "foreground"
`)
	findings, err = Lint(context.Background(), LintOptions{Root: hookRoot})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `rule "core-fixer": projection "fixer_checks"`)
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `rule "core-fixer": projection "green_proof:refs/tillr/green"`)
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `rule "charter-fixer": projection "fixer_checks"`)
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `rule "charter-fixer": projection "green_proof:refs/tillr/green"`)

	// 3. Projection-only refusals: missing items, wrong group_by, event.*, window/clear_when/fingerprint, {{event.*}}.
	badRoot := t.TempDir()
	writeLintFile(t, badRoot, "config/tillr/watch/bad.toml", `[[rule]]
id = "bad-proj-only"
scope = "core"
severity = "warning"

[rule.reads]
projections = ["fixer_checks"]

[rule.when]
group_by = "object_id"
pattern = "predicate_match"
match = 'event.kind == "drain.refusal" and item.fixer_proved("refs/tillr/green")'
window = "5m"
clear_when = "true"
fingerprint = ["item_id"]

[rule.guidance]
summary = "Bad {{event.item_id}}"
check = ["inspect"]
do = ["resolve"]

[rule.delivery]
receiver = "foreground"
`)
	findings, err = Lint(context.Background(), LintOptions{Root: badRoot})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `projection-only predicate_match requires "items"`)
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, `projection-only predicate_match requires group_by = "item_id"`)
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `cannot reference event.*`)
	assertHasLintCode(t, findings, CodeWatchLintUnknownSignal, `item.fixer_proved("refs/tillr/green") requires`)
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, `must omit window`)
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, `must omit clear_when`)
	assertHasLintCode(t, findings, CodeWatchLintRuleSchema, `must omit fingerprint`)
	assertHasLintCode(t, findings, CodeWatchLintEmptyGuidance, `{{event.item_id}}`)
}

func TestLintRuleVariables(t *testing.T) {
	registerLintTestKinds(t)

	newLintRoot := func(t *testing.T, ruleTOML string) string {
		t.Helper()
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/backend-policy.json", minimalPolicyJSON)
		writeLintFile(t, root, "config/tillr/workflows/simple-build@v1.json", minimalLandingWorkflowJSON)
		writeLintFile(t, root, "config/tillr/watch/rules.toml", ruleTOML)
		return root
	}

	t.Run("valid_derived_aliased_and_declared_var_placeholders", func(t *testing.T) {
		doc := `[[rule]]
id = "vars-valid"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = ["item_id", "detail.stage"]
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
threshold = 2
window = "1h"

[rule.variables]
stage_alias = "detail.stage"
reason = "detail.reason"

[rule.guidance]
summary = "Item {{var.item_id}} at {{var.stage_alias}} ({{var.reason}})."
check = ["Check {{var.item_id}} on {{var.stage_alias}}."]
do = ["Handle {{var.reason}} for {{var.item_id}}."]

[rule.delivery]
receiver = "foreground"
`
		findings, err := Lint(context.Background(), LintOptions{Root: newLintRoot(t, doc)})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings, got %v", findings)
		}
	})

	t.Run("schema_errors_on_rule_variables", func(t *testing.T) {
		base := func(topExtra, sectionExtra, summary string) string {
			return `[[rule]]
id = "vars-bad"
scope = "core"
severity = "warning"
` + topExtra + `

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
threshold = 2
window = "1h"
` + sectionExtra + `

[rule.guidance]
summary = "` + summary + `"
check = ["Inspect {{subject}}."]
do = ["Fix {{subject}}."]

[rule.delivery]
receiver = "foreground"
`
		}

		lagWithVars := `[[rule]]
id = "lag-vars"
scope = "core"
severity = "warning"

[rule.reads]
projections = ["git_refs:refs/heads/main,refs/heads/proved"]

[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/heads/proved"
threshold_commits = 1

[rule.variables]
item = "item_id"

[rule.guidance]
summary = "Lag {{count}}."
check = ["Check lag."]
do = ["Advance proved."]

[rule.delivery]
receiver = "foreground"
`

		cases := []struct {
			name string
			doc  string
			want string
		}{
			{"non_table", base(`variables = "bad"`, "", "Summary {{subject}}."), "expected a table"},
			{"ident_digit", base("", "[rule.variables]\n\"1bad\" = \"detail.x\"", "Summary {{subject}}."), "ASCII identifier"},
			{"ident_hyphen", base("", "[rule.variables]\n\"bad-name\" = \"detail.x\"", "Summary {{subject}}."), "ASCII identifier"},
			{"ident_dot", base("", "[rule.variables]\n\"bad.name\" = \"detail.x\"", "ASCII identifier"), "ASCII identifier"},
			{"path_empty", base("", "[rule.variables]\nx = \"\"", "Summary {{subject}}."), "non-empty event path"},
			{"path_leading_dot", base("", "[rule.variables]\nx = \".foo\"", "Summary {{subject}}."), "non-empty event path"},
			{"path_trailing_dot", base("", "[rule.variables]\nx = \"foo.\"", "Summary {{subject}}."), "non-empty event path"},
			{"path_double_dot", base("", "[rule.variables]\nx = \"foo..bar\"", "Summary {{subject}}."), "non-empty event path"},
			{"path_event_prefix", base("", "[rule.variables]\nx = \"event.foo\"", "Summary {{subject}}."), "non-empty event path"},
			{"path_whitespace", base("", "[rule.variables]\nx = \"foo bar\"", "Summary {{subject}}."), "non-empty event path"},
			{"path_non_string", base("", "[rule.variables]\nx = 123", "Summary {{subject}}."), "non-empty event path"},
			{"collision", base("", "[rule.variables]\nitem_id = \"other.path\"", "Summary {{subject}}."), "collides"},
			{"lag_behind_forbidden", lagWithVars, "not allowed on lag_behind"},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				findings, err := Lint(context.Background(), LintOptions{Root: newLintRoot(t, tc.doc)})
				if err != nil {
					t.Fatal(err)
				}
				assertHasLintCode(t, findings, CodeWatchLintRuleSchema, tc.want)
			})
		}
	})

	t.Run("invalid_var_guidance_placeholders", func(t *testing.T) {
		unknownVar := `[[rule]]
id = "unknown-var"
scope = "core"
severity = "warning"
[rule.reads]
events = ["drain.refusal"]
[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
threshold = 2
window = "1h"
[rule.guidance]
summary = "Unknown {{var.missing}}."
check = ["Check {{var.item_id}}."]
do = ["Fix {{var.item_id}}."]
[rule.delivery]
receiver = "foreground"
`

		globalVar := `[[rule]]
id = "global-var"
scope = "core"
severity = "warning"
[rule.reads]
events = ["drain.refusal"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
threshold = 2
window = "1h"
[rule.guidance]
summary = "Global {{var.item_id}}."
check = ["Check global."]
do = ["Fix global."]
[rule.delivery]
receiver = "foreground"
`

		lagVar := `[[rule]]
id = "lag-var"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/heads/proved"]
[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/heads/proved"
threshold_commits = 1
[rule.guidance]
summary = "Lag {{var.anything}}."
check = ["Check lag."]
do = ["Advance proved."]
[rule.delivery]
receiver = "foreground"
`

		for _, tc := range []struct {
			name string
			doc  string
			want string
		}{
			{"unknown_var", unknownVar, "{{var.missing}}"},
			{"global_rule_var", globalVar, "{{var.item_id}}"},
			{"lag_behind_var", lagVar, "{{var.anything}}"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				findings, err := Lint(context.Background(), LintOptions{Root: newLintRoot(t, tc.doc)})
				if err != nil {
					t.Fatal(err)
				}
				assertHasLintCode(t, findings, CodeWatchLintEmptyGuidance, tc.want)
			})
		}
	})
}

func TestLintPolicyAdapterChecks(t *testing.T) {
	registerLintTestKinds(t)

	// 1. Missing adapter.
	rootMissing := t.TempDir()
	missingPolicy := writeLintFile(t, rootMissing, "policy.json", `{
	  "default_backend": "pi",
	  "seats": { "build": "pi" },
	  "backends": { "pi": { "adapter": "./does-not-exist", "model": "m" } }
	}`)
	findings, err := Lint(context.Background(), LintOptions{
		Root:        rootMissing,
		PolicyPaths: []string{missingPolicy},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "missing")

	// 2. Non-executable adapter (mode 0644).
	rootNonExec := t.TempDir()
	adapterPath := filepath.Join(rootNonExec, "non-exec-adapter")
	if err := os.WriteFile(adapterPath, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nonExecPolicy := writeLintFile(t, rootNonExec, "policy.json", `{
	  "default_backend": "pi",
	  "seats": { "build": "pi" },
	  "backends": { "pi": { "adapter": "`+filepath.ToSlash(adapterPath)+`", "model": "m" } }
	}`)
	findings, err = Lint(context.Background(), LintOptions{
		Root:        rootNonExec,
		PolicyPaths: []string{nonExecPolicy},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, findings, CodeWatchLintSeatUnmapped, "not executable")
}
