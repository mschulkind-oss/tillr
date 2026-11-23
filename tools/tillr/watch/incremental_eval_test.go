package watch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func watchEventOfKind(t *testing.T, s *store.Store, kind string, at time.Time, body string) int64 {
	t.Helper()
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

func TestWatchEvaluationContinuesFromLastEventWithoutRereadingHistory(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := t.Context()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	doc := `[[rule]]
id = "count-1h"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "detail.item-id"
pattern = "count_in_window"
threshold = 3
window = "1h"
[rule.guidance]
summary = "{{subject}} hit {{count}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "count-10m"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "detail.item-id"
pattern = "count_in_window"
threshold = 3
window = "10m"
[rule.guidance]
summary = "{{subject}} hit {{count}} in 10m"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)
	if _, err := (&Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}).EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	// Seed historical events: 10 outside the 1h window, 8 inside for other
	// subjects, and 2 inside for "item-a" (below threshold 3).
	for i := 0; i < 10; i++ {
		watchFact(t, s, now.Add(-2*time.Hour), fmt.Sprintf(`{"detail":{"item-id":"old-%d"}}`, i))
	}
	for i := 0; i < 8; i++ {
		watchFact(t, s, now.Add(-30*time.Minute), fmt.Sprintf(`{"detail":{"item-id":"other-%d"}}`, i))
	}
	watchFact(t, s, now.Add(-20*time.Minute), `{"detail":{"item-id":"item-a"}}`)
	headAfterSeed := watchFact(t, s, now.Add(-5*time.Minute), `{"detail":{"item-id":"item-a"}}`)

	type eventQuery struct {
		kind     string
		sinceSeq int64
		rows     int
	}
	var queries []eventQuery
	e := Evaluator{
		Store: s,
		Rules: rules,
		Clock: func() time.Time { return now },
		onEventQuery: func(kind string, sinceSeq int64, rows int) {
			queries = append(queries, eventQuery{kind: kind, sinceSeq: sinceSeq, rows: rows})
		},
	}

	// Pass 1 (cold): loads watch-test.fact once for both rules from seq 0.
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].kind != "watch-test.fact" || queries[0].sinceSeq != 0 || queries[0].rows != 10 {
		t.Fatalf("cold pass queries = %+v, want 1 query for watch-test.fact from seq 0 reading 10 in-window rows", queries)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("pending after cold pass: %+v, %v", notices, err)
	}

	// Pass 2 (idle, no new events): must not query the events table at all.
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 0 {
		t.Fatalf("idle pass re-read events: %+v", queries)
	}

	// Pass 3 (unrelated event appended): seeks only after the last seen seq and reads 0 rows.
	headAfterUnrelated := watchEventOfKind(t, s, "watch-test.other", now, `{"note":"unrelated"}`)
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].sinceSeq != headAfterSeed || queries[0].rows != 0 {
		t.Fatalf("unrelated-event pass queries = %+v, want 1 tail query since %d reading 0 rows", queries, headAfterSeed)
	}

	// Pass 4 (idle after unrelated event): 0 queries.
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 0 {
		t.Fatalf("idle pass after unrelated event re-read events: %+v", queries)
	}

	// Pass 5 (1 new matching event for item-a): reads ONLY the 1 new row, continues
	// from cached history, and opens count-1h with all 3 observations.
	watchFact(t, s, now, `{"detail":{"item-id":"item-a"}}`)
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].sinceSeq != headAfterUnrelated || queries[0].rows != 1 {
		t.Fatalf("new-event pass queries = %+v, want 1 tail query since %d reading 1 row", queries, headAfterUnrelated)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.RuleID != "count-1h" || notices[0].Bundle.Subject != "item-a" || len(notices[0].Bundle.Events) != 3 {
		t.Fatalf("pending after 3rd event: %+v, %v", notices, err)
	}

	// Advance past the watch.alert.opened event appended by Pass 5 (reads 0 watch-test.fact rows).
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].rows != 0 {
		t.Fatalf("post-open pass queries = %+v, want 1 tail query reading 0 rows", queries)
	}

	// Pass 6 (clock advances past 1h window, no new events): 0 event queries, alert resolves.
	now = now.Add(2 * time.Hour)
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 0 {
		t.Fatalf("window-expiry pass with no new events re-read events: %+v", queries)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("pending after window expiry: %+v, %v", notices, err)
	}

	// Pass 7 (live override lowers threshold with no new events): evaluates from cache with 0 queries.
	watchFact(t, s, now, `{"detail":{"item-id":"item-b"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	override := `[[rule]]
id = "count-1h"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "detail.item-id"
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.guidance]
summary = "{{subject}} hit {{count}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	seedLiveOverride(t, s, "count-1h", override)
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 0 {
		t.Fatalf("override pass with no new events re-read events: %+v", queries)
	}
	// Under D-TW66, lowering threshold does not open an alert on pre-change events.
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 0 {
		t.Fatalf("pending after override on pre-change event: %+v, %v", notices, err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"item-b"}}`)
	queries = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].rows != 1 {
		t.Fatalf("post-override event pass queries = %+v, want 1 tail query reading 1 row", queries)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "item-b" {
		t.Fatalf("pending after post-override event: %+v, %v", notices, err)
	}
}

func TestWatchEvaluationReusesGitRefsAndLagWithoutSubprocesses(t *testing.T) {
	ctx := t.Context()
	now := time.Unix(1700000000, 0)
	root, leader, _ := forkedRepo(t, now)
	s := watchStore(t)
	doc := `[[rule]]
id = "lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
[rule.guidance]
summary = "{{subject}} behind {{count}}"
check = ["look"]
do = ["consider"]
[rule.delivery]
receiver = "foreground"
`
	var gitCalls [][]string
	e := Evaluator{
		Store: s,
		Root:  root,
		Rules: watchRules(t, doc),
		Clock: func() time.Time { return now },
		onGitExec: func(args []string) {
			gitCalls = append(gitCalls, append([]string(nil), args...))
		},
	}

	// Pass 1 (cold): peels both refs and computes lag.
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gitCalls) == 0 {
		t.Fatal("cold pass expected git subprocesses")
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("pending after cold pass: %+v, %v", notices, err)
	}

	// Pass 2 (idle, loose refs unchanged): 0 git subprocesses.
	gitCalls = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gitCalls) != 0 {
		t.Fatalf("idle pass spawned git subprocesses: %v", gitCalls)
	}

	// Pass 3 (packed-refs, still unchanged): 0 git subprocesses.
	fixtureGit(t, root, "", now, "pack-refs", "--all")
	gitCalls = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gitCalls) != 0 {
		t.Fatalf("packed-refs idle pass spawned git subprocesses: %v", gitCalls)
	}

	// Pass 4 (follower catches up to leader): resolves alert without spawning git.
	fixtureGit(t, root, "", now, "update-ref", followerRef, leader)
	gitCalls = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gitCalls) != 0 {
		t.Fatalf("ref catch-up spawned git subprocesses: %v", gitCalls)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("pending after catch-up: %+v, %v", notices, err)
	}

	// Pass 5 (new commit on leader): detects new OID, peels and computes lag, opens alert.
	tree := fixtureGit(t, root, "", now, "mktree")
	newer := fixtureGit(t, root, "newer", now, "commit-tree", tree, "-p", leader)
	fixtureGit(t, root, "", now, "update-ref", leaderRef, newer)
	gitCalls = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gitCalls) == 0 {
		t.Fatal("new commit pass expected git subprocesses")
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 {
		t.Fatalf("pending after new commit: %+v, %v", notices, err)
	}

	// Pass 6 (idle after new commit): 0 git subprocesses.
	gitCalls = nil
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if len(gitCalls) != 0 {
		t.Fatalf("idle pass after new commit spawned git subprocesses: %v", gitCalls)
	}
}

func TestMultiKindIncrementalPassBatchesTailQueryAndCachesCompositeGroupBy(t *testing.T) {
	ctx := t.Context()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	var doc string
	for i := 0; i < 8; i++ {
		kind := fmt.Sprintf("watch-test.multi-%d", i)
		register(t, kind)
		doc += fmt.Sprintf(`[[rule]]
id = "multi-%d"
scope = "core"
severity = "warning"
[rule.reads]
events = [%q]
[rule.when]
group_by = ["detail.item_id", "detail.step"]
pattern = "count_in_window"
threshold = 2
window = "1h"
[rule.guidance]
summary = "{{subject}} hit {{count}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"

`, i, kind)
	}
	rules := watchRules(t, doc)
	if _, err := (&Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}).EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	headAfterSeed := watchEventOfKind(t, s, "watch-test.multi-0", now.Add(-5*time.Minute), `{"detail":{"item_id":"item-1","step":"build"}}`)

	var queries int
	e := Evaluator{
		Store: s,
		Rules: rules,
		Clock: func() time.Time { return now },
		onEventQuery: func(string, int64, int) {
			queries++
		},
	}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if queries != 8 {
		t.Fatalf("cold pass queries = %d, want 8", queries)
	}

	// Appending one unrelated event must advance all 8 synced kinds in a single
	// tail query instead of issuing 8 per-kind SQL queries on every commit.
	watchEventOfKind(t, s, "watch-test.unrelated-tail", now, `{"note":"tick"}`)
	queries = 0
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if queries != 1 {
		t.Fatalf("unrelated commit executed %d event queries across 8 kinds (since %d), want 1 batched tail query", queries, headAfterSeed)
	}

	// Composite group_by rule still fires when threshold is reached incrementally,
	// and Pending ignores already-seen open episodes while delivering new ones.
	seenHead := watchEventOfKind(t, s, "watch-test.unrelated-tail", now.Add(time.Second), `{"note":"seen"}`)
	if err := e.Shown(ctx, "operator-session", seenHead); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "operator-session", seenHead); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	watchEventOfKind(t, s, "watch-test.multi-0", now, `{"detail":{"item_id":"item-1","step":"build"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "operator-session")
	if err != nil || len(notices) != 1 || notices[0].Bundle.RuleID != "multi-0" {
		t.Fatalf("pending notices = %+v, err = %v; want 1 multi-0 notice", notices, err)
	}
	if err := e.Shown(ctx, "operator-session", notices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "operator-session", notices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "operator-session"); err != nil || len(notices) != 0 {
		t.Fatalf("pending notices after Seen = %+v, err = %v; want none", notices, err)
	}
}
