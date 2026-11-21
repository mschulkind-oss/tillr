package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaFirstMessageRefusalHasNoContinuation(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "pending"}[pending], func(t *testing.T) {
			_, l, steps, d, rec := quotaContinuationFixture(t, true, false)
			var result LoopResult
			if pending {
				settleContinuation(t, l, &result)
			} else {
				ctx := context.Background()
				out := l.settle(ctx, d, rec, logTail(rec.ArtifactRefs), l.laneBase(d.LaneID), "tick", steps.at, &result, &PassTiming{}, nil, false)
				if out.err != nil || out.stop != "" {
					t.Fatalf("settle: %+v", out)
				}
				if err := l.markExamined(ctx, d); err != nil {
					t.Fatal(err)
				}
			}
			if len(steps.requests) != 1 {
				t.Fatalf("settlement started another run: %+v", steps.requests)
			}
			next := mustPlan(t, l)[0].decision
			if next.RunRequest == nil || next.RunRequest.Backend != "beta" || next.RunRequest.RunID == d.RunRequest.RunID || next.RunRequest.Resume || next.RunRequest.ResumePrompt != "" {
				t.Fatalf("quota refusal did not plan a new run on beta: %+v", next)
			}
		})
	}
}

func TestQuotaRefusalPausesLivePeersOnceAndContinuesTheirFiles(t *testing.T) {
	f, l, steps, d, refusal := quotaContinuationFixture(t, true, true)
	base := l.laneBase(d.LaneID)
	dir := l.laneDirPath("lane-2")
	runGit(t, f.root, "worktree", "add", "-q", "-b", LaneBranch(f.laneRoot, "lane-2"), filepath.Join(dir, WorktreeDir), TrunkRef)
	for name, body := range map[string]string{BaseSHAFile: base + "\n", FenceFile: "one.txt\ntwo.txt\n", BriefFile: "finish peer attempt\n", SessionIDFile: "33b5f2d2-0cd4-4a49-b293-c58f5635561f\n"} {
		writeForkFile(t, filepath.Join(dir, name), body)
	}
	for _, path := range []string{"one.txt", "two.txt"} {
		writeForkFile(t, filepath.Join(dir, WorktreeDir, path), path+"\n")
	}
	peer := d
	peer.ItemID, peer.LaneID = "item-b", "lane-2"
	req := *d.RunRequest
	req.ItemID, req.LaneID, req.RunID = peer.ItemID, peer.LaneID, "run-peer"
	peer.RunRequest = &req
	db, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, db, peer.ItemID)
	db.Close()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, req, peer.LaneID, steps.at); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, req.RunID, pendingWork{Decision: peer, BaseSHA: base}, steps.at)
	})
	for i := 0; i < 2; i++ {
		if err := l.recordBackendWindow(context.Background(), refusal); err != nil {
			t.Fatal(err)
		}
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, run.PauseRequestKind, req.RunID).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("pause requests=%d", n)
		}
		var backend, state string
		err := r.QueryRow(`SELECT backend,state FROM quota_pauses WHERE run_id=?`, req.RunID).Scan(&backend, &state)
		if backend != "alpha" || state != "requested" {
			t.Errorf("pause=%s/%s", backend, state)
		}
		return err
	})
	paused := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StatePaused, EndCause: run.EndRestart, StartedAt: steps.at.Format("2006-01-02T15:04:05Z07:00"), FinishedAt: steps.at.Format("2006-01-02T15:04:05Z07:00")}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, paused) })
	var result LoopResult
	out := l.settle(context.Background(), peer, paused, "", base, "tick", steps.at, &result, &PassTiming{}, nil, false)
	if out.err != nil || out.stop != "" {
		t.Fatalf("settle: %+v", out)
	}
	next := steps.requests[len(steps.requests)-1]
	if next.Backend != "beta" || next.RunID != req.RunID || !next.Resume || !strings.Contains(next.ResumePrompt, "quota") {
		t.Fatalf("peer resumed as restart: %+v", next)
	}
	if steps.gates != 1 {
		t.Fatalf("peer gates=%d", steps.gates)
	}
}
