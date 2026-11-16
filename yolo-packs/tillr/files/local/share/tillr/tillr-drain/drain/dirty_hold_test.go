package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The lander's real dirty-path detection is covered in tillr-land. This step
// returns its recorded refusal to exercise retention and restart in the drain.
type dirtyPathSteps struct {
	recordingSteps
	t                      *testing.T
	root                   string
	store                  string
	gates, lands, verdicts int
}

func (s *dirtyPathSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.gates++
	return GateOutcome{Outcome: GatePassed}, nil
}
func (s *dirtyPathSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	dirty := runGit(s.t, s.root, "diff", "--name-only", "HEAD")
	changed := runGit(s.t, s.root, "diff", "--name-only", c.BaseSHA, c.HeadSHA)
	var paths []string
	for _, p := range strings.Fields(changed) {
		for _, d := range strings.Fields(dirty) {
			if p == d {
				paths = append(paths, p)
			}
		}
	}
	if len(paths) > 0 {
		return LandOutcome{Result: LandRefused, Reason: "trunk-worktree-uncommitted", ConflictPaths: paths}, nil
	}
	tip := runGit(s.t, s.root, "rev-parse", "HEAD")
	tree := strings.Split(runGit(s.t, s.root, "merge-tree", "--write-tree", "--merge-base="+c.BaseSHA, tip, c.HeadSHA), "\n")[0]
	commit := runGit(s.t, s.root, "commit-tree", tree, "-p", tip, "-m", "land")
	runGit(s.t, s.root, "merge", "--ff-only", commit)
	return LandOutcome{Result: LandLanded}, nil
}
func (s *dirtyPathSteps) Verdict(context.Context, Candidate, string) error { s.verdicts++; return nil }
func (s *dirtyPathSteps) ItemState(ctx context.Context, id, runID, state string) error {
	st, err := store.Open(ctx, s.store, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id=?`, state, id)
		return err
	})
}

func TestDirtyPathWaitRetainsCandidateAcrossReplacement(t *testing.T) {
	for _, resolution := range []string{"restore", "commit"} {
		t.Run(resolution, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Lanes = []string{"lane-1"}
			steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
			l.cfg.Steps = steps
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths='["base.txt"]'`)
				return err
			})
			plan := mustPlan(t, l)[0]
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("dispatch: %v / %v", ok, err)
			}
			lane := l.lanePath("lane-1")
			writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "base.txt\n")
			writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\n")
			runGit(t, lane, "add", "base.txt")
			runGit(t, lane, "commit", "-qm", "candidate")
			c := Candidate{ItemID: "item-a", RunID: plan.decision.RunRequest.RunID, LaneID: "lane-1", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD"), Request: plan.decision.RunRequest}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				return applyTerminal(tx, run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}})
			})
			if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, "base.txt"), "owner\n")
			var result LoopResult
			if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
				t.Fatalf("judge %s / %v", stop, err)
			}
			drainOwned(t, l, &result)
			if result.Rejected != 0 || result.Landed != 0 || steps.gates != 1 || steps.verdicts != 0 {
				t.Fatalf("dirty wait refused or landed: %+v / %+v", result, steps)
			}
			replacement := f.loop(t, steps, &itemContentStager{})
			replacement.cfg.Lanes = l.cfg.Lanes
			for i := 0; i < 2; i++ {
				plans := mustPlan(t, replacement)
				d := plans[0].decision
				if d.Action != ActionHold || d.Reason != "trunk-path-dirty: base.txt" {
					t.Fatalf("lost wait on replacement: %+v", d)
				}
				if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
					t.Fatalf("wait %s / %v", stop, err)
				}
				drainOwned(t, replacement, &result)
			}
			if steps.lands != 1 || steps.gates != 1 {
				t.Fatalf("unchanged dirt retried: %+v", steps)
			}
			if resolution == "restore" {
				runGit(t, f.root, "restore", "base.txt")
			} else {
				// Commit the candidate's content: the next landing is now a no-op merge,
				// not a conflict between an unrelated owner edit and the candidate.
				writeForkFile(t, filepath.Join(f.root, "base.txt"), "candidate\n")
				runGit(t, f.root, "add", "base.txt")
				runGit(t, f.root, "commit", "-qm", "owner")
			}
			if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
				t.Fatalf("reopen %s / %v", stop, err)
			}
			drainOwned(t, replacement, &result)
			if result.Landed != 1 || steps.lands != 2 || steps.gates != 1 || steps.verdicts != 1 {
				t.Fatalf("wait did not land by itself: %+v / %+v", result, steps)
			}
		})
	}
}

func TestDirtyWaitUsesChangeReappliedToCurrentTrunk(t *testing.T) {
	f, l := passFixture(t, "item-a")
	lane := l.lanePath("lane-1")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\n")
	writeForkFile(t, filepath.Join(lane, "b.txt"), "candidate b\n")
	runGit(t, lane, "add", ".")
	runGit(t, lane, "commit", "-qm", "candidate")
	head := runGit(t, lane, "rev-parse", "HEAD")
	// The owner has already committed the candidate's base.txt change. A later
	// uncommitted edit there does not overlap the remaining b.txt landing.
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "candidate\n")
	runGit(t, f.root, "add", "base.txt")
	runGit(t, f.root, "commit", "-qm", "owner applied part")
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "owner new edit\n")
	hold := dirtyHold{Root: f.root, Candidate: Candidate{BaseSHA: base, HeadSHA: head}}
	paths, err := hold.paths(context.Background())
	if err != nil || len(paths) != 0 {
		t.Fatalf("already-applied path still blocks landing: %v / %v", paths, err)
	}
	// A conflicting committed owner change must reach the lander's conflict
	// handling, not be masked by another dirty path.
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "conflicting owner\n")
	runGit(t, f.root, "add", "base.txt")
	runGit(t, f.root, "commit", "-qm", "conflict")
	writeForkFile(t, filepath.Join(f.root, "b.txt"), "dirty owner b\n")
	paths, err = hold.paths(context.Background())
	if err != nil || len(paths) != 0 {
		t.Fatalf("dirt masks a squash conflict: %v / %v", paths, err)
	}
}

func TestDirtyWaitRecordsChangedAndReturningPathSets(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for i, reason := range []string{"trunk-path-dirty: a.txt", "trunk-path-dirty: a.txt", "trunk-path-dirty: a.txt, b.txt", "trunk-path-dirty: a.txt"} {
		fixtureWrite(t, f, func(tx *store.Tx) error {
			d := Decision{ItemID: "item-a", From: "candidate", Action: ActionHold, Reason: reason}
			identifyDecision(&d)
			return recordDirtyDecision(tx, d, start.Add(time.Duration(i)*time.Minute))
		})
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT reason,dispatched_at FROM dispatches WHERE item_id='item-a' AND reason LIKE 'trunk-path-dirty:%' ORDER BY dispatched_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var got []int64
		for rows.Next() {
			var reason string
			var at int64
			if err := rows.Scan(&reason, &at); err != nil {
				return err
			}
			got = append(got, at)
		}
		if len(got) != 3 || got[0] != start.UnixMilli() || got[1] != start.Add(2*time.Minute).UnixMilli() || got[2] != start.Add(3*time.Minute).UnixMilli() {
			t.Fatalf("wait streaks not retained: %v", got)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnrelatedDirtyPathDoesNotDelayLanding(t *testing.T) {
	f, l := passFixture(t, "item-a")
	steps := &dirtyPathSteps{t: t, root: f.root, store: f.storePath}
	l.cfg.Steps = steps
	base := runGit(t, f.root, "rev-parse", "HEAD")
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(lane, "candidate.txt"), "candidate\n")
	runGit(t, lane, "add", "candidate.txt")
	runGit(t, lane, "commit", "-qm", "candidate")
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "owner\n")
	var result LoopResult
	stop, landed, err := l.Verify(context.Background(), Candidate{ItemID: "item-a", RunID: "run-a", LaneID: "lane-1", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}, &result, nil)
	if err != nil || stop != "" || !landed {
		t.Fatalf("unrelated dirt delayed landing: %s / %v / %v", stop, landed, err)
	}
	if got := runGit(t, f.root, "diff", "--name-only"); got != "base.txt" {
		t.Fatalf("owner edit lost: %q", got)
	}
}
