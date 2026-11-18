package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestReturnedWorkerGoneHarnessSession(t *testing.T) {
	f, l, _, base := returnedSessionFixture(t, RefusalGate)
	p := mustPlan(t, l)[0]
	p.decision.RunRequest.Command = []string{continuationShell(t), "-c", `if [ "$TILLR_SESSION_MODE" = resume ]; then echo 'session not found'; exit 1; fi
case "$(cat ../brief.md)" in *"fixture refusal"*"documents.log"*) ;; *) exit 72 ;; esac
printf 'second\n' > candidate.txt
git add candidate.txt
git commit -qm replacement
`}
	d := applyReturnedTurn(t, l, p, base)
	steps := &returnedLandingSteps{continuationSteps: &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}, root: f.root}
	l.cfg.Steps = steps
	rec, log, err := steps.Run(context.Background(), *d.RunRequest)
	if err != nil {
		t.Fatal(err)
	}
	outcome := l.settle(context.Background(), d, rec, log, base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("replacement: %+v", outcome)
	}
	if steps.gates != 1 || steps.lands != 1 {
		t.Fatalf("replacement not settled: gates=%d lands=%d", steps.gates, steps.lands)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.Events(0)
		if err != nil {
			return err
		}
		found := false
		for _, e := range events {
			if e.Kind != dispatchStartedKind {
				continue
			}
			var w pendingWork
			if err := json.Unmarshal(e.Payload, &w); err != nil {
				return err
			}
			if w.Decision.WorkerSessionReason != "resume-failed" {
				continue
			}
			found = true
			if w.Decision.RunRequest.SessionTurn || w.Decision.Returned == nil || w.Decision.Returned.Cause != RefusalGate {
				t.Errorf("fallback lost evidence: %+v", w)
			}
		}
		if !found {
			t.Error("replacement decision did not record resume-failed")
		}
		return nil
	})
}

func TestReturnedTurnWithoutNewCommitIsRefused(t *testing.T) {
	f, l, _, base := returnedSessionFixture(t, RefusalGate)
	p := mustPlan(t, l)[0]
	p.decision.RunRequest.Command = []string{continuationShell(t), "-c", "exit 0"}
	d := applyReturnedTurn(t, l, p, base)
	steps := &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()}
	l.cfg.Steps = steps
	rec, log, err := steps.Run(context.Background(), *d.RunRequest)
	if err != nil {
		t.Fatal(err)
	}
	outcome := l.settle(context.Background(), d, rec, log, base, "tick", l.cfg.Now(), &LoopResult{}, nil, nil, false)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle: %+v", outcome)
	}
	if steps.gates != 0 || steps.lands != 0 {
		t.Fatal("prior refused commit was judged as a new candidate")
	}
	s, err := l.retainedWorkerSession(context.Background(), d.RunRequest.RunID)
	if err != nil || s == nil {
		t.Fatalf("no-op lost continuation: %+v %v", s, err)
	}
}

func TestReturnedLaneIsReservedBeforeEarlierItem(t *testing.T) {
	f, l, _, _ := returnedSessionFixture(t, RefusalGate)
	db, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, db, "item-earlier")
	db.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET rank='0' WHERE item_id='item-earlier'`)
		return err
	})
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == "item-earlier" && d.Action != ActionHold {
			t.Fatalf("another item stole retained lane: %+v", d)
		}
		if d.ItemID == "item-a" && (d.Action != ActionDispatch || d.LaneID != "lane-1") {
			t.Fatalf("returned worker lost lane: %+v", d)
		}
	}
}

func TestReturnedRetirementRestoresWorkerHead(t *testing.T) {
	f, l, _, _ := returnedSessionFixture(t, RefusalGate)
	target := l.laneTarget("lane-1")
	head := runGit(t, target, "rev-parse", "HEAD")
	// Model the extra commit the loop makes when it retires an item block.
	writeForkFile(t, filepath.Join(target, "loop-owned.txt"), "retired\n")
	runGit(t, target, "add", "loop-owned.txt")
	runGit(t, target, "commit", "-qm", "retirement")
	prepared := runGit(t, target, "rev-parse", "HEAD")
	plans := mustPlan(t, l)
	previous := plans[0].decision.Returned.RunID
	if stop := l.abandon(context.Background(), "lane-1", previous, prepared); stop != "" {
		t.Fatal(stop)
	}
	if got := runGit(t, target, "rev-parse", "HEAD"); got != head {
		t.Fatalf("loop-owned change retained: %s", got)
	}
	if _, err := os.Stat(filepath.Join(target, "loop-owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("loop-owned file remains: %v", err)
	}
	if got := runGit(t, f.root, "show", "refs/tillr/runs/"+previous+":loop-owned.txt"); !strings.Contains(got, "retired") {
		t.Fatal(got)
	}
}
