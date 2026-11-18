package drain

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// noVerdictSteps answers "error" -- the gate ran and reached no verdict -- for item-a's
// first `errors` gates, then passes. It records every candidate it was asked to gate.
type noVerdictSteps struct {
	recordingSteps
	errors int
	mu     sync.Mutex
	gated  []Candidate
}

func (s *noVerdictSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	s.gated = append(s.gated, c)
	answer := GatePassed
	if c.ItemID == "item-a" && s.errors > 0 {
		s.errors--
		answer = GateErrored
	}
	s.mu.Unlock()
	return GateOutcome{Outcome: answer}, nil
}

func (s *noVerdictSteps) gatesOf(itemID string) []Candidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Candidate
	for _, c := range s.gated {
		if c.ItemID == itemID {
			out = append(out, c)
		}
	}
	return out
}

// A gate that answers "error" reached no verdict, so nothing about the candidate was
// judged. The loop gates the same candidate again on its own -- same commit, same lane --
// with no operator, no retry, no acknowledgment and no count, and the item never sits
// in a hold whose mover is a person. Run on 2026-11-18: item-gate-pinned-fork-inputs sat
// an hour in `gate-harness-error` with mover "operator" after one such answer.
func TestGateNoVerdictRegatesTheSameCandidate(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	steps := &noVerdictSteps{errors: 3}
	l.cfg.Steps = steps
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["candidate.txt"]'`)
		return err
	})
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	for _, p := range mustPlan(t, l) {
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
	}
	result, err := l.Run(context.Background())
	if err != nil || result.Stop != StopQueueDrained || result.Landed != 2 {
		t.Fatalf("a no-verdict gate did not end in a landing on its own: %+v / %v", result, err)
	}
	gates := steps.gatesOf("item-a")
	if len(gates) != 4 {
		t.Fatalf("item-a was gated %d times, want 3 no-verdict answers and the judging fourth", len(gates))
	}
	for _, c := range gates[1:] {
		if c.RunID != gates[0].RunID || c.LaneID != gates[0].LaneID || c.Worktree != gates[0].Worktree {
			t.Fatalf("re-gate moved the candidate: first %+v, again %+v", gates[0], c)
		}
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if fault, held := faults["item-a"]; held {
			t.Errorf("a no-verdict gate held the item for an operator: %+v", fault)
		}
		var waits, retries int
		if err := r.QueryRow(`SELECT count(*) FROM dispatches WHERE item_id='item-a' AND action=? AND reason=?`, ActionHold, ReasonGateNoVerdict).Scan(&waits); err != nil {
			return err
		}
		if waits == 0 {
			t.Errorf("no %s hold was recorded while the re-gate was pending", ReasonGateNoVerdict)
		}
		if err := r.QueryRow(`SELECT count(*) FROM retries`).Scan(&retries); err != nil {
			return err
		}
		if retries != 0 {
			t.Errorf("a re-gate needed %d retry authorization(s)", retries)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// While the re-gate is pending, the hold names the loop as its mover, never a person.
func TestGateNoVerdictHoldNamesTheLoop(t *testing.T) {
	rule := holdrules.For(ReasonGateNoVerdict)
	if rule.Class != "waits on the loop" || rule.Where != "gate" {
		t.Fatalf("%s is shown as %+v, want the loop's own wait at the gate", ReasonGateNoVerdict, rule)
	}
}
