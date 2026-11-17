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

type expressParkedSteps struct {
	recordingSteps
	started chan run.Request
	release chan struct{}
}

func (s *expressParkedSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.started <- req
	select {
	case <-s.release:
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	}
	return s.recordingSteps.Run(ctx, req)
}

func TestExpressDispatchNextPassWhileTwoOrdinaryRunsRemainActive(t *testing.T) {
	f, l := passFixture(t, "item-one", "item-two", "item-idea-saved")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1"}
	steps := &expressParkedSteps{started: make(chan run.Request, 3), release: make(chan struct{})}
	l.cfg.Steps = steps
	result := &LoopResult{}
	defer func() { close(steps.release); drainOwned(t, l, result) }()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='not-started' WHERE item_id='item-idea-saved'`)
		return err
	})
	if err := l.stageLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case req := <-steps.started:
			if !strings.HasPrefix(req.LaneID, "lane-") {
				t.Fatalf("ordinary on %s", req.LaneID)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary run not started")
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue' WHERE item_id='item-idea-saved'`)
		return err
	})
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	select {
	case req := <-steps.started:
		if req.ItemID != "item-idea-saved" || req.LaneID != "express-1" {
			t.Fatalf("wrong dispatch: %+v", req)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("express run not started on next pass")
	}
	if len(l.active) != 3 || result.Dispatched != 3 {
		t.Fatalf("ordinary runs displaced: active=%v result=%+v", l.active, result)
	}
}

func TestExpressAllocationWithOrdinaryLanesRunning(t *testing.T) {
	f, l := passFixture(t, "item-one", "item-two", "item-idea-abc123", "item-ordinary")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, id := range []string{"item-one", "item-two"} {
			lane := l.cfg.Lanes[i]
			req := run.Request{RunID: "run-" + id, ItemID: id, Backend: "fixture"}
			if _, err := run.ClaimLease(context.Background(), tx, req, lane, l.cfg.Now()); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,?,?,'fixture','running',1,0,'[]',0)`, req.RunID, id, lane); err != nil {
				return err
			}
		}
		return nil
	})
	plans := mustPlan(t, l)
	for _, p := range plans {
		d := p.decision
		if d.ItemID == "item-idea-abc123" && (d.Action != ActionDispatch || d.LaneID != "express-1") {
			t.Fatalf("idea did not bypass busy cap: %+v", d)
		}
		if d.ItemID == "item-ordinary" && d.Action != ActionHold {
			t.Fatalf("ordinary used express lane: %+v", d)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE state='running'`).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Fatalf("stopped ordinary runs: %d", n)
		}
		return nil
	})
}

func TestSavedIdeaExpressSurvivesWorkflowSeat(t *testing.T) {
	f, l := passFixture(t, "item-idea-abc123")
	l.cfg.Lanes = []string{"lane-1", "express-1"}
	l.cfg.Policy.Seats = map[string]string{"design": "fixture", "scout": "fixture", "build": "fixture"}
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), workflowFixture)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET workflow='scout-then-build@v1', seat='design' WHERE item_id='item-idea-abc123'`)
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-idea-abc123" {
			if p.decision.Action != ActionDispatch || p.decision.Seat != "scout" || p.decision.LaneID != "express-1" {
				t.Fatalf("workflow changed eligibility: %+v", p.decision)
			}
			return
		}
	}
	t.Fatal("idea missing")
}

func TestExpressUpgradeContinuesLegacyWorkflowInputs(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	body, err := item.DefinitionBody(f.root, "briefs/item-a.md", "item-a")
	if err != nil {
		t.Fatal(err)
	}
	legacyHash, err := inputHash([]any{"the item item-a", "briefs/item-a.md", "", "", "scout-then-build@v1", [][2]string{{"c1", "the first criterion of item-a"}}, body})
	if err != nil {
		t.Fatal(err)
	}
	result := &LoopResult{}
	graphPass(t, l, result)
	// Replay an unfinished attempt with exactly the pre-express input encoding.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work := works["item-a"]
		work.Decision.Inputs.ItemBlock = legacyHash
		return appendEffect(tx, graphChangedKind, work.Decision.RunRequest.RunID, work, l.cfg.Now())
	})
	next := replaceGraphLoop(t, f, l, steps)
	for i := 0; i < 8 && result.Landed == 0; i++ {
		graphPass(t, next, result)
	}
	if len(steps.runs) != 3 || result.Landed != 1 {
		t.Fatalf("legacy workflow invalidated: runs=%d result=%+v", len(steps.runs), result)
	}
}

func TestExpressPoolsRegisteredBeforeFirstDispatch(t *testing.T) {
	f, l := passFixture(t, "item-one")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1", "express-2"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "foreign", ItemID: "item-one", Backend: "fixture"}, "lane-1", l.cfg.Now())
		return err
	})
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var total, idle int
		if err := tx.QueryRow(`SELECT COUNT(*), SUM(state='idle') FROM lanes`).Scan(&total, &idle); err != nil {
			return err
		}
		if total != 4 || idle != 3 {
			t.Fatalf("unused pool missing or live lease overwritten: total=%d idle=%d", total, idle)
		}
		return nil
	})
}

func TestExpressDefinitionAndCommentsStaySeparate(t *testing.T) {
	f, l := passFixture(t, "item-urgent", "item-comments-doc-1")
	l.cfg.Lanes = []string{"lane-1", "express-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET express=1; UPDATE items SET design_doc='docs/comment.md' WHERE item_id='item-comments-doc-1'`)
		return err
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-urgent" && (p.decision.Action != ActionDispatch || p.decision.LaneID != "express-1") {
			t.Fatalf("explicit express not dispatched: %+v", p.decision)
		}
		if p.decision.ItemID == "item-comments-doc-1" && strings.HasPrefix(p.decision.LaneID, "express-") {
			t.Fatalf("comment moved to express: %+v", p.decision)
		}
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rows, err := readItems(tx.Reader())
		if err != nil {
			return err
		}
		for _, row := range rows {
			if itemIsComment(row.ItemID) && row.Express {
				t.Fatal("comment counted as express")
			}
		}
		return nil
	})
}

func TestExpressFallbackAndOrdinaryIsolation(t *testing.T) {
	for _, held := range [][]string{nil, {"express-1"}, {"lane-1"}, {"express-1", "lane-1"}} {
		p := planner{tick: Pass{AvailableLanes: &[]string{"lane-1", "express-1", "desk-doc"}}, held: map[string]bool{}, assigned: map[string]bool{}}
		for _, lane := range held {
			p.held[lane] = true
		}
		lane, ok := p.freeLane(true)
		want := "express-1"
		if p.held[want] {
			want = "lane-1"
		}
		if p.held[want] {
			want = ""
		}
		if ok != (want != "") || lane != want {
			t.Fatalf("held=%v: %s %t want %s", held, lane, ok, want)
		}
		lane, _ = p.freeLane()
		if strings.HasPrefix(lane, "express-") || strings.HasPrefix(lane, "desk-") {
			t.Fatalf("ordinary got %s", lane)
		}
	}
}
