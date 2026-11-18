package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaPausedPeerSurvivesRestartBeforeSettlement(t *testing.T) {
	f, l, steps, d, rec := quotaContinuationFixture(t, false, true)
	if err := l.recordBackendWindow(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO quota_pauses VALUES(?,?,?,?,'requested',?)`, rec.RunID, rec.ItemID, rec.LaneID, rec.Backend, steps.at.UnixMilli()); err != nil {
			return err
		}
		paused := rec
		paused.State, paused.ExitCode, paused.EndCause = run.StatePaused, nil, run.EndRestart
		return applyTerminal(tx, paused)
	})
	// No call to settle has occurred. The executor's paused finish, not settlement,
	// makes the row visible to the waiting face and the replacement loop.
	replacement := f.loop(t, steps, &itemContentStager{})
	replacement.cfg.Lanes = l.cfg.Lanes
	replacement.cfg.Policy = l.cfg.Policy
	next := mustPlan(t, replacement)[0].decision
	if next.Action != ActionHold || next.From != "running" || next.Reason != ReasonBackendWindow || next.RunRequest.RunID != d.RunRequest.RunID {
		t.Fatalf("replacement did not retain pause: %+v", next)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		err := r.QueryRow(`SELECT state FROM quota_pauses WHERE run_id=?`, rec.RunID).Scan(&state)
		if state != "waiting" {
			t.Errorf("waiting face cannot see state=%s", state)
		}
		return err
	})
	if len(steps.requests) != 1 || steps.gates != 0 {
		t.Fatalf("paused run dispatched or judged: requests=%d gates=%d", len(steps.requests), steps.gates)
	}
}
