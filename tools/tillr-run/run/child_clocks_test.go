package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestParallelChildClocksAreNotBatchClocks(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "child-clocks-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w, err := newWorkerRecords(context.Background(), st, Request{RunID: "parent"}, dir, filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	children := []any{}
	for i := 0; i < 2; i++ {
		path := filepath.Join(dir, fmt.Sprintf("child-%d.jsonl", i))
		body := fmt.Sprintf("{\"type\":\"session\",\"timestamp\":\"2026-11-18T00:00:0%dZ\"}\n{\"type\":\"message\",\"timestamp\":\"2026-11-18T00:00:0%d.500Z\",\"message\":{\"role\":\"assistant\",\"timestamp\":%d}}\n", i+1, i+1, 1794960000000+int64(i+1)*1000)
		if err = os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		children = append(children, map[string]any{"agent": "worker", "sessionFile": path, "status": "completed", "updatedAt": float64(1794960009000)})
	}
	if err = w.discover(context.Background(), map[string]any{"startedAt": float64(1794960000000), "endedAt": float64(1794960009000), "children": children}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var c childSession
		if err = json.Unmarshal([]byte(w.children[filepath.Join(dir, fmt.Sprintf("child-%d.jsonl", i))]), &c); err != nil {
			t.Fatal(err)
		}
		if c.StartedAt == nil || *c.StartedAt != 1794960000000+int64(i+1)*1000 {
			t.Fatalf("batch start used for child %d: %+v", i, c)
		}
		if c.EndedAt == nil || *c.EndedAt != 1794960000000+int64(i+1)*1000+500 {
			t.Fatalf("batch end used for child %d: %+v", i, c)
		}
	}
}
