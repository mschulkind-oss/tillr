package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/packmirror"
	"github.com/mschulkind-oss/tillr/packsync"
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

func TestFilterMirrorConflictReports(t *testing.T) {
	reports := []string{
		"CONFLICT (content): Merge conflict in tools/tillr-drain/drain/conflict.go",
		"CONFLICT (content): Merge conflict in " + tillrMirrorRoot + "tillr-drain/drain/conflict.go",
		"CONFLICT (modify/delete): " + tillrMirrorRoot + "tillr-loop/tillr-loop deleted in HEAD",
		"CONFLICT (content): Merge conflict in " + tillrMirrorRoot + "tillr-loop/README.md",
		"CONFLICT (content): Merge conflict in " + tillrMirrorRoot + "docs/reference/gate.md",
	}
	nonMirror, modules := filterMirrorConflictReports(reports)
	wantNonMirror := []string{
		"CONFLICT (content): Merge conflict in tools/tillr-drain/drain/conflict.go",
		"CONFLICT (content): Merge conflict in " + tillrMirrorRoot + "tillr-loop/README.md",
		"CONFLICT (content): Merge conflict in " + tillrMirrorRoot + "docs/reference/gate.md",
	}
	if len(nonMirror) != len(wantNonMirror) {
		t.Fatalf("nonMirror = %v, want %v", nonMirror, wantNonMirror)
	}
	for i := range wantNonMirror {
		if nonMirror[i] != wantNonMirror[i] {
			t.Fatalf("nonMirror[%d] = %q, want %q", i, nonMirror[i], wantNonMirror[i])
		}
	}
	wantModules := []string{"tillr-drain", "tillr-loop"}
	if len(modules) != len(wantModules) || modules[0] != wantModules[0] || modules[1] != wantModules[1] {
		t.Fatalf("modules = %v, want %v", modules, wantModules)
	}
}

func TestStagePackMirrorUpdatesSupportRevisionOnSupportFileChange(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-sr")
	lane := l.laneTarget("lane-1")
	for path, body := range map[string]string{
		"yolo-packs/tillr/pack.json":              "{}\n",
		"tools/tillr/README.md":                   "tillr\n",
		"tools/tillr/buildstamp/support-revision": "sha256:0000000000000000000000000000000000000000000000000000000000000000\n",
		"tools/tillr-gate/change-gate.py":         "# v1\n",
	} {
		writePackFixture(t, f.root, path, body)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "support fixture")
	base := runGit(t, f.root, "rev-parse", "HEAD")

	runGit(t, lane, "reset", "--hard", base)
	writeForkFile(t, filepath.Join(lane, "tools/tillr-gate/change-gate.py"), "# v2\n")
	runGit(t, lane, "add", "tools/tillr-gate/change-gate.py")
	runGit(t, lane, "commit", "-m", "feat: update gate runner")
	worker := runGit(t, lane, "rev-parse", "HEAD")

	c := Candidate{ItemID: "item-sr", LaneID: "lane-1", RunID: "run-sr", Worktree: lane, BaseSHA: base, HeadSHA: worker}
	staged, err := l.stagePackMirror(ctx, c)
	if err != nil {
		t.Fatalf("stagePackMirror: %v", err)
	}
	if !staged {
		t.Fatal("expected stagePackMirror to stage regenerated support-revision and mirrors")
	}

	gotSrc, err := os.ReadFile(filepath.Join(lane, "tools/tillr/buildstamp/support-revision"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gotSrc), "0000000000000000") || !strings.HasPrefix(strings.TrimSpace(string(gotSrc)), "sha256:") {
		t.Fatalf("support-revision was not regenerated: %q", string(gotSrc))
	}
	gotMirror, err := os.ReadFile(filepath.Join(lane, tillrMirrorRoot+"tillr/buildstamp/support-revision"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotMirror) != string(gotSrc) {
		t.Fatalf("mirrored support-revision %q != source %q", string(gotMirror), string(gotSrc))
	}
	// Verify filterMirrorConflictReports recognizes tools/tillr/buildstamp/support-revision.
	nonMirror, mods := filterMirrorConflictReports([]string{"CONFLICT (content): Merge conflict in tools/tillr/buildstamp/support-revision"})
	if len(nonMirror) != 0 || len(mods) != 1 || mods[0] != "tillr" {
		t.Fatalf("filterMirrorConflictReports = (%v, %v), want ([], [tillr])", nonMirror, mods)
	}
}

type realSquashLandSteps struct {
	Steps
	root            string
	beforeFirstLand func()
	conflicts       int
	lands           int
}

func (s *realSquashLandSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if s.beforeFirstLand != nil {
		fn := s.beforeFirstLand
		s.beforeFirstLand = nil
		fn()
	}
	trunk, err := land.OpenTrunk(s.root)
	if err != nil {
		return LandOutcome{}, err
	}
	branch, err := trunk.Branch()
	if err != nil {
		return LandOutcome{}, err
	}
	head, err := trunk.Head(branch)
	if err != nil {
		return LandOutcome{}, err
	}
	sq, err := trunk.Squash(c.BaseSHA, head, c.HeadSHA, time.Now())
	if err != nil {
		return LandOutcome{}, err
	}
	if sq.Conflicted() {
		s.conflicts++
		return LandOutcome{
			Result:        land.ResultSquashConflict,
			ConflictPaths: sq.Conflicts,
		}, nil
	}
	if err := trunk.Move(branch, sq.Commit, head); err != nil {
		return LandOutcome{}, err
	}
	cmd := exec.Command("git", "-C", s.root, "reset", "--hard", sq.Commit)
	if out, err := cmd.CombinedOutput(); err != nil {
		return LandOutcome{}, fmt.Errorf("reset trunk worktree: %v\n%s", err, out)
	}
	s.lands++
	return LandOutcome{
		Result:     land.ResultLanded,
		TrunkAfter: sq.Commit,
	}, nil
}

// TestConcurrentSupportFileLandingsKeepSupportRevisionFresh verifies that when two
// concurrent changes each touch support files, the first lands through the lander's
// squash, the second conflicts on support-revision and is repaired mechanically by the
// loop, and the resulting trunk passes pack-sync-test's support-revision check with no
// hand step.
func TestConcurrentSupportFileLandingsKeepSupportRevisionFresh(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	const (
		supportFile1 = "config/tillr/workflows/first.json"
		supportFile2 = "tools/tillr-run/backends/pi/second.sh"
	)
	writePackFixture(t, f.root, "yolo-packs/tillr/pack.json", "{}\n")
	writePackFixture(t, f.root, "tools/tillr/buildstamp/build.go", "package buildstamp\n")
	writePackFixture(t, f.root, supportFile1, `{"id":"first"}`+"\n")
	writePackFixture(t, f.root, "tools/tillr-run/backends/pi/init.sh", "#!/bin/sh\n")
	var syncOut bytes.Buffer
	if rc := packmirror.Sync(f.root, []string{"tillr", packmirror.WorkflowsMount, "tillr-run"}, &syncOut, &syncOut); rc != 0 {
		t.Fatalf("initial packmirror.Sync failed (%d): %s", rc, syncOut.String())
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "initial support files and pack mirror")
	baseSHA := runGit(t, f.root, "rev-parse", "HEAD")

	// Change 1: branched from baseSHA, updates supportFile1 and regenerates support-revision.
	change1Dir := filepath.Join(t.TempDir(), "change1")
	runGit(t, f.root, "worktree", "add", "-q", change1Dir, baseSHA)
	t.Cleanup(func() {
		_ = exec.Command("git", "-C", f.root, "worktree", "remove", "--force", change1Dir).Run()
	})
	writePackFixture(t, change1Dir, supportFile1, `{"id":"first","v":2}`+"\n")
	syncOut.Reset()
	if rc := packmirror.Sync(change1Dir, []string{"tillr", packmirror.WorkflowsMount}, &syncOut, &syncOut); rc != 0 {
		t.Fatalf("change1 packmirror.Sync failed (%d): %s", rc, syncOut.String())
	}
	runGit(t, change1Dir, "add", ".")
	runGit(t, change1Dir, "commit", "-m", "feat: update first support file")
	change1Head := runGit(t, change1Dir, "rev-parse", "HEAD")

	trunk, err := land.OpenTrunk(f.root)
	if err != nil {
		t.Fatalf("OpenTrunk: %v", err)
	}
	sq1, err := trunk.Squash(baseSHA, baseSHA, change1Head, time.Now())
	if err != nil {
		t.Fatalf("Squash change 1: %v", err)
	}
	if sq1.Conflicted() {
		t.Fatalf("unexpected conflict on change 1: %v", sq1.Conflicts)
	}
	branch, err := trunk.Branch()
	if err != nil {
		t.Fatalf("trunk.Branch: %v", err)
	}

	// Change 2: staged on lane-1 from baseSHA, updates supportFile2.
	if err := l.resetLane(ctx, "lane-1"); err != nil {
		t.Fatal(err)
	}
	stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths = '["candidate.txt","tools/tillr-run/","tools/tillr/","yolo-packs/tillr/"]' WHERE item_id = 'item-a'`)
		return err
	})
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "candidate.txt\ntools/tillr-run/\ntools/tillr/\nyolo-packs/tillr/\n")
	writePackFixture(t, lane, supportFile2, "#!/bin/sh\necho second\n")
	runGit(t, lane, "add", supportFile2)
	runGit(t, lane, "commit", "--amend", "--no-edit")

	landSteps := &realSquashLandSteps{
		Steps: &countingJudgeSteps{storePath: f.storePath},
		root:  f.root,
		beforeFirstLand: func() {
			// Land Change 1 onto trunk right before Change 2 attempts to land.
			if err := trunk.Move(branch, sq1.Commit, baseSHA); err != nil {
				t.Fatalf("Move change 1: %v", err)
			}
			runGit(t, f.root, "reset", "--hard", sq1.Commit)
		},
	}
	l.cfg.Steps = landSteps

	var result LoopResult
	stop, err := l.pass(ctx, &result)
	if err != nil || stop != "" {
		t.Fatalf("pass failed: stop=%q err=%v", stop, err)
	}
	drainOwned(t, l, &result)
	if result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("expected 1 landed and 0 rejected (conflicts=%d lands=%d), got %+v", landSteps.conflicts, landSteps.lands, result)
	}
	if result.ConflictFixes != 0 {
		t.Fatalf("ConflictFixes = %d, want 0 (mirror-only conflict must not run a worker)", result.ConflictFixes)
	}
	if landSteps.conflicts != 1 || landSteps.lands != 1 {
		t.Fatalf("conflicts=%d lands=%d, want 1 conflict (repaired mechanically) and 1 landing", landSteps.conflicts, landSteps.lands)
	}

	var checkOut bytes.Buffer
	if err := packsync.CaseSupportRevision(f.root, false, &checkOut); err != nil {
		t.Fatalf("resulting trunk failed CaseSupportRevision: %v\n%s", err, checkOut.String())
	}
	if err := packsync.CaseWorkflows(f.root, false, &checkOut); err != nil {
		t.Fatalf("resulting trunk failed CaseWorkflows: %v\n%s", err, checkOut.String())
	}
}

const candidatePackMirrorGoMod = "module github.com/mschulkind-oss/tillr\n\ngo 1.24\n"

const candidatePackMirrorMain = `package main

import (
	"os"

	"github.com/mschulkind-oss/tillr/packmirror"
)

func main() {
	os.Exit(packmirror.Main(os.Args[1:], os.Stdout, os.Stderr))
}
`

const candidatePackMirrorBase = `package packmirror

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "--sync" {
		fmt.Fprintln(stderr, "usage: pack-mirror --sync <root> [module...]")
		return 2
	}
	root := args[1]
	for _, mod := range args[2:] {
		src := filepath.Join(root, "tools", mod, "main.go")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(root, "yolo-packs", "tillr", "files", "local", "share", "tillr", mod, "main.go")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			fmt.Fprintf(stderr, "mkdir: %v\n", err)
			return 1
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			fmt.Fprintf(stderr, "write: %v\n", err)
			return 1
		}
	}
	return 0
}
`

const candidatePackMirrorAddsDist = `package packmirror

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "--sync" {
		fmt.Fprintln(stderr, "usage: pack-mirror --sync <root> [module...]")
		return 2
	}
	root := args[1]
	for _, mod := range args[2:] {
		modDst := filepath.Join(root, "yolo-packs", "tillr", "files", "local", "share", "tillr", mod)
		src := filepath.Join(root, "tools", mod, "main.go")
		if data, err := os.ReadFile(src); err == nil {
			dst := filepath.Join(modDst, "main.go")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				fmt.Fprintf(stderr, "mkdir: %v\n", err)
				return 1
			}
			if err := os.WriteFile(dst, data, 0o644); err != nil {
				fmt.Fprintf(stderr, "write: %v\n", err)
				return 1
			}
		}
		if mod == "tillr-view" {
			distIndex := filepath.Join(modDst, "web", "dist", "index.html")
			if err := os.MkdirAll(filepath.Dir(distIndex), 0o755); err != nil {
				fmt.Fprintf(stderr, "mkdir dist: %v\n", err)
				return 1
			}
			if err := os.WriteFile(distIndex, []byte("<html>observatory</html>\n"), 0o644); err != nil {
				fmt.Fprintf(stderr, "write dist: %v\n", err)
				return 1
			}
			digest := filepath.Join(modDst, "dist-source-digest")
			if err := os.WriteFile(digest, []byte("sha256:abc123\n"), 0o644); err != nil {
				fmt.Fprintf(stderr, "write digest: %v\n", err)
				return 1
			}
		}
	}
	return 0
}
`

func TestStagePackMirrorUsesCandidateSyncWhenSyncChanges(t *testing.T) {
	const (
		viewSrc     = "tools/tillr-view/main.go"
		viewMirror  = tillrMirrorRoot + "tillr-view/main.go"
		distIndex   = tillrMirrorRoot + "tillr-view/web/dist/index.html"
		distDigest  = tillrMirrorRoot + "tillr-view/dist-source-digest"
		syncSrc     = "tools/tillr/packmirror/packmirror.go"
		syncMainSrc = "tools/tillr/cmd/pack-mirror/main.go"
	)
	setup := func(t *testing.T) (*Loop, string, string) {
		t.Helper()
		f, l := passFixture(t, "item-a")
		for path, body := range map[string]string{
			"yolo-packs/tillr/pack.json": "{}\n",
			"tools/tillr/go.mod":         candidatePackMirrorGoMod,
			syncMainSrc:                  candidatePackMirrorMain,
			syncSrc:                      candidatePackMirrorBase,
			viewSrc:                      "package main // v1\n",
			viewMirror:                   "package main // v1\n",
		} {
			writePackFixture(t, f.root, path, body)
		}
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-m", "pack fixture with candidate sync")
		base := runGit(t, f.root, "rev-parse", "HEAD")
		lane := l.laneTarget("lane-1")
		runGit(t, lane, "reset", "--hard", base)
		return l, lane, base
	}

	t.Run("candidate added mirror files survive staging into gate", func(t *testing.T) {
		ctx := context.Background()
		l, lane, base := setup(t)
		writePackFixture(t, lane, syncSrc, candidatePackMirrorAddsDist)
		writePackFixture(t, lane, viewSrc, "package main // v2\n")
		writePackFixture(t, lane, viewMirror, "package main // v2\n")
		writePackFixture(t, lane, distIndex, "<html>observatory</html>\n")
		writePackFixture(t, lane, distDigest, "sha256:abc123\n")
		runGit(t, lane, "add", ".")
		runGit(t, lane, "commit", "-m", "feat: add observatory dist to pack mirror")
		worker := runGit(t, lane, "rev-parse", "HEAD")
		writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "tools/tillr/\ntools/tillr-view/\nyolo-packs/tillr/\n")
		c := Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: "run-cand-sync", Worktree: lane, BaseSHA: base, HeadSHA: worker}

		gateStop := errors.New("stop after checking gate input")
		calls := 0
		l.cfg.Steps = packMirrorGate{Steps: l.cfg.Steps, gate: func(c Candidate) (GateOutcome, error) {
			calls++
			if got := runGit(t, lane, "show", c.HeadSHA+":"+distIndex); got != "<html>observatory</html>" {
				t.Fatalf("gate lost candidate-added dist index: %q", got)
			}
			if got := runGit(t, lane, "show", c.HeadSHA+":"+distDigest); got != "sha256:abc123" {
				t.Fatalf("gate lost candidate-added dist digest: %q", got)
			}
			if got := runGit(t, lane, "show", c.HeadSHA+":"+viewMirror); got != "package main // v2" {
				t.Fatalf("gate saw stale view mirror: %q", got)
			}
			return GateOutcome{}, gateStop
		}}
		if _, _, err := l.verify(ctx, &c, &LoopResult{}, nil); !errors.Is(err, gateStop) || calls != 1 {
			t.Fatalf("verify: calls=%d, err=%v", calls, err)
		}
	})

	t.Run("staging regenerates added mirror files using candidate sync", func(t *testing.T) {
		ctx := context.Background()
		l, lane, base := setup(t)
		writePackFixture(t, lane, syncSrc, candidatePackMirrorAddsDist)
		writePackFixture(t, lane, viewSrc, "package main // v2\n")
		runGit(t, lane, "add", ".")
		runGit(t, lane, "commit", "-m", "feat: update sync without pre-generating mirror")
		c := Candidate{ItemID: "item-a", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
		changed, err := l.stagePackMirror(ctx, c)
		if err != nil || !changed {
			t.Fatalf("stagePackMirror = (%v, %v), want (true, nil)", changed, err)
		}
		if got := runGit(t, lane, "show", ":"+distIndex); got != "<html>observatory</html>" {
			t.Fatalf("staged dist index = %q", got)
		}
		if got := runGit(t, lane, "show", ":"+distDigest); got != "sha256:abc123" {
			t.Fatalf("staged dist digest = %q", got)
		}
	})
}

func TestStagePackMirrorUsesLoopSyncWhenSyncUnchanged(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	const (
		viewSrc    = "tools/tillr-view/main.go"
		viewMirror = tillrMirrorRoot + "tillr-view/main.go"
	)
	for path, body := range map[string]string{
		"yolo-packs/tillr/pack.json":          "{}\n",
		"tools/tillr/cmd/pack-mirror/main.go": "this is not valid go and would fail if executed\n",
		viewSrc:                               "package main // v1\n",
		viewMirror:                            "package main // v1\n",
	} {
		writePackFixture(t, f.root, path, body)
	}
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-m", "fixture with non-runnable candidate sync")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	lane := l.laneTarget("lane-1")
	runGit(t, lane, "reset", "--hard", base)
	writePackFixture(t, lane, viewSrc, "package main // v2\n")
	runGit(t, lane, "add", viewSrc)
	runGit(t, lane, "commit", "-m", "feat: update view without touching sync")

	c := Candidate{ItemID: "item-a", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}
	changed, err := l.stagePackMirror(ctx, c)
	if err != nil || !changed {
		t.Fatalf("stagePackMirror = (%v, %v), want (true, nil)", changed, err)
	}
	if got := runGit(t, lane, "show", ":"+viewMirror); got != "package main // v2" {
		t.Fatalf("staged view mirror = %q, want updated copy from loop sync", got)
	}
}

func TestStagePackMirrorRefusesBrokenCandidateSyncWithoutFallback(t *testing.T) {
	const (
		viewSrc     = "tools/tillr-view/main.go"
		viewMirror  = tillrMirrorRoot + "tillr-view/main.go"
		syncSrc     = "tools/tillr/packmirror/packmirror.go"
		syncMainSrc = "tools/tillr/cmd/pack-mirror/main.go"
	)
	setup := func(t *testing.T) (*Loop, string, string) {
		t.Helper()
		f, l := passFixture(t, "item-a")
		for path, body := range map[string]string{
			"yolo-packs/tillr/pack.json": "{}\n",
			"tools/tillr/go.mod":         candidatePackMirrorGoMod,
			syncMainSrc:                  candidatePackMirrorMain,
			syncSrc:                      candidatePackMirrorBase,
			viewSrc:                      "package main // v1\n",
			viewMirror:                   "package main // v1\n",
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

	t.Run("unbuildable candidate sync is refused without falling back", func(t *testing.T) {
		ctx := context.Background()
		l, lane, base := setup(t)
		writePackFixture(t, lane, syncSrc, "package packmirror\n\nimport \"io\"\n\nfunc Main(args []string, stdout, stderr io.Writer) int {\n\treturn undefinedCandidateSyncSymbol()\n}\n")
		writePackFixture(t, lane, viewSrc, "package main // v2\n")
		runGit(t, lane, "add", ".")
		runGit(t, lane, "commit", "-m", "break packmirror build")
		worker := runGit(t, lane, "rev-parse", "HEAD")
		writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "tools/tillr/\ntools/tillr-view/\n")
		c := Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: "run-broken-build", Worktree: lane, BaseSHA: base, HeadSHA: worker}

		gateCalls := 0
		l.cfg.Steps = packMirrorGate{Steps: l.cfg.Steps, gate: func(Candidate) (GateOutcome, error) {
			gateCalls++
			return GateOutcome{}, nil
		}}
		if _, _, err := l.verify(ctx, &c, &LoopResult{}, nil); !errors.Is(err, errItemFaultHeld) {
			t.Fatalf("verify err = %v, want errItemFaultHeld", err)
		}
		if gateCalls != 0 {
			t.Fatalf("gate was called %d times, want 0 when candidate sync cannot build", gateCalls)
		}
		if got, err := os.ReadFile(filepath.Join(lane, viewMirror)); err != nil || string(got) != "package main // v1\n" {
			t.Fatalf("loop sync fell back and overwrote mirror: err=%v got=%q", err, string(got))
		}
		if err := l.readStore(ctx, func(r *store.Reader) error {
			faults, err := ReadItemFaults(r)
			if err != nil {
				return err
			}
			fault := faults["item-a"]
			if len(faults) != 1 || fault.Reason != ReasonPackSyncFailed || !strings.Contains(fault.Detail, "candidate pack-sync") || !strings.Contains(fault.Detail, "undefinedCandidateSyncSymbol") {
				t.Fatalf("wanted pack-sync-failed hold naming candidate sync build error; got %+v", faults)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("failing candidate sync is refused without falling back", func(t *testing.T) {
		ctx := context.Background()
		l, lane, base := setup(t)
		writePackFixture(t, lane, syncMainSrc, "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Fprintln(os.Stderr, \"candidate sync boom\"); os.Exit(1) }\n")
		writePackFixture(t, lane, viewSrc, "package main // v2\n")
		runGit(t, lane, "add", ".")
		runGit(t, lane, "commit", "-m", "make candidate sync fail at runtime")
		c := Candidate{ItemID: "item-a", Worktree: lane, BaseSHA: base, HeadSHA: runGit(t, lane, "rev-parse", "HEAD")}

		changed, err := l.stagePackMirror(ctx, c)
		if err == nil || changed {
			t.Fatalf("stagePackMirror = (%v, %v), want (false, error)", changed, err)
		}
		if !strings.Contains(err.Error(), "candidate sync boom") {
			t.Fatalf("error %q did not carry candidate sync failure output", err.Error())
		}
		if got, err := os.ReadFile(filepath.Join(lane, viewMirror)); err != nil || string(got) != "package main // v1\n" {
			t.Fatalf("loop sync fell back and overwrote mirror: err=%v got=%q", err, string(got))
		}
	})
}
