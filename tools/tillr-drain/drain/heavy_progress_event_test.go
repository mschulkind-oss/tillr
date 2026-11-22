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
