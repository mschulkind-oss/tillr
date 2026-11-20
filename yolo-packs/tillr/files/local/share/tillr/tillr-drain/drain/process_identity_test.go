package drain

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// inheritedLoopStore is the TILLR_LOOP_STORE this test process was started under, kept for
// the test below. It is the owner's real board whenever a worker runs the suite.
var inheritedLoopStore = os.Getenv("TILLR_LOOP_STORE")

// init gives this test process a run-process identity of its own before any test runs.
//
// tillr-run ends a run's leftover processes by sweeping /proc for those carrying the run's
// TILLR_RUN_ID and the TILLR_LOOP_STORE the sweeper was itself started under. A worker's
// environment names the real board's store, so every test process on the machine inherited
// the same store, and the fixtures all name the same runs (run-item-a-a2, ...). A sweep in
// one test then matched and killed another test process's harness: on 2026-11-20 three
// concurrent copies of this suite each lost harnesses to a sibling's sweep, and
// TestLandingCallDeliversRefusalsAsToolResults failed in five of six concurrent runs
// (exit -1, the harness killed) and in none alone, on the unmodified tree as well. A store
// identity no other process shares makes a sweep reach only this process's own runs. The
// fixtures that need a particular store name it for their children explicitly.
func init() {
	os.Setenv("TILLR_LOOP_STORE", fmt.Sprintf("/nonexistent/tillr-test-process-%d-%d/state.db", os.Getpid(), time.Now().UnixNano()))
}

func TestATestProcessSweepsUnderAStoreNoOtherProcessShares(t *testing.T) {
	got := os.Getenv("TILLR_LOOP_STORE")
	if got == inheritedLoopStore {
		t.Fatalf("the test process kept the inherited TILLR_LOOP_STORE %q", got)
	}
	if !strings.Contains(got, fmt.Sprint(os.Getpid())) {
		t.Fatalf("TILLR_LOOP_STORE %q does not name this process", got)
	}
}
