package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWaitingQuotaFailureShowsErrorInsteadOfFractions(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir()+"/store.db", true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(quota.DDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_quota VALUES('claude','[]',?,0.2,'[]','quota command failed: invalid quota JSON')`, at.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		row := rows[0]
		if row.Class != "alarm" || row.Mover != "operator; repair the quota probe for a successful reading" || row.Quota == nil || row.Quota.Error == "" {
			t.Fatalf("failed reading not visible: %+v", row)
		}
		for _, want := range []string{"reserve 0.2", "invalid quota JSON", "reopening unknown", at.Add(quota.ProbeInterval).Format(time.RFC3339)} {
			if !strings.Contains(row.Why, want) {
				t.Errorf("missing %q: %s", want, row.Why)
			}
		}
		if strings.Contains(row.Why, "used ") || strings.Contains(row.Why, "open now") {
			t.Fatalf("failed reading claims usage or availability: %s", row.Why)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingQuotaShowsOpenReserveAndAllWindows(t *testing.T) {
	path := t.TempDir() + "/store.db"
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	at := time.Now().UTC()
	short, long := at.Add(time.Hour), at.Add(7*24*time.Hour)
	windows, _ := json.Marshal([]quota.QuotaWindow{{Kind: "five_hour", Fraction: .8, ResetAt: &short}, {Kind: "seven_day", Fraction: .9, ResetAt: &long}})
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(quota.DDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO backend_quota VALUES('claude','[]',?,0.2,?,'')`, at.UnixMilli(), string(windows))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		row := rows[0]
		for _, want := range []string{"five_hour used 0.8", "seven_day used 0.9", "reserve 0.2", long.Format(time.RFC3339)} {
			if !strings.Contains(row.Why, want) {
				t.Errorf("missing %q: %s", want, row.Why)
			}
		}
		if row.Quota == nil || len(row.Quota.Windows) != 2 {
			t.Fatalf("missing JSON reading: %+v", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at.Add(quota.ProbeInterval+time.Second))
		if err == nil && (!strings.Contains(rows[0].Why, "stale") || strings.Contains(rows[0].Why, "open now")) {
			t.Errorf("stale=%+v", rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE backend_quota SET windows='[{"kind":"five_hour","fraction":0,"reset_at":null}]'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotas(r, at)
		if err == nil && (!strings.Contains(rows[0].Why, "open now") || !strings.Contains(rows[0].Why, "idle")) {
			t.Errorf("idle=%+v", rows)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingBackendQuotasWithRootInactiveVsAllClosedAndProbeError(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, ".tillr", "state.db")
	s, err := store.InitFixture(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	at := time.Date(2026, 11, 27, 18, 0, 0, 0, time.UTC)
	reset := at.Add(2 * time.Hour)
	closedWin := `[{"kind":"five_hour","fraction":0.85,"reset_at":"` + reset.Format(time.RFC3339) + `"}]`
	openWin := `[{"kind":"five_hour","fraction":0.10,"reset_at":"` + reset.Format(time.RFC3339) + `"}]`

	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(quota.DDL); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO backend_quota VALUES ('claude','["probe-claude"]',?,0.2,?,'')`, at.UnixMilli(), closedWin); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO backend_quota VALUES ('copilot','["probe-copilot"]',?,0.2,?,'')`, at.UnixMilli(), openWin); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Active profile is copilot (open); claude (inactive profile) is closed -> waits on work, NOT alarm.
	writeTestLoopPolicyState(t, root, map[string]any{
		"profile":       "copilot-gemini",
		"profiles":      []string{"claude-sub", "copilot-gemini"},
		"profile_index": 1,
		"source":        "toml",
		"skipped_profiles": []map[string]any{
			{
				"profile":          "claude-sub",
				"backend":          "claude",
				"reserve":          0.2,
				"reopen_threshold": 0.7,
				"reset_at":         reset.Format(time.RFC3339),
			},
		},
		"content": `{"default_backend":"copilot","seats":{"build":"copilot"},"backends":{"copilot":{"adapter":"true","model":"m","effort":"low"}}}`,
	})
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotasWithRoot(r, at, root)
		if err != nil {
			return err
		}
		byID := map[string]waitingRow{}
		for _, row := range rows {
			byID[row.ItemID] = row
		}
		claudeRow := byID["backend:claude"]
		if claudeRow.Class != "waits on work" {
			t.Fatalf("inactive closed profile class = %q, want %q (row=%+v)", claudeRow.Class, "waits on work", claudeRow)
		}
		if !strings.Contains(claudeRow.Mover, "switched to copilot-gemini") || !strings.Contains(claudeRow.Mover, "<= 0.7") {
			t.Fatalf("inactive closed profile mover = %q", claudeRow.Mover)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// All profiles closed (copilot also closed) -> claude and copilot both alarm.
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE backend_quota SET windows=? WHERE backend='copilot'`, closedWin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotasWithRoot(r, at, root)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Class != "alarm" {
				t.Fatalf("all profiles closed: %s class = %q, want alarm", row.ItemID, row.Class)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Copilot open again, but claude has a probe error -> claude MUST still alarm.
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE backend_quota SET windows=? WHERE backend='copilot'`, openWin); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE backend_quota SET windows='[]', error='auth expired' WHERE backend='claude'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := waitingBackendQuotasWithRoot(r, at, root)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.ItemID == "backend:claude" && row.Class != "alarm" {
				t.Fatalf("probe error on inactive profile class = %q, want alarm", row.Class)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
