package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestBuildRestartContinuesCannotResumeBackendInFreshSessionAndJudgesOnce(t *testing.T) {
	command := `if [ -z "$TILLR_RESUME_PROMPT_FILE" ]; then echo retained > candidate.txt; exit 0; fi
 test "$TILLR_SESSION_MODE" = new || exit 11
 test "$(cat candidate.txt)" = retained || exit 12
 cat "$TILLR_RESUME_PROMPT_FILE"
 git add candidate.txt; git commit -qm candidate
 echo 'RESULT: done'`
	f, l, first, d := continuationDispatchFixture(t, command, false)
	adapter := d.RunRequest.Command[0]
	writeForkFile(t, filepath.Join(filepath.Dir(adapter), "pause-kind"), run.PauseCannotResume+"\n")
	steps := &quotaContinuationSteps{continuationSteps: continuationSteps{t: t, store: first.store, laneRoot: first.laneRoot, at: first.at}, backends: filepath.Dir(adapter)}
	l.cfg.Steps = steps
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.Restart = &RestartConfig{Executable: exe}
	l.build.Revision = "new-build"
	lane := l.laneDirPath(d.LaneID)
	before, err := ReadSessionID(lane)
	if err != nil {
		t.Fatal(err)
	}
	var paused run.Record
	fixtureRead(t, f, func(r *store.Reader) error {
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if rec != nil {
			paused = *rec
		}
		return err
	})
	paused.State, paused.EndCause, paused.ExitCode = run.StatePaused, run.EndRestart, nil
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, paused) })
	writeForkFile(t, filepath.Join(run.DurableDir(lane, d.RunRequest.RunID), "pause-notice"), "The session was interrupted by a restart.\n")
	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		settleContinuation(t, l, result)
	}
	after, err := ReadSessionID(lane)
	if err != nil {
		t.Fatal(err)
	}
	if before == after || !run.IsSessionID(after) {
		t.Fatalf("session not replaced: %s => %s", before, after)
	}
	if len(steps.requests) != 1 || !steps.requests[0].Resume || steps.requests[0].RunID != d.RunRequest.RunID {
		t.Fatalf("continuations=%+v", steps.requests)
	}
	if steps.gates != 1 || steps.lands != 1 || result.InfraFailures != 0 {
		t.Fatalf("gates=%d lands=%d result=%+v", steps.gates, steps.lands, result)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, RetryAckKind).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("restart spent %d retries", n)
		}
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if err != nil {
			return err
		}
		log := logTail(rec.ArtifactRefs)
		for _, want := range []string{"stopped for a restart", "Transcript:", "Worktree:", "RESULT: done"} {
			if !strings.Contains(log, want) {
				t.Errorf("fresh session missing %q: %s", want, log)
			}
		}
		return nil
	})
}
