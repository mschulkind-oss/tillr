package main_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

// The outer verdict can name the wrapper; the fast verdict names the actual tree
// check. Replay and the fixer must receive the latter, not the wrapper's id.
const failingTreeGateBody = `dir=$(dirname "$TILLR_GATE_SPANS_OUT")
mkdir -p "$dir"
printf '%s\n' '{"payload":{"checks":[{"check_id":"scratch","result":"fail"}]}}' > "$TILLR_FAST_VERDICT_OUT"
exit 1
`

func TestTreeCheckAcquittalHoldsOnTheFixerWithoutAConflict(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	if err := os.MkdirAll(filepath.Join(f.root, "tools/gate/checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "tools/gate/checks/scratch.json"), `{"id":"scratch","scope":"tree","tier":"fast","reads":["."]}`)
	git(t, f.root, "add", "tools/gate/checks/scratch.json")
	git(t, f.root, "commit", "-m", "tree declaration fixture")
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	steps := &trunkSteps{ProcessSteps: f.steps(t, stub(t, "gate", failingTreeGateBody), always(t, "land", 0), always(t, "verdict", 0)),
		replays: map[string]drain.ReplayOutcome{base: {Red: []string{"scratch"}, Known: true}}}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.TrunkAcquitted != 1 || countMatching(result.RefusedItems, "item_041") != 0 {
		t.Fatalf("result=%+v", result)
	}
	fixer := drain.TrunkFixerID(base)
	if !strings.Contains(result.Detail, "trunk-broken") || !strings.Contains(result.Detail, fixer) {
		t.Fatalf("hold does not name trunk-broken and fixer: %s", result.Detail)
	}
	if countMatching(lines(t, f.count), "verdict --item item_041") != 0 {
		t.Fatal("acquittal charged a verdict")
	}
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "item.add" && e.ObjectID != nil && *e.ObjectID == fixer {
			if !strings.Contains(string(e.Payload), `"base.txt"`) || strings.Contains(string(e.Payload), `"tools/gate/checks/scratch.json"`) {
				t.Fatalf("acquittal fixer must reach base source, not its judge: %s", e.Payload)
			}
		}
		if e.Kind == drain.RefusalKind {
			payload := eventPayload(t, e)
			if payload["step"] == drain.RefusalGate && payload["item_id"] == "item_041" {
				t.Fatal("acquittal charged an ordinary refusal")
			}
		}
	}
	refusal := refusalOf(t, f.store, drain.RefusalTrunkAlreadyRed)
	if refusal == nil || refusal["red_sha"] != base {
		t.Fatalf("refusal=%v", refusal)
	}
	// The fixer is ordinary work and may repeat its own refusal; the acquitted item must not
	// be charged a conflict.
	for _, e := range eventsOf(t, f.store) {
		if strings.Contains(e.Kind, "conflict") && eventPayload(t, e)["item_id"] == "item_041" {
			t.Fatalf("acquittal raised %s", e.Kind)
		}
	}
}

func TestAcquittedTreeItemReturnsOnlyAfterItsFixerLands(t *testing.T) {
	for _, state := range []string{"landed", "closed", "in-queue"} {
		for _, check := range []string{"scratch", "unit"} {
			t.Run(state+"/"+check, func(t *testing.T) {
				f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
				fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
				base := git(t, f.root, "rev-parse", "main")
				body := failingTreeGateBody
				if check == "unit" {
					body = strings.ReplaceAll(failingGateBody, `"reads":["src/"]`, `"reads":["candidate.txt"]`)
				}
				steps := &trunkSteps{ProcessSteps: f.steps(t, stub(t, "gate", body), always(t, "land", 0), always(t, "verdict", 0)),
					replays: map[string]drain.ReplayOutcome{base: {Red: []string{check}, Known: true}}}
				first, err := runTrunkLoop(t, f, steps, io.Discard)
				if err != nil || first.TrunkAcquitted != 1 {
					t.Fatalf("first=%+v err=%v", first, err)
				}
				fixer := drain.TrunkFixerID(base)
				// Model the fixer's disposition without rewriting its frozen criteria.
				if state == "in-queue" {
					legacyItemState(t, f.store, fixer, "blocked")
				} else {
					legacyItemState(t, f.store, fixer, state)
				}
				git(t, f.root, "commit", "--allow-empty", "-m", "repaired trunk")
				repaired := git(t, f.root, "rev-parse", "main")
				steps.ProcessSteps.GateProgram = always(t, "green", 0)
				second, err := runTrunkLoop(t, f, steps, io.Discard)
				if err != nil {
					t.Fatal(err)
				}
				if state == "landed" || state == "closed" {
					if second.Dispatched != 1 || second.Landed != 1 {
						t.Fatalf("second=%+v", second)
					}
					if itemStateOf(t, f.store, "item_041") != "landed" {
						t.Fatal("released item did not land")
					}
					calls := strings.Join(lines(t, f.count), "\n")
					if !strings.Contains(calls, "--run run-item_041-a2") || !strings.Contains(calls, "--base "+repaired) {
						t.Fatalf("release did not stage a fresh run from repaired trunk: %s", calls)
					}
				} else if second.Dispatched != 0 || itemStateOf(t, f.store, "item_041") != "blocked" {
					t.Fatalf("open unproved fixer released the item: %+v", second)
				}
			})
		}
	}
}

func TestAcquittedAuthorizedRetryIsNotChargedOrStranded(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	steps := &trunkSteps{ProcessSteps: f.steps(t, always(t, "original-red", 1), always(t, "land", 0), always(t, "verdict", 0))}
	// The first failure goes back to its worker; the identical second one waits on the owner.
	first, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil || first.Rejected != 2 || !strings.Contains(first.Detail, "item_041 ("+drain.ReasonConflictOwner+")") {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	charged := len(eventsOf(t, f.store))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	s, err := store.Open(context.Background(), f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := drain.AuthorizeRetry(context.Background(), tx, drain.RetryRequest{ItemID: "item_041", RunID: "run-item_041-a2", By: "owner", Why: "diagnosed", Acknowledge: drain.RetryAckGateFailed, At: steps.Now()})
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	steps.GateProgram = stub(t, "base-red", failingTreeGateBody)
	steps.replays = map[string]drain.ReplayOutcome{base: {Red: []string{"scratch"}, Known: true}}
	second, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil || second.TrunkAcquitted != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	for _, e := range eventsOf(t, f.store)[charged:] {
		if strings.Contains(e.Kind, "conflict") && eventPayload(t, e)["item_id"] == "item_041" {
			t.Fatalf("acquitted retry charged: %s %s", e.Kind, e.Payload)
		}
	}
	legacyItemState(t, f.store, drain.TrunkFixerID(base), "landed")
	steps.GateProgram = always(t, "repaired", 0)
	third, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil || third.Dispatched != 1 || third.Landed != 1 {
		t.Fatalf("release=%+v err=%v", third, err)
	}
}

func TestAcquittedPredecessorDoesNotBecomeASecondFailure(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	steps := &trunkSteps{ProcessSteps: f.steps(t, stub(t, "gate", failingTreeGateBody), always(t, "land", 0), always(t, "verdict", 0)),
		replays: map[string]drain.ReplayOutcome{base: {Red: []string{"scratch"}, Known: true}}}
	first, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil || first.TrunkAcquitted != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	legacyItemState(t, f.store, drain.TrunkFixerID(base), "landed")
	// A fixer may repair an input the item's next decision reads. That makes
	// this decision a readmission, but cannot turn acquittal into a failed run.
	fixtureAt(t, f.store, itemRecordFenced("item_041", "blocked", "", []string{"candidate.txt", "src/"}))
	steps.ProcessSteps.GateProgram = always(t, "own-red", 1)
	steps.replays = nil
	second, err := runTrunkLoop(t, f, steps, io.Discard)
	// The item's own red goes back to its worker, and the identical second red waits on its
	// mover. The acquittal before it is not part of that repeat: the repeat's conflict names
	// two of this pass's runs, never the acquitted one.
	if err != nil || second.Dispatched != 2 || second.Rejected != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	acquitted := runsForItem(t, f.store, "item_041")[0].RunID
	for _, e := range eventsOf(t, f.store) {
		if strings.Contains(e.Kind, "conflict") && strings.Contains(string(e.Payload), acquitted) {
			t.Fatalf("acquittal counted as earlier failure: %s %s", e.Kind, e.Payload)
		}
	}
}

func TestReplayNamesOnlyTheChecksActuallyRed(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	base := git(t, f.root, "rev-parse", "main")
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	steps.ReplayProgram = stub(t, "replay", `case "$*" in *scratch*) exit 1 ;; *) exit 0 ;; esac`) + " --only {only}"
	out, err := steps.Replay(context.Background(), drain.Candidate{}, base, []string{"scratch", "own-red"})
	if err != nil || !out.Known || len(out.Red) != 1 || out.Red[0] != "scratch" {
		t.Fatalf("replay=%+v err=%v", out, err)
	}
}

func TestMixedTreeFailureDoesNotAcquitTheCandidatesOwnRed(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	body := strings.Replace(failingTreeGateBody, `[{"check_id":"scratch","result":"fail"}]`, `[{"check_id":"scratch","result":"fail"},{"check_id":"own-red","result":"fail"}]`, 1)
	steps := &trunkSteps{ProcessSteps: f.steps(t, stub(t, "gate", body), always(t, "land", 0), always(t, "verdict", 0)),
		replays: map[string]drain.ReplayOutcome{base: {Red: []string{"scratch"}, Known: true}}}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if result.TrunkAcquitted != 0 || result.Rejected != 2 {
		t.Fatalf("mixed failure acquitted: %+v", result)
	}
}

func TestRedTreeRollingProgramKeepsWatermarkAndDeduplicatesFixer(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	if err := os.MkdirAll(filepath.Join(f.root, "tools/gate"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "tools/gate/judge.py"), "gate code\n")
	git(t, f.root, "add", "tools/gate/judge.py")
	git(t, f.root, "commit", "-m", "gate fixture")
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	steps := f.steps(t, always(t, "gate", 1), always(t, "land", 0), always(t, "verdict", 0))
	steps.RollingProgram = stub(t, "rolling", `printf '%s' '{"check_spans":[{"check_id":"scratch/whole-tree","status":"FAIL","attributes_json":"{\"writes\":[\".\"]}"}]}' > "$TILLR_GATE_SPANS_OUT"
exit 1
`)
	for i := 0; i < 2; i++ {
		_, err := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, io.Discard).Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if watermark, _ := rollingStateOf(t, f.store); watermark != "" {
			t.Fatalf("red advanced watermark: %s", watermark)
		}
	}
	fixer := drain.TrunkFixerID(base)
	if !itemExists(t, f.store, fixer) {
		t.Fatalf("missing fixer %s", fixer)
	}
	files := 0
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "item.add" && e.ObjectID != nil && *e.ObjectID == fixer {
			files++
			if !strings.Contains(string(e.Payload), "scratch/whole-tree") {
				t.Fatal("fixer omits failing check")
			}
			if !strings.Contains(string(e.Payload), `"base.txt"`) || strings.Contains(string(e.Payload), `"tools/gate/judge.py"`) {
				t.Fatalf("whole-tree fence must reach tracked source but not its judge: %s", e.Payload)
			}
		}
	}
	if files != 1 {
		t.Fatalf("fixer filed %d times", files)
	}
}

type laterRedReplaySteps struct {
	*trunkSteps
	base string
}

// Replay is green at a candidate's own base and red at any other revision, so a loop that
// judged a failure against newer trunk instead of the base would acquit it. Each returned
// run starts from the trunk the gate moved, which is that run's own base.
func (s *laterRedReplaySteps) Replay(_ context.Context, candidate drain.Candidate, rev string, checks []string) (drain.ReplayOutcome, error) {
	if rev == s.base || rev == candidate.BaseSHA {
		return drain.ReplayOutcome{Rev: rev, Known: true}, nil
	}
	return drain.ReplayOutcome{Rev: rev, Known: true, Red: checks}, nil
}

func TestTreeCheckRedOnlyOnNewerTrunkStillChargesTheCandidate(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	body := fmt.Sprintf("git -C %q commit --allow-empty -qm 'unrelated trunk move'\n", f.root) + failingTreeGateBody
	steps := &laterRedReplaySteps{trunkSteps: &trunkSteps{ProcessSteps: f.steps(t, stub(t, "gate", body), always(t, "land", 0), always(t, "verdict", 0))}, base: base}
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true, Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)}, Policy: f.policy, Steps: steps, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, Now: steps.Now, Report: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Charged, returned to its worker, and charged identically again.
	if result.Rejected != 2 || result.TrunkAcquitted != 0 || firstRefusalStep(t, f.store, "run-item_041-a1") != drain.RefusalGate {
		t.Fatalf("own failure acquitted against later trunk: %+v", result)
	}
}

func TestTreeCheckGreenOnBaseChargesTheCandidate(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	steps := &trunkSteps{ProcessSteps: f.steps(t, stub(t, "gate", failingTreeGateBody), always(t, "land", 0), always(t, "verdict", 0))}
	result, err := runTrunkLoop(t, f, steps, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// Charged, returned to its worker, and charged identically again.
	if result.Rejected != 2 || result.TrunkAcquitted != 0 || refusalOf(t, f.store, drain.RefusalGate) == nil {
		t.Fatalf("candidate not charged: %+v", result)
	}
}

// firstRefusalStep is the step of the first refusal recorded for one run, or "".
func firstRefusalStep(t *testing.T, storePath, runID string) string {
	t.Helper()
	for _, e := range eventsOf(t, storePath) {
		if e.Kind == drain.RefusalKind && e.ObjectID != nil && *e.ObjectID == runID {
			step, _ := eventPayload(t, e)["step"].(string)
			return step
		}
	}
	return ""
}
