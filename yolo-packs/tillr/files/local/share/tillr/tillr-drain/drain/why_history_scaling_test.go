package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func seedWhyHistoryStore(t *testing.T, finishedCount int) string {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), fmt.Sprintf("board-%d.sqlite3", finishedCount))
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("init fixture: %v", err)
	}
	defer s.Close()

	baseAt := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		// Seed offered lanes.
		for _, lane := range []string{"lane-1", "lane-2"} {
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state) VALUES (?, 'idle')`, lane); err != nil {
				return err
			}
		}

		// Seed finished history (landed and closed items with runs, node_runs, landings, backend_outcomes, and events).
		for i := 0; i < finishedCount; i++ {
			itemID := fmt.Sprintf("item-done-%04d", i)
			runID := fmt.Sprintf("run-done-%04d", i)
			laneID := fmt.Sprintf("item-%s", itemID)
			state := StateLanded
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
				`INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome, seat) VALUES (?, 1, 'build', ?, 'pass', 'build')`,
				itemID, runID,
			); err != nil {
				return err
			}
			if state == StateLanded {
				if _, err := tx.Exec(
					`INSERT INTO landings(landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after) VALUES (?, ?, ?, ?, 'landed', 'aaa', 'bbb', 'ccc', 'ddd')`,
					"land-"+runID, itemID, runID, "verdict-"+runID,
				); err != nil {
					return err
				}
			}

			// Events across all kinds that previously caused full-history scans.
			if _, err := tx.Append(event.Event{
				Kind: item.UpdateKind, ObjectType: "item", ObjectID: &itemID, At: at,
				Payload: []byte(`{"state":"in-progress"}`),
			}); err != nil {
				return err
			}
			startPayload, _ := json.Marshal(pendingWork{
				Decision: Decision{
					ItemID: itemID, LaneID: laneID, Action: ActionDispatch, Attempt: 1,
					RunRequest: &run.Request{RunID: runID, ItemID: itemID, LaneID: laneID, Backend: "fixture"},
				},
				Done: true, LaneReleased: true,
			})
			if _, err := tx.Append(event.Event{
				Kind: dispatchStartedKind, ObjectType: "run", ObjectID: &runID, At: at, Payload: startPayload,
			}); err != nil {
				return err
			}
			allocPayload, _ := json.Marshal(map[string]string{
				"run_id": runID, "item_id": itemID, "lane_id": laneID, "backend": "fixture", "started_at": at.Format(time.RFC3339),
			})
			if _, err := tx.Append(event.Event{
				Kind: run.AllocateKind, ObjectType: "run", ObjectID: &runID, At: at, Payload: allocPayload,
			}); err != nil {
				return err
			}
			finPayload, _ := json.Marshal(run.Record{
				RunID: runID, ItemID: itemID, LaneID: laneID, Backend: "fixture", State: run.StateExited,
				StartedAt: at.Format(time.RFC3339), FinishedAt: at.Add(30 * time.Second).Format(time.RFC3339),
			})
			finSeq, err := tx.Append(event.Event{
				Kind: run.FinishKind, ObjectType: "run", ObjectID: &runID, At: at.Add(30 * time.Second), Payload: finPayload,
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO backend_outcomes(terminal_seq, class, closes) VALUES (?, 'answered', 0)`, finSeq); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: GateVerdictKind, ObjectType: "run", ObjectID: &runID, At: at.Add(35 * time.Second),
				Payload: []byte(`{"result":"pass"}`),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: RefusalKind, ObjectType: "run", ObjectID: &runID, At: at.Add(20 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"item_id":%q,"step":"verify-not-done"}`, itemID)),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: item.ConflictKind, ObjectType: "item", ObjectID: &itemID, At: at.Add(25 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"conflict_id":"c-%d","item_id":%q,"run_id":%q,"text":"done","waiting_on":"build","status":"answered","answer_head":"abc"}`, i, itemID, runID)),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: obstacles.HeldKind, ObjectType: "item", ObjectID: &itemID, At: at.Add(26 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"item_id":%q,"run_id":%q,"kind":"owner"}`, itemID, runID)),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: itemFaultKind, ObjectType: "item", ObjectID: &itemID, At: at.Add(27 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"item_id":%q,"run_id":%q,"reason":"stage-failed","detail":"old"}`, itemID, runID)),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: itemFaultClearedKind, ObjectType: "item", ObjectID: &itemID, At: at.Add(28 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"item_id":%q,"reason":"stage-failed"}`, itemID)),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: laneRefusedKind, ObjectType: "lane", ObjectID: &laneID, At: at.Add(29 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"lane_id":%q,"reason":"lane-holds-unlanded-change","detail":"old","remedy":"reset"}`, laneID)),
			}); err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind: callOffered, ObjectType: "call", At: at.Add(31 * time.Second),
				Payload: []byte(fmt.Sprintf(`{"call_id":"call-%d","run_id":%q,"item_id":%q,"tool":"land"}`, i, runID, itemID)),
			}); err != nil {
				return err
			}
		}

		// Seed 3 identical open items (ready, blocked, and held).
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
		heldID := "open-held"
		if _, err := tx.Append(event.Event{
			Kind: itemFaultKind, ObjectType: "item", ObjectID: &heldID, At: openAt,
			Payload: []byte(`{"item_id":"open-held","reason":"stage-failed","detail":"worktree error"}`),
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	return path
}

func measureWhy(t *testing.T, storePath string) (WhyAnswer, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("open read: %v", err)
	}
	defer s.Close()

	qBefore := store.SQLQueriesIssued()
	rBefore := store.SQLRowsRead()
	evBefore := store.EventRowsRead()
	evCallsBefore := store.EventLogReads()

	var answer WhyAnswer
	lanes := []string{"lane-1", "lane-2"}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		answer, err = Why(r, Pass{
			Now:            "2026-11-27T00:00:00Z",
			AvailableLanes: &lanes,
		}, nil, nil)
		return err
	}); err != nil {
		t.Fatalf("Why: %v", err)
	}
	t.Logf("why %s: eventLogReads=%d openItems=%d", storePath, store.EventLogReads()-evCallsBefore, len(answer.Rows))
	return answer, store.SQLQueriesIssued() - qBefore, store.SQLRowsRead() - rBefore, store.EventRowsRead() - evBefore
}

func TestWhyDoesNotScaleWithFinishedHistory(t *testing.T) {
	smallPath := seedWhyHistoryStore(t, 5)
	largePath := seedWhyHistoryStore(t, 100)

	smallAns, smallQ, smallRows, smallEv := measureWhy(t, smallPath)
	largeAns, largeQ, largeRows, largeEv := measureWhy(t, largePath)

	if len(smallAns.Rows) != 3 || len(largeAns.Rows) != 3 {
		t.Fatalf("rows len: small=%d large=%d, want 3", len(smallAns.Rows), len(largeAns.Rows))
	}
	for i := range smallAns.Rows {
		sRow, lRow := smallAns.Rows[i], largeAns.Rows[i]
		// clear-fault embeds the fault's event seq, which shifts with prior history.
		if sRow.ItemID == "open-held" {
			sRow.Mover, lRow.Mover = "", ""
		}
		if !reflect.DeepEqual(sRow, lRow) {
			t.Fatalf("Why row %d differs between small and large history:\nsmall: %+v\nlarge: %+v", i, smallAns.Rows[i], largeAns.Rows[i])
		}
	}
	t.Logf("why small(5 finished): queries=%d sqlRows=%d eventRows=%d", smallQ, smallRows, smallEv)
	t.Logf("why large(100 finished): queries=%d sqlRows=%d eventRows=%d", largeQ, largeRows, largeEv)

	if largeQ > smallQ {
		t.Errorf("SQLQueriesIssued grew with finished history: small=%d large=%d", smallQ, largeQ)
	}
	if largeRows > smallRows {
		t.Errorf("SQLRowsRead grew with finished history: small=%d large=%d", smallRows, largeRows)
	}
	if largeEv > smallEv {
		t.Errorf("EventRowsRead grew with finished history: small=%d large=%d", smallEv, largeEv)
	}
}
