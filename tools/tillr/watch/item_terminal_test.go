package watch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func seedWatchItemsTable(t *testing.T, s *store.Store) {
	t.Helper()
	watchSQL(t, s, `CREATE TABLE IF NOT EXISTS items(item_id TEXT PRIMARY KEY, state TEXT, seat TEXT, waiting_on TEXT)`)
}

func setWatchItemState(t *testing.T, s *store.Store, itemID, state string) {
	t.Helper()
	watchSQL(t, s, `INSERT INTO items(item_id, state) VALUES (?, ?) ON CONFLICT(item_id) DO UPDATE SET state = excluded.state`, itemID, state)
}

func TestItemTerminalAutoResolvesAndSuppressesReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)
	seedWatchItemsTable(t, s)

	for _, id := range []string{"item-open", "item-ack", "item-silenced", "item-composite", "item/slash", "global"} {
		setWatchItemState(t, s, id, "in-queue")
	}
	// Even if a synthetic row named "global" were closed, global subjects are never item subjects.
	setWatchItemState(t, s, "global", "closed")

	doc := `[[rule]]
id = "item-refusals"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "item_id"
pattern = "count_in_window"
match = 'event.step != ""'
threshold = 2
window = "24h"
fingerprint = ["step"]
[rule.guidance]
summary = "{{subject}} hit {{count}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "step-runs"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["item_id", "node"]
pattern = "count_in_window"
match = 'event.node != ""'
threshold = 2
window = "24h"
[rule.guidance]
summary = "{{subject}} hit {{count}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "global-faults"
scope = "core"
severity = "high"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
match = 'event.global_hit == true'
threshold = 1
window = "24h"
[rule.guidance]
summary = "global hit"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	for _, itemID := range []string{"item-open", "item-ack", "item-silenced", "item/slash", "non-item-sha"} {
		watchFact(t, s, now.Add(-2*time.Minute), fmt.Sprintf(`{"item_id":%q,"step":"verify"}`, itemID))
		watchFact(t, s, now.Add(-time.Minute), fmt.Sprintf(`{"item_id":%q,"step":"verify"}`, itemID))
	}
	for range 2 {
		watchFact(t, s, now.Add(-time.Minute), `{"item_id":"item-composite","node":"verify"}`)
		watchFact(t, s, now.Add(-time.Minute), `{"item_id":"lane-1","node":"dirty"}`)
	}
	watchFact(t, s, now.Add(-time.Minute), `{"global_hit":true}`)

	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}

	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]AlertStatus{}
	for _, a := range st.Alerts {
		byKey[a.RuleID+":"+a.Subject] = a
	}
	for _, k := range []string{
		"item-refusals:item-open",
		"item-refusals:item-ack",
		"item-refusals:item-silenced",
		"item-refusals:item/slash",
		"item-refusals:non-item-sha",
		"step-runs:item-composite/verify",
		"step-runs:lane-1/dirty",
		"global-faults:global",
	} {
		if _, ok := byKey[k]; !ok {
			t.Fatalf("missing initial alert %s in %+v", k, st.Alerts)
		}
	}

	if err := e.Ack(ctx, byKey["item-refusals:item-ack"].ID, "operator", nil); err != nil {
		t.Fatal(err)
	}
	until := "2h"
	if err := e.Silence(ctx, byKey["item-refusals:item-silenced"].ID, &until, nil); err != nil {
		t.Fatal(err)
	}

	// Transition items to landed, closed, and resolved-elsewhere.
	now = now.Add(time.Minute)
	setWatchItemState(t, s, "item-open", "landed")
	setWatchItemState(t, s, "item-ack", "closed")
	setWatchItemState(t, s, "item-silenced", "resolved-elsewhere")
	setWatchItemState(t, s, "item-composite", "landed")
	setWatchItemState(t, s, "item/slash", "closed")
	headBeforeEval, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}

	// ReadStatus is strictly read-only and must not close episodes before evaluation.
	stBefore, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(stBefore.Alerts) != 8 {
		t.Fatalf("ReadStatus before eval changed alert count: %+v (%v)", stBefore.Alerts, err)
	}

	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	// Check resolved reasons and watch.alert.resolved events.
	wantReasons := map[string]string{
		byKey["item-refusals:item-open"].ID:         "item-landed",
		byKey["item-refusals:item-ack"].ID:          "item-closed",
		byKey["item-refusals:item-silenced"].ID:     "item-resolved-elsewhere",
		byKey["step-runs:item-composite/verify"].ID: "item-landed",
		byKey["item-refusals:item/slash"].ID:        "item-closed",
	}
	for alertID, wantReason := range wantReasons {
		var state, reason string
		var resSeq int64
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE alert_id = %q`, alertID), &state, &reason, &resSeq)
		if state != "resolved" || reason != wantReason || resSeq != headBeforeEval {
			t.Fatalf("alert %s: got state=%q reason=%q seq=%d, want resolved/%q/%d", alertID, state, reason, resSeq, wantReason, headBeforeEval)
		}
		var evReason, evReceiver string
		var evSeq int64
		lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.reason'), json_extract(payload,'$.receiver'), json_extract(payload,'$.resolved_seq') FROM events WHERE object_id = %q AND kind = %q`, alertID, resolvedKind), &evReason, &evReceiver, &evSeq)
		if evReason != wantReason || evReceiver != "foreground" || evSeq != headBeforeEval {
			t.Fatalf("resolved event for %s: reason=%q receiver=%q seq=%d, want %q/foreground/%d", alertID, evReason, evReceiver, evSeq, wantReason, headBeforeEval)
		}
	}

	// Non-item subjects and global remain open.
	stAfter, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	remaining := map[string]bool{}
	for _, a := range stAfter.Alerts {
		remaining[a.RuleID+":"+a.Subject] = true
	}
	if len(remaining) != 3 || !remaining["item-refusals:non-item-sha"] || !remaining["step-runs:lane-1/dirty"] || !remaining["global-faults:global"] {
		t.Fatalf("unexpected remaining open alerts: %+v", stAfter.Alerts)
	}

	// Subsequent evaluations while in-window events remain (and even if another event
	// arrives while the item is still terminal) never re-open a terminal item's episode.
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"item_id":"item-open","step":"verify"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var openEpCount int
	lifecycleRead(t, s, `SELECT COUNT(*) FROM alert_episodes WHERE rule_id = 'item-refusals' AND subject_key = 'item-open'`, &openEpCount)
	if openEpCount != 1 {
		t.Fatalf("terminal item-open reopened episode: count=%d", openEpCount)
	}

	// Reopening item-ack without a new post-resolution counted event keeps it suppressed
	// because newest.Seq <= resolved_seq; a new post-reopen counted event opens episode 2.
	now = now.Add(time.Minute)
	setWatchItemState(t, s, "item-ack", "in-queue")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ackCount int
	lifecycleRead(t, s, `SELECT COUNT(*) FROM alert_episodes WHERE rule_id = 'item-refusals' AND subject_key = 'item-ack'`, &ackCount)
	if ackCount != 1 {
		t.Fatalf("reopened item-ack without new counted event opened episode: count=%d", ackCount)
	}
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"item_id":"item-ack","step":"verify-after-reopen"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ackEp2State string
	var ackEp2Num int64
	lifecycleRead(t, s, `SELECT episode_num, state FROM alert_episodes WHERE rule_id = 'item-refusals' AND subject_key = 'item-ack' ORDER BY episode_num DESC LIMIT 1`, &ackEp2Num, &ackEp2State)
	if ackEp2Num != 2 || ackEp2State != "open" {
		t.Fatalf("post-reopen counted event should open episode 2, got num=%d state=%q", ackEp2Num, ackEp2State)
	}
}

func TestItemTerminalUnloadedAndDisabledRulesAndCustomCharter(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC)
	s := watchStore(t)
	seedWatchItemsTable(t, s)

	// Custom charter where closed ("cancelled") is outside finished (["confirmed"]).
	content := []byte(`{"definition":"custom-verdict","version":1,"states":["queued","blocked","cancelled","confirmed"],"ready":"queued","blocked":"blocked","closed":"cancelled","finished":["confirmed"],"completes":"landing"}`)
	digest := sha256.Sum256(content)
	recJSON, err := json.Marshal(map[string]any{
		"definition": "custom-verdict",
		"version":    1,
		"content":    string(content),
		"sha256":     fmt.Sprintf("%x", digest),
		"default":    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `INSERT INTO meta(key, value) VALUES ('store.definition', ?)`, string(recJSON))

	setWatchItemState(t, s, "item-unloaded", "queued")
	setWatchItemState(t, s, "item%slash", "queued")
	setWatchItemState(t, s, "item-disabled", "queued")

	doc := `[[rule]]
id = "unloaded-single"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
window = "1h"
[rule.guidance]
summary = "{{subject}}"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "subscribers"

[[rule]]
id = "unloaded-composite"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["item_id", "node"]
pattern = "predicate_match"
window = "1h"
[rule.guidance]
summary = "{{subject}}"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "file-disabled"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
window = "1h"
[rule.guidance]
summary = "{{subject}}"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`
	watchFact(t, s, now.Add(-time.Minute), `{"item_id":"item-unloaded"}`)
	watchFact(t, s, now.Add(-time.Minute), `{"item_id":"item%slash","node":"verify"}`)
	watchFact(t, s, now.Add(-time.Minute), `{"item_id":"item-disabled"}`)
	rules := watchRules(t, doc)

	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	// Now unload the first two rules and file-disable the third, then move items to terminal states.
	disabledRule := rules[2]
	disabledRule.Enabled = false
	eUnloaded := Evaluator{Store: s, Rules: []Rule{disabledRule}, Clock: func() time.Time { return now }}

	setWatchItemState(t, s, "item-unloaded", "cancelled")
	setWatchItemState(t, s, "item%slash", "confirmed")
	setWatchItemState(t, s, "item-disabled", "confirmed")

	// Also verify legacy Evaluate closes item-terminal episodes.
	if _, err := eUnloaded.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		ruleID, subject, wantReason, wantReceiver string
	}{
		{"unloaded-single", "item-unloaded", "item-cancelled", "subscribers"},
		{"unloaded-composite", "item%25slash/verify", "item-confirmed", "foreground"},
		{"file-disabled", "item-disabled", "item-confirmed", "foreground"},
	} {
		var state, reason string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason FROM alert_episodes WHERE rule_id = %q AND subject_key = %q`, tc.ruleID, tc.subject), &state, &reason)
		if state != "resolved" || reason != tc.wantReason {
			t.Fatalf("%s/%s: got state=%q reason=%q, want resolved/%q", tc.ruleID, tc.subject, state, reason, tc.wantReason)
		}
		var evReceiver, evReason string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.receiver'), json_extract(payload,'$.reason') FROM events WHERE kind = %q AND json_extract(payload,'$.rule_id') = %q AND json_extract(payload,'$.subject_key') = %q`, resolvedKind, tc.ruleID, tc.subject), &evReceiver, &evReason)
		if evReceiver != tc.wantReceiver || evReason != tc.wantReason {
			t.Fatalf("%s event: receiver=%q reason=%q, want %q/%q", tc.ruleID, evReceiver, evReason, tc.wantReceiver, tc.wantReason)
		}
	}
}

func TestItemTerminalEventOnlyStoreAndCorruptCharterAreSafeNoOps(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 15, 0, 0, 0, time.UTC)

	// 1. Bare event-only store with no items table: rules evaluate normally with no Unreadable diagnostic.
	bare := watchStore(t)
	watchFact(t, bare, now.Add(-time.Minute), `{"item_id":"item-bare"}`)
	rule := watchRules(t, strings.Replace(validRule, `"test.watch"`, `"watch-test.fact"`, 1))[0]
	rule.When.GroupBy = "item_id"
	eBare := Evaluator{Store: bare, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
	results, err := eBare.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || results[0].Diagnostic() != "" {
		t.Fatalf("event-only store results=%+v err=%v", results, err)
	}
	if notices, err := eBare.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("event-only store notices=%+v err=%v", notices, err)
	}

	// 2. Store with items table where meta.store.definition becomes corrupt: does not error or resolve episodes.
	corrupt := watchStore(t)
	seedWatchItemsTable(t, corrupt)
	setWatchItemState(t, corrupt, "item-c", "in-queue")
	watchFact(t, corrupt, now.Add(-time.Minute), `{"item_id":"item-c"}`)
	eCorrupt := Evaluator{Store: corrupt, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := eCorrupt.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	setWatchItemState(t, corrupt, "item-c", "closed")
	watchSQL(t, corrupt, `INSERT INTO meta(key, value) VALUES ('store.definition', 'not-valid-json')`)
	results, err = eCorrupt.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || results[0].Diagnostic() != "" {
		t.Fatalf("corrupt charter results=%+v err=%v", results, err)
	}
	if notices, err := eCorrupt.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("corrupt charter should skip item-terminal resolution, got notices=%+v err=%v", notices, err)
	}
}

func TestItemTerminalInMemorySourceUnloadedAndComposite(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 16, 0, 0, 0, time.UTC)
	items := map[string]any{
		"item-a": map[string]any{"item.state": "in-queue"},
		"item-b": map[string]any{"item.state": "in-queue"},
	}
	src := &memoryObservationSource{
		projections: map[string]any{
			"charter": map[string]any{
				"closed":   "closed",
				"finished": []string{"landed", "closed"},
			},
			"items": items,
		},
	}
	src.append("run.allocate", now.Add(-time.Minute), map[string]any{"item_id": "item-a", "node": "verify"})
	src.append("drain.refusal", now.Add(-time.Minute), map[string]any{"item_id": "item-b"})

	rules := []Rule{
		{
			ID:       "comp-src",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"run.allocate"}},
			When: When{
				GroupByList: []string{"item_id", "node"},
				Pattern:     PredicateMatch,
				Threshold:   1,
				Window:      time.Hour,
			},
			Guidance: Guidance{Summary: "{{subject}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		},
		{
			ID:       "unloaded-src",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"drain.refusal"}},
			When: When{
				GroupBy:   "item_id",
				Pattern:   PredicateMatch,
				Threshold: 1,
				Window:    time.Hour,
			},
			Guidance: Guidance{Summary: "{{subject}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		},
	}

	e := Evaluator{Source: src, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st1, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st1.Alerts) != 2 {
		t.Fatalf("expected 2 open alerts in memory source, got %+v (%v)", st1.Alerts, err)
	}

	// Unload the second rule and transition item-a -> landed, item-b -> closed.
	e.Rules = rules[:1]
	items["item-a"] = map[string]any{"item.state": "landed"}
	items["item-b"] = map[string]any{"item.state": "closed"}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st2, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st2.Alerts) != 0 {
		t.Fatalf("expected 0 open alerts after items finished, got %+v (%v)", st2.Alerts, err)
	}
}

func TestItemTerminalViaTillrStoreSource(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	s := watchStore(t)
	seedWatchItemsTable(t, s)
	setWatchItemState(t, s, "item-src", "in-queue")
	watchFact(t, s, now.Add(-time.Minute), `{"item_id":"item-src"}`)

	src := NewTillrStoreSource(s)
	src.Clock = func() time.Time { return now }
	rule := watchRules(t, strings.Replace(validRule, `"test.watch"`, `"watch-test.fact"`, 1))[0]
	rule.When.GroupBy = "item_id"
	e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}

	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "item-src" {
		t.Fatalf("expected open notice via Source, got %+v (%v)", notices, err)
	}

	setWatchItemState(t, s, "item-src", "closed")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 0 {
		t.Fatalf("expected closed notice via Source, got %+v (%v)", notices, err)
	}
	st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Alerts) != 0 {
		t.Fatalf("expected 0 open alerts in Source status, got %+v (%v)", st.Alerts, err)
	}
}
