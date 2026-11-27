package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func TestItemWorktreesPlanOrdinaryLimitAndDeferral(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c")
	at := l.cfg.Now().UTC()
	lanes := []string{"lane-1", "lane-2"}

	// With ItemWorktrees off (default), pool lanes are assigned unchanged.
	off := planDecisionByItem(t, f, Pass{
		Now:            at.Format(time.RFC3339Nano),
		AvailableLanes: &lanes,
		Build:          &l.build,
	}, l.cfg.Policy)
	if off["item-a"].Action != ActionDispatch || off["item-a"].LaneID != "lane-1" {
		t.Fatalf("off item-a = %+v, want dispatch on lane-1", off["item-a"])
	}
	if off["item-b"].Action != ActionDispatch || off["item-b"].LaneID != "lane-2" {
		t.Fatalf("off item-b = %+v, want dispatch on lane-2", off["item-b"])
	}

	// With ItemWorktrees on, ordinary dispatches receive their item id up to the
	// running-agent limit (2) and the third ready item is deferred on agent-limit.
	on := planDecisionByItem(t, f, Pass{
		Now:            at.Format(time.RFC3339Nano),
		AvailableLanes: &lanes,
		ItemWorktrees:  true,
		Build:          &l.build,
	}, l.cfg.Policy)
	for _, id := range []string{"item-a", "item-b"} {
		d := on[id]
		if d.Action != ActionDispatch || d.LaneID != id {
			t.Fatalf("on %s = %+v, want dispatch with lane %q", id, d, id)
		}
		if d.RunRequest == nil || d.RunRequest.LaneID != id {
			t.Fatalf("on %s RunRequest = %+v, want LaneID %q", id, d.RunRequest, id)
		}
	}
	c := on["item-c"]
	if c.Action != ActionHold || c.Reason != ReasonAgentLimit {
		t.Fatalf("on item-c = %+v, want hold/%s", c, ReasonAgentLimit)
	}
	if !strings.Contains(c.DeferredBy, on["item-a"].RunRequest.RunID) || !strings.Contains(c.DeferredBy, on["item-b"].RunRequest.RunID) {
		t.Fatalf("on item-c DeferredBy = %q, want both dispatched ordinary run ids", c.DeferredBy)
	}
}

func TestItemWorktreesPlanExpressAndDesk(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b", "item-c", "item-exp", "item-desk")
	at := l.cfg.Now().UTC()
	l.cfg.Policy.Seats = map[string]string{"design": "fixture"}
	comments := &settings.Comments{Paths: []string{"docs/design/"}, MaxRunning: 3}
	setItemExpress(t, f, "item-exp", true)
	setDesignFence(t, f, "item-desk", "docs/design/example.md")

	// No reserved express lane is listed; the two pool lanes supply only the limit of 2.
	lanes := []string{"lane-1", "lane-2"}
	got := planDecisionByItem(t, f, Pass{
		Now:            at.Format(time.RFC3339Nano),
		AvailableLanes: &lanes,
		ItemWorktrees:  true,
		Comments:       comments,
		Build:          &l.build,
	}, l.cfg.Policy)

	// Express item runs in its own item directory without consuming an ordinary slot.
	exp := got["item-exp"]
	if exp.Action != ActionDispatch || exp.LaneID != "item-exp" || exp.RunRequest == nil || exp.RunRequest.LaneID != "item-exp" {
		t.Fatalf("item-exp = %+v, want express dispatch in item-exp", exp)
	}
	// Desk item keeps its desk lane without consuming an ordinary slot.
	desk := got["item-desk"]
	if desk.Action != ActionDispatch || desk.LaneID != "desk-example" || desk.DeskDocument != "docs/design/example.md" {
		t.Fatalf("item-desk = %+v, want desk dispatch on desk-example", desk)
	}
	// Ordinary items still fill the limit of 2, and the third is deferred on agent-limit.
	for _, id := range []string{"item-a", "item-b"} {
		d := got[id]
		if d.Action != ActionDispatch || d.LaneID != id {
			t.Fatalf("%s = %+v, want dispatch in %q", id, d, id)
		}
	}
	c := got["item-c"]
	if c.Action != ActionHold || c.Reason != ReasonAgentLimit {
		t.Fatalf("item-c = %+v, want hold/%s", c, ReasonAgentLimit)
	}

	// Now fill the ordinary limit with live leases on item-a and item-b, and confirm
	// that item-exp is still dispatched to its own item directory and item-desk keeps
	// its desk lane while the limit is already full.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, id := range []string{"item-a", "item-b"} {
			req := *got[id].RunRequest
			if _, err := run.ClaimLease(context.Background(), tx, req, id, at); err != nil {
				return err
			}
			if err := appendEffect(tx, dispatchStartedKind, req.RunID, &pendingWork{Decision: got[id]}, at); err != nil {
				return err
			}
		}
		return nil
	})
	full := planDecisionByItem(t, f, Pass{
		Now:            at.Format(time.RFC3339Nano),
		AvailableLanes: &lanes,
		ItemWorktrees:  true,
		Comments:       comments,
		Build:          &l.build,
	}, l.cfg.Policy)
	if full["item-a"].Action != ActionHold || full["item-a"].Reason != ReasonRunLive || full["item-a"].LaneID != "item-a" {
		t.Fatalf("live item-a = %+v, want hold/run-live keeping item-a", full["item-a"])
	}
	if full["item-b"].Action != ActionHold || full["item-b"].Reason != ReasonRunLive || full["item-b"].LaneID != "item-b" {
		t.Fatalf("live item-b = %+v, want hold/run-live keeping item-b", full["item-b"])
	}
	if full["item-c"].Action != ActionHold || full["item-c"].Reason != ReasonAgentLimit {
		t.Fatalf("full item-c = %+v, want hold/%s", full["item-c"], ReasonAgentLimit)
	}
	if full["item-exp"].Action != ActionDispatch || full["item-exp"].LaneID != "item-exp" {
		t.Fatalf("full item-exp = %+v, want express dispatch in item-exp while limit is full", full["item-exp"])
	}
	if full["item-desk"].Action != ActionDispatch || full["item-desk"].LaneID != "desk-example" {
		t.Fatalf("full item-desk = %+v, want desk dispatch on desk-example while limit is full", full["item-desk"])
	}
}

func TestItemWorktreesPlanKeepsExistingDirectory(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	at := l.cfg.Now().UTC()
	lanes := []string{"lane-1", "lane-2"}

	// item-a already holds a live lease on pool lane-1; it must keep lane-1
	// and never receive a second directory.
	reqA := run.Request{RunID: "run-item-a-a1", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture"}
	decA := Decision{ItemID: "item-a", LaneID: "lane-1", Action: ActionDispatch, Attempt: 1, RunRequest: &reqA}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := run.ClaimLease(context.Background(), tx, reqA, "lane-1", at); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, reqA.RunID, &pendingWork{Decision: decA}, at)
	})

	got := planDecisionByItem(t, f, Pass{
		Now:            at.Format(time.RFC3339Nano),
		AvailableLanes: &lanes,
		ItemWorktrees:  true,
		Build:          &l.build,
	}, l.cfg.Policy)
	if got["item-a"].Action != ActionHold || got["item-a"].Reason != ReasonRunLive || got["item-a"].LaneID != "lane-1" {
		t.Fatalf("item-a = %+v, want hold/run-live keeping lane-1", got["item-a"])
	}
	if got["item-b"].Action != ActionDispatch || got["item-b"].LaneID != "item-b" {
		t.Fatalf("item-b = %+v, want dispatch in item-b", got["item-b"])
	}
}

func TestItemWorktreesLoopSequentialNodesShareWorktreeWithSeparateScratch(t *testing.T) {
	f, l, ws := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.ItemWorktrees = true
	rec := &itemDirRecordingSteps{workflowSteps: ws, laneRoot: f.laneRoot}
	l.cfg.Steps = rec

	result := &LoopResult{}
	for i := 0; i < 6 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 || result.Rejected != 0 || result.InfraFailures != 0 {
		t.Fatalf("item-worktrees workflow did not land cleanly: %+v", result)
	}
	if len(ws.runs) != 2 {
		t.Fatalf("runs = %d, want 2 sequential nodes: %+v", len(ws.runs), ws.runs)
	}
	wantWT := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
	for i, lane := range rec.laneIDs {
		if lane != "item-a" {
			t.Fatalf("node %d lane id = %q, want item-a", i, lane)
		}
		if !rec.worktreeSeen[i] {
			t.Fatalf("node %d did not see shared worktree at %s", i, wantWT)
		}
	}
	if rec.scratchDirs[0] == rec.scratchDirs[1] {
		t.Fatalf("sequential nodes shared one scratch dir %q", rec.scratchDirs[0])
	}
	wantScratchPrefix := filepath.Join(f.laneRoot, "item-a", "scratch") + string(filepath.Separator)
	for i, dir := range rec.scratchDirs {
		if !strings.HasPrefix(dir, wantScratchPrefix) {
			t.Fatalf("node %d scratch dir = %q, want prefix %q", i, dir, wantScratchPrefix)
		}
		if _, err := os.Stat(filepath.Join(dir, "marker.txt")); err != nil {
			t.Fatalf("node %d scratch marker missing in %s: %v", i, dir, err)
		}
	}
}

type concurrentItemDirSteps struct {
	Steps
	laneRoot string

	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	bothStarted chan struct{}
	closedOnce  sync.Once
	laneByItem  map[string]string
	wtByItem    map[string]string
	scratchByID map[string]string
}

func (s *concurrentItemDirSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	if s.inFlight >= 2 {
		s.closedOnce.Do(func() { close(s.bothStarted) })
	}
	s.laneByItem[req.ItemID] = req.LaneID
	wt := filepath.Join(s.laneRoot, req.LaneID, WorktreeDir)
	s.wtByItem[req.ItemID] = wt
	scratch := run.DurableDir(filepath.Join(s.laneRoot, req.LaneID), req.RunID)
	s.scratchByID[req.ItemID] = scratch
	s.mu.Unlock()

	if _, err := os.Stat(wt); err != nil {
		return run.Record{}, "", err
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return run.Record{}, "", err
	}
	select {
	case <-s.bothStarted:
	case <-ctx.Done():
		return run.Record{}, "", ctx.Err()
	case <-time.After(10 * time.Second):
		return run.Record{}, "", context.DeadlineExceeded
	}
	defer func() {
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
	}()
	return run.Record{
		RunID:      req.RunID,
		ItemID:     req.ItemID,
		LaneID:     req.LaneID,
		Backend:    req.Backend,
		FinishedAt: time.Now().UTC().Format(time.RFC3339),
	}, "RESULT: done\n", nil
}

func TestItemWorktreesLoopTwoItemsRunConcurrentlyInSeparateDirectories(t *testing.T) {
	f, l, ws := workflowLoopFixture(t, "")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.ItemWorktrees = true
	steps := &concurrentItemDirSteps{
		Steps:       ws,
		laneRoot:    f.laneRoot,
		bothStarted: make(chan struct{}),
		laneByItem:  map[string]string{},
		wtByItem:    map[string]string{},
		scratchByID: map[string]string{},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	graphPass(t, l, result)

	if steps.maxInFlight < 2 {
		t.Fatalf("max concurrent items = %d, want 2 running at once", steps.maxInFlight)
	}
	if steps.laneByItem["item-a"] != "item-a" || steps.laneByItem["item-b"] != "item-b" {
		t.Fatalf("laneByItem = %v, want item-a->item-a and item-b->item-b", steps.laneByItem)
	}
	wantA := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
	wantB := filepath.Join(f.laneRoot, "item-b", WorktreeDir)
	if steps.wtByItem["item-a"] != wantA || steps.wtByItem["item-b"] != wantB || wantA == wantB {
		t.Fatalf("worktrees = %v, want distinct %s and %s", steps.wtByItem, wantA, wantB)
	}
	if steps.scratchByID["item-a"] == "" || steps.scratchByID["item-b"] == "" || steps.scratchByID["item-a"] == steps.scratchByID["item-b"] {
		t.Fatalf("scratch dirs = %v, want distinct non-empty item scratch dirs", steps.scratchByID)
	}
	if !strings.HasPrefix(steps.scratchByID["item-a"], filepath.Join(f.laneRoot, "item-a", "scratch")+string(filepath.Separator)) {
		t.Fatalf("item-a scratch = %q, want under item-a/scratch", steps.scratchByID["item-a"])
	}
	if !strings.HasPrefix(steps.scratchByID["item-b"], filepath.Join(f.laneRoot, "item-b", "scratch")+string(filepath.Separator)) {
		t.Fatalf("item-b scratch = %q, want under item-b/scratch", steps.scratchByID["item-b"])
	}
}
