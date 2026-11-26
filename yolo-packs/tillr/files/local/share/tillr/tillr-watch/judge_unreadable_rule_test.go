package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainJudgeUnreadableRule(t *testing.T) {
	const ruleID = "drain-judge-unreadable"
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}
	rule := rules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}
	if rule.Severity != "warning" {
		t.Fatalf("severity = %q, want warning", rule.Severity)
	}
	if rule.Delivery.Receiver != "foreground" {
		t.Fatalf("receiver = %q, want foreground", rule.Delivery.Receiver)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 18, 25, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	appendRefusal := func(runID, itemID, step, detail string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"item_id":       itemID,
			"lane_id":       "lane-1",
			"tick_id":       "tick-1",
			"step":          step,
			"detail":        detail,
			"base_sha":      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"head_sha":      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"writer_run_id": "run-" + itemID + "-a1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.RefusalKind,
				ObjectType: "run",
				ObjectID:   &runID,
				At:         now,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	evaluatePending := func() []watch.Notice {
		t.Helper()
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != ruleID || results[0].Diagnostic() != "" || results[0].Skipped != 0 {
			t.Fatalf("unexpected evaluation results: %+v", results)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	// Unrelated drain.refusal steps do not open an alert.
	appendRefusal("run-1", "item-other", drain.RefusalVerifyNotDone, "c1: missing test")
	appendRefusal("run-2", "item-other", drain.RefusalGate, "gate failed")
	if notices := evaluatePending(); len(notices) != 0 {
		t.Fatalf("unrelated refusals opened notices: %+v", notices)
	}

	// The 2026-11-22 hold ("judge-unreadable: saved judgment belongs to a different candidate")
	// opens a warning alert with the item as subject.
	const (
		itemA    = "item-request-f47a9d41f7"
		judgeRun = "judge-item-request-f47a9d41f7-1"
		detailA  = "evaluator-candidate: item-request-f47a9d41f7: saved judgment belongs to a different candidate"
	)
	now = now.Add(10 * time.Second)
	appendRefusal(judgeRun, itemA, drain.ReasonJudgeUnreadable, detailA)
	notices := evaluatePending()
	if len(notices) != 1 {
		t.Fatalf("want 1 notice, got %d: %+v", len(notices), notices)
	}
	n := notices[0]
	if n.Bundle.RuleID != ruleID || n.Bundle.Subject != itemA || n.Bundle.Severity != "warning" {
		t.Fatalf("unexpected notice bundle: %+v", n.Bundle)
	}
	if !strings.Contains(n.Bundle.Guidance.Summary, itemA) || !strings.Contains(n.Bundle.Guidance.Summary, "saved judgment belongs to a different candidate") {
		t.Fatalf("summary must include item and refusal detail, got: %q", n.Bundle.Guidance.Summary)
	}
	if len(n.Bundle.Guidance.Check) == 0 || !strings.Contains(n.Bundle.Guidance.Check[0], judgeRun) {
		t.Fatalf("check guidance must include judge run %q, got: %+v", judgeRun, n.Bundle.Guidance.Check)
	}
	if len(n.Bundle.Guidance.Do) == 0 {
		t.Fatal("do guidance must not be empty")
	}

	// Repeat judge-unreadable refusals on the same item stay in one episode without
	// queuing a duplicate notice; another item opens its own episode.
	const itemB = "item-request-second"
	now = now.Add(2 * time.Minute)
	appendRefusal("judge-item-request-f47a9d41f7-2", itemA, drain.ReasonJudgeUnreadable, detailA)
	appendRefusal("judge-item-request-second-1", itemB, drain.ReasonJudgeUnreadable, "judge verdict unreadable: unexpected EOF")
	notices = evaluatePending()
	counts := map[string]int{}
	for _, notice := range notices {
		counts[notice.Bundle.Subject]++
		if notice.Bundle.Subject == itemA && notice.AlertID != n.AlertID {
			t.Fatalf("repeat refusal replaced %s notice: first %+v, now %+v", itemA, n, notice)
		}
	}
	if len(notices) != 2 || counts[itemA] != 1 || counts[itemB] != 1 {
		t.Fatalf("want one notice per held item, got %+v", notices)
	}

	status, err := e.ReadStatus(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Alerts) != 2 {
		t.Fatalf("want 2 active alerts, got %+v", status.Alerts)
	}
	for _, a := range status.Alerts {
		if a.Subject == itemA && (a.ID != n.AlertID || a.Observed != 2 || a.Severity != "warning") {
			t.Fatalf("unexpected alert status for %s: %+v", itemA, a)
		}
	}

	// Once the 1h window passes without new judge-unreadable refusals, both episodes resolve.
	now = now.Add(65 * time.Minute)
	_ = evaluatePending()
	status, err = e.ReadStatus(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Alerts) != 0 {
		t.Fatalf("expected 0 active alerts past 1h window, got %+v", status.Alerts)
	}
}
