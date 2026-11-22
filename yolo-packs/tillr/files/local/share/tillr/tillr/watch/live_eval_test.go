package watch

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func seedLiveOverride(t *testing.T, s *store.Store, id, doc string) {
	t.Helper()
	if err := s.WriteTx(t.Context(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT OR REPLACE INTO watch_rule_overrides (rule_id,rule_toml,set_by,set_at) VALUES (?,?,'fixture',0)`, id, doc)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchEvaluationOnlyWritesChangedEpisodes(t *testing.T) {
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	watchFact(t, s, now.Add(-24*time.Hour), `{"detail":{"item-id":"one"}}`)
	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
	writes := 0
	ctx := store.ObserveWrites(t.Context(), func(_, _ time.Time) { writes++ })
	pass := func(want int) {
		t.Helper()
		writes = 0
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if writes != want {
			t.Errorf("evaluation took %d writer transactions, want %d", writes, want)
		}
	}
	pass(1) // Initial baseline registration.
	pass(0) // Quiet.
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	pass(1) // Open.
	pass(0) // Same firing condition and observations.
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	pass(1) // New observation.
	pass(0)
	now = now.Add(24 * time.Hour)
	pass(1) // Window expired.
	pass(0)
}

func TestWatchEvaluationDiscardsStaleEpisodePlan(t *testing.T) {
	register(t, "watch-test.fact")
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	head := watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	plans := 0
	e.afterPlan = func() {
		if store.HoldsWrite() {
			t.Fatal("plan must run without a writer")
		}
		plans++
		if plans == 1 {
			seedLiveOverride(t, s, e.Rules[0].ID, strings.Replace(doc, "[rule.reads]", "enabled=false\n[rule.reads]", 1))
		} else if plans > 2 {
			t.Fatalf("unexpected plan retry %d", plans)
		}
	}
	got, err := e.EvaluateRules(t.Context())
	if plans < 1 || err != nil || len(got) != 1 || !got[0].Disabled {
		t.Fatalf("did not discard stale opening: plans=%d results=%+v err=%v", plans, got, err)
	}
	if after, err := s.EventHeadSeq(); after != head || err != nil {
		t.Fatalf("stale opening appended a transition: before=%d after=%d err=%v", head, after, err)
	}
	if pending, err := e.Pending(t.Context(), "foreground"); len(pending) != 0 || err != nil {
		t.Fatalf("stale opening left a notice: %v %v", pending, err)
	}
}

func TestWatchEvaluationRestartsForNewGitFacts(t *testing.T) {
	now := time.Unix(1700000000, 0)
	root, _, _ := forkedRepo(t, now)
	lag := func(leader, follower string) string {
		return fmt.Sprintf(`[[rule]]
id = "lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:%s,%s"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = %q
follower = %q
threshold_commits = 1
[rule.guidance]
summary = "{{subject}} behind"
check = ["look"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`, leaderRef, followerRef, leader, follower)
	}
	for _, pairOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("pair-only=%t", pairOnly), func(t *testing.T) {
			s := watchStore(t)
			firstHead := watchFact(t, s, now, `{}`)
			e := Evaluator{Store: s, Root: root, Clock: func() time.Time { return now }}
			if pairOnly {
				// Both refs are already read, but their reverse lag is not.
				e.Rules = watchRules(t, lag(followerRef, leaderRef))
			}
			var retryHead int64
			passes := 0
			e.afterGit = func() {
				passes++
				switch passes {
				case 1:
					seedLiveOverride(t, s, "lag", lag(leaderRef, followerRef))
					retryHead = watchFact(t, s, now, `{}`)
				case 2:
					if head, err := s.EventHeadSeq(); head != retryHead || head != firstHead+1 || err != nil {
						t.Fatalf("abandoned pass wrote events: head=%d, retry head=%d, err=%v", head, retryHead, err)
					}
					if err := s.ReadTx(t.Context(), func(rd *store.Reader) error {
						var n int
						if err := rd.QueryRow(`SELECT count(*) FROM alert_episodes`).Scan(&n); err != nil || n != 0 {
							t.Fatalf("abandoned pass left %d episodes: %v", n, err)
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
					watchFact(t, s, now, `{}`)
				default:
					t.Fatal("unexpected third pass")
				}
			}
			got, err := e.EvaluateRules(t.Context())
			if err != nil || len(got) != 1 || got[0].Diagnostic() != "" || passes != 2 {
				t.Fatalf("results=%+v err=%v passes=%d", got, err, passes)
			}
			if err := s.ReadTx(t.Context(), func(rd *store.Reader) error {
				var opened, observed int64
				if err := rd.QueryRow(`SELECT opened_seq,last_observed_seq FROM alert_episodes`).Scan(&opened, &observed); err != nil || opened != retryHead || observed != retryHead {
					t.Fatalf("retry used the wrong event head: opened=%d observed=%d want=%d err=%v", opened, observed, retryHead, err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWatchEvaluationAppliesDisabledOverrideFromSecondRead(t *testing.T) {
	register(t, "watch-test.fact")
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	if _, err := e.EvaluateRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	head, err := s.EventHeadSeq()
	if err != nil || head != 2 {
		t.Fatalf("first pass did not open an episode: head=%d: %v", head, err)
	}
	passes := 0
	e.afterGit = func() {
		passes++
		if passes > 1 {
			t.Fatal("unexpected retry")
		}
		seedLiveOverride(t, s, e.Rules[0].ID, strings.Replace(doc, "[rule.reads]", "enabled=false\n[rule.reads]", 1))
	}
	got, err := e.EvaluateRules(t.Context())
	if err != nil || len(got) != 1 || !got[0].Disabled {
		t.Fatalf("second read ignored override: results=%+v, err=%v", got, err)
	}
	assertWatchResolutionEvent(t, s, head)
}

func TestWatchEvaluationValidatesSecondRuleRead(t *testing.T) {
	const missing = "refs/heads/absent"
	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`)
	gitDoc := strings.Replace(doc, "[rule.when]", "projections=['git_refs:"+missing+"']\n[rule.when]", 1)
	now := time.Unix(1700000000, 0)
	for _, tc := range []struct {
		name, before, after, errorText string
	}{
		{"malformed", doc, "[[bad", "stored rule"},
		{"missing-root", doc, gitDoc, "requires --root"},
		{"unreadable-counts-as-read", gitDoc, gitDoc, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := watchStore(t)
			head := watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
			e := Evaluator{Store: s, Rules: watchRules(t, tc.before), Clock: func() time.Time { return now }}
			if tc.errorText == "" {
				e.Root, _, _ = forkedRepo(t, now)
			}
			passes := 0
			e.afterGit = func() {
				passes++
				if passes > 1 {
					t.Fatal("unexpected retry")
				}
				seedLiveOverride(t, s, e.Rules[0].ID, tc.after)
			}
			got, err := e.EvaluateRules(t.Context())
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) || got != nil {
					t.Fatalf("results=%+v err=%v, want refusal %q", got, err, tc.errorText)
				}
			} else if err != nil || len(got) != 1 || !strings.Contains(got[0].Unreadable, missing) {
				t.Fatalf("results=%+v err=%v, want unreadable ref", got, err)
			}
			if after, err := s.EventHeadSeq(); after != head || err != nil {
				t.Fatalf("unexpected writes: before=%d after=%d err=%v", head, after, err)
			}
		})
	}
}
