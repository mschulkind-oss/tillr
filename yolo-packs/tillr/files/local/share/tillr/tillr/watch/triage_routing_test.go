package watch

import (
	"context"
	"testing"
	"time"
)

func TestLoadRuleDocumentAcceptsTriageAndOperatorSessionReceivers(t *testing.T) {
	register(t, "watch-test.fact")

	for _, receiver := range []string{"triage", "operator-session", "foreground", "subscribers"} {
		doc := `
[[rule]]
id = "recv-` + receiver + `"
scope = "core"
severity = "warning"

[rule.reads]
events = ["watch-test.fact"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
window = "1h"

[rule.guidance]
summary = "summary"
check = ["check"]
do = ["do"]

[rule.delivery]
receiver = "` + receiver + `"
also_notify = ["operator-session", "triage"]
`
		rules, err := LoadRuleDocument("test.toml", []byte(doc))
		if err != nil {
			t.Fatalf("LoadRuleDocument(receiver=%q): %v", receiver, err)
		}
		if len(rules) != 1 || rules[0].Delivery.Receiver != receiver {
			t.Fatalf("got rules %+v, want receiver %q", rules, receiver)
		}
	}
}

func TestPerOwnerDeliveryIsolationAndNoTimerPromotion(t *testing.T) {
	register(t, "watch-test.triage", "watch-test.triage-clear", "watch-test.ops", "watch-test.fg")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	rules := []Rule{
		{
			ID:       "rule-triage",
			Scope:    "core",
			Enabled:  true,
			Severity: "warning",
			Reads:    Reads{Events: []string{"watch-test.triage", "watch-test.triage-clear"}},
			When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, Match: mustExpr(t, `event.kind == "watch-test.triage"`), ClearWhen: mustExpr(t, `event.kind == "watch-test.triage-clear"`)},
			Guidance: Guidance{Summary: "triage alert", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "triage"},
		},
		{
			ID:       "rule-ops",
			Scope:    "core",
			Enabled:  true,
			Severity: "warning",
			Reads:    Reads{Events: []string{"watch-test.ops"}},
			When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour},
			Guidance: Guidance{Summary: "ops alert", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "operator-session"},
		},
		{
			ID:       "rule-fg",
			Scope:    "core",
			Enabled:  true,
			Severity: "warning",
			Reads:    Reads{Events: []string{"watch-test.fg"}},
			When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour},
			Guidance: Guidance{Summary: "fg alert", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		},
	}

	e := Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	// Open one episode on each rule.
	watchFactKind(t, s, now, "watch-test.triage", `{}`)
	watchFactKind(t, s, now, "watch-test.ops", `{}`)
	watchFactKind(t, s, now, "watch-test.fg", `{}`)
	evaluateLifecycle(t, &e)

	// tillr-triage receives ONLY rule-triage.
	triageNotices, err := e.Pending(ctx, "tillr-triage")
	if err != nil {
		t.Fatal(err)
	}
	if len(triageNotices) != 1 || triageNotices[0].Bundle.RuleID != "rule-triage" {
		t.Fatalf("tillr-triage pending = %+v, want only rule-triage", triageNotices)
	}
	triageAlertID := triageNotices[0].AlertID

	// operator-session receives rule-ops and uncutover rule-fg, never rule-triage.
	opsNotices, err := e.Pending(ctx, "operator-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(opsNotices) != 2 {
		t.Fatalf("operator-session pending = %+v, want 2 (rule-ops, rule-fg)", opsNotices)
	}
	opsRuleIDs := map[string]bool{}
	for _, n := range opsNotices {
		opsRuleIDs[n.Bundle.RuleID] = true
	}
	if !opsRuleIDs["rule-ops"] || !opsRuleIDs["rule-fg"] || opsRuleIDs["rule-triage"] {
		t.Fatalf("operator-session got unexpected rule set: %v", opsRuleIDs)
	}

	// foreground receives ONLY rule-fg, neither rule-triage nor rule-ops.
	fgNotices, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(fgNotices) != 1 || fgNotices[0].Bundle.RuleID != "rule-fg" {
		t.Fatalf("foreground pending = %+v, want only rule-fg", fgNotices)
	}

	// Independent cursors: advancing tillr-triage's cursor does not affect operator-session.
	if err := e.Shown(ctx, "tillr-triage", triageNotices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "tillr-triage", triageNotices[0].Seq); err != nil {
		t.Fatal(err)
	}
	if after, err := e.Pending(ctx, "tillr-triage"); err != nil || len(after) != 0 {
		t.Fatalf("tillr-triage after Seen = %+v, %v; want empty", after, err)
	}
	if opsStill, err := e.Pending(ctx, "operator-session"); err != nil || len(opsStill) != 2 {
		t.Fatalf("operator-session pending changed after tillr-triage Seen: %+v, %v", opsStill, err)
	}

	// Advance operator-session's cursor past its notices without settling them.
	opsLastSeq := opsNotices[len(opsNotices)-1].Seq
	if err := e.Shown(ctx, "operator-session", opsLastSeq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "operator-session", opsLastSeq); err != nil {
		t.Fatal(err)
	}

	// Status reports per-owner pending counts cleanly.
	st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	ownerPending := map[string]int{}
	for _, o := range st.Owners {
		ownerPending[o.Owner] = o.Pending
	}
	if ownerPending["tillr-triage"] != 0 || ownerPending["operator-session"] != 0 {
		t.Fatalf("unexpected status owner pending counts after Seen: %v", ownerPending)
	}

	// OQ-AT3: No timer promotes an untriaged alert. Advance clock by 24h:
	// rule-triage remains untriaged and MUST NOT appear in operator-session Pending or UnsettledReminders.
	now = now.Add(24 * time.Hour)
	evaluateLifecycle(t, &e)

	if opsAfter24h, err := e.Pending(ctx, "operator-session"); err != nil || len(opsAfter24h) != 0 {
		t.Fatalf("operator-session Pending promoted untriaged alert after 24h: %+v, %v", opsAfter24h, err)
	}
	opsReminders, err := e.UnsettledReminders(ctx, "operator-session", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, rem := range opsReminders {
		if rem.RuleID == "rule-triage" {
			t.Fatalf("timer promoted untriaged triage alert %s to operator-session reminders", rem.AlertID)
		}
	}
	if len(opsReminders) != 2 {
		t.Fatalf("expected 2 reminders for operator-session (rule-ops, rule-fg), got %+v", opsReminders)
	}
	// tillr-triage receives no unsettled reminders.
	if triageRem, err := e.UnsettledReminders(ctx, "tillr-triage", 15*time.Minute); err != nil || len(triageRem) != 0 {
		t.Fatalf("tillr-triage should not receive reminders, got %+v, %v", triageRem, err)
	}

	// Claim and escalate rule-triage to operator-session:
	// it must now wake operator-session (not tillr-triage) with Bundle.Triage.
	if err := e.ClaimTriage(ctx, triageAlertID, "run-triage-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, triageAlertID, RecordTriageOptions{
		RunID:        "run-triage-1",
		Outcome:      "escalated",
		EscalateTo:   "operator-session",
		Finding:      "ambiguous cause",
		ActionNeeded: "inspect store",
	}); err != nil {
		t.Fatal(err)
	}
	if tAfterEsc, err := e.Pending(ctx, "tillr-triage"); err != nil || len(tAfterEsc) != 0 {
		t.Fatalf("escalated alert must not wake tillr-triage: %+v, %v", tAfterEsc, err)
	}
	opsEsc, err := e.Pending(ctx, "operator-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(opsEsc) != 1 || opsEsc[0].AlertID != triageAlertID || opsEsc[0].Bundle.Triage == nil {
		t.Fatalf("expected 1 escalated notice for operator-session with Triage header, got %+v", opsEsc)
	}
	// While the escalation is unseen in Pending, UnsettledReminders must not duplicate it.
	opsReminders, err = e.UnsettledReminders(ctx, "operator-session", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, rem := range opsReminders {
		if rem.AlertID == triageAlertID {
			t.Fatalf("unseen escalated alert %s duplicated in UnsettledReminders", triageAlertID)
		}
	}
	// Once operator-session confirms Seen without settling, UnsettledReminders includes it.
	if err := e.Shown(ctx, "operator-session", opsEsc[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "operator-session", opsEsc[0].Seq); err != nil {
		t.Fatal(err)
	}
	opsReminders, err = e.UnsettledReminders(ctx, "operator-session", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	foundEscRem := false
	for _, rem := range opsReminders {
		if rem.AlertID == triageAlertID {
			foundEscRem = true
		}
	}
	if !foundEscRem {
		t.Fatalf("expected seen-but-unsettled escalated alert %s in operator-session reminders, got %+v", triageAlertID, opsReminders)
	}

	// Auto-resolved uninvestigated triage episode is still delivered to tillr-triage,
	// and never promoted to operator-session even after hours elapse.
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.triage-clear", `{}`)
	evaluateLifecycle(t, &e)

	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.triage", `{}`)
	evaluateLifecycle(t, &e)
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.triage-clear", `{}`)
	evaluateLifecycle(t, &e)

	triageAuto, err := e.Pending(ctx, "tillr-triage")
	if err != nil {
		t.Fatal(err)
	}
	if len(triageAuto) != 1 || triageAuto[0].Bundle.RuleID != "rule-triage" {
		t.Fatalf("expected auto-resolved uninvestigated triage episode for tillr-triage, got %+v", triageAuto)
	}
	now = now.Add(3 * time.Hour)
	if opsAuto, err := e.Pending(ctx, "operator-session"); err != nil || len(opsAuto) != 0 {
		t.Fatalf("auto-resolved triage episode must not wake operator-session: %+v, %v", opsAuto, err)
	}
	opsAutoRem, err := e.UnsettledReminders(ctx, "operator-session", 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, rem := range opsAutoRem {
		if rem.AlertID == triageAuto[0].AlertID {
			t.Fatalf("auto-resolved untriaged alert %s promoted to operator-session reminders", rem.AlertID)
		}
	}
}

func TestMemorySourcePerOwnerDelivery(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	src := &memoryObservationSource{}
	rules := []Rule{
		{
			ID:       "mem-triage",
			Enabled:  true,
			Severity: "warning",
			Reads:    Reads{Events: []string{"src.triage"}},
			When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, Match: mustExpr(t, `event.kind == "src.triage"`)},
			Guidance: Guidance{Summary: "mem triage", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "triage"},
		},
		{
			ID:       "mem-ops",
			Enabled:  true,
			Severity: "warning",
			Reads:    Reads{Events: []string{"src.ops"}},
			When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, Match: mustExpr(t, `event.kind == "src.ops"`)},
			Guidance: Guidance{Summary: "mem ops", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "operator-session"},
		},
	}
	e := Evaluator{Source: src, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	src.append("src.triage", now, map[string]any{})
	src.append("src.ops", now, map[string]any{})
	res, err := e.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	triageNotices, err := e.Pending(ctx, "tillr-triage")
	if err != nil || len(triageNotices) != 1 || triageNotices[0].Bundle.RuleID != "mem-triage" {
		t.Fatalf("memory tillr-triage pending = %+v, %v (results=%+v); want mem-triage", triageNotices, err, res)
	}
	opsNotices, err := e.Pending(ctx, "operator-session")
	if err != nil || len(opsNotices) != 1 || opsNotices[0].Bundle.RuleID != "mem-ops" {
		t.Fatalf("memory operator-session pending = %+v, %v; want mem-ops", opsNotices, err)
	}
}

func TestLintLivenessReceiverCheck(t *testing.T) {
	register(t, "drain.loop-pass", "drain.loop-ended", "drain.heavy-progress", "drain.heavy-fault", "drain.build-started", "drain.item-fault")
	ctx := context.Background()

	// 1. duration_without_event watching absence of drain.loop-pass routed to triage => rejected.
	t.Run("rejects-loop-no-pass-on-triage", func(t *testing.T) {
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/watch/loop-no-pass.toml", `
[[rule]]
id = "drain-loop-no-pass"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-pass"]

[rule.when]
group_by = "global"
pattern = "duration_without_event"
after = 'event.kind == "drain.loop-pass"'
threshold_duration = "5m"
window = "24h"

[rule.guidance]
summary = "No loop pass for {{elapsed}}."
check = ["Check drain loop."]
do = ["Restart drain loop."]

[rule.delivery]
receiver = "triage"
`)
		findings, err := Lint(ctx, LintOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		assertHasLintCode(t, findings, CodeWatchLintLivenessReceiver, "drain-loop-no-pass")
	})

	// 2. predicate_match watching drain.loop-ended routed to triage => rejected.
	t.Run("rejects-loop-ended-on-triage", func(t *testing.T) {
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/watch/loop-fault.toml", `
[[rule]]
id = "drain-loop-fault"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.stop == "fault"'
window = "1h"

[rule.guidance]
summary = "Drain loop faulted."
check = ["Check drain loop log."]
do = ["Diagnose fault."]

[rule.delivery]
receiver = "triage"
`)
		findings, err := Lint(ctx, LintOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		assertHasLintCode(t, findings, CodeWatchLintLivenessReceiver, "drain-loop-fault")
	})

	// 3. Multi-event rule explicitly matching drain.loop-ended routed to triage => rejected.
	t.Run("rejects-multi-event-loop-ended-on-triage", func(t *testing.T) {
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/watch/multi.toml", `
[[rule]]
id = "multi-loop-ended"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended", "drain.item-fault"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.kind == "drain.loop-ended"'
window = "1h"

[rule.guidance]
summary = "Loop ended."
check = ["Check log."]
do = ["Diagnose."]

[rule.delivery]
receiver = "triage"
`)
		findings, err := Lint(ctx, LintOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		assertHasLintCode(t, findings, CodeWatchLintLivenessReceiver, "multi-loop-ended")
	})

	// 4. Both liveness rules routed to operator-session => accepted with zero findings.
	t.Run("accepts-liveness-rules-on-operator-session", func(t *testing.T) {
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/watch/liveness.toml", `
[[rule]]
id = "drain-loop-no-pass"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-pass"]

[rule.when]
group_by = "global"
pattern = "duration_without_event"
after = 'event.kind == "drain.loop-pass"'
threshold_duration = "5m"
window = "24h"

[rule.guidance]
summary = "No loop pass for {{elapsed}}."
check = ["Check drain loop."]
do = ["Restart drain loop."]

[rule.delivery]
receiver = "operator-session"

[[rule]]
id = "drain-loop-fault"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.loop-ended"]

[rule.when]
group_by = "global"
pattern = "predicate_match"
match = 'event.stop == "fault"'
window = "1h"

[rule.guidance]
summary = "Drain loop faulted."
check = ["Check drain loop log."]
do = ["Diagnose fault."]

[rule.delivery]
receiver = "operator-session"
`)
		findings, err := Lint(ctx, LintOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings, got %v", findings)
		}
	})

	// 5. Non-liveness rule that only uses drain.loop-ended in clear_when (like trunk-proof-stalled)
	// routed to triage => accepted.
	t.Run("accepts-clear-when-loop-ended-on-triage", func(t *testing.T) {
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/watch/proof-stalled.toml", `
[[rule]]
id = "trunk-proof-stalled"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.heavy-progress", "drain.heavy-fault", "drain.loop-ended", "drain.build-started"]

[rule.when]
group_by = "global"
pattern = "duration_without_event"
after = 'event.kind == "drain.heavy-progress" and event.phase in ["started", "progress"]'
match = 'event.kind == "drain.heavy-progress" and event.phase == "ended"'
clear_when = 'event.kind == "drain.heavy-fault" or event.kind == "drain.loop-ended" or (event.kind == "drain.build-started" and event.role == "drain")'
threshold_duration = "10m"
window = "24h"

[rule.guidance]
summary = "Trunk proof stalled for {{elapsed}}."
check = ["Check heavy runner."]
do = ["Diagnose hung proof."]

[rule.delivery]
receiver = "triage"
`)
		findings, err := Lint(ctx, LintOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		assertNoLintCode(t, findings, CodeWatchLintLivenessReceiver)
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings, got %v", findings)
		}
	})

	// 6. Regular non-liveness rule routed to triage => accepted.
	t.Run("accepts-non-liveness-rule-on-triage", func(t *testing.T) {
		root := t.TempDir()
		writeLintFile(t, root, "config/tillr/watch/item-fault.toml", `
[[rule]]
id = "drain-item-fault"
scope = "core"
severity = "warning"

[rule.reads]
events = ["drain.item-fault"]

[rule.when]
group_by = "item_id"
pattern = "predicate_match"
window = "1h"

[rule.guidance]
summary = "Item {{subject}} faulted."
check = ["Check item."]
do = ["File defect."]

[rule.delivery]
receiver = "triage"
`)
		findings, err := Lint(ctx, LintOptions{Root: root})
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != 0 {
			t.Fatalf("expected 0 findings, got %v", findings)
		}
	})
}
