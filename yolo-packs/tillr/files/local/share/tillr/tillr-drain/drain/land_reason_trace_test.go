package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// landTraceStub is a land program that fails the way the 2026-11-19 04:46:53Z landing of
// item-request-cfae4676fc did with TILLR_SQLITE_TRACE=1: a stack dump of the blocked
// transaction, the lander's reason, the adapter's reason, and then the store's close trace.
// The trace on either side of the reason is more than a stderr tail holds.
func landTraceStub(t *testing.T) string {
	t.Helper()
	frame := `tillr-sqlite-stack at=2026-11-19T04:46:50.887706035Z pid=3089251 tx=2 store="/workspace/.tillr/state.db" operation="github.com/mschulkind-oss/tillr-land/land.claimLanding" elapsed=2.05131262s stage="begin" frame="database/sql.(*DB).connectionOpener(0x39e6df359450, {0xbc23c8, 0x39e6df37c1e0})"`
	closing := `tillr-sqlite at=2026-11-19T04:46:53.856135402Z pid=3089251 tx=3 store="/workspace/.tillr/state.db" operation="close" phase=writer.close.end elapsed=399.956µs error=""`
	git := `tillr-git at=2026-11-19T04:46:48.836Z pid=3089251 repo="/workspace" command="rev-parse" phase=end elapsed=1ms error=""`
	var body strings.Builder
	for i := 0; i < 40; i++ {
		body.WriteString("printf '%s\\n' '" + frame + "' >&2\n")
		body.WriteString("printf '%s\\n' '" + git + "' >&2\n")
	}
	body.WriteString("printf '%s\\n' 'tillr-land: store: beginning the write transaction: database is locked (5) (SQLITE_BUSY)' >&2\n")
	body.WriteString("printf '%s\\n' 'tillr-loop land: tillr-land pass answered exit 5: no judgment was made' >&2\n")
	for i := 0; i < 40; i++ {
		body.WriteString("printf '%s\\n' '" + closing + "' >&2\n")
	}
	body.WriteString("exit 4")
	return executable(t, body.String())
}

// The hold a failed landing records is the lander's reason. On 2026-11-19 the hold of
// item-request-cfae4676fc read "…x39e6df37c190})" — the cut-off end of a stack frame from
// the store's trace — because the step's stderr tail kept the trace lines, which outnumber
// what a program says by hundreds to one: the reason was buried in the middle, and a little
// more trace after it would have pushed it out of the tail altogether.
func TestALandFailureCarriesTheLandersReasonAndNotTheStoreTrace(t *testing.T) {
	for _, trace := range []string{"", "1"} {
		t.Run("trace="+trace, func(t *testing.T) {
			t.Setenv("TILLR_SQLITE_TRACE", trace)
			root := t.TempDir()
			captured := filepath.Join(root, "loop-stderr")
			capture, err := os.Create(captured)
			if err != nil {
				t.Fatal(err)
			}
			original := os.Stderr
			os.Stderr = capture
			steps := &ProcessSteps{Root: root, LandProgram: landTraceStub(t), Store: filepath.Join(root, "s.db")}
			_, landErr := steps.Land(context.Background(), Candidate{ItemID: "item-request-cfae4676fc", RunID: "run-item-request-cfae4676fc-a2"})
			os.Stderr = original
			capture.Close()
			if landErr == nil {
				t.Fatal("a land program that exited 4 was read as an answer")
			}
			first, _, _ := strings.Cut(landErr.Error(), "\n")
			want := "the land program answered exit 4 for item-request-cfae4676fc: tillr-land: store: beginning the write transaction: database is locked (5) (SQLITE_BUSY)"
			if first != want {
				t.Fatalf("the hold's first line is\n%q\nwant\n%q", first, want)
			}
			if !strings.Contains(landErr.Error(), "tillr-loop land: tillr-land pass answered exit 5: no judgment was made") {
				t.Fatalf("the adapter's reason is missing from %q", landErr)
			}
			for _, prefix := range []string{"tillr-sqlite", "tillr-git", "…"} {
				if strings.Contains(landErr.Error(), prefix) {
					t.Fatalf("the hold carries trace (%q): %q", prefix, landErr)
				}
			}
			raw, err := os.ReadFile(filepath.Join(RunDir(root, "run-item-request-cfae4676fc-a2"), StepsFile))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "database is locked (5) (SQLITE_BUSY)") || strings.Contains(string(raw), "tillr-sqlite-stack") {
				t.Fatalf("the land span's stderr tail is not the lander's reason: %s", raw)
			}
			// The trace is not lost: with tracing on it reaches the loop's own stderr, which
			// is where every other process's trace lines already are.
			log, err := os.ReadFile(captured)
			if err != nil {
				t.Fatal(err)
			}
			if trace == "" {
				if len(log) != 0 {
					t.Fatalf("trace-off forwarded output: %s", log)
				}
				return
			}
			for _, prefix := range []string{"tillr-sqlite-stack ", "tillr-sqlite ", "tillr-git "} {
				if !strings.Contains(string(log), prefix) {
					t.Fatalf("the land program's %q trace did not reach the loop's stderr:\n%s", prefix, log)
				}
			}
			if strings.Contains(string(log), "database is locked") {
				t.Fatalf("the loop's stderr forwarded a non-trace line: %s", log)
			}
		})
	}
}

// A trace line is recognized however its writes are split, and a line that only looks like
// trace at its start is kept.
func TestTheStderrTailLeavesOutTraceLinesSplitAcrossWrites(t *testing.T) {
	var tail stderrTail
	for _, part := range []string{"first reason\ntillr-", "sqlite pid=1 tx=2\ntill", "r-git command=x\ntillr-sqlite-lookalike kept\n", "tillr-sqlite-stack frame\nlast", " reason"} {
		if n, err := tail.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write answered %d/%v", n, err)
		}
	}
	want := "first reason\ntillr-sqlite-lookalike kept\nlast reason"
	if got := tail.String(); got != want {
		t.Fatalf("tail %q, want %q", got, want)
	}
	var short stderrTail
	_, _ = short.Write([]byte("tillr-sq"))
	if got := short.String(); got != "tillr-sq" {
		t.Fatalf("an unfinished short line %q was dropped", got)
	}
}
