package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

type packMirrorGate struct {
	Steps
	gate func(Candidate) (GateOutcome, error)
}

func (s packMirrorGate) Gate(_ context.Context, c Candidate) (GateOutcome, error) {
	return s.gate(c)
}

func writePackFixture(t *testing.T, root, path, body string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, path, body)
}

func writeCurrentPackRecipe(t *testing.T, root string) {
	t.Helper()
	for _, path := range []string{".justfile"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", path))
		if err != nil {
			t.Fatal(err)
		}
		writePackFixture(t, root, path, string(body))
	}
}

func TestGateSeesRegeneratedTillrPackMirror(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	lane := l.laneTarget("lane-1")
	const source = "tools/tillr-watch/"
	const mirror = "yolo-packs/tillr/files/local/share/tillr/tillr-watch/"
	writePackFixture(t, f.root, ".justfile", "pack-sync:\n    @true\n") // Old recipe: it takes no module arguments.
	for path, body := range map[string]string{
		"yolo-packs/tillr/pack.json": "{}\n",
		source + "script":            "old",
		mirror + "script":            "old",
		source + "removed":           "old",
		mirror + "removed":           "old",
	} {
		writePackFixture(t, f.root, path, body)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "pack fixture")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeCurrentPackRecipe(t, f.root)
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "accept modules in pack-sync on trunk")
	runGit(t, lane, "reset", "--hard", base)
	writeForkFile(t, filepath.Join(lane, source, "script"), "new")
	if err := os.Chmod(filepath.Join(lane, source, "script"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane, source, "README.md"), "[Doc](../../docs/work.md)\n")
	runGit(t, lane, "rm", source+"removed")
	runGit(t, lane, "add", source)
	runGit(t, lane, "commit", "-m", "feat: update watch source only")
	worker := runGit(t, lane, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), source+"\n")
	c := Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: "run-pack-mirror", Worktree: lane, BaseSHA: base, HeadSHA: worker}

	gateStop := errors.New("stop after checking the gate input")
	calls := 0
	l.cfg.Steps = packMirrorGate{Steps: l.cfg.Steps, gate: func(c Candidate) (GateOutcome, error) {
		calls++
		if c.WorkerHeadSHA != worker || c.HeadSHA != runGit(t, lane, "rev-parse", "HEAD") || c.HeadSHA == worker {
			t.Fatalf("gate did not receive a committed maintenance tree bound to the worker: %+v", c)
		}
		if got := runGit(t, lane, "show", c.HeadSHA+":"+mirror+"script"); got != "new" {
			t.Fatalf("gate saw stale mirror: %q", got)
		}
		if got := runGit(t, lane, "ls-tree", c.HeadSHA, mirror+"script"); !strings.HasPrefix(got, "100755 ") {
			t.Fatalf("gate saw wrong mirror mode: %q", got)
		}
		if got := runGit(t, lane, "ls-tree", c.HeadSHA, mirror+"removed"); got != "" {
			t.Fatalf("gate saw deleted source in mirror: %q", got)
		}
		if got := runGit(t, lane, "show", c.HeadSHA+":"+mirror+"README.md"); got != "[Doc](https://github.com/mschulkind-oss/tillr/blob/main/docs/work.md)" {
			t.Fatalf("gate saw wrong mirror link: %q", got)
		}
		if got := runGit(t, lane, "status", "--porcelain"); got != "" {
			t.Fatalf("gate saw uncommitted preparation: %s", got)
		}
		return GateOutcome{}, gateStop
	}}
	if _, _, err := l.verify(ctx, &c, &LoopResult{}, nil); !errors.Is(err, gateStop) || calls != 1 {
		t.Fatalf("first gate: calls=%d, err=%v", calls, err)
	}
	if retired, err := l.retiredOriginal(ctx, c.RunID); retired || err != nil {
		t.Fatalf("mirror-only maintenance falsely recorded retirement: %v, %v", retired, err)
	}
	// Re-enter with the pre-commit intent that a replacement drain loads.
	var saved Candidate
	if err := l.readStore(ctx, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, candidatePreparedKind, c.RunID).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &saved)
	}); err != nil {
		t.Fatal(err)
	}
	if saved.HeadSHA != worker || saved.RetirementTreeSHA != runGit(t, lane, "rev-parse", c.HeadSHA+"^{tree}") {
		t.Fatalf("missing pre-commit binding: %+v", saved)
	}
	if _, _, err := l.verify(ctx, &saved, &LoopResult{}, nil); !errors.Is(err, gateStop) || calls != 2 || saved.HeadSHA != c.HeadSHA {
		t.Fatalf("recovered gate: calls=%d, head=%s, err=%v", calls, saved.HeadSHA, err)
	}
}

func TestPackMirrorRegeneratesLoopAdapterWithoutChangingDocs(t *testing.T) {
	f, l := passFixture(t, "item-a")
	const source = "tools/tillr-loop/main.go"
	const mirror = tillrMirrorRoot + "tillr-loop/main.go"
	const readme = tillrMirrorRoot + "tillr-loop/README.md"
	writePackFixture(t, f.root, "yolo-packs/tillr/pack.json", "{}\n")
	writePackFixture(t, f.root, readme, "snapshot\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "pack fixture")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	lane := l.laneTarget("lane-1")
	runGit(t, lane, "reset", "--hard", base)
	writePackFixture(t, lane, source, "adapter\n")
	if err := os.Chmod(filepath.Join(lane, source), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, lane, "add", source)
	runGit(t, lane, "commit", "-m", "add the adapter")
	c := Candidate{Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
	if changed, err := l.stagePackMirror(context.Background(), c); err != nil || !changed {
		t.Fatalf("regenerate loop adapter: changed=%v, err=%v", changed, err)
	}
	if got := runGit(t, lane, "diff", "--cached", "--name-only"); got != mirror {
		t.Fatalf("staged mirror paths = %q, want only %s", got, mirror)
	}
	if got := runGit(t, lane, "show", ":"+mirror); got != "adapter" {
		t.Fatalf("staged adapter = %q", got)
	}
	if got := runGit(t, lane, "ls-files", "--stage", mirror); !strings.HasPrefix(got, "100755 ") {
		t.Fatalf("staged adapter has wrong mode: %q", got)
	}
}

func TestPackMirrorFailureHoldsOnlyThatCandidate(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a", "item-b")
	steps := &countingJudgeSteps{storePath: f.storePath}
	l.cfg.Steps = steps
	writeCurrentPackRecipe(t, f.root)
	writePackFixture(t, f.root, "yolo-packs/tillr/pack.json", "{}\n")
	if err := os.Symlink("missing", filepath.Join(f.root, "yolo-packs/tillr/files")); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "pack with blocked mirror destination")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	for _, id := range l.cfg.Lanes {
		if err := l.resetLane(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	a := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
	stageFinishedCandidateOnLane(t, f, l, "item-b", "lane-2")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt","tools/tillr-drain/"]' WHERE item_id = 'item-a'`)
		return err
	})
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\ntools/tillr-drain/\n")
	writePackFixture(t, lane, "tools/tillr-drain/script", "new")
	runGit(t, lane, "add", "tools/tillr-drain")
	runGit(t, lane, "commit", "--amend", "--no-edit")
	worker := runGit(t, lane, "rev-parse", "HEAD")

	var result LoopResult
	stop, err := l.pass(ctx, &result)
	if err != nil || stop != "" {
		t.Fatalf("pack-sync failure stopped the pass: %q/%v", stop, err)
	}
	drainOwned(t, l, &result)
	if result.Candidates != 2 || result.Landed != 1 || result.Rejected != 0 || steps.gates.Load() != 1 || steps.verdicts.Load() != 1 {
		t.Fatalf("only the unaffected candidate should be judged and land: %+v (gates=%d verdicts=%d)", result, steps.gates.Load(), steps.verdicts.Load())
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		fault := faults["item-a"]
		if len(faults) != 1 || fault.Reason != "pack-sync-failed" || fault.RunID != a.RunRequest.RunID || !strings.Contains(fault.Detail, "pack-sync: refusing symlinked destination ancestor:") {
			t.Fatalf("wanted an item-a hold with renderer output; got %+v", faults)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+a.RunRequest.RunID); got != worker {
		t.Fatalf("held candidate pin = %s, want worker commit %s", got, worker)
	}
}

type packMirrorLandRecorder struct {
	*countingJudgeSteps
	landed func(Candidate)
}

func (s *packMirrorLandRecorder) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if s.landed != nil {
		s.landed(c)
	}
	return s.countingJudgeSteps.Land(ctx, c)
}

func TestPackMirrorDeletingLoopAdapterAndCopyLands(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	const (
		adapterSource = "tools/tillr-loop/main.go"
		adapterMirror = tillrMirrorRoot + "tillr-loop/main.go"
		adapterReadme = tillrMirrorRoot + "tillr-loop/README.md"
		drainSource   = "tools/tillr-drain/script"
		drainMirror   = tillrMirrorRoot + "tillr-drain/script"
	)
	for path, body := range map[string]string{
		"yolo-packs/tillr/pack.json": "{}\n",
		adapterSource:                "#!/bin/sh\n",
		adapterMirror:                "#!/bin/sh\n",
		adapterReadme:                "snapshot\n",
		drainSource:                  "old\n",
		drainMirror:                  "old\n",
	} {
		writePackFixture(t, f.root, path, body)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "pack fixture with loop adapter")
	if err := l.resetLane(ctx, "lane-1"); err != nil {
		t.Fatal(err)
	}
	stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt","tools/tillr-loop/","tools/tillr-drain/","yolo-packs/tillr/"]' WHERE item_id = 'item-a'`)
		return err
	})
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\ntools/tillr-loop/\ntools/tillr-drain/\nyolo-packs/tillr/\n")
	runGit(t, lane, "rm", adapterSource, adapterMirror)
	writePackFixture(t, lane, drainSource, "new\n")
	runGit(t, lane, "add", drainSource)
	runGit(t, lane, "commit", "--amend", "--no-edit")

	steps := &countingJudgeSteps{storePath: f.storePath}
	var landedHead string
	l.cfg.Steps = &packMirrorLandRecorder{
		countingJudgeSteps: steps,
		landed: func(c Candidate) {
			landedHead = c.HeadSHA
			if got := runGit(t, c.Worktree, "ls-tree", c.HeadSHA, adapterSource, adapterMirror); got != "" {
				t.Errorf("deleted loop adapter still present in landed candidate: %q", got)
			}
			if got := runGit(t, c.Worktree, "show", c.HeadSHA+":"+adapterReadme); got != "snapshot" {
				t.Errorf("loop adapter README was not preserved: %q", got)
			}
			if got := runGit(t, c.Worktree, "show", c.HeadSHA+":"+drainMirror); got != "new" {
				t.Errorf("sibling module mirror was not regenerated: %q", got)
			}
		},
	}

	var result LoopResult
	stop, err := l.pass(ctx, &result)
	if err != nil || stop != "" {
		t.Fatalf("pass failed: stop=%q err=%v", stop, err)
	}
	drainOwned(t, l, &result)
	if result.Candidates != 1 || result.Landed != 1 || result.Rejected != 0 || steps.gates.Load() != 1 || steps.lands.Load() != 1 || steps.verdicts.Load() != 1 || landedHead == "" {
		t.Fatalf("candidate deleting loop adapter and copy did not land: %+v (gates=%d lands=%d verdicts=%d head=%q)", result, steps.gates.Load(), steps.lands.Load(), steps.verdicts.Load(), landedHead)
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if len(faults) != 0 {
			t.Fatalf("unexpected item faults: %+v", faults)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPackMirrorDeletingLoopAdapter(t *testing.T) {
	const (
		source = "tools/tillr-loop/main.go"
		mirror = tillrMirrorRoot + "tillr-loop/main.go"
		readme = tillrMirrorRoot + "tillr-loop/README.md"
	)
	setup := func(t *testing.T) (*Loop, string, string) {
		t.Helper()
		f, l := passFixture(t, "item-a")
		for path, body := range map[string]string{
			"yolo-packs/tillr/pack.json": "{}\n",
			source:                       "#!/bin/sh\n",
			mirror:                       "#!/bin/sh\n",
			readme:                       "snapshot\n",
		} {
			writePackFixture(t, f.root, path, body)
		}
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-m", "pack fixture")
		base := runGit(t, f.root, "rev-parse", "HEAD")
		lane := l.laneTarget("lane-1")
		runGit(t, lane, "reset", "--hard", base)
		return l, lane, base
	}

	t.Run("both deleted", func(t *testing.T) {
		l, lane, base := setup(t)
		runGit(t, lane, "rm", source, mirror)
		runGit(t, lane, "commit", "-m", "remove adapter and mirror")
		c := Candidate{Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
		changed, err := l.stagePackMirror(context.Background(), c)
		if err != nil || changed {
			t.Fatalf("stagePackMirror = (%v, %v), want (false, nil)", changed, err)
		}
		if got := runGit(t, lane, "status", "--porcelain"); got != "" {
			t.Fatalf("worktree not clean after deleting adapter and mirror: %q", got)
		}
	})

	t.Run("copy left behind", func(t *testing.T) {
		l, lane, base := setup(t)
		runGit(t, lane, "rm", source)
		runGit(t, lane, "commit", "-m", "remove adapter only")
		c := Candidate{Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
		changed, err := l.stagePackMirror(context.Background(), c)
		if err != nil || !changed {
			t.Fatalf("stagePackMirror = (%v, %v), want (true, nil)", changed, err)
		}
		if got := runGit(t, lane, "diff", "--cached", "--name-status"); got != "D\t"+mirror {
			t.Fatalf("staged diff = %q, want deletion of %s", got, mirror)
		}
		if _, err := os.Lstat(filepath.Join(lane, mirror)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("mirror copy still exists on disk: %v", err)
		}
		if got := runGit(t, lane, "ls-files", "--", readme); got != readme {
			t.Fatalf("loop README should remain tracked: %q", got)
		}
	})
}

func TestPackMirrorDeletingWholeModuleDirectory(t *testing.T) {
	const (
		source = "tools/tillr-roll/script.py"
		mirror = tillrMirrorRoot + "tillr-roll/script.py"
		dir    = tillrMirrorRoot + "tillr-roll"
	)
	setup := func(t *testing.T) (*Loop, string, string) {
		t.Helper()
		f, l := passFixture(t, "item-a")
		for path, body := range map[string]string{
			"yolo-packs/tillr/pack.json": "{}\n",
			source:                       "print('hi')\n",
			mirror:                       "print('hi')\n",
		} {
			writePackFixture(t, f.root, path, body)
		}
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-m", "pack fixture")
		base := runGit(t, f.root, "rev-parse", "HEAD")
		lane := l.laneTarget("lane-1")
		runGit(t, lane, "reset", "--hard", base)
		return l, lane, base
	}

	t.Run("both deleted", func(t *testing.T) {
		l, lane, base := setup(t)
		runGit(t, lane, "rm", source, mirror)
		runGit(t, lane, "commit", "-m", "remove module and mirror")
		c := Candidate{Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
		changed, err := l.stagePackMirror(context.Background(), c)
		if err != nil || changed {
			t.Fatalf("stagePackMirror = (%v, %v), want (false, nil)", changed, err)
		}
		if _, err := os.Lstat(filepath.Join(lane, dir)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted module mirror directory should not be recreated: %v", err)
		}
		if got := runGit(t, lane, "status", "--porcelain"); got != "" {
			t.Fatalf("worktree not clean after deleting module and mirror: %q", got)
		}
	})

	t.Run("copy left behind", func(t *testing.T) {
		l, lane, base := setup(t)
		runGit(t, lane, "rm", source)
		runGit(t, lane, "commit", "-m", "remove module only")
		c := Candidate{Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
		changed, err := l.stagePackMirror(context.Background(), c)
		if err != nil || !changed {
			t.Fatalf("stagePackMirror = (%v, %v), want (true, nil)", changed, err)
		}
		if got := runGit(t, lane, "diff", "--cached", "--name-status"); got != "D\t"+mirror {
			t.Fatalf("staged diff = %q, want deletion of %s", got, mirror)
		}
		if _, err := os.Lstat(filepath.Join(lane, dir)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted module mirror directory should not remain on disk: %v", err)
		}
	})
}
