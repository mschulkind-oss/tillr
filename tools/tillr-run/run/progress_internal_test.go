package run

import (
	"context"
	"encoding/json"
	"fmt"
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
	if _, err := os.Stat(filepath.Join(dir, "lanes", "lane_broken-progress", "scratch", "broken-progress", "worker-finished")); err != nil {
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

func TestChildSessionWrittenOnlyWhenReaderFieldsChange(t *testing.T) {
	dir, err := os.MkdirTemp(os.Getenv("YOLO_DURABLE_DIR"), "worker-child-lifecycle-")
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
	logPath := filepath.Join(dir, "stream.log")
	if err = os.WriteFile(logPath, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "child-sessions-recorded"), []byte("pi-subagents-v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	statusDir := filepath.Join(dir, "pi-subagents", "async-subagent-runs", "r1")
	fgDir := filepath.Join(dir, "pi-subagents", "async-subagent-results", "foreground-history")
	if err = os.MkdirAll(statusDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(fgDir, 0755); err != nil {
		t.Fatal(err)
	}
	statusPath := filepath.Join(statusDir, "status.json")
	fgPath := filepath.Join(fgDir, "r1.json")
	childPath := filepath.Join(dir, "child.jsonl")

	writeJSON := func(path string, v any) {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	appendFile := func(path, line string) {
		t.Helper()
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			t.Fatal(err)
		}
		_, werr := f.WriteString(line)
		cerr := f.Close()
		if werr != nil {
			t.Fatal(werr)
		}
		if cerr != nil {
			t.Fatal(cerr)
		}
	}
	events := func() []childSession {
		t.Helper()
		var out []childSession
		if err := st.ReadTx(ctx, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT payload FROM events WHERE kind='run.child-session' ORDER BY seq`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var raw string
				if err := rows.Scan(&raw); err != nil {
					return err
				}
				var c childSession
				if err := json.Unmarshal([]byte(raw), &c); err != nil {
					return err
				}
				out = append(out, c)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}

	req := Request{RunID: "parent-lifecycle", ItemID: "item", LaneID: "lane"}
	w, err := newWorkerRecords(ctx, st, req, dir, logPath)
	if err != nil {
		t.Fatal(err)
	}

	// Unstarted pending steps with no transcript on disk write no child-session event.
	writeJSON(statusPath, map[string]any{
		"status": "pending",
		"steps":  []any{map[string]any{"agent": "scout", "status": "pending", "sessionFile": childPath}},
	})
	if err = w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got := events(); len(got) != 0 {
		t.Fatalf("pending unstarted step wrote %d child-session events: %+v", len(got), got)
	}

	// Launching the child writes exactly one start record.
	appendFile(childPath, `{"type":"session","timestamp":"2026-11-18T00:00:01Z"}`+"\n")
	writeJSON(statusPath, map[string]any{
		"status":    "running",
		"updatedAt": float64(1794960001050),
		"steps":     []any{map[string]any{"agent": "scout", "status": "running", "startedAt": float64(1794960001000), "sessionFile": childPath}},
	})
	if err = w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	got := events()
	if len(got) != 1 || got[0].Role != "scout" || got[0].StartedAt == nil || *got[0].StartedAt != 1794960001000 || got[0].EndedAt != nil || got[0].Turns != nil {
		t.Fatalf("start event = %+v, want 1 open child event without live turn_count", got)
	}

	// Live assistant turns, status heartbeats, and stream updates while running write nothing.
	for turn := 1; turn <= 4; turn++ {
		appendFile(childPath, fmt.Sprintf(`{"type":"message","timestamp":"2026-11-18T00:00:0%dZ","message":{"role":"assistant","timestamp":%d}}`+"\n", turn+1, 1794960001000+int64(turn)*1000))
		writeJSON(statusPath, map[string]any{
			"status":    "running",
			"updatedAt": float64(1794960001000 + turn*1000),
			"usage":     map[string]any{"turns": float64(turn), "input": float64(turn * 100)},
			"steps":     []any{map[string]any{"agent": "scout", "status": "running", "startedAt": float64(1794960001000), "updatedAt": float64(1794960001000 + turn*1000), "sessionFile": childPath, "turnCount": float64(turn)}},
		})
		appendFile(logPath, fmt.Sprintf(`{"type":"tool_execution_update","partialResult":{"details":{"results":[{"agent":"scout","status":"running","sessionFile":%q,"turnCount":%d}]}}}`+"\n", childPath, turn))
		if err = w.observe(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	if got = events(); len(got) != 1 {
		t.Fatalf("running child churn appended %d events, want still 1: %+v", len(got), got)
	}

	// Completion across status, foreground-history, and stream.log in one sample writes once
	// and preserves the child's closeObservedAt over later wrapper endedAt timestamps.
	writeJSON(statusPath, map[string]any{
		"status": "completed",
		"steps": []any{map[string]any{
			"agent":       "scout",
			"status":      "completed",
			"startedAt":   float64(1794960001000),
			"endedAt":     float64(1794960005080),
			"sessionFile": childPath,
			"processTerminal": map[string]any{
				"instances": []any{map[string]any{"kind": "pi-writer", "closeObservedAt": float64(1794960005030)}},
			},
		}},
	})
	writeJSON(fgPath, map[string]any{
		"agent":       "scout",
		"status":      "completed",
		"sessionFile": childPath,
		"endedAt":     float64(1794960005095),
		"turnCount":   float64(2),
	})
	appendFile(logPath, fmt.Sprintf(`{"type":"tool_execution_end","result":{"details":{"results":[{"agent":"scout","status":"completed","sessionFile":%q,"endedAt":1794960005110}]}}}`+"\n", childPath))
	if err = w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	got = events()
	if len(got) != 2 {
		t.Fatalf("completion wrote %d total events, want 2: %+v", len(got), got)
	}
	if got[1].EndedAt == nil || *got[1].EndedAt != 1794960005030 || got[1].EndClock != "observed" || got[1].Turns == nil || *got[1].Turns != 4 {
		t.Fatalf("terminal event = %+v, want closeObservedAt 1794960005030 and 4 turns", got[1])
	}

	// Further samples, a restarted recorder (even if only foreground-history's later wrapper
	// endedAt remains on disk), and final flush write no duplicate events.
	for i := 0; i < 3; i++ {
		if err = w.observe(ctx, false); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Remove(statusPath); err != nil {
		t.Fatal(err)
	}
	again, err := newWorkerRecords(ctx, st, req, dir, logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = again.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err = again.observe(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got = events(); len(got) != 2 {
		t.Fatalf("post-completion or restarted samples wrote extra events (%d != 2): %+v", len(got), got)
	}

	// A child already completed when first observed across disagreeing files writes once.
	fastPath := filepath.Join(dir, "fast-child.jsonl")
	appendFile(fastPath, `{"type":"session","timestamp":"2026-11-18T00:00:10Z"}`+"\n"+`{"type":"message","timestamp":"2026-11-18T00:00:11Z","message":{"role":"assistant","timestamp":1794960011000}}`+"\n")
	r2Dir := filepath.Join(dir, "pi-subagents", "async-subagent-runs", "r2")
	if err = os.MkdirAll(r2Dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeJSON(filepath.Join(r2Dir, "status.json"), map[string]any{"agent": "reviewer", "status": "completed", "sessionFile": fastPath, "endedAt": float64(1794960011500), "turnCount": float64(1)})
	writeJSON(filepath.Join(fgDir, "r2.json"), map[string]any{"agent": "reviewer", "status": "completed", "sessionFile": fastPath, "endedAt": float64(1794960011600), "turnCount": float64(0)})
	if err = again.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got = events(); len(got) != 3 {
		t.Fatalf("already-completed child wrote %d events, want 1 (total 3): %+v", len(got)-2, got)
	}
}
