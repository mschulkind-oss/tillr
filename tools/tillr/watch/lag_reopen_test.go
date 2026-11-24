package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func initLagTestRepo(t *testing.T, now time.Time) (root string, makeCommit func(string) string, setRef func(string, string)) {
	t.Helper()
	root = t.TempDir()
	gitRun := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			fmt.Sprintf("GIT_AUTHOR_DATE=%d +0000", now.Unix()),
			fmt.Sprintf("GIT_COMMITTER_DATE=%d +0000", now.Unix()),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	gitRun("init", "-q")
	makeCommit = func(msg string) string {
		gitRun("commit", "--allow-empty", "-q", "-m", msg)
		out := gitRun("rev-parse", "HEAD")
		return strings.TrimSpace(out)
	}
	setRef = func(ref, oid string) {
		gitRun("update-ref", ref, oid)
	}
	return root, makeCommit, setRef
}

func TestLagReopenSchemaAndLint(t *testing.T) {
	register(t, "watch-test.ended", "watch-test.other")

	validDoc := `[[rule]]
id = "lag-reopen"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.ended"]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
reopen_when = 'event.kind == "watch-test.ended" and event.outcome == "red"'
[rule.guidance]
summary = "follower {{follower_short}} lags leader {{leader_short}} by {{count}}"
reopen_summary = "proof failed on {{event.tip_sha}}: {{event.failing_checks}} (lag {{count}})"
check = ["inspect proof"]
do = ["advance follower"]
[rule.delivery]
receiver = "foreground"
`
	rules, err := LoadRuleDocument("valid.toml", []byte(validDoc))
	if err != nil {
		t.Fatalf("LoadRuleDocument(valid): %v", err)
	}
	if rules[0].When.ReopenWhen == nil || rules[0].Guidance.ReopenSummary == "" {
		t.Fatalf("expected ReopenWhen and ReopenSummary to be populated, got %+v", rules[0])
	}

	invalidDocs := []struct {
		name string
		doc  string
	}{
		{
			name: "reopen_when_on_non_lag",
			doc: `[[rule]]
id = "r1"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.ended"]
[rule.when]
pattern = "count_in_window"
group_by = "subject"
threshold = 2
window = "1h"
reopen_when = 'event.outcome == "red"'
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "reopen_when_empty_events",
			doc: `[[rule]]
id = "r2"
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
reopen_when = 'event.outcome == "red"'
[rule.guidance]
summary = "s"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "reopen_summary_without_reopen_when",
			doc: `[[rule]]
id = "r3"
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
summary = "s"
reopen_summary = "reopened"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
		{
			name: "reopen_summary_whitespace",
			doc: `[[rule]]
id = "r4"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.ended"]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
reopen_when = 'event.outcome == "red"'
[rule.guidance]
summary = "s"
reopen_summary = "   "
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`,
		},
	}
	for _, tc := range invalidDocs {
		if _, err := LoadRuleDocument(tc.name+".toml", []byte(tc.doc)); err == nil {
			t.Errorf("%s: expected LoadRuleDocument refusal, got nil", tc.name)
		}
	}

	// Lint checks
	dir := t.TempDir()
	writeLintFile(t, dir, "config/tillr/backend-policy.json", minimalPolicyJSON)
	writeLintFile(t, dir, "config/tillr/workflows/simple-build@v1.json", minimalLandingWorkflowJSON)
	rulePath := writeLintFile(t, dir, "config/tillr/watch/lag.toml", validDoc)

	res, err := Lint(context.Background(), LintOptions{
		Root:      dir,
		RulesPath: rulePath,
	})
	if err != nil {
		t.Fatalf("Lint(valid): %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("Lint(valid) unexpected findings: %v", res)
	}

	lintCases := []struct {
		name string
		doc  string
		code string
	}{
		{
			name: "unknown_event_kind_in_reopen_when",
			doc:  strings.Replace(validDoc, `event.kind == "watch-test.ended"`, `event.kind == "watch-test.missing"`, 1),
			code: CodeWatchLintUnknownSignal,
		},
		{
			name: "unlisted_event_kind_in_reopen_when",
			doc:  strings.Replace(validDoc, `event.kind == "watch-test.ended"`, `event.kind == "watch-test.other"`, 1),
			code: CodeWatchLintUnknownSignal,
		},
		{
			name: "event_placeholder_in_lag_summary",
			doc:  strings.Replace(validDoc, `follower {{follower_short}} lags leader {{leader_short}} by {{count}}`, `{{event.tip_sha}}`, 1),
			code: CodeWatchLintEmptyGuidance,
		},
		{
			name: "reopen_summary_blank",
			doc:  strings.Replace(validDoc, `proof failed on {{event.tip_sha}}: {{event.failing_checks}} (lag {{count}})`, `   `, 1),
			code: CodeWatchLintEmptyGuidance,
		},
		{
			name: "reopen_when_without_events",
			doc:  strings.Replace(validDoc, "events = [\"watch-test.ended\"]\n", "", 1),
			code: CodeWatchLintUnknownSignal,
		},
	}
	for _, tc := range lintCases {
		p := writeLintFile(t, dir, "config/tillr/watch/"+tc.name+".toml", tc.doc)
		r, err := Lint(context.Background(), LintOptions{
			Root:      dir,
			RulesPath: p,
		})
		if err != nil {
			t.Fatalf("%s: Lint error: %v", tc.name, err)
		}
		assertHasLintCode(t, r, tc.code, "")
	}
}

func TestLagReopenDefinitionHashAndLoadedSeqGating(t *testing.T) {
	register(t, "watch-test.ended")
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)

	root, makeCommit, setRef := initLagTestRepo(t, now)
	c1 := makeCommit("c1")
	c2 := makeCommit("c2")
	setRef("refs/heads/leader", c2)
	setRef("refs/heads/follower", c1)

	doc1 := `[[rule]]
id = "lag-reopen"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.ended"]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
reopen_when = 'event.kind == "watch-test.ended" and event.outcome == "red"'
[rule.guidance]
summary = "lag {{count}}"
reopen_summary = "red {{event.failing_checks}}"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`
	doc2 := strings.Replace(doc1, `red {{event.failing_checks}}`, `red2 {{event.failing_checks}}`, 1)
	doc3 := strings.Replace(doc1, `event.outcome == "red"`, `event.outcome == "red" and event.phase == "ended"`, 1)

	r1 := watchRules(t, doc1)[0]
	r2 := watchRules(t, doc2)[0]
	r3 := watchRules(t, doc3)[0]

	if definitionHash(r1) != definitionHash(r2) {
		t.Fatalf("reopen_summary edit should not change definitionHash")
	}
	if definitionHash(r1) == definitionHash(r3) {
		t.Fatalf("reopen_when edit must change definitionHash")
	}
	if ruleEqual(r1, r2) || ruleEqual(r1, r3) {
		t.Fatalf("ruleEqual must distinguish reopen_summary and reopen_when edits")
	}

	// Historical red event before first load must not reopen an acknowledged episode.
	s := watchStore(t)
	watchKindFact(t, s, now, "watch-test.ended", `{"outcome":"red","phase":"ended","failing_checks":"TestOld"}`)
	now = now.Add(time.Second)

	e := Evaluator{Store: s, Root: root, Rules: []Rule{r1}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("initial pending: %+v (%v)", notices, err)
	}
	alertID := notices[0].AlertID
	if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
		t.Fatal(err)
	}

	// Evaluate again: historical event (seq <= first_loaded_seq) must NOT reopen.
	now = now.Add(time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
	if state != "acknowledged" {
		t.Fatalf("historical event reopened acknowledged lag episode: state=%q", state)
	}

	// Edit reopen_when (advancing loaded_seq) after an event that occurred before the edit:
	// wait - if the event was already <= acked_seq it won't reopen anyway; test an event
	// committed AFTER ack, then edit reopen_when BEFORE EvaluateRules runs!
	watchKindFact(t, s, now.Add(time.Second), "watch-test.ended", `{"outcome":"red","phase":"ended","failing_checks":"TestBeforeEdit"}`)
	e.Rules = []Rule{r3}
	now = now.Add(2 * time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
	if state != "acknowledged" {
		t.Fatalf("event before reopen_when edit reopened episode despite loaded_seq gate: state=%q", state)
	}
}

func TestLagReopenStoreAndSourceLifecycle(t *testing.T) {
	register(t, "watch-test.ended", "watch-test.green")
	ctx := context.Background()

	runScenario := func(t *testing.T, useStore bool) {
		t.Helper()
		now := time.Date(2026, 11, 24, 11, 0, 0, 0, time.UTC)
		root, makeCommit, setRef := initLagTestRepo(t, now)
		c1 := makeCommit("c1")
		c2 := makeCommit("c2")
		setRef("refs/heads/leader", c2)
		setRef("refs/heads/follower", c1)

		doc := `[[rule]]
id = "lag-reopen"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.ended", "watch-test.green"]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
reopen_when = 'event.kind == "watch-test.ended" and event.outcome == "red"'
[rule.guidance]
summary = "lag {{count}}"
reopen_summary = "red {{event.tip_sha}} failed {{event.failing_checks}} (lag {{count}})"
check = ["inspect"]
do = ["fix"]
[rule.delivery]
receiver = "foreground"
`
		rule := watchRules(t, doc)[0]

		var (
			e         Evaluator
			src       *memoryObservationSource
			appendEv  func(kind, body string) int64
			readState func(alertID string) (state string, obsCount int64, trackingItem sql.NullString)
		)
		if useStore {
			s := watchStore(t)
			seedWatchItemsTable(t, s)
			setWatchItemState(t, s, "item-1", "in-queue")
			e = Evaluator{Store: s, Root: root, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
			appendEv = func(kind, body string) int64 {
				return watchKindFact(t, s, now, kind, body)
			}
			readState = func(alertID string) (string, int64, sql.NullString) {
				var st string
				var cnt int64
				var trk sql.NullString
				lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, alertID), &st, &cnt, &trk)
				return st, cnt, trk
			}
		} else {
			rule.Scope = "*"
			src = &memoryObservationSource{
				projections: map[string]any{
					"charter": map[string]any{
						"closed":   "closed",
						"finished": []string{"landed"},
					},
					"items": map[string]map[string]any{
						"item-1": {"item.state": "in-queue"},
					},
				},
			}
			e = Evaluator{Source: src, Root: root, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
			appendEv = func(kind, body string) int64 {
				var fields map[string]any
				if err := json.Unmarshal([]byte(body), &fields); err != nil {
					t.Fatal(err)
				}
				return src.append(kind, now, fields)
			}
			readState = func(alertID string) (string, int64, sql.NullString) {
				ep := e.mem.findAlertLocked(alertID)
				if ep == nil {
					t.Fatalf("missing memEpisode %s", alertID)
				}
				return ep.state, ep.observationCount, ep.trackingItemID
			}
		}

		// 1. Initial eval opens the alert.
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("initial pending: %+v (%v)", notices, err)
		}
		alertID := notices[0].AlertID
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// 2. Red event while ALREADY open must not emit a reopen or duplicate notice.
		now = now.Add(time.Second)
		appendEv("watch-test.ended", `{"outcome":"red","tip_sha":"aaa","failing_checks":"TestWhileOpen"}`)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
			t.Fatalf("expected no duplicate notice while already open, got %+v (%v)", pending, err)
		}

		// 3. Plain Ack -> non-matching events AND ref advances stay silent!
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		appendEv("watch-test.green", `{"outcome":"green"}`)
		c3 := makeCommit("c3")
		setRef("refs/heads/leader", c3)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, obsCount, _ := readState(alertID)
		if st != "acknowledged" {
			t.Fatalf("expected acknowledged across non-red event + ref advance, got %q", st)
		}
		if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
			t.Fatalf("expected no pending across non-red event + ref advance, got %+v (%v)", pending, err)
		}

		// 4. Post-ack red event reopens in place with reopen_summary!
		now = now.Add(time.Second)
		appendEv("watch-test.ended", `{"outcome":"red","tip_sha":"bbb","failing_checks":"TestRedOne"}`)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, newObsCount, _ := readState(alertID)
		if st != "open" || newObsCount != obsCount+1 {
			t.Fatalf("after first red reopen: state=%q obsCount=%d (want open, %d)", st, newObsCount, obsCount+1)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].AlertID != alertID {
			t.Fatalf("expected pending wake on first red reopen, got %+v (%v)", notices, err)
		}
		if !strings.Contains(notices[0].Bundle.Guidance.Summary, "red bbb failed TestRedOne") {
			t.Fatalf("unexpected reopen_summary: %q", notices[0].Bundle.Guidance.Summary)
		}
		if len(notices[0].Bundle.Events) != 1 || notices[0].Bundle.Events[0].Kind != "watch-test.ended" {
			t.Fatalf("unexpected reopen bundle events: %+v", notices[0].Bundle.Events)
		}
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// 5. Re-ack, then a second red event reopens again!
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		appendEv("watch-test.ended", `{"outcome":"red","tip_sha":"ccc","failing_checks":"TestRedTwo"}`)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || !strings.Contains(notices[0].Bundle.Guidance.Summary, "red ccc failed TestRedTwo") {
			t.Fatalf("expected second red reopen naming TestRedTwo, got %+v (%v)", notices, err)
		}
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// 6. Track to open item -> non-red + ref advance stay quiet -> post-track red reopens!
		by := "operator"
		if err := e.Track(ctx, alertID, "item-1", &by, nil); err != nil {
			t.Fatalf("Track: %v", err)
		}
		now = now.Add(time.Second)
		appendEv("watch-test.green", `{"outcome":"green"}`)
		c4 := makeCommit("c4")
		setRef("refs/heads/leader", c4)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, obsCount, trk := readState(alertID)
		if st != "acknowledged" || !trk.Valid || trk.String != "item-1" {
			t.Fatalf("tracked episode should stay acknowledged: state=%q trk=%+v", st, trk)
		}
		if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
			t.Fatalf("expected no pending while tracked without red proof, got %+v (%v)", pending, err)
		}

		now = now.Add(time.Second)
		appendEv("watch-test.ended", `{"outcome":"red","tip_sha":"ddd","failing_checks":"TestRedTracked"}`)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, newObsCount, trk = readState(alertID)
		if st != "open" || trk.Valid || newObsCount != obsCount+1 {
			t.Fatalf("after tracked red reopen: state=%q trk=%+v obs=%d (want open, null, %d)", st, trk, newObsCount, obsCount+1)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || !strings.Contains(notices[0].Bundle.Guidance.Summary, "red ddd failed TestRedTracked") {
			t.Fatalf("expected pending wake on tracked red reopen, got %+v (%v)", notices, err)
		}
	}

	t.Run("store", func(t *testing.T) { runScenario(t, true) })
	t.Run("source", func(t *testing.T) { runScenario(t, false) })
}

func TestLagReopenPreMigrationAckedWatermarkFallback(t *testing.T) {
	register(t, "watch-test.ended")
	ctx := context.Background()

	doc := `[[rule]]
id = "lag-reopen"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.ended"]
projections = ["git_refs:refs/heads/leader,refs/heads/follower"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/leader"
follower = "refs/heads/follower"
threshold_commits = 1
reopen_when = 'event.kind == "watch-test.ended" and event.outcome == "red"'
[rule.guidance]
summary = "lag {{count}}"
reopen_summary = "red {{event.failing_checks}}"
check = ["inspect"]
do = ["fix"]
[rule.delivery]
receiver = "foreground"
`

	t.Run("store", func(t *testing.T) {
		now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		root, makeCommit, setRef := initLagTestRepo(t, now)
		c1 := makeCommit("c1")
		c2 := makeCommit("c2")
		setRef("refs/heads/leader", c2)
		setRef("refs/heads/follower", c1)

		s := watchStore(t)
		e := Evaluator{Store: s, Root: root, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("initial pending: %+v (%v)", notices, err)
		}
		alertID := notices[0].AlertID
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// Red event BEFORE ack, then Ack, then clear acked_seq to simulate pre-migration row.
		watchKindFact(t, s, now.Add(time.Second), "watch-test.ended", `{"outcome":"red","failing_checks":"TestBeforeAck"}`)
		now = now.Add(2 * time.Second)
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatal(err)
		}
		watchSQL(t, s, `UPDATE alert_episodes SET acked_seq=NULL, acked_fingerprint=NULL, acked_lag_pair=NULL WHERE alert_id=?`, alertID)

		// Eval: ackedWatermark from watch.alert.acknowledged is > TestBeforeAck seq, so it must NOT reopen.
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var state string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
		if state != "acknowledged" {
			t.Fatalf("pre-ack red event falsely reopened via fallback watermark: state=%q", state)
		}

		// Clear acked_seq again and append a post-ack red event -> MUST reopen!
		watchSQL(t, s, `UPDATE alert_episodes SET acked_seq=NULL, acked_fingerprint=NULL, acked_lag_pair=NULL WHERE alert_id=?`, alertID)
		watchKindFact(t, s, now.Add(time.Second), "watch-test.ended", `{"outcome":"red","failing_checks":"TestAfterAck"}`)
		now = now.Add(2 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
		if state != "open" {
			t.Fatalf("post-ack red event failed to reopen via fallback watermark: state=%q", state)
		}
	})

	t.Run("source", func(t *testing.T) {
		now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
		root, makeCommit, setRef := initLagTestRepo(t, now)
		c1 := makeCommit("c1")
		c2 := makeCommit("c2")
		setRef("refs/heads/leader", c2)
		setRef("refs/heads/follower", c1)

		rule := watchRules(t, doc)[0]
		rule.Scope = "*"
		src := &memoryObservationSource{}
		e := Evaluator{Source: src, Root: root, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("initial pending: %+v (%v)", notices, err)
		}
		alertID := notices[0].AlertID

		src.append("watch-test.ended", now.Add(time.Second), map[string]any{"outcome": "red", "failing_checks": "TestBeforeAck"})
		now = now.Add(2 * time.Second)
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatal(err)
		}
		// Clear ackedSeq on memEpisode to exercise fallback watermark from m.events.
		ep := e.mem.findAlertLocked(alertID)
		ep.ackedSeq = sql.NullInt64{}
		ep.ackedFingerprint = sql.NullString{}
		ep.ackedLagPair = sql.NullString{}

		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if ep.state != "acknowledged" {
			t.Fatalf("pre-ack red event falsely reopened on source via fallback watermark: state=%q", ep.state)
		}

		src.append("watch-test.ended", now.Add(time.Second), map[string]any{"outcome": "red", "failing_checks": "TestAfterAck"})
		src.append("watch-test.ended", now.Add(2*time.Second), map[string]any{"outcome": "red", "failing_checks": "TestAfterAck2"})
		now = now.Add(3 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if ep.state != "open" {
			t.Fatalf("post-ack red event failed to reopen on source via fallback watermark: state=%q", ep.state)
		}
	})
}
