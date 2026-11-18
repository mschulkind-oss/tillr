package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func savedFixture(t *testing.T) (refusalLoopFixture, *Loop, Decision, run.Record) {
	t.Helper()
	f, l := passFixture(t, "item-a")
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("apply: %v / %v", ok, err)
	}
	d := plan.decision
	rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	writeForkFile(t, filepath.Join(l.laneTarget(d.LaneID), "base.txt"), "earlier work\n")
	return f, l, d, rec
}

func TestSettlementSavesBlockedNoCandidateAndOrphanedWork(t *testing.T) {
	for _, cause := range []string{"blocked", "no-candidate", "orphaned"} {
		t.Run(cause, func(t *testing.T) {
			f, l, d, rec := savedFixture(t)
			log := "RESULT: done\n"
			want := "ended without a candidate commit"
			if cause == "blocked" {
				log = "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: the block contradicts the tree\nRESULT: blocked\n"
				want = "reported RESULT: blocked"
			}
			if cause == "orphaned" {
				want = "orphaned by a loop stop"
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return appendEffect(tx, loopOrphanedKind, rec.RunID, struct{}{}, l.cfg.Now())
				})
			}
			out := l.settle(context.Background(), d, rec, log, runGit(t, f.root, "rev-parse", TrunkRef), "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, true)
			if out.err != nil || out.stop != "" {
				t.Fatalf("settle: %+v", out)
			}
			ref := preservedPrefix + rec.RunID
			if got := runGit(t, f.root, "show", ref+":base.txt"); got != "earlier work" {
				t.Fatal(got)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				saved, err := ReadPreservedWork(r)
				if err != nil {
					return err
				}
				if len(saved) != 1 || saved[0].Reason != want {
					t.Fatalf("saved: %+v", saved)
				}
				var n int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind IN ('gate.verdict','drain.candidate-ready')`).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Fatalf("preserved work was a candidate: %d", n)
				}
				return nil
			})
		})
	}
}

func TestPreservationBriefsAndRetention(t *testing.T) {
	for _, state := range []string{"landed", "closed"} {
		t.Run(state, func(t *testing.T) {
			f, l, d, _ := savedFixture(t)
			if stop := l.abandon(context.Background(), d.LaneID, d.RunRequest.RunID, l.headOf(context.Background(), d.LaneID), "reported RESULT: blocked"); stop != "" {
				t.Fatal(stop)
			}
			ref := preservedPrefix + d.RunRequest.RunID
			stage := &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
			for i, conflict := range []*item.Conflict{nil, {ItemID: d.ItemID, RunID: d.RunRequest.RunID, ConflictID: "conflict-a", BodyRef: "briefs/item-a.md", Text: "cannot finish"}} {
				spec := StageSpec{ItemID: d.ItemID, RunID: fmt.Sprintf("retry-%d", i), LaneID: d.LaneID, Backend: "fixture", WorkingDir: WorktreeDir, BaseSHA: runGit(t, f.root, "rev-parse", TrunkRef), BlockConflict: conflict}
				if err := stage.Stage(context.Background(), spec); err != nil {
					t.Fatal(err)
				}
				brief, err := os.ReadFile(filepath.Join(f.laneRoot, d.LaneID, BriefFile))
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{ref, "Earlier work exists", "reported RESULT: blocked", "1 files, +1 -1"} {
					if !strings.Contains(string(brief), want) {
						t.Fatalf("brief omits %q: %s", want, brief)
					}
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, state, d.ItemID)
				return err
			})
			if err := l.prunePreservedWork(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := runGit(t, f.root, "for-each-ref", ref); got != "" {
				t.Fatalf("%s retained ref: %s", state, got)
			}
			facts, err := preservedFacts(context.Background(), f.storePath, StageSpec{ItemID: d.ItemID})
			if err != nil {
				t.Fatal(err)
			}
			if facts != "" {
				t.Fatalf("brief claims deleted work exists: %s", facts)
			}
		})
	}
}

func TestFailoverPreservesBothRunsWork(t *testing.T) {
	f, l, d, rec := savedFixture(t)
	ctx := context.Background()
	l.cfg.Policy.Backends["other"] = Backend{Adapter: "other"}
	l.cfg.Policy.Preference = []string{"fixture", "other"}
	l.cfg.Steps = &callbackRunSteps{run: func(req run.Request) (run.Record, string, error) {
		writeForkFile(t, filepath.Join(l.laneTarget(d.LaneID), "base.txt"), "failover work\n")
		next := rec
		next.RunID, next.Backend, next.State = req.RunID, req.Backend, run.StateExited
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, next) })
		return next, "RESULT: done\n", nil
	}}
	rec.State = run.StateFailedToStart
	out := l.settle(ctx, d, rec, "", runGit(t, f.root, "rev-parse", TrunkRef), "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, false)
	if out.err != nil || out.stop != "" {
		t.Fatalf("settle failover: %+v", out)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		saved, err := ReadPreservedWork(r)
		if err != nil {
			return err
		}
		if len(saved) != 2 || saved[0].RunID == saved[1].RunID {
			t.Fatalf("failover overwrote the earlier run's snapshot: %+v", saved)
		}
		for i, want := range []string{"earlier work", "failover work"} {
			if got := runGit(t, f.root, "show", saved[i].Ref+":base.txt"); got != want {
				t.Fatalf("run %s preserved %q, want %q", saved[i].RunID, got, want)
			}
		}
		return nil
	})
}

func TestPreservationReplayAfterRefBeforeEvent(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	ctx := context.Background()
	ref := preservedPrefix + d.RunRequest.RunID
	p := PreservedWork{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Ref: ref, Repository: f.root, Reason: "blocked", Summary: "1 files, +1 -1 lines"}
	body, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	lane := l.laneTarget(d.LaneID)
	runGit(t, lane, "add", "base.txt")
	tree := runGit(t, lane, "write-tree")
	sha := runGit(t, lane, "commit-tree", tree, "-p", "HEAD", "-m", string(body))
	runGit(t, lane, "reset", "--mixed", "HEAD")
	// The crashed loop created its ref but never wrote a preservation event.
	runGit(t, f.root, "update-ref", ref, sha)
	if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, l.headOf(ctx, d.LaneID)); stop != "" {
		t.Fatal(stop)
	}
	if got := runGit(t, f.root, "rev-parse", ref); got != sha {
		t.Fatalf("replay replaced original snapshot: %s / %s", got, sha)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		p, err := ReadPreservedWork(r)
		if err == nil && (len(p) != 1 || p[0].Reason != "blocked") {
			t.Fatalf("recovered record: %+v", p)
		}
		return err
	})
}

func TestPreservationFailureDoesNotResetOrExamine(t *testing.T) {
	f, l, d, rec := savedFixture(t)
	dir := scratchUnderDurable(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := fmt.Sprintf("#!/bin/sh\ncase \"$3\" in commit-tree) echo snapshot-write-failed >&2; exit 99;; esac\nexec %q \"$@\"\n", realGit)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out := l.settle(context.Background(), d, rec, "RESULT: done\n", runGit(t, f.root, "rev-parse", TrunkRef), "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, true)
	if out.stop != stopLaneRetained {
		t.Fatalf("preservation failure did not retain the lane: %+v", out)
	}
	body, err := os.ReadFile(filepath.Join(l.laneTarget(d.LaneID), "base.txt"))
	if err != nil || string(body) != "earlier work\n" {
		t.Fatalf("reset discarded work despite failed preservation: %s / %v", body, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = ?`, examinationDoneKind).Scan(&n)
		if err == nil && n != 0 {
			t.Fatalf("failed preservation was examined: %d", n)
		}
		return err
	})
}

// The wrapper tries BEGIN IMMEDIATE before every Git invocation. It succeeds
// only when the loop holds no SQLite write transaction; the negative control
// deliberately holds one and proves the same probe refuses Git.
func TestPreservationGitOutsideWriteTransaction(t *testing.T) {
	f, l, d, _ := savedFixture(t)
	dir := scratchUnderDurable(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := fmt.Sprintf("#!/bin/sh\n%q -test.run '^TestPreservationGitProbe$' > %q 2>&1 || exit 99\nexec %q \"$@\"\n", binary, filepath.Join(dir, "probe.log"), realGit)
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(wrapper), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_PRESERVE_PROBE", f.storePath)
	if err := l.preserveWork(context.Background(), d.LaneID, d.RunRequest.RunID, "blocked"); err != nil {
		t.Fatal(err)
	}
	// Retention is Git outside the transaction too.
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state = 'closed'`); return err })
	if err := l.prunePreservedWork(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := l.git(context.Background(), f.root, "rev-parse", "HEAD"); err == nil {
			t.Fatal("negative control: probe permitted Git inside a write")
		}
		return nil
	})
}

func TestPreservationGitProbe(t *testing.T) {
	path := os.Getenv("TILLR_PRESERVE_PROBE")
	if path == "" {
		t.Skip("subprocess probe only")
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA busy_timeout = 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal("Git ran inside a write transaction:", err)
	}
	if _, err := db.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
}
