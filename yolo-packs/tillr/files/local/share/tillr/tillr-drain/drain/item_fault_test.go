package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type faultSteps struct {
	recordingSteps
	fault string
}

func (s *faultSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	if c.ItemID == "item-a" && s.fault == "internal" {
		return GateOutcome{}, Internalf("gate could not bind its candidate")
	}
	if c.ItemID == "item-a" && s.fault == "gate" {
		// The gate STEP failed: the drain could not run the gate or record its answer.
		// That is the operator-held case; a gate that ran and answered "error" is re-gated
		// instead (gate_no_verdict_test.go).
		return GateOutcome{}, Externalf("the gate step could not start")
	}
	return s.recordingSteps.Gate(ctx, c)
}
func (s *faultSteps) Sync(context.Context) (SyncOutcome, error) {
	if s.fault == "sync" {
		return SyncOutcome{Findings: []string{"definition cannot be synced"}, ItemFaults: map[string]string{"item-a": "definition cannot be synced"}}, nil
	}
	return SyncOutcome{}, nil
}

// Every fault has a sibling that reaches a landing in the same loop, not merely
// another hold in a pure plan. Candidates are persisted before restart.
func TestItemFaultHoldsOnlyItsItem(t *testing.T) {
	for _, fault := range []string{"gate", "boundary", "candidate", "sync", "mirror", "internal", "delivery"} {
		t.Run(fault, func(t *testing.T) {
			f, l := passFixture(t, "item-a", "item-b")
			l.cfg.Steps = &faultSteps{fault: fault}
			candidatePath := "candidate.txt"
			if fault == "mirror" {
				candidatePath = "tools/tillr-drain/script"
				writePackFixture(t, f.root, ".justfile", "unrelated:\n    @true\n")
				writePackFixture(t, f.root, "yolo-packs/tillr/pack.json", "{}\n")
				runGit(t, f.root, "add", ".justfile", "yolo-packs")
				runGit(t, f.root, "commit", "-qm", "pack without renderer")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths=CASE WHEN item_id='item-a' THEN json_array(?) ELSE '["candidate.txt"]' END`, candidatePath)
				return err
			})
			plans := mustPlan(t, l)
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			for _, p := range plans {
				d := p.decision
				ok, _, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
				if err != nil || !ok {
					t.Fatalf("dispatch %v/%v", ok, err)
				}
				path := l.lanePath(d.LaneID)
				if fault == "mirror" {
					runGit(t, path, "reset", "--hard", base)
				}
				changed := "candidate.txt"
				if d.ItemID == "item-a" {
					changed = candidatePath
				}
				writePackFixture(t, path, changed, d.ItemID+"\n")
				runGit(t, path, "add", changed)
				runGit(t, path, "commit", "-qm", "candidate")
				head := runGit(t, path, "rev-parse", "HEAD")
				writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), changed+"\n")
				c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: path, BaseSHA: base, HeadSHA: head}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					rec := run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}}
					if err := applyTerminal(tx, rec); err != nil {
						return err
					}
					if fault == "delivery" && c.ItemID == "item-a" {
						if err := appendEffectOn(tx, deliveryDecisionKind, "item", c.ItemID, deliveryDecision{Kind: "both", Fork: "pi-fork"}, l.cfg.Now()); err != nil {
							return err
						}
						call := landingCall{landingOffer: landingOffer{CallID: "early-offer", Commit: head, Base: base}, ItemID: c.ItemID, RunID: c.RunID}
						if err := appendLandingCallEvent(tx, callOffered, call.CallID, call, l.cfg.Now()); err != nil {
							return err
						}
					}
					return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
				})
				if d.ItemID == "item-a" {
					if fault == "candidate" {
						runGit(t, path, "commit", "--allow-empty", "-qm", "unrecorded head")
					}
					if fault == "boundary" {
						if err := l.recordBoundaryCheck(context.Background(), BoundaryScopeRun, "pass", []runRef{{c.RunID, c.ItemID, c.LaneID}}, []string{path}, []Violation{{Kind: "scope", Detail: "escaped.txt"}}, l.cfg.Now()); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if fault == "delivery" {
				result := runOnePass(t, l)
				if result.Landed != 1 || result.Rejected != 1 {
					t.Fatalf("delivery refusal stopped sibling: %+v", result)
				}
				next := mustPlan(t, l)[0].decision
				const order = "land fork pi-fork first"
				if !isDispatch(next) || next.Returned == nil || next.Returned.Cause != RefusalDeliveryOrder || !strings.Contains(next.Returned.Detail, order) {
					t.Fatalf("worker was not told to deliver the fork first: %+v", next)
				}
				fixtureRead(t, f, func(r *store.Reader) error {
					var answer string
					if err := r.QueryRow(`SELECT json_extract(payload,'$.text') FROM events WHERE kind=? AND object_id='early-offer'`, callAnswered).Scan(&answer); err != nil {
						return err
					}
					if !strings.Contains(answer, order) {
						t.Fatalf("landing offer was not answered with the order: %q", answer)
					}
					return nil
				})
				return
			}
			result, err := l.Run(context.Background())
			if err != nil || result.Stop != StopQueueDrained || result.Landed != 1 {
				t.Fatalf("fault %s stopped siblings: %+v / %v", fault, result, err)
			}
			next := mustPlan(t, l)[0].decision
			if next.Action != ActionHold || !strings.Contains(next.Reason, map[string]string{"gate": "gate-harness-error", "boundary": "host-boundary-violation", "candidate": "candidate-record-mismatch", "sync": "definition-sync-failed", "mirror": ReasonSettleFailed, "internal": ReasonSettleFailed}[fault]) {
				t.Fatalf("fault not held: %+v", next)
			}
			if fault == "candidate" {
				runGit(t, l.lanePath("lane-1"), "reset", "--hard", "HEAD^1")
			}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				faults, err := ReadItemFaults(tx.Reader())
				if err != nil {
					return err
				}
				f := faults["item-a"]
				if want := map[string]string{"mirror": "pack-sync", "internal": "gate could not bind its candidate"}[fault]; want != "" {
					if !strings.Contains(f.Detail, want) || !strings.Contains(f.CompleteMover(holdrules.For(f.Reason).Mover, l.cfg.Store), "clear-fault") {
						t.Fatalf("fault has no diagnostic or mover: %+v", f)
					}
				}
				return ClearItemFault(tx, "item-a", f.Seq, l.cfg.Now())
			})
			l.cfg.Steps = &faultSteps{}
			if _, err := l.refreshDocuments(context.Background(), &result); err != nil {
				t.Fatal(err)
			}
			if next := mustPlan(t, l)[0].decision; next.Action != ActionJudge {
				t.Fatalf("diagnosed fault did not release retained candidate: %+v", next)
			}
		})
	}
}

func TestExaminationCannotHoldWithoutStoreOrTrunk(t *testing.T) {
	for _, shared := range []string{"store", "trunk"} {
		t.Run(shared, func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			d := mustPlan(t, l)[0].decision
			if shared == "store" {
				l.cfg.Store = filepath.Join(t.TempDir(), "absent.sqlite3")
			} else {
				runGit(t, f.root, "update-ref", "-d", "refs/heads/"+TrunkRef)
			}
			err := l.holdExaminationFailure(context.Background(), d, &pendingWork{Decision: d}, ActionJudge, Internalf("bad candidate"))
			if err == nil || !strings.Contains(err.Error(), "reading "+shared) {
				t.Fatalf("unreadable %s did not escape candidate containment: %v", shared, err)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				if holds := mustFaults(t, r); len(holds) != 0 {
					t.Fatalf("shared failure recorded against the item: %+v", holds)
				}
				return nil
			})
		})
	}
}

func TestFailedStagingCanMoveToAnotherLane(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Stage = &brokenTemplateStager{template: "broken"}
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); stop != "" || err != nil {
		t.Fatalf("first pass %s/%v", stop, err)
	}
	l.cfg.Lanes = []string{"lane-2"}
	plans := mustPlan(t, l)
	if plans[0].decision.LaneID != "lane-2" {
		t.Fatalf("different lane not planned: %+v", plans[0].decision)
	}
	if stop, err := l.pass(context.Background(), &result); stop != "" || err != nil {
		t.Fatalf("staging collision stopped loop: %s/%v", stop, err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE action='dispatch'`).Scan(&n)
		if n != 0 {
			t.Errorf("failed staging retained %d dispatch records", n)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
