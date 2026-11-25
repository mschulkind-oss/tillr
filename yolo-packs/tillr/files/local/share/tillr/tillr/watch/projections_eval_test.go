package watch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func watchSQL(t *testing.T, s *store.Store, sql string, args ...any) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := tx.Exec(sql, args...); return err }); err != nil {
		t.Fatal(err)
	}
}

func assertWatchResolutionEvent(t *testing.T, s *store.Store, want int64) {
	t.Helper()
	var seq int64
	var reason string
	if err := s.ReadTx(context.Background(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT json_extract(payload,'$.resolved_seq'), json_extract(payload,'$.reason')
			FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`, resolvedKind).Scan(&seq, &reason)
	}); err != nil || seq != want || reason != "condition-cleared" {
		t.Fatalf("resolution event sequence=%d reason=%q, want %d condition-cleared: %v", seq, reason, want, err)
	}
}

func TestWatchLegacyEligibilityAndCounts(t *testing.T) {
	ctx := context.Background()
	s := watchStore(t)
	now := time.Unix(1700000000, 0)
	watchFact(t, s, now.Add(-time.Minute), `{"detail":{"item-id":"one"}}`)
	eventRule := strings.Replace(validRule, `"test.watch"`, `"watch-test.fact"`, 1)
	gitRule := strings.NewReplacer(`id = "case"`, `id = "git"`, "[rule.when]", "projections=['git_refs:refs/heads/main']\n[rule.when]", `window = "90s"`, "window = '90s'\n"+`match = 'git.ref("refs/heads/main") == "example"'`).Replace(eventRule)
	durationRule := strings.NewReplacer(`id = "case"`, `id = "duration"`, `"count_in_window"`, `'duration_without_event'`, `threshold = 1`, "threshold_duration = '10s'\nafter = 'true'").Replace(eventRule)
	notInRule := strings.NewReplacer(`id = "case"`, `id = "not-in"`, `window = "90s"`, "window = '90s'\n"+`match = 'event.kind not in ["other"]'`).Replace(eventRule)
	rules := watchRules(t, gitRule+durationRule+notInRule)
	for i, want := range []string{"reads projections", "unsupported pattern duration_without_event", "uses an unsupported expression form"} {
		if got := rules[i].UnevaluatedReason(); got != want {
			t.Errorf("legacy reason for %s = %q, want %q", rules[i].ID, got, want)
		}
	}
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if counts, err := e.Skipped(ctx); err != nil || len(counts) != 0 {
		t.Errorf("legacy status counts = %v, err = %v", counts, err)
	}
	if counts, err := e.Evaluate(ctx); err != nil || len(counts) != 0 {
		t.Errorf("legacy eval counts = %v, err = %v", counts, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("legacy eval opened episodes: %+v: %v", notices, err)
	}
	e.Root = t.TempDir() // An explicit non-repository root makes the Git fact unreadable.
	if got, err := e.EvaluateRules(ctx); err != nil || len(got) != 3 || got[0].ID != "duration" || got[0].Diagnostic() != "" || got[1].ID != "git" || got[1].Unreadable == "" || got[2].ID != "not-in" || got[2].Diagnostic() != "" {
		t.Fatalf("evaluator results = %+v, err = %v", got, err)
	}
	watchFact(t, s, now.Add(-time.Minute), `{"detail":{"item-id":"one"}}`)
	if got, err := e.EvaluateRules(ctx); err != nil || len(got) != 3 || got[0].ID != "duration" || got[0].Diagnostic() != "" || got[1].ID != "git" || got[1].Unreadable == "" || got[2].ID != "not-in" || got[2].Diagnostic() != "" {
		t.Fatalf("evaluator results = %+v, err = %v", got, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 2 {
		t.Fatalf("extended eval did not open episodes: %+v: %v", notices, err)
	}
}

func TestWatchNamedWorkflowFiltersByCurrentMembership(t *testing.T) {
	register(t, "watch-test.fact")
	s := watchStore(t)
	now := time.Unix(1700000000, 0)
	watchSQL(t, s, `CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY, workflow TEXT NOT NULL)`)
	rule := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, `scope = "workflow:*"`, `scope = "workflow:build@v1"`).Replace(validRule)
	e := Evaluator{Store: s, Rules: watchRules(t, rule), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{"selected", "other", "unassigned"} {
		watchFact(t, s, now.Add(-time.Minute), fmt.Sprintf(`{"detail":{"item-id":%q}}`, subject))
	}
	watchFact(t, s, now.Add(-time.Minute), `{}`)
	// Membership recorded after the events still determines their scope.
	watchSQL(t, s, `INSERT INTO workflow_selections VALUES ('selected','build@v1'),('other','build@v2')`)
	results, err := e.EvaluateRules(t.Context())
	if err != nil || len(results) != 1 || results[0].Diagnostic() != "" || results[0].Skipped != 1 {
		t.Fatalf("evaluation: %+v, %v", results, err)
	}
	notices, err := e.Pending(t.Context(), "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "selected" {
		t.Fatalf("workflow notices: %+v, %v", notices, err)
	}
}

func TestWatchDurationEvaluation(t *testing.T) {
	ctx := context.Background()
	s := watchStore(t)
	start := time.Unix(1700000000, 0)
	now := start.Add(20*time.Second - time.Millisecond)
	expr := func(text string) *Expression {
		t.Helper()
		e, err := ParseExpression(text)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	r := Rule{ID: "duration", Scope: "core", Enabled: true, Severity: "warning", Reads: Reads{Events: []string{"watch-test.fact"}}, When: When{
		Pattern: DurationWithoutEvent, GroupBy: "item", After: expr(`event.start == true`), Match: expr(`event.end == true`), ClearWhen: expr(`event.clear == true`), ThresholdDuration: 20 * time.Second, Window: 90 * time.Second,
	}, Guidance: Guidance{Summary: "{{subject}} {{count}} {{elapsed}}"}, Delivery: Delivery{Receiver: "foreground"}}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	initial := watchFact(t, s, start, `{"item":"a","start":true,"end":true}`)
	watchFact(t, s, start, `{"item":"no-start","end":true}`)
	check := func(opened, resolved int64, summary string) {
		t.Helper()
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var gotOpen, gotResolved int64
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT coalesce(max(CASE WHEN state='open' THEN opened_seq END),0),coalesce(max(resolved_seq),0) FROM alert_episodes`).Scan(&gotOpen, &gotResolved)
		}); err != nil {
			t.Fatal(err)
		}
		if gotOpen != opened || gotResolved != resolved {
			t.Fatalf("sequences = %d,%d, want %d,%d", gotOpen, gotResolved, opened, resolved)
		}
		if summary != "" {
			notices, err := e.Pending(ctx, "foreground")
			if err != nil || len(notices) == 0 {
				t.Fatalf("notices %v: %v", notices, err)
			}
			b := notices[len(notices)-1].Bundle
			if b.Guidance.Summary != summary || len(b.Events) != 1 || b.Events[0].Seq != opened {
				t.Fatalf("bundle: %+v", b)
			}
		}
	}
	check(0, 0, "")
	now = start.Add(20 * time.Second)
	check(initial, 0, "a 1 20s")
	// Sequence, not timestamp, determines which start replaces the previous one.
	replacement := watchFact(t, s, start.Add(-time.Second), `{"item":"a","start":true}`)
	check(replacement, replacement, "a 1 21s")
	clear := watchFact(t, s, now, `{"item":"a","start":true,"clear":true}`)
	check(0, clear, "")
	now = start.Add(90 * time.Second)
	oldest := watchFact(t, s, start, `{"item":"a","start":true}`)
	check(oldest, clear, "a 1 1m")
	startHead, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Millisecond)
	e.afterGit = func() { watchFact(t, s, start, `{"item":"a","end":true}`) } // outside the window
	check(0, startHead, "")
	assertWatchResolutionEvent(t, s, startHead)
	e.afterGit = nil
	waiting := watchFact(t, s, now.Add(-20*time.Second), `{"item":"a","start":true}`)
	check(waiting, startHead, "a 1 20s")
	end := watchFact(t, s, now, `{"item":"a","end":true}`)
	check(0, end, "")

	e.Rules[0].When.Match = nil
	check(0, end, "")
	waiting = watchFact(t, s, now.Add(-20*time.Second), `{"item":"a","start":true}`)
	watchFact(t, s, now, `{"item":"a","end":true}`) // not an end without match
	check(waiting, end, "a 1 20s")
	tooNew := watchFact(t, s, now, `{"item":"a","start":true}`)
	check(0, tooNew, "")
}

func watchRules(t *testing.T, text string) []Rule {
	t.Helper()
	rules, err := LoadRuleDocument("test rule", []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestWatchCanonicalCharterProjection(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	s := watchStore(t)
	now := time.Unix(1700000000, 0)
	rule := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, "[rule.when]", "projections=['charter']\n[rule.when]", `window = "90s"`, "window = '90s'\n"+`match = 'charter.ready == "in-queue" and charter.blocked == "blocked" and charter.closed == "closed" and charter.closed in charter.finished and charter.ready in charter.states'`).Replace(validRule)
	e := Evaluator{Store: s, Rules: watchRules(t, rule), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	check := func(wantOpen int, unreadable bool) {
		t.Helper()
		got, err := e.EvaluateRules(ctx)
		if err != nil || len(got) != 1 || (got[0].Unreadable != "") != unreadable {
			t.Fatalf("results %+v: %v", got, err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != wantOpen {
			t.Fatalf("notices %+v: %v", notices, err)
		}
	}
	check(1, false) // embedded default
	content := []byte(`{"definition":"custom","version":1,"states":["ready","blocked","closed"],"ready":"ready","blocked":"blocked","closed":"closed","finished":["closed"],"completes":"landing"}`)
	digest := sha256.Sum256(content)
	record, err := json.Marshal(map[string]any{"definition": "custom", "version": 1, "content": string(content), "sha256": fmt.Sprintf("%x", digest), "default": false})
	if err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `INSERT INTO meta(key,value) VALUES('store.definition',?)`, string(record))
	check(0, false) // recorded charter changes the comparison
	e.Rules = watchRules(t, strings.Replace(rule, `charter.ready == "in-queue"`, `charter.name == "custom" and charter.ready == "ready"`, 1))
	check(0, false) // changed rule does not open on pre-change event
	watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
	check(1, false)
	watchSQL(t, s, `UPDATE meta SET value='not-json' WHERE key='store.definition'`)
	check(1, true) // corruption is not a default and does not resolve the episode
}

func TestWatchProjectionSnapshotCache(t *testing.T) {
	s := watchStore(t)
	watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT)`)
	watchSQL(t, s, `INSERT INTO items VALUES ('one','ready',NULL,NULL)`)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		p := readStoreFacts(tx.Reader())
		before, err := p.item("one")
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='one'`); err != nil {
			return err
		}
		after, err := p.item("one")
		if err != nil {
			return err
		}
		if before.state.String != "ready" || after.state.String != "ready" {
			t.Fatalf("uncached: %+v %+v", before, after)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWatchAgeInEvaluation(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	s := watchStore(t)
	now := time.Unix(1700000000, 0)
	text := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, `window = "90s"`, "window = '90s'\n"+`match = 'age(event.ts) >= 20'`).Replace(validRule)
	e := Evaluator{Store: s, Rules: watchRules(t, text), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now.Add(-20*time.Second), `{"detail":{"item-id":"old"}}`)
	watchFact(t, s, now.Add(-20*time.Second+time.Millisecond), `{"detail":{"item-id":"new"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "old" {
		t.Fatalf("age evaluation: %+v: %v", notices, err)
	}
}

func TestWatchItemNegatedMembershipTypes(t *testing.T) {
	register(t, "watch-test.fact")
	for _, tc := range []struct {
		name, list string
		want       int
	}{
		{"incompatible", `[1]`, 0},
		{"mixed nonmatch", `["closed", 1]`, 0},
		{"compatible nonmatch", `["closed"]`, 1},
		{"compatible match", `["ready"]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := watchStore(t)
			now := time.Unix(1700000000, 0)
			watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT)`)
			watchSQL(t, s, `INSERT INTO items VALUES ('one','ready',NULL,NULL)`)
			text := strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, "[rule.when]", "projections=['items']\n[rule.when]", `window = "90s"`, "window = '90s'\n"+`match = 'item.state not in `+tc.list+`'`).Replace(validRule)
			e := Evaluator{Store: s, Rules: watchRules(t, text), Clock: func() time.Time { return now }}
			if _, err := e.EvaluateRules(ctx); err != nil {
				t.Fatal(err)
			}
			watchFact(t, s, now, `{"detail":{"item-id":"one"}}`)
			if got, err := e.EvaluateRules(ctx); err != nil || len(got) != 1 || got[0].Diagnostic() != "" {
				t.Fatalf("results %+v: %v", got, err)
			}
			if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != tc.want {
				t.Fatalf("notices %+v, want %d: %v", notices, tc.want, err)
			}
		})
	}
}

func TestWatchItemAbsentVersusUnreadable(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	s := watchStore(t)
	now := time.Unix(1700000000, 0)
	watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT)`)
	watchSQL(t, s, `INSERT INTO items VALUES ('present','ready',NULL,'owner')`)
	rules := watchRules(t, strings.NewReplacer(`"test.watch"`, `"watch-test.fact"`, "[rule.when]", "projections=['items']\n[rule.when]", `window = "90s"`, "window = '90s'\n"+`match = 'item.state not in ["closed"] and item.waiting_on == "owner" and empty(item.seat)'`).Replace(validRule))
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"absent"}}`)
	watchFact(t, s, now, `{"detail":{"item-id":"present"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "present" {
			t.Fatalf("notices %v: %v", notices, err)
		}
	}
	check()
	watchSQL(t, s, `DROP TABLE items`)
	if got, err := e.EvaluateRules(ctx); err != nil || len(got) != 1 || !strings.Contains(got[0].Unreadable, "items") {
		t.Fatalf("results %+v: %v", got, err)
	}
	check()
}

func TestWatchItemLookupFallsBackToEventItemID(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	s := watchStore(t)
	now := time.Unix(1700000000, 0)
	watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT)`)
	watchSQL(t, s, `INSERT INTO items VALUES ('item-open','in-queue',NULL,NULL), ('item-closed','closed',NULL,NULL)`)
	doc := `[[rule]]
id = "run-item-state"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
projections = ["items"]
[rule.when]
group_by = "object_id"
pattern = "predicate_match"
match = 'event.kind == "watch-test.fact"'
clear_when = 'item.state == "closed"'
[rule.guidance]
summary = "run {{subject}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ runID, itemID string }{
		{"run-1", "item-open"},
		{"run-2", "item-closed"},
	} {
		runID := tc.runID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "watch-test.fact",
				ObjectType: "run",
				ObjectID:   &runID,
				At:         now,
				Payload:    []byte(fmt.Sprintf(`{"item_id":%q,"run_id":%q}`, tc.itemID, tc.runID)),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := e.EvaluateRules(ctx); err != nil || len(got) != 1 || got[0].Diagnostic() != "" {
		t.Fatalf("results %+v: %v", got, err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Bundle.Subject != "run-1" {
		t.Fatalf("want only run-1 (item-open), got %+v", notices)
	}
}

func writeGreenProofJSON(t *testing.T, root, oid string, provedAt time.Time, passed []string) string {
	t.Helper()
	gateDir := filepath.Join(root, ".git", "tillr-gate")
	if err := os.MkdirAll(gateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"commit": oid,
		"passed": passed,
	}
	if !provedAt.IsZero() {
		payload["proved_at"] = provedAt.UTC().Format(time.RFC3339)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(gateDir, "green-"+oid+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectionPredicateMatchFixerProved(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	root := t.TempDir()
	fixtureGit(t, root, "", now, "init")
	fixtureGit(t, root, "", now, "config", "user.name", "Test")
	fixtureGit(t, root, "", now, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "", now, "commit", "--allow-empty", "-m", "red")
	redOID := fixtureGit(t, root, "", now, "rev-parse", "HEAD")
	fixtureGit(t, root, "", now, "branch", "-M", "main")
	fixtureGit(t, root, "", now.Add(time.Minute), "commit", "--allow-empty", "-m", "green")
	greenOID := fixtureGit(t, root, "", now.Add(time.Minute), "rev-parse", "HEAD")
	fixtureGit(t, root, "", now.Add(time.Minute), "update-ref", "refs/tillr/green", greenOID)

	s := watchStore(t)
	watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT,event_seq INTEGER)`)

	doc := `[[rule]]
id = "fixer-proved-queued"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.id matches "^fixer-main-" and item.state == "in-queue" and item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "Fixer {{subject}} proved ({{count}} {{elapsed}})"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)
	if got := rules[0].UnevaluatedReason(); got != "reads projections" {
		t.Fatalf("UnevaluatedReason = %q, want %q", got, "reads projections")
	}
	if got := rules[0].evaluatorReason(); got != "" {
		t.Fatalf("evaluatorReason = %q, want empty", got)
	}

	e := Evaluator{Store: s, Root: root, Rules: rules, Clock: func() time.Time { return now.Add(2 * time.Minute) }}

	// Missing item_criteria table -> unreadableProjection{"fixer_checks", ...}.
	res, err := e.EvaluateRules(ctx)
	if err != nil || len(res) != 1 || !strings.Contains(res[0].Unreadable, "fixer_checks:") {
		t.Fatalf("expected fixer_checks unreadable, got %+v (%v)", res, err)
	}

	watchSQL(t, s, `CREATE TABLE item_criteria(item_id TEXT,criterion_id TEXT,text TEXT)`)

	fixerID := "fixer-main-" + redOID[:12]
	watchSQL(t, s, `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (?,'item.add','item',?,?)`, now.UnixMilli(), fixerID, `{"item_id":"`+fixerID+`"}`)
	var addSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT MAX(seq) FROM events WHERE object_id = ?`, fixerID).Scan(&addSeq)
	}); err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `INSERT INTO items VALUES (?,'in-queue',NULL,NULL,?)`, fixerID, addSeq)
	watchSQL(t, s, `INSERT INTO item_criteria VALUES (?,'trunk-green','(red: check-a, go-test:tools/tillr:watch:TestFoo)')`, fixerID)

	// Missing proof file -> quiet.
	if res, err := e.EvaluateRules(ctx); err != nil || len(res) != 1 || res[0].Diagnostic() != "" {
		t.Fatalf("missing proof should be clean: %+v (%v)", res, err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("missing proof should not alert: %+v (%v)", notices, err)
	}

	// Proof recorded at or before seen_ms -> quiet.
	writeGreenProofJSON(t, root, greenOID, now, []string{"check-a", "go-tool/tools-tillr-s-whole-suite-passes"})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("stale proof should not alert: %+v (%v)", notices, err)
	}

	// Proof missing one check -> quiet.
	writeGreenProofJSON(t, root, greenOID, now.Add(time.Minute), []string{"check-a"})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("partial proof should not alert: %+v (%v)", notices, err)
	}

	// Empty (red: ) check list never fires vacuously.
	emptyFixer := "fixer-main-" + greenOID[:12]
	watchSQL(t, s, `INSERT INTO items VALUES (?,'in-queue',NULL,NULL,?)`, emptyFixer, addSeq)
	watchSQL(t, s, `INSERT INTO item_criteria VALUES (?,'trunk-green','(red: )')`, emptyFixer)

	// Full proof after seen_ms -> opens alert for fixerID only.
	writeGreenProofJSON(t, root, greenOID, now.Add(time.Minute), []string{"check-a", "go-tool/tools-tillr-s-whole-suite-passes"})
	if res, err := e.EvaluateRules(ctx); err != nil || len(res) != 1 || res[0].Diagnostic() != "" {
		t.Fatalf("EvaluateRules: %+v (%v)", res, err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("expected 1 notice, got %+v (%v)", notices, err)
	}
	if notices[0].Bundle.Subject != fixerID || notices[0].Bundle.Events == nil || len(notices[0].Bundle.Events) != 0 {
		t.Fatalf("unexpected notice bundle: %+v", notices[0].Bundle)
	}
	if notices[0].Bundle.Guidance.Summary != fmt.Sprintf("Fixer %s proved (1 0s)", fixerID) {
		t.Fatalf("unexpected summary: %q", notices[0].Bundle.Guidance.Summary)
	}

	// Unrelated store writes do not advance last_observed_seq or observation_count, and preserve ack.
	note := "investigating"
	if err := e.Ack(ctx, notices[0].AlertID, "op", &note); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now.Add(2*time.Minute), `{"unrelated":true}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var epState string
	var epLast, epCount int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, last_observed_seq, observation_count FROM alert_episodes WHERE alert_id = ?`, notices[0].AlertID).Scan(&epState, &epLast, &epCount)
	}); err != nil {
		t.Fatal(err)
	}
	if epState != "acknowledged" || epLast != addSeq || epCount != 1 {
		t.Fatalf("after unrelated write: state=%q last=%d count=%d, want acknowledged/%d/1", epState, epLast, epCount, addSeq)
	}

	// Condition cleared (e.g. item leaves in-queue to ready) -> resolves with condition-cleared.
	watchSQL(t, s, `UPDATE items SET state='ready' WHERE item_id=?`, fixerID)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var resolvedReason string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id = ?`, notices[0].AlertID).Scan(&epState, &resolvedReason)
	}); err != nil {
		t.Fatal(err)
	}
	if epState != "resolved" || resolvedReason != "condition-cleared" {
		t.Fatalf("expected resolved/condition-cleared, got %q/%q", epState, resolvedReason)
	}

	// Re-arm condition (back to in-queue) -> opens episode 2, then manual resolve suppresses re-open while subject_seq <= resolved_seq.
	watchSQL(t, s, `UPDATE items SET state='in-queue' WHERE item_id=?`, fixerID)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ep2ID string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE rule_id='fixer-proved-queued' AND state='open'`).Scan(&ep2ID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.Resolve(ctx, ep2ID, "manual"); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now.Add(3*time.Minute), `{"unrelated":2}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var activeCount int
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT COUNT(*) FROM alert_episodes WHERE rule_id='fixer-proved-queued' AND state!='resolved'`).Scan(&activeCount)
	}); err != nil {
		t.Fatal(err)
	}
	if activeCount != 0 {
		t.Fatalf("manual resolution should suppress reopen while subject_seq <= resolved_seq, activeCount=%d", activeCount)
	}

	// Newer drain.fixer-attached advances subject_seq past resolved_seq -> re-opens episode 3, then item-closed resolves it.
	watchSQL(t, s, `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (?,'drain.fixer-attached','item',?,?)`, now.Add(30*time.Second).UnixMilli(), fixerID, `{"item_id":"`+fixerID+`"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ep3ID string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE rule_id='fixer-proved-queued' AND state='open'`).Scan(&ep3ID)
	}); err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `UPDATE items SET state='closed' WHERE item_id=?`, fixerID)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id = ?`, ep3ID).Scan(&epState, &resolvedReason)
	}); err != nil {
		t.Fatal(err)
	}
	if epState != "resolved" || resolvedReason != "item-closed" {
		t.Fatalf("expected resolved/item-closed, got %q/%q", epState, resolvedReason)
	}
}
