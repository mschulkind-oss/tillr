package drain

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A read-only reader whose restart again leaves no readable report is refused through the
// report-missing route and is not restarted a second time; the lane is released and the
// examination completes rather than the group advancing or restarting without end.
func TestParallelReaderUnreadTwiceIsRefusedNotRestartedAgain(t *testing.T) {
	for _, tc := range []struct{ name, suffix string }{{"sibling reader", "-n2"}, {"primary reader", "-n1"}} {
		t.Run(tc.name, func(t *testing.T) {
			f, l, steps := parallelFixture(t)
			steps.writerAfter = 5
			steps.badReportNode = tc.suffix
			steps.badReport = "no report footer\n"
			steps.badReports = map[string]string{tc.suffix + "-restart": "[Pi's last assistant turn had no text or tool call.]\n"}
			result := &LoopResult{}
			for i := 0; i < 6; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if steps.started != 5 || steps.builders != 0 {
				t.Fatalf("started=%d builders=%d, want four readers and exactly one restart, no writer", steps.started, steps.builders)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var restarts int
				if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE run_id LIKE '%-restart' OR run_id LIKE '%-restart-%'`).Scan(&restarts); err != nil {
					return err
				}
				if restarts != 1 {
					t.Errorf("%d restart runs, want exactly 1", restarts)
				}
				var runID, detail string
				if err := r.QueryRow(`SELECT object_id, json_extract(payload,'$.detail') FROM events WHERE kind=? AND json_extract(payload,'$.step')=?`, RefusalKind, RefusalReportMissing).Scan(&runID, &detail); err != nil {
					t.Fatalf("no report-missing refusal: %v", err)
				}
				if !strings.HasSuffix(runID, tc.suffix+"-restart") {
					t.Errorf("refusal names run %q, want the restarted reader", runID)
				}
				if !strings.Contains(detail, runID) || !strings.Contains(detail, "node ") {
					t.Errorf("refusal detail %q names neither node nor run", detail)
				}
				var outcome string
				if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, runID).Scan(&outcome); err != nil {
					return err
				}
				if outcome != "refused" {
					t.Errorf("node outcome = %q, want refused", outcome)
				}
				return nil
			})
		})
	}
}

// The flag marks the one restart; interruption restarts and infrastructure restarts leave
// it clear, and a run that is not itself the restart is restarted once.
func TestUnreadReportRepeatsOnlyForTheRestartOfAnUnreadNode(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	d := p.decision
	exited := func(id string, exit int) run.Record {
		return run.Record{RunID: id, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(exit)}
	}
	if got, err := l.unreadReportRepeats(ctx, d, exited(d.RunRequest.RunID, 0)); err != nil || got {
		t.Fatalf("first unreadable ending: repeats = %v, %v; want false", got, err)
	}
	d.UnreadReportRestart = true
	if got, err := l.unreadReportRepeats(ctx, d, exited(d.RunRequest.RunID+"-restart", 0)); err != nil || !got {
		t.Fatalf("restart's unreadable ending: repeats = %v, %v; want true", got, err)
	}
	if got, _ := l.unreadReportRepeats(ctx, d, exited(d.RunRequest.RunID+"-restart", 1)); got {
		t.Fatal("a non-zero exit is not a missing report")
	}
	d.Seat = "design"
	if got, _ := l.unreadReportRepeats(ctx, d, exited(d.RunRequest.RunID+"-restart", 0)); got {
		t.Fatal("a design run keeps its own route")
	}
}

// A serial node whose first run exits 0 with Pi's placeholder report is restarted once,
// flagged as that restart; the restart leaving none again is refused as report-missing and
// no further restart is recorded.
func TestSerialNodeUnreadTwiceIsRestartedOnceThenRefused(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	ctx := context.Background()
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	p := mustPlan(t, l)[0]
	if ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now()); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	d := p.decision
	runGit(t, l.laneTarget(d.LaneID), "reset", "--hard", base)
	const placeholder = "[Pi's last assistant turn had no text or tool call.]"
	exited := func(id string) run.Record {
		rec := run.Record{RunID: id, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, EndCause: run.EndNormalExit, ExitCode: code(0),
			StartedAt: "2026-11-28T08:22:00Z", FinishedAt: "2026-11-28T08:22:05Z"}
		fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec
	}
	first := exited(d.RunRequest.RunID)
	if handled, err := l.settleNode(ctx, d, first, placeholder, base, &LoopResult{}, false); err != nil || !handled {
		t.Fatalf("first settle = (%v, %v)", handled, err)
	}
	restarted := pendingForTest(t, f, d.ItemID).Decision
	if !restarted.WorkflowRestart || !restarted.UnreadReportRestart || restarted.InfrastructureFrom != first.RunID {
		t.Fatalf("first unreadable ending was not restarted as the one unread-report restart: %+v", restarted)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error { return startNode(tx, restarted) })
	second := exited(restarted.RunRequest.RunID)
	if handled, err := l.settleNode(ctx, restarted, second, placeholder, base, &LoopResult{}, false); err != nil || !handled {
		t.Fatalf("second settle = (%v, %v)", handled, err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var detail string
		if err := r.QueryRow(`SELECT json_extract(payload,'$.detail') FROM events WHERE kind=? AND object_id=? AND json_extract(payload,'$.step')=?`, RefusalKind, second.RunID, RefusalReportMissing).Scan(&detail); err != nil {
			t.Fatalf("no report-missing refusal for %s: %v", second.RunID, err)
		}
		if !strings.Contains(detail, second.RunID) {
			t.Errorf("refusal detail %q does not name the run", detail)
		}
		var outcome string
		if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, second.RunID).Scan(&outcome); err != nil {
			return err
		}
		if outcome != "refused" {
			t.Errorf("node outcome = %q, want refused", outcome)
		}
		return nil
	})
	if after := pendingForTest(t, f, d.ItemID).Decision; after.RunRequest.RunID != second.RunID {
		t.Fatalf("a third run was dispatched: %s", after.RunRequest.RunID)
	}
}
