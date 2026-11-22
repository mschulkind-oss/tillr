package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestModelTimeoutEventRegistered(t *testing.T) {
	reg, err := event.Lookup(ModelTimeoutKind)
	if err != nil {
		t.Fatalf("%s is not registered: %v", ModelTimeoutKind, err)
	}
	if reg.Owner != "run" {
		t.Fatalf("%s owner = %q, want run", ModelTimeoutKind, reg.Owner)
	}
}

func TestWorkerModelTimeoutObservation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	logPath := filepath.Join(dir, "worker.log")
	req := Request{
		RunID:   "run-timeout-1",
		ItemID:  "item-timeout",
		LaneID:  "lane-6",
		Backend: "pi-designer",
		Node:    "work",
	}

	// Non-timeout errors, normal assistant messages, and tool results quoting timeout text
	// must not record run.model-timeout. Body timeout and request timeout must record it
	// with incrementing counts even when no child-sessions-recorded marker exists.
	lines := []string{
		`{"type":"message_end","message":{"role":"assistant","stopReason":"toolUse","content":[]}}`,
		`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"Request timed out.: This operation was aborted (20)"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"429 Too Many Requests"}}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)"}}`,
		`{"type":"auto_retry_start","attempt":1,"maxAttempts":3,"delayMs":2000,"errorMessage":"terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)"}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Request timed out.: This operation was aborted (20)"}}`,
	}
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := newWorkerRecords(ctx, st, req, dir, logPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}
	// Observing again with no new lines must not duplicate events.
	if err := w.observe(ctx, false); err != nil {
		t.Fatal(err)
	}

	// Simulate a resumed execution of the same run: start scanning from the previous EOF,
	// while newWorkerRecords restores the prior count from the store.
	resumedOffset := int64(len(content))
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Request timed out.: This operation was aborted (20)"}}` + "\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	resumed, err := newWorkerRecords(ctx, st, req, dir, logPath)
	if err != nil {
		t.Fatal(err)
	}
	resumed.timeoutOffset = resumedOffset
	if err := resumed.observe(ctx, true); err != nil {
		t.Fatal(err)
	}

	type timeoutPayload struct {
		RunID   string `json:"run_id"`
		ItemID  string `json:"item_id"`
		LaneID  string `json:"lane_id"`
		Backend string `json:"backend"`
		Node    string `json:"node"`
		Error   string `json:"error"`
		Count   int    `json:"count"`
	}
	var got []timeoutPayload
	if err := st.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq`, ModelTimeoutKind, req.RunID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var p timeoutPayload
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				return err
			}
			got = append(got, p)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}

	wantErrors := []string{
		"terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)",
		"Request timed out.: This operation was aborted (20)",
		"Request timed out.: This operation was aborted (20)",
	}
	if len(got) != len(wantErrors) {
		t.Fatalf("recorded %d %s events, want %d: %+v", len(got), ModelTimeoutKind, len(wantErrors), got)
	}
	for i, wantErr := range wantErrors {
		p := got[i]
		if p.RunID != req.RunID || p.ItemID != req.ItemID || p.LaneID != req.LaneID || p.Backend != req.Backend || p.Node != req.Node || p.Error != wantErr || p.Count != i+1 {
			t.Errorf("event[%d] = %+v, want run=%s item=%s lane=%s backend=%s node=%s error=%q count=%d",
				i, p, req.RunID, req.ItemID, req.LaneID, req.Backend, req.Node, wantErr, i+1)
		}
	}
}

func TestExecRecordsPiModelTimeouts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	script := `set -eu
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)"}}'
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Request timed out.: This operation was aborted (20)"}}'
`
	req := Request{
		RunID:      "run-exec-timeouts",
		ItemID:     "item-exec",
		Backend:    "pi-designer",
		Node:       "work",
		WorkingDir: WorktreeDir,
		Command:    []string{"sh", "-c", script},
	}
	if _, err := Exec(ctx, st, req, ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := st.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`, ModelTimeoutKind, req.RunID).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("recorded %d %s events from Exec, want 2", count, ModelTimeoutKind)
	}
}
