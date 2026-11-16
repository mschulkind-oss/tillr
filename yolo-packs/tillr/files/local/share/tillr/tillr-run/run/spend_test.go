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

// backendWithExtractor writes a backends directory holding one adapter and its
// `extract-report`, a shell stub that writes the report body it is given to the path `--out`
// names. The real extractors read a harness stream; what these cases are about is the
// contract between the report header and the run record, so the body is the whole dialect.
//
// It answers the directory and the adapter's own path. A run's extractor is resolved beside
// the adapter `Command[0]` names, so these cases run an adapter in the backend's directory
// rather than `/bin/true`: the dialect is the adapter's, and a backend is only a name for one
// invocation of it.
func backendWithExtractor(t *testing.T, body, exit string) (backends, adapter string) {
	t.Helper()
	backends = filepath.Join(scratch(t), "backends")
	dir := filepath.Join(backends, "fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the backend directory failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pause-kind"), []byte("stops-where-it-stands\n"), 0644); err != nil {
		t.Fatal(err)
	}
	adapter = filepath.Join(dir, "run-fixture")
	if err := os.WriteFile(adapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing the adapter failed: %v", err)
	}
	script := "#!/bin/sh\n" +
		`out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--out" ]; then out=$a; fi
  prev=$a
done
`
	if body != "" {
		script += "printf '%s' '" + body + "' > \"$out\"\n"
	}
	script += "exit " + exit + "\n"
	path := filepath.Join(dir, "extract-report")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the extractor failed: %v", err)
	}
	return backends, adapter
}

// execFixture opens a store and runs one request through it, answering the stored record. The
// adapter is the program the request's argv names and the backend is the name the request
// carries, so a case can make them differ the way an alias does.
func execFixture(t *testing.T, backendsDir, adapter, backend string) (run.Record, *store.Store) {
	t.Helper()
	dir := scratch(t)
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatalf("opening the store failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	env, err := run.Exec(ctx, s, run.Request{
		RunID: "run_001", Backend: backend,
		Command: []string{adapter}, WorkingDir: ".", TimeoutMS: 60000,
	}, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), BackendsDir: backendsDir, Now: now})
	if err != nil {
		t.Fatalf("running the request failed: %v", err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatalf("reading the record failed: %v", err)
	}
	return rec, s
}

// TestARunRecordsTheSpendItsReportCarries is the fill path: the extractor's report header
// is where the run's spend comes from, and the record and the row both carry it.
func TestARunRecordsTheSpendItsReportCarries(t *testing.T) {
	backends, adapter := backendWithExtractor(t, `# run report
tokens: 1500
cost usd: 0.030000
---
`, "0")
	rec, s := execFixture(t, backends, adapter, "fixture")
	if rec.Usage == nil || rec.Usage.Tokens == nil || *rec.Usage.Tokens != 1500 {
		t.Fatalf("the record's tokens are %+v, want 1500 read from the report", rec.Usage)
	}
	if rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != 0.03 {
		t.Fatalf("the record's cost is %+v, want $0.03 read from the report", rec.Usage)
	}
	// The row is what a later tick reads, so the spend must be there and not only in the
	// record the face printed.
	stored, found := storedRun(t, s, "run_001")
	if !found {
		t.Fatalf("the store does not hold the run it just wrote")
	}
	if stored.Usage == nil || stored.Usage.Tokens == nil || *stored.Usage.Tokens != 1500 {
		t.Fatalf("the row's spend is %+v, want the record's 1500 tokens", stored.Usage)
	}
	if stored.Usage.CostUSD == nil || *stored.Usage.CostUSD != 0.03 {
		t.Fatalf("the row's cost is %+v, want the record's $0.03", stored.Usage.CostUSD)
	}
}

// TestAReportedZeroIsNotAnUnknownSpend is the distinction the whole ceiling rests on: a
// backend that reports zero spent zero, and a run whose report carries no usage at all
// carries no usage block — the two must not read the same.
func TestAReportedZeroIsNotAnUnknownSpend(t *testing.T) {
	zeroBackends, zeroAdapter := backendWithExtractor(t, `# run report
tokens: 0
cost usd: 0.000000
---
`, "0")
	zero, _ := execFixture(t, zeroBackends, zeroAdapter, "fixture")
	if zero.Usage == nil || zero.Usage.Tokens == nil || *zero.Usage.Tokens != 0 {
		t.Fatalf("a report of zero tokens produced %+v, want a reported zero", zero.Usage)
	}
	if zero.Usage.CostUSD == nil || *zero.Usage.CostUSD != 0 {
		t.Fatalf("a report of $0.000000 produced %+v, want a reported zero cost", zero.Usage.CostUSD)
	}

	unknownBackends, unknownAdapter := backendWithExtractor(t, `# run report
tokens: unknown
cost usd: unknown
---
`, "0")
	unknown, _ := execFixture(t, unknownBackends, unknownAdapter, "fixture")
	if unknown.Usage != nil {
		t.Fatalf("a report of two unknowns produced %+v, want no usage block at all", unknown.Usage)
	}
}

// TestAnExtractorThatFailsLeavesTheRunItsOutcome is the rule the loop cannot bend: a run
// that exited 0 stays an exited run, its spend reads unknown, and the failure is in the
// log as its own event rather than as a failed run.
func TestAnExtractorThatFailsLeavesTheRunItsOutcome(t *testing.T) {
	backends, adapter := backendWithExtractor(t, "", "1")
	rec, s := execFixture(t, backends, adapter, "fixture")
	if rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0 {
		t.Fatalf("the run is %q with exit %v, want an exited run with 0: a failed extractor must not change the outcome",
			rec.State, rec.ExitCode)
	}
	if rec.Usage != nil {
		t.Fatalf("the record carries %+v, want no spend: the extraction failed", rec.Usage)
	}
	events, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	seen := false
	for _, ev := range events {
		if ev.Kind == run.UsageKind {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("no %s event was appended for the failed extraction: the spend is missing and the log does not say why", run.UsageKind)
	}
}

// TestNoBackendDirectoryReadsNoSpend is the fixture path: a run with no extractor to run
// records no spend and is not an error, which is what lets every other case in this module
// ignore the spend entirely.
func TestNoBackendDirectoryReadsNoSpend(t *testing.T) {
	rec, _ := execFixture(t, "", "/bin/true", "fixture")
	if rec.Usage != nil {
		t.Fatalf("a run with no backends directory recorded %+v, want no spend", rec.Usage)
	}
	if rec.State != run.StateExited {
		t.Fatalf("the run is %q, want an exited run", rec.State)
	}
}

// TestAnAliasedBackendReadsTheExtractorBesideItsAdapter is criterion 1's extraction half. A
// backend is a name for one invocation of an adapter, and the extractor belongs to the
// adapter's dialect rather than to the backend's name: `pi-designer` runs the pi adapter on
// the design seat's model, so its report is the one beside that adapter. Resolving
// `<BackendsDir>/<backend>/extract-report` asks for a directory that never existed, which is
// the 2026-11-15 incident's `missing .../backends/pi-designer/extract-report`: the run
// finished and its spend read unknown for every seated backend.
func TestAnAliasedBackendReadsTheExtractorBesideItsAdapter(t *testing.T) {
	backends, adapter := backendWithExtractor(t, `# run report
tokens: 1500
cost usd: 0.030000
---
`, "0")
	rec, _ := execFixture(t, backends, adapter, "fixture-designer")
	if rec.Usage == nil || rec.Usage.Tokens == nil || *rec.Usage.Tokens != 1500 {
		t.Fatalf("the aliased backend's record carries %+v, want 1500 read from the extractor beside its adapter", rec.Usage)
	}
	if rec.Usage.CostUSD == nil || *rec.Usage.CostUSD != 0.03 {
		t.Fatalf("the aliased backend's cost is %+v, want $0.03", rec.Usage.CostUSD)
	}
}
