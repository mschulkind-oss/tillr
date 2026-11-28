package drain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

type failingRepairStager struct {
	passStager
	signals   string
	calls     int
	corrected bool
}

func (s *failingRepairStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	if spec.Returned != nil && spec.Returned.Cause == drain.RefusalConflict {
		s.calls++
		for _, name := range []string{"started-2", "started-3"} {
			if _, err := os.Stat(filepath.Join(s.signals, name)); err != nil {
				return fmt.Errorf("sibling not active: %w", err)
			}
		}
		if !s.corrected {
			if err := os.WriteFile(filepath.Join(s.signals, "release"), nil, 0600); err != nil {
				return err
			}
			return fmt.Errorf("conflict facts contain a literal brace pair")
		}
	}
	return s.passStager.Stage(ctx, spec)
}

func clearStageFailedFault(t *testing.T, storePath, itemID string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		faults, err := drain.ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		f, ok := faults[itemID]
		if !ok {
			return fmt.Errorf("item %s has no active fault", itemID)
		}
		if f.Reason != drain.ReasonStageFailed {
			return fmt.Errorf("item %s fault reason = %q, want %q", itemID, f.Reason, drain.ReasonStageFailed)
		}
		return drain.ClearItemFault(tx, itemID, f.Seq, at.UTC())
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepairStagingFailureSurvivesRestartWithoutCancelingSiblings(t *testing.T) {
	signals := scratch(t, "repair-signals")
	t.Setenv("REPAIR_SIGNALS", signals)
	adapter := stub(t, "adapter", `lane=$(basename "$(dirname "$(pwd)")")
case "$lane" in
 lane-1) while [ ! -f "$REPAIR_SIGNALS/started-2" ] || [ ! -f "$REPAIR_SIGNALS/started-3" ]; do sleep 0.01; done ;;
 *) touch "$REPAIR_SIGNALS/started-${lane#lane-}"
    while [ ! -f "$REPAIR_SIGNALS/release" ]; do sleep 0.01; done
    file="sibling-${lane#lane-}.txt"
    echo candidate > "$file"
    git add "$file"
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
    printf 'RESULT: done\n'
    exit 0 ;;
esac
`+conflictAdapter)
	f := newLoopFixture(t, adapter)
	t.Cleanup(func() { cleanupStoreRuns(t, f.store) })
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"), itemRecord("item_042", "in-queue"), itemRecord("item_043", "in-queue"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	base := git(t, f.root, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(f.root, "base.txt"), "trunk\n")
	git(t, f.root, "add", "base.txt")
	git(t, f.root, "commit", "-qm", "moved trunk fixture")
	moved := git(t, f.root, "rev-parse", "HEAD")
	git(t, f.root, "reset", "--hard", base)
	// Keep the operator checkout on a separate branch: land moves main, not host files.
	git(t, f.root, "checkout", "-b", "operator")
	landFirst := stub(t, "land-conflict", fmt.Sprintf(`unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
if [ ! -f %q/land-once ]; then
 touch %q/land-once
 git -C %q update-ref refs/heads/main %q
 printf '%%s\n' '%s'
 exit 1
fi
printf '%%s\n' '%s'
`, signals, signals, f.root, moved, conflict, landingEnvelope(t, "landed", nil)))
	land := stub(t, "land-select", fmt.Sprintf(`case " $* " in
 *" --item item_041 "*) exec %q "$@" ;;
 *) exit 0 ;;
esac
`, landFirst))
	stage := &failingRepairStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt", "sibling-2.txt", "sibling-3.txt"}}, signals: signals}
	steps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	result, err := f.newLoopWith(t, 3, steps, stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatalf("repair failure canceled loop and siblings: %+v / %v", result, err)
	}
	if result.Landed != 2 || result.ConflictFixes != 0 || stage.calls < 1 {
		t.Fatalf("siblings did not finish or returned conflict run was not staged: %+v, calls=%d", result, stage.calls)
	}
	callsAfterHold := stage.calls
	for _, id := range []string{"item_042", "item_043"} {
		if state := itemStateOf(t, f.store, id); state != "landed" {
			t.Fatalf("%s: %s", id, state)
		}
	}
	var candidate drain.Candidate
	var cause string
	heldEvents := 0
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "drain.candidate" {
			var c drain.Candidate
			if err := json.Unmarshal(e.Payload, &c); err != nil {
				t.Fatal(err)
			}
			if c.ItemID == "item_041" {
				candidate = c
			}
		}
		if e.Kind == "drain.item-fault" && e.ObjectID != nil && *e.ObjectID == "item_041" {
			heldEvents++
			cause = string(e.Payload)
		}
	}
	if heldEvents != 1 || candidate.HeadSHA == "" || !strings.Contains(cause, drain.ReasonStageFailed) || !strings.Contains(cause, "literal brace pair") {
		t.Fatalf("candidate/failure lost: %+v / %s (heldEvents=%d)", candidate, cause, heldEvents)
	}
	if got := git(t, f.root, "rev-parse", "refs/tillr/runs/"+candidate.RunID); got != candidate.HeadSHA {
		t.Fatalf("candidate not retained: %s", got)
	}
	// Another landing advances trunk, but cannot clear a stage-failed hold on its own.
	newer := git(t, f.root, "commit-tree", moved+"^{tree}", "-p", moved, "-m", "later trunk fixture")
	git(t, f.root, "update-ref", "refs/heads/main", newer)
	for i := 0; i < 2; i++ {
		result, err = f.newLoopWith(t, 3, steps, stage, os.Stderr).Run(context.Background())
		if err != nil || result.Dispatched != 0 || stage.calls != callsAfterHold || !strings.Contains(result.Detail, drain.ReasonStageFailed) {
			t.Fatalf("restart repeated unchanged failure: %+v / %v calls=%d (want %d)", result, err, stage.calls, callsAfterHold)
		}
	}
	// Correct the staging cause and clear the stage-failed hold: item_041 recovers and lands.
	stage.corrected = true
	clearStageFailedFault(t, f.store, "item_041", steps.Now())
	result, err = f.newLoopWith(t, 3, steps, stage, os.Stderr).Run(context.Background())
	if err != nil || result.Landed != 1 || result.ConflictFixes != 0 || stage.calls != callsAfterHold+1 || itemStateOf(t, f.store, "item_041") != "landed" {
		t.Fatalf("corrected repair did not recover: %+v / %v calls=%d", result, err, stage.calls)
	}
	retained := 0
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "drain.item-fault" && e.ObjectID != nil && *e.ObjectID == "item_041" {
			retained++
			if string(e.Payload) != cause {
				t.Fatal("original failure evidence was changed")
			}
		}
	}
	if retained != 1 {
		t.Fatalf("unchanged failure was recorded %d times", retained)
	}
}
