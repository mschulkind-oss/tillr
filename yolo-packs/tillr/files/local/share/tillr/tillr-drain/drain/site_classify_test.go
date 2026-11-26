package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/remotesync"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

type processStepsWithStoreState struct {
	*ProcessSteps
	storePath string
}

func (s *processStepsWithStoreState) ItemState(ctx context.Context, itemID, runID, state string, waiting ...item.WaitingOn) error {
	return (&oqgp7ReplaySteps{storePath: s.storePath}).ItemState(ctx, itemID, runID, state, waiting...)
}

func withClassifier(t *testing.T, fn func(checkresult.Observation) checkresult.Result) {
	t.Helper()
	prev := classifyCheck
	prevModel := drainmodel.ClassifyCheck
	prevRemote := remotesync.ClassifyCheck
	prevFixer := fixer.ClassifyCheck
	prevHeavyRun := heavyrun.ClassifyCheck
	classifyCheck = fn
	drainmodel.ClassifyCheck = fn
	remotesync.ClassifyCheck = fn
	fixer.ClassifyCheck = fn
	heavyrun.ClassifyCheck = fn
	t.Cleanup(func() {
		classifyCheck = prev
		drainmodel.ClassifyCheck = prevModel
		remotesync.ClassifyCheck = prevRemote
		fixer.ClassifyCheck = prevFixer
		heavyrun.ClassifyCheck = prevHeavyRun
	})
}

func writeVerdictRecord(t *testing.T, path string, payload map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          GateVerdictKind,
		ID:            "verdict-site-test",
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: "0.1.0"},
		CreatedAt:     time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
		Payload:       rawPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSiteD1GateUsesClassifier(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	storePath := gateTimingStore(t, root)
	script := executable(t, "exit 0")
	steps := &ProcessSteps{
		Root:        root,
		Store:       storePath,
		GateProgram: script,
		Now:         time.Now,
	}
	c := Candidate{ItemID: "item-d1", RunID: "run-d1", Worktree: root, HeadSHA: "deadbeef1234", skipFastGateTiming: true}

	var mu sync.Mutex
	var seen []checkresult.Observation
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		mu.Lock()
		seen = append(seen, o)
		mu.Unlock()
		if o.Source == checkresult.JudgingProgram {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	out, err := steps.Gate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != GateFailed {
		t.Fatalf("Gate outcome = %q, want %q from classifier override", out.Outcome, GateFailed)
	}
	if len(seen) == 0 || seen[0].Source != checkresult.JudgingProgram || seen[0].ExitCode == nil || *seen[0].ExitCode != 0 {
		t.Fatalf("observations = %+v, want JudgingProgram exit 0", seen)
	}

	// Also verify pass-inherited settlement stays intact when JudgingProgram answers Pass.
	withClassifier(t, checkresult.Classify)
	c2 := Candidate{ItemID: "item-d1b", RunID: "run-d1b", Worktree: root, HeadSHA: "deadbeef1234", skipFastGateTiming: true}
	writeVerdictRecord(t, filepath.Join(RunDir(root, c2.RunID), GateVerdictRecord), map[string]any{
		"result":             GatePassedInherited,
		"inherited_failures": []map[string]string{{"check_id": "unit", "tip_sha": "abc", "output": "red on tip"}},
	})
	out2, err := steps.finishDetachedGate(ctx, c2, detachedGateHandle{dir: RunDir(root, c2.RunID)}, detachedGateDone{
		RunID:        c2.RunID,
		Argv:         []string{script},
		Cwd:          root,
		StartedAtMS:  10,
		FinishedAtMS: 20,
		ExitCode:     0,
	})
	if err != nil || out2.Outcome != GatePassedInherited || len(out2.InheritedFailures) != 1 {
		t.Fatalf("inherited outcome = %+v, err = %v", out2, err)
	}
}

func TestSiteD2FailureContextAndSpansUseClassifier(t *testing.T) {
	root := t.TempDir()
	c := Candidate{ItemID: "item-d2", RunID: "run-d2", Worktree: root, HeadSHA: "111122223333"}
	runDir := RunDir(root, c.RunID)
	writeVerdictRecord(t, filepath.Join(runDir, GateVerdictRecord), map[string]any{
		"result": "fail",
		"checks": []map[string]any{
			{"check_id": "verdict-pass", "result": "pass", "log_ref": filepath.Join(runDir, "vp.log")},
		},
	})
	writeVerdictRecord(t, filepath.Join(runDir, GateFastVerdictFile), map[string]any{
		"result": "pass",
		"checks": []map[string]any{
			{"check_id": "fast-pass", "result": "pass"},
		},
	})
	spansRaw, err := json.Marshal(gateSpansFile{
		CheckSpans: []gateSpan{
			{CheckID: "span-ok", Status: "ok", AttributesJSON: `{"reads":["docs/a.md"]}`},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, GateSpansFile), spansRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		switch o.Check {
		case "verdict-pass", "fast-pass", "span-ok":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	steps := &ProcessSteps{Root: root}
	checks, _, watches := steps.gateFailureContext(c)
	if !slices.Equal(checks, []string{"fast-pass", "span-ok"}) {
		t.Fatalf("gateFailureContext checks = %v, want [fast-pass span-ok]", checks)
	}
	if !slices.Equal(watches["span-ok"], []string{"docs/a.md"}) {
		t.Fatalf("span-ok watches = %v, want [docs/a.md]", watches["span-ok"])
	}
	ending := gateFailingChecks(runDir, c.RunID)
	if len(ending) != 2 || ending[0].CheckID != "fast-pass" || ending[1].CheckID != "verdict-pass" {
		t.Fatalf("gateFailingChecks = %+v, want [fast-pass verdict-pass]", ending)
	}
}

func TestSiteD3VerifySettlesClassifierGateOutcome(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-d3")
	lane := l.cfg.Lanes[0]
	target := l.laneTarget(lane)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, target, "checkout", "-q", "--detach", base)
	writeForkFile(t, filepath.Join(target, "d3.txt"), "change\n")
	runGit(t, target, "add", "d3.txt")
	runGit(t, target, "commit", "-qm", "d3 candidate")
	head := runGit(t, target, "rev-parse", "HEAD")

	script := executable(t, "exit 0")
	l.cfg.Steps = &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{
			Root:        f.root,
			LaneRoot:    f.laneRoot,
			Store:       f.storePath,
			GateProgram: script,
			Now:         l.cfg.Now,
		},
		storePath: f.storePath,
	}

	// Flip exit 0 into Fail via classifyCheck so D3 (`verify`) takes the GateFailed refusal branch.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.JudgingProgram {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	c := Candidate{
		ItemID:             "item-d3",
		LaneID:             lane,
		RunID:              "run-d3",
		Worktree:           target,
		BaseSHA:            base,
		HeadSHA:            head,
		skipFastGateTiming: true,
	}
	var res LoopResult
	if _, landed, err := l.verify(ctx, &c, &res, nil); err != nil || landed {
		t.Fatalf("verify landed=%v err=%v, want refused via classifier", landed, err)
	}
	if res.Rejected != 1 {
		t.Fatalf("Rejected = %d, want 1", res.Rejected)
	}
}

func TestSiteD4StallContextAndPendingHistoryUseClassifier(t *testing.T) {
	f, l := passFixture(t, "item-d4")
	c := Candidate{ItemID: "item-d4", RunID: "run-d4", Worktree: f.root, HeadSHA: "abcd1234"}
	runDir := RunDir(f.root, c.RunID)
	writeVerdictRecord(t, filepath.Join(runDir, GateVerdictRecord), map[string]any{
		"result": "pass",
		"checks": []map[string]any{
			{"check_id": "slow-check", "result": "pass", "silent_samples": 60},
		},
	})

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.ResultWord {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailStalled}
		}
		return checkresult.Classify(o)
	})

	steps := &ProcessSteps{Root: f.root}
	obs, err := steps.gateStallContext(c)
	if err != nil || len(obs) != 1 || obs[0].CheckID != "slow-check" {
		t.Fatalf("gateStallContext = %+v, err = %v", obs, err)
	}

	// Also exercise foldPendingHistory's stalled second-observation path via store events.
	ctx := context.Background()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := Decision{ItemID: c.ItemID, LaneID: "lane-1", RunRequest: &run.Request{RunID: c.RunID, ItemID: c.ItemID, LaneID: "lane-1"}}
		if err := appendEffect(tx, dispatchStartedKind, c.RunID, pendingWork{Decision: d, Ready: true}, l.cfg.Now().UTC()); err != nil {
			return err
		}
		hold := gateStallHold{
			Candidate:    c,
			Observations: obs,
		}
		if err := appendEffect(tx, gateStallHeldKind, c.RunID, hold, l.cfg.Now().UTC()); err != nil {
			return err
		}
		return appendEffect(tx, GateVerdictKind, c.RunID, map[string]any{
			"result": "pass",
			"checks": []map[string]any{{"check_id": "slow-check", "result": "pass"}},
		}, l.cfg.Now().UTC())
	})
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		_, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		w := byRun[c.RunID]
		if w == nil || w.GateStall == nil || len(w.GateStall.SecondObservations) != 1 || w.GateStall.SecondObservations[0].CheckID != "slow-check" {
			t.Fatalf("byRun[%s] = %+v, want [slow-check] SecondObservations", c.RunID, w)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSiteD5ReadForkConflictsUsesClassifier(t *testing.T) {
	dir := t.TempDir()
	spansPath := filepath.Join(dir, GateSpansFile)
	raw, err := json.Marshal(gateSpansFile{
		CheckSpans: []gateSpan{
			{
				CheckID:        "patch-branch-inv2:pi",
				Kind:           forkDriftCheck,
				Status:         "ok",
				AttributesJSON: `{"fork_conflict":{"fork":"pi","kind":"release","ref":"refs/tags/v2.0.0","commit":"1111","delivery":"patch/pi"}}`,
			},
			{
				CheckID: "patch-branch-inv2:other",
				Kind:    forkDriftCheck,
				Status:  "ok",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spansPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		switch o.Check {
		case "patch-branch-inv2:pi":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		case "patch-branch-inv2:other":
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailStale}
		}
		return checkresult.Classify(o)
	})

	obs, unfinished, unanswered := readForkConflicts(spansPath)
	if !unfinished || !unanswered || len(obs) != 1 || obs["patch-branch-inv2:pi"].Fork != "pi" {
		t.Fatalf("readForkConflicts = (%+v, unfinished=%v, unanswered=%v)", obs, unfinished, unanswered)
	}
}

func TestSiteD6SettleGateFailureUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l, o, base := driftFixture(t, false)
	replayScript := executable(t, `printf 'fork-check replay target: {"fork":"pi","kind":"release","ref":"refs/tags/v2.0.0","commit":"`+o.Commit+`","delivery":"patch/pi"}\n'; exit 0`)
	l.cfg.Steps = &ProcessSteps{
		Root:          f.root,
		LaneRoot:      f.laneRoot,
		Store:         f.storePath,
		ReplayProgram: replayScript,
	}

	// Replay exits 0, so without the classifier override settleGateFailure would treat it as candidate-only ("");
	// overriding BinaryProgram to Fail makes settleGateFailure promote disjoint drift to GatePassedInherited.
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.BinaryProgram {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(obs)
	})

	c := Candidate{ItemID: "item-doc", RunID: "run-d6", BaseSHA: base}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{driftID},
		forkConflicts:         map[string]forkObservation{driftID: o},
		CandidateChangedPaths: []string{"yolo-packs/pi-fork/pack.json"},
	}
	outcome, held, err := l.settleGateFailure(ctx, &c, gate, &LoopResult{})
	if err != nil || held || outcome != GatePassedInherited {
		t.Fatalf("settleGateFailure = (%q, held=%v, err=%v), want (%q, false, nil)", outcome, held, err, GatePassedInherited)
	}
}

type stubRequiredSteps struct {
	*recordingSteps
	result HeavyCheck
}

func (s *stubRequiredSteps) JudgeRequired(_ context.Context, _, _, _ string) (HeavyCheck, error) {
	return s.result, nil
}

func TestSiteD7JudgeRequiredUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-d7")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, RefusalKind, "run-prev", refusalPayload{
			ItemID:        "item-d7",
			Step:          RefusalReverted,
			FailingChecks: []string{"go-suite"},
		}, l.cfg.Now().UTC())
	})
	l.cfg.Steps = &stubRequiredSteps{
		recordingSteps: &recordingSteps{},
		result:         HeavyCheck{Result: "pass", Detail: "suite output"},
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord && o.Check == "go-suite" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	c := Candidate{ItemID: "item-d7", RunID: "run-d7", Worktree: f.root, HeadSHA: "abcd1234"}
	detail, err := l.judgeRequired(ctx, &c)
	if err != nil || !strings.Contains(detail, "go-suite failed") {
		t.Fatalf("judgeRequired = (%q, %v), want go-suite failure from classifier", detail, err)
	}
}

func TestSiteD8FixerCheckAndSettleFixerGateUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t)
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	fixer, err := l.fileTrunkFixer(ctx, base, []string{"unit-check"}, GateOutcome{SuspectPaths: []string{"src/a.go"}})
	if err != nil {
		t.Fatal(err)
	}
	script := executable(t, "echo check-output; exit 0")
	steps := &ProcessSteps{
		Root:             f.root,
		LaneRoot:         f.laneRoot,
		Store:            f.storePath,
		FixerGateProgram: script,
		Now:              l.cfg.Now,
	}
	l.cfg.Steps = steps

	c := Candidate{ItemID: fixer, LaneID: "lane-1", RunID: "run-d8", Worktree: f.root, BaseSHA: base, HeadSHA: base}
	verdictPath := filepath.Join(RunDir(f.root, c.RunID), GateVerdictRecord)
	writeVerdictRecord(t, verdictPath, map[string]any{"result": "pass", "checks": []any{}})

	// Script exits 0, but JudgingProgram -> Fail and FixerCheckWord -> Fail routes settleFixerGate to refusal.
	var sources []checkresult.Source
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		sources = append(sources, o.Source)
		if o.Source == checkresult.JudgingProgram || o.Source == checkresult.FixerCheckWord {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	gate := GateOutcome{Outcome: GatePassed}
	var res LoopResult
	accepted, err := l.settleFixerGate(ctx, &c, &gate, &res)
	if err != nil || accepted || res.Rejected != 1 {
		t.Fatalf("settleFixerGate accepted=%v rejected=%d err=%v, want rejected", accepted, res.Rejected, err)
	}
	if !slices.Contains(sources, checkresult.JudgingProgram) || !slices.Contains(sources, checkresult.FixerCheckWord) {
		t.Fatalf("observed sources = %v, want JudgingProgram and FixerCheckWord", sources)
	}
}

func TestSiteD9RetryHoldAndRemoteSyncUseClassifier(t *testing.T) {
	// 1. retryHoldReason + readRunHistory + gateRefusalDetail
	f, l := passFixture(t, "item-d9")
	runDir := RunDir(f.root, "run-d9")
	logPath := filepath.Join(runDir, "logs", "00-gate.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("failure log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeVerdictRecord(t, filepath.Join(runDir, GateVerdictRecord), map[string]any{
		"result": "pass",
		"checks": []map[string]any{{"check_id": "lint", "result": "pass", "log_ref": logPath}},
	})

	ctx := context.Background()
	if err := appendJudgment(ctx, f.storePath, GateVerdictKind, "run", "run-d9", map[string]any{"result": "pass"}, l.cfg.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var hist runHistory
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		hist, err = readRunHistory(r)
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.ResultWord {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})
	detail := gateRefusalDetail(runDir, "", gateFailingChecks(runDir, "run-d9"))
	if !strings.Contains(detail, "lint") || !strings.Contains(detail, logPath) {
		t.Fatalf("gateRefusalDetail = %q, want lint and %s", detail, logPath)
	}
	pl := &planner{
		latest:  map[string]string{"item-d9": "run-d9"},
		history: hist,
		classes: map[string]AttemptClass{},
	}
	if got := pl.retryHoldReason("item-d9"); got != ReasonGateFailed {
		t.Fatalf("retryHoldReason = %q, want %q from classifier", got, ReasonGateFailed)
	}

	// 2. landRemoteSync refuses a passing verdict when ResultWord classifies it as Fail.
	p, _, syncCandidate := stagedReplay(t)
	var syncRes LoopResult
	if _, ok, err := p.l.landRemoteSync(ctx, syncCandidate, &syncRes); err != nil || ok || syncRes.Rejected != 1 {
		t.Fatalf("landRemoteSync ok=%v rejected=%d err=%v, want rejected via classifier", ok, syncRes.Rejected, err)
	}
}

func TestSiteD10TrunkAlreadyRedAndRouteObstacleUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d10")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	script := executable(t, "exit 0")
	replaySteps := &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{
			Root:          f.root,
			LaneRoot:      f.laneRoot,
			Store:         f.storePath,
			ReplayProgram: script,
			Now:           l.cfg.Now,
		},
		storePath: f.storePath,
	}
	l.cfg.Steps = replaySteps

	// Replay script exits 0, but BinaryProgram override returns Fail, making trunkAlreadyRed,
	// redOnTip, and routeObstacle confirm trunk red.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	c := Candidate{ItemID: "item-d10", LaneID: "lane-1", RunID: "run-d10", BaseSHA: base, HeadSHA: base}
	reading, err := l.trunkAlreadyRed(ctx, c, GateOutcome{Outcome: GateFailed, FailingChecks: []string{"unit"}})
	if err != nil || !reading.Confirmed || !slices.Equal(reading.Red, []string{"unit"}) {
		t.Fatalf("trunkAlreadyRed = %+v, err = %v", reading, err)
	}
	tipRed, _, ok, err := l.redOnTip(ctx, replaySteps, c, base, []string{"unit"})
	if err != nil || !ok || !slices.Equal(tipRed, []string{"unit"}) {
		t.Fatalf("redOnTip = (%v, ok=%v, err=%v)", tipRed, ok, err)
	}

	obsSteps := &obstacleSteps{known: true}
	f2, l2, work := obstacleFixture(t, obsSteps, "item-obs-d10")
	l2.cfg.Steps = &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{
			Root:          f2.root,
			LaneRoot:      f2.laneRoot,
			Store:         f2.storePath,
			ReplayProgram: script,
			Now:           l2.cfg.Now,
		},
		storePath: f2.storePath,
	}
	baseObs := runGit(t, f2.root, "rev-parse", TrunkRef)
	var obsRes LoopResult
	obsOut, routed := l2.routeObstacle(ctx, work.Decision, *work.Decision.RunRequest, work.Decision.LaneID, "tick-d10", baseObs, Obstacle{
		Kind:   ObstacleTrunk,
		Fields: map[string]string{"CHECKS": "unit", "BASE": baseObs},
	}, &obsRes)
	if !routed || obsOut.err != nil {
		t.Fatalf("routeObstacle = (%+v, routed=%v), want confirmed trunk obstacle via classifier", obsOut, routed)
	}
}

func TestSiteD11JudgeTrunkChecksUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t)
	rev := runGit(t, f.root, "rev-parse", TrunkRef)
	heavyDir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(heavyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	proofRaw, err := json.Marshal(heavy.GreenProof{
		Commit:   rev,
		ProvedAt: time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC),
		Passed:   []string{"cached-check"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(heavyDir, "green-"+rev+".json"), proofRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	replayed := false
	replayStub := &oqgp7ReplaySteps{
		recordingSteps: &recordingSteps{},
		storePath:      f.storePath,
		replays:        map[string]ReplayOutcome{rev: {Rev: rev, Known: true, Red: []string{"cached-check"}}},
	}

	// When CachedRecord classifies as Fail instead of Pass, judgeTrunkChecks refuses the cached
	// green proof and falls through to replay.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.CachedRecord && o.Check == "cached-check" {
			replayed = true
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	out, source, err := l.judgeTrunkChecks(ctx, replayStub, Candidate{BaseSHA: rev}, rev, []string{"cached-check"}, true)
	if err != nil || !replayed || source != "replay" || !slices.Equal(out.Red, []string{"cached-check"}) {
		t.Fatalf("judgeTrunkChecks = (%+v, source=%q, replayed=%v, err=%v)", out, source, replayed, err)
	}
}

func TestSiteD12ReplayUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, _ := passFixture(t)
	rev := runGit(t, f.root, "rev-parse", TrunkRef)
	script := executable(t, "exit 0")
	steps := &ProcessSteps{
		Root:          f.root,
		LaneRoot:      f.laneRoot,
		ReplayProgram: script,
	}

	var seen []checkresult.Observation
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		seen = append(seen, o)
		if o.Source == checkresult.BinaryProgram {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	out, err := steps.Replay(ctx, Candidate{}, rev, []string{"unit-check"})
	if err != nil || !out.Known || !slices.Equal(out.Red, []string{"unit-check"}) {
		t.Fatalf("Replay = (%+v, %v), want Known=true Red=[unit-check]", out, err)
	}
	if len(seen) != 1 || seen[0].Source != checkresult.BinaryProgram || seen[0].Check != "unit-check" || seen[0].ExitCode == nil || *seen[0].ExitCode != 0 {
		t.Fatalf("Replay observations = %+v", seen)
	}
}

func TestSiteD13SettleTrunkAlreadyRedUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d13")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	script := executable(t, "exit 0")
	l.cfg.Steps = &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{
			Root:          f.root,
			LaneRoot:      f.laneRoot,
			Store:         f.storePath,
			ReplayProgram: script,
			Now:           l.cfg.Now,
		},
		storePath: f.storePath,
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	// Disjoint path promotes to GatePassedInherited; intersecting path holds on trunk fixer.
	cDisjoint := Candidate{ItemID: "item-d13", RunID: "run-d13-a", BaseSHA: base, HeadSHA: base}
	gateDisjoint := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"src-check"},
		CandidateChangedPaths: []string{"docs/readme.md"},
		CheckReads:            map[string][]string{"src-check": {"src/"}},
	}
	outcome, held, err := l.settleTrunkAlreadyRed(ctx, &cDisjoint, gateDisjoint, &LoopResult{})
	if err != nil || held || outcome != GatePassedInherited {
		t.Fatalf("disjoint settleTrunkAlreadyRed = (%q, held=%v, err=%v)", outcome, held, err)
	}

	cOverlap := Candidate{ItemID: "item-d13", RunID: "run-d13-b", BaseSHA: base, HeadSHA: base}
	gateOverlap := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"src-check"},
		CandidateChangedPaths: []string{"src/main.go"},
		CheckReads:            map[string][]string{"src-check": {"src/"}},
	}
	var res LoopResult
	outcome2, held2, err := l.settleTrunkAlreadyRed(ctx, &cOverlap, gateOverlap, &res)
	if err != nil || !held2 || outcome2 != "" || res.TrunkAcquitted != 1 {
		t.Fatalf("overlap settleTrunkAlreadyRed = (%q, held=%v, acq=%d, err=%v)", outcome2, held2, res.TrunkAcquitted, err)
	}
}

func TestSiteD14PromoteVerdictPassInheritedUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-d14")
	c := Candidate{ItemID: "item-d14", RunID: "run-d14", Worktree: f.root, HeadSHA: "1234abcd"}
	verdictPath := filepath.Join(RunDir(f.root, c.RunID), GateVerdictRecord)
	writeVerdictRecord(t, verdictPath, map[string]any{
		"result": "custom-red",
		"checks": []map[string]any{
			{"check_id": "red-check", "result": "custom-red"},
			{"check_id": "green-check", "result": "pass"},
		},
	})

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.ResultWord && o.Word == "custom-red" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	if err := l.promoteVerdictPassInherited(ctx, c, "deadbeef9999", []string{"red-check"}); err != nil {
		t.Fatal(err)
	}
	payload, ok := readRecordPayload(verdictPath)
	if !ok {
		t.Fatal("missing rewritten verdict")
	}
	var decoded struct {
		Result string `json:"result"`
		Checks []struct {
			CheckID string `json:"check_id"`
			Result  string `json:"result"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Result != GatePassedInherited || len(decoded.Checks) != 2 || decoded.Checks[0].Result != "inherited" || decoded.Checks[1].Result != "pass" {
		t.Fatalf("rewritten verdict = %+v", decoded)
	}
}

func TestSiteD15CulpritLandingUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t)
	green := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(f.root, "c1.txt"), "one\n")
	runGit(t, f.root, "add", "c1.txt")
	runGit(t, f.root, "commit", "-qm", "landing one")
	writeForkFile(t, filepath.Join(f.root, "c2.txt"), "two\n")
	runGit(t, f.root, "add", "c2.txt")
	runGit(t, f.root, "commit", "-qm", "landing two")
	c2 := runGit(t, f.root, "rev-parse", "HEAD")

	l.cfg.Store = ""
	script := executable(t, "exit 0")
	replay := &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, ReplayProgram: script}

	// Classify green and c1 as Pass, and c2 as Fail: culpritLanding bisects to c2.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram && o.Commit == c2 {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	culprit, note := l.culpritLanding(ctx, replay, Candidate{}, green, c2, []string{"unit"})
	if culprit != c2 || note != "" {
		t.Fatalf("culpritLanding = (%q, %q), want (%q, \"\")", culprit, note, c2)
	}
}

func TestSiteD16FixerRoutingAndMatchingUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d16")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	script := executable(t, "exit 0")
	l.cfg.Steps = &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{
			Root:          f.root,
			LaneRoot:      f.laneRoot,
			Store:         f.storePath,
			ReplayProgram: script,
			Now:           l.cfg.Now,
		},
		storePath: f.storePath,
	}

	// Only "tillr-docs:every face" is classified Fail on replay; "other-check" stays Pass.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram && o.Check == "tillr-docs:every face" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	c := Candidate{ItemID: "item-d16", RunID: "run-d16", BaseSHA: base, HeadSHA: base}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"tillr-docs:every face", "other-check"},
		CandidateChangedPaths: []string{"docs/guide.md"},
	}
	reading, err := l.trunkAlreadyRed(ctx, c, gate)
	if err != nil || !slices.Equal(reading.Red, []string{"tillr-docs:every face"}) {
		t.Fatalf("reading = %+v, err = %v", reading, err)
	}
	fixerID, err := l.routeTrunkFixer(ctx, reading.RedSHA, reading.Red, gate, "", c.ItemID)
	if err != nil || fixerID == "" {
		t.Fatalf("routeTrunkFixer = (%q, %v)", fixerID, err)
	}
	openID, err := l.openFixerFor(ctx, []string{"tillr-docs:every face"})
	if err != nil || openID != fixerID {
		t.Fatalf("openFixerFor = (%q, %v), want %q", openID, err, fixerID)
	}
	declID := forkCheckDeclID("tillr-docs", "every face", f.root)
	if !fixerCheckOverlaps("tillr-docs:every face", declID, f.root) {
		t.Fatalf("fixerCheckOverlaps(%q, %q) = false, want true", "tillr-docs:every face", declID)
	}
	if got := fixerChecks("Fix trunk (red: " + reading.Red[0] + ")"); !slices.Equal(got, []string{"tillr-docs:every face"}) {
		t.Fatalf("fixerChecks = %v", got)
	}
}

func TestSiteD34OpenTrunkRedNoticeUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d34")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	script := executable(t, "exit 0")
	l.cfg.Steps = &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{
			Root:          f.root,
			LaneRoot:      f.laneRoot,
			Store:         f.storePath,
			ReplayProgram: script,
			Now:           l.cfg.Now,
		},
		storePath: f.storePath,
	}

	// Pass 1: BinaryProgram classifies as Pass -> no trunk fixer filed -> openTrunkRedNotice is empty.
	withClassifier(t, checkresult.Classify)
	c := Candidate{ItemID: "item-d34", RunID: "run-d34-a", BaseSHA: base, HeadSHA: base}
	gate := GateOutcome{
		Outcome:               GateFailed,
		FailingChecks:         []string{"lint-check"},
		CandidateChangedPaths: []string{"src/app.go"},
		CheckReads:            map[string][]string{"lint-check": {"src/"}},
	}
	if _, _, err := l.settleTrunkAlreadyRed(ctx, &c, gate, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	if got, err := openTrunkRedNotice(ctx, f.storePath); err != nil || got != "" {
		t.Fatalf("openTrunkRedNotice before classifier fail = (%q, %v), want empty", got, err)
	}

	// Pass 2: BinaryProgram classifies as Fail -> trunk fixer filed -> openTrunkRedNotice reports lint-check.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram && o.Check == "lint-check" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})
	c2 := Candidate{ItemID: "item-d34", RunID: "run-d34-b", BaseSHA: base, HeadSHA: base}
	if _, _, err := l.settleTrunkAlreadyRed(ctx, &c2, gate, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	notice, err := openTrunkRedNotice(ctx, f.storePath)
	if err != nil || !strings.Contains(notice, "Trunk check red on lint-check; remedy fixer: fixer-main-") {
		t.Fatalf("openTrunkRedNotice = (%q, %v), want lint-check fixer notice", notice, err)
	}
}

func TestSiteGateTimingReadsThroughClassifier(t *testing.T) {
	dir := t.TempDir()
	writeVerdictRecord(t, filepath.Join(dir, GateFastVerdictFile), map[string]any{
		"result": "pass",
		"checks": []map[string]any{
			{"check_id": "fast-a", "result": "custom-mem", "duration_ms": 50},
			{"check_id": "fast-b", "result": "custom-skip", "duration_ms": 0},
			{"check_id": "fast-c", "result": "custom-ran", "duration_ms": 120, "start_unix_nano": 100, "end_unix_nano": 200},
		},
	})
	spansRaw, err := json.Marshal(gateSpansFile{
		CheckSpans: []gateSpan{
			{CheckID: "span-cached", Status: "custom-cached", StartNS: 10, EndNS: 20},
			{CheckID: "span-notrun", Status: "custom-notrun"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, GateSpansFile), spansRaw, 0o644); err != nil {
		t.Fatal(err)
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		switch o.Word {
		case "custom-mem":
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailRemembered}
		case "custom-skip":
			return checkresult.Result{Outcome: checkresult.Unavailable, Detail: checkresult.DetailSkipped}
		case "custom-ran":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		case "custom-cached":
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailCached}
		case "custom-notrun":
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailNotRun}
		}
		return checkresult.Classify(o)
	})

	got := readGateTimingChecks(dir)
	if got["fast-a"].Execution != gateExecutionReused ||
		got["fast-b"].Execution != gateExecutionNotRun ||
		got["fast-c"].Execution != gateExecutionRan ||
		got["span-cached"].Execution != gateExecutionReused ||
		got["span-notrun"].Execution != gateExecutionNotRun {
		t.Fatalf("readGateTimingChecks = %+v", got)
	}
}
