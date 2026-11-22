package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestTrackEventRepeatsAcrossDipsAndReopensWhenItemLands(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s := watchStore(t)
	seedWatchItemsTable(t, s)
	setWatchItemState(t, s, "item-slice-d", "in-queue")
	setWatchItemState(t, s, "item-slice-e", "blocked")

	doc := `[[rule]]
id = "fast-gate-run-slow"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "slowest_check"
pattern = "count_in_window"
match = 'event.wall_ms > 120000'
threshold = 1
window = "1h"
fingerprint = ["slowest_check"]
[rule.guidance]
summary = "fast-gate check {{subject}} ran {{count}} slow pass(es)"
check = ["inspect timing"]
do = ["speed up {{subject}}"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "fast-gate-median-slow"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.median_ms > 90000'
clear_when = 'event.median_ms > 0 and event.median_ms <= 90000'
[rule.guidance]
summary = "fast-gate median exceeded 90s"
check = ["inspect median"]
do = ["reduce median"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}

	// Open initial alerts for both rules.
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":150000}`)
	watchFact(t, s, now, `{"median_ms":110000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 2 {
		t.Fatalf("initial Pending = %+v (%v), want 2 notices", notices, err)
	}
	through := notices[len(notices)-1].Seq
	if err := e.Shown(ctx, "foreground", through); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", through); err != nil {
		t.Fatal(err)
	}

	var runAlertID, medAlertID string
	for _, n := range notices {
		switch n.Bundle.RuleID {
		case "fast-gate-run-slow":
			runAlertID = n.AlertID
		case "fast-gate-median-slow":
			medAlertID = n.AlertID
		}
	}
	if runAlertID == "" || medAlertID == "" {
		t.Fatalf("missing alert IDs: run=%q med=%q", runAlertID, medAlertID)
	}

	// Attach item-slice-d via Track (with --by and --note) and item-slice-e via Track without --by.
	by := "operator"
	note := "gate rework slice D"
	if err := e.Track(ctx, runAlertID, "item-slice-d", &by, &note); err != nil {
		t.Fatalf("Track runAlert: %v", err)
	}
	if err := e.Track(ctx, medAlertID, "item-slice-e", nil, nil); err != nil {
		t.Fatalf("Track medAlert: %v", err)
	}

	// Verify watch.alert.tracked payloads.
	var rawTracked string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT payload FROM events WHERE kind='watch.alert.tracked' AND object_id=%q`, runAlertID), &rawTracked)
	var trackedPayload map[string]any
	if err := json.Unmarshal([]byte(rawTracked), &trackedPayload); err != nil {
		t.Fatal(err)
	}
	if trackedPayload["item_id"] != "item-slice-d" || trackedPayload["by"] != "operator" || trackedPayload["note"] != "gate rework slice D" {
		t.Fatalf("unexpected tracked payload: %s", rawTracked)
	}
	if _, hasReceiver := trackedPayload["receiver"]; hasReceiver {
		t.Fatalf("unexpected receiver in tracked payload: %s", rawTracked)
	}

	// Silence on a tracked alert is refused.
	until := "1h"
	if err := e.Silence(ctx, runAlertID, &until, nil); err == nil || !strings.Contains(err.Error(), "cannot silence an item-tracked alert") || !strings.Contains(err.Error(), "(tracked by item-slice-d)") {
		t.Fatalf("Silence on tracked alert = %v, want tracked refusal", err)
	}

	// ReadStatus surfaces item_id (and by only when non-null).
	st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Alerts) != 2 {
		t.Fatalf("ReadStatus = %+v (%v)", st, err)
	}
	for _, a := range st.Alerts {
		switch a.ID {
		case runAlertID:
			if a.State != "acknowledged" || a.By == nil || *a.By != "operator" || a.ItemID == nil || *a.ItemID != "item-slice-d" || a.Observed != 1 {
				t.Fatalf("unexpected run alert status: %+v", a)
			}
		case medAlertID:
			if a.State != "acknowledged" || a.By != nil || a.ItemID == nil || *a.ItemID != "item-slice-e" || a.Observed != 1 {
				t.Fatalf("unexpected median alert status: %+v", a)
			}
		}
	}

	// Repeat 1 while open: observation_count rises, Pending stays empty.
	now = now.Add(10 * time.Minute)
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":160000}`)
	watchFact(t, s, now, `{"median_ms":105000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending while tracked open = %+v (%v), want empty", p, err)
	}

	// Dip / window aging while tracked item is still open:
	// - Advance 2 hours so fast-gate-run-slow's 1h window ages out (!firing).
	// - Emit clear_when dip (median_ms = 80000) on fast-gate-median-slow (!firing + c.clears).
	now = now.Add(2 * time.Hour)
	watchFact(t, s, now, `{"median_ms":80000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var runState, medState string
	var runCount, medCount int64
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count FROM alert_episodes WHERE alert_id=%q`, runAlertID), &runState, &runCount)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count FROM alert_episodes WHERE alert_id=%q`, medAlertID), &medState, &medCount)
	if runState != "acknowledged" || runCount != 2 || medState != "acknowledged" || medCount != 2 {
		t.Fatalf("after dip while tracked: run=%s/%d med=%s/%d, want acknowledged/2 for both", runState, runCount, medState, medCount)
	}

	// Repeat after dip while item is still open (move item-slice-d to not-started to cover all open states):
	setWatchItemState(t, s, "item-slice-d", "not-started")
	now = now.Add(5 * time.Minute)
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":175000}`)
	watchFact(t, s, now, `{"median_ms":99000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var epCount int
	lifecycleRead(t, s, `SELECT COUNT(*) FROM alert_episodes`, &epCount)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT observation_count FROM alert_episodes WHERE alert_id=%q`, runAlertID), &runCount)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT observation_count FROM alert_episodes WHERE alert_id=%q`, medAlertID), &medCount)
	if epCount != 2 || runCount != 3 || medCount != 3 {
		t.Fatalf("after post-dip repeat: epCount=%d runCount=%d medCount=%d, want 2/3/3", epCount, runCount, medCount)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending after post-dip repeat = %+v (%v), want empty", p, err)
	}

	// Now item-slice-d lands while fast-gate-run-slow is STILL true (plus one more repeat on the same pass),
	// and item-slice-e closes while fast-gate-median-slow is STILL true!
	setWatchItemState(t, s, "item-slice-d", "landed")
	setWatchItemState(t, s, "item-slice-e", "closed")
	now = now.Add(2 * time.Minute)
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":180000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	reopened, err := e.Pending(ctx, "foreground")
	if err != nil || len(reopened) != 2 {
		t.Fatalf("Pending after tracked items finished = %+v (%v), want 2 notices", reopened, err)
	}
	byAlert := map[string]Notice{}
	for _, n := range reopened {
		byAlert[n.AlertID] = n
	}
	if got := byAlert[runAlertID].Bundle.Guidance.Summary; !strings.HasPrefix(got, "tracked fix item-slice-d landed and the condition persists: fast-gate check go-affected ran ") {
		t.Fatalf("runAlert reopened summary = %q", got)
	}
	if got := byAlert[medAlertID].Bundle.Guidance.Summary; got != "tracked fix item-slice-e closed and the condition persists: fast-gate median exceeded 90s" {
		t.Fatalf("medAlert reopened summary = %q", got)
	}

	// Check reopened events and cleared tracking fields.
	var runCause, runItem, runItemState string
	var runTracking sql.NullString
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, runAlertID), &runState, &runCount, &runTracking)
	if runState != "open" || runCount != 4 || runTracking.Valid {
		t.Fatalf("reopened runAlert row: state=%q count=%d tracking=%+v", runState, runCount, runTracking)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause'), json_extract(payload,'$.item_id'), json_extract(payload,'$.item_state') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q`, runAlertID), &runCause, &runItem, &runItemState)
	if runCause != "tracked-item-landed" || runItem != "item-slice-d" || runItemState != "landed" {
		t.Fatalf("runAlert reopened event: cause=%q item=%q state=%q", runCause, runItem, runItemState)
	}
	var medCause, medItem, medItemState string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause'), json_extract(payload,'$.item_id'), json_extract(payload,'$.item_state') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q`, medAlertID), &medCause, &medItem, &medItemState)
	if medCause != "tracked-item-closed" || medItem != "item-slice-e" || medItemState != "closed" {
		t.Fatalf("medAlert reopened event: cause=%q item=%q state=%q", medCause, medItem, medItemState)
	}

	// Confirm the reopened notices; subsequent evaluations must neither re-wake nor
	// falsely re-resolve medAlert from the earlier median_ms=80000 clear at seq <= ep.last!
	newThrough := reopened[len(reopened)-1].Seq
	if err := e.Shown(ctx, "foreground", newThrough); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", newThrough); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":190000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending after confirming reopened notice = %+v (%v), want empty", p, err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, medAlertID), &medState)
	if medState != "open" {
		t.Fatalf("medAlert falsely resolved by earlier pre-last clear: state=%q, want open", medState)
	}
}

func TestTrackLagBehindRepeatsAndQuietResolveOrPersistReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet", "-b", "main")
	git("commit", "--quiet", "--allow-empty", "-m", "c0")
	c0 := git("rev-parse", "HEAD")
	git("update-ref", "refs/tillr/green", c0)
	git("commit", "--quiet", "--allow-empty", "-m", "c1")
	c1 := git("rev-parse", "HEAD")

	s := watchStore(t)
	seedWatchItemsTable(t, s)
	setWatchItemState(t, s, "item-lag-fix", "in-queue")

	doc := `[[rule]]
id = "trunk-proof-lag"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["git_refs:refs/heads/main,refs/tillr/green"]
[rule.when]
pattern = "lag_behind"
group_by = "global"
leader = "refs/heads/main"
follower = "refs/tillr/green"
threshold_commits = 1
[rule.guidance]
summary = "green lags main by {{count}} commit(s)"
check = ["check proof"]
do = ["advance green"]
[rule.delivery]
receiver = "foreground"
`
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Root: repo, Clock: func() time.Time { return now }}

	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var alertID string
	var lagPair sql.NullString
	var count int64
	lifecycleRead(t, s, `SELECT alert_id, tracked_lag_pair, observation_count FROM alert_episodes`, &alertID, &lagPair, &count)
	if !lagPair.Valid || lagPair.String != c1+":"+c0 || count != 1 {
		t.Fatalf("initial lag episode: pair=%+v count=%d, want %s:%s / 1", lagPair, count, c1, c0)
	}

	// AckWithItem attaches item-lag-fix on open episode and records item_id in watch.alert.acknowledged.
	itemID := "item-lag-fix"
	if err := e.AckWithItem(ctx, alertID, "operator", nil, &itemID); err != nil {
		t.Fatalf("AckWithItem: %v", err)
	}
	var ackItem string
	lifecycleRead(t, s, `SELECT json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.acknowledged'`, &ackItem)
	if ackItem != "item-lag-fix" {
		t.Fatalf("ack payload item_id = %q, want item-lag-fix", ackItem)
	}

	// Unchanged lag pair writes nothing and leaves observation_count = 1.
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT observation_count FROM alert_episodes WHERE alert_id=%q`, alertID), &count)
	if count != 1 {
		t.Fatalf("unchanged lag incremented count to %d", count)
	}

	// Distinct lag pair c2:c0 increments observation_count to 2.
	git("commit", "--quiet", "--allow-empty", "-m", "c2")
	c2 := git("rev-parse", "HEAD")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT tracked_lag_pair, observation_count FROM alert_episodes WHERE alert_id=%q`, alertID), &lagPair, &count)
	if lagPair.String != c2+":"+c0 || count != 2 {
		t.Fatalf("after c2: pair=%+v count=%d, want %s:%s / 2", lagPair, count, c2, c0)
	}

	// Follower catches up (!firing) while item-lag-fix is still open: stays acknowledged, clears tracked_lag_pair to "".
	git("update-ref", "refs/tillr/green", c2)
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var state string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, tracked_lag_pair, observation_count FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &lagPair, &count)
	if state != "acknowledged" || !lagPair.Valid || lagPair.String != "" || count != 2 {
		t.Fatalf("after catch-up while open: state=%q pair=%+v count=%d", state, lagPair, count)
	}

	// New lag c3:c2 while item-lag-fix is still open: increments observation_count to 3 without waking.
	git("commit", "--quiet", "--allow-empty", "-m", "c3")
	c3 := git("rev-parse", "HEAD")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, tracked_lag_pair, observation_count FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &lagPair, &count)
	if state != "acknowledged" || lagPair.String != c3+":"+c2 || count != 3 {
		t.Fatalf("after c3 lag: state=%q pair=%+v count=%d", state, lagPair, count)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending while lag tracked = %+v (%v), want empty", p, err)
	}

	// Follower catches up to c3 AND item-lag-fix lands -> resolves quietly with item_id in watch.alert.resolved!
	git("update-ref", "refs/tillr/green", c3)
	setWatchItemState(t, s, "item-lag-fix", "landed")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending on quiet tracked resolve = %+v (%v), want empty", p, err)
	}
	var resReason, resTrack, evItem string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &resReason, &resTrack)
	if state != "resolved" || resReason != "condition-cleared" || resTrack != "item-lag-fix" {
		t.Fatalf("quiet resolved row: state=%q reason=%q tracking=%q", state, resReason, resTrack)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved' AND object_id=%q`, alertID), &evItem)
	if evItem != "item-lag-fix" {
		t.Fatalf("resolved event item_id = %q, want item-lag-fix", evItem)
	}

	// A subsequent lag opens episode 2 fresh (untracked) and tests resolved-elsewhere reopen wording.
	git("commit", "--quiet", "--allow-empty", "-m", "c4")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ep2ID string
	var ep2Num int64
	var ep2Track sql.NullString
	lifecycleRead(t, s, `SELECT alert_id, episode_num, tracking_item_id FROM alert_episodes WHERE state='open'`, &ep2ID, &ep2Num, &ep2Track)
	if ep2Num != 2 || ep2Track.Valid {
		t.Fatalf("episode 2: num=%d track=%+v", ep2Num, ep2Track)
	}
	setWatchItemState(t, s, "item-alt", "in-queue")
	if err := e.Track(ctx, ep2ID, "item-alt", nil, nil); err != nil {
		t.Fatal(err)
	}
	setWatchItemState(t, s, "item-alt", "resolved-elsewhere")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := e.Pending(ctx, "foreground")
	if err != nil || len(reopened) != 1 {
		t.Fatalf("Pending on resolved-elsewhere reopen = %+v (%v)", reopened, err)
	}
	if !strings.HasPrefix(reopened[0].Bundle.Guidance.Summary, "tracked fix item-alt closed (resolved-elsewhere) and the condition persists: ") {
		t.Fatalf("unexpected resolved-elsewhere summary: %q", reopened[0].Bundle.Guidance.Summary)
	}
	var altCause, altState string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause'), json_extract(payload,'$.item_state') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q`, ep2ID), &altCause, &altState)
	if altCause != "tracked-item-landed" || altState != "resolved-elsewhere" {
		t.Fatalf("resolved-elsewhere event: cause=%q state=%q", altCause, altState)
	}
}

func TestTrackPrecedenceDamagedStoreAndObservationSourceParity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	t.Run("item_subject_precedence_and_damaged_items_table", func(t *testing.T) {
		s := watchStore(t)
		seedWatchItemsTable(t, s)
		setWatchItemState(t, s, "item-subject", "in-queue")
		setWatchItemState(t, s, "item-tracker", "in-queue")

		doc := `[[rule]]
id = "item-faults"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "item_id"
pattern = "count_in_window"
threshold = 1
window = "1h"
[rule.guidance]
summary = "fault on {{subject}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "foreground"
`
		e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
		watchFact(t, s, now, `{"item_id":"item-subject"}`)
		watchFact(t, s, now, `{"item_id":"non-item-check"}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var subAlertID, checkAlertID string
		lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='item-subject'`, &subAlertID)
		lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='non-item-check'`, &checkAlertID)

		if err := e.Track(ctx, subAlertID, "item-tracker", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.Track(ctx, checkAlertID, "item-tracker", nil, nil); err != nil {
			t.Fatal(err)
		}

		// D-TW63 precedence: item-subject lands while item-tracker is still open -> resolves as item-landed with item_id!
		setWatchItemState(t, s, "item-subject", "landed")
		now = now.Add(time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var subState, subReason, subEvItem string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id=%q`, subAlertID), &subState, &subReason)
		lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved' AND object_id=%q`, subAlertID), &subEvItem)
		if subState != "resolved" || subReason != "item-landed" || subEvItem != "item-tracker" {
			t.Fatalf("D-TW63 precedence: state=%q reason=%q item_id=%q", subState, subReason, subEvItem)
		}

		// Damaged store where items table is dropped: checkAlertID stays untouched (neither resolved nor reopened).
		watchSQL(t, s, `DROP TABLE items`)
		now = now.Add(2 * time.Hour)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var checkState, checkTracker string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, checkAlertID), &checkState, &checkTracker)
		if checkState != "acknowledged" || checkTracker != "item-tracker" {
			t.Fatalf("damaged items table mutated tracked episode: state=%q tracker=%q", checkState, checkTracker)
		}
	})

	t.Run("observation_source_parity", func(t *testing.T) {
		src := &memoryObservationSource{projections: map[string]any{}}
		rule := Rule{
			ID:       "slow",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"gate.done"}},
			When: When{
				GroupBy:   "check",
				Pattern:   CountInWindow,
				Threshold: 1,
				Window:    time.Hour,
			},
			Guidance: Guidance{Summary: "{{subject}} slow", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		curNow := now
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return curNow }}
		src.append("gate.done", curNow, map[string]any{"check": "lint"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("Pending = %+v (%v)", notices, err)
		}
		alertID := notices[0].AlertID
		_ = e.Shown(ctx, "foreground", notices[0].Seq)
		_ = e.Seen(ctx, "foreground", notices[0].Seq)

		// Without charter/items projections, Track refuses without mutating memEpisode.
		if err := e.Track(ctx, alertID, "item-1", nil, nil); err == nil {
			t.Fatal("expected Track refusal when ObservationSource has no charter/items projection")
		}
		st0, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil || len(st0.Alerts) != 1 || st0.Alerts[0].State != "open" || st0.Alerts[0].ItemID != nil {
			t.Fatalf("mutated memEpisode on failed Source Track: %+v (%v)", st0, err)
		}

		// Configure charter and items projections on Source.
		itemsProj := map[string]map[string]any{"item-1": {"item.state": "in-queue"}}
		src.projections["charter"] = map[string]any{"closed": "closed", "finished": []string{"landed", "closed"}}
		src.projections["items"] = itemsProj

		if err := e.Track(ctx, alertID, "item-1", nil, nil); err != nil {
			t.Fatalf("Source Track: %v", err)
		}
		curNow = curNow.Add(5 * time.Minute)
		src.append("gate.done", curNow, map[string]any{"check": "lint"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st1, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil || st1.Alerts[0].Observed != 2 || st1.Alerts[0].ItemID == nil || *st1.Alerts[0].ItemID != "item-1" {
			t.Fatalf("Source status while tracked: %+v (%v)", st1, err)
		}
		if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
			t.Fatalf("Source Pending while tracked = %+v (%v)", p, err)
		}

		// Missing item row reopens with tracked-item-closed ("missing").
		delete(itemsProj, "item-1")
		curNow = curNow.Add(time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		p, err := e.Pending(ctx, "foreground")
		if err != nil || len(p) != 1 || !strings.HasPrefix(p[0].Bundle.Guidance.Summary, "tracked fix item-1 closed and the condition persists: ") {
			t.Fatalf("Source Pending after missing item = %+v (%v)", p, err)
		}
		if err := e.Track(ctx, "alrt-missing", "item-1", nil, nil); !errors.Is(err, ErrAlertNotFound) {
			t.Fatalf("Source Track missing alert = %v, want ErrAlertNotFound", err)
		}
	})
}
