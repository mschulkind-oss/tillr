package run_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWorkerProgressAndChildRecords(t *testing.T) {
	dir := scratch(t)
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	script := `set -eu
: "${TILLR_PROGRESS_FILE:?}" "${TILLR_CHILD_SESSIONS_ROOT:?}" "${TILLR_CHILD_SESSIONS_MARKER:?}"
printf 'c1\nc2\n' > "$TILLR_PROGRESS_FILE"
mkdir -p "$TILLR_CHILD_SESSIONS_ROOT/async-subagent-runs/a"
printf '{"steps":[{"agent":"reviewer","sessionFile":"/recorded/child.jsonl","startedAt":1000,"endedAt":2000,"turnCount":6}]}' > "$TILLR_CHILD_SESSIONS_ROOT/async-subagent-runs/a/status.json"
printf 'pi-subagents-v1\n' > "$TILLR_CHILD_SESSIONS_MARKER"
sleep 0.3
`
	req := run.Request{RunID: "worker-records", ItemID: "item-a", Backend: "command", WorkingDir: run.WorktreeDir, Command: []string{"sh", "-c", script}}
	if _, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='run.progress' AND json_extract(payload,'$.item_id')='item-a' AND json_extract(payload,'$.criterion_id') IN ('c1','c2')`).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Errorf("progress events = %d, want 2", count)
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='run.child-session' AND json_extract(payload,'$.role')='reviewer' AND json_extract(payload,'$.started_at_ms')=1000 AND json_extract(payload,'$.ended_at_ms')=2000 AND json_extract(payload,'$.turn_count')=6 AND json_extract(payload,'$.transcript')='/recorded/child.jsonl'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("child records = %d, want 1", count)
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind='run.children-recorded'`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("capability records = %d, want 1", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Ensure the producer's path is run-local rather than a reusable lane file.
	if _, err := os.Stat(filepath.Join(dir, "lanes", "lane_worker-records", "scratch", "worker-records", "progress")); err != nil {
		t.Fatal(err)
	}
}
