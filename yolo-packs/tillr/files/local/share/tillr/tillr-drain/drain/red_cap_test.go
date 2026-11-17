package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/redcap"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// redCapSteps models the locked lander's refusal after checking for an empty
// squash. The real Git/lock behavior is covered by tillr-land's cap tests.
type redCapSteps struct {
	*dirtyPathSteps
	now func() time.Time
}

func (s *redCapSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	tip := runGit(s.t, s.root, "rev-parse", TrunkRef)
	tree := strings.Split(runGit(s.t, s.root, "merge-tree", "--write-tree", "--merge-base="+c.BaseSHA, tip, c.HeadSHA), "\n")[0]
	if tree != runGit(s.t, s.root, "rev-parse", tip+"^{tree}") {
		reader := openFixture(s.t, s.store)
		var cap *redcap.Hold
		err := reader.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			cap, err = redcap.Check(ctx, r, s.root, TrunkRef, tip, c.ItemID, s.now())
			return err
		})
		reader.Close()
		if err != nil {
			return LandOutcome{}, err
		}
		if cap != nil {
			s.lands++
			return LandOutcome{Result: LandRefused, Reason: ReasonRedCap, RedCap: cap}, nil
		}
	}
	return s.dirtyPathSteps.Land(ctx, c)
}

func prepareCapCandidate(t *testing.T, f refusalLoopFixture, l *Loop, p plannedApplication, path string) Candidate {
	t.Helper()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch %v / %v", ok, err)
	}
	lane := l.lanePath(p.decision.LaneID)
	if _, err := os.Stat(lane); os.IsNotExist(err) {
		if err := os.MkdirAll(l.laneDirPath(p.decision.LaneID), 0755); err != nil {
			t.Fatal(err)
		}
		runGit(t, f.root, "worktree", "add", "-B", LaneBranch(l.cfg.LaneRoot, p.decision.LaneID), lane, base)
	}
	runGit(t, lane, "reset", "--hard", base)
	writeForkFile(t, filepath.Join(l.laneDirPath(p.decision.LaneID), FenceFile), path+"\n")
	writeForkFile(t, filepath.Join(lane, path), "candidate\n")
	runGit(t, lane, "add", path)
	runGit(t, lane, "commit", "-qm", "candidate")
	c := Candidate{ItemID: p.decision.ItemID, RunID: p.decision.RunRequest.RunID, LaneID: p.decision.LaneID, Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD"), Request: p.decision.RunRequest}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{}})
	})
	if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", c.RunID, c, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRedCapSupersededCandidateSettlesWithoutGreenMovement(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(strconv.FormatBool(held), func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			steps := &redCapSteps{dirtyPathSteps: &dirtyPathSteps{t: t, root: f.root, store: f.storePath}, now: func() time.Time { return l.cfg.Now() }}
			l.cfg.Steps = steps
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths='["ordinary.txt"]' WHERE item_id='item-a'`)
				return err
			})
			c := prepareCapCandidate(t, f, l, mustPlan(t, l)[0], "ordinary.txt")
			green := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, f.root, "update-ref", GreenRef, green)
			writeForkFile(t, filepath.Join(f.root, "red.txt"), "red\n")
			runGit(t, f.root, "add", "red.txt")
			runGit(t, f.root, "commit", "-qm", "red landing")
			seconds, err := strconv.ParseInt(runGit(t, f.root, "show", "-s", "--format=%ct", green), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			l.cfg.Now = func() time.Time { return time.Unix(seconds+121, 0).UTC() }
			if err := os.MkdirAll(filepath.Join(f.root, "config/tillr"), 0755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(f.root, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`)
			var result LoopResult
			if held {
				if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
					t.Fatalf("hold: %s / %v", stop, err)
				}
				drainOwned(t, l, &result)
				if steps.lands != 1 || steps.verdicts != 0 {
					t.Fatalf("not cap held: %+v", steps)
				}
			}
			// The owner applies the exact change while the proof pointer stays behind.
			runGit(t, f.root, "cherry-pick", c.HeadSHA)
			if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
				t.Fatalf("supersession: %s / %v", stop, err)
			}
			drainOwned(t, l, &result)
			wantLands := 1
			if held {
				wantLands++
			}
			if result.Rejected != 0 || result.Landed != 0 || steps.gates != 1 || steps.lands != wantLands || steps.verdicts != 1 || itemStateOf(t, f.storePath, c.ItemID) != StateResolvedElsewhere {
				t.Fatalf("superseded work did not settle: %+v / %+v", result, steps)
			}
			if runGit(t, f.root, "rev-parse", GreenRef) != green {
				t.Fatal("settlement required proof movement")
			}
			reader := openFixture(t, f.storePath)
			err = reader.ReadTx(context.Background(), func(r *store.Reader) error {
				works, err := readPending(r)
				if err == nil && !works[c.ItemID].Done {
					t.Fatal("settled work still occupies its lane")
				}
				return err
			})
			reader.Close()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRedCapRetainsCandidateAndLandsAfterFixerWithEveryOfferedLaneOccupied(t *testing.T) {
	f, l := passFixture(t, "item-a", "spare")
	l.cfg.Lanes = []string{"lane-1"}
	steps := &redCapSteps{dirtyPathSteps: &dirtyPathSteps{t: t, root: f.root, store: f.storePath}, now: func() time.Time { return l.cfg.Now() }}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["ordinary.txt"]' WHERE item_id='item-a'; UPDATE items SET state='closed' WHERE item_id='spare'`)
		return err
	})
	c := prepareCapCandidate(t, f, l, mustPlan(t, l)[0], "ordinary.txt")
	green := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, green)
	writeForkFile(t, filepath.Join(f.root, "red.txt"), "red\n")
	runGit(t, f.root, "add", "red.txt")
	runGit(t, f.root, "commit", "-qm", "red landing")
	red := runGit(t, f.root, "rev-parse", TrunkRef)
	seconds, err := strconv.ParseInt(runGit(t, f.root, "show", "-s", "--format=%ct", green), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	l.cfg.Now = func() time.Time { return time.Unix(seconds+121, 0).UTC() }
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/gate-policy.json"), `{"landing_strategy":"after-merge","red_cap":{"landings":0,"minutes":1}}`)
	common := runGit(t, f.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err := os.MkdirAll(filepath.Join(common, "tillr-gate"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(common, "tillr-gate/heavy.json"), `{"tip_sha":"`+red+`","result":"red","checks":[{"check_id":"unit","result":"fail","first_red_landing":"`+red+`"}]}`)
	fixer := TrunkFixerID(red)
	s, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, s, fixer)
	s.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["fixer.txt"]',state='blocked' WHERE item_id=?`, fixer)
		return err
	})
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("hold %s / %v", stop, err)
	}
	drainOwned(t, l, &result)
	if result.Rejected != 0 || result.Landed != 0 || steps.gates != 1 || steps.lands != 1 || steps.verdicts != 0 {
		t.Fatalf("hold convicted or landed: %+v / %+v", result, steps)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id=?`, fixer)
		return err
	})
	// Reconstruct the loop from events. Keep the heavy runner outside this deterministic
	// pass test; publishing its proved pointer is simulated below, after a real fixer move.
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.gatePolicy.LandingStrategy = "before-merge"
	replacement.cfg.Lanes = l.cfg.Lanes
	replacement.cfg.Now = l.cfg.Now
	plans := mustPlan(t, replacement)
	var fixerPlan plannedApplication
	seen := false
	for _, p := range plans {
		if p.decision.ItemID == c.ItemID {
			seen = true
			if p.decision.Action != ActionHold || !strings.HasPrefix(p.decision.Reason, ReasonRedCap+": ") || p.decision.DeferredBy != fixer {
				t.Fatalf("lost cap wait: %+v", p.decision)
			}
		}
		if p.decision.ItemID == fixer {
			fixerPlan = p
		}
	}
	if !seen || fixerPlan.decision.Action != ActionDispatch {
		t.Fatalf("held candidate lost or fixer held: %+v", plans)
	}
	read := openFixture(t, f.storePath)
	err = read.ReadTx(context.Background(), func(r *store.Reader) error {
		why, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339)}, l.cfg.Policy, &VantageQuestions{Root: f.root})
		if err != nil {
			return err
		}
		for _, row := range why.Rows {
			if row.ItemID == c.ItemID && (row.Mover != fixer || row.Class != "waits on work" || row.Where != "land") {
				t.Fatalf("waiting hides fixer: %+v", row)
			}
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if works[c.ItemID].Candidate.HeadSHA != c.HeadSHA || !works[c.ItemID].Gated || works[c.ItemID].Done {
			t.Fatal("candidate or verdict lost")
		}
		return nil
	})
	read.Close()
	if err != nil {
		t.Fatal(err)
	}
	prepareCapCandidate(t, f, replacement, fixerPlan, "fixer.txt")
	if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("fixer %s / %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	if result.Landed != 1 || steps.gates != 2 || steps.lands != 2 {
		t.Fatalf("fixer did not bypass cap: %+v / %+v", result, steps)
	}
	proved := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, proved)
	if stop, err := replacement.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("retry %s / %v", stop, err)
	}
	drainOwned(t, replacement, &result)
	if result.Landed != 2 || result.Rejected != 0 || steps.gates != 2 || steps.lands != 3 || steps.verdicts != 2 || itemStateOf(t, f.storePath, c.ItemID) != StateLanded {
		raw, _ := json.Marshal(result)
		t.Fatalf("candidate did not land without a run/gate: %s / %+v", raw, steps)
	}
}
