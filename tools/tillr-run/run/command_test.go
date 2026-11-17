package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCommandExecHasNoRouteSpendOrExtractor(t *testing.T) {
	dir := scratch(t)
	backends, adapter := backendWithExtractor(t, "# run report\ntokens: 123\ncost usd: 1.0\n---\nRESULT: done\n", "0")
	// A command has no adapter declaration, even with --model in its own argv.
	if err := os.Remove(filepath.Join(filepath.Dir(adapter), "pause-kind")); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := run.Request{RunID: "command", Backend: "command", Command: []string{adapter, "--model", "not-a-route"}, WorkingDir: run.WorktreeDir}
	env, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), BackendsDir: backends, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Backend != "command" || rec.Route != "" || rec.Usage != nil || rec.SpendNote != "" || rec.SpendStatus != "not-applicable" || rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0 {
		t.Fatalf("command %+v", rec)
	}
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind == "report" {
			t.Fatalf("extractor ran: %+v", rec)
		}
	}
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='run.usage'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("usage events %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
