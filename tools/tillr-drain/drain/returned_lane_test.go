package drain

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestReturnedJudgeFindingOwnLaneAfterTrunkAdvances(t *testing.T) {
	for _, mode := range []string{"free", "earlier-item", "leased", "leased-only", "out", "retired", "reassigned"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a", "item-b")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
				return err
			})
			d := stageFinishedCandidateOnLane(t, f, l, "item-a", "lane-1")
			target, base := l.laneTarget(d.LaneID), l.laneBase(d.LaneID)
			head := runGit(t, target, "rev-parse", "HEAD")
			c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID, Worktree: target, BaseSHA: base, HeadSHA: head, EvidenceKey: "judge-finding"}
			fixtureWrite(t, f, func(tx *store.Tx) error { return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now()) })
			if err := l.holdJudge(ctx, c, "judge-run", ReasonEvalFailed, "Criterion c1: missing evidence", &LoopResult{}); err != nil {
				t.Fatal(err)
			}
			if err := l.markExamined(ctx, d); err != nil {
				t.Fatal(err)
			}
			tip := conflictTestCommit(t, f.root, "trunk-only.txt", "later trunk\n", "advance trunk")
			// Offer another lane first: ordering must not divert the returned work.
			l.cfg.Lanes = []string{"lane-2", "lane-1"}
			if err := l.stageLanes(ctx); err != nil {
				t.Fatal(err)
			}
			out := map[string]bool{}
			switch mode {
			case "earlier-item":
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE items SET state='in-queue',rank='0' WHERE item_id='item-b'`)
					return err
				})
			case "leased", "leased-only":
				if mode == "leased-only" {
					l.cfg.Lanes = []string{"lane-1"}
				}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE lanes SET state='leased',run_id=?,leased_until=? WHERE lane_id='lane-1'`, d.RunRequest.RunID, l.cfg.Now().Add(time.Hour).UnixMilli())
					return err
				})
			case "out":
				out["lane-1"] = true
			case "retired":
				l.cfg.Lanes = []string{"lane-2"}
			case "reassigned":
				runGit(t, target, "reset", "--hard", tip)
			}
			l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
			tick := Pass{Now: l.cfg.Now().Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes, OutLanes: out, Build: &l.build, BriefTemplate: &l.cfg.Stage.(*LaneStager).Template}
			fixtureRead(t, f, func(r *store.Reader) error {
				answer, err := Why(r, tick, l.cfg.Policy, &VantageQuestions{Root: f.root})
				if err != nil {
					return err
				}
				var row WhyRow
				for _, got := range answer.Rows {
					if mode == "earlier-item" && got.ItemID == "item-b" && got.Decision.LaneID != "lane-2" {
						t.Fatalf("earlier item took reserved lane: %+v", got)
					}
					if got.ItemID == "item-a" {
						row = got
					}
				}
				got := row.Decision
				switch mode {
				case "leased", "leased-only", "out":
					if got.Action != ActionHold || got.Reason != ReasonNoLane || got.LaneID != "lane-1" {
						t.Fatalf("hold: %+v", got)
					}
					if !reflect.DeepEqual(row.WaitingOn, []string{"lane-1"}) || !strings.Contains(row.Why, "holds the offered commit") {
						t.Fatalf("why: %+v", row)
					}
				case "retired", "reassigned":
					if got.Action != ActionDispatch || got.LaneID != "lane-2" || got.RetryCandidate != nil {
						t.Fatalf("free-lane fallback: %+v", got)
					}
				default:
					if got.Action != ActionDispatch || got.Reason != ReasonReturned || got.LaneID != "lane-1" || got.Returned == nil || got.Returned.WorkerSession != nil {
						t.Fatalf("own-lane dispatch: %+v", got)
					}
					if got.RetryCandidate == nil || !got.RetryCandidate.Available || got.RetryCandidate.HeadSHA != head || got.RetryCandidate.BaseSHA != base {
						t.Fatalf("restoration: %+v", got.RetryCandidate)
					}
				}
				return nil
			})
			if mode == "free" {
				p := mustPlan(t, l)[0]
				ok, work, err := l.applyPlan(ctx, p, tip, l.cfg.Now())
				if err != nil || !ok {
					t.Fatalf("apply: %v %v", ok, err)
				}
				staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, tip, &LoopResult{})
				if err != nil || len(staged) != 1 {
					t.Fatalf("stage: %+v %v", staged, err)
				}
				if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
					t.Fatalf("restaged head=%s want %s", got, head)
				}
				if got := l.settleBase("lane-1", tip); got != base {
					t.Fatalf("restaged base=%s want %s", got, base)
				}
			}
		})
	}
}
