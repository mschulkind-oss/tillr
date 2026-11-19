package drain

import (
	"context"
	"strings"
	"testing"

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
	l.cfg.Policy.Preference = []string{"fixture", "alternate"}
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
	fixtureRead(t, f, func(r *store.Reader) error {
		var id string
		err := r.QueryRow(`SELECT run_id FROM node_runs WHERE node_id='scout2' AND outcome='passed'`).Scan(&id)
		if id != "run-item-a-a1-n2-failover" {
			t.Errorf("the unread reader did not run again alone: %q", id)
		}
		return err
	})
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
