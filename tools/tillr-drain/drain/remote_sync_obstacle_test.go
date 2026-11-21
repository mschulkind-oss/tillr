package drain

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func checkRemoteSyncMover(t *testing.T, p *pushFixture, st remoteSyncState, who, detail string) {
	t.Helper()
	ev := lastPushEvent(t, p.f)
	text, _ := ev["detail"].(string)
	if ev["kind"] != pushKindConflict || ev["escalated_mover"] != who || ev["remote_tip"] != st.RemoteTip || !strings.Contains(text, detail) {
		t.Fatalf("push event = %+v, want %s escalation (%s)", ev, who, detail)
	}
}

func TestRemoteSyncObstacleEscalation(t *testing.T) {
	p, st := fileDispatchableRemoteSyncRepair(t)
	steps := &remoteSyncReplayProbe{Steps: &recordingSteps{}}
	p.l.cfg.Steps = steps
	req := run.Request{ItemID: st.ItemID, RunID: "run-trunk", Backend: "fixture"}
	out, handled := p.l.routeObstacle(context.Background(), Decision{ItemID: st.ItemID, RunRequest: &req}, req, "lane-1", "tick", st.BaseSHA,
		Obstacle{Kind: ObstacleTrunk, Fields: map[string]string{"CHECKS": "fixture-check", "BASE": st.BaseSHA}}, &LoopResult{})
	if !handled || out.err != nil || steps.called {
		t.Fatalf("trunk obstacle = %+v, handled=%v, replay=%v", out, handled, steps.called)
	}
	if r := refusalOf(t, p.f, req.RunID); r.Step != RefusalNoOp || !strings.Contains(r.Detail, "`trunk` is not an obstacle open to it") {
		t.Fatalf("trunk obstacle refusal = %+v", r)
	}
	req.RunID = "run-definition"
	if err := p.l.reportedBlock(context.Background(), Decision{ItemID: st.ItemID, RunRequest: &req},
		"OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: contradiction\nRESULT: blocked\n"); err != nil {
		t.Fatal(err)
	}
	checkRemoteSyncMover(t, p, st, "operator", "mover: operator; rebase main onto origin/main")
}

func TestRemoteSyncFenceSeatAndOwnerObstacleEscalation(t *testing.T) {
	p, st := fileDispatchableRemoteSyncRepair(t)
	ctx := context.Background()
	obstacle := func(id, report string, wantHandled bool, who string) {
		t.Helper()
		req := run.Request{ItemID: st.ItemID, RunID: id, Backend: "fixture"}
		d := Decision{ItemID: st.ItemID, RunRequest: &req, Seat: "build"}
		o := parseObstacle(report)
		if o.Problem != "" {
			t.Fatal(o.Problem)
		}
		out, handled := p.l.routeObstacle(ctx, d, req, "lane-1", "tick", st.BaseSHA, o, &LoopResult{})
		if handled != wantHandled || out.err != nil {
			t.Fatalf("%s route = %+v, handled=%v", id, out, handled)
		}
		if handled {
			return
		}
		if err := p.l.reportedBlock(ctx, d, report); err != nil {
			t.Fatal(err)
		}
		if c, ok := conflictOf(t, p.f, st.ItemID); !ok || c.RunID != id || c.WaitingOn != "owner" {
			t.Fatalf("%s conflict = %+v", id, c)
		}
		checkRemoteSyncMover(t, p, st, who, "escalated ("+o.Kind+")")
	}
	obstacle("run-fence", "OBSTACLE: fence\nPATHS: three.txt\nEVIDENCE: needed\nRESULT: blocked\n", true, "")
	obstacle("run-gate", "OBSTACLE: fence\nPATHS: tools/tillr-gate/main.go, four.txt\nEVIDENCE: gate\nRESULT: blocked\n", false, "owner")
	row, _, _ := filedRemoteSyncItem(t, p, st.ItemID)
	if !reflect.DeepEqual(row.Allowed, []string{"three.txt", "two.txt"}) {
		t.Fatalf("allowed paths = %v", row.Allowed)
	}
	obstacle("run-seat", "OBSTACLE: seat\nTRIED: x\nMISSING: stronger model\nRESULT: blocked\n", false, "operator")
	obstacle("run-owner", "OBSTACLE: owner\nQUESTION: which value wins?\nRESULT: blocked\n", false, "owner")
}

func checkRemoteSyncRepeat(t *testing.T, p *pushFixture, st remoteSyncState) {
	t.Helper()
	c, ok := conflictOf(t, p.f, st.ItemID)
	if !ok || c.WaitingOn != "owner" || c.BodyRef != "" || !strings.HasPrefix(c.Text, IdenticalRepeatPrefix) || !strings.Contains(c.Text, "reconcile main with the staged remote tip") || strings.Contains(c.Text, "supply one") || strings.Contains(c.Text, "amend the item's block") {
		t.Fatalf("repeat conflict = %+v, want operator reconciliation without a block", c)
	}
	checkRemoteSyncMover(t, p, st, "operator", "escalated (repeated ending)")
}

func TestRemoteSyncRepeatedEndingEscalatesWithoutBlock(t *testing.T) {
	p, st := fileDispatchableRemoteSyncRepair(t)
	p.l.cfg.Policy.Seats["design"] = "fixture"
	for i := 0; i < 2; i++ {
		d := decisionFor(t, p.l, st.ItemID)
		if d.decision.Action != ActionDispatch {
			t.Fatalf("repair attempt %d not dispatched: %+v", i+1, d.decision)
		}
		finishRefused(t, p.f, p.l, d, RefusalGate, "", "fixture-check", "same failure")
	}
	checkRemoteSyncRepeat(t, p, st)
}

// The worker exits normally without committing. Let the real pass dispatch, stage,
// settle and examine both attempts instead of planting refusal events.
type remoteSyncNoCandidateSteps struct {
	*recordingSteps
	f refusalLoopFixture
}

func (s *remoteSyncNoCandidateSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: countPtr(0), EndCause: run.EndNormalExit,
		StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{}}
	db, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, "", err
	}
	defer db.Close()
	err = db.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}

func TestRemoteSyncRepeatedEndingThroughPass(t *testing.T) {
	p, st := fileDispatchableRemoteSyncRepair(t)
	l := p.l
	l.cfg.Policy.Seats["design"] = "fixture"
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Stage = &LaneStager{Root: p.f.root, LaneRoot: p.f.laneRoot, Store: p.f.storePath, Now: l.cfg.Now,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md")}
	l.cfg.Steps = &remoteSyncNoCandidateSteps{recordingSteps: &recordingSteps{}, f: p.f}
	result := &LoopResult{}
	for i := 0; i < 8; i++ {
		graphPass(t, l, result)
		if _, ok := conflictOf(t, p.f, st.ItemID); ok {
			break
		}
	}
	checkRemoteSyncRepeat(t, p, st)
	graphPass(t, l, result)
	fixtureRead(t, p.f, func(r *store.Reader) error {
		prior, err := readPriorRuns(r)
		if err == nil && len(prior[st.ItemID]) != 2 {
			t.Errorf("repair ran %d times, want exactly two", len(prior[st.ItemID]))
		}
		return err
	})
}
