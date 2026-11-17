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
