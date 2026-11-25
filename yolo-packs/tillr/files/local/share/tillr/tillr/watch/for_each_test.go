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

func TestWatchForEachLoadAndLint(t *testing.T) {
	registerLintTestKinds(t)

	const validForEachConsecutive = `[[rule]]
id = "foreach-consecutive"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
for_each = "processes"
group_by = "process_key"
pattern = "consecutive_count"
match = 'event.role in ["tillr-watch-wait", "tillr-view-serve"] and event.cpu_percent > 50'
threshold = 2
window = "30m"

[rule.guidance]
summary = "Process {{event.role}} (PID {{event.pid}}, {{subject}}) over 50% for {{count}} samples ({{event.cpu_percent}}%, session {{event.session_id}})."
check = ["Inspect {{event.pid}}"]
do = ["Profile {{event.pid}}"]

[rule.delivery]
receiver = "foreground"
`

	const validForEachDotted = `[[rule]]
id = "foreach-dotted"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
for_each = "detail.items"
group_by = "key"
pattern = "predicate_match"
match = 'event.value > 10'
window = "1h"

[rule.guidance]
summary = "Item {{subject}} reached {{event.value}}"
check = ["Check {{subject}}"]
do = ["Investigate {{subject}}"]

[rule.delivery]
receiver = "foreground"
`

	dir := t.TempDir()
	put(t, dir, "consecutive.toml", validForEachConsecutive)
	put(t, dir, "dotted.toml", validForEachDotted)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatalf("LoadRules valid for_each rules: %v", err)
	}
	if len(rules) != 2 || rules[0].When.ForEach != "processes" || rules[1].When.ForEach != "detail.items" {
		t.Fatalf("unexpected ForEach values: %+v", rules)
	}

	cleanRoot := t.TempDir()
	writeLintFile(t, cleanRoot, "config/tillr/watch/consecutive.toml", validForEachConsecutive)
	writeLintFile(t, cleanRoot, "config/tillr/watch/dotted.toml", validForEachDotted)
	findings, err := Lint(context.Background(), LintOptions{Root: cleanRoot})
	if err != nil {
		t.Fatalf("Lint clean for_each rules: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected 0 lint findings, got: %v", findings)
	}

	for _, tc := range []struct {
		name     string
		doc      string
		lintCode string
		lintSub  string
	}{
		{
			name: "lag_behind_refuses_for_each",
			doc: `[[rule]]
id = "bad-lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]
[rule.when]
for_each = "processes"
group_by = "global"
pattern = "lag_behind"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 1
[rule.guidance]
summary = "lag"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`,
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "event_prefix_refused",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "event.processes"`, 1),
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "global_refused",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "global"`, 1),
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "kind_refused",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "kind"`, 1),
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "seat_refused",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "seat"`, 1),
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "object_type_refused",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "object_type"`, 1),
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "object_id_refused",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "object_id"`, 1),
			lintCode: CodeWatchLintRuleSchema,
			lintSub:  "when.for_each",
		},
		{
			name:     "seq_forbidden_column",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "seq"`, 1),
			lintCode: CodeWatchLintForbiddenRemedy,
			lintSub:  "when.for_each",
		},
		{
			name:     "ts_forbidden_column",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "ts"`, 1),
			lintCode: CodeWatchLintForbiddenRemedy,
			lintSub:  "when.for_each",
		},
		{
			name:     "revision_forbidden_column",
			doc:      strings.Replace(validForEachConsecutive, `for_each = "processes"`, `for_each = "revision"`, 1),
			lintCode: CodeWatchLintForbiddenRemedy,
			lintSub:  "when.for_each",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			badDir := t.TempDir()
			put(t, badDir, "bad.toml", tc.doc)
			if _, err := LoadRules(badDir); err == nil {
				t.Fatalf("LoadRules unexpectedly accepted %s", tc.name)
			}
			lintRoot := t.TempDir()
			writeLintFile(t, lintRoot, "config/tillr/watch/bad.toml", tc.doc)
			got, err := Lint(context.Background(), LintOptions{Root: lintRoot})
			if err != nil {
				t.Fatalf("Lint %s: %v", tc.name, err)
			}
			matched := false
			for _, f := range got {
				if f.Code == tc.lintCode && strings.Contains(f.Detail, tc.lintSub) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("expected lint finding (%s, %q), got: %v", tc.lintCode, tc.lintSub, got)
			}
		})
	}
}

func TestWatchForEachFanOutSpoofProtectionSkippedAndAbsentReset(t *testing.T) {
	registerLintTestKinds(t)
	ctx := context.Background()

	const ruleTOML = `[[rule]]
id = "foreach-cpu"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.refusal"]

[rule.when]
for_each = "processes"
group_by = "process_key"
pattern = "consecutive_count"
match = 'event.role in ["tillr-watch-wait", "tillr-view-serve"] and event.cpu_percent > 50 and event.object_type == "session"'
threshold = 2
window = "30m"

[rule.guidance]
summary = "Process {{event.role}} (PID {{event.pid}}, {{subject}}) at {{event.cpu_percent}}% in {{event.session_id}} ({{count}})."
check = ["Inspect {{event.pid}}"]
do = ["Profile {{event.pid}}"]

[rule.delivery]
receiver = "foreground"
`

	dir := t.TempDir()
	put(t, dir, "cpu.toml", ruleTOML)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	rule := rules[0]

	// Verify definitionHash preserves exact hashes when ForEach == "" and changes when ForEach is set.
	plainRule := rule
	plainRule.When.ForEach = ""
	rawPlain, err := json.Marshal(struct {
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
	}{
		Scope:     plainRule.Scope,
		Events:    plainRule.Reads.Events,
		GroupBy:   plainRule.When.GroupBy,
		Pattern:   plainRule.When.Pattern,
		Threshold: plainRule.When.Threshold,
		Window:    int64(plainRule.When.Window),
		Match:     plainRule.When.Match.source,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPlainSum := sha256.Sum256(rawPlain)
	if got, want := definitionHash(plainRule), hex.EncodeToString(wantPlainSum[:]); got != want {
		t.Fatalf("definitionHash with empty ForEach changed existing digest: got %s, want %s", got, want)
	}
	if definitionHash(rule) == definitionHash(plainRule) {
		t.Fatal("definitionHash did not change when ForEach was set")
	}
	if ruleEqual(rule, plainRule) {
		t.Fatal("ruleEqual must distinguish ForEach")
	}

	// 1. Store evaluation with warm evalCache: parent field inheritance, store-column spoof protection,
	// skipped accounting, and absent-subject resolution with resolved_seq.
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	s, err := store.Open(ctx, t.TempDir()+"/state.db", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	e := Evaluator{Store: s, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendEv := func(at time.Time, payload map[string]any) int64 {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		var seq int64
		sess := "sess-1"
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			var err error
			seq, err = tx.Append(event.Event{
				Kind:       "drain.refusal",
				ObjectType: "session",
				ObjectID:   &sess,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return seq
	}

	// Sample 1: element tries to spoof object_type="spoofed"; parent object_type="session" must win.
	// Also includes a non-object element and a missing-key element (both counted in Skipped).
	appendEv(now, map[string]any{
		"session_id": "sess-1",
		"processes": []any{
			map[string]any{"process_key": "tillr-watch-wait:10:100", "role": "tillr-watch-wait", "pid": 10, "cpu_percent": 133, "object_type": "spoofed"},
			"not-an-object",
			map[string]any{"role": "tillr-watch-wait", "pid": 11, "cpu_percent": 90},
		},
	})
	// Non-list for_each field increments Skipped without resetting the streak.
	appendEv(now.Add(30*time.Second), map[string]any{
		"session_id": "sess-1",
		"processes":  "not-a-list",
	})
	// Sample 2: second consecutive sample opens the episode.
	now = now.Add(time.Minute)
	appendEv(now, map[string]any{
		"session_id": "sess-1",
		"processes": []any{
			map[string]any{"process_key": "tillr-watch-wait:10:100", "role": "tillr-watch-wait", "pid": 10, "cpu_percent": 140},
		},
	})

	results, err := e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Skipped != 3 {
		t.Fatalf("expected Skipped=3 (1 non-object + 1 missing key + 1 non-list), got %+v", results)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Bundle.Subject != "tillr-watch-wait:10:100" {
		t.Fatalf("expected 1 open alert for tillr-watch-wait:10:100, got %+v", notices)
	}
	if got := notices[0].Bundle.Guidance.Summary; !strings.Contains(got, "tillr-watch-wait") || !strings.Contains(got, "PID 10") || !strings.Contains(got, "140%") || !strings.Contains(got, "sess-1") {
		t.Fatalf("unexpected rendered summary: %q", got)
	}

	// Sample 3: empty processes[] clears the open episode immediately with resolved_seq == exitSeq.
	now = now.Add(time.Minute)
	exitSeq := appendEv(now, map[string]any{
		"session_id": "sess-1",
		"processes":  []any{},
	})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
		t.Fatalf("expected 0 pending alerts after empty processes[], got %+v (err=%v)", pending, err)
	}
	var state, reason string
	var resolvedSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE rule_id=? AND subject_key=?`,
			rule.ID, "tillr-watch-wait:10:100").Scan(&state, &reason, &resolvedSeq)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" || reason != "condition-cleared" || resolvedSeq != exitSeq {
		t.Fatalf("resolved row = (%q, %q, %d), want (resolved, condition-cleared, %d)", state, reason, resolvedSeq, exitSeq)
	}

	// 2. Uncached TillrStoreSource.deriveRule pre-seeding check.
	now = now.Add(time.Minute)
	appendEv(now, map[string]any{
		"session_id": "sess-1",
		"processes": []any{
			map[string]any{"process_key": "tillr-view-serve:20:200", "role": "tillr-view-serve", "pid": 20, "cpu_percent": 120},
		},
	})
	now = now.Add(time.Minute)
	appendEv(now, map[string]any{
		"session_id": "sess-1",
		"processes": []any{
			map[string]any{"process_key": "tillr-view-serve:20:200", "role": "tillr-view-serve", "pid": 20, "cpu_percent": 125},
		},
	})
	// Cold evaluator (nil cache) opens episode.
	cold := Evaluator{Store: s, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
	if _, err := cold.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	exitSeq2 := appendEv(now, map[string]any{
		"session_id": "sess-1",
		"processes":  []any{},
	})
	ts := NewTillrStoreSource(s)
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		subjects, _, err := ts.deriveRule(rd, rule, now, gitRuleFacts{}, 0)
		if err != nil {
			return err
		}
		c := subjects["tillr-view-serve:20:200"]
		if c == nil || len(c.events) != 0 || len(c.clears) == 0 || c.clears[len(c.clears)-1] != exitSeq2 {
			t.Fatalf("uncached deriveRule did not record absent-subject clear at seq %d: %+v", exitSeq2, c)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 3. ObservationSource (memoryState) absent-subject resolution.
	src := &stubObservationSource{epoch: 1}
	srcNow := time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC)
	memRule := rule
	memRule.Scope = "*"
	memEval := Evaluator{Source: src, Rules: []Rule{memRule}, Clock: func() time.Time { return srcNow }}
	if _, err := memEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	src.records = append(src.records,
		Observation{
			Seq:  1,
			TS:   srcNow.UnixMilli(),
			Kind: "drain.refusal",
			Fields: map[string]any{
				"session_id":  "sess-mem",
				"object_type": "session",
				"processes": []map[string]any{
					{"process_key": "tillr-watch-wait:30:300", "role": "tillr-watch-wait", "pid": 30, "cpu_percent": 133},
				},
			},
		},
		Observation{
			Seq:  2,
			TS:   srcNow.Add(time.Minute).UnixMilli(),
			Kind: "drain.refusal",
			Fields: map[string]any{
				"session_id":  "sess-mem",
				"object_type": "session",
				"processes": []map[string]any{
					{"process_key": "tillr-watch-wait:30:300", "role": "tillr-watch-wait", "pid": 30, "cpu_percent": 135},
				},
			},
		},
	)
	srcNow = srcNow.Add(time.Minute)
	memRes, err := memEval.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	memNotices, err := memEval.Pending(ctx, "foreground")
	if err != nil || len(memNotices) != 1 || memNotices[0].Bundle.Subject != "tillr-watch-wait:30:300" {
		t.Fatalf("expected 1 open alert from ObservationSource, got %+v (results=%+v, err=%v)", memNotices, memRes, err)
	}
	srcNow = srcNow.Add(time.Minute)
	src.records = append(src.records, Observation{
		Seq:  3,
		TS:   srcNow.UnixMilli(),
		Kind: "drain.refusal",
		Fields: map[string]any{
			"session_id":  "sess-mem",
			"object_type": "session",
			"processes":   []map[string]any{},
		},
	})
	if _, err := memEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	memNotices, err = memEval.Pending(ctx, "foreground")
	if err != nil || len(memNotices) != 0 {
		t.Fatalf("expected 0 open alerts from ObservationSource after absent process, got %+v (err=%v)", memNotices, err)
	}
}

type stubObservationSource struct {
	epoch   int64
	records []Observation
}

func (s *stubObservationSource) ReadWindow(_ context.Context, sinceMillis int64, kinds []string) ([]Observation, SourceCursor, SkippedLines, error) {
	var out []Observation
	var maxSeq int64
	for _, r := range s.records {
		if r.Seq > maxSeq {
			maxSeq = r.Seq
		}
		if r.TS >= sinceMillis {
			out = append(out, r)
		}
	}
	return out, SourceCursor{Epoch: s.epoch, Seq: maxSeq}, 0, nil
}

func (s *stubObservationSource) Project(_ context.Context, _ []string) (map[string]any, error) {
	return map[string]any{}, nil
}

func (s *stubObservationSource) WatchTargets() ([]string, time.Duration) {
	return nil, time.Second
}
