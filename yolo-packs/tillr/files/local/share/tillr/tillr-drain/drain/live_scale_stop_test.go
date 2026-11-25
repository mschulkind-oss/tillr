package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/store"
)

func startUnflaggedFixture(t *testing.T, f loopFixture, extraArgs ...string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	policy := filepath.Join(scratch(t, "unflagged-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	args := []string{
		"loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		"--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0),
	}
	args = append(args, extraArgs...)
	cmd := exec.Command(drainBin(t), args...)
	out := new(strings.Builder)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return cmd, out
}

func readSavedOrdinaryLanes(t *testing.T, path string) (int, bool) {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	var found bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var exists bool
		if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='drain_runtime_config')`).Scan(&exists); err != nil || !exists {
			return err
		}
		err := r.QueryRow(`SELECT value_json FROM drain_runtime_config WHERE key='ordinary_lanes'`).Scan(&count)
		if err == nil {
			found = true
			return nil
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count, found
}

func TestLoopScalesAndStopsWithLiveRuns(t *testing.T) {
	markers := scratch(t, "live-scale")
	t.Setenv("LIVE_SCALE_TEST", markers)
	adapter := stub(t, "live-scale-worker", counting+`touch "$LIVE_SCALE_TEST/$TILLR_RUN_ID.started"
while [ ! -f "$LIVE_SCALE_TEST/$TILLR_RUN_ID.release" ] && [ ! -f "$LIVE_SCALE_TEST/cleanup" ]; do sleep 0.02; done
`+committingAdapterBody+`printf 'RESULT: done\n'`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"), itemRecordWithFence("item_b", "in-queue", "candidate.txt"))
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "cleanup"), "finish") })
	// Record the actual exec face PID, not an adapter descendant.
	runBin := stub(t, "record-run-pid", `if [ "$1" = exec ]; then echo "$$" > "$LIVE_SCALE_TEST/$TILLR_RUN_ID.pid"; fi
exec "`+sibling(t, "run")+`" "$@"`)
	cmd, out := startStopFixtureWithRun(t, f, 1, runBin)
	const first, second = "run-item_a-a1", "run-item_b-a1"
	waitForFile(t, filepath.Join(markers, first+".started"), hangGuard)
	scale := func(count string) {
		t.Helper()
		if code, output := invoke(t, "scale", "--root", f.root, "--store", f.store, "--lanes", count); code != 0 {
			t.Fatalf("scale: %d: %s", code, output)
		}
	}
	waitFor := func(what string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(hangGuard)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	verdict := func(id string) bool {
		for _, e := range stopEventsOf(t, f.store, "gate.verdict") {
			if e.ObjectID != nil && *e.ObjectID == id {
				return true
			}
		}
		return false
	}
	scale("2")
	waitForFile(t, filepath.Join(markers, second+".started"), hangGuard)
	if n := len(terminalRunRows(t, f.store)); n != 0 {
		t.Fatalf("scaling ended a run: %d terminal rows", n)
	}
	pid, err := os.ReadFile(filepath.Join(markers, first+".pid"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(markers, first+".release"), "finish")
	waitFor("reaping finished run face "+strings.TrimSpace(string(pid)), func() bool {
		_, err := os.Stat(filepath.Join("/proc", strings.TrimSpace(string(pid))))
		return os.IsNotExist(err)
	})
	waitFor("the first run's verdict after scaling up", func() bool { return verdict(first) })

	offersNoLanes := func() bool {
		ctx := context.Background()
		s, err := store.OpenRead(ctx, f.store)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var empty bool
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			lanes, live, err := drain.LiveLoopLanes(r, f.store, time.Now())
			empty = live && len(lanes) == 0
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return empty
	}
	scale("0")
	waitFor("the live loop to offer zero lanes", offersNoLanes)

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	if raw, err := exec.CommandContext(ctx, drainBin(t), "stop", "--root", f.root, "--store", f.store).CombinedOutput(); err != nil {
		t.Fatalf("stop: %v: %s", err, raw)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop: %v: %s", err, out.String())
		}
	case <-ctx.Done():
		t.Fatal("stopped loop did not exit while the second run was still executing")
	}
	if n := len(terminalRunRows(t, f.store)); n != 1 {
		t.Fatalf("stop did not leave the second run in flight: %d terminal rows", n)
	}
	next, nextOut := startUnflaggedFixture(t, f)
	waitFor("the successor to honor the saved zero target", offersNoLanes)
	writeFile(t, filepath.Join(markers, second+".release"), "finish")
	waitFor("the successor to examine the surviving run", func() bool { return verdict(second) })
	stopFixture(t, f, next, nextOut, false)
	if !strings.Contains(nextOut.String(), "loop: ordinary lanes: 0 (from saved target)") {
		t.Fatalf("unflagged successor did not report saved zero target:\n%s", nextOut.String())
	}
}

func TestFreshStartExplicitLanesReplacesSavedTargetAndReportsSource(t *testing.T) {
	markers := scratch(t, "fresh-explicit-lanes")
	t.Setenv("FRESH_LANES_TEST", markers)
	adapter := stub(t, "fresh-lanes-worker", counting+`touch "$FRESH_LANES_TEST/$TILLR_RUN_ID.started"
while [ ! -f "$FRESH_LANES_TEST/$TILLR_RUN_ID.release" ] && [ ! -f "$FRESH_LANES_TEST/cleanup" ]; do sleep 0.02; done
`+committingAdapterBody+`printf 'RESULT: done\n'`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store,
		itemRecordWithFence("item_a", "in-queue", "candidate_a.txt"),
		itemRecordWithFence("item_b", "in-queue", "candidate_b.txt"),
	)
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "cleanup"), "finish") })

	if code, output := invoke(t, "scale", "--root", f.root, "--store", f.store, "--lanes", "6"); code != 0 {
		t.Fatalf("scale 6: %d: %s", code, output)
	}

	// --check with --lanes 1 must not mutate the saved target 6.
	policy := filepath.Join(scratch(t, "check-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	if code, output := invoke(t, "loop", "--check", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--lanes", "1", "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		"--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0),
	); code != 0 {
		t.Fatalf("loop --check: %d: %s", code, output)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 6 {
		t.Fatalf("loop --check mutated saved target: got %d (ok=%v), want 6", saved, ok)
	}

	// Fresh start with --lanes 1 replaces saved target 6, runs 1 lane, and says so at start.
	cmd, out := startStopFixture(t, f, 1)
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "cleanup"), "finish") })
	waitForFile(t, filepath.Join(markers, "run-item_a-a1.started"), hangGuard)

	// Wait until at least one pass has completed in drain_passes so pass-start
	// refreshLaneCount and full ready-set planning have both executed.
	ctx := context.Background()
	deadline := time.Now().Add(hangGuard)
	for {
		s, err := store.OpenRead(ctx, f.store)
		if err != nil {
			t.Fatal(err)
		}
		var completedPasses int
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM drain_passes WHERE ended_at IS NOT NULL`).Scan(&completedPasses)
		})
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if completedPasses >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for first drain pass to finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := os.Stat(filepath.Join(markers, "run-item_b-a1.started")); !os.IsNotExist(err) {
		t.Fatalf("second item started while first item held the single lane (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(f.laneRoot, "lane-2")); !os.IsNotExist(err) {
		t.Fatalf("lane-2 worktree was staged despite --lanes 1 (err=%v)", err)
	}

	s, err := store.OpenRead(ctx, f.store)
	if err != nil {
		t.Fatal(err)
	}
	var liveLanes []string
	var live bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		liveLanes, live, err = drain.LiveLoopLanes(r, f.store, time.Now())
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	if !live || !slices.Equal(liveLanes, []string{"lane-1"}) {
		t.Fatalf("live loop lanes = %v (live=%v), want [lane-1]", liveLanes, live)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 1 {
		t.Fatalf("saved ordinary_lanes after fresh --lanes 1 = %d (ok=%v), want 1", saved, ok)
	}

	writeFile(t, filepath.Join(markers, "run-item_a-a1.release"), "finish")
	writeFile(t, filepath.Join(markers, "run-item_b-a1.release"), "finish")
	waitForFile(t, filepath.Join(markers, "run-item_b-a1.started"), hangGuard)
	stopFixture(t, f, cmd, out, false)

	log := out.String()
	buildIdx := strings.Index(log, "loop: build ")
	lanesLine := "loop: ordinary lanes: 1 (from --lanes flag; replaced saved target 6)"
	lanesIdx := strings.Index(log, lanesLine)
	if buildIdx < 0 || lanesIdx < 0 || buildIdx > lanesIdx {
		t.Fatalf("startup log missing build-then-lanes startup lines (buildIdx=%d, lanesIdx=%d):\n%s", buildIdx, lanesIdx, log)
	}
	if strings.Contains(log, "ordinary lane target is now 6") {
		t.Fatalf("loop logged stale saved target 6 despite --lanes 1:\n%s", log)
	}
}

func TestStartupLaneProvenanceForDefaultZeroNegativeAndInPlaceRestart(t *testing.T) {
	f := newLoopFixture(t, stub(t, "noop-worker", `printf 'RESULT: done\n'`))
	fixtureAt(t, f.store)
	policy := filepath.Join(scratch(t, "prov-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	baseArgs := []string{
		"loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		"--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0),
		"--exit-when-drained",
	}

	// 1. Negative --lanes is refused.
	if code, output := invoke(t, append(slices.Clone(baseArgs), "--lanes", "-1")...); code != 2 {
		t.Fatalf("--lanes -1 code = %d, want 2:\n%s", code, output)
	}

	// 2. Unflagged start with no saved target reports default 2.
	code, output := invoke(t, baseArgs...)
	if code != 0 {
		t.Fatalf("default start: %d:\n%s", code, output)
	}
	if !strings.Contains(string(output), "loop: ordinary lanes: 2 (from default)") {
		t.Fatalf("default start missing default provenance:\n%s", output)
	}

	// 3. Explicit --lanes 0 on fresh start runs 0 lanes and replaces saved target.
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--lanes", "3"); code != 0 {
		t.Fatalf("scale 3: %d: %s", code, out)
	}
	code, output = invoke(t, append(slices.Clone(baseArgs), "--lanes", "0")...)
	if code != 0 {
		t.Fatalf("--lanes 0 start: %d:\n%s", code, output)
	}
	if !strings.Contains(string(output), "loop: ordinary lanes: 0 (from --lanes flag; replaced saved target 3)") {
		t.Fatalf("--lanes 0 start missing replacement provenance:\n%s", output)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 0 {
		t.Fatalf("saved ordinary_lanes after --lanes 0 = %d (ok=%v), want 0", saved, ok)
	}

	// 4. Adopted in-place restart (TILLR_RESTART_REQUEST set) preserves live-scaled target 5
	// even when os.Args still carries boot-time --lanes 1.
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--lanes", "5"); code != 0 {
		t.Fatalf("scale 5: %d: %s", code, out)
	}
	reqCode, reqOut := invoke(t, "restart", "--root", f.root, "--store", f.store)
	if reqCode != 0 {
		t.Fatalf("restart request: %d: %s", reqCode, reqOut)
	}
	reqID := strings.TrimSpace(string(reqOut))
	cmd := exec.Command(drainBin(t), append(slices.Clone(baseArgs), "--lanes", "1")...)
	cmd.Env = append(os.Environ(), restart.RequestEnv+"="+reqID, restart.FromEnv+"=old-build")
	restartRaw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("adopted restart loop: %v:\n%s", err, restartRaw)
	}
	restartLog := string(restartRaw)
	if !strings.Contains(restartLog, "loop: ordinary lanes: 5 (from saved target)") {
		t.Fatalf("adopted restart did not keep saved target 5:\n%s", restartLog)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 5 {
		t.Fatalf("adopted restart overwrote saved target: got %d (ok=%v), want 5", saved, ok)
	}
}

func TestMaxAgentsFlagAndScaleAlias(t *testing.T) {
	f := newLoopFixture(t, stub(t, "noop-worker", `printf 'RESULT: done\n'`))
	fixtureAt(t, f.store)
	policy := filepath.Join(scratch(t, "max-agents-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	baseArgs := []string{
		"loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		"--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0),
		"--exit-when-drained",
	}

	// Help output advertises --max-agents and keeps --lanes as an alias.
	for _, face := range []string{"loop", "scale"} {
		code, out := invoke(t, face, "--help")
		if code != 0 {
			t.Fatalf("%s --help code = %d, want 0:\n%s", face, code, out)
		}
		if !strings.Contains(string(out), "-max-agents") || !strings.Contains(string(out), "-lanes") {
			t.Fatalf("%s --help missing -max-agents or -lanes:\n%s", face, out)
		}
	}

	// scale --max-agents and scale --lanes set the same saved target.
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--max-agents", "4"); code != 0 {
		t.Fatalf("scale --max-agents 4: %d: %s", code, out)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 4 {
		t.Fatalf("saved target after scale --max-agents 4 = %d (ok=%v), want 4", saved, ok)
	}
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--lanes", "2"); code != 0 {
		t.Fatalf("scale --lanes 2: %d: %s", code, out)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 2 {
		t.Fatalf("saved target after scale --lanes 2 = %d (ok=%v), want 2", saved, ok)
	}

	// scale validation: missing, negative, and disagreeing flags are refused; matching flags succeed.
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store); code != 2 {
		t.Fatalf("scale without limit code = %d, want 2:\n%s", code, out)
	}
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--max-agents", "-1"); code != 2 {
		t.Fatalf("scale --max-agents -1 code = %d, want 2:\n%s", code, out)
	}
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--max-agents", "3", "--lanes", "5"); code != 2 {
		t.Fatalf("scale disagreeing flags code = %d, want 2:\n%s", code, out)
	}
	if code, out := invoke(t, "scale", "--root", f.root, "--store", f.store, "--max-agents", "3", "--lanes", "3"); code != 0 {
		t.Fatalf("scale agreeing flags code = %d, want 0:\n%s", code, out)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 3 {
		t.Fatalf("saved target after agreeing scale flags = %d (ok=%v), want 3", saved, ok)
	}

	// loop validation: negative --max-agents and disagreeing --max-agents/--lanes are refused.
	if code, out := invoke(t, append(slices.Clone(baseArgs), "--max-agents", "-1")...); code != 2 {
		t.Fatalf("loop --max-agents -1 code = %d, want 2:\n%s", code, out)
	}
	if code, out := invoke(t, append(slices.Clone(baseArgs), "--max-agents", "1", "--lanes", "2")...); code != 2 {
		t.Fatalf("loop disagreeing flags code = %d, want 2:\n%s", code, out)
	}

	// loop --check --max-agents 1 does not mutate the saved target 3.
	if code, out := invoke(t, append(slices.Clone(baseArgs), "--check", "--max-agents", "1")...); code != 0 {
		t.Fatalf("loop --check --max-agents 1: %d:\n%s", code, out)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 3 {
		t.Fatalf("loop --check --max-agents 1 mutated saved target: got %d (ok=%v), want 3", saved, ok)
	}

	// Fresh start with --max-agents 0 replaces saved target 3.
	if code, out := invoke(t, append(slices.Clone(baseArgs), "--max-agents", "0")...); code != 0 {
		t.Fatalf("loop --max-agents 0: %d:\n%s", code, out)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 0 {
		t.Fatalf("saved target after loop --max-agents 0 = %d (ok=%v), want 0", saved, ok)
	}

	// Fresh start with --max-agents 1 sets the running-agent limit to 1.
	if code, out := invoke(t, append(slices.Clone(baseArgs), "--max-agents", "1")...); code != 0 {
		t.Fatalf("loop --max-agents 1: %d:\n%s", code, out)
	}
	if saved, ok := readSavedOrdinaryLanes(t, f.store); !ok || saved != 1 {
		t.Fatalf("saved target after loop --max-agents 1 = %d (ok=%v), want 1", saved, ok)
	}
}
