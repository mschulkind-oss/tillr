package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type gateBaseRecordingSteps struct {
	recordingSteps
	gated []Candidate
}

func (s *gateBaseRecordingSteps) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	s.gated = append(s.gated, c)
	return GateOutcome{Outcome: GatePassed}, nil
}

func ordinaryCandidateFixture(t *testing.T) (refusalLoopFixture, *Loop, *gateBaseRecordingSteps, Candidate, string, *strings.Builder) {
	t.Helper()
	f, l, _, oldBase := fixerRoutingFixture(t)
	steps := &gateBaseRecordingSteps{}
	l.cfg.Steps = steps
	report := &strings.Builder{}
	l.cfg.Report = report

	lane := l.laneTarget("lane-1")
	runGit(t, lane, "reset", "-q", "--hard", oldBase)
	laneDir := l.laneDirPath("lane-1")
	for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
		if err := os.WriteFile(filepath.Join(laneDir, name), []byte(oldBase+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeForkFile(t, filepath.Join(lane, "feature.txt"), "candidate change\n")
	runGit(t, lane, "add", "feature.txt")
	runGit(t, lane, "commit", "-qm", "feat: candidate change")
	head := runGit(t, lane, "rev-parse", "HEAD")

	c := Candidate{
		ItemID:   "item-spare",
		LaneID:   "lane-1",
		RunID:    "run-ordinary-1",
		Worktree: lane,
		BaseSHA:  oldBase,
		HeadSHA:  head,
	}
	return f, l, steps, c, oldBase, report
}

// When the drain has recorded that the trunk is repaired at a commit newer than a
// candidate's base, the candidate is rebased onto the current trunk before its landing
// gate starts, and no gate run is started on the old base.
func TestPreRebaseRepairedTrunkRebasesBeforeGateWithNoOldBaseGate(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, oldBase, report := ordinaryCandidateFixture(t)

	writeForkFile(t, filepath.Join(f.root, "repair.txt"), "trunk repair\n")
	runGit(t, f.root, "add", "repair.txt")
	runGit(t, f.root, "commit", "-qm", "fix: repair flaky restart test")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)

	writeForkFile(t, filepath.Join(f.root, "later.txt"), "subsequent landing\n")
	runGit(t, f.root, "add", "later.txt")
	runGit(t, f.root, "commit", "-qm", "chore: subsequent landing")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": the focused check passed on the advanced trunk"); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil {
		t.Fatalf("Verify failed: %v\n%s", err, report.String())
	}
	if !landed {
		t.Fatalf("candidate did not land\n%s", report.String())
	}
	if len(steps.gated) != 1 {
		t.Fatalf("gate ran %d time(s), want 1: %+v", len(steps.gated), steps.gated)
	}
	got := steps.gated[0]
	if got.BaseSHA == oldBase {
		t.Fatalf("gate was started on the old base %s; want current trunk %s", shortSHA(oldBase), shortSHA(tip))
	}
	if got.BaseSHA != tip {
		t.Fatalf("gated BaseSHA = %s, want current trunk %s", shortSHA(got.BaseSHA), shortSHA(tip))
	}
	if got.HeadSHA == c.HeadSHA {
		t.Fatalf("gated HeadSHA was not rebased from %s", shortSHA(c.HeadSHA))
	}
	runGit(t, c.Worktree, "merge-base", "--is-ancestor", tip, got.HeadSHA)
	if gotBase := l.laneBase("lane-1"); gotBase != tip {
		t.Fatalf("lane base = %s, want %s", shortSHA(gotBase), shortSHA(tip))
	}
	if !strings.Contains(report.String(), "pre-rebased "+c.ItemID+" onto "+shortSHA(tip)+" before gate") {
		t.Fatalf("missing pre-rebase report line:\n%s", report.String())
	}
}

// Even when only heavy.json carries a retainRedRepairs note ("repair deferred to proof
// of <sha>: ..."), the drain still recognizes the recorded repair and rebases candidates
// from older bases onto the current trunk before their landing gate starts.
func TestPreRebaseRepairedTrunkReadsDeferredRepairFromHeavyJSON(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, oldBase, report := ordinaryCandidateFixture(t)

	writeForkFile(t, filepath.Join(f.root, "repair.txt"), "trunk repair\n")
	runGit(t, f.root, "add", "repair.txt")
	runGit(t, f.root, "commit", "-qm", "fix: repair restart test")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)

	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(HeavyRun{
		BaseSHA: oldBase,
		TipSHA:  oldBase,
		Result:  "red",
		Checks: []HeavyCheck{{
			CheckID: goTestPrefix + "tools/tillr-drain:example/drain:TestWhyAfterRestart",
			Result:  "fail",
			Detail:  "repair deferred to proof of " + shortSHA(repaired) + ": the focused check passed on the advanced trunk",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "heavy.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil || !landed {
		t.Fatalf("Verify = %v, %v\n%s", landed, err, report.String())
	}
	if len(steps.gated) != 1 || steps.gated[0].BaseSHA != repaired {
		t.Fatalf("gated = %+v, want single gate on repaired tip %s", steps.gated, shortSHA(repaired))
	}
}

// A candidate whose base is at or newer than the recorded repair is NOT needlessly
// rebased on unrelated trunk movement (landing principle 3).
func TestPreRebaseRepairedTrunkLeavesCandidateAtOrAfterRepairAlone(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, oldBase, report := ordinaryCandidateFixture(t)

	if err := recordTrunkRepair(ctx, f.root, oldBase, "repaired at candidate base"); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "other.txt"), "unrelated landing\n")
	runGit(t, f.root, "add", "other.txt")
	runGit(t, f.root, "commit", "-qm", "chore: unrelated landing")

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil || !landed {
		t.Fatalf("Verify = %v, %v\n%s", landed, err, report.String())
	}
	if len(steps.gated) != 1 || steps.gated[0].BaseSHA != oldBase {
		t.Fatalf("candidate at repair base was needlessly rebased: gated=%+v want base=%s", steps.gated, shortSHA(oldBase))
	}
	workerHead := steps.gated[0].WorkerHeadSHA
	if workerHead == "" {
		workerHead = steps.gated[0].HeadSHA
	}
	if workerHead != c.HeadSHA {
		t.Fatalf("candidate worker head changed from %s to %s without a newer repair", shortSHA(c.HeadSHA), shortSHA(workerHead))
	}
}

// A pre-gate rebase onto a repaired trunk that conflicts aborts cleanly, starts no gate
// on the old base, and routes through conflictFix.
func TestPreRebaseRepairedTrunkConflictRoutesToConflictFixWithoutGate(t *testing.T) {
	ctx := context.Background()
	f, l, steps, c, _, report := ordinaryCandidateFixture(t)

	writeForkFile(t, filepath.Join(c.Worktree, "base.txt"), "candidate edit\n")
	runGit(t, c.Worktree, "commit", "-qam", "candidate edits base")
	c.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")

	writeForkFile(t, filepath.Join(f.root, "base.txt"), "trunk repair edit\n")
	runGit(t, f.root, "commit", "-qam", "fix: conflicting trunk repair")
	repaired := runGit(t, f.root, "rev-parse", TrunkRef)

	if err := recordTrunkRepair(ctx, f.root, repaired, "repair deferred to proof of "+shortSHA(repaired)+": the focused check passed on the advanced trunk"); err != nil {
		t.Fatal(err)
	}

	var result LoopResult
	_, landed, err := l.Verify(ctx, c, &result, nil)
	if err != nil {
		t.Fatalf("Verify returned error on conflict: %v\n%s", err, report.String())
	}
	if landed {
		t.Fatal("conflicting candidate landed")
	}
	if len(steps.gated) != 0 {
		t.Fatalf("gate started %d time(s) on old base despite pre-gate rebase conflict: %+v", len(steps.gated), steps.gated)
	}
	var conflicts int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND payload LIKE '%"evidence":"conflict:paths=base.txt,hunks=%'`, RefusalKind, c.RunID).Scan(&conflicts)
	})
	if conflicts == 0 {
		t.Fatalf("no conflict refusal recorded for %s:\n%s", c.RunID, report.String())
	}
}

// A "trunk was rewritten" deferral note is not a trunk repair.
func TestDeferredRepairPrefixesIgnoresTrunkRewrite(t *testing.T) {
	got := deferredRepairPrefixes("repair deferred to proof of 51cd904b55b4: trunk was rewritten")
	if len(got) != 0 {
		t.Fatalf("deferredRepairPrefixes = %v, want empty", got)
	}
	got = deferredRepairPrefixes("first note; repair deferred to proof of 51cd904b55b4: the focused check passed on the advanced trunk")
	if len(got) != 1 || got[0] != "51cd904b55b4" {
		t.Fatalf("deferredRepairPrefixes = %v, want [51cd904b55b4]", got)
	}
}
