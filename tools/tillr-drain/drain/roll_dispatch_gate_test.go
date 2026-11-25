package drain

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

type rollDispatchTestSteps struct {
	Steps
	root        string
	binDir      string
	adapterPath string
	storePath   string
	mu          sync.Mutex
	runs        []run.Record
	itemRuns    map[string]int
}

func (s *rollDispatchTestSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	cmd := exec.CommandContext(ctx, s.adapterPath)
	cmd.Dir = s.root
	cmd.Env = append(os.Environ(), "PATH="+s.binDir+":"+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	rec := run.Record{
		RunID:      req.RunID,
		ItemID:     req.ItemID,
		LaneID:     req.LaneID,
		Backend:    req.Backend,
		State:      run.StateExited,
		ExitCode:   code(exitCode),
		StderrTail: stderr.String(),
		StartedAt:  "2026-11-25T15:37:00Z",
		FinishedAt: "2026-11-25T15:37:01Z",
	}
	s.mu.Lock()
	s.runs = append(s.runs, rec)
	if s.itemRuns == nil {
		s.itemRuns = map[string]int{}
	}
	s.itemRuns[req.ItemID]++
	s.mu.Unlock()
	if db, openErr := store.Open(ctx, s.storePath, true); openErr == nil {
		_ = db.WriteTx(ctx, func(tx *store.Tx) error {
			return applyTerminal(tx, rec)
		})
		db.Close()
	}
	if exitCode == 0 {
		if f, err := os.Create(filepath.Join(req.WorkingDir, "candidate.txt")); err == nil {
			_, _ = f.WriteString(req.ItemID + "\n")
			f.Close()
		}
		_, _ = actionCommand(ctx, req.WorkingDir, "git", "add", "candidate.txt")
		_, _ = actionCommand(ctx, req.WorkingDir, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "candidate "+req.ItemID)
	}
	return rec, stdout.String(), nil
}

func (s *rollDispatchTestSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	if c.ItemID == "item-a-roll" {
		// Land a change that adds a face under tools/tillr-run/ and updates the adapter script to call it.
		newAdapter := "#!/bin/sh\nfake-tillr-run new-helper\n"
		if err := os.WriteFile(s.adapterPath, []byte(newAdapter), 0755); err != nil {
			return LandOutcome{}, err
		}
		newFaceRel := filepath.Join("tools", "tillr-run", "new_helper.go")
		if err := os.WriteFile(filepath.Join(s.root, newFaceRel), []byte("package main\n"), 0644); err != nil {
			return LandOutcome{}, err
		}
		adapterRel, err := filepath.Rel(s.root, s.adapterPath)
		if err != nil {
			return LandOutcome{}, err
		}
		if _, err := actionCommand(ctx, s.root, "git", "add", adapterRel, newFaceRel); err != nil {
			return LandOutcome{}, err
		}
		if _, err := actionCommand(ctx, s.root, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "add new-helper face and adapter call"); err != nil {
			return LandOutcome{}, err
		}
	}
	head, err := actionCommand(ctx, s.root, "git", "rev-parse", "HEAD")
	if err != nil {
		return LandOutcome{}, err
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: strings.TrimSpace(head)}, nil
}

func (s *rollDispatchTestSteps) countFor(itemID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.itemRuns[itemID]
}

func TestLandingAddsFaceAndAdapterCallDispatchesDuringRollWithNoFailureOrAlarm(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "item-a-roll", "item-b-after")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.Backends = map[string]Backend{"fixture": {Adapter: "fixture"}}
	holdWALKeeper(t, f.storePath)
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "fake-tillr-run")
	oldBinScript := `#!/bin/sh
if [ "$1" = "new-helper" ]; then
  echo 'tillr-run: unknown face "new-helper": want exec, pause' >&2
  exit 2
fi
exit 0
`
	if err := os.WriteFile(fakeBin, []byte(oldBinScript), 0755); err != nil {
		t.Fatal(err)
	}

	adapterRel := filepath.Join("tools", "tillr-run", "backends", "fake", "run-fake")
	adapterAbs := filepath.Join(f.root, adapterRel)
	if err := os.MkdirAll(filepath.Dir(adapterAbs), 0755); err != nil {
		t.Fatal(err)
	}
	oldAdapter := "#!/bin/sh\nfake-tillr-run exec\n"
	if err := os.WriteFile(adapterAbs, []byte(oldAdapter), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := actionCommand(ctx, f.root, "git", "add", adapterRel); err != nil {
		t.Fatal(err)
	}
	if _, err := actionCommand(ctx, f.root, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "initial adapter"); err != nil {
		t.Fatal(err)
	}

	rollStarted := filepath.Join(t.TempDir(), "roll.started")
	rollRelease := filepath.Join(t.TempDir(), "roll.release")
	newBinScript := "#!/bin/sh\nexit 0\n"
	newBinSrc := filepath.Join(t.TempDir(), "new-fake-tillr-run")
	if err := os.WriteFile(newBinSrc, []byte(newBinScript), 0755); err != nil {
		t.Fatal(err)
	}

	steps := &rollDispatchTestSteps{
		Steps:       l.cfg.Steps,
		root:        f.root,
		binDir:      binDir,
		adapterPath: adapterAbs,
		storePath:   f.storePath,
	}
	l.cfg.Steps = steps
	l.cfg.ExitWhenDrained = true
	l.cfg.StorePollInterval = 25 * time.Millisecond
	l.cfg.PostLanding.Roll = settings.Roll{
		Paths: []string{"tools/tillr-run/"},
		Command: fmt.Sprintf(
			`touch %q; while [ ! -f %q ]; do sleep 0.01; done; cp %q %q && chmod +x %q`,
			rollStarted, rollRelease, newBinSrc, fakeBin, fakeBin,
		),
	}

	// Run the full loop: item-roll lands the new face + adapter call and starts the roll;
	// item-after is queued right behind it and must not dispatch until the roll finishes.
	runDone := make(chan error, 1)
	go func() {
		_, err := l.Run(ctx)
		runDone <- err
	}()

	// Wait until item-roll has landed and the roll command has started.
	deadline := time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(rollStarted); err == nil {
			break
		}
		select {
		case err := <-runDone:
			t.Fatalf("Loop.Run exited before roll started: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for post-landing roll to start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// While the roll is in progress, give the warm loop plenty of time to try dispatching item-b-after.
	time.Sleep(300 * time.Millisecond)
	if got := steps.countFor("item-b-after"); got != 0 {
		_ = os.WriteFile(rollRelease, nil, 0600)
		waitAction(t, &l.rollAction)
		steps.mu.Lock()
		runs := append([]run.Record(nil), steps.runs...)
		steps.mu.Unlock()
		t.Fatalf("dispatched item-b-after %d time(s) while post-landing roll was still running; want 0; runs=%+v", got, runs)
	}

	// Release the roll so the new binary is installed and the roll completes.
	if err := os.WriteFile(rollRelease, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitAction(t, &l.rollAction)

	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Loop.Run: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("Loop.Run did not finish after roll completed: %v", ctx.Err())
	}

	steps.mu.Lock()
	runs := append([]run.Record(nil), steps.runs...)
	steps.mu.Unlock()
	if steps.countFor("item-b-after") == 0 {
		t.Fatal("item-b-after was never dispatched after roll finished")
	}
	for _, rec := range runs {
		if rec.ExitCode == nil || *rec.ExitCode != 0 {
			t.Fatalf("unexpected failed run %s: exit=%v stderr=%q", rec.RunID, rec.ExitCode, rec.StderrTail)
		}
	}

	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatalf("RecordBackendOutcomes: %v", err)
	}
	db, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		closed, err := readInfrastructureClosed(r)
		if err != nil {
			return err
		}
		if len(closed) != 0 {
			t.Fatalf("infrastructure closed backends = %v, want none", closed)
		}
		active, err := alarms.Read(r, l.cfg.Now(), nil)
		if err != nil {
			return err
		}
		for _, a := range active {
			if a.Kind == alarms.Closed {
				t.Fatalf("unexpected backend-closed alarm: %s", a)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownFaceDuringPendingRollDoesNotCloseBackend(t *testing.T) {
	f, l := passFixture(t, "item-a")
	ctx := context.Background()

	rollStarted := filepath.Join(t.TempDir(), "roll.started")
	rollRelease := filepath.Join(t.TempDir(), "roll.release")
	l.cfg.PostLanding.Roll = settings.Roll{
		Paths:   []string{"tools/tillr-run/"},
		Command: fmt.Sprintf(`touch %q; while [ ! -f %q ]; do sleep 0.01; done`, rollStarted, rollRelease),
	}

	// Commit a change touching a roll path and trigger the post-landing roll.
	rollFile := filepath.Join(f.root, "tools", "tillr-run", "face.go")
	if err := os.MkdirAll(filepath.Dir(rollFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := actionCommand(ctx, f.root, "git", "add", "tools/tillr-run/face.go"); err != nil {
		t.Fatal(err)
	}
	if _, err := actionCommand(ctx, f.root, "git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "add face"); err != nil {
		t.Fatal(err)
	}
	tip, err := actionCommand(ctx, f.root, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	l.afterLanding(ctx, strings.TrimSpace(tip))
	defer func() {
		_ = os.WriteFile(rollRelease, nil, 0600)
		waitAction(t, &l.rollAction)
	}()
	waitActionCondition(t, &l.rollAction, func() bool {
		_, err := os.Stat(rollStarted)
		return err == nil
	})

	// 1. A run fails with unknown face while the roll is pending/running: must NOT close the backend.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:      "run-unknown-during-roll",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "fixture",
			State:      run.StateExited,
			ExitCode:   code(2),
			StderrTail: `tillr-run: unknown face "pi-stop-at-gap": want exec, pause`,
			StartedAt:  "2026-11-25T15:38:00Z",
			FinishedAt: "2026-11-25T15:38:01Z",
		})
	})
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatalf("RecordBackendOutcomes: %v", err)
	}
	checkClosed := func(wantClosed bool, label string) {
		t.Helper()
		db, err := store.OpenRead(ctx, f.storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		if err := db.ReadTx(ctx, func(r *store.Reader) error {
			closed, err := readInfrastructureClosed(r)
			if err != nil {
				return err
			}
			if closed["fixture"] != wantClosed {
				t.Fatalf("%s: readInfrastructureClosed[fixture] = %v, want %v", label, closed["fixture"], wantClosed)
			}
			active, err := alarms.Read(r, l.cfg.Now(), nil)
			if err != nil {
				return err
			}
			hasAlarm := false
			for _, a := range active {
				if a.Kind == alarms.Closed && a.Backend == "fixture" {
					hasAlarm = true
				}
			}
			if hasAlarm != wantClosed {
				t.Fatalf("%s: backend-closed alarm = %v, want %v", label, hasAlarm, wantClosed)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	checkClosed(false, "unknown face while roll running")

	// 2. A non-unknown-face failure on another backend while the roll is running DOES close that backend.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:      "run-other-infra-during-roll",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "other",
			State:      run.StateExited,
			ExitCode:   code(2),
			StderrTail: "connection refused",
			StartedAt:  "2026-11-25T15:38:02Z",
			FinishedAt: "2026-11-25T15:38:03Z",
		})
	})
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatalf("RecordBackendOutcomes: %v", err)
	}
	db, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReadTx(ctx, func(r *store.Reader) error {
		closed, err := readInfrastructureClosed(r)
		if err != nil {
			return err
		}
		if !closed["other"] {
			t.Fatal("non-unknown-face failure during roll did not close backend other")
		}
		return nil
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	// 3. Release the roll so it finishes. The earlier unknown-face run must still not close fixture.
	if err := os.WriteFile(rollRelease, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitAction(t, &l.rollAction)
	checkClosed(false, "after roll finished for earlier unknown-face run")

	// 4. After the roll has finished, a new unknown-face failure DOES close the backend.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, run.Record{
			RunID:      "run-unknown-after-roll",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "fixture",
			State:      run.StateExited,
			ExitCode:   code(2),
			StderrTail: `tillr-run: unknown face "pi-stop-at-gap": want exec, pause`,
			StartedAt:  "2026-11-25T15:39:00Z",
			FinishedAt: "2026-11-25T15:39:01Z",
		})
	})
	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatalf("RecordBackendOutcomes: %v", err)
	}
	checkClosed(true, "unknown face after roll finished")
}
