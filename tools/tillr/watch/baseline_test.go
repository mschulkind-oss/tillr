package watch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func readBaselineTestRow(t *testing.T, s *store.Store, ruleID string) (ruleBaseline, bool) {
	t.Helper()
	var (
		b  ruleBaseline
		ok bool
	)
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		var err error
		b, ok, err = readRuleBaselineRow(rd, ruleID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return b, ok
}

func TestWatchRuleBaselineFirstLoadIgnoresOldHistoryAndOpensOnNewEvent(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	// Seed matching old events and a non-string group_by event before the rule is loaded.
	watchFact(t, s, now.Add(-20*time.Minute), `{"detail":{"item-id":"old-1"}}`)
	watchFact(t, s, now.Add(-10*time.Minute), `{"detail":{"item-id":"old-2"}}`)
	watchFact(t, s, now.Add(-5*time.Minute), `{"detail":{"item-id":123}}`)
	headBeforeLoad, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}

	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}

	// First load over old matching history: 0 alerts, skipped = 0.
	results, err := e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped != 0 || results[0].Diagnostic() != "" {
		t.Fatalf("first load results = %+v, want skipped=0 with no diagnostic", results)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("first load opened alerts on pre-load history: %+v, err=%v", notices, err)
	}
	row, ok := readBaselineTestRow(t, s, "case")
	if !ok || !row.enabled || row.firstLoadedSeq != headBeforeLoad || row.loadedSeq != headBeforeLoad || row.firstLoadedAt != now.UnixMilli() || row.loadedAt != now.UnixMilli() {
		t.Fatalf("baseline row = %+v (ok=%t), want enabled at seq %d", row, ok, headBeforeLoad)
	}

	// Append 1 new matching event after first load: opens 1 alert.
	newSeq := watchFact(t, s, now, `{"detail":{"item-id":"fresh-1"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "fresh-1" || len(notices[0].Bundle.Events) != 1 || notices[0].Bundle.Events[0].Seq != newSeq {
		t.Fatalf("post-load notices = %+v (err=%v), want 1 alert for fresh-1 at seq %d", notices, err, newSeq)
	}
}

func TestWatchRuleBaselineThresholdTwoDoesNotPrefillFromOldHistory(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	// Seed 5 matching in-window events for item-a before loading threshold=2 rule.
	for i := 0; i < 5; i++ {
		watchFact(t, s, now.Add(-time.Duration(10-i)*time.Minute), `{"detail":{"item-id":"item-a"}}`)
	}
	doc := strings.NewReplacer(
		`"test.watch"`, `"watch-test.fact"`,
		`threshold = 1`, `threshold = 2`,
		`window = "90s"`, `window = "1h"`,
	).Replace(validRule)
	e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}

	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("first load opened alerts: %+v, %v", notices, err)
	}

	// One post-load event must NOT combine with pre-load events to reach threshold=2.
	watchFact(t, s, now, `{"detail":{"item-id":"item-a"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("single post-load event pre-filled from old history: %+v, %v", notices, err)
	}

	// Second post-load event reaches threshold=2 and opens 1 alert with 2 events.
	watchFact(t, s, now, `{"detail":{"item-id":"item-a"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "item-a" || len(notices[0].Bundle.Events) != 2 {
		t.Fatalf("second post-load notices = %+v (err=%v), want 1 alert with 2 events", notices, err)
	}
}

func TestWatchRuleBaselineConditionChangeAndTunePreserveOpenEpisodesAndGateNewOnes(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	doc := strings.NewReplacer(
		`"test.watch"`, `"watch-test.fact"`,
		`threshold = 1`, `threshold = 3`,
		`window = "90s"`, `window = "1h"`,
	).Replace(validRule)
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	// item-open reaches threshold=3 and opens; item-sub has 2 events (below 3).
	for i := 0; i < 3; i++ {
		watchFact(t, s, now, `{"detail":{"item-id":"item-open"}}`)
	}
	for i := 0; i < 2; i++ {
		watchFact(t, s, now, `{"detail":{"item-id":"item-sub"}}`)
	}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "item-open" {
		t.Fatalf("initial open = %+v, %v", notices, err)
	}

	// Lower threshold to 2 via RuleStore.Tune:
	// - first_loaded_seq stays 0, loaded_seq advances to watch.rule.set changeSeq.
	// - item-open stays open (unaffected by loaded_seq).
	// - item-sub has 2 pre-change events and MUST NOT open a new alert.
	rs := RuleStore{Store: s, FileRules: rules, Clock: func() time.Time { return now }}
	if err := rs.Tune(ctx, "case", []string{"when.threshold=2"}, "operator"); err != nil {
		t.Fatal(err)
	}
	rowAfterTune, ok := readBaselineTestRow(t, s, "case")
	if !ok || rowAfterTune.firstLoadedSeq != 0 || rowAfterTune.loadedSeq == 0 {
		t.Fatalf("baseline after tune = %+v (ok=%t), want first_loaded_seq=0 and loaded_seq>0", rowAfterTune, ok)
	}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "item-open" {
		t.Fatalf("after threshold tune notices = %+v (err=%v), want only item-open", notices, err)
	}

	// One post-change event for item-sub has seq > loaded_seq -> opens 1 alert for item-sub.
	watchFact(t, s, now, `{"detail":{"item-id":"item-sub"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 2 {
		t.Fatalf("after post-tune event notices = %+v (err=%v), want 2 alerts", notices, err)
	}

	// Tightening threshold to 5 via file edit resolves both open episodes as condition-cleared.
	if err := rs.Reset(ctx, "case", "operator"); err != nil {
		t.Fatal(err)
	}
	tightDoc := strings.Replace(doc, `threshold = 3`, `threshold = 5`, 1)
	e.Rules = watchRules(t, tightDoc)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("tightened rule did not resolve open episodes: %+v, %v", notices, err)
	}
	rowAfterTight, ok := readBaselineTestRow(t, s, "case")
	if !ok || rowAfterTight.firstLoadedSeq != 0 || rowAfterTight.loadedSeq <= rowAfterTune.loadedSeq {
		t.Fatalf("baseline after tighten = %+v, want first_loaded_seq=0 and advanced loaded_seq", rowAfterTight)
	}
}

func TestWatchRuleBaselinePresentationEditsAndExportResetPreserveLoadedSeq(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	doc := strings.NewReplacer(
		`"test.watch"`, `"watch-test.fact"`,
		`window = "90s"`, `window = "1h"`,
	).Replace(validRule)
	rules := watchRules(t, doc)

	// Verify Go-literal and TOML rules produce identical definition_hash.
	litRule := Rule{
		ID:       "case",
		Scope:    "workflow:*",
		Severity: "high", // different presentation field
		Enabled:  true,
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When: When{
			GroupBy:     "detail.item-id",
			Pattern:     CountInWindow,
			Threshold:   1,
			Window:      time.Hour,
			Fingerprint: []string{"detail.item-id"}, // excluded from hash
		},
		Guidance: Guidance{Summary: "other", Check: []string{"x"}, Do: []string{"y"}},
		Delivery: Delivery{Receiver: "subscribers"},
	}
	if definitionHash(rules[0]) != definitionHash(litRule) {
		t.Fatalf("TOML hash %s != Go-literal hash %s", definitionHash(rules[0]), definitionHash(litRule))
	}

	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	initial, ok := readBaselineTestRow(t, s, "case")
	if !ok || initial.firstLoadedSeq != 0 || initial.loadedSeq != 0 {
		t.Fatalf("initial baseline = %+v (ok=%t)", initial, ok)
	}

	// Append an event, then edit Guidance, Delivery, Severity, and Fingerprint only.
	watchFact(t, s, now, `{"detail":{"item-id":"x"}}`)
	rs := RuleStore{Store: s, FileRules: rules, Clock: func() time.Time { return now }}
	if err := rs.Tune(ctx, "case", []string{
		`severity="high"`,
		`guidance.summary="updated {{count}}"`,
		`delivery.receiver="subscribers"`,
		`when.fingerprint=["detail.item-id"]`,
	}, "operator"); err != nil {
		t.Fatal(err)
	}
	afterPresentation, ok := readBaselineTestRow(t, s, "case")
	if !ok || afterPresentation.firstLoadedSeq != initial.firstLoadedSeq || afterPresentation.loadedSeq != initial.loadedSeq {
		t.Fatalf("presentation edit changed baseline: before=%+v after=%+v", initial, afterPresentation)
	}

	// Promote via export + reset: file rule now matches stored rule; reset must preserve first_loaded_seq and loaded_seq.
	var exported []Rule
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		exported, err = ReadEffectiveRules(rd, rules)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rsPromoted := RuleStore{Store: s, FileRules: exported, Clock: func() time.Time { return now }}
	if err := rsPromoted.Reset(ctx, "case", "operator"); err != nil {
		t.Fatal(err)
	}
	afterReset, ok := readBaselineTestRow(t, s, "case")
	if !ok || afterReset.firstLoadedSeq != initial.firstLoadedSeq || afterReset.loadedSeq != initial.loadedSeq {
		t.Fatalf("export+reset changed baseline: before=%+v after=%+v", initial, afterReset)
	}
}

func TestWatchRuleBaselineColdReloadAndZeroSteadyStateIdleWrites(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	// Seed pre-load event, load on first Evaluator.
	watchFact(t, s, now.Add(-time.Minute), `{"detail":{"item-id":"pre"}}`)
	doc := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, `window = "90s"`, `window = "1h"`).Replace(validRule)
	rules := watchRules(t, doc)

	e1 := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e1.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	// Append post-load event between processes; a cold Evaluator reloads watch_rule_baselines from SQLite.
	watchFact(t, s, now, `{"detail":{"item-id":"between"}}`)
	writes := 0
	obsCtx := store.ObserveWrites(ctx, func(_, _ time.Time) { writes++ })
	e2 := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e2.EvaluateRules(obsCtx); err != nil {
		t.Fatal(err)
	}
	if writes != 1 {
		t.Fatalf("cold pass opening 1 episode took %d writes, want 1", writes)
	}
	if notices, err := e2.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "between" {
		t.Fatalf("cold reload notices = %+v (err=%v), want 1 alert for between", notices, err)
	}

	// Steady-state quiet passes (both warm and brand-new cold Evaluator) must take 0 SQLite writes.
	writes = 0
	if _, err := e2.EvaluateRules(obsCtx); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("warm quiet pass took %d writes, want 0", writes)
	}
	e3 := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	writes = 0
	if _, err := e3.EvaluateRules(obsCtx); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("cold quiet pass took %d writes, want 0", writes)
	}
}

func TestWatchRuleBaselineReadOnlyStatusWritesNothingBeforeAndAfterLoad(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	watchFact(t, s, now.Add(-time.Minute), `{"detail":{"item-id":123}}`)
	watchFact(t, s, now.Add(-30*time.Second), `{"detail":{"item-id":"pre"}}`)
	doc := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, `window = "90s"`, `window = "1h"`).Replace(validRule)
	rules := watchRules(t, doc)

	// ReadStatus before first load: read-only store, reports skipped=0, writes no baseline row.
	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	roEval := Evaluator{Store: ro, Rules: rules, Clock: func() time.Time { return now }}
	st, err := roEval.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Rules) != 1 || st.Rules[0].Skipped != 0 || len(st.Alerts) != 0 {
		t.Fatalf("pre-load ReadStatus = %+v (err=%v), want skipped=0 alerts=0", st, err)
	}
	if _, ok := readBaselineTestRow(t, s, "case"); ok {
		t.Fatal("ReadStatus created a watch_rule_baselines row")
	}

	// Load via writable Evaluator, then append one post-load malformed-group_by event.
	rwEval := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := rwEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":456}}`)
	st, err = roEval.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Rules) != 1 || st.Rules[0].Skipped != 1 {
		t.Fatalf("post-load ReadStatus = %+v (err=%v), want skipped=1", st, err)
	}
}

func TestWatchRuleBaselineDisableReenableAndSyncKindsReload(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	ruleA := strings.NewReplacer(`id = "case"`, `id = "rule-a"`, `"test.watch"`, `"watch-test.fact"`, `window = "90s"`, `window = "1h"`).Replace(validRule)
	ruleB := strings.NewReplacer(`id = "case"`, `id = "rule-b"`, `"test.watch"`, `"watch-test.fact"`, `window = "90s"`, `window = "1h"`).Replace(validRule)
	disabledRule := strings.NewReplacer(`id = "case"`, `id = "never-enabled"`, `"test.watch"`, `"watch-test.fact"`, `[rule.reads]`, "enabled = false\n[rule.reads]").Replace(validRule)

	// Load rule-a and never-enabled at seq 0.
	e := Evaluator{Store: s, Rules: watchRules(t, ruleA+"\n"+disabledRule), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := readBaselineTestRow(t, s, "never-enabled"); ok {
		t.Fatal("disabled rule with no prior row must not record a baseline")
	}

	// Event at seq 1 (after rule-a's first_loaded_seq = 0).
	watchFact(t, s, now, `{"detail":{"item-id":"x"}}`)
	// Disable rule-a and load rule-b at seq 1.
	e.Rules = watchRules(t, strings.Replace(ruleA, "[rule.reads]", "enabled = false\n[rule.reads]", 1)+"\n"+ruleB)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	rowA, ok := readBaselineTestRow(t, s, "rule-a")
	if !ok || rowA.enabled || rowA.firstLoadedSeq != 0 {
		t.Fatalf("disabled rule-a baseline = %+v (ok=%t), want enabled=false first_loaded_seq=0", rowA, ok)
	}
	rowB, ok := readBaselineTestRow(t, s, "rule-b")
	if !ok || !rowB.enabled || rowB.firstLoadedSeq != 1 {
		t.Fatalf("rule-b baseline = %+v (ok=%t), want enabled=true first_loaded_seq=1", rowB, ok)
	}

	// Re-enable rule-a on the SAME Evaluator: syncKinds must reload since minFirstLoadedSeq drops from 1 to 0,
	// while loaded_seq advances so seq 1 does not open a new alert for rule-a.
	e.Rules = watchRules(t, ruleA+"\n"+ruleB)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	rowA2, ok := readBaselineTestRow(t, s, "rule-a")
	if !ok || !rowA2.enabled || rowA2.firstLoadedSeq != 0 || rowA2.loadedSeq != 1 {
		t.Fatalf("re-enabled rule-a baseline = %+v, want enabled=true first_loaded_seq=0 loaded_seq=1", rowA2)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("re-enabled rule-a opened on pre-reenable event: %+v, %v", notices, err)
	}
	// Post-reenable event opens for both rule-a and rule-b.
	watchFact(t, s, now, `{"detail":{"item-id":"y"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 2 {
		t.Fatalf("post-reenable notices = %+v (err=%v), want 2", notices, err)
	}
}

type epochMemorySource struct {
	memoryObservationSource
	epoch int64
}

func (m *epochMemorySource) ReadWindow(ctx context.Context, sinceMillis int64, kinds []string) ([]Observation, SourceCursor, SkippedLines, error) {
	obs, cur, skipped, err := m.memoryObservationSource.ReadWindow(ctx, sinceMillis, kinds)
	cur.Epoch = m.epoch
	return obs, cur, skipped, err
}

func TestWatchRuleBaselineMemorySourceAndEpochReset(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	src := &epochMemorySource{epoch: 1}
	src.append("ev.fail", now.Add(-time.Minute), map[string]any{"pkg": "old"})

	rule := Rule{
		ID:       "mem-rule",
		Scope:    "*",
		Severity: "warning",
		Enabled:  true,
		Reads:    Reads{Events: []string{"ev.fail"}},
		When: When{
			GroupBy:   "pkg",
			Pattern:   CountInWindow,
			Threshold: 1,
			Window:    time.Hour,
		},
		Guidance: Guidance{Summary: "{{subject}}", Check: []string{"c"}, Do: []string{"d"}},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}

	// Read-only ReadStatus before first load writes nothing and sees 0 alerts.
	if st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil }); err != nil || len(st.Alerts) != 0 || e.mem != nil {
		t.Fatalf("pre-load memory ReadStatus = %+v (mem=%v, err=%v)", st, e.mem, err)
	}

	// First load over old event: 0 alerts.
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("first load opened on old memory event: %+v, %v", notices, err)
	}

	// New event in epoch 1 opens 1 alert.
	src.append("ev.fail", now, map[string]any{"pkg": "fresh-e1"})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "fresh-e1" {
		t.Fatalf("epoch 1 fresh event notices = %+v, %v", notices, err)
	}

	// Rotate source to epoch 2 with pre-existing events at seq 1..2: baselines reset to epoch 2's head (2), opening 0 new alerts.
	src.epoch = 2
	src.records = nil
	src.append("ev.fail", now.Add(-time.Minute), map[string]any{"pkg": "e2-old-1"})
	src.append("ev.fail", now.Add(-30*time.Second), map[string]any{"pkg": "e2-old-2"})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("epoch 2 rotation opened on pre-rotation events: %+v, %v", notices, err)
	}

	// New event in epoch 2 (seq 3 > 2) opens 1 alert.
	src.append("ev.fail", now, map[string]any{"pkg": "e2-fresh"})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "e2-fresh" {
		t.Fatalf("epoch 2 fresh event notices = %+v, %v", notices, err)
	}
}
