package watch

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func registerTestKind(t *testing.T, kind string) {
	t.Helper()
	if err := event.Register(kind, "watch-test"); err != nil {
		t.Fatal(err)
	}
}

func appendKindEvent(t *testing.T, s *store.Store, kind string, at time.Time, body string) int64 {
	t.Helper()
	registerTestKind(t, kind)
	var seq int64
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) (err error) {
		seq, err = tx.Append(event.Event{Kind: kind, At: at, Payload: []byte(body)})
		return
	}); err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestDTW50LoaderCompositeGroupByAndOptionalWindow(t *testing.T) {
	registerTestKind(t, "watch-test.fact")

	validCases := []struct {
		name      string
		toml      string
		wantGroup string
		wantList  []string
		wantWin   time.Duration
	}{
		{
			name: "single-element-array-equivalent-to-string",
			toml: `[[rule]]
id = "r1"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["lane_id"]
pattern = "predicate_match"
window = "5m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
			wantGroup: "lane_id",
			wantList:  nil,
			wantWin:   5 * time.Minute,
		},
		{
			name: "composite-unwindowed-predicate-match-with-clear-when",
			toml: `[[rule]]
id = "r2"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["lane_id", "reason"]
pattern = "predicate_match"
match = 'event.kind == "watch-test.fact"'
clear_when = 'event.clear == true'
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
			wantGroup: "lane_id, reason",
			wantList:  []string{"lane_id", "reason"},
			wantWin:   0,
		},
		{
			name: "composite-duration-without-event",
			toml: `[[rule]]
id = "r3"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["a", "b"]
pattern = "duration_without_event"
after = 'event.start == true'
threshold_duration = "10s"
window = "1m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
			wantGroup: "a, b",
			wantList:  []string{"a", "b"},
			wantWin:   time.Minute,
		},
	}

	for _, tc := range validCases {
		t.Run(tc.name, func(t *testing.T) {
			rules, err := LoadRuleDocument("test.toml", []byte(tc.toml))
			if err != nil {
				t.Fatalf("unexpected load error: %v", err)
			}
			if len(rules) != 1 {
				t.Fatalf("got %d rules, want 1", len(rules))
			}
			r := rules[0]
			if r.When.GroupBy != tc.wantGroup {
				t.Errorf("GroupBy = %q, want %q", r.When.GroupBy, tc.wantGroup)
			}
			if !slices.Equal(r.When.GroupByList, tc.wantList) {
				t.Errorf("GroupByList = %v, want %v", r.When.GroupByList, tc.wantList)
			}
			if r.When.Window != tc.wantWin {
				t.Errorf("Window = %v, want %v", r.When.Window, tc.wantWin)
			}
		})
	}

	invalidCases := []struct {
		name string
		toml string
	}{
		{
			name: "empty-array",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = []
pattern = "predicate_match"
window = "5m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "global-in-array",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["lane_id", "global"]
pattern = "predicate_match"
window = "5m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "duplicate-in-array",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["lane_id", "lane_id"]
pattern = "predicate_match"
window = "5m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "invalid-path-in-array",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["lane_id", "a..b"]
pattern = "predicate_match"
window = "5m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "multi-group-by-on-lag-behind",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/heads/green"]
[rule.when]
group_by = ["a", "b"]
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/heads/green"
threshold_commits = 1
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "multi-group-by-on-workflow-scope",
			toml: `[[rule]]
id = "r"
scope = "workflow:express"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = ["a", "b"]
pattern = "predicate_match"
window = "5m"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "predicate-match-without-clear-when-requires-window",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "lane_id"
pattern = "predicate_match"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "count-in-window-with-clear-when-requires-window",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "lane_id"
pattern = "count_in_window"
threshold = 2
clear_when = 'event.clear == true'
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "predicate-match-with-clear-when-and-invalid-window",
			toml: `[[rule]]
id = "r"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "lane_id"
pattern = "predicate_match"
clear_when = 'event.clear == true'
window = "0s"
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadRuleDocument("test.toml", []byte(tc.toml)); err == nil {
				t.Fatalf("expected load refusal, got nil")
			}
		})
	}
}

func TestDTW50CompositeSubjectEscapingPrefixClearAndPreSeed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)

	// Unmigrated store: RuleResults / Skipped must succeed even without alert_episodes table.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE alert_episodes;`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	registerTestKind(t, "watch-test.refused")
	registerTestKind(t, "watch-test.restored")

	ruleTOML := `[[rule]]
id = "lane-refused"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.refused", "watch-test.restored"]
[rule.when]
group_by = ["lane_id", "reason"]
pattern = "predicate_match"
match = 'event.kind == "watch-test.refused"'
clear_when = 'event.kind == "watch-test.restored"'
[rule.guidance]
summary = "Lane {{event.lane_id}} refused ({{event.reason}}) by {{event.left_by}}"
check = ["check {{event.lane_id}}"]
do = ["clean {{event.lane_id}}"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := LoadRuleDocument("lane-refused.toml", []byte(ruleTOML))
	if err != nil {
		t.Fatal(err)
	}
	appendKindEvent(t, s, "watch-test.refused", now.Add(-48*time.Hour), `{"lane_id":"a/b","reason":"c%d","left_by":"run-1"}`)
	appendKindEvent(t, s, "watch-test.refused", now.Add(-47*time.Hour), `{"lane_id":123,"reason":"bad"}`)
	appendKindEvent(t, s, "watch-test.restored", now.Add(-46*time.Hour), `{"lane_id":"other"}`)

	unmigratedEval := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	results, err := unmigratedEval.RuleResults(ctx)
	if err != nil {
		t.Fatalf("RuleResults on unmigrated store failed: %v", err)
	}
	if len(results) != 1 || results[0].GroupBy != "lane_id, reason" || results[0].Skipped != 0 {
		t.Fatalf("unexpected unmigrated RuleResults: %+v", results)
	}

	// Fresh migrated store for full episode lifecycle + incremental cache + prefix clear.
	s2 := watchStore(t)
	if _, err := (&Evaluator{Store: s2, Rules: rules, Clock: func() time.Time { return now }}).EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	seq1 := appendKindEvent(t, s2, "watch-test.refused", now.Add(-72*time.Hour), `{"lane_id":"a/b","reason":"c%d","left_by":"run-1"}`)
	seq2 := appendKindEvent(t, s2, "watch-test.refused", now.Add(-71*time.Hour), `{"lane_id":"a/b","reason":"other-reason","left_by":"run-2"}`)
	seq3 := appendKindEvent(t, s2, "watch-test.refused", now.Add(-70*time.Hour), `{"lane_id":"lane-2","reason":"lane-branch-held"}`)
	// Skipped events: non-string, missing prefix, or prefix on non-clear_when event.
	appendKindEvent(t, s2, "watch-test.refused", now.Add(-69*time.Hour), `{"lane_id":"a/b","reason":null}`)
	appendKindEvent(t, s2, "watch-test.refused", now.Add(-68*time.Hour), `{"reason":"only-second"}`)
	appendKindEvent(t, s2, "watch-test.restored", now.Add(-67*time.Hour), `{"lane_id":42}`)
	// Future event should not be included or counted in skipped until clock reaches it.
	appendKindEvent(t, s2, "watch-test.refused", now.Add(time.Hour), `{"lane_id":99}`)

	var queries []int64
	eval := Evaluator{
		Store: s2,
		Rules: rules,
		Clock: func() time.Time { return now },
		onEventQuery: func(_ string, sinceSeq int64, _ int) {
			queries = append(queries, sinceSeq)
		},
	}
	res1, err := eval.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res1[0].Skipped != 3 || res1[0].GroupBy != "lane_id, reason" {
		t.Fatalf("pass 1 result = %+v, want skipped=3 group_by='lane_id, reason'", res1[0])
	}

	// Verify 3 distinct active episodes with escaped subject_keys.
	activeKeys := map[string]int64{}
	if err := s2.ReadTx(ctx, func(rd *store.Reader) error {
		rows, err := rd.Query(`SELECT subject_key, opened_seq FROM alert_episodes WHERE state='open'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var seq int64
			if err := rows.Scan(&k, &seq); err != nil {
				return err
			}
			activeKeys[k] = seq
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if activeKeys["a%2Fb/c%25d"] != seq1 || activeKeys["a%2Fb/other-reason"] != seq2 || activeKeys["lane-2/lane-branch-held"] != seq3 {
		t.Fatalf("unexpected activeKeys: %v", activeKeys)
	}

	// Advance wall clock by 30 days: unwindowed predicate_match must NOT age out.
	now = now.Add(30 * 24 * time.Hour)
	// Also append a prefix-clearing drain.lane-restored for "a/b" (reason absent).
	seqClear := appendKindEvent(t, s2, "watch-test.restored", now.Add(-time.Minute), `{"lane_id":"a/b"}`)
	queries = nil
	res2, err := eval.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Incremental query must read only tail (sinceSeq > 0), not rescan from 0.
	if len(queries) == 0 || slices.Contains(queries, int64(0)) {
		t.Fatalf("expected incremental tail queries only, got sinceSeqs=%v", queries)
	}
	// Future event at +1h is now in the past (30d > 1h), so skipped becomes 4 (not 5 for the prefix clear).
	if res2[0].Skipped != 4 {
		t.Fatalf("pass 2 skipped = %d, want 4 (prefix clear must not increment skipped)", res2[0].Skipped)
	}

	// Both a%2Fb episodes must resolve at seqClear; lane-2 must remain open.
	var states = map[string]string{}
	var resolvedSeqs = map[string]int64{}
	if err := s2.ReadTx(ctx, func(rd *store.Reader) error {
		rows, err := rd.Query(`SELECT subject_key, state, COALESCE(resolved_seq, 0) FROM alert_episodes`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, st string
			var rs int64
			if err := rows.Scan(&k, &st, &rs); err != nil {
				return err
			}
			states[k] = st
			resolvedSeqs[k] = rs
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if states["a%2Fb/c%25d"] != "resolved" || resolvedSeqs["a%2Fb/c%25d"] != seqClear ||
		states["a%2Fb/other-reason"] != "resolved" || resolvedSeqs["a%2Fb/other-reason"] != seqClear ||
		states["lane-2/lane-branch-held"] != "open" {
		t.Fatalf("unexpected states=%v resolvedSeqs=%v (want seqClear=%d)", states, resolvedSeqs, seqClear)
	}

	// Verify pre-seeding works even in a fresh uncached Evaluator when a pre-existing episode is resolved.
	s3 := watchStore(t)
	e3a := Evaluator{Store: s3, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e3a.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	appendKindEvent(t, s3, "watch-test.refused", now.Add(-time.Hour), `{"lane_id":"x/y","reason":"r%1"}`)
	if _, err := e3a.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	seqClear3 := appendKindEvent(t, s3, "watch-test.restored", now, `{"lane_id":"x/y","reason":null}`)
	e3b := Evaluator{Store: s3, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e3b.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var gotState string
	var gotSeq int64
	if err := s3.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_seq FROM alert_episodes WHERE subject_key='x%2Fy/r%251'`).Scan(&gotState, &gotSeq)
	}); err != nil {
		t.Fatal(err)
	}
	if gotState != "resolved" || gotSeq != seqClear3 {
		t.Fatalf("fresh evaluator pre-seed resolve: state=%q seq=%d, want resolved seq=%d", gotState, gotSeq, seqClear3)
	}
}

func TestDTW50EventPlaceholdersInGuidanceAndReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)

	r := Rule{
		ID:       "ev-guidance",
		Scope:    "core",
		Severity: "warning",
		Enabled:  true,
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When: When{
			GroupBy:   "lane_id",
			Pattern:   PredicateMatch,
			Threshold: 1,
			Window:    time.Hour,
			Fingerprint: []string{
				"detail.check",
			},
		},
		Guidance: Guidance{
			Summary: "lane={{event.lane_id}} nested={{event.detail.check}} int={{event.count}} bool={{event.flag}} seq={{event.seq}} kind={{event.kind}}",
			Check:   []string{"missing={{event.absent}} empty={{event.empty}} null={{event.nullv}} float={{event.floatv}} list1={{event.listv}} listn={{event.multilist}} emptylist={{event.emptylist}} blanklist={{event.blanklist}} nulllist={{event.nulllist}} floatlist={{event.floatlist}} nestedlist={{event.nestedlist}} objlist={{event.objlist}} obj={{event.objv}}"},
			Do:      []string{"invalid={{event.}} bad={{event.a..b}} prefix={{event.event.lane_id}}"},
		},
		Delivery: Delivery{Receiver: "foreground"},
	}

	eval := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	if _, err := eval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendKindEvent(t, s, "watch-test.fact", now.Add(-time.Minute), `{
		"lane_id": "lane-1",
		"detail": {"check": "dirty"},
		"count": 42,
		"flag": true,
		"empty": "",
		"nullv": null,
		"floatv": 3.14,
		"listv": ["a"],
		"multilist": ["a.go", 7, false],
		"emptylist": [],
		"blanklist": [""],
		"nulllist": ["a", null],
		"floatlist": [1, 2.5],
		"nestedlist": [["a"]],
		"objlist": [{"k": 1}],
		"objv": {"k": 1}
	}`)

	if _, err := eval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	var alertID, rawBundle string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id, bundle_json FROM alert_episodes`).Scan(&alertID, &rawBundle)
	}); err != nil {
		t.Fatal(err)
	}

	var b Bundle
	if err := json.Unmarshal([]byte(rawBundle), &b); err != nil {
		t.Fatal(err)
	}
	wantSummary := "lane=lane-1 nested=dirty int=42 bool=true seq=1 kind=watch-test.fact"
	if b.Guidance.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", b.Guidance.Summary, wantSummary)
	}
	wantCheck := "missing=unknown empty=unknown null=unknown float=unknown list1=a listn=a.go, 7, false emptylist=unknown blanklist=unknown nulllist=unknown floatlist=unknown nestedlist=unknown objlist=unknown obj=unknown"
	if len(b.Guidance.Check) != 1 || b.Guidance.Check[0] != wantCheck {
		t.Errorf("check = %v, want [%q]", b.Guidance.Check, wantCheck)
	}
	wantDo := "invalid={{event.}} bad={{event.a..b}} prefix={{event.event.lane_id}}"
	if len(b.Guidance.Do) != 1 || b.Guidance.Do[0] != wantDo {
		t.Errorf("do = %v, want [%q]", b.Guidance.Do, wantDo)
	}

	// Verify TimelineBundle.events remains restricted to seq, ts, kind and value only (D-TW19).
	var rawMap map[string]any
	if err := json.Unmarshal([]byte(rawBundle), &rawMap); err != nil {
		t.Fatal(err)
	}
	evs, _ := rawMap["events"].([]any)
	if len(evs) != 1 {
		t.Fatalf("expected 1 bundle event, got %d", len(evs))
	}
	evObj := evs[0].(map[string]any)
	for k := range evObj {
		if k != "seq" && k != "ts" && k != "kind" && k != "value" {
			t.Errorf("unexpected field %q in bundle event", k)
		}
	}

	// Silence until fingerprint changes and then reopen with a newer event; guidance must re-render from newest event.
	if err := eval.Silence(ctx, alertID, nil, nil); err != nil {
		t.Fatal(err)
	}
	seq2Fact := appendKindEvent(t, s, "watch-test.fact", now, `{"lane_id":"lane-1","detail":{"check":"unlanded"},"count":99,"flag":false}`)
	if _, err := eval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT bundle_json FROM alert_episodes WHERE alert_id=?`, alertID).Scan(&rawBundle)
	}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(rawBundle), &b); err != nil {
		t.Fatal(err)
	}
	wantReopen := "lane=lane-1 nested=unlanded int=99 bool=false seq=4 kind=watch-test.fact"
	if seq2Fact != 4 {
		t.Fatalf("seq2Fact = %d, want 4", seq2Fact)
	}
	if b.Guidance.Summary != wantReopen {
		t.Errorf("reopened summary = %q, want %q", b.Guidance.Summary, wantReopen)
	}

	// When there are no counted events (e.g. lag_behind or empty condition), {{event.<name>}} is left untouched.
	noEventsBundle, err := makeBundle(Rule{
		ID:       "lag",
		Severity: "warning",
		When:     When{Pattern: LagBehind, Leader: "refs/heads/main", Follower: "refs/heads/green"},
		Guidance: Guidance{Summary: "lag {{leader}} {{event.lane_id}}"},
	}, "global", "alrt-1", &condition{lag: &lagCondition{leaderOID: "abcdef1234567890", followerOID: "1234567890abcdef", count: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(noEventsBundle), "{{event.lane_id}}") {
		t.Errorf("expected {{event.lane_id}} untouched with no counted events, got %s", noEventsBundle)
	}

	// In-memory ObservationSource callers passing []string join non-empty strings and fall back on empty/blank slices (D-TW68).
	strSliceBundle, err := makeBundle(Rule{
		ID:       "mem",
		Severity: "warning",
		When:     When{Pattern: ConsecutiveCount, Threshold: 1},
		Guidance: Guidance{
			Summary: "paths={{event.conflict_paths}}",
			Check:   []string{"empty={{event.empty_paths}} blank={{event.blank_paths}}"},
			Do:      []string{"ok"},
		},
	}, "item-1", "alrt-2", &condition{events: []observation{{
		Seq:  1,
		TS:   now.UnixMilli(),
		Kind: "land.landing",
		lookup: func(name string) any {
			switch name {
			case "conflict_paths":
				return []string{"docs/design/tillr-watch.md", "tools/tillr/watch/bundle.go"}
			case "empty_paths":
				return []string{}
			case "blank_paths":
				return []string{"a.go", ""}
			default:
				return nil
			}
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var memB Bundle
	if err := json.Unmarshal(strSliceBundle, &memB); err != nil {
		t.Fatal(err)
	}
	if memB.Guidance.Summary != "paths=docs/design/tillr-watch.md, tools/tillr/watch/bundle.go" {
		t.Errorf("[]string summary = %q", memB.Guidance.Summary)
	}
	if len(memB.Guidance.Check) != 1 || memB.Guidance.Check[0] != "empty=unknown blank=unknown" {
		t.Errorf("[]string check = %v", memB.Guidance.Check)
	}
}
