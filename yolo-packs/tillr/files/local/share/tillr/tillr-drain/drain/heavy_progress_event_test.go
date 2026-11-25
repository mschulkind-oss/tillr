package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	if last := redEvents[len(redEvents)-1].Fact; last != (HeavyProgressFact{TipSHA: tipRed, Phase: "ended", Outcome: "red", FailingChecks: "unit"}) {
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

func TestHeavyProgressEventsWatchDeclarationCheckLogGrowth(t *testing.T) {
	t.Run("growing-log", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		storePath := newHeavyProgressStore(t)
		work := scratchUnderDurable(t)

		checkID := "go-test-shards-test"
		if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", checkID+".json"),
			`{"id":"`+checkID+`","scope":"tree","tier":"heavy","reads":["value"],"command":["true"]}`)
		tip := heavyCommit(t, root, "decl-grow")

		startedFile := filepath.Join(work, "started")
		growFile := filepath.Join(work, "grow")
		releaseFile := filepath.Join(work, "release")

		fakeGate := executable(t, `
logs=""
only=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--logs) logs="$2"; shift 2 ;;
		--only) only="$2"; shift 2 ;;
		*) shift ;;
	esac
done
mkdir -p "$logs"
log="$logs/00-${only}.log"
printf 'watchdog CPU source: kernel task-clock counter\n' > "$log"
touch `+startedFile+`
while [ ! -e `+growFile+` ]; do sleep 0.01; done
while [ ! -e `+releaseFile+` ]; do
	printf '.' >> "$log"
	sleep 0.02
done
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass","duration_ms":50}]}}\n' "$only"
`)

		steps := &ProcessSteps{
			Root:                   root,
			LaneRoot:               work,
			HeavyGateBin:           fakeGate,
			heavyLogSampleInterval: 20 * time.Millisecond,
		}
		runner := heavyFixture(t, root, steps.JudgeHeavy)
		runner.Store = storePath

		done := make(chan error, 1)
		go func() {
			_, err := runner.Run(context.Background())
			done <- err
		}()
		defer func() {
			_ = os.WriteFile(growFile, nil, 0o644)
			_ = os.WriteFile(releaseFile, nil, 0o644)
		}()

		deadline := time.After(hangGuard)
		for {
			if _, err := os.Stat(startedFile); err == nil {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("heavy exited early waiting for %s: %v", startedFile, err)
			case <-deadline:
				t.Fatalf("timed out waiting for %s", startedFile)
			case <-time.After(10 * time.Millisecond):
			}
		}

		// Initial log creation/header alone must not emit a running progress event.
		time.Sleep(80 * time.Millisecond)
		for _, ev := range readHeavyProgressEvents(t, storePath) {
			if ev.Fact.Phase == "progress" {
				t.Fatalf("saw unexpected progress event before log grew: %+v", ev.Fact)
			}
		}

		// Grow the log and wait for a live running progress event before completion.
		if err := os.WriteFile(growFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		deadline = time.After(hangGuard)
		for {
			events := readHeavyProgressEvents(t, storePath)
			sawRunning := false
			for _, ev := range events {
				if ev.Fact.Phase == "ended" {
					t.Fatalf("saw premature ended before check was released: %+v", events)
				}
				if ev.Fact == (HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"}) {
					sawRunning = true
				}
			}
			if sawRunning {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("heavy exited before release: %v", err)
			case <-deadline:
				t.Fatalf("timed out waiting for live log-growth progress event; events=%+v", readHeavyProgressEvents(t, storePath))
			case <-time.After(20 * time.Millisecond):
			}
		}

		if err := os.WriteFile(releaseFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}

		events := readHeavyProgressEvents(t, storePath)
		if len(events) < 4 {
			t.Fatalf("events = %+v, want started, >=1 running progress, pass progress, ended", events)
		}
		if want := (HeavyProgressFact{TipSHA: tip, Phase: "started"}); events[0].Fact != want {
			t.Fatalf("event[0] = %+v, want %+v", events[0].Fact, want)
		}
		runningFact := HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"}
		for i := 1; i < len(events)-2; i++ {
			if events[i].Fact != runningFact {
				t.Fatalf("event[%d] = %+v, want %+v", i, events[i].Fact, runningFact)
			}
		}
		if want := (HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "pass"}); events[len(events)-2].Fact != want {
			t.Fatalf("completion event = %+v, want %+v", events[len(events)-2].Fact, want)
		}
		if want := (HeavyProgressFact{TipSHA: tip, Phase: "ended", Outcome: "green"}); events[len(events)-1].Fact != want {
			t.Fatalf("final event = %+v, want %+v", events[len(events)-1].Fact, want)
		}
	})

	t.Run("silent-log", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		storePath := newHeavyProgressStore(t)
		work := scratchUnderDurable(t)

		checkID := "silent-check"
		if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", checkID+".json"),
			`{"id":"`+checkID+`","scope":"tree","tier":"heavy","reads":["value"],"command":["true"]}`)
		tip := heavyCommit(t, root, "decl-silent")

		startedFile := filepath.Join(work, "started")
		releaseFile := filepath.Join(work, "release")

		fakeGate := executable(t, `
logs=""
only=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--logs) logs="$2"; shift 2 ;;
		--only) only="$2"; shift 2 ;;
		*) shift ;;
	esac
done
mkdir -p "$logs"
log="$logs/00-${only}.log"
printf 'watchdog CPU source: kernel task-clock counter\n' > "$log"
touch `+startedFile+`
while [ ! -e `+releaseFile+` ]; do sleep 0.01; done
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass","duration_ms":50}]}}\n' "$only"
`)

		steps := &ProcessSteps{
			Root:                   root,
			LaneRoot:               work,
			HeavyGateBin:           fakeGate,
			heavyLogSampleInterval: 20 * time.Millisecond,
		}
		runner := heavyFixture(t, root, steps.JudgeHeavy)
		runner.Store = storePath

		done := make(chan error, 1)
		go func() {
			_, err := runner.Run(context.Background())
			done <- err
		}()
		defer func() {
			_ = os.WriteFile(releaseFile, nil, 0o644)
		}()

		deadline := time.After(hangGuard)
		for {
			if _, err := os.Stat(startedFile); err == nil {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("heavy exited early: %v", err)
			case <-deadline:
				t.Fatal("timed out waiting for startedFile")
			case <-time.After(10 * time.Millisecond):
			}
		}

		// Wait across several sample intervals while log size stays flat.
		time.Sleep(100 * time.Millisecond)
		for _, ev := range readHeavyProgressEvents(t, storePath) {
			if ev.Fact.Phase == "progress" {
				t.Fatalf("silent log emitted progress event: %+v", ev.Fact)
			}
		}

		if err := os.WriteFile(releaseFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}

		events := readHeavyProgressEvents(t, storePath)
		want := []HeavyProgressFact{
			{TipSHA: tip, Phase: "started"},
			{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "pass"},
			{TipSHA: tip, Phase: "ended", Outcome: "green"},
		}
		if len(events) != len(want) {
			t.Fatalf("events = %+v, want %+v", events, want)
		}
		for i := range want {
			if events[i].Fact != want[i] {
				t.Fatalf("event[%d] = %+v, want %+v", i, events[i].Fact, want[i])
			}
		}
	})
}

func TestHeavyProgressEventsWatchDeclarationCheckShardProgress(t *testing.T) {
	t.Run("advancing-snapshots", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		storePath := newHeavyProgressStore(t)
		work := scratchUnderDurable(t)

		checkID := "go-test-tillr-drain"
		if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", checkID+".json"),
			`{"id":"`+checkID+`","scope":"tree","tier":"heavy","reads":["value"],"command":["true"]}`)
		tip := heavyCommit(t, root, "decl-shards-progress")

		startedFile := filepath.Join(work, "started")
		step1File := filepath.Join(work, "step1")
		step2File := filepath.Join(work, "step2")
		releaseFile := filepath.Join(work, "release")
		envRecord := filepath.Join(work, "env-progress-path")

		fakeGate := executable(t, `
logs=""
only=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--logs) logs="$2"; shift 2 ;;
		--only) only="$2"; shift 2 ;;
		*) shift ;;
	esac
done
mkdir -p "$logs"
# Keep the check log completely flat, matching go-test-shards.py while shards run.
: > "$logs/00-${only}.log"
printf '%s' "${TILLR_GO_SHARDS_PROGRESS_OUT:-}" > `+envRecord+`
touch `+startedFile+`
while [ ! -e `+step1File+` ]; do sleep 0.01; done
if [ -n "${TILLR_GO_SHARDS_PROGRESS_OUT:-}" ]; then
	printf '{"seq":1,"completed_units":3,"completed_tests":3}\n' > "$TILLR_GO_SHARDS_PROGRESS_OUT"
fi
while [ ! -e `+step2File+` ]; do sleep 0.01; done
if [ -n "${TILLR_GO_SHARDS_PROGRESS_OUT:-}" ]; then
	printf '{"seq":2,"completed_units":7,"completed_tests":7}\n' > "$TILLR_GO_SHARDS_PROGRESS_OUT"
fi
while [ ! -e `+releaseFile+` ]; do sleep 0.01; done
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass","duration_ms":50}]}}\n' "$only"
`)

		steps := &ProcessSteps{
			Root:                   root,
			LaneRoot:               work,
			HeavyGateBin:           fakeGate,
			heavyLogSampleInterval: 20 * time.Millisecond,
		}
		runner := heavyFixture(t, root, steps.JudgeHeavy)
		runner.Store = storePath

		done := make(chan error, 1)
		go func() {
			_, err := runner.Run(context.Background())
			done <- err
		}()
		defer func() {
			_ = os.WriteFile(step1File, nil, 0o644)
			_ = os.WriteFile(step2File, nil, 0o644)
			_ = os.WriteFile(releaseFile, nil, 0o644)
		}()

		waitFor := func(path string) {
			t.Helper()
			deadline := time.After(hangGuard)
			for {
				if _, err := os.Stat(path); err == nil {
					return
				}
				select {
				case err := <-done:
					t.Fatalf("heavy exited early waiting for %s: %v", path, err)
				case <-deadline:
					t.Fatalf("timed out waiting for %s", path)
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
		waitForRunningCount := func(want int) {
			t.Helper()
			deadline := time.After(hangGuard)
			for {
				events := readHeavyProgressEvents(t, storePath)
				count := 0
				for _, ev := range events {
					if ev.Fact.Phase == "ended" {
						t.Fatalf("saw premature ended before check was released: %+v", events)
					}
					if ev.Fact == (HeavyProgressFact{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"}) {
						count++
					}
				}
				if count >= want {
					return
				}
				select {
				case err := <-done:
					t.Fatalf("heavy exited before release: %v", err)
				case <-deadline:
					t.Fatalf("timed out waiting for %d live running progress events; events=%+v", want, events)
				case <-time.After(20 * time.Millisecond):
				}
			}
		}

		waitFor(startedFile)

		rawPath, err := os.ReadFile(envRecord)
		if err != nil {
			t.Fatalf("read recorded progress path: %v", err)
		}
		progressPath := string(rawPath)
		if progressPath == "" || !filepath.IsAbs(progressPath) {
			t.Fatalf("TILLR_GO_SHARDS_PROGRESS_OUT = %q, want non-empty absolute path", progressPath)
		}

		if err := os.WriteFile(step1File, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		waitForRunningCount(1)

		if err := os.WriteFile(step2File, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		waitForRunningCount(2)

		if err := os.WriteFile(releaseFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := os.Stat(progressPath); !os.IsNotExist(err) {
			t.Fatalf("temporary progress file %q was not removed: err=%v", progressPath, err)
		}

		events := readHeavyProgressEvents(t, storePath)
		want := []HeavyProgressFact{
			{TipSHA: tip, Phase: "started"},
			{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"},
			{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "running"},
			{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "pass"},
			{TipSHA: tip, Phase: "ended", Outcome: "green"},
		}
		if len(events) != len(want) {
			t.Fatalf("events = %+v, want %+v", events, want)
		}
		for i := range want {
			if events[i].Fact != want[i] {
				t.Fatalf("event[%d] = %+v, want %+v", i, events[i].Fact, want[i])
			}
		}
	})

	t.Run("stalled-snapshots", func(t *testing.T) {
		root, base := heavyRepo(t)
		heavyGit(t, root, "update-ref", GreenRef, base)
		storePath := newHeavyProgressStore(t)
		work := scratchUnderDurable(t)

		checkID := "go-test-tillr-drain"
		if err := os.MkdirAll(filepath.Join(root, "tools", "gate", "checks"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(root, "tools", "gate", "checks", checkID+".json"),
			`{"id":"`+checkID+`","scope":"tree","tier":"heavy","reads":["value"],"command":["true"]}`)
		tip := heavyCommit(t, root, "decl-shards-stalled")

		startedFile := filepath.Join(work, "started")
		releaseFile := filepath.Join(work, "release")

		fakeGate := executable(t, `
logs=""
only=""
while [ "$#" -gt 0 ]; do
	case "$1" in
		--logs) logs="$2"; shift 2 ;;
		--only) only="$2"; shift 2 ;;
		*) shift ;;
	esac
done
mkdir -p "$logs"
: > "$logs/00-${only}.log"
# Write a zero-progress snapshot; a hung check completing no tests must not emit running progress.
if [ -n "${TILLR_GO_SHARDS_PROGRESS_OUT:-}" ]; then
	printf '{"seq":0,"completed_units":0,"completed_tests":0}\n' > "$TILLR_GO_SHARDS_PROGRESS_OUT"
fi
touch `+startedFile+`
while [ ! -e `+releaseFile+` ]; do sleep 0.01; done
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"pass","duration_ms":50}]}}\n' "$only"
`)

		steps := &ProcessSteps{
			Root:                   root,
			LaneRoot:               work,
			HeavyGateBin:           fakeGate,
			heavyLogSampleInterval: 20 * time.Millisecond,
		}
		runner := heavyFixture(t, root, steps.JudgeHeavy)
		runner.Store = storePath

		done := make(chan error, 1)
		go func() {
			_, err := runner.Run(context.Background())
			done <- err
		}()
		defer func() {
			_ = os.WriteFile(releaseFile, nil, 0o644)
		}()

		deadline := time.After(hangGuard)
		for {
			if _, err := os.Stat(startedFile); err == nil {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("heavy exited early: %v", err)
			case <-deadline:
				t.Fatal("timed out waiting for startedFile")
			case <-time.After(10 * time.Millisecond):
			}
		}

		time.Sleep(100 * time.Millisecond)
		for _, ev := range readHeavyProgressEvents(t, storePath) {
			if ev.Fact.Phase == "progress" {
				t.Fatalf("stalled declaration check emitted progress event: %+v", ev.Fact)
			}
		}

		if err := os.WriteFile(releaseFile, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run: %v", err)
		}

		events := readHeavyProgressEvents(t, storePath)
		want := []HeavyProgressFact{
			{TipSHA: tip, Phase: "started"},
			{TipSHA: tip, Phase: "progress", CheckID: checkID, Outcome: "pass"},
			{TipSHA: tip, Phase: "ended", Outcome: "green"},
		}
		if len(events) != len(want) {
			t.Fatalf("events = %+v, want %+v", events, want)
		}
		for i := range want {
			if events[i].Fact != want[i] {
				t.Fatalf("event[%d] = %+v, want %+v", i, events[i].Fact, want[i])
			}
		}
	})
}

func TestHeavyProgressEventsEndedFailingChecks(t *testing.T) {
	root, base := heavyRepo(t)
	heavyGit(t, root, "update-ref", GreenRef, base)
	tip := heavyCommit(t, root, "multi-red")

	storePath := newHeavyProgressStore(t)
	runner := heavyFixture(t, root, func(_ context.Context, _ string, only string) ([]HeavyCheck, error) {
		if only != "" {
			return []HeavyCheck{{CheckID: only, Result: "fail"}}, nil
		}
		return []HeavyCheck{
			{CheckID: "unit", Result: "fail"},
			{CheckID: "lint", Result: "pass"},
			{CheckID: "", Result: "fail"},
			{CheckID: "vet", Result: "fail"},
			{CheckID: "unit", Result: "fail"},
		}, nil
	})
	runner.Store = storePath
	out, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Runs) != 1 || out.Runs[0].Result != "red" {
		t.Fatalf("Run result = %+v", out.Runs)
	}
	events := readHeavyProgressEvents(t, storePath)
	if len(events) == 0 {
		t.Fatal("expected heavy progress events")
	}
	last := events[len(events)-1].Fact
	wantLast := HeavyProgressFact{TipSHA: tip, Phase: "ended", Outcome: "red", FailingChecks: "unit, vet"}
	if last != wantLast {
		t.Fatalf("ended fact = %+v, want %+v", last, wantLast)
	}

	// Non-red outcomes (and red with no failing checks) omit FailingChecks.
	for _, tc := range []struct {
		outcome string
		checks  []string
	}{
		{outcome: "green", checks: []string{"unit"}},
		{outcome: "fault", checks: []string{"unit"}},
		{outcome: "cancelled", checks: []string{"unit"}},
		{outcome: "red", checks: nil},
	} {
		s := newHeavyProgressStore(t)
		_, sp := (HeavyRunner{Store: s}).StartStoreProgress(context.Background(), "tip-"+tc.outcome)
		sp.Finish(tc.outcome, tc.checks...)
		got := readHeavyProgressEvents(t, s)
		if len(got) != 2 {
			t.Fatalf("%s events = %+v, want 2", tc.outcome, got)
		}
		if got[1].Fact.FailingChecks != "" {
			t.Fatalf("%s FailingChecks = %q, want empty", tc.outcome, got[1].Fact.FailingChecks)
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
