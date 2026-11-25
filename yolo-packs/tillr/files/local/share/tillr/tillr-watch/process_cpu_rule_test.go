package main

import (
	"bytes"
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

func TestDrainProcessCPUHighRule(t *testing.T) {
	const ruleID = "drain-process-cpu-high"

	allRules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := allRules[idx]
	if rule.Scope != "core" || rule.Severity != "warning" || !rule.Enabled ||
		rule.Delivery.Receiver != "foreground" ||
		!slices.Equal(rule.Delivery.AlsoNotify, []string{"subscribers"}) ||
		!slices.Equal(rule.Reads.Events, []string{drain.ProcessCPUKind}) ||
		len(rule.Reads.Projections) != 0 ||
		rule.When.ForEach != "processes" ||
		rule.When.GroupBy != "process_key" ||
		rule.When.Pattern != watch.ConsecutiveCount ||
		rule.When.Match == nil ||
		rule.When.Reset != nil ||
		rule.When.Threshold != 10 ||
		rule.When.Window != 30*time.Minute {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}
	if strings.Contains(strings.ToLower(rule.Guidance.Summary), "expected") {
		t.Fatalf("guidance summary must not contain 'expected': %q", rule.Guidance.Summary)
	}
	for _, line := range append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...) {
		if strings.Contains(strings.ToLower(line), "expected") {
			t.Fatalf("guidance line must not contain 'expected': %q", line)
		}
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	type harness struct {
		ctx     context.Context
		store   *store.Store
		now     *time.Time
		append  func(entries ...drain.ProcessCPUEntry) int64
		pending func() []watch.Notice
	}
	newHarness := func(t *testing.T, rules []watch.Rule) harness {
		t.Helper()
		ctx := context.Background()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := time.Date(2026, 11, 23, 5, 50, 0, 0, time.UTC)
		e := watch.Evaluator{Store: s, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		appendSample := func(entries ...drain.ProcessCPUEntry) int64 {
			t.Helper()
			now = now.Add(time.Minute)
			fact := drain.ProcessCPUFact{
				SessionID: "session-cpu-incident",
				WindowMS:  60_000,
				Processes: entries,
			}
			for i, entry := range entries {
				switch entry.Role {
				case drain.ProcessRoleDrainLoop:
					fact.SelfPID = entry.PID
					fact.SelfCPUPercent = entry.CPUPercent
				case drain.ProcessRoleWatchWait:
					if fact.WatchWaitPID == 0 || entry.CPUPercent > fact.WatchWaitCPUPercent {
						fact.WatchWaitPID = entry.PID
						fact.WatchWaitCPUPercent = entry.CPUPercent
					}
				case drain.ProcessRoleViewServe:
					if fact.ViewServePID == 0 || entry.CPUPercent > fact.ViewServeCPUPercent {
						fact.ViewServePID = entry.PID
						fact.ViewServeCPUPercent = entry.CPUPercent
					}
				}
				if i == 0 || entry.CPUPercent > fact.MaxCPUPercent {
					fact.MaxCPUPercent = entry.CPUPercent
					fact.MaxRole = entry.Role
					fact.MaxPID = entry.PID
				}
			}
			body, err := json.Marshal(fact)
			if err != nil {
				t.Fatal(err)
			}
			sessID := fact.SessionID
			var seq int64
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				var err error
				seq, err = tx.Append(event.Event{
					Kind:       drain.ProcessCPUKind,
					ObjectType: "session",
					ObjectID:   &sessID,
					At:         now,
					Payload:    body,
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			return seq
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
		return harness{ctx: ctx, store: s, now: &now, append: appendSample, pending: pending}
	}

	// 1. Nine samples at 133% followed by a 10th sample at 50% (boundary) resets the streak;
	// drain-loop at 150% across 12 samples never fires.
	t.Run("under_threshold_boundary_reset_and_drain_loop_excluded", func(t *testing.T) {
		h := newHarness(t, allRules)
		loopEntry := drain.ProcessCPUEntry{PID: 1001, Role: drain.ProcessRoleDrainLoop, CPUPercent: 150, ProcessKey: "drain-loop:1001:5000"}
		for range 9 {
			h.append(
				loopEntry,
				drain.ProcessCPUEntry{PID: 2001, Role: drain.ProcessRoleWatchWait, CPUPercent: 133, ProcessKey: "tillr-watch-wait:2001:6000"},
			)
		}
		if notices := h.pending(); len(notices) != 0 {
			t.Fatalf("9 samples must not alert: %+v", notices)
		}
		// 10th sample at exact 50% boundary resets the streak.
		h.append(
			loopEntry,
			drain.ProcessCPUEntry{PID: 2001, Role: drain.ProcessRoleWatchWait, CPUPercent: 50, ProcessKey: "tillr-watch-wait:2001:6000"},
		)
		if notices := h.pending(); len(notices) != 0 {
			t.Fatalf("50%% boundary sample must reset streak: %+v", notices)
		}
		// 9 more samples at 133% still must not alert (and drain-loop now has 19 samples at 150%).
		for range 9 {
			h.append(
				loopEntry,
				drain.ProcessCPUEntry{PID: 2001, Role: drain.ProcessRoleWatchWait, CPUPercent: 133, ProcessKey: "tillr-watch-wait:2001:6000"},
			)
		}
		if notices := h.pending(); len(notices) != 0 {
			t.Fatalf("9 post-reset samples and 19 drain-loop samples must not alert: %+v", notices)
		}
	})

	// 2. Alternating spikes across two tillr-watch-wait processes never fire.
	t.Run("alternating_two_processes_never_alert", func(t *testing.T) {
		h := newHarness(t, allRules)
		for i := range 12 {
			p1, p2 := 80, 0
			if i%2 == 1 {
				p1, p2 = 0, 80
			}
			h.append(
				drain.ProcessCPUEntry{PID: 2001, Role: drain.ProcessRoleWatchWait, CPUPercent: p1, ProcessKey: "tillr-watch-wait:2001:6000"},
				drain.ProcessCPUEntry{PID: 2002, Role: drain.ProcessRoleWatchWait, CPUPercent: p2, ProcessKey: "tillr-watch-wait:2002:6100"},
			)
		}
		if notices := h.pending(); len(notices) != 0 {
			t.Fatalf("alternating spikes across two processes must not alert: %+v", notices)
		}
	})

	// 3. 2026-11-23 00:50 ET incident replay: simultaneous tillr-watch-wait (133%) and
	// tillr-view-serve (120%) for 10 consecutive samples open two distinct alerts, then
	// resolve independently on CPU drop and on process exit + PID reuse.
	t.Run("simultaneous_incident_and_independent_resolution", func(t *testing.T) {
		h := newHarness(t, allRules)
		const (
			watchKey = "tillr-watch-wait:41820:98123456"
			viewKey  = "tillr-view-serve:41835:98123510"
		)
		for range 10 {
			h.append(
				drain.ProcessCPUEntry{PID: 1001, Role: drain.ProcessRoleDrainLoop, CPUPercent: 150, ProcessKey: "drain-loop:1001:5000"},
				drain.ProcessCPUEntry{PID: 41820, Role: drain.ProcessRoleWatchWait, CPUPercent: 133, ProcessKey: watchKey},
				drain.ProcessCPUEntry{PID: 41835, Role: drain.ProcessRoleViewServe, CPUPercent: 120, ProcessKey: viewKey},
			)
		}
		notices := h.pending()
		if len(notices) != 2 {
			t.Fatalf("want 2 simultaneous alerts across config/tillr/watch, got %d: %+v", len(notices), notices)
		}
		bySubject := map[string]watch.Notice{}
		for _, n := range notices {
			if n.Bundle.RuleID != ruleID {
				t.Fatalf("unexpected rule %q fired: %+v", n.Bundle.RuleID, n)
			}
			bySubject[n.Bundle.Subject] = n
		}
		watchNotice, ok := bySubject[watchKey]
		if !ok {
			t.Fatalf("missing alert for %s in %+v", watchKey, notices)
		}
		for _, want := range []string{"tillr-watch-wait", "PID 41820", watchKey, "10 consecutive", "133%"} {
			if !strings.Contains(watchNotice.Bundle.Guidance.Summary, want) {
				t.Fatalf("watch summary missing %q: %q", want, watchNotice.Bundle.Guidance.Summary)
			}
		}
		viewNotice, ok := bySubject[viewKey]
		if !ok {
			t.Fatalf("missing alert for %s in %+v", viewKey, notices)
		}
		for _, want := range []string{"tillr-view-serve", "PID 41835", viewKey, "10 consecutive", "120%"} {
			if !strings.Contains(viewNotice.Bundle.Guidance.Summary, want) {
				t.Fatalf("view summary missing %q: %q", want, viewNotice.Bundle.Guidance.Summary)
			}
		}

		// Drop tillr-view-serve to 50% while tillr-watch-wait stays at 133%: view resolves, watch stays open.
		dropSeq := h.append(
			drain.ProcessCPUEntry{PID: 1001, Role: drain.ProcessRoleDrainLoop, CPUPercent: 10, ProcessKey: "drain-loop:1001:5000"},
			drain.ProcessCPUEntry{PID: 41820, Role: drain.ProcessRoleWatchWait, CPUPercent: 133, ProcessKey: watchKey},
			drain.ProcessCPUEntry{PID: 41835, Role: drain.ProcessRoleViewServe, CPUPercent: 50, ProcessKey: viewKey},
		)
		afterDrop := h.pending()
		if len(afterDrop) != 1 || afterDrop[0].Bundle.Subject != watchKey {
			t.Fatalf("want only %s open after view dropped to 50%%, got %+v", watchKey, afterDrop)
		}
		var viewState, viewReason string
		var viewResolvedSeq int64
		if err := h.store.ReadTx(h.ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE rule_id=? AND subject_key=?`,
				ruleID, viewKey).Scan(&viewState, &viewReason, &viewResolvedSeq)
		}); err != nil {
			t.Fatal(err)
		}
		if viewState != "resolved" || viewReason != "condition-cleared" || viewResolvedSeq != dropSeq {
			t.Fatalf("view episode = (%q, %q, %d), want (resolved, condition-cleared, %d)", viewState, viewReason, viewResolvedSeq, dropSeq)
		}

		// tillr-watch-wait exits and a new watcher reuses PID 41820 with a newer startTime:
		// the old episode resolves immediately and the new process starts its own streak at 1.
		exitSeq := h.append(
			drain.ProcessCPUEntry{PID: 1001, Role: drain.ProcessRoleDrainLoop, CPUPercent: 10, ProcessKey: "drain-loop:1001:5000"},
			drain.ProcessCPUEntry{PID: 41820, Role: drain.ProcessRoleWatchWait, CPUPercent: 133, ProcessKey: "tillr-watch-wait:41820:99999999"},
		)
		afterExit := h.pending()
		if len(afterExit) != 0 {
			t.Fatalf("want 0 open alerts after watch exited and PID was reused, got %+v", afterExit)
		}
		var watchState, watchReason string
		var watchResolvedSeq int64
		if err := h.store.ReadTx(h.ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE rule_id=? AND subject_key=?`,
				ruleID, watchKey).Scan(&watchState, &watchReason, &watchResolvedSeq)
		}); err != nil {
			t.Fatal(err)
		}
		if watchState != "resolved" || watchReason != "condition-cleared" || watchResolvedSeq != exitSeq {
			t.Fatalf("watch episode = (%q, %q, %d), want (resolved, condition-cleared, %d)", watchState, watchReason, watchResolvedSeq, exitSeq)
		}
	})
}
