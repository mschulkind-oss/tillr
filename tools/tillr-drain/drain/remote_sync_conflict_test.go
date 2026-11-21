package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func blockRemoteSyncRepair(t *testing.T, p *pushFixture, st remoteSyncState, report string) {
	t.Helper()
	req := &run.Request{RunID: "run-obstacle", ItemID: st.ItemID}
	if err := p.l.reportedBlock(context.Background(), Decision{ItemID: st.ItemID, RunRequest: req}, report); err != nil {
		t.Fatal(err)
	}
}

const remoteSyncOwnerReport = "OBSTACLE: owner\nQUESTION: which value wins?\nRESULT: blocked\n"

func TestRemoteSyncRetryPreservesObstacleEscalation(t *testing.T) {
	for _, tc := range []struct{ who, reason, report string }{
		{"owner", "owner", remoteSyncOwnerReport},
		{"operator", "tooling", "OBSTACLE: tooling\nCOMMAND: resolver\nEVIDENCE: cannot start\nPATHS: two.txt\nRESULT: blocked\n"},
	} {
		t.Run(tc.who, func(t *testing.T) {
			ctx := context.Background()
			p, st := fileDispatchableRemoteSyncRepair(t)
			blockRemoteSyncRepair(t, p, st, tc.report)
			checkRemoteSyncMover(t, p, st, tc.who, "escalated ("+tc.reason+")")
			conflict, err := p.l.fileOrUpdateRemoteSyncItem(ctx, st)
			if err != nil || conflict == nil {
				t.Fatalf("reattach to staged repair: %v, %v", conflict, err)
			}
			p.l.recordPush(ctx, settings.PushTip, p.local[2], conflict)
			checkRemoteSyncMover(t, p, st, tc.who, "escalated ("+tc.reason+")")
		})
	}
}

func TestRemoteSyncAlreadyPublishedGreenKeepsEscalation(t *testing.T) {
	for _, failedFetch := range []bool{false, true} {
		name := "already published"
		if failedFetch {
			name = "failed fetch then already published"
		}
		t.Run(name, func(t *testing.T) {
			p, st := fileDispatchableRemoteSyncRepair(t)
			ctx, root := context.Background(), p.f.root
			blockRemoteSyncRepair(t, p, st, remoteSyncOwnerReport)
			writeForkFile(t, filepath.Join(root, ".tillr.toml"), "[push]\nwhat = \"green\"\n")
			runGit(t, root, "update-ref", GreenRef, st.RemoteTip)
			push := func() {
				t.Helper()
				out, err := PushOnce(ctx, root, p.l.cfg.Store)
				if err == nil || out.OK || out.Kind != pushKindConflict || out.Mover != "owner" || !strings.Contains(out.Detail, "mover: owner; resolve "+st.ItemID) {
					t.Errorf("manual push = %+v, %v; want conflict with owner mover", out, err)
				}
				checkRemoteSyncMover(t, p, st, "owner", "escalated (owner)")
			}
			if failedFetch {
				runGit(t, root, "remote", "set-url", "origin", p.remote+"-unavailable")
				push()
				runGit(t, root, "remote", "set-url", "origin", p.remote)
			}
			push()
		})
	}
}

func TestRemoteSyncStaleSweepClearsConflictWithoutLanding(t *testing.T) {
	ctx := context.Background()
	p, st := fileDispatchableRemoteSyncRepair(t)
	blockRemoteSyncRepair(t, p, st, remoteSyncOwnerReport)
	checkRemoteSyncMover(t, p, st, "owner", "escalated (owner)")
	runGit(t, p.f.root, "reset", "--hard", st.RemoteTip)
	main := ownerCommit(t, p.f.root, "reconciled.txt")
	p.l.cfg.Lanes = nil
	for i := 0; i < 2; i++ {
		graphPass(t, p.l, &LoopResult{})
	}
	if state := itemStateOf(t, p.f.storePath, st.ItemID); state != "closed" {
		t.Errorf("manually reconciled item = %s; want closed", state)
	}
	if ev := lastPushEvent(t, p.f); ev["kind"] != "remote-sync-cleared" || ev["detail"] != "" {
		t.Errorf("idle stale sweep still reports the closed repair: %+v", ev)
	}
	if got := optionalCommit(ctx, p.f.root, remoteSyncPinRef(st.ItemID)); got != "" {
		t.Errorf("closed repair retained pin %s", got)
	}
	if got := runGit(t, p.f.root, "rev-parse", TrunkRef); got != main {
		t.Errorf("sweep moved main to %s", got)
	}
	if got := runGit(t, p.remote, "rev-parse", TrunkRef); got != st.RemoteTip {
		t.Errorf("sweep unexpectedly published %s", got)
	}
	fixtureRead(t, p.f, func(r *store.Reader) error {
		var pushEvents, landings int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND json_extract(payload,'$.action')='push'`, postLandingKind).Scan(&pushEvents); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM landings`).Scan(&landings); err != nil {
			return err
		}
		if pushEvents != 2 || landings != 0 {
			t.Errorf("got %d push events and %d landings; want one hold and one clearance only", pushEvents, landings)
		}
		return nil
	})
}
