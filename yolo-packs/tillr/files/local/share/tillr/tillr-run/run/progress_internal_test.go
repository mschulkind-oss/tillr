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

// On 2026-11-18 a lane's Pi records named one child transcript twice, with different end
// clocks and turn counts (a status file and a foreground-history entry). Each sample
// compared a record only with the last one written, so the two took turns and every
// sample appended both again: 24,163 of 24,240 events in one stretch, about 36 a second,
// for six sessions. Observations of one transcript now merge into one record that does
// not depend on the order they are read in, so a re-read of the same sources writes
// nothing.
func TestDisagreeingRecordsOfOneChildAreWrittenOnlyWhenTheMergeChanges(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "worker-children-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	path := filepath.Join(dir, "child.jsonl")
	if err = os.WriteFile(path, []byte(`{"type":"session","timestamp":"2026-11-18T00:00:00Z"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	status := map[string]any{"agent": "reviewer", "sessionFile": path, "status": "completed", "endedAt": float64(1794979177691), "turnCount": float64(10)}
	history := map[string]any{"agent": "reviewer", "sessionFile": path, "status": "completed", "endedAt": float64(1794979988384), "turnCount": float64(7)}
	count := func() int {
		var n int
		if err := st.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind='run.child-session'`).Scan(&n)
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	w, err := newWorkerRecords(ctx, st, Request{RunID: "forward", ItemID: "item", LaneID: "lane"}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for sample := 0; sample < 5; sample++ {
		for _, record := range []map[string]any{status, history} {
			if err = w.discover(ctx, record); err != nil {
				t.Fatal(err)
			}
		}
	}
	if n := count(); n > 2 {
		t.Fatalf("five samples of the same two records appended %d child-session events, not at most 2", n)
	}
	var forward childSession
	if err = json.Unmarshal([]byte(w.children[path]), &forward); err != nil {
		t.Fatal(err)
	}

	backward, err := newWorkerRecords(ctx, st, Request{RunID: "backward", ItemID: "item", LaneID: "lane"}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []map[string]any{history, status} {
		if err = backward.discover(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	var reverse childSession
	if err = json.Unmarshal([]byte(backward.children[path]), &reverse); err != nil {
		t.Fatal(err)
	}
	forward.RunID, forward.SessionID, reverse.RunID, reverse.SessionID = "", "", "", ""
	a, _ := json.Marshal(forward)
	b, _ := json.Marshal(reverse)
	if string(a) != string(b) {
		t.Fatalf("the merged record depends on read order:\n  %s\n  %s", a, b)
	}

	// A restarted recorder reloads the merged record and writes nothing for the same sources.
	before := count()
	again, err := newWorkerRecords(ctx, st, Request{RunID: "forward", ItemID: "item", LaneID: "lane"}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []map[string]any{history, status} {
		if err = again.discover(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(); n != before {
		t.Fatalf("a restarted recorder appended %d events for sources already merged", n-before)
	}
}
