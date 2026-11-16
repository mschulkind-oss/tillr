package drain

import (
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

// code answers an exit code pointer for a record.
func code(exit int) *int { return &exit }

// The classification, both directions, on the records the store can actually hold.
//
// The negative control that matters is the pair at the end: a candidate that times out
// mid-run and a harness that never started are the same shape to a careless classifier —
// non-zero exit, no report — and only one of them may spend a second backend.
func TestClassifyAttemptSeparatesMachineryFromWork(t *testing.T) {
	cases := []struct {
		name string
		rec  run.Record
		log  string
		want AttemptClass
	}{
		{
			name: "the harness could not start",
			rec:  run.Record{State: run.StateFailedToStart},
			want: AttemptInfrastructure,
		},
		{
			name: "the adapter refused before anything ran",
			rec:  run.Record{State: run.StateExited, ExitCode: code(2)},
			want: AttemptInfrastructure,
		},
		{
			name: "the harness died with no report at all",
			rec:  run.Record{State: run.StateExited, ExitCode: code(1)},
			log:  "some stream lines, no footer",
			want: AttemptInfrastructure,
		},
		{
			name: "the model was unreachable and nothing judged the item",
			rec:  run.Record{State: run.StateExited, ExitCode: code(1)},
			log:  "provider: does not exist or you lack access\n",
			want: AttemptInfrastructure,
		},
		{
			name: "the harness answered and exited cleanly",
			rec:  run.Record{State: run.StateExited, ExitCode: code(0)},
			log:  "paths changed: none\n\nRESULT: done\n",
			want: AttemptAnswered,
		},
		{
			name: "the model reported a failed answer and exited non-zero",
			rec:  run.Record{State: run.StateExited, ExitCode: code(1)},
			log:  "verification: go test ./... -> FAIL\n\nRESULT: failed\n",
			want: AttemptAnswered,
		},
		{
			name: "the adapter's own wall killed the harness",
			rec:  run.Record{State: run.StateExited, ExitCode: code(124)},
			want: AttemptUnjudged,
		},
		{
			name: "run exec's bound killed the run",
			rec:  run.Record{State: run.StateTimedOut},
			want: AttemptUnjudged,
		},
		{
			// The control: a candidate mid-work when the wall hit it. It exits non-zero
			// with no footer, exactly like a harness that never started, and it must NOT
			// be read as machinery: the tree may hold half-finished work, so it is
			// inspected rather than re-dispatched on a second backend.
			name: "a candidate that times out mid-run",
			rec:  run.Record{State: run.StateTimedOut},
			log:  "editing src/example.go\nrunning tests\n",
			want: AttemptUnjudged,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ClassifyAttempt(c.rec, c.log); got != c.want {
				t.Fatalf("ClassifyAttempt(%s, %q) = %q, want %q", c.rec.State, c.log, got, c.want)
			}
		})
	}
}
