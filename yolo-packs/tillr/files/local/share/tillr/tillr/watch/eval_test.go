package watch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func watchStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func watchFact(t *testing.T, s *store.Store, at time.Time, body string) int64 {
	t.Helper()
	const kind = "watch-test.fact"
	if err := event.Register(kind, "watch-test"); err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) (err error) {
		seq, err = tx.Append(event.Event{Kind: kind, At: at, Payload: []byte(body)})
		return
	}); err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestWatchWindowAndSkipped(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := watchStore(t)
	for _, f := range []struct {
		age  time.Duration
		json string
	}{
		{61 * time.Minute, `{"item":"x","step":"plan"}`},
		{59 * time.Minute, `{"item":"x","step":"plan"}`},
		{10 * time.Minute, `{"item":7,"step":"other"}`},
		{9 * time.Minute, `{"step":"other"}`},
		{time.Minute, `{"item":"x","step":"design"}`},
	} {
		watchFact(t, s, now.Add(-f.age), f.json)
	}
	r := Rule{ID: "bounce", Scope: "core", Severity: "warning", Enabled: true, Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{Pattern: AlternatingPair, GroupBy: "item", Field: "step", Pairs: [][2]string{{"plan", "design"}}, Threshold: 2, Window: time.Hour}, Guidance: Guidance{Summary: "{{subject}} {{count}} {{elapsed}} {{pair_a}}/{{pair_b}}"}, Delivery: Delivery{Receiver: "foreground"}}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	counts, err := e.Skipped(ctx)
	if err != nil || counts[r.ID] != 2 {
		t.Fatalf("skipped: %v %v", counts, err)
	}
	if head, err := s.EventHeadSeq(); err != nil || head != 5 {
		t.Fatalf("status wrote facts: %d %v", head, err)
	}
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("notices: %v %v", notices, err)
	}
	if got := notices[0].Bundle.Guidance.Summary; got != "x 2 58m plan/design" {
		t.Fatal(got)
	}
}

func TestWatchQuotedJSONPaths(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := watchStore(t)
	for _, body := range []string{
		`{"details":{"item name":"a","step name":"plan"}}`,
		`{"details":{"item name":"b","step name":"design"}}`,
		`{"details":{"item name":"a","step name":"design"}}`,
	} {
		watchFact(t, s, now, body)
	}
	dir := t.TempDir()
	text := strings.NewReplacer(
		`"test.watch"`, `"watch-test.fact"`,
		`"detail.item-id"`, `"details.item name"`,
		`pattern = "count_in_window"`, "pattern = 'alternating_pair'\nfield = 'details.step name'\npairs = [['plan', 'design']]",
		`threshold = 1`, `threshold = 2`,
	).Replace(validRule)
	put(t, dir, "alternation.toml", text)
	rules, err := LoadRules(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	counts, err := e.Evaluate(ctx)
	if err != nil || counts["case"] != 0 {
		t.Fatalf("skipped: %v %v", counts, err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "a" || notices[0].Bundle.Guidance.Summary != "Found 2" {
		t.Fatalf("expected only subject a to reach the threshold: %v %v", notices, err)
	}
}

func TestWatchAlternatingPairFiresOnFourthObservation(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := watchStore(t)
	r := Rule{ID: "bounce", Scope: "core", Severity: "warning", Enabled: true, Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{Pattern: AlternatingPair, GroupBy: "global", Field: "step", Pairs: [][2]string{{"plan", "design"}}, Threshold: 4, Window: time.Hour}, Guidance: Guidance{Summary: "{{count}} observations"}, Delivery: Delivery{Receiver: "foreground"}}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	for i, step := range []string{"plan", "design", "design", "plan", "design"} {
		watchFact(t, s, now, fmt.Sprintf(`{"step":%q}`, step))
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if i < 4 {
			if len(notices) != 0 {
				t.Fatalf("fired early at event %d: %v", i+1, notices)
			}
		} else if len(notices) != 1 || notices[0].Bundle.Guidance.Summary != "4 observations" {
			t.Fatalf("expected one notice on the fourth distinct observation: %v", notices)
		}
	}
}

func TestWatchAlternationIgnoresMissingOrNullField(t *testing.T) {
	for _, tc := range []struct {
		name, middle string
		want         int
	}{
		{"missing", `{}`, 1},
		{"missing-but-clears", `{"clear":true}`, 0},
		{"outside-pair", `{"step":"verify"}`, 0},
		{"present-null", `{"step":null}`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			s := watchStore(t)
			clear, err := ParseExpression(`event.clear == true`)
			if err != nil {
				t.Fatal(err)
			}
			r := Rule{ID: "bounce", Scope: "core", Severity: "warning", Enabled: true, Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{Pattern: AlternatingPair, GroupBy: "global", Field: "step", Pairs: [][2]string{{"plan", "design"}}, Threshold: 2, Window: time.Hour, ClearWhen: clear}, Delivery: Delivery{Receiver: "foreground"}}
			for _, body := range []string{`{"step":"plan"}`, tc.middle, `{"step":"design"}`} {
				watchFact(t, s, now, body)
			}
			e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
			if _, err := e.Evaluate(ctx); err != nil {
				t.Fatal(err)
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil || len(notices) != tc.want {
				t.Fatalf("notices: got %v (%v), want %d", notices, err, tc.want)
			}
		})
	}
}

func TestWatchEpisodesResolveAndReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := watchStore(t)
	match, err := ParseExpression(`event.failure == true`)
	if err != nil {
		t.Fatal(err)
	}
	r := Rule{ID: "failure", Scope: "core", Severity: "warning", Enabled: true, Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{GroupBy: "global", Pattern: ConsecutiveCount, Threshold: 2, Window: time.Hour, Match: match}, Delivery: Delivery{Receiver: "foreground"}}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	appendFailures := func(n int) {
		for i := 0; i < n; i++ {
			watchFact(t, s, now, `{"failure":true}`)
		}
	}
	eval := func() {
		t.Helper()
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	appendFailures(2)
	eval()
	eval()
	appendFailures(1)
	eval()
	resetSeq := watchFact(t, s, now, `{"failure":false}`)
	appendFailures(2)
	eval()
	err = s.ReadTx(ctx, func(rd *store.Reader) error {
		rows, err := rd.Query(`SELECT episode_num,state,observation_count,COALESCE(resolved_seq,0),condition_fingerprint FROM alert_episodes ORDER BY episode_num`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var n, count int
			var seq int64
			var state, fingerprint string
			if err := rows.Scan(&n, &state, &count, &seq, &fingerprint); err != nil {
				return err
			}
			got = append(got, fmt.Sprintf("%d:%s:%d:%d:%s", n, state, count, seq, fingerprint))
		}
		if want := fmt.Sprintf("[1:resolved:2:%d: 2:open:1:0:]", resetSeq); fmt.Sprint(got) != want {
			t.Errorf("episodes: %v, want %s", got, want)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Events(ctx, resetSeq+2)
	if err != nil || len(events) != 2 || events[0].Kind != "watch.alert.resolved" || events[1].Kind != "watch.alert.opened" {
		t.Fatalf("events: %v %v", events, err)
	}
	now = now.Add(time.Hour + time.Second)
	eval()
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("after expiry: %v %v", notices, err)
	}
}
