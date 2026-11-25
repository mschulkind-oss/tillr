package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

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

func TestWatchTrackAndAckWithItemWaitEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, dir := fixture(t, false)
	seedCLIItems(t, s, map[string]string{
		"item-slice-d": "in-queue",
		"item-slice-e": "blocked",
	})

	invoke := func(args ...string) (int, string, string) {
		var out, stderr bytes.Buffer
		code := run(ctx, append([]string{args[0], "--store", s.Path()}, args[1:]...), &out, &stderr)
		return code, out.String(), stderr.String()
	}

	// Establish rule baselines before seeding facts.
	if code, evalOut, evalErr := invoke("eval", "--once", "--rules", dir); code != 0 || evalErr != "" {
		t.Fatalf("init eval: code=%d out=%q err=%q", code, evalOut, evalErr)
	}

	// Open an alert for subject "go-affected".
	fact(t, s, `{"item_id":"go-affected"}`)
	fact(t, s, `{"item_id":"go-affected"}`)

	code, firstOut, firstErr := invoke("wait", "--rules", dir, "--timeout", "2s")
	if code != 0 || firstErr != "" {
		t.Fatalf("initial wait: code=%d out=%q err=%q", code, firstOut, firstErr)
	}
	var alertID string
	faceRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='go-affected'`, &alertID)
	wantAckLine := fmt.Sprintf("  ack: tillr watch ack %s --by 'foreground' --store %s\n", alertID, shellWord(s.Path()))
	wantTrackLine := fmt.Sprintf("  track: tillr watch track %s --item <item-id> --by 'foreground' --store %s\n", alertID, shellWord(s.Path()))
	if !strings.Contains(firstOut, wantAckLine+wantTrackLine) {
		t.Fatalf("wait output missing ack+track lines:\ngot:\n%s\nwant:\n%s", firstOut, wantAckLine+wantTrackLine)
	}
	through := cursor(t, firstOut)

	// Attach tracking item via tillr watch track.
	code, trackOut, trackErr := invoke("track", alertID, "--item", "item-slice-d", "--by", "operator", "--note", "slice D")
	if code != 0 || trackErr != "" || trackOut != fmt.Sprintf("alert %s tracked by item-slice-d\n", alertID) {
		t.Fatalf("track: code=%d out=%q err=%q", code, trackOut, trackErr)
	}

	// Update tracking item via track without --by (preserves acked_by="operator", updates item_id to item-slice-e, then back to item-slice-d).
	code, trackOut2, trackErr2 := invoke("track", alertID, "--item", "item-slice-e")
	if code != 0 || trackErr2 != "" || trackOut2 != fmt.Sprintf("alert %s tracked by item-slice-e\n", alertID) {
		t.Fatalf("track update: code=%d out=%q err=%q", code, trackOut2, trackErr2)
	}
	code, _, trackErr3 := invoke("track", alertID, "--item", "item-slice-d")
	if code != 0 || trackErr3 != "" {
		t.Fatalf("track restore: code=%d err=%q", code, trackErr3)
	}

	// Open a second alert; ack --item is refused, while track attaches item-slice-e.
	fact(t, s, "{"+`"item_id":"trunk-lag"`+"}")
	fact(t, s, "{"+`"item_id":"trunk-lag"`+"}")
	code, secondOut, secondErr := invoke("wait", "--rules", dir, "--timeout", "2s", "--seen-through", fmt.Sprint(through))
	if code != 0 || secondErr != "" {
		t.Fatalf("second wait: code=%d out=%q err=%q", code, secondOut, secondErr)
	}
	through2 := cursor(t, secondOut)
	var alertID2 string
	faceRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='trunk-lag'`, &alertID2)
	code, ackRefOut, ackRefErr := invoke("ack", alertID2, "--by", "operator", "--item", "item-slice-e", "--note", "slice E")
	if code != 1 || ackRefOut != "" || !strings.Contains(ackRefErr, "--item is not accepted on ack") {
		t.Fatalf("ack --item should refuse: code=%d out=%q err=%q", code, ackRefOut, ackRefErr)
	}
	code, track2Out, track2Err := invoke("track", alertID2, "--by", "operator", "--item", "item-slice-e", "--note", "slice E")
	if code != 0 || track2Err != "" || track2Out != fmt.Sprintf("alert %s tracked by item-slice-e\n", alertID2) {
		t.Fatalf("track second alert: code=%d out=%q err=%q", code, track2Out, track2Err)
	}

	// Start a live background wait (re-armed with --seen-through through2).
	type waitRes struct {
		code     int
		out, err string
	}
	waitDone := make(chan waitRes, 1)
	go func() {
		c, o, e := invoke("wait", "--rules", dir, "--seen-through", fmt.Sprint(through2))
		waitDone <- waitRes{c, o, e}
	}()
	awaitAttached(t, s)

	// Emit repeats for both subjects while their tracking items remain open!
	for range 3 {
		fact(t, s, `{"item_id":"go-affected"}`)
		fact(t, s, `{"item_id":"trunk-lag"}`)
	}
	// Wait until wait evaluates the repeats and raises both recorded observed counts; it MUST NOT exit!
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case r := <-waitDone:
			t.Fatalf("wait exited for tracked repeat while item is open: %+v", r)
		default:
		}
		var risen int
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(
				`SELECT COUNT(*) FROM alert_episodes WHERE alert_id IN (?, ?) AND observation_count >= 2`,
				alertID, alertID2,
			).Scan(&risen)
		}); err != nil {
			t.Fatal(err)
		}
		if risen == 2 {
			select {
			case r := <-waitDone:
				t.Fatalf("wait exited for tracked repeat while item is open: %+v", r)
			default:
			}
			break
		}
		select {
		case r := <-waitDone:
			t.Fatalf("wait exited for tracked repeat while item is open: %+v", r)
		case <-poll.C:
		}
	}
	poll.Stop()

	// Verify status text and JSON show observed count rising and item=...
	code, stText, stErr := invoke("status", "--rules", dir)
	if code != 0 || stErr != "" {
		t.Fatalf("status text: code=%d err=%q", code, stErr)
	}
	if !strings.Contains(stText, fmt.Sprintf(`alert %s rule=refusals subject="go-affected" state=acknowledged severity=warning`, alertID)) ||
		!strings.Contains(stText, `by="operator" item="item-slice-d"`) {
		t.Fatalf("unexpected status text:\n%s", stText)
	}
	code, stJSON, stJErr := invoke("status", "--rules", dir, "--json")
	if code != 0 || stJErr != "" {
		t.Fatalf("status json: code=%d err=%q", code, stJErr)
	}
	var parsedStatus struct {
		Alerts []struct {
			AlertID  string  `json:"alert_id"`
			Observed int64   `json:"observed"`
			By       *string `json:"by"`
			ItemID   *string `json:"item_id"`
		} `json:"alerts"`
	}
	if err := json.Unmarshal([]byte(stJSON), &parsedStatus); err != nil || len(parsedStatus.Alerts) != 2 {
		t.Fatalf("unmarshal status json: %v\n%s", err, stJSON)
	}
	for _, a := range parsedStatus.Alerts {
		if a.Observed < 2 || a.ItemID == nil || a.By == nil || *a.By != "operator" {
			t.Fatalf("unexpected alert json row: %+v", a)
		}
	}

	// Now item-slice-d lands while the earlier go-affected events are still in the window.
	// An unrelated event must NOT wake wait because no new matching event occurred after landing.
	seedCLIItems(t, s, map[string]string{"item-slice-d": "landed"})
	fact(t, s, `{"item_id":"unrelated-single"}`)
	select {
	case r := <-waitDone:
		t.Fatalf("wait exited after tracked item landed with no new matching event: %+v", r)
	case <-time.After(150 * time.Millisecond):
	}

	// Recording a new matching event after item-slice-d landed wakes wait with the persistence notice.
	fact(t, s, `{"item_id":"go-affected"}`)

	var woke waitRes
	select {
	case woke = <-waitDone:
	case <-time.After(hangGuard):
		t.Fatal("wait did not wake when matching event occurred after tracked item landed")
	}
	if woke.code != 0 || woke.err != "" {
		t.Fatalf("wake on landed fix: %+v", woke)
	}
	if !strings.Contains(woke.out, "tracked fix item-slice-d landed and the condition persists: go-affected has ") {
		t.Fatalf("wake output missing tracked-fix-landed prefix:\n%s", woke.out)
	}
	if strings.Contains(woke.out, "trunk-lag") {
		t.Fatalf("wake output included trunk-lag whose item-slice-e is still open:\n%s", woke.out)
	}
	through3 := cursor(t, woke.out)

	// Once confirmed, subsequent repeats on go-affected do NOT wake wait again!
	fact(t, s, `{"item_id":"go-affected"}`)
	code, quietOut, quietErr := invoke("wait", "--rules", dir, "--timeout", "40ms", "--seen-through", fmt.Sprint(through3))
	if code != 2 || quietErr != "" || strings.Contains(quietOut, "TILLR_WATCH_NOTICE") {
		t.Fatalf("expected quiet timeout after confirming persistence reopen: code=%d out=%q err=%q", code, quietOut, quietErr)
	}
}

func TestWatchTrackRefusalsAndPreMigrationStorePreservation(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "0")
	ctx := t.Context()

	t.Run("migrated_store_refusals_and_silenced_to_tracked", func(t *testing.T) {
		s, dir := fixture(t, false)
		seedCLIItems(t, s, map[string]string{
			"item-open":   "in-queue",
			"item-landed": "landed",
			"item-closed": "closed",
			"item-else":   "resolved-elsewhere",
		})
		if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", dir}, io.Discard, io.Discard); code != 0 {
			t.Fatal("init eval failed")
		}
		fact(t, s, `{"item_id":"one"}`)
		fact(t, s, `{"item_id":"one"}`)
		if code := run(ctx, []string{"eval", "--once", "--store", s.Path(), "--rules", dir}, io.Discard, io.Discard); code != 0 {
			t.Fatal("eval failed")
		}
		var id string
		faceRead(t, s, `SELECT alert_id FROM alert_episodes`, &id)

		refuse := func(wantSubstr string, args ...string) {
			t.Helper()
			before, err := s.EventHeadSeq()
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			code := run(ctx, append([]string{args[0], "--store", s.Path()}, args[1:]...), &out, &stderr)
			if code != 1 || out.Len() != 0 || !strings.Contains(stderr.String(), wantSubstr) {
				t.Fatalf("%v: code=%d out=%q stderr=%q, want %q", args, code, &out, &stderr, wantSubstr)
			}
			after, err := s.EventHeadSeq()
			if err != nil || after != before {
				t.Fatalf("%v appended an event (%d -> %d)", args, before, after)
			}
		}

		refuse("--item is required", "track", id)
		refuse("--item is required", "track", id, "--item=")
		refuse("--by must not be empty", "track", id, "--item=item-open", "--by=")
		refuse("--item is not accepted on ack", "ack", id, "--by=alice", "--item=")
		refuse("item item-missing not found", "track", id, "--item=item-missing")
		refuse("item item-landed is landed; tracking requires an open item", "track", id, "--item=item-landed")
		refuse("item item-closed is closed; tracking requires an open item", "track", id, "--item=item-closed")
		refuse("--item is not accepted on ack", "ack", id, "--by=alice", "--item=item-else")
		refuse("item item-else is resolved-elsewhere; tracking requires an open item", "track", id, "--item=item-else")

		// Silence the alert first, then attach tracking item via track (clears silence fields!).
		if code := run(ctx, []string{"silence", "--store", s.Path(), id, "--until=1h"}, io.Discard, io.Discard); code != 0 {
			t.Fatal("silence failed")
		}
		var trackOut bytes.Buffer
		if code := run(ctx, []string{"track", "--store", s.Path(), id, "--item=item-open"}, &trackOut, io.Discard); code != 0 {
			t.Fatal("track on silenced failed")
		}
		var st, tracked string
		var silUntil *int64
		var silFP *string
		faceRead(t, s, `SELECT state, tracking_item_id, silence_until_ms, silence_on_fingerprint FROM alert_episodes WHERE alert_id='`+id+`'`, &st, &tracked, &silUntil, &silFP)
		if st != "acknowledged" || tracked != "item-open" || silUntil != nil || silFP != nil {
			t.Fatalf("after track on silenced: state=%q tracked=%q until=%v fp=%v", st, tracked, silUntil, silFP)
		}

		// Refusals while acknowledged (tracked by item-open) with NULL acked_by:
		refuse("alert "+id+" is acknowledged (tracked by item-open)", "ack", id, "--by=bob")
		refuse("alert "+id+" is acknowledged (tracked by item-open): cannot silence an item-tracked alert", "silence", id, "--until=1h")

		// Resolve remains allowed; once resolved, track is refused.
		if code := run(ctx, []string{"resolve", "--store", s.Path(), id, "--reason=manual check"}, io.Discard, io.Discard); code != 0 {
			t.Fatal("resolve failed")
		}
		var resEvItem string
		faceRead(t, s, `SELECT json_extract(payload,'$.item_id') FROM events WHERE kind='watch.alert.resolved'`, &resEvItem)
		if resEvItem != "item-open" {
			t.Fatalf("manual resolve on tracked episode missing item_id: %q", resEvItem)
		}
		refuse("alert "+id+" is resolved", "track", id, "--item=item-open")
	})

	t.Run("pre_migration_delete_journal_store", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "legacy.db")
		s, err := store.Open(ctx, path, true)
		if err != nil {
			t.Fatal(err)
		}
		// Rebuild alert_episodes WITHOUT tracking_item_id or tracked_lag_pair to simulate a pre-D-TW67 store,
		// and clear schema.applied.* so OpenMigrated knows to migrate when accepted.
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			for _, q := range []string{
				`DROP TABLE alert_episodes`,
				`CREATE TABLE alert_episodes (
					alert_id TEXT PRIMARY KEY,
					rule_id TEXT NOT NULL,
					subject_key TEXT NOT NULL,
					episode_num INTEGER NOT NULL,
					state TEXT NOT NULL CHECK (state IN ('open', 'acknowledged', 'silenced', 'resolved')),
					severity TEXT NOT NULL,
					condition_fingerprint TEXT NOT NULL,
					opened_seq INTEGER NOT NULL,
					opened_at INTEGER NOT NULL,
					last_observed_seq INTEGER NOT NULL,
					last_observed_at INTEGER NOT NULL,
					observation_count INTEGER NOT NULL DEFAULT 1,
					acked_by TEXT,
					acked_note TEXT,
					acked_at INTEGER,
					silence_until_ms INTEGER,
					silence_on_fingerprint TEXT,
					active_triage_run_id TEXT,
					resolved_seq INTEGER,
					resolved_at INTEGER,
					resolved_reason TEXT,
					bundle_json TEXT NOT NULL
				)`,
				`INSERT INTO alert_episodes(alert_id,rule_id,subject_key,episode_num,state,severity,condition_fingerprint,opened_seq,opened_at,last_observed_seq,last_observed_at,bundle_json)
					VALUES ('alrt-legacy','refusals','one',1,'open','warning','',1,1000,1,1000,'{}')`,
				`INSERT INTO items (item_id, title, state, updated_at) VALUES ('item-open', 'item-open', 'in-queue', 1000), ('item-closed', 'item-closed', 'closed', 1000)`,
				`DELETE FROM meta WHERE key LIKE 'schema.applied.%'`,
			} {
				if _, err := tx.Exec(q); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}

		rulesDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(rulesDir, "r.toml"), []byte(testRule), 0600); err != nil {
			t.Fatal(err)
		}

		journal := func(query string) string {
			t.Helper()
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var mode string
			if err := db.QueryRowContext(context.Background(), query).Scan(&mode); err != nil {
				t.Fatal(err)
			}
			return mode
		}
		if mode := journal("PRAGMA journal_mode=DELETE"); mode != "delete" {
			t.Fatalf("journal setup: %q", mode)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		// 1. Read-only status on unmigrated store works without writing or changing journal_mode.
		var stOut, stErr bytes.Buffer
		if code := run(ctx, []string{"status", "--store", path, "--rules", rulesDir}, &stOut, &stErr); code != 0 || stErr.Len() != 0 || !strings.Contains(stOut.String(), "alert alrt-legacy") {
			t.Fatalf("legacy status: code=%d out=%q err=%q", code, &stOut, &stErr)
		}
		afterStatus, _ := os.ReadFile(path)
		if !bytes.Equal(before, afterStatus) || journal("PRAGMA journal_mode") != "delete" {
			t.Fatal("status mutated legacy unmigrated store")
		}

		// 2. Refusals on unmigrated DELETE-mode store preserve journal_mode=delete and exact bytes!
		for _, args := range [][]string{
			{"track", "missing", "--item", "item-open"},
			{"track", "alrt-legacy", "--item", "item-missing"},
			{"track", "alrt-legacy", "--item", "item-closed"},
			{"track", "alrt-legacy", "--item="},
			{"ack", "alrt-legacy", "--by", "op", "--item", "item-closed"},
			{"ack", "alrt-legacy", "--by", "op", "--item", "item-open"},
			{"ack", "alrt-legacy", "--by="},
			{"ack", "missing", "--by", "op"},
		} {
			var out, stderr bytes.Buffer
			if code := run(ctx, append([]string{args[0], "--store", path}, args[1:]...), &out, &stderr); code != 1 || out.Len() != 0 {
				t.Fatalf("expected refusal for %v: code=%d out=%q err=%q", args, code, &out, &stderr)
			}
			cur, _ := os.ReadFile(path)
			if !bytes.Equal(before, cur) || journal("PRAGMA journal_mode") != "delete" {
				t.Fatalf("refusal %v mutated legacy store bytes or journal_mode", args)
			}
		}

		// 3. Accepted ack on a copy of the unmigrated DELETE-mode store migrates via store.OpenMigrated and records watermarks!
		ackPath := filepath.Join(t.TempDir(), "legacy-ack.db")
		if err := os.WriteFile(ackPath, before, 0600); err != nil {
			t.Fatal(err)
		}
		var ackLegacyOut, ackLegacyErr bytes.Buffer
		if code := run(ctx, []string{"ack", "--store", ackPath, "alrt-legacy", "--by", "op"}, &ackLegacyOut, &ackLegacyErr); code != 0 || ackLegacyErr.Len() != 0 || ackLegacyOut.String() != "alert alrt-legacy acknowledged\n" {
			t.Fatalf("accepted legacy ack: code=%d out=%q err=%q", code, &ackLegacyOut, &ackLegacyErr)
		}
		ackReopened, err := store.OpenRead(ctx, ackPath)
		if err != nil {
			t.Fatal(err)
		}
		defer ackReopened.Close()
		var ackLegacyState string
		var ackLegacySeq sql.NullInt64
		var ackLegacyFP sql.NullString
		faceRead(t, ackReopened, `SELECT state, acked_seq, acked_fingerprint FROM alert_episodes WHERE alert_id='alrt-legacy'`, &ackLegacyState, &ackLegacySeq, &ackLegacyFP)
		if ackLegacyState != "acknowledged" || !ackLegacySeq.Valid || ackLegacySeq.Int64 != 1 || !ackLegacyFP.Valid {
			t.Fatalf("migrated ack row: state=%q seq=%+v fp=%+v", ackLegacyState, ackLegacySeq, ackLegacyFP)
		}

		// 4. Accepted track migrates via store.OpenMigrated and commits tracking_item_id!
		var accOut, accErr bytes.Buffer
		if code := run(ctx, []string{"track", "--store", path, "alrt-legacy", "--item", "item-open", "--by", "op"}, &accOut, &accErr); code != 0 || accErr.Len() != 0 || accOut.String() != "alert alrt-legacy tracked by item-open\n" {
			t.Fatalf("accepted legacy track: code=%d out=%q err=%q", code, &accOut, &accErr)
		}
		reopened, err := store.OpenRead(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		var gotItem, gotState string
		faceRead(t, reopened, `SELECT state, tracking_item_id FROM alert_episodes WHERE alert_id='alrt-legacy'`, &gotState, &gotItem)
		if gotState != "acknowledged" || gotItem != "item-open" {
			t.Fatalf("migrated row: state=%q item=%q", gotState, gotItem)
		}
	})
}
