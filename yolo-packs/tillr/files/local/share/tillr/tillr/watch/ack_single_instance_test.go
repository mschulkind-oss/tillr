package watch

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func watchKindFact(t *testing.T, s *store.Store, at time.Time, kind, body string) int64 {
	t.Helper()
	var seq int64
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) (err error) {
		seq, err = tx.Append(event.Event{Kind: kind, At: at, Payload: []byte(body)})
		return
	}); err != nil {
		t.Fatal(err)
	}
	return seq
}

func TestAckSingleInstanceEventAndFingerprintWatermarks(t *testing.T) {
	register(t, "watch-test.fact")
	register(t, "watch-test.noise")
	ctx := context.Background()

	t.Run("store", func(t *testing.T) {
		now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		s := watchStore(t)
		doc := `[[rule]]
id = "refusals"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "subject"
pattern = "count_in_window"
threshold = 2
window = "1h"
fingerprint = ["step"]
[rule.guidance]
summary = "{{subject}} had {{count}} refusals at {{event.step}}"
check = ["inspect refusals"]
do = ["investigate"]
[rule.delivery]
receiver = "foreground"
`
		e := Evaluator{Store: s, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		watchKindFact(t, s, now, "watch-test.fact", `{"subject":"alpha","step":"build"}`)
		seq2 := watchKindFact(t, s, now.Add(time.Second), "watch-test.fact", `{"subject":"alpha","step":"build"}`)
		now = now.Add(2 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("initial pending: %+v, %v", notices, err)
		}
		alertID := notices[0].AlertID
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		note := "checking instance 1"
		now = now.Add(time.Second)
		if err := e.Ack(ctx, alertID, "operator", &note); err != nil {
			t.Fatalf("Ack: %v", err)
		}

		var state, fp string
		var ackedSeq sql.NullInt64
		var ackedFP, ackedLag sql.NullString
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, condition_fingerprint, acked_seq, acked_fingerprint, acked_lag_pair FROM alert_episodes WHERE alert_id=%q`, alertID),
			&state, &fp, &ackedSeq, &ackedFP, &ackedLag)
		if state != "acknowledged" || !ackedSeq.Valid || ackedSeq.Int64 != seq2 || !ackedFP.Valid || ackedFP.String != fp || fp == "" || ackedLag.Valid {
			t.Fatalf("watermarks after ack: state=%q fp=%q ackedSeq=%+v ackedFP=%+v ackedLag=%+v (want seq=%d)", state, fp, ackedSeq, ackedFP, ackedLag, seq2)
		}

		// Re-evaluating with unrelated events and advancing clock within window keeps state=acknowledged.
		watchKindFact(t, s, now.Add(time.Second), "watch-test.noise", `{"msg":"unrelated"}`)
		now = now.Add(5 * time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
		if state != "acknowledged" {
			t.Fatalf("same instance reopened unexpectedly: state=%q", state)
		}
		if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
			t.Fatalf("expected no pending notices on same instance, got %+v (%v)", pending, err)
		}

		// A newer matching event (same fingerprint) reopens to state='open' with cause="new-instance".
		seq3 := watchKindFact(t, s, now.Add(time.Second), "watch-test.fact", `{"subject":"alpha","step":"build"}`)
		now = now.Add(2 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var ackedBy, ackedNote sql.NullString
		var ackedAt sql.NullInt64
		var obsCount int64
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, observation_count, acked_by, acked_note, acked_at, acked_seq, acked_fingerprint, acked_lag_pair FROM alert_episodes WHERE alert_id=%q`, alertID),
			&state, &obsCount, &ackedBy, &ackedNote, &ackedAt, &ackedSeq, &ackedFP, &ackedLag)
		if state != "open" || obsCount != 2 || ackedBy.Valid || ackedNote.Valid || ackedAt.Valid || ackedSeq.Valid || ackedFP.Valid || ackedLag.Valid {
			t.Fatalf("after newer matching event (seq=%d): state=%q obs=%d by=%+v note=%+v at=%+v seq=%+v fp=%+v lag=%+v",
				seq3, state, obsCount, ackedBy, ackedNote, ackedAt, ackedSeq, ackedFP, ackedLag)
		}
		var reopenCause string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q ORDER BY seq DESC LIMIT 1`, alertID), &reopenCause)
		if reopenCause != "new-instance" {
			t.Fatalf("reopen cause = %q, want new-instance", reopenCause)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].AlertID != alertID {
			t.Fatalf("expected wake after new-instance reopen, got %+v (%v)", notices, err)
		}
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// Ack instance 2, then change the rule's fingerprint fields WITHOUT appending a newer matching event.
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatalf("second Ack: %v", err)
		}
		tunedDoc := `[[rule]]
id = "refusals"
scope = "core"
severity = "warning"
[rule.reads]
events = ["watch-test.fact"]
[rule.when]
group_by = "subject"
pattern = "count_in_window"
threshold = 2
window = "1h"
fingerprint = ["step", "subject"]
[rule.guidance]
summary = "{{subject}} had {{count}} refusals at {{event.step}}"
check = ["inspect refusals"]
do = ["investigate"]
[rule.delivery]
receiver = "foreground"
`
		e.Rules = watchRules(t, tunedDoc)
		now = now.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, acked_seq, acked_fingerprint FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &ackedSeq, &ackedFP)
		if state != "open" || ackedSeq.Valid || ackedFP.Valid {
			t.Fatalf("fingerprint change did not reopen: state=%q ackedSeq=%+v ackedFP=%+v", state, ackedSeq, ackedFP)
		}
		var reopenCount int
		lifecycleRead(t, s, fmt.Sprintf(`SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened' AND object_id=%q AND json_extract(payload,'$.cause')='new-instance'`, alertID), &reopenCount)
		if reopenCount != 2 {
			t.Fatalf("expected 2 new-instance reopen events, got %d", reopenCount)
		}

		// Ack instance 3, then advance clock past the 1h window: untracked acknowledged episode resolves with condition-cleared.
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatalf("third Ack: %v", err)
		}
		now = now.Add(2 * time.Hour)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		var resReason string
		lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &resReason)
		if state != "resolved" || resReason != "condition-cleared" {
			t.Fatalf("expected condition-cleared resolution on untracked acked episode, got state=%q reason=%q", state, resReason)
		}
	})

	t.Run("standalone_source", func(t *testing.T) {
		now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
		src := &memoryObservationSource{}
		rule := Rule{
			ID:       "refusals",
			Scope:    "*",
			Enabled:  true,
			Severity: "warning",
			Reads:    Reads{Events: []string{"ext.refusal"}},
			When: When{
				GroupBy:     "subject",
				Pattern:     CountInWindow,
				Threshold:   2,
				Window:      time.Hour,
				Fingerprint: []string{"step"},
			},
			Guidance: Guidance{Summary: "{{subject}} {{count}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		src.append("ext.refusal", now.Add(time.Second), map[string]any{"subject": "alpha", "step": "build"})
		src.append("ext.refusal", now.Add(2*time.Second), map[string]any{"subject": "alpha", "step": "build"})
		now = now.Add(3 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("initial source pending: %+v (%v)", notices, err)
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

		// Unrelated event does not reopen.
		src.append("ext.other", now.Add(time.Second), map[string]any{"subject": "alpha"})
		now = now.Add(2 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
			t.Fatalf("unexpected pending on same source instance: %+v (%v)", pending, err)
		}

		// Newer matching event reopens and wakes Pending.
		src.append("ext.refusal", now.Add(time.Second), map[string]any{"subject": "alpha", "step": "build"})
		now = now.Add(2 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].AlertID != alertID {
			t.Fatalf("expected source Pending wake on newer matching event, got %+v (%v)", notices, err)
		}
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// Ack again, then let window expire -> condition-cleared.
		if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Hour)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil || len(st.Alerts) != 0 {
			t.Fatalf("expected resolved source alert after window expiry, got %+v (%v)", st.Alerts, err)
		}
	})
}

func TestAckSingleInstanceLagBehindAndUpgradeBackfill(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 13, 0, 0, 0, time.UTC)

	root := t.TempDir()
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
	makeCommit := func(msg string) string {
		gitRun("commit", "--allow-empty", "-q", "-m", msg)
		out := gitRun("rev-parse", "HEAD")
		return out[:len(out)-1]
	}
	c1 := makeCommit("c1")
	c2 := makeCommit("c2")
	gitRun("update-ref", "refs/heads/leader", c2)
	gitRun("update-ref", "refs/heads/follower", c1)

	doc := `[[rule]]
id = "trunk-proof-lag"
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
summary = "follower {{follower_short}} lags leader {{leader_short}} by {{count}}"
check = ["inspect proof"]
do = ["advance follower"]
[rule.delivery]
receiver = "foreground"
`

	s := watchStore(t)
	e := Evaluator{Store: s, Root: root, Rules: watchRules(t, doc), Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("initial lag pending: %+v (%v)", notices, err)
	}
	alertID := notices[0].AlertID
	if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
		t.Fatal(err)
	}

	if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
		t.Fatalf("Ack lag: %v", err)
	}
	var state string
	var ackedLag sql.NullString
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, acked_lag_pair FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &ackedLag)
	if state != "acknowledged" || !ackedLag.Valid || ackedLag.String != c2+":"+c1 {
		t.Fatalf("after lag ack: state=%q ackedLag=%+v, want %s:%s", state, ackedLag, c2, c1)
	}

	// Unrelated store events advance EventHeadSeq() while refs stay c2:c1 -> must remain acknowledged!
	watchFact(t, s, now.Add(time.Second), `{}`)
	watchFact(t, s, now.Add(2*time.Second), `{}`)
	now = now.Add(3 * time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
	if state != "acknowledged" {
		t.Fatalf("lag episode reopened on unrelated store event without ref change: state=%q", state)
	}

	// Leader advances to c3 -> new leader:follower pair reopens to state='open' with cause="new-instance".
	c3 := makeCommit("c3")
	gitRun("update-ref", "refs/heads/leader", c3)
	now = now.Add(time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var cause string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, acked_lag_pair FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &ackedLag)
	lifecycleRead(t, s, fmt.Sprintf(`SELECT json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened' AND object_id=%q ORDER BY seq DESC LIMIT 1`, alertID), &cause)
	if state != "open" || ackedLag.Valid || cause != "new-instance" {
		t.Fatalf("after leader advance: state=%q ackedLag=%+v cause=%q", state, ackedLag, cause)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].AlertID != alertID {
		t.Fatalf("expected Pending wake on lag pair change, got %+v (%v)", notices, err)
	}

	// Simulate pre-Slice-1 upgrade: an acknowledged row whose acked_seq/acked_fingerprint/acked_lag_pair are NULL.
	if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, fmt.Sprintf(`UPDATE alert_episodes SET acked_seq=NULL, acked_fingerprint=NULL, acked_lag_pair=NULL WHERE alert_id=%q`, alertID))

	// First eval at the same c3:c1 pair does NOT reopen, and backfills acked_* watermarks!
	now = now.Add(time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ackedSeq sql.NullInt64
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, acked_seq, acked_lag_pair FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &ackedSeq, &ackedLag)
	if state != "acknowledged" || !ackedSeq.Valid || !ackedLag.Valid || ackedLag.String != c3+":"+c1 {
		t.Fatalf("upgrade backfill on same lag pair: state=%q ackedSeq=%+v ackedLag=%+v", state, ackedSeq, ackedLag)
	}

	// Next pair change (follower advances to c2 while still behind c3) reopens cleanly!
	gitRun("update-ref", "refs/heads/follower", c2)
	now = now.Add(time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state FROM alert_episodes WHERE alert_id=%q`, alertID), &state)
	if state != "open" {
		t.Fatalf("expected reopen when follower moved from c1 to c2 behind c3, got state=%q", state)
	}

	// Ack again, then follower catches up to c3 -> resolves with condition-cleared!
	if err := e.Ack(ctx, alertID, "operator", nil); err != nil {
		t.Fatal(err)
	}
	gitRun("update-ref", "refs/heads/follower", c3)
	now = now.Add(time.Second)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var resReason string
	lifecycleRead(t, s, fmt.Sprintf(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id=%q`, alertID), &state, &resReason)
	if state != "resolved" || resReason != "condition-cleared" {
		t.Fatalf("expected condition-cleared when follower caught up, got state=%q reason=%q", state, resReason)
	}
}
