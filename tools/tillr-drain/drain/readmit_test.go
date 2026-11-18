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

// sameWork answers whether two decisions do the same thing to an item's work: the same
// action for the same reason, with nothing re-admitted.
func sameWork(a, b Decision) bool {
	return a.Action == b.Action && a.Reason == b.Reason && !a.Readmitted && !b.Readmitted
}

// The owner ruled on 2026-11-17 that nothing re-runs because a hash changed without
// knowing what the change means (audit finding F-01): an edit to the item's block, its
// fence, the backend policy or the tillr build does nothing to a held item's work. The
// operator's explicit retry still moves it.
func TestAChangedInputReDispatchesNothing(t *testing.T) {
	for _, change := range []string{"item-block", "allowed-paths", "backend-policy", "tillr-build"} {
		t.Run(change, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			first := mustPlan(t, l)[0]
			finishHeldPass(t, f, l, first, run.EndNormalExit)
			before := mustPlan(t, l)[0].decision
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
			after := mustPlan(t, l)[0].decision
			if !sameWork(before, after) || strings.Contains(after.Reason, "inputs-changed") {
				t.Fatalf("a changed %s did something to the work:\nbefore %+v\nafter  %+v", change, before, after)
			}
			// A replacement loop reads the same store and decides the same.
			l2 := f.loop(t, &recordingSteps{}, &itemContentStager{})
			l2.cfg.Policy = l.cfg.Policy
			l2.build = l.build
			if again := mustPlan(t, l2)[0].decision; !sameWork(after, again) {
				t.Fatalf("a replacement loop decided otherwise: %+v / %+v", after, again)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: first.decision.RunRequest.RunID, By: "owner", Why: "diagnosed", At: l.cfg.Now()})
				return err
			})
			if d := mustPlan(t, l2)[0].decision; d.Action != ActionRetry {
				t.Fatalf("owner retry refused: %+v", d)
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
			// An ordinary termination that committed nothing goes back to its worker with the
			// evidence, uncounted (the owner's ruling of 2026-11-17): not as an orphan, and
			// not as a re-admission.
			if !orphan && (d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Readmitted) {
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

func TestADefinitionProseEditReadmitsNothing(t *testing.T) {
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
	before := mustPlan(t, l)[0].decision
	write("# Item\nfirst body\n# Other\nchanged unrelated\n")
	if d := mustPlan(t, l)[0].decision; !sameWork(before, d) {
		t.Fatalf("an unrelated section's edit did something to the work: %+v", d)
	}
	write("# Item\nrevised body\n# Other\nchanged unrelated\n")
	if d := mustPlan(t, l)[0].decision; !sameWork(before, d) {
		t.Fatalf("a prose-only edit to the item's own body did something to its work: %+v / %+v", before, d)
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
			before := mustPlan(t, l)[0].decision
			write(strings.Replace(doc, "Second prose.", "Changed second prose.", 1))
			if d := mustPlan(t, l)[0].decision; !sameWork(before, d) {
				t.Fatalf("a sibling's edit did something to the work: %+v", d)
			}
			write(strings.Replace(doc, "First prose.", "Changed first prose.", 1))
			if d := mustPlan(t, l)[0].decision; !sameWork(before, d) {
				t.Fatalf("a prose-only edit to the item's own bullet did something to its work: %+v / %+v", before, d)
			}
		})
	}
}

func TestTerminationWithoutLoopStopEvidenceDoesNotReadmit(t *testing.T) {
	for _, cause := range []string{run.EndSignal, run.EndLeaseReclaimed} {
		f, l := passFixture(t, "item-a")
		finishHeldPass(t, f, l, mustPlan(t, l)[0], cause)
		// Without the loop's stop evidence the run is not an orphan: its no-op ending goes
		// back to its worker as any other does (the owner's ruling of 2026-11-17).
		if d := mustPlan(t, l)[0].decision; d.Action != ActionDispatch || d.Reason != ReasonReturned || d.Readmitted {
			t.Fatalf("termination alone re-admitted: %+v", d)
		}
	}
}

// The owner ruled on 2026-11-17 that a change to an item's block (a metadata write, a
// prose rewrite) is no reason to spend a design run. An item built on the design seat is
// not re-admitted on a changed input: it waits for an explicit retry.
func TestADesignSeatItemIsNotReadmittedOnAChangedInput(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	l.cfg.Policy.SeatLists = map[string][]string{"design": {"fixture"}}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat = 'design' WHERE item_id = 'item-a'`)
		return err
	})
	finishHeldPass(t, f, l, mustPlan(t, l)[0], run.EndNormalExit)
	before := mustPlan(t, l)[0].decision
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE item_criteria SET text = 'revised criterion' WHERE item_id = 'item-a'`)
		return err
	})
	if d := mustPlan(t, l)[0].decision; !sameWork(before, d) || d.Readmitted || strings.Contains(d.Reason, "inputs-changed") {
		t.Fatalf("a design-seat item was re-admitted on a changed input: %+v", d)
	}
}
