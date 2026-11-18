package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestProgressPartialResumeAndNestedChildren(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "worker-records-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := filepath.Join(dir, "stream.log")
	if err = os.WriteFile(log, nil, 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	req := Request{RunID: "parent", ItemID: "item", LaneID: "lane"}
	w, err := newWorkerRecords(ctx, st, req, dir, log)
	if err != nil {
		t.Fatal(err)
	}
	progress := filepath.Join(dir, "progress")
	if err = os.WriteFile(progress, []byte("c1\nc"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	if w.line != 1 {
		t.Fatalf("partial claim recorded: %d", w.line)
	}
	w, err = newWorkerRecords(ctx, st, req, dir, log)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(progress, []byte("c1\nc2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "child.jsonl")
	if err = os.WriteFile(path, []byte(`{"type":"session","timestamp":"2026-11-18T00:00:00Z"}`+"\n"+`{"type":"message","timestamp":"2026-11-18T00:00:00.001Z","message":{"role":"assistant","timestamp":1794960000001}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"updatedAt": float64(time.Now().UnixMilli()), "children": []any{map[string]any{"agent": "reviewer", "sessionFile": path, "status": "completed"}}}
	if err = w.discover(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	var c childSession
	if err = json.Unmarshal([]byte(w.children[path]), &c); err != nil {
		t.Fatal(err)
	}
	if c.Role != "reviewer" || c.StartedAt == nil || c.EndedAt == nil || c.Turns == nil || *c.Turns != 1 {
		t.Fatalf("missing child facts: %+v", c)
	}
	if err = st.ReadTx(ctx, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='run.progress'`).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("resume duplicated claims: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnreadableProgressStillReleasesRun(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "worker-error-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := Request{RunID: "broken-progress", Backend: "command", WorkingDir: WorktreeDir, Command: []string{"sh", "-c", `set -eu; mkdir "$TILLR_PROGRESS_FILE"; sleep 0.3; printf finished > "$YOLO_DURABLE_DIR/worker-finished"`}}
	_, err = Exec(context.Background(), st, req, ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lanes", "lane_broken-progress", "durable", "broken-progress", "worker-finished")); err != nil {
		t.Fatalf("observation error killed worker: %v", err)
	}
	if err = st.ReadTx(context.Background(), func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM lanes WHERE state='leased'`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("recording failure orphaned lease")
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='run.observation-error'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("observation failure was not recorded once: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
