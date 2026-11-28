package lanestate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestReadReservationHistoryNeedsNoDrainSchema(t *testing.T) {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "lane-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	appendEvent := func(kind string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (1,?,'run','run-a',?)`, kind, string(raw))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	assert := func(status, reason string) {
		t.Helper()
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			states, err := Read(r)
			if err != nil {
				return err
			}
			if got := states["item-a"]; got.Status != status || got.Reason != reason {
				t.Errorf("state=%+v, want %s/%s", got, status, reason)
			}
			var drainTables int
			if err := r.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='trunk_history'`).Scan(&drainTables); err != nil {
				return err
			}
			if drainTables != 0 {
				t.Error("read projection registered drain schema")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("drain.started", map[string]any{"decision": map[string]any{"item_id": "item-a"}})
	appendEvent("drain.red-cap", map[string]any{"candidate": map[string]any{}})
	assert("kept", "candidate awaits pin verification and lane release")
	appendEvent("drain.candidate", map[string]any{})
	appendEvent("boundary.check", map[string]any{"clean": false, "runs": []map[string]string{{"run_id": "run-a"}}})
	assert("kept", "boundary violation needs the original checkout for inspection")
	appendEvent("drain.item-fault-cleared", map[string]any{"run_id": "run-a", "reason": "host-boundary-violation"})
	assert("kept", "candidate awaits pin verification and lane release")
	appendEvent("drain.candidate-lane", map[string]any{"candidate": map[string]any{}, "released": true})
	assert("released", "")
	// The writer's payload omits a false lane_released (omitempty): the assignment
	// itself, not an explicit false, is what occupies the lane again.
	appendEvent("drain.candidate-assigned", map[string]any{"decision": map[string]any{"item_id": "item-a"}, "candidate": map[string]any{}, "needs_stage": true})
	assert("kept", "candidate awaits pin verification and lane release")
	appendEvent("drain.examined", map[string]any{})
	assert("", "")
	// A validated continuation reopens the same run after an examined segment.
	appendEvent("drain.started", map[string]any{"decision": map[string]any{"item_id": "item-a", "lane_id": "lane-1", "run_request": map[string]any{"run_id": "run-a", "backend": "fixture"}}})
	appendEvent("drain.examined", map[string]any{})
	segment := map[string]any{"run_id": "run-a", "item_id": "item-a", "lane_id": "lane-1", "backend": "fixture", "session_id": "33b5f2d2-0cd4-4a49-b293-c58f5635561f", "resumed_at": "2026-11-17T00:00:00Z"}
	appendEvent("run.resume", segment)
	appendEvent("drain.candidate-lane", map[string]any{"candidate": map[string]any{}, "released": true})
	assert("released", "")
	appendEvent("drain.examined", map[string]any{})
	appendEvent("run.resume", segment) // replay is not another continuation
	assert("", "")
	segment["resumed_at"] = "2026-11-17T00:01:00Z"
	segment["backend"] = "wrong-backend"
	appendEvent("run.resume", segment)
	assert("", "")
	segment["backend"] = "fixture"
	segment["session_id"] = "invalid-session"
	appendEvent("run.resume", segment)
	assert("", "")
	segment["session_id"] = "33b5f2d2-0cd4-4a49-b293-c58f5635561f"
	appendEvent("gate.verdict", map[string]any{"result": "pass"})
	appendEvent("run.resume", segment)
	assert("", "")
}

func TestHeldByLaneMatchesUnfinishedUnreleasedReservations(t *testing.T) {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "lane-held-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	appendRunEvent := func(runID, kind string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (1,?,'run',?,?)`, kind, runID, string(raw))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendRunEvent("run-2", "drain.started", map[string]any{
		"decision": map[string]any{
			"item_id":     "item-2",
			"lane_id":     "lane-2",
			"run_request": map[string]any{"run_id": "run-2", "backend": "pi"},
		},
	})
	appendRunEvent("run-3", "drain.graph-changed", map[string]any{
		"decision": map[string]any{
			"item_id":     "item-3",
			"lane_id":     "lane-3",
			"run_request": map[string]any{"run_id": "run-3", "backend": "pi"},
			"parallel": []map[string]any{
				{"run_request": map[string]any{"run_id": "run-3-sib", "backend": "pi"}},
			},
		},
	})
	appendRunEvent("run-4", "drain.started", map[string]any{
		"decision": map[string]any{
			"item_id":     "item-4",
			"lane_id":     "lane-4",
			"run_request": map[string]any{"run_id": "run-4", "backend": "pi"},
		},
	})
	appendRunEvent("run-4", "drain.candidate-lane", map[string]any{"candidate": map[string]any{}, "released": true})
	appendRunEvent("run-5", "drain.started", map[string]any{
		"decision": map[string]any{
			"item_id":     "item-5",
			"lane_id":     "lane-5",
			"run_request": map[string]any{"run_id": "run-5", "backend": "pi"},
		},
	})
	appendRunEvent("run-5", "drain.examined", map[string]any{})

	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		held, err := HeldByLane(r)
		if err != nil {
			return err
		}
		if len(held) != 2 {
			t.Fatalf("held = %+v, want 2 lanes (lane-2 and lane-3)", held)
		}
		if got := held["lane-2"]; got.ItemID != "item-2" || got.RunID != "run-2" {
			t.Fatalf("lane-2 = %+v", got)
		}
		if got := held["lane-3"]; got.ItemID != "item-3" || got.RunID != "run-3" {
			t.Fatalf("lane-3 = %+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Completing via sibling run ID in parallel marks the reservation done.
	appendRunEvent("run-3-sib", "drain.examined", map[string]any{})
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		held, err := HeldByLane(r)
		if err != nil {
			return err
		}
		if len(held) != 1 || held["lane-2"].ItemID != "item-2" {
			t.Fatalf("after sibling examined, held = %+v", held)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadForItems(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	appendRunEvent := func(runID, kind string, payload any) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (1,?,'run',?,?)`, kind, runID, string(raw))
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendRunEvent("run-a", "drain.started", map[string]any{"decision": map[string]any{"item_id": "item-a"}})
	appendRunEvent("run-a", "drain.red-cap", map[string]any{"candidate": map[string]any{}})
	appendRunEvent("run-b", "drain.started", map[string]any{"decision": map[string]any{"item_id": "item-b"}})
	appendRunEvent("run-b", "drain.red-cap", map[string]any{"candidate": map[string]any{}})

	ro, err := store.OpenRead(ctx, s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		qBefore := store.SQLQueriesIssued()
		empty, err := ReadForItems(r, nil)
		if err != nil {
			return err
		}
		if len(empty) != 0 {
			t.Fatalf("empty ReadForItems = %+v, want empty", empty)
		}
		if got := store.SQLQueriesIssued() - qBefore; got != 0 {
			t.Fatalf("empty ReadForItems issued %d queries, want 0", got)
		}

		all, err := Read(r)
		if err != nil {
			return err
		}
		scoped, err := ReadForItems(r, []string{"item-a"})
		if err != nil {
			return err
		}
		if len(scoped) != 1 {
			t.Fatalf("ReadForItems returned %d items, want 1: %+v", len(scoped), scoped)
		}
		if scoped["item-a"] != all["item-a"] {
			t.Fatalf("scoped[item-a] = %+v, want %+v", scoped["item-a"], all["item-a"])
		}
		if _, ok := scoped["item-b"]; ok {
			t.Fatalf("unexpected out-of-scope item-b: %+v", scoped)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
