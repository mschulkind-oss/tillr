package gatespans

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
)

func writeForkFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestFastVerdictSpansMergeWithoutReplacingForkChecks(t *testing.T) {
	root := t.TempDir()
	dir := drainmodel.RunDir(root, "run-a")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(dir, GateSpansFileName), `{"check_spans":[{"check_id":"remaining-suite","status":"OK","start_ns":100,"end_ns":200}]}`)
	writeForkFile(t, filepath.Join(dir, "gate.fast.verdict.json"), `{"kind":"gate.verdict","payload":{"checks":[{"check_id":"documents","result":"stalled","duration_ms":60000,"budget_ms":2000,"over_budget_ms":58000,"start_unix_nano":1000000000,"end_unix_nano":61000000000,"cpu_time_ms":50,"silent_samples":60,"last_output_line":"still here","log_ref":"documents.log"}]}}`)
	rows, state := ReadGateSpans(root, "run-a", "v-a", "tick-a")
	if state != GateSpansRead || len(rows) != 2 {
		t.Fatalf("merge: %s %+v", state, rows)
	}
	row := rows[1]
	var attrs map[string]any
	if err := json.Unmarshal([]byte(row.AttributesJSON), &attrs); err != nil {
		t.Fatal(err)
	}
	if row.CheckID != "documents" || row.Status != "stalled" || attrs["budget_ms"] != float64(2000) || attrs["silent_samples"] != float64(60) || attrs["over_budget_ms"] != float64(58000) {
		t.Fatalf("fast metadata missing: %+v %v", row, attrs)
	}
}

func TestFastSpansRecordedWithoutRemainingSpans(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(drainmodel.RunDir(root, "run-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(drainmodel.RunDir(root, "run-a"), "gate.fast.verdict.json"), `{"payload":{"checks":[{"check_id":"documents","result":"pass","duration_ms":100,"start_unix_nano":1000000000,"end_unix_nano":1100000000,"budget_ms":2000,"over_budget_ms":0}]}}`)
	rows, state := ReadGateSpans(root, "run-a", "v", "tick")
	if state != GateSpansRead || len(rows) != 1 {
		t.Fatalf("fast-only spans: %s %+v", state, rows)
	}
}
