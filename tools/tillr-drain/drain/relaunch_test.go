package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// streamSteps is recordingSteps whose run faces, like ProcessSteps', write their two
// streams to the run's own record directory, so the reaper knows where to look.
type streamSteps struct {
	*recordingSteps
	root string
}

func (s streamSteps) runOutputFiles(id string) []string {
	dir := RunDir(s.root, id)
	return []string{filepath.Join(dir, RunRecordFile), filepath.Join(dir, RunStderrFile)}
}

// relaunchedJailClaim records p's dispatch as owned by a loop in another PID namespace
// of this same boot — the shape a podman jail relaunch leaves: the bind-mounted store
// survives, every process of the old container is gone, and the new container has a
// fresh PID namespace whose PID numbers mean nothing to the old ones.
func relaunchedJailClaim(t *testing.T, f refusalLoopFixture, l *Loop, p plannedApplication) LoopOwner {
	t.Helper()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	owner := *work.Owner
	boot, _, _ := strings.Cut(owner.Domain, ":")
	owner.Domain = boot + ":pid:[1]"
	owner.PID = 8300
	work.Owner = &owner
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, p.decision.RunRequest.RunID, work, l.cfg.Now())
	})
	return owner
}

// recordSession records a drain session for owner, ended or still open.
func recordSession(t *testing.T, f refusalLoopFixture, owner LoopOwner, ended bool) {
	t.Helper()
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	var end any
	if ended {
		end = int64(2)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, ended_at) VALUES (?, 1, 1, 1, ?, ?)`, "session-"+owner.Domain, string(raw), end)
		return err
	})
}

// runStreams creates the run's two stream files as its face would have, with no writer.
func runStreams(t *testing.T, f refusalLoopFixture, id string) []string {
	t.Helper()
	dir := RunDir(f.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(dir, RunRecordFile), filepath.Join(dir, RunStderrFile)}
	for _, path := range paths {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func laneHolder(t *testing.T, f refusalLoopFixture, lane string) string {
	t.Helper()
	s := openFixture(t, f.storePath)
	defer s.Close()
	var holder string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT COALESCE(run_id, '') FROM lanes WHERE lane_id = ? AND state = ?`, lane, run.LaneStateLeased).Scan(&holder)
	}); err != nil && !strings.Contains(err.Error(), "no rows") {
		t.Fatal(err)
	}
	return holder
}

// On 2026-11-17 a jail relaunch left six runs reported run-live for hours: their
// claims named a loop in the old container's PID namespace, which the new loop
// refused to judge at all, so no evidence could ever release them.
func TestARelaunchedJailsEndedRunIsRecoveredByTheNewLoop(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	if p.decision.RunRequest.TimeoutMS != 0 {
		t.Fatal("test must use an unbounded run")
	}
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, true)
	runStreams(t, f, p.decision.RunRequest.RunID)
	next := f.loop(t, streamSteps{&recordingSteps{}, f.root}, &itemContentStager{})
	if d := mustPlan(t, next)[0].decision; d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
		t.Fatalf("a run whose loop ended and whose face holds nothing open was kept: %+v", d)
	}
	if holder := laneHolder(t, f, p.decision.LaneID); holder != "" {
		t.Fatalf("lane %s still leased to %s", p.decision.LaneID, holder)
	}
}

// A claim dispatched while the face had not started yet (no stream files) is also over
// once its loop is proven gone: ProcessSteps creates both files before the face starts.
func TestARelaunchedJailsUnstartedRunIsRecoveredByTheNewLoop(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, true)
	next := f.loop(t, streamSteps{&recordingSteps{}, f.root}, &itemContentStager{})
	if d := mustPlan(t, next)[0].decision; d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
		t.Fatalf("an unstarted run of an ended loop was kept: %+v", d)
	}
}

// A face that still writes its stream, in a namespace this loop cannot see, is live.
func TestARelaunchedJailsRunStaysLiveWhileItsStreamIsOpenForWriting(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, true)
	paths := runStreams(t, f, p.decision.RunRequest.RunID)
	writer, err := os.OpenFile(paths[1], os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	next := f.loop(t, streamSteps{&recordingSteps{}, f.root}, &itemContentStager{})
	if d := mustPlan(t, next)[0].decision; d.Action != ActionHold || d.Reason != ReasonRunLive {
		t.Fatalf("a run whose face still writes was not kept live: %+v", d)
	}
	if holder := laneHolder(t, f, p.decision.LaneID); holder != p.decision.RunRequest.RunID {
		t.Fatalf("lane %s leased to %q, want the live run", p.decision.LaneID, holder)
	}
}

// With no proof the other namespace's loop is gone, its claim keeps its protection even
// though nothing writes the run's streams: that loop may be about to settle the run.
func TestARelaunchedJailsRunStaysLiveWithoutProofItsLoopEnded(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, false)
	runStreams(t, f, p.decision.RunRequest.RunID)
	next := f.loop(t, streamSteps{&recordingSteps{}, f.root}, &itemContentStager{})
	if d := mustPlan(t, next)[0].decision; d.Action != ActionHold || d.Reason != ReasonRunLive {
		t.Fatalf("a claim of a loop with an open session was reaped: %+v", d)
	}
}

// A loop killed with the jail never ends its session; the presence file it held open
// for writing is then the proof, and only while nothing holds it.
func TestARelaunchedJailsKilledLoopIsJudgedByItsPresenceFile(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, false)
	runStreams(t, f, p.decision.RunRequest.RunID)
	identity, err := processStoreIdentity(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	presence, err := openLoopPresence(identity, owner)
	if err != nil {
		t.Fatal(err)
	}
	next := f.loop(t, streamSteps{&recordingSteps{}, f.root}, &itemContentStager{})
	if d := mustPlan(t, next)[0].decision; d.Action != ActionHold || d.Reason != ReasonRunLive {
		t.Fatalf("a claim of a loop holding its presence file was reaped: %+v", d)
	}
	if err := presence.Close(); err != nil {
		t.Fatal(err)
	}
	if d := mustPlan(t, next)[0].decision; d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
		t.Fatalf("a claim of a loop whose presence file nothing holds was kept: %+v", d)
	}
}

func TestOpenForWritingSeesAWriterAndNoReader(t *testing.T) {
	dir := scratchUnderDurable(t)
	path := filepath.Join(dir, "stream")
	if state, err := openForWriting(path); err != nil || state != streamAbsent {
		t.Fatalf("absent file: %v %v", state, err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if state, err := openForWriting(path); err != nil || state != streamUnwritten {
		t.Fatalf("file with only a reader: %v %v", state, err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := openForWriting(path); err != nil || state != streamWritten {
		t.Fatalf("file with a writer: %v %v", state, err)
	}
	writer.Close()
	if state, err := openForWriting(path); err != nil || state != streamUnwritten {
		t.Fatalf("file after its writer closed: %v %v", state, err)
	}
}

// The evidence foreignRunLive reads is only as good as ProcessSteps' wiring: the face
// must be started with the run's two stream files as its stdout and stderr, so that it
// holds them open for writing for exactly as long as it lives.
func TestARunFaceHoldsItsStreamsOpenForWritingWhileItLives(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	dir := scratchUnderDurable(t)
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	for _, fifo := range []string{ready, release} {
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	face := filepath.Join(dir, "face")
	script := "#!/bin/sh\nprintf x > " + ready + "\nread line < " + release + "\n"
	if err := os.WriteFile(face, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	steps := &ProcessSteps{RunBin: face, Store: f.storePath, LaneRoot: f.laneRoot, Root: f.root, Now: l.cfg.Now}
	done := make(chan error, 1)
	go func() { _, _, err := steps.Run(context.Background(), *p.decision.RunRequest); done <- err }()
	opened := make(chan *os.File, 1)
	go func() {
		r, err := os.Open(ready)
		if err != nil {
			t.Error(err)
		}
		opened <- r
	}()
	select {
	case r := <-opened:
		if r == nil {
			t.FailNow()
		}
		defer r.Close()
		if _, err := r.Read(make([]byte, 1)); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("the face never started: %v", err)
	}
	paths := steps.runOutputFiles(p.decision.RunRequest.RunID)
	for _, path := range paths {
		if state, err := openForWriting(path); err != nil || state != streamWritten {
			t.Fatalf("%s while the face lives: %v %v", path, state, err)
		}
	}
	w, err := os.OpenFile(release, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	w.Close()
	<-done
	for _, path := range paths {
		if state, err := openForWriting(path); err != nil || state != streamUnwritten {
			t.Fatalf("%s after the face exited: %v %v", path, state, err)
		}
	}
}

// A loop records its presence before its session, so a killed loop leaves a presence
// file with no writer behind an open session (TestARelaunchedJailsKilledLoopIsJudgedByItsPresenceFile).
func TestALoopHoldsItsPresenceFileOnceItsSessionOpens(t *testing.T) {
	f, l := passFixture(t, "item-a")
	if err := l.refreshSession(context.Background(), l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	identity, err := processStoreIdentity(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	if state, err := openForWriting(loopPresencePath(identity, *owner)); err != nil || state != streamWritten {
		t.Fatalf("presence file of a loop with an open session: %v %v", state, err)
	}
}

func TestSingleWorkerFirstPassRecoveryPreservesDirtyAndCleanCommittedWork(t *testing.T) {
	for _, tc := range []struct {
		name   string
		commit bool
		reboot bool
	}{
		{name: "dirty-relaunch", commit: false, reboot: false},
		{name: "clean-committed-relaunch", commit: true, reboot: false},
		{name: "clean-committed-reboot", commit: true, reboot: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			p := mustPlan(t, l)[0]
			owner := relaunchedJailClaim(t, f, l, p)
			if tc.reboot {
				owner.Domain = "prior-boot-id:pid:[1]"
				fixtureWrite(t, f, func(tx *store.Tx) error {
					works, err := readPending(tx.Reader())
					if err != nil {
						return err
					}
					w := works["item-a"]
					w.Owner = &owner
					return appendEffect(tx, dispatchStartedKind, p.decision.RunRequest.RunID, w, l.cfg.Now())
				})
			} else {
				recordSession(t, f, owner, true)
			}
			wt := l.laneTarget(p.decision.LaneID)
			if err := os.MkdirAll(filepath.Join(wt, "tools/item-a"), 0o755); err != nil {
				t.Fatal(err)
			}
			writeForkFile(t, filepath.Join(wt, "tools/item-a/work.txt"), "progress\n")
			if tc.commit {
				runGit(t, wt, "add", ".")
				runGit(t, wt, "commit", "-qm", "worker commit")
			}

			var steps Steps = streamSteps{&recordingSteps{}, f.root}
			if tc.reboot {
				// Even with no stream locator, !SameBoot proves the old run is gone.
				steps = &recordingSteps{}
			}
			next := f.loop(t, steps, &itemContentStager{})
			d := mustPlan(t, next)[0].decision
			if d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
				t.Fatalf("expected Pass 1 dispatch after recovery, got: %+v", d)
			}
			if holder := laneHolder(t, f, p.decision.LaneID); holder != "" {
				t.Fatalf("lane %s still leased to %s", p.decision.LaneID, holder)
			}
			ref := preservedPrefix + p.decision.RunRequest.RunID
			if out := runGit(t, f.root, "for-each-ref", "--format=%(refname)", ref); strings.TrimSpace(out) != ref {
				t.Fatalf("preserved ref %s missing: %q", ref, out)
			}
			s := openFixture(t, f.storePath)
			defer s.Close()
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				rec, err := terminalRecord(r, p.decision.RunRequest.RunID)
				if err != nil {
					return err
				}
				if rec == nil || rec.State != run.StateTimedOut || rec.EndCause != run.EndLeaseReclaimed {
					t.Fatalf("expected timed-out/lease-reclaimed record, got: %+v", rec)
				}
				saved, err := ReadPreservedWork(r)
				if err != nil {
					return err
				}
				if len(saved) == 0 || saved[0].RunID != p.decision.RunRequest.RunID {
					t.Fatalf("preserved work event missing: %+v", saved)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResumedSingleWorkerRunKilledByRelaunchRecoversInPass1(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, true)
	req := p.decision.RunRequest
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// Prior segment paused, then resumed by the same dead owner.
		rec := run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       p.decision.LaneID,
			Backend:      req.Backend,
			State:        run.StatePaused,
			StartedAt:    l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			FinishedAt:   l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			ArtifactRefs: []run.ArtifactRef{},
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, req.RunID, p.decision.LaneID); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{
			"run_id":     req.RunID,
			"item_id":    req.ItemID,
			"lane_id":    p.decision.LaneID,
			"backend":    req.Backend,
			"session_id": "33b5f2d2-0cd4-4a49-b293-c58f5635561f",
			"resumed_at": l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
		})
		return appendEffect(tx, run.ResumeKind, req.RunID, json.RawMessage(body), l.cfg.Now())
	})
	next := f.loop(t, streamSteps{&recordingSteps{}, f.root}, &itemContentStager{})
	d := mustPlan(t, next)[0].decision
	if d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop") {
		t.Fatalf("resumed single-worker run not recovered in Pass 1: %+v", d)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		active, err := runHasActiveResume(r, req.RunID)
		if err != nil {
			return err
		}
		if active {
			t.Fatal("runHasActiveResume still true after reap")
		}
		rec, err := terminalRecord(r, req.RunID)
		if err != nil {
			return err
		}
		if rec == nil || rec.State != run.StateTimedOut || rec.EndCause != run.EndLeaseReclaimed || !rec.Resumed {
			t.Fatalf("resumed run record not closed as timed-out/lease-reclaimed: %+v", rec)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResumedWorkflowNodeKilledByRelaunchMarksInterruptedAndPreservesCommit(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	// Complete node one (dispatch + settle).
	graphPass(t, l, result)
	graphPass(t, l, result)
	if len(steps.runs) != 1 {
		t.Fatalf("expected 1 completed node, got %+v", steps.runs)
	}
	// Plan node two and claim it under a foreign-domain dead owner.
	plans := mustPlan(t, l)
	var p plannedApplication
	for _, cand := range plans {
		if cand.decision.ItemID == "item-a" && cand.decision.Action == ActionDispatch {
			p = cand
			break
		}
	}
	if p.decision.RunRequest == nil {
		t.Fatalf("expected node two dispatch for item-a, got %+v", plans)
	}
	owner := relaunchedJailClaim(t, f, l, p)
	recordSession(t, f, owner, true)
	req := p.decision.RunRequest
	// Commit clean work on node two's branch, pause + resume under the dead owner.
	if err := os.MkdirAll(RunDir(f.root, req.RunID), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(RunDir(f.root, req.RunID), BaseSHAFile), p.decision.NodeBaseSHA+"\n")
	wt := l.laneTarget(p.decision.LaneID)
	writeForkFile(t, filepath.Join(wt, "tools/item-a/node2.txt"), "node2 committed\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "node2 commit")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       p.decision.LaneID,
			Backend:      req.Backend,
			State:        run.StatePaused,
			StartedAt:    l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			FinishedAt:   l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
			ArtifactRefs: []run.ArtifactRef{},
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, req.RunID, p.decision.LaneID); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{
			"run_id":     req.RunID,
			"item_id":    req.ItemID,
			"lane_id":    p.decision.LaneID,
			"backend":    req.Backend,
			"session_id": "33b5f2d2-0cd4-4a49-b293-c58f5635561f",
			"resumed_at": l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"),
		})
		return appendEffect(tx, run.ResumeKind, req.RunID, json.RawMessage(body), l.cfg.Now())
	})

	next := replaceGraphLoop(t, f, l, streamSteps{&recordingSteps{}, f.root})
	d := mustPlan(t, next)[0].decision
	if d.Action != ActionDispatch || !d.WorkflowRestart {
		t.Fatalf("expected workflow restart dispatch in Pass 1, got: %+v", d)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var outcome string
		if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id = ?`, req.RunID).Scan(&outcome); err != nil {
			return err
		}
		if outcome != "interrupted" {
			t.Fatalf("node_runs.outcome = %q, want interrupted", outcome)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Run pass on next so stageDispatches preserves node two's clean committed work and starts it.
	if _, err := next.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	ref := preservedPrefix + req.RunID
	if out := runGit(t, f.root, "for-each-ref", "--format=%(refname)", ref); strings.TrimSpace(out) != ref {
		t.Fatalf("preserved ref %s missing for clean committed workflow node: %q", ref, out)
	}
}

func TestEndedSessionOwnersHandlesInPlaceRestartSessions(t *testing.T) {
	f, l := passFixture(t, "item-a")
	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(*owner)
	if err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, ended_at) VALUES('pre-exec', 10, 10, 10, ?, NULL)`, string(raw)); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, ended_at) VALUES('post-exec', 20, 20, 20, ?, 30)`, string(raw))
		return err
	})
	_ = l
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		ended, err := endedSessionOwners(r)
		if err != nil {
			return err
		}
		if !ended[*owner] {
			t.Fatalf("endedSessionOwners[%+v] = false, want true when latest session ended", *owner)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
