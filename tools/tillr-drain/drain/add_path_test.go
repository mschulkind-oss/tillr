package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAPathAdditionRetriesTheSameFenceHeldRequest(t *testing.T) {
	ctx := context.Background()
	f, l, _ := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed'`)
		return err
	})
	id := fileFixtureRequest(t, f, "build", item.SingleWorkerWorkflow, requestFence)
	const added = "extra.txt"
	var stagedFences []string
	steps := &obstacleRequestSteps{
		t: t, f: f,
		report: func(req run.Request) (string, *landingOffer) {
			fence, err := os.ReadFile(filepath.Join(f.laneRoot, req.LaneID, FenceFile))
			if err != nil {
				t.Error(err)
				return "", nil
			}
			stagedFences = append(stagedFences, string(fence))
			if len(stagedFences) == 1 {
				return wrapExtractedReport(req.RunID, "OBSTACLE: fence\nPATHS: "+added+"\nEVIDENCE: a reference in this file needs repair\nRESULT: blocked\n"), nil
			}
			target := filepath.Join(f.laneRoot, req.LaneID, WorktreeDir)
			if err := os.WriteFile(filepath.Join(target, added), []byte("repaired\n"), 0o644); err != nil {
				t.Error(err)
				return "", nil
			}
			runGit(t, target, "add", added)
			runGit(t, target, "commit", "-qm", "repair the reference")
			return wrapExtractedReport(req.RunID, "RESULT: done\n"), nil
		},
	}
	l.cfg.Steps = steps
	result := &LoopResult{}
	pass := func() {
		t.Helper()
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	for i := 0; i < 5 && len(steps.runs) == 0; i++ {
		pass()
	}
	pass()
	if len(steps.runs) != 1 {
		t.Fatalf("wanted one held attempt, got %v", steps.runs)
	}
	if d := planFor(t, l, id); d.Action != ActionHold || d.Reason != ReasonObstacleFence {
		t.Fatalf("expected fence hold, got %+v", d)
	}
	original := steps.runs[0]
	readOriginal := func() int64 {
		t.Helper()
		var seq int64
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT event_seq FROM runs WHERE run_id=? AND item_id=? AND state=?`, original, id, run.StateExited).Scan(&seq)
		})
		return seq
	}
	firstSeq := readOriginal()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.AddPath(tx, id, added, "operator", "repair the reference", l.cfg.Now().Add(time.Minute))
		return err
	})
	for i := 0; i < 5 && result.Landed == 0; i++ {
		pass()
	}
	if result.Landed != 1 || len(steps.runs) != 2 || steps.runs[1] == original {
		t.Fatalf("expected a new run to land after the path addition, got landed=%d, runs=%v, refusals=%v", result.Landed, steps.runs, result.RefusedSteps)
	}
	if len(stagedFences) != 2 || strings.Contains(stagedFences[0], added+"\n") || stagedFences[1] != stagedFences[0]+added+"\n" {
		t.Fatalf("retry should keep its old fence and add %s; got %q", added, stagedFences)
	}
	if got := readOriginal(); got != firstSeq {
		t.Fatalf("original run event sequence changed from %d to %d", firstSeq, got)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE item_id=? AND run_id IN (?,?)`, id, original, steps.runs[1]).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Fatalf("item %s should retain both attempts; found %d", id, count)
		}
		return nil
	})
}
