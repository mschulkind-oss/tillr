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
		case <-time.After(hangGuard):
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
	case <-time.After(hangGuard):
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

func leaseRunningFixtureItem(t *testing.T, f refusalLoopFixture, l *Loop, itemID, laneID string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		req := run.Request{RunID: "run-" + itemID + "-live", ItemID: itemID, LaneID: laneID, Backend: "fixture"}
		if _, err := run.ClaimLease(context.Background(), tx, req, laneID, l.cfg.Now()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES(?,?,?,'fixture','running',1,0,'[]',0)`, req.RunID, itemID, laneID); err != nil {
			return err
		}
		owner, err := currentLoopOwner()
		if err != nil {
			return err
		}
		d := Decision{DecisionID: "dec-" + req.RunID, ItemID: itemID, Action: ActionDispatch, Reason: ReasonReady, From: "queued", To: "running", LaneID: laneID, Seat: "build", RunRequest: &req}
		return appendEffect(tx, dispatchStartedKind, req.RunID, &pendingWork{Decision: d, Owner: owner, BaseSHA: "base", LoopID: l.sessionID}, l.cfg.Now())
	})
}

func TestExpressReservedLaneQueueOrderAheadOfReturnedSession(t *testing.T) {
	f, l := designSeatFixture(t)
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"lane-1", "express-1", "express-2"}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Occupy lane-1 with ordinary item-a so express-1 and express-2 are the only free lanes.
	leaseRunningFixtureItem(t, f, l, "item-a", "lane-1")

	runningID := fileFixtureRequestWith(t, f, "Already running express request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/running.md"}, false)
	leaseRunningFixtureItem(t, f, l, runningID, "express-2")

	// Older express item further back in the queue ran on express-1 and returned with its session there.
	backID := fileFixtureRequestWith(t, f, "Older express request further back.\n", "build", item.SingleWorkerWorkflow, []string{"candidate.txt"}, false)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Place(context.Background(), tx, backID, item.Destination{To: "last"}, l.cfg.Now(), "owner", "cli")
		return err
	})
	var firstBack plannedApplication
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == backID {
			firstBack = p
		}
	}
	if firstBack.decision.Action != ActionDispatch || firstBack.decision.LaneID != "express-1" {
		t.Fatalf("initial back express dispatch = %+v, want express-1", firstBack.decision)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	dir := l.laneDirPath("express-1")
	writeForkFile(t, filepath.Join(dir, SessionIDFile), "11111111-1111-4111-8111-111111111111\n")
	writeForkFile(t, filepath.Join(dir, BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(dir, FenceFile), "candidate.txt\n")
	target := l.laneTarget("express-1")
	writeForkFile(t, filepath.Join(target, "candidate.txt"), "first\n")
	runGit(t, target, "add", "candidate.txt")
	runGit(t, target, "commit", "-qm", "first")
	finishRefused(t, f, l, firstBack, RefusalGate, "fixture refusal", "documents", "dead anchor")
	if stop := l.abandon(context.Background(), "express-1", firstBack.decision.RunRequest.RunID, runGit(t, target, "rev-parse", "HEAD")); stop != "" {
		t.Fatal(stop)
	}

	// File a new express request at the front while express-1 is the one free reserved lane.
	frontID := fileFixtureRequestWith(t, f, "New express request at the front.\n", "build", item.SingleWorkerWorkflow, []string{"docs/front.md"}, false)

	decisions := map[string]Decision{}
	for _, p := range mustPlan(t, l) {
		decisions[p.decision.ItemID] = p.decision
	}
	if d := decisions[runningID]; d.Position != "running" || d.LaneID != "express-2" {
		t.Fatalf("already-running express item interrupted: %+v", d)
	}
	if d := decisions[frontID]; d.Action != ActionDispatch || d.LaneID != "express-1" {
		t.Fatalf("front express item = %+v, want dispatch on express-1", d)
	}
	if d := decisions[backID]; d.Action != ActionHold || d.Reason != ReasonNoLane {
		t.Fatalf("older express item = %+v, want hold/no-lane", d)
	}

	// Offering a free ordinary lane lets the older returned express item fall back without taking express-1.
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1", "express-2"}
	decisions = map[string]Decision{}
	for _, p := range mustPlan(t, l) {
		decisions[p.decision.ItemID] = p.decision
	}
	if d := decisions[frontID]; d.Action != ActionDispatch || d.LaneID != "express-1" {
		t.Fatalf("front express item with free ordinary lane = %+v, want express-1", d)
	}
	if d := decisions[backID]; d.Action != ActionDispatch || d.LaneID != "lane-2" || d.WorkerSessionReason != "lane-unavailable" {
		t.Fatalf("older express item fallback = %+v, want dispatch on lane-2 with lane-unavailable", d)
	}

	// When no express item precedes backID, backID still resumes its worker session on express-1.
	l.cfg.Lanes = []string{"lane-1", "express-1", "express-2"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id=?`, frontID)
		return err
	})
	decisions = map[string]Decision{}
	for _, p := range mustPlan(t, l) {
		decisions[p.decision.ItemID] = p.decision
	}
	if d := decisions[backID]; d.Action != ActionDispatch || d.LaneID != "express-1" || !d.RunRequest.SessionTurn || d.WorkerSessionReason != "resumed-worker" {
		t.Fatalf("frontmost returned express item did not resume on express-1: %+v", d)
	}
}

func TestExpressReservedLaneQueueOrderAheadOfDesignContinuation(t *testing.T) {
	f, l := designConflictFixture(t)
	l.cfg.Policy.Seats["build"] = "fixture"
	doc := filepath.Join(f.root, "briefs", "item-a.md")
	writeForkFile(t, doc, "# Doc\n\n### Build item a\n\n<!-- tillr: item id=\"item-a\" paths=\"tools/item-a/\" express=\"true\" -->\n- **c1:** the first criterion of item-a\n")
	runGit(t, f.root, "add", "briefs/item-a.md")
	runGit(t, f.root, "commit", "-qm", "item definition")
	declareWorkerResumeFixture(t, l)
	l.cfg.Lanes = []string{"express-1"}
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	backID := "item-a"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET express=1, rank='z' WHERE item_id=?`, backID)
		return err
	})
	firstDesign := mustPlan(t, l)[0]
	if firstDesign.decision.ItemID != backID || firstDesign.decision.Action != ActionDispatch || firstDesign.decision.LaneID != "express-1" {
		t.Fatalf("initial design dispatch = %+v, want item-a on express-1", firstDesign.decision)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(l.laneDirPath("express-1"), SessionIDFile), "22222222-2222-4222-8222-222222222222\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("express-1"), BaseSHAFile), base+"\n")
	target := l.laneTarget("express-1")
	writeForkFile(t, filepath.Join(target, "briefs", "item-a.md"), "design draft\n")
	runGit(t, target, "add", "briefs/item-a.md")
	runGit(t, target, "commit", "-qm", "design draft")
	head := runGit(t, target, "rev-parse", "HEAD")
	if err := l.pin(context.Background(), firstDesign.decision.RunRequest.RunID, head); err != nil {
		t.Fatal(err)
	}
	finishRefused(t, f, l, firstDesign, RefusalGate, "fixture refusal", "documents", "invalid heading")
	if stop := l.abandon(context.Background(), "express-1", firstDesign.decision.RunRequest.RunID, head); stop != "" {
		t.Fatal(stop)
	}
	if err := l.settleUnansweredDesignRuns(context.Background()); err != nil {
		t.Fatal(err)
	}

	l.cfg.Lanes = []string{"lane-1", "express-1", "express-2"}
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	ordinaryID := fileFixtureRequestWith(t, f, "Busy ordinary request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/ord.md"}, true)
	leaseRunningFixtureItem(t, f, l, ordinaryID, "lane-1")
	runningID := fileFixtureRequestWith(t, f, "Running express item.\n", "build", item.SingleWorkerWorkflow, []string{"docs/running.md"}, false)
	leaseRunningFixtureItem(t, f, l, runningID, "express-2")

	frontID := fileFixtureRequestWith(t, f, "Front express request.\n", "build", item.SingleWorkerWorkflow, []string{"docs/front.md"}, false)

	var frontPlan plannedApplication
	decisions := map[string]Decision{}
	for _, p := range mustPlan(t, l) {
		decisions[p.decision.ItemID] = p.decision
		if p.decision.ItemID == frontID {
			frontPlan = p
		}
	}
	if d := decisions[runningID]; d.Position != "running" || d.LaneID != "express-2" {
		t.Fatalf("already-running express item interrupted: %+v", d)
	}
	if d := decisions[frontID]; d.Action != ActionDispatch || d.LaneID != "express-1" {
		t.Fatalf("front express item = %+v, want dispatch on express-1", d)
	}
	if d := decisions[backID]; d.Action != ActionHold || d.Reason != ReasonNoLane {
		t.Fatalf("blocked back express item = %+v, want hold/no-lane", d)
	}
	ok, _, err := l.applyPlan(context.Background(), frontPlan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan for front express item on express-1 = (%v, %v), want (true, nil)", ok, err)
	}

	// With lane-2 free, the displaced design continuation can take lane-2 cleanly.
	l.cfg.Lanes = []string{"lane-1", "lane-2", "express-1", "express-2"}
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	var backPlan plannedApplication
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == backID {
			backPlan = p
		}
	}
	if backPlan.decision.Action != ActionDispatch || backPlan.decision.LaneID != "lane-2" {
		t.Fatalf("displaced design continuation = %+v, want dispatch on lane-2", backPlan.decision)
	}
	ok, _, err = l.applyPlan(context.Background(), backPlan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan for displaced design continuation on lane-2 = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestExpressReservedLaneStalePlanYieldsToFrontRequest(t *testing.T) {
	f, l := passFixture(t, "item-ordinary")
	l.cfg.Lanes = []string{"lane-1", "express-1", "express-2"}
	l.cfg.Policy.Seats = map[string]string{"build": "fixture"}
	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatal(err)
	}
	leaseRunningFixtureItem(t, f, l, "item-ordinary", "lane-1")
	runningID := fileFixtureRequestWith(t, f, "Already running express item.\n", "build", item.SingleWorkerWorkflow, []string{"docs/running.md"}, false)
	leaseRunningFixtureItem(t, f, l, runningID, "express-2")

	// Plan when only backID is waiting so backID is planned onto the one free reserved lane (express-1).
	backID := fileFixtureRequestWith(t, f, "Older express request at the back.\n", "build", item.SingleWorkerWorkflow, []string{"docs/back.md"}, false)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Place(context.Background(), tx, backID, item.Destination{To: "last"}, l.cfg.Now(), "owner", "cli")
		return err
	})
	var staleBack plannedApplication
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == backID {
			staleBack = p
		}
	}
	if staleBack.decision.Action != ActionDispatch || staleBack.decision.LaneID != "express-1" {
		t.Fatalf("initial back plan = %+v, want dispatch on express-1", staleBack.decision)
	}

	// Before staleBack is applied, a new express request arrives at the front of the queue.
	frontID := fileFixtureRequestWith(t, f, "Just-filed express request at the front.\n", "build", item.SingleWorkerWorkflow, []string{"docs/front.md"}, false)
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	ok, _, err := l.applyPlan(context.Background(), staleBack, base, l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("stale back express item claimed express-1 ahead of newly filed front express item")
	}

	// Re-planning gives express-1 to frontID, leaves runningID running on express-2, and holds backID.
	var frontPlan plannedApplication
	decisions := map[string]Decision{}
	for _, p := range mustPlan(t, l) {
		decisions[p.decision.ItemID] = p.decision
		if p.decision.ItemID == frontID {
			frontPlan = p
		}
	}
	if d := decisions[runningID]; d.Position != "running" || d.LaneID != "express-2" {
		t.Fatalf("already-running express item interrupted: %+v", d)
	}
	if d := decisions[frontID]; d.Action != ActionDispatch || d.LaneID != "express-1" {
		t.Fatalf("front express item = %+v, want dispatch on express-1", d)
	}
	if d := decisions[backID]; d.Action != ActionHold || d.Reason != ReasonNoLane {
		t.Fatalf("back express item = %+v, want hold/no-lane", d)
	}
	ok, _, err = l.applyPlan(context.Background(), frontPlan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("applyPlan for front express item = (%v, %v), want (true, nil)", ok, err)
	}
}
