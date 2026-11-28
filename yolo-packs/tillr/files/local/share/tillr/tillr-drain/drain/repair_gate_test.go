package drain

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// countingRepairSteps runs the real repair check and records every selector it ran.
type countingRepairSteps struct {
	*ProcessSteps
	ran []string
}

func (s *countingRepairSteps) repairCheck(ctx context.Context, c Candidate, id string) (string, string) {
	s.ran = append(s.ran, id)
	return s.ProcessSteps.repairCheck(ctx, c, id)
}

const (
	repairFlakyID   = "go-test:tools/tillr-drain:example.invalid/repair:TestFlaky"
	repairGoneID    = "go-test:tools/tillr-drain:example.invalid/repair:TestGone"
	repairSkippedID = "go-test:tools/tillr-drain:example.invalid/repair:TestVariants/skipped"
	repairOtherID   = "go-test:tools/tillr-drain:example.invalid/repair:TestOther"
)

func repairListJSON(selectors ...string) string {
	var entries []string
	for _, sel := range selectors {
		entries = append(entries, `{"selector":"`+sel+`","source":"trunk-proof"}`)
	}
	return `{"version":1,"disabled":[` + strings.Join(entries, ",") + "]}\n"
}

func repairTestSource(flaky string) string {
	return `package repair
import "testing"
func TestFlaky(t *testing.T) { if "` + flaky + `" != "repaired" { t.Fatal("still red") } }
func TestOther(t *testing.T) { t.Fatal("a test the repair did not re-enable must not run") }
func TestVariants(t *testing.T) {
	t.Run("skipped", func(t *testing.T) { t.Skip("fixture skipped") })
}
`
}

// TestRepairGateRunsEachReenabledTestOnce builds the one-run repair gate (D-TPF4): a
// candidate that removes disabled-list entries runs each removed selector once with
// `go test -count=1`, lands on one pass, and is refused when a re-enabled test fails,
// is absent or is skipped. Selectors still listed are never run.
func TestRepairGateRunsEachReenabledTestOnce(t *testing.T) {
	declareSiblingInputs(t)
	for _, tc := range []struct {
		name       string
		headList   string // "" deletes the list file
		flaky      string
		wantRan    []string
		accepted   bool
		wantDetail string
	}{
		{name: "one pass re-enables", headList: repairListJSON(repairGoneID, repairSkippedID, repairOtherID), flaky: "repaired", wantRan: []string{repairFlakyID}, accepted: true},
		{name: "one failure refuses", headList: repairListJSON(repairGoneID, repairSkippedID, repairOtherID), flaky: "broken", wantRan: []string{repairFlakyID}, wantDetail: "failed its one run: " + repairFlakyID},
		{name: "absent test refuses", headList: repairListJSON(repairFlakyID, repairSkippedID, repairOtherID), flaky: "broken", wantRan: []string{repairGoneID}, wantDetail: repairGoneID + " (absent)"},
		{name: "skipped test refuses", headList: repairListJSON(repairFlakyID, repairGoneID, repairOtherID), flaky: "broken", wantRan: []string{repairSkippedID}, wantDetail: repairSkippedID + " (skipped)"},
		{name: "no removal runs nothing", headList: repairListJSON(repairFlakyID, repairGoneID, repairSkippedID, repairOtherID), flaky: "broken", accepted: true},
		{name: "deleting the list re-enables every entry", flaky: "repaired", wantRan: []string{repairFlakyID, repairGoneID, repairSkippedID, repairOtherID}, wantDetail: repairOtherID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			f, l := passFixture(t, "item-a")
			write := func(path, text string) {
				path = filepath.Join(f.root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, path, text)
			}
			write("tools/tillr-drain/go.mod", "module example.invalid/repair\n\ngo 1.22\n")
			write("tools/tillr-drain/repair.go", "package repair\n")
			write("tools/tillr-drain/repair_test.go", repairTestSource("broken"))
			write("tillr-disabled-tests.json", repairListJSON(repairFlakyID, repairGoneID, repairSkippedID, repairOtherID))
			runGit(t, f.root, "add", ".")
			runGit(t, f.root, "commit", "-qm", "disable the failing tests")
			base := runGit(t, f.root, "rev-parse", "HEAD")
			lane := filepath.Join(scratchUnderDurable(t), "lane")
			runGit(t, f.root, "worktree", "add", "-q", "-b", "repair-lane", lane, base)
			writeForkFile(t, filepath.Join(lane, "tools/tillr-drain/repair_test.go"), repairTestSource(tc.flaky))
			if tc.headList == "" {
				runGit(t, lane, "rm", "-q", "tillr-disabled-tests.json")
			} else {
				writeForkFile(t, filepath.Join(lane, "tillr-disabled-tests.json"), tc.headList)
			}
			runGit(t, lane, "add", "-A")
			runGit(t, lane, "commit", "-qm", "fix: repair the flaky test", "--allow-empty")
			head := runGit(t, lane, "rev-parse", "HEAD")

			steps := &countingRepairSteps{ProcessSteps: &ProcessSteps{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot, Now: time.Now}}
			l.cfg.Steps = steps
			c := Candidate{ItemID: "item-a", RunID: "run-repair", BaseSHA: base, HeadSHA: head, Worktree: lane}
			c.reenabled, c.reenabledErr = l.reenabledSelectors(ctx, c)
			if c.reenabledErr != nil {
				t.Fatal(c.reenabledErr)
			}
			if c.reenablesTests() != (len(tc.wantRan) > 0) {
				t.Fatalf("reenabled=%v, want the gate to run %v", c.reenabled, tc.wantRan)
			}
			gate := GateOutcome{Outcome: GatePassed}
			var result LoopResult
			accepted, err := l.settleRepairGate(ctx, &c, &gate, &result)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(steps.ran, tc.wantRan) {
				t.Fatalf("ran %v, want each removed selector once: %v", steps.ran, tc.wantRan)
			}
			if accepted != tc.accepted {
				t.Fatalf("accepted=%v want %v; refusal: %q", accepted, tc.accepted, c.RefusalDetail)
			}
			if tc.accepted {
				if result.Rejected != 0 || gate.Outcome != GatePassed {
					t.Fatalf("an accepted repair was refused: %+v %q", result, gate.Outcome)
				}
				return
			}
			if result.RefusedSteps[RefusalGate] != 1 || gate.Outcome != GateFailed {
				t.Fatalf("no gate refusal: %+v %q", result, gate.Outcome)
			}
			if !strings.Contains(c.RefusalDetail, tc.wantDetail) {
				t.Fatalf("refusal %q does not name %q", c.RefusalDetail, tc.wantDetail)
			}
		})
	}
}

// TestRepairGateRefusesAnUnreadableList keeps a malformed list at the candidate's head
// from passing as "no entry removed".
func TestRepairGateRefusesAnUnreadableList(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	writeForkFile(t, filepath.Join(f.root, "tillr-disabled-tests.json"), repairListJSON(repairFlakyID))
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "disable a test")
	base := runGit(t, f.root, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(f.root, "tillr-disabled-tests.json"), "{not json\n")
	runGit(t, f.root, "commit", "-qam", "break the list")
	head := runGit(t, f.root, "rev-parse", "HEAD")
	c := Candidate{ItemID: "item-a", RunID: "run-repair", BaseSHA: base, HeadSHA: head}
	c.reenabled, c.reenabledErr = l.reenabledSelectors(ctx, c)
	if c.reenabledErr == nil || !c.reenablesTests() {
		t.Fatalf("a malformed head list read as %v", c.reenabled)
	}
	gate := GateOutcome{Outcome: GatePassed}
	var result LoopResult
	accepted, err := l.settleRepairGate(ctx, &c, &gate, &result)
	if err != nil || accepted || result.RefusedSteps[RefusalGate] != 1 {
		t.Fatalf("accepted=%v err=%v result=%+v", accepted, err, result)
	}
}

// answeringRepairSteps answers every repair check with one word.
type answeringRepairSteps struct {
	*gateBaseRecordingSteps
	answer string
	ran    []string
}

func (s *answeringRepairSteps) repairCheck(_ context.Context, _ Candidate, id string) (string, string) {
	s.ran = append(s.ran, id)
	return s.answer, "fixture answer"
}

// TestVerifyRunsTheRepairGateForAnOrdinaryCandidate closes the zero-run escape: an
// ordinary (non-fixer) candidate that removes a disabled-list entry does not land on
// the ordinary gate's pass alone; it lands only when the re-enabled test passes.
func TestVerifyRunsTheRepairGateForAnOrdinaryCandidate(t *testing.T) {
	for _, tc := range []struct {
		answer string
		lands  bool
	}{{repairPass, true}, {repairFail, false}} {
		t.Run(tc.answer, func(t *testing.T) {
			ctx := context.Background()
			f, l, recording, c, _, report := ordinaryCandidateFixture(t)
			writeForkFile(t, filepath.Join(f.root, "tillr-disabled-tests.json"), repairListJSON(repairFlakyID, repairOtherID))
			runGit(t, f.root, "add", "tillr-disabled-tests.json")
			runGit(t, f.root, "commit", "-qm", "chore(gate): disable tests that failed the trunk proof")
			base := runGit(t, f.root, "rev-parse", TrunkRef)
			runGit(t, c.Worktree, "reset", "-q", "--hard", base)
			for _, name := range []string{BaseSHAFile, ItemBaseSHAFile} {
				writeForkFile(t, filepath.Join(l.laneDirPath(c.LaneID), name), base+"\n")
			}
			writeForkFile(t, filepath.Join(c.Worktree, "tillr-disabled-tests.json"), repairListJSON(repairOtherID))
			runGit(t, c.Worktree, "commit", "-qam", "fix: repair the flaky test")
			c.BaseSHA, c.HeadSHA = base, runGit(t, c.Worktree, "rev-parse", "HEAD")
			steps := &answeringRepairSteps{gateBaseRecordingSteps: recording, answer: tc.answer}
			l.cfg.Steps = steps
			var result LoopResult
			_, landed, err := l.Verify(ctx, c, &result, nil)
			if err != nil {
				t.Fatalf("Verify: %v\n%s", err, report.String())
			}
			if !slices.Equal(steps.ran, []string{repairFlakyID}) {
				t.Fatalf("repair gate ran %v, want only the removed selector", steps.ran)
			}
			if landed != tc.lands || (!tc.lands && result.RefusedSteps[RefusalGate] != 1) {
				t.Fatalf("landed=%v want %v; result %+v\n%s", landed, tc.lands, result, report.String())
			}
		})
	}
}
