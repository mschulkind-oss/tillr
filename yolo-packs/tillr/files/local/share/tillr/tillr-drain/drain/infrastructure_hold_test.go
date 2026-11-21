package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRecoveredBackendReusesOnlyItsOwnResumableSession(t *testing.T) {
	f, l := passFixture(t, "item-a")
	adapterDir := t.TempDir()
	writeForkFile(t, filepath.Join(adapterDir, "pause-kind"), run.PauseAtGap+"\n")
	transcript := filepath.Join(adapterDir, "session.jsonl")
	writeForkFile(t, transcript, "{}\n")
	request := run.Request{RunID: "next", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
		Command: []string{filepath.Join(adapterDir, "run-fixture"), "--backend", "fixture", "--model", "test"}}
	prior := run.Record{RunID: "failed", ItemID: request.ItemID, LaneID: request.LaneID, Backend: request.Backend, Route: request.Route(),
		State: run.StateExited, ExitCode: code(4), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "transcript", Ref: transcript}}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, prior) })
	dir := RunDir(l.cfg.Root, prior.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const session = "503f6d2a-32b9-48b6-a09a-fa476150913e"
	writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
	cases := []struct {
		name, lane, backend string
		resume              bool
	}{
		{"same lane and route", "lane-1", "fixture", true},
		{"different lane", "lane-2", "fixture", false},
		{"different backend", "lane-1", "alternate", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := request
			req.LaneID, req.Backend = tc.lane, tc.backend
			d := Decision{RunRequest: &req, InfrastructureFrom: prior.RunID}
			var spec StageSpec
			if err := l.stageInfrastructureSession(context.Background(), &d, &spec); err != nil {
				t.Fatal(err)
			}
			if req.SessionTurn != tc.resume || (spec.SessionID == session) != tc.resume {
				t.Fatalf("session turn=%v, session ID=%q; want resume=%v", req.SessionTurn, spec.SessionID, tc.resume)
			}
		})
	}
}
