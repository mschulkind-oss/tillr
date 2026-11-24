package drain

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A model that writes its footer in Markdown emphasis or code still answered: the word is
// read through the markers. `**RESULT: done**` read as `done**` is what held the
// 2026-11-18 triage item at read-9 under run-link-missing.
func TestReportFooterReadsTheAnswerThroughMarkdownEmphasis(t *testing.T) {
	for log, want := range map[string]string{
		"Findings.\n**RESULT: done**\n": "done",
		"`RESULT: failed`":              "failed",
		"RESULT: **blocked**.":          "blocked",
		"_RESULT: done_\n":              "done",
		"RESULT: done\n":                "done",
		"RESULT: trunk-broken\n":        "trunk-broken",
		"RESULT: finished\n":            "finished",
		"no footer at all\n":            "",
	} {
		if got := reportFooter(log); got != want {
			t.Errorf("reportFooter(%q) = %q, want %q", log, got, want)
		}
	}
}

// A reader whose footer names no ending the contract knows gave no answer the loop can
// read, exactly like a reader with no footer: it alone runs again, and its finished
// siblings are kept rather than the set being abandoned with no cause recorded.
func TestParallelReaderWithAnUnreadableFooterRunsAgainAlone(t *testing.T) {
	f, l, steps := parallelFixture(t)
	steps.badReportNode = "-n2"
	steps.badReport = "Scout findings.\nRESULT: finished\n"
	steps.writerAfter = 5
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "alternate"}
	l.cfg.Policy.SeatLists = map[string][]string{"scout": {"fixture", "alternate"}, "build": {"fixture", "alternate"}}
	result := &LoopResult{}
	for i := 0; i < 5; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.started != 5 || steps.builders != 1 {
		t.Fatalf("started=%d builders=%d", steps.started, steps.builders)
	}
	if result.InfraFailures != 0 || l.infraStreak != 0 {
		t.Fatalf("unreadable footer on normal exit counted infrastructure failure: result=%d streak=%d", result.InfraFailures, l.infraStreak)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var id, backend string
		err := r.QueryRow(`SELECT n.run_id,r.backend FROM node_runs n JOIN runs r ON r.run_id=n.run_id WHERE n.node_id='scout2' AND n.outcome='passed'`).Scan(&id, &backend)
		if id != "run-item-a-a1-n2-restart" || backend != "fixture" {
			t.Errorf("the unread reader did not run again alone on its original backend: %q on %q", id, backend)
		}
		return err
	})
	briefBytes, err := os.ReadFile(filepath.Join(RunDir(f.root, "run-item-a-a1-n2-restart"), BriefFile))
	if err != nil {
		t.Fatalf("read restarted reader brief: %v", err)
	}
	brief := string(briefBytes)
	for _, want := range []string{`"finished"`, "done", "failed", "blocked", "trunk-broken"} {
		if !strings.Contains(brief, want) {
			t.Errorf("restarted parallel reader brief missing %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "orphaned workflow restarted") {
		t.Errorf("restarted parallel reader brief still says orphaned workflow restarted:\n%s", brief)
	}
}

// legacyUnreadReader reproduces the store a loop built before footers were read through
// emphasis left behind: one reader of a finished set recorded `blocked` with no conflict
// and no cause, the set abandoned and examined, later readers never run.
func legacyUnreadReader(t *testing.T) (refusalLoopFixture, *Loop, *parallelSteps) {
	t.Helper()
	f, l, steps := parallelFixture(t)
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	drainOwned(t, l, result)
	// The fixture's set is all four readers, run and not yet settled. Record the outcomes
	// the old settlement did — three passed, scout2 blocked on a misread `done` — then
	// abandon and examine the set as it did.
	work := pendingForTest(t, f, "item-a")
	d := work.Decision
	if steps.ended != 4 || steps.builders != 0 {
		t.Fatalf("fixture: ended=%d builders=%d", steps.ended, steps.builders)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE node_runs SET outcome=CASE node_id WHEN 'scout2' THEN 'blocked' ELSE 'passed' END WHERE item_id='item-a' AND attempt=1`)
		return err
	})
	head := runGit(t, l.laneTarget(d.LaneID), "rev-parse", "HEAD")
	if stop := l.abandon(context.Background(), d.LaneID, d.RunRequest.RunID, head, "read-only nodes failed"); stop != "" {
		t.Fatal(stop)
	}
	if err := l.markExamined(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return f, l, steps
}

// The legacy state names its node and its mover instead of run-link-missing, and the
// mover's retry keeps every reader that passed: only the unread reader and the nodes after
// it run in the new attempt, and the item lands.
func TestAnUnreadReaderHoldsNamedAndItsRetryKeepsThePassedReaders(t *testing.T) {
	f, l, steps := legacyUnreadReader(t)
	var held Decision
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" {
			held = p.decision
		}
	}
	if held.Action != ActionHold || held.Reason != ReasonNodeAnswerUnread+": scout2" {
		t.Fatalf("held as %s %s (builders=%d started=%d)", held.Action, held.Reason, steps.builders, steps.started)
	}
	if steps.builders != 0 || steps.started != 4 {
		t.Fatalf("started=%d builders=%d", steps.started, steps.builders)
	}
	// The retry names the item's latest finished run, as every build retry does.
	var latest string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT run_id FROM runs WHERE item_id='item-a' ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`).Scan(&latest)
	})
	// A retry naming an earlier run would be recorded and then ignored by the plan; it is
	// refused, naming the run that is accepted.
	earlier := "run-item-a-a1-n1"
	if latest == earlier {
		earlier = "run-item-a-a1-n2"
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: earlier, By: "operator", Why: "reader answer unread", At: l.cfg.Now()})
		if err == nil || !strings.Contains(err.Error(), "latest finished run is "+latest) {
			t.Errorf("a retry of an earlier run: %v", err)
		}
		return nil
	})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: latest, By: "operator", Why: "reader answer unread", At: l.cfg.Now()})
		return err
	})
	for _, p := range mustPlan(t, l) {
		if d := p.decision; d.ItemID == "item-a" && (d.Action != ActionRetry || d.NodeIndex != 1 || d.Attempt != 2 || len(d.ReusedNodes) != 3) {
			t.Fatalf("retry plan: %s %s node=%d attempt=%d reused=%v", d.Action, d.Reason, d.NodeIndex, d.Attempt, d.ReusedNodes)
		}
	}
	// The trunk moves on before the retry, as it did on 2026-11-19: the retry is a fresh
	// attempt on the new trunk, so its first node is staged fresh even though it is not
	// node 0, not refused because the lane still sits at the earlier trunk.
	writeForkFile(t, filepath.Join(f.root, "unrelated.md"), "trunk moved\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "trunk moves before the retry")
	steps.firstHead = runGit(t, f.root, "rev-parse", "HEAD")
	steps.writerAfter = 5
	result := &LoopResult{}
	for i := 0; i < 6 && result.Landed == 0; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if result.Landed != 1 || steps.started != 5 || steps.builders != 1 {
		t.Fatalf("landed=%d started=%d builders=%d", result.Landed, steps.started, steps.builders)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		outcomes, err := workflowOutcomes(r, "item-a", 2)
		for _, node := range []string{"scout1", "scout2", "scout3", "scout4", "build"} {
			if outcomes[node] != "passed" {
				t.Errorf("attempt 2 %s: %q", node, outcomes[node])
			}
		}
		var reran int
		if err == nil {
			err = r.QueryRow(`SELECT COUNT(*) FROM node_runs WHERE item_id='item-a' AND attempt=2 AND node_id IN ('scout1','scout3','scout4')`).Scan(&reran)
		}
		if reran != 0 {
			t.Errorf("%d passed readers ran again", reran)
		}
		return err
	})
}

func TestNormalExitWorkflowNodeWithoutFooterNotCountedAsInfrastructure(t *testing.T) {
	t.Run("missing footer without recorded backend failure", func(t *testing.T) {
		f, l, _ := workflowLoopFixture(t, "")
		var reportBuf bytes.Buffer
		l.cfg.Report = &reportBuf
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		p := mustPlan(t, l)[0]
		if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		d := p.decision
		runGit(t, l.laneTarget(d.LaneID), "reset", "--hard", base)
		rec := run.Record{
			RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture",
			State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-23T14:43:54Z", FinishedAt: "2026-11-23T15:06:19Z",
		}
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

		result := &LoopResult{}
		handled, err := l.settleNode(context.Background(), d, rec, "Took the do-it-now path and committed.", base, result, false)
		if err != nil || !handled {
			t.Fatalf("settleNode = (%v, %v), want (true, nil)", handled, err)
		}
		if result.InfraFailures != 0 || l.infraStreak != 0 {
			t.Fatalf("normal exit without footer counted infrastructure failure: result=%d streak=%d", result.InfraFailures, l.infraStreak)
		}
		if strings.Contains(reportBuf.String(), "failed in the infrastructure class") {
			t.Fatalf("normal exit without footer logged infrastructure failure: %s", reportBuf.String())
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			var count int
			if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=?`, infrastructureFailureKind, d.ItemID).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				t.Errorf("recorded %d infrastructure failure events, want 0", count)
			}
			return nil
		})
		restarted := pendingForTest(t, f, d.ItemID)
		if !restarted.Decision.WorkflowRestart || restarted.Decision.InfrastructureFrom != rec.RunID {
			t.Fatalf("restarted = %+v, want WorkflowRestart=true InfrastructureFrom=%s", restarted.Decision, rec.RunID)
		}
	})

	t.Run("recorded backend transport error", func(t *testing.T) {
		f, l, _ := workflowLoopFixture(t, "")
		var reportBuf bytes.Buffer
		l.cfg.Report = &reportBuf
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		p := mustPlan(t, l)[0]
		if ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		d := p.decision
		runGit(t, l.laneTarget(d.LaneID), "reset", "--hard", base)
		d.Seat = "design"
		const errText = "503 Service Unavailable"
		streamLog := filepath.Join(t.TempDir(), "run.log")
		writeForkFile(t, streamLog, strings.Join([]string{
			`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + errText + `","content":[]}]}`,
			`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"` + errText + `"}`,
		}, "\n")+"\n")
		rec := run.Record{
			RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture",
			State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-23T14:43:54Z", FinishedAt: "2026-11-23T15:06:19Z",
			ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: streamLog}},
		}
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

		result := &LoopResult{}
		handled, err := l.settleNode(context.Background(), d, rec, "", base, result, false)
		if err != nil || !handled {
			t.Fatalf("settleNode = (%v, %v), want (true, nil)", handled, err)
		}
		if result.InfraFailures != 1 || l.infraStreak != 1 {
			t.Fatalf("recorded backend transport error did not count infrastructure failure: result=%d streak=%d", result.InfraFailures, l.infraStreak)
		}
		if !strings.Contains(reportBuf.String(), "failed in the infrastructure class") {
			t.Fatalf("recorded backend transport error did not log infrastructure failure: %s", reportBuf.String())
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			failure, err := readRecordedInfrastructureFailure(r, d.ItemID, "$.run_id", rec.RunID)
			if err != nil {
				return err
			}
			if !strings.Contains(failure.Words, errText) {
				t.Errorf("recorded infrastructure failure words = %q, want %q", failure.Words, errText)
			}
			return nil
		})
	})
}

func TestUnreadFooterRestartBriefQuotesWordAndAcceptedEndings(t *testing.T) {
	runRestart := func(t *testing.T, writeNode, dirty, commit bool, rec run.Record, report string) string {
		t.Helper()
		f, l, _ := workflowLoopFixture(t, "")
		ctx := context.Background()
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		p := mustPlan(t, l)[0]
		if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		d := p.decision
		if writeNode {
			d.Workflow.Stages[0].Access = "write"
		}
		staged, err := l.stageDispatches(ctx, []Decision{d}, base, &LoopResult{})
		if err != nil || len(staged) != 1 {
			t.Fatalf("stage first run: len=%d err=%v", len(staged), err)
		}
		d = staged[0]
		target := l.laneTarget(d.LaneID)
		if dirty || commit {
			writeForkFile(t, filepath.Join(target, "a.txt"), "earlier step work\n")
		}
		if commit {
			runGit(t, target, "add", "a.txt")
			runGit(t, target, "commit", "-qm", "earlier step commit")
		}

		rec.RunID = d.RunRequest.RunID
		rec.ItemID = d.ItemID
		rec.LaneID = d.LaneID
		rec.Backend = "fixture"
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })

		result := &LoopResult{}
		handled, err := l.settleNode(ctx, d, rec, report, base, result, false)
		if err != nil || !handled {
			t.Fatalf("settleNode = (%v, %v), want (true, nil)", handled, err)
		}
		restarted := pendingForTest(t, f, d.ItemID)
		if !restarted.Decision.WorkflowRestart {
			t.Fatalf("restarted = %+v, want WorkflowRestart=true", restarted.Decision)
		}
		stagedRestart, err := l.stageDispatches(ctx, []Decision{restarted.Decision}, base, &LoopResult{})
		if err != nil || len(stagedRestart) != 1 {
			t.Fatalf("stage restarted run: len=%d err=%v", len(stagedRestart), err)
		}
		briefBytes, err := os.ReadFile(filepath.Join(RunDir(f.root, stagedRestart[0].RunRequest.RunID), BriefFile))
		if err != nil {
			t.Fatalf("read restarted brief: %v", err)
		}
		return string(briefBytes)
	}

	t.Run("unread footer word on write step with committed work", func(t *testing.T) {
		brief := runRestart(t, true, false, true, run.Record{
			State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-23T23:03:00Z", FinishedAt: "2026-11-23T23:38:13Z",
		}, "Did the work and committed.\nRESULT: ok\n")
		for _, want := range []string{`"ok"`, "done", "failed", "blocked", "trunk-broken"} {
			if !strings.Contains(brief, want) {
				t.Errorf("restarted brief missing %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, "orphaned workflow restarted") {
			t.Errorf("restarted brief still contains orphaned workflow restarted:\n%s", brief)
		}
	})

	t.Run("unread footer word on write step with dirty work", func(t *testing.T) {
		brief := runRestart(t, true, true, false, run.Record{
			State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-23T23:03:00Z", FinishedAt: "2026-11-23T23:38:13Z",
		}, "Did the work.\nRESULT: ok\n")
		for _, want := range []string{"Earlier work exists", `"ok"`, "done", "failed", "blocked", "trunk-broken"} {
			if !strings.Contains(brief, want) {
				t.Errorf("restarted brief missing %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, "orphaned workflow restarted") || strings.Contains(brief, "workflow node answer unread") {
			t.Errorf("restarted brief still contains generic reason:\n%s", brief)
		}
	})

	t.Run("unread footer word on read-only step with no worktree changes", func(t *testing.T) {
		brief := runRestart(t, false, false, false, run.Record{
			State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-23T23:03:00Z", FinishedAt: "2026-11-23T23:38:13Z",
		}, "Plan ready.\nRESULT: ok\n")
		for _, want := range []string{`"ok"`, "done", "failed", "blocked", "trunk-broken"} {
			if !strings.Contains(brief, want) {
				t.Errorf("restarted brief missing %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, "orphaned workflow restarted") {
			t.Errorf("restarted brief still contains orphaned workflow restarted:\n%s", brief)
		}
	})

	t.Run("infrastructure failure restart keeps present wording", func(t *testing.T) {
		brief := runRestart(t, true, true, false, run.Record{
			State: run.StateFailedToStart, EndCause: run.EndFailedToStart,
			StartedAt: "2026-11-23T23:03:00Z", FinishedAt: "2026-11-23T23:03:05Z",
		}, "")
		if !strings.Contains(brief, "That run stopped: workflow infrastructure failure.") {
			t.Errorf("infrastructure restart brief changed wording:\n%s", brief)
		}
		if strings.Contains(brief, "report ended with unread") {
			t.Errorf("infrastructure restart brief unexpectedly mentions unread footer:\n%s", brief)
		}
	})

	t.Run("signal interruption restart keeps present wording", func(t *testing.T) {
		brief := runRestart(t, true, false, true, run.Record{
			State: run.StateExited, EndCause: run.EndSignal, ExitCode: code(143),
			StartedAt: "2026-11-23T23:03:00Z", FinishedAt: "2026-11-23T23:05:00Z",
		}, "")
		if !strings.Contains(brief, "That run stopped: orphaned workflow restarted.") {
			t.Errorf("signal restart brief changed wording:\n%s", brief)
		}
		if strings.Contains(brief, "report ended with unread") {
			t.Errorf("signal restart brief unexpectedly mentions unread footer:\n%s", brief)
		}
	})

	t.Run("missing footer restart keeps present wording", func(t *testing.T) {
		brief := runRestart(t, true, true, false, run.Record{
			State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-23T23:03:00Z", FinishedAt: "2026-11-23T23:05:00Z",
		}, "Finished without writing a footer.")
		if !strings.Contains(brief, "That run stopped: workflow node answer unread.") {
			t.Errorf("missing-footer restart brief changed wording:\n%s", brief)
		}
		if strings.Contains(brief, "report ended with unread") {
			t.Errorf("missing-footer restart brief unexpectedly mentions unread footer:\n%s", brief)
		}
	})
}
