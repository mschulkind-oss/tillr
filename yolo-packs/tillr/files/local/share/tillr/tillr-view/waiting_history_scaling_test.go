package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr/store"
)

func seedWaitingHistoryStore(t *testing.T, finishedCount int) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("waiting-%d.sqlite3", finishedCount))
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("init fixture: %v", err)
	}
	defer s.Close()

	baseAt := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, ddl := range []string{
			`CREATE TABLE IF NOT EXISTS lanes (lane_id TEXT PRIMARY KEY, state TEXT NOT NULL, run_id TEXT, leased_until INTEGER)`,
			`CREATE TABLE IF NOT EXISTS items (item_id TEXT PRIMARY KEY, title TEXT NOT NULL, body_ref TEXT, state TEXT NOT NULL, design_doc TEXT, updated_at INTEGER NOT NULL, event_seq INTEGER NOT NULL DEFAULT 0, allowed_paths TEXT NOT NULL DEFAULT '', rank TEXT NOT NULL DEFAULT '', waiting_on TEXT NOT NULL DEFAULT '', close_reason TEXT NOT NULL DEFAULT '')`,
			`CREATE TABLE IF NOT EXISTS item_criteria (item_id TEXT NOT NULL, criterion_id TEXT NOT NULL, text TEXT NOT NULL, state TEXT NOT NULL, claim_class TEXT NOT NULL DEFAULT 'verification-only', PRIMARY KEY (item_id, criterion_id))`,
			`CREATE TABLE IF NOT EXISTS runs (run_id TEXT PRIMARY KEY, item_id TEXT, lane_id TEXT NOT NULL, backend TEXT NOT NULL, route TEXT, harness TEXT, seat TEXT, workflow TEXT, node TEXT, state TEXT NOT NULL, exit_code INTEGER, end_cause TEXT, started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL, artifact_refs TEXT NOT NULL DEFAULT '[]', event_seq INTEGER NOT NULL DEFAULT 0, extension_set TEXT)`,
			`CREATE TABLE IF NOT EXISTS candidates (candidate_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, base_ref TEXT NOT NULL, base_sha TEXT NOT NULL, head_sha TEXT NOT NULL, tree_sha TEXT NOT NULL, state TEXT NOT NULL, enqueued_at INTEGER NOT NULL, event_seq INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE IF NOT EXISTS landing_attempts (attempt_id TEXT PRIMARY KEY, candidate_id TEXT NOT NULL, verdict_id TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', trunk_before TEXT NOT NULL, trunk_after TEXT NOT NULL, gated_tree TEXT NOT NULL DEFAULT '', landed_tree TEXT NOT NULL DEFAULT '', carried INTEGER NOT NULL DEFAULT 0, event_seq INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE IF NOT EXISTS landings (landing_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, run_id TEXT NOT NULL, verdict_id TEXT NOT NULL, result TEXT NOT NULL, base_sha TEXT NOT NULL, head_sha TEXT NOT NULL, trunk_before TEXT NOT NULL, trunk_after TEXT NOT NULL, event_seq INTEGER NOT NULL DEFAULT 0)`,
			`CREATE TABLE IF NOT EXISTS item_splits (parent_id TEXT PRIMARY KEY, statement BLOB NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS dispatches (decision_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, lane_id TEXT, run_id TEXT, seat TEXT, action TEXT NOT NULL, reason TEXT NOT NULL, deferred_by TEXT, dispatched_at INTEGER NOT NULL, pass_id TEXT)`,
			`CREATE TABLE IF NOT EXISTS drain_sessions (session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER, lanes TEXT)`,
			`CREATE TABLE IF NOT EXISTS graph_revisions (revision_id INTEGER PRIMARY KEY AUTOINCREMENT, item_id TEXT NOT NULL, attempt INTEGER NOT NULL, proposing_run TEXT NOT NULL UNIQUE, proposing_node TEXT NOT NULL, target TEXT NOT NULL, workflow TEXT NOT NULL, rule TEXT NOT NULL, detail TEXT NOT NULL, before_graph TEXT NOT NULL, after_graph TEXT NOT NULL, before_nodes INTEGER NOT NULL, after_nodes INTEGER NOT NULL, recorded_at INTEGER NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS items_state ON items (state, rank, item_id)`,
			`CREATE INDEX IF NOT EXISTS runs_item_started ON runs (item_id, started_at)`,
			`CREATE INDEX IF NOT EXISTS dispatches_item ON dispatches (item_id)`,
			`CREATE INDEX IF NOT EXISTS candidates_item ON candidates (item_id)`,
			`CREATE INDEX IF NOT EXISTS landing_attempts_candidate ON landing_attempts (candidate_id)`,
			`CREATE INDEX IF NOT EXISTS graph_revisions_item ON graph_revisions (item_id, revision_id)`,
			`CREATE INDEX IF NOT EXISTS events_drain_started_item ON events(json_extract(payload, '$.decision.item_id')) WHERE kind = 'drain.started' AND json_valid(payload)`,
			`CREATE INDEX IF NOT EXISTS events_node_advanced_item ON events(json_extract(payload, '$.decision.item_id')) WHERE kind = 'drain.node-advanced' AND json_valid(payload)`,
			`CREATE INDEX IF NOT EXISTS events_graph_changed_item ON events(json_extract(payload, '$.decision.item_id')) WHERE kind = 'drain.graph-changed' AND json_valid(payload)`,
			`CREATE INDEX IF NOT EXISTS events_run_allocate_run ON events(json_extract(payload, '$.run_id')) WHERE kind = 'run.allocate' AND json_valid(payload)`,
		} {
			if _, err := tx.Exec(ddl); err != nil {
				return err
			}
		}
		for _, lane := range []string{"lane-1", "lane-2"} {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES (?, 'idle')`, lane); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(
			`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, lanes) VALUES ('sess-1', ?, ?, ?, '["lane-1","lane-2"]')`,
			baseAt.UnixMilli(), baseAt.UnixMilli(), baseAt.Add(time.Minute).UnixMilli(),
		); err != nil {
			return err
		}

		for i := 0; i < finishedCount; i++ {
			itemID := fmt.Sprintf("item-done-%04d", i)
			runID := fmt.Sprintf("run-done-%04d", i)
			candID := fmt.Sprintf("cand-done-%04d", i)
			attID := fmt.Sprintf("att-done-%04d", i)
			laneID := fmt.Sprintf("item-%s", itemID)
			state := "landed"
			closeReason := ""
			if i%5 == 0 {
				state = "closed"
				closeReason = "superseded"
			}
			at := baseAt.Add(time.Duration(i) * time.Minute)
			if _, err := tx.Exec(
				`INSERT INTO items(item_id, title, state, updated_at, close_reason) VALUES (?, ?, ?, ?, ?)`,
				itemID, "Finished "+itemID, state, at.UnixMilli(), closeReason,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at) VALUES (?, ?, ?, 'fixture', 'exited', 0, ?, ?)`,
				runID, itemID, laneID, at.UnixMilli(), at.Add(30*time.Second).UnixMilli(),
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO candidates(candidate_id, item_id, base_ref, base_sha, head_sha, tree_sha, state, enqueued_at) VALUES (?, ?, 'main', 'aaa', 'bbb', 'ccc', 'landed', ?)`,
				candID, itemID, at.UnixMilli(),
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO landing_attempts(attempt_id, candidate_id, verdict_id, result, trunk_before, trunk_after) VALUES (?, ?, 'v-1', 'landed', 'aaa', 'bbb')`,
				attID, candID,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO landings(landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after) VALUES (?, ?, ?, 'v-1', 'landed', 'aaa', 'bbb', 'aaa', 'bbb')`,
				"land-"+runID, itemID, runID,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO item_splits(parent_id, statement) VALUES (?, '{"parts":[]}')`,
				itemID,
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO dispatches(decision_id, item_id, lane_id, run_id, action, reason, dispatched_at) VALUES (?, ?, ?, ?, 'dispatch', 'ready', ?)`,
				"dec-"+runID, itemID, laneID, runID, at.UnixMilli(),
			); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO graph_revisions(item_id, attempt, proposing_run, proposing_node, target, workflow, rule, detail, before_graph, after_graph, before_nodes, after_nodes, recorded_at)
				 VALUES (?, 1, ?, 'plan', 'item', 'wf', 'rule', 'detail', '{}', '{}', 1, 2, ?)`,
				itemID, runID, at.UnixMilli(),
			); err != nil {
				return err
			}

			insertEv := func(kind, objType, objID string, at time.Time, payload string) error {
				_, err := tx.Exec(
					`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, ?, ?, ?, ?)`,
					at.UnixMilli(), kind, objType, objID, payload,
				)
				return err
			}
			allocPayload, _ := json.Marshal(map[string]string{
				"run_id": runID, "item_id": itemID, "lane_id": laneID, "backend": "fixture", "started_at": at.Format(time.RFC3339),
			})
			if err := insertEv(allocateKind, "run", runID, at, string(allocPayload)); err != nil {
				return err
			}
			if err := insertEv("item.conflict", "item", itemID, at,
				fmt.Sprintf(`{"conflict_id":"c-%d","item_id":%q,"run_id":%q,"text":"done","waiting_on":"build","status":"answered","answer_head":"abc"}`, i, itemID, runID),
			); err != nil {
				return err
			}
			if err := insertEv(obstacles.HeldKind, "item", itemID, at,
				fmt.Sprintf(`{"item_id":%q,"run_id":%q,"kind":"owner"}`, itemID, runID),
			); err != nil {
				return err
			}
			if err := insertEv("drain.item-fault", "item", itemID, at,
				fmt.Sprintf(`{"item_id":%q,"run_id":%q,"reason":"stage-failed","detail":"old"}`, itemID, runID),
			); err != nil {
				return err
			}
			if err := insertEv("drain.item-fault-cleared", "item", itemID, at.Add(time.Second),
				fmt.Sprintf(`{"item_id":%q,"reason":"stage-failed"}`, itemID),
			); err != nil {
				return err
			}
			if err := insertEv("drain.lane-refused", "lane", laneID, at,
				fmt.Sprintf(`{"lane_id":%q,"reason":"lane-holds-unlanded-change","detail":"old","remedy":"reset"}`, laneID),
			); err != nil {
				return err
			}
			if err := insertEv(preserved.Kind, "run", runID, at,
				fmt.Sprintf(`{"item_id":%q,"run_id":%q,"lane_id":%q,"ref":"refs/tillr/preserved/%s","repository":"/repo","reason":"stopped","summary":"1 file"}`, itemID, runID, laneID, runID),
			); err != nil {
				return err
			}
			if err := insertEv("drain.started", "run", runID, at,
				fmt.Sprintf(`{"decision":{"item_id":%q,"lane_id":%q,"run_request":{"run_id":%q,"backend":"fixture"}}}`, itemID, laneID, runID),
			); err != nil {
				return err
			}
			if err := insertEv("drain.examined", "run", runID, at.Add(time.Second),
				fmt.Sprintf(`{"item_id":%q,"run_id":%q}`, itemID, runID),
			); err != nil {
				return err
			}
		}

		openAt := baseAt.Add(24 * time.Hour)
		if _, err := tx.Exec(
			`INSERT INTO items(item_id, title, state, updated_at, rank) VALUES
			 ('open-ready', 'Open Ready', 'in-queue', ?, '001'),
			 ('open-blocked', 'Open Blocked', 'blocked', ?, '002'),
			 ('open-held', 'Open Held', 'in-queue', ?, '003')`,
			openAt.UnixMilli(), openAt.UnixMilli(), openAt.UnixMilli(),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET waiting_on='{"waiter":"owner","reason":"owner review"}' WHERE item_id='open-blocked'`); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at) VALUES ('run-open-held', 'open-held', 'lane-1', 'fixture', 'exited', 1, ?, ?)`,
			openAt.UnixMilli(), openAt.Add(10*time.Second).UnixMilli(),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.work-preserved', 'run', 'run-open-held', '{"item_id":"open-held","run_id":"run-open-held","lane_id":"lane-1","ref":"refs/tillr/preserved/run-open-held","repository":"/repo","reason":"stopped","summary":"1 file"}')`,
			openAt.UnixMilli(),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.item-fault', 'item', 'open-held', '{"item_id":"open-held","reason":"stage-failed","detail":"worktree error"}')`,
			openAt.UnixMilli(),
		); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	return path
}

func measureWaitingHistory(t *testing.T, storePath string) ([]waitingRow, string, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	defer s.Close()

	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	qBefore := store.SQLQueriesIssued()
	rBefore := store.SQLRowsRead()
	evBefore := store.EventRowsRead()

	var rows []waitingRow
	var capLine string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		rows, err = assembleWaitingAt(r, nil, storePath, now)
		if err != nil {
			return err
		}
		// Exercise default planner-backed decoration (conflict + preserved work) and table lane-capacity summary.
		plannerRows := make([]waitingRow, len(rows))
		copy(plannerRows, rows)
		itemIDs := make([]string, 0, len(plannerRows))
		for _, row := range plannerRows {
			if row.ItemID != "" {
				itemIDs = append(itemIDs, row.ItemID)
			}
		}
		conflicts, err := waitingConflictsForItems(r, itemIDs)
		if err != nil {
			return err
		}
		addConflictHistory(plannerRows, conflicts)
		if err := addPreservedHistory(r, plannerRows); err != nil {
			return err
		}
		capRows, err := assembleWaitingLaneCapacityRows(r, filepath.Join(filepath.Dir(storePath), "loop"))
		if err != nil {
			return err
		}
		capLine = laneCapacityLine(capRows)
		// Also exercise the default-mode store readers (quotas, comment intake, lane conditions, patch moves, graph revisions).
		if _, err := waitingBackendQuotas(r, now); err != nil {
			return err
		}
		if _, err := waitingCommentIntake(r); err != nil {
			return err
		}
		if _, err := waitingLaneConditions(r); err != nil {
			return err
		}
		if _, err := waitingPatchMoves(r, storePath); err != nil {
			return err
		}
		if _, err := readAlarms(r, filepath.Dir(storePath), "", now); err != nil {
			return err
		}
		return addGraphRevisionHistory(r, rows)
	}); err != nil {
		t.Fatalf("waiting read: %v", err)
	}
	return rows, capLine, store.SQLQueriesIssued() - qBefore, store.SQLRowsRead() - rBefore, store.EventRowsRead() - evBefore
}

func TestWaitingDoesNotScaleWithFinishedHistory(t *testing.T) {
	smallPath := seedWaitingHistoryStore(t, 5)
	largePath := seedWaitingHistoryStore(t, 100)

	smallRowsOut, smallCap, smallQ, smallSQL, smallEv := measureWaitingHistory(t, smallPath)
	largeRowsOut, largeCap, largeQ, largeSQL, largeEv := measureWaitingHistory(t, largePath)

	if len(smallRowsOut) != 3 {
		t.Fatalf("small rows = %d, want 3", len(smallRowsOut))
	}
	if smallCap != largeCap {
		t.Fatalf("lane capacity line differs: small=%q large=%q", smallCap, largeCap)
	}
	foundPreserved := false
	for i := range smallRowsOut {
		sRow, lRow := smallRowsOut[i], largeRowsOut[i]
		// clear-fault embeds the fault's event seq, which shifts with prior history.
		if sRow.ItemID == "open-held" {
			if strings.Contains(sRow.Why, "refs/tillr/preserved/run-open-held") {
				foundPreserved = true
			}
			sRow.Mover, lRow.Mover = "", ""
		}
		if !reflect.DeepEqual(sRow, lRow) {
			t.Fatalf("waiting row %d differs between small and large history:\nsmall: %+v\nlarge: %+v", i, smallRowsOut[i], largeRowsOut[i])
		}
	}
	if !foundPreserved {
		t.Fatalf("open-held row omitted preserved work: %+v", smallRowsOut)
	}

	t.Logf("waiting small(5 finished): queries=%d sqlRows=%d eventRows=%d", smallQ, smallSQL, smallEv)
	t.Logf("waiting large(100 finished): queries=%d sqlRows=%d eventRows=%d", largeQ, largeSQL, largeEv)

	if largeQ > smallQ {
		t.Errorf("SQLQueriesIssued grew with finished history: small=%d large=%d", smallQ, largeQ)
	}
	if largeSQL > smallSQL {
		t.Errorf("SQLRowsRead grew with finished history: small=%d large=%d", smallSQL, largeSQL)
	}
	if largeEv > smallEv {
		t.Errorf("EventRowsRead grew with finished history: small=%d large=%d", smallEv, largeEv)
	}
}
