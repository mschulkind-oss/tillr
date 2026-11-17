package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// terminalSteps counts every judging step, so a test can prove none ran for an item
// that already reached a terminal state by another route. The hooks model a hand
// landing that commits while a step is in flight.
type terminalSteps struct {
	recordingSteps
	gates, lands, verdicts, states int
	gateOutcome                    string
	landOutcome                    LandOutcome
	duringGate, duringLand         func()
}

func (s *terminalSteps) Gate(context.Context, Candidate) (GateOutcome, error) {
	s.gates++
	if s.duringGate != nil {
		s.duringGate()
	}
	outcome := s.gateOutcome
	if outcome == "" {
		outcome = GatePassed
	}
	return GateOutcome{Outcome: outcome}, nil
}

func (s *terminalSteps) Land(context.Context, Candidate) (LandOutcome, error) {
	s.lands++
	if s.duringLand != nil {
		s.duringLand()
	}
	if s.landOutcome.Result == "" {
		return LandOutcome{Result: LandLanded}, nil
	}
	return s.landOutcome, nil
}

func (s *terminalSteps) Verdict(context.Context, Candidate, string) error { s.verdicts++; return nil }

func (s *terminalSteps) ItemState(context.Context, string, string, string, ...item.WaitingOn) error {
	s.states++
	return nil
}

// pendingTerminalFixture leaves what a killed loop leaves: one dispatched run on lane-1
// with its terminal record and a commit in the lane, and — when candidate is set — the
// recorded candidate the next pass would gate. report, when not empty, is the run's
// report text.
type pendingTerminalFixture struct {
	f     refusalLoopFixture
	runID string
	head  string
	base  string
}

func newPendingTerminalFixture(t *testing.T, candidate bool, report string) pendingTerminalFixture {
	t.Helper()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["base.txt"]'`)
		return err
	})
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, _, err := l.applyPlan(context.Background(), plan, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("dispatch: %v / %v", ok, err)
	}
	lane := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), FenceFile), "base.txt\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "candidate\n")
	runGit(t, lane, "add", "base.txt")
	runGit(t, lane, "commit", "-qm", "candidate")
	head := runGit(t, lane, "rev-parse", "HEAD")
	runID := plan.decision.RunRequest.RunID
	refs := []run.ArtifactRef{}
	if report != "" {
		path := filepath.Join(scratchUnderDurable(t), "report.md")
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, run.ArtifactRef{Kind: "report", Ref: path})
	}
	stamp := l.cfg.Now().Format(time.RFC3339)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{RunID: runID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: stamp, FinishedAt: stamp, ArtifactRefs: refs})
	})
	if candidate {
		c := Candidate{ItemID: "item-a", RunID: runID, LaneID: "lane-1", Worktree: lane, BaseSHA: base, HeadSHA: head, Request: plan.decision.RunRequest}
		if err := appendJudgment(context.Background(), f.storePath, candidateReadyKind, "run", runID, c, l.cfg.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return pendingTerminalFixture{f: f, runID: runID, head: head, base: base}
}

// finishByHand moves the item to a terminal state the way an operator does after landing
// or closing it outside the loop: a store write the loop did not make.
func finishByHand(t *testing.T, f refusalLoopFixture, state string) {
	t.Helper()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state=?, updated_at=updated_at+1 WHERE item_id='item-a'`, state)
		return err
	})
}

func (p pendingTerminalFixture) replacement(t *testing.T, steps Steps) *Loop {
	t.Helper()
	l := p.f.loop(t, steps, &itemContentStager{})
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	l.cfg.Lanes = []string{"lane-1"}
	return l
}

type storeSnapshot struct {
	seq      int64
	criteria string
}

func snapshotStore(t *testing.T, path string) storeSnapshot {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	var snap storeSnapshot
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&snap.seq); err != nil {
			return err
		}
		return r.QueryRow(`SELECT COALESCE(group_concat(criterion_id || '=' || state || '=' || text, ';'),'') FROM (SELECT * FROM item_criteria WHERE item_id='item-a' ORDER BY criterion_id)`).Scan(&snap.criteria)
	}); err != nil {
		t.Fatal(err)
	}
	return snap
}

type storedEvent struct {
	kind, object string
	payload      []byte
}

func eventsAfter(t *testing.T, path string, seq int64) []storedEvent {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	var out []storedEvent
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT kind, COALESCE(object_id,''), payload FROM events WHERE seq > ? ORDER BY seq`, seq)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e storedEvent
			if err := rows.Scan(&e.kind, &e.object, &e.payload); err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertSettledUnjudged is the whole contract for a run whose item was already terminal:
// one cheap, visible settlement, and nothing recorded against the item.
func assertSettledUnjudged(t *testing.T, p pendingTerminalFixture, l *Loop, result LoopResult, before storeSnapshot, state string, earlier ...string) {
	t.Helper()
	settled := 0
	for _, e := range eventsAfter(t, p.f.storePath, before.seq) {
		if slices.Contains(earlier, e.kind) {
			// Recorded while the item was still in the queue, before the hand landing.
			continue
		}
		switch e.kind {
		case itemFaultKind, item.ConflictKind, item.VerdictRecordKind, RefusalKind, candidateGatedKind, repairHeldKind, dirtyHeldKind, "land.landing", GateVerdictKind:
			t.Errorf("a run of a %s item was judged: %s %s %s", state, e.kind, e.object, e.payload)
		case item.UpdateKind:
			t.Errorf("the item's state was written: %s", e.payload)
		case itemTerminalSettledKind:
			settled++
			var body itemTerminalSettlement
			if err := json.Unmarshal(e.payload, &body); err != nil {
				t.Fatal(err)
			}
			if body.ItemID != "item-a" || body.RunID != p.runID || body.LaneID != "lane-1" || body.ItemState != state || !strings.Contains(body.Detail, "already "+state) {
				t.Errorf("settlement does not say why: %+v", body)
			}
		}
	}
	if settled != 1 {
		t.Errorf("recorded %d settlements of the run, want exactly 1", settled)
	}
	after := snapshotStore(t, p.f.storePath)
	if after.criteria != before.criteria {
		t.Errorf("criteria moved: %q -> %q", before.criteria, after.criteria)
	}
	if got := itemStateOf(t, p.f.storePath, "item-a"); got != state {
		t.Errorf("item state %q, want it left %q", got, state)
	}
	if result.Rejected != 0 || result.Landed != 0 || result.InfraFailures != 0 || result.ConflictFixes != 0 || result.Dispatched != 0 {
		t.Errorf("the settlement spent or judged: %+v", result)
	}
	// The lane is released and reset, and the never-judged head stays reachable.
	s := openFixture(t, p.f.storePath)
	var laneState string
	var laneRun *string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, run_id FROM lanes WHERE lane_id='lane-1'`).Scan(&laneState, &laneRun)
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if laneState == run.LaneStateLeased || laneRun != nil {
		t.Errorf("lane-1 not released: %s %v", laneState, laneRun)
	}
	lane := l.lanePath("lane-1")
	if head := runGit(t, lane, "rev-parse", "HEAD"); head != runGit(t, p.f.root, "rev-parse", TrunkRef) {
		t.Errorf("lane-1 not reset to the trunk: %s", head)
	}
	if dirty := runGit(t, lane, "status", "--porcelain"); dirty != "" {
		t.Errorf("lane-1 not clean: %s", dirty)
	}
	if pinned := runGit(t, p.f.root, "rev-parse", "refs/tillr/runs/"+p.runID); pinned != p.head {
		t.Errorf("unjudged head not pinned: %s, want %s", pinned, p.head)
	}
	// Settled once: the next plans neither examine the run again nor spend a retry or a
	// re-admission on the item, and a further pass writes nothing about it.
	for _, plan := range mustPlan(t, l) {
		d := plan.decision
		if d.ItemID == "item-a" && d.Action != ActionHold && d.Action != "" {
			t.Errorf("a %s item was planned again: %+v", state, d)
		}
	}
	mid := snapshotStore(t, p.f.storePath)
	var again LoopResult
	if stop, err := l.pass(context.Background(), &again); err != nil {
		t.Fatalf("second pass: %s / %v", stop, err)
	}
	drainOwned(t, l, &again)
	for _, e := range eventsAfter(t, p.f.storePath, mid.seq) {
		if e.object == p.runID || e.object == "item-a" {
			t.Errorf("a second pass wrote about the settled run: %s %s", e.kind, e.payload)
		}
	}
}

func runOnePass(t *testing.T, l *Loop) LoopResult {
	t.Helper()
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("pass: %q / %v", stop, err)
	}
	drainOwned(t, l, &result)
	return result
}

// The observed pass: a killed loop left candidates and a blocked report on lanes whose
// items were later landed or closed by hand. The next loop must settle each run without
// gating, landing, faulting or recording a conflict.
func TestARunWhoseItemIsAlreadyTerminalIsSettledWithoutJudging(t *testing.T) {
	for _, state := range []string{StateLanded, "closed", StateResolvedElsewhere} {
		for _, shape := range []string{"candidate", "blocked-report", "unsettled-commit"} {
			t.Run(state+"/"+shape, func(t *testing.T) {
				report := ""
				if shape == "blocked-report" {
					report = "# run report\n\nc1 cannot be met as written\n\nRESULT: blocked\n"
				}
				p := newPendingTerminalFixture(t, shape == "candidate", report)
				finishByHand(t, p.f, state)
				steps := &terminalSteps{}
				l := p.replacement(t, steps)
				before := snapshotStore(t, p.f.storePath)
				result := runOnePass(t, l)
				if steps.gates != 0 || steps.lands != 0 || steps.verdicts != 0 || steps.states != 0 {
					t.Fatalf("judged a %s item: %+v", state, steps)
				}
				assertSettledUnjudged(t, p, l, result, before, state)
			})
		}
	}
}

// The item lands by hand while its run is being examined. Each step that would record
// something against the item rereads its state first, so none of them does.
func TestAnItemLandedByHandDuringExaminationIsNotJudged(t *testing.T) {
	for _, during := range []string{"gate-pass", "gate-error", "land-already-on-trunk"} {
		t.Run(during, func(t *testing.T) {
			p := newPendingTerminalFixture(t, true, "")
			steps := &terminalSteps{}
			hand := func() { finishByHand(t, p.f, StateLanded) }
			switch during {
			case "gate-pass":
				steps.duringGate = hand
			case "gate-error":
				// The observed lane-6 shape: a gate that could not judge must not leave a
				// gate-harness-error fault on an item that is already landed.
				steps.duringGate, steps.gateOutcome = hand, GateErrored
			case "land-already-on-trunk":
				// The lander's own idempotency answers already-on-trunk for a change the
				// hand landing put there. The item must stay landed, not become
				// resolved-elsewhere, and no second verdict is recorded.
				steps.duringLand = hand
				steps.landOutcome = LandOutcome{Result: LandRefused, Reason: LandAlreadyOnTrunk}
			}
			l := p.replacement(t, steps)
			before := snapshotStore(t, p.f.storePath)
			result := runOnePass(t, l)
			wantLands := 0
			if during == "land-already-on-trunk" {
				wantLands = 1
			}
			if steps.gates != 1 || steps.lands != wantLands || steps.verdicts != 0 || steps.states != 0 {
				t.Fatalf("judged an item landed by hand: %+v", steps)
			}
			// The gate ran against the candidate before the item landed; that count is
			// the one piece of judging the race permits, and it records nothing.
			result.Candidates = 0
			var earlier []string
			if during == "land-already-on-trunk" {
				// The gate passed before the hand landing; its binding is not a judgment
				// made against a landed item.
				earlier = append(earlier, candidateGatedKind)
			}
			assertSettledUnjudged(t, p, l, result, before, StateLanded, earlier...)
		})
	}
}

// The lander moves the trunk to this run's change, but the item was closed by hand while
// it ran. The landing is counted, because the trunk moved; the item stays closed, with no
// verdict and no state write, and the settlement says the run's landing finished after.
func TestALandingThatFinishesAfterTheItemWasClosedLeavesTheItemAlone(t *testing.T) {
	p := newPendingTerminalFixture(t, true, "")
	steps := &terminalSteps{duringLand: func() { finishByHand(t, p.f, "closed") }}
	l := p.replacement(t, steps)
	before := snapshotStore(t, p.f.storePath)
	result := runOnePass(t, l)
	if steps.gates != 1 || steps.lands != 1 || steps.verdicts != 0 || steps.states != 0 || result.Landed != 1 {
		t.Fatalf("landing after a hand close: %+v / %+v", steps, result)
	}
	if got := itemStateOf(t, p.f.storePath, "item-a"); got != "closed" {
		t.Fatalf("item state %q, want closed", got)
	}
	settled := 0
	for _, e := range eventsAfter(t, p.f.storePath, before.seq) {
		switch e.kind {
		case item.UpdateKind, item.VerdictRecordKind, item.ConflictKind, itemFaultKind, RefusalKind:
			t.Errorf("recorded against a closed item: %s %s", e.kind, e.payload)
		case itemTerminalSettledKind:
			settled++
			var body itemTerminalSettlement
			if err := json.Unmarshal(e.payload, &body); err != nil {
				t.Fatal(err)
			}
			if !body.Landed || body.ItemState != "closed" || body.Step != "landed" {
				t.Errorf("settlement does not say the run landed after the close: %+v", body)
			}
		}
	}
	if settled != 1 {
		t.Fatalf("recorded %d settlements, want 1", settled)
	}
	if head := runGit(t, l.lanePath("lane-1"), "rev-parse", "HEAD"); head != runGit(t, p.f.root, "rev-parse", TrunkRef) {
		t.Fatalf("lane-1 not reset: %s", head)
	}
	for _, plan := range mustPlan(t, l) {
		if d := plan.decision; d.ItemID == "item-a" && d.Action != ActionHold && d.Action != "" {
			t.Fatalf("a closed item was planned again: %+v", d)
		}
	}
}
