package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainCheckpointNotRestartedRule(t *testing.T) {
	const (
		ruleID    = "drain-checkpoint-not-restarted"
		rulePath  = "rules/drain-checkpoint-not-restarted.toml"
		threshold = 65
	)

	rawRule, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read %s: %v", rulePath, err)
	}
	var commentLines []string
	for _, line := range strings.Split(string(rawRule), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			commentLines = append(commentLines, trimmed)
		}
	}
	comment := strings.Join(commentLines, "\n")
	for _, required := range []string{"sqlite3", "2026-11-24", "268", "15", "52", "60.9s", "62.6s"} {
		if !strings.Contains(comment, required) {
			t.Fatalf("rule comment in %s must state command, date, and figures (missing %q):\n%s", rulePath, required, comment)
		}
	}

	allRules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := allRules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}
	if rule.Scope != "core" || rule.Severity != "warning" || !rule.Enabled ||
		rule.Delivery.Receiver != "foreground" ||
		!slices.Equal(rule.Reads.Events, []string{drain.CheckpointNotRestartedKind}) ||
		len(rule.Reads.Projections) != 0 ||
		rule.When.GroupBy != "global" ||
		rule.When.Pattern != watch.PredicateMatch ||
		rule.When.Match == nil ||
		rule.When.ClearWhen == nil ||
		rule.When.Window != 3*time.Minute {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance calls a firing alarm expected: %q", ruleID, text)
		}
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	newHarness := func(t *testing.T, rules []watch.Rule, root string, start time.Time) (context.Context, *store.Store, *time.Time, func(string, int, int, int64), func() []watch.Notice) {
		t.Helper()
		ctx := context.Background()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := start
		e := watch.Evaluator{Store: s, Root: root, Rules: rules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		appendMissed := func(sessionID string, consecutive, frames int, logBytes int64) {
			t.Helper()
			body, err := json.Marshal(drain.CheckpointNotRestartedFact{
				LogFrames:         frames,
				LogBytes:          logBytes,
				ConsecutiveCycles: consecutive,
				SessionID:         sessionID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					Kind:       drain.CheckpointNotRestartedKind,
					ObjectType: "session",
					ObjectID:   &sessionID,
					At:         now,
					Payload:    body,
				})
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
		return ctx, s, &now, appendMissed, pending
	}

	assertResolvedReason := func(t *testing.T, ctx context.Context, s *store.Store, alertID, wantReason string) {
		t.Helper()
		var state, reason string
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT state, COALESCE(resolved_reason, '') FROM alert_episodes WHERE alert_id = ?`, alertID).Scan(&state, &reason)
		}); err != nil {
			t.Fatalf("read episode %s: %v", alertID, err)
		}
		if state != "resolved" || reason != wantReason {
			t.Fatalf("episode %s state=%q resolved_reason=%q, want resolved/%q", alertID, state, reason, wantReason)
		}
	}

	t0 := time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)

	t.Run("sub-threshold streaks stay quiet", func(t *testing.T) {
		_, _, now, appendMissed, pending := newHarness(t, []watch.Rule{rule}, "", t0)
		// Longest recorded self-clearing streak (52 cycles) and the boundary (64 cycles) must stay quiet.
		for _, cyc := range []int{1, 8, 40, 52, threshold - 1} {
			*now = now.Add(time.Minute)
			appendMissed("sess-1", cyc, 8073, 724304272)
			if notices := pending(); len(notices) != 0 {
				t.Fatalf("consecutive_cycles=%d (< %d) opened notices: %+v", cyc, threshold, notices)
			}
		}
	})

	t.Run("fires past threshold and clears when clean restart stops missed-cycle events", func(t *testing.T) {
		ctx, s, now, appendMissed, pending := newHarness(t, []watch.Rule{rule}, "", t0)

		*now = t0.Add(time.Minute)
		appendMissed("sess-clean", threshold, 175802, 724304272)
		first := pending()
		if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != "global" {
			t.Fatalf("want one %s notice at threshold %d, got %+v", ruleID, threshold, first)
		}
		g := first[0].Bundle.Guidance
		for _, want := range []string{"65", "175802", "724304272"} {
			if !strings.Contains(g.Summary, want) {
				t.Fatalf("summary missing %q: %+v", want, g)
			}
		}
		if !strings.Contains(strings.Join(g.Check, " "), "tillr drain why") {
			t.Fatalf("check guidance missing tillr drain why: %+v", g)
		}

		// Ongoing 1-minute missed cycles (~61s apart) keep the same single episode open.
		for i, cyc := range []int{threshold + 1, threshold + 2} {
			*now = t0.Add(time.Minute + time.Duration(i+1)*61*time.Second)
			appendMissed("sess-clean", cyc, 176500+i*400, 724304272)
			again := pending()
			if len(again) != 1 || again[0].AlertID != first[0].AlertID {
				t.Fatalf("cycle %d should keep episode %s open, got %+v", cyc, first[0].AlertID, again)
			}
		}

		// When a checkpoint cycle restarts the WAL, wal_keeper appends no new event.
		// Before the 3m window expires (+2m after the last missed cycle), the episode stays open;
		// once 3m has elapsed with clean checkpoints and no new missed-cycle event, it resolves.
		lastMissAt := *now
		*now = lastMissAt.Add(2 * time.Minute)
		if stillOpen := pending(); len(stillOpen) != 1 || stillOpen[0].AlertID != first[0].AlertID {
			t.Fatalf("expected episode to remain open 2m after last miss (< 3m window), got %+v", stillOpen)
		}
		*now = lastMissAt.Add(3*time.Minute + time.Second)
		if cleared := pending(); len(cleared) != 0 {
			t.Fatalf("expected clean WAL restart (no missed cycles for > 3m) to clear %s, got %+v", ruleID, cleared)
		}
		assertResolvedReason(t, ctx, s, first[0].AlertID, "condition-cleared")
	})

	t.Run("clears immediately when log restarts with real CheckpointOwner and resets cycle count", func(t *testing.T) {
		ctx := context.Background()
		storePath := filepath.Join(t.TempDir(), "state.db")
		s, err := store.Open(ctx, storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })

		owner, err := store.CreateAndOwnCheckpoints(ctx, storePath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { owner.Close(ctx) })

		now := t0
		e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}

		sessionID := "sess-real-wal"
		recordOutcome := func(c store.Checkpointed, consecutive *int) {
			t.Helper()
			if c.Restarted {
				*consecutive = 0
				return
			}
			if c.WriterBusy || c.PinBusy {
				return
			}
			*consecutive++
			body, err := json.Marshal(drain.CheckpointNotRestartedFact{
				LogFrames:         c.LogFrames,
				LogBytes:          c.LogBytes,
				ConsecutiveCycles: *consecutive,
				SessionID:         sessionID,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					Kind:       drain.CheckpointNotRestartedKind,
					ObjectType: "session",
					ObjectID:   &sessionID,
					At:         now,
					Payload:    body,
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}

		holdSnapshot := func() func() {
			t.Helper()
			// Put an uncopied commit in the WAL first so the reader snapshot pins WAL frames.
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('wal_pin_probe', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			release := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				r, err := store.OpenRead(ctx, storePath)
				if err != nil {
					t.Error(err)
					close(started)
					return
				}
				defer r.Close()
				_ = r.ReadTx(ctx, func(rd *store.Reader) error {
					var n int
					err := rd.QueryRow(`SELECT count(*) FROM meta`).Scan(&n)
					close(started)
					<-release
					return err
				})
			}()
			<-started
			var once sync.Once
			return func() {
				once.Do(func() {
					close(release)
					<-done
				})
			}
		}

		// Simulate a streak reaching cycle 64 prior to a real pinned checkpoint at cycle 65.
		consecutive := threshold - 1
		release1 := holdSnapshot()
		defer release1()
		now = t0.Add(time.Minute)
		cMiss, err := owner.Checkpoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cMiss.Restarted {
			t.Fatalf("expected pinned reader snapshot to prevent WAL restart, got %+v", cMiss)
		}
		recordOutcome(cMiss, &consecutive)
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID {
			t.Fatalf("want %s open on cycle %d, got %+v", ruleID, consecutive, notices)
		}
		firstAlertID := notices[0].AlertID

		// Release the reader and run a real checkpoint that restarts the WAL (appending no event
		// and resetting consecutive to 0), then a fresh pinned cycle 1m later (inside the 3m window)
		// appends consecutive_cycles = 1, clearing the alert immediately via clear_when.
		release1()
		now = t0.Add(2 * time.Minute)
		cRestart, err := owner.Checkpoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !cRestart.Restarted {
			t.Fatalf("expected WAL to restart once reader snapshot released, got %+v", cRestart)
		}
		recordOutcome(cRestart, &consecutive)
		if consecutive != 0 {
			t.Fatalf("consecutive after restart = %d, want 0", consecutive)
		}

		release2 := holdSnapshot()
		defer release2()
		now = t0.Add(2*time.Minute + 30*time.Second)
		cResetMiss, err := owner.Checkpoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cResetMiss.Restarted {
			t.Fatalf("expected second pinned reader snapshot to prevent WAL restart, got %+v", cResetMiss)
		}
		recordOutcome(cResetMiss, &consecutive)
		release2()
		if consecutive != 1 {
			t.Fatalf("consecutive on first miss after restart = %d, want 1", consecutive)
		}

		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err = e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 0 {
			t.Fatalf("expected post-restart consecutive_cycles=1 (< %d) inside 3m window to clear %s, got %+v", threshold, ruleID, notices)
		}
		assertResolvedReason(t, ctx, s, firstAlertID, "condition-cleared")

		// A subsequent climb back to threshold opens a distinct episode.
		consecutive = threshold - 1
		release3 := holdSnapshot()
		defer release3()
		now = t0.Add(4 * time.Minute)
		cReopen, err := owner.Checkpoint(ctx)
		if err != nil {
			t.Fatal(err)
		}
		release3()
		recordOutcome(cReopen, &consecutive)
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		reopened, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(reopened) != 1 || reopened[0].AlertID == firstAlertID {
			t.Fatalf("want fresh episode after post-restart climb to %d, got %+v (first=%s)", threshold, reopened, firstAlertID)
		}
	})

	t.Run("across all config rules", func(t *testing.T) {
		root := t.TempDir()
		_, _, now, appendMissed, pending := newHarness(t, allRules, root, t0)
		*now = t0.Add(time.Minute)
		appendMissed("sess-all", threshold, 175802, 724304272)
		notices := pending()
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != "global" {
			t.Fatalf("want only %s across config/tillr/watch, got %+v", ruleID, notices)
		}
		*now = now.Add(3*time.Minute + time.Second)
		if after := pending(); len(after) != 0 {
			t.Fatalf("want %s resolved across config/tillr/watch after clean restart window, got %+v", ruleID, after)
		}
	})
}
