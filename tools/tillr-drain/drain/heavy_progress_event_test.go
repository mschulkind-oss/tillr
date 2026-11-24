package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type recordedHeavyProgress struct {
	ObjectType string
	ObjectID   string
	At         time.Time
	Fact       HeavyProgressFact
}

func readHeavyProgressEvents(t *testing.T, storePath string) []recordedHeavyProgress {
	t.Helper()
	db, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	var events []event.Event
	if err := db.ReadTx(context.Background(), func(r *store.Reader) error {
		all, err := r.Events(0)
		if err != nil {
			return err
		}
		for _, ev := range all {
			if ev.Kind == HeavyProgressKind {
				events = append(events, ev)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("read %s events: %v", HeavyProgressKind, err)
	}
	out := make([]recordedHeavyProgress, 0, len(events))
	for _, ev := range events {
		var fact HeavyProgressFact
		if err := json.Unmarshal(ev.Payload, &fact); err != nil {
			t.Fatalf("decode %s payload %s: %v", HeavyProgressKind, ev.Payload, err)
		}
		var objectID string
		if ev.ObjectID != nil {
			objectID = *ev.ObjectID
		}
		out = append(out, recordedHeavyProgress{
			ObjectType: ev.ObjectType,
			ObjectID:   objectID,
			At:         ev.At,
			Fact:       fact,
		})
	}
	return out
}

func newHeavyProgressStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(scratchUnderDurable(t), "state.db")
	db, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return path
}

func TestHeavyProgressEventsEmitStartedProgressAndEnded(t *testing.T) {
	if !slices.ContainsFunc(event.Kinds(), func(k event.Kind) bool { return k.Name == HeavyProgressKind && k.Owner == Utility }) {
		t.Fatalf("%s is not registered to %s", HeavyProgressKind, Utility)
	}
	root, base := heavyRepo(t)
	storePath := newHeavyProgressStore(t)
	heavyGit(t, root, "update-ref", GreenRef, base)

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	nextNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		now = now.Add(time.Second)
		return now
	}

	// Idle pass (tip == base) emits no proof-progress events.
	idleRunner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		t.Fatal("idle run invoked Judge")
		return nil, nil
	})
	idleRunner.Store = storePath
	idleRunner.Now = nextNow
	if _, err := idleRunner.Run(context.Background()); err != nil {
		t.Fatalf("idle Run: %v", err)
	}
	if got := readHeavyProgressEvents(t, storePath); len(got) != 0 {
		t.Fatalf("want no events on idle tip == base pass, got %+v", got)
	}

	// Tip 1 stalls once on lint, retries lint to pass, and advances directly onto tip 2.
	tip1 := heavyCommit(t, root, "tip-1")
	var tip2 string
	stalledOnce := false
	runner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		head := heavyGit(t, dir, "rev-parse", "HEAD")
		if only == "lint" {
			return []HeavyCheck{{CheckID: "lint", Result: "pass"}}, nil
		}
		if head == tip1 && !stalledOnce {
			stalledOnce = true
			tip2 = heavyCommit(t, root, "tip-2")
			return []HeavyCheck{
				{CheckID: "unit", Result: "pass"},
				{CheckID: "lint", Result: "stalled"},
			}, nil
		}
		return []HeavyCheck{
			{CheckID: "unit", Result: "pass"},
			{CheckID: "lint", Result: "pass"},
		}, nil
	})
	runner.Store = storePath
	runner.Now = nextNow
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("green Run: %v", err)
	}

	got := readHeavyProgressEvents(t, storePath)
	want := []HeavyProgressFact{
		{TipSHA: tip1, Phase: "started"},
		{TipSHA: tip1, Phase: "progress", CheckID: "unit", Outcome: "pass"},
		{TipSHA: tip1, Phase: "progress", CheckID: "lint", Outcome: "stalled"},
		{TipSHA: tip1, Phase: "progress", CheckID: "lint", Outcome: "pass"},
		{TipSHA: tip1, Phase: "ended", Outcome: "green"},
		{TipSHA: tip2, Phase: "started"},
		{TipSHA: tip2, Phase: "progress", CheckID: "unit", Outcome: "pass"},
		{TipSHA: tip2, Phase: "progress", CheckID: "lint", Outcome: "pass"},
		{TipSHA: tip2, Phase: "ended", Outcome: "green"},
	}
	if len(got) != len(want) {
		t.Fatalf("events count = %d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].ObjectType != "trunk" || got[i].ObjectID != "main" {
			t.Fatalf("event[%d] envelope = (%q, %q), want (\"trunk\", \"main\")", i, got[i].ObjectType, got[i].ObjectID)
		}
		if got[i].Fact != want[i] {
			t.Fatalf("event[%d] fact = %+v, want %+v", i, got[i].Fact, want[i])
		}
	}

	// Red proof with culprit attribution replay emits replay progress before ended(red).
	badA := heavyCommit(t, root, "bad-a")
	tipRed := heavyCommit(t, root, "bad-b")
	redRunner := heavyFixture(t, root, func(_ context.Context, dir, only string) ([]HeavyCheck, error) {
		head := heavyGit(t, dir, "rev-parse", "HEAD")
		res := "pass"
		if head == badA || head == tipRed {
			res = "fail"
		}
		return []HeavyCheck{{CheckID: "unit", Result: res}}, nil
	})
	redStore := newHeavyProgressStore(t)
	redRunner.Store = redStore
	redRunner.Now = nextNow
	out, err := redRunner.Run(context.Background())
	if err != nil {
		t.Fatalf("red Run: %v", err)
	}
	if len(out.Runs) != 1 || out.Runs[0].Result != "red" {
		t.Fatalf("red Run result = %+v", out.Runs)
	}
	redEvents := readHeavyProgressEvents(t, redStore)
	if len(redEvents) < 4 {
		t.Fatalf("want started, judge progress, replay progress, and ended on red run; got %+v", redEvents)
	}
	if first := redEvents[0].Fact; first != (HeavyProgressFact{TipSHA: tipRed, Phase: "started"}) {
		t.Fatalf("red first = %+v", first)
	}
	if last := redEvents[len(redEvents)-1].Fact; last != (HeavyProgressFact{TipSHA: tipRed, Phase: "ended", Outcome: "red"}) {
		t.Fatalf("red last = %+v", last)
	}
	replayProgress := 0
	for _, ev := range redEvents[1 : len(redEvents)-1] {
		if ev.Fact.TipSHA != tipRed || ev.Fact.Phase != "progress" || ev.Fact.CheckID != "unit" {
			t.Fatalf("unexpected intermediate red event %+v", ev.Fact)
		}
		replayProgress++
	}
	if replayProgress < 2 {
		t.Fatalf("want whole-judge and attribution replay progress events, got %d", replayProgress)
	}
}

func TestHeavyProgressEventsEmitLiveLegacyChecksDuringRun(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	storePath := newHeavyProgressStore(t)
	work := scratchUnderDurable(t)
	enteredB := filepath.Join(work, "entered-b")
	releaseB := filepath.Join(work, "release-b")

	if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", ".keep"), "")
	transport, err := os.ReadFile(filepath.Join("..", "..", "gate", "classify.py"))
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "gate", "classify.py"), string(transport))
	source, err := os.ReadFile(filepath.Join("..", "..", "fork-check.py"))
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "if __name__ == \"__main__\":\n    sys.exit(main(sys.argv[1:]))", "", 1))
	bCmd := "touch " + enteredB + "; while [ ! -e " + releaseB + " ]; do sleep 0.02; done"
	source = append(source, []byte(`
def main(argv):
    import os
    if "--registry-json" in argv:
        print(json.dumps({"declarations": [{"id": "fixture/" + x, "kind": "fixture", "label": x, "watches": [{"path": "value"}]} for x in ("a", "b")]}))
        return 0
    root = Path(__file__).resolve().parents[1]
    plan = Plan()
    plan.checks = [
        Check("fixture", "a", ["sh", "-c", "true"], "fixture", 0),
        Check("fixture", "b", ["sh", "-c", "`+bCmd+`"], "fixture", 0, after=("a",)),
    ]
    run_plan_checks(plan, root, None, 1)
    raw = {"check_spans": [{"check_id": declaration_id(c.kind, c.label, root), "kind": c.kind, "label": c.label, "status": check_status(c), "attributes_json": "{}"} for c in plan.checks], "skipped_checks": []}
    Path(os.environ["TILLR_GATE_SPANS_OUT"]).write_text(json.dumps(raw))
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
`)...)
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), string(source))
	tip := heavyCommit(t, root, "legacy-live")

	steps := &ProcessSteps{
		Root:           root,
		LaneRoot:       work,
		RollingProgram: "python3 tools/fork-check.py {since} --tier rolling",
		ReplayProgram:  "python3 tools/fork-check.py --only {only}",
	}
	runner := heavyFixture(t, root, steps.JudgeHeavy)
	runner.Store = storePath

	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background())
		done <- err
	}()
	defer func() {
		_ = os.WriteFile(releaseB, nil, 0o644)
	}()

	deadline := time.After(20 * time.Second)
	for {
		if _, err := os.Stat(enteredB); err == nil {
			events := readHeavyProgressEvents(t, storePath)
			sawA := false
			for _, ev := range events {
				if ev.Fact.Phase == "ended" {
					t.Fatalf("saw premature ended while fixture/b is still running: %+v", events)
				}
				if ev.Fact == (HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: "fixture/a", Outcome: "pass"}) {
					sawA = true
				}
			}
			if sawA {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("heavy exited before fixture/b was released: %v", err)
		case <-deadline:
			t.Fatalf("timed out waiting for live fixture/a progress event; events=%+v", readHeavyProgressEvents(t, storePath))
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := os.WriteFile(releaseB, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("legacy Run: %v", err)
	}

	events := readHeavyProgressEvents(t, storePath)
	want := []HeavyProgressFact{
		{TipSHA: tip, Phase: "started"},
		{TipSHA: tip, Phase: "progress", CheckID: "fixture/a", Outcome: "pass"},
		{TipSHA: tip, Phase: "progress", CheckID: "fixture/b", Outcome: "pass"},
		{TipSHA: tip, Phase: "ended", Outcome: "green"},
	}
	if len(events) != len(want) {
		t.Fatalf("legacy events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i].Fact != want[i] {
			t.Fatalf("legacy event[%d] = %+v, want %+v", i, events[i].Fact, want[i])
		}
	}
}

func TestHeavyProgressEventsCloseBracketOnCancelFaultAndIgnoreStoreErrors(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "cancel-me")

	// 1. Cancellation closes the started bracket with phase="ended", outcome="cancelled".
	cancelStore := newHeavyProgressStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancelRunner := heavyFixture(t, root, func(ctx context.Context, _, _ string) ([]HeavyCheck, error) {
		cancel()
		return nil, ctx.Err()
	})
	cancelRunner.Store = cancelStore
	if _, err := cancelRunner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel Run err = %v, want context.Canceled", err)
	}
	gotCancel := readHeavyProgressEvents(t, cancelStore)
	wantCancel := []HeavyProgressFact{
		{TipSHA: tip, Phase: "started"},
		{TipSHA: tip, Phase: "ended", Outcome: "cancelled"},
	}
	if len(gotCancel) != len(wantCancel) || gotCancel[0].Fact != wantCancel[0] || gotCancel[1].Fact != wantCancel[1] {
		t.Fatalf("cancel events = %+v, want %+v", gotCancel, wantCancel)
	}

	// 2. Judge fault closes the started bracket with phase="ended", outcome="fault".
	faultStore := newHeavyProgressStore(t)
	faultRunner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		return nil, errors.New("runner boom")
	})
	faultRunner.Store = faultStore
	if _, err := faultRunner.Run(context.Background()); err == nil {
		t.Fatal("fault Run unexpectedly succeeded")
	}
	gotFault := readHeavyProgressEvents(t, faultStore)
	wantFault := []HeavyProgressFact{
		{TipSHA: tip, Phase: "started"},
		{TipSHA: tip, Phase: "ended", Outcome: "fault"},
	}
	if len(gotFault) != len(wantFault) || gotFault[0].Fact != wantFault[0] || gotFault[1].Fact != wantFault[1] {
		t.Fatalf("fault events = %+v, want %+v", gotFault, wantFault)
	}

	// 3. Store write failures are logged via Progress without failing the proof.
	badStoreDir := scratchUnderDurable(t)
	var logged []string
	okRunner := heavyFixture(t, root, func(context.Context, string, string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	})
	okRunner.Store = badStoreDir
	okRunner.Progress = func(line string) {
		logged = append(logged, line)
	}
	if _, err := okRunner.Run(context.Background()); err != nil {
		t.Fatalf("Run with unwritable store path failed proof: %v", err)
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
		t.Fatalf("green ref = %s, want %s", green, tip)
	}
	if len(logged) == 0 {
		t.Fatal("want progress log diagnostic when store open fails")
	}
}

func TestHeavyProgressEventsEmitRunningForShardedGoChecksAndStallReplay(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	storePath := newHeavyProgressStore(t)
	work := scratchUnderDurable(t)

	enteredRolling := filepath.Join(work, "entered-rolling")
	releaseRolling := filepath.Join(work, "release-rolling")
	enteredReplay := filepath.Join(work, "entered-replay")
	releaseReplay := filepath.Join(work, "release-replay")

	if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", ".keep"), "")
	transport, err := os.ReadFile(filepath.Join("..", "..", "gate", "classify.py"))
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "gate", "classify.py"), string(transport))

	// Fake tools/go-test-shards.py that writes monotonic progress snapshots and
	// waits for release files so the test can observe live store events.
	writeForkFile(t, filepath.Join(root, "tools", "go-test-shards.py"), `import argparse
import json
import os
import sys
import time
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("module")
parser.add_argument("-k", type=int, default=6)
parser.add_argument("--cache", action="store_true")
parser.add_argument("--slot-class", default="default")
parser.add_argument("--timeout", type=int, default=0)
parser.add_argument("--events", type=Path, default=None)
parser.add_argument("--progress-file", type=Path, default=None)
args = parser.parse_args()

progress_path = args.progress_file or (Path(os.environ["TILLR_GO_SHARDS_PROGRESS_OUT"]) if os.environ.get("TILLR_GO_SHARDS_PROGRESS_OUT") else None)
mode = os.environ.get("FAKE_SHARD_MODE", "rolling")
if mode == "rolling":
    if progress_path is not None:
        progress_path.write_text(json.dumps({"seq": 1, "completed_units": 3}))
    Path(`+jsonString(enteredRolling)+`).touch()
    while not Path(`+jsonString(releaseRolling)+`).exists():
        time.sleep(0.02)
    # Repeat same completed_units with higher seq, and same seq with higher units:
    # neither should emit an extra running event.
    if progress_path is not None:
        progress_path.write_text(json.dumps({"seq": 2, "completed_units": 3}))
        time.sleep(0.05)
        progress_path.write_text(json.dumps({"seq": 1, "completed_units": 4}))
        time.sleep(0.05)
    sys.exit(0)
if mode == "stall":
    # Emit no snapshot on stalled first run.
    sys.exit(0)
if progress_path is not None:
    progress_path.write_text(json.dumps({"seq": 1, "completed_units": 7}))
Path(`+jsonString(enteredReplay)+`).touch()
while not Path(`+jsonString(releaseReplay)+`).exists():
    time.sleep(0.02)
sys.exit(0)
`)

	source, err := os.ReadFile(filepath.Join("..", "..", "fork-check.py"))
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(strings.Replace(string(source), "if __name__ == \"__main__\":\n    sys.exit(main(sys.argv[1:]))", "", 1))
	source = append(source, []byte(`
def main(argv):
    import os
    module = "tools/tillr-drain"
    check_id = declaration_id("go-tool", module + "'s whole suite passes", Path(__file__).resolve().parents[1])
    if "--registry-json" in argv:
        print(json.dumps({"declarations": [{"id": check_id, "kind": "go-tool", "label": module + "'s whole suite passes", "watches": [{"path": "value"}]}]}))
        return 0
    if "--only" in argv:
        cmd = go_tool_command(module)
        env = dict(os.environ, FAKE_SHARD_MODE="replay")
        rc = subprocess.run(["sh", "-c", cmd], env=env).returncode
        return rc
    root = Path(__file__).resolve().parents[1]
    plan = Plan()
    c = Check("go-tool", module + "'s whole suite passes", ["sh", "-c", go_tool_command(module)], "go-tool", 0)
    plan.checks = [c]
    run_plan_checks(plan, root, None, 1)
    raw = {"check_spans": [{"check_id": check_id, "kind": c.kind, "label": c.label, "status": check_status(c), "attributes_json": "{}"}], "skipped_checks": []}
    Path(os.environ["TILLR_GATE_SPANS_OUT"]).write_text(json.dumps(raw))
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
`)...)
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), string(source))
	tip := heavyCommit(t, root, "sharded-go-live")

	// Verify wholeStepCount() ignores "running" and counts only completed whole checks.
	spCtx, sp := (HeavyRunner{Store: storePath}).StartStoreProgress(context.Background(), tip)
	heavyStoreStepProgress(spCtx, "go-tool/tools-tillr-drain-s-whole-suite-passes", "running")
	if got := sp.WholeStepCount(); got != 0 {
		t.Fatalf("wholeStepCount after running = %d, want 0", got)
	}
	heavyStoreWholeStepProgress(spCtx, "go-tool/tools-tillr-drain-s-whole-suite-passes", "pass")
	if got := sp.WholeStepCount(); got != 1 {
		t.Fatalf("wholeStepCount after whole pass = %d, want 1", got)
	}
	// Reset store for the live runner tests.
	storePath = newHeavyProgressStore(t)

	steps := &ProcessSteps{
		Root:           root,
		LaneRoot:       work,
		RollingProgram: "python3 tools/fork-check.py {since} --tier rolling",
		ReplayProgram:  "python3 tools/fork-check.py --only {only}",
	}
	runner := heavyFixture(t, root, steps.JudgeHeavy)
	runner.Store = storePath

	done := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background())
		done <- err
	}()
	defer func() {
		_ = os.WriteFile(releaseRolling, nil, 0o644)
	}()

	checkID := "go-tool/tools-tillr-drain-s-whole-suite-passes"
	deadline := time.After(20 * time.Second)
	for {
		if _, err := os.Stat(enteredRolling); err == nil {
			events := readHeavyProgressEvents(t, storePath)
			sawRunning := false
			for _, ev := range events {
				if ev.Fact.Phase == "ended" {
					t.Fatalf("saw premature ended while sharded check is still running: %+v", events)
				}
				if ev.Fact == (HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"}) {
					sawRunning = true
				}
			}
			if sawRunning {
				break
			}
		}
		select {
		case err := <-done:
			t.Fatalf("heavy exited before rolling check was released: %v", err)
		case <-deadline:
			t.Fatalf("timed out waiting for live running progress event; events=%+v", readHeavyProgressEvents(t, storePath))
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := os.WriteFile(releaseRolling, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("sharded rolling Run: %v", err)
	}

	events := readHeavyProgressEvents(t, storePath)
	want := []HeavyProgressFact{
		{TipSHA: tip, Phase: "started"},
		{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"},
		{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "pass"},
		{TipSHA: tip, Phase: "ended", Outcome: "green"},
	}
	if len(events) != len(want) {
		t.Fatalf("rolling sharded events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i].Fact != want[i] {
			t.Fatalf("rolling sharded event[%d] = %+v, want %+v", i, events[i].Fact, want[i])
		}
	}

	// Now test ReplayProgram (only != "") via stall retry on a new tip.
	tip2 := heavyCommit(t, root, "sharded-go-replay")
	replayStore := newHeavyProgressStore(t)
	stalledOnce := false
	replayRunner := heavyFixture(t, root, func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		if only == "" && !stalledOnce {
			stalledOnce = true
			return []HeavyCheck{{CheckID: checkID, Result: "stalled"}}, nil
		}
		return steps.JudgeHeavy(ctx, dir, only)
	})
	replayRunner.Store = replayStore

	doneReplay := make(chan error, 1)
	go func() {
		_, err := replayRunner.Run(context.Background())
		doneReplay <- err
	}()
	defer func() {
		_ = os.WriteFile(releaseReplay, nil, 0o644)
	}()

	deadline = time.After(20 * time.Second)
	for {
		if _, err := os.Stat(enteredReplay); err == nil {
			evList := readHeavyProgressEvents(t, replayStore)
			sawRunning := false
			for _, ev := range evList {
				if ev.Fact.Phase == "ended" {
					t.Fatalf("saw premature ended during replay: %+v", evList)
				}
				if ev.Fact == (HeavyProgressFact{TipSHA: tip2, Phase: "progress", CheckID: checkID, Outcome: "running"}) {
					sawRunning = true
				}
			}
			if sawRunning {
				break
			}
		}
		select {
		case err := <-doneReplay:
			t.Fatalf("replay exited before release: %v", err)
		case <-deadline:
			t.Fatalf("timed out waiting for live replay running event; events=%+v", readHeavyProgressEvents(t, replayStore))
		case <-time.After(20 * time.Millisecond):
		}
	}

	if err := os.WriteFile(releaseReplay, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-doneReplay; err != nil {
		t.Fatalf("replay Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(work, ".rolling", "heavy-replay-progress.json")); !os.IsNotExist(err) {
		t.Fatalf("expected replay progress file to be unlinked, stat err = %v", err)
	}

	replayEvents := readHeavyProgressEvents(t, replayStore)
	wantReplay := []HeavyProgressFact{
		{TipSHA: tip2, Phase: "started"},
		{TipSHA: tip2, Phase: "progress", CheckID: checkID, Outcome: "stalled"},
		{TipSHA: tip2, Phase: "progress", CheckID: checkID, Outcome: "running"},
		{TipSHA: tip2, Phase: "progress", CheckID: checkID, Outcome: "pass"},
		{TipSHA: tip2, Phase: "ended", Outcome: "green"},
	}
	if len(replayEvents) != len(wantReplay) {
		t.Fatalf("replay events = %+v, want %+v", replayEvents, wantReplay)
	}
	for i := range wantReplay {
		if replayEvents[i].Fact != wantReplay[i] {
			t.Fatalf("replay event[%d] = %+v, want %+v", i, replayEvents[i].Fact, wantReplay[i])
		}
	}
}

func TestHeavyProgressEventsSilentWhenUnitsDoNotAdvance(t *testing.T) {
	storePath := newHeavyProgressStore(t)
	work := scratchUnderDurable(t)
	progressPath := filepath.Join(work, "replay-progress.json")

	// Start with invalid/boolean and stalled snapshots; none should emit "running".
	if err := os.WriteFile(progressPath, []byte(`{"seq": true, "completed_units": 5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, sp := (HeavyRunner{Store: storePath}).StartStoreProgress(context.Background(), "tip-hang")
	stop := startHeavyReplayProgressWatcher(ctx, progressPath, "go-tool/tools-tillr-drain-s-whole-suite-passes")
	time.Sleep(80 * time.Millisecond)

	// Valid snapshot emits once.
	if err := os.WriteFile(progressPath, []byte(`{"seq": 1, "completed_units": 5}`), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)

	// Repeating same (seq, completed_units) or advancing only one of the two emits nothing more.
	for _, payload := range []string{
		`{"seq": 1, "completed_units": 5}`,
		`{"seq": 2, "completed_units": 5}`,
		`{"seq": 1, "completed_units": 6}`,
	} {
		if err := os.WriteFile(progressPath, []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Millisecond)
	}
	stop()
	sp.Finish("green")

	events := readHeavyProgressEvents(t, storePath)
	want := []HeavyProgressFact{
		{TipSHA: "tip-hang", Phase: "started"},
		{TipSHA: "tip-hang", Phase: "progress", CheckID: "go-tool/tools-tillr-drain-s-whole-suite-passes", Outcome: "running"},
		{TipSHA: "tip-hang", Phase: "ended", Outcome: "green"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v, want %+v", events, want)
	}
	for i := range want {
		if events[i].Fact != want[i] {
			t.Fatalf("event[%d] = %+v, want %+v", i, events[i].Fact, want[i])
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
