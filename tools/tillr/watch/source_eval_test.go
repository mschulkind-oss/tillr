package watch

import (
	"context"
	"encoding/json"
	"fmt"
	goparse "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type memoryObservationSource struct {
	records      []Observation
	skippedLines SkippedLines
	projections  map[string]any
	watchPaths   []string
	pollEvery    time.Duration
}

func (m *memoryObservationSource) append(kind string, ts time.Time, fields map[string]any) int64 {
	seq := int64(len(m.records) + 1)
	m.records = append(m.records, Observation{
		Seq:    seq,
		TS:     ts.UnixMilli(),
		Kind:   kind,
		Fields: fields,
	})
	return seq
}

func (m *memoryObservationSource) ReadWindow(_ context.Context, sinceMillis int64, kinds []string) ([]Observation, SourceCursor, SkippedLines, error) {
	var head int64
	if len(m.records) > 0 {
		head = m.records[len(m.records)-1].Seq
	}
	var out []Observation
	for _, rec := range m.records {
		if rec.TS < sinceMillis {
			continue
		}
		if len(kinds) > 0 && !slices.Contains(kinds, rec.Kind) {
			continue
		}
		out = append(out, rec)
	}
	return out, SourceCursor{Epoch: 1, Seq: head}, m.skippedLines, nil
}

func (m *memoryObservationSource) Project(_ context.Context, names []string) (map[string]any, error) {
	out := make(map[string]any, len(names))
	for _, name := range names {
		if v, ok := m.projections[name]; ok {
			out[name] = v
		}
	}
	return out, nil
}

func (m *memoryObservationSource) WatchTargets() ([]string, time.Duration) {
	return slices.Clone(m.watchPaths), m.pollEvery
}

func mustExpr(t *testing.T, text string) *Expression {
	t.Helper()
	e, err := ParseExpression(text)
	if err != nil {
		t.Fatalf("ParseExpression(%q): %v", text, err)
	}
	return e
}

func TestSourceNeutralCoreFilesHaveZeroTillrImports(t *testing.T) {
	fset := gotoken.NewFileSet()
	for _, file := range []string{
		"source.go",
		"pattern.go",
		"git.go",
		"projection.go",
		"bundle.go",
		"expression.go",
	} {
		parsed, err := goparse.ParseFile(fset, file, nil, goparse.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, imp := range parsed.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(path, "github.com/mschulkind-oss/tillr/") {
				t.Errorf("%s imports %q; expected zero tillr/* imports", file, path)
			}
		}
	}
}

func TestInMemorySourceAllSixPatternsClearingSkipsGitRefsAndTruncation(t *testing.T) {
	ctx := context.Background()
	baseNow := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	t.Run("predicate_match_and_clearing_events", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{}
		rule := Rule{
			ID:       "pred-clear",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"test.failed", "test.passed"}},
			When: When{
				GroupBy:   "pkg",
				Pattern:   PredicateMatch,
				Threshold: 1,
				Window:    5 * time.Minute,
				Match:     mustExpr(t, `event.kind == "test.failed"`),
				ClearWhen: mustExpr(t, `event.kind == "test.passed"`),
			},
			Guidance: Guidance{
				Summary: "pkg={{subject}} count={{count}} line={{event.line}}",
				Check:   []string{"check {{subject}}"},
				Do:      []string{"fix {{subject}}"},
			},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		src.append("test.failed", now.Add(-time.Minute), map[string]any{"pkg": "watch", "line": "boom-1"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "watch" || notices[0].Bundle.Guidance.Summary != "pkg=watch count=1 line=boom-1" {
			t.Fatalf("expected 1 open notice, got %+v (err=%v)", notices, err)
		}
		firstAlertID := notices[0].AlertID
		if err := e.Shown(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}
		if err := e.Seen(ctx, "foreground", notices[0].Seq); err != nil {
			t.Fatal(err)
		}

		// Duplicate observation inside window keeps same episode without re-notifying.
		src.append("test.failed", now.Add(-30*time.Second), map[string]any{"pkg": "watch", "line": "boom-2"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if pending, err := e.Pending(ctx, "foreground"); err != nil || len(pending) != 0 {
			t.Fatalf("duplicate observation re-notified: %+v (err=%v)", pending, err)
		}

		// Clearing event resolves the episode.
		src.append("test.passed", now.Add(-10*time.Second), map[string]any{"pkg": "watch"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		status, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil || len(status.Alerts) != 0 {
			t.Fatalf("clearing event did not resolve episode: %+v (err=%v)", status, err)
		}

		// A fresh failure after resolution opens a distinct episode and notifies again.
		src.append("test.failed", now, map[string]any{"pkg": "watch", "line": "boom-3"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].AlertID == firstAlertID {
			t.Fatalf("expected new episode after clear, got %+v (err=%v)", notices, err)
		}

		// Composite group_by + prefix clearing without any tillr store open:
		compSrc := &memoryObservationSource{}
		compRule := Rule{
			ID:       "comp-clear",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"step.fail", "job.reset"}},
			When: When{
				GroupByList: []string{"job", "step"},
				Pattern:     PredicateMatch,
				Threshold:   1,
				Match:       mustExpr(t, `event.kind == "step.fail"`),
				ClearWhen:   mustExpr(t, `event.kind == "job.reset"`),
			},
			Guidance: Guidance{Summary: "{{subject}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		compEval := Evaluator{Source: compSrc, Rules: []Rule{compRule}, Clock: func() time.Time { return now }}
		if _, err := compEval.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		compSrc.append("step.fail", now.Add(-time.Minute), map[string]any{"job": "ci", "step": "lint"})
		if _, err := compEval.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if pending, err := compEval.Pending(ctx, "foreground"); err != nil || len(pending) != 1 || pending[0].Bundle.Subject != "ci/lint" {
			t.Fatalf("expected ci/lint notice, got %+v (err=%v)", pending, err)
		}
		// Prefix clear event carries only "job": "ci" and clears all active "ci/*" episodes.
		compSrc.append("job.reset", now, map[string]any{"job": "ci"})
		if _, err := compEval.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if st, err := compEval.ReadStatus(ctx, func(string) (bool, error) { return false, nil }); err != nil || len(st.Alerts) != 0 {
			t.Fatalf("expected prefix clear to resolve ci/lint, got %+v (err=%v)", st, err)
		}
	})

	t.Run("count_in_window_and_group_by_skip_counts", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{skippedLines: 2}
		rule := Rule{
			ID:       "count-win",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"pkg.fail", "pkg.pass"}},
			When: When{
				GroupBy:   "pkg",
				Pattern:   CountInWindow,
				Threshold: 3,
				Window:    10 * time.Minute,
				Match:     mustExpr(t, `event.kind == "pkg.fail"`),
				ClearWhen: mustExpr(t, `event.kind == "pkg.pass"`),
			},
			Guidance: Guidance{Summary: "{{subject}} {{count}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		// Expired record outside 10m window:
		src.append("pkg.fail", now.Add(-15*time.Minute), map[string]any{"pkg": "alpha"})
		// Skipped group_by records (missing, null, integer):
		src.append("pkg.fail", now.Add(-5*time.Minute), map[string]any{"other": "x"})
		src.append("pkg.fail", now.Add(-4*time.Minute), map[string]any{"pkg": nil})
		src.append("pkg.fail", now.Add(-3*time.Minute), map[string]any{"pkg": 42})
		// Future record (> now) ignored:
		src.append("pkg.fail", now.Add(time.Minute), map[string]any{"pkg": "alpha"})
		// Two in-window records for alpha (below threshold 3):
		src.append("pkg.fail", now.Add(-2*time.Minute), map[string]any{"pkg": "alpha"})
		src.append("pkg.fail", now.Add(-time.Minute), map[string]any{"pkg": "alpha"})

		// 2 skipped lines from source + 3 non-string group_by records = 5 skipped.
		skipped, err := e.Skipped(ctx)
		if err != nil || skipped["count-win"] != 5 {
			t.Fatalf("Skipped = %v (err=%v), want 5", skipped, err)
		}
		results, err := e.EvaluateRules(ctx)
		if err != nil || len(results) != 1 || results[0].Skipped != 5 {
			t.Fatalf("results = %+v (err=%v), want Skipped=5", results, err)
		}
		if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
			t.Fatalf("fired below threshold: %+v (err=%v)", notices, err)
		}

		// Third in-window failure triggers episode.
		src.append("pkg.fail", now, map[string]any{"pkg": "alpha"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].Bundle.Guidance.Summary != "alpha 3" {
			t.Fatalf("expected alpha 3 notice, got %+v (err=%v)", notices, err)
		}
	})

	t.Run("consecutive_count", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{}
		rule := Rule{
			ID:       "consec",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"step.done"}},
			When: When{
				GroupBy:   "job",
				Pattern:   ConsecutiveCount,
				Threshold: 2,
				Window:    10 * time.Minute,
				Match:     mustExpr(t, `event.status == "fail"`),
				Reset:     mustExpr(t, `event.status == "ok"`),
			},
			Guidance: Guidance{Summary: "{{subject}} {{count}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		src.append("step.done", now.Add(-4*time.Minute), map[string]any{"job": "build", "status": "fail"})
		src.append("step.done", now.Add(-3*time.Minute), map[string]any{"job": "build", "status": "ok"})
		src.append("step.done", now.Add(-2*time.Minute), map[string]any{"job": "build", "status": "fail"})
		src.append("step.done", now.Add(-time.Minute), map[string]any{"job": "build", "status": "skip"}) // ignored when Reset is explicit
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
			t.Fatalf("fired at 1 consecutive fail: %+v (err=%v)", notices, err)
		}

		src.append("step.done", now, map[string]any{"job": "build", "status": "fail"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].Bundle.Guidance.Summary != "build 2" {
			t.Fatalf("expected build 2 notice, got %+v (err=%v)", notices, err)
		}
	})

	t.Run("alternating_pair", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{}
		rule := Rule{
			ID:       "alt",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"stage.move"}},
			When: When{
				GroupBy:   "global",
				Pattern:   AlternatingPair,
				Field:     "stage",
				Pairs:     [][2]string{{"plan", "design"}},
				Threshold: 4,
				Window:    time.Hour,
			},
			Guidance: Guidance{Summary: "{{count}} {{pair_a}}<->{{pair_b}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		for i, stage := range []any{"plan", "design", "design", nil, "plan", "design"} {
			fields := map[string]any{}
			if stage != nil {
				fields["stage"] = stage
			}
			src.append("stage.move", now.Add(time.Duration(i-6)*time.Minute), fields)
		}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].Bundle.Guidance.Summary != "4 plan<->design" {
			t.Fatalf("expected alternating_pair notice, got %+v (err=%v)", notices, err)
		}
	})

	t.Run("duration_without_event", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{}
		rule := Rule{
			ID:       "silence",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Events: []string{"build.phase"}},
			When: When{
				GroupBy:           "job",
				Pattern:           DurationWithoutEvent,
				After:             mustExpr(t, `event.phase == "started"`),
				Match:             mustExpr(t, `event.phase == "finished"`),
				ClearWhen:         mustExpr(t, `event.phase == "aborted"`),
				ThresholdDuration: 30 * time.Second,
				Window:            5 * time.Minute,
			},
			Guidance: Guidance{Summary: "{{subject}} silent {{elapsed}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		src.append("build.phase", now.Add(-20*time.Second), map[string]any{"job": "compile", "phase": "started"})
		src.append("build.phase", now.Add(-40*time.Second), map[string]any{"job": "done-job", "phase": "started"})
		src.append("build.phase", now.Add(-35*time.Second), map[string]any{"job": "done-job", "phase": "finished"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
			t.Fatalf("fired before threshold_duration: %+v (err=%v)", notices, err)
		}

		now = now.Add(10 * time.Second) // compile is now 30s without "finished"
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].Bundle.Guidance.Summary != "compile silent 30s" {
			t.Fatalf("expected compile silent 30s, got %+v (err=%v)", notices, err)
		}

		src.append("build.phase", now, map[string]any{"job": "compile", "phase": "aborted"})
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
			t.Fatalf("aborted clear_when did not resolve episode: %+v (err=%v)", notices, err)
		}
	})

	t.Run("lag_behind_and_git_refs_without_tillr_store", func(t *testing.T) {
		now := baseNow
		root, leaderOID, _ := forkedRepo(t, now)
		src := &memoryObservationSource{}

		refMatchRule := Rule{
			ID:       "git-ref-match",
			Scope:    "*",
			Severity: "info",
			Enabled:  true,
			Reads: Reads{
				Events:      []string{"ref.check"},
				Projections: []string{"git_refs"},
			},
			When: When{
				GroupBy:   "branch",
				Pattern:   PredicateMatch,
				Threshold: 1,
				Window:    5 * time.Minute,
				Match:     mustExpr(t, fmt.Sprintf(`git.ref(%q) == %q`, leaderRef, leaderOID)),
			},
			Guidance: Guidance{Summary: "matched {{subject}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}
		lagRule := Rule{
			ID:       "git-lag",
			Scope:    "*",
			Severity: "warning",
			Enabled:  true,
			Reads:    Reads{Projections: []string{"git_refs:" + leaderRef + "," + followerRef}},
			When: When{
				GroupBy:          "global",
				Pattern:          LagBehind,
				Leader:           leaderRef,
				Follower:         followerRef,
				ThresholdCommits: 2,
				Mode:             "any",
			},
			Guidance: Guidance{Summary: "lag={{count}} {{leader_short}}", Check: []string{"c"}, Do: []string{"d"}},
			Delivery: Delivery{Receiver: "foreground"},
		}

		e := Evaluator{Source: src, Root: root, Rules: []Rule{refMatchRule, lagRule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		src.append("ref.check", now.Add(-time.Minute), map[string]any{"branch": "main"})
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range results {
			if r.Diagnostic() != "" {
				t.Fatalf("unexpected diagnostic for %s: %+v", r.ID, r)
			}
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 2 {
			t.Fatalf("expected 2 git notices with zero tillr store open, got %+v (err=%v)", notices, err)
		}

		// Fast-forward follower to leader and verify lag_behind resolves with no store open.
		fixtureGit(t, root, "", now, "update-ref", followerRef, leaderOID)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 || notices[0].Bundle.RuleID != "git-ref-match" {
			t.Fatalf("expected lag_behind to resolve leaving only git-ref-match, got %+v (err=%v)", notices, err)
		}
	})

	t.Run("bundle_8kib_truncation_without_tillr_store", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{}
		rule := Rule{
			ID:       "oversized-bundle",
			Scope:    "*",
			Severity: "high",
			Enabled:  true,
			Reads:    Reads{Events: []string{"log.error"}},
			When: When{
				GroupBy:   "service",
				Pattern:   CountInWindow,
				Threshold: 10,
				Window:    5 * time.Minute,
			},
			Guidance: Guidance{
				Summary: "service={{subject}} count={{count}} " + strings.Repeat("S", 3000),
				Check:   []string{strings.Repeat("C", 3000), strings.Repeat("c", 3000)},
				Do:      []string{strings.Repeat("D", 3000), strings.Repeat("d", 3000)},
			},
			Delivery: Delivery{Receiver: "foreground"},
		}
		e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 60; i++ {
			src.append("log.error", now.Add(-time.Duration(60-i)*time.Second), map[string]any{
				"service": "api",
				"msg":     strings.Repeat("x", 200),
			})
		}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("expected 1 truncated notice, got %+v (err=%v)", notices, err)
		}
		encoded, err := json.Marshal(notices[0].Bundle)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > bundleBudget {
			t.Fatalf("bundle size %d exceeds 8 KiB budget %d", len(encoded), bundleBudget)
		}
		if notices[0].Bundle.Omitted.Events != 60 || notices[0].Bundle.Omitted.Do != 2 || notices[0].Bundle.Omitted.Check == 0 {
			t.Fatalf("unexpected truncation ladder omissions: %+v", notices[0].Bundle.Omitted)
		}
	})

	t.Run("tillr_only_projections_and_scopes_report_clean_unsupported_without_store", func(t *testing.T) {
		now := baseNow
		src := &memoryObservationSource{}
		src.append("ev", now, map[string]any{"id": "one"})
		rules := []Rule{
			{
				ID: "needs-items", Scope: "*", Severity: "info", Enabled: true,
				Reads:    Reads{Events: []string{"ev"}, Projections: []string{"items"}},
				When:     When{GroupBy: "id", Pattern: PredicateMatch, Threshold: 1, Window: time.Minute},
				Guidance: Guidance{Summary: "s", Check: []string{"c"}, Do: []string{"d"}},
				Delivery: Delivery{Receiver: "foreground"},
			},
			{
				ID: "needs-core-scope", Scope: "core", Severity: "info", Enabled: true,
				Reads:    Reads{Events: []string{"ev"}},
				When:     When{GroupBy: "id", Pattern: PredicateMatch, Threshold: 1, Window: time.Minute},
				Guidance: Guidance{Summary: "s", Check: []string{"c"}, Do: []string{"d"}},
				Delivery: Delivery{Receiver: "foreground"},
			},
			{
				ID: "needs-charter-wildcard", Scope: "charter:*", Severity: "info", Enabled: true,
				Reads:    Reads{Events: []string{"ev"}},
				When:     When{GroupBy: "id", Pattern: PredicateMatch, Threshold: 1, Window: time.Minute},
				Guidance: Guidance{Summary: "s", Check: []string{"c"}, Do: []string{"d"}},
				Delivery: Delivery{Receiver: "foreground"},
			},
			{
				ID: "needs-workflow-wildcard", Scope: "workflow:*", Severity: "info", Enabled: true,
				Reads:    Reads{Events: []string{"ev"}},
				When:     When{GroupBy: "id", Pattern: PredicateMatch, Threshold: 1, Window: time.Minute},
				Guidance: Guidance{Summary: "s", Check: []string{"c"}, Do: []string{"d"}},
				Delivery: Delivery{Receiver: "foreground"},
			},
			{
				ID: "needs-charter-scope", Scope: "charter:custom", Severity: "info", Enabled: true,
				Reads:    Reads{Events: []string{"ev"}},
				When:     When{GroupBy: "id", Pattern: PredicateMatch, Threshold: 1, Window: time.Minute},
				Guidance: Guidance{Summary: "s", Check: []string{"c"}, Do: []string{"d"}},
				Delivery: Delivery{Receiver: "foreground"},
			},
			{
				ID: "needs-workflow-scope", Scope: "workflow:planned-build@v1", Severity: "info", Enabled: true,
				Reads:    Reads{Events: []string{"ev"}},
				When:     When{GroupBy: "id", Pattern: PredicateMatch, Threshold: 1, Window: time.Minute},
				Guidance: Guidance{Summary: "s", Check: []string{"c"}, Do: []string{"d"}},
				Delivery: Delivery{Receiver: "foreground"},
			},
		}
		// Ensure zero files exist in scratch dir before and after evaluation.
		emptyDir := t.TempDir()
		e := Evaluator{Source: src, Rules: rules, Clock: func() time.Time { return now }}
		results, err := e.EvaluateRules(ctx)
		if err != nil || len(results) != 6 {
			t.Fatalf("results=%+v err=%v", results, err)
		}
		if want := `not evaluated: projection "items" requires a tillr store source`; results[0].Unsupported != want {
			t.Errorf("items unsupported = %q, want %q", results[0].Unsupported, want)
		}
		for idx := 1; idx < len(results); idx++ {
			if !strings.Contains(results[idx].Unsupported, "requires a tillr store source") {
				t.Errorf("rule %s unsupported = %q, want requires a tillr store source", results[idx].ID, results[idx].Unsupported)
			}
		}
		entries, err := os.ReadDir(emptyDir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("unexpected files created: %v %v", entries, err)
		}
	})
}

func TestTillrStoreSourceAdapterContract(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)

	if err := event.Register("watch-test.source", "watch-test"); err != nil {
		t.Fatal(err)
	}
	objID := "item-env"
	rev := int64(7)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO items VALUES ('item-env','in-queue','build',NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY, workflow TEXT NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO workflow_selections VALUES ('item-env','planned-build@v1')`); err != nil {
			return err
		}
		_, err := tx.Append(event.Event{
			Kind:       "watch-test.source",
			ObjectType: "item",
			ObjectID:   &objID,
			Revision:   &rev,
			At:         now.Add(-30 * time.Second),
			Payload:    []byte(`{"seat":"verify","detail":{"step":"check"}}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	src := NewTillrStoreSource(s)
	src.Clock = func() time.Time { return now }

	paths, poll := src.WatchTargets()
	if len(paths) != 1 || paths[0] != filepath.Clean(s.Path())+"-wal" || poll <= 0 {
		t.Fatalf("WatchTargets = (%v, %v)", paths, poll)
	}

	obs, cur, skipped, err := src.ReadWindow(ctx, now.Add(-time.Minute).UnixMilli(), []string{"watch-test.source"})
	if err != nil || skipped != 0 || cur.Seq < 1 || len(obs) != 1 {
		t.Fatalf("ReadWindow = (%+v, %+v, %d, %v)", obs, cur, skipped, err)
	}
	if obs[0].Lookup("object_id") != "item-env" || obs[0].Lookup("object_type") != "item" || obs[0].Lookup("revision") != int64(7) || obs[0].Lookup("seat") != "verify" || obs[0].Lookup("detail.step") != "check" {
		t.Fatalf("unexpected observation fields: %+v", obs[0])
	}

	proj, err := src.Project(ctx, []string{"charter", "items", "workflow_selections"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if proj["charter.ready"] != "in-queue" {
		t.Fatalf("unexpected charter projection: %+v", proj)
	}

	// Evaluator configured via Source: src (with Evaluator.Store == nil) evaluates scopes and projections through TillrStoreSource.
	rule := Rule{
		ID:       "via-source",
		Scope:    "workflow:planned-build@v1",
		Severity: "warning",
		Enabled:  true,
		Reads:    Reads{Events: []string{"watch-test.source"}, Projections: []string{"items", "charter"}},
		When: When{
			GroupBy:   "object_id",
			Pattern:   PredicateMatch,
			Threshold: 1,
			Window:    time.Minute,
			Match:     mustExpr(t, `item.state == charter.ready and item.seat == "build" and event.seat == "verify"`),
		},
		Guidance: Guidance{Summary: "{{subject}} matched", Check: []string{"c"}, Do: []string{"d"}},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Source: src, Rules: []Rule{rule}, Clock: func() time.Time { return now }}
	results, err := e.EvaluateRules(ctx)
	if err != nil || len(results) != 1 || results[0].Diagnostic() != "" {
		t.Fatalf("EvaluateRules via TillrStoreSource: %+v, %v", results, err)
	}
	// Pre-load event does not fire; append one post-load event and re-evaluate.
	if notices, err := e.Pending(ctx, "foreground"); err != nil || len(notices) != 0 {
		t.Fatalf("pre-load event should not open notice via TillrStoreSource: %+v, %v", notices, err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       "watch-test.source",
			ObjectType: "item",
			ObjectID:   &objID,
			Revision:   &rev,
			At:         now,
			Payload:    []byte(`{"seat":"verify","detail":{"step":"check"}}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != "item-env" {
		t.Fatalf("Pending via TillrStoreSource: %+v, %v", notices, err)
	}
}

func TestSourceProjectionPredicateMatchParity(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	root := t.TempDir()
	fixtureGit(t, root, "", now, "init")
	fixtureGit(t, root, "", now, "config", "user.name", "Test")
	fixtureGit(t, root, "", now, "config", "user.email", "test@example.com")
	fixtureGit(t, root, "", now, "commit", "--allow-empty", "-m", "red")
	redOID := fixtureGit(t, root, "", now, "rev-parse", "HEAD")
	fixtureGit(t, root, "", now, "branch", "-M", "main")
	fixtureGit(t, root, "", now.Add(time.Minute), "commit", "--allow-empty", "-m", "green")
	greenOID := fixtureGit(t, root, "", now.Add(time.Minute), "rev-parse", "HEAD")
	fixtureGit(t, root, "", now.Add(time.Minute), "update-ref", "refs/tillr/green", greenOID)
	writeGreenProofJSON(t, root, greenOID, now.Add(time.Minute), []string{"check-a"})

	s := watchStore(t)
	watchSQL(t, s, `CREATE TABLE items(item_id TEXT PRIMARY KEY,state TEXT,seat TEXT,waiting_on TEXT,event_seq INTEGER)`)
	watchSQL(t, s, `CREATE TABLE item_criteria(item_id TEXT,criterion_id TEXT,text TEXT)`)

	fixerID := "fixer-main-" + redOID[:12]
	watchSQL(t, s, `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (?,'item.add','item',?,?)`, now.UnixMilli(), fixerID, `{"item_id":"`+fixerID+`"}`)
	var addSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT MAX(seq) FROM events WHERE object_id = ?`, fixerID).Scan(&addSeq)
	}); err != nil {
		t.Fatal(err)
	}
	watchSQL(t, s, `INSERT INTO items VALUES (?,'in-queue',NULL,NULL,?)`, fixerID, addSeq)
	watchSQL(t, s, `INSERT INTO item_criteria VALUES (?,'trunk-green','(red: check-a)')`, fixerID)

	src := NewTillrStoreSource(s)
	src.Clock = func() time.Time { return now.Add(2 * time.Minute) }

	// Project exposes item.id on items and fixer_checks.
	proj, err := src.Project(ctx, []string{"items", "fixer_checks"})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	itemsProj := proj["items"].(map[string]map[string]any)
	if itemsProj[fixerID]["item.id"] != fixerID {
		t.Fatalf("items[fixerID][item.id] = %v, want %s", itemsProj[fixerID]["item.id"], fixerID)
	}
	fcProj := proj["fixer_checks"].(map[string]map[string]any)
	if fcProj[fixerID]["subject_seq"] != addSeq {
		t.Fatalf("fixer_checks[fixerID][subject_seq] = %v, want %d", fcProj[fixerID]["subject_seq"], addSeq)
	}

	// Non-TillrStoreSource refuses fixer_checks.
	memSrc := &memoryObservationSource{}
	memRule := watchRules(t, `[[rule]]
id = "mem-fixer"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.id matches "^fixer-main-" and item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "{{subject}}"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`)[0]
	memEval := Evaluator{Source: memSrc, Root: root, Rules: []Rule{memRule}, Clock: src.Clock}
	if res, err := memEval.RuleResults(ctx); err != nil || len(res) != 1 || res[0].Unsupported != `not evaluated: scope "core" requires a tillr store source` {
		t.Fatalf("expected unsupported scope on memory source, got %+v (%v)", res, err)
	}

	rules := watchRules(t, `[[rule]]
id = "source-fixer-proved"
scope = "core"
severity = "warning"
[rule.reads]
projections = ["items", "fixer_checks", "green_proof:refs/tillr/green"]
[rule.when]
group_by = "item_id"
pattern = "predicate_match"
match = 'item.id matches "^fixer-main-" and item.state == "in-queue" and item.fixer_proved("refs/tillr/green")'
[rule.guidance]
summary = "Fixer {{subject}} proved ({{count}} {{elapsed}})"
check = ["c"]
do = ["d"]
[rule.delivery]
receiver = "foreground"
`)
	e := Evaluator{Source: src, Root: root, Rules: rules, Clock: src.Clock}
	res, err := e.EvaluateRules(ctx)
	if err != nil || len(res) != 1 || res[0].Diagnostic() != "" {
		t.Fatalf("EvaluateRules via Source: %+v (%v)", res, err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 || notices[0].Bundle.Subject != fixerID || len(notices[0].Bundle.Events) != 0 {
		t.Fatalf("Pending via Source: %+v (%v)", notices, err)
	}

	// Ack watermark stability across unrelated store writes.
	note := "ack"
	if err := e.Ack(ctx, notices[0].AlertID, "op", &note); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now.Add(2*time.Minute), `{"unrelated":true}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Alerts) != 1 || st.Alerts[0].State != "acknowledged" {
		t.Fatalf("expected acknowledged alert after unrelated write, got %+v (%v)", st, err)
	}

	// condition-cleared resolution when item leaves in-queue.
	watchSQL(t, s, `UPDATE items SET state='ready' WHERE item_id=?`, fixerID)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Alerts) != 0 {
		t.Fatalf("expected 0 active alerts after condition-cleared, got %+v (%v)", st, err)
	}

	// Re-open on in-queue, then item-closed resolution.
	watchSQL(t, s, `UPDATE items SET state='in-queue' WHERE item_id=?`, fixerID)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Alerts) != 1 || st.Alerts[0].State != "open" {
		t.Fatalf("expected reopened open alert, got %+v (%v)", st, err)
	}
	watchSQL(t, s, `UPDATE items SET state='closed' WHERE item_id=?`, fixerID)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
	if err != nil || len(st.Alerts) != 0 {
		t.Fatalf("expected 0 active alerts after item-closed, got %+v (%v)", st, err)
	}
}
