package drain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type schedulingSteps struct {
	recordingSteps
	t       *testing.T
	fixture refusalLoopFixture
	wall    bool
	blocked chan struct{}
	release chan struct{}
	third   chan struct{}
}

func (s *schedulingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.ItemID == "item-a" && req.Resume {
		close(s.blocked)
		select {
		case <-s.release:
		case <-ctx.Done():
			return run.Record{}, "", ctx.Err()
		}
	}
	if req.ItemID == "item-c" {
		close(s.third)
	}
	path := filepath.Join(s.fixture.laneRoot, req.LaneID, WorktreeDir)
	writeForkFile(s.t, filepath.Join(path, "candidate.txt"), "candidate\n")
	runGit(s.t, path, "add", "candidate.txt")
	runGit(s.t, path, "commit", "-qm", "candidate")
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, Resumed: req.Resume, ExitCode: countPtr(0), StartedAt: time.Now().UTC().Format(time.RFC3339), FinishedAt: time.Now().UTC().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}}
	st, err := store.Open(ctx, s.fixture.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}

func (s *schedulingSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if c.ItemID == "item-a" && !s.wall {
		close(s.blocked)
		select {
		case <-s.release:
		case <-ctx.Done():
			return GateOutcome{}, ctx.Err()
		}
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func TestABlockedContinuationOrGateDoesNotHoldASiblingsNextItem(t *testing.T) {
	for _, wall := range []bool{true, false} {
		name := "gate"
		if wall {
			name = "continuation"
		}
		t.Run(name, func(t *testing.T) {
			f, l := passFixture(t, "item-a", "item-b", "item-c")
			steps := &schedulingSteps{t: t, fixture: f, wall: wall, blocked: make(chan struct{}), release: make(chan struct{}), third: make(chan struct{})}
			l.cfg.Steps = steps
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt"]'`)
				return err
			})
			plans := mustPlan(t, l)
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			for i := 0; i < 2; i++ {
				d := plans[i].decision
				ok, _, err := l.applyPlan(context.Background(), plans[i], base, l.cfg.Now())
				if err != nil || !ok {
					t.Fatalf("dispatch %v / %v", ok, err)
				}
				dir := l.laneDirPath(d.LaneID)
				writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
				writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
				state := run.StateExited
				exit := 0
				if i == 0 && wall {
					state = run.StateTimedOut
					exit = 124
					writeForkFile(t, filepath.Join(dir, BriefFile), "the run's brief\n")
					writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
				} else {
					path := l.lanePath(d.LaneID)
					writeForkFile(t, filepath.Join(path, "candidate.txt"), "candidate\n")
					runGit(t, path, "add", "candidate.txt")
					runGit(t, path, "commit", "-qm", "candidate")
				}
				exitCode := countPtr(exit)
				if state == run.StateTimedOut {
					exitCode = nil
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: state, ExitCode: exitCode, StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
				})
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ended := make(chan laneOutcome, 1)
			go func() { result, err := l.Run(ctx); ended <- laneOutcome{stop: result.Stop, err: err} }()
			select {
			case <-steps.blocked:
			case <-time.After(hangGuard):
				cancel()
				close(steps.release)
				<-ended
				t.Fatal("examination did not block")
			}
			thirdDispatched := false
			select {
			case <-steps.third:
				thirdDispatched = true
			case <-time.After(hangGuard):
			}
			close(steps.release)
			select {
			case outcome := <-ended:
				if outcome.err != nil || outcome.stop != StopQueueDrained {
					t.Fatalf("loop %s / %v", outcome.stop, outcome.err)
				}
			case <-time.After(hangGuard):
				cancel()
				<-ended
				t.Fatal("loop did not finish")
			}
			if !thirdDispatched {
				t.Fatal("sibling did not receive third item before blocked examination was released")
			}
		})
	}
}
