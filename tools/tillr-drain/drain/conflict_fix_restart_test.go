package drain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func doneFencedWorkflowItemRecord(itemID, state, workflow string, paths ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"body_ref":      "briefs/" + itemID + ".md",
		"state":         state,
		"seat":          "build",
		"workflow":      workflow,
		"blocked_by":    []string{},
		"allowed_paths": paths,
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "criterion 1", "state": "done"},
		},
	})
}

// TestConflictFixRunSurvivesInPlaceLoopRestart restarts a real loop process in place
// while a returned conflict build run (-a2-n2) is live for a two-node (plan -> build)
// workflow item. The returned conflict run must stay alive across the restart, produce
// no run.finish with end_cause lease-reclaimed and no third attempt, and be settled and
// landed by the new loop.
func TestConflictFixRunSurvivesInPlaceLoopRestart(t *testing.T) {
	dir := scratch(t, "cfix-restart-live")
	cfixPIDFile := filepath.Join(dir, "cfix.pid")
	cfixRelease := filepath.Join(dir, "cfix.release")
	cfixSignals := filepath.Join(dir, "cfix.signals")
	planCountFile := filepath.Join(dir, "plan.count")
	t.Cleanup(func() { writeFile(t, cfixRelease, "release\n") })

	adapter := stub(t, "adapter", fmt.Sprintf(`set -eu
case "$TILLR_RUN_ID" in
 *-a2-n2)
  echo "$$" > %q
  trap 'echo term >> %q; exit 143' TERM
  while [ ! -f %q ]; do sleep 0.02; done
  echo repaired > base.txt
  git add base.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m repaired
  printf 'RESULT: done\n'
  exit 0 ;;
esac
case "${TILLR_NODE_ID:-}" in
 plan)
  echo "$TILLR_RUN_ID" >> %q
  printf 'RESULT: done\n'
  exit 0 ;;
 *)
  echo candidate > base.txt
  git add base.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
  printf 'RESULT: done\n'
  exit 0 ;;
esac
`, cfixPIDFile, cfixSignals, cfixRelease, planCountFile))

	f := newLoopFixture(t, adapter)
	f.policy.Seats = map[string]string{
		"plan":  "fixture",
		"build": "fixture",
	}
	wfPath := filepath.Join(f.root, "config/tillr/workflows/two-step@v1.json")
	if err := os.MkdirAll(filepath.Dir(wfPath), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wfPath, `{"name":"two-step","version":1,"stages":[
		{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},
		{"id":"build","kind":"agent","seat":"build","access":"write"}],"edges":[
		{"from_node":"plan","to_node":"build"}]}`)
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[workflows.default]\nbuild = 'two-step@v1'\n")
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-qm", "workflow fixture")
	fixtureAt(t, f.store, doneFencedWorkflowItemRecord("item_041", "in-queue", "two-step@v1", "base.txt"))

	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	landed := landingEnvelope(t, "landed", nil)
	land := filepath.Join(dir, "land")
	writeFile(t, land, fmt.Sprintf(`#!/bin/sh
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
[ "${1:-}" = "--help" ] && exit 1
if [ ! -f %[1]q/land-once ]; then
  touch %[1]q/land-once
  echo trunk > %[1]q/base.txt
  git -C %[1]q add base.txt
  git -C %[1]q commit -q -m "trunk move"
  printf '%%s\n' '%[2]s'
  exit 1
fi
printf '%%s\n' '%[3]s'
exit 0
`, f.root, conflict, landed))
	if err := os.Chmod(land, 0o755); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(dir, "tillr-drain")
	copyFile(t, drainBin(t), exe, nil)
	policyPath := filepath.Join(dir, "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policyPath, string(raw))

	outPath := filepath.Join(dir, "loop.out")
	outFile, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { outFile.Close() })

	adapterDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends"))
	if err != nil {
		t.Fatal(err)
	}
	itemWrap := filepath.Join(dir, "item-wrap")
	writeFile(t, itemWrap, fmt.Sprintf("#!/bin/sh\nif [ \"${1:-}\" = state ]; then exit 0; fi\nexec %q \"$@\"\n", sibling(t, "item")))
	if err := os.Chmod(itemWrap, 0o755); err != nil {
		t.Fatal(err)
	}

	s := &rollScenario{f: f, exe: exe, out: outPath, lanes: 1}
	briefTpl := filepath.Join(adapterDir, "brief-template.md")
	s.cmd = exec.Command(exe, "loop",
		"--root", f.root,
		"--store", f.store,
		"--lane-root", f.laneRoot,
		"--lanes", "1",
		"--policy", policyPath,
		"--run-bin", sibling(t, "run"),
		"--item-bin", itemWrap,
		"--brief-template", briefTpl,
		"--gate", always(t, "gate", 0),
		"--land", land,
		"--verdict", always(t, "verdict", 0),
	)
	s.cmd.Stdout, s.cmd.Stderr = outFile, outFile
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if here, err := presence.CurrentOwner(); err == nil {
		if start, err := presence.ProcessStart(s.cmd.Process.Pid); err == nil && start != "" {
			s.loopOwner = presence.Owner{PID: s.cmd.Process.Pid, Start: start, Domain: here.Domain, UID: here.UID}
		}
	}
	t.Cleanup(func() { s.stop(t) })

	// Wait for the conflict-repair (-cfix) run to be live.
	s.waitFor(t, "conflict-repair run to start", func() bool {
		raw, err := os.ReadFile(cfixPIDFile)
		return err == nil && strings.TrimSpace(string(raw)) != ""
	})
	cfixPID, err := strconv.Atoi(strings.TrimSpace(readText(t, cfixPIDFile)))
	if err != nil || !alive(cfixPID) {
		t.Fatalf("cfix PID %d not alive before restart: %v\n%s", cfixPID, err, s.output(t))
	}

	// Replace the binary and restart the loop in place while -cfix is live.
	s.replaceBinary(t)
	answer := s.restart(t)
	if !strings.Contains(answer, "restarted") {
		t.Fatalf("restart answer = %q, want restarted\n%s", answer, s.output(t))
	}

	// Give the successor loop time to run its startup reap and first pass.
	time.Sleep(1500 * time.Millisecond)

	if !alive(cfixPID) {
		t.Fatalf("conflict-repair process %d died across in-place restart\n%s", cfixPID, s.output(t))
	}
	if sigs, _ := os.ReadFile(cfixSignals); len(strings.TrimSpace(string(sigs))) > 0 {
		t.Fatalf("conflict-repair process received signal across restart: %s\n%s", sigs, s.output(t))
	}

	// Release the -cfix worker and wait for the new loop to settle and land the item.
	writeFile(t, cfixRelease, "release\n")
	s.waitFor(t, "cfix run to settle and land after restart", func() bool {
		return strings.Contains(s.output(t), "loop: landed item_041")
	})

	// Verify no run.finish with end_cause=lease-reclaimed, no drain.orphaned, and no third attempt.
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "run.finish" && strings.Contains(string(e.Payload), "lease-reclaimed") {
			t.Fatalf("unexpected lease-reclaimed run.finish: %s\n%s", e.Payload, s.output(t))
		}
		if e.Kind == "drain.orphaned" {
			t.Fatalf("unexpected drain.orphaned: %s\n%s", e.Payload, s.output(t))
		}
		if e.Kind == "run.allocate" && strings.Contains(string(e.Payload), "run-item_041-a3") {
			t.Fatalf("unexpected third attempt run.allocate: %s\n%s", e.Payload, s.output(t))
		}
	}
	if planRuns := lines(t, planCountFile); len(planRuns) != 1 {
		t.Fatalf("plan runs = %v, want 1 (reused across returned conflict, no third attempt)\n%s", planRuns, s.output(t))
	}
}

// TestConflictFixEndedAtRestartResumesConflictRepairNotFirstNode simulates a returned
// conflict build run (-a2-n2) ending at an in-place restart (reaped with run.finish
// timed-out/lease-reclaimed and drain.orphaned) for a two-node (plan -> build) workflow
// item. The successor loop must resume the interrupted conflict build node rather than
// starting a brand-new attempt at the plan node.
func TestConflictFixEndedAtRestartResumesConflictRepairNotFirstNode(t *testing.T) {
	dir := scratch(t, "cfix-restart-ended")
	cfixCountFile := filepath.Join(dir, "cfix.count")
	planCountFile := filepath.Join(dir, "plan.count")
	buildCountFile := filepath.Join(dir, "build.count")

	adapter := stub(t, "adapter", fmt.Sprintf(`set -eu
case "$TILLR_RUN_ID" in
 *-a2-n2*)
  echo "$TILLR_RUN_ID" >> %q
  echo repaired > base.txt
  git add base.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m repaired
  printf 'RESULT: done\n'
  exit 0 ;;
esac
case "${TILLR_NODE_ID:-}" in
 plan)
  echo "$TILLR_RUN_ID" >> %q
  printf 'RESULT: done\n'
  exit 0 ;;
 *)
  echo "$TILLR_RUN_ID" >> %q
  echo candidate > base.txt
  git add base.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
  printf 'RESULT: done\n'
  exit 0 ;;
esac
`, cfixCountFile, planCountFile, buildCountFile))

	f := newLoopFixture(t, adapter)
	f.policy.Seats = map[string]string{
		"plan":  "fixture",
		"build": "fixture",
	}
	wfPath := filepath.Join(f.root, "config/tillr/workflows/two-step@v1.json")
	if err := os.MkdirAll(filepath.Dir(wfPath), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, wfPath, `{"name":"two-step","version":1,"stages":[
		{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},
		{"id":"build","kind":"agent","seat":"build","access":"write"}],"edges":[
		{"from_node":"plan","to_node":"build"}]}`)
	writeFile(t, filepath.Join(f.root, ".tillr.toml"), "[workflows.default]\nbuild = 'two-step@v1'\n")
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-qm", "workflow fixture")
	fixtureAt(t, f.store, doneFencedWorkflowItemRecord("item_042", "in-queue", "two-step@v1", "base.txt"))

	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	landed := landingEnvelope(t, "landed", nil)
	land := filepath.Join(dir, "land")
	writeFile(t, land, fmt.Sprintf(`#!/bin/sh
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
[ "${1:-}" = "--help" ] && exit 1
case "$*" in
 *--run*-a2-n2*)
  printf '%%s\n' '%[3]s'
  exit 0 ;;
 *)
  if [ ! -f %[1]q/land-once ]; then
    touch %[1]q/land-once
    echo trunk > %[1]q/base.txt
    git -C %[1]q add base.txt
    git -C %[1]q commit -q -m "trunk move"
  fi
  printf '%%s\n' '%[2]s'
  exit 1 ;;
esac
`, f.root, conflict, landed))
	if err := os.Chmod(land, 0o755); err != nil {
		t.Fatal(err)
	}

	// First loop: intercept the first -cfix Run call to simulate the process vanishing
	// across an in-place restart (stream files exist, no run.finish, loop exits).
	baseSteps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	var lostCfixID string
	intercept := &interceptCfixSteps{
		ProcessSteps: baseSteps,
		onFirstCfix: func(runID string) {
			lostCfixID = runID
			// Create stream files so orphan.go treats the run face as vanished (not unlaunched)
			// and reaps it with run.finish (timed-out / lease-reclaimed) + drain.orphaned.
			outDir := filepath.Join(f.root, ".tillr", "loop", runID)
			_ = os.MkdirAll(outDir, 0o755)
			writeFile(t, filepath.Join(outDir, drain.RunRecordFile), "")
			writeFile(t, filepath.Join(outDir, drain.RunStderrFile), "")
			cancel1()
		},
	}
	stage := &drain.LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.store,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      baseSteps.Now,
	}
	loop1, err := drain.NewLoop(drain.LoopConfig{
		ExitWhenDrained: true,
		Root:            f.root,
		Store:           f.store,
		LaneRoot:        f.laneRoot,
		Lanes:           []string{"lane-1"},
		Policy:          f.policy,
		Steps:           intercept,
		Stage:           stage,
		Now:             baseSteps.Now,
		Report:          os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = loop1.Run(ctx1)
	if lostCfixID == "" {
		t.Fatal("first loop did not reach conflict-fix handoff")
	}

	// Second loop (after restart): must reap the vanished -cfix run, restore the built
	// candidate, run a fresh -cfix run on that candidate, and land it without restarting plan.
	var out2 strings.Builder
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	loop2 := f.newLoopWith(t, 1, baseSteps, stage, &out2)
	_, err = loop2.Run(ctx2)
	if err != nil && !strings.Contains(out2.String(), "loop: landed item_042") {
		t.Fatalf("second loop err = %v\n%s", err, out2.String())
	}

	// Verify the first -cfix was reaped as lease-reclaimed + drain.orphaned.
	sawReaped, sawOrphaned := false, false
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "run.finish" && e.ObjectID != nil && *e.ObjectID == lostCfixID && strings.Contains(string(e.Payload), "lease-reclaimed") {
			sawReaped = true
		}
		if e.Kind == "drain.orphaned" && e.ObjectID != nil && *e.ObjectID == lostCfixID {
			sawOrphaned = true
		}
	}
	if !sawReaped || !sawOrphaned {
		t.Fatalf("expected vanished %s to be reaped (sawReaped=%v, sawOrphaned=%v)\n%s", lostCfixID, sawReaped, sawOrphaned, out2.String())
	}

	// Verify plan and initial build each ran only ONCE, and the resumed returned conflict build run landed the item.
	if planRuns := lines(t, planCountFile); len(planRuns) != 1 {
		t.Fatalf("plan runs = %v, want exactly 1 (must resume interrupted build node, not restart at plan)\n%s", planRuns, out2.String())
	}
	if buildRuns := lines(t, buildCountFile); len(buildRuns) != 1 {
		t.Fatalf("build runs = %v, want exactly 1 (initial attempt)\n%s", buildRuns, out2.String())
	}
	if cfixRuns := lines(t, cfixCountFile); len(cfixRuns) != 1 {
		t.Fatalf("resumed conflict build runs = %v, want 1\n%s", cfixRuns, out2.String())
	}
	if !strings.Contains(out2.String(), "loop: landed item_042") {
		t.Fatalf("item_042 did not land after resumed conflict repair\n%s", out2.String())
	}
	// Mark item_042 landed in store is stubbed by always(item,0), so verify no third attempt was allocated.
	db, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var a3Count int
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id LIKE 'run-item_042-a3%'`).Scan(&a3Count)
	}); err != nil {
		t.Fatal(err)
	}
	if a3Count != 0 {
		t.Fatalf("unexpected third attempt runs (%d) for item_042\n%s", a3Count, out2.String())
	}
}

type interceptCfixSteps struct {
	*drain.ProcessSteps
	onFirstCfix func(runID string)
	hit         bool
}

func (s *interceptCfixSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if !s.hit && strings.HasSuffix(req.RunID, "-a2-n2") {
		s.hit = true
		s.onFirstCfix(req.RunID)
		return run.Record{}, "", context.Canceled
	}
	return s.ProcessSteps.Run(ctx, req)
}
