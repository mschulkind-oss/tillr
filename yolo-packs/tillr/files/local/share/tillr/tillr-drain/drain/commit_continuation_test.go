package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type commitContinuationSteps struct {
	*continuationSteps
	resumed   int
	candidate Candidate
}

func (s *commitContinuationSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.Resume {
		s.resumed++
		db := openFixture(s.t, s.store)
		defer db.Close()
		if err := db.ReadTx(ctx, func(r *store.Reader) error {
			var count int
			if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, req.RunID).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				s.t.Fatal("first exit already settled as no-op before worker could commit")
			}
			return nil
		}); err != nil {
			s.t.Fatal(err)
		}
	}
	return s.continuationSteps.Run(ctx, req)
}
func (s *commitContinuationSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.candidate = c
	if head := runGit(s.t, c.Worktree, "rev-parse", "HEAD"); head != c.HeadSHA || head == c.BaseSHA {
		s.t.Fatalf("candidate is not continuation commit: %+v head=%s", c, head)
	}
	return s.continuationSteps.Gate(ctx, c)
}

func TestDirtyExitContinuesBeforeNoCommitRefusal(t *testing.T) {
	for _, commit := range []bool{true, false} {
		t.Run(map[bool]string{true: "commits", false: "still-dirty"}[commit], func(t *testing.T) {
			command := `if [ "$TILLR_SESSION_MODE" = new ]; then echo work > candidate.txt; git add candidate.txt; exit 0; fi
IFS= read -r prompt < "$TILLR_RESUME_PROMPT_FILE"
case "$prompt" in *candidate.txt*"one requested commit"*) ;; *) exit 71 ;; esac
`
			if commit {
				command += "git commit -qm finished\n"
			} else {
				command += "exit 0\n"
			}
			f, l, original, d := continuationDispatchFixture(t, command, false)
			steps := &commitContinuationSteps{continuationSteps: original}
			l.cfg.Steps = steps
			var result LoopResult
			settleContinuation(t, l, &result)
			if commit {
				settleContinuation(t, l, &result)
			}
			// The still-dirty worker's one continuation changed nothing, so it is an identical
			// repeat and is refused; the worker that committed needed only the one.
			if steps.resumed != 1 {
				t.Fatalf("worker given %d continuations, want one before refusal", steps.resumed)
			}
			db := openFixture(t, f.storePath)
			defer db.Close()
			if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
				var refusals int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, d.RunRequest.RunID).Scan(&refusals); err != nil {
					return err
				}
				if commit {
					if refusals != 0 || steps.gates != 1 || steps.lands != 1 {
						t.Fatalf("committed continuation not settled: refusals=%d gates=%d lands=%d", refusals, steps.gates, steps.lands)
					}
					if got := runGit(t, steps.candidate.Worktree, "show", steps.candidate.HeadSHA+":candidate.txt"); strings.TrimSpace(got) != "work" {
						t.Fatalf("lost work: %q", got)
					}
				} else {
					var body []byte
					if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, d.RunRequest.RunID).Scan(&body); err != nil {
						return err
					}
					var refusal refusalPayload
					if err := json.Unmarshal(body, &refusal); err != nil {
						return err
					}
					if refusal.Step != RefusalNoOp || !strings.Contains(refusal.Detail, "candidate.txt") {
						t.Fatalf("refusal hides uncommitted work: %s", body)
					}
					if steps.gates != 0 || steps.lands != 0 {
						t.Fatal("uncommitted work reached gate")
					}
				}
				var resumes int
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`, run.ResumeKind, d.RunRequest.RunID).Scan(&resumes); err != nil {
					return err
				}
				if resumes != 1 {
					t.Fatalf("resume lifecycle count=%d", resumes)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if commit {
				// A finished candidate must not execute again on later passes.
				settleContinuation(t, l, &result)
				if steps.resumed != 1 {
					t.Fatal("unbounded continuation")
				}
				return
			}
			// The refused run is not continued again: the item goes back to its worker as a
			// fresh run with the ending in its brief.
			if d := mustPlan(t, l)[0].decision; d.Reason != ReasonReturned || d.Returned == nil || d.Returned.Cause != RefusalNoOp {
				t.Fatalf("refused run not returned to its worker: %+v", d)
			}
		})
	}
}

// A continuation a stop killed is continued again by the replacement loop, uncounted (the
// owner's ruling of 2026-11-17); the identical-repeat rule then ends it when that segment
// leaves the work as it found it.
func TestDirtyExitContinuesAKilledContinuationAgain(t *testing.T) {
	f, l, original, d := continuationDispatchFixture(t, "echo work > candidate.txt", false)
	// The continued face persisted its claim, then died before run.finish. The
	// dead supervisor's lease has been released; the only finish is the first exit.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, run.ResumeKind, d.RunRequest.RunID, map[string]string{
			"run_id": d.RunRequest.RunID, "item_id": d.ItemID, "lane_id": d.LaneID,
			"backend": d.RunRequest.Backend, "session_id": "33b5f2d2-0cd4-4a49-b293-c58f5635561f",
			"resumed_at": original.at.Add(time.Second).Format(time.RFC3339Nano),
		}, original.at.Add(time.Second))
	})
	steps := &commitContinuationSteps{continuationSteps: original}
	cfg := l.cfg
	cfg.Steps = steps
	replacement, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var result LoopResult
	settleContinuation(t, replacement, &result)
	if steps.resumed != 1 || result.RefusedSteps[RefusalNoOp] != 1 {
		t.Fatalf("replacement did not continue the killed continuation once and refuse the repeat: resumes=%d refused=%v", steps.resumed, result.RefusedSteps)
	}
	db := openFixture(t, f.storePath)
	defer db.Close()
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		var body string
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, d.RunRequest.RunID).Scan(&body); err != nil {
			return err
		}
		if !strings.Contains(body, "candidate.txt") || !strings.Contains(body, "continuation") {
			t.Fatalf("crashed continuation lost diagnostic: %s", body)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanExitDoesNotContinue(t *testing.T) {
	_, l, original, _ := continuationDispatchFixture(t, "exit 0", false)
	steps := &commitContinuationSteps{continuationSteps: original}
	l.cfg.Steps = steps
	var result LoopResult
	settleContinuation(t, l, &result)
	if steps.resumed != 0 || result.RefusedSteps[RefusalNoOp] != 1 {
		t.Fatalf("clean no-op resumed: %+v", result)
	}
}

func TestDirtyExitWithoutSessionNamesWork(t *testing.T) {
	f, l, original, d := continuationDispatchFixture(t, "echo work > candidate.txt", false)
	if err := os.Remove(filepath.Join(l.laneDirPath("lane-1"), SessionIDFile)); err != nil {
		t.Fatal(err)
	}
	steps := &commitContinuationSteps{continuationSteps: original}
	l.cfg.Steps = steps
	var result LoopResult
	settleContinuation(t, l, &result)
	if steps.resumed != 0 {
		t.Fatal("invented a session")
	}
	db := openFixture(t, f.storePath)
	defer db.Close()
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		var body string
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, d.RunRequest.RunID).Scan(&body); err != nil {
			return err
		}
		if !strings.Contains(body, "candidate.txt") || !strings.Contains(body, "session") {
			t.Fatalf("missing work/session diagnostic: %s", body)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The owner ruled on 2026-11-17 that uncommitted fenced work is continued uncounted (audit
// finding F-09): a session that changes its work on every continuation is continued again
// until it commits, and only a continuation that leaves the work as it was ends it.
func TestDirtyExitContinuesForAsLongAsEachContinuationChangesTheWork(t *testing.T) {
	// Use the fixture's global Git identity, as the landing check rejects local overrides.
	command := `set -e
# If a commit failed, do not keep changing the file forever on retries.
if [ -f candidate.txt ] && [ "$(wc -l < candidate.txt)" -ge 4 ]; then exit 1; fi
echo step >> candidate.txt
if [ "$TILLR_SESSION_MODE" = new ]; then exit 0; fi
if [ "$(wc -l < candidate.txt)" -ge 4 ]; then git add candidate.txt; git commit -qm finished; fi
exit 0
`
	_, l, original, _ := continuationDispatchFixture(t, command, false)
	steps := &commitContinuationSteps{continuationSteps: original}
	l.cfg.Steps = steps
	var result LoopResult
	settleContinuation(t, l, &result)
	settleContinuation(t, l, &result)
	if steps.resumed != 3 || steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("continuations=%d gates=%d lands=%d, want three continuations and one landing", steps.resumed, steps.gates, steps.lands)
	}
	if got := runGit(t, steps.candidate.Worktree, "show", steps.candidate.HeadSHA+":candidate.txt"); strings.Count(got, "step") != 4 {
		t.Fatalf("lost a segment's work: %q", got)
	}
}
