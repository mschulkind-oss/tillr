package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchStatusUnsettledAndSilenceLines(t *testing.T) {
	s, rules := fixture(t, false)
	ctx := t.Context()
	openedAt := time.Now().UTC().Add(-14 * time.Minute)
	untilAt := time.Now().UTC().Add(2 * time.Hour)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO alert_episodes(
			alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint,
			opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, variables_json,
			settlement_state, silence_id, silence_until_ms
		) VALUES
			('alrt-open', 'refusals', 'item-a', 1, 'open', 'warning', 'fp1', 1, ?, 1, ?, '{}', '{"item_id":"item-a"}', 'uninvestigated', NULL, NULL),
			('alrt-res-unsettled', 'refusals', 'item-b', 1, 'resolved', 'warning', 'fp2', 2, ?, 2, ?, '{}', '{"item_id":"item-b"}', 'investigated', NULL, NULL),
			('alrt-silenced', 'refusals', 'item-c', 1, 'silenced', 'warning', 'fp3', 3, ?, 3, ?, '{}', '{"item_id":"item-c"}', 'settled', 'sil-1', ?)`,
			openedAt.UnixMilli(), openedAt.UnixMilli(),
			openedAt.UnixMilli(), openedAt.UnixMilli(),
			openedAt.UnixMilli(), openedAt.UnixMilli(), untilAt.UnixMilli(),
		); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO watch_silences(
			silence_id, rule_id, vars_json, tracking_item_id, created_by, created_at,
			reason, investigation_cause, investigation_evidence, silence_until_ms
		) VALUES ('sil-1', 'refusals', '{"item_id":"item-c"}', 'item-fix', 'operator-session', ?, 'known flake', 'cause', 'ev', ?)`,
			openedAt.UnixMilli(), untilAt.UnixMilli(),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var out, stderr bytes.Buffer
	if code := run(ctx, []string{"status", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("status: code=%d stderr=%q", code, &stderr)
	}
	text := out.String()
	if !strings.HasPrefix(text, "summary unsettled=2 (uninvestigated=1 investigating=0 investigated=1) settled=1 active_silences=1\n") {
		t.Fatalf("missing summary header:\n%s", text)
	}
	if !strings.Contains(text, `alert alrt-open `) || !strings.Contains(text, `settlement=uninvestigated unsettled_for=`) {
		t.Fatalf("missing open unsettled alert:\n%s", text)
	}
	if !strings.Contains(text, `silence sil-1 rule=refusals vars={"item_id":"item-c"} item="item-fix" by="operator-session" until=`) || !strings.Contains(text, ` matched_alerts=1`) {
		t.Fatalf("missing active silence line:\n%s", text)
	}

	out.Reset()
	if code := run(ctx, []string{"status", "--unsettled", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("status --unsettled: code=%d stderr=%q", code, &stderr)
	}
	unsettledText := out.String()
	if !strings.HasPrefix(unsettledText, "summary unsettled=2 (uninvestigated=1 investigating=0 investigated=1) settled=1 active_silences=1\n") {
		t.Fatalf("missing summary header on --unsettled:\n%s", unsettledText)
	}
	if !strings.Contains(unsettledText, "alert alrt-open ") || !strings.Contains(unsettledText, "alert alrt-res-unsettled ") {
		t.Fatalf("--unsettled must include both open and resolved unsettled episodes:\n%s", unsettledText)
	}
	if strings.Contains(unsettledText, "alert alrt-silenced ") || strings.Contains(unsettledText, "rule refusals") {
		t.Fatalf("--unsettled should omit settled alerts and rule lines:\n%s", unsettledText)
	}

	out.Reset()
	if code := run(ctx, []string{"status", "--unsettled", "--json", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("status --unsettled --json: code=%d stderr=%q", code, &stderr)
	}
	var report struct {
		Summary struct {
			Unsettled      int `json:"unsettled"`
			Settled        int `json:"settled"`
			ActiveSilences int `json:"active_silences"`
		} `json:"summary"`
		Alerts []struct {
			ID         string `json:"alert_id"`
			State      string `json:"state"`
			Settlement string `json:"settlement"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, &out)
	}
	if report.Summary.Unsettled != 2 || report.Summary.Settled != 1 || report.Summary.ActiveSilences != 1 || len(report.Alerts) != 2 {
		t.Fatalf("unexpected --unsettled --json report: %+v", report)
	}
}

func TestWatchWaitUnsettledReminderAndFooter(t *testing.T) {
	s, rules := fixture(t, false)
	ctx := t.Context()

	// Load rule baseline and fire one alert.
	var out, stderr bytes.Buffer
	if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", rules}, &out, &stderr); code != 0 {
		t.Fatalf("baseline eval: %d %s", code, &stderr)
	}
	fact(t, s, `{"item_id":"item-a"}`)
	fact(t, s, `{"item_id":"item-a"}`)

	now := time.Now().UTC().Add(time.Second)
	clockNow := now
	hooks := &waitTestHooks{clock: func() time.Time { return clockNow }}

	// First wait delivers the opening notice with all 4 command templates.
	out.Reset()
	stderr.Reset()
	code := runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "1s", "--no-notify"}, &out, &stderr, nil, hooks)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("first wait: code=%d stderr=%q out=%q", code, &stderr, &out)
	}
	first := out.String()
	var through int64
	if _, err := fmt.Sscanf(first, "TILLR_WATCH_NOTICE 1 pending, through_seq %d", &through); err != nil {
		t.Fatalf("expected TILLR_WATCH_NOTICE: %s", first)
	}
	for _, want := range []string{"  ack: ", "  investigate: ", "  settle: ", "  silence: "} {
		if !strings.Contains(first, want) {
			t.Fatalf("notice footer missing %q:\n%s", want, first)
		}
	}

	// Confirm seen_seq at t+5m: before 15m remind_after, wait times out quietly.
	clockNow = now.Add(5 * time.Minute)
	out.Reset()
	stderr.Reset()
	code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--seen-through", fmt.Sprint(through), "--timeout", "30ms", "--no-notify"}, &out, &stderr, nil, hooks)
	if code != 2 || strings.Contains(out.String(), "UNSETTLED") || strings.Contains(out.String(), "TILLR_WATCH_NOTICE") {
		t.Fatalf("wait at 5m should time out quietly: code=%d out=%q err=%q", code, &out, &stderr)
	}

	// At t+16m: seen_seq alone does not suppress the UNSETTLED reminder.
	clockNow = now.Add(16 * time.Minute)
	out.Reset()
	stderr.Reset()
	code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "1s", "--no-notify"}, &out, &stderr, nil, hooks)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("wait at 16m should emit reminder: code=%d stderr=%q out=%q", code, &stderr, &out)
	}
	remOut := out.String()
	if !strings.Contains(remOut, "TILLR_WATCH_UNSETTLED 1 unsettled reminder(s)") ||
		!strings.Contains(remOut, "UNSETTLED [alert ") ||
		!strings.Contains(remOut, "settlement=uninvestigated") ||
		!strings.Contains(remOut, "  silence: ") {
		t.Fatalf("unexpected reminder output:\n%s", remOut)
	}

	// Immediately at t+17m: MarkReminded suppresses tight-loop repeats.
	clockNow = now.Add(17 * time.Minute)
	out.Reset()
	stderr.Reset()
	code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "30ms", "--no-notify"}, &out, &stderr, nil, hooks)
	if code != 2 || strings.Contains(out.String(), "UNSETTLED") {
		t.Fatalf("wait at 17m should not repeat reminder: code=%d out=%q err=%q", code, &out, &stderr)
	}

	// Mark settled; at t+40m no reminder fires.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET settlement_state='settled'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	clockNow = now.Add(40 * time.Minute)
	out.Reset()
	stderr.Reset()
	code = runWithWaitHooks(ctx, []string{"wait", "--store", s.Path(), "--rules", rules, "--timeout", "30ms", "--no-notify"}, &out, &stderr, nil, hooks)
	if code != 2 || strings.Contains(out.String(), "UNSETTLED") {
		t.Fatalf("settled alert must not remind: code=%d out=%q err=%q", code, &out, &stderr)
	}
}
