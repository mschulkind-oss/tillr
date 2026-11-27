package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

var (
	faceSiblingsOnce sync.Once
	faceRunBin       string
	faceItemBin      string
	faceSiblingsErr  error
)

func faceSiblingBins(t *testing.T) (runBin, itemBin string) {
	t.Helper()
	faceSiblingsOnce.Do(func() {
		faceRunBin = filepath.Join(binDir, "tillr-run")
		cmd := exec.Command("go", "build", "-C", "../tillr-run", "-o", faceRunBin, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			faceSiblingsErr = fmt.Errorf("building tillr-run: %v\n%s", err, out)
			return
		}
		faceItemBin = filepath.Join(binDir, "tillr-item")
		cmd = exec.Command("go", "build", "-C", "../tillr-item", "-o", faceItemBin, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			faceSiblingsErr = fmt.Errorf("building tillr-item: %v\n%s", err, out)
			return
		}
	})
	if faceSiblingsErr != nil {
		t.Fatalf("%v", faceSiblingsErr)
	}
	return faceRunBin, faceItemBin
}

func runFixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_DIR=") || strings.HasPrefix(kv, "GIT_WORK_TREE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %v failed: %v\n%s", dir, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeExecScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	writeFile(t, path, "#!/bin/sh\nset -eu\n"+body+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

func faceItemRecord(itemID, state, rank string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    []string{},
		"rank":          rank,
		"allowed_paths": []string{},
		"criteria":      []any{map[string]any{"criterion_id": "c1", "text": "change lands cleanly", "state": "done"}},
	})
}

func newFaceLoopRepo(t *testing.T, toml string, records ...string) (root, laneRoot, storePath, templatePath string) {
	t.Helper()
	base := scratch(t, "face-item-wt")
	root = filepath.Join(base, "repo")
	laneRoot = filepath.Join(base, "lanes")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runFixtureGit(t, root, "init", "-b", "main")
	writeFile(t, filepath.Join(root, "base.txt"), "base\n")
	if toml != "" {
		writeFile(t, filepath.Join(root, ".tillr.toml"), toml)
	}
	writeFile(t, filepath.Join(root, ".git", "info", "exclude"), ".tillr/\n")
	runFixtureGit(t, root, "add", ".")
	runFixtureGit(t, root, "commit", "-q", "-m", "initial")

	storePath = filepath.Join(root, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := store.InitFixture(ctx, storePath)
	if err != nil {
		t.Fatalf("init fixture store: %v", err)
	}
	if len(records) > 0 {
		raw, err := json.Marshal(map[string]any{
			"kind":    "fixture.seed",
			"payload": map[string]any{"records": rawRecords(records)},
		})
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		if err := s.Seed(ctx, raw); err != nil {
			s.Close()
			t.Fatalf("seed fixture store: %v", err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	templatePath = filepath.Join(base, "brief-template.md")
	writeFile(t, templatePath, "# Fixture brief\n")
	return root, laneRoot, storePath, templatePath
}

func writeFaceLandStub(t *testing.T, dir, root, storePath string) string {
	t.Helper()
	return writeExecScript(t, dir, "land.sh", fmt.Sprintf(`
python3 - %q %q "$@" <<'PY'
import sqlite3, subprocess, sys
root, db = sys.argv[1], sys.argv[2]
args = sys.argv[3:]
def flag(name):
    for i, a in enumerate(args):
        if a == name and i + 1 < len(args):
            return args[i + 1]
    return ""
item_id = flag("--item")
run_id = flag("--run")
base = flag("--base")
head = flag("--head")
trunk = subprocess.check_output(["git", "-C", root, "rev-parse", "main"], text=True).strip()
con = sqlite3.connect(db)
cur = con.cursor()
seq = cur.execute("SELECT COALESCE(MAX(seq), 0) + 1 FROM events").fetchone()[0]
cur.execute(
    "INSERT OR REPLACE INTO landings(landing_id, item_id, run_id, verdict_id, result, reason, base_sha, head_sha, trunk_before, trunk_after, gated_tree, landed_tree, conflict_paths, gate_skipped_reason, event_seq) VALUES (?, ?, ?, 'verdict-stub', 'landed', '', ?, ?, ?, ?, '', '', '[]', '', ?)",
    (f"land-{run_id}", item_id, run_id, base or trunk, head or trunk, trunk, trunk, seq),
)
con.commit()
con.close()
PY
`, root, storePath))
}

func writeFaceAdapterPolicy(t *testing.T, adapter string) string {
	t.Helper()
	return policyDoc(t, map[string]any{
		"default_backend": "fixture",
		"seats":           map[string]string{"build": "fixture", "plan": "fixture", "verify": "fixture"},
		"backends": map[string]any{
			"fixture": map[string]any{
				"adapter":         adapter,
				"model":           "fixture-model",
				"timeout_seconds": 60,
			},
		},
	})
}

func setFixtureItemExpress(t *testing.T, storePath, itemID string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET express = 1 WHERE item_id = ?`, itemID)
		return err
	}); err != nil {
		t.Fatalf("set express on %s: %v", itemID, err)
	}
}

// Criterion 5: `tillr-drain loop` runs an item in <lane-root>/<item-id>/worktree and
// stages no ordinary or express pool worktree at start.
func TestLoopRunsItemInItemWorktreeWithoutStagingPoolLanes(t *testing.T) {
	runBin, itemBin := faceSiblingBins(t)
	itemID := "item_live_a"
	root, laneRoot, storePath, tmpl := newFaceLoopRepo(t, "[lanes]\nexpress = 2\n", faceItemRecord(itemID, "in-queue", ""))

	scripts := scratch(t, "scripts")
	observed := filepath.Join(scripts, "observed.txt")
	adapter := writeExecScript(t, scripts, "adapter.sh", fmt.Sprintf(`
printf 'PWD=%%s\nLANE=%%s\nSCRATCH=%%s\n' "$(pwd)" "${TILLR_LANE_ID:-}" "${YOLO_DURABLE_DIR:-}" > %q
printf 'work\n' > change.txt
git add change.txt
git commit -q -m "work for %s"
printf 'RESULT: done\n'
`, observed, itemID))
	writeFile(t, filepath.Join(scripts, "pause-kind"), "stops-where-it-stands\n")

	passStub := writeExecScript(t, scripts, "pass.sh", `exit 0`)
	landStub := writeFaceLandStub(t, scripts, root, storePath)
	policyPath := writeFaceAdapterPolicy(t, adapter)

	code, out := invoke(t, "loop",
		"--root", root,
		"--store", storePath,
		"--lane-root", laneRoot,
		"--policy", policyPath,
		"--run-bin", runBin,
		"--item-bin", itemBin,
		"--brief-template", tmpl,
		"--gate", passStub,
		"--land", landStub,
		"--verdict", passStub,
		"--replay", "",
		"--rolling", "",
		"--fixer-gate", "",
		"--lanes", "2",
		"--no-notify",
		"--fallback-poll", "50ms",
		"--exit-when-drained",
	)
	if code != 0 {
		t.Fatalf("tillr-drain loop exited %d:\n%s", code, out)
	}

	raw, err := os.ReadFile(observed)
	if err != nil {
		t.Fatalf("reading adapter observation: %v\nloop output:\n%s", err, out)
	}
	wantWorktree := filepath.Join(laneRoot, itemID, "worktree")
	if !strings.Contains(string(raw), "PWD="+wantWorktree+"\n") {
		t.Fatalf("adapter ran in %q, want PWD=%s", string(raw), wantWorktree)
	}
	if !strings.Contains(string(raw), "LANE="+itemID+"\n") {
		t.Fatalf("adapter TILLR_LANE_ID mismatch: %q, want LANE=%s", string(raw), itemID)
	}

	for _, pool := range []string{"lane-1", "lane-2", "express-1", "express-2"} {
		if _, err := os.Stat(filepath.Join(laneRoot, pool)); !os.IsNotExist(err) {
			t.Errorf("pool lane directory %s should not be staged at start (stat err=%v)", pool, err)
		}
	}

	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		for _, pool := range []string{"lane-1", "lane-2", "express-1", "express-2"} {
			var n int
			if err := r.QueryRow(`SELECT count(*) FROM lanes WHERE lane_id = ?`, pool).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				t.Errorf("pool lane %s was registered in lanes table (%d rows)", pool, n)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Criterion 6: an express item runs with [lanes] express set and no express directory
// staged, and the `plan` and `why` faces name the item's own directory.
func TestExpressItemRunsWithoutExpressDirAndPlanWhyNameItemDir(t *testing.T) {
	runBin, itemBin := faceSiblingBins(t)
	expID := "item_exp_1"
	ordID := "item_ord_1"
	root, laneRoot, storePath, tmpl := newFaceLoopRepo(t, "[lanes]\nexpress = 2\n",
		faceItemRecord(expID, "in-queue", ""),
		faceItemRecord(ordID, "in-queue", ""),
	)
	setFixtureItemExpress(t, storePath, expID)

	scripts := scratch(t, "scripts")
	observed := filepath.Join(scripts, "observed.txt")
	adapter := writeExecScript(t, scripts, "adapter.sh", fmt.Sprintf(`
printf 'PWD=%%s\nLANE=%%s\n' "$(pwd)" "${TILLR_LANE_ID:-}" >> %q
printf 'express work\n' > exp.txt
git add exp.txt
git commit -q -m "express work"
printf 'RESULT: done\n'
`, observed))
	writeFile(t, filepath.Join(scripts, "pause-kind"), "stops-where-it-stands\n")
	passStub := writeExecScript(t, scripts, "pass.sh", `exit 0`)
	landStub := writeFaceLandStub(t, scripts, root, storePath)
	policyPath := writeFaceAdapterPolicy(t, adapter)

	// 1. `plan` names each item's own directory.
	planOut := filepath.Join(scratch(t, "plan"), "decisions.json")
	tickFile := tick(t, "2026-11-27T00:00:00Z", []string{"lane-1"}, 2, policyPath)
	code, out := invoke(t, "plan", "--root", root, "--store", storePath, "--input", tickFile, "--output", planOut)
	if code != 0 {
		t.Fatalf("plan exited %d:\n%s", code, out)
	}
	planRaw, err := os.ReadFile(planOut)
	if err != nil {
		t.Fatalf("reading plan output: %v", err)
	}
	var envs []struct {
		Payload drain.Decision `json:"payload"`
	}
	if err := json.Unmarshal(planRaw, &envs); err != nil {
		t.Fatalf("decoding plan decisions: %v\n%s", err, planRaw)
	}
	planLane := map[string]string{}
	for _, e := range envs {
		if e.Payload.Action == drain.ActionDispatch {
			planLane[e.Payload.ItemID] = e.Payload.LaneID
		}
	}
	if planLane[expID] != expID {
		t.Fatalf("plan dispatched express item %s to %q, want %q", expID, planLane[expID], expID)
	}
	if planLane[ordID] != ordID {
		t.Fatalf("plan dispatched ordinary item %s to %q, want %q", ordID, planLane[ordID], ordID)
	}

	// 2. `why` names each item's own directory.
	code, whyOut := invoke(t, "why", "--root", root, "--store", storePath, "--policy", policyPath, "--lanes", "1", "--json")
	if code != 0 {
		t.Fatalf("why exited %d:\n%s", code, whyOut)
	}
	var whyAns struct {
		Rows []drain.WhyRow `json:"rows"`
	}
	if err := json.Unmarshal(whyOut, &whyAns); err != nil {
		t.Fatalf("decoding why json: %v\n%s", err, whyOut)
	}
	whyLane := map[string]string{}
	for _, row := range whyAns.Rows {
		whyLane[row.ItemID] = row.Decision.LaneID
	}
	if whyLane[expID] != expID {
		t.Fatalf("why planned express item %s on %q, want %q", expID, whyLane[expID], expID)
	}
	if whyLane[ordID] != ordID {
		t.Fatalf("why planned ordinary item %s on %q, want %q", ordID, whyLane[ordID], ordID)
	}

	// 3. `loop` with --lanes 0 still runs the express item in its own item directory
	// outside the ordinary limit, with [lanes] express = 2 set and no express-* directory staged.
	code, loopOut := invoke(t, "loop",
		"--root", root,
		"--store", storePath,
		"--lane-root", laneRoot,
		"--policy", policyPath,
		"--run-bin", runBin,
		"--item-bin", itemBin,
		"--brief-template", tmpl,
		"--gate", passStub,
		"--land", landStub,
		"--verdict", passStub,
		"--replay", "",
		"--rolling", "",
		"--fixer-gate", "",
		"--lanes", "0",
		"--no-notify",
		"--fallback-poll", "50ms",
		"--exit-when-drained",
	)
	if code != 0 {
		t.Fatalf("loop exited %d:\n%s", code, loopOut)
	}
	raw, err := os.ReadFile(observed)
	if err != nil {
		t.Fatalf("reading express observation: %v\nloop output:\n%s", err, loopOut)
	}
	wantExpWT := filepath.Join(laneRoot, expID, "worktree")
	if !strings.Contains(string(raw), "PWD="+wantExpWT+"\n") {
		t.Fatalf("express item ran in %q, want PWD=%s", string(raw), wantExpWT)
	}
	for _, pool := range []string{"express-1", "express-2", "lane-1"} {
		if _, err := os.Stat(filepath.Join(laneRoot, pool)); !os.IsNotExist(err) {
			t.Errorf("pool directory %s must not be staged (stat err=%v)", pool, err)
		}
	}
}

// Criterion 7: a fixture item ends through `tillr-drain loop` and afterward its worktree
// and scratch are gone while its branch head, records and logs are present; the scratch
// of a still-running node is not deleted.
func TestLoopCollectsEndedItemWorktreeAndScratchWhilePreservingRunningScratch(t *testing.T) {
	runBin, itemBin := faceSiblingBins(t)
	doneID := "item_done_collect"
	runningID := "item_still_running"
	root, laneRoot, storePath, tmpl := newFaceLoopRepo(t, "",
		faceItemRecord(doneID, "in-queue", "a"),
		faceItemRecord(runningID, "in-queue", "b"),
	)

	scripts := scratch(t, "scripts")
	doneInfo := filepath.Join(scripts, "done-info.txt")
	runningStarted := filepath.Join(scripts, "running.started")
	releaseRunning := filepath.Join(scripts, "release.flag")

	adapter := writeExecScript(t, scripts, "adapter.sh", fmt.Sprintf(`
mkdir -p "${YOLO_DURABLE_DIR}"
printf 'scratch for %%s\n' "${TILLR_RUN_ID}" > "${YOLO_DURABLE_DIR}/build.out"
if [ "${TILLR_LANE_ID}" = %q ]; then
  printf 'done work\n' > done.txt
  git add done.txt
  git commit -q -m "done work"
  printf '%%s\n%%s\n' "${TILLR_RUN_ID}" "$(git rev-parse HEAD)" > %q
  printf 'RESULT: done\n'
  exit 0
fi
printf '%%s\n%%s\n' "${TILLR_RUN_ID}" "${YOLO_DURABLE_DIR}" > %q
i=0
while [ ! -f %q ] && [ "$i" -lt 300 ]; do
  sleep 0.1
  i=$((i + 1))
done
printf 'running work\n' > running.txt
git add running.txt
git commit -q -m "running work"
printf 'RESULT: done\n'
`, doneID, doneInfo, runningStarted, releaseRunning))
	writeFile(t, filepath.Join(scripts, "pause-kind"), "stops-where-it-stands\n")

	// When item_done_collect finishes its run, leave it ready to end (we close it via
	// tillr-item once both runs have produced their artifacts, while item_still_running
	// is still running). For item_still_running, passStub lands it cleanly.
	passStub := writeExecScript(t, scripts, "pass.sh", `exit 0`)
	landStub := writeFaceLandStub(t, scripts, root, storePath)
	policyPath := writeFaceAdapterPolicy(t, adapter)

	cmd := exec.Command(bin, "loop",
		"--root", root,
		"--store", storePath,
		"--lane-root", laneRoot,
		"--policy", policyPath,
		"--run-bin", runBin,
		"--item-bin", itemBin,
		"--brief-template", tmpl,
		"--gate", passStub,
		"--land", landStub,
		"--verdict", passStub,
		"--replay", "",
		"--rolling", "",
		"--fixer-gate", "",
		"--lanes", "2",
		"--no-notify",
		"--fallback-poll", "100ms",
		"--exit-when-drained",
	)
	cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	var loopOut strings.Builder
	cmd.Stdout = &loopOut
	cmd.Stderr = &loopOut
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting loop: %v", err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(releaseRunning, []byte("go\n"), 0o644)
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})

	// Wait until item_done_collect has landed (committed & settled) and item_still_running is live.
	const hangGuard = 5 * time.Minute
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		_, err1 := os.Stat(doneInfo)
		_, err2 := os.Stat(runningStarted)
		if err1 == nil && err2 == nil {
			// Also wait until item_done_collect is in landed state so its run has settled.
			s, err := store.OpenRead(context.Background(), storePath)
			if err == nil {
				var state string
				_ = s.ReadTx(context.Background(), func(r *store.Reader) error {
					return r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, doneID).Scan(&state)
				})
				s.Close()
				if state == "landed" {
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	doneMetaRaw, err := os.ReadFile(doneInfo)
	if err != nil {
		t.Fatalf("item_done_collect did not finish in time: %v\nloop:\n%s", err, loopOut.String())
	}
	doneLines := strings.Split(strings.TrimSpace(string(doneMetaRaw)), "\n")
	if len(doneLines) < 2 {
		t.Fatalf("unexpected doneInfo: %q", string(doneMetaRaw))
	}
	doneRunID, doneHead := doneLines[0], doneLines[1]

	runMetaRaw, err := os.ReadFile(runningStarted)
	if err != nil {
		t.Fatalf("item_still_running did not start in time: %v\nloop:\n%s", err, loopOut.String())
	}
	runLines := strings.Split(strings.TrimSpace(string(runMetaRaw)), "\n")
	if len(runLines) < 2 {
		t.Fatalf("unexpected runningStarted: %q", string(runMetaRaw))
	}
	runningScratch := runLines[1]

	// Verify running node's scratch exists before collection.
	runningScratchFile := filepath.Join(runningScratch, "build.out")
	if _, err := os.Stat(runningScratchFile); err != nil {
		t.Fatalf("running node scratch file missing before collection: %v", err)
	}

	// Prove item_done_collect's landing while item_still_running is still actively running
	// by advancing refs/tillr/green to trunk tip. On the loop's next pass, collection must
	// delete doneID's worktree and scratch while preserving its branch head, records, logs,
	// and the still-running node's scratch.
	trunkHead := runFixtureGit(t, root, "rev-parse", "main")
	runFixtureGit(t, root, "update-ref", "refs/tillr/green", trunkHead)

	doneWT := filepath.Join(laneRoot, doneID, "worktree")
	doneScratch := filepath.Join(laneRoot, doneID, "scratch")
	collectDeadline := time.Now().Add(hangGuard)
	for time.Now().Before(collectDeadline) {
		_, wtErr := os.Stat(doneWT)
		_, scErr := os.Stat(doneScratch)
		if os.IsNotExist(wtErr) && os.IsNotExist(scErr) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(doneWT); !os.IsNotExist(err) {
		t.Fatalf("ended item worktree %s still exists (err=%v)\nloop:\n%s", doneWT, err, loopOut.String())
	}
	if _, err := os.Stat(doneScratch); !os.IsNotExist(err) {
		t.Fatalf("ended item scratch %s still exists (err=%v)\nloop:\n%s", doneScratch, err, loopOut.String())
	}

	// The still-running node's scratch must NOT be deleted.
	if _, err := os.Stat(runningScratchFile); err != nil {
		t.Fatalf("still-running node scratch %s was deleted or missing: %v", runningScratchFile, err)
	}

	// Branch head (item pin + run pin), records, and run log for the ended item must remain present.
	itemPin := runFixtureGit(t, root, "rev-parse", "refs/tillr/items/"+doneID)
	if itemPin == "" {
		t.Errorf("pinned item ref refs/tillr/items/%s is empty", doneID)
	}
	runPin := runFixtureGit(t, root, "rev-parse", "refs/tillr/runs/"+doneRunID)
	if runPin != doneHead {
		t.Errorf("pinned run ref refs/tillr/runs/%s = %s, want %s", doneRunID, runPin, doneHead)
	}
	doneLog := filepath.Join(laneRoot, doneID, doneRunID+".log")
	if _, err := os.Stat(doneLog); err != nil {
		t.Errorf("ended item run log %s missing: %v", doneLog, err)
	}

	// Release item_still_running and let the loop drain and exit cleanly.
	if err := os.WriteFile(releaseRunning, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("loop exited with error after releasing running node: %v\n%s", err, loopOut.String())
	}

	// Verify store records (items, runs, collection event) are present for the ended item.
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var itemState string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, doneID).Scan(&itemState); err != nil {
			return fmt.Errorf("reading item record: %w", err)
		}
		if itemState != "landed" {
			t.Errorf("item state = %q, want landed", itemState)
		}
		var runCount int
		if err := r.QueryRow(`SELECT count(*) FROM runs WHERE run_id = ?`, doneRunID).Scan(&runCount); err != nil {
			return fmt.Errorf("reading run record: %w", err)
		}
		if runCount != 1 {
			t.Errorf("runs count for %s = %d, want 1", doneRunID, runCount)
		}
		var collectedCount int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = 'drain.ended-worktree-collected' AND object_id = ?`, doneID).Scan(&collectedCount); err != nil {
			return fmt.Errorf("reading collection event: %w", err)
		}
		if collectedCount < 1 {
			t.Errorf("expected drain.ended-worktree-collected event for %s, got %d", doneID, collectedCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
