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

func TestWatchBroadcastEventsLoadAndLint(t *testing.T) {
	registerLintTestKinds(t)

	const validBroadcastConsecutive = `[[rule]]
id = "broadcast-consecutive"
scope = "core"
severity = "high"

[rule.reads]
events = ["drain.refusal", "land.landing"]

[rule.when]
for_each = "refusal_causes"
group_by = "cause"
broadcast_events = ["land.landing"]
pattern = "consecutive_count"
match = 'event.kind == "drain.refusal"'
distinct_by = "item_id"
reset = 'event.kind == "land.landing" and event.result == "landed"'
clear_when = 'event.kind == "land.landing" and event.result == "landed"'
threshold = 2
window = "24h"

[rule.guidance]
summary = "Shared cause {{subject}} across {{count}} items ({{distinct_values}})."
check = ["Inspect {{subject}}"]
do = ["Fix {{subject}}"]

[rule.delivery]
receiver = "foreground"
`

	dir := t.TempDir()
	put(t, dir, "broadcast.toml", validBroadcastConsecutive)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules valid broadcast_events rule: %v", err)
	}
	if len(rules) != 1 || len(rules[0].When.BroadcastEvents) != 1 || rules[0].When.BroadcastEvents[0] != "land.landing" {
		t.Fatalf("unexpected BroadcastEvents: %+v", rules)
	}

	cleanRoot := t.TempDir()
	writeLintFile(t, cleanRoot, "config/tillr/watch/broadcast.toml", validBroadcastConsecutive)
	findings, err := Lint(context.Background(), LintOptions{Root: cleanRoot})
	if err != nil {
		t.Fatalf("Lint clean broadcast_events rule: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 lint findings, got: %v", findings)
	}

	for _, tc := range []struct {
		name    string
		doc     string
		lintSub string
	}{
		{
			name:    "empty_list",
			doc:     strings.Replace(validBroadcastConsecutive, `broadcast_events = ["land.landing"]`, `broadcast_events = []`, 1),
			lintSub: "broadcast_events",
		},
		{
			name:    "empty_entry",
			doc:     strings.Replace(validBroadcastConsecutive, `broadcast_events = ["land.landing"]`, `broadcast_events = [""]`, 1),
			lintSub: "broadcast_events",
		},
		{
			name:    "duplicate_entry",
			doc:     strings.Replace(validBroadcastConsecutive, `broadcast_events = ["land.landing"]`, `broadcast_events = ["land.landing", "land.landing"]`, 1),
			lintSub: "duplicate",
		},
		{
			name: "lag_behind_refuses_broadcast_events",
			doc: `[[rule]]
id = "bad-lag-broadcast"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]
[rule.when]
broadcast_events = ["land.landing"]
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 1
[rule.guidance]
summary = "Lag"
check = ["Check"]
do = ["Fix"]
[rule.delivery]
receiver = "foreground"
`,
			lintSub: "broadcast_events",
		},
		{
			name: "projection_only_refuses_broadcast_events",
			doc: `[[rule]]
id = "bad-proj-broadcast"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items"]
[rule.when]
broadcast_events = ["land.landing"]
group_by = "item_id"
pattern = "predicate_match"
match = 'projection.state == "held"'
[rule.guidance]
summary = "Held {{subject}}"
check = ["Check"]
do = ["Fix"]
[rule.delivery]
receiver = "foreground"
`,
			lintSub: "broadcast_events",
		},
		{
			name:    "global_group_by_refuses_broadcast_events",
			doc:     strings.Replace(validBroadcastConsecutive, `group_by = "cause"`, `group_by = "global"`, 1),
			lintSub: "global",
		},
		{
			name: "missing_reset_and_clear_when",
			doc: strings.Replace(
				strings.Replace(validBroadcastConsecutive, "reset = 'event.kind == \"land.landing\" and event.result == \"landed\"'\n", "", 1),
				"clear_when = 'event.kind == \"land.landing\" and event.result == \"landed\"'\n", "", 1,
			),
			lintSub: "reset or clear_when",
		},
		{
			name:    "kind_not_in_reads_events",
			doc:     strings.Replace(validBroadcastConsecutive, `events = ["drain.refusal", "land.landing"]`, `events = ["drain.refusal"]`, 1),
			lintSub: "reads.events",
		},
		{
			name:    "all_reads_events_are_broadcast",
			doc:     strings.Replace(validBroadcastConsecutive, `broadcast_events = ["land.landing"]`, `broadcast_events = ["drain.refusal", "land.landing"]`, 1),
			lintSub: "non-broadcast",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := t.TempDir()
			put(t, d, "bad.toml", tc.doc)
			if _, err := LoadRules(d); err == nil {
				t.Fatalf("LoadRules(%s): expected error, got nil", tc.name)
			}

			root := t.TempDir()
			writeLintFile(t, root, "config/tillr/watch/bad.toml", tc.doc)
			got, err := Lint(context.Background(), LintOptions{Root: root})
			if err != nil {
				t.Fatalf("Lint(%s): %v", tc.name, err)
			}
			matched := false
			for _, f := range got {
				if strings.Contains(f.Detail, tc.lintSub) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("Lint(%s): expected finding containing %q, got %v", tc.name, tc.lintSub, got)
			}
		})
	}
}

func TestWatchBroadcastEventsEvaluationCacheSeedingAndParity(t *testing.T) {
	registerLintTestKinds(t)
	ctx := context.Background()

	const ruleTOML = `[[rule]]
id = "broadcast-refusals"
scope = "core"
severity = "high"

[rule.reads]
events = ["drain.refusal", "land.landing"]

[rule.when]
for_each = "refusal_causes"
group_by = "cause"
broadcast_events = ["land.landing"]
pattern = "consecutive_count"
match = 'event.kind == "drain.refusal"'
distinct_by = "item_id"
reset = 'event.kind == "land.landing" and event.result == "landed"'
clear_when = 'event.kind == "land.landing" and event.result == "landed"'
threshold = 2
window = "24h"

[rule.guidance]
summary = "Shared cause {{subject}}: {{count}} items ({{distinct_values}})."
check = ["Inspect {{subject}}"]
do = ["Fix {{subject}}"]

[rule.delivery]
receiver = "foreground"
`

	dir := t.TempDir()
	put(t, dir, "rule.toml", ruleTOML)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	rule := rules[0]

	// 1. Verify definitionHash preserves exact hashes when BroadcastEvents is nil and changes when set.
	plainRule := rule
	plainRule.When.BroadcastEvents = nil
	rawPlain, err := json.Marshal(struct {
		Scope             string      `json:"scope"`
		Events            []string    `json:"events"`
		Projections       []string    `json:"projections"`
		GroupBy           string      `json:"group_by"`
		GroupByList       []string    `json:"group_by_list"`
		Pattern           string      `json:"pattern"`
		Field             string      `json:"field"`
		DistinctBy        string      `json:"distinct_by,omitempty"`
		ForEach           string      `json:"for_each,omitempty"`
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
	}{
		Scope:      plainRule.Scope,
		Events:     plainRule.Reads.Events,
		GroupBy:    plainRule.When.GroupBy,
		Pattern:    plainRule.When.Pattern,
		DistinctBy: plainRule.When.DistinctBy,
		ForEach:    plainRule.When.ForEach,
		Threshold:  plainRule.When.Threshold,
		Window:     int64(plainRule.When.Window),
		Match:      plainRule.When.Match.source,
		Reset:      plainRule.When.Reset.source,
		ClearWhen:  plainRule.When.ClearWhen.source,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPlainSum := sha256.Sum256(rawPlain)
	if got, want := definitionHash(plainRule), hex.EncodeToString(wantPlainSum[:]); got != want {
		t.Fatalf("definitionHash with empty BroadcastEvents changed existing digest: got %s, want %s", got, want)
	}
	if definitionHash(rule) == definitionHash(plainRule) {
		t.Fatal("definitionHash did not change when BroadcastEvents was set")
	}
	if ruleEqual(rule, plainRule) {
		t.Fatal("ruleEqual must distinguish BroadcastEvents")
	}

	// 2. Store evaluation with warm evalCache + aged-out episode clear + Skipped accounting.
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	s, err := store.Open(ctx, t.TempDir()+"/state.db", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := Evaluator{Store: s, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendEv := func(at time.Time, kind, objID string, payload map[string]any) int64 {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var seq int64
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			var err error
			seq, err = tx.Append(event.Event{
				Kind:       kind,
				ObjectType: "run",
				ObjectID:   &objID,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return seq
	}

	// Open an episode on gate:documents across two distinct items, with a squash-conflict landing between them.
	appendEv(now, "drain.refusal", "run-1", map[string]any{
		"item_id":        "item-a",
		"step":           "gate",
		"refusal_causes": []any{map[string]any{"cause": "gate:documents"}},
	})
	// Non-matching broadcast event (squash-conflict) must leave streak intact with Skipped == 0.
	appendEv(now.Add(1*time.Minute), "land.landing", "run-conflict", map[string]any{
		"result": "squash-conflict",
	})
	appendEv(now.Add(2*time.Minute), "drain.refusal", "run-2", map[string]any{
		"item_id":        "item-b",
		"step":           "gate",
		"refusal_causes": []any{map[string]any{"cause": "gate:documents"}},
	})
	now = now.Add(3 * time.Minute)
	res, err := e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Skipped != 0 {
		t.Fatalf("expected Skipped == 0 across squash-conflict landing, got %+v", res)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Bundle.Subject != "gate:documents" {
		t.Fatalf("expected 1 open alert on gate:documents, got %+v", notices)
	}

	// Age past the 24h window so the triggering refusals are no longer in-window;
	// a landed commit must still resolve the open episode at landedSeq via pre-seeding.
	now = now.Add(25 * time.Hour)
	landedSeq := appendEv(now, "land.landing", "run-land", map[string]any{
		"result": "landed",
	})

	// Check uncached TillrStoreSource.deriveRule pre-seeding directly before reconciling.
	ts := NewTillrStoreSource(s)
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		subjects, skipped, err := ts.deriveRule(rd, rule, now, gitRuleFacts{}, 0)
		if err != nil {
			return err
		}
		if skipped != 0 {
			t.Fatalf("uncached deriveRule skipped = %d, want 0", skipped)
		}
		c := subjects["gate:documents"]
		if c == nil || len(c.clears) == 0 || c.clears[len(c.clears)-1] != landedSeq {
			t.Fatalf("uncached deriveRule did not record clear at landedSeq %d: %+v", landedSeq, c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Reconcile through warm evalCache.
	res, err = e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Skipped != 0 {
		t.Fatalf("expected Skipped == 0 after landed commit, got %d", res[0].Skipped)
	}
	if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
		t.Fatalf("expected 0 pending alerts after landed commit, got %+v (err=%v)", pending, err)
	}
	var state, reason string
	var resolvedSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE rule_id=? AND subject_key=?`,
			rule.ID, "gate:documents").Scan(&state, &reason, &resolvedSeq)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" || reason != "condition-cleared" || resolvedSeq != landedSeq {
		t.Fatalf("resolved row = (%q, %q, %d), want (resolved, condition-cleared, %d)", state, reason, resolvedSeq, landedSeq)
	}

	// A malformed drain.refusal lacking refusal_causes must increment Skipped (no fallback).
	now = now.Add(time.Minute)
	appendEv(now, "drain.refusal", "run-legacy", map[string]any{
		"item_id": "item-legacy",
		"step":    "gate",
	})
	res, err = e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Skipped == 0 {
		t.Fatalf("expected Skipped > 0 for drain.refusal lacking refusal_causes, got 0")
	}

	// 3. ObservationSource parity (deriveFromSource + memoryState pre-seeding).
	obsNow := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	src := &stubObservationSource{epoch: 1}
	memRule := rule
	memRule.Scope = "*"
	memEval := Evaluator{Source: src, Rules: []Rule{memRule}, Clock: func() time.Time { return obsNow }}
	if _, err := memEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	src.records = append(src.records,
		Observation{
			Seq:  1,
			TS:   obsNow.UnixMilli(),
			Kind: "drain.refusal",
			Fields: map[string]any{
				"item_id":        "item-1",
				"step":           "gate",
				"refusal_causes": []any{map[string]any{"cause": "gate:go-build"}},
			},
		},
		Observation{
			Seq:    2,
			TS:     obsNow.Add(time.Minute).UnixMilli(),
			Kind:   "land.landing",
			Fields: map[string]any{"result": "refused"},
		},
		Observation{
			Seq:  3,
			TS:   obsNow.Add(2 * time.Minute).UnixMilli(),
			Kind: "drain.refusal",
			Fields: map[string]any{
				"item_id":        "item-2",
				"step":           "gate",
				"refusal_causes": []any{map[string]any{"cause": "gate:go-build"}},
			},
		},
	)
	obsNow = obsNow.Add(3 * time.Minute)
	memRes, err := memEval.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if memRes[0].Skipped != 0 {
		t.Fatalf("ObservationSource unexpected skipped: %+v", memRes[0])
	}
	memNotices, err := memEval.Pending(ctx, "foreground")
	if err != nil || len(memNotices) != 1 || memNotices[0].Bundle.Subject != "gate:go-build" {
		t.Fatalf("ObservationSource unexpected open alerts: %+v (err=%v)", memNotices, err)
	}
	// Age past window and clear via landed commit.
	obsNow = obsNow.Add(25 * time.Hour)
	src.records = append(src.records, Observation{
		Seq:    4,
		TS:     obsNow.UnixMilli(),
		Kind:   "land.landing",
		Fields: map[string]any{"result": "landed"},
	})
	memRes, err = memEval.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if memRes[0].Skipped != 0 {
		t.Fatalf("ObservationSource unexpected skipped on clear: %+v", memRes[0])
	}
	memNotices, err = memEval.Pending(ctx, "foreground")
	if err != nil || len(memNotices) != 0 {
		t.Fatalf("ObservationSource expected 0 open alerts after landed commit, got %+v (err=%v)", memNotices, err)
	}
}
