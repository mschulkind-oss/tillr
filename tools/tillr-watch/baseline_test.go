package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchCLILoadBaselineSuppressesPreloadBurstAndAlertsOnPostLoad(t *testing.T) {
	ctx := context.Background()

	t.Run("eval once over multi-item pre-load history with threshold gt 1", func(t *testing.T) {
		dir := t.TempDir()
		s, err := store.Open(ctx, filepath.Join(dir, "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })

		rulesDir := filepath.Join(dir, "rules")
		if err := os.MkdirAll(rulesDir, 0700); err != nil {
			t.Fatal(err)
		}
		ruleSrc, err := os.ReadFile("rules/drain-repeated-verifier-refusals.toml")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rulesDir, "drain-repeated-verifier-refusals.toml"), ruleSrc, 0600); err != nil {
			t.Fatal(err)
		}

		appendRefusal := func(itemID string, at time.Time) {
			t.Helper()
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					At:      at,
					Kind:    "drain.refusal",
					Payload: []byte(fmt.Sprintf(`{"item_id":%q,"step":"verify-not-done"}`, itemID)),
				})
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}

		// Seed 11 items each with 5 matching in-window refusals before the rule is ever loaded.
		now := time.Now().UTC()
		for i := range 11 {
			itemID := fmt.Sprintf("item-preload-%02d", i)
			for j := range 5 {
				appendRefusal(itemID, now.Add(-time.Duration(10-j)*time.Minute))
			}
		}

		common := []string{"--store", s.Path(), "--rules", rulesDir, "--no-shipped-rules"}
		var out, stderr bytes.Buffer
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("first eval --once: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
		if code := run(ctx, append([]string{"wait", "--timeout", "30ms"}, common...), &out, &stderr); code != 2 || stderr.Len() != 0 || strings.Contains(out.String(), "TILLR_WATCH_NOTICE") {
			t.Fatalf("wait after first eval should be quiet: code=%d out=%q stderr=%q", code, &out, &stderr)
		}

		assertStatus := func(wantAlerts, wantPending int) {
			t.Helper()
			out.Reset()
			stderr.Reset()
			if code := run(ctx, append([]string{"status", "--json"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
				t.Fatalf("status: code=%d out=%q stderr=%q", code, &out, &stderr)
			}
			var report struct {
				Alerts []struct {
					RuleID  string `json:"rule_id"`
					Subject string `json:"subject"`
				}
				Owners []struct {
					Pending int `json:"pending"`
				}
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("unmarshal status %q: %v", &out, err)
			}
			if len(report.Alerts) != wantAlerts {
				t.Fatalf("alerts=%d (%+v), want %d", len(report.Alerts), report.Alerts, wantAlerts)
			}
			pending := 0
			for _, o := range report.Owners {
				pending += o.Pending
			}
			if pending != wantPending {
				t.Fatalf("pending=%d, want %d", pending, wantPending)
			}
		}
		assertStatus(0, 0)

		// One post-load refusal on a pre-load item must not combine with its 5 pre-load events.
		appendRefusal("item-preload-00", time.Now().UTC())
		// Four post-load refusals on a new item stay below threshold=5.
		for range 4 {
			appendRefusal("item-postload", time.Now().UTC())
		}
		out.Reset()
		stderr.Reset()
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("eval --once below threshold: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
		assertStatus(0, 0)

		// The fifth post-load refusal for item-postload opens exactly one alert.
		appendRefusal("item-postload", time.Now().UTC())
		out.Reset()
		stderr.Reset()
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("eval --once at threshold: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
		assertStatus(1, 1)
	})

	t.Run("wait over pre-load history with threshold 1", func(t *testing.T) {
		s, rules := fixture(t, false)
		rule1 := strings.Replace(testRule, "threshold=2", "threshold=1", 1)
		if err := os.WriteFile(filepath.Join(rules, "nested", "refusals.toml"), []byte(rule1), 0600); err != nil {
			t.Fatal(err)
		}
		fact(t, s, `{"item_id":"preload-a"}`)
		fact(t, s, `{"item_id":"preload-b"}`)

		common := []string{"--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
		var out, stderr bytes.Buffer
		if code := run(ctx, append([]string{"wait", "--timeout", "30ms"}, common...), &out, &stderr); code != 2 || stderr.Len() != 0 || strings.Contains(out.String(), "TILLR_WATCH_NOTICE") {
			t.Fatalf("first wait over pre-load history should be quiet: code=%d out=%q stderr=%q", code, &out, &stderr)
		}

		fact(t, s, `{"item_id":"postload-c"}`)
		out.Reset()
		stderr.Reset()
		if code := run(ctx, append([]string{"wait", "--timeout", "2s"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(out.String(), "TILLR_WATCH_NOTICE 1 pending") || !strings.Contains(out.String(), "postload-c has 1 refusals") {
			t.Fatalf("wait after 1 post-load event should open 1 alert: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
	})

	t.Run("rule tune lowering threshold ignores pre-change events until post-change event", func(t *testing.T) {
		s, rules := fixture(t, false)
		rule3 := strings.Replace(testRule, "threshold=2", "threshold=3", 1)
		if err := os.WriteFile(filepath.Join(rules, "nested", "refusals.toml"), []byte(rule3), 0600); err != nil {
			t.Fatal(err)
		}
		common := []string{"--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
		var out, stderr bytes.Buffer
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("init eval: code=%d out=%q stderr=%q", code, &out, &stderr)
		}

		// Append 2 events while threshold=3 (below threshold).
		fact(t, s, `{"item_id":"item-tuned"}`)
		fact(t, s, `{"item_id":"item-tuned"}`)
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("eval before tune: code=%d out=%q stderr=%q", code, &out, &stderr)
		}

		// Tune threshold down to 2: pre-change events alone must not open an alert.
		if code := run(ctx, append([]string{"rule", "tune", "refusals", "--set=when.threshold=2", "--by=operator"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("rule tune: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
		out.Reset()
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("eval after tune: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
		var openCount int
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT COUNT(*) FROM alert_episodes WHERE state='open'`).Scan(&openCount)
		}); err != nil || openCount != 0 {
			t.Fatalf("open episodes after tune before post-change event: %d (%v)", openCount, err)
		}

		// One post-change matching event opens 1 alert.
		fact(t, s, `{"item_id":"item-tuned"}`)
		if code := run(ctx, append([]string{"eval", "--once"}, common...), &out, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("eval after post-change event: code=%d out=%q stderr=%q", code, &out, &stderr)
		}
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT COUNT(*) FROM alert_episodes WHERE state='open' AND subject_key='item-tuned'`).Scan(&openCount)
		}); err != nil || openCount != 1 {
			t.Fatalf("open episodes after post-change event: %d (%v)", openCount, err)
		}
	})

	t.Run("wait records rule baselines before advertising waiting_since", func(t *testing.T) {
		s, rules := fixture(t, false)
		// Capture the baseline count at the exact instant BeginWait sets waiting_since,
		// so events appended as soon as attachment is visible cannot be misclassified
		// as pre-load history if wait's goroutine is descheduled before its loop.
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			if _, err := tx.Exec(`CREATE TABLE test_attach_probe (baselines_at_attach INTEGER NOT NULL)`); err != nil {
				return err
			}
			_, err := tx.Exec(`CREATE TRIGGER probe_baseline_before_attach
				AFTER INSERT ON foreground_delivery
				WHEN NEW.waiting_since IS NOT NULL
				BEGIN
					INSERT INTO test_attach_probe(baselines_at_attach)
					VALUES ((SELECT COUNT(*) FROM watch_rule_baselines WHERE rule_id = 'refusals' AND enabled = 1));
				END`)
			return err
		}); err != nil {
			t.Fatal(err)
		}

		waitCtx, cancel := context.WithCancel(ctx)
		ticks := make(chan time.Time)
		output := make(outputChunks, 10)
		done := make(chan int, 1)
		var stderr bytes.Buffer
		go func() {
			defer close(done)
			done <- runWithTicks(waitCtx, []string{"wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--follow"}, output, &stderr, ticks)
		}()
		defer func() { cancel(); <-done }()

		awaitAttached(t, s)
		var baselinesAtAttach int
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT baselines_at_attach FROM test_attach_probe LIMIT 1`).Scan(&baselinesAtAttach)
		}); err != nil || baselinesAtAttach != 1 {
			t.Fatalf("baselines_at_attach=%d (err=%v), want 1 recorded before waiting_since is visible", baselinesAtAttach, err)
		}

		fact(t, s, `{"item_id":"item-after-attach"}`)
		fact(t, s, `{"item_id":"item-after-attach"}`)
		notice := awaitChunk(t, output)
		if !strings.Contains(notice, "item-after-attach has 2 refusals") {
			t.Fatalf("unexpected notice after attach: %q", notice)
		}
	})
}
