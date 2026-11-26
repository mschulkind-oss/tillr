package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWatchCLISettleInvestigateAndNoticeFooter(t *testing.T) {
	ctx := context.Background()
	s, rulesDir := fixture(t, true)
	now := time.Now()
	invoke := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		full := append([]string{args[0], "--store", s.Path()}, args[1:]...)
		code := run(ctx, full, &out, &errOut)
		return code, out.String(), errOut.String()
	}

	// Establish rule load baseline before appending refusal events.
	if c, _, e := invoke("eval", "--once", "--rules", rulesDir, "--no-shipped-rules"); c != 0 || e != "" {
		t.Fatalf("init eval: code=%d err=%q", c, e)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id, title, state, updated_at, event_seq) VALUES
			('item-open', 'open', 'in-queue', 1000, 0),
			('item-closed', 'closed', 'closed', 1000, 999999),
			('item-stale', 'stale', 'landed', 1000, 1),
			('item-fresh', 'fresh', 'landed', 1000, 999999)`); err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			if _, err := tx.Append(event.Event{Kind: "drain.refusal", At: now.Add(time.Duration(i) * time.Millisecond), Payload: []byte(`{"item_id":"item-subj"}`)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Retired commands are refused as unknown faces.
	for _, retired := range []string{"resolve", "track"} {
		code, _, errOut := invoke(retired, "alrt-any")
		if code != 1 || !strings.Contains(errOut, "unknown face") {
			t.Fatalf("%s should be retired: code=%d err=%q", retired, code, errOut)
		}
	}

	// Notice footer prints investigate and settle, not track.
	code, waitOut, waitErr := invoke("wait", "--rules", rulesDir, "--no-shipped-rules", "--timeout", "2s")
	if code != 0 || waitErr != "" {
		t.Fatalf("wait: code=%d err=%q", code, waitErr)
	}
	var alertID string
	faceRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &alertID)
	wantAck := fmt.Sprintf("  ack: tillr watch ack %s --by 'foreground' --store %s\n", alertID, shellWord(s.Path()))
	wantInv := fmt.Sprintf("  investigate: tillr watch investigate %s --by 'foreground' --cause <cause> --evidence <evidence> --store %s\n", alertID, shellWord(s.Path()))
	wantSettle := fmt.Sprintf("  settle: tillr watch settle %s --by 'foreground' --outcome <fix-rule|fix-fault> --item <item-id> --store %s\n", alertID, shellWord(s.Path()))
	if !strings.Contains(waitOut, wantAck) || !strings.Contains(waitOut, wantInv) || !strings.Contains(waitOut, wantSettle) || strings.Contains(waitOut, "  track:") {
		t.Fatalf("unexpected wait notice footer:\n%s", waitOut)
	}

	// Uninvestigated settle exits 2 without mutating store.
	beforeHead, err := s.EventHeadSeq()
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := invoke("settle", alertID, "--by", "operator", "--outcome", "fix-fault", "--item", "item-open")
	if code != 2 || out != "" || errOut == "" {
		t.Fatalf("uninvestigated settle: code=%d out=%q err=%q, want exit 2", code, out, errOut)
	}
	afterHead, err := s.EventHeadSeq()
	if err != nil || afterHead != beforeHead {
		t.Fatalf("uninvestigated settle mutated event head: %d -> %d", beforeHead, afterHead)
	}

	// Investigate succeeds.
	code, out, errOut = invoke("investigate", alertID, "--by", "operator", "--cause", "bad retry", "--evidence", "seq 1-2")
	if code != 0 || errOut != "" || out != fmt.Sprintf("alert %s investigated\n", alertID) {
		t.Fatalf("investigate: code=%d out=%q err=%q", code, out, errOut)
	}

	// Refuses expected outcome, missing, closed, stale-finished items.
	for _, bad := range [][]string{
		{"settle", alertID, "--by", "operator", "--outcome", "expected", "--item", "item-open"},
		{"settle", alertID, "--by", "operator", "--outcome", "fix-fault", "--item", "item-missing"},
		{"settle", alertID, "--by", "operator", "--outcome", "fix-fault", "--item", "item-closed"},
		{"settle", alertID, "--by", "operator", "--outcome", "fix-fault", "--item", "item-stale"},
	} {
		c, _, e := invoke(bad...)
		if c != 1 || e == "" {
			t.Fatalf("expected exit 1 for %v, got code=%d err=%q", bad, c, e)
		}
	}

	// Newly-finished item succeeds.
	code, out, errOut = invoke("settle", alertID, "--by", "operator", "--outcome", "fix-rule", "--item", "item-fresh", "--link-kind", "filed")
	if code != 0 || errOut != "" || out != fmt.Sprintf("alert %s settled (fix-rule -> item-fresh)\n", alertID) {
		t.Fatalf("settle: code=%d out=%q err=%q", code, out, errOut)
	}

	// Idempotent repeat exits 0; conflicting repeat exits 1.
	if c, _, e := invoke("settle", alertID, "--by", "operator", "--outcome", "fix-rule", "--item", "item-fresh"); c != 0 || e != "" {
		t.Fatalf("idempotent settle: code=%d err=%q", c, e)
	}
	if c, _, e := invoke("settle", alertID, "--by", "other", "--outcome", "fix-fault", "--item", "item-open"); c != 1 || !strings.Contains(e, "operator") {
		t.Fatalf("conflicting settle: code=%d err=%q", c, e)
	}

	// New instance past settled_seq reopens with recurred-after-item-landed (since item-fresh landed).
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: "drain.refusal", At: time.Now(), Payload: []byte(`{"item_id":"item-subj"}`)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if c, _, e := invoke("eval", "--once", "--rules", rulesDir, "--no-shipped-rules"); c != 0 || e != "" {
		t.Fatalf("eval: code=%d err=%q", c, e)
	}
	var st, sst string
	faceRead(t, s, `SELECT state, settlement_state FROM alert_episodes WHERE alert_id='`+alertID+`'`, &st, &sst)
	if st != "open" || sst != "uninvestigated" {
		t.Fatalf("after reopen: state=%q sst=%q, want open/uninvestigated", st, sst)
	}

	// Inline --cause / --evidence settle works in one command.
	code, out, errOut = invoke("settle", alertID, "--by", "operator", "--outcome", "fix-fault", "--item", "item-open", "--cause", "new fault", "--evidence", "seq 5")
	if code != 0 || errOut != "" || out != fmt.Sprintf("alert %s settled (fix-fault -> item-open)\n", alertID) {
		t.Fatalf("inline settle: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestWatchCLILegacyStoreMigrationOnInvestigateAndSettle(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES (6, 1000);
		CREATE TABLE events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL,
			seat TEXT, object_type TEXT, object_id TEXT, revision INTEGER, payload TEXT NOT NULL
		);
		CREATE TABLE items (item_id TEXT PRIMARY KEY, state TEXT NOT NULL, event_seq INTEGER NOT NULL DEFAULT 0);
		INSERT INTO items(item_id, state, event_seq) VALUES ('item-open', 'in-queue', 0);
		CREATE TABLE definition_versions (version INTEGER PRIMARY KEY, raw TEXT NOT NULL, parsed_json TEXT NOT NULL, at INTEGER NOT NULL);
		CREATE TABLE alert_episodes (
			alert_id TEXT PRIMARY KEY, rule_id TEXT NOT NULL, subject_key TEXT NOT NULL,
			episode_num INTEGER NOT NULL, state TEXT NOT NULL, severity TEXT NOT NULL,
			condition_fingerprint TEXT NOT NULL, opened_seq INTEGER NOT NULL, opened_at INTEGER NOT NULL,
			last_observed_seq INTEGER NOT NULL, last_observed_at INTEGER NOT NULL,
			observation_count INTEGER NOT NULL DEFAULT 1, acked_by TEXT, acked_note TEXT, acked_at INTEGER,
			silence_until_ms INTEGER, silence_on_fingerprint TEXT, active_triage_run_id TEXT,
			resolved_seq INTEGER, resolved_at INTEGER, resolved_reason TEXT, bundle_json TEXT NOT NULL
		);
		INSERT INTO alert_episodes(alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, resolved_seq, resolved_at, resolved_reason, bundle_json)
		VALUES
			('alrt-old-resolved', 'r1', 's1', 1, 'resolved', 'warning', 'fp1', 1, 1000, 1, 1000, 2, 2000, 'condition-cleared', '{}'),
			('alrt-old-open', 'r1', 's2', 1, 'open', 'warning', 'fp2', 3, 3000, 3, 3000, NULL, NULL, NULL, '{}');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	// Uninvestigated settle on legacy DB still exits 2 without migrating or mutating.
	var out, errOut bytes.Buffer
	code := run(ctx, []string{"settle", "--store", path, "alrt-old-open", "--by", "op", "--outcome", "fix-fault", "--item", "item-open"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("uninvestigated settle on legacy DB: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}

	// Investigate triggers migration and marks pre-existing resolved row settled.
	out.Reset()
	errOut.Reset()
	code = run(ctx, []string{"investigate", "--store", path, "alrt-old-open", "--by", "op", "--cause", "c", "--evidence", "e"}, &out, &errOut)
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("investigate on legacy DB: code=%d out=%q err=%q", code, out.String(), errOut.String())
	}

	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var oldResSST, oldOpenSST string
	faceRead(t, s, `SELECT settlement_state FROM alert_episodes WHERE alert_id='alrt-old-resolved'`, &oldResSST)
	faceRead(t, s, `SELECT settlement_state FROM alert_episodes WHERE alert_id='alrt-old-open'`, &oldOpenSST)
	if oldResSST != "settled" || oldOpenSST != "investigated" {
		t.Fatalf("legacy migration: old-resolved=%q old-open=%q, want settled/investigated", oldResSST, oldOpenSST)
	}
}

func seedCLIItems(t *testing.T, s *store.Store, items map[string]string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		head, err := tx.Reader().EventHeadSeq()
		if err != nil {
			return err
		}
		for id, st := range items {
			if _, err := tx.Exec(
				`INSERT INTO items (item_id, title, state, updated_at, event_seq) VALUES (?, ?, ?, 1000, ?)
				 ON CONFLICT(item_id) DO UPDATE SET state = excluded.state, event_seq = excluded.event_seq`,
				id, id, st, head,
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
