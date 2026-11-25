package drain

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

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSurvivingRunProcessesScansMarkedLiveProcesses(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "store.db")
	if err := os.WriteFile(storePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	startMarked := func(runID string) *exec.Cmd {
		cmd := exec.Command("sleep", "60")
		cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+runID, "TILLR_LOOP_STORE="+storePath)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		return cmd
	}

	p1 := startMarked("run-a")
	p2 := startMarked("run-a")
	_ = startMarked("run-b")

	procs, err := survivingRunProcesses(storePath, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 2 {
		t.Fatalf("got %d processes, want 2: %+v", len(procs), procs)
	}
	want1, want2 := p1.Process.Pid, p2.Process.Pid
	if want1 > want2 {
		want1, want2 = want2, want1
	}
	if procs[0].PID != want1 || procs[1].PID != want2 {
		t.Fatalf("PIDs = [%d, %d], want sorted [%d, %d]", procs[0].PID, procs[1].PID, want1, want2)
	}
	for _, p := range procs {
		if !strings.Contains(p.String(), fmt.Sprintf("%d (", p.PID)) || !strings.Contains(p.Command, "sleep") {
			t.Fatalf("unexpected process format: %q (%+v)", p.String(), p)
		}
	}
}

type survivingLoopSteps struct {
	*recordingSteps
	t                *testing.T
	f                refusalLoopFixture
	strayCmd         *exec.Cmd
	itemARun         string
	itemALane        string
	itemARuns        int
	skipLandingOffer bool
}

func (s *survivingLoopSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	path := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	workFile := filepath.Join("tools", req.ItemID, "work.txt")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(path, workFile)), 0o755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, filepath.Join(s.f.laneRoot, req.LaneID, FenceFile), "tools/"+req.ItemID+"/\n")
	writeForkFile(s.t, filepath.Join(path, workFile), "work for "+req.ItemID+"\n")
	runGit(s.t, path, "add", workFile)
	runGit(s.t, path, "commit", "-qm", "feat: "+req.ItemID)
	commit := runGit(s.t, path, "rev-parse", "HEAD")

	if req.ItemID == "item-a" {
		s.itemARuns++
		s.itemARun = req.RunID
		s.itemALane = req.LaneID
		if !s.skipLandingOffer {
			// Reproduce the 2026-11-19 crash: landing-offer.json exists and a marked
			// process outlives the ended run.
			offerDir := run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID)
			if err := os.MkdirAll(offerDir, 0o755); err != nil {
				return run.Record{}, "", err
			}
			offer := fmt.Sprintf(`{"call_id":"call-a","summary":"item-a work","commit":%q,"base":%q}`, commit, runGit(s.t, s.f.root, "rev-parse", TrunkRef))
			if err := os.WriteFile(filepath.Join(offerDir, landingOfferFile), []byte(offer), 0o600); err != nil {
				return run.Record{}, "", err
			}
		}
		cmd := exec.Command("sleep", "600")
		cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+req.RunID, "TILLR_LOOP_STORE="+s.f.storePath)
		if err := cmd.Start(); err != nil {
			return run.Record{}, "", err
		}
		s.strayCmd = cmd
		s.t.Cleanup(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
	}

	logPath := filepath.Join(s.f.laneRoot, req.LaneID, req.RunID+".log")
	writeForkFile(s.t, logPath, "RESULT: done\n")
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    time.Now().UTC().Format(time.RFC3339),
		FinishedAt:   time.Now().UTC().Format(time.RFC3339),
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: logPath}},
	}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}

func (s *survivingLoopSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	out, err := s.recordingSteps.Land(ctx, c)
	if err == nil && out.Result == LandLanded {
		fixtureLanding(s.t, s.f.storePath, c)
	}
	return out, err
}

func (s *survivingLoopSteps) ItemState(ctx context.Context, id, _, state string, _ ...item.WaitingOn) error {
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, state, id)
		return err
	})
}

func TestSurvivingProcessesHoldOnlyTheirItemAndKeepLoopRunning(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	steps := &survivingLoopSteps{
		recordingSteps: l.cfg.Steps.(*recordingSteps),
		t:              t,
		f:              f,
	}
	l.cfg.Steps = steps

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		res LoopResult
		err error
	}, 1)
	defer func() {
		cancel()
		<-done
	}()
	go func() {
		defer close(done)
		res, err := l.Run(ctx)
		done <- struct {
			res LoopResult
			err error
		}{res, err}
	}()

	// A successful Land return precedes item settlement and lane examination.
	// Wait for both items' durable outcomes before canceling the loop.
	deadline := time.NewTimer(hangGuard)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var heldA, settledB bool
		fixtureRead(t, f, func(r *store.Reader) error {
			var stateB string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id = 'item-b'`).Scan(&stateB); err != nil {
				return err
			}
			works, err := readPending(r)
			if err != nil {
				return err
			}
			settledB = stateB == StateLanded && works["item-b"] != nil && works["item-b"].Done
			if !settledB {
				return nil
			}
			answer, err := Why(r, Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
			if err != nil {
				return err
			}
			for _, row := range answer.Rows {
				if row.ItemID == "item-a" && row.Reason == ReasonRunProcessesLive {
					heldA = true
					break
				}
			}
			return nil
		})
		if heldA && settledB {
			break
		}
		select {
		case out := <-done:
			t.Fatalf("loop exited before settlement: stop=%q detail=%q err=%v", out.res.Stop, out.res.Detail, out.err)
		case <-deadline.C:
			t.Fatalf("timed out waiting for settlement: item-a held=%t, item-b settled=%t", heldA, settledB)
		case <-ticker.C:
		}
	}
	cancel()
	out := <-done
	if out.res.Stop == StopFault || (out.err != nil && out.err != context.Canceled) {
		t.Fatalf("loop stopped unexpectedly: stop=%q detail=%q err=%v", out.res.Stop, out.res.Detail, out.err)
	}
	if steps.strayCmd == nil {
		t.Fatal("item-a never ran")
	}
	strayPID := strconv.Itoa(steps.strayCmd.Process.Pid)

	// Verify item-a's lane is retained (not idle) and its worktree is untouched.
	fixtureRead(t, f, func(r *store.Reader) error {
		var stateB string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id = 'item-b'`).Scan(&stateB); err != nil {
			return err
		}
		if stateB != StateLanded {
			t.Fatalf("item-b state = %q, want %q", stateB, StateLanded)
		}
		var laneState string
		if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id = ?`, steps.itemALane).Scan(&laneState); err != nil {
			return err
		}
		if laneState == "idle" {
			t.Fatalf("retained lane %s became idle", steps.itemALane)
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works["item-a"]; w == nil || w.Done {
			t.Fatalf("item-a run was marked examined while processes survived: %+v", w)
		}

		answer, err := Why(r, Pass{Now: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, nil)
		if err != nil {
			return err
		}
		var found bool
		for _, row := range answer.Rows {
			if row.ItemID == "item-b" && row.Decision.Action == ActionHold {
				t.Errorf("item-b should not be held in Why: %+v", row)
			}
			if row.ItemID != "item-a" {
				continue
			}
			found = true
			if row.Reason != ReasonRunProcessesLive {
				t.Errorf("why reason = %q, want %q", row.Reason, ReasonRunProcessesLive)
			}
			if !strings.Contains(row.Mover, "clear-fault") {
				t.Errorf("why mover = %q, want clear-fault", row.Mover)
			}
			if !strings.Contains(row.Why, strayPID) || !strings.Contains(row.Why, "sleep") {
				t.Errorf("why detail = %q, want PID %s and command sleep", row.Why, strayPID)
			}
		}
		if !found {
			t.Fatal("Why did not report a row for item-a")
		}
		return nil
	})
	if raw, err := os.ReadFile(filepath.Join(l.laneTarget(steps.itemALane), "tools", "item-a", "work.txt")); err != nil || string(raw) != "work for item-a\n" {
		t.Fatalf("item-a worktree was touched: %q / %v", raw, err)
	}
}

func TestEndedRunExaminedAfterRunProcessesLiveFaultCleared(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.ExitWhenDrained = false
	l.cfg.StorePollInterval = 20 * time.Millisecond
	steps := &survivingLoopSteps{
		recordingSteps:   l.cfg.Steps.(*recordingSteps),
		t:                t,
		f:                f,
		skipLandingOffer: true,
	}
	l.cfg.Steps = steps

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		res LoopResult
		err error
	}, 1)
	defer func() {
		cancel()
		<-done
	}()
	go func() {
		defer close(done)
		res, err := l.Run(ctx)
		done <- struct {
			res LoopResult
			err error
		}{res, err}
	}()

	waitFor := func(what string, limit time.Duration, check func() bool) {
		t.Helper()
		deadline := time.NewTimer(limit)
		defer deadline.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			if check() {
				return
			}
			select {
			case out := <-done:
				t.Fatalf("loop exited while waiting for %s: stop=%q detail=%q err=%v", what, out.res.Stop, out.res.Detail, out.err)
			case <-deadline.C:
				t.Fatalf("timed out waiting for %s", what)
			case <-ticker.C:
			}
		}
	}

	var fault ItemFault
	waitFor("run-processes-live fault on item-a", hangGuard, func() bool {
		var ok bool
		fixtureRead(t, f, func(r *store.Reader) error {
			faults, err := ReadItemFaults(r)
			if err != nil {
				return err
			}
			fault, ok = faults["item-a"]
			return nil
		})
		return ok && fault.Reason == ReasonRunProcessesLive
	})

	// End the surviving process and clear the fault while the same loop keeps running.
	if steps.strayCmd == nil || steps.strayCmd.Process == nil {
		t.Fatal("item-a did not start a marked child process")
	}
	_ = steps.strayCmd.Process.Kill()
	_ = steps.strayCmd.Wait()
	steps.strayCmd = nil

	fixtureWrite(t, f, func(tx *store.Tx) error {
		cleared, err := ClearItemFaultHold(tx, "item-a", fault.Seq, l.cfg.Now().UTC())
		if err != nil {
			return err
		}
		if cleared.Reason != ReasonRunProcessesLive {
			t.Fatalf("cleared reason = %q, want %q", cleared.Reason, ReasonRunProcessesLive)
		}
		return nil
	})

	// Without restarting the loop, a following pass examines the ended run and lands it.
	waitFor("ended run examination and landing after clear-fault", hangGuard, func() bool {
		var settled bool
		fixtureRead(t, f, func(r *store.Reader) error {
			var state, laneState string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id = 'item-a'`).Scan(&state); err != nil {
				return err
			}
			if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id = ?`, steps.itemALane).Scan(&laneState); err != nil {
				return err
			}
			works, err := readPending(r)
			if err != nil {
				return err
			}
			settled = state == StateLanded && laneState == run.LaneStateIdle && works["item-a"] != nil && works["item-a"].Done
			return nil
		})
		return settled
	})

	if steps.itemARuns != 1 {
		t.Fatalf("item-a ran %d times, want 1 (the ended run itself should be examined)", steps.itemARuns)
	}
	cancel()
	out := <-done
	if out.res.Stop == StopFault || out.res.Stop == StopLanesLeased || (out.err != nil && out.err != context.Canceled) {
		t.Fatalf("loop stopped unexpectedly: stop=%q detail=%q err=%v", out.res.Stop, out.res.Detail, out.err)
	}
}

func TestSurvivingProcessesForTerminalItemHoldLaneWithoutTouchingWorktree(t *testing.T) {
	f, l, d, rec := savedFixture(t)
	ctx := context.Background()
	lane := d.LaneID

	// Close item-a in the store so holdItemFault returns itemTerminalError.
	st, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, "closed", d.ItemID)
		return err
	}); err != nil {
		st.Close()
		t.Fatal(err)
	}
	st.Close()

	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+rec.RunID, "TILLR_LOOP_STORE="+f.storePath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	out := l.retainSurvivingProcesses(ctx, d.ItemID, lane, rec.RunID, []survivingProcess{{PID: cmd.Process.Pid, Command: "sleep 60"}})
	if out.stop != stopLaneRetained || out.err != nil {
		t.Fatalf("got %+v, want stopLaneRetained with nil err", out)
	}
	if out.laneRefusal == nil || out.laneRefusal.Reason != ReasonLaneChildren {
		t.Fatalf("laneRefusal = %+v, want %s", out.laneRefusal, ReasonLaneChildren)
	}
	var result LoopResult
	if stop, err := l.takeCompletion(runCompletion{id: rec.RunID, itemID: d.ItemID, outcome: out, laneRefusal: out.laneRefusal}, &result); stop != "" || err != nil {
		t.Fatalf("takeCompletion stopped loop: %q / %v", stop, err)
	}
	if c := l.laneConditions[lane]; c == nil || !c.out || c.reason != ReasonLaneChildren {
		t.Fatalf("lane condition = %+v, want %s", c, ReasonLaneChildren)
	}
	if raw, err := os.ReadFile(filepath.Join(l.laneTarget(lane), "base.txt")); err != nil || string(raw) != "earlier work\n" {
		t.Fatalf("lane worktree was touched while processes survived: %q / %v", raw, err)
	}
}

func TestRecordLoopResultPreservesConsecutiveZeroGenerationRecords(t *testing.T) {
	root := t.TempDir()
	t1 := time.Date(2026, 11, 20, 10, 0, 0, 111, time.UTC)
	t2 := time.Date(2026, 11, 20, 10, 1, 0, 222, time.UTC)

	RecordLoopResult(root, t1, BuildStamp{}, LoopResult{Stop: StopFault, Detail: "first exit"}, nil)
	RecordLoopResult(root, t2, BuildStamp{}, LoopResult{Stop: StopFault, Detail: "second exit"}, nil)

	currentPath := LoopResultFile(root, "", "")
	archivePath := filepath.Join(LoopDir(root), fmt.Sprintf("drain-0-0-%d.json", t2.UnixNano()))

	readDetail := func(path string) string {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var env struct {
			Payload LoopResult `json:"payload"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return env.Payload.Detail
	}

	if got := readDetail(archivePath); got != "first exit" {
		t.Fatalf("archived detail = %q, want %q", got, "first exit")
	}
	if got := readDetail(currentPath); got != "second exit" {
		t.Fatalf("current detail = %q, want %q", got, "second exit")
	}
}
