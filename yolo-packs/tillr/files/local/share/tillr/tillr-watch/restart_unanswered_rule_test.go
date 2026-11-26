package main

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainRestartUnansweredRule(t *testing.T) {
	const ruleID = "drain-restart-unanswered"
	allRules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := allRules[idx]
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance calls a firing alarm expected: %q", ruleID, text)
		}
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	newHarness := func(t *testing.T, rules []watch.Rule, start time.Time) (context.Context, string, *time.Time, func() []watch.Notice) {
		t.Helper()
		ctx := context.Background()
		storePath := filepath.Join(t.TempDir(), "state.db")
		s, err := store.Open(ctx, storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := start
		e := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		pending := func() []watch.Notice {
			t.Helper()
			results, err := e.EvaluateRules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, res := range results {
				if res.ID == ruleID && (res.Diagnostic() != "" || res.Skipped != 0) {
					t.Fatalf("unexpected %s evaluation result: %+v", ruleID, res)
				}
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil {
				t.Fatal(err)
			}
			return notices
		}
		return ctx, storePath, &now, pending
	}

	t0 := time.Date(2026, 11, 23, 6, 2, 53, 0, time.UTC)

	t.Run("answered within threshold stays quiet", func(t *testing.T) {
		ctx, storePath, now, pending := newHarness(t, []watch.Rule{rule}, t0)
		req, err := restart.File(ctx, storePath, restart.RoleDrain, *now)
		if err != nil {
			t.Fatal(err)
		}
		*now = t0.Add(32 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("request unanswered for 32m (< 35m) opened notices: %+v", notices)
		}
		rs := restart.Restarter{Store: storePath, Role: restart.RoleDrain, Now: func() time.Time { return *now }}
		if err := rs.Record(ctx, restart.Answer{RequestID: req.ID, Answer: restart.AnswerRestarted, FromBuild: "old", ToBuild: "new"}); err != nil {
			t.Fatal(err)
		}
		*now = t0.Add(35 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("answered request opened notices at 35m: %+v", notices)
		}
		*now = t0.Add(70 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("answered request opened notices at 70m: %+v", notices)
		}
	})

	t.Run("unanswered past threshold fires and resolves when answered", func(t *testing.T) {
		ctx, storePath, now, pending := newHarness(t, []watch.Rule{rule}, t0)
		req1, err := restart.File(ctx, storePath, restart.RoleDrain, *now)
		if err != nil {
			t.Fatal(err)
		}
		*now = t0.Add(35*time.Minute - time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("want quiet before 35m threshold, got %+v", notices)
		}

		*now = t0.Add(35 * time.Minute)
		first := pending()
		if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != req1.ID {
			t.Fatalf("want one %s notice for %s at 35m, got %+v", ruleID, req1.ID, first)
		}
		if !strings.Contains(first[0].Bundle.Guidance.Summary, req1.ID) || !strings.Contains(first[0].Bundle.Guidance.Summary, "35m") {
			t.Fatalf("summary missing request id or elapsed duration: %+v", first[0].Bundle.Guidance)
		}

		// A newer request arriving while the first is still unanswered must not mask the first.
		*now = t0.Add(40 * time.Minute)
		req2, err := restart.File(ctx, storePath, restart.RoleDrain, *now)
		if err != nil {
			t.Fatal(err)
		}
		*now = t0.Add(67 * time.Minute)
		at67m := pending()
		if len(at67m) != 1 || at67m[0].AlertID != first[0].AlertID || at67m[0].Bundle.Subject != req1.ID {
			t.Fatalf("want same open episode for %s at 67m (while %s is 27m old), got %+v", req1.ID, req2.ID, at67m)
		}

		// Settling answers older requests with superseded and the newest with restarted, resolving the episode.
		rs := restart.Restarter{Store: storePath, Role: restart.RoleDrain, Now: func() time.Time { return *now }}
		if err := rs.Record(ctx, restart.Answer{RequestID: req1.ID, Answer: restart.AnswerSuperseded, FromBuild: "old", ToBuild: "new", Detail: req2.ID}); err != nil {
			t.Fatal(err)
		}
		if err := rs.Record(ctx, restart.Answer{RequestID: req2.ID, Answer: restart.AnswerRestarted, FromBuild: "old", ToBuild: "new"}); err != nil {
			t.Fatal(err)
		}
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("settling unanswered requests should resolve %s, got %+v", ruleID, notices)
		}
	})

	t.Run("view restart requests stay quiet", func(t *testing.T) {
		ctx, storePath, now, pending := newHarness(t, []watch.Rule{rule}, t0)
		if _, err := restart.File(ctx, storePath, restart.RoleView, *now); err != nil {
			t.Fatal(err)
		}
		*now = t0.Add(70 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("view restart request opened loop alert: %+v", notices)
		}
	})

	t.Run("across all config rules on 67m unanswered loop restart", func(t *testing.T) {
		root := t.TempDir()
		ctx := context.Background()
		storePath := filepath.Join(t.TempDir(), "state.db")
		s, err := store.Open(ctx, storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := t0
		e := watch.Evaluator{Store: s, Root: root, Rules: allRules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		req, err := restart.File(ctx, storePath, restart.RoleDrain, now)
		if err != nil {
			t.Fatal(err)
		}
		now = t0.Add(67 * time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != req.ID {
			t.Fatalf("want only %s for %s across config/tillr/watch, got %+v", ruleID, req.ID, notices)
		}
	})
}
