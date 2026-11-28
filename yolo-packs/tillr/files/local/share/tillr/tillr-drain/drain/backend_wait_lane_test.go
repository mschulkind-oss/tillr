package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestBackendWaitLane(t *testing.T) {
	for _, contents := range []string{"empty", "dirty", "commit", "output"} {
		t.Run(contents, func(t *testing.T) {
			ctx := context.Background()
			f, l, _ := workflowLoopFixture(t, "")
			if contents == "dirty" || contents == "commit" {
				// Exercise the retry path, not the read-only node's boundary refusal.
				workflowPath := filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json")
				writeForkFile(t, workflowPath, strings.Replace(workflowFixture, `"access":"read_only"`, `"access":"write"`, 1))
				runGit(t, f.root, "add", ".")
				runGit(t, f.root, "commit", "-qm", "writer workflow fixture")
			}
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			l.cfg.Policy.Backends["open"] = l.cfg.Policy.Backends["fixture"]
			plan := mustPlan(t, l)[0]
			if ok, _, err := l.applyPlan(ctx, plan, base, l.cfg.Now()); err != nil || !ok {
				t.Fatalf("claim first node: %v / %v", ok, err)
			}
			staged, err := l.stageDispatches(ctx, []Decision{plan.decision}, base, &LoopResult{})
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage first node: %v / %+v", err, staged)
			}
			d := staged[0]
			target := l.laneTarget(d.LaneID)
			switch contents {
			case "dirty", "commit":
				if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(target, "tools/item-a/partial.txt"), "work before refusal\n")
				if contents == "commit" {
					runGit(t, target, "add", ".")
					runGit(t, target, "commit", "-qm", "partial work")
				}
			case "output":
				writeForkFile(t, filepath.Join(nodeOutputsDir(f.root, d.RunRequest.RunID), "piece"), "staged output\n")
			}
			at := l.cfg.Now().UTC().Truncate(time.Second)
			reset := at.Add(time.Hour)
			report := filepath.Join(f.root, "quota-report.md")
			log := fmt.Sprintf("# run report\nrate limit kind: five-hour\nstatus: rejected\nfraction: 1\nreset: %d\n---\nquota exhausted\n", reset.Unix())
			writeForkFile(t, report, log)
			rec := run.Record{RunID: d.RunRequest.RunID, ItemID: d.ItemID, LaneID: d.LaneID, Backend: d.RunRequest.Backend,
				State: run.StateExited, ExitCode: code(2), StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
				ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}}
			fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
			out := l.settle(ctx, d, rec, log, base, "tick", at, &LoopResult{}, &PassTiming{}, nil, true)
			if out.err != nil || out.stop != "" {
				t.Fatalf("settle refused node: %+v", out)
			}

			db, err := store.Open(ctx, f.storePath, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"item-c", "item-d", "item-e"} {
				seedRefusalItem(t, db, id)
			}
			db.Close()
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1' WHERE item_id IN ('item-c','item-d','item-e');
					UPDATE items SET rank=substr(item_id,6,1) WHERE item_id!='item-b';
					UPDATE items SET rank='z' WHERE item_id='item-b'`)
				return err
			})
			l.cfg.Policy.Overrides = map[string]string{"item-b": "open"}

			plans := mustPlan(t, l)
			if len(plans) != 5 || plans[4].decision.ItemID != "item-b" {
				t.Fatalf("fixture should put the open item fifth: %+v", plans)
			}
			for i, p := range plans[:4] {
				wait := p.decision
				wantLane := ""
				if i == 0 && contents != "empty" {
					wantLane = d.LaneID
				}
				if wait.Action != ActionHold || wait.Reason != ReasonBackendWindow || wait.LaneID != wantLane {
					t.Errorf("backend hold = %+v, want lane %q", wait, wantLane)
				}
			}
			fifth := plans[4].decision
			if contents != "empty" {
				if fifth.Reason != ReasonAgentLimit {
					t.Errorf("item with work should keep its only lane: %+v", fifth)
				}
				return
			}
			if fifth.Action != ActionDispatch || fifth.LaneID != d.LaneID {
				t.Errorf("open backend did not get the freed lane: %+v", fifth)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				answer, err := Why(r, Pass{Now: at.Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, l.designerQuestions(ctx))
				if err != nil {
					return err
				}
				if answer.Summary.LanesBusy != 0 || len(answer.Rows) != 5 {
					t.Errorf("why should see one free lane and five items: %+v", answer)
				}
				for _, row := range answer.Rows {
					if row.ItemID == "item-b" {
						if row.Decision.Action != ActionDispatch {
							t.Errorf("why did not offer the free lane: %+v", row)
						}
					} else if row.Reason != ReasonBackendWindow || row.Decision.LaneID != "" || row.Decision.DeferredBy != rec.Backend ||
						!strings.Contains(row.Why, "five-hour quota refusal") || !strings.Contains(row.Mover, reset.Format(time.RFC3339)) {
						t.Errorf("why should report the closed backend and reset, not a lane wait: %+v", row)
					}
				}
				return nil
			})
			if t.Failed() {
				return
			}
			if ok, _, err := l.applyPlan(ctx, plans[4], base, at); err != nil || !ok {
				t.Fatalf("claim freed lane for open item: %v / %v", ok, err)
			}
			l.cfg.Lanes = append(l.cfg.Lanes, "lane-2")
			// While scout still maps to the closed backend, the released node waits for its reset.
			wait := mustPlan(t, l)[0].decision
			if wait.Reason != ReasonBackendWindow || wait.DeferredBy != rec.Backend || !strings.Contains(wait.BackendHoldMover, reset.Format(time.RFC3339)) {
				t.Fatalf("released node should wait for the original backend: %+v", wait)
			}
			// Reassigning scout to an open backend moves the waiting node immediately.
			l.cfg.Policy.Seats["scout"] = "open"
			moved := mustPlan(t, l)[0].decision
			if moved.Action != ActionDispatch || moved.LaneID != "lane-2" || moved.RunRequest == nil || moved.RunRequest.Backend != "open" ||
				moved.BackendMove == nil || moved.BackendMove.FromBackend != rec.Backend || moved.BackendMove.ToBackend != "open" {
				t.Fatalf("reassigned seat should move released node to open: %+v", moved)
			}
			// Restoring scout to the closed backend keeps waiting until reset, then offers lane-2.
			l.cfg.Policy.Seats["scout"] = rec.Backend
			l.cfg.Now = func() time.Time { return reset.Add(time.Second) }
			next := mustPlan(t, l)[0]
			got := next.decision
			if got.Action != ActionDispatch || got.LaneID != "lane-2" || got.RunRequest == nil || got.RunRequest.Backend != rec.Backend ||
				got.Attempt != d.Attempt || got.NodeIndex != d.NodeIndex || got.RunRequest.RunID == rec.RunID {
				t.Fatalf("after reset the same node should be offered another lane: %+v", got)
			}
			ok, work, err := l.applyPlan(ctx, next, base, l.cfg.Now())
			if err != nil || !ok || work == nil || work.Decision.RunRequest.Backend != rec.Backend {
				t.Fatalf("claim on another lane after reset: %v / %v / %+v", ok, err, work)
			}
			if staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, work.BaseSHA, &LoopResult{}); err != nil || len(staged) != 1 {
				t.Fatalf("fresh stage on another lane: %v / %+v", err, staged)
			}
		})
	}
}
