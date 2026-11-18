package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaPauseFinishAndContinuationClaimAreAtomic(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	req := Request{RunID: "run", ItemID: "item", LaneID: "lane", Backend: "alpha"}
	now := time.Now().UTC()
	old := "33b5f2d2-0cd4-4a49-b293-c58f5635561f"
	if err := claimLane(ctx, s, req, req.LaneID, old, now); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO quota_pauses VALUES('run','item','lane','alpha','requested',?)`, now.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rec := Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: StatePaused, EndCause: EndRestart, StartedAt: now.Format(time.RFC3339), FinishedAt: now.Format(time.RFC3339), ArtifactRefs: []ArtifactRef{}}
	if _, err := writeTerminal(ctx, s, rec, now, ""); err != nil {
		t.Fatal(err)
	}
	// A replacement drain/view can see this state without another settlement pass.
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var state string
		err := r.QueryRow(`SELECT state FROM quota_pauses WHERE run_id='run'`).Scan(&state)
		if state != "waiting" {
			t.Errorf("paused finish left state=%s", state)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	req.Resume = true
	req.FreshSession = true
	req.Backend = "beta"
	if err := claimLane(ctx, s, req, req.LaneID, "43b5f2d2-0cd4-4a49-b293-c58f5635561f", now); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var pauses, resumes int
		if err := r.QueryRow(`SELECT count(*) FROM quota_pauses`).Scan(&pauses); err != nil {
			return err
		}
		err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=?`, ResumeKind).Scan(&resumes)
		if pauses != 0 || resumes != 1 {
			t.Errorf("claim not atomic: pauses=%d resumes=%d", pauses, resumes)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaSessionRecoveryReadsTheClaimInsteadOfStaleLaneFile(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "store.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	lane := t.TempDir()
	old := "33b5f2d2-0cd4-4a49-b293-c58f5635561f"
	claimed := "43b5f2d2-0cd4-4a49-b293-c58f5635561f"
	if err := os.WriteFile(filepath.Join(lane, SessionIDFile), []byte(old+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req := Request{RunID: "run", ItemID: "item", LaneID: "lane", Backend: "beta", Resume: true, FreshSession: true}
	if err := claimLane(ctx, s, req, req.LaneID, claimed, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Simulate death after the store commit and before publication to session-id.
	req.FreshSession = false
	// Exercise the executor's session read, not a second, independent source of truth.
	got, err := continuationSessionID(ctx, s, req, lane)
	if err != nil {
		t.Fatal(err)
	}
	if got != claimed {
		t.Fatalf("recovered %s, want claimed %s", got, claimed)
	}
	req.Backend = "alpha"
	got, err = continuationSessionID(ctx, s, req, lane)
	if err != nil {
		t.Fatal(err)
	}
	if got != old {
		t.Fatalf("foreign backend claim replaced original: %s", got)
	}
}
