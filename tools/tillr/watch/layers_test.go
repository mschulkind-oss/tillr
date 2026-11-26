package watch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	_ = event.Register("drain.refusal", "watch-test")
}

const testShippedRuleTOML = `[[rule]]
id = "shipped-a"
scope = "core"
severity = "warning"

[rule.reads]
events = ["watch.alert.opened"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.reason == "verify-refusal"'
threshold = 5
window = "24h"

[rule.guidance]
summary = "shipped-a {{subject}}"
check = ["check shipped-a"]
do = ["do shipped-a"]

[rule.delivery]
receiver = "foreground"
`

const testShippedRuleBTOML = `[[rule]]
id = "shipped-b"
scope = "core"
severity = "info"

[rule.reads]
events = ["watch.alert.resolved"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.outcome == "fault"'
window = "1h"

[rule.guidance]
summary = "shipped-b"
check = ["check shipped-b"]
do = ["do shipped-b"]

[rule.delivery]
receiver = "foreground"
`

func TestLoadRuleLayersAndEffectiveSources(t *testing.T) {
	shippedFS := fstest.MapFS{
		"rules/shipped-a.toml": &fstest.MapFile{Data: []byte(testShippedRuleTOML)},
		"rules/shipped-b.toml": &fstest.MapFile{Data: []byte(testShippedRuleBTOML)},
	}
	repoDir := t.TempDir()
	err := os.WriteFile(filepath.Join(repoDir, "repo.toml"), []byte(`
[[override]]
id = "shipped-a"
severity = "high"

[override.when]
threshold = 9

[[rule]]
id = "shipped-b"
scope = "core"
severity = "warning"

[rule.reads]
events = ["watch.rule.set"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.outcome == "ok"'
window = "1h"

[rule.guidance]
summary = "replaced-b"
check = ["check replaced-b"]
do = ["do replaced-b"]

[rule.delivery]
receiver = "foreground"

[[rule]]
id = "repo-only"
scope = "core"
severity = "info"

[rule.reads]
events = ["watch.rule.reset"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.op == "write"'
window = "1h"

[rule.guidance]
summary = "repo-only"
check = ["check repo-only"]
do = ["do repo-only"]

[rule.delivery]
receiver = "foreground"
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	// Shipped-only first.
	shippedOnly, err := LoadRuleLayers(shippedFS, "")
	if err != nil {
		t.Fatalf("LoadRuleLayers(shippedFS, \"\"): %v", err)
	}
	if len(shippedOnly) != 2 || shippedOnly[0].Origin() != RuleSourceShipped || shippedOnly[1].Origin() != RuleSourceShipped {
		t.Fatalf("unexpected shippedOnly: %+v", shippedOnly)
	}

	// Layered with repoDir.
	layered, err := LoadRuleLayers(shippedFS, repoDir)
	if err != nil {
		t.Fatalf("LoadRuleLayers(shippedFS, repoDir): %v", err)
	}
	if len(layered) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(layered))
	}
	byID := map[string]Rule{}
	for _, r := range layered {
		byID[r.ID] = r
	}
	if got := byID["shipped-a"]; got.Origin() != RuleSourceFile || got.Severity != "high" || got.When.Threshold != 9 || got.When.Window != 24*time.Hour {
		t.Fatalf("shipped-a override mismatch: %+v", got)
	}
	if got := byID["shipped-b"]; got.Origin() != RuleSourceFile || got.Guidance.Summary != "replaced-b" {
		t.Fatalf("shipped-b replacement mismatch: %+v", got)
	}
	if got := byID["repo-only"]; got.Origin() != RuleSourceFile {
		t.Fatalf("repo-only origin mismatch: %+v", got)
	}

	// EffectiveRules with store override, Tune, and Reset fallback.
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rs := RuleStore{Store: st, FileRules: shippedOnly}

	if err := rs.Tune(ctx, "shipped-a", []string{"severity=\"high\""}, "op"); err != nil {
		t.Fatalf("Tune shipped-a: %v", err)
	}
	var eff []EffectiveRule
	if err := st.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		eff, err = EffectiveRules(rd, shippedOnly)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if eff[0].ID != "shipped-a" || eff[0].Source != RuleSourceStore || eff[1].ID != "shipped-b" || eff[1].Source != RuleSourceShipped {
		t.Fatalf("unexpected EffectiveRules after Tune: %+v", eff)
	}

	// Reset falls back to shipped.
	if err := rs.Reset(ctx, "shipped-a", "op"); err != nil {
		t.Fatalf("Reset shipped-a: %v", err)
	}
	if err := st.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		eff, err = EffectiveRules(rd, shippedOnly)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if eff[0].Source != RuleSourceShipped || eff[0].Severity != "warning" {
		t.Fatalf("expected fallback to shipped, got %+v", eff[0])
	}

	// With repoDir layered, Reset falls back to file.
	rsLayered := RuleStore{Store: st, FileRules: layered}
	if err := rsLayered.Tune(ctx, "shipped-a", []string{"when.threshold=12"}, "op"); err != nil {
		t.Fatalf("Tune layered: %v", err)
	}
	if err := rsLayered.Reset(ctx, "shipped-a", "op"); err != nil {
		t.Fatalf("Reset layered: %v", err)
	}
	if err := st.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		eff, err = EffectiveRules(rd, layered)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if eff[1].ID != "shipped-a" || eff[1].Source != RuleSourceFile || eff[1].When.Threshold != 9 {
		t.Fatalf("expected fallback to file override, got %+v", eff[1])
	}
}

func TestLoadRuleLayersRefusals(t *testing.T) {
	shippedFS := fstest.MapFS{
		"rules/shipped-a.toml": &fstest.MapFile{Data: []byte(testShippedRuleTOML)},
	}
	badShippedFS := fstest.MapFS{
		"rules/bad.toml": &fstest.MapFile{Data: []byte("[[override]]\nid = \"shipped-a\"\nenabled = false\n")},
	}
	if _, err := LoadRulesFS(badShippedFS); err == nil {
		t.Fatal("expected refusal for [[override]] in shipped FS")
	}

	cases := []struct {
		name      string
		shippedFS fstest.MapFS
		toml      string
		want      string
	}{
		{
			name:      "empty override",
			shippedFS: shippedFS,
			toml:      "[[override]]\nid = \"shipped-a\"\n",
			want:      "must specify at least one key besides id",
		},
		{
			name:      "unknown override key",
			shippedFS: shippedFS,
			toml:      "[[override]]\nid = \"shipped-a\"\nunknown = 1\n",
			want:      "unknown key",
		},
		{
			name:      "non-shipped id",
			shippedFS: shippedFS,
			toml:      "[[override]]\nid = \"missing\"\nenabled = false\n",
			want:      "no shipped rule with id",
		},
		{
			name:      "nil shippedFS",
			shippedFS: nil,
			toml:      "[[override]]\nid = \"shipped-a\"\nenabled = false\n",
			want:      "no shipped rule with id",
		},
		{
			name:      "duplicate id rule and override",
			shippedFS: shippedFS,
			toml: `
[[override]]
id = "shipped-a"
enabled = false

[[rule]]
id = "shipped-a"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch.alert.opened"]
[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.reason == "x"'
window = "1h"
[rule.guidance]
summary = "dup"
check = ["dup"]
do = ["dup"]

[rule.delivery]
receiver = "foreground"
`,
			want: "duplicate rule ID",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "bad.toml"), []byte(tc.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRuleLayers(tc.shippedFS, dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestBaselineHashStableAcrossFileToShipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shipped-a.toml"), []byte(testShippedRuleTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	fileRules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	shippedFS := fstest.MapFS{
		"rules/shipped-a.toml": &fstest.MapFile{Data: []byte(testShippedRuleTOML)},
	}
	shippedRules, err := LoadRulesFS(shippedFS)
	if err != nil {
		t.Fatal(err)
	}
	if definitionHash(fileRules[0]) != definitionHash(shippedRules[0]) {
		t.Fatalf("definitionHash changed between file (%s) and shipped (%s)", definitionHash(fileRules[0]), definitionHash(shippedRules[0]))
	}
}

func TestNonLandingCharterSkipsGitRuleWithoutRoot(t *testing.T) {
	shippedFS := fstest.MapFS{
		"rules/trunk-proof-lag.toml": &fstest.MapFile{Data: []byte(`[[rule]]
id = "trunk-proof-lag"
scope = "charter:` + definition.GitLandingName + `"
severity = "warning"

[rule.reads]
events = ["watch.alert.opened"]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]

[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
mode = "any"
threshold_commits = 1
threshold_duration = "30m"

[rule.guidance]
summary = "lag"
check = ["check lag"]
do = ["do lag"]

[rule.delivery]
receiver = "foreground"
`)},
	}
	rules, err := LoadRulesFS(shippedFS)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defJSON := []byte(`{
		"version": 1,
		"definition": "recorded-verdict",
		"ready": "queued",
		"blocked": "blocked",
		"closed": "closed",
		"completes": "hook",
		"hooks": {"argv": ["hook"], "names": ["complete-item"]},
		"states": ["queued", "blocked", "done", "closed"],
		"finished": ["done"],
		"transitions": [{"from": "queued", "to": "done"}]
	}`)
	if _, err := definition.Set(ctx, st, defJSON); err != nil {
		t.Fatal(err)
	}

	ev := &Evaluator{Store: st, Rules: rules, Root: ""}
	results, err := ev.EvaluateRules(ctx)
	if err != nil {
		t.Fatalf("EvaluateRules without --root on recorded-verdict failed: %v", err)
	}
	if len(results) != 1 || results[0].OutOfScope != "recorded-verdict" {
		t.Fatalf("expected OutOfScope=recorded-verdict, got %+v", results)
	}
	status, err := ev.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatalf("ReadStatus without --root on recorded-verdict failed: %v", err)
	}
	if len(status.Rules) != 1 || status.Rules[0].OutOfScope != "recorded-verdict" {
		t.Fatalf("expected status OutOfScope=recorded-verdict, got %+v", status.Rules)
	}
}

func TestLintMergedShippedAndRepo(t *testing.T) {
	root := t.TempDir()
	// Write minimal charter, workflow, and policy with a bounded verify->plan cycle.
	for _, dir := range []string{"config/tillr/workflows"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config/tillr/workflows/pb@v1.json"), []byte(`{
		"name": "pb",
		"version": 1,
		"stages": [
			{"id": "plan", "kind": "agent", "seat": "plan", "access": "read_only"},
			{"id": "work", "kind": "agent", "seat": "build", "access": "write"},
			{"id": "verify", "kind": "agent", "seat": "verify", "access": "read_only"}
		],
		"edges": [
			{"from_node": "plan", "to_node": "work"},
			{"from_node": "work", "to_node": "verify"}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "policy.json"), []byte(minimalPolicyJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	shippedFS := fstest.MapFS{
		"rules/verifier.toml": &fstest.MapFile{Data: []byte(`[[rule]]
id = "drain-repeated-verifier-refusals"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.reason == "verify-refusal"'
threshold = 5
window = "24h"

[rule.guidance]
summary = "refusals {{subject}}"
check = ["check refusals"]
do = ["do refusals"]

[rule.delivery]
receiver = "foreground"
`)},
		"rules/bad-shipped.toml": &fstest.MapFile{Data: []byte(`[[rule]]
id = "bad-shipped"
scope = "workflow:nonexistent"
severity = "warning"

[rule.reads]
events = ["watch.alert.opened"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'event.x == "y"'
window = "1h"

[rule.guidance]
summary = "bad"
check = ["check bad"]
do = ["do bad"]

[rule.delivery]
receiver = "foreground"
`)},
	}

	// 1. Shipped verifier rule satisfies watch-lint-unbounded-return; bad-shipped is attributed to shipped:rules/bad-shipped.toml.
	findings, err := Lint(context.Background(), LintOptions{
		Root:        root,
		PolicyPaths: []string{filepath.Join(root, "policy.json")},
		ShippedFS:   shippedFS,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawShippedAttribution bool
	for _, f := range findings {
		if f.Code == CodeWatchLintUnboundedReturn {
			t.Fatalf("unexpected unbounded-return finding when shipped rule is active: %v", f)
		}
		if f.Source == "shipped:rules/bad-shipped.toml" && f.Code == CodeWatchLintUnknownStage {
			sawShippedAttribution = true
		}
	}
	if !sawShippedAttribution {
		t.Fatalf("expected finding attributed to shipped:rules/bad-shipped.toml, got %+v", findings)
	}

	// 2. Disabling drain-repeated-verifier-refusals via [[override]] catches unbounded-return,
	// and overriding bad-shipped attributes findings to the repo file.
	watchDir := filepath.Join(root, "config/tillr/watch")
	if err := os.MkdirAll(watchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(watchDir, "local.toml"), []byte(`
[[override]]
id = "drain-repeated-verifier-refusals"
enabled = false

[[override]]
id = "bad-shipped"
severity = "high"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err = Lint(context.Background(), LintOptions{
		Root:        root,
		PolicyPaths: []string{filepath.Join(root, "policy.json")},
		ShippedFS:   shippedFS,
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawUnbounded, sawRepoAttribution bool
	for _, f := range findings {
		if f.Code == CodeWatchLintUnboundedReturn {
			sawUnbounded = true
		}
		if f.Source == "config/tillr/watch/local.toml" && f.Code == CodeWatchLintUnknownStage {
			sawRepoAttribution = true
		}
		if f.Source == "shipped:rules/bad-shipped.toml" {
			t.Fatalf("overridden shipped rule should be attributed to repo file, got %v", f)
		}
	}
	if !sawUnbounded || !sawRepoAttribution {
		t.Fatalf("expected unbounded-return and repo attribution, got %+v", findings)
	}
}
