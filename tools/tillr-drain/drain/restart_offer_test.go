package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRestartOffersAlreadyCommittedWork(t *testing.T) {
	for _, ending := range []string{"offer", "rebased", "inherited", "hold"} {
		t.Run(ending, func(t *testing.T) {
			f, l, _ := workflowLoopFixture(t, "")
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET state='closed'`)
				return err
			})
			workflow := `{"name":"scout-then-build","version":1,"stages":[{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[]}`
			if ending == "inherited" {
				workflow = `{"name":"scout-then-build","version":1,"stages":[{"id":"first","kind":"agent","seat":"build","access":"write"},{"id":"work","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"first","to_node":"work"}]}`
			}
			writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), workflow)
			runGit(t, f.root, "add", "config")
			runGit(t, f.root, "commit", "-qm", "restart workflow")
			id := fileFixtureRequest(t, f, "build", "scout-then-build@v1", []string{"candidate.txt"})
			result := &LoopResult{}
			var head string
			if ending == "inherited" {
				l.cfg.Steps = &obstacleRequestSteps{t: t, f: f, report: func(req run.Request) (string, *landingOffer) {
					head = conflictTestCommit(t, l.laneTarget(req.LaneID), "candidate.txt", "ready\n", "candidate from previous node")
					return "RESULT: done\n", nil
				}}
				graphPass(t, l, result)
				graphPass(t, l, result)
			}
			var p plannedApplication
			for _, plan := range mustPlan(t, l) {
				if plan.decision.ItemID == id {
					p = plan
				}
			}
			if p.decision.Action != ActionDispatch || p.decision.Workflow == nil {
				t.Fatalf("expected workflow dispatch, got %+v", p.decision)
			}
			deadOwnerClaim(t, f, l, p)
			work := pendingForTest(t, f, id)
			ctx := context.Background()
			staged, err := l.stageDispatches(ctx, []Decision{work.Decision}, work.BaseSHA, result)
			if err != nil || len(staged) != 1 {
				t.Fatalf("stage before restart: %v / %v", staged, err)
			}
			lane := staged[0].LaneID
			target := l.laneTarget(lane)
			base := l.settleBase(lane, "")
			if ending != "inherited" {
				head = conflictTestCommit(t, target, "candidate.txt", "ready\n", "candidate before restart")
			}
			if ending == "offer" || ending == "rebased" {
				tip := conflictTestCommit(t, f.root, "trunk.txt", "unrelated\n", "advance trunk before restart")
				if ending == "rebased" {
					head, err = l.moveLaneOntoTip(ctx, lane, head, tip)
					if err != nil || head == "" {
						t.Fatalf("rebase before restart: %s / %v", head, err)
					}
					base = tip
				}
			}
			steps := &obstacleRequestSteps{t: t, f: f, report: func(req run.Request) (string, *landingOffer) {
				if req.LaneID != lane || req.RunID != p.decision.RunRequest.RunID+"-restart" {
					t.Errorf("expected a restart on %s, got %+v", lane, req)
				}
				gotBase := l.laneFile(lane, BaseSHAFile)
				gotHead := runGit(t, target, "rev-parse", "HEAD")
				if gotBase != base || gotHead != head {
					t.Errorf("restart range %s..%s, want %s..%s", gotBase, gotHead, base, head)
				}
				if ending == "hold" || gotBase == gotHead {
					return "OBSTACLE: tooling\nCOMMAND: validate\nEVIDENCE: validator failed to start\nPATHS: validator\nRESULT: blocked\n", nil
				}
				return "RESULT: done\n", &landingOffer{CallID: "restart-offer", Commit: gotHead, Base: gotBase}
			}}
			next := replaceGraphLoop(t, f, l, steps)
			for i := 0; i < 3; i++ {
				graphPass(t, next, result)
			}
			if len(steps.runs) != 1 {
				t.Fatalf("expected one restarted run, got %v", steps.runs)
			}
			if ending != "hold" && result.Landed != 1 {
				t.Errorf("the resumed offer did not land: %+v", result)
			}
			if ending == "hold" {
				// Reuse the same lane while the first request is still held.
				other := fileFixtureRequestWith(t, f, "Independent change", "build", "scout-then-build@v1", []string{"other.txt"}, false)
				steps.report = func(req run.Request) (string, *landingOffer) {
					if req.ItemID != other || req.LaneID != lane {
						t.Errorf("expected %s to reuse %s, got %+v", other, lane, req)
					}
					return "OBSTACLE: tooling\nCOMMAND: validate\nEVIDENCE: validator failed to start\nPATHS: validator\nRESULT: blocked\n", nil
				}
				graphPass(t, next, result)
				if len(steps.runs) != 2 || runGit(t, target, "rev-parse", "HEAD") != base {
					t.Fatalf("lane was not reused on its clean base: %v", steps.runs)
				}
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				if ending == "hold" {
					saved, err := ReadPreservedWork(r)
					if err != nil {
						return err
					}
					for _, p := range saved {
						if p.ItemID == id && p.RunID == steps.runs[0] && !p.Pruned {
							if got := runGit(t, p.Repository, "rev-parse", p.Ref+"^1"); got != head {
								t.Fatalf("retained commit = %s, want %s", got, head)
							}
							return nil
						}
					}
					t.Fatalf("held work not retained on behalf of %s: %+v", id, saved)
				}
				var raw []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id='restart-offer'`, callOffered).Scan(&raw); err != nil {
					return err
				}
				var call landingCall
				if err := json.Unmarshal(raw, &call); err != nil {
					return err
				}
				if call.Base != base || call.Commit != head || call.RunID != steps.runs[0] {
					t.Fatalf("incorrect recorded offer: %+v", call)
				}
				if got := runGit(t, f.root, "rev-list", call.Base+".."+call.Commit); got != head {
					t.Fatalf("offer range contains %s, want only %s", got, head)
				}
				return nil
			})
		})
	}
}
