package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// gateReplayRevertSteps extends revertSteps with Replay and ItemState recording so a
// single fixture can drive both settleTrunkAlreadyRed (gate replay) and HeavyRunner
// (trunk proof) against the same repository and store.
type gateReplayRevertSteps struct {
	*revertSteps
	replay  func(ctx context.Context, rev string, checks []string) (ReplayOutcome, error)
	blocked *[]string
}

func (s gateReplayRevertSteps) Replay(ctx context.Context, _ Candidate, rev string, checks []string) (ReplayOutcome, error) {
	return s.replay(ctx, rev, checks)
}

func (s gateReplayRevertSteps) ItemState(_ context.Context, itemID, _ string, state string, waiters ...item.WaitingOn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state == StateBlocked {
		reason := ""
		if len(waiters) > 0 {
			reason = waiters[0].Waiter + ": " + waiters[0].Reason
		}
		*s.blocked = append(*s.blocked, itemID+" -> "+reason)
	}
	return nil
}

// replayFileRed judges "unit" red on any commit whose tree contains any of names.
func replayFileRed(root string, names ...string) func(context.Context, string, []string) (ReplayOutcome, error) {
	return func(ctx context.Context, rev string, checks []string) (ReplayOutcome, error) {
		red := false
		for _, name := range names {
			if _, err := heavyGitOutput(ctx, root, "cat-file", "-e", rev+":"+name); err == nil {
				red = true
				break
			}
		}
		out := ReplayOutcome{Rev: rev, Known: true}
		if red {
			out.Red = append([]string{}, checks...)
		}
		return out, nil
	}
}

func newGateReplayRevertHarness(t *testing.T, judge func(context.Context, string, string) ([]HeavyCheck, error), replay func(context.Context, string, []string) (ReplayOutcome, error)) (*revertHarness, *gateReplayRevertSteps) {
	t.Helper()
	h := newRevertHarness(t, judge)
	blocked := &[]string{}
	steps := &gateReplayRevertSteps{revertSteps: h.steps, replay: replay, blocked: blocked}
	h.loop.cfg.Steps = steps
	return h, steps
}

// Reproduces the 2026-11-22 incident: a gate replay finds the trunk red at a landing
// and then a trunk proof runs. With auto_revert on, the gate-replay path reverts the
// landing and files no fixer; the subsequent proof sees the repaired trunk and starts no
// second repair.
func TestGateReplayRevertsCulpritAndProofStartsNoSecondRepair(t *testing.T) {
	h, steps := newGateReplayRevertHarness(t, fileRed("a"), nil)
	steps.replay = replayFileRed(h.root, "a")

	culprit := h.land("item-bad", "a", "broken\n")
	tip := h.land("item-clean", "c", "unrelated\n")

	laneWork := filepath.Join(h.loop.cfg.LaneRoot, "lane-1", "worktree")
	if err := os.MkdirAll(filepath.Dir(laneWork), 0o755); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, h.root, "worktree", "add", "--detach", laneWork, tip)
	cand := Candidate{ItemID: "item-other", LaneID: "lane-1", RunID: "run-other", BaseSHA: tip, HeadSHA: tip}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"unit"},
		CandidateChangedPaths: []string{"a"},
		CheckReads:            map[string][]string{"unit": {"a", "b"}},
	}
	var res LoopResult
	outcome, held, err := h.loop.settleTrunkAlreadyRed(context.Background(), &cand, gate, &res)
	if err != nil {
		t.Fatalf("settleTrunkAlreadyRed: %v", err)
	}
	if outcome != "" || held {
		t.Fatalf("settleTrunkAlreadyRed = (%q, held=%v), want (\"\", false) after automatic revert", outcome, held)
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("reverted landing filed fixers %v, want none", f)
	}
	if len(*steps.blocked) != 0 {
		t.Fatalf("candidate was blocked behind nonexistent fixer: %v", *steps.blocked)
	}
	revertedTip := heavyGit(t, h.root, "rev-parse", TrunkRef)
	if revertedTip == tip {
		t.Fatalf("trunk stayed at %s; want automatic revert", shortSHA(tip))
	}
	if _, err := heavyGitOutput(context.Background(), h.root, "cat-file", "-e", revertedTip+":a"); err == nil {
		t.Fatalf("broken file a still exists in reverted trunk tree %s", shortSHA(revertedTip))
	}
	if cand.BaseSHA != revertedTip {
		t.Fatalf("candidate base = %s, want reverted tip %s", shortSHA(cand.BaseSHA), shortSHA(revertedTip))
	}
	if !strings.Contains(cand.RefusalDetail, shortSHA(revertedTip)) {
		t.Fatalf("candidate refusal detail %q does not name revert %s", cand.RefusalDetail, shortSHA(revertedTip))
	}
	if got := h.query(`SELECT state FROM items WHERE item_id=?`, "item-bad"); len(got) != 1 || got[0] != StateInQueue {
		t.Fatalf("item-bad state = %v, want [%s]", got, StateInQueue)
	}

	// Now the trunk proof runs after the gate replay: it proves the reverted tip green
	// and starts no fixer and no second revert.
	h.proof()
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("proof after gate-replay revert filed fixers %v", f)
	}
	if got := heavyGit(t, h.root, "rev-parse", GreenRef); got != revertedTip {
		t.Fatalf("green ref = %s, want reverted tip %s", shortSHA(got), shortSHA(revertedTip))
	}
	if len(*h.steps.gated) != 1 || (*h.steps.gated)[0].ItemID != RevertItemID(culprit) {
		t.Fatalf("revert gates = %+v, want exactly one revert for %s", *h.steps.gated, shortSHA(culprit))
	}
	reverts := h.query(`SELECT json_extract(payload,'$.result') FROM events WHERE kind='drain.revert' AND object_id=?`, culprit)
	if len(reverts) != 1 || reverts[0] != "reverted" {
		t.Fatalf("drain.revert events = %v, want [reverted]", reverts)
	}
}

// Reproduces the concurrent race where the trunk proof was already judging the red tip
// when the gate replay reverted the culprit landing, and the proof then bisects to that
// same landing: offerRevert recognizes the landing is already reverted, so the proof
// starts neither a second revert nor a fixer.
func TestGateReplayRevertsWhileProofJudgesSameTipStartsOneRepair(t *testing.T) {
	var once sync.Once
	var replayErr error
	var h *revertHarness
	var steps *gateReplayRevertSteps
	var tip string

	judge := func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		// On the whole-tier proof of tip, run the gate replay concurrently so the
		// landing is reverted before the proof finishes attributing it.
		if only == "" {
			sha := heavyGit(t, dir, "rev-parse", "HEAD")
			if sha == tip {
				once.Do(func() {
					cand := Candidate{ItemID: "item-other", LaneID: "lane-1", RunID: "run-other", BaseSHA: tip}
					gate := GateOutcome{
						Outcome:               GateFailed,
						FailingChecks:         []string{"unit"},
						CandidateChangedPaths: []string{"a"},
						CheckReads:            map[string][]string{"unit": {"a", "b"}},
					}
					_, _, replayErr = h.loop.settleTrunkAlreadyRed(ctx, &cand, gate, &LoopResult{})
				})
			}
		}
		return fileRed("a")(ctx, dir, only)
	}

	h, steps = newGateReplayRevertHarness(t, judge, nil)
	steps.replay = replayFileRed(h.root, "a")

	culprit := h.land("item-bad", "a", "broken\n")
	tip = h.land("item-clean", "c", "unrelated\n")

	h.proof()
	if replayErr != nil {
		t.Fatalf("concurrent settleTrunkAlreadyRed: %v", replayErr)
	}
	if f := h.fixers(); len(f) != 0 {
		t.Fatalf("concurrent gate replay + proof filed fixers %v, want none", f)
	}
	if len(*h.steps.gated) != 1 || (*h.steps.gated)[0].ItemID != RevertItemID(culprit) {
		t.Fatalf("revert gates = %+v, want exactly one revert for %s", *h.steps.gated, shortSHA(culprit))
	}
	reverts := h.query(`SELECT json_extract(payload,'$.result') FROM events WHERE kind='drain.revert' AND object_id=?`, culprit)
	if len(reverts) != 1 || reverts[0] != "reverted" {
		t.Fatalf("drain.revert events = %v, want [reverted]", reverts)
	}
}

// When the landing cannot be reverted, the gate-replay path files the fixer with the
// reason, and a subsequent trunk proof that bisects to the same landing starts no revert
// and no second fixer.
func TestGateReplayFilesFixerWhenRevertNotPossibleAndProofStartsNoSecondRepair(t *testing.T) {
	h, steps := newGateReplayRevertHarness(t, fileRed("a"), nil)
	steps.replay = replayFileRed(h.root, "a")

	culprit := h.land("item-bad", "a", "broken\n")
	tip := h.land("item-later", "a", "later edit on same line\n")

	cand := Candidate{ItemID: "item-other", LaneID: "lane-1", RunID: "run-other", BaseSHA: tip, HeadSHA: tip}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"unit"},
		CandidateChangedPaths: []string{"a"},
		CheckReads:            map[string][]string{"unit": {"a", "b"}},
	}
	var res LoopResult
	outcome, held, err := h.loop.settleTrunkAlreadyRed(context.Background(), &cand, gate, &res)
	if err != nil {
		t.Fatalf("settleTrunkAlreadyRed: %v", err)
	}
	if outcome != "" || !held || res.TrunkAcquitted != 1 {
		t.Fatalf("settleTrunkAlreadyRed = (%q, held=%v, acq=%d), want (\"\", true, 1)", outcome, held, res.TrunkAcquitted)
	}
	wantFixer := TrunkFixerID(culprit)
	if f := h.fixers(); len(f) != 1 || f[0] != wantFixer {
		t.Fatalf("fixers = %v, want [%s]", f, wantFixer)
	}
	if text := h.fixerText(wantFixer); !strings.Contains(text, "Automatic revert of "+shortSHA(culprit)+" for unit was not possible") || !strings.Contains(text, "later landings changed a") {
		t.Fatalf("fixer brief lacks revert-refused reason: %s", text)
	}

	// Now the trunk proof runs and bisects to the same landing: it must neither revert
	// nor file a second fixer.
	h.proof()
	if f := h.fixers(); len(f) != 1 || f[0] != wantFixer {
		t.Fatalf("fixers after proof = %v, want [%s]", f, wantFixer)
	}
	if len(*h.steps.gated) != 0 {
		t.Fatalf("revert was gated despite open fixer: %+v", *h.steps.gated)
	}
	if got := heavyGit(t, h.root, "rev-parse", TrunkRef); got != tip {
		t.Fatalf("trunk moved to %s, want %s", shortSHA(got), shortSHA(tip))
	}
}

// Even if the gate-replay revert refusal was transient (e.g., fast gate failed once),
// once the fixer is open the subsequent trunk proof does not also revert the same landing.
func TestProofDoesNotRevertLandingWithOpenFixerFromGateReplay(t *testing.T) {
	h, steps := newGateReplayRevertHarness(t, fileRed("a"), nil)
	steps.replay = replayFileRed(h.root, "a")
	h.steps.gate = GateFailed // first revert attempt fails the fast gate

	culprit := h.land("item-bad", "a", "broken\n")

	cand := Candidate{ItemID: "item-other", LaneID: "lane-1", RunID: "run-other", BaseSHA: culprit, HeadSHA: culprit}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"unit"},
		CandidateChangedPaths: []string{"a"},
		CheckReads:            map[string][]string{"unit": {"a", "b"}},
	}
	var res LoopResult
	_, held, err := h.loop.settleTrunkAlreadyRed(context.Background(), &cand, gate, &res)
	if err != nil || !held {
		t.Fatalf("settleTrunkAlreadyRed = (held=%v, err=%v), want (true, nil)", held, err)
	}
	wantFixer := TrunkFixerID(culprit)
	if f := h.fixers(); len(f) != 1 || f[0] != wantFixer {
		t.Fatalf("fixers = %v, want [%s]", f, wantFixer)
	}

	// Fast gate would now pass, but the fixer is already open: proof must not also revert.
	h.steps.gate = ""
	h.proof()
	if f := h.fixers(); len(f) != 1 || f[0] != wantFixer {
		t.Fatalf("fixers after proof = %v, want [%s]", f, wantFixer)
	}
	if got := heavyGit(t, h.root, "rev-parse", TrunkRef); got != culprit {
		t.Fatalf("proof reverted %s even though %s was already open (trunk now %s)", shortSHA(culprit), wantFixer, shortSHA(got))
	}
	reverts := h.query(`SELECT json_extract(payload,'$.result') FROM events WHERE kind='drain.revert' AND object_id=? AND json_extract(payload,'$.result')='reverted'`, culprit)
	if len(reverts) != 0 {
		t.Fatalf("expected no landed revert, got %v", reverts)
	}
}

// When the check still fails on the tip with the attributed landing reverted (StillRed),
// the gate-replay path files the fixer against the tip rather than the disproved landing.
func TestGateReplayStillRedFilesFixerAgainstTip(t *testing.T) {
	h, steps := newGateReplayRevertHarness(t, fileRed("a", "b"), nil)
	culprit := h.land("item-bad", "a", "broken\n")
	tip := h.land("item-also-bad", "b", "also broken\n")

	// Replay says green at base, red at culprit and tip, AND still red on any other commit
	// (including the revert of culprit, where "b" is still present).
	steps.replay = replayFileRed(h.root, "a", "b")

	cand := Candidate{ItemID: "item-other", LaneID: "lane-1", RunID: "run-other", BaseSHA: tip, HeadSHA: tip}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"unit"},
		CandidateChangedPaths: []string{"a"},
		CheckReads:            map[string][]string{"unit": {"a", "b"}},
	}
	var res LoopResult
	_, held, err := h.loop.settleTrunkAlreadyRed(context.Background(), &cand, gate, &res)
	if err != nil || !held {
		t.Fatalf("settleTrunkAlreadyRed = (held=%v, err=%v), want (true, nil)", held, err)
	}
	wantFixer := TrunkFixerID(tip)
	if f := h.fixers(); len(f) != 1 || f[0] != wantFixer {
		t.Fatalf("fixers = %v, want [%s] (against tip, not disproved %s)", f, wantFixer, shortSHA(culprit))
	}
	if text := h.fixerText(wantFixer); !strings.Contains(text, "still fails on the tip with "+shortSHA(culprit)+" reverted") {
		t.Fatalf("fixer brief lacks StillRed explanation: %s", text)
	}
	// Verify no revert landed.
	if got := heavyGit(t, h.root, "rev-parse", TrunkRef); got != tip {
		t.Fatalf("trunk moved to %s, want %s", shortSHA(got), shortSHA(tip))
	}
	var count int
	if err := h.loop.readStore(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND json_extract(payload,'$.result')='reverted'`, RevertKind).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("reverted count = %d, want 0", count)
	}
}
