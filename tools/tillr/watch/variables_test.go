package watch

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func registerShippedWatchKinds(t *testing.T) {
	t.Helper()
	register(t,
		"drain.backend-outcome",
		"drain.backend-probe",
		"drain.base-replay-ended",
		"drain.base-replay-started",
		"drain.build-started",
		"drain.checkpoint-not-restarted",
		"drain.dispatch-declined",
		"drain.ended-work-saved",
		"drain.fast-gate-timing",
		"drain.fixer-attached",
		"drain.fixer-proved",
		"drain.heavy-fault",
		"drain.heavy-progress",
		"drain.item-fault",
		"drain.judge-unreadable",
		"drain.lane-kept",
		"drain.lane-refused",
		"drain.lane-released",
		"drain.lane-restored",
		"drain.loop-ended",
		"drain.loop-pass",
		"drain.obstacle-hold",
		"drain.pi-build-current",
		"drain.pi-build-stale",
		"drain.refusal",
		"drain.restart-answer",
		"drain.restart-request",
		"drain.revert",
		"drain.store-busy",
		"drain.trunk-core-worktree",
		"gate.started",
		"gate.verdict",
		"item.add",
		"item.conflict",
		"land.landing",
		"run.allocate",
		"run.finish",
		"run.model-timeout",
		"run.release",
	)
}

func TestShippedWatchRulesVariableDerivation(t *testing.T) {
	registerShippedWatchKinds(t)
	rules, err := LoadRules("../../../config/tillr/watch")
	if err != nil {
		t.Fatalf("LoadRules shipped: %v", err)
	}
	if len(rules) < 30 {
		t.Fatalf("expected >= 30 shipped rules, got %d", len(rules))
	}

	var globalCount, singleCount, compositeCount int
	byID := make(map[string]Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
		vars := r.VariableNames()
		switch {
		case len(r.When.GroupByList) >= 2:
			compositeCount++
			want := make([]string, len(r.When.GroupByList))
			for i, p := range r.When.GroupByList {
				want[i] = lastDotSegment(p)
			}
			if !slices.Equal(vars, want) {
				t.Errorf("rule %q composite group_by %v: got vars %v, want %v", r.ID, r.When.GroupByList, vars, want)
			}
		case len(r.When.GroupByAny) > 0:
			singleCount++
			want := []string{lastDotSegment(r.When.GroupByAny[0])}
			if !slices.Equal(vars, want) {
				t.Errorf("rule %q group_by.any %v: got vars %v, want %v", r.ID, r.When.GroupByAny, vars, want)
			}
		case r.When.GroupBy == "global" || r.When.GroupBy == "":
			globalCount++
			if len(vars) != 0 {
				t.Errorf("rule %q global: got vars %v, want empty", r.ID, vars)
			}
			if got := encodeVariablesJSON(extractVariables(r, "global", nil)); got != "{}" {
				t.Errorf("rule %q global: encoded %q, want {}", r.ID, got)
			}
		default:
			singleCount++
			want := []string{lastDotSegment(r.When.GroupBy)}
			if !slices.Equal(vars, want) {
				t.Errorf("rule %q single group_by %q: got vars %v, want %v", r.ID, r.When.GroupBy, vars, want)
			}
		}
	}
	if globalCount == 0 || singleCount == 0 || compositeCount == 0 {
		t.Fatalf("expected all rule kinds in shipped rules, got global=%d single=%d composite=%d", globalCount, singleCount, compositeCount)
	}

	for id, want := range map[string][]string{
		"drain-lane-refused":             {"lane_id", "reason"},
		"drain-repeated-step-dispatches": {"item_id", "node"},
	} {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("missing shipped rule %q", id)
		}
		if got := r.VariableNames(); !slices.Equal(got, want) {
			t.Errorf("rule %q: got vars %v, want %v", id, got, want)
		}
	}
}

func TestRuleVariablesExtractionPersistenceAndUpdate(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()

	doc := `[[rule]]
id = "var-test"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "detail.item-id"
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.variables]
item = "detail.item-id"
node = "detail.node"
[rule.guidance]
summary = "Item {{var.item}} failed at {{var.node}}"
check = ["Check {{var.item}} on {{var.node}}"]
do = ["Inspect {{var.item}} ({{var.node}})"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := LoadRuleDocument("var-test.toml", []byte(doc))
	if err != nil {
		t.Fatalf("LoadRuleDocument: %v", err)
	}
	r := rules[0]
	if got, want := r.VariableNames(), []string{"item", "node"}; !slices.Equal(got, want) {
		t.Fatalf("VariableNames = %v, want %v (alias should replace derived item-id)", got, want)
	}

	t.Run("sqlite_persists_updates_and_handles_missing_or_non_string", func(t *testing.T) {
		s := watchStore(t)
		e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		readVarsJSON := func(subject string) map[string]string {
			t.Helper()
			var raw string
			if err := s.ReadTx(ctx, func(rd *store.Reader) error {
				return rd.QueryRow(`SELECT variables_json FROM alert_episodes WHERE rule_id='var-test' AND subject_key=?`, subject).Scan(&raw)
			}); err != nil {
				t.Fatal(err)
			}
			var got map[string]string
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatalf("unmarshal variables_json %q: %v", raw, err)
			}
			return got
		}

		// Initial observation: item="it-1", node="plan".
		watchFact(t, s, now, `{"detail":{"item-id":"it-1","node":"plan"}}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if got, want := readVarsJSON("it-1"), map[string]string{"item": "it-1", "node": "plan"}; !maps.Equal(got, want) {
			t.Fatalf("initial sqlite variables_json = %v, want %v", got, want)
		}

		// Newer observation updates variables_json from newest counted event.
		now = now.Add(10 * time.Second)
		watchFact(t, s, now, `{"detail":{"item-id":"it-1","node":"build"}}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if got, want := readVarsJSON("it-1"), map[string]string{"item": "it-1", "node": "build"}; !maps.Equal(got, want) {
			t.Fatalf("updated sqlite variables_json = %v, want %v", got, want)
		}

		// Non-string node field omits node from variables_json.
		now = now.Add(10 * time.Second)
		watchFact(t, s, now, `{"detail":{"item-id":"it-1","node":42}}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if got, want := readVarsJSON("it-1"), map[string]string{"item": "it-1"}; !maps.Equal(got, want) {
			t.Fatalf("non-string field sqlite variables_json = %v, want %v", got, want)
		}

		// Missing node field on new subject omits node and expands {{var.node}} to "unknown".
		now = now.Add(10 * time.Second)
		watchFact(t, s, now, `{"detail":{"item-id":"it-2"}}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if got, want := readVarsJSON("it-2"), map[string]string{"item": "it-2"}; !maps.Equal(got, want) {
			t.Fatalf("missing field sqlite variables_json = %v, want %v", got, want)
		}
	})

	t.Run("mem_episode_persists_updates_and_handles_missing_or_non_string", func(t *testing.T) {
		src := &memoryObservationSource{}
		memRule := r
		memRule.Scope = "*"
		t0 := now
		e := Evaluator{Source: src, Rules: []Rule{memRule}, Clock: func() time.Time { return t0 }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		src.append("watch-test.fact", t0, map[string]any{"detail": map[string]any{"item-id": "mem-1", "node": "plan"}})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Alerts) != 1 || !maps.Equal(st.Alerts[0].Vars, map[string]string{"item": "mem-1", "node": "plan"}) {
			t.Fatalf("initial memEpisode vars = %+v", st.Alerts)
		}

		// Newer observation updates memEpisode.variablesJSON.
		t0 = t0.Add(10 * time.Second)
		src.append("watch-test.fact", t0, map[string]any{"detail": map[string]any{"item-id": "mem-1", "node": "verify"}})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Alerts) != 1 || !maps.Equal(st.Alerts[0].Vars, map[string]string{"item": "mem-1", "node": "verify"}) {
			t.Fatalf("updated memEpisode vars = %+v", st.Alerts)
		}

		// Non-string / missing field omits node.
		t0 = t0.Add(10 * time.Second)
		src.append("watch-test.fact", t0, map[string]any{"detail": map[string]any{"item-id": "mem-1", "node": true}})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Alerts) != 1 || !maps.Equal(st.Alerts[0].Vars, map[string]string{"item": "mem-1"}) {
			t.Fatalf("non-string field memEpisode vars = %+v", st.Alerts)
		}
	})
}

func TestRuleVariablesGuidanceExpansion(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()

	doc := `[[rule]]
id = "guidance-vars"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["item_id", "detail.stage"]
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.variables]
stage_alias = "detail.stage"
reason = "detail.reason"
[rule.guidance]
summary = "item={{var.item_id}} stage={{var.stage_alias}} reason={{var.reason}}"
check = ["check {{var.item_id}} {{var.stage_alias}} {{var.reason}}"]
do = ["do {{var.item_id}} {{var.stage_alias}} {{var.reason}}"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := LoadRuleDocument("guidance-vars.toml", []byte(doc))
	if err != nil {
		t.Fatalf("LoadRuleDocument: %v", err)
	}

	s := watchStore(t)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	// Present derived (item_id), aliased (stage_alias), and declared (reason).
	watchFact(t, s, now, `{"item_id":"it-1","detail":{"stage":"build","reason":"timeout"}}`)
	// Absent declared variable (reason missing) -> "unknown".
	watchFact(t, s, now, `{"item_id":"it-2","detail":{"stage":"verify"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 2 {
		t.Fatalf("Pending: %+v, %v", notices, err)
	}
	if got := notices[0].Bundle.Guidance; got.Summary != "item=it-1 stage=build reason=timeout" ||
		!slices.Equal(got.Check, []string{"check it-1 build timeout"}) ||
		!slices.Equal(got.Do, []string{"do it-1 build timeout"}) {
		t.Fatalf("present guidance = %+v", got)
	}
	if got := notices[1].Bundle.Guidance; got.Summary != "item=it-2 stage=verify reason=unknown" ||
		!slices.Equal(got.Check, []string{"check it-2 verify unknown"}) ||
		!slices.Equal(got.Do, []string{"do it-2 verify unknown"}) {
		t.Fatalf("fallback guidance = %+v", got)
	}
}

func TestLoadRuleDocumentVariableRefusals(t *testing.T) {
	register(t, "test.watch", "test.leader", "test.follower")

	baseRule := func(topExtra, sectionExtra string) string {
		return `[[rule]]
id = "case"
scope = "core"
severity = "warning"
` + topExtra + `
[rule.reads]
events = ["test.watch"]
[rule.when]
group_by = "detail.item_id"
pattern = "count_in_window"
threshold = 1
window = "90s"
` + sectionExtra + `
[rule.guidance]
summary = "Found {{count}}"
check = ["look"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`
	}

	lagRule := `[[rule]]
id = "lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
[rule.variables]
item = "item_id"
[rule.guidance]
summary = "Lag {{count}}"
check = ["look"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`

	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"non_table", baseRule(`variables = "bad"`, ""), "expected a table"},
		{"ident_starts_with_digit", baseRule("", "[rule.variables]\n\"1bad\" = \"x\""), "ASCII identifier"},
		{"ident_hyphen", baseRule("", "[rule.variables]\n\"bad-name\" = \"x\""), "ASCII identifier"},
		{"ident_dot", baseRule("", "[rule.variables]\n\"bad.name\" = \"x\""), "ASCII identifier"},
		{"ident_empty", baseRule("", "[rule.variables]\n\"\" = \"x\""), "ASCII identifier"},
		{"path_empty", baseRule("", "[rule.variables]\nx = \"\""), "non-empty event path"},
		{"path_leading_dot", baseRule("", "[rule.variables]\nx = \".foo\""), "non-empty event path"},
		{"path_trailing_dot", baseRule("", "[rule.variables]\nx = \"foo.\""), "non-empty event path"},
		{"path_double_dot", baseRule("", "[rule.variables]\nx = \"foo..bar\""), "non-empty event path"},
		{"path_event_prefix", baseRule("", "[rule.variables]\nx = \"event.foo\""), "non-empty event path"},
		{"path_whitespace", baseRule("", "[rule.variables]\nx = \"foo bar\""), "non-empty event path"},
		{"path_non_string", baseRule("", "[rule.variables]\nx = 123"), "non-empty event path"},
		{"forbidden_on_lag_behind", lagRule, "not allowed on lag_behind"},
		{"collision_with_derived", baseRule("", "[rule.variables]\nitem_id = \"other.path\""), "collides"},
		{"duplicate_alias", baseRule("", "[rule.variables]\na = \"detail.item_id\"\nb = \"detail.item_id\""), "duplicate alias"},
		{"composite_derived_collision", strings.Replace(baseRule("", ""), `group_by = "detail.item_id"`, `group_by = ["a.id", "b.id"]`, 1), "collides"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadRuleDocument(tc.name+".toml", []byte(tc.doc))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadRuleDocument(%s): err=%v, want substring %q", tc.name, err, tc.want)
			}
		})
	}
}
