package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func finishHeldPass(t *testing.T, f refusalLoopFixture, l *Loop, p plannedApplication, cause string) {
	t.Helper()
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("apply: %v %v", ok, err)
	}
	d := p.decision
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: cause}); err != nil {
			return err
		}
		if err := appendEffect(tx, examinationDoneKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now()); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state = 'idle', run_id = NULL, leased_until = NULL WHERE lane_id = ?`, d.LaneID)
		return err
	})
	if err := l.refuse(context.Background(), Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID}, &LoopResult{}, RefusalNoOp); err != nil {
		t.Fatal(err)
	}
}

func TestHeldPassReadmitsOnlyOnceForChangedInputs(t *testing.T) {
	for _, change := range []string{"unchanged", "item-block", "allowed-paths", "backend-policy", "tillr-build"} {
		t.Run(change, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			finishHeldPass(t, f, l, mustPlan(t, l)[0], run.EndNormalExit)
			switch change {
			case "item-block":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE item_criteria SET text = 'revised criterion' WHERE item_id = 'item-a'`)
					return err
				})
			case "allowed-paths":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET allowed_paths = '["tools/changed/"]' WHERE item_id = 'item-a'`)
					return err
				})
			case "backend-policy":
				l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "changed"}
			case "tillr-build":
				l.build.Digest = "changed-build"
			}
			p := mustPlan(t, l)[0]
			if change == "unchanged" {
				if p.decision.Action != ActionHold {
					t.Fatalf("unchanged dispatched: %+v", p.decision)
				}
				return
			}
			if p.decision.Action != ActionDispatch || !strings.Contains(p.decision.Reason, change) {
				t.Fatalf("changed %s not named and dispatched: %+v", change, p.decision)
			}
			finishHeldPass(t, f, l, p, run.EndNormalExit)
			l.cfg.Policy.Backends["fixture"] = Backend{Adapter: "another-change"}
			// A replacement loop must read the spent allowance from the store.
			l2 := f.loop(t, &recordingSteps{}, &itemContentStager{})
			l2.cfg.Policy = l.cfg.Policy
			held := mustPlan(t, l2)[0].decision
			if held.Action != ActionHold || held.Reason != "readmission-exhausted" {
				t.Fatalf("second automatic attempt: %+v", held)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: p.decision.RunRequest.RunID, By: "owner", Why: "diagnosed", At: l.cfg.Now()})
				return err
			})
			ownerPlan := mustPlan(t, l2)[0]
			if d := ownerPlan.decision; d.Action != ActionRetry {
				t.Fatalf("owner retry refused: %+v", d)
			}
			finishHeldPass(t, f, l2, ownerPlan, run.EndNormalExit)
			if d := mustPlan(t, l2)[0].decision; d.Action != ActionHold {
				t.Fatalf("owner retry repeated unchanged inputs: %+v", d)
			}
			l2.cfg.Policy.Backends["fixture"] = Backend{Adapter: "changed after owner retry"}
			if d := mustPlan(t, l2)[0].decision; d.Action != ActionDispatch || !strings.Contains(d.Reason, "backend-policy") {
				t.Fatalf("owner did not reset allowance: %+v", d)
			}
		})
	}
}

func TestOrphanedNoCommitPassSpendsNoReadmission(t *testing.T) {
	for _, cause := range []string{run.EndSignal, run.EndLeaseReclaimed, run.EndTimeLimit, run.EndNormalExit} {
		t.Run(cause, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			finishHeldPass(t, f, l, mustPlan(t, l)[0], cause)
			if cause == run.EndSignal || cause == run.EndLeaseReclaimed {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return appendEffect(tx, loopOrphanedKind, "run-item-a-a1", struct{}{}, l.cfg.Now())
				})
			}
			d := mustPlan(t, l)[0].decision
			orphan := cause == run.EndSignal || cause == run.EndLeaseReclaimed
			if orphan && (d.Action != ActionDispatch || !strings.Contains(d.Reason, "orphaned by a loop stop")) {
				t.Fatalf("orphan not admitted: %+v", d)
			}
			if !orphan && d.Action != ActionHold {
				t.Fatalf("ordinary termination re-admitted: %+v", d)
			}
			if orphan {
				for attempt := 2; attempt <= 3; attempt++ {
					p := mustPlan(t, l)[0]
					if p.decision.Readmitted || p.decision.RetryID != "" {
						t.Fatalf("interruption charged an allowance: %+v", p.decision)
					}
					finishHeldPass(t, f, l, p, cause)
					fixtureWrite(t, f, func(tx *store.Tx) error {
						return appendEffect(tx, loopOrphanedKind, p.decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
					})
				}
				if next := mustPlan(t, l)[0].decision; next.Action != ActionDispatch || next.Attempt != 4 || next.Readmitted || next.RetryID != "" {
					t.Fatalf("repeated interruption spent a budget: %+v", next)
				}
			}
		})
	}
}

func TestDefinitionProseNotOtherSectionsReadmits(t *testing.T) {
	f, l := passFixture(t, "item-a")
	path := filepath.Join(f.root, "definition.md")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("# Item\nfirst body\n# Other\nunrelated\n")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref = 'definition.md#item' WHERE item_id = 'item-a'`)
		return err
	})
	finishHeldPass(t, f, l, mustPlan(t, l)[0], run.EndNormalExit)
	write("# Item\nfirst body\n# Other\nchanged unrelated\n")
	if d := mustPlan(t, l)[0].decision; d.Action != ActionHold {
		t.Fatalf("unrelated section dispatched: %+v", d)
	}
	write("# Item\nrevised body\n# Other\nchanged unrelated\n")
	if d := mustPlan(t, l)[0].decision; d.Action != ActionDispatch || !strings.Contains(d.Reason, "item-block") {
		t.Fatalf("body change ignored: %+v", d)
	}
}

func TestSnapshotApplicationRejectsAChangedDefinition(t *testing.T) {
	f, l := passFixture(t, "item-a")
	p := mustPlan(t, l)[0]
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET text = 'changed after plan' WHERE item_id = 'item-a'`)
		return err
	})
	ok, _, err := l.applyPlan(context.Background(), p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || ok {
		t.Fatalf("stale definition applied: %v %v", ok, err)
	}
}

func TestStoppedLoopSettlementExaminesTheTreeBeforeFailover(t *testing.T) {
	for _, cause := range []string{run.EndSignal, run.EndLeaseReclaimed} {
		t.Run(cause, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			p := mustPlan(t, l)[0]
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
			if err != nil || !ok {
				t.Fatalf("apply: %v %v", ok, err)
			}
			d := p.decision
			rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(-1), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: cause}
			if cause == run.EndLeaseReclaimed {
				rec.State, rec.ExitCode = run.StateTimedOut, nil
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if err := applyTerminal(tx, rec); err != nil {
					return err
				}
				return appendEffect(tx, loopOrphanedKind, rec.RunID, struct{}{}, l.cfg.Now())
			})
			outcome := l.settle(context.Background(), d, rec, "", base, "tick", l.cfg.Now(), &LoopResult{}, &PassTiming{}, nil, true)
			if outcome.err != nil || outcome.stop != "" {
				t.Fatalf("settle: %+v", outcome)
			}
			if err := l.releaseExamination(context.Background(), d); err != nil {
				t.Fatal(err)
			}
			if next := mustPlan(t, l)[0].decision; next.Action != ActionDispatch || !strings.Contains(next.Reason, "orphaned by a loop stop") {
				t.Fatalf("orphan settlement did not admit: %+v", next)
			}
		})
	}
}

func TestSiblingRoadmapBulletDoesNotReadmitUnchangedItem(t *testing.T) {
	for _, ref := range []string{"roadmap.md#items", "roadmap.md"} {
		t.Run(ref, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			path := filepath.Join(f.root, "roadmap.md")
			doc := "# Items\n- **First item**\n  <!-- tillr: item id=\"item-a\" paths=\"tools/a/\" -->\n  First prose.\n  - **c1:** first criterion\n- **Second item**\n  <!-- tillr: item id=\"item-b\" paths=\"tools/b/\" -->\n  Second prose.\n  - **c1:** second criterion\n"
			write := func(text string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if ref == "roadmap.md" {
				doc = strings.TrimPrefix(doc, "# Items\n")
			}
			write(doc)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET body_ref = ? WHERE item_id = 'item-a'`, ref)
				return err
			})
			finishHeldPass(t, f, l, mustPlan(t, l)[0], run.EndNormalExit)
			write(strings.Replace(doc, "Second prose.", "Changed second prose.", 1))
			if d := mustPlan(t, l)[0].decision; d.Action != ActionHold {
				t.Fatalf("sibling edit re-admitted unchanged item: %+v", d)
			}
			write(strings.Replace(doc, "First prose.", "Changed first prose.", 1))
			if d := mustPlan(t, l)[0].decision; d.Action != ActionDispatch || !strings.Contains(d.Reason, "item-block") {
				t.Fatalf("own bullet edit ignored: %+v", d)
			}
		})
	}
}

func TestTerminationWithoutLoopStopEvidenceDoesNotReadmit(t *testing.T) {
	for _, cause := range []string{run.EndSignal, run.EndLeaseReclaimed} {
		f, l := passFixture(t, "item-a")
		finishHeldPass(t, f, l, mustPlan(t, l)[0], cause)
		if d := mustPlan(t, l)[0].decision; d.Action != ActionHold {
			t.Fatalf("termination alone re-admitted: %+v", d)
		}
	}
}
