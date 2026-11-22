package watch

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWatchPredicateAndConsecutivePatterns(t *testing.T) {
	type observationStep struct {
		step    string
		pending int
	}
	for _, tc := range []struct {
		when         string
		observations []observationStep
	}{
		{`pattern = 'predicate_match'
match = 'event.step == "refused"'
clear_when = 'event.step == "accepted"'`, []observationStep{{"other", 0}, {"refused", 1}, {"accepted", 0}}},
		{`pattern = 'consecutive_count'
match = 'event.step == "refused"'
reset = 'event.step == "accepted"'
threshold = 2`, []observationStep{{"refused", 0}, {"accepted", 0}, {"refused", 0}, {"other", 0}, {"refused", 1}, {"accepted", 0}}},
	} {
		pattern, _, _ := strings.Cut(tc.when, "\n")
		t.Run(pattern, func(t *testing.T) {
			now := time.Now()
			s := watchStore(t)
			register(t, "watch-test.fact")
			text := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, `"detail.item-id"`, `"item"`, "pattern = \"count_in_window\"\nthreshold = 1", tc.when).Replace(validRule)
			e := Evaluator{Store: s, Rules: watchRules(t, text), Clock: func() time.Time { return now }}
			ctx := context.Background()
			for _, o := range tc.observations {
				watchFact(t, s, now, `{"item":"i","step":"`+o.step+`"}`)
				if _, err := e.EvaluateRules(ctx); err != nil {
					t.Fatal(err)
				}
				pending, err := e.Pending(ctx, "foreground")
				if err != nil || len(pending) != o.pending {
					t.Fatalf("step %q: pending %d, want %d: %v", o.step, len(pending), o.pending, err)
				}
			}
		})
	}
}
