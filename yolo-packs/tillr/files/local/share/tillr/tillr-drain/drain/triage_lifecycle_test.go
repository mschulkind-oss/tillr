package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestTriageSettledWithoutBlockEscalatesOncePerRequest(t *testing.T) {
	for _, footer := range []string{"done", "blocked"} {
		t.Run(footer, func(t *testing.T) {
			f, l := triageFixture(t)
			p := findTriagePlan(t, l, "item-request")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			ok, w, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			d := w.Decision
			target := l.laneTarget(d.LaneID)
			runGit(t, target, "reset", "--hard", base)
			writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), BaseSHAFile), base)
			rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format(time.RFC3339), FinishedAt: l.cfg.Now().Format(time.RFC3339), EndCause: run.EndNormalExit, ArtifactRefs: []run.ArtifactRef{}}
			fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
			report := "Duplicates a named item item-declared; already done.\nRESULT: " + footer + "\n"
			outcome := l.settle(context.Background(), d, rec, report, base, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, true)
			if outcome.err != nil || outcome.stop != "" {
				t.Fatalf("settle: %+v", outcome)
			}
			if err := l.releaseExamination(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				hold := findTriagePlan(t, l, d.ItemID)
				if hold.decision.Action != ActionHold || hold.decision.Reason != ReasonConflictOwner {
					t.Fatalf("second triage: %+v", hold.decision)
				}
				_, _, err := l.applyPlan(context.Background(), hold, base, l.cfg.Now())
				if err != nil {
					t.Fatal(err)
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				cs, err := item.ReadConflicts(tx.Reader())
				if err != nil {
					return err
				}
				if !strings.Contains(cs[d.ItemID].Text, report) {
					t.Errorf("report lost: %+v", cs[d.ItemID])
				}
				_, err = tx.Exec(`UPDATE items SET title='Add a changed request' WHERE item_id=?`, d.ItemID)
				return err
			})
			next := findTriagePlan(t, l, d.ItemID).decision
			if next.Action != ActionDispatch || next.Reason != ReasonTriageNoBlock || next.BlockConflict.TriageVersion == d.BlockConflict.TriageVersion {
				t.Fatalf("changed request not retriaged: %+v", next)
			}
		})
	}
}

func TestTriageMissingDocumentRecordsOwnerConflictWithoutRun(t *testing.T) {
	for _, ref := range []string{"", "request text", ".", "docs/", "../outside.md", "README.md#request", "other/request.md"} {
		t.Run(ref, func(t *testing.T) {
			f, l := triageFixture(t)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET body_ref=? WHERE item_id='item-request'`, ref)
				return err
			})
			p := findTriagePlan(t, l, "item-request")
			if p.decision.RunRequest != nil || p.decision.Reason != ReasonConflictOwner {
				t.Fatalf("no-document: %+v", p.decision)
			}
			_, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
			if err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				cs, err := item.ReadConflicts(tx.Reader())
				if cs["item-request"].WaitingOn != "owner" || cs["item-request"].RunID != "" {
					t.Errorf("no conflict: %+v", cs)
				}
				return err
			})
		})
	}
}

func TestTriageTerminalItemsNeverDispatch(t *testing.T) {
	for _, state := range []string{"closed", "landed", "resolved-elsewhere"} {
		t.Run(state, func(t *testing.T) {
			f, l := triageFixture(t)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state=? WHERE item_id='item-request'`, state)
				return err
			})
			d := findTriagePlan(t, l, "item-request").decision
			if d.RunRequest != nil || d.BlockConflict != nil {
				t.Fatalf("terminal triaged: %+v", d)
			}
		})
	}
}

func TestTriageRejectsEverySyncWarningBeforeGateThenBindsByID(t *testing.T) {
	f, l := triageFixture(t)
	p := findTriagePlan(t, l, "item-request")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	target := l.laneTarget(p.decision.LaneID)
	runGit(t, target, "reset", "--hard", base)
	valid := triageBlock + strings.Replace(strings.ReplaceAll(triageBlock, "item-declared", "item-request"), `prose="2"`, `prose="2" seat="build"`, 1)
	// All mechanically checked prose shapes and ordinary findings/warnings use the
	// same admission path, not a small allowlist of prose diagnostics.
	cases := []struct{ name, body, rule string }{
		{"unmarked", strings.ReplaceAll(valid, ` prose="2"`, ""), "prose-unmarked"},
		{"verb", strings.ReplaceAll(valid, "Add the requested feature", "The requested feature"), "prose-shape"},
		{"length", strings.ReplaceAll(valid, "Add the requested feature", "Add one two three four five six seven eight nine ten eleven twelve"), "prose-shape"},
		{"paragraph", strings.ReplaceAll(valid, "Readers need this feature to complete their work.\n", ""), "prose-shape"},
		{"criteria-count", valid, ""},
		{"finding", strings.ReplaceAll(valid, `paths="docs/"`, `unexpected="value"`), "unknown-attribute"},
	}
	many := valid
	for i := 2; i <= 9; i++ {
		many += fmt.Sprintf("\n- **c%d:** Another criterion.\n", i)
	}
	cases[4].body, cases[4].rule = many, "prose-shape"
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runGit(t, target, "reset", "--hard", base)
			writeForkFile(t, filepath.Join(target, "docs/request.md"), tc.body)
			runGit(t, target, "add", "docs")
			runGit(t, target, "commit", "-qm", "triage block")
			c := Candidate{ItemID: "item-request", RunID: p.decision.RunRequest.RunID, LaneID: p.decision.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD"), BlockConflict: p.decision.BlockConflict}
			detail, err := l.validateTriage(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(detail, tc.rule) {
				t.Fatalf("admitted %s: %s", tc.name, detail)
			}
		})
	}
	runGit(t, target, "reset", "--hard", base)
	// Legacy path attributes, even with old invalid values, do not affect admission.
	writeForkFile(t, filepath.Join(target, "docs/request.md"), strings.ReplaceAll(valid, `paths="docs/"`, `paths="../escape,missing/"`))
	runGit(t, target, "add", "docs")
	runGit(t, target, "commit", "-qm", "ready triage block")
	c := Candidate{ItemID: "item-request", RunID: p.decision.RunRequest.RunID, LaneID: p.decision.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD"), BlockConflict: p.decision.BlockConflict}
	if detail, err := l.validateTriage(context.Background(), c); err != nil || detail != "" {
		t.Fatalf("valid rejected: %s %v", detail, err)
	}
	// Model the gate-approved landing, then use the loop's real answer-and-sync path.
	runGit(t, f.root, "merge", "--ff-only", c.HeadSHA)
	if err := l.answerBlock(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		docs, err := item.ReadSyncDocuments(f.root)
		if err != nil {
			return err
		}
		report, err := item.Sync(context.Background(), tx.Reader(), nil, item.SyncInput{Root: f.root, Documents: docs})
		if err != nil {
			return err
		}
		for _, n := range append(report.Findings, report.Warnings...) {
			if n.ItemID == c.ItemID {
				t.Errorf("landed block: %+v", n)
			}
		}
		return nil
	})
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", "printf '%s' '{\"index\":{\"documents\":[]}}'"}}
	next := findTriagePlan(t, l, "item-request").decision
	if next.Action != ActionDispatch || next.Seat != "build" || next.BlockConflict != nil {
		t.Fatalf("did not return to build: %+v", next)
	}
}
