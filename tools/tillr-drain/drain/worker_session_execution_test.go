package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func returnedSessionFixture(t *testing.T, cause string) (refusalLoopFixture, *Loop, plannedApplication, string) {
	t.Helper()
	return returnedSessionFixtureWith(t, cause, "dead anchor")
}

func returnedSessionFixtureWith(t *testing.T, cause, gateOutput string) (refusalLoopFixture, *Loop, plannedApplication, string) {
	t.Helper()
	f, l := designSeatFixture(t)
	commitDenyListFixture(t, f.root)
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]' WHERE item_id='item-a'`)
		return err
	})
	first := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	dir := l.laneDirPath(first.decision.LaneID)
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	target := l.laneTarget(first.decision.LaneID)
	if cause != RefusalNoOp {
		writeForkFile(t, filepath.Join(target, "candidate.txt"), "first\n")
		runGit(t, target, "add", "candidate.txt")
		runGit(t, target, "commit", "-qm", "first")
	}
	finishRefused(t, f, l, first, cause, "fixture refusal", "documents", gateOutput)
	if stop := l.abandon(context.Background(), first.decision.LaneID, first.decision.RunRequest.RunID, runGit(t, target, "rev-parse", "HEAD")); stop != "" {
		t.Fatal(stop)
	}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	return f, l, first, base
}

func applyReturnedTurn(t *testing.T, l *Loop, p plannedApplication, base string) Decision {
	t.Helper()
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply returned turn: %v %v", ok, err)
	}
	staged, err := l.stageDispatches(context.Background(), []Decision{p.decision}, base, &LoopResult{})
	if err != nil || len(staged) != 1 {
		t.Fatalf("stage returned turn: %v %+v", err, staged)
	}
	return staged[0]
}

type returnedLandingSteps struct {
	*continuationSteps
	root string
}

func (s *returnedLandingSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	runGit(s.t, s.root, "cherry-pick", c.BaseSHA+".."+c.HeadSHA)
	return LandOutcome{Result: LandLanded}, nil
}

func TestReturnedSessionTurn(t *testing.T) {
	for _, cause := range []string{RefusalGate, RefusalNoOp, RefusalFence, RefusalLand} {
		t.Run(cause, func(t *testing.T) {
			f, l, first, base := returnedSessionFixture(t, cause)
			p := mustPlan(t, l)[0]
			if !p.decision.RunRequest.SessionTurn || p.decision.WorkerSessionReason != "resumed-worker" {
				t.Fatalf("not a session turn: %+v", p.decision)
			}
			script := `test "$TILLR_SESSION_MODE" = resume || exit 71
case "$(cat ../session-id)" in 11111111-1111-4111-8111-111111111111) ;; *) exit 72 ;; esac
case "$(cat "$TILLR_RESUME_PROMPT_FILE")" in *"fixture refusal"*"current run identity"*) ;; *) exit 73 ;; esac
printf 'second\n' > candidate.txt
git add candidate.txt
git commit -qm second
`
			p.decision.RunRequest.Command = []string{continuationShell(t), "-c", script}
			// The old session has no pin, and the adapter now supports calls. It
			// must still receive the refusal as a message, under a footer brief.
			l.endingCapabilities = map[string]string{p.decision.RunRequest.Command[0]: run.EndingCallSuspends}
			d := applyReturnedTurn(t, l, p, base)
			if got, err := stagedEndingCapability(RunDir(f.root, d.RunRequest.RunID)); err != nil || got != run.EndingFooter {
				t.Fatalf("legacy returned turn was not staged as footer: %q %v", got, err)
			}
			if cause == RefusalGate {
				if !strings.Contains(d.RunRequest.ResumePrompt, "documents") || !strings.Contains(d.RunRequest.ResumePrompt, "documents.log") {
					t.Fatal(d.RunRequest.ResumePrompt)
				}
			}
			steps := &returnedLandingSteps{continuationSteps: &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}, root: f.root}
			l.cfg.Steps = steps
			rec, log, err := steps.Run(context.Background(), *d.RunRequest)
			if err != nil {
				t.Fatal(err)
			}
			result := &LoopResult{}
			outcome := l.settle(context.Background(), d, rec, log, base, "tick", l.cfg.Now(), result, nil, nil, false)
			if outcome.err != nil || outcome.stop != "" {
				t.Fatalf("settle: %+v", outcome)
			}
			if steps.gates != 1 || steps.lands != 1 {
				t.Fatalf("second commit not gated and landed: %+v", steps)
			}
			if got := runGit(t, f.root, "show", TrunkRef+":candidate.txt"); strings.TrimSpace(got) != "second" {
				t.Fatal(got)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var records, resumes int
				if err := r.QueryRow(`SELECT count(*) FROM runs WHERE item_id='item-a'`).Scan(&records); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, run.ResumeKind).Scan(&resumes); err != nil {
					return err
				}
				if records != 2 || resumes != 0 {
					t.Errorf("distinct returned records=%d resumes=%d", records, resumes)
				}
				return nil
			})
			if d.RunRequest.RunID == first.decision.RunRequest.RunID {
				t.Fatal("refusal record overwritten")
			}
		})
	}
}

func TestReturnedWorkerFreshFallback(t *testing.T) {
	for _, why := range []string{"cannot-resume", "session-missing"} {
		t.Run(why, func(t *testing.T) {
			f, l, _, base := returnedSessionFixture(t, RefusalGate)
			if why == "cannot-resume" {
				adapter := filepath.Join(scratchUnderDurable(t), "adapter", "run-fixture")
				if err := os.MkdirAll(filepath.Dir(adapter), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, adapter, "#!/bin/sh\nexit 0\n")
				writeForkFile(t, filepath.Join(filepath.Dir(adapter), "pause-kind"), run.PauseCannotResume+"\n")
				l.cfg.Policy.Backends["fixture"] = Backend{Adapter: adapter}
			} else if err := os.Remove(filepath.Join(l.laneDirPath("lane-1"), SessionIDFile)); err != nil {
				t.Fatal(err)
			}
			p := mustPlan(t, l)[0]
			if p.decision.WorkerSessionReason != why || p.decision.RunRequest.SessionTurn {
				t.Fatalf("fallback not explained: %+v", p.decision)
			}
			d := applyReturnedTurn(t, l, p, base)
			id, err := ReadSessionID(l.laneDirPath(d.LaneID))
			if err != nil || id == "11111111-1111-4111-8111-111111111111" {
				t.Fatalf("not fresh: %s %v", id, err)
			}
			brief, err := os.ReadFile(filepath.Join(l.laneDirPath(d.LaneID), BriefFile))
			if err != nil || !strings.Contains(string(brief), "documents.log") || !strings.Contains(string(brief), "fixture refusal") {
				t.Fatalf("fallback lost evidence: %v %s", err, brief)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, d.RunRequest.RunID).Scan(&raw); err != nil {
					return err
				}
				var recorded pendingWork
				if err := json.Unmarshal(raw, &recorded); err != nil {
					return err
				}
				if recorded.Decision.WorkerSessionReason != why {
					t.Errorf("decision lost fallback: %s", raw)
				}
				return nil
			})
		})
	}
}

func TestReturnedSessionIdenticalRepeat(t *testing.T) {
	f, l, _, base := returnedSessionFixture(t, RefusalGate)
	steps := &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}
	l.cfg.Steps = steps
	previous := ""
	for i := 0; i < 2; i++ {
		p := mustPlan(t, l)[0]
		if !p.decision.RunRequest.SessionTurn {
			t.Fatal("session was not continued")
		}
		p.decision.RunRequest.Command = []string{continuationShell(t), "-c", `test "$TILLR_SESSION_MODE" = resume || exit 71
mkdir -p .forks
printf '%s\n' "$TILLR_RUN_ID" > .forks/outside.txt
git add -f .forks/outside.txt
git commit -qm escaped
`}
		d := applyReturnedTurn(t, l, p, base)
		session, err := ReadSessionID(l.laneDirPath(d.LaneID))
		if err != nil || session != "11111111-1111-4111-8111-111111111111" {
			t.Fatalf("different session: %s %v", session, err)
		}
		rec, log, err := steps.Run(context.Background(), *d.RunRequest)
		if err != nil {
			t.Fatal(err)
		}
		outcome := l.settle(context.Background(), d, rec, log, base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
		if outcome.err != nil || outcome.stop != "" {
			t.Fatalf("settle: %+v", outcome)
		}
		if err := l.markExamined(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			previous = d.RunRequest.RunID
		} else {
			c := itemConflict(t, f)
			if c.WaitingOn != "design" || !strings.Contains(c.Text, previous) || !strings.Contains(c.Text, d.RunRequest.RunID) {
				t.Fatalf("repeat lost both turn records: %+v", c)
			}
			if next := mustPlan(t, l)[0].decision; next.Seat != "design" {
				t.Fatalf("repeat did not go to design: %+v", next)
			}
			target := l.laneTarget(d.LaneID)
			writeForkFile(t, filepath.Join(target, "designer.txt"), "new owner\n")
			runGit(t, target, "add", "designer.txt")
			runGit(t, target, "commit", "-qm", "designer work")
			head := runGit(t, target, "rev-parse", "HEAD")
			if err := l.markExamined(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
				t.Fatalf("replayed examination reset new owner: %s", got)
			}
		}
	}
}
