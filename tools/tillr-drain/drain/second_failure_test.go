package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSecondFailureRoutesToDesign(t *testing.T) {
	for _, mode := range []string{"readmission", "retry", "stopped", "first-stopped", "landed", "no-reference", "blocked", "standalone", "repair"} {
		t.Run(mode, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				if mode == "no-reference" {
					_, err := tx.Exec(`UPDATE items SET body_ref = NULL WHERE item_id = 'item-a'`)
					return err
				}
				_, err := tx.Exec(`UPDATE items SET body_ref = 'docs/work.md#work' WHERE item_id = 'item-a'`)
				return err
			})
			first := mustPlan(t, l)[0]
			if mode == "standalone" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					d := first.decision
					if err := applyTerminal(tx, run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}, EndCause: run.EndNormalExit}); err != nil {
						return err
					}
					return appendEffect(tx, GateVerdictKind, d.RunRequest.RunID, map[string]any{"result": "fail", "checks": []string{"standalone-check"}}, l.cfg.Now())
				})
			} else {
				finishHeldPass(t, f, l, first, run.EndNormalExit)
				if err := l.markExamined(context.Background(), first.decision); err != nil {
					t.Fatal(err)
				}
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				cs, err := item.ReadConflicts(tx.Reader())
				if cs["item-a"].ConflictID != "" {
					t.Fatal("first failure raised a conflict")
				}
				return err
			})
			if mode == "stopped" || mode == "first-stopped" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					return appendEffect(tx, loopOrphanedKind, first.decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
				})
			}
			if mode == "retry" || mode == "standalone" || mode == "repair" {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					ack := ""
					if mode == "standalone" {
						ack = RetryAckGateFailed
					}
					_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{ItemID: "item-a", RunID: first.decision.RunRequest.RunID, By: "owner", Why: "diagnosed", Acknowledge: ack, At: l.cfg.Now()})
					return err
				})
			} else {
				l.build.Digest = "changed"
			}
			second := mustPlan(t, l)[0]
			if second.decision.Action != ActionRetry && !second.decision.Readmitted {
				t.Fatalf("not readmitted: %+v", second.decision)
			}
			finishHeldPass(t, f, l, second, run.EndNormalExit)
			fixtureWrite(t, f, func(tx *store.Tx) error {
				for i, id := range []string{first.decision.RunRequest.RunID, second.decision.RunRequest.RunID} {
					path := filepath.Join(f.root, id+".report")
					if err := os.WriteFile(path, []byte("report for "+id+"\nRESULT: failed\n"), 0600); err != nil {
						return err
					}
					refs := mustJSON([]run.ArtifactRef{{Kind: "report", Ref: path}})
					if _, err := tx.Exec(`UPDATE runs SET artifact_refs = ? WHERE run_id = ?`, refs, id); err != nil {
						return err
					}
					if err := appendEffect(tx, GateVerdictKind, id, map[string]any{"result": "fail", "checks": []string{"failing-check-" + string(rune('1'+i))}}, l.cfg.Now()); err != nil {
						return err
					}
				}
				if mode == "stopped" {
					return appendEffect(tx, loopOrphanedKind, second.decision.RunRequest.RunID, struct{}{}, l.cfg.Now())
				}
				if mode == "landed" {
					_, err := tx.Exec(`UPDATE items SET state = 'landed' WHERE item_id = 'item-a'`)
					return err
				}
				return nil
			})
			completed := second.decision
			if mode == "blocked" {
				if err := l.reportedBlock(context.Background(), completed, "c1 requires a ruling\nRESULT: blocked\n"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "repair" {
				request := *completed.RunRequest
				request.RunID += ConflictFixSuffix
				completed.Action, completed.Reason, completed.Readmitted = ActionDispatch, ReasonConflictFix, false
				completed.RetryID, completed.SupersedesRunID, completed.RunRequest = "", "", &request
				completed.Attempt++
				completed.From, completed.To = "candidate", "running"
				identifyDecision(&completed)
				if err := l.handoffRepair(context.Background(), Candidate{ItemID: "item-a", LaneID: completed.LaneID, RunID: second.decision.RunRequest.RunID}, completed, "base"); err != nil {
					t.Fatal(err)
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					path := filepath.Join(f.root, "repair.report")
					if err := os.WriteFile(path, []byte("repair failed\nRESULT: failed\n"), 0600); err != nil {
						return err
					}
					if err := applyTerminal(tx, run.Record{RunID: request.RunID, ItemID: "item-a", LaneID: completed.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}, EndCause: run.EndNormalExit}); err != nil {
						return err
					}
					if err := appendEffect(tx, RefusalKind, request.RunID, map[string]any{"step": ReasonConflictExhausted}, l.cfg.Now()); err != nil {
						return err
					}
					_, err := tx.Exec(`UPDATE lanes SET state = 'idle', run_id = NULL WHERE lane_id = ?`, completed.LaneID)
					return err
				})
				// Replacement settlement reads the durable handoff, not local state.
				l = f.loop(t, &recordingSteps{}, &itemContentStager{})
				l.cfg.Policy.Seats = map[string]string{"build": "fixture", "design": "fixture"}
			}
			if err := l.markExamined(context.Background(), completed); err != nil {
				t.Fatal(err)
			}
			var c item.Conflict
			fixtureWrite(t, f, func(tx *store.Tx) error { cs, err := item.ReadConflicts(tx.Reader()); c = cs["item-a"]; return err })
			if mode == "stopped" || mode == "first-stopped" || mode == "landed" {
				if c.ConflictID != "" {
					t.Fatalf("nonfailure raised conflict: %+v", c)
				}
				return
			}
			if mode == "no-reference" {
				if c.WaitingOn != "owner" || c.Status != "owner" || !strings.Contains(c.Text, "No item block reference") {
					t.Fatalf("missing explicit owner hold: %+v", c)
				}
				if d := mustPlan(t, l)[0].decision; d.Action != ActionHold || d.Reason != ReasonConflictOwner {
					t.Fatalf("reference-less conflict dispatched: %+v", d)
				}
				return
			}
			if c.Status != "raised" || c.WaitingOn != "design" {
				t.Fatalf("missing design conflict: %+v", c)
			}
			// Replaying examination after a restart must not duplicate the conflict.
			if err := l.markExamined(context.Background(), completed); err != nil {
				t.Fatal(err)
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				var count int
				err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'item.conflict'`).Scan(&count)
				want := 1
				if mode == "blocked" {
					want = 2
				} // original finding plus enriched transition
				if count != want {
					t.Fatalf("duplicate conflicts: %d", count)
				}
				return err
			})
			for _, want := range []string{"report for " + first.decision.RunRequest.RunID, "report for " + second.decision.RunRequest.RunID, "failing-check-1", "failing-check-2"} {
				if !strings.Contains(c.Text, want) {
					t.Fatalf("missing %q: %+v", want, c)
				}
			}
			if mode == "repair" && (!strings.Contains(c.Text, "repair failed") || !strings.Contains(c.Text, ReasonConflictExhausted)) {
				t.Fatalf("lost repair evidence: %+v", c)
			}
			if mode == "blocked" && !strings.Contains(c.Text, "c1 requires a ruling") {
				t.Fatalf("lost blocked finding: %+v", c)
			}
			designer := mustPlan(t, l)[0]
			if designer.decision.Seat != "design" || designer.decision.BlockConflict == nil {
				t.Fatalf("not routed: %+v", designer.decision)
			}
			finishHeldPass(t, f, l, designer, run.EndNormalExit)
			for i := 0; i < 10; i++ {
				if d := mustPlan(t, l)[0].decision; d.Action != ActionHold {
					t.Fatalf("third run: %+v", d)
				}
			}
			// The existing answer path releases the original item, not a replacement id.
			fixtureWrite(t, f, func(tx *store.Tx) error {
				c.Status, c.WaitingOn, c.AnswerHead = "answered", "build", "changed-block-head"
				return item.RecordConflict(tx, c, l.cfg.Now())
			})
			answered := mustPlan(t, l)[0]
			if answered.decision.Action != ActionDispatch || answered.decision.Reason != ReasonConflictAnswered {
				t.Fatalf("answer not dispatched: %+v", answered.decision)
			}
			finishHeldPass(t, f, l, answered, run.EndNormalExit)
			if d := mustPlan(t, l)[0].decision; d.Action != ActionHold {
				t.Fatalf("answer dispatched again: %+v", d)
			}
		})
	}
}
