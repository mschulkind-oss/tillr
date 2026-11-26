package alarms

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
	_ "github.com/mschulkind-oss/tillr/watch"
)

func TestUnsettledWatchAlertsAlarm(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	now := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)

	readAlarms := func(at time.Time) []Alarm {
		t.Helper()
		var active []Alarm
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			active, err = Read(r, at, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return active
	}

	// Store with no unsettled alerts produces no alarm.
	if got := readAlarms(now); len(got) != 0 {
		t.Fatalf("empty store alarms = %+v, want none", got)
	}

	// Seed:
	// - alrt-old: open, uninvestigated, 25m old -> included
	// - alrt-res: resolved, investigated, 10m old -> included (resolved still unsettled)
	// - alrt-triage: open, uninvestigated, under active triage claim -> excluded
	// - alrt-owner: open, investigated, escalated to owner -> excluded
	// - alrt-settled: open, settled -> excluded
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes(
			alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint,
			opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json,
			settlement_state, active_triage_run_id, escalated_to
		) VALUES
			('alrt-old', 'r1', 's1', 1, 'open', 'warning', 'fp1', 1, ?, 1, ?, '{}', 'uninvestigated', NULL, NULL),
			('alrt-res', 'r1', 's2', 1, 'resolved', 'warning', 'fp2', 2, ?, 2, ?, '{}', 'investigated', NULL, NULL),
			('alrt-triage', 'r1', 's3', 1, 'open', 'warning', 'fp3', 3, ?, 3, ?, '{}', 'uninvestigated', 'triage-run-1', NULL),
			('alrt-owner', 'r1', 's4', 1, 'open', 'warning', 'fp4', 4, ?, 4, ?, '{}', 'investigated', NULL, 'owner'),
			('alrt-settled', 'r1', 's5', 1, 'silenced', 'warning', 'fp5', 5, ?, 5, ?, '{}', 'settled', NULL, NULL)`,
			now.Add(-25*time.Minute).UnixMilli(), now.Add(-25*time.Minute).UnixMilli(),
			now.Add(-10*time.Minute).UnixMilli(), now.Add(-10*time.Minute).UnixMilli(),
			now.Add(-40*time.Minute).UnixMilli(), now.Add(-40*time.Minute).UnixMilli(),
			now.Add(-50*time.Minute).UnixMilli(), now.Add(-50*time.Minute).UnixMilli(),
			now.Add(-60*time.Minute).UnixMilli(), now.Add(-60*time.Minute).UnixMilli(),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	got := readAlarms(now)
	if len(got) != 1 || got[0].Kind != UnsettledWatchAlerts {
		t.Fatalf("alarms = %+v, want one %s", got, UnsettledWatchAlerts)
	}
	rendered := got[0].String()
	for _, want := range []string{"2 unsettled watch alert(s)", "oldest 25m", "alrt-old", "alrt-res"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("alarm %q missing %q", rendered, want)
		}
	}
	for _, excluded := range []string{"alrt-triage", "alrt-owner", "alrt-settled"} {
		if strings.Contains(rendered, excluded) {
			t.Fatalf("alarm %q should exclude %q", rendered, excluded)
		}
	}

	// Settling both included alerts clears the alarm.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET settlement_state='settled' WHERE alert_id IN ('alrt-old', 'alrt-res')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := readAlarms(now); len(got) != 0 {
		t.Fatalf("expected unsettled-watch-alerts alarm to clear, got %+v", got)
	}
}

func TestUnsettledWatchAlertsAlarmWithoutWatchTable(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE IF EXISTS alert_episodes`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var active []Alarm
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		active, err = Read(r, time.Now().UTC(), nil)
		return err
	}); err != nil {
		t.Fatalf("Read without alert_episodes failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no alarms without alert_episodes, got %+v", active)
	}
}
