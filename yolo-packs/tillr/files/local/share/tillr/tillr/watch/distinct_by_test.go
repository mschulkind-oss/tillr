package watch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchDistinctByLoadAndLint(t *testing.T) {
	registerLintTestKinds(t)

	const validDistinctConsecutive = `[[rule]]
id = "distinct-consecutive"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal", "land.landing"]

[rule.when]
group_by = "global"
pattern = "consecutive_count"
match = 'event.kind == "drain.refusal"'
distinct_by = "item_id"
reset = 'event.kind == "land.landing" and event.result == "landed"'
clear_when = 'event.kind == "land.landing" and event.result == "landed"'
threshold = 2
window = "24h"

[rule.guidance]
summary = "Refused {{count}} items: {{distinct_values}}"
check = ["Inspect {{distinct_values}} ({{event.step}})"]
do = ["Fix shared failure for {{distinct_values}}"]

[rule.delivery]
receiver = "foreground"
`

	const validDistinctWindow = `[[rule]]
id = "distinct-window"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
group_by = "global"
pattern = "count_in_window"
match = 'event.kind == "drain.refusal"'
distinct_by = "detail.item_id"
threshold = 3
window = "1h"

[rule.guidance]
summary = "Window distinct {{count}}: {{distinct_values}}"
check = ["Inspect {{distinct_values}}"]
do = ["Diagnose {{distinct_values}}"]

[rule.delivery]
receiver = "foreground"
`

	dir := t.TempDir()
	put(t, dir, "consecutive.toml", validDistinctConsecutive)
	put(t, dir, "window.toml", validDistinctWindow)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules valid distinct_by rules: %v", err)
	}
	if len(rules) != 2 || rules[0].When.DistinctBy != "item_id" || rules[1].When.DistinctBy != "detail.item_id" {
		t.Fatalf("unexpected DistinctBy values: %+v", rules)
	}

	cleanRoot := t.TempDir()
	writeLintFile(t, cleanRoot, "config/tillr/watch/consecutive.toml", validDistinctConsecutive)
	writeLintFile(t, cleanRoot, "config/tillr/watch/window.toml", validDistinctWindow)
	findings, err := Lint(context.Background(), LintOptions{Root: cleanRoot})
	if err != nil {
		t.Fatalf("Lint clean distinct_by rules: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 lint findings, got: %v", findings)
	}

	for _, tc := range []struct {
		name     string
		doc      string
		loadKey  string
		lintCode string
		lintSub  string
	}{
		{
			name: "predicate_match_disallows_distinct_by",
			doc: strings.Replace(
				strings.Replace(validDistinctWindow, `pattern = "count_in_window"`, `pattern = "predicate_match"`, 1),
				"threshold = 3\n", "", 1,
			),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name: "alternating_pair_disallows_distinct_by",
			doc: strings.Replace(
				validDistinctWindow,
				"pattern = \"count_in_window\"\nmatch = 'event.kind == \"drain.refusal\"'",
				"pattern = \"alternating_pair\"\nfield = \"seat\"\npairs = [[\"plan\", \"build\"]]",
				1,
			),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name: "duration_without_event_disallows_distinct_by",
			doc: strings.Replace(
				validDistinctWindow,
				"pattern = \"count_in_window\"\nmatch = 'event.kind == \"drain.refusal\"'\ndistinct_by = \"detail.item_id\"\nthreshold = 3",
				"pattern = \"duration_without_event\"\nafter = 'event.kind == \"drain.refusal\"'\ndistinct_by = \"item_id\"\nthreshold_duration = \"5m\"",
				1,
			),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name: "lag_behind_disallows_distinct_by",
			doc: `[[rule]]
id = "lag-distinct"
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
distinct_by = "item_id"
threshold_duration = "10m"
[rule.guidance]
summary = "Lag {{elapsed}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`,
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "non_string_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = 42`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "empty_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = ""`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "global_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = "global"`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "event_prefix_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = "event.item_id"`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "empty_segment_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = "detail..item_id"`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "forbidden_seq_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = "seq"`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintForbiddenRemedy,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "forbidden_ts_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = "ts"`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintForbiddenRemedy,
			lintSub:  "when.distinct_by",
		},
		{
			name:     "forbidden_revision_distinct_by",
			doc:      strings.Replace(validDistinctConsecutive, `distinct_by = "item_id"`, `distinct_by = "revision"`, 1),
			loadKey:  "when.distinct_by",
			lintCode: CodeWatchLintForbiddenRemedy,
			lintSub:  "when.distinct_by",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			p := put(t, d, "bad.toml", tc.doc)
			loaded, err := LoadRules(d)
			if loaded != nil || !hasError(err, p, ": "+tc.loadKey+":") {
				t.Fatalf("LoadRules expected refusal on %s: loaded=%+v err=%v", tc.loadKey, loaded, err)
			}
			lintRoot := t.TempDir()
			writeLintFile(t, lintRoot, "config/tillr/watch/bad.toml", tc.doc)
			fs, err := Lint(context.Background(), LintOptions{Root: lintRoot})
			if err != nil {
				t.Fatalf("Lint error: %v", err)
			}
			assertHasLintCode(t, fs, tc.lintCode, tc.lintSub)
		})
	}

	// {{distinct_values}} is refused when distinct_by is omitted.
	noDistinctRoot := t.TempDir()
	writeLintFile(t, noDistinctRoot, "config/tillr/watch/bad_guidance.toml", strings.Replace(validDistinctConsecutive, "distinct_by = \"item_id\"\n", "", 1))
	fs, err := Lint(context.Background(), LintOptions{Root: noDistinctRoot})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, fs, CodeWatchLintEmptyGuidance, "distinct_values")

	// watch-lint-unbounded-return requires r.When.DistinctBy == "" on count_in_window.
	const verifyWorkflowJSON = `{
  "name": "verified-build",
  "version": 1,
  "stages": [
    { "id": "work", "kind": "agent", "seat": "build", "access": "write" },
    { "id": "verify", "kind": "agent", "seat": "verify", "access": "read_only" }
  ],
  "edges": [
    { "from_node": "work", "to_node": "verify" }
  ],
  "exit": { "requires_nodes": ["work", "verify"], "requires_candidate": "commit_required" }
}`
	unboundedRoot := t.TempDir()
	writeLintFile(t, unboundedRoot, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, unboundedRoot, "config/tillr/workflows/verified-build@v1.json", verifyWorkflowJSON)
	writeLintFile(t, unboundedRoot, "config/tillr/watch/distinct_per_item.toml", strings.Replace(minimalRuleTOML, "threshold = 3", "distinct_by = \"item_id\"\nthreshold = 3", 1))
	fsUnbounded, err := Lint(context.Background(), LintOptions{Root: unboundedRoot})
	if err != nil {
		t.Fatal(err)
	}
	assertHasLintCode(t, fsUnbounded, CodeWatchLintUnboundedReturn, "verified-build")

	boundedRoot := t.TempDir()
	writeLintFile(t, boundedRoot, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, boundedRoot, "config/tillr/workflows/verified-build@v1.json", verifyWorkflowJSON)
	writeLintFile(t, boundedRoot, "config/tillr/watch/per_item.toml", minimalRuleTOML)
	fsBounded, err := Lint(context.Background(), LintOptions{Root: boundedRoot})
	if err != nil {
		t.Fatal(err)
	}
	assertNoLintCode(t, fsBounded, CodeWatchLintUnboundedReturn)
}

func TestWatchDistinctByEvaluatorAndDefinitionHash(t *testing.T) {
	register(t, "watch-test.refusal", "watch-test.landing")
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 9, 20, 0, 0, time.UTC)

	const ruleDoc = `[[rule]]
id = "distinct-refusal-test"
scope = "core"
severity = "warning"

[rule.reads]
events = ["watch-test.refusal", "watch-test.landing"]

[rule.when]
group_by = "global"
pattern = "consecutive_count"
match = 'event.kind == "watch-test.refusal"'
distinct_by = "item_id"
reset = 'event.kind == "watch-test.landing" and event.result == "landed"'
clear_when = 'event.kind == "watch-test.landing" and event.result == "landed"'
threshold = 2
window = "24h"

[rule.guidance]
summary = "Refused {{count}} items ({{distinct_values}}); latest {{event.step}} on {{event.item_id}}"
check = ["Inspect {{distinct_values}}"]
do = ["Diagnose {{distinct_values}}"]

[rule.delivery]
receiver = "foreground"
`

	rules := watchRules(t, ruleDoc)
	rule := rules[0]

	// Verify definitionHash preserves exact hashes for rules with DistinctBy == "" and changes when DistinctBy changes.
	plainRule := rule
	plainRule.When.DistinctBy = ""
	type legacyDigest struct {
		Scope             string      `json:"scope"`
		Events            []string    `json:"events"`
		Projections       []string    `json:"projections"`
		GroupBy           string      `json:"group_by"`
		GroupByList       []string    `json:"group_by_list"`
		Pattern           string      `json:"pattern"`
		Field             string      `json:"field"`
		Pairs             [][2]string `json:"pairs"`
		Threshold         int64       `json:"threshold"`
		ThresholdCommits  int64       `json:"threshold_commits"`
		Window            int64       `json:"window"`
		ThresholdDuration int64       `json:"threshold_duration"`
		Leader            string      `json:"leader"`
		Follower          string      `json:"follower"`
		Mode              string      `json:"mode"`
		Match             string      `json:"match"`
		Reset             string      `json:"reset"`
		ClearWhen         string      `json:"clear_when"`
		After             string      `json:"after"`
		ReopenWhen        string      `json:"reopen_when"`
	}
	legacyBytes, err := json.Marshal(legacyDigest{
		Scope:     plainRule.Scope,
		Events:    plainRule.Reads.Events,
		GroupBy:   plainRule.When.GroupBy,
		Pattern:   plainRule.When.Pattern,
		Threshold: plainRule.When.Threshold,
		Window:    int64(plainRule.When.Window),
		Match:     exprSource(plainRule.When.Match),
		Reset:     exprSource(plainRule.When.Reset),
		ClearWhen: exprSource(plainRule.When.ClearWhen),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacySum := sha256.Sum256(legacyBytes)
	if got, want := definitionHash(plainRule), hex.EncodeToString(legacySum[:]); got != want {
		t.Fatalf("definitionHash with empty DistinctBy changed existing digest: got %s, want %s", got, want)
	}
	if definitionHash(rule) == definitionHash(plainRule) {
		t.Fatal("definitionHash did not change when DistinctBy was set")
	}
	if ruleEqual(rule, plainRule) {
		t.Fatal("ruleEqual must distinguish DistinctBy")
	}

	s := watchStore(t)
	e := Evaluator{Store: s, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendKind := func(kind, payload string) int64 {
		t.Helper()
		var seq int64
		if err := s.WriteTx(ctx, func(tx *store.Tx) (err error) {
			seq, err = tx.Append(event.Event{Kind: kind, At: now, Payload: []byte(payload)})
			return
		}); err != nil {
			t.Fatal(err)
		}
		return seq
	}

	// 1. Two refusals on the same item + missing/empty/non-string distinct_by values do not fire, reset, or increment skipped.
	appendKind("watch-test.refusal", `{"item_id":"item-a","step":"gate"}`)
	now = now.Add(time.Minute)
	appendKind("watch-test.refusal", `{"item_id":"item-a","step":"gate"}`)
	appendKind("watch-test.refusal", `{"step":"gate"}`)
	appendKind("watch-test.refusal", `{"item_id":"","step":"gate"}`)
	appendKind("watch-test.refusal", `{"item_id":99,"step":"gate"}`)
	appendKind("watch-test.landing", `{"result":"squash-conflict","item_id":"item-z"}`)
	res, err := e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Skipped != 0 {
		t.Fatalf("unexpected results after same-item/malformed events: %+v", res)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("expected 0 notices, got %+v (err=%v)", notices, err)
	}

	// 2. Second distinct item opens 1 global alert with ordered distinct_values.
	now = now.Add(time.Minute)
	appendKind("watch-test.refusal", `{"item_id":"item-b","step":"verify-not-done"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("expected 1 notice, got %+v (err=%v)", notices, err)
	}
	alertID := notices[0].AlertID
	if got, want := notices[0].Bundle.Guidance.Summary, "Refused 2 items (item-a, item-b); latest verify-not-done on item-b"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	if len(notices[0].Bundle.Events) != 2 || notices[0].Bundle.Events[0].Value != nil || notices[0].Bundle.Events[1].Value != nil {
		t.Fatalf("unexpected bundle events: %+v", notices[0].Bundle.Events)
	}

	// 3. Third distinct item and repeat of item-a move item-a to newest seq tail and update episode in place.
	now = now.Add(time.Minute)
	appendKind("watch-test.refusal", `{"item_id":"item-c","step":"gate"}`)
	now = now.Add(time.Minute)
	seqA2 := appendKind("watch-test.refusal", `{"item_id":"item-a","step":"fence"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var obsCount int64
	var lastSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT observation_count, last_observed_seq FROM alert_episodes WHERE alert_id=?`, alertID).Scan(&obsCount, &lastSeq)
	}); err != nil {
		t.Fatal(err)
	}
	if obsCount < 2 || lastSeq != seqA2 {
		t.Fatalf("episode not updated in place: observation_count=%d last_observed_seq=%d (want %d)", obsCount, lastSeq, seqA2)
	}
	// Silence and expire silence so reopen re-renders bundle with updated distinct_values order ("item-b, item-c, item-a").
	until := "1m"
	if _, err := e.Silence(ctx, SilenceOptions{
		AlertID:  alertID,
		By:       "operator",
		ItemID:   "item-silence",
		Reason:   "check order",
		Until:    &until,
		Cause:    "known ordering",
		Evidence: "bundle check",
	}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := e.Pending(ctx, "foreground")
	if err != nil || len(reopened) != 1 {
		t.Fatalf("expected reopened notice, got %+v (err=%v)", reopened, err)
	}
	if got, want := reopened[0].Bundle.Guidance.Summary, "Refused 3 items (item-b, item-c, item-a); latest fence on item-a"; got != want {
		t.Fatalf("reopened summary = %q, want %q", got, want)
	}

	// 4. Landing event with no item_id field still resets and clears the global episode.
	now = now.Add(time.Minute)
	appendKind("watch-test.landing", `{"result":"landed"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("expected landing without item_id to clear alert, got %+v (err=%v)", notices, err)
	}

	// 5. count_in_window survival in window is anchored at each distinct item's newest observation
	// across both store-backed cached evaluation and in-memory ObservationSource evaluation.
	winRule := watchRules(t, `[[rule]]
id = "distinct-win-anchor"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.refusal"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
distinct_by = "item_id"
threshold = 2
window = "10m"
[rule.guidance]
summary = "{{count}}: {{distinct_values}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`)[0]

	t0 := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	cur := t0
	sWin := watchStore(t)
	storeWinEval := Evaluator{Store: sWin, Rules: []Rule{winRule}, Clock: func() time.Time { return cur }}
	if _, err := storeWinEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	appendWinStore := func(at time.Time, payload string) {
		t.Helper()
		if err := sWin.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: "watch-test.refusal", At: at, Payload: []byte(payload)})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendWinStore(t0, `{"item_id":"item-a"}`)
	cur = t0.Add(8 * time.Minute)
	appendWinStore(cur, `{"item_id":"item-a"}`)
	if _, err := storeWinEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, err := storeWinEval.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
		t.Fatalf("same item in window fired: %+v (%v)", pending, err)
	}
	// At t0+12m, t0 is outside the 10m window, while item-a's t0+8m repeat is still inside.
	cur = t0.Add(12 * time.Minute)
	appendWinStore(cur, `{"item_id":"item-b"}`)
	if _, err := storeWinEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := storeWinEval.Pending(ctx, "foreground")
	if err != nil || len(pending) != 1 || pending[0].Bundle.Guidance.Summary != "2: item-a, item-b" {
		t.Fatalf("expected item-a@8m + item-b@12m to fire on store, got %+v (%v)", pending, err)
	}

	src := &memoryObservationSource{}
	srcWinRule := winRule
	srcWinRule.Scope = "*"
	cur = t0
	srcEval := Evaluator{Source: src, Rules: []Rule{srcWinRule}, Clock: func() time.Time { return cur }}
	if _, err := srcEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	src.append("watch-test.refusal", t0, map[string]any{"item_id": "item-a"})
	cur = t0.Add(8 * time.Minute)
	src.append("watch-test.refusal", cur, map[string]any{"item_id": "item-a"})
	if _, err := srcEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	cur = t0.Add(12 * time.Minute)
	src.append("watch-test.refusal", cur, map[string]any{"item_id": "item-b"})
	if _, err := srcEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	pendingSrc, err := srcEval.Pending(ctx, "foreground")
	if err != nil || len(pendingSrc) != 1 || pendingSrc[0].Bundle.Guidance.Summary != "2: item-a, item-b" {
		t.Fatalf("expected item-a@8m + item-b@12m to fire on source, got %+v (%v)", pendingSrc, err)
	}
}
