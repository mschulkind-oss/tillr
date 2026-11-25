package watch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestDTW79GroupByAnySchemaAndLint(t *testing.T) {
	registerTestKind(t, "watch-test.dtw79-a")
	registerTestKind(t, "watch-test.dtw79-b")

	validDoc := `[[rule]]
id = "any-group"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.dtw79-a", "watch-test.dtw79-b"]
[rule.when]
group_by = { any = ["item_id", "fixer_item", "object_id"] }
pattern = "predicate_match"
after = 'event.kind == "watch-test.dtw79-a"'
reset = 'event.kind == "watch-test.dtw79-b" and event.reset == true'
match = 'event.kind == "watch-test.dtw79-b"'
window = "24h"
keep_on_terminal_item = true
[rule.guidance]
summary = "Rule fired for {{subject}}"
check = ["inspect {{subject}}"]
do = ["fix {{subject}}"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := LoadRuleDocument("valid.toml", []byte(validDoc))
	if err != nil {
		t.Fatalf("LoadRuleDocument valid: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("len(rules) = %d, want 1", len(rules))
	}
	r := rules[0]
	if r.When.GroupBy != "item_id | fixer_item | object_id" {
		t.Fatalf("GroupBy = %q, want %q", r.When.GroupBy, "item_id | fixer_item | object_id")
	}
	if len(r.When.GroupByList) != 0 {
		t.Fatalf("GroupByList = %v, want empty", r.When.GroupByList)
	}
	if !slices.Equal(r.When.GroupByAny, []string{"item_id", "fixer_item", "object_id"}) {
		t.Fatalf("GroupByAny = %v, want [item_id fixer_item object_id]", r.When.GroupByAny)
	}
	if !r.When.KeepOnTerminalItem {
		t.Fatalf("KeepOnTerminalItem = false, want true")
	}

	// Single-element group_by = { any = ["item_id"] } sets GroupBy = "item_id" and GroupByAny = ["item_id"].
	singleDoc := strings.Replace(validDoc, `["item_id", "fixer_item", "object_id"]`, `["item_id"]`, 1)
	singleRules, err := LoadRuleDocument("single.toml", []byte(singleDoc))
	if err != nil {
		t.Fatalf("LoadRuleDocument single: %v", err)
	}
	if singleRules[0].When.GroupBy != "item_id" || !slices.Equal(singleRules[0].When.GroupByAny, []string{"item_id"}) {
		t.Fatalf("single group_by.any = (%q, %v), want (%q, [item_id])", singleRules[0].When.GroupBy, singleRules[0].When.GroupByAny, "item_id")
	}

	// definitionHash and ruleEqual distinguish GroupByAny vs GroupByList and KeepOnTerminalItem.
	listRule := r
	listRule.When.GroupByAny = nil
	listRule.When.GroupByList = []string{"item_id", "fixer_item", "object_id"}
	listRule.When.GroupBy = "item_id, fixer_item, object_id"
	if ruleEqual(r, listRule) {
		t.Fatal("ruleEqual returned true for GroupByAny vs GroupByList")
	}
	if definitionHash(r) == definitionHash(listRule) {
		t.Fatal("definitionHash equal for GroupByAny vs GroupByList")
	}
	noKeep := r
	noKeep.When.KeepOnTerminalItem = false
	if ruleEqual(r, noKeep) {
		t.Fatal("ruleEqual returned true when KeepOnTerminalItem differs")
	}
	if definitionHash(r) == definitionHash(noKeep) {
		t.Fatal("definitionHash equal when KeepOnTerminalItem differs")
	}

	// Refusal cases in both LoadRuleDocument and lint.
	cases := []struct {
		name     string
		doc      string
		wantCode string
	}{
		{
			name:     "unknown-key-in-group-by-table",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = ["item_id"], extra = ["object_id"] }`, 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "missing-any-key",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ other = ["item_id"] }`, 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "empty-any-list",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = [] }`, 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "global-in-any",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = ["global", "item_id"] }`, 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "duplicate-in-any",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = ["item_id", "item_id"] }`, 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "integer-column-seq-in-any",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = ["item_id", "seq"] }`, 1),
			wantCode: CodeWatchLintForbiddenRemedy,
		},
		{
			name:     "integer-column-ts-in-any",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = ["ts", "item_id"] }`, 1),
			wantCode: CodeWatchLintForbiddenRemedy,
		},
		{
			name:     "integer-column-revision-in-any",
			doc:      strings.Replace(validDoc, `{ any = ["item_id", "fixer_item", "object_id"] }`, `{ any = ["revision"] }`, 1),
			wantCode: CodeWatchLintForbiddenRemedy,
		},
		{
			name:     "predicate-match-reset-without-after",
			doc:      strings.Replace(validDoc, "after = 'event.kind == \"watch-test.dtw79-a\"'\n", "", 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "predicate-match-after-without-match",
			doc:      strings.Replace(validDoc, "match = 'event.kind == \"watch-test.dtw79-b\"'\n", "", 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name:     "keep-on-terminal-item-non-bool",
			doc:      strings.Replace(validDoc, "keep_on_terminal_item = true", `keep_on_terminal_item = "yes"`, 1),
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name: "keep-on-terminal-item-on-lag-behind",
			doc: `[[rule]]
id = "lag-keep"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]
[rule.when]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 1
keep_on_terminal_item = true
[rule.guidance]
summary = "lag {{subject}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`,
			wantCode: CodeWatchLintRuleSchema,
		},
		{
			name: "group-by-any-on-lag-behind",
			doc: `[[rule]]
id = "lag-any"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]
[rule.when]
group_by = { any = ["item_id", "object_id"] }
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 1
[rule.guidance]
summary = "lag {{subject}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`,
			wantCode: CodeWatchLintRuleSchema,
		},
	}

	kinds := registeredRuleKinds()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadRuleDocument(tc.name+".toml", []byte(tc.doc)); err == nil {
				t.Fatalf("LoadRuleDocument(%s) succeeded, want error", tc.name)
			}
			path := filepath.Join(t.TempDir(), tc.name+".toml")
			if err := os.WriteFile(path, []byte(tc.doc), 0o644); err != nil {
				t.Fatal(err)
			}
			fc := newFindingCollector("")
			lintRuleFile(path, kinds, map[string]string{}, fc)
			if !slices.ContainsFunc(fc.findings, func(f LintFinding) bool { return f.Code == tc.wantCode }) {
				t.Fatalf("lint findings = %+v, want code %s", fc.findings, tc.wantCode)
			}
		})
	}
}

func TestDTW79OrderedPredicateMatchAndGroupByAnyEval(t *testing.T) {
	registerTestKind(t, "watch-test.dtw79-proved")
	registerTestKind(t, "watch-test.dtw79-attached")
	registerTestKind(t, "watch-test.dtw79-gate")
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	s := watchStore(t)

	doc := `[[rule]]
id = "proved-fixer-gated"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.dtw79-proved", "watch-test.dtw79-attached", "watch-test.dtw79-gate"]
[rule.when]
group_by = { any = ["item_id", "fixer_item", "object_id"] }
pattern = "predicate_match"
after = 'event.kind == "watch-test.dtw79-proved"'
reset = 'event.kind == "watch-test.dtw79-attached" and not (event.kept_candidate == true)'
match = 'event.kind == "watch-test.dtw79-gate"'
window = "24h"
keep_on_terminal_item = true
[rule.guidance]
summary = "Gate started for {{subject}} (run {{event.run_id}})"
check = ["check {{subject}}"]
do = ["fix {{subject}}"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)
	ev := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	// First pass establishes rule baseline (D-TW66).
	if _, err := ev.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendEv := func(kind, objID string, payload map[string]any, at time.Time) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var oid *string
		if objID != "" {
			id := objID
			oid = &id
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: oid, Payload: raw, At: at})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. [gate, proved] on fixer-order-ok -> must stay quiet.
	appendEv("watch-test.dtw79-gate", "run-1", map[string]any{"item_id": "fixer-order-ok", "run_id": "run-1"}, now.Add(time.Second))
	appendEv("watch-test.dtw79-proved", "fixer-order-ok", map[string]any{"fixer_item": "fixer-order-ok"}, now.Add(2*time.Second))

	// 2. [proved, attached(fixer-own), gate] on fixer-reattached -> reset disarms, stays quiet.
	appendEv("watch-test.dtw79-proved", "fixer-reattached", map[string]any{"fixer_item": "fixer-reattached"}, now.Add(3*time.Second))
	appendEv("watch-test.dtw79-attached", "fixer-reattached", map[string]any{"fixer_item": "fixer-reattached"}, now.Add(4*time.Second))
	appendEv("watch-test.dtw79-gate", "run-2", map[string]any{"item_id": "fixer-reattached", "run_id": "run-2"}, now.Add(5*time.Second))

	// 3. [proved, gate] on fixer-bad (with item_id="" on proved to verify empty-string fallthrough to fixer_item).
	appendEv("watch-test.dtw79-proved", "fixer-bad", map[string]any{"item_id": "", "fixer_item": "fixer-bad"}, now.Add(6*time.Second))
	appendEv("watch-test.dtw79-gate", "run-3", map[string]any{"item_id": "fixer-bad", "run_id": "run-3"}, now.Add(7*time.Second))

	// 4. Event with all GroupByAny candidates empty/missing/non-string -> counted as skipped.
	appendEv("watch-test.dtw79-gate", "", map[string]any{"item_id": "", "fixer_item": 42}, now.Add(8*time.Second))

	ev.Clock = func() time.Time { return now.Add(10 * time.Second) }
	res, err := ev.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("len(res) = %d, want 1", len(res))
	}
	if res[0].GroupBy != "item_id | fixer_item | object_id" {
		t.Fatalf("res[0].GroupBy = %q, want %q", res[0].GroupBy, "item_id | fixer_item | object_id")
	}
	if res[0].Skipped != 1 {
		t.Fatalf("res[0].Skipped = %d, want 1", res[0].Skipped)
	}

	st, err := ev.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Alerts) != 1 || st.Alerts[0].Subject != "fixer-bad" || st.Alerts[0].State != "open" {
		t.Fatalf("alerts = %+v, want 1 open alert for fixer-bad", st.Alerts)
	}
	notices, err := ev.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("pending notices = %+v (err=%v), want 1", notices, err)
	}
	if !strings.Contains(notices[0].Bundle.Guidance.Summary, "fixer-bad") || !strings.Contains(notices[0].Bundle.Guidance.Summary, "run-3") {
		t.Fatalf("summary = %q, want fixer-bad and run-3", notices[0].Bundle.Guidance.Summary)
	}
}

func TestDTW79KeepOnTerminalItemStoreAndSourceParity(t *testing.T) {
	registerTestKind(t, "watch-test.dtw79-term-a")
	registerTestKind(t, "watch-test.dtw79-term-b")
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	doc := `[[rule]]
id = "keep-terminal"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.dtw79-term-a", "watch-test.dtw79-term-b"]
[rule.when]
group_by = { any = ["item_id", "fixer_item"] }
pattern = "predicate_match"
after = 'event.kind == "watch-test.dtw79-term-a"'
match = 'event.kind == "watch-test.dtw79-term-b"'
window = "24h"
keep_on_terminal_item = true
[rule.guidance]
summary = "violation on {{subject}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)

	t.Run("store-backed", func(t *testing.T) {
		s := watchStore(t)
		seedWatchItemsTable(t, s)
		setWatchItemState(t, s, "fixer-settled", "resolved-elsewhere")

		ev := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
		if _, err := ev.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		for i, pair := range []struct {
			kind    string
			payload map[string]any
		}{
			{"watch-test.dtw79-term-a", map[string]any{"fixer_item": "fixer-settled"}},
			{"watch-test.dtw79-term-b", map[string]any{"item_id": "fixer-settled"}},
		} {
			raw, _ := json.Marshal(pair.payload)
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{Kind: pair.kind, ObjectType: "run", Payload: raw, At: now.Add(time.Duration(i+1) * time.Second)})
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}

		ev.Clock = func() time.Time { return now.Add(5 * time.Second) }
		if _, err := ev.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		// Second evaluation must keep the episode open (not auto-close as item-resolved-elsewhere).
		ev.Clock = func() time.Time { return now.Add(10 * time.Second) }
		if _, err := ev.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		st, err := ev.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Alerts) != 1 || st.Alerts[0].State != "open" || st.Alerts[0].Subject != "fixer-settled" {
			t.Fatalf("store alerts = %+v, want 1 open alert for fixer-settled", st.Alerts)
		}

		// Investigate and settle with an open item; re-evaluating the same window does not reopen or wake.
		setWatchItemState(t, s, "item-fix", "in-queue")
		ev.Clock = func() time.Time { return now.Add(15 * time.Second) }
		if err := ev.Investigate(ctx, st.Alerts[0].ID, "operator", "fixer resolved elsewhere", "checked item state"); err != nil {
			t.Fatal(err)
		}
		if err := ev.Settle(ctx, st.Alerts[0].ID, SettleOptions{By: "operator", Outcome: "fix-fault", ItemID: "item-fix"}); err != nil {
			t.Fatal(err)
		}
		ev.Clock = func() time.Time { return now.Add(20 * time.Second) }
		if _, err := ev.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var reopenEvs int
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened' AND object_id=?`, st.Alerts[0].ID).Scan(&reopenEvs)
		}); err != nil {
			t.Fatal(err)
		}
		if reopenEvs != 0 {
			t.Fatalf("after settle on same window: reopen events = %d, want 0", reopenEvs)
		}
	})

	t.Run("observation-source", func(t *testing.T) {
		src := &memoryObservationSource{
			projections: map[string]any{
				"charter.closed":   "closed",
				"charter.finished": []string{"landed", "resolved-elsewhere"},
				"items": map[string]any{
					"fixer-settled": map[string]any{"item.state": "resolved-elsewhere"},
				},
			},
		}
		srcRules := slices.Clone(rules)
		srcRules[0].Scope = "*"
		ev := Evaluator{Source: src, Rules: srcRules, Clock: func() time.Time { return now }}
		if _, err := ev.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		src.append("watch-test.dtw79-term-a", now.Add(time.Second), map[string]any{"fixer_item": "fixer-settled"})
		src.append("watch-test.dtw79-term-b", now.Add(2*time.Second), map[string]any{"item_id": "fixer-settled"})

		ev.Clock = func() time.Time { return now.Add(5 * time.Second) }
		res1, err := ev.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ev.Clock = func() time.Time { return now.Add(10 * time.Second) }
		res2, err := ev.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		st, err := ev.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(st.Alerts) != 1 || st.Alerts[0].State != "open" || st.Alerts[0].Subject != "fixer-settled" {
			t.Fatalf("source alerts = %+v (res1=%+v res2=%+v), want 1 open alert for fixer-settled", st.Alerts, res1, res2)
		}
	})
}
