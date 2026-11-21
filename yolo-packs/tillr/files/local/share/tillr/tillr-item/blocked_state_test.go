package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func blockState(t *testing.T, db, waiter, reason string) (int, []byte) {
	t.Helper()
	return run(t, "state", "--store", db, "--item", "item_042", "--state", "blocked", "--waiter", waiter, "--reason", reason, "--now", "2026-11-16T00:00:00Z")
}

func blockedPayload(t *testing.T, db string) map[string]any {
	t.Helper()
	code, out := run(t, "show", "--store", db, "--item", "item_042")
	if code != 0 {
		t.Fatalf("show: %d: %s", code, out)
	}
	var env struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	return env.Payload
}

func TestBlockedStateRequiresWaiterAndReason(t *testing.T) {
	db := filepath.Join(scratch(t, "blocked-required"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "work"))); code != 0 {
		t.Fatalf("apply: %s", out)
	}
	for _, args := range [][]string{nil, {"--waiter", "owner"}, {"--reason", "decision"}, {"--waiter", " ", "--reason", "decision"}, {"--waiter", "owner", "--reason", " "}} {
		code, out := run(t, append([]string{"state", "--store", db, "--item", "item_042", "--state", "blocked", "--now", "2026-11-16T00:00:00Z"}, args...)...)
		if code != 1 || decode(t, out).Payload.Rule != "blocked-waiter-missing" {
			t.Fatalf("missing metadata %v: %d: %s", args, code, out)
		}
	}
	if got := stateRow(t, db, "item_042"); got != "not-started" {
		t.Fatalf("refusal changed state: %s", got)
	}
}

func TestBlockedStateWaiterVocabulary(t *testing.T) {
	for _, waiter := range []string{"owner", "design", "plan", "item:item_043", "build", "item:absent", "item:"} {
		t.Run(waiter, func(t *testing.T) {
			db := filepath.Join(scratch(t, "blocked-waiter"), "board.sqlite3")
			for _, id := range []string{"item_042", "item_043"} {
				if code, out := apply(t, db, envelope(t, "item.record", id, base(id, "work"))); code != 0 {
					t.Fatalf("apply: %s", out)
				}
			}
			code, out := blockState(t, db, waiter, "needs a decision")
			valid := waiter == "owner" || waiter == "design" || waiter == "plan" || waiter == "item:item_043"
			if valid && code != 0 || !valid && code != 1 {
				t.Fatalf("waiter %s: %d: %s", waiter, code, out)
			}
		})
	}
}

func TestBlockedStateTransitionsAndClearing(t *testing.T) {
	for _, from := range []string{"not-started", "in-queue", "blocked", "in-progress", "landed", "resolved-elsewhere", "closed", "abandoned", "deleted"} {
		for _, to := range []string{"not-started", "in-queue"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				db := filepath.Join(scratch(t, "blocked-transitions"), "board.sqlite3")
				s, err := store.Open(context.Background(), db, true)
				if err != nil {
					t.Fatal(err)
				}
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES ('item_042','work',?,0)`, from)
					return err
				})
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				code, out := blockState(t, db, "design", "first reason")
				if from != "not-started" && from != "in-queue" && from != "blocked" {
					if code != 1 {
						t.Fatalf("illegal move from %s: %d: %s", from, code, out)
					}
					return
				}
				if code != 0 {
					t.Fatalf("block: %d: %s", code, out)
				}
				p := blockedPayload(t, db)
				if p["underlying_state"] != from {
					t.Fatalf("underlying state: %+v", p)
				}
				n := eventsOfKind(t, db, "item.update")
				if code, out := blockState(t, db, "design", "first reason"); code != 0 {
					t.Fatalf("repeat: %s", out)
				}
				if got := eventsOfKind(t, db, "item.update"); got != n {
					t.Fatalf("repeat appended event")
				}
				if code, out := blockState(t, db, "owner", "changed reason"); code != 0 {
					t.Fatalf("reblock: %s", out)
				}
				p = blockedPayload(t, db)
				waiting := p["waiting_on"].(map[string]any)
				if waiting["waiter"] != "owner" || waiting["reason"] != "changed reason" || p["underlying_state"] != from {
					t.Fatalf("reblock: %+v", p)
				}
				if code, out := state(t, db, "item_042", to, "2026-11-16T00:01:00Z"); code != 0 {
					t.Fatalf("unblock: %s", out)
				}
				p = blockedPayload(t, db)
				if p["waiting_on"] != nil || p["underlying_state"] != nil {
					t.Fatalf("metadata not cleared: %+v", p)
				}
			})
		}
	}
}

func TestBlockedStateLegacyStoreReadableBeforeAndAfterMigration(t *testing.T) {
	db := filepath.Join(scratch(t, "blocked-legacy"), "board.sqlite3")
	seeded := base("item_042", "old blocked item")
	seeded.State = "blocked"
	if code, out := apply(t, db, envelope(t, "item.record", "rec", seeded)); code != 0 {
		t.Fatalf("apply: %s", out)
	}
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"waiting_on", "underlying_state"} {
		var count int
		if err := raw.QueryRow(`SELECT count(*) FROM pragma_table_info('items') WHERE name = ?`, col).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			if _, err := raw.Exec(`ALTER TABLE items DROP COLUMN ` + col); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw.Close()
	for i := 0; i < 2; i++ {
		p := blockedPayload(t, db)
		b, _ := json.Marshal(p)
		if !strings.Contains(string(b), "waiter not recorded") {
			t.Fatalf("legacy absence hidden: %s", b)
		}
		code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "blocked", "--now", "2026-11-16T00:00:00Z")
		if code != 1 || decode(t, out).Payload.Rule != "blocked-waiter-missing" {
			t.Fatalf("legacy new move: %d: %s", code, out)
		}
	}
}

func countStoredEvents(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM events").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func seedBlockedRunTables(t *testing.T, db string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	const ddl = `
CREATE TABLE IF NOT EXISTS lanes (
  lane_id      TEXT PRIMARY KEY,
  state        TEXT NOT NULL,
  run_id       TEXT,
  leased_until INTEGER
);
CREATE TABLE IF NOT EXISTS runs (
  run_id        TEXT PRIMARY KEY,
  item_id       TEXT,
  lane_id       TEXT NOT NULL REFERENCES lanes(lane_id),
  backend       TEXT NOT NULL,
  route         TEXT,
  harness       TEXT,
  state         TEXT NOT NULL,
  exit_code     INTEGER,
  end_cause     TEXT,
  started_at    INTEGER NOT NULL,
  finished_at   INTEGER NOT NULL,
  artifact_refs TEXT NOT NULL DEFAULT '[]',
  event_seq     INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS retries (
  retry_id          TEXT PRIMARY KEY,
  item_id           TEXT NOT NULL,
  supersedes_run_id TEXT NOT NULL,
  requested_by      TEXT NOT NULL,
  reason            TEXT NOT NULL,
  requested_at      INTEGER NOT NULL
);
INSERT OR IGNORE INTO lanes (lane_id, state) VALUES ('lane-1', 'idle');
`
	if _, err := raw.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBlockedStateRefusesDirectQueueMoveWhenRunIsUnanswered(t *testing.T) {
	t.Run("unanswered worker run prints exact retry command", func(t *testing.T) {
		db := filepath.Join(scratch(t, "blocked-unanswered-run"), "board.sqlite3")
		if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "work"))); code != 0 {
			t.Fatalf("apply: %s", out)
		}
		if code, out := blockState(t, db, "owner", "worker reported an obstacle"); code != 0 {
			t.Fatalf("block: %d: %s", code, out)
		}
		raw := seedBlockedRunTables(t, db)
		if _, err := raw.Exec(
			`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, end_cause, started_at, finished_at)
			 VALUES ('run-item_042-a1-n1', 'item_042', 'lane-1', 'pi', 'exited', 0, 'normal-exit', 1000, 2000)`,
		); err != nil {
			t.Fatal(err)
		}
		before := countStoredEvents(t, raw)
		wantCmd := "tillr drain retry --item item_042 --run run-item_042-a1-n1 --by <who> --why <reason>"
		for _, target := range []string{"in-queue", "not-started"} {
			code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", target, "--now", "2026-11-16T01:00:00Z")
			if code != 1 {
				t.Fatalf("state %s exited %d, want 1: %s", target, code, out)
			}
			got := decode(t, out)
			if got.Payload.Rule != "blocked-use-retry" || !strings.Contains(got.Payload.Reason, wantCmd) {
				t.Fatalf("state %s refusal = %+v, want rule blocked-use-retry with %q", target, got.Payload, wantCmd)
			}
			// Literal angle brackets must appear in stdout so operators can copy-paste without \u003c escapes.
			if !strings.Contains(string(out), wantCmd) {
				t.Fatalf("stdout escaped retry command: %s", out)
			}
		}
		if got := stateRow(t, db, "item_042"); got != "blocked" {
			t.Fatalf("refusal mutated state to %s", got)
		}
		if got := countStoredEvents(t, raw); got != before {
			t.Fatalf("refusal appended %d events", got-before)
		}

		// Once tillr drain retry records the supersession row, moving to in-queue succeeds.
		if _, err := raw.Exec(
			`INSERT INTO retries (retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at)
			 VALUES ('retry-item_042-a1', 'item_042', 'run-item_042-a1-n1', 'matt', 'answered obstacle', 3000)`,
		); err != nil {
			t.Fatal(err)
		}
		code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "in-queue", "--now", "2026-11-16T01:05:00Z")
		if code != 0 {
			t.Fatalf("superseded move failed (%d): %s", code, out)
		}
		if got := stateRow(t, db, "item_042"); got != "in-queue" {
			t.Fatalf("state after superseded move = %s, want in-queue", got)
		}
	})

	t.Run("acknowledge flags match gate-failed and unjudged runs", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			runID    string
			state    string
			exitCode int
			gateEvt  bool
			wantCmd  string
		}{
			{
				name:     "gate-failed",
				runID:    "run-judged-1",
				state:    "exited",
				exitCode: 0,
				gateEvt:  true,
				wantCmd:  "tillr drain retry --item item_042 --run run-judged-1 --by <who> --why <reason> --acknowledge gate-failed",
			},
			{
				name:     "unjudged-timed-out",
				runID:    "run-timeout-1",
				state:    "timed-out",
				exitCode: 124,
				wantCmd:  "tillr drain retry --item item_042 --run run-timeout-1 --by <who> --why <reason> --acknowledge unjudged",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				db := filepath.Join(scratch(t, "blocked-ack-"+tc.name), "board.sqlite3")
				if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "work"))); code != 0 {
					t.Fatalf("apply: %s", out)
				}
				if code, out := blockState(t, db, "owner", "held"); code != 0 {
					t.Fatalf("block: %d: %s", code, out)
				}
				raw := seedBlockedRunTables(t, db)
				if _, err := raw.Exec(
					`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at)
					 VALUES (?, 'item_042', 'lane-1', 'pi', ?, ?, 1000, 2000)`,
					tc.runID, tc.state, tc.exitCode,
				); err != nil {
					t.Fatal(err)
				}
				if tc.gateEvt {
					if _, err := raw.Exec(
						`INSERT INTO events (ts, kind, object_type, object_id, payload)
						 VALUES (2500, 'gate.verdict', 'run', ?, '{}')`,
						tc.runID,
					); err != nil {
						t.Fatal(err)
					}
				}
				code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "in-queue", "--now", "2026-11-16T01:00:00Z")
				if code != 1 {
					t.Fatalf("state exited %d, want 1: %s", code, out)
				}
				got := decode(t, out)
				if got.Payload.Rule != "blocked-use-retry" || !strings.Contains(got.Payload.Reason, tc.wantCmd) {
					t.Fatalf("refusal = %+v, want %q", got.Payload, tc.wantCmd)
				}
			})
		}
	})

	t.Run("owner-held design conflict prints design retry command", func(t *testing.T) {
		db := filepath.Join(scratch(t, "blocked-design-conflict"), "board.sqlite3")
		if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "work"))); code != 0 {
			t.Fatalf("apply: %s", out)
		}
		if code, out := blockState(t, db, "owner", "design escalated"); code != 0 {
			t.Fatalf("block: %d: %s", code, out)
		}
		raw := seedBlockedRunTables(t, db)
		if _, err := raw.Exec(
			`INSERT INTO events (ts, kind, object_type, object_id, payload)
			 VALUES (2500, 'item.conflict', 'item', 'item_042',
			         '{"conflict_id":"conflict-item_042-1","item_id":"item_042","run_id":"run-worker-1","design_run_id":"run-design-1","status":"owner","waiting_on":"owner","text":"design escalated"}')`,
		); err != nil {
			t.Fatal(err)
		}
		wantCmd := "tillr drain retry --item item_042 --run run-design-1 --by <who> --why <reason>"
		code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "in-queue", "--now", "2026-11-16T01:00:00Z")
		if code != 1 {
			t.Fatalf("state exited %d, want 1: %s", code, out)
		}
		got := decode(t, out)
		if got.Payload.Rule != "blocked-use-retry" || !strings.Contains(got.Payload.Reason, wantCmd) {
			t.Fatalf("refusal = %+v, want %q", got.Payload, wantCmd)
		}
	})

	t.Run("acquitted trunk-already-red run releases cleanly without retry", func(t *testing.T) {
		db := filepath.Join(scratch(t, "blocked-acquitted"), "board.sqlite3")
		if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "work"))); code != 0 {
			t.Fatalf("apply: %s", out)
		}
		if code, out := apply(t, db, envelope(t, "item.record", "rec-f", base("item_043", "fixer"))); code != 0 {
			t.Fatalf("apply fixer: %s", out)
		}
		if code, out := blockState(t, db, "item:item_043", "trunk-already-red: unit"); code != 0 {
			t.Fatalf("block: %d: %s", code, out)
		}
		raw := seedBlockedRunTables(t, db)
		if _, err := raw.Exec(
			`INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at)
			 VALUES ('run-acq-1', 'item_042', 'lane-1', 'pi', 'exited', 0, 1000, 2000);
			 INSERT INTO events (ts, kind, object_type, object_id, payload)
			 VALUES (2500, 'drain.refusal', 'run', 'run-acq-1', '{"step":"trunk-already-red"}')`,
		); err != nil {
			t.Fatal(err)
		}
		code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "in-queue", "--now", "2026-11-16T01:00:00Z")
		if code != 0 {
			t.Fatalf("acquitted state move failed (%d): %s", code, out)
		}
		if got := stateRow(t, db, "item_042"); got != "in-queue" {
			t.Fatalf("state = %s, want in-queue", got)
		}
	})
}
