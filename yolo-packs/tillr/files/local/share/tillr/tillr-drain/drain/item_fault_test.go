package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type faultSteps struct {
	recordingSteps
	fault string
}

func (s *faultSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
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
	for _, fault := range []string{"gate", "boundary", "candidate", "sync"} {
		t.Run(fault, func(t *testing.T) {
			f, l := passFixture(t, "item-a", "item-b")
			l.cfg.Steps = &faultSteps{fault: fault}
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]'`)
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
				writeForkFile(t, filepath.Join(path, "candidate.txt"), d.ItemID+"\n")
				runGit(t, path, "add", "candidate.txt")
				runGit(t, path, "commit", "-qm", "candidate")
				head := runGit(t, path, "rev-parse", "HEAD")
				writeForkFile(t, filepath.Join(l.laneDirPath(d.LaneID), FenceFile), "candidate.txt\n")
				c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: d.RunRequest.RunID, Worktree: path, BaseSHA: base, HeadSHA: head}
				fixtureWrite(t, f, func(tx *store.Tx) error {
					rec := run.Record{RunID: c.RunID, ItemID: c.ItemID, LaneID: c.LaneID, Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), FinishedAt: l.cfg.Now().Format("2006-01-02T15:04:05Z07:00"), ArtifactRefs: []run.ArtifactRef{}}
					if err := applyTerminal(tx, rec); err != nil {
						return err
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
			result, err := l.Run(context.Background())
			if err != nil || result.Stop != StopQueueDrained || result.Landed != 1 {
				t.Fatalf("fault %s stopped siblings: %+v / %v", fault, result, err)
			}
			next := mustPlan(t, l)[0].decision
			if next.Action != ActionHold || !strings.Contains(next.Reason, map[string]string{"gate": "gate-harness-error", "boundary": "host-boundary-violation", "candidate": "candidate-record-mismatch", "sync": "definition-sync-failed"}[fault]) {
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
				return ClearItemFault(tx, "item-a", faults["item-a"].Seq, l.cfg.Now())
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
