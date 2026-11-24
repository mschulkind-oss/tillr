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
	register(t, "watch-test.fact")
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
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

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

	// Repeat 1 while open: observation_count rises, state stays acknowledged, Pending stays empty.
	now = now.Add(10 * time.Minute)
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":160000}`)
	watchFact(t, s, now, `{"median_ms":105000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending while tracked open = %+v (%v), want empty", p, err)
	}
	var runState, medState string
	var runCount, medCount int64
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count FROM alert_episodes WHERE alert_id=%q`, runAlertID), &runState, &runCount)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count FROM alert_episodes WHERE alert_id=%q`, medAlertID), &medState, &medCount)
	if runState != "acknowledged" || runCount != 2 || medState != "acknowledged" || medCount != 2 {
		t.Fatalf("while condition still holds: run=%s/%d med=%s/%d, want acknowledged/2 for both", runState, runCount, medState, medCount)
	}

	// Another evaluation while condition still holds (inside 1h window, no clear_when) keeps both acknowledged.
	now = now.Add(10 * time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count FROM alert_episodes WHERE alert_id=%q`, runAlertID), &runState, &runCount)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count FROM alert_episodes WHERE alert_id=%q`, medAlertID), &medState, &medCount)
	if runState != "acknowledged" || runCount != 2 || medState != "acknowledged" || medCount != 2 {
		t.Fatalf("steady firing pass while tracked: run=%s/%d med=%s/%d, want acknowledged/2 for both", runState, runCount, medState, medCount)
	}

	// Condition stops holding while tracked item is still open:
	// - Advance 2 hours so fast-gate-run-slow's 1h window ages out (!firing).
	// - Emit clear_when dip (median_ms = 80000) on fast-gate-median-slow (!firing + c.clears).
	// Both tracked episodes resolve with reason condition-cleared and item_id in watch.alert.resolved.
	now = now.Add(2 * time.Hour)
	watchFact(t, s, now, `{"median_ms":80000}`)
	var clearSeq int64
	lifecycleRead(t, s, `SELECT MAX(seq) FROM events`, &clearSeq)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending on condition-cleared while tracked = %+v (%v), want empty", p, err)
	}
	var runReason, runTrack, runEvReason, runEvItem string
	var medReason, medTrack, medEvReason, medEvItem string
	var medResolvedSeq int64
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, tracking_item_id, observation_count FROM alert_episodes WHERE alert_id=%q`, runAlertID), &runState, &runReason, &runTrack, &runCount)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, resolved_seq, tracking_item_id, observation_count FROM alert_episodes WHERE alert_id=%q`, medAlertID), &medState, &medReason, &medResolvedSeq, &medTrack, &medCount)
	if runState != "resolved" || runReason != "condition-cleared" || runTrack != "item-slice-d" || runCount != 2 {
		t.Fatalf("runAlert after window clear while tracked: state=%q reason=%q track=%q count=%d", runState, runReason, runTrack, runCount)
	}
	if medState != "resolved" || medReason != "condition-cleared" || medResolvedSeq != clearSeq || medTrack != "item-slice-e" || medCount != 2 {
		t.Fatalf("medAlert after clear_when while tracked: state=%q reason=%q seq=%d (want %d) track=%q count=%d", medState, medReason, medResolvedSeq, clearSeq, medTrack, medCount)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.reason'), json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved' AND object_id=%q`, runAlertID), &runEvReason, &runEvItem)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.reason'), json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved' AND object_id=%q`, medAlertID), &medEvReason, &medEvItem)
	if runEvReason != "condition-cleared" || runEvItem != "item-slice-d" || medEvReason != "condition-cleared" || medEvItem != "item-slice-e" {
		t.Fatalf("resolved events: run=%s/%s med=%s/%s", runEvReason, runEvItem, medEvReason, medEvItem)
	}

	// Firing again later opens episode_num = 2 fresh (untracked, state = open).
	setWatchItemState(t, s, "item-slice-d", "not-started")
	now = now.Add(5 * time.Minute)
	watchFact(t, s, now, `{"slowest_check":"go-affected","wall_ms":175000}`)
	watchFact(t, s, now, `{"median_ms":99000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	ep2Notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(ep2Notices) != 2 {
		t.Fatalf("Pending on re-fire after condition-cleared = %+v (%v), want 2 notices", ep2Notices, err)
	}
	ep2Through := ep2Notices[len(ep2Notices)-1].Seq
	_ = e.Shown(ctx, "foreground", ep2Through)
	_ = e.Seen(ctx, "foreground", ep2Through)

	var runEp2ID, medEp2ID string
	for _, n := range ep2Notices {
		switch n.Bundle.RuleID {
		case "fast-gate-run-slow":
			runEp2ID = n.AlertID
		case "fast-gate-median-slow":
			medEp2ID = n.AlertID
		}
	}
	var runEp2Num, medEp2Num int64
	var runEp2Track, medEp2Track sql.NullString
	lifecycleRead(t, s, fmt.Sprintf(`SELECT episode_num, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, runEp2ID), &runEp2Num, &runEp2Track)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT episode_num, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, medEp2ID), &medEp2Num, &medEp2Track)
	if runEp2Num != 2 || runEp2Track.Valid || medEp2Num != 2 || medEp2Track.Valid {
		t.Fatalf("fresh episode 2 rows: run=%d/%+v med=%d/%+v", runEp2Num, runEp2Track, medEp2Num, medEp2Track)
	}

	// Attach open items again; also cover clear + re-fire between two evaluations on medEp2ID.
	if err := e.Track(ctx, runEp2ID, "item-slice-d", &by, &note); err != nil {
		t.Fatal(err)
	}
	if err := e.Track(ctx, medEp2ID, "item-slice-e", nil, nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	watchFact(t, s, now, `{"median_ms":80000}`)
	var midClearSeq int64
	lifecycleRead(t, s, `SELECT MAX(seq) FROM events`, &midClearSeq)
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"median_ms":98000}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE alert_id=%q`, medEp2ID), &medState, &medReason, &medResolvedSeq)
	if medState != "resolved" || medReason != "condition-cleared" || medResolvedSeq != midClearSeq {
		t.Fatalf("medEp2 clear+refire in one pass: state=%q reason=%q seq=%d (want %d)", medState, medReason, medResolvedSeq, midClearSeq)
	}
	ep3Notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(ep3Notices) != 1 || ep3Notices[0].Bundle.RuleID != "fast-gate-median-slow" {
		t.Fatalf("Pending on clear+refire in one pass = %+v (%v), want fast-gate-median-slow episode 3", ep3Notices, err)
	}
	medEp3ID := ep3Notices[0].AlertID
	_ = e.Shown(ctx, "foreground", ep3Notices[0].Seq)
	_ = e.Seen(ctx, "foreground", ep3Notices[0].Seq)
	if err := e.Track(ctx, medEp3ID, "item-slice-e", nil, nil); err != nil {
		t.Fatal(err)
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
	if got := byAlert[runEp2ID].Bundle.Guidance.Summary; !strings.HasPrefix(got, "tracked fix item-slice-d landed and the condition persists: fast-gate check go-affected ran ") {
		t.Fatalf("runEp2 reopened summary = %q", got)
	}
	if got := byAlert[medEp3ID].Bundle.Guidance.Summary; got != "tracked fix item-slice-e closed and the condition persists: fast-gate median exceeded 90s" {
		t.Fatalf("medEp3 reopened summary = %q", got)
	}

	// Check reopened events and cleared tracking fields.
	var runCause, runItem, runItemState string
	var runTracking sql.NullString
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, runEp2ID), &runState, &runCount, &runTracking)
	if runState != "open" || runCount != 2 || runTracking.Valid {
		t.Fatalf("reopened runEp2 row: state=%q count=%d tracking=%+v", runState, runCount, runTracking)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause'), json_extract(payload,'$.item_id'), json_extract(payload,'$.item_state') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q`, runEp2ID), &runCause, &runItem, &runItemState)
	if runCause != "tracked-item-landed" || runItem != "item-slice-d" || runItemState != "landed" {
		t.Fatalf("runEp2 reopened event: cause=%q item=%q state=%q", runCause, runItem, runItemState)
	}
	var medCause, medItem, medItemState string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause'), json_extract(payload,'$.item_id'), json_extract(payload,'$.item_state') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q`, medEp3ID), &medCause, &medItem, &medItemState)
	if medCause != "tracked-item-closed" || medItem != "item-slice-e" || medItemState != "closed" {
		t.Fatalf("medEp3 reopened event: cause=%q item=%q state=%q", medCause, medItem, medItemState)
	}

	// Confirm the reopened notices; subsequent evaluations must neither re-wake nor
	// falsely re-resolve medEp3 from the earlier median_ms=80000 clear at seq <= ep.last!
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
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, medEp3ID), &medState)
	if medState != "open" {
		t.Fatalf("medEp3 falsely resolved by earlier pre-last clear: state=%q, want open", medState)
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

	// Follower catches up (!firing) while item-lag-fix is still open: resolves with reason condition-cleared.
	git("update-ref", "refs/tillr/green", c2)
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
		t.Fatalf("Pending after lag catch-up while open = %+v (%v), want empty", p, err)
	}
	var state, resReason, resTrack, evReason, evItem string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, tracking_item_id, observation_count FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &resReason, &resTrack, &count)
	if state != "resolved" || resReason != "condition-cleared" || resTrack != "item-lag-fix" || count != 2 {
		t.Fatalf("after catch-up while open: state=%q reason=%q track=%q count=%d", state, resReason, resTrack, count)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.reason'), json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved' AND object_id=%q`, alertID), &evReason, &evItem)
	if evReason != "condition-cleared" || evItem != "item-lag-fix" {
		t.Fatalf("resolved event while open: reason=%q item_id=%q", evReason, evItem)
	}

	// New lag c3:c2 while item-lag-fix is still open opens episode 2 fresh (untracked).
	git("commit", "--quiet", "--allow-empty", "-m", "c3")
	c3 := git("rev-parse", "HEAD")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	ep2Notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(ep2Notices) != 1 {
		t.Fatalf("Pending on fresh lag episode 2 = %+v (%v), want 1", ep2Notices, err)
	}
	ep2ID := ep2Notices[0].AlertID
	_ = e.Shown(ctx, "foreground", ep2Notices[0].Seq)
	_ = e.Seen(ctx, "foreground", ep2Notices[0].Seq)
	var ep2Num int64
	var ep2Track sql.NullString
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, episode_num, tracking_item_id, tracked_lag_pair, observation_count FROM alert_episodes WHERE alert_id=%q`, ep2ID), &state, &ep2Num, &ep2Track, &lagPair, &count)
	if state != "open" || ep2Num != 2 || ep2Track.Valid || lagPair.String != c3+":"+c2 || count != 1 {
		t.Fatalf("after c3 lag: state=%q num=%d track=%+v pair=%+v count=%d", state, ep2Num, ep2Track, lagPair, count)
	}
	if err := e.Track(ctx, ep2ID, "item-lag-fix", nil, nil); err != nil {
		t.Fatal(err)
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
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason, tracking_item_id FROM alert_episodes WHERE alert_id=%q`, ep2ID), &state, &resReason, &resTrack)
	if state != "resolved" || resReason != "condition-cleared" || resTrack != "item-lag-fix" {
		t.Fatalf("quiet resolved row: state=%q reason=%q tracking=%q", state, resReason, resTrack)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved' AND object_id=%q`, ep2ID), &evItem)
	if evItem != "item-lag-fix" {
		t.Fatalf("resolved event item_id = %q, want item-lag-fix", evItem)
	}

	// A subsequent lag opens episode 3 fresh (untracked) and tests resolved-elsewhere reopen wording.
	git("commit", "--quiet", "--allow-empty", "-m", "c4")
	now = now.Add(time.Minute)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ep3ID string
	var ep3Num int64
	var ep3Track sql.NullString
	lifecycleRead(t, s, `SELECT alert_id, episode_num, tracking_item_id FROM alert_episodes WHERE state='open'`, &ep3ID, &ep3Num, &ep3Track)
	if ep3Num != 3 || ep3Track.Valid {
		t.Fatalf("episode 3: num=%d track=%+v", ep3Num, ep3Track)
	}
	setWatchItemState(t, s, "item-alt", "in-queue")
	if err := e.Track(ctx, ep3ID, "item-alt", nil, nil); err != nil {
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
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause'), json_extract(payload,'$.item_state') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q`, ep3ID), &altCause, &altState)
	if altCause != "tracked-item-landed" || altState != "resolved-elsewhere" {
		t.Fatalf("resolved-elsewhere event: cause=%q state=%q", altCause, altState)
	}
}

func TestTrackPrecedenceDamagedStoreAndObservationSourceParity(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	t.Run("item_subject_precedence_and_damaged_items_table", func(t *testing.T) {
		register(t, "watch-test.fact")
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
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
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
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
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

		// Clearing while item-1 is still open resolves the episode with condition-cleared and item_id.
		curNow = curNow.Add(2 * time.Hour)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		stCleared, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil || len(stCleared.Alerts) != 0 {
			t.Fatalf("Source status after clear while item open = %+v (%v), want 0 active alerts", stCleared, err)
		}
		if len(e.mem.episodes) != 1 || e.mem.episodes[0].state != "resolved" || e.mem.episodes[0].resolvedReason.String != "condition-cleared" || e.mem.episodes[0].trackingItemID.String != "item-1" {
			t.Fatalf("Source memEpisode after clear = %+v", e.mem.episodes[0])
		}
		var resolvedEventPayload map[string]any
		for _, ev := range e.mem.events {
			if ev.objectID == alertID && ev.kind == resolvedKind {
				if err := json.Unmarshal(ev.payload, &resolvedEventPayload); err != nil {
					t.Fatal(err)
				}
			}
		}
		if resolvedEventPayload["reason"] != "condition-cleared" || resolvedEventPayload["item_id"] != "item-1" {
			t.Fatalf("Source watch.alert.resolved payload = %+v", resolvedEventPayload)
		}

		// Re-firing later opens fresh episode 2 untracked; track it again to test item disappearance.
		curNow = curNow.Add(5 * time.Minute)
		src.append("gate.done", curNow, map[string]any{"check": "lint"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		ep2Notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(ep2Notices) != 1 || e.mem.episodes[1].episodeNum != 2 || e.mem.episodes[1].trackingItemID.Valid {
			t.Fatalf("Source episode 2 = %+v (%v)", ep2Notices, err)
		}
		alertID2 := ep2Notices[0].AlertID
		_ = e.Shown(ctx, "foreground", ep2Notices[0].Seq)
		_ = e.Seen(ctx, "foreground", ep2Notices[0].Seq)
		if err := e.Track(ctx, alertID2, "item-1", nil, nil); err != nil {
			t.Fatalf("Source Track ep2: %v", err)
		}

		// Missing item row on a windowed rule does not reopen until a new matching event is recorded.
		delete(itemsProj, "item-1")
		curNow = curNow.Add(time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
			t.Fatalf("Source Pending after missing item without new event = %+v (%v), want empty", p, err)
		}
		curNow = curNow.Add(time.Minute)
		src.append("gate.done", curNow, map[string]any{"check": "lint"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		p, err := e.Pending(ctx, "foreground")
		if err != nil || len(p) != 1 || !strings.HasPrefix(p[0].Bundle.Guidance.Summary, "tracked fix item-1 closed and the condition persists: ") {
			t.Fatalf("Source Pending after post-close event = %+v (%v)", p, err)
		}
		if err := e.Track(ctx, "alrt-missing", "item-1", nil, nil); !errors.Is(err, ErrAlertNotFound) {
			t.Fatalf("Source Track missing alert = %v, want ErrAlertNotFound", err)
		}
	})
}

func TestTrackWindowedRuleReopensOnlyAfterPostTerminalEvent(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 17, 16, 0, 0, time.UTC)

	t.Run("windowed_predicate_and_count_wait_for_post_landing_event_while_present_state_reopens", func(t *testing.T) {
		s := watchStore(t)
		seedWatchItemsTable(t, s)
		setWatchItemState(t, s, "fixer-revert", "in-queue")
		setWatchItemState(t, s, "fixer-count", "in-queue")
		setWatchItemState(t, s, "fixer-latch", "in-queue")
		setWatchItemState(t, s, "fixer-stall", "in-queue")

		doc := `[[rule]]
id = "drain-revert-not-possible"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "landing"
pattern = "predicate_match"
match = 'event.result == "not-possible"'
window = "1h"
[rule.guidance]
summary = "The drain could not revert landing {{subject}}."
check = ["check revert"]
do = ["fix trunk"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "windowed-count"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "check"
pattern = "count_in_window"
match = 'event.slow == true'
threshold = 1
window = "1h"
[rule.guidance]
summary = "check {{subject}} was slow"
check = ["check timing"]
do = ["speed up"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "present-state-latch"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "backend"
pattern = "predicate_match"
match = 'event.closed == true'
clear_when = 'event.closed == false'
[rule.guidance]
summary = "backend {{subject}} is closed"
check = ["check backend"]
do = ["reopen backend"]
[rule.delivery]
receiver = "foreground"

[[rule]]
id = "present-state-stall"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "runner"
pattern = "duration_without_event"
after = 'event.phase == "started"'
match = 'event.phase == "ended"'
threshold_duration = "10m"
window = "24h"
[rule.guidance]
summary = "runner {{subject}} stalled"
check = ["check runner"]
do = ["unstick runner"]
[rule.delivery]
receiver = "foreground"
`
		e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		// Record original events inside the 1h window.
		watchFact(t, s, now, `{"landing":"a42c74ee9196","result":"not-possible"}`)
		watchFact(t, s, now, `{"check":"go-affected","slow":true}`)
		watchFact(t, s, now, `{"backend":"pi","closed":true}`)
		watchFact(t, s, now, `{"runner":"heavy","phase":"started"}`)
		now = now.Add(15 * time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		initial, err := e.Pending(ctx, "foreground")
		if err != nil || len(initial) != 4 {
			t.Fatalf("initial Pending = %+v (%v), want 4 notices", initial, err)
		}
		through := initial[len(initial)-1].Seq
		if err := e.Shown(ctx, "foreground", through); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", through); err != nil {
			t.Fatal(err)
		}

		alerts := map[string]string{}
		for _, n := range initial {
			alerts[n.Bundle.RuleID] = n.AlertID
		}
		if err := e.Track(ctx, alerts["drain-revert-not-possible"], "fixer-revert", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.Track(ctx, alerts["windowed-count"], "fixer-count", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.Track(ctx, alerts["present-state-latch"], "fixer-latch", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.Track(ctx, alerts["present-state-stall"], "fixer-stall", nil, nil); err != nil {
			t.Fatal(err)
		}

		// Land all tracked items 25 minutes after the original events (still inside the 1h window).
		now = now.Add(10 * time.Minute)
		setWatchItemState(t, s, "fixer-revert", "landed")
		setWatchItemState(t, s, "fixer-count", "closed")
		setWatchItemState(t, s, "fixer-latch", "landed")
		setWatchItemState(t, s, "fixer-stall", "landed")
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		// Present-state rules (latch and duration_without_event) reopen immediately;
		// windowed event rules (predicate_match with window, count_in_window) do NOT raise a notice.
		afterLand, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		gotRules := map[string]Notice{}
		for _, n := range afterLand {
			gotRules[n.Bundle.RuleID] = n
		}
		if len(gotRules) != 2 || gotRules["present-state-latch"].AlertID == "" || gotRules["present-state-stall"].AlertID == "" {
			t.Fatalf("after landing with original events in window, Pending rules = %+v, want only present-state-latch and present-state-stall", gotRules)
		}
		if _, bad := gotRules["drain-revert-not-possible"]; bad {
			t.Fatalf("windowed predicate_match falsely reopened without a post-landing event: %+v", gotRules["drain-revert-not-possible"])
		}
		if _, bad := gotRules["windowed-count"]; bad {
			t.Fatalf("windowed count_in_window falsely reopened without a post-landing event: %+v", gotRules["windowed-count"])
		}

		through = afterLand[len(afterLand)-1].Seq
		if err := e.Shown(ctx, "foreground", through); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", through); err != nil {
			t.Fatal(err)
		}

		// Record new matching events after the tracked items landed/closed -> notices ARE raised!
		now = now.Add(5 * time.Minute)
		watchFact(t, s, now, `{"landing":"a42c74ee9196","result":"not-possible"}`)
		watchFact(t, s, now, `{"check":"go-affected","slow":true}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		afterNewEvent, err := e.Pending(ctx, "foreground")
		if err != nil || len(afterNewEvent) != 2 {
			t.Fatalf("Pending after post-landing matching events = %+v (%v), want 2 notices", afterNewEvent, err)
		}
		byRule := map[string]Notice{}
		for _, n := range afterNewEvent {
			byRule[n.Bundle.RuleID] = n
		}
		if got := byRule["drain-revert-not-possible"].Bundle.Guidance.Summary; got != "tracked fix fixer-revert landed and the condition persists: The drain could not revert landing a42c74ee9196." {
			t.Fatalf("revert reopened summary = %q", got)
		}
		if got := byRule["windowed-count"].Bundle.Guidance.Summary; got != "tracked fix fixer-count closed and the condition persists: check go-affected was slow" {
			t.Fatalf("count reopened summary = %q", got)
		}
	})

	t.Run("event_seq_ignores_pre_landing_unobserved_repeat_and_window_expiry_resolves_quietly", func(t *testing.T) {
		s := watchStore(t)
		watchSQL(t, s, `CREATE TABLE IF NOT EXISTS items(item_id TEXT PRIMARY KEY, state TEXT, seat TEXT, waiting_on TEXT, event_seq INTEGER NOT NULL DEFAULT 0)`)
		watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES('fixer-seq', 'in-queue', 0), ('fixer-expire', 'in-queue', 0)`)

		doc := `[[rule]]
id = "drain-revert-not-possible"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "landing"
pattern = "predicate_match"
match = 'event.result == "not-possible"'
window = "1h"
[rule.guidance]
summary = "The drain could not revert landing {{subject}}."
check = ["check revert"]
do = ["fix trunk"]
[rule.delivery]
receiver = "foreground"
`
		curNow := now
		e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return curNow }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		watchFact(t, s, curNow, `{"landing":"land-seq","result":"not-possible"}`)
		watchFact(t, s, curNow, `{"landing":"land-expire","result":"not-possible"}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		initial, err := e.Pending(ctx, "foreground")
		if err != nil || len(initial) != 2 {
			t.Fatalf("initial Pending = %+v (%v), want 2", initial, err)
		}
		through := initial[len(initial)-1].Seq
		_ = e.Shown(ctx, "foreground", through)
		_ = e.Seen(ctx, "foreground", through)

		bySub := map[string]string{}
		for _, n := range initial {
			bySub[n.Bundle.Subject] = n.AlertID
		}
		if err := e.Track(ctx, bySub["land-seq"], "fixer-seq", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.Track(ctx, bySub["land-expire"], "fixer-expire", nil, nil); err != nil {
			t.Fatal(err)
		}

		// A repeat arrives for land-seq BEFORE fixer-seq lands, without an evaluation in between.
		curNow = curNow.Add(10 * time.Minute)
		watchFact(t, s, curNow, `{"landing":"land-seq","result":"not-possible"}`)
		// Now fixer-seq and fixer-expire land at a later event_seq.
		curNow = curNow.Add(5 * time.Minute)
		watchFact(t, s, curNow, `{"unrelated":true}`)
		var landSeq int64
		lifecycleRead(t, s, `SELECT MAX(seq) FROM events`, &landSeq)
		watchSQL(t, s, fmt.Sprintf(`UPDATE items SET state='landed', event_seq=%d WHERE item_id IN ('fixer-seq', 'fixer-expire')`, landSeq))

		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
			t.Fatalf("Pending after pre-landing repeat + landing = %+v (%v), want empty", p, err)
		}
		var obsCount int64
		lifecycleRead(t, s, fmt.Sprintf(`SELECT observation_count FROM alert_episodes WHERE alert_id=%q`, bySub["land-seq"]), &obsCount)
		if obsCount != 2 {
			t.Fatalf("pre-landing repeat observation_count = %d, want 2", obsCount)
		}

		// Record a new matching event for land-seq AFTER landSeq -> reopens!
		curNow = curNow.Add(5 * time.Minute)
		watchFact(t, s, curNow, `{"landing":"land-seq","result":"not-possible"}`)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		p, err := e.Pending(ctx, "foreground")
		if err != nil || len(p) != 1 || p[0].AlertID != bySub["land-seq"] {
			t.Fatalf("Pending after post-landing event = %+v (%v), want land-seq only", p, err)
		}
		if !strings.HasPrefix(p[0].Bundle.Guidance.Summary, "tracked fix fixer-seq landed and the condition persists: ") {
			t.Fatalf("unexpected summary: %q", p[0].Bundle.Guidance.Summary)
		}
		_ = e.Shown(ctx, "foreground", p[0].Seq)
		_ = e.Seen(ctx, "foreground", p[0].Seq)

		// Advance past the 1h window with no new event for land-expire -> resolves quietly!
		curNow = curNow.Add(2 * time.Hour)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if p, err := e.Pending(ctx, "foreground"); err != nil || len(p) != 0 {
			t.Fatalf("Pending after window expiry = %+v (%v), want empty", p, err)
		}
		var state, reason string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id=%q`, bySub["land-expire"]), &state, &reason)
		if state != "resolved" || reason != "condition-cleared" {
			t.Fatalf("land-expire after window expiry: state=%q reason=%q, want resolved/condition-cleared", state, reason)
		}
	})
}
