package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func detachedGateTestSteps(t *testing.T, f refusalLoopFixture, now func() time.Time, gateProg string) *ProcessSteps {
	t.Helper()
	landProg := landStub(t, landRecord(t, LandLanded, nil), 0)
	verdictProg := executable(t, "exit 0\n")
	itemBin := executable(t, `exec python3 - "$@" <<'PY'
import sqlite3, sys
args = sys.argv[1:]
if not args:
    sys.exit(0)
if args[0] == "sync":
    print('{"kind":"item.sync","payload":{}}')
    sys.exit(0)
if args[0] == "state":
    store = args[args.index("--store") + 1]
    item_id = args[args.index("--item") + 1]
    state = args[args.index("--state") + 1]
    conn = sqlite3.connect(store)
    conn.execute("UPDATE items SET state=? WHERE item_id=?", (state, item_id))
    conn.commit()
    conn.close()
    sys.exit(0)
PY
`)
	return &ProcessSteps{
		Root:           f.root,
		Store:          f.storePath,
		LaneRoot:       f.laneRoot,
		Now:            now,
		GateProgram:    gateProg,
		LandProgram:    landProg,
		VerdictProgram: verdictProg,
		ItemBin:        itemBin,
	}
}

func configureDetachedGateLoop(t *testing.T, f refusalLoopFixture, steps Steps) *Loop {
	t.Helper()
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	l.cfg.Report = io.Discard
	return l
}

func blockingCounterGateProgram(t *testing.T, workDir, counterFile, enteredFile, releaseFile string) string {
	t.Helper()
	script := fmt.Sprintf(`exec python3 - <<'PY'
import fcntl, os, pathlib, time

counter = pathlib.Path(%q)
with counter.open("a+") as f:
    fcntl.flock(f.fileno(), fcntl.LOCK_EX)
    f.seek(0)
    raw = f.read().strip()
    n = int(raw) if raw else 0
    f.seek(0)
    f.truncate()
    f.write(str(n + 1) + "\n")
    f.flush()
    fcntl.flock(f.fileno(), fcntl.LOCK_UN)

entered = pathlib.Path(%q)
entered.parent.mkdir(parents=True, exist_ok=True)
entered.write_text(str(os.getpid()) + "\n")

release = pathlib.Path(%q)
while not release.exists():
    time.Sleep(0.02) if hasattr(time, "Sleep") else time.sleep(0.02)
PY
`, counterFile, enteredFile, releaseFile)
	return executable(t, script)
}

func readCounterFile(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("parsing counter %q: %v", string(raw), err)
	}
	return n
}

func readRestartEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func readLatestRestartAnswer(t *testing.T, storePath, reqID string) restart.Answer {
	t.Helper()
	answers, err := restart.ReadAnswers(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	ans, ok := answers[restart.RoleDrain]
	if !ok || ans.RequestID != reqID {
		t.Fatalf("missing restart answer for %s (got %+v)", reqID, answers)
	}
	return ans
}

func assertLaneLeasedTo(t *testing.T, storePath, laneID, wantRunID string) {
	t.Helper()
	s := openFixture(t, storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var state string
		var runID sql.NullString
		if err := r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id=?`, laneID).Scan(&state, &runID); err != nil {
			return err
		}
		if state != "leased" || !runID.Valid || runID.String != wantRunID {
			return fmt.Errorf("lane %s state=%q run_id=%q, want leased/%q", laneID, state, runID.String, wantRunID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedGateSurvivesLoopRestartAndSettlesWithoutRerunning(t *testing.T) {
	t.Run("finishes_after_successor_starts", func(t *testing.T) {
		testDetachedGateSurvivesLoopRestart(t, false)
	})
	t.Run("finishes_between_restart_and_successor_first_pass", func(t *testing.T) {
		testDetachedGateSurvivesLoopRestart(t, true)
	})
}

func testDetachedGateSurvivesLoopRestart(t *testing.T, releaseBeforeSuccessor bool) {
	t.Helper()
	t.Setenv("TILLR_LOOP_STORE", "")
	t.Setenv(restart.RequestEnv, "")
	t.Setenv(restart.FromEnv, "")

	f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
	holdWALKeeper(t, f.storePath)

	work := scratchUnderDurable(t)
	counterFile := filepath.Join(work, "gate-count")
	enteredFile := filepath.Join(work, "gate-entered")
	releaseFile := filepath.Join(work, "gate-release")
	gateProg := blockingCounterGateProgram(t, work, counterFile, enteredFile, releaseFile)

	steps1 := detachedGateTestSteps(t, f, time.Now, gateProg)
	l1 := configureDetachedGateLoop(t, f, steps1)
	c := prepareReadyCandidate(t, f, l1, "item-a")

	exe := filepath.Join(work, "tillr-drain-next")
	if err := writeForkSafe(exe, []byte("replacement-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execMu sync.Mutex
	var capturedEnv []string
	l1.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
		Exec: func(_ string, _ []string, env []string) error {
			execMu.Lock()
			capturedEnv = append([]string(nil), env...)
			execMu.Unlock()
			return nil
		},
	}

	var reportBuf bytes.Buffer
	l1.cfg.Report = &reportBuf
	ctx1, cancel1 := context.WithTimeout(context.Background(), hangGuard)
	defer cancel1()
	done1 := make(chan struct{})
	var res1 LoopResult
	var err1 error
	go func() {
		defer close(done1)
		res1, err1 = l1.Run(ctx1)
	}()

	for {
		if _, err := os.Stat(enteredFile); err == nil {
			break
		}
		select {
		case <-done1:
			t.Fatalf("l1 exited before %s: res=%+v err=%v\nreport:\n%s", enteredFile, res1, err1, reportBuf.String())
		default:
			time.Sleep(15 * time.Millisecond)
		}
	}
	runDir := RunDir(f.root, c.RunID)
	face, ok := readDetachedGateFace(runDir)
	if !ok {
		t.Fatalf("missing %s in %s while gate blocked", GateFaceFile, runDir)
	}
	if !processAlive(face.PID) {
		t.Fatalf("detached gate supervisor pid %d not alive", face.PID)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("cleanup\n"), 0o644)
		_ = syscall.Kill(face.PID, syscall.SIGKILL)
	})

	req, err := restart.File(ctx1, f.storePath, restart.RoleDrain, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done1:
	case <-time.After(hangGuard):
		t.Fatalf("l1 did not replace itself while detached gate was blocked")
	}
	if err1 != nil || res1.Stop != StopRestarted {
		t.Fatalf("l1 stopped with %+v, %v; want %q", res1, err1, StopRestarted)
	}
	if _, err := os.Stat(releaseFile); !os.IsNotExist(err) {
		t.Fatalf("gate-release already existed before l1 exited")
	}
	if !processAlive(face.PID) {
		t.Fatalf("detached gate supervisor pid %d died when l1 exited", face.PID)
	}
	assertLaneLeasedTo(t, f.storePath, c.LaneID, c.RunID)

	execMu.Lock()
	reqEnv := readRestartEnvValue(capturedEnv, restart.RequestEnv)
	fromEnv := readRestartEnvValue(capturedEnv, restart.FromEnv)
	execMu.Unlock()
	if reqEnv != req.ID {
		t.Fatalf("captured %s = %q, want %q", restart.RequestEnv, reqEnv, req.ID)
	}
	if fromEnv == "" {
		t.Fatalf("captured %s was empty", restart.FromEnv)
	}

	if releaseBeforeSuccessor {
		if err := os.WriteFile(releaseFile, []byte("go\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		waitForPath(t, filepath.Join(runDir, GateDoneFile), make(chan struct{}))
	}

	t.Setenv(restart.RequestEnv, reqEnv)
	t.Setenv(restart.FromEnv, fromEnv)

	steps2 := detachedGateTestSteps(t, f, time.Now, gateProg)
	l2 := configureDetachedGateLoop(t, f, steps2)
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l2.cfg.Restart = &RestartConfig{Executable: selfExe}

	ctx2, cancel2 := context.WithTimeout(context.Background(), hangGuard)
	defer cancel2()
	if err := l2.adoptRestart(ctx2); err != nil {
		t.Fatal(err)
	}
	ans := readLatestRestartAnswer(t, f.storePath, req.ID)
	if ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}

	if !releaseBeforeSuccessor {
		if err := os.WriteFile(releaseFile, []byte("go\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var reportBuf2 bytes.Buffer
	l2.cfg.Report = &reportBuf2
	done2 := make(chan struct{})
	var res2 LoopResult
	var err2 error
	go func() {
		defer close(done2)
		res2, err2 = l2.Run(ctx2)
	}()

	select {
	case <-done2:
	case <-time.After(hangGuard):
		t.Fatalf("l2 did not finish settling item-a")
	}
	if err2 != nil {
		t.Fatalf("l2 Run error: %v (step=%s detail=%s)\nreport:\n%s", err2, res2.StopStep, res2.Detail, reportBuf2.String())
	}
	if res2.Landed != 1 {
		t.Fatalf("l2 Landed = %d, want 1 (result=%+v)", res2.Landed, res2)
	}
	if got := readCounterFile(t, counterFile); got != 1 {
		t.Fatalf("gate invocation count = %d, want 1", got)
	}
	if got := itemStateOf(t, f.storePath, "item-a"); got != StateLanded {
		t.Fatalf("item-a state = %q, want %q", got, StateLanded)
	}

	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var verdicts int
		var tickID sql.NullString
		if err := r.QueryRow(`SELECT COUNT(*), MAX(json_extract(payload, '$.tick_id')) FROM events WHERE kind=? AND object_id=?`, GateVerdictKind, c.RunID).Scan(&verdicts, &tickID); err != nil {
			return err
		}
		if verdicts != 1 {
			return fmt.Errorf("gate.verdict count for %s = %d, want 1", c.RunID, verdicts)
		}
		if !tickID.Valid || tickID.String == "" {
			return fmt.Errorf("gate.verdict tick_id for %s is empty", c.RunID)
		}
		var gateCount int
		if err := r.QueryRow(`SELECT COALESCE(SUM(gate_count), 0) FROM drain_passes`).Scan(&gateCount); err != nil {
			return err
		}
		if gateCount < 1 {
			return fmt.Errorf("drain_passes SUM(gate_count) = %d, want >= 1", gateCount)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, mustExist := range []string{gateTimingFile, StepsFile} {
		if _, err := os.Stat(filepath.Join(runDir, mustExist)); err != nil {
			t.Fatalf("expected %s in %s: %v", mustExist, runDir, err)
		}
	}
	for _, mustGone := range []string{GateFaceFile, GateDoneFile} {
		if _, err := os.Stat(filepath.Join(runDir, mustGone)); !os.IsNotExist(err) {
			t.Fatalf("transient %s still present in %s (err=%v)", mustGone, runDir, err)
		}
	}
}

func TestSiblingFastGateLandsWhileLaneOneGateBlocksAndRestartIsPending(t *testing.T) {
	t.Run("concurrent_blocked_gate", func(t *testing.T) {
		t.Setenv("TILLR_LOOP_STORE", "")
		f := newRefusalLoopFixtureWithLanes(t, 2, "item-a", "item-b")
		holdWALKeeper(t, f.storePath)

		work := scratchUnderDurable(t)
		enteredA := filepath.Join(work, "entered-a")
		releaseA := filepath.Join(work, "release-a")
		counterA := filepath.Join(work, "count-a")
		gateProg := executable(t, fmt.Sprintf(`exec python3 - "$@" <<'PY'
import os, pathlib, sys, time
args = sys.argv[1:]
item = args[args.index("--item") + 1] if "--item" in args else ""
if item == "item-a":
    pathlib.Path(%q).write_text("1\n")
    pathlib.Path(%q).write_text(str(os.getpid()) + "\n")
    rel = pathlib.Path(%q)
    while not rel.exists():
        time.sleep(0.02)
PY
`, counterA, enteredA, releaseA))

		steps := detachedGateTestSteps(t, f, time.Now, gateProg)
		l := configureDetachedGateLoop(t, f, steps)
		cA := prepareReadyCandidate(t, f, l, "item-a")
		cB := prepareReadyCandidate(t, f, l, "item-b")

		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		gateADone := make(chan error, 1)
		go func() {
			_, err := steps.Gate(ctx, cA)
			gateADone <- err
		}()
		t.Cleanup(func() {
			_ = os.WriteFile(releaseA, []byte("cleanup\n"), 0o644)
			select {
			case <-gateADone:
			case <-time.After(5 * time.Second):
			}
		})

		waitForPath(t, enteredA, gateADone)

		here, err := currentLoopOwner()
		if err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, cB.RunID, cB.LaneID); err != nil {
				return err
			}
			return appendEffect(tx, examinationOwnerKind, cB.RunID, here, l.cfg.Now())
		})

		var localB LoopResult
		outB, actionB := l.judgeSettledCandidate(ctx, Decision{
			Action: ActionJudge, ItemID: cB.ItemID, LaneID: cB.LaneID,
			RunRequest: &run.Request{RunID: cB.RunID, ItemID: cB.ItemID, LaneID: cB.LaneID},
		}, &localB, &PassTiming{})
		if outB.err != nil || localB.Landed != 1 {
			t.Fatalf("sibling judgeSettledCandidate failed while lane-1 gate blocked: action=%s out=%+v local=%+v", actionB, outB, localB)
		}
		if _, err := os.Stat(releaseA); !os.IsNotExist(err) {
			t.Fatalf("release-a existed before item-b landed")
		}
		if got := itemStateOf(t, f.storePath, "item-b"); got != StateLanded {
			t.Fatalf("item-b state = %q, want %q", got, StateLanded)
		}

		if err := os.WriteFile(releaseA, []byte("go\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-gateADone:
			if err != nil {
				t.Fatalf("lane-1 gate failed: %v", err)
			}
		case <-time.After(hangGuard):
			t.Fatalf("lane-1 gate did not finish after release")
		}
	})

	t.Run("sibling_admission_under_settlement_and_pending_restart", func(t *testing.T) {
		t.Setenv("TILLR_LOOP_STORE", "")
		f := newRefusalLoopFixtureWithLanes(t, 2, "item-a", "item-b")
		holdWALKeeper(t, f.storePath)

		gateProg := executable(t, "exit 0\n")
		steps := detachedGateTestSteps(t, f, time.Now, gateProg)
		l := configureDetachedGateLoop(t, f, steps)
		exe := filepath.Join(scratchUnderDurable(t), "tillr-drain-next")
		if err := writeForkSafe(exe, []byte("replacement-binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		l.cfg.Restart = &RestartConfig{
			Executable: exe,
			Check:      func(context.Context, string) error { return nil },
		}

		_ = prepareReadyCandidate(t, f, l, "item-a")
		cB := prepareReadyCandidate(t, f, l, "item-b")

		here, err := currentLoopOwner()
		if err != nil {
			t.Fatal(err)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE lanes SET state=?, run_id=?, leased_until=NULL WHERE lane_id=?`, run.LaneStateLeased, cB.RunID, cB.LaneID); err != nil {
				return err
			}
			return appendEffect(tx, examinationOwnerKind, cB.RunID, here, l.cfg.Now())
		})

		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		// Simulate an active settlement holding restartSettlements > 0 while a restart is pending.
		settleCtx, releaseSettlement, err := l.lockRestartSettlement(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer releaseSettlement()
		if !l.restartDefersToSettlement() {
			t.Fatalf("expected restartDefersToSettlement() to be true")
		}

		if _, err := restart.File(ctx, f.storePath, restart.RoleDrain, time.Now()); err != nil {
			t.Fatal(err)
		}

		dB := Decision{
			Action: ActionJudge, ItemID: cB.ItemID, LaneID: cB.LaneID,
			RunRequest: &run.Request{RunID: cB.RunID, ItemID: cB.ItemID, LaneID: cB.LaneID},
		}
		claimed, ok, err := l.claimSettledCandidateJudge(settleCtx, dB, &PassTiming{})
		if err != nil || !ok || claimed == nil {
			t.Fatalf("claimSettledCandidateJudge refused sibling under active settlement + pending restart: claimed=%v ok=%v err=%v", claimed != nil, ok, err)
		}

		sibCtx, releaseSib, err := l.lockRestartSettlement(ctx)
		if err != nil {
			t.Fatalf("lockRestartSettlement refused sibling under active settlement + pending restart: %v", err)
		}
		defer releaseSib()

		var localB LoopResult
		outB := l.runCandidateExamination(sibCtx, claimed, &localB, &PassTiming{})
		if outB.err != nil || localB.Landed != 1 {
			t.Fatalf("sibling runCandidateExamination failed under active settlement + pending restart: out=%+v local=%+v", outB, localB)
		}
		if got := itemStateOf(t, f.storePath, "item-b"); got != StateLanded {
			t.Fatalf("item-b state = %q, want %q", got, StateLanded)
		}
	})
}

func TestDetachedGateSupervisorReapsDescendantsWhenOwnerDies(t *testing.T) {
	if os.Getenv("TILLR_DETACHED_GATE_OWNER_HELPER") == "1" {
		worktree := os.Getenv("TILLR_HELPER_WORKTREE")
		gateProg := os.Getenv("TILLR_HELPER_GATE_PROG")
		runID := os.Getenv("TILLR_HELPER_RUN_ID")
		steps := &ProcessSteps{
			GateProgram: gateProg,
			Now:         time.Now,
		}
		_, _ = steps.Gate(context.Background(), Candidate{
			ItemID:   "item-a",
			LaneID:   "lane-1",
			RunID:    runID,
			Worktree: worktree,
			BaseSHA:  "base",
			HeadSHA:  "head",
		})
		return
	}

	work := scratchUnderDurable(t)
	worktree := filepath.Join(work, "worktree")
	if err := os.MkdirAll(filepath.Join(worktree, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	pidsFile := filepath.Join(work, "pids")
	readyFile := filepath.Join(work, "ready")

	gateProg := executable(t, fmt.Sprintf(`exec python3 - <<'PY'
import os, pathlib, subprocess

leaf = subprocess.Popen(["sleep", "300"], start_new_session=True)
pids_part = pathlib.Path(%q)
pids = pathlib.Path(%q)
pids_part.write_text(f"{os.getpid()} {leaf.pid}\n")
os.replace(pids_part, pids)
pathlib.Path(%q).touch()
leaf.wait()
PY
`, pidsFile+".part", pidsFile, readyFile))

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestDetachedGateSupervisorReapsDescendantsWhenOwnerDies$")
	cmd.Env = append(os.Environ(),
		"TILLR_DETACHED_GATE_OWNER_HELPER=1",
		"TILLR_HELPER_WORKTREE="+worktree,
		"TILLR_HELPER_GATE_PROG="+gateProg,
		"TILLR_HELPER_RUN_ID=run-owner-death",
	)
	helperDone := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ownerPID := cmd.Process.Pid
	go func() { helperDone <- cmd.Wait() }()
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	waitForPath(t, readyFile, helperDone)
	raw, err := os.ReadFile(pidsFile)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 {
		t.Fatalf("unexpected pids %q", string(raw))
	}
	var pids []int
	for _, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
		if !processAlive(pid) {
			t.Fatalf("descendant %d not alive before SIGKILL", pid)
		}
	}

	if err := syscall.Kill(ownerPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-helperDone:
	case <-time.After(hangGuard):
		t.Fatalf("owner helper did not exit after SIGKILL")
	}

	deadline := time.Now().Add(hangGuard)
	for {
		alive := 0
		for _, pid := range pids {
			if processAlive(pid) {
				alive++
			}
		}
		if alive == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("detached gate descendants %v survived owner SIGKILL", pids)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestDetachedGateSurvivesLoopRestartForAgentJudgedCandidate(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	t.Setenv(restart.RequestEnv, "")
	t.Setenv(restart.FromEnv, "")

	f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
	holdWALKeeper(t, f.storePath)
	persistFixtureJudgeSelection(t, f)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE workflow_selections SET graph=? WHERE item_id='item-a'`, store.CompatibilityJudgeGraph(singleWorkerWorkflow, "")); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET judge_mode='agent' WHERE item_id='item-a'`)
		return err
	})

	work := scratchUnderDurable(t)
	counterFile := filepath.Join(work, "gate-count")
	enteredFile := filepath.Join(work, "gate-entered")
	releaseFile := filepath.Join(work, "gate-release")
	gateProg := blockingCounterGateProgram(t, work, counterFile, enteredFile, releaseFile)

	steps1 := detachedGateTestSteps(t, f, time.Now, gateProg)
	// Ensure mechanical VerdictProgram is never called for an agent-judged item.
	steps1.VerdictProgram = executable(t, "exit 1\n")
	l1 := configureDetachedGateLoop(t, f, steps1)
	c := prepareReadyCandidate(t, f, l1, "item-a")

	buildHash, err := l1.build.Hash()
	if err != nil {
		t.Fatal(err)
	}
	headTree := runGit(t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}")
	var binding item.EvaluatorBinding
	var claimClass string
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		binding, err = item.ReadEvaluatorBinding(r, c.ItemID)
		if err != nil {
			return err
		}
		return r.QueryRow(`SELECT claim_class FROM item_criteria WHERE item_id=? AND criterion_id='c1'`, c.ItemID).Scan(&claimClass)
	})
	rawVerdict, err := json.Marshal(item.JudgeOutput{
		EvaluatedHead: c.HeadSHA,
		EvaluatedTree: headTree,
		Outcome:       "pass",
		Verdicts: []item.JudgeEntry{{
			CriterionID:   "c1",
			CriterionHash: item.CriterionHash("the first criterion of item-a"),
			Verdict:       "done",
			ClaimClass:    claimClass,
			Reason:        "verified",
			Evidence:      []string{"commit:" + c.HeadSHA},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := &JudgeReceipt{
		Build: buildHash,
		Request: run.Request{
			RunID:   c.RunID + "-judge",
			ItemID:  c.ItemID,
			LaneID:  c.LaneID,
			Backend: "fixture",
			NodeID:  binding.EvaluatorNode,
		},
		Options: item.JudgeOptions{
			EvaluatorBinding: binding,
			ItemID:           c.ItemID,
			Head:             c.HeadSHA,
			Tree:             headTree,
			Actor:            "review/fixture",
			JudgedActor:      "worker/fixture",
		},
		Raw:     rawVerdict,
		Writers: []JudgeWriter{{RunID: c.RunID}},
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(RunDir(f.root, c.RunID), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(RunDir(f.root, c.RunID), judgeReceiptFile), string(rawReceipt))

	exe := filepath.Join(work, "tillr-drain-next")
	if err := writeForkSafe(exe, []byte("replacement-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	var execMu sync.Mutex
	var capturedEnv []string
	l1.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
		Exec: func(_ string, _ []string, env []string) error {
			execMu.Lock()
			capturedEnv = append([]string(nil), env...)
			execMu.Unlock()
			return nil
		},
	}

	var reportBuf1 bytes.Buffer
	l1.cfg.Report = &reportBuf1
	ctx1, cancel1 := context.WithTimeout(context.Background(), hangGuard)
	defer cancel1()
	done1 := make(chan struct{})
	var res1 LoopResult
	var err1 error
	go func() {
		defer close(done1)
		res1, err1 = l1.Run(ctx1)
	}()

	for {
		if _, err := os.Stat(enteredFile); err == nil {
			break
		}
		select {
		case <-done1:
			var holdPayload []string
			fixtureRead(t, f, func(r *store.Reader) error {
				rows, err := r.Query(`SELECT kind || ':' || payload FROM events ORDER BY rowid DESC LIMIT 5`)
				if err != nil {
					return err
				}
				defer rows.Close()
				for rows.Next() {
					var s string
					if err := rows.Scan(&s); err == nil {
						holdPayload = append(holdPayload, s)
					}
				}
				return nil
			})
			t.Fatalf("l1 exited before %s: res=%+v err=%v events=%v\nreport:\n%s", enteredFile, res1, err1, holdPayload, reportBuf1.String())
		default:
			time.Sleep(15 * time.Millisecond)
		}
	}
	runDir := RunDir(f.root, c.RunID)
	face, ok := readDetachedGateFace(runDir)
	if !ok {
		t.Fatalf("missing %s in %s while gate blocked", GateFaceFile, runDir)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(releaseFile, []byte("cleanup\n"), 0o644)
		_ = syscall.Kill(face.PID, syscall.SIGKILL)
	})

	req, err := restart.File(ctx1, f.storePath, restart.RoleDrain, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done1:
	case <-time.After(hangGuard):
		t.Fatalf("l1 did not replace itself while detached gate was blocked")
	}
	if err1 != nil || res1.Stop != StopRestarted {
		t.Fatalf("l1 stopped with %+v, %v; want %q", res1, err1, StopRestarted)
	}

	execMu.Lock()
	reqEnv := readRestartEnvValue(capturedEnv, restart.RequestEnv)
	fromEnv := readRestartEnvValue(capturedEnv, restart.FromEnv)
	execMu.Unlock()
	t.Setenv(restart.RequestEnv, reqEnv)
	t.Setenv(restart.FromEnv, fromEnv)

	steps2 := detachedGateTestSteps(t, f, time.Now, gateProg)
	steps2.VerdictProgram = executable(t, "exit 1\n")
	landedRecord := landRecord(t, LandLanded, nil)
	steps2.LandProgram = executable(t, fmt.Sprintf(`exec python3 - <<'PY'
import sqlite3
conn = sqlite3.connect(%q)
seq = conn.execute("SELECT COALESCE(MAX(seq), 0) FROM events").fetchone()[0]
conn.execute(
    "INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'fixture','landed',?,?,?,?,?)",
    ("fixture-" + %q, %q, %q, %q, %q, %q, %q, seq),
)
conn.commit()
conn.close()
print(%q)
PY
`, f.storePath, c.RunID, c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, c.BaseSHA, c.HeadSHA, landedRecord))
	l2 := configureDetachedGateLoop(t, f, steps2)
	selfExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l2.cfg.Restart = &RestartConfig{Executable: selfExe}

	ctx2, cancel2 := context.WithTimeout(context.Background(), hangGuard)
	defer cancel2()
	if err := l2.adoptRestart(ctx2); err != nil {
		t.Fatal(err)
	}
	if ans := readLatestRestartAnswer(t, f.storePath, req.ID); ans.Answer != restart.AnswerRestarted {
		t.Fatalf("restart answer = %+v, want %q", ans, restart.AnswerRestarted)
	}

	if err := os.WriteFile(releaseFile, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var reportBuf2 bytes.Buffer
	l2.cfg.Report = &reportBuf2
	done2 := make(chan struct{})
	var res2 LoopResult
	var err2 error
	go func() {
		defer close(done2)
		res2, err2 = l2.Run(ctx2)
	}()

	select {
	case <-done2:
	case <-time.After(hangGuard):
		t.Fatalf("l2 did not finish settling agent-judged item-a")
	}
	if err2 != nil {
		t.Fatalf("l2 Run error: %v (step=%s detail=%s)\nreport:\n%s", err2, res2.StopStep, res2.Detail, reportBuf2.String())
	}
	if res2.Landed != 1 {
		var evs []string
		fixtureRead(t, f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT kind || ':' || payload FROM events ORDER BY rowid DESC LIMIT 8`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err == nil {
					evs = append(evs, s)
				}
			}
			return nil
		})
		t.Fatalf("l2 Landed = %d, want 1 (result=%+v)\nevents=%v\nreport:\n%s", res2.Landed, res2, evs, reportBuf2.String())
	}
	if got := readCounterFile(t, counterFile); got != 1 {
		t.Fatalf("gate invocation count = %d, want 1", got)
	}
	if got := itemStateOf(t, f.storePath, "item-a"); got != StateLanded {
		t.Fatalf("item-a state = %q, want %q", got, StateLanded)
	}

	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var itemVerdicts int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict' AND object_id='item-a'`).Scan(&itemVerdicts); err != nil {
			return err
		}
		if itemVerdicts != 1 {
			return fmt.Errorf("agent item.verdict count = %d, want 1", itemVerdicts)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDetachedGateStopWithRestartConfigReapsDescendantsSynchronously(t *testing.T) {
	for _, mode := range []string{"stop_requested", "ctx_cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TILLR_LOOP_STORE", "")
			f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
			holdWALKeeper(t, f.storePath)

			work := scratchUnderDurable(t)
			pidsFile := filepath.Join(work, "pids")
			readyFile := filepath.Join(work, "ready")
			gateProg := executable(t, fmt.Sprintf(`exec python3 - <<'PY'
import os, pathlib, subprocess

leaf = subprocess.Popen(["sleep", "300"], start_new_session=True)
pids_part = pathlib.Path(%q)
pids = pathlib.Path(%q)
pids_part.write_text(f"{os.getpid()} {leaf.pid}\n")
os.replace(pids_part, pids)
pathlib.Path(%q).touch()
leaf.wait()
PY
`, pidsFile+".part", pidsFile, readyFile))

			steps := detachedGateTestSteps(t, f, time.Now, gateProg)
			l := configureDetachedGateLoop(t, f, steps)
			selfExe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			l.cfg.Restart = &RestartConfig{
				Executable: selfExe,
				Check:      func(context.Context, string) error { return nil },
			}
			c := prepareReadyCandidate(t, f, l, "item-a")

			ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
			defer cancel()

			verifyDone := make(chan error, 1)
			go func() {
				if mode == "stop_requested" {
					_, err := l.Run(ctx)
					verifyDone <- err
				} else {
					_, _, err := l.Verify(ctx, c, &LoopResult{}, &PassTiming{})
					verifyDone <- err
				}
			}()

			waitForPath(t, readyFile, verifyDone)
			raw, err := os.ReadFile(pidsFile)
			if err != nil {
				t.Fatal(err)
			}
			fields := strings.Fields(string(raw))
			if len(fields) != 2 {
				t.Fatalf("unexpected pids %q", string(raw))
			}
			gatePID, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			leafPID, err := strconv.Atoi(fields[1])
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = syscall.Kill(gatePID, syscall.SIGKILL)
				_ = syscall.Kill(leafPID, syscall.SIGKILL)
			})
			if !processAlive(gatePID) || !processAlive(leafPID) {
				t.Fatalf("expected gatePID=%d and leafPID=%d to be alive before stop", gatePID, leafPID)
			}

			if mode == "stop_requested" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					res, err := tx.Exec(`UPDATE drain_sessions SET stop_requested = 1 WHERE ended_at IS NULL`)
					if err != nil {
						return err
					}
					n, err := res.RowsAffected()
					if err != nil {
						return err
					}
					if n == 0 {
						return fmt.Errorf("no active drain_sessions row to mark stop_requested")
					}
					return nil
				})
				select {
				case err := <-verifyDone:
					if err != nil {
						t.Fatalf("Run returned error after %s: %v", mode, err)
					}
				case <-time.After(hangGuard):
					t.Fatalf("Run did not return after %s", mode)
				}
			} else {
				cancel()
				select {
				case err := <-verifyDone:
					if err == nil {
						t.Fatalf("expected Verify to return cancellation error after %s", mode)
					}
				case <-time.After(hangGuard):
					t.Fatalf("Verify did not return after %s", mode)
				}
			}

			if processAlive(gatePID) || processAlive(leafPID) {
				t.Fatalf("Verify returned while gatePID=%d (alive=%v) or leafPID=%d (alive=%v) still running",
					gatePID, processAlive(gatePID), leafPID, processAlive(leafPID))
			}

			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				var verdicts int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, GateVerdictKind, c.RunID).Scan(&verdicts); err != nil {
					return err
				}
				if verdicts != 0 {
					return fmt.Errorf("gate.verdict count = %d, want 0", verdicts)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
