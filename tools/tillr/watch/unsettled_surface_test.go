package watch

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchStatusSummarySilencesAndUnsettledFilter(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := t.Context()
	s := watchStore(t)
	openedAt := time.Unix(1700000000, 0).UTC()
	now := openedAt.Add(14 * time.Minute)

	watchSQL(t, s, `INSERT INTO alert_episodes(
		alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint,
		opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, variables_json,
		settlement_state, silence_id, acked_by, silence_until_ms
	) VALUES
		('alrt-open', 'r1', 'sub-1', 1, 'open', 'warning', 'fp1', 1, ?, 1, ?, '{}', '{"k":"v1"}', 'uninvestigated', NULL, NULL, NULL),
		('alrt-ack', 'r1', 'sub-2', 1, 'acknowledged', 'warning', 'fp2', 2, ?, 2, ?, '{}', '{"k":"v2"}', 'investigating', NULL, 'op', NULL),
		('alrt-res-unsettled', 'r1', 'sub-3', 1, 'resolved', 'warning', 'fp3', 3, ?, 3, ?, '{}', '{"k":"v3"}', 'investigated', NULL, NULL, NULL),
		('alrt-silenced', 'r2', 'sub-4', 1, 'silenced', 'warning', 'fp4', 4, ?, 4, ?, '{}', '{"pkg":"flaky"}', 'settled', 'sil-1', NULL, ?),
		('alrt-res-settled', 'r2', 'sub-5', 1, 'resolved', 'warning', 'fp5', 5, ?, 5, ?, '{}', '{}', 'settled', NULL, NULL, NULL)`,
		openedAt.UnixMilli(), openedAt.UnixMilli(),
		openedAt.UnixMilli(), openedAt.UnixMilli(),
		openedAt.UnixMilli(), openedAt.UnixMilli(),
		openedAt.UnixMilli(), openedAt.UnixMilli(), now.Add(time.Hour).UnixMilli(),
		openedAt.UnixMilli(), openedAt.UnixMilli(),
	)
	watchSQL(t, s, `INSERT INTO watch_silences(
		silence_id, rule_id, vars_json, tracking_item_id, created_by, created_at,
		reason, investigation_cause, investigation_evidence, silence_until_ms
	) VALUES ('sil-1', 'r2', '{"pkg":"flaky"}', 'item-1', 'operator-session', ?, 'known flake', 'cause', 'ev', ?)`,
		openedAt.UnixMilli(), now.Add(time.Hour).UnixMilli(),
	)

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	e := Evaluator{Store: ro, Clock: func() time.Time { return now }}

	got, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.Unsettled != 3 || got.Summary.Uninvestigated != 1 || got.Summary.Investigating != 1 ||
		got.Summary.Investigated != 1 || got.Summary.Settled != 2 || got.Summary.ActiveSilences != 1 {
		t.Fatalf("unexpected summary: %+v", got.Summary)
	}
	if len(got.Silences) != 1 || got.Silences[0].SilenceID != "sil-1" || got.Silences[0].MatchedAlerts != 1 {
		t.Fatalf("unexpected silences: %+v", got.Silences)
	}
	// Default ReadStatus lists non-resolved episodes (open, acknowledged, silenced).
	if len(got.Alerts) != 3 {
		t.Fatalf("default status alerts = %d, want 3: %+v", len(got.Alerts), got.Alerts)
	}
	if got.Alerts[0].ID != "alrt-open" || got.Alerts[0].Settlement != "uninvestigated" || got.Alerts[0].UnsettledFor != "14m" {
		t.Fatalf("alert[0] = %+v", got.Alerts[0])
	}
	if got.Alerts[2].ID != "alrt-silenced" || got.Alerts[2].Settlement != "settled" || got.Alerts[2].UnsettledFor != "" {
		t.Fatalf("alert[2] = %+v", got.Alerts[2])
	}

	// OnlyUnsettled includes resolved-but-unsettled and excludes settled episodes.
	unsettled, err := e.ReadStatusWithOptions(ctx, func(string) (bool, error) { return false, nil }, StatusOptions{OnlyUnsettled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(unsettled.Alerts) != 3 {
		t.Fatalf("unsettled alerts = %d, want 3: %+v", len(unsettled.Alerts), unsettled.Alerts)
	}
	ids := []string{unsettled.Alerts[0].ID, unsettled.Alerts[1].ID, unsettled.Alerts[2].ID}
	if ids[0] != "alrt-open" || ids[1] != "alrt-ack" || ids[2] != "alrt-res-unsettled" {
		t.Fatalf("unsettled alert IDs = %v, want [alrt-open alrt-ack alrt-res-unsettled]", ids)
	}
	if unsettled.Alerts[2].State != "resolved" || unsettled.Alerts[2].Settlement != "investigated" || unsettled.Alerts[2].UnsettledFor != "14m" {
		t.Fatalf("resolved-but-unsettled alert = %+v", unsettled.Alerts[2])
	}
}

func TestWatchUnsettledReminders(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := t.Context()
	s := watchStore(t)
	now := time.Unix(1700000000, 0).UTC()

	doc := strings.ReplaceAll(validRule, `"test.watch"`, `"watch-test.fact"`) + `
[[rule]]
id = "bg-rule"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
pattern = "count_in_window"
group_by = "detail.item-id"
threshold = 1
window = "1h"
[rule.guidance]
summary = "bg {{subject}}"
check = ["check"]
do = ["do"]
[rule.delivery]
receiver = "subscribers"
`
	rules := watchRules(t, doc)
	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"detail":{"item-id":"fg-1"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := e.Pending(ctx, "foreground")
	if err != nil || len(pending) != 1 {
		t.Fatalf("initial pending = %+v, err=%v", pending, err)
	}
	alertID := pending[0].AlertID

	// While still pending (not yet seen), UnsettledReminders does not duplicate it.
	e.Clock = func() time.Time { return now.Add(20 * time.Minute) }
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("pending alert should not also appear as reminder: %+v, %v", rem, err)
	}

	// Confirm seen_seq past the opening event: Pending is now empty.
	if err := e.Shown(ctx, "foreground", pending[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", pending[0].Seq); err != nil {
		t.Fatal(err)
	}

	// Before remind_after (10m < 15m): no reminder yet.
	e.Clock = func() time.Time { return now.Add(10 * time.Minute) }
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("reminder fired too early: %+v, %v", rem, err)
	}

	// At 15m: seen_seq alone does not suppress the reminder.
	e.Clock = func() time.Time { return now.Add(15 * time.Minute) }
	rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute)
	if err != nil || len(rem) != 1 || rem[0].AlertID != alertID || rem[0].UnsettledFor != "15m" {
		t.Fatalf("expected reminder at 15m: %+v, %v", rem, err)
	}

	// MarkReminded suppresses until another 15m elapses.
	if err := e.MarkReminded(ctx, []string{alertID}); err != nil {
		t.Fatal(err)
	}
	e.Clock = func() time.Time { return now.Add(20 * time.Minute) }
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("reminder repeated before interval: %+v, %v", rem, err)
	}
	e.Clock = func() time.Time { return now.Add(30 * time.Minute) }
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 1 {
		t.Fatalf("expected second reminder at 30m: %+v, %v", rem, err)
	}

	// Auto-resolution leaves the episode unsettled, so reminders still fire.
	watchSQL(t, s, `UPDATE alert_episodes SET state='resolved', resolved_reason='condition-cleared' WHERE alert_id=?`, alertID)
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 1 || rem[0].State != "resolved" {
		t.Fatalf("resolved-but-unsettled alert must still remind: %+v, %v", rem, err)
	}

	// Active triage claim excludes from foreground reminders.
	watchSQL(t, s, `UPDATE alert_episodes SET active_triage_run_id='triage-1' WHERE alert_id=?`, alertID)
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("active triage claim should exclude reminder: %+v, %v", rem, err)
	}
	watchSQL(t, s, `UPDATE alert_episodes SET active_triage_run_id=NULL WHERE alert_id=?`, alertID)

	// Escalation to owner excludes from reminders.
	watchSQL(t, s, `UPDATE alert_episodes SET escalated_to='owner' WHERE alert_id=?`, alertID)
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("owner-escalated alert must be excluded: %+v, %v", rem, err)
	}

	// Non-foreground rule is excluded unless escalated_to='operator-session'.
	var bgID string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT alert_id FROM alert_episodes WHERE rule_id='bg-rule'`).Scan(&bgID)
	}); err != nil {
		t.Fatal(err)
	}
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("subscribers rule must not remind foreground: %+v, %v", rem, err)
	}
	watchSQL(t, s, `UPDATE alert_episodes SET escalated_to='operator-session' WHERE alert_id=?`, bgID)
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 1 || rem[0].AlertID != bgID {
		t.Fatalf("operator-session escalation should remind foreground: %+v, %v", rem, err)
	}

	// Settling stops reminders.
	watchSQL(t, s, `UPDATE alert_episodes SET settlement_state='settled' WHERE alert_id=?`, bgID)
	if rem, err := e.UnsettledReminders(ctx, "foreground", 15*time.Minute); err != nil || len(rem) != 0 {
		t.Fatalf("settled alert must not remind: %+v, %v", rem, err)
	}
}
