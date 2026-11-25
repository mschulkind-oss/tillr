package watch

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchFingerprintSchema(t *testing.T) {
	register(t, "test.watch")
	for _, value := range []string{`[]`, `['event.step']`, `['detail..check']`, `[42]`} {
		doc := strings.Replace(validRule, "[rule.when]\n", "[rule.when]\nfingerprint = "+value+"\n", 1)
		if _, err := LoadRuleDocument("test.toml", []byte(doc)); err == nil || !strings.Contains(err.Error(), "when.fingerprint") {
			t.Errorf("fingerprint %s: want a fingerprint refusal, got %v", value, err)
		}
	}
}

func TestWatchFingerprintEncodingAndSource(t *testing.T) {
	for _, pattern := range []string{PredicateMatch, CountInWindow, ConsecutiveCount, AlternatingPair, DurationWithoutEvent} {
		t.Run(pattern, func(t *testing.T) {
			s := watchStore(t)
			now := time.Unix(1700000000, 0)
			w := When{Pattern: pattern, GroupBy: "global", Field: "step", Pairs: [][2]string{{"a", "b"}}, Threshold: 1, Window: time.Hour, ThresholdDuration: time.Second, Fingerprint: []string{"missing", "detail.check", "step", "empty", "number", "bool", "list", "obj", "null"}}
			expr, _ := ParseExpression(`event.step == "b"`)
			if pattern == DurationWithoutEvent {
				w.After = expr
			} else if pattern != AlternatingPair {
				w.Match = expr
				if pattern != PredicateMatch {
					w.Reset, _ = ParseExpression(`event.step == "reset"`)
				}
			}
			r := Rule{ID: "rule", Scope: "core", Enabled: true, Severity: "warning", Reads: Reads{Events: []string{"watch-test.fact"}}, When: w}
			e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
			evaluateLifecycle(t, &e)
			watchFact(t, s, now.Add(-time.Minute), `{"step":"a"}`)
			watchFact(t, s, now.Add(-time.Second), `{"step":"b","detail":{"check":"λ"},"empty":"","number":42,"bool":true,"list":[],"obj":{},"null":null}`)
			if pattern != DurationWithoutEvent {
				// An equal observation is not counted for alternation; event patterns that
				// use match must likewise take the most recent *counted* fact.
				body := `{"step":"noise"}`
				if pattern == AlternatingPair {
					body = `{"step":"b","detail":{"check":"other"}}`
				}
				watchFact(t, s, now, body)
			}
			evaluateLifecycle(t, &e)
			// 0 for missing; tagged two-byte UTF-8 string, one-byte string, empty
			// string; then one zero for each of the five remaining nonstrings.
			encoded := []byte("\x00\x01\x00\x00\x00\x00\x00\x00\x00\x02λ\x01\x00\x00\x00\x00\x00\x00\x00\x01b\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
			var got string
			lifecycleRead(t, s, `SELECT condition_fingerprint FROM alert_episodes`, &got)
			if want := fmt.Sprintf("%x", sha256.Sum256(encoded)); got != want {
				t.Fatalf("fingerprint = %q, want %q", got, want)
			}
		})
	}
}

func evaluateLifecycle(t *testing.T, e *Evaluator) {
	t.Helper()
	if _, err := e.EvaluateRules(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func lifecycleRead(t *testing.T, s *store.Store, query string, dest ...any) {
	t.Helper()
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error { return rd.QueryRow(query).Scan(dest...) }); err != nil {
		t.Fatal(err)
	}
}

func TestWatchEpisodeTimeSilenceAndClearing(t *testing.T) {
	for _, mode := range []string{"still-firing", "cleared", "cleared-and-firing"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.Unix(1700000000, 0)
			s := watchStore(t)
			clear, err := ParseExpression(`event.step == "clear"`)
			if err != nil {
				t.Fatal(err)
			}
			r := Rule{ID: "rule", Scope: "core", Enabled: true, Severity: "warning", Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, ClearWhen: clear, Fingerprint: []string{"step"}}, Delivery: Delivery{Receiver: "foreground"}}
			e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
			evaluateLifecycle(t, &e)
			watchFact(t, s, now, `{"step":"build"}`)
			pending := func() []Notice {
				t.Helper()
				n, err := e.Pending(ctx, "foreground")
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			evaluateLifecycle(t, &e)
			first := pending()
			if len(first) != 1 {
				t.Fatalf("first delivery: %v", first)
			}
			id := first[0].AlertID
			until := "10s"
			// Replacing fingerprint silence with time silence must clear the old terms.
			if err := e.Silence(ctx, id, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := e.Silence(ctx, id, &until, nil); err != nil {
				t.Fatal(err)
			}
			now = now.Add(9 * time.Second)
			watchFact(t, s, now, `{"step":"smoke"}`)
			evaluateLifecycle(t, &e)
			if got := pending(); len(got) != 0 {
				t.Fatalf("time silence reopened early: %v", got)
			}
			if mode != "still-firing" {
				watchFact(t, s, now, `{"step":"clear"}`)
			}
			if mode == "cleared-and-firing" {
				watchFact(t, s, now, `{"step":"build"}`)
			}
			now = now.Add(time.Second)
			evaluateLifecycle(t, &e)
			got := pending() // The old opening was never confirmed.
			switch mode {
			case "still-firing":
				if len(got) != 1 || got[0].AlertID != id || got[0].Seq <= first[0].Seq {
					t.Fatalf("latest delivery: %v (first %v)", got, first)
				}
				var cause string
				lifecycleRead(t, s, `SELECT json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened'`, &cause)
				if cause != "silence-expired" {
					t.Fatalf("reopen cause = %s", cause)
				}
			case "cleared", "cleared-and-firing":
				if mode == "cleared" && len(got) != 0 || mode == "cleared-and-firing" && (len(got) != 1 || got[0].AlertID == id) {
					t.Fatalf("delivery after clearing: %v", got)
				}
				var state, reason string
				var reopened int
				lifecycleRead(t, s, `SELECT state,resolved_reason FROM alert_episodes WHERE episode_num=1`, &state, &reason)
				lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened'`, &reopened)
				if state != "resolved" || reason != "condition-cleared" || reopened != 0 {
					t.Fatalf("cleared state=%s reason=%s reopen events=%d", state, reason, reopened)
				}
			}
		})
	}
}

func TestWatchEpisodeFractionalSilenceDeadline(t *testing.T) {
	ctx := t.Context()
	now := time.Unix(1700000000, 0).UTC()
	s := watchStore(t)
	r := Rule{ID: "rule", Scope: "core", Enabled: true, Severity: "warning", Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour}, Delivery: Delivery{Receiver: "foreground"}}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var id string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes`, &id)
	deadline := now.Add(time.Second + 900*time.Microsecond)
	until := deadline.Format(time.RFC3339Nano)
	if err := e.Silence(ctx, id, &until, nil); err != nil {
		t.Fatal(err)
	}
	var state string
	var count int
	now = deadline.Add(-time.Nanosecond)
	evaluateLifecycle(t, &e)
	lifecycleRead(t, s, `SELECT state FROM alert_episodes`, &state)
	lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened'`, &count)
	if state != "silenced" || count != 0 {
		t.Fatalf("before deadline: state=%s, reopen events=%d", state, count)
	}
	// The deadline column has millisecond resolution; reopen at the first
	// representable instant at or after the requested time.
	now = deadline.Add(100 * time.Microsecond)
	evaluateLifecycle(t, &e)
	lifecycleRead(t, s, `SELECT state FROM alert_episodes`, &state)
	lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened'`, &count)
	if state != "open" || count != 1 {
		t.Fatalf("after deadline: state=%s, reopen events=%d", state, count)
	}
}

func TestWatchEpisodeManualResolveSequence(t *testing.T) {
	for _, lag := range []bool{false, true} {
		t.Run(fmt.Sprintf("lag=%t", lag), func(t *testing.T) {
			ctx := context.Background()
			now := time.Unix(1700000000, 0)
			s := watchStore(t)
			r := Rule{ID: "rule", Scope: "core", Enabled: true, Severity: "warning", Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour}, Delivery: Delivery{Receiver: "foreground"}}
			e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
			if lag {
				root, _, _ := forkedRepo(t, now)
				e.Root = root
				e.Rules[0].Reads = Reads{Projections: []string{"git_refs:" + leaderRef + "," + followerRef}}
				e.Rules[0].When = When{Pattern: LagBehind, GroupBy: "global", Leader: leaderRef, Follower: followerRef, ThresholdCommits: 1}
			} else {
				evaluateLifecycle(t, &e)
			}
			factSeq := watchFact(t, s, now, `{}`)
			evaluateLifecycle(t, &e)
			notices, err := e.Pending(ctx, "foreground")
			if err != nil || len(notices) != 1 {
				t.Fatalf("pending: %v %v", notices, err)
			}
			head := notices[0].Seq
			if err := e.Settle(ctx, notices[0].AlertID, SettleOptions{
				By: "operator", Outcome: "fix-fault", ItemID: "item-1",
				Cause: "investigated", Evidence: "checked logs",
			}); err != nil {
				t.Fatal(err)
			}
			var settledSeq int64
			lifecycleRead(t, s, `SELECT settled_seq FROM alert_episodes`, &settledSeq)
			wantMinSeq := factSeq
			if lag {
				wantMinSeq = head
			}
			if settledSeq < wantMinSeq {
				t.Fatalf("settled_seq = %d, want >= %d", settledSeq, wantMinSeq)
			}
			evaluateLifecycle(t, &e)
			if !lag {
				var st string
				lifecycleRead(t, s, `SELECT state FROM alert_episodes`, &st)
				if st != "open" {
					t.Fatalf("settle should keep same window quiet without reopening: state=%q", st)
				}
				head = watchFact(t, s, now, `{}`)
				evaluateLifecycle(t, &e)
				var reopenEvCount int
				lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened'`, &reopenEvCount)
				if reopenEvCount != 1 {
					t.Fatalf("expected 1 reopen event after new fact past settled_seq, got %d", reopenEvCount)
				}
			} else {
				evaluateLifecycle(t, &e)
				var reopenEvCount int
				lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened'`, &reopenEvCount)
				if reopenEvCount != 0 {
					t.Fatalf("lag rule should not reopen on same lag pair after settle, got %d", reopenEvCount)
				}
				return
			}
			var reopenedSeq int64
			lifecycleRead(t, s, `SELECT last_observed_seq FROM alert_episodes WHERE state='open'`, &reopenedSeq)
			if reopenedSeq != head {
				t.Fatalf("reopened last_observed_seq = %d, want %d", reopenedSeq, head)
			}
		})
	}
}

func TestWatchFingerprintIncrementalCacheAndSharedKind(t *testing.T) {
	register(t, "watch-test.fact")
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	doc := `[[rule]]
id = "by-step"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
threshold = 1
window = "1h"
fingerprint = ["step"]
[rule.guidance]
summary = "step"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "by-check"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "global"
pattern = "count_in_window"
threshold = 1
window = "1h"
fingerprint = ["detail.check"]
[rule.guidance]
summary = "check"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	var queries int
	e := Evaluator{
		Store:        s,
		Rules:        watchRules(t, doc),
		Clock:        func() time.Time { return now },
		onEventQuery: func(string, int64, int) { queries++ },
	}
	writes := 0
	ctx := store.ObserveWrites(t.Context(), func(_, _ time.Time) { writes++ })
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"step":"build","detail":{"check":"lint"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var stepID, stepFP, checkID, checkFP string
	lifecycleRead(t, s, `SELECT alert_id,condition_fingerprint FROM alert_episodes WHERE rule_id='by-step'`, &stepID, &stepFP)
	lifecycleRead(t, s, `SELECT alert_id,condition_fingerprint FROM alert_episodes WHERE rule_id='by-check'`, &checkID, &checkFP)
	if stepFP == "" || checkFP == "" || stepFP == checkFP {
		t.Fatalf("shared kind cache mixed fingerprints: step=%q check=%q", stepFP, checkFP)
	}
	if err := e.Silence(t.Context(), stepID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Settle(t.Context(), checkID, SettleOptions{
		By: "operator", Outcome: "fix-fault", ItemID: "item-1",
		Cause: "handled", Evidence: "checked logs",
	}); err != nil {
		t.Fatal(err)
	}
	// Advance cache past the silence and settle events, then verify an idle pass takes no writer.
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	writes = 0
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if writes != 0 {
		t.Fatalf("quiet pass on silenced and manually-resolved episodes took %d writer transactions", writes)
	}
	// A live override changing when.fingerprint invalidates the rule cache without re-reading events.
	override := strings.Replace(strings.Split(doc, "\n\n")[0], `fingerprint = ["step"]`, `fingerprint = ["detail.check"]`, 1)
	seedLiveOverride(t, s, "by-step", override)
	queries = 0
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if queries != 0 {
		t.Fatalf("fingerprint override re-read %d event queries", queries)
	}
	var state, reopenedFP string
	lifecycleRead(t, s, `SELECT state,condition_fingerprint FROM alert_episodes WHERE alert_id='`+stepID+`'`, &state, &reopenedFP)
	if state != "open" || reopenedFP != checkFP {
		t.Fatalf("after fingerprint override: state=%s fp=%q, want open with %q", state, reopenedFP, checkFP)
	}
}
