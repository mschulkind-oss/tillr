package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/store"
)

func openHeavyTestStore(t *testing.T, work string) string {
	t.Helper()
	path := filepath.Join(work, "store.sqlite")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return path
}

func makeFakeHeavyGoGate(t *testing.T, work, wholeRunsFile string) string {
	t.Helper()
	return executable(t, `
set -eu
head=""
logs=""
only=""
worktree=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --head) head="$2"; shift 2 ;;
    --logs) logs="$2"; shift 2 ;;
    --only) only="$2"; shift 2 ;;
    --worktree) worktree="$2"; shift 2 ;;
    *) shift ;;
  esac
done
printf '%s %s\n' "$only" "$head" >> `+wholeRunsFile+`
mkdir -p "$logs"
log="$logs/00-${only}.log"
if [ -n "${FAKE_GATE_HOLD_MARKER:-}" ] && [ -n "${FAKE_GATE_HOLD_CHECK:-}" ] && [ "$only" = "$FAKE_GATE_HOLD_CHECK" ]; then
  touch "$FAKE_GATE_HOLD_MARKER"
  sleep 30
fi
if [ "$only" = "non-go-check" ]; then
  printf 'FAIL src/app.test.ts\n  x renders header (5 ms)\n' > "$log"
  printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"fail","duration_ms":5,"log_ref":"%s"}]}}\n' "$only" "$log"
  exit 1
fi
set +e
(cd "$worktree/tools/example" && CGO_ENABLED=0 go test -count=1 ./...) > "$log" 2>&1
rc=$?
set -e
res="pass"
if [ "$rc" -ne 0 ]; then
  res="fail"
fi
printf '{"kind":"gate.verdict","payload":{"checks":[{"check_id":"%s","result":"%s","duration_ms":10,"log_ref":"%s"}]}}\n' "$only" "$res" "$log"
exit "$rc"
`)
}

func readWholeRuns(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read whole runs: %v", err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func writeExampleGoSuite(t *testing.T, root, body string) {
	t.Helper()
	modDir := filepath.Join(root, "tools", "example")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(modDir, "go.mod"), "module example.com/suite\n\ngo 1.22\n")
	writeForkFile(t, filepath.Join(modDir, "suite_test.go"), body)
}

func writeGoSuiteCheckDecl(t *testing.T, root string) {
	t.Helper()
	checksDir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	decl := map[string]any{
		"id":         "go-test-example",
		"label":      "tools/example's whole Go suite passes",
		"scope":      "tree",
		"tier":       "heavy",
		"timeout_ms": 60000,
		"command":    []string{"sh", "-c", "cd tools/example && CGO_ENABLED=0 go test -count=1 ./..."},
		"reads":      []string{"tools/example/"},
		"remember":   false,
	}
	raw, err := json.Marshal(decl)
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(checksDir, "go-test-example.json"), string(raw))
}

func commitAllHeavy(t *testing.T, root, msg string) string {
	t.Helper()
	heavyGit(t, root, "add", "-A")
	heavyGit(t, root, "commit", "-m", msg)
	return heavyGit(t, root, "rev-parse", "HEAD")
}

func TestHeavyProofRunsOnlyFailingTestsUntilTheyPassThenWholeSuite(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	storePath := openHeavyTestStore(t, work)
	wholeRunsFile := filepath.Join(work, "whole-runs.log")
	gateBin := makeFakeHeavyGoGate(t, work, wholeRunsFile)

	writeGoSuiteCheckDecl(t, root)
	writeExampleGoSuite(t, root, `package suite
import "testing"
func TestPass(t *testing.T) {}
func TestFailA(t *testing.T) { t.Fatal("fail A") }
func TestFailB(t *testing.T) { t.Fatal("fail B") }
`)
	tip1 := commitAllHeavy(t, root, "break A and B")

	steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: gateBin}
	newRunner := func() HeavyRunner {
		return HeavyRunner{
			Root:  root,
			Work:  filepath.Join(work, ".rolling", "worktree"),
			Trunk: TrunkRef,
			Store: storePath,
			Lock:  loopHeld(t, root),
			Judge: steps.JudgeHeavy,
		}
	}

	// Proof 1 on tip1: no recorded failing tests -> runs whole suite -> red, records A and B.
	out1, err := newRunner().Run(context.Background())
	if err != nil {
		t.Fatalf("proof 1: %v", err)
	}
	if len(out1.Runs) != 1 || out1.Runs[0].Result != "red" {
		t.Fatalf("proof 1 want red, got %+v", out1.Runs)
	}
	if runs := readWholeRuns(t, wholeRunsFile); len(runs) != 1 {
		t.Fatalf("proof 1 whole runs = %v, want 1", runs)
	}
	wantAB := []string{
		"go-test:tools/example:example.com/suite:TestFailA",
		"go-test:tools/example:example.com/suite:TestFailB",
	}
	got, err := heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, "go-test-example")
	if err != nil || !slices.Equal(got, wantAB) {
		t.Fatalf("after proof 1 saved failing tests = %v (%v), want %v", got, err, wantAB)
	}

	// Proof 2 on same tip1: runs only A and B; whole suite must NOT be started.
	out2, err := newRunner().Run(context.Background())
	if err != nil {
		t.Fatalf("proof 2: %v", err)
	}
	if len(out2.Runs) != 1 || out2.Runs[0].Result != "red" {
		t.Fatalf("proof 2 want red, got %+v", out2.Runs)
	}
	if runs := readWholeRuns(t, wholeRunsFile); len(runs) != 1 {
		t.Fatalf("proof 2 started whole suite while failing tests still failed: %v", runs)
	}

	// Tip 2: fix TestFailA, keep TestFailB red.
	writeExampleGoSuite(t, root, `package suite
import "testing"
func TestPass(t *testing.T) {}
func TestFailA(t *testing.T) {}
func TestFailB(t *testing.T) { t.Fatal("fail B") }
`)
	tip2 := commitAllHeavy(t, root, "fix A only")

	// Proof 3 on newer tip2: runs A (pass) and B (fail); whole suite must NOT be started on tip2,
	// and saved failing tests must narrow to [TestFailB].
	out3, err := newRunner().Run(context.Background())
	if err != nil {
		t.Fatalf("proof 3: %v", err)
	}
	if len(out3.Runs) != 1 || out3.Runs[0].Result != "red" {
		t.Fatalf("proof 3 want red, got %+v", out3.Runs)
	}
	for _, line := range readWholeRuns(t, wholeRunsFile) {
		if strings.HasSuffix(line, " "+tip2) {
			t.Fatalf("proof 3 started whole suite on newer tip %s while TestFailB still failed: %v", tip2, readWholeRuns(t, wholeRunsFile))
		}
	}
	wantB := []string{"go-test:tools/example:example.com/suite:TestFailB"}
	got, err = heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, "go-test-example")
	if err != nil || !slices.Equal(got, wantB) {
		t.Fatalf("after proof 3 saved failing tests = %v (%v), want %v", got, err, wantB)
	}
	if green, gerr := heavy.Green(context.Background(), root); gerr != nil || green != "" {
		t.Fatalf("green moved prematurely to %q (%v), want empty", green, gerr)
	}

	// Tip 3: fix TestFailB, but break TestPass.
	// Focused rerun of TestFailB passes, so the whole suite MUST run and fail on TestPass;
	// passing the selected test alone must NEVER mark the check green.
	writeExampleGoSuite(t, root, `package suite
import "testing"
func TestPass(t *testing.T) { t.Fatal("new failure in whole suite") }
func TestFailA(t *testing.T) {}
func TestFailB(t *testing.T) {}
`)
	tip3 := commitAllHeavy(t, root, "fix B, break TestPass")

	out4, err := newRunner().Run(context.Background())
	if err != nil {
		t.Fatalf("proof 4: %v", err)
	}
	if len(out4.Runs) != 1 || out4.Runs[0].Result != "red" {
		t.Fatalf("proof 4 want red (whole suite failed on TestPass), got %+v", out4.Runs)
	}
	if !slices.Contains(readWholeRuns(t, wholeRunsFile), "go-test-example "+tip3) {
		t.Fatalf("proof 4 did not run whole suite on %s after all previously failing tests passed: %v", tip3, readWholeRuns(t, wholeRunsFile))
	}
	if green, gerr := heavy.Green(context.Background(), root); gerr != nil || green != "" {
		t.Fatalf("green moved on focused pass despite red whole suite: %q (%v), want empty", green, gerr)
	}
	wantPass := []string{"go-test:tools/example:example.com/suite:TestPass"}
	got, err = heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, "go-test-example")
	if err != nil || !slices.Equal(got, wantPass) {
		t.Fatalf("after proof 4 saved failing tests = %v (%v), want %v", got, err, wantPass)
	}

	// Tip 4: fix TestPass so everything passes -> focused TestPass passes -> whole suite runs and passes -> green!
	writeExampleGoSuite(t, root, `package suite
import "testing"
func TestPass(t *testing.T) {}
func TestFailA(t *testing.T) {}
func TestFailB(t *testing.T) {}
`)
	tip4 := commitAllHeavy(t, root, "all green")

	out5, err := newRunner().Run(context.Background())
	if err != nil {
		t.Fatalf("proof 5: %v", err)
	}
	if len(out5.Runs) != 1 || out5.Runs[0].Result != "green" {
		t.Fatalf("proof 5 want green, got %+v", out5.Runs)
	}
	if !slices.Contains(readWholeRuns(t, wholeRunsFile), "go-test-example "+tip4) {
		t.Fatalf("proof 5 did not run whole suite on %s before marking green: %v", tip4, readWholeRuns(t, wholeRunsFile))
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip4 {
		t.Fatalf("green = %s, want %s", green, tip4)
	}
	got, err = heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, "go-test-example")
	if err != nil || len(got) != 0 {
		t.Fatalf("after green proof saved failing tests = %v (%v), want empty", got, err)
	}
	_ = tip1
}

func TestHeavyProofFailingSetSurvivesLoopRestartAndCancelledProof(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	storePath := openHeavyTestStore(t, work)
	wholeRunsFile := filepath.Join(work, "whole-runs.log")
	gateBin := makeFakeHeavyGoGate(t, work, wholeRunsFile)

	writeGoSuiteCheckDecl(t, root)
	writeExampleGoSuite(t, root, `package suite
import "testing"
func TestPass(t *testing.T) {}
func TestFailA(t *testing.T) { t.Fatal("fail A") }
`)
	tip1 := commitAllHeavy(t, root, "break A")

	newRunner := func() HeavyRunner {
		steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: gateBin}
		return HeavyRunner{
			Root:  root,
			Work:  filepath.Join(work, ".rolling", "worktree"),
			Trunk: TrunkRef,
			Store: storePath,
			Lock:  loopHeld(t, root),
			Judge: steps.JudgeHeavy,
		}
	}

	// Proof 1: whole suite runs once, records TestFailA in the store.
	out1, err := newRunner().Run(context.Background())
	if err != nil || len(out1.Runs) != 1 || out1.Runs[0].Result != "red" {
		t.Fatalf("proof 1: out=%+v err=%v", out1, err)
	}
	if runs := readWholeRuns(t, wholeRunsFile); len(runs) != 1 {
		t.Fatalf("proof 1 whole runs = %v, want 1", runs)
	}

	// Simulated loop restart: fresh ProcessSteps + HeavyRunner against the same store.
	out2, err := newRunner().Run(context.Background())
	if err != nil || len(out2.Runs) != 1 || out2.Runs[0].Result != "red" {
		t.Fatalf("after restart proof 2: out=%+v err=%v", out2, err)
	}
	if runs := readWholeRuns(t, wholeRunsFile); len(runs) != 1 {
		t.Fatalf("after restart whole suite ran unexpectedly: %v", runs)
	}

	// Cancelled proof during focused test run: TestFailA blocks after touching a marker,
	// we cancel the context, and verify the store still holds TestFailA.
	focusedMarker := filepath.Join(work, "focused-started")
	writeExampleGoSuite(t, root, `package suite
import ("os"; "testing"; "time")
func TestPass(t *testing.T) {}
func TestFailA(t *testing.T) {
	_ = os.WriteFile(`+strconvQuote(focusedMarker)+`, []byte("1"), 0o644)
	time.Sleep(30 * time.Second)
}
`)
	tip2 := commitAllHeavy(t, root, "slow A")

	ctxCancelFocused, cancelFocused := context.WithCancel(context.Background())
	doneFocused := make(chan error, 1)
	go func() {
		_, err := newRunner().Run(ctxCancelFocused)
		doneFocused <- err
	}()
	for {
		if _, err := os.Stat(focusedMarker); err == nil {
			break
		}
		select {
		case err := <-doneFocused:
			t.Fatalf("focused proof ended before marker: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancelFocused()
	if err := <-doneFocused; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled focused proof err = %v, want context.Canceled", err)
	}
	wantA := []string{"go-test:tools/example:example.com/suite:TestFailA"}
	got, err := heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, "go-test-example")
	if err != nil || !slices.Equal(got, wantA) {
		t.Fatalf("after cancelled focused proof saved tests = %v (%v), want %v", got, err, wantA)
	}
	for _, line := range readWholeRuns(t, wholeRunsFile) {
		if strings.HasSuffix(line, " "+tip2) {
			t.Fatalf("whole suite ran on tip2 during cancelled focused proof: %v", readWholeRuns(t, wholeRunsFile))
		}
	}

	// Cancelled proof during whole-suite run (after focused test passed):
	// store must STILL retain TestFailA because the whole suite did not complete.
	writeExampleGoSuite(t, root, `package suite
import "testing"
func TestPass(t *testing.T) {}
func TestFailA(t *testing.T) {}
`)
	commitAllHeavy(t, root, "fix A for whole-suite cancel")
	wholeMarker := filepath.Join(work, "whole-started")
	t.Setenv("FAKE_GATE_HOLD_CHECK", "go-test-example")
	t.Setenv("FAKE_GATE_HOLD_MARKER", wholeMarker)

	ctxCancelWhole, cancelWhole := context.WithCancel(context.Background())
	doneWhole := make(chan error, 1)
	go func() {
		_, err := newRunner().Run(ctxCancelWhole)
		doneWhole <- err
	}()
	for {
		if _, err := os.Stat(wholeMarker); err == nil {
			break
		}
		select {
		case err := <-doneWhole:
			t.Fatalf("whole-suite proof ended before marker: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancelWhole()
	if err := <-doneWhole; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled whole-suite proof err = %v, want context.Canceled", err)
	}
	got, err = heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, "go-test-example")
	if err != nil || !slices.Equal(got, wantA) {
		t.Fatalf("after cancelled whole-suite proof saved tests = %v (%v), want %v", got, err, wantA)
	}
	_ = tip1
}

func TestHeavyProofUnnamedFailureStillRunsWhole(t *testing.T) {
	cases := []struct {
		name    string
		checkID string
		setup   func(t *testing.T, root string)
	}{
		{
			name:    "build-failure",
			checkID: "go-test-example",
			setup: func(t *testing.T, root string) {
				writeGoSuiteCheckDecl(t, root)
				writeExampleGoSuite(t, root, `package suite
syntax error here
`)
			},
		},
		{
			name:    "panic-outside-test",
			checkID: "go-test-example",
			setup: func(t *testing.T, root string) {
				writeGoSuiteCheckDecl(t, root)
				writeExampleGoSuite(t, root, `package suite
import "testing"
func TestMain(m *testing.M) { panic("boom outside test") }
func TestPass(t *testing.T) {}
`)
			},
		},
		{
			name:    "non-go-runner",
			checkID: "non-go-check",
			setup: func(t *testing.T, root string) {
				checksDir := filepath.Join(root, "tools", "gate", "checks")
				if err := os.MkdirAll(checksDir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(checksDir, "non-go-check.json"), `{"id":"non-go-check","label":"web tests pass","scope":"tree","tier":"heavy","timeout_ms":60000,"command":["sh","-c","npm test"],"reads":["web/"],"remember":false}`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := heavyRepo(t)
			work := scratchUnderDurable(t)
			storePath := openHeavyTestStore(t, work)
			wholeRunsFile := filepath.Join(work, "whole-runs.log")
			gateBin := makeFakeHeavyGoGate(t, work, wholeRunsFile)

			tc.setup(t, root)
			commitAllHeavy(t, root, tc.name)

			steps := &ProcessSteps{Root: root, LaneRoot: work, HeavyGateBin: gateBin}
			newRunner := func() HeavyRunner {
				return HeavyRunner{
					Root:  root,
					Work:  filepath.Join(work, ".rolling", "worktree"),
					Trunk: TrunkRef,
					Store: storePath,
					Lock:  loopHeld(t, root),
					Judge: steps.JudgeHeavy,
				}
			}

			out1, err := newRunner().Run(context.Background())
			if err != nil || len(out1.Runs) != 1 || out1.Runs[0].Result != "red" {
				t.Fatalf("proof 1: out=%+v err=%v", out1, err)
			}
			got, err := heavy.LoadFailingTests(context.Background(), storePath, TrunkRef, tc.checkID)
			if err != nil || len(got) != 0 {
				t.Fatalf("saved failing tests = %v (%v), want empty", got, err)
			}

			out2, err := newRunner().Run(context.Background())
			if err != nil || len(out2.Runs) != 1 || out2.Runs[0].Result != "red" {
				t.Fatalf("proof 2: out=%+v err=%v", out2, err)
			}
			if runs := readWholeRuns(t, wholeRunsFile); len(runs) != 2 {
				t.Fatalf("whole runs = %v, want 2 (unnamed failure must run whole)", runs)
			}
		})
	}
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
