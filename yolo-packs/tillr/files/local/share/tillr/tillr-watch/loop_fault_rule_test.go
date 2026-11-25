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

func TestDrainLoopFaultRule(t *testing.T) {
	const (
		ruleID        = "drain-loop-fault"
		loopEndedKind = "drain.loop-ended"
	)
	rules, err := watch.LoadRules("../../config/tillr/watch")
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

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 11, 21, 19, 5, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	appendEvent := func(kind, objectType, objectID string, at time.Time, payload any) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: kind, ObjectType: objectType, ObjectID: &objectID, At: at, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() []watch.Notice {
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
	loopEnded := func(sessionID, stop, detail string, exitCode int, at time.Time) {
		t.Helper()
		payload := map[string]any{
			"stop":     stop,
			"detail":   detail,
			"ended_at": at.Format(time.RFC3339Nano),
		}
		if sessionID != "" {
			payload["session_id"] = sessionID
		}
		if exitCode != 0 {
			payload["exit_code"] = exitCode
		}
		objID := sessionID
		if objID == "" {
			objID = "loop"
		}
		appendEvent(loopEndedKind, "session", objID, at, payload)
	}

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}
	appendEvent(drain.LoopPassKind, "session", "sess-pass", now, drain.LoopPassFact{SessionID: "sess-pass"})
	loopEnded("sess-clean", drain.StopOperatorStop, "asked to stop", 0, now)
	loopEnded("sess-old", drain.StopFault, "expired fault exit", 5, now.Add(-2*time.Hour))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("unrelated, clean-stop, or expired events opened notices: %+v", notices)
	}

	loopEnded("sess-1405", drain.StopFault, "tillr-run exec could not run run-x: exit status 5", 5, now)
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != "global" {
		t.Fatalf("want one global notice after loop fault exit, got %+v", first)
	}
	guidance := first[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	if !strings.Contains(guidance.Summary, "1 in the last hour") ||
		!strings.Contains(checkText, loopEndedKind) ||
		!strings.Contains(checkText, ".tillr/loop") ||
		doText == "" {
		t.Fatalf("unexpected guidance on loop fault notice: %+v", guidance)
	}

	now = now.Add(22 * time.Minute)
	loopEnded("", drain.StopFault, "repeat loop fault without session_id", 1, now)
	repeated := pending()
	if len(repeated) != 1 || repeated[0].AlertID != first[0].AlertID {
		t.Fatalf("repeat loop fault inside window should keep one episode: first %+v, repeated %+v", first, repeated)
	}

	now = now.Add(61 * time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("episode should resolve once loop faults age past the window, got %+v", aged)
	}

	now = now.Add(2*time.Hour + 23*time.Minute)
	loopEnded("sess-1751", drain.StopFault, "later loop fault at 5:51 PM ET", 1, now)
	reopened := pending()
	if len(reopened) != 1 || reopened[0].AlertID == first[0].AlertID {
		t.Fatalf("later fault after resolution should open a new episode: first %+v, reopened %+v", first[0], reopened)
	}
}
