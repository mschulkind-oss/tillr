package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestClassifyAttemptSignalAndExceptionEdges(t *testing.T) {
	cases := []struct {
		name string
		rec  run.Record
		log  string
		want AttemptClass
	}{
		{
			name: "end-signal-exit-minus-one",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(-1), EndCause: run.EndSignal},
			want: AttemptInterrupted,
		},
		{
			name: "exit-143-without-exception-or-footer",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(143)},
			want: AttemptInterrupted,
		},
		{
			name: "exit-130-without-exception-or-footer",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(130)},
			want: AttemptInterrupted,
		},
		{
			name: "exit-143-with-ending-exception-is-not-interrupted",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(143), EndingException: "adapter-settled"},
			want: AttemptInfrastructure,
		},
		{
			name: "exit-130-with-readable-footer-without-end-signal-is-answered",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(130)},
			log:  "RESULT: done\n",
			want: AttemptAnswered,
		},
		{
			name: "end-signal-even-with-footer-is-interrupted",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal},
			log:  "RESULT: done\n",
			want: AttemptInterrupted,
		},
		{
			name: "crash-signal-exit-minus-one-without-end-signal-is-infrastructure",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(-1)},
			want: AttemptInfrastructure,
		},
		{
			name: "exit-1-without-report-is-infrastructure",
			rec:  run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(1)},
			want: AttemptInfrastructure,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyAttempt(tc.rec, tc.log); got != tc.want {
				t.Fatalf("ClassifyAttempt = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSignalEndingsLeaveBackendAvailabilityUnchangedAndInfraStillCloses(t *testing.T) {
	// (a1) 4+ SIGTERM/SIGINT endings leave an open backend open, keep InfraFailures == 0,
	// and stay on the same backend.
	f, l := passFixture(t, "item-a")
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["backup"] = b
	l.cfg.Policy.Preference = []string{"fixture", "backup"}

	signals := []run.Record{
		{RunID: "sig-1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(-1), EndCause: run.EndSignal, StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z"},
		{RunID: "sig-2", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(143), StartedAt: "2026-11-19T00:00:02Z", FinishedAt: "2026-11-19T00:00:03Z"},
		{RunID: "sig-3", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(130), StartedAt: "2026-11-19T00:00:04Z", FinishedAt: "2026-11-19T00:00:05Z"},
		{RunID: "sig-4", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal, StartedAt: "2026-11-19T00:00:06Z", FinishedAt: "2026-11-19T00:00:07Z"},
	}
	for _, rec := range signals {
		r := rec
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, r) })
	}
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosed(tx.Reader())
		if err != nil {
			return err
		}
		if closed["fixture"] {
			t.Fatal("4 signal endings closed open backend fixture")
		}
		return nil
	})
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.RunRequest.Backend != "fixture" {
		t.Fatalf("4 signal endings failed over or held: %+v", d)
	}

	// (a2) Signal endings also leave an already-closed backend closed.
	backendRun(t, f, "infra-close", "fixture", 1)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: "sig-after-close", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal, StartedAt: "2026-11-19T00:00:08Z", FinishedAt: "2026-11-19T00:00:09Z"})
	})
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosed(tx.Reader())
		if err != nil {
			return err
		}
		if !closed["fixture"] {
			t.Fatal("signal ending reopened closed backend fixture")
		}
		return nil
	})
	d = mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.RunRequest.Backend != "backup" {
		t.Fatalf("expected failover to backup after real infra close, got %+v", d)
	}

	// (a3) Crash signal ExitCode=-1 without EndSignal still closes backup too.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: "crash-close", ItemID: "item-a", LaneID: "lane-1", Backend: "backup", State: run.StateExited, ExitCode: code(-1), StartedAt: "2026-11-19T00:00:10Z", FinishedAt: "2026-11-19T00:00:11Z"})
	})
	if err := RecordBackendOutcomes(context.Background(), f.storePath); err != nil {
		t.Fatal(err)
	}
	d = mustPlan(t, l)[0].decision
	if d.Action != ActionHold || d.Reason != ReasonBackendClosed {
		t.Fatalf("crash signal did not close backup: %+v", d)
	}
}

func TestSingleRunSettleSignalDoesNotIncrementInfraOrFailover(t *testing.T) {
	f, l := passFixture(t, "item-a")
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["backup"] = b
	l.cfg.Policy.Preference = []string{"fixture", "backup"}

	p := mustPlan(t, l)[0]
	mainSHA := runGit(t, f.root, "rev-parse", TrunkRef)
	if ok, _, err := l.applyPlan(context.Background(), p, mainSHA, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("applyPlan: %v %v", ok, err)
	}
	rec := run.Record{
		RunID: p.decision.RunRequest.RunID, ItemID: "item-a", LaneID: p.decision.LaneID,
		Backend: "fixture", State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal,
		StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
	}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	var result LoopResult
	out := l.settle(context.Background(), p.decision, rec, "", mainSHA, "tick-1", l.cfg.Now(), &result, &PassTiming{}, nil, true)
	if out.err != nil || out.stop != "" {
		t.Fatalf("settle: %+v", out)
	}
	if result.InfraFailures != 0 || l.infraStreak != 0 {
		t.Fatalf("signal incremented infra: result=%d streak=%d", result.InfraFailures, l.infraStreak)
	}
	next := mustPlan(t, l)[0].decision
	if next.Action != ActionDispatch || next.RunRequest.Backend != "fixture" {
		t.Fatalf("signal failed over: %+v", next)
	}
}

func TestUnsavedEndSignalClassifiedBeforeRecordBackendOutcomes(t *testing.T) {
	f, l := passFixture(t, "item-a")
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["backup"] = b
	l.cfg.Policy.Preference = []string{"fixture", "backup"}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID: "unsaved-sig", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
			State: run.StateExited, ExitCode: code(-1), EndCause: run.EndSignal,
			StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
		})
	})
	// Call mustPlan directly WITHOUT calling RecordBackendOutcomes first.
	d := mustPlan(t, l)[0].decision
	if d.Action != ActionDispatch || d.RunRequest.Backend != "fixture" {
		t.Fatalf("unsaved EndSignal failed over to %s: %+v", d.RunRequest.Backend, d)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		priors, err := readPriorRuns(r)
		if err != nil {
			return err
		}
		if len(priors["item-a"]) != 1 || priors["item-a"][0].Class != AttemptInterrupted {
			t.Fatalf("unsaved EndSignal priors = %+v, want AttemptInterrupted", priors["item-a"])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type seqSignalWorkflowSteps struct {
	*workflowSteps
	interruptFirst bool
	interrupted    bool
	runs           []run.Request
}

func (s *seqSignalWorkflowSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	if s.interruptFirst && !s.interrupted && req.NodeID == "work" {
		s.interrupted = true
		rec := run.Record{
			RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
			State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal,
			StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
			ArtifactRefs: []run.ArtifactRef{},
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "", nil
	}
	return s.workflowSteps.Run(ctx, req)
}

func TestSequentialWorkflowNodeInterruptedRestartsSameBackendAndLands(t *testing.T) {
	f, l, ws := workflowLoopFixture(t, "")
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["backup"] = b
	l.cfg.Policy.Preference = []string{"fixture", "backup"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	steps := &seqSignalWorkflowSteps{workflowSteps: ws, interruptFirst: true}
	l.cfg.Steps = steps

	result, err := l.Run(context.Background())
	if err != nil || result.Stop != StopQueueDrained || result.Landed != 1 {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
	if result.InfraFailures != 0 {
		t.Fatalf("InfraFailures = %d, want 0", result.InfraFailures)
	}
	foundInterrupted := false
	for _, r := range steps.runs {
		if strings.HasSuffix(r.RunID, "-interrupted") {
			foundInterrupted = true
			if r.Backend != "fixture" {
				t.Fatalf("restart run = %+v, want -interrupted on fixture", r)
			}
		}
	}
	if !foundInterrupted {
		t.Fatalf("missing -interrupted run in %+v", steps.runs)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosed(tx.Reader())
		if err != nil {
			return err
		}
		if closed["fixture"] {
			t.Fatal("interrupted sequential workflow node closed fixture")
		}
		return nil
	})
}

type parallelSignalSteps struct {
	*parallelSteps
	mu             sync.Mutex
	signalNode     string
	signaled       bool
	readerRuns     map[string]int
	readerBackends map[string]string
}

func (s *parallelSignalSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if s.signalNode != "" && strings.HasSuffix(req.RunID, "-"+s.signalNode) {
		s.mu.Lock()
		first := !s.signaled
		s.signaled = true
		s.mu.Unlock()
		if first {
			// Wait for siblings so the overlap barrier in parallelSteps.Run doesn't hang.
			s.parallelSteps.mu.Lock()
			s.parallelSteps.started++
			wantOverlap := s.parallelSteps.wantOverlap
			if wantOverlap == 0 {
				wantOverlap = 4
			}
			if s.parallelSteps.started == wantOverlap {
				close(s.parallelSteps.overlap)
			}
			s.parallelSteps.mu.Unlock()
			<-s.parallelSteps.overlap
			s.parallelSteps.mu.Lock()
			s.parallelSteps.ended++
			s.parallelSteps.mu.Unlock()
			rec := run.Record{
				RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
				State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal,
				StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
				ArtifactRefs: []run.ArtifactRef{},
			}
			fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
			return rec, "", nil
		}
	}
	if !strings.HasSuffix(req.RunID, "-n5") {
		s.mu.Lock()
		s.readerRuns[req.NodeID]++
		s.readerBackends[req.RunID] = req.Backend
		s.mu.Unlock()
	}
	return s.parallelSteps.Run(ctx, req)
}

func TestParallelReadOnlyNodeInterruptedRestartsOnlyInterruptedReaderSameBackend(t *testing.T) {
	f, l, ps := parallelFixture(t)
	b := l.cfg.Policy.Backends["fixture"]
	l.cfg.Policy.Backends["backup"] = b
	l.cfg.Policy.Preference = []string{"fixture", "backup"}
	ps.writerAfter = 5
	ps.builderSuffix = "-n5"
	steps := &parallelSignalSteps{
		parallelSteps:  ps,
		signalNode:     "n1",
		readerRuns:     map[string]int{},
		readerBackends: map[string]string{},
	}
	l.cfg.Steps = steps

	result, err := l.Run(context.Background())
	if err != nil || result.Stop != StopQueueDrained || result.Landed != 1 {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
	if result.InfraFailures != 0 {
		t.Fatalf("InfraFailures = %d, want 0", result.InfraFailures)
	}
	if steps.readerRuns["scout2"] != 1 || steps.readerRuns["scout3"] != 1 || steps.readerRuns["scout4"] != 1 {
		t.Fatalf("finished siblings reran: %+v", steps.readerRuns)
	}
	if steps.readerRuns["scout1"] != 1 {
		t.Fatalf("interrupted scout1 retry ran %d times after signal, want 1", steps.readerRuns["scout1"])
	}
	foundInterrupted := false
	for runID, backend := range steps.readerBackends {
		if strings.Contains(runID, "-n1-interrupted") {
			foundInterrupted = true
			if backend != "fixture" {
				t.Fatalf("interrupted reader backend = %s, want fixture", backend)
			}
		}
	}
	if !foundInterrupted {
		t.Fatalf("missing -n1-interrupted run in %+v", steps.readerBackends)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		closed, err := readInfrastructureClosed(tx.Reader())
		if err != nil {
			return err
		}
		if closed["fixture"] {
			t.Fatal("interrupted parallel reader closed fixture")
		}
		return nil
	})
}

type judgeSignalSteps struct {
	*workflowSteps
	judgeCount int
}

func (s *judgeSignalSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	if !strings.HasSuffix(req.RunID, "-judge") && !strings.HasSuffix(req.RunID, "-judge-interrupted") && req.NodeID != "verify" {
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a/out.txt"), req.RunID+"\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "work")
		if req.NodeID == "" || req.NodeID == "build" {
			report := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
			if req.NodeID != "" {
				report = filepath.Join(RunDir(s.f.root, req.RunID), "report.md")
			}
			writeForkFile(s.t, report, "RESULT: done\n")
			rec := run.Record{
				RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
				State: run.StateExited, ExitCode: code(0),
				StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:00Z",
				ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}},
			}
			fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
			return rec, "RESULT: done\n", nil
		}
		return s.workflowSteps.Run(ctx, req)
	}
	s.judgeCount++
	if s.judgeCount == 1 {
		rec := run.Record{
			RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
			State: run.StateExited, ExitCode: code(143), EndCause: run.EndSignal,
			StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:00:01Z",
			ArtifactRefs: []run.ArtifactRef{},
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "", nil
	}
	head := runGit(s.t, target, "rev-parse", "HEAD")
	v := item.JudgeOutput{
		EvaluatedHead: head,
		EvaluatedTree: runGit(s.t, target, "rev-parse", head+"^{tree}"),
		Outcome:       "pass",
		Verdicts: []item.JudgeEntry{
			{CriterionID: "c1", CriterionHash: item.CriterionHash("the first criterion of item-a"), Verdict: "done", ClaimClass: item.ClaimVerification, Reason: "ok", Evidence: []string{"commit:" + head}},
		},
	}
	raw, _ := json.Marshal(v)
	writeForkFile(s.t, filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "eval.output"), string(raw))
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: code(0),
		StartedAt: "2026-11-19T00:00:02Z", FinishedAt: "2026-11-19T00:00:03Z",
		ArtifactRefs: []run.ArtifactRef{},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", nil
}

func TestAgentJudgeAndWorkflowEvaluatorInterruptedRestartSameBackendAndLand(t *testing.T) {
	t.Run("implicit-agent-judge", func(t *testing.T) {
		f, l, ws := workflowLoopFixture(t, "")
		ws.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
		l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
		l.cfg.Policy.Backends["reviewer-backup"] = Backend{Adapter: "fixture", Model: "review-backup"}
		l.cfg.Policy.Seats["review"] = "reviewer"
		l.cfg.Policy.SeatLists = map[string][]string{"review": {"reviewer", "reviewer-backup"}}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET workflow='',judge_mode='agent' WHERE item_id='item-a'`); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-review','verification-only','owner',?,?)`, l.cfg.Now().Add(-time.Hour).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
			return err
		})
		steps := &judgeSignalSteps{workflowSteps: ws}
		l.cfg.Steps = steps

		result, err := l.Run(context.Background())
		if err != nil || result.Stop != StopQueueDrained || result.Landed != 1 {
			t.Fatalf("unexpected result: %+v err=%v", result, err)
		}
		if result.Rejected != 0 || result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
			t.Fatalf("recorded judge unreadable/rejected: %+v", result)
		}
		if steps.judgeCount != 2 {
			t.Fatalf("judgeCount = %d, want 2", steps.judgeCount)
		}
		last := steps.runs[len(steps.runs)-1]
		if !strings.HasSuffix(last.RunID, "-interrupted") || last.Backend != "reviewer" {
			t.Fatalf("replacement judge run = %+v, want -interrupted on reviewer", last)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			closed, err := readInfrastructureClosed(tx.Reader())
			if err != nil {
				return err
			}
			if closed["reviewer"] {
				t.Fatal("interrupted agent judge closed reviewer")
			}
			return nil
		})
	})

	t.Run("workflow-evaluator", func(t *testing.T) {
		f, l, ws := workflowLoopFixture(t, "")
		l.cfg.Policy.Backends["reviewer"] = Backend{Adapter: "fixture", Model: "review-model"}
		l.cfg.Policy.Backends["reviewer-backup"] = Backend{Adapter: "fixture", Model: "review-backup"}
		l.cfg.Policy.Seats["review"] = "reviewer"
		l.cfg.Policy.SeatLists = map[string][]string{"review": {"reviewer", "reviewer-backup"}}
		raw := `{"name":"eval-signal","version":1,"stages":[{"id":"build","kind":"agent","seat":"build","access":"write","prompt":"build"},{"id":"verify","kind":"agent","seat":"review","access":"read_only","prompt":"verify"}],"edges":[{"from_node":"build","to_node":"verify","when_outcome":"pass"}],"exit":{"evaluator_slot":"verify","requires_nodes":["build","verify"]}}`
		writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/eval-signal@v1.json"), raw)
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-qm", "eval-signal workflow")
		ws.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET workflow='eval-signal@v1' WHERE item_id='item-a'`); err != nil {
				return err
			}
			_, err := tx.Exec(`INSERT INTO item_grants(grant_id,claim_classes,grantor,issued_at,expires_at) VALUES('grant-review','verification-only','owner',?,?)`, l.cfg.Now().Add(-time.Hour).UnixMilli(), l.cfg.Now().Add(time.Hour).UnixMilli())
			return err
		})
		steps := &judgeSignalSteps{workflowSteps: ws}
		l.cfg.Steps = steps

		result, err := l.Run(context.Background())
		if err != nil || result.Stop != StopQueueDrained || result.Landed != 1 {
			t.Fatalf("unexpected result: %+v err=%v", result, err)
		}
		if result.Rejected != 0 || result.RefusedSteps[ReasonJudgeUnreadable] != 0 {
			t.Fatalf("recorded judge unreadable/rejected: %+v", result)
		}
		if steps.judgeCount != 2 {
			t.Fatalf("judgeCount = %d, want 2; runs=%+v", steps.judgeCount, steps.runs)
		}
		last := steps.runs[len(steps.runs)-1]
		if !strings.HasSuffix(last.RunID, "-interrupted") || last.Backend != "reviewer" {
			t.Fatalf("replacement verify run = %+v, want -interrupted on reviewer", last)
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			closed, err := readInfrastructureClosed(tx.Reader())
			if err != nil {
				return err
			}
			if closed["reviewer"] {
				t.Fatal("interrupted evaluator closed reviewer")
			}
			return nil
		})
	})
}
